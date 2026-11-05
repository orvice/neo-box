package cloudflareacct

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

// ZoneRulesView is a live, independently readable view of the five Zone
// Ruleset phases. An issue in one phase does not hide the others.
type ZoneRulesView struct {
	Categories map[cloudflare.ZoneRuleCategory][]cloudflare.ZoneRuleset
	Issues     map[cloudflare.ZoneRuleCategory]string
}

var zoneRulePhases = []cloudflare.ZoneRuleCategory{
	cloudflare.ZoneRuleSingleRedirect, cloudflare.ZoneRuleURLRewrite,
	cloudflare.ZoneRuleRequestHeader, cloudflare.ZoneRuleResponseHeader,
	cloudflare.ZoneRuleOrigin,
}

func (a *Account) ZoneRules(ctx context.Context, zoneID string) (*ZoneRulesView, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	v := &ZoneRulesView{Categories: make(map[cloudflare.ZoneRuleCategory][]cloudflare.ZoneRuleset), Issues: make(map[cloudflare.ZoneRuleCategory]string)}
	var wg sync.WaitGroup
	for _, phase := range zoneRulePhases {
		phase := phase
		wg.Add(1)
		go func() {
			defer wg.Done()
			sets, e := a.api.ListZoneRulesets(ctx, zone.ID, phase)
			if e != nil {
				v.Issues[phase] = e.Error()
			} else {
				v.Categories[phase] = sets
			}
		}()
	}
	wg.Wait()
	return v, nil
}

func zoneRuleSet(sets []cloudflare.ZoneRuleset) (*cloudflare.ZoneRuleset, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	if len(sets) > 1 {
		return nil, fmt.Errorf("Cloudflare returned multiple rulesets for one phase")
	}
	return &sets[0], nil
}

func ruleLogState(r cloudflare.ZoneRule) map[string]any {
	// Action parameters can contain header values, so never put them in the
	// operation payload. The live API response remains available to the caller.
	return map[string]any{"rule_id": r.ID, "description": r.Description, "expression": r.Expression, "action": r.Action, "enabled": r.Enabled != nil && *r.Enabled}
}

func (a *Account) mutateZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, action string, target string, before, requested map[string]any, send func(context.Context) (map[string]any, error)) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if !phase.Valid() {
		return nil, fmt.Errorf("unsupported Zone Rules category %q", phase)
	}
	defer a.m.lockResource("rules/" + a.ID + "/" + zone.ID + "/" + string(phase))()
	return a.operate(ctx, actor, resourceChange{kind: cfrepo.KindZoneRules, resource: zone.Name, action: action, target: target, before: before, requested: requested, what: "change Zone Rules", permission: "Zone Rules Write", send: send})
}

func (a *Account) CreateZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, rule cloudflare.ZoneRule) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	sets, err := a.api.ListZoneRulesets(ctx, zone.ID, phase)
	if err != nil && !cloudflare.IsNotFound(err) {
		return nil, a.classify(ctx, err, "read Zone Rules", "Zone Rules Read")
	}
	rs, err := zoneRuleSet(sets)
	if err != nil {
		return nil, err
	}
	if rs != nil {
		for _, old := range rs.Rules {
			if old.ID == rule.ID && rule.ID != "" {
				return nil, fmt.Errorf("rule already exists")
			}
		}
	}
	state := ruleLogState(rule)
	return a.mutateZoneRule(ctx, actor, zoneID, phase, "create_rule", rule.ID, nil, state, func(wctx context.Context) (map[string]any, error) {
		if rs == nil {
			created, e := a.api.CreateZoneRuleset(wctx, zone.ID, cloudflare.ZoneRuleset{Phase: phase, Kind: "zone", Rules: []cloudflare.ZoneRule{rule}})
			if e != nil {
				return nil, e
			}
			if len(created.Rules) == 0 {
				return state, nil
			}
			return ruleLogState(created.Rules[len(created.Rules)-1]), nil
		}
		rs.Rules = append(rs.Rules, rule)
		_, e := a.api.ReplaceZoneRuleset(wctx, zone.ID, *rs)
		if e != nil {
			return nil, e
		}
		return state, nil
	})
}

func (a *Account) UpdateZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, ruleID string, rule cloudflare.ZoneRule) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	sets, err := a.api.ListZoneRulesets(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classify(ctx, err, "read Zone Rules", "Zone Rules Read")
	}
	rs, err := zoneRuleSet(sets)
	if err != nil || rs == nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("ruleset not found")
	}
	old := rs.Rule(ruleID)
	if old == nil {
		return nil, fmt.Errorf("rule not found")
	}
	if old.Action != rule.Action && strings.TrimSpace(rule.Action) == "" {
		return nil, fmt.Errorf("action is required")
	}
	return a.mutateZoneRule(ctx, actor, zoneID, phase, "update_rule", ruleID, ruleLogState(*old), ruleLogState(rule), func(wctx context.Context) (map[string]any, error) {
		got, e := a.api.UpdateZoneRule(wctx, zone.ID, rs.ID, rule)
		if e != nil {
			return nil, e
		}
		return ruleLogState(*got), nil
	})
}

func (a *Account) DeleteZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, ruleID string) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	sets, err := a.api.ListZoneRulesets(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classify(ctx, err, "read Zone Rules", "Zone Rules Read")
	}
	rs, err := zoneRuleSet(sets)
	if err != nil || rs == nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("ruleset not found")
	}
	old := rs.Rule(ruleID)
	if old == nil {
		return nil, fmt.Errorf("rule not found")
	}
	return a.mutateZoneRule(ctx, actor, zoneID, phase, "delete_rule", ruleID, ruleLogState(*old), nil, func(wctx context.Context) (map[string]any, error) {
		return nil, a.api.DeleteZoneRule(wctx, zone.ID, rs.ID, ruleID)
	})
}

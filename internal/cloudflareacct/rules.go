package cloudflareacct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

var (
	// ErrZoneRuleNotFound: no such rule (or ruleset) in this Zone category.
	ErrZoneRuleNotFound = errors.New("rule not found in this category")
	// ErrZoneRuleReadOnly: the rule uses unsupported configuration and cannot
	// be edited, toggled, deleted or moved (directly or indirectly).
	ErrZoneRuleReadOnly = errors.New("this rule is read-only and cannot be changed here")
	// ErrZoneRuleConflict: the live ruleset changed after the user loaded it.
	ErrZoneRuleConflict = errors.New("this category changed since it was loaded; refresh and review your changes before trying again")
)

// FeatureRestrictedError means Cloudflare refused the operation because the
// Zone's plan or account entitlements do not allow this rule/feature.
type FeatureRestrictedError struct {
	What string
	Err  error
}

func (e *FeatureRestrictedError) Error() string {
	return fmt.Sprintf("Cloudflare plan or entitlement restriction while trying to %s: %s", e.What, apiErrMessage(e.Err))
}
func (e *FeatureRestrictedError) Unwrap() error { return e.Err }

// QuotaExceededError means Cloudflare refused the operation because the Zone
// reached its rule quota or limit for this category.
type QuotaExceededError struct {
	What string
	Err  error
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("Cloudflare rule quota reached while trying to %s: %s", e.What, apiErrMessage(e.Err))
}
func (e *QuotaExceededError) Unwrap() error { return e.Err }

func apiErrMessage(err error) string {
	var ae *cloudflare.APIError
	if errors.As(err, &ae) {
		return ae.Message()
	}
	return err.Error()
}

// ZoneRulesIssueKind classifies a per-category read failure.
type ZoneRulesIssueKind string

const (
	ZoneRulesIssueNone       ZoneRulesIssueKind = ""
	ZoneRulesIssuePermission ZoneRulesIssueKind = "permission_denied"
	ZoneRulesIssueToken      ZoneRulesIssueKind = "token_rejected"
	ZoneRulesIssueFeature    ZoneRulesIssueKind = "feature_restricted"
	ZoneRulesIssueError      ZoneRulesIssueKind = "error"
)

// ZoneRuleCategoryView is the live state of one Zone Rules category.
type ZoneRuleCategoryView struct {
	Category       cloudflare.ZoneRuleCategory
	RulesetID      string
	Snapshot       string
	Rules          []cloudflare.ZoneRule
	RulesetMissing bool
	Issue          string
	IssueKind      ZoneRulesIssueKind
}

// ZoneRuleResult returns the confirmed rule (when applicable) alongside the
// operation log outcome.
type ZoneRuleResult struct {
	Rule            *cloudflare.ZoneRule
	OperationResult *OperationResult
}

func (a *Account) zoneRulesLock(zoneID string, phase cloudflare.ZoneRuleCategory) func() {
	return a.m.lockResource("rules/" + a.ID + "/" + zoneID + "/" + string(phase))
}

func (a *Account) classifyZoneRulesErr(ctx context.Context, err error, what, permission string) error {
	if err == nil {
		return nil
	}
	if cloudflare.IsAuthError(err) {
		return a.tokenRejected(ctx, err)
	}
	var ae *cloudflare.APIError
	if errors.As(err, &ae) {
		if isQuotaError(ae) {
			return &QuotaExceededError{What: what, Err: err}
		}
		if isFeatureRestrictionError(ae) {
			return &FeatureRestrictedError{What: what, Err: err}
		}
	}
	return a.classify(ctx, err, what, permission)
}

func isQuotaError(ae *cloudflare.APIError) bool {
	for _, d := range ae.Errors {
		switch d.Code {
		case 20217, 20218, 20219:
			return true
		}
		msg := strings.ToLower(d.Message)
		if strings.Contains(msg, "maximum number of") ||
			strings.Contains(msg, "exceeded the maximum") ||
			strings.Contains(msg, "quota") ||
			strings.Contains(msg, "too many rules") ||
			strings.Contains(msg, "rule limit") {
			return true
		}
	}
	return false
}

func isFeatureRestrictionError(ae *cloudflare.APIError) bool {
	for _, d := range ae.Errors {
		switch d.Code {
		case 20201, 20213, 20214, 10014, 10042:
			return true
		}
		msg := strings.ToLower(d.Message)
		if strings.Contains(msg, "not entitled") ||
			strings.Contains(msg, "entitlement") ||
			strings.Contains(msg, "upgrade your plan") ||
			strings.Contains(msg, "current plan") ||
			strings.Contains(msg, "plan does not") ||
			strings.Contains(msg, "feature is not available") ||
			strings.Contains(msg, "subscription") {
			return true
		}
	}
	return false
}

func issueKindOf(err error) ZoneRulesIssueKind {
	var (
		perm     *PermissionError
		rejected *TokenRejectedError
		feature  *FeatureRestrictedError
	)
	switch {
	case errors.As(err, &rejected):
		return ZoneRulesIssueToken
	case errors.As(err, &perm):
		return ZoneRulesIssuePermission
	case errors.As(err, &feature):
		return ZoneRulesIssueFeature
	default:
		return ZoneRulesIssueError
	}
}

// ZoneRules reads the live Zone Rulesets for one category (when only is set)
// or all five categories independently. A failure in one category never hides
// the others or turns into an empty list.
func (a *Account) ZoneRules(ctx context.Context, zoneID string, only cloudflare.ZoneRuleCategory) ([]ZoneRuleCategoryView, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	var phases []cloudflare.ZoneRuleCategory
	if only != "" {
		if !only.Valid() {
			return nil, &cloudflare.FieldError{Field: "category", Message: "must be a supported Zone Rules category"}
		}
		phases = []cloudflare.ZoneRuleCategory{only}
	} else {
		phases = cloudflare.AllZoneRuleCategories()
	}

	views := make([]ZoneRuleCategoryView, len(phases))
	var wg sync.WaitGroup
	for i, phase := range phases {
		i, phase := i, phase
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs, e := a.api.GetZonePhaseRuleset(ctx, zone.ID, phase)
			if e != nil {
				classified := a.classifyZoneRulesErr(ctx, e, "read "+phase.Label(), phase.ReadPermission())
				views[i] = ZoneRuleCategoryView{
					Category:  phase,
					Issue:     classified.Error(),
					IssueKind: issueKindOf(classified),
				}
				return
			}
			if rs == nil {
				views[i] = ZoneRuleCategoryView{
					Category:       phase,
					Snapshot:       cloudflare.AbsentRulesetSnapshot,
					RulesetMissing: true,
				}
				return
			}
			views[i] = ZoneRuleCategoryView{
				Category:  phase,
				RulesetID: rs.ID,
				Snapshot:  rs.Snapshot(),
				Rules:     rs.Rules,
			}
		}()
	}
	wg.Wait()
	return views, nil
}

// RuleReadOnly reports whether r uses configuration Neo Box does not safely
// edit in category.
func RuleReadOnly(category cloudflare.ZoneRuleCategory, r cloudflare.ZoneRule) (bool, string) {
	_, reason := cloudflare.ParseZoneRuleConfig(category, r)
	return reason != "", reason
}

func ruleTarget(phase cloudflare.ZoneRuleCategory, desc, ruleID string) string {
	label := strings.TrimSpace(desc)
	if label == "" {
		label = ruleID
	}
	if label == "" {
		return phase.Label()
	}
	return phase.Label() + " · " + label
}

// ruleLogState produces a redacted state map for the operation log. Header
// values and expressions are never recorded so sensitive headers cannot leak.
func ruleLogState(zoneName string, phase cloudflare.ZoneRuleCategory, r cloudflare.ZoneRule) map[string]any {
	m := map[string]any{
		"zone":       zoneName,
		"category":   phase.Label(),
		"expression": r.Expression,
		"enabled":    r.IsEnabled(),
	}
	if r.ID != "" {
		m["rule_id"] = r.ID
	}
	if r.Description != "" {
		m["description"] = r.Description
	}
	cfg, _ := cloudflare.ParseZoneRuleConfig(phase, r)
	if cfg == nil {
		m["action"] = r.Action
		return m
	}
	switch {
	case cfg.SingleRedirect != nil:
		sr := cfg.SingleRedirect
		m["status_code"] = sr.StatusCode
		m["target_kind"] = sr.TargetKind
		m["target"] = sr.TargetValue
		m["preserve_query_string"] = sr.PreserveQueryString
	case cfg.URLRewrite != nil:
		ur := cfg.URLRewrite
		m["path_mode"] = ur.PathMode
		if ur.PathMode != cloudflare.RewritePartKeep {
			m["path"] = ur.PathValue
		}
		m["query_mode"] = ur.QueryMode
		if ur.QueryMode != cloudflare.RewritePartKeep {
			m["query"] = ur.QueryValue
		}
	case cfg.RequestHeader != nil:
		m["headers"] = redactedHeaderSummary(cfg.RequestHeader.Headers)
	case cfg.ResponseHeader != nil:
		m["headers"] = redactedHeaderSummary(cfg.ResponseHeader.Headers)
	case cfg.Origin != nil:
		o := cfg.Origin
		if o.DestinationPort != nil {
			m["destination_port"] = *o.DestinationPort
		}
		if o.HostHeader != nil {
			m["host_header"] = *o.HostHeader
		}
		if o.SNI != nil {
			m["sni"] = *o.SNI
		}
		if o.DNSRecord != nil {
			m["dns_record"] = *o.DNSRecord
		}
	}
	return m
}

func redactedHeaderSummary(mods []cloudflare.HeaderModification) []string {
	out := make([]string, 0, len(mods))
	for _, h := range mods {
		switch h.Operation {
		case cloudflare.HeaderOpRemove:
			out = append(out, h.Name+" (remove)")
		case cloudflare.HeaderOpSetStatic:
			out = append(out, h.Name+" (set static, value redacted)")
		case cloudflare.HeaderOpSetExpression:
			out = append(out, h.Name+" (set expression, value redacted)")
		case cloudflare.HeaderOpAddStatic:
			out = append(out, h.Name+" (add static, value redacted)")
		case cloudflare.HeaderOpAddExpression:
			out = append(out, h.Name+" (add expression, value redacted)")
		default:
			out = append(out, h.Name)
		}
	}
	return out
}

func currentSnapshot(rs *cloudflare.ZoneRuleset) string {
	if rs == nil {
		return cloudflare.AbsentRulesetSnapshot
	}
	return rs.Snapshot()
}

func (a *Account) runZoneRuleChange(ctx context.Context, actor Actor, c resourceChange) (*OperationResult, error) {
	res, err := a.operate(ctx, actor, c)
	if err != nil {
		// Reclassify Cloudflare quota / plan entitlement errors surfaced by operate.
		var ae *cloudflare.APIError
		if errors.As(err, &ae) {
			if isQuotaError(ae) {
				return nil, &QuotaExceededError{What: c.what, Err: ae}
			}
			if isFeatureRestrictionError(ae) {
				return nil, &FeatureRestrictedError{What: c.what, Err: ae}
			}
		}
		return nil, err
	}
	return res, nil
}

// CreateZoneRule appends a new rule to phase (or initializes the phase
// entrypoint ruleset with this rule when none exists yet). If snapshot is
// supplied, it must match the live state under lock.
func (a *Account) CreateZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, snapshot string, in cloudflare.ZoneRuleInput) (*ZoneRuleResult, error) {
	built, err := cloudflare.BuildZoneRule(phase, in, nil)
	if err != nil {
		return nil, err
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.zoneRulesLock(zone.ID, phase)()

	rs, err := a.api.GetZonePhaseRuleset(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classifyZoneRulesErr(ctx, err, "read "+phase.Label(), phase.ReadPermission())
	}
	if snapshot != "" && snapshot != currentSnapshot(rs) {
		return nil, ErrZoneRuleConflict
	}

	var createdRule *cloudflare.ZoneRule
	reqState := ruleLogState(zone.Name, phase, built)
	if rs == nil {
		reqState["initializes_ruleset"] = true
	}
	res, err := a.runZoneRuleChange(ctx, actor, resourceChange{
		kind:       cfrepo.KindZoneRules,
		resource:   zone.Name,
		action:     "create_http_rule",
		target:     ruleTarget(phase, built.Description, ""),
		requested:  reqState,
		what:       "create a rule in " + phase.Label(),
		permission: phase.WritePermission(),
		send: func(wctx context.Context) (map[string]any, error) {
			var (
				updated *cloudflare.ZoneRuleset
				sendErr error
			)
			if rs == nil {
				updated, sendErr = a.api.CreateZoneRuleset(wctx, zone.ID, cloudflare.ZoneRuleset{
					Name:  "default",
					Kind:  "zone",
					Phase: phase,
					Rules: []cloudflare.ZoneRule{built},
				})
			} else {
				updated, sendErr = a.api.AddZoneRulesetRule(wctx, zone.ID, rs.ID, built)
			}
			if sendErr != nil {
				return nil, sendErr
			}
			if updated != nil && len(updated.Rules) > 0 {
				r := updated.Rules[len(updated.Rules)-1]
				createdRule = &r
				return ruleLogState(zone.Name, phase, r), nil
			}
			return ruleLogState(zone.Name, phase, built), nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &ZoneRuleResult{Rule: createdRule, OperationResult: res}, nil
}

// UpdateZoneRule edits or enables/disables an existing supported rule after
// checking the live snapshot under the per-category lock.
func (a *Account) UpdateZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, ruleID, snapshot string, in cloudflare.ZoneRuleInput) (*ZoneRuleResult, error) {
	if !phase.Valid() {
		return nil, &cloudflare.FieldError{Field: "category", Message: "must be a supported Zone Rules category"}
	}
	if strings.TrimSpace(ruleID) == "" {
		return nil, &cloudflare.FieldError{Field: "rule_id", Message: "is required"}
	}
	if strings.TrimSpace(snapshot) == "" {
		return nil, &cloudflare.FieldError{Field: "snapshot", Message: "is required"}
	}
	if err := cloudflare.ValidateZoneRuleInput(phase, in); err != nil {
		return nil, err
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.zoneRulesLock(zone.ID, phase)()

	rs, err := a.api.GetZonePhaseRuleset(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classifyZoneRulesErr(ctx, err, "read "+phase.Label(), phase.ReadPermission())
	}
	if snapshot != currentSnapshot(rs) {
		return nil, ErrZoneRuleConflict
	}
	if rs == nil {
		return nil, ErrZoneRuleNotFound
	}
	old := rs.Rule(ruleID)
	if old == nil {
		return nil, ErrZoneRuleNotFound
	}
	if ro, reason := RuleReadOnly(phase, *old); ro {
		return nil, fmt.Errorf("%w: %s", ErrZoneRuleReadOnly, reason)
	}
	next, err := cloudflare.BuildZoneRule(phase, in, old)
	if err != nil {
		return nil, err
	}
	if cloudflare.ZoneRulesEqual(phase, *old, next) {
		return &ZoneRuleResult{Rule: old, OperationResult: noChange()}, nil
	}

	action := "update_http_rule"
	toggled := *old
	en := next.IsEnabled()
	toggled.Enabled = &en
	if cloudflare.ZoneRulesEqual(phase, toggled, next) {
		if en {
			action = "enable_http_rule"
		} else {
			action = "disable_http_rule"
		}
	}

	var updatedRule *cloudflare.ZoneRule
	res, err := a.runZoneRuleChange(ctx, actor, resourceChange{
		kind:       cfrepo.KindZoneRules,
		resource:   zone.Name,
		action:     action,
		target:     ruleTarget(phase, old.Description, old.ID),
		before:     ruleLogState(zone.Name, phase, *old),
		requested:  ruleLogState(zone.Name, phase, next),
		what:       "update a rule in " + phase.Label(),
		permission: phase.WritePermission(),
		send: func(wctx context.Context) (map[string]any, error) {
			updatedRS, sendErr := a.api.UpdateZoneRulesetRule(wctx, zone.ID, rs.ID, old.ID, next)
			if sendErr != nil {
				return nil, sendErr
			}
			if updatedRS != nil {
				if found := updatedRS.Rule(old.ID); found != nil {
					r := *found
					updatedRule = &r
					return ruleLogState(zone.Name, phase, r), nil
				}
			}
			r := next
			updatedRule = &r
			return ruleLogState(zone.Name, phase, next), nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &ZoneRuleResult{Rule: updatedRule, OperationResult: res}, nil
}

// DeleteZoneRule deletes a supported rule after verifying snapshot (when
// supplied) and read-only status under the per-category lock.
func (a *Account) DeleteZoneRule(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, ruleID, snapshot string) (*OperationResult, error) {
	if !phase.Valid() {
		return nil, &cloudflare.FieldError{Field: "category", Message: "must be a supported Zone Rules category"}
	}
	if strings.TrimSpace(ruleID) == "" {
		return nil, &cloudflare.FieldError{Field: "rule_id", Message: "is required"}
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.zoneRulesLock(zone.ID, phase)()

	rs, err := a.api.GetZonePhaseRuleset(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classifyZoneRulesErr(ctx, err, "read "+phase.Label(), phase.ReadPermission())
	}
	if snapshot != "" && snapshot != currentSnapshot(rs) {
		return nil, ErrZoneRuleConflict
	}
	if rs == nil {
		return nil, ErrZoneRuleNotFound
	}
	old := rs.Rule(ruleID)
	if old == nil {
		return nil, ErrZoneRuleNotFound
	}
	if ro, reason := RuleReadOnly(phase, *old); ro {
		return nil, fmt.Errorf("%w: %s", ErrZoneRuleReadOnly, reason)
	}
	return a.runZoneRuleChange(ctx, actor, resourceChange{
		kind:       cfrepo.KindZoneRules,
		resource:   zone.Name,
		action:     "delete_http_rule",
		target:     ruleTarget(phase, old.Description, old.ID),
		before:     ruleLogState(zone.Name, phase, *old),
		what:       "delete a rule in " + phase.Label(),
		permission: phase.WritePermission(),
		send: func(wctx context.Context) (map[string]any, error) {
			return nil, a.api.DeleteZoneRule(wctx, zone.ID, rs.ID, old.ID)
		},
	})
}

// ReorderZoneRules updates the order of rules within phase after checking the
// live snapshot and ensuring no read-only rule moves or is crossed.
func (a *Account) ReorderZoneRules(ctx context.Context, actor Actor, zoneID string, phase cloudflare.ZoneRuleCategory, snapshot string, ids []string) (*OperationResult, error) {
	if !phase.Valid() {
		return nil, &cloudflare.FieldError{Field: "category", Message: "must be a supported Zone Rules category"}
	}
	if strings.TrimSpace(snapshot) == "" {
		return nil, &cloudflare.FieldError{Field: "snapshot", Message: "is required"}
	}
	if len(ids) == 0 {
		return nil, &cloudflare.FieldError{Field: "rule_ids", Message: "must list all rules in the category"}
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.zoneRulesLock(zone.ID, phase)()

	rs, err := a.api.GetZonePhaseRuleset(ctx, zone.ID, phase)
	if err != nil {
		return nil, a.classifyZoneRulesErr(ctx, err, "read "+phase.Label(), phase.ReadPermission())
	}
	if snapshot != currentSnapshot(rs) {
		return nil, ErrZoneRuleConflict
	}
	if rs == nil {
		return nil, ErrZoneRuleNotFound
	}
	if len(ids) != len(rs.Rules) {
		return nil, &cloudflare.FieldError{Field: "rule_ids", Message: "must include every rule in the category"}
	}

	byID := make(map[string]cloudflare.ZoneRule, len(rs.Rules))
	for _, r := range rs.Rules {
		if r.ID == "" {
			return nil, fmt.Errorf("%w: category contains an unidentified rule", ErrZoneRuleReadOnly)
		}
		byID[r.ID] = r
	}
	seen := make(map[string]bool, len(ids))
	ordered := make([]cloudflare.ZoneRule, 0, len(ids))
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			return nil, &cloudflare.FieldError{Field: "rule_ids", Message: fmt.Sprintf("rule %q is not in this category", id)}
		}
		if seen[id] {
			return nil, &cloudflare.FieldError{Field: "rule_ids", Message: fmt.Sprintf("duplicate rule %q", id)}
		}
		seen[id] = true
		ordered = append(ordered, r)
	}

	curIDs := ruleIDs(rs.Rules)
	if slicesEqual(curIDs, ids) {
		return noChange(), nil
	}

	if err := validateReorderAgainstReadOnly(phase, rs.Rules, ids); err != nil {
		return nil, err
	}

	before := map[string]any{"zone": zone.Name, "category": phase.Label(), "rule_ids": curIDs}
	requested := map[string]any{"zone": zone.Name, "category": phase.Label(), "rule_ids": ids}
	return a.runZoneRuleChange(ctx, actor, resourceChange{
		kind:       cfrepo.KindZoneRules,
		resource:   zone.Name,
		action:     "reorder_http_rules",
		target:     phase.Label(),
		before:     before,
		requested:  requested,
		what:       "reorder rules in " + phase.Label(),
		permission: phase.WritePermission(),
		send: func(wctx context.Context) (map[string]any, error) {
			out, sendErr := a.api.ReorderZoneRules(wctx, zone.ID, *rs, ordered)
			if sendErr != nil {
				return nil, sendErr
			}
			return map[string]any{"zone": zone.Name, "category": phase.Label(), "rule_ids": ruleIDs(out.Rules)}, nil
		},
	})
}

// validateReorderAgainstReadOnly ensures that every read-only rule remains at
// its exact index and that no supported rule moves across a read-only rule
// (each segment between read-only rules must keep the exact same set of IDs).
func validateReorderAgainstReadOnly(phase cloudflare.ZoneRuleCategory, current []cloudflare.ZoneRule, nextIDs []string) error {
	roIndices := make([]int, 0)
	for i, r := range current {
		if ro, _ := RuleReadOnly(phase, r); ro {
			if nextIDs[i] != r.ID {
				return fmt.Errorf("%w: read-only rule %q cannot be moved or displaced", ErrZoneRuleReadOnly, r.ID)
			}
			roIndices = append(roIndices, i)
		}
	}
	if len(roIndices) == 0 {
		return nil
	}
	// Verify every intervening segment keeps the exact same set of rule IDs.
	start := 0
	for _, idx := range append(roIndices, len(current)) {
		if idx > start {
			want := make(map[string]bool, idx-start)
			for i := start; i < idx; i++ {
				want[current[i].ID] = true
			}
			for i := start; i < idx; i++ {
				if !want[nextIDs[i]] {
					return fmt.Errorf("%w: rules cannot be moved across a read-only rule", ErrZoneRuleReadOnly)
				}
			}
		}
		start = idx + 1
	}
	return nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ruleIDs(rules []cloudflare.ZoneRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.ID)
	}
	return out
}

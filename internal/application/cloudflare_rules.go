package application

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

func protoRuleCategory(c neoboxv1.CloudflareZoneRuleCategory) (cloudflare.ZoneRuleCategory, error) {
	m := map[neoboxv1.CloudflareZoneRuleCategory]cloudflare.ZoneRuleCategory{
		neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_SINGLE_REDIRECT: cloudflare.ZoneRuleSingleRedirect,
		neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_URL_REWRITE:     cloudflare.ZoneRuleURLRewrite,
		neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_REQUEST_HEADER:  cloudflare.ZoneRuleRequestHeader,
		neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_RESPONSE_HEADER: cloudflare.ZoneRuleResponseHeader,
		neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_ORIGIN:          cloudflare.ZoneRuleOrigin,
	}
	v, ok := m[c]
	if !ok {
		return "", fmt.Errorf("unsupported Zone Rules category")
	}
	return v, nil
}
func ruleCategoryProto(c cloudflare.ZoneRuleCategory) neoboxv1.CloudflareZoneRuleCategory {
	for k, v := range map[neoboxv1.CloudflareZoneRuleCategory]cloudflare.ZoneRuleCategory{neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_SINGLE_REDIRECT: cloudflare.ZoneRuleSingleRedirect, neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_URL_REWRITE: cloudflare.ZoneRuleURLRewrite, neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_REQUEST_HEADER: cloudflare.ZoneRuleRequestHeader, neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_RESPONSE_HEADER: cloudflare.ZoneRuleResponseHeader, neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_ORIGIN: cloudflare.ZoneRuleOrigin} {
		if v == c {
			return k
		}
	}
	return 0
}
func ruleToProto(r cloudflare.ZoneRule) *neoboxv1.CloudflareZoneRule {
	out := &neoboxv1.CloudflareZoneRule{Id: r.ID, Version: r.Version, Description: r.Description, Expression: r.Expression, Action: r.Action, Enabled: r.Enabled}
	if ro, reason := cloudflareacct.RuleReadOnly(r); ro {
		out.ReadOnly = true
		out.ReadOnlyReason = reason
	}
	if len(r.ActionParameters) > 0 {
		var m map[string]any
		if json.Unmarshal(r.ActionParameters, &m) == nil {
			out.ActionParameters, _ = structpb.NewStruct(m)
		}
	}
	return out
}
func resultProto(r *cloudflareacct.OperationResult) *neoboxv1.CloudflareChangeResult {
	out := &neoboxv1.CloudflareChangeResult{Changed: true}
	if r != nil {
		out.OperationLogError = logErrResult(r)
	}
	return out
}
func logErrResult(r *cloudflareacct.OperationResult) string {
	if r != nil && r.LogErr != nil {
		return r.LogErr.Error()
	}
	return ""
}
func (s *CloudflareServiceServer) GetCloudflareZoneRules(ctx context.Context, req *neoboxv1.GetCloudflareZoneRulesRequest) (*neoboxv1.GetCloudflareZoneRulesResponse, error) {
	_, a, e := s.openAccount(ctx, req.GetConnectionId())
	if e != nil {
		return nil, e
	}
	v, e := a.ZoneRules(ctx, req.GetZoneId())
	if e != nil {
		return nil, mapCloudflareErr(ctx, e)
	}
	out := &neoboxv1.GetCloudflareZoneRulesResponse{}
	for _, p := range []cloudflare.ZoneRuleCategory{cloudflare.ZoneRuleSingleRedirect, cloudflare.ZoneRuleURLRewrite, cloudflare.ZoneRuleRequestHeader, cloudflare.ZoneRuleResponseHeader, cloudflare.ZoneRuleOrigin} {
		x := &neoboxv1.CloudflareZoneRules{Category: ruleCategoryProto(p), Issue: v.Issues[p]}
		for _, rs := range v.Categories[p] {
			x.RulesetId = rs.ID
			x.Snapshot = rs.Snapshot()
			for _, r := range rs.Rules {
				x.Rules = append(x.Rules, ruleToProto(r))
			}
		}
		out.Categories = append(out.Categories, x)
	}
	return out, nil
}
func inputRule(x *neoboxv1.CloudflareZoneRule) cloudflare.ZoneRule {
	r := cloudflare.ZoneRule{ID: x.GetId(), Version: x.GetVersion(), Description: x.GetDescription(), Expression: x.GetExpression(), Action: x.GetAction(), Enabled: x.Enabled}
	if x.GetActionParameters() != nil {
		r.ActionParameters, _ = json.Marshal(x.GetActionParameters().AsMap())
	}
	return r
}
func (s *CloudflareServiceServer) CreateCloudflareZoneRule(ctx context.Context, req *neoboxv1.CreateCloudflareZoneRuleRequest) (*neoboxv1.CreateCloudflareZoneRuleResponse, error) {
	d, a, e := s.openAccount(ctx, req.GetConnectionId())
	if e != nil {
		return nil, e
	}
	p, e := protoRuleCategory(req.GetCategory())
	if e != nil {
		return nil, connectx.InvalidArgument(ctx, "category", e.Error())
	}
	r, e := a.CreateZoneRule(ctx, d.user, req.GetZoneId(), p, inputRule(req.GetRule()))
	if e != nil {
		return nil, mapCloudflareErr(ctx, e)
	}
	return &neoboxv1.CreateCloudflareZoneRuleResponse{Result: resultProto(r)}, nil
}
func (s *CloudflareServiceServer) UpdateCloudflareZoneRule(ctx context.Context, req *neoboxv1.UpdateCloudflareZoneRuleRequest) (*neoboxv1.UpdateCloudflareZoneRuleResponse, error) {
	d, a, e := s.openAccount(ctx, req.GetConnectionId())
	if e != nil {
		return nil, e
	}
	p, e := protoRuleCategory(req.GetCategory())
	if e != nil {
		return nil, e
	}
	r, e := a.UpdateZoneRule(ctx, d.user, req.GetZoneId(), p, req.GetRuleId(), req.GetSnapshot(), inputRule(req.GetRule()))
	if e != nil {
		return nil, mapCloudflareErr(ctx, e)
	}
	return &neoboxv1.UpdateCloudflareZoneRuleResponse{Result: resultProto(r)}, nil
}
func (s *CloudflareServiceServer) DeleteCloudflareZoneRule(ctx context.Context, req *neoboxv1.DeleteCloudflareZoneRuleRequest) (*neoboxv1.DeleteCloudflareZoneRuleResponse, error) {
	d, a, e := s.openAccount(ctx, req.GetConnectionId())
	if e != nil {
		return nil, e
	}
	p, e := protoRuleCategory(req.GetCategory())
	if e != nil {
		return nil, e
	}
	r, e := a.DeleteZoneRule(ctx, d.user, req.GetZoneId(), p, req.GetRuleId())
	if e != nil {
		return nil, mapCloudflareErr(ctx, e)
	}
	return &neoboxv1.DeleteCloudflareZoneRuleResponse{Result: resultProto(r)}, nil
}

func (s *CloudflareServiceServer) ReorderCloudflareZoneRules(ctx context.Context, req *neoboxv1.ReorderCloudflareZoneRulesRequest) (*neoboxv1.ReorderCloudflareZoneRulesResponse, error) {
	d, a, e := s.openAccount(ctx, req.GetConnectionId())
	if e != nil {
		return nil, e
	}
	p, e := protoRuleCategory(req.GetCategory())
	if e != nil {
		return nil, e
	}
	r, e := a.ReorderZoneRules(ctx, d.user, req.GetZoneId(), p, req.GetSnapshot(), req.GetRuleIds())
	if e != nil {
		return nil, mapCloudflareErr(ctx, e)
	}
	return &neoboxv1.ReorderCloudflareZoneRulesResponse{Result: resultProto(r)}, nil
}

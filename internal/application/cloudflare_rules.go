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
	switch c {
	case neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_SINGLE_REDIRECT:
		return cloudflare.ZoneRuleSingleRedirect, nil
	case neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_URL_REWRITE:
		return cloudflare.ZoneRuleURLRewrite, nil
	case neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_REQUEST_HEADER:
		return cloudflare.ZoneRuleRequestHeader, nil
	case neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_RESPONSE_HEADER:
		return cloudflare.ZoneRuleResponseHeader, nil
	case neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_ORIGIN:
		return cloudflare.ZoneRuleOrigin, nil
	}
	return "", fmt.Errorf("unsupported Zone Rules category")
}

func ruleCategoryProto(c cloudflare.ZoneRuleCategory) neoboxv1.CloudflareZoneRuleCategory {
	switch c {
	case cloudflare.ZoneRuleSingleRedirect:
		return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_SINGLE_REDIRECT
	case cloudflare.ZoneRuleURLRewrite:
		return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_URL_REWRITE
	case cloudflare.ZoneRuleRequestHeader:
		return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_REQUEST_HEADER
	case cloudflare.ZoneRuleResponseHeader:
		return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_RESPONSE_HEADER
	case cloudflare.ZoneRuleOrigin:
		return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_ORIGIN
	}
	return neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_UNSPECIFIED
}

func issueKindToProto(k cloudflareacct.ZoneRulesIssueKind) neoboxv1.CloudflareZoneRulesErrorKind {
	switch k {
	case cloudflareacct.ZoneRulesIssuePermission:
		return neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_PERMISSION_DENIED
	case cloudflareacct.ZoneRulesIssueToken:
		return neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_TOKEN_REJECTED
	case cloudflareacct.ZoneRulesIssueFeature:
		return neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_FEATURE_RESTRICTED
	case cloudflareacct.ZoneRulesIssueError:
		return neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_ERROR
	}
	return neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_UNSPECIFIED
}

func zoneRuleToProto(category cloudflare.ZoneRuleCategory, r cloudflare.ZoneRule) *neoboxv1.CloudflareZoneRule {
	enabled := r.IsEnabled()
	out := &neoboxv1.CloudflareZoneRule{
		Id:          r.ID,
		Version:     r.Version,
		Description: r.Description,
		Expression:  r.Expression,
		Action:      r.Action,
		Enabled:     &enabled,
		LastUpdated: r.LastUpdated,
	}
	if len(r.ActionParameters) > 0 {
		var m map[string]any
		if json.Unmarshal(r.ActionParameters, &m) == nil {
			out.ActionParameters, _ = structpb.NewStruct(m)
		}
	}
	cfg, reason := cloudflare.ParseZoneRuleConfig(category, r)
	if reason != "" {
		out.ReadOnly = true
		out.ReadOnlyReason = reason
		out.RawConfigJson = r.RawConfigJSON()
		return out
	}
	switch {
	case cfg.SingleRedirect != nil:
		sr := cfg.SingleRedirect
		tk := neoboxv1.CloudflareRedirectTargetKind_CLOUDFLARE_REDIRECT_TARGET_KIND_STATIC
		if sr.TargetKind == cloudflare.RedirectTargetExpression {
			tk = neoboxv1.CloudflareRedirectTargetKind_CLOUDFLARE_REDIRECT_TARGET_KIND_EXPRESSION
		}
		out.Config = &neoboxv1.CloudflareZoneRule_SingleRedirect{
			SingleRedirect: &neoboxv1.CloudflareSingleRedirectConfig{
				TargetKind:          tk,
				TargetValue:         sr.TargetValue,
				StatusCode:          int32(sr.StatusCode),
				PreserveQueryString: sr.PreserveQueryString,
			},
		}
	case cfg.URLRewrite != nil:
		ur := cfg.URLRewrite
		out.Config = &neoboxv1.CloudflareZoneRule_UrlRewrite{
			UrlRewrite: &neoboxv1.CloudflareURLRewriteConfig{
				PathMode:   rewriteModeToProto(ur.PathMode),
				PathValue:  ur.PathValue,
				QueryMode:  rewriteModeToProto(ur.QueryMode),
				QueryValue: ur.QueryValue,
			},
		}
	case cfg.RequestHeader != nil:
		out.Config = &neoboxv1.CloudflareZoneRule_RequestHeader{
			RequestHeader: headersToProto(cfg.RequestHeader),
		}
	case cfg.ResponseHeader != nil:
		out.Config = &neoboxv1.CloudflareZoneRule_ResponseHeader{
			ResponseHeader: headersToProto(cfg.ResponseHeader),
		}
	case cfg.Origin != nil:
		o := cfg.Origin
		po := &neoboxv1.CloudflareOriginRuleConfig{}
		if o.DestinationPort != nil {
			p := int32(*o.DestinationPort)
			po.DestinationPort = &p
		}
		if o.HostHeader != nil {
			v := *o.HostHeader
			po.HostHeader = &v
		}
		if o.SNI != nil {
			v := *o.SNI
			po.Sni = &v
		}
		if o.DNSRecord != nil {
			v := *o.DNSRecord
			po.DnsRecord = &v
		}
		out.Config = &neoboxv1.CloudflareZoneRule_Origin{Origin: po}
	}
	return out
}

func rewriteModeToProto(m string) neoboxv1.CloudflareRewritePartMode {
	switch m {
	case cloudflare.RewritePartStatic:
		return neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_STATIC
	case cloudflare.RewritePartExpression:
		return neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_EXPRESSION
	default:
		return neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_KEEP
	}
}

func rewriteModeFromProto(m neoboxv1.CloudflareRewritePartMode) string {
	switch m {
	case neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_STATIC:
		return cloudflare.RewritePartStatic
	case neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_EXPRESSION:
		return cloudflare.RewritePartExpression
	case neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_KEEP,
		neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_UNSPECIFIED:
		return cloudflare.RewritePartKeep
	}
	return "invalid"
}

func headerOpToProto(op string) neoboxv1.CloudflareHeaderOperation {
	switch op {
	case cloudflare.HeaderOpSetStatic:
		return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_SET_STATIC
	case cloudflare.HeaderOpSetExpression:
		return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_SET_EXPRESSION
	case cloudflare.HeaderOpAddStatic:
		return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_ADD_STATIC
	case cloudflare.HeaderOpAddExpression:
		return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_ADD_EXPRESSION
	case cloudflare.HeaderOpRemove:
		return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_REMOVE
	}
	return neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_UNSPECIFIED
}

func headerOpFromProto(op neoboxv1.CloudflareHeaderOperation) string {
	switch op {
	case neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_SET_STATIC:
		return cloudflare.HeaderOpSetStatic
	case neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_SET_EXPRESSION:
		return cloudflare.HeaderOpSetExpression
	case neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_ADD_STATIC:
		return cloudflare.HeaderOpAddStatic
	case neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_ADD_EXPRESSION:
		return cloudflare.HeaderOpAddExpression
	case neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_REMOVE:
		return cloudflare.HeaderOpRemove
	}
	return ""
}

func headersToProto(cfg *cloudflare.HeaderTransformConfig) *neoboxv1.CloudflareHeaderTransformConfig {
	out := &neoboxv1.CloudflareHeaderTransformConfig{
		Headers: make([]*neoboxv1.CloudflareHeaderModification, 0, len(cfg.Headers)),
	}
	for _, h := range cfg.Headers {
		out.Headers = append(out.Headers, &neoboxv1.CloudflareHeaderModification{
			Name:      h.Name,
			Operation: headerOpToProto(h.Operation),
			Value:     h.Value,
		})
	}
	return out
}

func headersFromProto(in *neoboxv1.CloudflareHeaderTransformConfig) *cloudflare.HeaderTransformConfig {
	if in == nil {
		return nil
	}
	mods := make([]cloudflare.HeaderModification, 0, len(in.GetHeaders()))
	for _, h := range in.GetHeaders() {
		mods = append(mods, cloudflare.HeaderModification{
			Name:      h.GetName(),
			Operation: headerOpFromProto(h.GetOperation()),
			Value:     h.GetValue(),
		})
	}
	return &cloudflare.HeaderTransformConfig{Headers: mods}
}

// zoneRuleInputFromProto extracts a typed ZoneRuleInput. Generic writable
// action_parameters JSON without a typed config is rejected.
func zoneRuleInputFromProto(ctx context.Context, x *neoboxv1.CloudflareZoneRule) (cloudflare.ZoneRuleInput, error) {
	if x == nil {
		return cloudflare.ZoneRuleInput{}, connectx.RequiredArgument(ctx, "rule")
	}
	if x.GetConfig() == nil {
		return cloudflare.ZoneRuleInput{}, connectx.InvalidArgument(ctx, "rule.config", "typed rule configuration is required")
	}
	// New rules default to enabled when omitted.
	enabled := true
	if x.Enabled != nil {
		enabled = *x.Enabled
	}
	in := cloudflare.ZoneRuleInput{
		Description: x.GetDescription(),
		Expression:  x.GetExpression(),
		Enabled:     enabled,
	}
	switch c := x.GetConfig().(type) {
	case *neoboxv1.CloudflareZoneRule_SingleRedirect:
		sr := c.SingleRedirect
		tk := ""
		switch sr.GetTargetKind() {
		case neoboxv1.CloudflareRedirectTargetKind_CLOUDFLARE_REDIRECT_TARGET_KIND_STATIC:
			tk = cloudflare.RedirectTargetStatic
		case neoboxv1.CloudflareRedirectTargetKind_CLOUDFLARE_REDIRECT_TARGET_KIND_EXPRESSION:
			tk = cloudflare.RedirectTargetExpression
		}
		in.Config.SingleRedirect = &cloudflare.SingleRedirectConfig{
			TargetKind:          tk,
			TargetValue:         sr.GetTargetValue(),
			StatusCode:          int(sr.GetStatusCode()),
			PreserveQueryString: sr.GetPreserveQueryString(),
		}
	case *neoboxv1.CloudflareZoneRule_UrlRewrite:
		ur := c.UrlRewrite
		in.Config.URLRewrite = &cloudflare.URLRewriteConfig{
			PathMode:   rewriteModeFromProto(ur.GetPathMode()),
			PathValue:  ur.GetPathValue(),
			QueryMode:  rewriteModeFromProto(ur.GetQueryMode()),
			QueryValue: ur.GetQueryValue(),
		}
	case *neoboxv1.CloudflareZoneRule_RequestHeader:
		in.Config.RequestHeader = headersFromProto(c.RequestHeader)
	case *neoboxv1.CloudflareZoneRule_ResponseHeader:
		in.Config.ResponseHeader = headersFromProto(c.ResponseHeader)
	case *neoboxv1.CloudflareZoneRule_Origin:
		o := c.Origin
		oc := &cloudflare.OriginRuleConfig{}
		if o.DestinationPort != nil {
			p := int(*o.DestinationPort)
			oc.DestinationPort = &p
		}
		if o.HostHeader != nil {
			v := *o.HostHeader
			oc.HostHeader = &v
		}
		if o.Sni != nil {
			v := *o.Sni
			oc.SNI = &v
		}
		if o.DnsRecord != nil {
			v := *o.DnsRecord
			oc.DNSRecord = &v
		}
		in.Config.Origin = oc
	}
	return in, nil
}

func (s *CloudflareServiceServer) GetCloudflareZoneRules(ctx context.Context, req *neoboxv1.GetCloudflareZoneRulesRequest) (*neoboxv1.GetCloudflareZoneRulesResponse, error) {
	_, a, err := s.openAccount(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	var only cloudflare.ZoneRuleCategory
	if req.GetCategory() != neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_UNSPECIFIED {
		p, perr := protoRuleCategory(req.GetCategory())
		if perr != nil {
			return nil, connectx.InvalidArgument(ctx, "category", perr.Error())
		}
		only = p
	}
	views, err := a.ZoneRules(ctx, trim(req.GetZoneId()), only)
	if err != nil {
		return nil, mapCloudflareErr(ctx, err)
	}
	out := &neoboxv1.GetCloudflareZoneRulesResponse{
		Categories: make([]*neoboxv1.CloudflareZoneRules, 0, len(views)),
	}
	for _, v := range views {
		cat := &neoboxv1.CloudflareZoneRules{
			Category:       ruleCategoryProto(v.Category),
			RulesetId:      v.RulesetID,
			Snapshot:       v.Snapshot,
			Issue:          v.Issue,
			IssueKind:      issueKindToProto(v.IssueKind),
			RulesetMissing: v.RulesetMissing,
		}
		for _, r := range v.Rules {
			cat.Rules = append(cat.Rules, zoneRuleToProto(v.Category, r))
		}
		out.Categories = append(out.Categories, cat)
	}
	return out, nil
}

func (s *CloudflareServiceServer) CreateCloudflareZoneRule(ctx context.Context, req *neoboxv1.CreateCloudflareZoneRuleRequest) (*neoboxv1.CreateCloudflareZoneRuleResponse, error) {
	d, a, err := s.openAccount(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	phase, err := protoRuleCategory(req.GetCategory())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "category", err.Error())
	}
	in, err := zoneRuleInputFromProto(ctx, req.GetRule())
	if err != nil {
		return nil, err
	}
	res, err := a.CreateZoneRule(ctx, d.user, trim(req.GetZoneId()), phase, trim(req.GetSnapshot()), in)
	if err != nil {
		return nil, mapCloudflareErr(ctx, err)
	}
	out := &neoboxv1.CreateCloudflareZoneRuleResponse{
		Result: changeResultToProto(res.OperationResult),
	}
	if res.Rule != nil {
		out.Rule = zoneRuleToProto(phase, *res.Rule)
	}
	return out, nil
}

func (s *CloudflareServiceServer) UpdateCloudflareZoneRule(ctx context.Context, req *neoboxv1.UpdateCloudflareZoneRuleRequest) (*neoboxv1.UpdateCloudflareZoneRuleResponse, error) {
	d, a, err := s.openAccount(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	phase, err := protoRuleCategory(req.GetCategory())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "category", err.Error())
	}
	in, err := zoneRuleInputFromProto(ctx, req.GetRule())
	if err != nil {
		return nil, err
	}
	res, err := a.UpdateZoneRule(ctx, d.user, trim(req.GetZoneId()), phase, trim(req.GetRuleId()), trim(req.GetSnapshot()), in)
	if err != nil {
		return nil, mapCloudflareErr(ctx, err)
	}
	out := &neoboxv1.UpdateCloudflareZoneRuleResponse{
		Result: changeResultToProto(res.OperationResult),
	}
	if res.Rule != nil {
		out.Rule = zoneRuleToProto(phase, *res.Rule)
	}
	return out, nil
}

func (s *CloudflareServiceServer) DeleteCloudflareZoneRule(ctx context.Context, req *neoboxv1.DeleteCloudflareZoneRuleRequest) (*neoboxv1.DeleteCloudflareZoneRuleResponse, error) {
	phase, err := protoRuleCategory(req.GetCategory())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "category", err.Error())
	}
	r, err := s.change(ctx, req.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteZoneRule(ctx, actor, trim(req.GetZoneId()), phase, trim(req.GetRuleId()), trim(req.GetSnapshot()))
	})
	if err != nil {
		return nil, err
	}
	return &neoboxv1.DeleteCloudflareZoneRuleResponse{Result: r}, nil
}

func (s *CloudflareServiceServer) ReorderCloudflareZoneRules(ctx context.Context, req *neoboxv1.ReorderCloudflareZoneRulesRequest) (*neoboxv1.ReorderCloudflareZoneRulesResponse, error) {
	phase, err := protoRuleCategory(req.GetCategory())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "category", err.Error())
	}
	r, err := s.change(ctx, req.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.ReorderZoneRules(ctx, actor, trim(req.GetZoneId()), phase, trim(req.GetSnapshot()), req.GetRuleIds())
	})
	if err != nil {
		return nil, err
	}
	return &neoboxv1.ReorderCloudflareZoneRulesResponse{Result: r}, nil
}

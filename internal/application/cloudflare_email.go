package application

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// --- Email Routing ---

func emailActionToProto(t string) neoboxv1.CloudflareEmailAction {
	switch t {
	case cloudflare.EmailActionForward:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD
	case cloudflare.EmailActionDrop:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP
	case cloudflare.EmailActionWorker:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_WORKER
	}
	return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_UNSPECIFIED
}

// emailActionFromProto leaves out Worker: Neo Box does not create or
// change Worker rules, so it fails the input check as "must be forward or
// drop".
func emailActionFromProto(a neoboxv1.CloudflareEmailAction) string {
	switch a {
	case neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD:
		return cloudflare.EmailActionForward
	case neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP:
		return cloudflare.EmailActionDrop
	}
	return ""
}

func emailRuleToProto(r cloudflare.EmailRule) *neoboxv1.CloudflareEmailRule {
	why := r.ReadOnlyReason(false)
	return &neoboxv1.CloudflareEmailRule{
		Id: r.ID, Name: r.Name, Enabled: r.Enabled, Priority: int32(r.Priority),
		Address: r.Address(), Action: emailActionToProto(r.ActionType()), Target: r.Target(),
		Editable: why == "", ReadonlyReason: why,
	}
}

func emailCatchAllToProto(r cloudflare.EmailRule) *neoboxv1.CloudflareEmailCatchAll {
	why := r.ReadOnlyReason(true)
	return &neoboxv1.CloudflareEmailCatchAll{
		Enabled: r.Enabled, Action: emailActionToProto(r.ActionType()), Target: r.Target(),
		Editable: why == "", ReadonlyReason: why,
	}
}

func emailRuleInputFromProto(in *neoboxv1.CloudflareEmailRuleInput) cloudflare.EmailRuleInput {
	return cloudflare.EmailRuleInput{
		Name: in.GetName(), Address: in.GetAddress(), Action: emailActionFromProto(in.GetAction()),
		Target: in.GetTarget(), Enabled: in.GetEnabled(),
	}
}

func (s *CloudflareServiceServer) GetCloudflareEmailRouting(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareEmailRoutingRequest]) (*connect.Response[neoboxv1.GetCloudflareEmailRoutingResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	v, err := a.EmailRouting(ctx, trim(req.Msg.GetZoneId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.GetCloudflareEmailRoutingResponse{
		Settings: &neoboxv1.CloudflareEmailRoutingSettings{
			Enabled: v.Settings.Enabled, Status: v.Settings.Status, SupportSubaddress: v.Settings.SupportSubaddress,
		},
		Issues: readIssuesToProto(v.Issues),
	}
	for _, r := range v.Rules {
		out.Rules = append(out.Rules, emailRuleToProto(r))
	}
	if v.CatchAll != nil {
		out.CatchAll = emailCatchAllToProto(*v.CatchAll)
	}
	for _, d := range v.RequiredDNS {
		rec := &neoboxv1.CloudflareEmailDNSRecord{Type: d.Type, Name: d.Name, Content: d.Content}
		if d.Priority != nil {
			rec.Priority = int32(*d.Priority)
		}
		out.RequiredDnsRecords = append(out.RequiredDnsRecords, rec)
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) ListCloudflareEmailAddresses(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareEmailAddressesRequest]) (*connect.Response[neoboxv1.ListCloudflareEmailAddressesResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	list, err := a.EmailAddresses(ctx)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflareEmailAddressesResponse{}
	for _, ad := range list {
		pa := &neoboxv1.CloudflareEmailAddress{Id: ad.ID, Email: ad.Email, Verified: ad.IsVerified()}
		if ad.Created != nil {
			pa.CreatedAt = timestamppb.New(*ad.Created)
		}
		if ad.IsVerified() {
			pa.VerifiedAt = timestamppb.New(*ad.Verified)
		}
		out.Addresses = append(out.Addresses, pa)
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) EnableCloudflareEmailRouting(ctx context.Context, req *connect.Request[neoboxv1.EnableCloudflareEmailRoutingRequest]) (*connect.Response[neoboxv1.EnableCloudflareEmailRoutingResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.EnableEmailRouting(ctx, actor, trim(req.Msg.GetZoneId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.EnableCloudflareEmailRoutingResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) CreateCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.CreateCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.CreateEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), emailRuleInputFromProto(req.Msg.GetRule()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) UpdateCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.UpdateCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.UpdateCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.UpdateEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetRuleId()), emailRuleInputFromProto(req.Msg.GetRule()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.UpdateCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.DeleteCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetRuleId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) SetCloudflareEmailCatchAll(ctx context.Context, req *connect.Request[neoboxv1.SetCloudflareEmailCatchAllRequest]) (*connect.Response[neoboxv1.SetCloudflareEmailCatchAllResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.SetEmailCatchAll(ctx, actor, trim(req.Msg.GetZoneId()), cloudflare.EmailCatchAllInput{
			Enabled: req.Msg.GetEnabled(), Action: emailActionFromProto(req.Msg.GetAction()), Target: req.Msg.GetTarget(),
		})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.SetCloudflareEmailCatchAllResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) CreateCloudflareEmailAddress(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareEmailAddressRequest]) (*connect.Response[neoboxv1.CreateCloudflareEmailAddressResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.CreateEmailAddress(ctx, actor, req.Msg.GetEmail())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareEmailAddressResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareEmailAddress(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareEmailAddressRequest]) (*connect.Response[neoboxv1.DeleteCloudflareEmailAddressResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteEmailAddress(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetAddressId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareEmailAddressResponse{Result: r}), nil
}

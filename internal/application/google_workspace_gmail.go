package application

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// gmailAPI is one user's Gmail settings, reached by impersonating that user.
// See docs/adr/0008.
type gmailAPI interface {
	ReadSettings(context.Context) (*googleworkspace.GmailSettings, error)
	UpdateVacation(context.Context, googleworkspace.VacationInput) (*googleworkspace.GmailVacation, error)
	SetWriteRecorder(googleworkspace.WriteRecorder)
}

type gmailClientFactory func(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, userEmail string, domains []string, reason string) (gmailAPI, error)

func defaultGmailClient(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, userEmail string, domains []string, reason string) (gmailAPI, error) {
	c, err := googleworkspace.NewGmailClient(ctx, cfg, sec, customer, userEmail)
	if err != nil {
		return nil, err
	}
	c.Domains, c.Reason = domains, reason
	return c, nil
}

// gmailTarget validates the confirmation and resolves the target inside the
// tenant. Nothing reaches Google's token endpoint for a user who is not in the
// tenant or whose primary email was not typed back.
func (s *GoogleWorkspaceServiceServer) gmailTarget(ctx context.Context, connectionID, tenantID, userID string, confirmation *neoboxv1.GmailAccessConfirmation) (*workspaceDeps, *repo.Tenant, workspaceAPI, *googleworkspace.User, string, error) {
	reason, err := gmailReason(confirmation)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	if strings.TrimSpace(userID) == "" {
		return nil, nil, nil, nil, "", connectx.RequiredArgument("user_id")
	}
	d, t, directory, err := s.open(ctx, connectionID, tenantID)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	u, err := directory.GetUser(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, nil, nil, nil, "", mapWorkspaceErr(err)
	}
	if !strings.EqualFold(strings.TrimSpace(confirmation.GetTargetEmail()), u.PrimaryEmail) {
		return nil, nil, nil, nil, "", connectx.InvalidArgument("confirmation.target_email", "must be this user's primary email")
	}
	return d, t, directory, u, reason, nil
}

// gmailAccess opens a Gmail settings client whose token subject is the
// resolved user.
func (s *GoogleWorkspaceServiceServer) gmailAccess(ctx context.Context, connectionID, tenantID, userID string, confirmation *neoboxv1.GmailAccessConfirmation) (*workspaceDeps, *repo.Tenant, gmailAPI, string, error) {
	d, t, directory, u, reason, err := s.gmailTarget(ctx, connectionID, tenantID, userID, confirmation)
	if err != nil {
		return nil, nil, nil, "", err
	}
	// Unknown domains leave "external" unknown rather than guessing.
	domains, err := directory.VerifiedDomains(ctx)
	if err != nil {
		domains = nil
	}
	var cfg googleworkspace.ConnectionConfig
	var sec googleworkspace.ConnectionSecret
	if err := d.conns.Open(d.c, &cfg, &sec); err != nil {
		return nil, nil, nil, "", mapConnectionErr(err)
	}
	api, err := d.gmail(ctx, cfg, sec, t.CustomerID, u.PrimaryEmail, domains, reason)
	if err != nil {
		return nil, nil, nil, "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	api.SetWriteRecorder(workspaceRecorder{repo: d.repo, connection: d.c, tenant: t, actorName: d.actorName})
	return d, t, api, u.PrimaryEmail, nil
}

func gmailReason(c *neoboxv1.GmailAccessConfirmation) (string, error) {
	reason := strings.TrimSpace(c.GetReason())
	if reason == "" {
		return "", connectx.InvalidArgument("confirmation.reason", "an access reason is required")
	}
	if utf8.RuneCountInString(reason) > googleworkspace.MaxAccessReason {
		return "", connectx.InvalidArgument("confirmation.reason", "must be at most 200 characters")
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", connectx.InvalidArgument("confirmation.reason", "must be a single line of text")
		}
	}
	return reason, nil
}

func (s *GoogleWorkspaceServiceServer) GetWorkspaceUserGmailSettings(ctx context.Context, req *connect.Request[neoboxv1.GetWorkspaceUserGmailSettingsRequest]) (*connect.Response[neoboxv1.GetWorkspaceUserGmailSettingsResponse], error) {
	_, _, api, email, err := s.gmailAccess(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), req.Msg.GetUserId(), req.Msg.GetConfirmation())
	if err != nil {
		return nil, err
	}
	settings, err := api.ReadSettings(ctx)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GmailSettingsScope)
	}
	out := &neoboxv1.GetWorkspaceUserGmailSettingsResponse{
		UserEmail:           email,
		Vacation:            gmailVacationToProto(settings.Vacation.Err, &settings.Vacation),
		AutoForwarding:      gmailAutoForwardingToProto(&settings.AutoForwarding),
		ForwardingAddresses: gmailForwardingAddressesToProto(&settings.ForwardingAddresses),
		Delegates:           gmailDelegatesToProto(&settings.Delegates),
		Filters:             gmailFiltersToProto(&settings.Filters),
		SendAs:              gmailSendAsToProto(&settings.SendAs),
		ReadAt:              timestamppb.New(settings.ReadAt),
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceUserGmailVacation(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceUserGmailVacationRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceUserGmailVacationResponse], error) {
	in := req.Msg.GetVacation()
	if in == nil {
		return nil, connectx.RequiredArgument("vacation")
	}
	input := googleworkspace.VacationInput{
		Enabled:            in.GetEnabled(),
		Subject:            in.GetSubject(),
		BodyPlainText:      in.GetBodyPlainText(),
		RestrictToContacts: in.GetRestrictToContacts(),
		RestrictToDomain:   in.GetRestrictToDomain(),
	}
	if in.GetStartTime() != nil {
		input.StartTime = in.GetStartTime().AsTime()
	}
	if in.GetEndTime() != nil {
		input.EndTime = in.GetEndTime().AsTime()
	}
	if err := input.Validate(); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	_, _, api, _, err := s.gmailAccess(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), req.Msg.GetUserId(), req.Msg.GetConfirmation())
	if err != nil {
		return nil, err
	}
	v, err := api.UpdateVacation(ctx, input)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GmailSettingsScope)
	}
	return connect.NewResponse(&neoboxv1.UpdateWorkspaceUserGmailVacationResponse{Vacation: gmailVacationToProto("", v)}), nil
}

func gmailState(msg string) *neoboxv1.GmailSectionState {
	return &neoboxv1.GmailSectionState{Ok: msg == "", Error: msg}
}

func gmailTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func gmailVacationToProto(errMsg string, v *googleworkspace.GmailVacation) *neoboxv1.GmailVacation {
	if errMsg != "" {
		return &neoboxv1.GmailVacation{State: gmailState(errMsg)}
	}
	return &neoboxv1.GmailVacation{State: gmailState(""), Enabled: v.Enabled, Subject: v.Subject, BodyPlainText: v.BodyPlainText, BodyIsHtml: v.BodyIsHTML, RestrictToContacts: v.RestrictToContacts, RestrictToDomain: v.RestrictToDomain, StartTime: gmailTime(v.StartTime), EndTime: gmailTime(v.EndTime)}
}

func gmailAutoForwardingToProto(a *googleworkspace.GmailAutoForwarding) *neoboxv1.GmailAutoForwarding {
	if a.Err != "" {
		return &neoboxv1.GmailAutoForwarding{State: gmailState(a.Err)}
	}
	return &neoboxv1.GmailAutoForwarding{State: gmailState(""), Enabled: a.Enabled, EmailAddress: a.EmailAddress, Disposition: a.Disposition, External: a.External}
}

func gmailForwardingAddressesToProto(a *googleworkspace.GmailForwardingAddresses) *neoboxv1.GmailForwardingAddresses {
	out := &neoboxv1.GmailForwardingAddresses{State: gmailState(a.Err)}
	for _, x := range a.Addresses {
		out.Addresses = append(out.Addresses, &neoboxv1.GmailForwardingAddress{Email: x.Email, VerificationStatus: x.VerificationStatus, External: x.External})
	}
	return out
}

func gmailDelegatesToProto(a *googleworkspace.GmailDelegates) *neoboxv1.GmailDelegates {
	out := &neoboxv1.GmailDelegates{State: gmailState(a.Err)}
	for _, x := range a.Delegates {
		out.Delegates = append(out.Delegates, &neoboxv1.GmailDelegate{Email: x.Email, VerificationStatus: x.VerificationStatus})
	}
	return out
}

func gmailFiltersToProto(a *googleworkspace.GmailFilters) *neoboxv1.GmailFilters {
	out := &neoboxv1.GmailFilters{State: gmailState(a.Err)}
	for _, f := range a.Filters {
		out.Filters = append(out.Filters, &neoboxv1.GmailFilter{Id: f.ID, From: f.From, To: f.To, Subject: f.Subject, Query: f.Query, NegatedQuery: f.NegatedQuery, HasAttachment: f.HasAttachment, ForwardTo: f.ForwardTo, ForwardExternal: f.ForwardExternal, Deletes: f.Deletes, SkipsInbox: f.SkipsInbox})
	}
	return out
}

func gmailSendAsToProto(a *googleworkspace.GmailSendAsList) *neoboxv1.GmailSendAsList {
	out := &neoboxv1.GmailSendAsList{State: gmailState(a.Err)}
	for _, x := range a.Addresses {
		out.Addresses = append(out.Addresses, &neoboxv1.GmailSendAs{Email: x.Email, DisplayName: x.DisplayName, IsPrimary: x.IsPrimary, IsDefault: x.IsDefault, TreatAsAlias: x.TreatAsAlias, VerificationStatus: x.VerificationStatus, External: x.External})
	}
	return out
}

package application

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type workspaceAPI interface {
	Verify(context.Context) error
	ListUsers(context.Context, string, string, int) (*googleworkspace.UserPage, error)
	GetUser(context.Context, string) (*googleworkspace.User, error)
	CreateUser(context.Context, string, map[string]any) (*googleworkspace.User, error)
	UpdateUser(context.Context, string, map[string]any) (*googleworkspace.User, error)
	ListAliases(context.Context, string) ([]googleworkspace.Alias, error)
	AddAlias(context.Context, string, string) (*googleworkspace.Alias, error)
	DeleteAlias(context.Context, string, string) error
	ListGroups(context.Context, string, string, int) (*googleworkspace.GroupPage, error)
	ListUserGroups(context.Context, string) ([]googleworkspace.Group, error)
	CreateGroup(context.Context, string, string, string) (*googleworkspace.Group, error)
	UpdateGroup(context.Context, string, string, string, string) (*googleworkspace.Group, error)
	DeleteGroup(context.Context, string) error
	ListMembers(context.Context, string, string, int) (*googleworkspace.MemberPage, error)
	AddMember(context.Context, string, string, string) (*googleworkspace.Member, error)
	UpdateMember(context.Context, string, string, string) (*googleworkspace.Member, error)
	DeleteMember(context.Context, string, string) error
	ListOrgUnits(context.Context) ([]googleworkspace.OrgUnit, error)
	CreateOrgUnit(context.Context, string, string, string) (*googleworkspace.OrgUnit, error)
	UpdateOrgUnit(context.Context, string, string, string, string) (*googleworkspace.OrgUnit, error)
	DeleteOrgUnit(context.Context, string) error
	SignOutUser(context.Context, string) error
	ListUserTokens(context.Context, string) ([]googleworkspace.UserToken, error)
	RevokeUserToken(context.Context, string, string) error
	SetWriteRecorder(googleworkspace.WriteRecorder)
	VerifiedDomains(context.Context) ([]string, error)
	ListAuditActivities(context.Context, googleworkspace.AuditQuery) (*googleworkspace.AuditPage, error)
	ListDriveUsage(context.Context, time.Time) (*googleworkspace.DriveUsage, error)
}
type workspaceClientFactory func(context.Context, googleworkspace.ConnectionConfig, googleworkspace.ConnectionSecret, string, string, ...string) (workspaceAPI, error)
type GoogleWorkspaceServiceServer struct {
	mu        sync.RWMutex
	conns     *connection.Service
	repo      repo.Repository
	newClient workspaceClientFactory
	newGmail  gmailClientFactory
	newDrive  driveClientFactory
}

func NewGoogleWorkspaceServiceServer() *GoogleWorkspaceServiceServer {
	return &GoogleWorkspaceServiceServer{newClient: func(ctx context.Context, c googleworkspace.ConnectionConfig, s googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		return googleworkspace.NewClient(ctx, c, s, customer, admin, scopes...)
	}, newGmail: defaultGmailClient, newDrive: defaultDriveClient}
}
func (s *GoogleWorkspaceServiceServer) SetDeps(r repo.Repository, c *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repo = r
	s.conns = c
}

type workspaceDeps struct {
	repo      repo.Repository
	conns     *connection.Service
	c         *connrepo.Connection
	factory   workspaceClientFactory
	gmail     gmailClientFactory
	drive     driveClientFactory
	actorName string
}

func (s *GoogleWorkspaceServiceServer) deps(ctx context.Context, id string) (*workspaceDeps, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	r, c, f, gm, dr := s.repo, s.conns, s.newClient, s.newGmail, s.newDrive
	s.mu.RUnlock()
	if r == nil || c == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Google Workspace is not available"))
	}
	conn, err := loadConnection(ctx, c, user.GetId(), id)
	if err != nil {
		return nil, err
	}
	if conn.Provider != googleworkspace.ProviderType {
		return nil, connectx.NotFound("connection not found")
	}
	actorName := user.GetDisplayName()
	if actorName == "" {
		actorName = user.GetUsername()
	}
	return &workspaceDeps{repo: r, conns: c, c: conn, factory: f, gmail: gm, drive: dr, actorName: actorName}, nil
}
func (d *workspaceDeps) tenant(ctx context.Context, id string) (*repo.Tenant, error) {
	if strings.TrimSpace(id) == "" {
		return nil, connectx.RequiredArgument("tenant_id")
	}
	t, err := d.repo.Get(ctx, d.c.UserID, d.c.ID, id)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return t, nil
}
func (d *workspaceDeps) client(ctx context.Context, t *repo.Tenant, scopes ...string) (workspaceAPI, error) {
	var cfg googleworkspace.ConnectionConfig
	var sec googleworkspace.ConnectionSecret
	if err := d.conns.Open(d.c, &cfg, &sec); err != nil {
		return nil, mapConnectionErr(err)
	}
	c, err := d.factory(ctx, cfg, sec, t.CustomerID, t.DelegatedAdminEmail, scopes...)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	c.SetWriteRecorder(workspaceRecorder{repo: d.repo, connection: d.c, tenant: t, actorName: d.actorName})
	return c, nil
}
func (d *workspaceDeps) record(ctx context.Context, t *repo.Tenant, err error) error {
	t.Status, t.StatusMessage, t.StatusCheckedAt = "ok", "", time.Now().UTC()
	if err != nil {
		t.Status = "error"
		t.StatusMessage = err.Error()
	}
	return d.repo.SetStatus(ctx, t)
}
func (s *GoogleWorkspaceServiceServer) ListWorkspaceTenants(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceTenantsRequest]) (*connect.Response[neoboxv1.ListWorkspaceTenantsResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	ts, err := d.repo.List(ctx, d.c.UserID, d.c.ID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := &neoboxv1.ListWorkspaceTenantsResponse{}
	for _, t := range ts {
		out.Tenants = append(out.Tenants, workspaceTenantToProto(t))
	}
	return connect.NewResponse(out), nil
}
func validateWorkspaceTenant(name, admin string) error {
	if strings.TrimSpace(name) == "" {
		return connectx.RequiredArgument("name")
	}
	if !googleworkspace.ValidEmail(strings.TrimSpace(admin)) {
		return connectx.InvalidArgument("delegated_admin_email", "must be an administrator email in this tenant")
	}
	return nil
}
func (s *GoogleWorkspaceServiceServer) CreateWorkspaceTenant(ctx context.Context, req *connect.Request[neoboxv1.CreateWorkspaceTenantRequest]) (*connect.Response[neoboxv1.CreateWorkspaceTenantResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err = validateWorkspaceTenant(req.Msg.GetName(), req.Msg.GetDelegatedAdminEmail()); err != nil {
		return nil, err
	}
	customer := strings.TrimSpace(req.Msg.GetCustomerId())
	if !googleworkspace.ValidCustomerID(customer) {
		return nil, connectx.InvalidArgument("customer_id", "must be the explicit Google Customer ID (C...), not my_customer or a domain")
	}
	now := time.Now().UTC()
	t := &repo.Tenant{ID: uuid.NewString(), UserID: d.c.UserID, ConnectionID: d.c.ID, Name: strings.TrimSpace(req.Msg.GetName()), CustomerID: customer, DelegatedAdminEmail: strings.TrimSpace(req.Msg.GetDelegatedAdminEmail()), Status: "ok", StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now}
	api, err := d.client(ctx, t)
	if err != nil {
		return nil, err
	}
	if err = api.Verify(ctx); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	if err = d.localMutation(ctx, t, "tenant_create", map[string]any{"name": t.Name, "customerId": t.CustomerID, "delegated_admin_email": t.DelegatedAdminEmail}, func() error { return d.repo.Create(ctx, t) }); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateWorkspaceTenantResponse{Tenant: workspaceTenantToProto(t)}), nil
}
func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceTenant(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceTenantRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceTenantResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	t, err := d.tenant(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err = validateWorkspaceTenant(req.Msg.GetName(), req.Msg.GetDelegatedAdminEmail()); err != nil {
		return nil, err
	}
	t.Name, t.DelegatedAdminEmail = strings.TrimSpace(req.Msg.GetName()), strings.TrimSpace(req.Msg.GetDelegatedAdminEmail())
	api, err := d.client(ctx, t)
	if err != nil {
		return nil, err
	}
	if err = api.Verify(ctx); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	t.Status, t.StatusMessage, t.StatusCheckedAt = "ok", "", time.Now().UTC()
	t.UpdatedAt = t.StatusCheckedAt
	if err = d.localMutation(ctx, t, "tenant_update", map[string]any{"name": t.Name, "delegated_admin_email": t.DelegatedAdminEmail}, func() error { return d.repo.Update(ctx, t) }); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateWorkspaceTenantResponse{Tenant: workspaceTenantToProto(t)}), nil
}
func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceTenant(ctx context.Context, req *connect.Request[neoboxv1.DeleteWorkspaceTenantRequest]) (*connect.Response[neoboxv1.DeleteWorkspaceTenantResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	t, err := d.tenant(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if err = d.localMutation(ctx, t, "tenant_delete", map[string]any{"name": t.Name}, func() error { return d.repo.Delete(ctx, d.c.UserID, d.c.ID, t.ID) }); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteWorkspaceTenantResponse{}), nil
}
func (s *GoogleWorkspaceServiceServer) TestWorkspaceTenant(ctx context.Context, req *connect.Request[neoboxv1.TestWorkspaceTenantRequest]) (*connect.Response[neoboxv1.TestWorkspaceTenantResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	t, err := d.tenant(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	api, err := d.client(ctx, t)
	if err != nil {
		return nil, err
	}
	check := api.Verify(ctx)
	if err = d.record(ctx, t, check); err != nil {
		return nil, connectx.InternalWith(err)
	}
	return connect.NewResponse(&neoboxv1.TestWorkspaceTenantResponse{Tenant: workspaceTenantToProto(t)}), nil
}
func (s *GoogleWorkspaceServiceServer) open(ctx context.Context, connectionID, tenantID string, scopes ...string) (*workspaceDeps, *repo.Tenant, workspaceAPI, error) {
	d, err := s.deps(ctx, connectionID)
	if err != nil {
		return nil, nil, nil, err
	}
	t, err := d.tenant(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	api, err := d.client(ctx, t, scopes...)
	return d, t, api, err
}

// Update tenant health only after an actual read, or an authorization refusal.
// Permission errors for a specific mutation do not mean all tenant access failed.
func workspaceReadStatus(ctx context.Context, d *workspaceDeps, t *repo.Tenant, err error) {
	var ae *googleworkspace.APIError
	if err == nil || errors.Is(err, googleworkspace.ErrDelegation) || errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
		_ = d.record(ctx, t, err)
	}
}
func (s *GoogleWorkspaceServiceServer) ListWorkspaceUsers(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUsersRequest]) (*connect.Response[neoboxv1.ListWorkspaceUsersResponse], error) {
	d, t, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	size := int(req.Msg.GetPageSize())
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 500 {
		return nil, connectx.InvalidArgument("page_size", "must be between 1 and 500")
	}
	page, err := api.ListUsers(ctx, strings.TrimSpace(req.Msg.GetQuery()), req.Msg.GetPageToken(), size)
	workspaceReadStatus(ctx, d, t, err)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	out := &neoboxv1.ListWorkspaceUsersResponse{NextPageToken: page.NextPageToken}
	for i := range page.Users {
		out.Users = append(out.Users, workspaceUserToProto(&page.Users[i]))
	}
	return connect.NewResponse(out), nil
}
func (s *GoogleWorkspaceServiceServer) GetWorkspaceUser(ctx context.Context, req *connect.Request[neoboxv1.GetWorkspaceUserRequest]) (*connect.Response[neoboxv1.GetWorkspaceUserResponse], error) {
	d, t, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	u, err := api.GetUser(ctx, req.Msg.GetUserId())
	workspaceReadStatus(ctx, d, t, err)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.GetWorkspaceUserResponse{User: workspaceUserToProto(u)}), nil
}
func workspaceProfile(p *neoboxv1.WorkspaceUserProfile) (map[string]any, error) {
	if p == nil {
		return nil, connectx.RequiredArgument("profile")
	}
	given, family := strings.TrimSpace(p.GetGivenName()), strings.TrimSpace(p.GetFamilyName())
	if given == "" || family == "" {
		return nil, connectx.InvalidArgument("profile", "given and family name are required")
	}
	path := strings.TrimSpace(p.GetOrgUnitPath())
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, connectx.InvalidArgument("org_unit_path", "must start with /")
	}
	return map[string]any{"name": map[string]string{"givenName": given, "familyName": family}, "orgUnitPath": path}, nil
}
func workspacePassword(password string) error {
	// Google requires 8..100 characters. Never trim a password.
	n := utf8.RuneCountInString(password)
	if n < 8 || n > 100 {
		return connectx.InvalidArgument("password", "must contain 8 to 100 characters")
	}
	return nil
}
func workspaceTenantToProto(t *repo.Tenant) *neoboxv1.WorkspaceTenant {
	out := &neoboxv1.WorkspaceTenant{Id: t.ID, ConnectionId: t.ConnectionID, Name: t.Name, CustomerId: t.CustomerID, DelegatedAdminEmail: t.DelegatedAdminEmail, Status: statusToProto(connrepo.Status(t.Status)), StatusMessage: t.StatusMessage}
	if !t.StatusCheckedAt.IsZero() {
		out.StatusCheckedAt = timestamppb.New(t.StatusCheckedAt)
	}
	return out
}
func workspaceUserToProto(u *googleworkspace.User) *neoboxv1.WorkspaceUser {
	if u == nil {
		return nil
	}
	return &neoboxv1.WorkspaceUser{Id: u.ID, PrimaryEmail: u.PrimaryEmail, CustomerId: u.CustomerID, GivenName: u.Name.GivenName, FamilyName: u.Name.FamilyName, OrgUnitPath: u.OrgUnitPath, Suspended: u.Suspended, IsAdmin: u.IsAdmin != nil && *u.IsAdmin, ChangePasswordAtNextLogin: u.ChangePasswordAtNextLogin, SuperAdmin: u.IsAdmin, DelegatedAdmin: u.IsDelegatedAdmin, TwoStepEnrolled: u.TwoStepEnrolled, TwoStepEnforced: u.TwoStepEnforced, LastLoginTime: u.LastLoginTime, CreationTime: u.CreationTime}
}
func mapWorkspaceErr(err error) error {
	var ce *connect.Error
	var ae *googleworkspace.APIError
	var field *googleworkspace.FieldError
	switch {
	case errors.As(err, &field):
		return connectx.InvalidArgument(field.Field, field.Message)
	case errors.As(err, &ce):
		return ce
	case errors.Is(err, repo.ErrNotFound), errors.Is(err, googleworkspace.ErrTenantMismatch):
		return connectx.NotFound(err.Error())
	case errors.Is(err, repo.ErrDuplicate):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, googleworkspace.ErrDelegation):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, googleworkspace.ErrLogUnavailable):
		return connect.NewError(connect.CodeUnavailable, err)
	case errors.Is(err, googleworkspace.ErrOutcomeLogUnavailable):
		return connect.NewError(connect.CodeUnknown, err)
	case errors.Is(err, googleworkspace.ErrUncertain):
		return connect.NewError(connect.CodeUnknown, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.As(err, &ae):
		switch ae.Status {
		case 401:
			return connect.NewError(connect.CodeFailedPrecondition, err)
		case 403:
			return connect.NewError(connect.CodePermissionDenied, err)
		case 404:
			return connectx.NotFound("user or tenant not found at Google")
		case 409:
			return connect.NewError(connect.CodeAlreadyExists, err)
		case 400:
			return connect.NewError(connect.CodeInvalidArgument, err)
		case 429:
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("Google Workspace request failed; check authorization and connectivity"))
}

func (s *GoogleWorkspaceServiceServer) CreateWorkspaceUser(ctx context.Context, req *connect.Request[neoboxv1.CreateWorkspaceUserRequest]) (*connect.Response[neoboxv1.CreateWorkspaceUserResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(req.Msg.GetPrimaryEmail())
	if !googleworkspace.ValidEmail(email) {
		return nil, connectx.InvalidArgument("primary_email", "must be a valid email")
	}
	body, err := workspaceProfile(req.Msg.GetProfile())
	if err != nil {
		return nil, err
	}
	if err = workspacePassword(req.Msg.GetPassword()); err != nil {
		return nil, err
	}
	body["password"], body["changePasswordAtNextLogin"] = req.Msg.GetPassword(), req.Msg.GetChangePasswordAtNextLogin()
	u, err := api.CreateUser(ctx, email, body)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateWorkspaceUserResponse{User: workspaceUserToProto(u)}), nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceUser(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceUserRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceUserResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	body, err := workspaceProfile(req.Msg.GetProfile())
	if err != nil {
		return nil, err
	}
	u, err := api.UpdateUser(ctx, req.Msg.GetUserId(), body)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateWorkspaceUserResponse{User: workspaceUserToProto(u)}), nil
}

func (s *GoogleWorkspaceServiceServer) ResetWorkspaceUserPassword(ctx context.Context, req *connect.Request[neoboxv1.ResetWorkspaceUserPasswordRequest]) (*connect.Response[neoboxv1.ResetWorkspaceUserPasswordResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	if err = workspacePassword(req.Msg.GetPassword()); err != nil {
		return nil, err
	}
	body := map[string]any{"password": req.Msg.GetPassword(), "changePasswordAtNextLogin": req.Msg.GetChangePasswordAtNextLogin()}
	u, err := api.UpdateUser(ctx, req.Msg.GetUserId(), body)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.ResetWorkspaceUserPasswordResponse{User: workspaceUserToProto(u)}), nil
}

func (s *GoogleWorkspaceServiceServer) SetWorkspaceUserSuspended(ctx context.Context, req *connect.Request[neoboxv1.SetWorkspaceUserSuspendedRequest]) (*connect.Response[neoboxv1.SetWorkspaceUserSuspendedResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	body := map[string]any{"suspended": req.Msg.GetSuspended()}
	u, err := api.UpdateUser(ctx, req.Msg.GetUserId(), body)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return connect.NewResponse(&neoboxv1.SetWorkspaceUserSuspendedResponse{User: workspaceUserToProto(u)}), nil
}

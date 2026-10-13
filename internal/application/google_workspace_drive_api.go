package application

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// driveAPI is Drive metadata read with one of the two Drive scopes. See
// docs/adr/0009.
type driveAPI interface {
	ListSharedDrives(context.Context, googleworkspace.SharedDriveQuery) (*googleworkspace.SharedDrivePage, error)
	ListExposedFiles(context.Context, googleworkspace.ExposedFilesQuery) (*googleworkspace.ExposedFilesPage, error)
	SetWriteRecorder(googleworkspace.WriteRecorder)
}

type driveClientFactory func(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, subject, scope string, domains []string, reason string) (driveAPI, error)

func defaultDriveClient(ctx context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, subject, scope string, domains []string, reason string) (driveAPI, error) {
	c, err := googleworkspace.NewDriveClient(ctx, cfg, sec, customer, subject, scope)
	if err != nil {
		return nil, err
	}
	c.Domains, c.Reason = domains, reason
	return c, nil
}

func accessReasonArg(raw, field string) (string, error) {
	reason, err := googleworkspace.ValidAccessReason(raw)
	if err != nil {
		return "", connectx.InvalidArgument(field, err.Error())
	}
	return reason, nil
}

// openDrive opens a Drive client for the tenant with exactly one Drive scope.
// The caller has already validated the reason and, for a user, the target.
func (d *workspaceDeps) openDrive(ctx context.Context, t *repo.Tenant, subject, scope string, domains []string, reason string) (driveAPI, error) {
	var cfg googleworkspace.ConnectionConfig
	var sec googleworkspace.ConnectionSecret
	if err := d.conns.Open(d.c, &cfg, &sec); err != nil {
		return nil, mapConnectionErr(err)
	}
	api, err := d.drive(ctx, cfg, sec, t.CustomerID, subject, scope, domains, reason)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	api.SetWriteRecorder(workspaceRecorder{repo: d.repo, connection: d.c, tenant: t, actorName: d.actorName})
	return api, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceSharedDrives(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceSharedDrivesRequest]) (*connect.Response[neoboxv1.ListWorkspaceSharedDrivesResponse], error) {
	in := req.Msg
	reason, err := accessReasonArg(in.GetReason(), "reason")
	if err != nil {
		return nil, err
	}
	q := googleworkspace.SharedDriveQuery{NamePrefix: in.GetNamePrefix(), NoOrganizer: in.GetNoOrganizer(), OrgUnitID: in.GetOrgUnitId(), PageToken: in.GetPageToken(), PageSize: int(in.GetPageSize())}
	if in.MaxMembers != nil {
		n := int(in.GetMaxMembers())
		q.MaxMembers = &n
	}
	if err := q.Validate(); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	d, t, err := s.openTenant(ctx, in.GetConnectionId(), in.GetTenantId())
	if err != nil {
		return nil, err
	}
	// The domain-wide list is read as the tenant's delegated administrator.
	api, err := d.openDrive(ctx, t, t.DelegatedAdminEmail, googleworkspace.DriveReadonlyScope, nil, reason)
	if err != nil {
		return nil, err
	}
	page, err := api.ListSharedDrives(ctx, q)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.DriveReadonlyScope)
	}
	out := &neoboxv1.ListWorkspaceSharedDrivesResponse{NextPageToken: page.NextPageToken, ReadAt: timestamppb.New(page.ReadAt)}
	for _, sd := range page.Drives {
		row := &neoboxv1.WorkspaceSharedDrive{Id: sd.ID, Name: sd.Name, OrgUnitId: sd.OrgUnitID, Restrictions: &neoboxv1.WorkspaceSharedDriveRestrictions{
			DomainUsersOnly: sd.Restrictions.DomainUsersOnly, DriveMembersOnly: sd.Restrictions.DriveMembersOnly, AdminManagedRestrictions: sd.Restrictions.AdminManaged,
			CopyRequiresWriterPermission: sd.Restrictions.CopyRequiresWriterPermission, SharingFoldersRequiresOrganizerPermission: sd.Restrictions.SharingFoldersRequiresOrganizerPermission,
		}}
		if !sd.CreatedTime.IsZero() {
			row.CreatedTime = timestamppb.New(sd.CreatedTime)
		}
		out.Drives = append(out.Drives, row)
	}
	return connect.NewResponse(out), nil
}

// openTenant loads the connection and the tenant without opening a Google
// client; the Drive clients are opened with their own scope.
func (s *GoogleWorkspaceServiceServer) openTenant(ctx context.Context, connectionID, tenantID string) (*workspaceDeps, *repo.Tenant, error) {
	d, err := s.deps(ctx, connectionID)
	if err != nil {
		return nil, nil, err
	}
	t, err := d.tenant(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	return d, t, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserExposedFiles(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUserExposedFilesRequest]) (*connect.Response[neoboxv1.ListWorkspaceUserExposedFilesResponse], error) {
	in := req.Msg
	reason, err := accessReasonArg(in.GetConfirmation().GetReason(), "confirmation.reason")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	q := googleworkspace.ExposedFilesQuery{Exposures: in.GetExposures(), PageToken: in.GetPageToken(), PageSize: int(in.GetPageSize())}
	if err := q.Validate(); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	// The target is resolved inside the tenant with the administrator's own
	// client before a token whose subject is that user is requested.
	d, t, directory, err := s.open(ctx, in.GetConnectionId(), in.GetTenantId())
	if err != nil {
		return nil, err
	}
	u, err := directory.GetUser(ctx, strings.TrimSpace(in.GetUserId()))
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	if !strings.EqualFold(strings.TrimSpace(in.GetConfirmation().GetTargetEmail()), u.PrimaryEmail) {
		return nil, connectx.InvalidArgument("confirmation.target_email", "must be this user's primary email")
	}
	// Unknown domains leave "outside the tenant" unknown rather than guessed.
	domains, err := directory.VerifiedDomains(ctx)
	if err != nil {
		domains = nil
	}
	api, err := d.openDrive(ctx, t, u.PrimaryEmail, googleworkspace.DriveMetadataScope, domains, reason)
	if err != nil {
		return nil, err
	}
	page, err := api.ListExposedFiles(ctx, q)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.DriveMetadataScope)
	}
	out := &neoboxv1.ListWorkspaceUserExposedFilesResponse{UserEmail: u.PrimaryEmail, NextPageToken: page.NextPageToken, IncompleteSearch: page.IncompleteSearch, ReadAt: timestamppb.New(page.ReadAt)}
	for _, f := range page.Files {
		row := &neoboxv1.WorkspaceExposedFile{Id: f.ID, Name: f.Name, MimeType: f.MimeType, WebViewLink: f.WebViewLink}
		if !f.ModifiedTime.IsZero() {
			row.ModifiedTime = timestamppb.New(f.ModifiedTime)
		}
		for _, e := range f.Exposures {
			row.Exposures = append(row.Exposures, &neoboxv1.WorkspaceFileExposure{Kind: e.Kind, Role: e.Role, Domain: e.Domain, External: e.External})
		}
		out.Files = append(out.Files, row)
	}
	return connect.NewResponse(out), nil
}

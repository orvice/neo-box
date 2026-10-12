package application

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const workspaceOverviewScanLimit = 5000
const workspaceOverviewTenantLimit = 20

type workspaceOverviewSource struct {
	d *workspaceDeps
	t *repo.Tenant
}
type workspaceObservedUser struct {
	u      googleworkspace.User
	f      googleworkspace.SecurityFlags
	source workspaceOverviewSource
}
type workspaceOverviewScan struct {
	summary *neoboxv1.WorkspaceTenantSummary
	users   []workspaceObservedUser
}

func validSecurityFilter(raw string) bool {
	switch raw {
	case "", "all", "admins", "admins_without_two_step", "inactive", "never_logged_in", "suspended", "unknown":
		return true
	}
	return false
}
func (s *GoogleWorkspaceServiceServer) overviewSources(ctx context.Context, connectionID, tenantID string) ([]workspaceOverviewSource, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	if tenantID != "" && connectionID == "" {
		return nil, connectx.RequiredArgument("connection_id")
	}
	s.mu.RLock()
	r, conns, f := s.repo, s.conns, s.newClient
	s.mu.RUnlock()
	if r == nil || conns == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Google Workspace is not available"))
	}
	var connections []*connrepo.Connection
	if connectionID != "" {
		d, err := s.deps(ctx, connectionID)
		if err != nil {
			return nil, err
		}
		connections = []*connrepo.Connection{d.c}
	} else {
		var err error
		connections, err = conns.List(ctx, connrepo.Filter{UserID: user.GetId(), Provider: googleworkspace.ProviderType})
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
	}
	out := []workspaceOverviewSource{}
	for _, c := range connections {
		d := &workspaceDeps{repo: r, conns: conns, c: c, factory: f, actorName: user.GetDisplayName()}
		if tenantID != "" {
			t, err := d.tenant(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			out = append(out, workspaceOverviewSource{d, t})
			continue
		}
		tenants, err := r.List(ctx, user.GetId(), c.ID)
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
		for _, t := range tenants {
			out = append(out, workspaceOverviewSource{d, t})
		}
	}
	if len(out) > workspaceOverviewTenantLimit {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("overview reads at most 20 tenants at once; select a Service Account or one tenant"))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.d.c.ID != b.d.c.ID {
			return a.d.c.ID < b.d.c.ID
		}
		return a.t.ID < b.t.ID
	})
	return out, nil
}
func scanWorkspaceUsers(ctx context.Context, api workspaceAPI) ([]googleworkspace.User, bool, error) {
	out := []googleworkspace.User{}
	token := ""
	seenTokens := map[string]bool{}
	seenUsers := map[string]bool{}
	for {
		page, err := api.ListUsers(ctx, "", token, 500)
		if err != nil {
			return out, false, err
		}
		for index, u := range page.Users {
			if u.ID == "" {
				return out, false, errors.New("Google did not return an immutable user ID")
			}
			if !seenUsers[u.ID] {
				seenUsers[u.ID] = true
				out = append(out, u)
				if len(out) == workspaceOverviewScanLimit {
					return out, page.NextPageToken == "" && index == len(page.Users)-1, nil
				}
			}
		}
		if page.NextPageToken == "" {
			return out, true, nil
		}
		if seenTokens[page.NextPageToken] {
			return out, false, errors.New("Google repeated a user page token")
		}
		seenTokens[page.NextPageToken] = true
		token = page.NextPageToken
	}
}
func scanWorkspaceGroups(ctx context.Context, api workspaceAPI) (int, bool, error) {
	token := ""
	seen := map[string]bool{}
	ids := map[string]bool{}
	for {
		p, err := api.ListGroups(ctx, "", token, 200)
		if err != nil {
			return len(ids), false, err
		}
		for index, g := range p.Groups {
			if g.ID == "" {
				return len(ids), false, errors.New("Google did not return an immutable group ID")
			}
			ids[g.ID] = true
			if len(ids) == workspaceOverviewScanLimit {
				return len(ids), p.NextPageToken == "" && index == len(p.Groups)-1, nil
			}
		}
		if p.NextPageToken == "" {
			return len(ids), true, nil
		}
		if seen[p.NextPageToken] {
			return len(ids), false, errors.New("Google repeated a group page token")
		}
		seen[p.NextPageToken] = true
		token = p.NextPageToken
	}
}
func scanWorkspaceSource(ctx context.Context, source workspaceOverviewSource, now time.Time, days int, groups bool) workspaceOverviewScan {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, t := source.d, source.t
	summary := &neoboxv1.WorkspaceTenantSummary{ConnectionId: d.c.ID, ConnectionName: d.c.Name, Tenant: workspaceTenantToProto(t)}
	result := workspaceOverviewScan{summary: summary}
	api, err := d.client(ctx, t)
	if err == nil {
		var users []googleworkspace.User
		users, summary.UsersComplete, err = scanWorkspaceUsers(ctx, api)
		for _, u := range users {
			flags := googleworkspace.AssessSecurity(u, now, days)
			result.users = append(result.users, workspaceObservedUser{u, flags, source})
			summary.UserCount++
			if u.Suspended {
				summary.SuspendedCount++
			} else {
				summary.ActiveCount++
			}
			if flags.Admin {
				summary.AdminCount++
			}
			if flags.AdminWithoutTwoStep {
				summary.AdminsWithoutTwoStep++
			}
			if flags.Inactive {
				summary.InactiveCount++
			}
			if flags.NeverLoggedIn {
				summary.NeverLoggedInCount++
			}
			if flags.Unknown {
				summary.SecurityUnknownCount++
			}
		}
	}
	if err != nil {
		summary.UsersError = mapWorkspaceErr(err).Error()
	} else if !summary.UsersComplete {
		summary.UsersError = "Scan limit reached; counts cover only observed users"
	}
	if groups {
		groupAPI, gerr := d.client(ctx, t, googleworkspace.GroupScope)
		if gerr == nil {
			var count int
			count, summary.GroupsComplete, gerr = scanWorkspaceGroups(ctx, groupAPI)
			value := int64(count)
			summary.GroupCount = &value
		}
		if gerr != nil {
			summary.GroupsError = mapWorkspaceFeatureErr(gerr, googleworkspace.GroupScope).Error()
		} else if !summary.GroupsComplete {
			summary.GroupsError = "Scan limit reached; count covers only observed groups"
		}
	}
	return result
}
func overviewUserMatches(u workspaceObservedUser, filter, query string) bool {
	switch filter {
	case "admins":
		if !u.f.Admin {
			return false
		}
	case "admins_without_two_step":
		if !u.f.AdminWithoutTwoStep {
			return false
		}
	case "inactive":
		if !u.f.Inactive {
			return false
		}
	case "never_logged_in":
		if !u.f.NeverLoggedIn {
			return false
		}
	case "suspended":
		if !u.u.Suspended {
			return false
		}
	case "unknown":
		if !u.f.Unknown {
			return false
		}
	}
	if query == "" {
		return true
	}
	text := strings.ToLower(strings.Join([]string{u.u.PrimaryEmail, u.u.Name.GivenName, u.u.Name.FamilyName, u.u.OrgUnitPath, u.source.t.Name, u.source.t.CustomerID, u.source.d.c.Name}, " "))
	return strings.Contains(text, strings.ToLower(query))
}
func (s *GoogleWorkspaceServiceServer) GetWorkspaceOverview(ctx context.Context, req *connect.Request[neoboxv1.GetWorkspaceOverviewRequest]) (*connect.Response[neoboxv1.GetWorkspaceOverviewResponse], error) {
	if !validSecurityFilter(req.Msg.GetSecurityFilter()) {
		return nil, connectx.InvalidArgument("security_filter", "unknown account filter")
	}
	days := int(req.Msg.GetInactivityDays())
	if days == 0 {
		days = 90
	}
	if days < 1 || days > 3650 {
		return nil, connectx.InvalidArgument("inactivity_days", "must be between 1 and 3650")
	}
	sources, err := s.overviewSources(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	now := time.Now().UTC()
	scans := make([]workspaceOverviewScan, len(sources))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, source := range sources {
		wg.Add(1)
		go func(i int, source workspaceOverviewSource) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				scans[i] = workspaceOverviewScan{summary: &neoboxv1.WorkspaceTenantSummary{ConnectionId: source.d.c.ID, ConnectionName: source.d.c.Name, Tenant: workspaceTenantToProto(source.t), UsersError: "Overview read timed out"}}
				return
			}
			scans[i] = scanWorkspaceSource(ctx, source, now, days, req.Msg.GetIncludeGroups())
		}(i, source)
	}
	wg.Wait()
	out := &neoboxv1.GetWorkspaceOverviewResponse{ObservedAt: timestamppb.New(now), UsersComplete: true}

	unique := map[string]workspaceObservedUser{}
	sourceNames := map[string]string{}
	for _, scan := range scans {
		out.Tenants = append(out.Tenants, scan.summary)
		out.UsersComplete = out.UsersComplete && scan.summary.UsersComplete
		for _, u := range scan.users {
			key := u.source.t.CustomerID + "\x00" + u.u.ID
			sourceNames[key] += " " + strings.Join([]string{u.source.t.Name, u.source.t.CustomerID, u.source.d.c.Name}, " ")
			if previous, ok := unique[key]; !ok || securityDataScore(u.u) > securityDataScore(previous.u) {
				unique[key] = u
			}
		}
	}
	matches := []workspaceObservedUser{}
	out.Totals = &neoboxv1.WorkspaceSecurityTotals{}
	for _, u := range unique {
		countSecurity(out.Totals, u)
		key := u.source.t.CustomerID + "\x00" + u.u.ID
		query := strings.ToLower(strings.TrimSpace(req.Msg.GetQuery()))
		if overviewUserMatches(u, req.Msg.GetSecurityFilter(), "") && (query == "" || overviewUserMatches(u, "all", query) || strings.Contains(strings.ToLower(sourceNames[key]), query)) {
			matches = append(matches, u)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.u.PrimaryEmail != b.u.PrimaryEmail {
			return a.u.PrimaryEmail < b.u.PrimaryEmail
		}
		if a.source.t.ID != b.source.t.ID {
			return a.source.t.ID < b.source.t.ID
		}
		return a.u.ID < b.u.ID
	})
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), 50, 1, 200)
	out.TotalCount, out.Page, out.PageSize = int64(len(matches)), int32(page), int32(size)
	start := min((page-1)*size, len(matches))
	end := min(start+size, len(matches))
	for _, u := range matches[start:end] {
		out.Users = append(out.Users, &neoboxv1.WorkspaceOverviewUser{ConnectionId: u.source.d.c.ID, ConnectionName: u.source.d.c.Name, TenantId: u.source.t.ID, TenantName: u.source.t.Name, User: workspaceUserToProto(&u.u), Inactive: u.f.Inactive, NeverLoggedIn: u.f.NeverLoggedIn, AdminWithoutTwoStep: u.f.AdminWithoutTwoStep, SecurityUnknown: u.f.Unknown})
	}
	return connect.NewResponse(out), nil
}

func securityDataScore(u googleworkspace.User) int {
	score := 0
	for _, v := range []*bool{u.IsAdmin, u.IsDelegatedAdmin, u.TwoStepEnrolled, u.TwoStepEnforced} {
		if v != nil {
			score++
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, u.LastLoginTime); err == nil {
		score++
	}
	return score
}
func countSecurity(t *neoboxv1.WorkspaceSecurityTotals, u workspaceObservedUser) {
	t.UserCount++
	if u.u.Suspended {
		t.SuspendedCount++
	} else {
		t.ActiveCount++
	}
	if u.f.Admin {
		t.AdminCount++
	}
	if u.f.AdminWithoutTwoStep {
		t.AdminsWithoutTwoStep++
	}
	if u.f.Inactive {
		t.InactiveCount++
	}
	if u.f.NeverLoggedIn {
		t.NeverLoggedInCount++
	}
	if u.f.Unknown {
		t.SecurityUnknownCount++
	}
}

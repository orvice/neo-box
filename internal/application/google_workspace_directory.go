package application

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// Optional capability failures do not change the tenant's base user health.
// Request only that capability's scope, retaining the original user scopes.
func mapWorkspaceFeatureErr(err error, scope string) error {
	if errors.Is(err, googleworkspace.ErrDelegation) && scope != "" {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("Google rejected delegation for this feature; check the tenant's authorization for "+scope+" and the delegated administrator's privileges"))
	}
	return mapWorkspaceErr(err)
}
func workspaceListSize(size int32) (int, error) {
	if size == 0 {
		return 50, nil
	}
	if size < 1 || size > 200 {
		return 0, connectx.InvalidArgument("page_size", "must be between 1 and 200")
	}
	return int(size), nil
}
func aliasToProto(a *googleworkspace.Alias) *neoboxv1.WorkspaceAlias {
	return &neoboxv1.WorkspaceAlias{Email: a.Email, Editable: a.Editable}
}
func groupToProto(g *googleworkspace.Group) *neoboxv1.WorkspaceGroup {
	return &neoboxv1.WorkspaceGroup{Id: g.ID, Email: g.Email, Name: g.Name, Description: g.Description, DirectMemberCount: g.DirectMembersCount}
}
func memberToProto(m *googleworkspace.Member) *neoboxv1.WorkspaceGroupMember {
	return &neoboxv1.WorkspaceGroupMember{Id: m.ID, Email: m.Email, Role: m.Role, Type: m.Type, Status: m.Status}
}
func orgUnitToProto(o *googleworkspace.OrgUnit) *neoboxv1.WorkspaceOrgUnit {
	return &neoboxv1.WorkspaceOrgUnit{Id: o.ID, Name: o.Name, Description: o.Description, Path: o.Path, ParentPath: o.ParentPath}
}
func tokenToProto(t *googleworkspace.UserToken) *neoboxv1.WorkspaceUserToken {
	return &neoboxv1.WorkspaceUserToken{ClientId: t.ClientID, DisplayText: t.DisplayText, Scopes: t.Scopes, Anonymous: t.Anonymous, NativeApp: t.NativeApp}
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserAliases(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUserAliasesRequest]) (*connect.Response[neoboxv1.ListWorkspaceUserAliasesResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	result, err := api.ListAliases(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, "")
	}
	out := &neoboxv1.ListWorkspaceUserAliasesResponse{}
	for i := range result {
		out.Aliases = append(out.Aliases, aliasToProto(&result[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) AddWorkspaceUserAlias(ctx context.Context, req *connect.Request[neoboxv1.AddWorkspaceUserAliasRequest]) (*connect.Response[neoboxv1.AddWorkspaceUserAliasResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	result, err := api.AddAlias(ctx, req.Msg.GetUserId(), req.Msg.GetAlias())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, "")
	}
	out := &neoboxv1.AddWorkspaceUserAliasResponse{}
	out.Alias = aliasToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceUserAlias(ctx context.Context, req *connect.Request[neoboxv1.DeleteWorkspaceUserAliasRequest]) (*connect.Response[neoboxv1.DeleteWorkspaceUserAliasResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	err = api.DeleteAlias(ctx, req.Msg.GetUserId(), req.Msg.GetAlias())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, "")
	}
	out := &neoboxv1.DeleteWorkspaceUserAliasResponse{}

	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceGroups(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceGroupsRequest]) (*connect.Response[neoboxv1.ListWorkspaceGroupsResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	size, err := workspaceListSize(req.Msg.GetPageSize())
	if err != nil {
		return nil, err
	}
	result, err := api.ListGroups(ctx, req.Msg.GetQuery(), req.Msg.GetPageToken(), size)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceGroupsResponse{}
	out.NextPageToken = result.NextPageToken
	for i := range result.Groups {
		out.Groups = append(out.Groups, groupToProto(&result.Groups[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserGroups(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUserGroupsRequest]) (*connect.Response[neoboxv1.ListWorkspaceUserGroupsResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListUserGroups(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceUserGroupsResponse{}
	for i := range result {
		out.Groups = append(out.Groups, groupToProto(&result[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) CreateWorkspaceGroup(ctx context.Context, req *connect.Request[neoboxv1.CreateWorkspaceGroupRequest]) (*connect.Response[neoboxv1.CreateWorkspaceGroupResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	in := req.Msg.GetGroup()
	if in == nil {
		return nil, connectx.RequiredArgument("group")
	}
	result, err := api.CreateGroup(ctx, in.GetEmail(), in.GetName(), in.GetDescription())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.CreateWorkspaceGroupResponse{}
	out.Group = groupToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceGroup(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceGroupRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceGroupResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	in := req.Msg.GetGroup()
	if in == nil {
		return nil, connectx.RequiredArgument("group")
	}
	result, err := api.UpdateGroup(ctx, req.Msg.GetGroupId(), in.GetEmail(), in.GetName(), in.GetDescription())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.UpdateWorkspaceGroupResponse{}
	out.Group = groupToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceGroup(ctx context.Context, req *connect.Request[neoboxv1.DeleteWorkspaceGroupRequest]) (*connect.Response[neoboxv1.DeleteWorkspaceGroupResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteGroup(ctx, req.Msg.GetGroupId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.DeleteWorkspaceGroupResponse{}

	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceGroupMembers(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceGroupMembersRequest]) (*connect.Response[neoboxv1.ListWorkspaceGroupMembersResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	size, err := workspaceListSize(req.Msg.GetPageSize())
	if err != nil {
		return nil, err
	}
	result, err := api.ListMembers(ctx, req.Msg.GetGroupId(), req.Msg.GetPageToken(), size)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceGroupMembersResponse{}
	out.NextPageToken = result.NextPageToken
	for i := range result.Members {
		out.Members = append(out.Members, memberToProto(&result.Members[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) AddWorkspaceGroupMember(ctx context.Context, req *connect.Request[neoboxv1.AddWorkspaceGroupMemberRequest]) (*connect.Response[neoboxv1.AddWorkspaceGroupMemberResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.AddMember(ctx, req.Msg.GetGroupId(), req.Msg.GetEmail(), req.Msg.GetRole())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.AddWorkspaceGroupMemberResponse{}
	out.Member = memberToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceGroupMember(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceGroupMemberRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceGroupMemberResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.UpdateMember(ctx, req.Msg.GetGroupId(), req.Msg.GetMemberId(), req.Msg.GetRole())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.UpdateWorkspaceGroupMemberResponse{}
	out.Member = memberToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceGroupMember(ctx context.Context, req *connect.Request[neoboxv1.DeleteWorkspaceGroupMemberRequest]) (*connect.Response[neoboxv1.DeleteWorkspaceGroupMemberResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteMember(ctx, req.Msg.GetGroupId(), req.Msg.GetMemberId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.DeleteWorkspaceGroupMemberResponse{}

	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceOrgUnits(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceOrgUnitsRequest]) (*connect.Response[neoboxv1.ListWorkspaceOrgUnitsResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListOrgUnits(ctx)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.ListWorkspaceOrgUnitsResponse{}
	for i := range result {
		out.OrgUnits = append(out.OrgUnits, orgUnitToProto(&result[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) CreateWorkspaceOrgUnit(ctx context.Context, req *connect.Request[neoboxv1.CreateWorkspaceOrgUnitRequest]) (*connect.Response[neoboxv1.CreateWorkspaceOrgUnitResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}
	in := req.Msg.GetOrgUnit()
	if in == nil {
		return nil, connectx.RequiredArgument("org_unit")
	}
	result, err := api.CreateOrgUnit(ctx, in.GetName(), in.GetDescription(), in.GetParentPath())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.CreateWorkspaceOrgUnitResponse{}
	out.OrgUnit = orgUnitToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceOrgUnit(ctx context.Context, req *connect.Request[neoboxv1.UpdateWorkspaceOrgUnitRequest]) (*connect.Response[neoboxv1.UpdateWorkspaceOrgUnitResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}
	in := req.Msg.GetOrgUnit()
	if in == nil {
		return nil, connectx.RequiredArgument("org_unit")
	}
	result, err := api.UpdateOrgUnit(ctx, req.Msg.GetOrgUnitId(), in.GetName(), in.GetDescription(), in.GetParentPath())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.UpdateWorkspaceOrgUnitResponse{}
	out.OrgUnit = orgUnitToProto(result)
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceOrgUnit(ctx context.Context, req *connect.Request[neoboxv1.DeleteWorkspaceOrgUnitRequest]) (*connect.Response[neoboxv1.DeleteWorkspaceOrgUnitResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteOrgUnit(ctx, req.Msg.GetOrgUnitId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.DeleteWorkspaceOrgUnitResponse{}

	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) SignOutWorkspaceUser(ctx context.Context, req *connect.Request[neoboxv1.SignOutWorkspaceUserRequest]) (*connect.Response[neoboxv1.SignOutWorkspaceUserResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	err = api.SignOutUser(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.SignOutWorkspaceUserResponse{}

	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserTokens(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceUserTokensRequest]) (*connect.Response[neoboxv1.ListWorkspaceUserTokensResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListUserTokens(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.ListWorkspaceUserTokensResponse{}
	for i := range result {
		out.Tokens = append(out.Tokens, tokenToProto(&result[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *GoogleWorkspaceServiceServer) RevokeWorkspaceUserToken(ctx context.Context, req *connect.Request[neoboxv1.RevokeWorkspaceUserTokenRequest]) (*connect.Response[neoboxv1.RevokeWorkspaceUserTokenResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	err = api.RevokeUserToken(ctx, req.Msg.GetUserId(), req.Msg.GetClientId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.RevokeWorkspaceUserTokenResponse{}

	return connect.NewResponse(out), nil
}

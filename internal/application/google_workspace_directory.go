package application

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// Optional capability failures do not change the tenant's base user health.
// Request only that capability's scope, retaining the original user scopes.
func mapWorkspaceFeatureErr(ctx context.Context, err error, scope string) error {
	if errors.Is(err, googleworkspace.ErrDelegation) && scope != "" {
		return connect.NewError(connect.CodeFailedPrecondition, "Google rejected delegation for this feature; check the tenant's authorization for "+scope+" and the delegated administrator's privileges")
	}
	return mapWorkspaceErr(ctx, err)
}
func workspaceListSize(ctx context.Context, size int32) (int, error) {
	if size == 0 {
		return 50, nil
	}
	if size < 1 || size > 200 {
		return 0, connectx.InvalidArgument(ctx, "page_size", "must be between 1 and 200")
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

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserAliases(ctx context.Context, req *neoboxv1.ListWorkspaceUserAliasesRequest) (*neoboxv1.ListWorkspaceUserAliasesResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId())
	if err != nil {
		return nil, err
	}

	result, err := api.ListAliases(ctx, req.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, "")
	}
	out := &neoboxv1.ListWorkspaceUserAliasesResponse{}
	for i := range result {
		out.Aliases = append(out.Aliases, aliasToProto(&result[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) AddWorkspaceUserAlias(ctx context.Context, req *neoboxv1.AddWorkspaceUserAliasRequest) (*neoboxv1.AddWorkspaceUserAliasResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId())
	if err != nil {
		return nil, err
	}

	result, err := api.AddAlias(ctx, req.GetUserId(), req.GetAlias())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, "")
	}
	out := &neoboxv1.AddWorkspaceUserAliasResponse{}
	out.Alias = aliasToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceUserAlias(ctx context.Context, req *neoboxv1.DeleteWorkspaceUserAliasRequest) (*neoboxv1.DeleteWorkspaceUserAliasResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId())
	if err != nil {
		return nil, err
	}

	err = api.DeleteAlias(ctx, req.GetUserId(), req.GetAlias())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, "")
	}
	out := &neoboxv1.DeleteWorkspaceUserAliasResponse{}

	return out, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceGroups(ctx context.Context, req *neoboxv1.ListWorkspaceGroupsRequest) (*neoboxv1.ListWorkspaceGroupsResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	size, err := workspaceListSize(ctx, req.GetPageSize())
	if err != nil {
		return nil, err
	}
	result, err := api.ListGroups(ctx, req.GetQuery(), req.GetPageToken(), size)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceGroupsResponse{}
	out.NextPageToken = result.NextPageToken
	for i := range result.Groups {
		out.Groups = append(out.Groups, groupToProto(&result.Groups[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserGroups(ctx context.Context, req *neoboxv1.ListWorkspaceUserGroupsRequest) (*neoboxv1.ListWorkspaceUserGroupsResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListUserGroups(ctx, req.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceUserGroupsResponse{}
	for i := range result {
		out.Groups = append(out.Groups, groupToProto(&result[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) CreateWorkspaceGroup(ctx context.Context, req *neoboxv1.CreateWorkspaceGroupRequest) (*neoboxv1.CreateWorkspaceGroupResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	in := req.GetGroup()
	if in == nil {
		return nil, connectx.RequiredArgument(ctx, "group")
	}
	result, err := api.CreateGroup(ctx, in.GetEmail(), in.GetName(), in.GetDescription())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.CreateWorkspaceGroupResponse{}
	out.Group = groupToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceGroup(ctx context.Context, req *neoboxv1.UpdateWorkspaceGroupRequest) (*neoboxv1.UpdateWorkspaceGroupResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	in := req.GetGroup()
	if in == nil {
		return nil, connectx.RequiredArgument(ctx, "group")
	}
	result, err := api.UpdateGroup(ctx, req.GetGroupId(), in.GetEmail(), in.GetName(), in.GetDescription())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.UpdateWorkspaceGroupResponse{}
	out.Group = groupToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceGroup(ctx context.Context, req *neoboxv1.DeleteWorkspaceGroupRequest) (*neoboxv1.DeleteWorkspaceGroupResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteGroup(ctx, req.GetGroupId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.DeleteWorkspaceGroupResponse{}

	return out, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceGroupMembers(ctx context.Context, req *neoboxv1.ListWorkspaceGroupMembersRequest) (*neoboxv1.ListWorkspaceGroupMembersResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}
	size, err := workspaceListSize(ctx, req.GetPageSize())
	if err != nil {
		return nil, err
	}
	result, err := api.ListMembers(ctx, req.GetGroupId(), req.GetPageToken(), size)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.ListWorkspaceGroupMembersResponse{}
	out.NextPageToken = result.NextPageToken
	for i := range result.Members {
		out.Members = append(out.Members, memberToProto(&result.Members[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) AddWorkspaceGroupMember(ctx context.Context, req *neoboxv1.AddWorkspaceGroupMemberRequest) (*neoboxv1.AddWorkspaceGroupMemberResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.AddMember(ctx, req.GetGroupId(), req.GetEmail(), req.GetRole())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.AddWorkspaceGroupMemberResponse{}
	out.Member = memberToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceGroupMember(ctx context.Context, req *neoboxv1.UpdateWorkspaceGroupMemberRequest) (*neoboxv1.UpdateWorkspaceGroupMemberResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	result, err := api.UpdateMember(ctx, req.GetGroupId(), req.GetMemberId(), req.GetRole())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.UpdateWorkspaceGroupMemberResponse{}
	out.Member = memberToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceGroupMember(ctx context.Context, req *neoboxv1.DeleteWorkspaceGroupMemberRequest) (*neoboxv1.DeleteWorkspaceGroupMemberResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.GroupScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteMember(ctx, req.GetGroupId(), req.GetMemberId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.GroupScope)
	}
	out := &neoboxv1.DeleteWorkspaceGroupMemberResponse{}

	return out, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceOrgUnits(ctx context.Context, req *neoboxv1.ListWorkspaceOrgUnitsRequest) (*neoboxv1.ListWorkspaceOrgUnitsResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListOrgUnits(ctx)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.ListWorkspaceOrgUnitsResponse{}
	for i := range result {
		out.OrgUnits = append(out.OrgUnits, orgUnitToProto(&result[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) CreateWorkspaceOrgUnit(ctx context.Context, req *neoboxv1.CreateWorkspaceOrgUnitRequest) (*neoboxv1.CreateWorkspaceOrgUnitResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}
	in := req.GetOrgUnit()
	if in == nil {
		return nil, connectx.RequiredArgument(ctx, "org_unit")
	}
	result, err := api.CreateOrgUnit(ctx, in.GetName(), in.GetDescription(), in.GetParentPath())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.CreateWorkspaceOrgUnitResponse{}
	out.OrgUnit = orgUnitToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) UpdateWorkspaceOrgUnit(ctx context.Context, req *neoboxv1.UpdateWorkspaceOrgUnitRequest) (*neoboxv1.UpdateWorkspaceOrgUnitResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}
	in := req.GetOrgUnit()
	if in == nil {
		return nil, connectx.RequiredArgument(ctx, "org_unit")
	}
	result, err := api.UpdateOrgUnit(ctx, req.GetOrgUnitId(), in.GetName(), in.GetDescription(), in.GetParentPath())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.UpdateWorkspaceOrgUnitResponse{}
	out.OrgUnit = orgUnitToProto(result)
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) DeleteWorkspaceOrgUnit(ctx context.Context, req *neoboxv1.DeleteWorkspaceOrgUnitRequest) (*neoboxv1.DeleteWorkspaceOrgUnitResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.OrgUnitScope)
	if err != nil {
		return nil, err
	}

	err = api.DeleteOrgUnit(ctx, req.GetOrgUnitId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.OrgUnitScope)
	}
	out := &neoboxv1.DeleteWorkspaceOrgUnitResponse{}

	return out, nil
}

func (s *GoogleWorkspaceServiceServer) SignOutWorkspaceUser(ctx context.Context, req *neoboxv1.SignOutWorkspaceUserRequest) (*neoboxv1.SignOutWorkspaceUserResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	err = api.SignOutUser(ctx, req.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.SignOutWorkspaceUserResponse{}

	return out, nil
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceUserTokens(ctx context.Context, req *neoboxv1.ListWorkspaceUserTokensRequest) (*neoboxv1.ListWorkspaceUserTokensResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	result, err := api.ListUserTokens(ctx, req.GetUserId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.ListWorkspaceUserTokensResponse{}
	for i := range result {
		out.Tokens = append(out.Tokens, tokenToProto(&result[i]))
	}
	return out, nil
}

func (s *GoogleWorkspaceServiceServer) RevokeWorkspaceUserToken(ctx context.Context, req *neoboxv1.RevokeWorkspaceUserTokenRequest) (*neoboxv1.RevokeWorkspaceUserTokenResponse, error) {
	_, _, api, err := s.open(ctx, req.GetConnectionId(), req.GetTenantId(), googleworkspace.SecurityScope)
	if err != nil {
		return nil, err
	}

	err = api.RevokeUserToken(ctx, req.GetUserId(), req.GetClientId())
	if err != nil {
		return nil, mapWorkspaceFeatureErr(ctx, err, googleworkspace.SecurityScope)
	}
	out := &neoboxv1.RevokeWorkspaceUserTokenResponse{}

	return out, nil
}

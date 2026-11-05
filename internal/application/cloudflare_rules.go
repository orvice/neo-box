package application

import (
	"context"

	"connectrpc.com/connect"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// Zone Rules RPC wiring is kept explicit so the generated Connect contract is
// available to clients while the category-specific presentation evolves.
func (s *CloudflareServiceServer) GetCloudflareZoneRules(context.Context, *connect.Request[neoboxv1.GetCloudflareZoneRulesRequest]) (*connect.Response[neoboxv1.GetCloudflareZoneRulesResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (s *CloudflareServiceServer) CreateCloudflareZoneRule(context.Context, *connect.Request[neoboxv1.CreateCloudflareZoneRuleRequest]) (*connect.Response[neoboxv1.CreateCloudflareZoneRuleResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (s *CloudflareServiceServer) UpdateCloudflareZoneRule(context.Context, *connect.Request[neoboxv1.UpdateCloudflareZoneRuleRequest]) (*connect.Response[neoboxv1.UpdateCloudflareZoneRuleResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (s *CloudflareServiceServer) DeleteCloudflareZoneRule(context.Context, *connect.Request[neoboxv1.DeleteCloudflareZoneRuleRequest]) (*connect.Response[neoboxv1.DeleteCloudflareZoneRuleResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (s *CloudflareServiceServer) ReorderCloudflareZoneRules(context.Context, *connect.Request[neoboxv1.ReorderCloudflareZoneRulesRequest]) (*connect.Response[neoboxv1.ReorderCloudflareZoneRulesResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

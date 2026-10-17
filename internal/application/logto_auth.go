package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"go.orx.me/apps/neo-box/internal/repo/auth"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

// LogtoAuthServiceServer keeps the Me contract for business clients. Password,
// OAuth, local session and user-management RPCs are deliberately unimplemented.
type LogtoAuthServiceServer struct {
	neoboxv1connect.UnimplementedAuthServiceHandler
}

func (s *LogtoAuthServiceServer) Me(ctx context.Context, _ *connect.Request[neoboxv1.MeRequest]) (*connect.Response[neoboxv1.MeResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	return connect.NewResponse(&neoboxv1.MeResponse{User: user}), nil
}

package application

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"time"
)

type workspaceRecorder struct {
	repo       repo.Repository
	connection *connrepo.Connection
	tenant     *repo.Tenant
	actorName  string
}

func (r workspaceRecorder) Begin(ctx context.Context, w googleworkspace.Write) (func(context.Context, map[string]any, error) error, error) {
	target := w.Method + " " + w.Path
	for _, key := range []string{"primaryEmail", "email", "alias"} {
		if label, ok := w.Requested[key].(string); ok {
			target += " · " + label
			break
		}
	}
	o := &repo.Operation{ID: uuid.NewString(), UserID: r.connection.UserID, ConnectionID: r.connection.ID, TenantID: r.tenant.ID, TenantName: r.tenant.Name, CustomerID: r.tenant.CustomerID, ActorID: r.connection.UserID, ActorName: r.actorName, Action: googleworkspace.WriteAction(w), Target: target, Requested: w.Requested, Status: "pending", CreatedAt: time.Now().UTC()}
	if err := r.repo.CreateOperation(ctx, o); err != nil {
		return nil, googleworkspace.ErrLogUnavailable
	}
	return func(ctx context.Context, result map[string]any, writeErr error) error {
		o.Status, o.Error, o.Result, o.FinishedAt = "succeeded", "", result, time.Now().UTC()
		if writeErr != nil {
			o.Status, o.Result = "failed", nil
			var api *googleworkspace.APIError
			switch {
			case errors.Is(writeErr, googleworkspace.ErrUncertain):
				o.Status = "unknown"
				o.Error = "Google did not confirm the change; refresh the resource before retrying"
			case errors.Is(writeErr, googleworkspace.ErrDelegation):
				o.Error = "Google delegation was rejected before the change was sent"
			case errors.As(writeErr, &api):
				o.Error = fmt.Sprintf("Google rejected the change (HTTP %d)", api.Status)
			default:
				o.Error = "The local configuration change failed"
			}
		}
		return r.repo.FinishOperation(ctx, o)
	}, nil
}
func (d *workspaceDeps) localMutation(ctx context.Context, t *repo.Tenant, action string, fields map[string]any, run func() error) error {
	recorder := workspaceRecorder{repo: d.repo, connection: d.c, tenant: t, actorName: d.actorName}
	finish, err := recorder.Begin(ctx, googleworkspace.Write{Method: "LOCAL", Action: action, Path: "/tenants/" + t.ID, Requested: fields})
	if err != nil {
		return err
	}
	result := run()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = finish(finishCtx, fields, result); err != nil {
		return googleworkspace.ErrOutcomeLogUnavailable
	}
	return result
}
func workspaceOpStatus(raw string) neoboxv1.WorkspaceOperationStatus {
	switch raw {
	case "pending":
		return neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_PENDING
	case "succeeded":
		return neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_SUCCEEDED
	case "failed":
		return neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_FAILED
	case "unknown":
		return neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_UNKNOWN
	}
	return neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_UNSPECIFIED
}
func workspaceOpStatusKey(status neoboxv1.WorkspaceOperationStatus) (string, bool) {
	switch status {
	case neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_UNSPECIFIED:
		return "", true
	case neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_PENDING:
		return "pending", true
	case neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_SUCCEEDED:
		return "succeeded", true
	case neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_FAILED:
		return "failed", true
	case neoboxv1.WorkspaceOperationStatus_WORKSPACE_OPERATION_STATUS_UNKNOWN:
		return "unknown", true
	}
	return "", false
}
func operationToWorkspaceProto(o *repo.Operation) *neoboxv1.WorkspaceOperation {
	out := &neoboxv1.WorkspaceOperation{Id: o.ID, ConnectionId: o.ConnectionID, TenantId: o.TenantID, TenantName: o.TenantName, CustomerId: o.CustomerID, Action: o.Action, Target: o.Target, Status: workspaceOpStatus(o.Status), Error: o.Error, ActorId: o.ActorID, ActorName: o.ActorName, CreatedAt: timestamppb.New(o.CreatedAt)}
	if !o.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(o.FinishedAt)
	}
	if o.Requested != nil {
		out.Requested, _ = structpb.NewStruct(o.Requested)
	}
	if o.Result != nil {
		out.Result, _ = structpb.NewStruct(o.Result)
	}
	return out
}
func (s *GoogleWorkspaceServiceServer) ListWorkspaceOperations(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceOperationsRequest]) (*connect.Response[neoboxv1.ListWorkspaceOperationsResponse], error) {
	d, err := s.deps(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	status, ok := workspaceOpStatusKey(req.Msg.GetStatus())
	if !ok {
		return nil, connectx.InvalidArgument("status", "unknown operation status")
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), 20, 1, 100)
	// A deleted tenant has no configuration to load but its history stays scoped
	// by the owning connection and user.
	ops, total, err := d.repo.ListOperations(ctx, repo.OperationFilter{UserID: d.c.UserID, ConnectionID: d.c.ID, TenantID: req.Msg.GetTenantId(), Query: strings.TrimSpace(req.Msg.GetQuery()), Status: status, Offset: (page - 1) * size, Limit: size})
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := &neoboxv1.ListWorkspaceOperationsResponse{TotalCount: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, o := range ops {
		out.Operations = append(out.Operations, operationToWorkspaceProto(o))
	}
	return connect.NewResponse(out), nil
}

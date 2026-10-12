package application

import (
	"connectrpc.com/connect"
	"context"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"time"
)

func workspaceAuditQuery(in *neoboxv1.ListWorkspaceAuditActivitiesRequest, now time.Time) (googleworkspace.AuditQuery, error) {
	q := googleworkspace.AuditQuery{Application: in.GetApplication(), UserKey: strings.TrimSpace(in.GetUserKey()), EventName: strings.TrimSpace(in.GetEventName()), PageToken: in.GetPageToken(), PageSize: int(in.GetPageSize()), Start: now.Add(-7 * 24 * time.Hour), End: now}
	if q.Application == "" {
		q.Application = "login"
	}
	if !googleworkspace.ValidAuditApplication(q.Application) {
		return q, connectx.InvalidArgument("application", "unsupported audit application")
	}
	if q.PageSize == 0 {
		q.PageSize = 50
	}
	if q.PageSize < 1 || q.PageSize > 1000 {
		return q, connectx.InvalidArgument("page_size", "must be between 1 and 1000")
	}
	for _, item := range []struct {
		field, raw string
		dest       *time.Time
	}{{"start_time", in.GetStartTime(), &q.Start}, {"end_time", in.GetEndTime(), &q.End}} {
		if item.raw != "" {
			t, err := time.Parse(time.RFC3339, item.raw)
			if err != nil {
				return q, connectx.InvalidArgument(item.field, "must be an RFC3339 timestamp")
			}
			*item.dest = t
		}
	}
	if !q.Start.Before(q.End) || q.End.After(now.Add(time.Minute)) || q.Start.Before(now.Add(-180*24*time.Hour)) {
		return q, connectx.InvalidArgument("start_time", "use a past time range within the last 180 days, with start before end")
	}
	return q, nil
}
func (s *GoogleWorkspaceServiceServer) ListWorkspaceAuditActivities(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceAuditActivitiesRequest]) (*connect.Response[neoboxv1.ListWorkspaceAuditActivitiesResponse], error) {
	_, _, api, err := s.open(ctx, req.Msg.GetConnectionId(), req.Msg.GetTenantId(), googleworkspace.AuditScope)
	if err != nil {
		return nil, err
	}
	q, err := workspaceAuditQuery(req.Msg, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	page, err := api.ListAuditActivities(ctx, q)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.AuditScope)
	}
	out := &neoboxv1.ListWorkspaceAuditActivitiesResponse{NextPageToken: page.NextPageToken, StartTime: q.Start.UTC().Format(time.RFC3339), EndTime: q.End.UTC().Format(time.RFC3339)}
	for _, a := range page.Items {
		at, _ := googleworkspace.AuditTime(a.ID.Time)
		row := &neoboxv1.WorkspaceAuditActivity{Id: a.ID.Time + ":" + a.ID.Qualifier, OccurredAt: timestamppb.New(at), Application: a.ID.Application, ActorEmail: a.Actor.Email, ActorProfileId: a.Actor.ProfileID, IpAddress: a.IPAddress}
		for _, event := range a.Events {
			params, _ := structpb.NewStruct(googleworkspace.AuditParameters(event.Parameters))
			row.Events = append(row.Events, &neoboxv1.WorkspaceAuditEvent{Name: event.Name, Type: event.Type, Parameters: params})
		}
		out.Activities = append(out.Activities, row)
	}
	return connect.NewResponse(out), nil
}

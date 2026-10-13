package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// driveUsageTimeout bounds one usage read, like the overview's per-tenant limit.
const driveUsageTimeout = 20 * time.Second

func (s *GoogleWorkspaceServiceServer) ListWorkspaceDriveActivities(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceDriveActivitiesRequest]) (*connect.Response[neoboxv1.ListWorkspaceDriveActivitiesResponse], error) {
	in := req.Msg
	preset, ok := googleworkspace.DrivePresetByKey(strings.TrimSpace(in.GetPreset()))
	if !ok {
		return nil, connectx.InvalidArgument("preset", "unknown Drive activity preset")
	}
	// The time range, user and page rules are the Google audit page's.
	q, err := workspaceAuditQuery(&neoboxv1.ListWorkspaceAuditActivitiesRequest{Application: "drive", UserKey: in.GetUserKey(), StartTime: in.GetStartTime(), EndTime: in.GetEndTime(), PageToken: in.GetPageToken(), PageSize: in.GetPageSize()}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	q.EventName, q.Filters = preset.Event, preset.Filters
	_, _, api, err := s.open(ctx, in.GetConnectionId(), in.GetTenantId(), googleworkspace.AuditScope)
	if err != nil {
		return nil, err
	}
	page, err := api.ListAuditActivities(ctx, q)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.AuditScope)
	}
	out := &neoboxv1.ListWorkspaceDriveActivitiesResponse{NextPageToken: page.NextPageToken, StartTime: q.Start.UTC().Format(time.RFC3339), EndTime: q.End.UTC().Format(time.RFC3339)}
	for _, a := range page.Items {
		at, _ := googleworkspace.AuditTime(a.ID.Time)
		row := &neoboxv1.WorkspaceDriveActivity{Id: a.ID.Time + ":" + a.ID.Qualifier, OccurredAt: timestamppb.New(at), ActorEmail: a.Actor.Email, ActorProfileId: a.Actor.ProfileID, IpAddress: a.IPAddress}
		for _, e := range a.Events {
			ev := googleworkspace.ParseDriveEvent(e)
			if !in.GetIncludeSideEffects() && !ev.IsPrimary() {
				out.HiddenEventCount++
				continue
			}
			row.Events = append(row.Events, driveEventToProto(ev))
		}
		if len(row.Events) > 0 {
			out.Activities = append(out.Activities, row)
		}
	}
	return connect.NewResponse(out), nil
}

func driveEventToProto(e googleworkspace.DriveEvent) *neoboxv1.WorkspaceDriveEvent {
	params, _ := structpb.NewStruct(e.Parameters)
	return &neoboxv1.WorkspaceDriveEvent{
		Name: e.Name, DocId: e.DocID, DocTitle: e.DocTitle, DocType: e.DocType, Owner: e.Owner, OwnerIsSharedDrive: e.OwnerIsSharedDrive,
		SharedDriveId: e.SharedDriveID, Primary: e.Primary, Visibility: e.Visibility, OldVisibility: e.OldVisibility, VisibilityChange: e.VisibilityChange,
		TargetUser: e.TargetUser, TargetDomain: e.TargetDomain, Target: e.Target, OldRole: e.OldRole, NewRole: e.NewRole, NewOwner: e.NewOwner,
		MembershipChangeType: e.MembershipChange, AddedRole: e.AddedRole, RemovedRole: e.RemovedRole, Parameters: params,
	}
}

func (s *GoogleWorkspaceServiceServer) ListWorkspaceDriveUsage(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceDriveUsageRequest]) (*connect.Response[neoboxv1.ListWorkspaceDriveUsageResponse], error) {
	in := req.Msg
	sortBy := strings.TrimSpace(in.GetSort())
	if sortBy != "" && sortBy != "used" && sortBy != "email" {
		return nil, connectx.InvalidArgument("sort", "must be used or email")
	}
	_, _, api, err := s.open(ctx, in.GetConnectionId(), in.GetTenantId(), googleworkspace.UsageScope)
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(in.GetPage(), in.GetPageSize(), 50, 1, 200)
	readCtx, cancel := context.WithTimeout(ctx, driveUsageTimeout)
	defer cancel()
	usage, err := api.ListDriveUsage(readCtx, time.Now().UTC())
	if errors.Is(err, googleworkspace.ErrNoUsageData) {
		return connect.NewResponse(&neoboxv1.ListWorkspaceDriveUsageResponse{Page: int32(page), PageSize: int32(size)}), nil
	}
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.UsageScope)
	}
	out := &neoboxv1.ListWorkspaceDriveUsageResponse{HasData: true, Date: usage.Date, Page: int32(page), PageSize: int32(size), UsersRead: int64(len(usage.Users)), Partial: usage.Partial, PartialReason: usage.PartialReason}
	for _, u := range usage.Users {
		if u.UsedMB == nil {
			out.UnknownUsedCount++
		} else {
			out.TotalUsedMb += *u.UsedMB
		}
	}
	ranked := googleworkspace.RankDriveUsage(usage.Users, in.GetQuery(), sortBy)
	out.TotalCount = int64(len(ranked))
	start := (page - 1) * size
	for i := start; i < len(ranked) && i < start+size; i++ {
		u := ranked[i]
		out.Users = append(out.Users, &neoboxv1.WorkspaceDriveUsageUser{Email: u.Email, UsedMb: u.UsedMB, TotalMb: u.TotalMB, SharePercent: u.SharePercent()})
	}
	return connect.NewResponse(out), nil
}

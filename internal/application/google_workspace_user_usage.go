package application

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	defaultUserUsageDays = 14
	maxUserUsageDays     = 30
	// userUsageTimeout bounds the day reads of one user.
	userUsageTimeout = 25 * time.Second
)

// GetWorkspaceUserUsage reads one user's storage and Gmail counts per day from
// Google's usage reports. These are aggregates; the mailbox itself is never
// opened, so no access reason is asked for and nothing is logged.
func (s *GoogleWorkspaceServiceServer) GetWorkspaceUserUsage(ctx context.Context, req *connect.Request[neoboxv1.GetWorkspaceUserUsageRequest]) (*connect.Response[neoboxv1.GetWorkspaceUserUsageResponse], error) {
	in := req.Msg
	if strings.TrimSpace(in.GetUserId()) == "" {
		return nil, connectx.RequiredArgument("user_id")
	}
	days := int(in.GetDays())
	if days == 0 {
		days = defaultUserUsageDays
	}
	if days < 1 || days > maxUserUsageDays {
		return nil, connectx.InvalidArgument("days", "must be between 1 and 30")
	}
	_, _, api, err := s.open(ctx, in.GetConnectionId(), in.GetTenantId(), googleworkspace.UsageScope)
	if err != nil {
		return nil, err
	}
	// Resolving the user first keeps the read inside this tenant: GetUser
	// refuses another customer's user, and an alias or ID becomes the primary
	// email the report is keyed by.
	u, err := api.GetUser(ctx, strings.TrimSpace(in.GetUserId()))
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, userUsageTimeout)
	defer cancel()
	usage, err := api.ReadUserUsage(readCtx, u.PrimaryEmail, time.Now().UTC(), days)
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.UsageScope)
	}
	out := &neoboxv1.GetWorkspaceUserUsageResponse{UserEmail: u.PrimaryEmail, DaysRequested: int32(days)}
	for _, d := range usage.Days {
		out.Days = append(out.Days, &neoboxv1.WorkspaceUserUsageDay{
			Date: d.Date, GmailUsedMb: d.GmailMB, DriveUsedMb: d.DriveMB, PhotosUsedMb: d.PhotosMB,
			UsedMb: d.UsedMB, TotalMb: d.TotalMB,
			EmailsReceived: d.EmailsReceived, EmailsSent: d.EmailsSent, SpamReceived: d.SpamRecvd,
		})
	}
	return connect.NewResponse(out), nil
}

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
)

const (
	defaultStorageThreshold = 80
	// storageReadTimeout bounds each of the two reads (usage report, Directory).
	storageReadTimeout = 25 * time.Second
)

// ListWorkspaceStorageUsage shows every user's storage against the quota Google
// reports. Read-only: it does not set limits (Google has no API for that), and
// reads aggregate reports and the Directory only, so nothing is logged.
func (s *GoogleWorkspaceServiceServer) ListWorkspaceStorageUsage(ctx context.Context, req *connect.Request[neoboxv1.ListWorkspaceStorageUsageRequest]) (*connect.Response[neoboxv1.ListWorkspaceStorageUsageResponse], error) {
	in := req.Msg
	sortBy := strings.TrimSpace(in.GetSort())
	switch sortBy {
	case "", "used", "percent", "email":
	default:
		return nil, connectx.InvalidArgument("sort", "must be used, percent or email")
	}
	threshold := int(in.GetThresholdPercent())
	if threshold == 0 {
		threshold = defaultStorageThreshold
	}
	if threshold < 1 || threshold > 100 {
		return nil, connectx.InvalidArgument("threshold_percent", "must be between 1 and 100")
	}
	d, t, api, err := s.open(ctx, in.GetConnectionId(), in.GetTenantId(), googleworkspace.UsageScope)
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(in.GetPage(), in.GetPageSize(), 50, 1, 200)

	usageCtx, cancel := context.WithTimeout(ctx, storageReadTimeout)
	defer cancel()
	usage, err := api.ListStorageUsage(usageCtx, time.Now().UTC())
	if errors.Is(err, googleworkspace.ErrNoUsageData) {
		return connect.NewResponse(&neoboxv1.ListWorkspaceStorageUsageResponse{Page: int32(page), PageSize: int32(size), ThresholdPercent: int32(threshold)}), nil
	}
	if err != nil {
		return nil, mapWorkspaceFeatureErr(err, googleworkspace.UsageScope)
	}

	// Units come from the Directory. If that read fails the storage figures are
	// still right, so show them and say the units are missing.
	dirCtx, cancelDir := context.WithTimeout(ctx, storageReadTimeout)
	defer cancelDir()
	directory, complete, dirErr := scanWorkspaceUsers(dirCtx, api)
	workspaceReadStatus(ctx, d, t, dirErr)

	rows := googleworkspace.JoinStorage(usage.Users, directory)
	th := float64(threshold)
	out := &neoboxv1.ListWorkspaceStorageUsageResponse{
		HasData: true, Date: usage.Date, Page: int32(page), PageSize: int32(size),
		ThresholdPercent: int32(threshold), UsersRead: int64(len(rows)),
		Partial: usage.Partial, PartialReason: usage.PartialReason,
	}
	switch {
	case dirErr != nil:
		out.Partial, out.PartialReason = true, joinReasons(out.PartialReason, "organizational units could not be read from the Directory")
	case !complete:
		out.Partial, out.PartialReason = true, joinReasons(out.PartialReason, "the Directory read stopped early, so some users have no organizational unit")
	}
	for _, r := range rows {
		if used := r.Used(); used == nil {
			out.UnknownUsedCount++
		} else {
			out.TotalUsedMb += *used
		}
		switch r.StateAt(th) {
		case googleworkspace.StorageNoLimit:
			out.NoLimitCount++
		case googleworkspace.StorageNear:
			out.NearLimitCount++
		case googleworkspace.StorageOver:
			out.OverLimitCount++
		}
	}
	for _, u := range googleworkspace.SummarizeOrgUnits(rows, th) {
		out.OrgUnits = append(out.OrgUnits, &neoboxv1.WorkspaceStorageOrgUnit{
			Path: u.Path, UserCount: u.Users, UsedMb: u.UsedMB, UnknownCount: u.Unknown, LargestUsedMb: u.LargestMB,
			QuotaMb: u.QuotaMB, QuotaMixed: u.QuotaMixed, NearLimitCount: u.NearCount, OverLimitCount: u.OverCount,
		})
	}

	shown := googleworkspace.FilterStorage(rows, googleworkspace.StorageFilter{
		Query: in.GetQuery(), OrgUnit: strings.TrimSpace(in.GetOrgUnitPath()), OnlyNear: in.GetOnlyNearLimit(), Threshold: th,
	})
	googleworkspace.SortStorage(shown, sortBy)
	out.TotalCount = int64(len(shown))
	for i := (page - 1) * size; i < len(shown) && i < page*size; i++ {
		r := shown[i]
		out.Users = append(out.Users, &neoboxv1.WorkspaceStorageUser{
			Email: r.Email, Name: r.Name, OrgUnitPath: r.OrgUnit,
			GmailMb: r.GmailMB, DriveMb: r.DriveMB, PhotosMb: r.PhotosMB, UsedMb: r.Used(), QuotaMb: r.Quota(), UsedPercent: r.Percent(),
		})
	}
	return connect.NewResponse(out), nil
}

func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

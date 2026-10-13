package googleworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DrivePreset is a risk-oriented view of Drive audit events. Each preset is one
// Google event name plus an optional event-parameter filter, so Google does the
// filtering and every page of results matches.
type DrivePreset struct {
	Key, Event, Filters string
}

// Event names and parameters are from Google's Drive audit activity events
// reference (checked 2026-10). Every one of these events carries `visibility`;
// the sharing events also carry `visibility_change`.
var DrivePresets = []DrivePreset{
	{Key: "all"},
	{Key: "public_web", Event: "change_document_visibility", Filters: "visibility==public_on_the_web"},
	{Key: "link_sharing", Event: "change_document_visibility", Filters: "visibility==people_with_link"},
	{Key: "external_share", Event: "change_user_access", Filters: "visibility==shared_externally"},
	{Key: "ownership", Event: "change_owner"},
	{Key: "downloads", Event: "download"},
	{Key: "deletions", Event: "delete"},
	{Key: "trash", Event: "trash"},
	{Key: "shared_drive_members", Event: "shared_drive_membership_change"},
	{Key: "shared_drive_settings", Event: "shared_drive_settings_change"},
}

// DrivePresetByKey returns the preset; an empty key means all events.
func DrivePresetByKey(key string) (DrivePreset, bool) {
	if key == "" {
		key = "all"
	}
	for _, p := range DrivePresets {
		if p.Key == key {
			return p, true
		}
	}
	return DrivePreset{}, false
}

// DriveEvent is one Drive audit event with the fields governance needs. A field
// Google did not report stays empty; nothing is inferred.
type DriveEvent struct {
	Name                                     string
	DocID, DocTitle, DocType                 string
	Owner                                    string
	OwnerIsSharedDrive                       bool
	SharedDriveID                            string
	Primary                                  *bool
	Visibility, OldVisibility                string
	VisibilityChange                         string
	TargetUser, TargetDomain, Target         string
	OldRole, NewRole                         string
	NewOwner                                 string
	MembershipChange, AddedRole, RemovedRole string
	Parameters                               map[string]any
}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case []any:
		// Multi-valued parameters (several targets) are joined for display.
		parts := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

// ParseDriveEvent reads the documented Drive event parameters. Parameters are
// first passed through the audit sanitizer, so credential-like names are
// already redacted.
func ParseDriveEvent(e AuditEvent) DriveEvent {
	p := AuditParameters(e.Parameters)
	out := DriveEvent{
		Name:  e.Name,
		DocID: str(p, "doc_id"), DocTitle: str(p, "doc_title"), DocType: str(p, "doc_type"),
		Owner: str(p, "owner"), SharedDriveID: str(p, "shared_drive_id"),
		Visibility: str(p, "visibility"), OldVisibility: str(p, "old_visibility"), VisibilityChange: str(p, "visibility_change"),
		TargetUser: str(p, "target_user"), TargetDomain: str(p, "target_domain"), Target: str(p, "target"),
		OldRole: str(p, "old_value"), NewRole: str(p, "new_value"),
		NewOwner:         str(p, "new_owner"),
		MembershipChange: str(p, "membership_change_type"), AddedRole: str(p, "added_role"), RemovedRole: str(p, "removed_role"),
		Parameters: p,
	}
	if b, ok := p["owner_is_shared_drive"].(bool); ok {
		out.OwnerIsSharedDrive = b
	}
	if b, ok := p["primary_event"].(bool); ok {
		out.Primary = &b
	}
	if out.SharedDriveID == "" {
		out.SharedDriveID = str(p, "owner_shared_drive_id")
	}
	// Role changes on non-sharing events reuse old_value/new_value for titles.
	if e.Name != "change_user_access" && e.Name != "change_document_visibility" && e.Name != "change_document_access_scope" && e.Name != "change_acl_editors" {
		out.OldRole, out.NewRole = "", ""
	}
	return out
}

// IsPrimary reports whether an event is a user's direct action. A missing flag
// counts as primary: hiding an event Google didn't classify would hide risk.
func (e DriveEvent) IsPrimary() bool { return e.Primary == nil || *e.Primary }

// Exposure is how widely a file is visible after an event.
type Exposure string

const (
	ExposurePublic   Exposure = "public"   // anyone on the web
	ExposureLink     Exposure = "link"     // anyone with the link
	ExposureExternal Exposure = "external" // explicit users or groups outside the domain
	ExposureInternal Exposure = "internal" // inside the organization
	ExposurePrivate  Exposure = "private"
	ExposureUnknown  Exposure = "unknown"
)

// ExposureOf maps Google's `visibility` value. Anything unrecognized is
// unknown, never private.
func ExposureOf(visibility string) Exposure {
	switch visibility {
	case "public_on_the_web":
		return ExposurePublic
	case "people_with_link":
		return ExposureLink
	case "shared_externally":
		return ExposureExternal
	case "people_within_domain_with_link", "public_in_the_domain", "shared_internally":
		return ExposureInternal
	case "private":
		return ExposurePrivate
	}
	return ExposureUnknown
}

// --- storage usage ---

const (
	paramDriveUsed  = "accounts:drive_used_quota_in_mb"
	paramTotalQuota = "accounts:total_quota_in_mb"
	// MaxUsageUsers bounds one read, as the security overview does.
	MaxUsageUsers = 5000
	// usage reports lag by days; look back this far for the newest day with data.
	usageLookbackDays = 6
)

var ErrNoUsageData = errors.New("Google has no Drive usage report for the last few days")

// DriveUsageUser is one user's Drive storage. A figure Google did not report is
// nil, never zero.
type DriveUsageUser struct {
	Email   string
	UsedMB  *int64
	TotalMB *int64
}

// SharePercent is Drive's share of the user's total quota, or nil when either
// figure is unknown or the quota is unlimited (non-positive).
func (u DriveUsageUser) SharePercent() *float64 {
	if u.UsedMB == nil || u.TotalMB == nil || *u.TotalMB <= 0 {
		return nil
	}
	p := float64(*u.UsedMB) / float64(*u.TotalMB) * 100
	return &p
}

type DriveUsage struct {
	// Date is the report day (YYYY-MM-DD).
	Date    string
	Users   []DriveUsageUser
	Partial bool
	// PartialReason says why; empty when complete.
	PartialReason string
}

type usageResponse struct {
	UsageReports []struct {
		Entity struct {
			CustomerID string `json:"customerId"`
			UserEmail  string `json:"userEmail"`
			Type       string `json:"type"`
		} `json:"entity"`
		Parameters []struct {
			Name     string `json:"name"`
			IntValue string `json:"intValue"`
		} `json:"parameters"`
	} `json:"usageReports"`
	Warnings []struct {
		Code string `json:"code"`
	} `json:"warnings"`
	NextPageToken string `json:"nextPageToken"`
}

// ListDriveUsage reads the newest day with data, newest first, up to
// usageLookbackDays back from `now`. A day Google answers with "not available"
// is skipped; any other failure stops the read.
func (c *Client) ListDriveUsage(ctx context.Context, now time.Time) (*DriveUsage, error) {
	endpoint := c.ReportsEndpoint
	if endpoint == "" {
		endpoint = reportsURL
	}
	for back := 1; back <= usageLookbackDays; back++ {
		day := now.UTC().AddDate(0, 0, -back).Format("2006-01-02")
		out, err := c.readUsageDay(ctx, endpoint, day)
		var api *APIError
		switch {
		case err == nil && out != nil:
			return out, nil
		case err == nil:
			continue // no data that day
		case errors.As(err, &api) && api.Status == http.StatusBadRequest:
			continue // Google: data for this date is not available yet
		default:
			return nil, err
		}
	}
	return nil, ErrNoUsageData
}

func (c *Client) readUsageDay(ctx context.Context, endpoint, day string) (*DriveUsage, error) {
	out := &DriveUsage{Date: day}
	token := ""
	for {
		values := url.Values{"customerId": {c.CustomerID}, "maxResults": {"1000"}, "parameters": {paramDriveUsed + "," + paramTotalQuota}}
		if token != "" {
			values.Set("pageToken", token)
		}
		var page usageResponse
		if err := c.doAt(ctx, endpoint, http.MethodGet, "/usage/users/all/dates/"+url.PathEscape(day), values, nil, &page); err != nil {
			if len(out.Users) > 0 && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				out.Partial, out.PartialReason = true, "reading was cut short by a time limit"
				return out, nil
			}
			return nil, err
		}
		for _, w := range page.Warnings {
			if w.Code == "DATA_NOT_AVAILABLE" && len(page.UsageReports) == 0 && len(out.Users) == 0 {
				return nil, nil
			}
		}
		for _, r := range page.UsageReports {
			if r.Entity.CustomerID != c.CustomerID {
				return nil, ErrTenantMismatch
			}
			if r.Entity.Type != "" && r.Entity.Type != "USER" {
				continue
			}
			u := DriveUsageUser{Email: r.Entity.UserEmail}
			for _, p := range r.Parameters {
				n, err := strconv.ParseInt(p.IntValue, 10, 64)
				if err != nil {
					continue
				}
				switch p.Name {
				case paramDriveUsed:
					u.UsedMB = &n
				case paramTotalQuota:
					u.TotalMB = &n
				}
			}
			out.Users = append(out.Users, u)
			if len(out.Users) >= MaxUsageUsers {
				out.Partial, out.PartialReason = true, "stopped at "+strconv.Itoa(MaxUsageUsers)+" users"
				return out, nil
			}
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	if len(out.Users) == 0 {
		return nil, nil
	}
	return out, nil
}

// RankDriveUsage filters users by an email substring and sorts them. "used"
// puts the largest first with users whose usage is unknown last; "email" sorts
// alphabetically.
func RankDriveUsage(users []DriveUsageUser, query, sortBy string) []DriveUsageUser {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]DriveUsageUser, 0, len(users))
	for _, u := range users {
		if query == "" || strings.Contains(strings.ToLower(u.Email), query) {
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if sortBy == "email" {
			return strings.ToLower(a.Email) < strings.ToLower(b.Email)
		}
		switch {
		case a.UsedMB == nil && b.UsedMB == nil:
			return strings.ToLower(a.Email) < strings.ToLower(b.Email)
		case a.UsedMB == nil:
			return false
		case b.UsedMB == nil:
			return true
		case *a.UsedMB != *b.UsedMB:
			return *a.UsedMB > *b.UsedMB
		}
		return strings.ToLower(a.Email) < strings.ToLower(b.Email)
	})
	return out
}

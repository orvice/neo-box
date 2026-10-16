package googleworkspace

import (
	"context"
	"sort"
	"strings"
	"time"
)

// StorageUser is one user's storage from the usage report. A figure Google did
// not report is nil, never zero.
type StorageUser struct {
	Email                      string
	GmailMB, DriveMB, PhotosMB *int64
	UsedMB, TotalMB            *int64
}

// Used is Google's total, or the sum of the parts it reported when it gave no
// total, or nil when it gave neither.
func (u StorageUser) Used() *int64 {
	if u.UsedMB != nil {
		return u.UsedMB
	}
	var sum int64
	known := false
	for _, p := range []*int64{u.GmailMB, u.DriveMB, u.PhotosMB} {
		if p != nil {
			sum += *p
			known = true
		}
	}
	if !known {
		return nil
	}
	return &sum
}

// Quota is the limit Google reports, or nil when none is reported or it is not
// positive (no limit).
func (u StorageUser) Quota() *int64 {
	if u.TotalMB == nil || *u.TotalMB <= 0 {
		return nil
	}
	return u.TotalMB
}

// Percent is used / quota * 100, or nil when either figure is unknown.
func (u StorageUser) Percent() *float64 {
	used, quota := u.Used(), u.Quota()
	if used == nil || quota == nil {
		return nil
	}
	p := float64(*used) / float64(*quota) * 100
	return &p
}

type StorageUsage struct {
	// Date is the report day (YYYY-MM-DD).
	Date          string
	Users         []StorageUser
	Partial       bool
	PartialReason string
}

// ListStorageUsage reads every user's storage from the newest day with data,
// by the same rules as ListDriveUsage.
func (c *Client) ListStorageUsage(ctx context.Context, now time.Time) (*StorageUsage, error) {
	scan, err := c.newestUsageDay(ctx, now, []string{paramGmailUsed, paramDriveUsed, paramPhotosUsed, paramUsedQuota, paramTotalQuota})
	if err != nil {
		return nil, err
	}
	out := &StorageUsage{Date: scan.Date, Partial: scan.Partial, PartialReason: scan.PartialReason}
	for _, r := range scan.Rows {
		u := StorageUser{Email: r.Email}
		for name, dst := range map[string]**int64{paramGmailUsed: &u.GmailMB, paramDriveUsed: &u.DriveMB, paramPhotosUsed: &u.PhotosMB, paramUsedQuota: &u.UsedMB, paramTotalQuota: &u.TotalMB} {
			if v, ok := r.Values[name]; ok && v >= 0 {
				n := v
				*dst = &n
			}
		}
		out.Users = append(out.Users, u)
	}
	return out, nil
}

// StorageRow is a storage user joined with the Directory's name and unit.
type StorageRow struct {
	StorageUser
	Name    string
	OrgUnit string
}

// JoinStorage adds each user's name and organizational unit from the Directory,
// matched by primary email. A user the Directory read did not include keeps an
// empty unit.
func JoinStorage(users []StorageUser, directory []User) []StorageRow {
	byEmail := make(map[string]User, len(directory))
	for _, d := range directory {
		byEmail[strings.ToLower(d.PrimaryEmail)] = d
	}
	rows := make([]StorageRow, 0, len(users))
	for _, u := range users {
		row := StorageRow{StorageUser: u}
		if d, ok := byEmail[strings.ToLower(u.Email)]; ok {
			row.Name = strings.TrimSpace(d.Name.GivenName + " " + d.Name.FamilyName)
			row.OrgUnit = d.OrgUnitPath
		}
		rows = append(rows, row)
	}
	return rows
}

// StorageState is where a user stands against their quota.
type StorageState string

const (
	StorageUnknown StorageState = "unknown" // no used figure
	StorageNoLimit StorageState = "no_limit"
	StorageOK      StorageState = "ok"
	StorageNear    StorageState = "near" // at or above the threshold, under quota
	StorageOver    StorageState = "over" // at or over quota
)

// StateAt classifies a user against a threshold percentage.
func (u StorageUser) StateAt(threshold float64) StorageState {
	if u.Used() == nil {
		return StorageUnknown
	}
	p := u.Percent()
	switch {
	case p == nil:
		return StorageNoLimit
	case *p >= 100:
		return StorageOver
	case *p >= threshold:
		return StorageNear
	}
	return StorageOK
}

type StorageFilter struct {
	// Substring of the email or name, case-insensitive.
	Query string
	// Only users directly in this unit; empty is every unit.
	OrgUnit string
	// Only users near or over their quota at Threshold.
	OnlyNear  bool
	Threshold float64
}

func FilterStorage(rows []StorageRow, f StorageFilter) []StorageRow {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := make([]StorageRow, 0, len(rows))
	for _, r := range rows {
		if q != "" && !strings.Contains(strings.ToLower(r.Email), q) && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		if f.OrgUnit != "" && r.OrgUnit != f.OrgUnit {
			continue
		}
		if f.OnlyNear {
			if s := r.StateAt(f.Threshold); s != StorageNear && s != StorageOver {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// SortStorage orders rows in place. "used" puts the largest first, "percent"
// the fullest first; users whose figure is unknown always come last, then by
// email. "email" sorts alphabetically.
func SortStorage(rows []StorageRow, by string) {
	less := func(a, b StorageRow) bool {
		switch by {
		case "email":
		case "percent":
			pa, pb := a.Percent(), b.Percent()
			if (pa == nil) != (pb == nil) {
				return pa != nil
			}
			if pa != nil && *pa != *pb {
				return *pa > *pb
			}
		default:
			ua, ub := a.Used(), b.Used()
			if (ua == nil) != (ub == nil) {
				return ua != nil
			}
			if ua != nil && *ua != *ub {
				return *ua > *ub
			}
		}
		return strings.ToLower(a.Email) < strings.ToLower(b.Email)
	}
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
}

// OrgUnitStorage summarises the users sitting directly in one unit.
type OrgUnitStorage struct {
	Path                 string
	Users, Unknown       int64
	UsedMB, LargestMB    int64
	QuotaMB              *int64
	QuotaMixed           bool
	NearCount, OverCount int64
}

// SummarizeOrgUnits groups rows by unit, largest total first. A unit's quota is
// set only when every user in it that reports one reports the same; users with
// no quota reported do not make it mixed.
func SummarizeOrgUnits(rows []StorageRow, threshold float64) []OrgUnitStorage {
	byPath := map[string]*OrgUnitStorage{}
	for _, r := range rows {
		s := byPath[r.OrgUnit]
		if s == nil {
			s = &OrgUnitStorage{Path: r.OrgUnit}
			byPath[r.OrgUnit] = s
		}
		s.Users++
		if used := r.Used(); used == nil {
			s.Unknown++
		} else {
			s.UsedMB += *used
			s.LargestMB = max(s.LargestMB, *used)
		}
		if q := r.Quota(); q != nil {
			switch {
			case s.QuotaMixed:
			case s.QuotaMB == nil:
				v := *q
				s.QuotaMB = &v
			case *s.QuotaMB != *q:
				s.QuotaMB, s.QuotaMixed = nil, true
			}
		}
		switch r.StateAt(threshold) {
		case StorageNear:
			s.NearCount++
		case StorageOver:
			s.OverCount++
		}
	}
	out := make([]OrgUnitStorage, 0, len(byPath))
	for _, s := range byPath {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UsedMB != out[j].UsedMB {
			return out[i].UsedMB > out[j].UsedMB
		}
		return out[i].Path < out[j].Path
	})
	return out
}

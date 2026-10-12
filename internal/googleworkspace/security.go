package googleworkspace

import "time"

type SecurityFlags struct{ Admin, AdminWithoutTwoStep, Inactive, NeverLoggedIn, Unknown bool }

func AssessSecurity(u User, now time.Time, inactivityDays int) SecurityFlags {
	admin := (u.IsAdmin != nil && *u.IsAdmin) || (u.IsDelegatedAdmin != nil && *u.IsDelegatedAdmin)
	f := SecurityFlags{Admin: admin, AdminWithoutTwoStep: admin && u.TwoStepEnrolled != nil && !*u.TwoStepEnrolled}
	last, err := time.Parse(time.RFC3339Nano, u.LastLoginTime)
	f.NeverLoggedIn = err == nil && !last.After(time.Unix(0, 0))
	cutoff := now.Add(-time.Duration(inactivityDays) * 24 * time.Hour)
	if err == nil {
		if f.NeverLoggedIn {
			created, e := time.Parse(time.RFC3339Nano, u.CreationTime)
			f.Inactive = e == nil && created.Before(cutoff)
		} else {
			f.Inactive = last.Before(cutoff)
		}
	}
	f.Unknown = u.TwoStepEnrolled == nil || u.TwoStepEnforced == nil || u.IsAdmin == nil || u.IsDelegatedAdmin == nil || err != nil
	if f.NeverLoggedIn {
		_, e := time.Parse(time.RFC3339Nano, u.CreationTime)
		f.Unknown = f.Unknown || e != nil
	}
	return f
}

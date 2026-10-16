package googleworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// One user's usage report parameters, checked 2026-10 against Google's
// "Users usage parameters" reference (accounts and gmail).
const (
	paramGmailUsed   = "accounts:gmail_used_quota_in_mb"
	paramPhotosUsed  = "accounts:gplus_photos_used_quota_in_mb"
	paramUsedQuota   = "accounts:used_quota_in_mb"
	paramEmailsRecv  = "gmail:num_emails_received"
	paramEmailsSent  = "gmail:num_emails_sent"
	paramSpamRecv    = "gmail:num_spam_emails_received"
	maxUserUsageDays = 30
	// userUsageParallel bounds concurrent day reads of one user.
	userUsageParallel = 4
)

// UserUsageDay is one day of a user's usage report. A figure Google did not
// report is nil, never zero.
type UserUsageDay struct {
	Date                                  string
	GmailMB, DriveMB, PhotosMB            *int64
	UsedMB, TotalMB                       *int64
	EmailsReceived, EmailsSent, SpamRecvd *int64
}

type UserUsage struct {
	Email string
	// Days Google has a report for, oldest first.
	Days []UserUsageDay
}

// ReadUserUsage reads the `days` days before `now` (yesterday first) for one
// user. A day Google has no report for is left out. Any other failure, such as
// a missing scope, stops the read. The user is addressed by primary email, and
// a report for another customer is refused.
func (c *Client) ReadUserUsage(ctx context.Context, email string, now time.Time, days int) (*UserUsage, error) {
	if strings.TrimSpace(email) == "" {
		return nil, errors.New("user email is required")
	}
	if days < 1 || days > maxUserUsageDays {
		return nil, invalid("days", "must be between 1 and "+strconv.Itoa(maxUserUsageDays))
	}
	endpoint := c.ReportsEndpoint
	if endpoint == "" {
		endpoint = reportsURL
	}
	type result struct {
		day *UserUsageDay
		err error
	}
	results := make([]result, days)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, userUsageParallel)
	var wg sync.WaitGroup
	for i := 0; i < days; i++ {
		date := now.UTC().AddDate(0, 0, -(i + 1)).Format("2006-01-02")
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d, err := c.readUserUsageDay(ctx, endpoint, email, date)
			results[i] = result{d, err}
			if err != nil {
				cancel() // no point reading further days
			}
		}()
	}
	wg.Wait()
	out := &UserUsage{Email: email}
	var first error
	for _, r := range results {
		if r.err != nil && first == nil && !errors.Is(r.err, context.Canceled) {
			first = r.err
		}
		if r.day != nil {
			out.Days = append(out.Days, *r.day)
		}
	}
	if first == nil {
		// Only our own cancellation after an error leaves a Canceled result;
		// a cancelled caller context is reported as such.
		for _, r := range results {
			if r.err != nil {
				first = r.err
				break
			}
		}
	}
	if first != nil {
		return nil, first
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date < out.Days[j].Date })
	return out, nil
}

func (c *Client) readUserUsageDay(ctx context.Context, endpoint, email, date string) (*UserUsageDay, error) {
	values := url.Values{
		"customerId": {c.CustomerID},
		"parameters": {strings.Join([]string{paramGmailUsed, paramDriveUsed, paramPhotosUsed, paramUsedQuota, paramTotalQuota, paramEmailsRecv, paramEmailsSent, paramSpamRecv}, ",")},
	}
	var page usageResponse
	err := c.doAt(ctx, endpoint, http.MethodGet, "/usage/users/"+url.PathEscape(email)+"/dates/"+url.PathEscape(date), values, nil, &page)
	var api *APIError
	switch {
	case err == nil:
	case errors.As(err, &api) && api.Status == http.StatusBadRequest:
		return nil, nil // Google: data for this date is not available yet
	default:
		return nil, err
	}
	for _, r := range page.UsageReports {
		if r.Entity.CustomerID != c.CustomerID {
			return nil, ErrTenantMismatch
		}
		if r.Entity.Type != "" && r.Entity.Type != "USER" {
			continue
		}
		if r.Entity.UserEmail != "" && !strings.EqualFold(r.Entity.UserEmail, email) {
			continue
		}
		day := &UserUsageDay{Date: date}
		known := false
		for _, p := range r.Parameters {
			n, err := strconv.ParseInt(p.IntValue, 10, 64)
			if err != nil || n < 0 {
				continue
			}
			var dst **int64
			switch p.Name {
			case paramGmailUsed:
				dst = &day.GmailMB
			case paramDriveUsed:
				dst = &day.DriveMB
			case paramPhotosUsed:
				dst = &day.PhotosMB
			case paramUsedQuota:
				dst = &day.UsedMB
			case paramTotalQuota:
				dst = &day.TotalMB
			case paramEmailsRecv:
				dst = &day.EmailsReceived
			case paramEmailsSent:
				dst = &day.EmailsSent
			case paramSpamRecv:
				dst = &day.SpamRecvd
			default:
				continue
			}
			v := n
			*dst = &v
			known = true
		}
		if known {
			return day, nil
		}
	}
	return nil, nil
}

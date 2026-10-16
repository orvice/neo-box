package googleworkspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func userReport(customer, email string, params map[string]string) string {
	var ps []string
	for name, v := range params {
		ps = append(ps, fmt.Sprintf(`{"name":%q,"intValue":%q}`, name, v))
	}
	return fmt.Sprintf(`{"usageReports":[{"entity":{"customerId":%q,"userEmail":%q,"type":"USER"},"parameters":[%s]}]}`, customer, email, strings.Join(ps, ","))
}

func TestUserUsageReadsEachDayForOneUserOldestFirstAndSkipsMissingDays(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var paths []string
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		q := r.URL.Query()
		if q.Get("customerId") != "CtenantA" {
			t.Errorf("read was not tenant scoped: %v", q)
		}
		for _, want := range []string{"accounts:gmail_used_quota_in_mb", "accounts:total_quota_in_mb", "gmail:num_emails_sent"} {
			if !strings.Contains(q.Get("parameters"), want) {
				t.Errorf("parameters %q lack %s", q.Get("parameters"), want)
			}
		}
		switch strings.TrimPrefix(r.URL.Path, "/usage/users/ada@a.example/dates/") {
		case "2026-10-12":
			fmt.Fprint(w, userReport("CtenantA", "ada@a.example", map[string]string{"accounts:gmail_used_quota_in_mb": "300", "accounts:used_quota_in_mb": "500", "accounts:total_quota_in_mb": "15360", "gmail:num_emails_sent": "0", "gmail:num_emails_received": "7"}))
		case "2026-10-11":
			w.WriteHeader(400) // data not available yet
			fmt.Fprint(w, `{"error":{"errors":[{"reason":"invalid"}]}}`)
		case "2026-10-10":
			fmt.Fprint(w, `{"warnings":[{"code":"DATA_NOT_AVAILABLE"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	u, err := c.ReadUserUsage(context.Background(), "ada@a.example", now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Days) != 1 || u.Days[0].Date != "2026-10-12" || len(paths) != 3 {
		t.Fatalf("days=%+v paths=%v", u.Days, paths)
	}
	d := u.Days[0]
	if d.GmailMB == nil || *d.GmailMB != 300 || d.UsedMB == nil || *d.UsedMB != 500 || d.TotalMB == nil || *d.TotalMB != 15360 {
		t.Fatalf("storage=%+v", d)
	}
	if d.EmailsSent == nil || *d.EmailsSent != 0 || d.EmailsReceived == nil || *d.EmailsReceived != 7 {
		t.Fatalf("a reported zero must stay zero: %+v", d)
	}
	if d.DriveMB != nil || d.PhotosMB != nil || d.SpamRecvd != nil {
		t.Fatalf("a figure Google did not report became a number: %+v", d)
	}
}

func TestUserUsageSortsDaysOldestFirst(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, userReport("CtenantA", "ada@a.example", map[string]string{"accounts:used_quota_in_mb": "1"}))
	})
	u, err := c.ReadUserUsage(context.Background(), "ada@a.example", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), 6)
	if err != nil || len(u.Days) != 6 {
		t.Fatalf("u=%+v err=%v", u, err)
	}
	for i := 1; i < len(u.Days); i++ {
		if u.Days[i-1].Date >= u.Days[i].Date {
			t.Fatalf("not oldest first: %+v", u.Days)
		}
	}
}

func TestUserUsageRefusesAnotherCustomersReport(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, userReport("CtenantB", "ada@a.example", map[string]string{"accounts:used_quota_in_mb": "1"}))
	})
	if _, err := c.ReadUserUsage(context.Background(), "ada@a.example", time.Now(), 2); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestUserUsageIgnoresAReportForAnotherUser(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, userReport("CtenantA", "bob@a.example", map[string]string{"accounts:used_quota_in_mb": "1"}))
	})
	u, err := c.ReadUserUsage(context.Background(), "ada@a.example", time.Now(), 2)
	if err != nil || len(u.Days) != 0 {
		t.Fatalf("u=%+v err=%v", u, err)
	}
}

func TestUserUsagePermissionFailureIsReportedNotHiddenAsNoData(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}]}}`)
	})
	_, err := c.ReadUserUsage(context.Background(), "ada@a.example", time.Now(), 5)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 {
		t.Fatalf("err=%v", err)
	}
}

func TestUserUsageChecksItsInputs(t *testing.T) {
	c := reportsClient(t, func(http.ResponseWriter, *http.Request) { t.Error("Google reached") })
	for _, days := range []int{0, 31} {
		if _, err := c.ReadUserUsage(context.Background(), "ada@a.example", time.Now(), days); err == nil {
			t.Fatalf("days=%d accepted", days)
		}
	}
	if _, err := c.ReadUserUsage(context.Background(), " ", time.Now(), 3); err == nil {
		t.Fatal("empty email accepted")
	}
}

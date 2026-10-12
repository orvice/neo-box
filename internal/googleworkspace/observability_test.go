package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/types/known/structpb"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testWriteRecorder struct {
	beginErr, finishErr error
	writes              []Write
	results             []map[string]any
	outcomes            []error
	finishContextErr    error
}

func (r *testWriteRecorder) Begin(_ context.Context, w Write) (func(context.Context, map[string]any, error) error, error) {
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	r.writes = append(r.writes, w)
	return func(ctx context.Context, result map[string]any, err error) error {
		r.results = append(r.results, result)
		r.outcomes = append(r.outcomes, err)
		r.finishContextErr = ctx.Err()
		return r.finishErr
	}, nil
}
func TestWriteLogPrecedesMutationAndNeverIncludesSecrets(t *testing.T) {
	recorder := &testWriteRecorder{}
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeUser(w, "u", "CtenantA", false)
			return
		}
		writes++
		if len(recorder.writes) != 1 {
			t.Error("write sent without durable log first")
		}
		fmt.Fprint(w, `{"id":"u","customerId":"CtenantA","primaryEmail":"u@a.example","name":{"givenName":"A","familyName":"B"},"password":"SERVER-SECRET","access_token":"SERVER-TOKEN"}`)
	})
	c.SetWriteRecorder(recorder)
	_, err := c.UpdateUser(context.Background(), "u", map[string]any{"password": "REQUEST-SECRET", "changePasswordAtNextLogin": true, "client_secret": "HIDDEN"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(recorder)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("secret stored")
	}
	raw, _ = json.Marshal([]any{recorder.writes, recorder.results})
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "TOKEN") {
		t.Fatalf("credential leaked into log: %s", raw)
	}
	if writes != 1 || len(recorder.outcomes) != 1 || recorder.outcomes[0] != nil {
		t.Fatal("wrong write lifecycle")
	}
	if recorder.writes[0].Requested["password_changed"] != true || WriteAction(recorder.writes[0]) != "user_password_reset" {
		t.Fatal("password-reset metadata missing")
	}
}
func TestWriteRefusedWhenLogCannotBeStoredAndPendingWhenFinishFails(t *testing.T) {
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeUser(w, "u", "CtenantA", false)
			return
		}
		writes++
		writeUser(w, "u", "CtenantA", true)
	})
	recorder := &testWriteRecorder{beginErr: errors.New("database unavailable")}
	c.SetWriteRecorder(recorder)
	if _, err := c.UpdateUser(context.Background(), "u", map[string]any{"suspended": true}); !errors.Is(err, ErrLogUnavailable) || writes != 0 {
		t.Fatalf("unlogged mutation: %v writes=%d", err, writes)
	}
	recorder.beginErr = nil
	recorder.finishErr = errors.New("outcome storage unavailable")
	if _, err := c.UpdateUser(context.Background(), "u", map[string]any{"suspended": true}); !errors.Is(err, ErrOutcomeLogUnavailable) || writes != 1 {
		t.Fatalf("outcome-log failure=%v writes=%d", err, writes)
	}
}
func TestUnknownWriteOutcomeIsRecordedOnceAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeUser(w, "u", "CtenantA", false)
			return
		}
		writes++
		cancel()
		w.WriteHeader(503)
	})
	recorder := &testWriteRecorder{}
	c.SetWriteRecorder(recorder)
	_, err := c.UpdateUser(ctx, "u", map[string]any{"suspended": true})
	if !errors.Is(err, ErrUncertain) || writes != 1 || len(recorder.outcomes) != 1 || !errors.Is(recorder.outcomes[0], ErrUncertain) {
		t.Fatalf("unknown outcome=%v writes=%d log=%v", err, writes, recorder.outcomes)
	}
	if recorder.finishContextErr != nil {
		t.Fatal("cancelled request prevented recording outcome")
	}
}
func TestAuditCustomerUserTimePagingAndRedaction(t *testing.T) {
	requests := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/foreign" {
			writeUser(w, "foreign", "CtenantB", false)
			return
		}
		if r.URL.Path == "/users/u" {
			writeUser(w, "stable", "CtenantA", false)
			return
		}
		requests++
		q := r.URL.Query()
		if q.Get("customerId") != "CtenantA" || q.Get("pageToken") != "next" || q.Get("eventName") != "login_success" || q.Get("startTime") == "" || q.Get("endTime") == "" {
			t.Errorf("unsafe audit query %v", q)
		}
		if r.URL.Path != "/activity/users/stable/applications/login" {
			t.Errorf("unverified actor target: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"items":[{"id":{"time":"2026-10-12T00:00:00Z","uniqueQualifier":"123","customerId":"CtenantA","applicationName":"login"},"actor":{"email":"u@a.example"},"events":[{"name":"login_success","parameters":[{"name":"login_type","value":"google_password"},{"name":"access_token","value":"AUDIT-SECRET"},{"name":"nested","messageValue":{"parameter":[{"name":"private_key","value":"NESTED-SECRET"},{"name":"is_admin","boolValue":false}]}}],"sensitiveParameters":[{"name":"anything","value":"SENSITIVE-SECRET"}]}]}],"nextPageToken":"after"}`)
	})
	c.ReportsEndpoint = c.Endpoint
	q := AuditQuery{Application: "login", UserKey: "foreign", EventName: "login_success", PageToken: "next", PageSize: 50, Start: time.Now().Add(-time.Hour), End: time.Now()}
	if _, err := c.ListAuditActivities(context.Background(), q); !errors.Is(err, ErrTenantMismatch) || requests != 0 {
		t.Fatal("foreign user's audit was read", err)
	}
	q.UserKey = "u"
	p, err := c.ListAuditActivities(context.Background(), q)
	if err != nil || p.NextPageToken != "after" {
		t.Fatalf("audit=%v %v", p, err)
	}
	details := AuditParameters(p.Items[0].Events[0].Parameters)
	raw, _ := json.Marshal(details)
	if strings.Contains(string(raw), "SECRET") || details["access_token"] != "[redacted]" {
		t.Fatalf("audit credentials exposed: %s", raw)
	}
	raw, _ = json.Marshal(p)
	if strings.Contains(string(raw), "SENSITIVE-SECRET") {
		t.Fatal("sensitiveParameters were decoded")
	}
	for _, value := range []string{"2026-10-12T00:00:00Z", "1791763200"} {
		if _, err = AuditTime(value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = AuditTime("invalid"); err == nil {
		t.Fatal("invalid Google timestamp accepted")
	}
}
func TestForeignCustomerAuditPageIsRejected(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":{"time":"2026-10-12T00:00:00Z","applicationName":"login","customerId":"CtenantB"}}]}`)
	})
	c.ReportsEndpoint = c.Endpoint
	if _, err := c.ListAuditActivities(context.Background(), AuditQuery{Application: "login", PageSize: 50, Start: time.Now().Add(-time.Hour), End: time.Now()}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("foreign audit=%v", err)
	}
}
func TestSecurityAssessmentDistinguishesUnknownAndNewNeverUsedAccounts(t *testing.T) {
	yes, no := true, false
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	base := User{IsAdmin: &no, IsDelegatedAdmin: &no, TwoStepEnrolled: &yes, TwoStepEnforced: &no, LastLoginTime: now.Add(-24 * time.Hour).Format(time.RFC3339), CreationTime: now.Add(-365 * 24 * time.Hour).Format(time.RFC3339)}
	f := AssessSecurity(base, now, 90)
	if f.Unknown || f.Inactive || f.AdminWithoutTwoStep {
		t.Fatalf("normal account=%+v", f)
	}
	u := base
	u.IsAdmin = &yes
	u.TwoStepEnrolled = &no
	f = AssessSecurity(u, now, 90)
	if !f.AdminWithoutTwoStep || f.Unknown {
		t.Fatalf("unenrolled admin=%+v", f)
	}
	u.TwoStepEnrolled = nil
	f = AssessSecurity(u, now, 90)
	if f.AdminWithoutTwoStep || !f.Unknown {
		t.Fatalf("unknown 2-step was treated as disabled: %+v", f)
	}
	u = base
	u.LastLoginTime = ""
	f = AssessSecurity(u, now, 90)
	if !f.Unknown || f.Inactive || f.NeverLoggedIn {
		t.Fatalf("missing login=%+v", f)
	}
	u = base
	u.LastLoginTime = "1970-01-01T00:00:00.000Z"
	u.CreationTime = now.Format(time.RFC3339)
	f = AssessSecurity(u, now, 90)
	if !f.NeverLoggedIn || f.Inactive {
		t.Fatalf("new unused account=%+v", f)
	}
	u.CreationTime = base.CreationTime
	f = AssessSecurity(u, now, 90)
	if !f.Inactive {
		t.Fatal("old never-used account not inactive")
	}
	u.CreationTime = ""
	f = AssessSecurity(u, now, 90)
	if f.Inactive || !f.Unknown {
		t.Fatal("unknown creation date asserted inactivity")
	}
}

func TestAuditParametersPreserveMultiValuesAndFalseBooleans(t *testing.T) {
	var parameters []AuditParameter
	if err := json.Unmarshal([]byte(`[{"name":"scopes","multiValue":["read","write"]},{"name":"ids","multiIntValue":["9223372036854775807"]},{"name":"enforced","boolValue":false}]`), &parameters); err != nil {
		t.Fatal(err)
	}
	details := AuditParameters(parameters)
	value, err := structpb.NewStruct(details)
	if err != nil {
		t.Fatalf("audit details cannot be encoded for Connect: %v", err)
	}
	if value.Fields["enforced"].GetBoolValue() != false || len(value.Fields["scopes"].GetListValue().Values) != 2 || value.Fields["ids"].GetListValue().Values[0].GetStringValue() != "9223372036854775807" {
		t.Fatalf("audit parameter types lost: %v", value)
	}
}

func TestMutationRedirectDoesNotResendOrSwitchTarget(t *testing.T) {
	writes, redirects := 0, 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/users/u" {
			writeUser(w, "u", "CtenantA", false)
			return
		}
		if r.URL.Path == "/other-tenant-target" {
			redirects++
			writeUser(w, "foreign", "CtenantB", false)
			return
		}
		writes++
		w.Header().Set("Location", "/other-tenant-target")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	if err := c.SignOutUser(context.Background(), "u"); !errors.Is(err, ErrUncertain) || writes != 1 || redirects != 0 {
		t.Fatalf("redirect resent mutation: err=%v writes=%d redirects=%d", err, writes, redirects)
	}
}

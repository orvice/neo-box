package cloudflare_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
)

func emailFixture(t *testing.T) (*cftest.Server, *cloudflare.Client) {
	t.Helper()
	f := cftest.New()
	t.Cleanup(f.Close)
	f.AddAccount(accA, "A")
	f.AddToken("tok", &cftest.Token{Accounts: []string{accA}, Perms: []string{
		cftest.PermZoneRead, cftest.PermEmailRulesRead, cftest.PermEmailRulesEdit,
		cftest.PermEmailAddrRead, cftest.PermEmailAddrEdit,
	}})
	f.AddToken("ro", &cftest.Token{Accounts: []string{accA}, Perms: []string{
		cftest.PermZoneRead, cftest.PermEmailRulesRead, cftest.PermEmailAddrRead,
	}})
	f.AddZone(&cftest.Zone{ID: "z1", Name: "example.com", Account: accA})
	f.AddEmailAddress(accA, &cftest.EmailAddress{Email: "me@gmail.com", Verified: true})
	return f, newClient(t, f.URL, "tok")
}

func TestEmailRoutingEnable(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	s, err := c.GetEmailRouting(ctx, "z1")
	if err != nil || s.Enabled {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	recs, err := c.EmailRoutingDNS(ctx, "z1")
	if err != nil || len(recs) == 0 {
		t.Fatalf("dns records = %+v, %v", recs, err)
	}
	var mx bool
	for _, r := range recs {
		mx = mx || (r.Type == "MX" && r.Priority != nil)
	}
	if !mx {
		t.Fatalf("no MX record with a priority in %+v", recs)
	}
	s, err = c.EnableEmailRouting(ctx, "z1")
	if err != nil || !s.Enabled || s.Status != "ready" {
		t.Fatalf("enable = %+v, %v", s, err)
	}
	if !f.EmailRouting("z1").Enabled {
		t.Fatal("the fake was not enabled")
	}
}

func TestEmailRuleLifecycle(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()
	f.SetEmailEnabled("z1", true)

	in := cloudflare.EmailRuleInput{Name: "Info", Address: "info@example.com", Action: "forward", Target: "me@gmail.com", Enabled: true}
	created, err := c.CreateEmailRule(ctx, "z1", in.Rule(nil))
	if err != nil || created.ID == "" || created.Address() != "info@example.com" ||
		created.Target() != "me@gmail.com" || created.ActionType() != "forward" || !created.Enabled {
		t.Fatalf("create = %+v, %v", created, err)
	}
	f.AddEmailRule("z1", &cftest.EmailRule{Name: "hook", Address: "hook@example.com", Action: "worker", Target: "w", Enabled: true})

	rules, err := c.ListEmailRules(ctx, "z1")
	if err != nil || len(rules) != 2 {
		t.Fatalf("list = %+v, %v", rules, err)
	}
	for _, r := range rules {
		reason := r.ReadOnlyReason(false)
		if (r.Address() == "hook@example.com") != (reason != "") {
			t.Fatalf("rule %s read-only reason = %q", r.Address(), reason)
		}
	}

	off := in
	off.Enabled = false
	got, err := c.UpdateEmailRule(ctx, "z1", created.ID, off.Rule(created))
	if err != nil || got.Enabled || got.ID != created.ID || got.Target() != "me@gmail.com" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if !got.Same(off.Rule(created)) || got.Same(in.Rule(created)) {
		t.Fatal("Same does not tell the enabled flag apart")
	}

	bad := in
	bad.Target = "stranger@gmail.com"
	if _, err := c.UpdateEmailRule(ctx, "z1", created.ID, bad.Rule(created)); !cloudflare.IsRejected(err) {
		t.Fatalf("forward to an unverified address: %v", err)
	}

	if err := c.DeleteEmailRule(ctx, "z1", created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rules, _ := c.ListEmailRules(ctx, "z1"); len(rules) != 1 {
		t.Fatalf("after delete = %+v", rules)
	}
}

func TestEmailCatchAll(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	cur, err := c.GetEmailCatchAll(ctx, "z1")
	if err != nil || cur.Enabled || cur.ActionType() != "drop" || cur.ReadOnlyReason(true) != "" {
		t.Fatalf("catch-all = %+v, %v", cur, err)
	}
	in := cloudflare.EmailCatchAllInput{Enabled: true, Action: "forward", Target: "me@gmail.com"}
	got, err := c.UpdateEmailCatchAll(ctx, "z1", in.Rule(*cur))
	if err != nil || !got.Enabled || got.Target() != "me@gmail.com" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if st := f.EmailRouting("z1").CatchAll; !st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all = %+v", st)
	}
	// Off keeps the current action.
	off := cloudflare.EmailCatchAllInput{Enabled: false}.Rule(*got)
	if off.Enabled || off.ActionType() != "forward" || off.Target() != "me@gmail.com" {
		t.Fatalf("off rule = %+v", off)
	}
}

func TestEmailAddresses(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	list, err := c.ListEmailAddresses(ctx, accA)
	if err != nil || len(list) != 1 || !list[0].IsVerified() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	created, err := c.CreateEmailAddress(ctx, accA, "new@gmail.com")
	if err != nil || created.IsVerified() || created.Email != "new@gmail.com" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := c.CreateEmailAddress(ctx, accA, "new@gmail.com"); !cloudflare.IsRejected(err) {
		t.Fatalf("duplicate: %v", err)
	}
	f.VerifyEmailAddress(accA, "new@gmail.com")
	list, _ = c.ListEmailAddresses(ctx, accA)
	if len(list) != 2 || !list[1].IsVerified() {
		t.Fatalf("after verify = %+v", list)
	}
	if err := c.DeleteEmailAddress(ctx, accA, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := f.EmailAddresses(accA); len(got) != 1 {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestEmailPermissions(t *testing.T) {
	f, _ := emailFixture(t)
	ro := newClient(t, f.URL, "ro")
	ctx := context.Background()
	if _, err := ro.GetEmailRouting(ctx, "z1"); err != nil {
		t.Fatalf("read with a read token: %v", err)
	}
	if _, err := ro.EnableEmailRouting(ctx, "z1"); !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("enable with a read token: %v", err)
	}
	if _, err := ro.CreateEmailAddress(ctx, accA, "x@gmail.com"); !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("create address with a read token: %v", err)
	}
}

func TestValidateEmailRuleInput(t *testing.T) {
	ok := cloudflare.EmailRuleInput{Address: "info@example.com", Action: "forward", Target: "me@gmail.com"}
	if err := cloudflare.ValidateEmailRuleInput("example.com", ok); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	if err := cloudflare.ValidateEmailRuleInput("example.com", cloudflare.EmailRuleInput{Address: "info@example.com", Action: "drop"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	for name, in := range map[string]cloudflare.EmailRuleInput{
		"no address": {Action: "drop"},
		"other zone": {Address: "info@other.com", Action: "drop"},
		"bad target": {Address: "info@example.com", Action: "forward", Target: "nope"},
		"no action":  {Address: "info@example.com"},
		"worker":     {Address: "info@example.com", Action: "worker", Target: "w"},
		"long name":  {Address: "info@example.com", Action: "drop", Name: strings.Repeat("x", 257)},
	} {
		var fe *cloudflare.FieldError
		if err := cloudflare.ValidateEmailRuleInput("example.com", in); !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a FieldError", name, err)
		}
	}
	n := cloudflare.EmailRuleInput{Name: " X ", Address: " Info@Example.COM ", Action: " Forward ", Target: " Me@Gmail.com "}.Normalized()
	if n.Name != "X" || n.Address != "info@example.com" || n.Action != "forward" || n.Target != "me@gmail.com" {
		t.Fatalf("normalized = %+v", n)
	}
	if err := cloudflare.ValidateEmailCatchAllInput(cloudflare.EmailCatchAllInput{Enabled: true, Action: "forward", Target: "x"}); err == nil {
		t.Fatal("catch-all forward to a bad target was accepted")
	}
	if err := cloudflare.ValidateEmailCatchAllInput(cloudflare.EmailCatchAllInput{Enabled: false}); err != nil {
		t.Fatalf("catch-all off: %v", err)
	}
}

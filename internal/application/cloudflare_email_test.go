package application

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	emailForward = neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD
	emailDrop    = neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP
)

// emailFixture is a connection with one verified (me@) and one unverified
// (new@) destination address.
func emailFixture(t *testing.T) (*cfFixture, string) {
	f := newCFFixture(t)
	f.cf.AddEmailAddress(cfAccA, &cftest.EmailAddress{Email: "me@gmail.com", Verified: true})
	f.cf.AddEmailAddress(cfAccA, &cftest.EmailAddress{Email: "new@gmail.com"})
	return f, f.connect(t, "u1", cfAccA, "full")
}

type emailAPI struct {
	t    *testing.T
	f    *cfFixture
	conn string
	zone string
}

func (e emailAPI) get() *neoboxv1.GetCloudflareEmailRoutingResponse {
	e.t.Helper()
	r, err := e.f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: e.conn, ZoneId: e.zone}))
	if err != nil {
		e.t.Fatalf("GetCloudflareEmailRouting: %v", err)
	}
	return r.Msg
}

func (e emailAPI) enable() (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.EnableCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.EnableCloudflareEmailRoutingRequest{ConnectionId: e.conn, ZoneId: e.zone}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) createRule(in *neoboxv1.CloudflareEmailRuleInput) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.CreateCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.CreateCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, Rule: in}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) updateRule(id string, in *neoboxv1.CloudflareEmailRuleInput) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.UpdateCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.UpdateCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, RuleId: id, Rule: in}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) deleteRule(id string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.DeleteCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, RuleId: id}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) catchAll(enabled bool, action neoboxv1.CloudflareEmailAction, target string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.SetCloudflareEmailCatchAll(asUser("u1"), connect.NewRequest(&neoboxv1.SetCloudflareEmailCatchAllRequest{ConnectionId: e.conn, ZoneId: e.zone, Enabled: enabled, Action: action, Target: target}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) addresses() []*neoboxv1.CloudflareEmailAddress {
	e.t.Helper()
	r, err := e.f.srv.ListCloudflareEmailAddresses(asUser("u1"), connect.NewRequest(&neoboxv1.ListCloudflareEmailAddressesRequest{ConnectionId: e.conn}))
	if err != nil {
		e.t.Fatalf("ListCloudflareEmailAddresses: %v", err)
	}
	return r.Msg.GetAddresses()
}

func (e emailAPI) createAddress(email string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.CreateCloudflareEmailAddress(asUser("u1"), connect.NewRequest(&neoboxv1.CreateCloudflareEmailAddressRequest{ConnectionId: e.conn, Email: email}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) deleteAddress(id string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.DeleteCloudflareEmailAddress(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteCloudflareEmailAddressRequest{ConnectionId: e.conn, ZoneId: e.zone, AddressId: id}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func addressByEmail(list []*neoboxv1.CloudflareEmailAddress, email string) *neoboxv1.CloudflareEmailAddress {
	for _, a := range list {
		if a.GetEmail() == email {
			return a
		}
	}
	return nil
}

func ruleIn(addr string, action neoboxv1.CloudflareEmailAction, target string, enabled bool) *neoboxv1.CloudflareEmailRuleInput {
	return &neoboxv1.CloudflareEmailRuleInput{Address: addr, Action: action, Target: target, Enabled: enabled}
}

func TestCloudflareEmailRoutingEnableAndRead(t *testing.T) {
	f, conn := emailFixture(t)
	api := emailAPI{t, f, conn, "za1"}

	got := api.get()
	if got.GetSettings().GetEnabled() || len(got.GetRequiredDnsRecords()) == 0 || len(got.GetRules()) != 0 || len(got.GetIssues()) != 0 {
		t.Fatalf("before enabling = %+v", got)
	}
	res, err := api.enable()
	op := res.GetOperation()
	if err != nil || !res.GetChanged() || op.GetKind() != neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING ||
		op.GetAction() != "enable_routing" || op.GetResource() != "alpha.com" ||
		op.GetStatus() != neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_SUCCEEDED {
		t.Fatalf("enable = %+v, %v", res, err)
	}
	if !f.cf.EmailRouting("za1").Enabled {
		t.Fatal("Email Routing was not enabled at Cloudflare")
	}
	again, err := api.enable()
	if err != nil || again.GetChanged() || len(resourceOps(t, f, conn)) != 1 {
		t.Fatalf("enable again = %+v, %v (%d operations)", again, err, len(resourceOps(t, f, conn)))
	}
	got = api.get()
	if !got.GetSettings().GetEnabled() || got.GetSettings().GetStatus() != "ready" || len(got.GetRequiredDnsRecords()) != 0 {
		t.Fatalf("after enabling = %+v", got)
	}

	// A Zone of another account is never reachable.
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: conn, ZoneId: "zb1"}))
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u2"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: conn, ZoneId: "za1"}))
	wantCode(t, "connection of another user", err, connect.CodeNotFound)
}

func TestCloudflareEmailRules(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	res, err := api.createRule(ruleIn(" Info@Alpha.com ", emailForward, "me@gmail.com", true))
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "create_rule" || res.GetOperation().GetTarget() != "info@alpha.com" {
		t.Fatalf("create = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1"); len(st.Rules) != 1 || st.Rules[0].Address != "info@alpha.com" || st.Rules[0].Target != "me@gmail.com" {
		t.Fatalf("fake rules = %+v", st.Rules)
	}

	_, err = api.createRule(ruleIn("info@alpha.com", emailDrop, "", true))
	wantCode(t, "duplicate address", err, connect.CodeAlreadyExists)
	_, err = api.createRule(ruleIn("info@bravo.com", emailDrop, "", true))
	wantCode(t, "address of another zone", err, connect.CodeInvalidArgument)
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "not-an-address", true))
	wantCode(t, "bad target", err, connect.CodeInvalidArgument)
	_, err = api.createRule(&neoboxv1.CloudflareEmailRuleInput{Address: "x@alpha.com"})
	wantCode(t, "no action", err, connect.CodeInvalidArgument)
	writes := len(f.cf.Writes())
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "new@gmail.com", true))
	wantCode(t, "unverified target", err, connect.CodeFailedPrecondition)
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "stranger@gmail.com", true))
	wantCode(t, "unknown target", err, connect.CodeFailedPrecondition)
	if len(f.cf.Writes()) != writes {
		t.Fatalf("a refused rule was sent: %+v", f.cf.Writes()[writes:])
	}
	if _, err := api.createRule(ruleIn("sales@alpha.com", emailDrop, "", true)); err != nil {
		t.Fatalf("create drop rule: %v", err)
	}

	// Rules come back sorted by address.
	rules := api.get().GetRules()
	if len(rules) != 2 || rules[0].GetAddress() != "info@alpha.com" || rules[1].GetAddress() != "sales@alpha.com" || !rules[0].GetEditable() {
		t.Fatalf("rules = %+v", rules)
	}
	info := rules[0]

	// Disable it, again (nothing to do), re-enable to an unverified address
	// (refused), then rename.
	off := &neoboxv1.CloudflareEmailRuleInput{Name: info.GetName(), Address: info.GetAddress(), Action: info.GetAction(), Target: info.GetTarget(), Enabled: false}
	res, err = api.updateRule(info.GetId(), off)
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "disable_rule" || f.cf.EmailRouting("za1").Rules[0].Enabled {
		t.Fatalf("disable = %+v, %v", res, err)
	}
	if res, err = api.updateRule(info.GetId(), off); err != nil || res.GetChanged() {
		t.Fatalf("disable again = %+v, %v", res, err)
	}
	reenable := &neoboxv1.CloudflareEmailRuleInput{Name: info.GetName(), Address: info.GetAddress(), Action: info.GetAction(), Target: info.GetTarget(), Enabled: true}
	if res, err = api.updateRule(info.GetId(), reenable); err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "enable_rule" {
		t.Fatalf("enable = %+v, %v", res, err)
	}
	on := &neoboxv1.CloudflareEmailRuleInput{Address: info.GetAddress(), Action: emailForward, Target: "new@gmail.com", Enabled: true}
	_, err = api.updateRule(info.GetId(), on)
	wantCode(t, "re-enable to unverified", err, connect.CodeFailedPrecondition)
	on.Target, on.Name = "me@gmail.com", "Info"
	if res, err = api.updateRule(info.GetId(), on); err != nil || res.GetOperation().GetAction() != "update_rule" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	// Another rule's address cannot be taken.
	on.Address = "sales@alpha.com"
	_, err = api.updateRule(info.GetId(), on)
	wantCode(t, "address of another rule", err, connect.CodeAlreadyExists)
	_, err = api.updateRule("nope", ruleIn("info@alpha.com", emailDrop, "", true))
	wantCode(t, "unknown rule", err, connect.CodeNotFound)

	// A rule that sends to a Worker, or that a Worker's wrangler owns, is
	// shown read-only and never changed.
	f.cf.AddEmailRule("za1", &cftest.EmailRule{Name: "hook", Address: "hook@alpha.com", Action: "worker", Target: "mailer", Enabled: true})
	f.cf.AddEmailRule("za1", &cftest.EmailRule{Name: "wr", Address: "wr@alpha.com", Action: "drop", Source: "wrangler", Enabled: true})
	var worker, wrangler *neoboxv1.CloudflareEmailRule
	for _, r := range api.get().GetRules() {
		switch r.GetAddress() {
		case "hook@alpha.com":
			worker = r
		case "wr@alpha.com":
			wrangler = r
		}
	}
	if worker == nil || worker.GetEditable() || worker.GetReadonlyReason() == "" || worker.GetAction() != neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_WORKER ||
		wrangler == nil || wrangler.GetEditable() || wrangler.GetReadonlyReason() == "" {
		t.Fatalf("read-only rules = %+v, %+v", worker, wrangler)
	}
	writes = len(f.cf.Writes())
	for _, r := range []*neoboxv1.CloudflareEmailRule{worker, wrangler} {
		_, err = api.updateRule(r.GetId(), ruleIn(r.GetAddress(), emailDrop, "", false))
		wantCode(t, "update read-only rule", err, connect.CodeFailedPrecondition)
		_, err = api.deleteRule(r.GetId())
		wantCode(t, "delete read-only rule", err, connect.CodeFailedPrecondition)
	}
	if len(f.cf.Writes()) != writes {
		t.Fatalf("a read-only rule was changed: %+v", f.cf.Writes()[writes:])
	}

	// Delete.
	res, err = api.deleteRule(info.GetId())
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "delete_rule" {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	_, err = api.deleteRule(info.GetId())
	wantCode(t, "delete again", err, connect.CodeNotFound)

	for _, op := range resourceOps(t, f, conn) {
		if op.Kind != cfrepo.KindEmailRouting || op.Resource != "alpha.com" {
			t.Errorf("operation = %+v", op)
		}
	}
}

func TestCloudflareEmailCatchAll(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	if c := api.get().GetCatchAll(); c.GetEnabled() || c.GetAction() != emailDrop || !c.GetEditable() {
		t.Fatalf("catch-all at the start = %+v", c)
	}
	res, err := api.catchAll(true, emailForward, "me@gmail.com")
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "set_catch_all" {
		t.Fatalf("forward = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; !st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all = %+v", st)
	}
	if res, err = api.catchAll(true, emailForward, "ME@gmail.com"); err != nil || res.GetChanged() {
		t.Fatalf("same again = %+v, %v", res, err)
	}
	// Off keeps the forward address.
	if res, err = api.catchAll(false, 0, ""); err != nil || !res.GetChanged() {
		t.Fatalf("off = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all after off = %+v", st)
	}
	if res, err = api.catchAll(false, 0, ""); err != nil || res.GetChanged() {
		t.Fatalf("off again = %+v, %v", res, err)
	}
	_, err = api.catchAll(true, emailForward, "new@gmail.com")
	wantCode(t, "unverified target", err, connect.CodeFailedPrecondition)
	_, err = api.catchAll(true, emailForward, "nope")
	wantCode(t, "bad target", err, connect.CodeInvalidArgument)
	if res, err = api.catchAll(true, emailDrop, ""); err != nil || !res.GetChanged() {
		t.Fatalf("drop = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; !st.Enabled || st.Action != "drop" {
		t.Fatalf("fake catch-all after drop = %+v", st)
	}
}

func TestCloudflareEmailAddresses(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	list := api.addresses()
	if len(list) != 2 || list[0].GetEmail() != "me@gmail.com" || !list[0].GetVerified() || list[1].GetEmail() != "new@gmail.com" || list[1].GetVerified() {
		t.Fatalf("addresses = %+v", list)
	}
	_, err := api.createAddress("nope")
	wantCode(t, "bad address", err, connect.CodeInvalidArgument)
	if res, err := api.createAddress(" ME@gmail.com "); err != nil || res.GetChanged() {
		t.Fatalf("existing address = %+v, %v", res, err)
	}
	res, err := api.createAddress("extra@gmail.com")
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "create_address" ||
		res.GetOperation().GetResource() != "destination addresses" || res.GetOperation().GetTarget() != "extra@gmail.com" {
		t.Fatalf("create = %+v, %v", res, err)
	}
	if got := api.addresses(); len(got) != 3 || addressByEmail(got, "extra@gmail.com") == nil || addressByEmail(got, "extra@gmail.com").GetVerified() {
		t.Fatalf("addresses after create = %+v", got)
	}

	// An address a rule forwards to cannot be deleted.
	if _, err := api.createRule(ruleIn("info@alpha.com", emailForward, "me@gmail.com", true)); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	me := addressByEmail(api.addresses(), "me@gmail.com")
	_, err = api.deleteAddress(me.GetId())
	wantCode(t, "address in use by a rule", err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "info@alpha.com") {
		t.Errorf("the error does not name the rule: %v", err)
	}
	if len(f.cf.EmailAddresses(cfAccA)) != 3 {
		t.Fatal("an address in use was deleted")
	}
	rule := api.get().GetRules()[0]
	if _, err := api.deleteRule(rule.GetId()); err != nil {
		t.Fatal(err)
	}
	// ... nor one the catch-all forwards to.
	if _, err := api.catchAll(true, emailForward, "me@gmail.com"); err != nil {
		t.Fatal(err)
	}
	_, err = api.deleteAddress(me.GetId())
	wantCode(t, "address in use by the catch-all", err, connect.CodeFailedPrecondition)
	if _, err := api.catchAll(true, emailDrop, ""); err != nil {
		t.Fatal(err)
	}
	res, err = api.deleteAddress(me.GetId())
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "delete_address" || len(f.cf.EmailAddresses(cfAccA)) != 2 {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	_, err = api.deleteAddress("nope")
	wantCode(t, "unknown address", err, connect.CodeNotFound)
	other := emailAPI{t, f, conn, "zb1"}
	_, err = other.deleteAddress(api.addresses()[0].GetId())
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
}

func TestCloudflareEmailPermissions(t *testing.T) {
	f := newCFFixture(t)
	read := emailAPI{t, f, f.connect(t, "u1", cfAccA, "email-read"), "za1"}

	if got := read.get(); got.GetSettings().GetEnabled() {
		t.Fatalf("settings = %+v", got)
	}
	_ = read.addresses()
	_, err := read.enable()
	wantCode(t, "enable with a read token", err, connect.CodePermissionDenied)
	_, err = read.createAddress("x@gmail.com")
	wantCode(t, "create address with a read token", err, connect.CodePermissionDenied)
	_, err = read.createRule(ruleIn("x@alpha.com", emailDrop, "", true))
	wantCode(t, "create rule with a read token", err, connect.CodePermissionDenied)

	// A token without any Email Routing permission cannot read it either.
	none := emailAPI{t, f, f.connect(t, "u1", cfAccA, "manage-read"), "za1"}
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: none.conn, ZoneId: "za1"}))
	wantCode(t, "read without permission", err, connect.CodePermissionDenied)
}

func TestCloudflareEmailUnconfirmedWriteIsLoggedAsUnknown(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.DropAnswer = func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/email/routing/dns")
	}
	api := emailAPI{t, f, conn, "za1"}
	_, err := api.enable()
	wantCode(t, "enable without an answer", err, connect.CodeUnknown)
	ops := resourceOps(t, f, conn)
	if len(ops) != 1 || ops[0].Status != cfrepo.StatusUnknown || ops[0].Action != "enable_routing" {
		t.Fatalf("operations = %+v", ops)
	}
}

func TestCloudflareEmailLogs(t *testing.T) {
	f, conn := emailFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	f.cf.AddEmailEvent("za1", cftest.EmailEvent{
		Time: now.Add(-time.Hour), From: "bob@x.org", To: "info@alpha.com", Subject: "Hello", Status: "delivered",
		EventType: "forward", Action: "forward", RuleMatched: "rule-1", MessageID: "<m1>", SPF: "pass", DKIM: "pass", DMARC: "pass", IsLastEvent: true,
	})
	f.cf.AddEmailEvent("za1", cftest.EmailEvent{Time: now.Add(-time.Minute), From: "s@x.org", To: "x@alpha.com", Status: "dropped", Action: "drop", IsSpam: true})
	f.cf.AddEmailEvent("zb1", cftest.EmailEvent{Time: now.Add(-time.Minute), From: "o@x.org", To: "o@other-account.com", Status: "delivered"})
	list := func(user, conn, zone string, start, end time.Time, limit int32) (*neoboxv1.ListCloudflareEmailLogsResponse, error) {
		r, err := f.srv.ListCloudflareEmailLogs(asUser(user), connect.NewRequest(&neoboxv1.ListCloudflareEmailLogsRequest{
			ConnectionId: conn, ZoneId: zone, StartTime: timestamppb.New(start), EndTime: timestamppb.New(end), Limit: limit,
		}))
		if err != nil {
			return nil, err
		}
		return r.Msg, nil
	}

	got, err := list("u1", conn, "za1", now.Add(-2*time.Hour), now, 0)
	if err != nil || len(got.GetLogs()) != 2 || got.GetTruncated() {
		t.Fatalf("logs = %+v, %v", got, err)
	}
	newest, older := got.GetLogs()[0], got.GetLogs()[1]
	if newest.GetStatus() != "dropped" || !newest.GetSpam() || newest.GetAction() != "drop" {
		t.Fatalf("newest = %+v", newest)
	}
	if older.GetFrom() != "bob@x.org" || older.GetTo() != "info@alpha.com" || older.GetSubject() != "Hello" || older.GetRuleId() != "rule-1" ||
		older.GetMessageId() != "<m1>" || older.GetSpf() != "pass" || older.GetDmarc() != "pass" || !older.GetLastEvent() ||
		!older.GetTime().AsTime().Equal(now.Add(-time.Hour)) {
		t.Fatalf("older = %+v", older)
	}

	// Reaching the limit says older events may exist.
	got, err = list("u1", conn, "za1", now.Add(-2*time.Hour), now, 1)
	if err != nil || len(got.GetLogs()) != 1 || !got.GetTruncated() {
		t.Fatalf("limit 1 = %+v, %v", got, err)
	}

	// Reading logs is not a change: nothing is sent as a write or logged.
	if w := f.cf.Writes(); len(w) != 0 || len(resourceOps(t, f, conn)) != 0 {
		t.Fatalf("writes = %+v, operations = %+v", w, resourceOps(t, f, conn))
	}

	_, err = list("u1", conn, "zb1", now.Add(-2*time.Hour), now, 0)
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
	_, err = list("u2", conn, "za1", now.Add(-2*time.Hour), now, 0)
	wantCode(t, "connection of another user", err, connect.CodeNotFound)
	_, err = list("u1", conn, "za1", now.Add(-40*24*time.Hour), now, 0)
	wantCode(t, "older than Cloudflare keeps", err, connect.CodeInvalidArgument)
	_, err = list("u1", conn, "za1", now, now.Add(-time.Hour), 0)
	wantCode(t, "end before start", err, connect.CodeInvalidArgument)

	// A token without Analytics Read gets a permission error naming it.
	noAnalytics := f.connect(t, "u1", cfAccA, "email-read")
	_, err = list("u1", noAnalytics, "za1", now.Add(-time.Hour), now, 0)
	wantCode(t, "without Analytics Read", err, connect.CodePermissionDenied)
	if !strings.Contains(err.Error(), "Analytics Read") {
		t.Fatalf("error does not name the permission: %v", err)
	}
}

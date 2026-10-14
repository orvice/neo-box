package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

func mailB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

type mailFixture struct {
	*gmailFixture
	mu      sync.Mutex
	mailHit []string
	opened  []string
}

func (f *mailFixture) hits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.mailHit...)
}

// newMailFixture reuses the Directory side of the Gmail settings fixture and
// serves a mailbox whose content must never reach the operation log.
func newMailFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, path string) bool) *mailFixture {
	t.Helper()
	f := &mailFixture{gmailFixture: newGmailFixture(t, nil)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.mailHit = append(f.mailHit, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("mail reading sent %s %s", r.Method, r.URL.Path)
		}
		path := strings.TrimPrefix(r.URL.Path, "/users/ada@a.example")
		if handler != nil && handler(w, r, path) {
			return
		}
		switch {
		case path == "/labels":
			fmt.Fprint(w, `{"labels":[{"id":"INBOX","name":"INBOX","type":"system"},{"id":"Label_1","name":"SECRET-LABEL","type":"user"}]}`)
		case strings.HasPrefix(path, "/labels/"):
			fmt.Fprint(w, `{"messagesTotal":7,"messagesUnread":2}`)
		case path == "/messages":
			fmt.Fprint(w, `{"messages":[{"id":"m1","threadId":"t1"}],"resultSizeEstimate":1}`)
		case path == "/messages/m1" && r.URL.Query().Get("format") == "metadata":
			fmt.Fprint(w, `{"id":"m1","threadId":"t1","labelIds":["INBOX"],"snippet":"SECRET-SNIPPET","internalDate":"1760000000000","payload":{"mimeType":"text/plain","headers":[{"name":"From","value":"boss@secret.test"},{"name":"Subject","value":"SECRET-SUBJECT"}]}}`)
		case path == "/messages/m1":
			fmt.Fprintf(w, `{"id":"m1","threadId":"t1","internalDate":"1760000000000","payload":{"mimeType":"multipart/mixed","headers":[{"name":"From","value":"boss@secret.test"},{"name":"Subject","value":"SECRET-SUBJECT"}],"parts":[{"partId":"0","mimeType":"text/plain","body":{"data":%q}},{"partId":"1","mimeType":"application/pdf","filename":"SECRET.pdf","body":{"attachmentId":"A1","size":11}},{"partId":"2","mimeType":"application/zip","filename":"big.zip","body":{"attachmentId":"A2","size":99999999}}]}}`, mailB64("SECRET-BODY"))
		case path == "/messages/m1/attachments/A1":
			fmt.Fprintf(w, `{"size":11,"data":%q}`, mailB64("PDF-CONTENT"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	f.srv.newMail = func(_ context.Context, _ googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, email, reason string) (mailAPI, error) {
		if sec.ServiceAccountJSON != "KEY-SECRET" || customer != "Ca" {
			t.Errorf("wrong credential or tenant: %q %q", sec.ServiceAccountJSON, customer)
		}
		f.mu.Lock()
		f.opened = append(f.opened, email)
		f.mu.Unlock()
		c := googleworkspace.NewMailClientForHTTP(srv.Client(), srv.URL, customer, email)
		c.Reason = reason
		return c, nil
	}
	return f
}

type mailCall struct {
	name string
	run  func(f *mailFixture, user string, c *neoboxv1.GmailAccessConfirmation) error
}

func mailCalls() []mailCall {
	return []mailCall{
		{"labels", func(f *mailFixture, user string, c *neoboxv1.GmailAccessConfirmation) error {
			_, err := f.srv.ListWorkspaceUserGmailLabels(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceUserGmailLabelsRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c}))
			return err
		}},
		{"search", func(f *mailFixture, user string, c *neoboxv1.GmailAccessConfirmation) error {
			_, err := f.srv.SearchWorkspaceUserGmailMessages(asUser("owner"), connect.NewRequest(&neoboxv1.SearchWorkspaceUserGmailMessagesRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c, Query: "from:boss"}))
			return err
		}},
		{"message", func(f *mailFixture, user string, c *neoboxv1.GmailAccessConfirmation) error {
			_, err := f.srv.GetWorkspaceUserGmailMessage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailMessageRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c, MessageId: "m1"}))
			return err
		}},
		{"attachment", func(f *mailFixture, user string, c *neoboxv1.GmailAccessConfirmation) error {
			_, err := f.srv.DownloadWorkspaceUserGmailAttachment(asUser("owner"), connect.NewRequest(&neoboxv1.DownloadWorkspaceUserGmailAttachmentRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c, MessageId: "m1", PartId: "1"}))
			return err
		}},
	}
}

func TestMailRejectedBeforeAnyTokenIsRequested(t *testing.T) {
	f := newMailFixture(t, nil)
	long := strings.Repeat("x", 201)
	cases := []struct {
		name string
		user string
		c    *neoboxv1.GmailAccessConfirmation
		code connect.Code
	}{
		{"no confirmation", "ada", nil, connect.CodeInvalidArgument},
		{"blank reason", "ada", &neoboxv1.GmailAccessConfirmation{Reason: " \t ", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"reason too long", "ada", &neoboxv1.GmailAccessConfirmation{Reason: long, TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"multi-line reason", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "a\nb", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"missing target", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r"}, connect.CodeInvalidArgument},
		{"wrong target", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "eve@a.example"}, connect.CodeInvalidArgument},
		{"alias typed instead of primary", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "ada.l@a.example"}, connect.CodeInvalidArgument},
		{"user of another customer", "bob", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "bob@b.example"}, connect.CodeNotFound},
		{"no user", "", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
	}
	for _, call := range mailCalls() {
		for _, tc := range cases {
			if err := call.run(f, tc.user, tc.c); connect.CodeOf(err) != tc.code {
				t.Errorf("%s / %s: err=%v", call.name, tc.name, err)
			}
		}
	}
	if len(f.opened) != 0 || len(f.hits()) != 0 {
		t.Fatalf("token requested or Gmail reached: %v %v", f.opened, f.hits())
	}
	if len(f.rows.ops) != 0 {
		t.Fatal("a refused request was logged as an access")
	}
	// Another Neo Box user's connection stays unreachable.
	_, err := f.srv.SearchWorkspaceUserGmailMessages(asUser("other"), connect.NewRequest(&neoboxv1.SearchWorkspaceUserGmailMessagesRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: f.confirm("ada@a.example")}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other owner=%v", err)
	}
}

func TestMailInvalidArgumentsCostNoGoogleCall(t *testing.T) {
	f := newMailFixture(t, nil)
	c := f.confirm("ada@a.example")
	search := func(m *neoboxv1.SearchWorkspaceUserGmailMessagesRequest) error {
		m.ConnectionId, m.TenantId, m.UserId, m.Confirmation = f.conn, "a", "ada", c
		_, err := f.srv.SearchWorkspaceUserGmailMessages(asUser("owner"), connect.NewRequest(m))
		return err
	}
	for name, m := range map[string]*neoboxv1.SearchWorkspaceUserGmailMessagesRequest{
		"long query":  {Query: strings.Repeat("a", 1001)},
		"page size":   {PageSize: 51},
		"label":       {LabelIds: []string{"no spaces"}},
		"page token":  {PageToken: "no spaces"},
		"newline":     {Query: "a\nb"},
		"many labels": {LabelIds: make([]string, 11)},
		"negative":    {PageSize: -5},
	} {
		if err := search(m); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	for _, id := range []string{"", "../x", "a b", strings.Repeat("a", 65)} {
		_, err := f.srv.GetWorkspaceUserGmailMessage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailMessageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: id}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("message id %q: err=%v", id, err)
		}
	}
	for _, part := range []string{"", "x", "1/2", "1."} {
		_, err := f.srv.DownloadWorkspaceUserGmailAttachment(asUser("owner"), connect.NewRequest(&neoboxv1.DownloadWorkspaceUserGmailAttachmentRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: "m1", PartId: part}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("part id %q: err=%v", part, err)
		}
	}
	if len(f.opened) != 0 || len(f.hits()) != 0 || len(f.rows.ops) != 0 {
		t.Fatalf("invalid arguments reached Google or the log: %v %v %d", f.opened, f.hits(), len(f.rows.ops))
	}
}

func TestMailAccessesAreEachRecordedWithReasonAndNoContent(t *testing.T) {
	f := newMailFixture(t, nil)
	c := f.confirm(" ADA@a.example ")

	labels, err := f.srv.ListWorkspaceUserGmailLabels(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceUserGmailLabelsRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada.l@a.example", Confirmation: c}))
	if err != nil || len(labels.Msg.Labels) != 2 || !labels.Msg.Labels[0].CountsKnown || labels.Msg.Labels[0].MessagesTotal != 7 || labels.Msg.UserEmail != "ada@a.example" {
		t.Fatalf("labels=%v %v", labels, err)
	}

	found, err := f.srv.SearchWorkspaceUserGmailMessages(asUser("owner"), connect.NewRequest(&neoboxv1.SearchWorkspaceUserGmailMessagesRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, Query: "from:boss@secret.test SECRET-WORDS", LabelIds: []string{"INBOX"}}))
	if err != nil || len(found.Msg.Messages) != 1 || found.Msg.Messages[0].Subject != "SECRET-SUBJECT" || found.Msg.Messages[0].Date == nil || found.Msg.ResultSizeEstimate != 1 {
		t.Fatalf("search=%v %v", found, err)
	}

	msg, err := f.srv.GetWorkspaceUserGmailMessage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailMessageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: "m1"}))
	if err != nil || msg.Msg.Message.PlainText != "SECRET-BODY" || len(msg.Msg.Message.Attachments) != 2 {
		t.Fatalf("message=%v %v", msg, err)
	}
	pdf, zip := msg.Msg.Message.Attachments[0], msg.Msg.Message.Attachments[1]
	if pdf.PartId != "1" || pdf.TooLarge || !pdf.Downloadable || !zip.TooLarge {
		t.Fatalf("attachments=%v", msg.Msg.Message.Attachments)
	}

	file, err := f.srv.DownloadWorkspaceUserGmailAttachment(asUser("owner"), connect.NewRequest(&neoboxv1.DownloadWorkspaceUserGmailAttachmentRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: "m1", PartId: "1"}))
	if err != nil || string(file.Msg.Data) != "PDF-CONTENT" || file.Msg.Filename != "SECRET.pdf" || file.Msg.MimeType != "application/pdf" {
		t.Fatalf("download=%v %v", file, err)
	}

	// One token per call, always for the primary email, with the reason.
	if len(f.opened) != 4 {
		t.Fatalf("opened=%v", f.opened)
	}
	for _, e := range f.opened {
		if e != "ada@a.example" {
			t.Fatalf("token subject %q", e)
		}
	}
	actions := map[string]bool{}
	for _, o := range f.rows.ops {
		actions[o.Action] = true
		raw, _ := json.Marshal(o)
		if o.Status != "succeeded" || o.CustomerID != "Ca" || o.ActorID != "owner" || o.Requested["reason"] != "offboarding check" || o.Requested["email"] != "ada@a.example" {
			t.Fatalf("operation=%s", raw)
		}
		for _, leaked := range []string{"SECRET", "boss@", ".pdf", "A1"} {
			if strings.Contains(string(raw), leaked) {
				t.Fatalf("operation holds %q: %s", leaked, raw)
			}
		}
	}
	for _, want := range []string{googleworkspace.ActionGmailLabelsList, googleworkspace.ActionGmailMessagesSearch, googleworkspace.ActionGmailMessageRead, googleworkspace.ActionGmailAttachmentDownload} {
		if !actions[want] {
			t.Errorf("no %s operation in %v", want, actions)
		}
	}
	// Mail accesses are listed with the tenant's other operations, and the
	// existing search finds them by "gmail".
	list, err := f.srv.ListWorkspaceOperations(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceOperationsRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || len(list.Msg.Operations) != 4 {
		t.Fatalf("list=%v %v", list, err)
	}
}

func TestMailRefusedWhenTheAccessCannotBeRecorded(t *testing.T) {
	f := newMailFixture(t, nil)
	f.srv.SetDeps(&failingOperationRepo{workspaceTestRepo: f.rows, failBegin: true}, f.srv.conns)
	for _, call := range mailCalls() {
		if err := call.run(f, "ada", f.confirm("ada@a.example")); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("%s: err=%v", call.name, err)
		}
	}
	if len(f.hits()) != 0 {
		t.Fatalf("Gmail reached without a record: %v", f.hits())
	}
}

func TestMailMissingAuthorizationIsReportedBesideTheFeature(t *testing.T) {
	f := newMailFixture(t, nil)
	f.srv.newMail = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, email, reason string) (mailAPI, error) {
		c := googleworkspace.NewMailClientForHTTP(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, googleworkspace.ErrDelegation })}, "http://invalid", customer, email)
		c.Reason = reason
		return c, nil
	}
	before := f.rows.rows["a"].Status
	for _, call := range mailCalls() {
		err := call.run(f, "ada", f.confirm("ada@a.example"))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.GmailReadonlyScope) {
			t.Errorf("%s: err=%v", call.name, err)
		}
	}
	if f.rows.rows["a"].Status != before {
		t.Fatal("a missing Gmail scope changed the tenant's health")
	}
	for _, o := range f.rows.ops {
		if o.Status != "failed" {
			t.Fatalf("operation=%+v", o)
		}
	}
}

func TestMailMissingMessageAndOversizedAttachmentHaveOwnCodes(t *testing.T) {
	f := newMailFixture(t, nil)
	c := f.confirm("ada@a.example")
	_, err := f.srv.GetWorkspaceUserGmailMessage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailMessageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: "gone"}))
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "message") {
		t.Fatalf("missing message=%v", err)
	}
	get := func(part string) error {
		_, err := f.srv.DownloadWorkspaceUserGmailAttachment(asUser("owner"), connect.NewRequest(&neoboxv1.DownloadWorkspaceUserGmailAttachmentRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, MessageId: "m1", PartId: part}))
		return err
	}
	if err := get("2"); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "25 MB") {
		t.Fatalf("oversized=%v", err)
	}
	for _, h := range f.hits() {
		if strings.Contains(h, "/attachments/") {
			t.Fatalf("an oversized attachment was fetched: %v", f.hits())
		}
	}
	if err := get("7"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing part=%v", err)
	}
	for _, o := range f.rows.ops {
		if o.Status == "pending" {
			t.Fatalf("an access was left pending: %+v", o)
		}
	}
}

func TestMailMessageThatGmailWillNotGiveStaysInTheListAsUnavailable(t *testing.T) {
	f := newMailFixture(t, func(w http.ResponseWriter, r *http.Request, path string) bool {
		if path == "/messages/m1" {
			w.WriteHeader(500)
			return true
		}
		return false
	})
	res, err := f.srv.SearchWorkspaceUserGmailMessages(asUser("owner"), connect.NewRequest(&neoboxv1.SearchWorkspaceUserGmailMessagesRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: f.confirm("ada@a.example")}))
	if err != nil || len(res.Msg.Messages) != 1 || !res.Msg.Messages[0].Unavailable || res.Msg.Messages[0].Id != "m1" {
		t.Fatalf("res=%v %v", res, err)
	}
}

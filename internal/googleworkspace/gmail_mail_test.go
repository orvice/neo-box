package googleworkspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// mailFixture serves a Gmail API for the user ada@a.example whose content must
// never reach the operation log.
type mailFixture struct {
	client *MailClient
	rec    *testWriteRecorder
	mu     sync.Mutex
	hits   []string
}

func (f *mailFixture) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func newMailFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, path string) bool) *mailFixture {
	t.Helper()
	f := &mailFixture{rec: &testWriteRecorder{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("mail reading sent %s %s", r.Method, r.URL.Path)
		}
		prefix := "/users/" + gmailUser
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("request outside the target user: %s", r.URL.Path)
		}
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if handler != nil && handler(w, r, path) {
			return
		}
		switch {
		case path == "/labels":
			fmt.Fprint(w, `{"labels":[{"id":"INBOX","name":"INBOX","type":"system"},{"id":"Label_1","name":"SECRET-LABEL","type":"user"},{"id":"Label_2","name":"Broken","type":"user"}]}`)
		case path == "/labels/INBOX":
			fmt.Fprint(w, `{"id":"INBOX","messagesTotal":10,"messagesUnread":3}`)
		case path == "/labels/Label_1":
			fmt.Fprint(w, `{"id":"Label_1","messagesTotal":2,"messagesUnread":0}`)
		case path == "/labels/Label_2":
			w.WriteHeader(500)
		case path == "/messages":
			fmt.Fprint(w, `{"messages":[{"id":"m1","threadId":"t1"},{"id":"m2","threadId":"t2"},{"id":"m3","threadId":"t3"}],"nextPageToken":"NEXT","resultSizeEstimate":42}`)
		case path == "/messages/m1" && r.URL.Query().Get("format") == "metadata":
			fmt.Fprint(w, `{"id":"m1","threadId":"t1","labelIds":["INBOX","UNREAD"],"snippet":"SECRET-SNIPPET","internalDate":"1760000000000","payload":{"mimeType":"multipart/mixed","headers":[{"name":"From","value":"=?UTF-8?B?QWRhIEzDtnZlbGFjZQ==?= <ada@x.test>"},{"name":"To","value":"me@a.example"},{"name":"Subject","value":"SECRET-SUBJECT"},{"name":"Date","value":"Tue, 14 Oct 2025 08:00:00 +0000"}]}}`)
		case path == "/messages/m2" && r.URL.Query().Get("format") == "metadata":
			w.WriteHeader(500)
		case path == "/messages/m3" && r.URL.Query().Get("format") == "metadata":
			fmt.Fprint(w, `{"id":"m3","threadId":"t3","labelIds":["INBOX"],"internalDate":"1760000100000","payload":{"mimeType":"text/plain","headers":[{"name":"Subject","value":"plain"}]}}`)
		case path == "/messages/full" && r.URL.Query().Get("format") == "full":
			fmt.Fprint(w, fullMessage)
		case strings.HasPrefix(path, "/messages/full/attachments/"):
			fmt.Fprintf(w, `{"size":11,"data":%q}`, b64("PDF-CONTENT"))
		case strings.HasPrefix(path, "/messages/big/attachments/"):
			t.Errorf("an oversized attachment was fetched: %s", path)
			w.WriteHeader(500)
		case path == "/messages/big" && r.URL.Query().Get("format") == "full":
			fmt.Fprint(w, `{"id":"big","payload":{"mimeType":"multipart/mixed","parts":[{"partId":"1","mimeType":"application/zip","filename":"huge.zip","body":{"attachmentId":"A1","size":27000000}}]}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	f.client = NewMailClientForHTTP(srv.Client(), srv.URL, "Ca", gmailUser)
	f.client.Reason = "offboarding check"
	f.client.SetWriteRecorder(f.rec)
	return f
}

var fullMessage = `{"id":"full","threadId":"tf","labelIds":["INBOX"],"snippet":"s","internalDate":"1760000000000","payload":{
 "partId":"","mimeType":"multipart/mixed","headers":[
  {"name":"From","value":"ada@x.test"},{"name":"To","value":"me@a.example"},{"name":"Cc","value":"cc@a.example"},
  {"name":"Subject","value":"=?UTF-8?Q?Caf=C3=A9?="},{"name":"Date","value":"Tue, 14 Oct 2025 08:00:00 +0000"}],
 "parts":[
  {"partId":"0","mimeType":"multipart/alternative","parts":[
    {"partId":"0.0","mimeType":"text/plain","headers":[{"name":"Content-Type","value":"text/plain; charset=ISO-8859-1"}],"body":{"size":9,"data":"` + base64.URLEncoding.EncodeToString([]byte("caf\xe9 SECRET-BODY")) + `"}},
    {"partId":"0.1","mimeType":"text/html","body":{"data":"` + b64("<p>Hello <b>html</b></p>") + `"}}]},
  {"partId":"1","mimeType":"application/pdf","filename":"SECRET-FILE.pdf","body":{"attachmentId":"ATT-ONE","size":11}},
  {"partId":"2","mimeType":"image/png","filename":"logo.png","headers":[{"name":"Content-ID","value":"<logo>"},{"name":"Content-Disposition","value":"inline; filename=logo.png"}],"body":{"attachmentId":"ATT-TWO","size":5}}
 ]}}`

func (f *mailFixture) logJSON() string {
	raw, _ := json.Marshal([]any{f.rec.writes, f.rec.results})
	return string(raw)
}

func TestMailSearchIsLoggedFirstAndLogsOperatorNamesNotQueryText(t *testing.T) {
	f := newMailFixture(t, nil)
	f.client.transport.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if len(f.rec.writes) != 1 {
			t.Error("Gmail request sent before the search was recorded")
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	page, err := f.client.SearchMessages(context.Background(), MailSearch{Query: `from:boss@secret.test has:attachment "SECRET-WORDS" newer_than:7d`, LabelIDs: []string{"INBOX"}, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 || page.NextPageToken != "NEXT" || page.ResultSizeEstimate != 42 {
		t.Fatalf("page=%+v", page)
	}
	m1, m2, m3 := page.Messages[0], page.Messages[1], page.Messages[2]
	if m1.From != "Ada Lövelace <ada@x.test>" || m1.Subject != "SECRET-SUBJECT" || !m1.HasAttachment || m1.Date.IsZero() || m1.Unavailable {
		t.Fatalf("m1=%+v", m1)
	}
	if !m2.Unavailable || m2.ID != "m2" || m2.Subject != "" {
		t.Fatalf("a message whose headers failed must be unavailable, not dropped: %+v", m2)
	}
	if m3.HasAttachment || m3.Date.IsZero() {
		t.Fatalf("m3=%+v", m3)
	}

	if len(f.rec.writes) != 1 || f.rec.writes[0].Action != ActionGmailMessagesSearch {
		t.Fatalf("writes=%+v", f.rec.writes)
	}
	req := f.rec.writes[0].Requested
	if req["reason"] != "offboarding check" || req["email"] != gmailUser {
		t.Fatalf("requested=%v", req)
	}
	if !reflect.DeepEqual(req["operators"], []string{"from", "has", "newer_than"}) || req["label_filter"] != true || req["page_size"] != 3 {
		t.Fatalf("requested=%v", req)
	}
	if res := f.rec.results[0]; res["results"] != 3 || res["unavailable"] != 1 {
		t.Fatalf("result=%v", res)
	}
	log := f.logJSON()
	for _, secret := range []string{"SECRET", "boss@", "x.test", "me@a.example", "INBOX"} {
		if strings.Contains(log, secret) {
			t.Fatalf("log contains %q: %s", secret, log)
		}
	}
	// The first request is the list, with the query and page size passed on.
	first := f.paths()[0]
	u, _ := url.Parse(strings.TrimPrefix(first, "GET "))
	if u.Query().Get("maxResults") != "3" || !strings.Contains(u.Query().Get("q"), "SECRET-WORDS") || u.Query().Get("labelIds") != "INBOX" {
		t.Fatalf("list request %s", first)
	}
}

func TestQueryOperatorsListsOnlyKnownNames(t *testing.T) {
	cases := map[string][]string{
		"":                                       {},
		"hello world":                            {},
		`from:a@b.test`:                          {"from"},
		`FROM:a@b.test subject:"x" -label:x`:     {"from", "label", "subject"},
		`(has:attachment OR is:unread) in:inbox`: {"has", "in", "is"},
		`secretword:value http://x.test/a:b`:     {},
		`{from:a to:b}`:                          {"from", "to"},
	}
	for q, want := range cases {
		if got := QueryOperators(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", q, got, want)
		}
	}
}

func TestMailSearchValidationRejectsBeforeAnythingIsRecordedOrSent(t *testing.T) {
	f := newMailFixture(t, nil)
	bad := []MailSearch{
		{Query: strings.Repeat("a", MaxMailQueryRunes+1)},
		{Query: "a\nb"},
		{LabelIDs: []string{"has space"}},
		{LabelIDs: make([]string, 11)},
		{PageToken: "bad token!"},
		{PageSize: 51},
		{PageSize: -1},
	}
	for _, s := range bad {
		if _, err := f.client.SearchMessages(context.Background(), s); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	if len(f.rec.writes) != 0 || len(f.paths()) != 0 {
		t.Fatalf("recorded %d, sent %v", len(f.rec.writes), f.paths())
	}
}

func TestMailNothingIsSentWhenTheRecordCannotBeStored(t *testing.T) {
	f := newMailFixture(t, nil)
	f.rec.beginErr = errors.New("db down")
	if _, err := f.client.SearchMessages(context.Background(), MailSearch{}); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := f.client.ReadMessage(context.Background(), "full"); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := f.client.DownloadAttachment(context.Background(), "full", "1"); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := f.client.ListLabels(context.Background()); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if len(f.paths()) != 0 {
		t.Fatalf("sent %v", f.paths())
	}
	f.client.Recorder = nil
	if _, err := f.client.ReadMessage(context.Background(), "full"); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("without a recorder: %v", err)
	}
}

func TestMailLabelsKeepAnUnreadableCountUnknownAndLogOnlyTheCount(t *testing.T) {
	f := newMailFixture(t, nil)
	res, err := f.client.ListLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Labels) != 3 {
		t.Fatalf("labels=%+v", res.Labels)
	}
	if res.Labels[0].ID != "INBOX" || res.Labels[0].Type != "system" || !res.Labels[0].CountsKnown || res.Labels[0].MessagesTotal != 10 || res.Labels[0].MessagesUnread != 3 {
		t.Fatalf("inbox=%+v", res.Labels[0])
	}
	var broken MailLabel
	for _, l := range res.Labels {
		if l.ID == "Label_2" {
			broken = l
		}
	}
	if broken.CountsKnown || broken.MessagesTotal != 0 || broken.Type != "user" {
		t.Fatalf("a label whose counts failed must be unknown: %+v", broken)
	}
	if f.rec.writes[0].Action != ActionGmailLabelsList || f.rec.results[0]["labels"] != 3 {
		t.Fatalf("log=%s", f.logJSON())
	}
	if log := f.logJSON(); strings.Contains(log, "SECRET-LABEL") || strings.Contains(log, "Broken") {
		t.Fatalf("log names labels: %s", log)
	}
}

func TestMailReadParsesTheMessageAndKeepsContentOutOfTheLog(t *testing.T) {
	f := newMailFixture(t, nil)
	m, err := f.client.ReadMessage(context.Background(), "full")
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Café" || m.From != "ada@x.test" || m.Cc != "cc@a.example" || m.Date.IsZero() {
		t.Fatalf("headers: %+v", m)
	}
	if m.PlainText != "café SECRET-BODY" || m.PlainTextFromHTML {
		t.Fatalf("plain text must be decoded from its declared charset: %q", m.PlainText)
	}
	if m.HTML != "<p>Hello <b>html</b></p>" {
		t.Fatalf("html=%q", m.HTML)
	}
	if len(m.Attachments) != 2 {
		t.Fatalf("attachments=%+v", m.Attachments)
	}
	a, logo := m.Attachments[0], m.Attachments[1]
	if a.PartID != "1" || a.Filename != "SECRET-FILE.pdf" || a.MimeType != "application/pdf" || a.Size != 11 || a.Inline || !a.Downloadable {
		t.Fatalf("a=%+v", a)
	}
	if !logo.Inline || logo.PartID != "2" {
		t.Fatalf("logo=%+v", logo)
	}
	if f.rec.writes[0].Action != ActionGmailMessageRead || f.rec.writes[0].Requested["message_id"] != "full" {
		t.Fatalf("writes=%+v", f.rec.writes)
	}
	if res := f.rec.results[0]; res["attachments"] != 2 || res["has_html"] != true || res["message_id"] != "full" {
		t.Fatalf("result=%v", res)
	}
	log := f.logJSON()
	for _, secret := range []string{"SECRET", "Café", "ada@x.test", "me@a.example", "cc@a.example", ".pdf"} {
		if strings.Contains(log, secret) {
			t.Fatalf("log contains %q: %s", secret, log)
		}
	}
	// Reading never changes the mailbox: only GETs were sent (the fixture
	// fails the test on anything else), and the format is read-only.
	if got := f.paths(); len(got) != 1 || !strings.HasPrefix(got[0], "GET /users/"+gmailUser+"/messages/full?") {
		t.Fatalf("paths=%v", got)
	}
}

func TestMailReadFallsBackToTextMadeFromHTMLAndReportsAMissingMessage(t *testing.T) {
	f := newMailFixture(t, func(w http.ResponseWriter, r *http.Request, path string) bool {
		switch path {
		case "/messages/htmlonly":
			fmt.Fprintf(w, `{"id":"htmlonly","payload":{"mimeType":"text/html","body":{"data":%q}}}`, b64(`<html><head><style>p{}</style><title>T</title></head><body><script>alert(1)</script><p>Hello&nbsp;&amp; welcome</p><div>second<br>line</div></body></html>`))
			return true
		case "/messages/gone":
			w.WriteHeader(404)
			return true
		}
		return false
	})
	m, err := f.client.ReadMessage(context.Background(), "htmlonly")
	if err != nil {
		t.Fatal(err)
	}
	if !m.PlainTextFromHTML || m.PlainText != "Hello & welcome\nsecond\nline" || strings.Contains(m.PlainText, "alert") {
		t.Fatalf("plain=%q fromHTML=%v", m.PlainText, m.PlainTextFromHTML)
	}
	if _, err := f.client.ReadMessage(context.Background(), "gone"); !errors.Is(err, ErrMailNotFound) {
		t.Fatalf("err=%v", err)
	}
	if f.rec.outcomes[1] == nil {
		t.Fatal("a failed read must be recorded as failed")
	}
	if _, err := f.client.ReadMessage(context.Background(), "../x"); err == nil {
		t.Fatal("accepted an invalid message ID")
	}
}

func TestMailAttachmentIsFoundByPartAndLoggedByTypeAndSize(t *testing.T) {
	f := newMailFixture(t, nil)
	file, err := f.client.DownloadAttachment(context.Background(), "full", "1")
	if err != nil {
		t.Fatal(err)
	}
	if string(file.Data) != "PDF-CONTENT" || file.Filename != "SECRET-FILE.pdf" || file.MimeType != "application/pdf" {
		t.Fatalf("file=%+v", file)
	}
	// The message is read again for a fresh attachment ID, then the part fetched.
	got := f.paths()
	if len(got) != 2 || !strings.Contains(got[0], "/messages/full?") || !strings.Contains(got[1], "/messages/full/attachments/ATT-ONE") {
		t.Fatalf("paths=%v", got)
	}
	if f.rec.writes[0].Action != ActionGmailAttachmentDownload {
		t.Fatalf("writes=%+v", f.rec.writes)
	}
	if res := f.rec.results[0]; res["mime_type"] != "application/pdf" || res["size"] != 11 || res["message_id"] != "full" {
		t.Fatalf("result=%v", res)
	}
	if log := f.logJSON(); strings.Contains(log, "SECRET") || strings.Contains(log, ".pdf") || strings.Contains(log, "ATT-ONE") {
		t.Fatalf("log=%s", log)
	}
	if _, err := f.client.DownloadAttachment(context.Background(), "full", "9"); !errors.Is(err, ErrMailNotFound) {
		t.Fatalf("missing part: %v", err)
	}
	if _, err := f.client.DownloadAttachment(context.Background(), "full", "../1"); err == nil {
		t.Fatal("accepted an invalid part ID")
	}
}

func TestMailAttachmentOverTheLimitIsRefusedBeforeItIsFetched(t *testing.T) {
	f := newMailFixture(t, nil)
	_, err := f.client.DownloadAttachment(context.Background(), "big", "1")
	if !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("err=%v", err)
	}
	for _, p := range f.paths() {
		if strings.Contains(p, "/attachments/") {
			t.Fatalf("fetched: %v", f.paths())
		}
	}
	if f.rec.outcomes[0] == nil {
		t.Fatal("the refusal must be recorded as failed")
	}
}

func TestParsePayloadHandlesNestingAndBadInput(t *testing.T) {
	root := &gmailPart{MimeType: "multipart/mixed", Parts: []gmailPart{
		{PartID: "0", MimeType: "multipart/related", Parts: []gmailPart{
			{PartID: "0.0", MimeType: "multipart/alternative", Parts: []gmailPart{
				{PartID: "0.0.0", MimeType: "text/plain"},
			}},
		}},
		{PartID: "1", MimeType: "message/rfc822", Filename: "forwarded.eml", Parts: []gmailPart{{PartID: "1.0", MimeType: "text/plain"}}},
		{PartID: "2", MimeType: "text/plain", Filename: "notes.txt"},
		{PartID: "3", MimeType: "text/plain", Headers: []gmailHeader{{Name: "Content-Disposition", Value: `attachment; filename="a.txt"`}}},
	}}
	root.Parts[0].Parts[0].Parts[0].Body.Data = "!!!not base64!!!"
	root.Parts[3].Body.AttachmentID = "X"
	p := parsePayload(root)
	if len(p.plain) != 0 || len(p.html) != 0 {
		t.Fatalf("a body that is not valid must be skipped, got %v %v", p.plain, p.html)
	}
	if len(p.attachments) != 3 {
		t.Fatalf("attachments=%+v", p.attachments)
	}
	if p.attachments[0].Filename != "forwarded.eml" || p.attachments[0].Downloadable {
		t.Fatalf("a part without an attachment ID cannot be downloaded: %+v", p.attachments[0])
	}

	deep := &gmailPart{MimeType: "multipart/mixed"}
	cur := deep
	for i := 0; i < 40; i++ {
		cur.Parts = []gmailPart{{MimeType: "multipart/mixed"}}
		cur = &cur.Parts[0]
	}
	cur.Parts = []gmailPart{{MimeType: "text/plain"}}
	cur.Parts[0].Body.Data = b64("too deep")
	if got := parsePayload(deep); len(got.plain) != 0 {
		t.Fatal("nesting beyond the limit must be ignored")
	}
}

func TestDecodeTextReplacesInvalidBytesAndCutsHugeBodies(t *testing.T) {
	if got, _ := decodeText(b64("a\xffb"), ""); got != "a\uFFFDb" {
		t.Fatalf("got %q", got)
	}
	if got, _ := decodeText(b64("x"), "no-such-charset"); got != "x" {
		t.Fatalf("got %q", got)
	}
	huge := strings.Repeat("a", maxBodyBytes+10)
	got, truncated := decodeText(b64(huge), "utf-8")
	if !truncated || len(got) != maxBodyBytes {
		t.Fatalf("truncated=%v len=%d", truncated, len(got))
	}
}

func TestDecodeHeaderStripsControlCharacters(t *testing.T) {
	if got := decodeHeader("a\r\nBcc: x\x00y"); got != "a Bcc: xy" {
		t.Fatalf("got %q", got)
	}
	if got := decodeHeader("=?ISO-8859-1?Q?caf=E9?="); got != "café" {
		t.Fatalf("got %q", got)
	}
}

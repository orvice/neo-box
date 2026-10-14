package googleworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// GmailReadonlyScope is the only scope Neo Box requests to read mail. It can
// read messages, labels and attachments and change nothing. See docs/adr/0010.
const GmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"

// Action names recorded in the operation log.
const (
	ActionGmailLabelsList         = "gmail.labels.list"
	ActionGmailMessagesSearch     = "gmail.messages.search"
	ActionGmailMessageRead        = "gmail.message.read"
	ActionGmailAttachmentDownload = "gmail.attachment.download"
)

const (
	// MaxMailPageSize is the most messages one search page holds.
	MaxMailPageSize     = 50
	defaultMailPageSize = 25
	// MaxMailQueryRunes bounds a Gmail search query.
	MaxMailQueryRunes = 1000
	// MaxAttachmentBytes is Neo Box's own limit on a downloaded attachment.
	MaxAttachmentBytes = 25 << 20
	maxMailLabelFilter = 10
	mailConcurrency    = 8
	// Gmail returns an attachment as base64url inside JSON, about 4/3 of its
	// size; a response over this is refused.
	maxMailResponseBytes = 36 << 20
	mailTimeout          = 2 * time.Minute
)

var (
	// ErrAttachmentTooLarge is returned before any attachment bytes are fetched.
	ErrAttachmentTooLarge = errors.New("attachment is larger than Neo Box's 25 MB limit; take it from Gmail directly")
	// ErrMailNotFound means Gmail has no such message or attachment part.
	ErrMailNotFound = errors.New("message or attachment not found")

	mailIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	mailLabelPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	mailPartPattern  = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,19}$`)
	mailTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_=.-]{1,512}$`)
	operatorPattern  = regexp.MustCompile(`(?i)(?:^|[\s(){}"-])([a-z0-9_]+):`)
)

// mailOperators is the fixed list of Gmail search operators whose names may be
// logged. Their values never are.
var mailOperators = map[string]bool{
	"from": true, "to": true, "cc": true, "bcc": true, "subject": true, "label": true,
	"has": true, "is": true, "in": true, "filename": true, "larger": true, "smaller": true,
	"size": true, "newer_than": true, "older_than": true, "after": true, "before": true,
	"older": true, "newer": true, "category": true, "list": true, "deliveredto": true,
	"rfc822msgid": true, "around": true,
}

// QueryOperators returns the sorted names of the allowlisted Gmail operators
// the query uses. It is what a search is logged by; the query text is not.
func QueryOperators(q string) []string {
	seen := map[string]bool{}
	for _, m := range operatorPattern.FindAllStringSubmatch(q, -1) {
		if name := strings.ToLower(m[1]); mailOperators[name] {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// MailClient reads one user's labels, messages and attachments, as that user,
// with GmailReadonlyScope. Every call is recorded before it is sent; without a
// Recorder nothing is sent. It has no method that changes a mailbox.
type MailClient struct {
	transport *Client
	endpoint  string
	// User is the immutable primary email the token's subject is set to.
	User     string
	Recorder WriteRecorder
	// Reason is the caller's access reason, stored with each operation.
	Reason string
}

// NewMailClient exchanges a token whose subject is the target user and whose
// only scope is GmailReadonlyScope. The caller must already have resolved the
// user inside the tenant.
func NewMailClient(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret, customer, userEmail string) (*MailClient, error) {
	hc, err := impersonatedHTTPClient(ctx, identity, secret, customer, userEmail, GmailReadonlyScope)
	if err != nil {
		return nil, err
	}
	hc.Timeout = mailTimeout
	return NewMailClientForHTTP(hc, gmailURL, customer, userEmail), nil
}

// NewMailClientForHTTP builds a client over an existing HTTP client and
// endpoint, for tests and alternative token sources.
func NewMailClientForHTTP(hc *http.Client, endpoint, customer, userEmail string) *MailClient {
	return &MailClient{transport: &Client{HTTP: hc, Endpoint: endpoint, CustomerID: customer, AdminEmail: userEmail, MaxResponseBytes: maxMailResponseBytes}, endpoint: endpoint, User: userEmail}
}

func (m *MailClient) SetWriteRecorder(r WriteRecorder) { m.Recorder = r }

func (m *MailClient) path(parts ...string) string {
	return "/users/" + url.PathEscape(m.User) + strings.Join(parts, "")
}

func (m *MailClient) get(ctx context.Context, path string, q url.Values, out any) error {
	return m.transport.doAt(ctx, m.endpoint, http.MethodGet, path, q, nil, out)
}

func (m *MailClient) record(ctx context.Context, w Write, run func() (map[string]any, error)) error {
	return recordAccess(ctx, m.Recorder, m.User, m.Reason, w, run)
}

// fatalMailErr reports the errors that end a multi-request read: a delegation
// failure or a cancelled request. Anything else only affects one item.
func fatalMailErr(err error) bool {
	return errors.Is(err, ErrDelegation) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// forEach runs fn for every index with bounded concurrency and returns the
// first fatal error.
func forEach(ctx context.Context, n int, fn func(i int) error) error {
	sem := make(chan struct{}, mailConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fatal error
	for i := 0; i < n; i++ {
		mu.Lock()
		stop := fatal != nil
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			if err := fn(i); err != nil && fatalMailErr(err) {
				mu.Lock()
				if fatal == nil {
					fatal = err
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if fatal != nil {
		return fatal
	}
	return ctx.Err()
}

// ---- Labels ----

type MailLabel struct {
	ID, Name string
	// Type is "system" or "user".
	Type string
	// Counts are only valid when CountsKnown; a label whose counts could not
	// be read has unknown, not zero, mail.
	CountsKnown                   bool
	MessagesTotal, MessagesUnread int
}

type MailLabels struct {
	Labels []MailLabel
	ReadAt time.Time
}

// ListLabels lists the mailbox's labels with their message counts as one
// logged operation. A label whose counts can't be read is returned without
// them.
func (m *MailClient) ListLabels(ctx context.Context) (*MailLabels, error) {
	var out *MailLabels
	err := m.record(ctx, Write{Method: http.MethodGet, Path: "/gmail/labels", Action: ActionGmailLabelsList}, func() (map[string]any, error) {
		var list struct {
			Labels []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"labels"`
		}
		if err := m.get(ctx, m.path("/labels"), nil, &list); err != nil {
			return nil, err
		}
		labels := make([]MailLabel, len(list.Labels))
		for i, l := range list.Labels {
			kind := "user"
			if l.Type == "system" {
				kind = "system"
			}
			labels[i] = MailLabel{ID: l.ID, Name: l.Name, Type: kind}
		}
		err := forEach(ctx, len(labels), func(i int) error {
			if !mailLabelPattern.MatchString(labels[i].ID) {
				return nil
			}
			var one struct {
				MessagesTotal  int `json:"messagesTotal"`
				MessagesUnread int `json:"messagesUnread"`
			}
			if err := m.get(ctx, m.path("/labels/", url.PathEscape(labels[i].ID)), nil, &one); err != nil {
				return err
			}
			labels[i].CountsKnown, labels[i].MessagesTotal, labels[i].MessagesUnread = true, one.MessagesTotal, one.MessagesUnread
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.SliceStable(labels, func(a, b int) bool {
			if labels[a].Type != labels[b].Type {
				return labels[a].Type == "system"
			}
			return strings.ToLower(labels[a].Name) < strings.ToLower(labels[b].Name)
		})
		out = &MailLabels{Labels: labels, ReadAt: time.Now().UTC()}
		return map[string]any{"labels": len(labels)}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- Search ----

// MailSearch is one page of a mailbox search.
type MailSearch struct {
	// Query uses Gmail's search syntax. It may be empty to list recent mail.
	Query            string
	LabelIDs         []string
	IncludeSpamTrash bool
	PageToken        string
	// PageSize is 1..MaxMailPageSize; zero means 25.
	PageSize int
}

// Validate checks what Gmail would otherwise reject, before anything is
// logged or sent.
func (s MailSearch) Validate() error {
	if utf8.RuneCountInString(s.Query) > MaxMailQueryRunes {
		return invalid("query", "is too long")
	}
	for _, r := range s.Query {
		if unicode.IsControl(r) {
			return invalid("query", "must be a single line of text")
		}
	}
	if len(s.LabelIDs) > maxMailLabelFilter {
		return invalid("label_ids", "too many labels")
	}
	for _, id := range s.LabelIDs {
		if !mailLabelPattern.MatchString(id) {
			return invalid("label_ids", "contains an invalid label ID")
		}
	}
	if s.PageToken != "" && !mailTokenPattern.MatchString(s.PageToken) {
		return invalid("page_token", "is not valid")
	}
	if s.PageSize < 0 || s.PageSize > MaxMailPageSize {
		return invalid("page_size", "must be between 1 and 50")
	}
	return nil
}

func (s MailSearch) size() int {
	if s.PageSize == 0 {
		return defaultMailPageSize
	}
	return s.PageSize
}

// MailSummary is one search result. Unavailable is set when the message's
// headers could not be read; the other fields are then empty.
type MailSummary struct {
	ID, ThreadID               string
	From, To, Subject, Snippet string
	Date                       time.Time
	LabelIDs                   []string
	HasAttachment              bool
	Unavailable                bool
}

type MailPage struct {
	Messages []MailSummary
	// NextPageToken is empty on the last page.
	NextPageToken string
	// ResultSizeEstimate is Gmail's estimate of all matches, not a count.
	ResultSizeEstimate int
	ReadAt             time.Time
}

// SearchMessages lists one page of messages and reads each one's headers, as
// one logged operation. The log holds which operators the query used, never the
// query itself.
func (m *MailClient) SearchMessages(ctx context.Context, s MailSearch) (*MailPage, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	requested := map[string]any{
		"operators":          QueryOperators(s.Query),
		"label_filter":       len(s.LabelIDs) > 0,
		"page_size":          s.size(),
		"include_spam_trash": s.IncludeSpamTrash,
		"paged":              s.PageToken != "",
	}
	var out *MailPage
	err := m.record(ctx, Write{Method: http.MethodGet, Path: "/gmail/messages", Action: ActionGmailMessagesSearch, Requested: requested}, func() (map[string]any, error) {
		q := url.Values{"maxResults": {strconv.Itoa(s.size())}}
		if s.Query != "" {
			q.Set("q", s.Query)
		}
		for _, id := range s.LabelIDs {
			q.Add("labelIds", id)
		}
		if s.IncludeSpamTrash {
			q.Set("includeSpamTrash", "true")
		}
		if s.PageToken != "" {
			q.Set("pageToken", s.PageToken)
		}
		var list struct {
			Messages []struct {
				ID       string `json:"id"`
				ThreadID string `json:"threadId"`
			} `json:"messages"`
			NextPageToken      string `json:"nextPageToken"`
			ResultSizeEstimate int    `json:"resultSizeEstimate"`
		}
		if err := m.get(ctx, m.path("/messages"), q, &list); err != nil {
			return nil, err
		}
		rows := make([]MailSummary, len(list.Messages))
		for i, ref := range list.Messages {
			rows[i] = MailSummary{ID: ref.ID, ThreadID: ref.ThreadID, Unavailable: true}
		}
		err := forEach(ctx, len(rows), func(i int) error {
			if !mailIDPattern.MatchString(rows[i].ID) {
				return nil
			}
			mq := url.Values{"format": {"metadata"}, "metadataHeaders": {"From", "To", "Subject", "Date"}}
			var msg gmailMessage
			if err := m.get(ctx, m.path("/messages/", url.PathEscape(rows[i].ID)), mq, &msg); err != nil {
				return err
			}
			rows[i] = MailSummary{
				ID: rows[i].ID, ThreadID: rows[i].ThreadID, LabelIDs: msg.LabelIDs,
				From: decodeHeader(headerValue(msg.Payload.Headers, "From")), To: decodeHeader(headerValue(msg.Payload.Headers, "To")),
				Subject: decodeHeader(headerValue(msg.Payload.Headers, "Subject")), Snippet: strings.ToValidUTF8(msg.Snippet, "\uFFFD"),
				Date: messageDate(msg.Payload.Headers, msg.InternalDate), HasAttachment: hasAttachmentHint(&msg.Payload),
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		unavailable := 0
		for _, r := range rows {
			if r.Unavailable {
				unavailable++
			}
		}
		out = &MailPage{Messages: rows, NextPageToken: list.NextPageToken, ResultSizeEstimate: list.ResultSizeEstimate, ReadAt: time.Now().UTC()}
		return map[string]any{"results": len(rows), "unavailable": unavailable}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- One message ----

// MailMessage is one message read in full. PlainText is empty only when the
// message has no text at all; PlainTextFromHTML says it was made from HTML.
type MailMessage struct {
	ID, ThreadID                        string
	From, To, Cc, Bcc, ReplyTo, Subject string
	Date                                time.Time
	LabelIDs                            []string
	Snippet                             string
	PlainText, HTML                     string
	PlainTextFromHTML                   bool
	BodyTruncated                       bool
	Attachments                         []MailAttachment
	ReadAt                              time.Time
}

func (m *MailClient) readFull(ctx context.Context, id string) (*gmailMessage, error) {
	var msg gmailMessage
	if err := m.get(ctx, m.path("/messages/", url.PathEscape(id)), url.Values{"format": {"full"}}, &msg); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return nil, ErrMailNotFound
		}
		return nil, err
	}
	return &msg, nil
}

// ValidMailID reports whether s can be a Gmail message ID.
func ValidMailID(s string) bool { return mailIDPattern.MatchString(s) }

// ValidMailPartID reports whether s can be a Gmail MIME part ID.
func ValidMailPartID(s string) bool { return mailPartPattern.MatchString(s) }

// ReadMessage reads one message in full as one logged operation. Reading does
// not mark the message read or change its labels.
func (m *MailClient) ReadMessage(ctx context.Context, id string) (*MailMessage, error) {
	if !ValidMailID(id) {
		return nil, invalid("message_id", "is not valid")
	}
	var out *MailMessage
	err := m.record(ctx, Write{Method: http.MethodGet, Path: "/gmail/messages/" + id, Action: ActionGmailMessageRead, Requested: map[string]any{"message_id": id}}, func() (map[string]any, error) {
		msg, err := m.readFull(ctx, id)
		if err != nil {
			return nil, err
		}
		parsed := parsePayload(&msg.Payload)
		h := msg.Payload.Headers
		out = &MailMessage{
			ID: msg.ID, ThreadID: msg.ThreadID, LabelIDs: msg.LabelIDs,
			From: decodeHeader(headerValue(h, "From")), To: decodeHeader(headerValue(h, "To")), Cc: decodeHeader(headerValue(h, "Cc")),
			Bcc: decodeHeader(headerValue(h, "Bcc")), ReplyTo: decodeHeader(headerValue(h, "Reply-To")), Subject: decodeHeader(headerValue(h, "Subject")),
			Date: messageDate(h, msg.InternalDate), Snippet: strings.ToValidUTF8(msg.Snippet, "\uFFFD"),
			PlainText: strings.Join(parsed.plain, "\n\n"), HTML: strings.Join(parsed.html, "\n\n"),
			BodyTruncated: parsed.plainTruncate || parsed.htmlTruncate, Attachments: parsed.attachments, ReadAt: time.Now().UTC(),
		}
		if out.PlainText == "" && out.HTML != "" {
			out.PlainText, out.PlainTextFromHTML = htmlToText(out.HTML), true
		}
		return map[string]any{"message_id": id, "attachments": len(parsed.attachments), "has_html": out.HTML != ""}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- Attachment ----

// MailAttachmentFile is a downloaded attachment, held in memory for one
// response and never stored.
type MailAttachmentFile struct {
	Filename, MimeType string
	Data               []byte
}

// DownloadAttachment fetches one attachment as one logged operation. Gmail's
// attachment IDs change between reads, so the message is read again and the
// part found by its part ID. An attachment declared larger than
// MaxAttachmentBytes is refused before its bytes are fetched.
func (m *MailClient) DownloadAttachment(ctx context.Context, messageID, partID string) (*MailAttachmentFile, error) {
	if !ValidMailID(messageID) {
		return nil, invalid("message_id", "is not valid")
	}
	if !ValidMailPartID(partID) {
		return nil, invalid("part_id", "is not valid")
	}
	var out *MailAttachmentFile
	err := m.record(ctx, Write{Method: http.MethodGet, Path: "/gmail/messages/" + messageID + "/attachments/" + partID, Action: ActionGmailAttachmentDownload, Requested: map[string]any{"message_id": messageID, "part_id": partID}}, func() (map[string]any, error) {
		msg, err := m.readFull(ctx, messageID)
		if err != nil {
			return nil, err
		}
		var found *MailAttachment
		parsed := parsePayload(&msg.Payload)
		for i := range parsed.attachments {
			if parsed.attachments[i].PartID == partID {
				found = &parsed.attachments[i]
				break
			}
		}
		if found == nil || !found.Downloadable {
			return nil, ErrMailNotFound
		}
		if found.Size > MaxAttachmentBytes {
			return map[string]any{"message_id": messageID, "mime_type": found.MimeType, "size": found.Size}, ErrAttachmentTooLarge
		}
		var body struct {
			Size int64  `json:"size"`
			Data string `json:"data"`
		}
		path := m.path("/messages/", url.PathEscape(messageID), "/attachments/", url.PathEscape(found.attachmentID))
		if err := m.get(ctx, path, nil, &body); err != nil {
			return nil, err
		}
		data, err := decodeBase64URL(body.Data)
		if err != nil {
			return nil, errors.New("Gmail returned an attachment that could not be decoded")
		}
		if len(data) > MaxAttachmentBytes {
			return map[string]any{"message_id": messageID, "mime_type": found.MimeType, "size": len(data)}, ErrAttachmentTooLarge
		}
		out = &MailAttachmentFile{Filename: found.Filename, MimeType: found.MimeType, Data: data}
		return map[string]any{"message_id": messageID, "mime_type": found.MimeType, "size": len(data)}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

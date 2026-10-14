package googleworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"
)

// GmailSettingsScope is the only Gmail scope Neo Box requests. It reads and
// changes settings (filters, forwarding, delegates, send-as, vacation) but
// cannot read or change messages. See docs/adr/0008.
const GmailSettingsScope = "https://www.googleapis.com/auth/gmail.settings.basic"

const gmailURL = "https://gmail.googleapis.com/gmail/v1"

// Action names recorded in the operation log.
const (
	ActionGmailSettingsRead   = "gmail.settings.read"
	ActionGmailVacationUpdate = "gmail.vacation.update"
)

const (
	MaxAccessReason  = 200
	maxVacationText  = 20000
	maxVacationTitle = 500
)

// GmailClient reads and changes one user's Gmail settings, as that user. Every
// call is recorded before it is sent, reads included; without a Recorder
// nothing is sent.
type GmailClient struct {
	transport *Client
	endpoint  string
	// User is the immutable primary email the token's subject is set to.
	User string
	// Domains are the tenant's verified domains, used to tell internal from
	// external addresses. Empty means unknown, never "all external".
	Domains  []string
	Recorder WriteRecorder
	// Reason is the caller's access reason, stored with each operation.
	Reason string
}

// NewGmailClient exchanges a token whose subject is the target user and whose
// only scope is GmailSettingsScope. The caller must already have resolved the
// user inside the tenant.
func NewGmailClient(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret, customer, userEmail string) (*GmailClient, error) {
	hc, err := impersonatedHTTPClient(ctx, identity, secret, customer, userEmail, GmailSettingsScope)
	if err != nil {
		return nil, err
	}
	return NewGmailClientForHTTP(hc, gmailURL, customer, userEmail), nil
}

// impersonatedHTTPClient exchanges a token whose subject is userEmail and whose
// only scope is the given one. The token source lives only in the returned
// client and its errors are sanitized.
func impersonatedHTTPClient(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret, customer, userEmail, scope string) (*http.Client, error) {
	metadata, cfg, err := ParseKey(secret.ServiceAccountJSON)
	if err != nil {
		return nil, err
	}
	if *metadata != identity {
		return nil, errors.New("Service Account identity mismatch")
	}
	if !ValidCustomerID(customer) || !ValidEmail(userEmail) {
		return nil, errors.New("invalid Workspace customer or Gmail user")
	}
	cfg.Scopes = []string{scope}
	cfg.Subject = userEmail
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	hc := oauth2.NewClient(ctx, safeTokenSource{cfg.TokenSource(ctx)})
	hc.Timeout = 30 * time.Second
	return hc, nil
}

// NewGmailClientForHTTP builds a client over an existing HTTP client and
// endpoint, for tests and alternative token sources.
func NewGmailClientForHTTP(hc *http.Client, endpoint, customer, userEmail string) *GmailClient {
	return &GmailClient{transport: &Client{HTTP: hc, Endpoint: endpoint, CustomerID: customer, AdminEmail: userEmail}, endpoint: endpoint, User: userEmail}
}

func (g *GmailClient) SetWriteRecorder(r WriteRecorder) { g.Recorder = r }

func (g *GmailClient) path(parts ...string) string {
	return "/users/" + url.PathEscape(g.User) + "/settings" + strings.Join(parts, "")
}

// Section results. Err is a short sentence; empty means the section was read.
type GmailVacation struct {
	Err                                  string
	Enabled                              bool
	Subject, BodyPlainText               string
	BodyIsHTML                           bool
	RestrictToContacts, RestrictToDomain bool
	StartTime, EndTime                   time.Time
}
type GmailAutoForwarding struct {
	Err                       string
	Enabled                   bool
	EmailAddress, Disposition string
	External                  *bool
}
type GmailForwardingAddress struct {
	Email, VerificationStatus string
	External                  *bool
}
type GmailForwardingAddresses struct {
	Err       string
	Addresses []GmailForwardingAddress
}
type GmailDelegate struct{ Email, VerificationStatus string }
type GmailDelegates struct {
	Err       string
	Delegates []GmailDelegate
}
type GmailFilter struct {
	ID, From, To, Subject, Query, NegatedQuery string
	HasAttachment                              bool
	ForwardTo                                  string
	ForwardExternal                            *bool
	Deletes, SkipsInbox                        bool
}
type GmailFilters struct {
	Err     string
	Filters []GmailFilter
}
type GmailSendAs struct {
	Email, DisplayName                 string
	IsPrimary, IsDefault, TreatAsAlias bool
	VerificationStatus                 string
	External                           *bool
}
type GmailSendAsList struct {
	Err       string
	Addresses []GmailSendAs
}
type GmailSettings struct {
	User                string
	Vacation            GmailVacation
	AutoForwarding      GmailAutoForwarding
	ForwardingAddresses GmailForwardingAddresses
	Delegates           GmailDelegates
	Filters             GmailFilters
	SendAs              GmailSendAsList
	ReadAt              time.Time
}

// IsExternal reports whether the address is outside the given domains. It is
// nil, not true, when the domains or the address can't be interpreted.
func IsExternal(address string, domains []string) *bool {
	if len(domains) == 0 {
		return nil
	}
	a, err := mail.ParseAddress(strings.TrimSpace(address))
	if err != nil {
		return nil
	}
	at := strings.LastIndex(a.Address, "@")
	if at < 0 {
		return nil
	}
	host := strings.ToLower(a.Address[at+1:])
	for _, d := range domains {
		if strings.EqualFold(d, host) {
			f := false
			return &f
		}
	}
	t := true
	return &t
}

// sectionError is the sentence shown beside a section that could not be read.
// It never reflects Google's message.
func sectionError(err error) string {
	var api *APIError
	switch {
	case errors.As(err, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusUnauthorized):
		return "Google refused this request; check the tenant's Gmail settings authorization"
	case errors.As(err, &api) && (api.Status == http.StatusBadRequest || api.Status == http.StatusNotFound):
		return "Gmail does not offer this setting for this mailbox"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "the request was cancelled or timed out"
	default:
		return "this section could not be read"
	}
}

// record logs a Gmail request before sending it and its outcome after.
func (g *GmailClient) record(ctx context.Context, w Write, run func() (map[string]any, error)) error {
	return recordAccess(ctx, g.Recorder, g.User, g.Reason, w, run)
}

// recordAccess logs an impersonated or privileged request before it is sent
// and its outcome after, with a context that survives request cancellation.
// Without a recorder, or when the first record cannot be stored, nothing is
// sent. Shared by the Gmail and Drive clients (docs/adr/0008, 0009).
func recordAccess(ctx context.Context, rec WriteRecorder, email, reason string, w Write, run func() (map[string]any, error)) (resultErr error) {
	if rec == nil {
		return ErrLogUnavailable
	}
	if w.Requested == nil {
		w.Requested = map[string]any{}
	}
	w.Requested["email"] = email
	w.Requested["reason"] = reason
	finish, err := rec.Begin(ctx, w)
	if err != nil {
		return ErrLogUnavailable
	}
	result := map[string]any{}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := finish(finishCtx, result, resultErr); err != nil {
			resultErr = ErrOutcomeLogUnavailable
		}
	}()
	var runErr error
	result, runErr = run()
	if result == nil {
		result = map[string]any{}
	}
	return runErr
}

// ReadSettings reads every settings section as one logged operation. A section
// that fails is reported on its own; delegation failing stops the read.
func (g *GmailClient) ReadSettings(ctx context.Context) (*GmailSettings, error) {
	var out *GmailSettings
	err := g.record(ctx, Write{Method: http.MethodGet, Path: "/gmail/settings", Action: ActionGmailSettingsRead}, func() (map[string]any, error) {
		s := &GmailSettings{User: g.User, ReadAt: time.Now().UTC()}
		var delegation error
		note := func(err error) string {
			if err == nil {
				return ""
			}
			if errors.Is(err, ErrDelegation) {
				delegation = err
			}
			return sectionError(err)
		}
		get := func(path string, v any) error {
			return g.transport.doAt(ctx, g.endpoint, http.MethodGet, path, nil, nil, v)
		}

		var vac struct {
			EnableAutoReply       bool   `json:"enableAutoReply"`
			ResponseSubject       string `json:"responseSubject"`
			ResponseBodyPlainText string `json:"responseBodyPlainText"`
			ResponseBodyHTML      string `json:"responseBodyHtml"`
			RestrictToContacts    bool   `json:"restrictToContacts"`
			RestrictToDomain      bool   `json:"restrictToDomain"`
			StartTime             string `json:"startTime"`
			EndTime               string `json:"endTime"`
		}
		if s.Vacation.Err = note(get(g.path("/vacation"), &vac)); s.Vacation.Err == "" {
			s.Vacation = vacationFromGoogle(vac.EnableAutoReply, vac.ResponseSubject, vac.ResponseBodyPlainText, vac.ResponseBodyHTML, vac.RestrictToContacts, vac.RestrictToDomain, vac.StartTime, vac.EndTime)
		}

		var af struct {
			Enabled      bool   `json:"enabled"`
			EmailAddress string `json:"emailAddress"`
			Disposition  string `json:"disposition"`
		}
		if s.AutoForwarding.Err = note(get(g.path("/autoForwarding"), &af)); s.AutoForwarding.Err == "" {
			s.AutoForwarding = GmailAutoForwarding{Enabled: af.Enabled, EmailAddress: af.EmailAddress, Disposition: af.Disposition}
			if af.EmailAddress != "" {
				s.AutoForwarding.External = IsExternal(af.EmailAddress, g.Domains)
			}
		}

		var fa struct {
			ForwardingAddresses []struct {
				ForwardingEmail    string `json:"forwardingEmail"`
				VerificationStatus string `json:"verificationStatus"`
			} `json:"forwardingAddresses"`
		}
		if s.ForwardingAddresses.Err = note(get(g.path("/forwardingAddresses"), &fa)); s.ForwardingAddresses.Err == "" {
			for _, a := range fa.ForwardingAddresses {
				s.ForwardingAddresses.Addresses = append(s.ForwardingAddresses.Addresses, GmailForwardingAddress{Email: a.ForwardingEmail, VerificationStatus: a.VerificationStatus, External: IsExternal(a.ForwardingEmail, g.Domains)})
			}
		}

		var dl struct {
			Delegates []struct {
				DelegateEmail      string `json:"delegateEmail"`
				VerificationStatus string `json:"verificationStatus"`
			} `json:"delegates"`
		}
		if s.Delegates.Err = note(get(g.path("/delegates"), &dl)); s.Delegates.Err == "" {
			for _, d := range dl.Delegates {
				s.Delegates.Delegates = append(s.Delegates.Delegates, GmailDelegate{Email: d.DelegateEmail, VerificationStatus: d.VerificationStatus})
			}
		}

		var fl struct {
			Filter []struct {
				ID       string `json:"id"`
				Criteria struct {
					From          string `json:"from"`
					To            string `json:"to"`
					Subject       string `json:"subject"`
					Query         string `json:"query"`
					NegatedQuery  string `json:"negatedQuery"`
					HasAttachment bool   `json:"hasAttachment"`
				} `json:"criteria"`
				Action struct {
					AddLabelIDs    []string `json:"addLabelIds"`
					RemoveLabelIDs []string `json:"removeLabelIds"`
					Forward        string   `json:"forward"`
				} `json:"action"`
			} `json:"filter"`
		}
		if s.Filters.Err = note(get(g.path("/filters"), &fl)); s.Filters.Err == "" {
			for _, f := range fl.Filter {
				item := GmailFilter{ID: f.ID, From: f.Criteria.From, To: f.Criteria.To, Subject: f.Criteria.Subject, Query: f.Criteria.Query, NegatedQuery: f.Criteria.NegatedQuery, HasAttachment: f.Criteria.HasAttachment, ForwardTo: f.Action.Forward}
				if f.Action.Forward != "" {
					item.ForwardExternal = IsExternal(f.Action.Forward, g.Domains)
				}
				item.Deletes = contains(f.Action.AddLabelIDs, "TRASH")
				item.SkipsInbox = contains(f.Action.RemoveLabelIDs, "INBOX")
				s.Filters.Filters = append(s.Filters.Filters, item)
			}
		}

		var sa struct {
			SendAs []struct {
				SendAsEmail        string `json:"sendAsEmail"`
				DisplayName        string `json:"displayName"`
				IsPrimary          bool   `json:"isPrimary"`
				IsDefault          bool   `json:"isDefault"`
				TreatAsAlias       bool   `json:"treatAsAlias"`
				VerificationStatus string `json:"verificationStatus"`
			} `json:"sendAs"`
		}
		if s.SendAs.Err = note(get(g.path("/sendAs"), &sa)); s.SendAs.Err == "" {
			for _, a := range sa.SendAs {
				s.SendAs.Addresses = append(s.SendAs.Addresses, GmailSendAs{Email: a.SendAsEmail, DisplayName: a.DisplayName, IsPrimary: a.IsPrimary, IsDefault: a.IsDefault, TreatAsAlias: a.TreatAsAlias, VerificationStatus: a.VerificationStatus, External: IsExternal(a.SendAsEmail, g.Domains)})
			}
		}

		if delegation != nil {
			return nil, delegation
		}
		out = s
		summary := s.LogSummary()
		if failed := s.FailedSections(); failed == 6 {
			return summary, errors.New("no Gmail settings section could be read")
		}
		return summary, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// FailedSections counts sections that could not be read.
func (s *GmailSettings) FailedSections() int {
	n := 0
	for _, e := range []string{s.Vacation.Err, s.AutoForwarding.Err, s.ForwardingAddresses.Err, s.Delegates.Err, s.Filters.Err, s.SendAs.Err} {
		if e != "" {
			n++
		}
	}
	return n
}

// LogSummary is the structure-only record of a read. It holds counts, flags and
// a date window, never text, criteria, label names or addresses.
func (s *GmailSettings) LogSummary() map[string]any {
	out := map[string]any{"sections_failed": s.FailedSections()}
	if s.Vacation.Err == "" {
		out["vacation_enabled"] = s.Vacation.Enabled
		if !s.Vacation.StartTime.IsZero() {
			out["vacation_start"] = s.Vacation.StartTime.UTC().Format(time.RFC3339)
		}
		if !s.Vacation.EndTime.IsZero() {
			out["vacation_end"] = s.Vacation.EndTime.UTC().Format(time.RFC3339)
		}
	}
	external, unknown := false, false
	check := func(e *bool, present bool) {
		if !present {
			return
		}
		switch {
		case e == nil:
			unknown = true
		case *e:
			external = true
		}
	}
	if s.AutoForwarding.Err == "" {
		out["auto_forwarding_enabled"] = s.AutoForwarding.Enabled
		check(s.AutoForwarding.External, s.AutoForwarding.EmailAddress != "")
	}
	if s.ForwardingAddresses.Err == "" {
		out["forwarding_addresses"] = len(s.ForwardingAddresses.Addresses)
		for _, a := range s.ForwardingAddresses.Addresses {
			check(a.External, true)
		}
	}
	if s.Delegates.Err == "" {
		out["delegates"] = len(s.Delegates.Delegates)
	}
	if s.Filters.Err == "" {
		forwarding := 0
		for _, f := range s.Filters.Filters {
			if f.ForwardTo != "" {
				forwarding++
				check(f.ForwardExternal, true)
			}
		}
		out["filters"] = len(s.Filters.Filters)
		out["forwarding_filters"] = forwarding
	}
	if s.SendAs.Err == "" {
		out["send_as"] = len(s.SendAs.Addresses)
	}
	switch {
	case external:
		out["external_destination"] = true
	case unknown:
		// Domains could not be read: neither true nor false is recorded.
	default:
		out["external_destination"] = false
	}
	return out
}

func vacationFromGoogle(enabled bool, subject, plain, html string, contacts, domain bool, start, end string) GmailVacation {
	v := GmailVacation{Enabled: enabled, Subject: subject, BodyPlainText: plain, RestrictToContacts: contacts, RestrictToDomain: domain}
	if html != "" {
		v.BodyIsHTML = true
		if v.BodyPlainText == "" {
			v.BodyPlainText = html
		}
	}
	v.StartTime, v.EndTime = epochMillis(start), epochMillis(end)
	return v
}

func epochMillis(s string) time.Time {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

type VacationInput struct {
	Enabled                              bool
	Subject, BodyPlainText               string
	RestrictToContacts, RestrictToDomain bool
	StartTime, EndTime                   time.Time
}

// Validate checks what Gmail would otherwise reject, before anything is logged
// or sent.
func (v VacationInput) Validate() error {
	if utf8.RuneCountInString(v.Subject) > maxVacationTitle {
		return invalid("subject", "is too long")
	}
	if utf8.RuneCountInString(v.BodyPlainText) > maxVacationText {
		return invalid("body_plain_text", "is too long")
	}
	if v.Enabled && strings.TrimSpace(v.Subject) == "" && strings.TrimSpace(v.BodyPlainText) == "" {
		return invalid("subject", "a subject or a message is required to turn the auto-reply on")
	}
	if !v.StartTime.IsZero() && !v.EndTime.IsZero() && !v.StartTime.Before(v.EndTime) {
		return invalid("end_time", "must be after the start time")
	}
	return nil
}

// UpdateVacation replaces the vacation responder. It always sends the whole
// setting, with the body as plain text and the HTML body cleared, so a stored
// HTML body can't override what the caller wrote.
func (g *GmailClient) UpdateVacation(ctx context.Context, in VacationInput) (*GmailVacation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body := map[string]any{
		"enableAutoReply":       in.Enabled,
		"responseSubject":       in.Subject,
		"responseBodyPlainText": in.BodyPlainText,
		"responseBodyHtml":      "",
		"restrictToContacts":    in.RestrictToContacts,
		"restrictToDomain":      in.RestrictToDomain,
	}
	requested := map[string]any{"enabled": in.Enabled, "restrict_to_contacts": in.RestrictToContacts, "restrict_to_domain": in.RestrictToDomain}
	if !in.StartTime.IsZero() {
		body["startTime"] = strconv.FormatInt(in.StartTime.UnixMilli(), 10)
		requested["start"] = in.StartTime.UTC().Format(time.RFC3339)
	}
	if !in.EndTime.IsZero() {
		body["endTime"] = strconv.FormatInt(in.EndTime.UnixMilli(), 10)
		requested["end"] = in.EndTime.UTC().Format(time.RFC3339)
	}
	var out *GmailVacation
	err := g.record(ctx, Write{Method: http.MethodPut, Path: "/gmail/settings/vacation", Action: ActionGmailVacationUpdate, Requested: requested}, func() (map[string]any, error) {
		var res struct {
			EnableAutoReply       bool   `json:"enableAutoReply"`
			ResponseSubject       string `json:"responseSubject"`
			ResponseBodyPlainText string `json:"responseBodyPlainText"`
			ResponseBodyHTML      string `json:"responseBodyHtml"`
			RestrictToContacts    bool   `json:"restrictToContacts"`
			RestrictToDomain      bool   `json:"restrictToDomain"`
			StartTime             string `json:"startTime"`
			EndTime               string `json:"endTime"`
		}
		if err := g.transport.doAt(ctx, g.endpoint, http.MethodPut, g.path("/vacation"), nil, body, &res); err != nil {
			return nil, err
		}
		v := vacationFromGoogle(res.EnableAutoReply, res.ResponseSubject, res.ResponseBodyPlainText, res.ResponseBodyHTML, res.RestrictToContacts, res.RestrictToDomain, res.StartTime, res.EndTime)
		out = &v
		summary := map[string]any{"enabled": v.Enabled}
		if !v.StartTime.IsZero() {
			summary["start"] = v.StartTime.Format(time.RFC3339)
		}
		if !v.EndTime.IsZero() {
			summary["end"] = v.EndTime.Format(time.RFC3339)
		}
		return summary, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// VerifiedDomains lists the tenant's verified domains, the basis for telling
// internal addresses from external ones.
func (c *Client) VerifiedDomains(ctx context.Context) ([]string, error) {
	var domains struct {
		Domains []struct {
			DomainName string `json:"domainName"`
			Verified   bool   `json:"verified"`
		} `json:"domains"`
	}
	if err := c.do(ctx, http.MethodGet, "/customer/"+url.PathEscape(c.CustomerID)+"/domains", nil, nil, &domains); err != nil {
		return nil, err
	}
	var out []string
	for _, d := range domains.Domains {
		if d.Verified {
			out = append(out, strings.ToLower(d.DomainName))
		}
	}
	return out, nil
}

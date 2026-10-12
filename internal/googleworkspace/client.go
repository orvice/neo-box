// Package googleworkspace talks to Google's Admin SDK Directory API.
package googleworkspace

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
)

const ProviderType = "google_workspace"
const UserScope = "https://www.googleapis.com/auth/admin.directory.user"
const DomainScope = "https://www.googleapis.com/auth/admin.directory.domain.readonly"
const GroupScope = "https://www.googleapis.com/auth/admin.directory.group"
const OrgUnitScope = "https://www.googleapis.com/auth/admin.directory.orgunit"
const SecurityScope = "https://www.googleapis.com/auth/admin.directory.user.security"
const AuditScope = "https://www.googleapis.com/auth/admin.reports.audit.readonly"
const reportsURL = "https://admin.googleapis.com/admin/reports/v1"
const directoryURL = "https://admin.googleapis.com/admin/directory/v1"

type ConnectionConfig struct {
	ServiceAccountEmail string `json:"service_account_email"`
	ClientID            string `json:"client_id"`
}
type ConnectionSecret struct {
	ServiceAccountJSON string `json:"service_account_json"`
}

var reasonPattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
var ErrDelegation = errors.New("could not obtain delegated Google token; check the key, tenant authorization, scopes and administrator")

var numericID = regexp.MustCompile(`^[0-9]+$`)
var customerID = regexp.MustCompile(`^C[a-zA-Z0-9]+$`)

func ValidCustomerID(s string) bool { return customerID.MatchString(s) }
func ValidEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}

// ParseKey accepts only Service Account RSA key JSON, never external-account
// credentials or a caller-selected token endpoint.
func ParseKey(raw string) (*ConnectionConfig, *jwt.Config, error) {
	if len(raw) > 65536 {
		return nil, nil, errors.New("Service Account key JSON must be smaller than 64 KB")
	}
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		ClientID    string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(raw), &key); err != nil || key.Type != "service_account" || !ValidEmail(key.ClientEmail) || !numericID.MatchString(key.ClientID) {
		return nil, nil, errors.New("expected a Service Account key JSON with client_email and numeric client_id")
	}
	cfg, err := google.JWTConfigFromJSON([]byte(raw), UserScope, DomainScope)
	if err != nil {
		return nil, nil, errors.New("invalid Service Account key JSON")
	}
	block, _ := pem.Decode(cfg.PrivateKey)
	if block == nil {
		return nil, nil, errors.New("invalid Service Account private key")
	}
	var private any
	private, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		private, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	rsaKey, ok := private.(*rsa.PrivateKey)
	if err != nil || !ok || rsaKey.N.BitLen() < 2048 {
		return nil, nil, errors.New("Service Account needs an RSA key of at least 2048 bits")
	}
	cfg.TokenURL = "https://oauth2.googleapis.com/token"
	return &ConnectionConfig{ServiceAccountEmail: key.ClientEmail, ClientID: key.ClientID}, cfg, nil
}

// VerifyKey checks only the shared key. Delegation is tested per tenant.
func VerifyKey(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret) error {
	metadata, cfg, err := ParseKey(secret.ServiceAccountJSON)
	if err != nil {
		return err
	}
	if *metadata != identity {
		return errors.New("Service Account key does not match the configured identity")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	if _, err := cfg.TokenSource(ctx).Token(); err != nil {
		return errors.New("Google rejected the Service Account key or token request; check the key and network connectivity")
	}
	return nil
}

type User struct {
	ID           string `json:"id"`
	PrimaryEmail string `json:"primaryEmail"`
	CustomerID   string `json:"customerId"`
	Name         struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	} `json:"name"`
	OrgUnitPath               string `json:"orgUnitPath"`
	Suspended                 bool   `json:"suspended"`
	IsAdmin                   *bool  `json:"isAdmin"`
	IsDelegatedAdmin          *bool  `json:"isDelegatedAdmin"`
	TwoStepEnrolled           *bool  `json:"isEnrolledIn2Sv"`
	TwoStepEnforced           *bool  `json:"isEnforcedIn2Sv"`
	LastLoginTime             string `json:"lastLoginTime"`
	CreationTime              string `json:"creationTime"`
	ChangePasswordAtNextLogin bool   `json:"changePasswordAtNextLogin"`
}
type UserPage struct {
	Users         []User `json:"users"`
	NextPageToken string `json:"nextPageToken"`
}

type APIError struct {
	Status int
	Reason string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Google Directory API returned %d (%s)", e.Status, e.Reason)
}

var ErrUncertain = errors.New("Google did not confirm this change; refresh the affected resource before trying again")
var ErrTenantMismatch = errors.New("resource does not belong to this Workspace tenant")

type Client struct {
	HTTP            *http.Client
	Endpoint        string
	CustomerID      string
	AdminEmail      string
	ReportsEndpoint string
	Recorder        WriteRecorder
}

func NewClient(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret, customer, admin string, extraScopes ...string) (*Client, error) {
	metadata, cfg, err := ParseKey(secret.ServiceAccountJSON)
	if err != nil {
		return nil, err
	}
	if *metadata != identity {
		return nil, errors.New("Service Account identity mismatch")
	}
	if !ValidCustomerID(customer) || !ValidEmail(admin) {
		return nil, errors.New("invalid Workspace customer or delegated administrator")
	}
	cfg.Subject = admin
	// Optional capabilities never expand the scopes of base user requests.
	for _, scope := range extraScopes {
		switch scope {
		case GroupScope, OrgUnitScope, SecurityScope, AuditScope:
			cfg.Scopes = append(cfg.Scopes, scope)
		default:
			return nil, errors.New("unsupported Workspace capability scope")
		}
	}
	// The token source lives only in this tenant's client. Token errors are
	// sanitized: Google's response can contain credential-related details.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	hc := oauth2.NewClient(ctx, safeTokenSource{cfg.TokenSource(ctx)})
	hc.Timeout = 30 * time.Second
	return &Client{HTTP: hc, Endpoint: directoryURL, CustomerID: customer, AdminEmail: admin, ReportsEndpoint: reportsURL}, nil
}

type safeTokenSource struct{ oauth2.TokenSource }

func (s safeTokenSource) Token() (*oauth2.Token, error) {
	t, err := s.TokenSource.Token()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, ErrDelegation
	}
	return t, nil
}

// do never resends mutations. Reads retry temporary failures with bounded backoff.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	return c.doAt(ctx, c.Endpoint, method, path, query, body, out)
}
func (c *Client) doAt(ctx context.Context, endpoint, method, path string, query url.Values, body, out any) (resultErr error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	if method != http.MethodGet && c.Recorder != nil {
		finish, err := c.Recorder.Begin(ctx, Write{Method: method, Path: path, Requested: ResourceSummary(body)})
		if err != nil {
			return ErrLogUnavailable
		}
		defer func() {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			result := map[string]any{}
			if resultErr == nil && out != nil {
				result = ResourceSummary(out)
			}
			if err := finish(finishCtx, result, resultErr); err != nil {
				resultErr = ErrOutcomeLogUnavailable
			}
		}()
	}
	client := *c.HTTP
	// Even a redirect must not resend a mutation or change its tenant target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+path+"?"+query.Encode(), bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		var apiErr *APIError
		if err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil {
					return nil
				}
				if err = json.Unmarshal(raw, out); err == nil {
					return nil
				}
			} else {
				var envelope struct {
					Error struct {
						Errors []struct {
							Reason string `json:"reason"`
						} `json:"errors"`
					} `json:"error"`
				}
				_ = json.Unmarshal(raw, &envelope)
				reason := http.StatusText(resp.StatusCode)
				// Do not reflect Google's messages or request bodies (passwords).
				if len(envelope.Error.Errors) > 0 && reasonPattern.MatchString(envelope.Error.Errors[0].Reason) {
					reason = envelope.Error.Errors[0].Reason
				}
				apiErr = &APIError{Status: resp.StatusCode, Reason: reason}
				err = apiErr
			}
		}
		if errors.Is(err, ErrDelegation) {
			return ErrDelegation
		}
		if method != http.MethodGet {
			if apiErr != nil && apiErr.Status >= 400 && apiErr.Status < 500 {
				return apiErr
			}
			return ErrUncertain
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if apiErr != nil && apiErr.Status != 429 && apiErr.Status < 500 {
			return apiErr
		}
		if attempt == 2 {
			return errors.New("Google Directory read failed; check delegation, permissions and connectivity")
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("Google request failed")
}
func (c *Client) ListUsers(ctx context.Context, query, token string, size int) (*UserPage, error) {
	q := url.Values{"customer": {c.CustomerID}, "maxResults": {fmt.Sprint(size)}, "orderBy": {"email"}, "projection": {"basic"}}
	if query != "" {
		q.Set("query", query)
	}
	if token != "" {
		q.Set("pageToken", token)
	}
	var p UserPage
	if err := c.do(ctx, http.MethodGet, "/users", q, nil, &p); err != nil {
		return nil, err
	}
	for _, u := range p.Users {
		if u.CustomerID != c.CustomerID {
			return nil, ErrTenantMismatch
		}
	}
	return &p, nil
}
func (c *Client) GetUser(ctx context.Context, id string) (*User, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("user ID is required")
	}
	var u User
	if err := c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(id), nil, nil, &u); err != nil {
		return nil, err
	}
	if u.CustomerID != c.CustomerID {
		return nil, ErrTenantMismatch
	}
	return &u, nil
}
func (c *Client) Verify(ctx context.Context) error {
	u, err := c.GetUser(ctx, c.AdminEmail)
	if err != nil {
		return err
	}
	if u.Suspended {
		return errors.New("delegated administrator is suspended")
	}
	_, err = c.ListUsers(ctx, "", "", 1)
	return err
}
func (c *Client) CreateUser(ctx context.Context, email string, body map[string]any) (*User, error) {
	// Google assigns new users by their email domain. Check the domain against
	// this customer before sending any password or mutation.
	if !ValidEmail(email) {
		return nil, errors.New("invalid primary email")
	}
	domain := strings.ToLower(strings.Split(email, "@")[1])
	var domains struct {
		Domains []struct {
			DomainName string `json:"domainName"`
			Verified   bool   `json:"verified"`
		} `json:"domains"`
	}
	if err := c.do(ctx, http.MethodGet, "/customer/"+url.PathEscape(c.CustomerID)+"/domains", nil, nil, &domains); err != nil {
		return nil, err
	}
	allowed := false
	for _, d := range domains.Domains {
		if strings.EqualFold(d.DomainName, domain) && d.Verified {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrTenantMismatch
	}
	body["primaryEmail"] = email
	var u User
	if err := c.do(ctx, http.MethodPost, "/users", nil, body, &u); err != nil {
		return nil, err
	}
	if u.CustomerID != c.CustomerID {
		return nil, ErrUncertain
	}
	return &u, nil
}
func (c *Client) UpdateUser(ctx context.Context, id string, body map[string]any) (*User, error) {
	existing, err := c.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	var u User
	if err = c.do(ctx, http.MethodPatch, "/users/"+url.PathEscape(existing.ID), nil, body, &u); err != nil {
		return nil, err
	}
	if u.CustomerID != c.CustomerID {
		return nil, ErrUncertain
	}
	return &u, nil
}

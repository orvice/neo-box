package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type AuditQuery struct {
	Application, UserKey, EventName, PageToken string
	Start, End                                 time.Time
	PageSize                                   int
}
type AuditParameter struct {
	Name              string          `json:"name"`
	Value             *string         `json:"value,omitempty"`
	MultiValue        []string        `json:"multiValue,omitempty"`
	IntValue          string          `json:"intValue,omitempty"`
	MultiIntValue     []string        `json:"multiIntValue,omitempty"`
	BoolValue         *bool           `json:"boolValue,omitempty"`
	MessageValue      json.RawMessage `json:"messageValue,omitempty"`
	MultiMessageValue json.RawMessage `json:"multiMessageValue,omitempty"`
}
type AuditEvent struct {
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Parameters []AuditParameter `json:"parameters"`
	// sensitiveParameters are intentionally not decoded or exposed.
}
type AuditActivity struct {
	ID struct {
		Time        string `json:"time"`
		Qualifier   string `json:"uniqueQualifier"`
		Application string `json:"applicationName"`
		CustomerID  string `json:"customerId"`
	} `json:"id"`
	Actor struct {
		Email     string `json:"email"`
		ProfileID string `json:"profileId"`
	} `json:"actor"`
	IPAddress string       `json:"ipAddress"`
	Events    []AuditEvent `json:"events"`
}
type AuditPage struct {
	Items         []AuditActivity `json:"items"`
	NextPageToken string          `json:"nextPageToken"`
}

func ValidAuditApplication(app string) bool {
	switch app {
	case "login", "admin", "groups", "groups_enterprise", "token", "user_accounts":
		return true
	}
	return false
}
func (c *Client) ListAuditActivities(ctx context.Context, q AuditQuery) (*AuditPage, error) {
	if !ValidAuditApplication(q.Application) {
		return nil, invalid("application", "unsupported audit application")
	}
	if q.PageSize < 1 || q.PageSize > 1000 {
		return nil, invalid("page_size", "must be between 1 and 1000")
	}
	userKey := "all"
	if q.UserKey != "" && q.UserKey != "all" {
		u, err := c.GetUser(ctx, q.UserKey)
		if err != nil {
			return nil, err
		}
		userKey = u.ID
	}
	values := url.Values{"customerId": {c.CustomerID}, "startTime": {q.Start.UTC().Format(time.RFC3339)}, "endTime": {q.End.UTC().Format(time.RFC3339)}, "maxResults": {strconv.Itoa(q.PageSize)}}
	if q.PageToken != "" {
		values.Set("pageToken", q.PageToken)
	}
	if q.EventName != "" {
		values.Set("eventName", q.EventName)
	}
	endpoint := c.ReportsEndpoint
	if endpoint == "" {
		endpoint = reportsURL
	}
	var page AuditPage
	if err := c.doAt(ctx, endpoint, http.MethodGet, "/activity/users/"+url.PathEscape(userKey)+"/applications/"+url.PathEscape(q.Application), values, nil, &page); err != nil {
		return nil, err
	}
	for _, a := range page.Items {
		if a.ID.CustomerID != c.CustomerID || a.ID.Application != q.Application {
			return nil, ErrTenantMismatch
		}
		if _, err := AuditTime(a.ID.Time); err != nil {
			return nil, err
		}
	}
	return &page, nil
}
func AuditTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		t := time.Unix(seconds, 0).UTC()
		if t.Year() >= 1 && t.Year() <= 9999 {
			return t, nil
		}
	}
	return time.Time{}, errors.New("Google returned an invalid audit event time")
}
func sensitiveParameter(name string) bool {
	name = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(name))
	if name == "token" || name == "secret" {
		return true
	}
	for _, key := range []string{"password", "privatekey", "accesstoken", "refreshtoken", "clientsecret", "authorizationcode", "authcode"} {
		if strings.Contains(name, key) {
			return true
		}
	}
	return false
}
func AuditParameters(parameters []AuditParameter) map[string]any {
	out := map[string]any{}
	for _, p := range parameters {
		if sensitiveParameter(p.Name) {
			out[p.Name] = "[redacted]"
			continue
		}
		switch {
		case p.Value != nil:
			out[p.Name] = *p.Value
		case p.MultiValue != nil:
			out[p.Name] = auditStrings(p.MultiValue)
		case p.IntValue != "":
			out[p.Name] = p.IntValue
		case p.MultiIntValue != nil:
			out[p.Name] = auditStrings(p.MultiIntValue)
		case p.BoolValue != nil:
			out[p.Name] = *p.BoolValue
		case len(p.MessageValue) > 0:
			out[p.Name] = sanitizedAuditMessage(p.MessageValue)
		case len(p.MultiMessageValue) > 0:
			var messages []json.RawMessage
			if json.Unmarshal(p.MultiMessageValue, &messages) == nil {
				values := []any{}
				for _, m := range messages {
					values = append(values, sanitizedAuditMessage(m))
				}
				out[p.Name] = values
			}
		}
	}
	return out
}
func sanitizedAuditMessage(raw json.RawMessage) any {
	var m struct {
		Parameters []AuditParameter `json:"parameter"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "[unavailable]"
	}
	return AuditParameters(m.Parameters)
}

func auditStrings(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

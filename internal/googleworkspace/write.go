package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

var ErrLogUnavailable = errors.New("operation could not be recorded; no change was sent")
var ErrOutcomeLogUnavailable = errors.New("the change outcome could not be recorded; refresh the resource before retrying")

type Write struct {
	Method, Path string
	Action       string
	Requested    map[string]any
}

// Begin must persist the pending operation before returning. Finish is called
// exactly once after the HTTP attempt and receives only sanitized resource data.
type WriteRecorder interface {
	Begin(context.Context, Write) (func(context.Context, map[string]any, error) error, error)
}

// ResourceSummary uses an allowlist rather than redacting arbitrary request
// JSON. This also excludes injected tokens, passwords and private key material.
func ResourceSummary(body any) map[string]any {
	raw, err := json.Marshal(body)
	if err != nil {
		return map[string]any{}
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for _, key := range []string{"id", "primaryEmail", "email", "alias", "description", "orgUnitPath", "suspended", "changePasswordAtNextLogin", "role", "type", "status", "orgUnitId", "orgUnitPath", "parentOrgUnitPath", "directMembersCount", "clientId", "customerId"} {
		if v, ok := object[key]; ok {
			switch v.(type) {
			case string, bool, float64:
				out[key] = v
			}
		}
	}
	if v, ok := object["name"]; ok {
		switch n := v.(type) {
		case string:
			out["name"] = n
		case map[string]any:
			safe := map[string]any{}
			for _, key := range []string{"givenName", "familyName", "fullName"} {
				if value, ok := n[key].(string); ok {
					safe[key] = value
				}
			}
			out["name"] = safe
		}
	}
	if _, ok := object["password"]; ok {
		out["password_changed"] = true
	}
	return out
}
func WriteAction(w Write) string {
	if w.Action != "" {
		return w.Action
	}
	p := strings.Split(strings.Trim(w.Path, "/"), "/")
	if len(p) == 0 {
		return "unknown"
	}
	resource := p[0]
	if resource == "customer" {
		resource = "org_unit"
	}
	if len(p) > 2 && p[2] == "members" {
		resource = "group_member"
	}
	if len(p) > 2 && p[2] == "aliases" {
		resource = "user_alias"
	}
	if len(p) > 2 && p[2] == "tokens" {
		return "user_application_revoke"
	}
	if len(p) > 2 && p[2] == "signOut" {
		return "user_sign_out"
	}
	if resource == "users" {
		resource = "user"
		if _, ok := w.Requested["password_changed"]; ok && w.Method != http.MethodPost {
			return "user_password_reset"
		}
		if _, ok := w.Requested["suspended"]; ok {
			return "user_access_update"
		}
	}
	if resource == "groups" {
		resource = "group"
	}
	action := "update"
	switch w.Method {
	case http.MethodPost:
		action = "create"
	case http.MethodDelete:
		action = "delete"
	}
	return resource + "_" + action
}
func (c *Client) SetWriteRecorder(r WriteRecorder) { c.Recorder = r }

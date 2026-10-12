package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAliasesKeepGoogleManagedAliasesAndRejectOtherTenants(t *testing.T) {
	writes := 0
	deleted := ""
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/users/foreign":
			writeUser(w, "foreign", "CtenantB", false)
		case r.Method == http.MethodGet && r.URL.Path == "/users/u":
			fmt.Fprint(w, `{"id":"u","customerId":"CtenantA","nonEditableAliases":["auto@alias.example"]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/users/u/aliases":
			fmt.Fprint(w, `{"aliases":[{"alias":"editable@a.example"}]}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/domains"):
			fmt.Fprint(w, `{"domains":[{"domainName":"a.example","verified":true}]}`)
		case r.Method == http.MethodPost:
			writes++
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(map[string]string{"alias": in["alias"]})
		case r.Method == http.MethodDelete:
			writes++
			deleted = r.URL.Path
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	list, err := c.ListAliases(ctx, "u")
	if err != nil || len(list) != 2 || !list[0].Editable || list[1].Editable {
		t.Fatalf("aliases=%v err=%v", list, err)
	}
	if err = c.DeleteAlias(ctx, "u", "auto@alias.example"); err == nil {
		t.Fatal("Google-managed alias removed")
	}
	if _, err = c.AddAlias(ctx, "foreign", "new@a.example"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant alias=%v", err)
	}
	if _, err = c.AddAlias(ctx, "u", "new@b.example"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant domain=%v", err)
	}
	if writes != 0 {
		t.Fatal("refused alias operation wrote to Google")
	}
	a, err := c.AddAlias(ctx, "u", "new@a.example")
	if err != nil || a.Email != "new@a.example" {
		t.Fatalf("add=%v %v", a, err)
	}
	if err = c.DeleteAlias(ctx, "u", "editable@a.example"); err != nil || !strings.Contains(deleted, "/users/u/aliases/") {
		t.Fatalf("delete=%s %v", deleted, err)
	}
}
func TestGroupOwnershipUsesAllCustomerPagesAndUserMembershipIntersection(t *testing.T) {
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/u" {
			writeUser(w, "u", "CtenantA", false)
			return
		}
		if r.URL.Path == "/groups" {
			if r.URL.Query().Get("userKey") == "u" {
				if r.URL.Query().Get("customer") != "" {
					t.Error("Google forbids customer plus userKey")
				}
				fmt.Fprint(w, `{"groups":[{"id":"own","email":"team@a.example"},{"id":"foreign","email":"foreign@b.example"}]}`)
				return
			}
			if r.URL.Query().Get("customer") != "CtenantA" {
				t.Error("missing explicit customer")
			}
			if r.URL.Query().Get("pageToken") == "second" {
				fmt.Fprint(w, `{"groups":[{"id":"own","email":"team@a.example"}]}`)
			} else {
				fmt.Fprint(w, `{"groups":[{"id":"first","email":"first@a.example"}],"nextPageToken":"second"}`)
			}
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/groups/own" {
			writes++
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet {
			writes++
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	})
	ctx := context.Background()
	if err := c.DeleteGroup(ctx, "foreign"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("foreign group=%v", err)
	}
	if writes != 0 {
		t.Fatal("foreign group mutation sent")
	}
	g, err := c.GetGroup(ctx, "own")
	if err != nil || g.ID != "own" {
		t.Fatalf("later-page group=%v %v", g, err)
	}
	groups, err := c.ListUserGroups(ctx, "u")
	if err != nil || len(groups) != 1 || groups[0].ID != "own" {
		t.Fatalf("membership leaked another tenant: %v %v", groups, err)
	}
	if err = c.DeleteGroup(ctx, "own"); err != nil || writes != 1 {
		t.Fatalf("delete=%v writes=%d", err, writes)
	}
}
func TestGroupCRUDAndMemberRolesPreserveUnshownFields(t *testing.T) {
	writes := 0
	role := "MEMBER"
	groupName := "Old"
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/groups" {
			fmt.Fprintf(w, `{"groups":[{"id":"g","email":"team@a.example","name":%q}]}`, groupName)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/domains") {
			fmt.Fprint(w, `{"domains":[{"domainName":"a.example","verified":true}]}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/groups/g/members" {
			if r.URL.Query().Get("pageToken") != "page-2" || r.URL.Query().Get("includeDerivedMembership") != "false" {
				t.Error("invalid member pagination/derived filter")
			}
			fmt.Fprint(w, `{"members":[{"id":"m","email":"external@outside.example","role":"MEMBER"}],"nextPageToken":"page-3"}`)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/groups/g/members/") {
			fmt.Fprintf(w, `{"id":"m","email":"external@outside.example","role":%q,"delivery_settings":"ALL_MAIL"}`, role)
			return
		}
		if r.Method != http.MethodGet {
			writes++
		}
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/groups":
			groupName = body["name"].(string)
			fmt.Fprintf(w, `{"id":"g","email":"team@a.example","name":%q}`, groupName)
		case r.Method == http.MethodPatch && r.URL.Path == "/groups/g":
			if len(body) != 3 {
				t.Error("unshown group fields overwritten")
			}
			groupName = body["name"].(string)
			fmt.Fprintf(w, `{"id":"g","email":"team@a.example","name":%q}`, groupName)
		case r.Method == http.MethodPost && r.URL.Path == "/groups/g/members":
			if body["email"] != "external@outside.example" {
				t.Error("external member lost")
			}
			role = body["role"].(string)
			fmt.Fprintf(w, `{"id":"m","email":"external@outside.example","role":%q}`, role)
		case r.Method == http.MethodPatch && r.URL.Path == "/groups/g/members/m":
			if len(body) != 1 {
				t.Error("delivery settings overwritten")
			}
			role = body["role"].(string)
			fmt.Fprintf(w, `{"id":"m","email":"external@outside.example","role":%q}`, role)
		case r.Method == http.MethodDelete && r.URL.Path == "/groups/g/members/m":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	if _, err := c.CreateGroup(ctx, "team@a.example", "Team", "desc"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateGroup(ctx, "g", "team@a.example", "Updated", ""); err != nil {
		t.Fatal(err)
	}
	page, err := c.ListMembers(ctx, "g", "page-2", 50)
	if err != nil || page.NextPageToken != "page-3" {
		t.Fatalf("members=%v %v", page, err)
	}
	if _, err = c.AddMember(ctx, "g", "external@outside.example", "SUPERADMIN"); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, err = c.AddMember(ctx, "g", "external@outside.example", "MEMBER"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"MANAGER", "OWNER", "MEMBER"} {
		m, err := c.UpdateMember(ctx, "g", "external@outside.example", r)
		if err != nil || m.Role != r {
			t.Fatalf("role=%v %v", m, err)
		}
	}
	if err = c.DeleteMember(ctx, "g", "external@outside.example"); err != nil {
		t.Fatal(err)
	}
	if writes != 7 {
		t.Fatalf("writes=%d", writes)
	}
}
func TestOrgUnitsAreCustomerScopedAndCannotMoveIntoOwnSubtree(t *testing.T) {
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/customer/CtenantA/orgunits") {
			t.Error("org unit request is not customer scoped")
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("type") != "all" {
				t.Error("must include descendants")
			}
			fmt.Fprint(w, `{"organizationUnits":[{"orgUnitId":"id:p","orgUnitPath":"/Parent","name":"Parent"},{"orgUnitId":"id:c","orgUnitPath":"/Parent/Child","name":"Child","parentOrgUnitPath":"/Parent"}]}`)
			return
		}
		writes++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if len(body) != 3 {
			t.Error("unshown OU fields overwritten")
		}
		fmt.Fprint(w, `{"orgUnitId":"id:new","name":"New","orgUnitPath":"/New","parentOrgUnitPath":"/"}`)
	})
	ctx := context.Background()
	for _, id := range []string{"", "foreign", "id:root"} {
		if err := c.DeleteOrgUnit(ctx, id); err == nil {
			t.Fatalf("invalid OU %s accepted", id)
		}
	}
	for _, parent := range []string{"/Parent", "/Parent/Child"} {
		if _, err := c.UpdateOrgUnit(ctx, "id:p", "Parent", "", parent); err == nil {
			t.Fatal("cyclic OU move accepted")
		}
	}
	if _, err := c.CreateOrgUnit(ctx, "Bad/Name", "", "/"); err == nil {
		t.Fatal("path traversal name accepted")
	}
	if _, err := c.CreateOrgUnit(ctx, "New", "", "/Foreign"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("foreign parent=%v", err)
	}
	if writes != 0 {
		t.Fatal("invalid OU mutation sent")
	}
	if _, err := c.CreateOrgUnit(ctx, "New", "", "/Parent"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateOrgUnit(ctx, "id:c", "Child", "new description", "/"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteOrgUnit(ctx, "id:c"); err != nil {
		t.Fatal(err)
	}
	if writes != 3 {
		t.Fatalf("OU writes=%d", writes)
	}
}
func TestSecurityActionsAreUserScopedAndWritesAreNotRetried(t *testing.T) {
	for _, status := range []int{204, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			writes := 0
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/users/foreign" {
					writeUser(w, "foreign", "CtenantB", false)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/users/u" {
					writeUser(w, "u", "CtenantA", false)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/users/u/tokens" {
					fmt.Fprint(w, `{"items":[{"clientId":"app","displayText":"Example","scopes":["read"],"access_token":"MUST-NOT-EXPOSE"}]}`)
					return
				}
				if r.Method != http.MethodGet {
					writes++
					if r.URL.Path != "/users/u/signOut" && r.URL.Path != "/users/u/tokens/app" {
						t.Errorf("wrong security target %s", r.URL.Path)
					}
					w.WriteHeader(status)
					return
				}
				t.Error("unexpected security request")
				w.WriteHeader(500)
			})
			ctx := context.Background()
			if err := c.SignOutUser(ctx, "foreign"); !errors.Is(err, ErrTenantMismatch) {
				t.Fatal(err)
			}
			if err := c.RevokeUserToken(ctx, "foreign", "app"); !errors.Is(err, ErrTenantMismatch) {
				t.Fatal(err)
			}
			if writes != 0 {
				t.Fatal("foreign security operation sent")
			}
			tokens, err := c.ListUserTokens(ctx, "u")
			if err != nil || len(tokens) != 1 {
				t.Fatalf("tokens=%v %v", tokens, err)
			}
			raw, _ := json.Marshal(tokens)
			if strings.Contains(string(raw), "MUST-NOT-EXPOSE") {
				t.Fatal("actual token exposed")
			}
			for _, err := range []error{c.SignOutUser(ctx, "u"), c.RevokeUserToken(ctx, "u", "app")} {
				if status == 204 && err != nil {
					t.Fatal(err)
				}
				if status == 503 && !errors.Is(err, ErrUncertain) {
					t.Fatalf("unconfirmed security action=%v", err)
				}
			}
			if writes != 2 {
				t.Fatalf("security actions retried: %d", writes)
			}
		})
	}
}
func TestOptionalScopesAreNotRequiredForBaseUserClients(t *testing.T) {
	raw := testKey(t)
	identity, _, err := ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{GroupScope, OrgUnitScope, SecurityScope} {
		if _, err = NewClient(context.Background(), *identity, ConnectionSecret{raw}, "CtenantA", "admin@a.example", scope); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = NewClient(context.Background(), *identity, ConnectionSecret{raw}, "CtenantA", "admin@a.example", "https://untrusted.example/scope"); err == nil {
		t.Fatal("unsupported scope accepted")
	}
	if _, err = NewClient(context.Background(), *identity, ConnectionSecret{raw}, "CtenantA", "admin@a.example"); err != nil {
		t.Fatal("base scopes expanded")
	}
}

package googleworkspace

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"type": "service_account", "client_id": "123456789", "client_email": "shared@example.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": "http://untrusted.example/token"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestKeyValidationAndTenantTokenIsolation(t *testing.T) {
	raw := testKey(t)
	identity, cfg, err := ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Fatal("untrusted token endpoint accepted")
	}
	if strings.Join(cfg.Scopes, ",") != UserScope+","+DomainScope {
		t.Fatalf("scopes=%v", cfg.Scopes)
	}
	a, err := NewClient(context.Background(), *identity, ConnectionSecret{raw}, "CtenantA", "admin@a.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(context.Background(), *identity, ConnectionSecret{raw}, "CtenantB", "admin@b.example")
	if err != nil {
		t.Fatal(err)
	}
	if a.HTTP.Transport == b.HTTP.Transport || a.CustomerID == b.CustomerID || a.AdminEmail == b.AdminEmail {
		t.Fatal("tenant client or token transport shared")
	}
	other := *identity
	other.ClientID = "999"
	if _, err = NewClient(context.Background(), other, ConnectionSecret{raw}, "CtenantA", "admin@a.example"); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	for _, bad := range []string{`{"type":"external_account"}`, `{"type":"service_account","client_email":"a@b","client_id":"1","private_key":"invalid"}`} {
		if _, _, err = ParseKey(bad); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if ValidCustomerID("my_customer") || ValidCustomerID("example.com") {
		t.Fatal("ambiguous customer accepted")
	}
}

func fakeClient(t *testing.T, fn http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Endpoint: srv.URL, CustomerID: "CtenantA", AdminEmail: "admin@a.example"}
}
func writeUser(w http.ResponseWriter, id, customer string, suspended bool) {
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "customerId": customer, "primaryEmail": "user@a.example", "name": map[string]string{"givenName": "Ada", "familyName": "Lovelace"}, "suspended": suspended})
}
func TestCrossTenantReadAndMutationAreRejected(t *testing.T) {
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		writeUser(w, "other", "CtenantB", false)
	})
	if _, err := c.GetUser(context.Background(), "other"); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("get = %v", err)
	}
	if _, err := c.UpdateUser(context.Background(), "other", map[string]any{"password": "new-secret"}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("update = %v", err)
	}
	if writes != 0 {
		t.Fatal("cross-tenant mutation sent")
	}
	c = fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"users":[{"id":"other","customerId":"CtenantB"}]}`)
	})
	if _, err := c.ListUsers(context.Background(), "", "", 50); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("list=%v", err)
	}
}
func TestCreateChecksVerifiedCustomerDomainBeforeSendingPassword(t *testing.T) {
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/customer/CtenantA/domains" {
				t.Errorf("wrong domain lookup: %s", r.URL.Path)
			}
			fmt.Fprint(w, `{"domains":[{"domainName":"a.example","verified":true},{"domainName":"unverified.example","verified":false}]}`)
		case http.MethodPost:
			writes++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["primaryEmail"] != "new@a.example" || body["password"] != "  exact secret  " {
				t.Errorf("unexpected create body")
			}
			writeUser(w, "new", "CtenantA", false)
		}
	})
	for _, email := range []string{"new@b.example", "new@unverified.example"} {
		if _, err := c.CreateUser(context.Background(), email, map[string]any{"password": "not-for-other-tenant"}); !errors.Is(err, ErrTenantMismatch) {
			t.Fatalf("create %s=%v", email, err)
		}
	}
	if writes != 0 {
		t.Fatal("password sent to an unowned domain")
	}
	if _, err := c.CreateUser(context.Background(), "new@a.example", map[string]any{"password": "  exact secret  "}); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("creates=%d", writes)
	}
}
func TestSuspendAndRestoreUsePartialUpdates(t *testing.T) {
	suspended := false
	writes := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/stable-id" && r.Method != http.MethodGet {
			t.Errorf("write does not use immutable ID")
		}
		if r.Method == http.MethodPatch {
			writes++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			v, ok := body["suspended"].(bool)
			if !ok || len(body) != 1 {
				t.Errorf("suspended false omitted or unrelated fields overwritten")
			}
			suspended = v
		}
		writeUser(w, "stable-id", "CtenantA", suspended)
	})
	for _, v := range []bool{true, false} {
		u, err := c.UpdateUser(context.Background(), "user@a.example", map[string]any{"suspended": v})
		if err != nil || u.Suspended != v {
			t.Fatalf("suspend=%v: user=%v err=%v", v, u, err)
		}
	}
	if writes != 2 {
		t.Fatalf("writes=%d", writes)
	}
}
func TestUnconfirmedWritesNeverRetryOrExposeSecrets(t *testing.T) {
	for _, status := range []int{500, 502, 429, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			writes := 0
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeUser(w, "stable-id", "CtenantA", false)
					return
				}
				writes++
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"PASSWORD-SECRET", "errors":[{"reason":"forbidden"}]}}`)
			})
			_, err := c.UpdateUser(context.Background(), "stable-id", map[string]any{"password": "PASSWORD-SECRET"})
			if err == nil || strings.Contains(err.Error(), "PASSWORD-SECRET") {
				t.Fatalf("unsafe error=%v", err)
			}
			if status >= 500 && !errors.Is(err, ErrUncertain) {
				t.Fatalf("server error=%v", err)
			}
			if writes != 1 {
				t.Fatalf("writes retried: %d", writes)
			}
		})
	}
}
func TestPagingQueryAndReadRetry(t *testing.T) {
	reads := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		reads++
		q := r.URL.Query()
		if q.Get("customer") != "CtenantA" || q.Get("query") != "email:ada*" || q.Get("pageToken") != "next-a" || q.Get("maxResults") != "20" {
			t.Errorf("query=%v", q)
		}
		if reads == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"users":[{"id":"a","customerId":"CtenantA"}],"nextPageToken":"next-b"}`)
	})
	p, err := c.ListUsers(context.Background(), "email:ada*", "next-a", 20)
	if err != nil || len(p.Users) != 1 || p.NextPageToken != "next-b" || reads != 2 {
		t.Fatalf("page=%v err=%v reads=%d", p, err, reads)
	}
}

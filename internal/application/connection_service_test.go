package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/repo/connection/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// stubNocoDBProvider accepts any settings; cleanupErr fails Cleanup.
type stubNocoDBProvider struct {
	cleanupErr error
}

func (stubNocoDBProvider) Type() string { return nocodb.ProviderType }
func (stubNocoDBProvider) Verify(context.Context, json.RawMessage, json.RawMessage) error {
	return nil
}
func (p *stubNocoDBProvider) Cleanup(context.Context, *connrepo.Connection) error {
	return p.cleanupErr
}

func newConnectionServer(t *testing.T) (*ConnectionServiceServer, *memory.Store, *stubNocoDBProvider) {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	svc := connection.NewService(store, cipher)
	p := &stubNocoDBProvider{}
	svc.Register(p)
	srv := NewConnectionServiceServer()
	srv.SetService(svc)
	return srv, store, p
}

func asUser(id string) context.Context {
	return auth.WithAuthenticated(context.Background(), &neoboxv1.User{Id: id}, &auth.Session{ID: "s-" + id, UserID: id})
}

func nocodbCreate(name, token string) *connect.Request[neoboxv1.CreateConnectionRequest] {
	return connect.NewRequest(&neoboxv1.CreateConnectionRequest{
		Name: name,
		Settings: &neoboxv1.CreateConnectionRequest_Nocodb{Nocodb: &neoboxv1.NocoDBConnectionSettings{
			BaseUrl: "https://noco.example.com/", ApiToken: token,
		}},
	})
}

func TestConnectionSecretsAreNeverReturned(t *testing.T) {
	srv, _, _ := newConnectionServer(t)
	ctx := asUser("u1")
	const token, newToken = "tok-SECRET-1", "tok-SECRET-2"

	var responses []proto.Message
	check := func(what string, m proto.Message, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		responses = append(responses, m)
	}

	created, err := srv.CreateConnection(ctx, nocodbCreate("home", token))
	check("Create", created.Msg, err)
	id := created.Msg.GetConnection().GetId()
	if got := created.Msg.GetConnection().GetNocodb().GetBaseUrl(); got != "https://noco.example.com" {
		t.Fatalf("base_url = %q, want the normalized URL", got)
	}
	if got := created.Msg.GetConnection().GetStatus(); got != neoboxv1.ConnectionStatus_CONNECTION_STATUS_OK {
		t.Fatalf("status = %v, want OK", got)
	}

	list, err := srv.ListConnections(ctx, connect.NewRequest(&neoboxv1.ListConnectionsRequest{}))
	check("List", list.Msg, err)
	got, err := srv.GetConnection(ctx, connect.NewRequest(&neoboxv1.GetConnectionRequest{Id: id}))
	check("Get", got.Msg, err)
	updated, err := srv.UpdateConnection(ctx, connect.NewRequest(&neoboxv1.UpdateConnectionRequest{
		Id: id, Name: "home",
		Settings: &neoboxv1.UpdateConnectionRequest_Nocodb{Nocodb: &neoboxv1.NocoDBConnectionSettings{
			BaseUrl: "https://noco.example.com", ApiToken: newToken,
		}},
	}))
	check("Update", updated.Msg, err)
	tested, err := srv.TestConnection(ctx, connect.NewRequest(&neoboxv1.TestConnectionRequest{Id: id}))
	check("Test", tested.Msg, err)

	for _, m := range responses {
		raw, err := protojson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "SECRET") {
			t.Fatalf("response leaks the secret: %s", raw)
		}
	}
}

func TestConnectionRequestValidation(t *testing.T) {
	srv, store, _ := newConnectionServer(t)
	ctx := asUser("u1")

	if _, err := srv.CreateConnection(ctx, nocodbCreate("home", "")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Create without token = %v, want InvalidArgument", err)
	}
	if _, err := srv.CreateConnection(ctx, connect.NewRequest(&neoboxv1.CreateConnectionRequest{Name: "x"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Create without settings = %v, want InvalidArgument", err)
	}

	// Settings must match the connection's provider.
	now := time.Now().UTC()
	_ = store.Create(context.Background(), &connrepo.Connection{
		ID: "w1", UserID: "u1", Provider: "wasabi", Name: "w", Config: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	})
	_, err := srv.UpdateConnection(ctx, connect.NewRequest(&neoboxv1.UpdateConnectionRequest{
		Id: "w1", Name: "w",
		Settings: &neoboxv1.UpdateConnectionRequest_Nocodb{Nocodb: &neoboxv1.NocoDBConnectionSettings{BaseUrl: "https://noco"}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Update with another provider's settings = %v, want InvalidArgument", err)
	}
}

func TestConnectionOwnershipAndDelete(t *testing.T) {
	srv, _, p := newConnectionServer(t)
	created, err := srv.CreateConnection(asUser("u1"), nocodbCreate("home", "tok"))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.GetConnection().GetId()

	if _, err := srv.GetConnection(asUser("u2"), connect.NewRequest(&neoboxv1.GetConnectionRequest{Id: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Get by another user = %v, want NotFound", err)
	}
	if list, _ := srv.ListConnections(asUser("u2"), connect.NewRequest(&neoboxv1.ListConnectionsRequest{})); len(list.Msg.GetConnections()) != 0 {
		t.Fatalf("another user lists %d connections", len(list.Msg.GetConnections()))
	}
	if _, err := srv.DeleteConnection(asUser("u2"), connect.NewRequest(&neoboxv1.DeleteConnectionRequest{Id: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Delete by another user = %v, want NotFound", err)
	}

	p.cleanupErr = fmt.Errorf("%w: a snapshot is in progress", connection.ErrBusy)
	if _, err := srv.DeleteConnection(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteConnectionRequest{Id: id})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Delete while busy = %v, want FailedPrecondition", err)
	}
	p.cleanupErr = nil
	if _, err := srv.DeleteConnection(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteConnectionRequest{Id: id})); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = srv.GetConnection(asUser("u1"), connect.NewRequest(&neoboxv1.GetConnectionRequest{Id: id}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Get after delete = %v, want NotFound", err)
	}
}

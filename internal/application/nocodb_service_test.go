package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	connmemory "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/repo/nocodb/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	"go.orx.me/apps/neo-box/internal/wasabi"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// newNocoDBServer wires the service to in-memory stores and a manager that
// is never started, so queued restores stay pending.
func newNocoDBServer(t *testing.T) (*NocoDBServiceServer, *memory.Store) {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	connStore := connmemory.New()
	conns := connection.NewService(connStore, cipher)
	ctx := context.Background()
	for _, c := range []*connrepo.Connection{
		{ID: "c1", UserID: "u1", Provider: nocodb.ProviderType},
		{ID: "c2", UserID: "u1", Provider: nocodb.ProviderType},
		{ID: "w1", UserID: "u1", Provider: wasabi.ProviderType},
		{ID: "x1", UserID: "u2", Provider: nocodb.ProviderType},
	} {
		c.Config = json.RawMessage(`{}`)
		if err := connStore.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	srv := NewNocoDBServiceServer()
	srv.SetDeps(store, backup.New(backup.Config{}, store, conns, blobs), conns)

	for _, s := range []*repo.Snapshot{
		{ID: "s1", UserID: "u1", ConnectionID: "c1", BaseID: "p1", BaseTitle: "CRM", Status: repo.StatusSucceeded,
			HookCount: 3, Tables: []repo.SnapshotTable{{ID: "m1", Title: "Tasks", HookCount: 3}},
			Trigger: repo.TriggerManual, CreatedAt: time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)},
		{ID: "s2", UserID: "u1", ConnectionID: "c1", BaseID: "p1", Status: repo.StatusFailed,
			Trigger: repo.TriggerManual, CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "x", UserID: "u2", ConnectionID: "x1", BaseID: "p9", Status: repo.StatusSucceeded,
			Trigger: repo.TriggerManual, CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
	} {
		if err := store.CreateSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	return srv, store
}

func TestDefaultRestoreTitle(t *testing.T) {
	at := time.Date(2026, 10, 1, 23, 59, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"CRM":                    "CRM (restored from 2026-10-01)",
		"Q3: plan / #1":          "Q3 plan 1 (restored from 2026-10-01)",
		"":                       "p1 (restored from 2026-10-01)",
		strings.Repeat("长", 200): strings.Repeat("长", 150-len(" (restored from 2026-10-01)")) + " (restored from 2026-10-01)",
	} {
		got := defaultRestoreTitle(&repo.Snapshot{BaseID: "p1", BaseTitle: in, CreatedAt: at})
		if got != want || !nocodb.ValidBaseTitle(got) {
			t.Errorf("defaultRestoreTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func restoreReq(snapshotID, target, title string) *connect.Request[neoboxv1.RestoreSnapshotRequest] {
	return connect.NewRequest(&neoboxv1.RestoreSnapshotRequest{SnapshotId: snapshotID, TargetConnectionId: target, Title: title})
}

func TestRestoreSnapshot(t *testing.T) {
	srv, _ := newNocoDBServer(t)
	ctx := asUser("u1")

	resp, err := srv.RestoreSnapshot(ctx, restoreReq("s1", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Msg.GetRestore()
	if r.GetStatus() != neoboxv1.RestoreStatus_RESTORE_STATUS_PENDING || r.GetTargetConnectionId() != "c1" ||
		r.GetTargetBaseTitle() != "CRM (restored from 2026-10-01)" ||
		r.GetSourceConnectionId() != "c1" || r.GetSourceBaseId() != "p1" || r.GetSourceBaseTitle() != "CRM" {
		t.Fatalf("restore = %v", r)
	}

	resp, err = srv.RestoreSnapshot(ctx, restoreReq("s1", "c2", "  CRM copy "))
	if err != nil {
		t.Fatal(err)
	}
	if r := resp.Msg.GetRestore(); r.GetTargetConnectionId() != "c2" || r.GetTargetBaseTitle() != "CRM copy" {
		t.Fatalf("restore = %v", r)
	}

	for name, tc := range map[string]struct {
		req  *connect.Request[neoboxv1.RestoreSnapshotRequest]
		code connect.Code
	}{
		"missing snapshot id":      {restoreReq("", "", ""), connect.CodeInvalidArgument},
		"another user's snapshot":  {restoreReq("x", "", ""), connect.CodeNotFound},
		"failed snapshot":          {restoreReq("s2", "", ""), connect.CodeFailedPrecondition},
		"another user's target":    {restoreReq("s1", "x1", ""), connect.CodeNotFound},
		"target of other provider": {restoreReq("s1", "w1", ""), connect.CodeNotFound},
		"title NocoDB rejects":     {restoreReq("s1", "", "CRM: copy"), connect.CodeInvalidArgument},
	} {
		if _, err := srv.RestoreSnapshot(ctx, tc.req); connect.CodeOf(err) != tc.code {
			t.Errorf("%s: err = %v, want %v", name, err, tc.code)
		}
	}
}

func TestGetSnapshotHookCounts(t *testing.T) {
	srv, _ := newNocoDBServer(t)
	ctx := asUser("u1")
	resp, err := srv.GetSnapshot(ctx, connect.NewRequest(&neoboxv1.GetSnapshotRequest{Id: "s1"}))
	if err != nil {
		t.Fatal(err)
	}
	snap := resp.Msg.GetSnapshot()
	if snap.GetHookCount() != 3 || len(snap.GetTables()) != 1 || snap.GetTables()[0].GetHookCount() != 3 {
		t.Fatalf("snapshot = %v", snap)
	}
}

func TestGetAndListRestores(t *testing.T) {
	srv, store := newNocoDBServer(t)
	ctx := asUser("u1")
	first, err := srv.RestoreSnapshot(ctx, restoreReq("s1", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RestoreSnapshot(ctx, restoreReq("s1", "c2", "")); err != nil {
		t.Fatal(err)
	}
	id := first.Msg.GetRestore().GetId()
	stored, err := store.GetRestore(ctx, "u1", id)
	if err != nil {
		t.Fatal(err)
	}
	stored.HookCount = 2
	stored.Warnings = []repo.RestoreWarning{{Code: "hooks_disabled", Table: "Tasks", Count: 2, Message: "turn them on in NocoDB"}}
	if err := store.UpdateRestore(ctx, stored); err != nil {
		t.Fatal(err)
	}

	got, err := srv.GetRestore(ctx, connect.NewRequest(&neoboxv1.GetRestoreRequest{Id: id}))
	if err != nil || got.Msg.GetRestore().GetId() != id || got.Msg.GetRestore().GetHookCount() != 2 ||
		len(got.Msg.GetRestore().GetWarnings()) != 1 || got.Msg.GetRestore().GetWarnings()[0].GetCode() != "hooks_disabled" {
		t.Fatalf("GetRestore = %v, %v", got, err)
	}
	if _, err := srv.GetRestore(asUser("u2"), connect.NewRequest(&neoboxv1.GetRestoreRequest{Id: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("GetRestore by another user = %v, want NotFound", err)
	}

	count := func(ctx context.Context, req *neoboxv1.ListRestoresRequest) int {
		t.Helper()
		resp, err := srv.ListRestores(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return len(resp.Msg.GetRestores())
	}
	if n := count(ctx, &neoboxv1.ListRestoresRequest{SnapshotId: "s1"}); n != 2 {
		t.Errorf("by snapshot = %d, want 2", n)
	}
	if n := count(ctx, &neoboxv1.ListRestoresRequest{ConnectionId: "c2"}); n != 1 {
		t.Errorf("by target connection = %d, want 1", n)
	}
	if n := count(ctx, &neoboxv1.ListRestoresRequest{ConnectionId: "c1"}); n != 2 {
		t.Errorf("by source connection = %d, want 2", n)
	}
	if n := count(asUser("u2"), &neoboxv1.ListRestoresRequest{}); n != 0 {
		t.Errorf("another user's list = %d, want 0", n)
	}
}

func TestDeleteSnapshotRefusedWhileRestoring(t *testing.T) {
	srv, store := newNocoDBServer(t)
	ctx := asUser("u1")
	resp, err := srv.RestoreSnapshot(ctx, restoreReq("s1", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	del := connect.NewRequest(&neoboxv1.DeleteSnapshotRequest{Id: "s1"})
	if _, err := srv.DeleteSnapshot(ctx, del); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("DeleteSnapshot while restoring = %v, want FailedPrecondition", err)
	}

	r, _ := store.GetRestore(context.Background(), "", resp.Msg.GetRestore().GetId())
	r.Status = repo.RestoreSucceeded
	_ = store.UpdateRestore(context.Background(), r)
	if _, err := srv.DeleteSnapshot(ctx, del); err != nil {
		t.Fatalf("DeleteSnapshot after restore: %v", err)
	}
	// The restore keeps its history after the snapshot is gone.
	got, err := srv.GetRestore(ctx, connect.NewRequest(&neoboxv1.GetRestoreRequest{Id: r.ID}))
	if err != nil || got.Msg.GetRestore().GetSourceBaseTitle() != "CRM" {
		t.Fatalf("GetRestore after snapshot delete = %v, %v", got, err)
	}
}

func TestUpsertBackupPolicyIncludeAttachments(t *testing.T) {
	srv, _ := newNocoDBServer(t)
	ctx := asUser("u1")
	upsert := func(include *bool) *neoboxv1.BackupPolicy {
		t.Helper()
		resp, err := srv.UpsertBackupPolicy(ctx, connect.NewRequest(&neoboxv1.UpsertBackupPolicyRequest{
			ConnectionId: "c1", BaseId: "p1", Enabled: true, Cron: "@daily", Retention: 3, IncludeAttachments: include,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return resp.Msg.GetPolicy()
	}
	off, on := false, true
	// A new policy includes attachments unless told otherwise; an update
	// that doesn't say keeps the current setting.
	if !upsert(nil).GetIncludeAttachments() {
		t.Fatal("new policy without the flag should include attachments")
	}
	if upsert(&off).GetIncludeAttachments() {
		t.Fatal("flag not turned off")
	}
	if upsert(nil).GetIncludeAttachments() {
		t.Fatal("an update without the flag turned it back on")
	}
	if !upsert(&on).GetIncludeAttachments() {
		t.Fatal("flag not turned on")
	}
}

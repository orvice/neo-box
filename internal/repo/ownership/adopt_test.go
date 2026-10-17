package ownership

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/ent/migrate"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
)

func TestAdoptPreservesData(t *testing.T) {
	client, db := pgtest.NewClientAndDB(t)
	ctx := context.Background()
	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("attachment content")
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	for _, old := range []string{"old-a", "old-b"} {
		client.Connection.Create().SetID(old).SetUserID(old).SetProvider("nocodb").SetName(old).SetConfig("{}").SetSecretCiphertext("sealed-secret").SetCreatedAt(now).SetUpdatedAt(now).SaveX(ctx)
		client.NocoDBSnapshot.Create().SetID(old).SetUserID(old).SetConnectionID(old).SetBaseID("base").SetStatus("succeeded").SetTrigger("manual").SetCreatedAt(now).SetObjectKey("nocodb/" + old + "/snapshot.json.gz").SaveX(ctx)
		client.NocoDBFile.Create().SetUserID(old).SetSha256(sha).SetSize(int64(len(content))).SetCreatedAt(now).SaveX(ctx)
		client.NocoDBSnapshotFile.Create().SetSnapshotID(old).SetUserID(old).SetSha256(sha).SaveX(ctx)
		client.Alert.Create().SetID(old).SetUserID(old).SetSource("nocodb").SetKind("test").SetKey("same-key").SetSeverity("info").SetTitle(old).SetCreatedAt(now).SaveX(ctx)
		if err := blobs.Put(ctx, backup.FileKey(old, sha), bytes.NewReader(content), int64(len(content)), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Adopt(ctx, db, blobs, "personal", false); err == nil {
		t.Fatal("adopted without explicit migration setting")
	}
	if got := client.Connection.GetX(ctx, "old-a").UserID; got != "old-a" {
		t.Fatal("failed adoption changed ownership")
	}
	if err := Adopt(ctx, db, blobs, "personal", true); err != nil {
		t.Fatal(err)
	}
	for _, c := range client.Connection.Query().AllX(ctx) {
		if c.UserID != "personal" || c.SecretCiphertext != "sealed-secret" {
			t.Fatal("connection ownership or credentials changed incorrectly")
		}
	}
	for _, s := range client.NocoDBSnapshot.Query().AllX(ctx) {
		if s.UserID != "personal" || s.ObjectKey != "nocodb/"+s.ID+"/snapshot.json.gz" {
			t.Fatal("snapshot or object key lost")
		}
	}
	if n := client.NocoDBFile.Query().CountX(ctx); n != 1 {
		t.Fatalf("files not deduplicated: %d", n)
	}
	if n := client.NocoDBSnapshotFile.Query().CountX(ctx); n != 2 {
		t.Fatal("snapshot references lost")
	}
	if n := client.Alert.Query().CountX(ctx); n != 2 {
		t.Fatal("alerts with duplicate keys lost")
	}
	for _, owner := range []string{"personal", "old-a", "old-b"} {
		r, err := blobs.Get(ctx, backup.FileKey(owner, sha))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("attachment lost")
		}
	}
	before := client.Alert.GetX(ctx, "old-a").Key
	if err := Adopt(ctx, db, blobs, "personal", true); err != nil {
		t.Fatal(err)
	}
	if client.Alert.GetX(ctx, "old-a").Key != before {
		t.Fatal("second adoption changed data")
	}
}

func TestCorruptAttachmentLeavesOwnershipUntouched(t *testing.T) {
	client, db := pgtest.NewClientAndDB(t)
	ctx := context.Background()
	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("expected"))
	sha := hex.EncodeToString(sum[:])
	client.NocoDBFile.Create().SetUserID("old").SetSha256(sha).SetSize(8).SetCreatedAt(time.Now()).SaveX(ctx)
	if err := blobs.Put(ctx, backup.FileKey("old", sha), bytes.NewReader([]byte("corrupt!")), 8, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err := Adopt(ctx, db, blobs, "personal", true); err == nil {
		t.Fatal("adopted corrupt attachment")
	}
	if client.NocoDBFile.Query().OnlyX(ctx).UserID != "old" {
		t.Fatal("failed adoption changed data")
	}
}

// A new business table must be included in adoption to avoid silently hiding
// records when local authentication is replaced.
func TestAdoptionCoversBusinessOwners(t *testing.T) {
	for _, table := range migrate.Tables {
		if table.Name == "auth_sessions" {
			continue
		}
		for _, column := range table.Columns {
			if column.Name != "user_id" {
				continue
			}
			found := false
			for _, name := range tables {
				found = found || name == table.Name
			}
			if !found {
				t.Errorf("business owner in %s is not migrated", table.Name)
			}
		}
	}
}

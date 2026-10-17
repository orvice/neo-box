// Package ownership consolidates legacy business data into a stable owner.
// Authentication records are never consulted or migrated.
package ownership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/blobstore"
)

// Tables with a business owner. Connection-scoped Wasabi data and NocoDB file
// sources retain their connection IDs and need no rewrite. Actor identities in
// operation logs are historical facts and are preserved.
var tables = []string{
	"connections", "nocodb_backup_policies", "nocodb_snapshots", "nocodb_restores",
	"nocodb_files", "nocodb_snapshot_files", "notification_channels", "alerts",
	"cloudflare_dns_operations", "cloudflare_resource_operations",
	"google_workspace_tenants", "google_workspace_operations",
}

// Adopt runs before background workers start, with the old application stopped.
// SQL changes commit together after every attachment is copied and verified.
// Original blobs and stored snapshot object keys are retained, making a failed
// copy or transaction safe to retry. enabled=false rejects unclaimed data.
func Adopt(ctx context.Context, db *sql.DB, blobs blobstore.Store, owner string, enabled bool) error {
	if owner == "" || owner == "." || owner == ".." || strings.ContainsAny(owner, "/\\") {
		return errors.New("invalid data owner")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+strings.Join(tables, ", ")+" IN EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock business data: %w", err)
	}
	var needsAdoption bool
	for _, table := range tables {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" WHERE user_id <> $1)", owner).Scan(&exists); err != nil {
			return err
		}
		needsAdoption = needsAdoption || exists
	}
	if !needsAdoption {
		return tx.Commit()
	}
	if !enabled {
		return errors.New("legacy business data needs adoption: back up PostgreSQL and blob storage, stop the old application, then set auth.logto.adopt_existing_data=true")
	}
	rows, err := tx.QueryContext(ctx, "SELECT user_id, sha256, size FROM nocodb_files WHERE user_id <> $1 ORDER BY id", owner)
	if err != nil {
		return err
	}
	type file struct {
		owner, sha string
		size       int64
	}
	var files []file
	for rows.Next() {
		var f file
		if err := rows.Scan(&f.owner, &f.sha, &f.size); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := copyFile(ctx, blobs, f.owner, owner, f.sha, f.size); err != nil {
			return fmt.Errorf("adopt attachment: %w", err)
		}
	}
	// Equal content hashes become one per-owner file record. All snapshot-file
	// references survive, and originals in object storage are never deleted.
	if _, err := tx.ExecContext(ctx, "DELETE FROM nocodb_files a USING nocodb_files b WHERE a.sha256 = b.sha256 AND a.id > b.id"); err != nil {
		return err
	}
	// Alert keys were only unique within an old user. Include that owner in the
	// key to retain every alert even when two users had the same dedupe key.
	if _, err := tx.ExecContext(ctx, `UPDATE alerts SET key = 'legacy/' || encode(convert_to(user_id, 'UTF8'), 'hex') || '/' || key WHERE user_id <> $1`, owner); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET user_id = $1 WHERE user_id <> $1", owner); err != nil {
			return fmt.Errorf("adopt %s: %w", table, err)
		}
	}
	return tx.Commit()
}

func copyFile(ctx context.Context, blobs blobstore.Store, from, to, sha string, size int64) error {
	if from == "" || from == "." || from == ".." || strings.ContainsAny(from, "/\\") || len(sha) != sha256.Size*2 {
		return errors.New("invalid legacy attachment key")
	}
	src, err := blobs.Get(ctx, backup.FileKey(from, sha))
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp("", "neobox-adopt-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return errors.New("attachment content does not match its stored hash or size")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return blobs.Put(ctx, backup.FileKey(to, sha), tmp, size, "application/octet-stream")
}

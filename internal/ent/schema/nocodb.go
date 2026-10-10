package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"go.orx.me/apps/neo-box/internal/repo/nocodb"
)

// NocoDBBackupPolicy schedules snapshots of one Base.
type NocoDBBackupPolicy struct {
	ent.Schema
}

func (NocoDBBackupPolicy) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_backup_policies"}}
}

func (NocoDBBackupPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.String("connection_id").Immutable(),
		field.String("base_id").Immutable(),
		field.String("user_id").Immutable(),
		field.Bool("enabled"),
		field.String("cron"),
		field.Int("retention"),
		// include_attachments makes snapshots of the Base store attachment
		// files, not only their metadata.
		field.Bool("include_attachments").Default(true),
		field.Time("updated_at"),
	}
}

func (NocoDBBackupPolicy) Indexes() []ent.Index {
	return []ent.Index{
		// Upsert key: one policy per (Connection, Base).
		index.Fields("connection_id", "base_id").Unique(),
		index.Fields("user_id", "connection_id"),
	}
}

// NocoDBSnapshot is the metadata of one capture of a Base. The content lives
// in the blob store under object_key.
type NocoDBSnapshot struct {
	ent.Schema
}

func (NocoDBSnapshot) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_snapshots"}}
}

func (NocoDBSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("connection_id").Immutable(),
		field.String("base_id").Immutable(),
		field.String("base_title").Default(""),
		field.String("status"),
		field.String("trigger").Immutable(),
		field.String("error").Default(""),
		field.String("progress").Default(""),
		field.String("object_key").Default(""),
		field.Int64("size_bytes").Default(0),
		field.Int64("record_count").Default(0),
		field.Int64("link_count").Default(0),
		field.Int("view_count").Default(0),
		field.Bool("attachments_included").Default(false),
		field.Int64("file_count").Default(0),
		field.Int64("file_bytes").Default(0),
		field.Int64("files_missing").Default(0),
		field.JSON("tables", []nocodb.SnapshotTable{}).Optional(),
		field.Time("created_at").Immutable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (NocoDBSnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
		index.Fields("connection_id", "base_id", "created_at"),
		index.Fields("status"),
	}
}

// NocoDBRestore is one rebuild of a Snapshot into a new Base. The source is
// copied from the snapshot so the history still reads correctly after the
// snapshot is deleted.
type NocoDBRestore struct {
	ent.Schema
}

func (NocoDBRestore) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_restores"}}
}

func (NocoDBRestore) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("snapshot_id").Immutable(),
		field.String("source_connection_id").Immutable(),
		field.String("source_base_id").Immutable(),
		field.String("source_base_title").Default("").Immutable(),
		field.String("target_connection_id").Immutable(),
		// target_base_id is set as soon as the new Base exists.
		field.String("target_base_id").Default(""),
		field.String("target_base_title").Immutable(),
		field.String("status"),
		field.String("error").Default(""),
		field.String("progress").Default(""),
		field.Int("table_count").Default(0),
		field.Int64("record_count").Default(0),
		field.Int64("link_count").Default(0),
		field.Int64("file_count").Default(0),
		field.Int("view_count").Default(0),
		field.JSON("warnings", []nocodb.RestoreWarning{}).Optional(),
		field.Time("created_at").Immutable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (NocoDBRestore) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "snapshot_id", "created_at"),
		index.Fields("user_id", "target_connection_id", "created_at"),
		index.Fields("source_connection_id"),
		index.Fields("status"),
	}
}

// NocoDBFile is one attachment file stored for a user, once per content.
// The bytes live in the blob store under nocodb/{user_id}/files/{sha256}.
type NocoDBFile struct {
	ent.Schema
}

func (NocoDBFile) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_files"}}
}

func (NocoDBFile) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id").Immutable(),
		field.String("sha256").Immutable(),
		field.Int64("size").Immutable(),
		field.Time("created_at").Immutable(),
	}
}

func (NocoDBFile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "sha256").Unique(),
	}
}

// NocoDBSnapshotFile records that a snapshot uses a file. A file no
// snapshot uses is deleted.
type NocoDBSnapshotFile struct {
	ent.Schema
}

func (NocoDBSnapshotFile) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_snapshot_files"}}
}

func (NocoDBSnapshotFile) Fields() []ent.Field {
	return []ent.Field{
		field.String("snapshot_id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("sha256").Immutable(),
	}
}

func (NocoDBSnapshotFile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("snapshot_id", "sha256").Unique(),
		index.Fields("user_id", "sha256"),
	}
}

// NocoDBFileSource remembers which file an attachment of a connection
// held, so a later snapshot need not download it again. NocoDB never
// changes a stored file, so the source (path or url) and size identify it.
// Only a hint: the file itself may have been deleted since.
type NocoDBFileSource struct {
	ent.Schema
}

func (NocoDBFileSource) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_file_sources"}}
}

func (NocoDBFileSource) Fields() []ent.Field {
	return []ent.Field{
		field.String("connection_id").Immutable(),
		field.Text("source").Immutable(),
		field.Int64("size").Immutable(),
		field.String("sha256"),
	}
}

func (NocoDBFileSource) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("connection_id", "source", "size").Unique(),
	}
}

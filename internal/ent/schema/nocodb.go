package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"go.orx.me/apps/neo-box/internal/repo/nocodb"
)

// NocoDBConnection is one NocoDB instance + encrypted API token owned by a
// user.
type NocoDBConnection struct {
	ent.Schema
}

func (NocoDBConnection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nocodb_connections"}}
}

func (NocoDBConnection) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("name"),
		field.String("base_url"),
		field.String("token_ciphertext").Sensitive(),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

func (NocoDBConnection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
	}
}

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

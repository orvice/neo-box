package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

var dateType = map[string]string{dialect.Postgres: "date"}

// WasabiDailyUsage is one day of Stats API utilization for a Wasabi
// connection's account (bucket empty) or one of its buckets. Every numeric
// Stats field is kept.
type WasabiDailyUsage struct {
	ent.Schema
}

func (WasabiDailyUsage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "wasabi_daily_usage"}}
}

func (WasabiDailyUsage) Fields() []ent.Field {
	counters := []string{
		"num_billable_objects", "num_billable_deleted_objects",
		"raw_storage_size_bytes", "padded_storage_size_bytes", "metadata_storage_size_bytes",
		"deleted_storage_size_bytes", "orphaned_storage_size_bytes", "min_storage_charge_bytes",
		"num_api_calls", "upload_bytes", "download_bytes", "storage_wrote_bytes", "storage_read_bytes",
		"delete_bytes", "num_get_calls", "num_put_calls", "num_delete_calls", "num_list_calls", "num_head_calls",
	}
	fields := []ent.Field{
		field.String("connection_id").Immutable(),
		// bucket is empty for account-level rows.
		field.String("bucket").Default("").Immutable(),
		field.Time("day").SchemaType(dateType).Immutable(),
		field.String("region").Default(""),
	}
	for _, name := range counters {
		fields = append(fields, field.Int64(name).Default(0))
	}
	return fields
}

func (WasabiDailyUsage) Indexes() []ent.Index {
	return []ent.Index{
		// Upsert key.
		index.Fields("connection_id", "bucket", "day").Unique(),
	}
}

// WasabiSyncState tracks how far a Wasabi connection's usage is synced.
type WasabiSyncState struct {
	ent.Schema
}

func (WasabiSyncState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "wasabi_sync_states"}}
}

func (WasabiSyncState) Fields() []ent.Field {
	return []ent.Field{
		// id is the connection ID.
		field.String("id").Immutable(),
		// last_synced_day is the newest day with account data.
		field.Time("last_synced_day").SchemaType(dateType).Optional().Nillable(),
		field.Time("last_success_at").Optional().Nillable(),
		// The backfill covers backfill_from through backfill_through so far.
		field.Time("backfill_from").SchemaType(dateType).Optional().Nillable(),
		field.Time("backfill_through").SchemaType(dateType).Optional().Nillable(),
		field.Time("backfill_completed_at").Optional().Nillable(),
		field.Time("updated_at"),
	}
}

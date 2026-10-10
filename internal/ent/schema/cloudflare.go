package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"go.orx.me/apps/neo-box/internal/cloudflare"
)

// CloudflareDNSOperation is one DNS change started from Neo Box on a
// Cloudflare connection. It is stored before the change is sent and
// finished with the outcome.
type CloudflareDNSOperation struct {
	ent.Schema
}

func (CloudflareDNSOperation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "cloudflare_dns_operations"}}
}

func (CloudflareDNSOperation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("connection_id").Immutable(),
		field.String("account_id").Immutable(),
		field.String("zone_id").Immutable(),
		field.String("zone_name").Default("").Immutable(),
		field.String("action").Immutable(),
		// record_id is empty for a create until it succeeds.
		field.String("record_id").Default(""),
		field.String("record_type").Default("").Immutable(),
		field.String("record_name").Default("").Immutable(),
		field.JSON("before", &cloudflare.DNSRecord{}).Optional().Immutable(),
		field.JSON("after", &cloudflare.DNSRecord{}).Optional(),
		// requested is the change as sent to Cloudflare.
		field.JSON("requested", map[string]any{}).Optional().Immutable(),
		field.String("status"),
		field.String("error").Default(""),
		field.String("actor_id").Immutable(),
		field.String("actor_name").Default("").Immutable(),
		field.Time("created_at").Immutable(),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (CloudflareDNSOperation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "connection_id", "zone_id", "created_at"),
		index.Fields("connection_id"),
	}
}

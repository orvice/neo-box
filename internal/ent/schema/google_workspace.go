package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkspaceTenant is one independent customer authorized for a shared Service Account.
type WorkspaceTenant struct{ ent.Schema }

func (WorkspaceTenant) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "google_workspace_tenants"}}
}
func (WorkspaceTenant) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(), field.String("user_id").Immutable(), field.String("connection_id").Immutable(),
		field.String("name"), field.String("customer_id").Immutable(), field.String("delegated_admin_email"),
		field.String("status").Default("unknown"), field.String("status_message").Default(""), field.Time("status_checked_at").Optional().Nillable(),
		field.Time("created_at").Immutable(), field.Time("updated_at"),
	}
}
func (WorkspaceTenant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("connection_id", "customer_id").Unique(), index.Fields("user_id", "connection_id"),
	}
}

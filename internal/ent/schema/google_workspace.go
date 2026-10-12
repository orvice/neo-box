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

// WorkspaceOperation is a sanitized record made before a Workspace mutation.
// It has no foreign key to a tenant: removing a tenant retains its history.
type WorkspaceOperation struct{ ent.Schema }

func (WorkspaceOperation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "google_workspace_operations"}}
}
func (WorkspaceOperation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(), field.String("user_id").Immutable(), field.String("connection_id").Immutable(),
		field.String("tenant_id").Immutable(), field.String("tenant_name").Immutable(), field.String("customer_id").Immutable(),
		field.String("action").Immutable(), field.String("target").Immutable(),
		field.JSON("requested", map[string]any{}).Optional().Immutable(), field.JSON("result", map[string]any{}).Optional(),
		field.String("status"), field.String("error").Default(""), field.String("actor_id").Immutable(), field.String("actor_name").Immutable(),
		field.Time("created_at").Immutable(), field.Time("finished_at").Optional().Nillable(),
	}
}
func (WorkspaceOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "connection_id", "tenant_id", "created_at"), index.Fields("connection_id")}
}

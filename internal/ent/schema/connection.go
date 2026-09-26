package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Connection is one account at a third-party service (a Provider), owned by
// one user.
type Connection struct {
	ent.Schema
}

func (Connection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "connections"}}
}

func (Connection) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		// provider is the provider key, e.g. "nocodb".
		field.String("provider").Immutable(),
		field.String("name"),
		// config holds the provider's non-secret settings as JSON. It is a
		// string field rather than field.JSON(json.RawMessage): on Go 1.27
		// RawMessage aliases encoding/json/jsontext, and the generated code
		// would not build on Go 1.26.
		field.String("config").SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		// secret_ciphertext is the provider's secret settings (JSON), sealed
		// with secretbox.
		field.String("secret_ciphertext").Sensitive(),
		field.String("status").Default("unknown"),
		field.String("status_message").Default(""),
		field.Time("status_checked_at").Optional().Nillable(),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

func (Connection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "provider", "created_at"),
	}
}

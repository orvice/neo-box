package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LogtoSession stores a server-side login flow or authenticated session.
// ID is a SHA-256 hash of a random cookie; tokens and state are sealed.
type LogtoSession struct{ ent.Schema }

func (LogtoSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "logto_sessions"}}
}

func (LogtoSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.Enum("kind").Values("flow", "session").Immutable(),
		field.Text("ciphertext").Sensitive(),
		field.Time("created_at").Immutable(),
		field.Time("expires_at"),
	}
}

func (LogtoSession) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at")}
}

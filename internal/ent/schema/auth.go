package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User is a person who signs in to Neo Box.
type User struct {
	ent.Schema
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "users"}}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("username").Unique(),
		field.String("display_name").Default(""),
		field.String("avatar_url").Default(""),
		field.String("email").Default(""),
		// provider + external_id identify an OAuth user; both are empty for
		// password users.
		field.String("provider").Default(""),
		field.String("external_id").Default(""),
		field.String("password_hash").Default("").Sensitive(),
		field.String("role").Default(""),
		field.Bool("disabled").Default(false),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "external_id").
			Unique().
			Annotations(entsql.IndexWhere("external_id <> ''")),
	}
}

// Session is a bearer token issued at sign-in. Only the token's sha256 is
// stored.
type Session struct {
	ent.Schema
}

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_sessions"}}
}

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("token_hash").Unique().Immutable(),
		field.Time("created_at").Immutable(),
		field.Time("expires_at"),
		field.Time("last_used_at").Optional().Nillable(),
		field.Bool("revoked").Default(false),
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
	}
}

// OAuthState is a single-use CSRF state token for one OAuth login attempt.
type OAuthState struct {
	ent.Schema
}

func (OAuthState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "oauth_states"}}
}

func (OAuthState) Fields() []ent.Field {
	return []ent.Field{
		// id is the state token itself.
		field.String("id").Immutable(),
		field.String("provider").Immutable(),
		field.String("redirect_uri").Default("").Immutable(),
		field.Time("created_at").Immutable(),
		field.Time("expires_at").Immutable(),
	}
}

func (OAuthState) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
	}
}

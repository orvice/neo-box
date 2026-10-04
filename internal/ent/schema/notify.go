package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NotificationChannel is one place a user's alerts are delivered, e.g. a
// Telegram chat. The channel's secret (a bot token) is sealed like a
// connection's credentials.
type NotificationChannel struct {
	ent.Schema
}

func (NotificationChannel) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "notification_channels"}}
}

func (NotificationChannel) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("type").Immutable(),
		field.String("name"),
		field.Bool("enabled").Default(true),
		// config is the channel type's non-secret settings as JSON (a
		// string field for the same reason as Connection.config).
		field.String("config").SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		// secret_ciphertext is the channel's secret settings (JSON), sealed
		// with secretbox.
		field.String("secret_ciphertext").Sensitive(),
		field.Time("last_sent_at").Optional().Nillable(),
		field.String("last_error").Default(""),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

func (NotificationChannel) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at")}
}

// Alert is something a user was told about. key deduplicates: whoever
// raises an alert picks a key, and a key already raised for the user is
// not raised again.
type Alert struct {
	ent.Schema
}

func (Alert) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "alerts"}}
}

func (Alert) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.String("source").Immutable(),
		field.String("connection_id").Default("").Immutable(),
		field.String("kind").Immutable(),
		field.String("key").Immutable(),
		field.String("severity").Immutable(),
		field.String("title").Immutable(),
		field.Text("body").Default("").Immutable(),
		field.String("link").Default("").Immutable(),
		field.Time("created_at").Immutable(),
		field.Time("delivered_at").Optional().Nillable(),
		field.Text("delivery_error").Default(""),
	}
}

func (Alert) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "key").Unique(),
		index.Fields("user_id", "created_at"),
	}
}

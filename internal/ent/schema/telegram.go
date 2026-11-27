package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TelegramBinding links one Neo Box user to one Telegram user (private chat)
// for the system-level Telegram bot.
type TelegramBinding struct {
	ent.Schema
}

func (TelegramBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "telegram_bindings"}}
}

func (TelegramBinding) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("user_id").Unique().Immutable(),
		field.Int64("telegram_user_id").Unique().Immutable(),
		field.Int64("chat_id"),
		field.String("telegram_username").Default(""),
		field.String("display_name").Default(""),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
	}
}

// TelegramLinkToken is a single-use, short-lived token used to link a
// Telegram private chat to a Neo Box user via /start <token>. Only the
// sha256 hex digest is stored as id.
type TelegramLinkToken struct {
	ent.Schema
}

func (TelegramLinkToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "telegram_link_tokens"}}
}

func (TelegramLinkToken) Fields() []ent.Field {
	return []ent.Field{
		// id is the sha256 hex digest of the raw link token.
		field.String("id").Immutable(),
		field.String("user_id").Immutable(),
		field.Time("created_at").Immutable(),
		field.Time("expires_at").Immutable(),
	}
}

func (TelegramLinkToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
	}
}

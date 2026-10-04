package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/alert"
	"go.orx.me/apps/neo-box/internal/ent/notificationchannel"
	repo "go.orx.me/apps/neo-box/internal/repo/notify"
)

type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

// --- channels ---

func (s *Store) CreateChannel(ctx context.Context, c *repo.Channel) error {
	err := s.client.NotificationChannel.Create().
		SetID(c.ID).
		SetUserID(c.UserID).
		SetType(c.Type).
		SetName(c.Name).
		SetEnabled(c.Enabled).
		SetConfig(configString(c.Config)).
		SetSecretCiphertext(c.SecretCiphertext).
		SetCreatedAt(c.CreatedAt).
		SetUpdatedAt(c.UpdatedAt).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert notification channel: %w", err)
	}
	return nil
}

func (s *Store) GetChannel(ctx context.Context, userID, id string) (*repo.Channel, error) {
	row, err := s.client.NotificationChannel.Query().
		Where(notificationchannel.ID(id), notificationchannel.UserID(userID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find notification channel: %w", err)
	}
	return channelFromRow(row), nil
}

func (s *Store) UpdateChannel(ctx context.Context, c *repo.Channel) error {
	n, err := s.client.NotificationChannel.Update().
		Where(notificationchannel.ID(c.ID), notificationchannel.UserID(c.UserID)).
		SetName(c.Name).
		SetEnabled(c.Enabled).
		SetConfig(configString(c.Config)).
		SetSecretCiphertext(c.SecretCiphertext).
		SetUpdatedAt(c.UpdatedAt).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("update notification channel: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteChannel(ctx context.Context, userID, id string) error {
	n, err := s.client.NotificationChannel.Delete().
		Where(notificationchannel.ID(id), notificationchannel.UserID(userID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) ListChannels(ctx context.Context, userID string) ([]*repo.Channel, error) {
	rows, err := s.client.NotificationChannel.Query().
		Where(notificationchannel.UserID(userID)).
		Order(ent.Asc(notificationchannel.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	out := make([]*repo.Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, channelFromRow(r))
	}
	return out, nil
}

func (s *Store) RecordChannelResult(ctx context.Context, id string, sentAt time.Time, errMsg string) error {
	u := s.client.NotificationChannel.UpdateOneID(id).SetLastError(errMsg)
	if errMsg == "" {
		u.SetLastSentAt(sentAt)
	}
	if err := u.Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("record notification channel result: %w", err)
	}
	return nil
}

// --- alerts ---

func (s *Store) CreateAlert(ctx context.Context, a *repo.Alert) (bool, error) {
	n, err := s.client.Alert.Create().
		SetID(a.ID).
		SetUserID(a.UserID).
		SetSource(a.Source).
		SetConnectionID(a.ConnectionID).
		SetKind(a.Kind).
		SetKey(a.Key).
		SetSeverity(string(a.Severity)).
		SetTitle(a.Title).
		SetBody(a.Body).
		SetLink(a.Link).
		SetCreatedAt(a.CreatedAt).
		OnConflictColumns(alert.FieldUserID, alert.FieldKey).
		DoNothing().
		ID(ctx)
	if err != nil {
		// DO NOTHING returns no row when the key exists.
		if ent.IsNotFound(err) || errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("insert alert: %w", err)
	}
	return n == a.ID, nil
}

func (s *Store) GetAlert(ctx context.Context, id string) (*repo.Alert, error) {
	row, err := s.client.Alert.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find alert: %w", err)
	}
	return alertFromRow(row), nil
}

func (s *Store) SetAlertDelivery(ctx context.Context, id string, deliveredAt time.Time, deliveryError string) error {
	u := s.client.Alert.UpdateOneID(id).SetDeliveryError(deliveryError)
	if !deliveredAt.IsZero() {
		u.SetDeliveredAt(deliveredAt)
	}
	if err := u.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return repo.ErrNotFound
		}
		return fmt.Errorf("record alert delivery: %w", err)
	}
	return nil
}

func (s *Store) ListAlerts(ctx context.Context, userID string, limit int) ([]*repo.Alert, error) {
	q := s.client.Alert.Query().
		Where(alert.UserID(userID)).
		Order(ent.Desc(alert.FieldCreatedAt), ent.Desc(alert.FieldID))
	if limit > 0 {
		q = q.Limit(limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	return alertsFromRows(rows), nil
}

func (s *Store) ListUndelivered(ctx context.Context, since time.Time) ([]*repo.Alert, error) {
	rows, err := s.client.Alert.Query().
		Where(
			alert.CreatedAtGTE(since),
			alert.DeliveredAtIsNil(),
			alert.DeliveryError(""),
		).
		Order(ent.Asc(alert.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list undelivered alerts: %w", err)
	}
	return alertsFromRows(rows), nil
}

// --- conversions ---

func configString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func channelFromRow(r *ent.NotificationChannel) *repo.Channel {
	c := &repo.Channel{
		ID: r.ID, UserID: r.UserID, Type: r.Type, Name: r.Name, Enabled: r.Enabled,
		Config: json.RawMessage(r.Config), SecretCiphertext: r.SecretCiphertext,
		LastError: r.LastError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.LastSentAt != nil {
		c.LastSentAt = *r.LastSentAt
	}
	return c
}

func alertFromRow(r *ent.Alert) *repo.Alert {
	a := &repo.Alert{
		ID: r.ID, UserID: r.UserID, Source: r.Source, ConnectionID: r.ConnectionID, Kind: r.Kind,
		Key: r.Key, Severity: repo.Severity(r.Severity), Title: r.Title, Body: r.Body, Link: r.Link,
		CreatedAt: r.CreatedAt, DeliveryError: r.DeliveryError,
	}
	if r.DeliveredAt != nil {
		a.DeliveredAt = *r.DeliveredAt
	}
	return a
}

func alertsFromRows(rows []*ent.Alert) []*repo.Alert {
	out := make([]*repo.Alert, 0, len(rows))
	for _, r := range rows {
		out = append(out, alertFromRow(r))
	}
	return out
}

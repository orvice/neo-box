package postgres

import (
	"context"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/cloudflarednsoperation"
	repo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) CreateDNSOperation(ctx context.Context, op *repo.DNSOperation) error {
	c := s.client.CloudflareDNSOperation.Create().
		SetID(op.ID).
		SetUserID(op.UserID).
		SetConnectionID(op.ConnectionID).
		SetAccountID(op.AccountID).
		SetZoneID(op.ZoneID).
		SetZoneName(op.ZoneName).
		SetAction(string(op.Action)).
		SetRecordID(op.RecordID).
		SetRecordType(op.RecordType).
		SetRecordName(op.RecordName).
		SetStatus(string(op.Status)).
		SetError(op.Error).
		SetActorID(op.ActorID).
		SetActorName(op.ActorName).
		SetCreatedAt(op.CreatedAt).
		SetNillableFinishedAt(nilIfZero(op.FinishedAt))
	if op.Before != nil {
		c.SetBefore(op.Before)
	}
	if op.After != nil {
		c.SetAfter(op.After)
	}
	if op.Requested != nil {
		c.SetRequested(op.Requested)
	}
	if err := c.Exec(ctx); err != nil {
		return fmt.Errorf("insert dns operation: %w", err)
	}
	return nil
}

func (s *Store) FinishDNSOperation(ctx context.Context, op *repo.DNSOperation) error {
	u := s.client.CloudflareDNSOperation.UpdateOneID(op.ID).
		SetRecordID(op.RecordID).
		SetStatus(string(op.Status)).
		SetError(op.Error).
		SetNillableFinishedAt(nilIfZero(op.FinishedAt))
	if op.After != nil {
		u.SetAfter(op.After)
	} else {
		u.ClearAfter()
	}
	if err := u.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return repo.ErrNotFound
		}
		return fmt.Errorf("finish dns operation: %w", err)
	}
	return nil
}

func (s *Store) ListDNSOperations(ctx context.Context, f repo.Filter) ([]*repo.DNSOperation, int, error) {
	q := s.client.CloudflareDNSOperation.Query().Where(
		cloudflarednsoperation.UserID(f.UserID),
		cloudflarednsoperation.ConnectionID(f.ConnectionID),
	)
	if f.ZoneID != "" {
		q = q.Where(cloudflarednsoperation.ZoneID(f.ZoneID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count dns operations: %w", err)
	}
	q = q.Order(ent.Desc(cloudflarednsoperation.FieldCreatedAt), ent.Desc(cloudflarednsoperation.FieldID)).Offset(f.Offset)
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list dns operations: %w", err)
	}
	out := make([]*repo.DNSOperation, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, total, nil
}

func (s *Store) DeleteConnectionData(ctx context.Context, connectionID string) error {
	_, err := s.client.CloudflareDNSOperation.Delete().
		Where(cloudflarednsoperation.ConnectionID(connectionID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete dns operations: %w", err)
	}
	return nil
}

func fromRow(row *ent.CloudflareDNSOperation) *repo.DNSOperation {
	op := &repo.DNSOperation{
		ID: row.ID, UserID: row.UserID, ConnectionID: row.ConnectionID, AccountID: row.AccountID,
		ZoneID: row.ZoneID, ZoneName: row.ZoneName, Action: repo.Action(row.Action),
		RecordID: row.RecordID, RecordType: row.RecordType, RecordName: row.RecordName,
		Before: row.Before, After: row.After, Requested: row.Requested,
		Status: repo.Status(row.Status), Error: row.Error,
		ActorID: row.ActorID, ActorName: row.ActorName, CreatedAt: row.CreatedAt,
	}
	if row.FinishedAt != nil {
		op.FinishedAt = *row.FinishedAt
	}
	return op
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

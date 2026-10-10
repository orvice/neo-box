package cloudflareacct

import (
	"context"
	"fmt"
	"time"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

const (
	// writeTimeout bounds a change at Cloudflare. It does not end with the
	// request that started it, so a closed browser tab does not leave the
	// outcome unknown.
	writeTimeout  = time.Minute
	maxErrorChars = 1000
)

// Records returns one page of a Zone's DNS records.
func (a *Account) Records(ctx context.Context, zoneID string, q cloudflare.RecordQuery) ([]cloudflare.DNSRecord, *cloudflare.PageInfo, error) {
	if _, err := a.Zone(ctx, zoneID); err != nil {
		return nil, nil, err
	}
	records, info, err := a.api.ListDNSRecords(ctx, zoneID, q)
	if err != nil {
		return nil, nil, a.classify(ctx, err, "read DNS records", "DNS Read")
	}
	return records, info, nil
}

// Actor is who starts a change.
type Actor struct {
	ID, Name string
}

// Change is a DNS change Cloudflare made.
type Change struct {
	// Record is the record after the change; nil for a delete.
	Record    *cloudflare.DNSRecord
	Operation *cfrepo.DNSOperation
	// LogErr says the change's outcome could not be added to the operation
	// log, which then shows it as pending.
	LogErr error
}

// CreateRecord validates in and creates it in the Zone.
func (a *Account) CreateRecord(ctx context.Context, actor Actor, zoneID string, in cloudflare.RecordInput) (*Change, error) {
	in = in.Normalize()
	if err := in.Validate(); err != nil {
		return nil, err
	}
	zone, err := a.writableZone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	return a.change(ctx, actor, zone, recordChange{
		action: cfrepo.ActionCreate, recordType: in.Type, recordName: in.Name, requested: in.CreateBody(),
		send: func(ctx context.Context) (*cloudflare.DNSRecord, error) {
			return a.api.CreateDNSRecord(ctx, zone.ID, in)
		},
	})
}

// UpdateRecord changes the fields the form shows and keeps the record's
// other attributes. The type cannot change.
func (a *Account) UpdateRecord(ctx context.Context, actor Actor, zoneID, recordID string, in cloudflare.RecordInput) (*Change, error) {
	in = in.Normalize()
	if err := in.Validate(); err != nil {
		return nil, err
	}
	zone, before, err := a.editableRecord(ctx, zoneID, recordID)
	if err != nil {
		return nil, err
	}
	if in.Type != before.Type {
		return nil, &cloudflare.FieldError{Field: "type", Message: "cannot be changed; delete the record and create a new one"}
	}
	return a.change(ctx, actor, zone, recordChange{
		action: cfrepo.ActionUpdate, before: before, requested: in.EditBody(before.Proxiable),
		send: func(ctx context.Context) (*cloudflare.DNSRecord, error) {
			return a.api.EditDNSRecord(ctx, zone.ID, before.ID, in, before)
		},
	})
}

// SetProxied changes only whether Cloudflare proxies the record.
func (a *Account) SetProxied(ctx context.Context, actor Actor, zoneID, recordID string, proxied bool) (*Change, error) {
	zone, before, err := a.editableRecord(ctx, zoneID, recordID)
	if err != nil {
		return nil, err
	}
	if !before.Proxiable {
		return nil, fmt.Errorf("%w: Cloudflare cannot proxy this %s record", ErrNotEditable, before.Type)
	}
	return a.change(ctx, actor, zone, recordChange{
		action: cfrepo.ActionSetProxied, before: before, requested: cloudflare.ProxiedBody(proxied),
		send: func(ctx context.Context) (*cloudflare.DNSRecord, error) {
			return a.api.SetDNSRecordProxied(ctx, zone.ID, before.ID, proxied)
		},
	})
}

// DeleteRecord deletes a record of a type Neo Box edits.
func (a *Account) DeleteRecord(ctx context.Context, actor Actor, zoneID, recordID string) (*Change, error) {
	zone, before, err := a.editableRecord(ctx, zoneID, recordID)
	if err != nil {
		return nil, err
	}
	return a.change(ctx, actor, zone, recordChange{
		action: cfrepo.ActionDelete, before: before,
		send: func(ctx context.Context) (*cloudflare.DNSRecord, error) {
			return nil, a.api.DeleteDNSRecord(ctx, zone.ID, before.ID)
		},
	})
}

// writableZone is a Zone of the Account whose records Cloudflare does not
// say are read-only for this token.
func (a *Account) writableZone(ctx context.Context, zoneID string) (*cloudflare.Zone, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if zone.DNSAccess() == cloudflare.DNSAccessRead {
		return nil, ErrReadOnly
	}
	return zone, nil
}

// editableRecord reads a record of the Zone (looked up through the Zone, so
// a record of any other Zone is not found) whose type Neo Box edits.
func (a *Account) editableRecord(ctx context.Context, zoneID, recordID string) (*cloudflare.Zone, *cloudflare.DNSRecord, error) {
	zone, err := a.writableZone(ctx, zoneID)
	if err != nil {
		return nil, nil, err
	}
	rec, err := a.api.GetDNSRecord(ctx, zone.ID, recordID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return nil, nil, ErrRecordNotFound
		}
		return nil, nil, a.classify(ctx, err, "read DNS records", "DNS Read")
	}
	if rec.ID != recordID {
		return nil, nil, ErrRecordNotFound
	}
	if !cloudflare.Editable(rec.Type) {
		return nil, nil, fmt.Errorf("%w: Neo Box does not change %s records", ErrNotEditable, rec.Type)
	}
	return zone, rec, nil
}

// recordChange is one change to a record, about to be sent.
type recordChange struct {
	action cfrepo.Action
	// before is the record as read; nil for a create, which names the
	// record with recordType and recordName instead.
	before                 *cloudflare.DNSRecord
	recordType, recordName string
	// requested is the request body, kept in the log; nil for a delete.
	requested map[string]any
	send      func(context.Context) (*cloudflare.DNSRecord, error)
}

// change logs the operation as pending, sends it once, and logs its
// outcome. Without the pending entry nothing is sent. A change whose answer
// never came is logged as unknown and not sent again. If the outcome can't
// be logged, the change is still reported as it went, and the log keeps
// showing it pending.
func (a *Account) change(ctx context.Context, actor Actor, zone *cloudflare.Zone, c recordChange) (*Change, error) {
	logger := log.FromContext(ctx)
	op := &cfrepo.DNSOperation{
		ID: a.m.newID(), UserID: a.conn.UserID, ConnectionID: a.conn.ID, AccountID: a.ID,
		ZoneID: zone.ID, ZoneName: zone.Name, Action: c.action,
		RecordType: c.recordType, RecordName: c.recordName, Before: c.before, Requested: c.requested,
		Status: cfrepo.StatusPending, ActorID: actor.ID, ActorName: actor.Name, CreatedAt: a.m.now().UTC(),
	}
	if c.before != nil {
		op.RecordID, op.RecordType, op.RecordName = c.before.ID, c.before.Type, c.before.Name
	}
	if err := a.m.repo.CreateDNSOperation(ctx, op); err != nil {
		logger.Error("log dns operation failed", "connection_id", a.conn.ID, "zone_id", zone.ID, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrLogUnavailable, err)
	}

	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	rec, err := c.send(wctx)
	cancel()
	switch {
	case err == nil:
		op.Status = cfrepo.StatusSucceeded
		if c.action != cfrepo.ActionDelete {
			op.After, op.RecordID = rec, rec.ID
		}
	case cloudflare.IsUncertain(err):
		op.Status = cfrepo.StatusUnknown
	default:
		op.Status = cfrepo.StatusFailed
	}
	if err != nil {
		op.Error = truncate(err.Error(), maxErrorChars)
	}
	op.FinishedAt = a.m.now().UTC()

	var logErr error
	if ferr := a.m.repo.FinishDNSOperation(context.WithoutCancel(ctx), op); ferr != nil {
		logger.Error("log dns operation outcome failed", "operation_id", op.ID, "status", op.Status, "err", ferr)
		logErr = ferr
	}
	if err != nil {
		return nil, a.classify(ctx, err, "change DNS records", "DNS Write")
	}
	return &Change{Record: rec, Operation: op, LogErr: logErr}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

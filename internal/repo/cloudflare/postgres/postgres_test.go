package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	repo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func op(id, user, conn, zone string, at time.Time) *repo.DNSOperation {
	return &repo.DNSOperation{
		ID: id, UserID: user, ConnectionID: conn, AccountID: "acc", ZoneID: zone, ZoneName: zone + ".example",
		Action: repo.ActionCreate, RecordType: "A", RecordName: "www." + zone + ".example",
		Status: repo.StatusPending, ActorID: user, ActorName: "name-" + user, CreatedAt: at,
	}
}

func TestDNSOperationOutcome(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	prio := 10
	update := op("o1", "u1", "c1", "z1", t0)
	update.Action, update.RecordID = repo.ActionUpdate, "r1"
	update.Before = &cloudflare.DNSRecord{
		ID: "r1", Type: "MX", Name: "mail.z1.example", Content: "mx1.example", TTL: 1, Priority: &prio,
		Comment: "keep me", Tags: []string{"team:mail"},
	}
	if err := s.CreateDNSOperation(ctx, update); err != nil {
		t.Fatalf("CreateDNSOperation: %v", err)
	}

	update.Status, update.FinishedAt = repo.StatusSucceeded, t0.Add(time.Second)
	update.After = &cloudflare.DNSRecord{ID: "r1", Type: "MX", Name: "mail.z1.example", Content: "mx2.example", TTL: 300, Priority: &prio}
	if err := s.FinishDNSOperation(ctx, update); err != nil {
		t.Fatalf("FinishDNSOperation: %v", err)
	}

	create := op("o2", "u1", "c1", "z1", t0.Add(time.Minute))
	create.Requested = map[string]any{"type": "A", "name": "www.z1.example", "content": "192.0.2.9", "ttl": 1}
	if err := s.CreateDNSOperation(ctx, create); err != nil {
		t.Fatal(err)
	}
	create.Status, create.Error, create.FinishedAt = repo.StatusUnknown, "timeout", t0.Add(2*time.Minute)
	if err := s.FinishDNSOperation(ctx, create); err != nil {
		t.Fatal(err)
	}

	got, total, err := s.ListDNSOperations(ctx, repo.Filter{UserID: "u1", ConnectionID: "c1", Limit: 10})
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("ListDNSOperations = %d of %d, %v", len(got), total, err)
	}
	if got[0].ID != "o2" || got[0].Status != repo.StatusUnknown || got[0].Error != "timeout" ||
		got[0].Before != nil || got[0].After != nil || got[0].RecordID != "" || got[0].Requested["content"] != "192.0.2.9" {
		t.Fatalf("newest = %+v", got[0])
	}
	if got[1].Requested != nil {
		t.Fatalf("requested where none was given: %+v", got[1].Requested)
	}
	o := got[1]
	if o.ID != "o1" || o.Status != repo.StatusSucceeded || !o.FinishedAt.Equal(t0.Add(time.Second)) ||
		o.ActorName != "name-u1" || o.AccountID != "acc" || o.ZoneName != "z1.example" || o.RecordID != "r1" {
		t.Fatalf("oldest = %+v", o)
	}
	if o.Before == nil || o.Before.Content != "mx1.example" || o.Before.Comment != "keep me" || *o.Before.Priority != 10 ||
		o.After == nil || o.After.Content != "mx2.example" || o.After.TTL != 300 {
		t.Fatalf("before/after = %+v / %+v", o.Before, o.After)
	}
}

func TestDNSOperationScopingPagingAndCleanup(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.CreateDNSOperation(ctx, op(fmt.Sprintf("a%d", i), "u1", "c1", "z1", t0.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []*repo.DNSOperation{
		op("b1", "u1", "c1", "z2", t0),
		op("c1", "u1", "c2", "z1", t0),
		op("d1", "u2", "c1", "z1", t0),
	} {
		if err := s.CreateDNSOperation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	page, total, err := s.ListDNSOperations(ctx, repo.Filter{UserID: "u1", ConnectionID: "c1", ZoneID: "z1", Offset: 2, Limit: 2})
	if err != nil || total != 5 || len(page) != 2 || page[0].ID != "a2" || page[1].ID != "a1" {
		t.Fatalf("page 2 = %v of %d, %v", ids(page), total, err)
	}
	if all, total, _ := s.ListDNSOperations(ctx, repo.Filter{UserID: "u1", ConnectionID: "c1", Limit: 50}); total != 6 || len(all) != 6 {
		t.Fatalf("connection's operations = %v of %d", ids(all), total)
	}
	if other, total, _ := s.ListDNSOperations(ctx, repo.Filter{UserID: "u2", ConnectionID: "c2", Limit: 50}); total != 0 || len(other) != 0 {
		t.Fatalf("another user's view of c2 = %v", ids(other))
	}

	if err := s.DeleteConnectionData(ctx, "c1"); err != nil {
		t.Fatalf("DeleteConnectionData: %v", err)
	}
	if left, total, _ := s.ListDNSOperations(ctx, repo.Filter{UserID: "u1", ConnectionID: "c1", Limit: 50}); total != 0 || len(left) != 0 {
		t.Fatalf("left after cleanup = %v", ids(left))
	}
	if kept, _, _ := s.ListDNSOperations(ctx, repo.Filter{UserID: "u1", ConnectionID: "c2", Limit: 50}); len(kept) != 1 {
		t.Fatalf("other connection's operations = %v", ids(kept))
	}
}

func ids(ops []*repo.DNSOperation) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.ID
	}
	return out
}

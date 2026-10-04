package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	repo "go.orx.me/apps/neo-box/internal/repo/notify"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func TestChannelRoundTrip(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	c := &repo.Channel{
		ID: "ch1", UserID: "u1", Type: "telegram", Name: "phone", Enabled: true,
		Config: json.RawMessage(`{"chat_id":"42"}`), SecretCiphertext: "sealed", CreatedAt: t0, UpdatedAt: t0,
	}
	if err := s.CreateChannel(ctx, c); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if _, err := s.GetChannel(ctx, "u2", "ch1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetChannel other user = %v", err)
	}
	c.Name, c.Enabled, c.Config, c.UpdatedAt = "laptop", false, json.RawMessage(`{"chat_id":"43"}`), t0.Add(time.Minute)
	if err := s.UpdateChannel(ctx, c); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	if err := s.UpdateChannel(ctx, &repo.Channel{ID: "ch1", UserID: "u2"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("UpdateChannel other user = %v", err)
	}
	if err := s.RecordChannelResult(ctx, "ch1", t0.Add(2*time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordChannelResult(ctx, "ch1", t0.Add(3*time.Minute), "chat not found"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChannel(ctx, "u1", "ch1")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]string
	_ = json.Unmarshal(got.Config, &cfg)
	if got.Name != "laptop" || got.Enabled || cfg["chat_id"] != "43" || got.SecretCiphertext != "sealed" ||
		!got.LastSentAt.Equal(t0.Add(2*time.Minute)) || got.LastError != "chat not found" {
		t.Fatalf("channel = %+v", got)
	}
	if list, _ := s.ListChannels(ctx, "u1"); len(list) != 1 {
		t.Fatalf("ListChannels = %+v", list)
	}
	if err := s.DeleteChannel(ctx, "u2", "ch1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("DeleteChannel other user = %v", err)
	}
	if err := s.DeleteChannel(ctx, "u1", "ch1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListChannels(ctx, "u1"); len(list) != 0 {
		t.Fatalf("channels left = %+v", list)
	}
}

func TestAlertsDedupeAndDelivery(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	alert := func(id, user, key string, at time.Time) *repo.Alert {
		return &repo.Alert{
			ID: id, UserID: user, Source: "nocodb", ConnectionID: "c1", Kind: "snapshot_failed",
			Key: key, Severity: repo.SeverityWarning, Title: "t", Body: "b", Link: "/x", CreatedAt: at,
		}
	}
	for _, tc := range []struct {
		a    *repo.Alert
		want bool
	}{
		{alert("a1", "u1", "k1", t0), true},
		{alert("a2", "u1", "k1", t0.Add(time.Minute)), false}, // same key
		{alert("a3", "u2", "k1", t0.Add(time.Minute)), true},  // other user
		{alert("a4", "u1", "k2", t0.Add(2*time.Minute)), true},
	} {
		created, err := s.CreateAlert(ctx, tc.a)
		if err != nil || created != tc.want {
			t.Fatalf("CreateAlert(%s) = %v, %v; want %v", tc.a.ID, created, err, tc.want)
		}
	}
	list, err := s.ListAlerts(ctx, "u1", 10)
	if err != nil || len(list) != 2 || list[0].ID != "a4" || list[1].Severity != repo.SeverityWarning {
		t.Fatalf("ListAlerts = %+v, %v", list, err)
	}
	if err := s.SetAlertDelivery(ctx, "a1", t0.Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlertDelivery(ctx, "a3", time.Time{}, "no channels"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListUndelivered(ctx, t0)
	if err != nil || len(pending) != 1 || pending[0].ID != "a4" {
		t.Fatalf("ListUndelivered = %+v, %v", pending, err)
	}
	a1, _ := s.GetAlert(ctx, "a1")
	if !a1.DeliveredAt.Equal(t0.Add(time.Hour)) || a1.Link != "/x" {
		t.Fatalf("a1 = %+v", a1)
	}
}

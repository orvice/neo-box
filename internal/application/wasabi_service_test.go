package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"go.orx.me/apps/neo-box/internal/connection"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	connmemory "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	wasabirepo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	wasabimemory "go.orx.me/apps/neo-box/internal/repo/wasabi/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	"go.orx.me/apps/neo-box/internal/wasabi"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// stubWasabiProvider accepts any key.
type stubWasabiProvider struct{}

func (stubWasabiProvider) Type() string { return wasabi.ProviderType }
func (stubWasabiProvider) Verify(context.Context, json.RawMessage, json.RawMessage) error {
	return nil
}
func (stubWasabiProvider) Cleanup(context.Context, *connrepo.Connection) error { return nil }

type fakeSyncer struct{ refreshed []string }

func (f *fakeSyncer) Syncing(string) bool { return false }
func (f *fakeSyncer) Refresh(id string)   { f.refreshed = append(f.refreshed, id) }
func wasabiDay(s string) time.Time        { t, _ := time.Parse(wasabi.DateLayout, s); return t }
func wasabiCreate(name string, s *neoboxv1.WasabiConnectionSettings) *connect.Request[neoboxv1.CreateConnectionRequest] {
	return connect.NewRequest(&neoboxv1.CreateConnectionRequest{
		Name: name, Settings: &neoboxv1.CreateConnectionRequest_Wasabi{Wasabi: s},
	})
}

func TestWasabiConnectionSettings(t *testing.T) {
	cipher, _ := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	svc := connection.NewService(connmemory.New(), cipher)
	svc.Register(stubWasabiProvider{})
	srv := NewConnectionServiceServer()
	srv.SetService(svc)
	ctx := asUser("u1")

	for name, s := range map[string]*neoboxv1.WasabiConnectionSettings{
		"no key id":     {SecretKey: "sk"},
		"no secret":     {AccessKeyId: "AK"},
		"bad anchor":    {AccessKeyId: "AK", SecretKey: "sk", BillingCycleAnchor: "01/08/2026"},
		"future anchor": {AccessKeyId: "AK", SecretKey: "sk", BillingCycleAnchor: time.Now().AddDate(0, 1, 0).Format(wasabi.DateLayout)},
		"bad price":     {AccessKeyId: "AK", SecretKey: "sk", PricePerTbMonth: -1},
	} {
		if _, err := srv.CreateConnection(ctx, wasabiCreate("w", s)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}

	created, err := srv.CreateConnection(ctx, wasabiCreate("w", &neoboxv1.WasabiConnectionSettings{
		AccessKeyId: " AK ", SecretKey: "SK-SECRET", BillingCycleAnchor: "2026-08-01", CostEstimateEnabled: true,
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg := created.Msg.GetConnection().GetWasabi()
	if cfg.GetAccessKeyId() != "AK" || cfg.GetPricePerTbMonth() != wasabi.DefaultPricePerTBMonth ||
		cfg.GetBillingCycleAnchor() != "2026-08-01" || !cfg.GetCostEstimateEnabled() ||
		created.Msg.GetConnection().GetProvider() != neoboxv1.Provider_PROVIDER_WASABI {
		t.Fatalf("config = %+v", created.Msg.GetConnection())
	}
	raw, _ := protojson.Marshal(created.Msg)
	if strings.Contains(string(raw), "SK-SECRET") {
		t.Fatalf("response leaks the secret key: %s", raw)
	}

	list, err := srv.ListConnections(ctx, connect.NewRequest(&neoboxv1.ListConnectionsRequest{Provider: neoboxv1.Provider_PROVIDER_WASABI}))
	if err != nil || len(list.Msg.GetConnections()) != 1 {
		t.Fatalf("List(wasabi) = %v, %v", list, err)
	}
}

type wasabiFixture struct {
	srv    *WasabiServiceServer
	usage  *wasabimemory.Store
	syncer *fakeSyncer
}

func newWasabiFixture(t *testing.T, costEstimate bool) *wasabiFixture {
	t.Helper()
	cipher, _ := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	conns := connmemory.New()
	svc := connection.NewService(conns, cipher)
	now := time.Now().UTC()
	cfg, _ := json.Marshal(wasabi.ConnectionConfig{AccessKeyID: "AK", CostEstimateEnabled: costEstimate})
	_ = conns.Create(context.Background(), &connrepo.Connection{
		ID: "w1", UserID: "u1", Provider: wasabi.ProviderType, Name: "w", Config: cfg, CreatedAt: now, UpdatedAt: now,
	})
	_ = conns.Create(context.Background(), &connrepo.Connection{
		ID: "n1", UserID: "u1", Provider: "nocodb", Name: "n", Config: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	})

	f := &wasabiFixture{srv: NewWasabiServiceServer(), usage: wasabimemory.New(), syncer: &fakeSyncer{}}
	f.srv.SetDeps(f.usage, f.syncer, svc)
	f.srv.now = func() time.Time { return wasabiDay("2026-09-30").Add(10 * time.Hour) }

	var rows []wasabi.Usage
	for d := wasabiDay("2026-09-01"); !d.After(wasabiDay("2026-09-29")); d = d.AddDate(0, 0, 1) {
		rows = append(rows,
			wasabi.Usage{Day: d, PaddedStorageSizeBytes: 2 << 40, DownloadBytes: 1 << 30},
			wasabi.Usage{Day: d, Bucket: "media", Region: "us-east-1", PaddedStorageSizeBytes: 2 << 40})
	}
	// "old" is gone after 09-10.
	rows = append(rows, wasabi.Usage{Day: wasabiDay("2026-09-10"), Bucket: "old", Region: "eu-central-1"})
	_ = f.usage.UpsertUsage(context.Background(), "w1", rows)
	_ = f.usage.SaveSyncState(context.Background(), &wasabirepo.SyncState{
		ConnectionID: "w1", BackfillFrom: wasabiDay("2025-09-30"), BackfillThrough: wasabiDay("2026-09-30"),
		BackfillCompletedAt: now, LastSuccessAt: now, LastSyncedDay: wasabiDay("2026-09-29"),
	})
	return f
}

func TestWasabiOverview(t *testing.T) {
	f := newWasabiFixture(t, true)
	resp, err := f.srv.GetWasabiOverview(asUser("u1"), connect.NewRequest(&neoboxv1.GetWasabiOverviewRequest{ConnectionId: "w1"}))
	if err != nil {
		t.Fatalf("GetWasabiOverview: %v", err)
	}
	m := resp.Msg
	if m.GetLatest().GetDay() != "2026-09-29" || m.GetLatest().GetActiveStorageBytes() != 2<<40 || m.GetBucketCount() != 1 {
		t.Fatalf("latest = %+v, buckets = %d", m.GetLatest(), m.GetBucketCount())
	}
	e := m.GetCostEstimate()
	// Rolling 30 days (09-01..09-30), 29 days of 2 TB at 7.99/TB-month.
	if !e.GetRolling() || e.GetPeriodStart() != "2026-09-01" || e.GetDaysWithData() != 29 {
		t.Fatalf("estimate period = %+v", e)
	}
	if want := 29 * 2 * 7.99 / 30; e.GetCostToDate() < want-1e-9 || e.GetCostToDate() > want+1e-9 {
		t.Fatalf("cost to date = %v, want %v", e.GetCostToDate(), want)
	}
	if s := m.GetSync(); !s.GetBackfillComplete() || s.GetBackfillProgress() != 1 || s.GetLastSyncedDay() != "2026-09-29" {
		t.Fatalf("sync = %+v", s)
	}

	off := newWasabiFixture(t, false)
	resp, err = off.srv.GetWasabiOverview(asUser("u1"), connect.NewRequest(&neoboxv1.GetWasabiOverviewRequest{ConnectionId: "w1"}))
	if err != nil || resp.Msg.GetCostEstimate() != nil {
		t.Fatalf("estimate should be omitted when disabled: %+v, %v", resp, err)
	}
}

func TestWasabiBucketsAndUsage(t *testing.T) {
	f := newWasabiFixture(t, true)
	ctx := asUser("u1")

	list, err := f.srv.ListWasabiBuckets(ctx, connect.NewRequest(&neoboxv1.ListWasabiBucketsRequest{ConnectionId: "w1"}))
	if err != nil || len(list.Msg.GetBuckets()) != 1 || list.Msg.GetBuckets()[0].GetName() != "media" {
		t.Fatalf("ListWasabiBuckets = %+v, %v", list, err)
	}
	all, _ := f.srv.ListWasabiBuckets(ctx, connect.NewRequest(&neoboxv1.ListWasabiBucketsRequest{ConnectionId: "w1", IncludeDeleted: true}))
	if b := all.Msg.GetBuckets(); len(b) != 2 || b[1].GetName() != "old" || !b[1].GetDeleted() || b[1].GetRegion() != "eu-central-1" {
		t.Fatalf("ListWasabiBuckets(include_deleted) = %+v", b)
	}

	usage, err := f.srv.GetWasabiUsage(ctx, connect.NewRequest(&neoboxv1.GetWasabiUsageRequest{ConnectionId: "w1", From: "2026-09-20"}))
	if err != nil || len(usage.Msg.GetDays()) != 10 || usage.Msg.GetDays()[0].GetDay() != "2026-09-20" {
		t.Fatalf("GetWasabiUsage = %d days, %v", len(usage.Msg.GetDays()), err)
	}
	bucket, _ := f.srv.GetWasabiUsage(ctx, connect.NewRequest(&neoboxv1.GetWasabiUsageRequest{ConnectionId: "w1", Bucket: "media"}))
	if len(bucket.Msg.GetDays()) != 29 || bucket.Msg.GetDays()[0].GetBucket() != "media" {
		t.Fatalf("bucket usage = %d days", len(bucket.Msg.GetDays()))
	}
	for _, bad := range []*neoboxv1.GetWasabiUsageRequest{
		{ConnectionId: "w1", From: "2026-09-30", To: "2026-09-01"},
		{ConnectionId: "w1", From: "Sept 1"},
		{ConnectionId: "w1", From: "2020-01-01"},
	} {
		if _, err := f.srv.GetWasabiUsage(ctx, connect.NewRequest(bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("GetWasabiUsage(%+v) = %v, want InvalidArgument", bad, err)
		}
	}
}

func TestWasabiSyncAndScoping(t *testing.T) {
	f := newWasabiFixture(t, true)
	if _, err := f.srv.SyncWasabiConnection(asUser("u1"), connect.NewRequest(&neoboxv1.SyncWasabiConnectionRequest{ConnectionId: "w1"})); err != nil {
		t.Fatalf("SyncWasabiConnection: %v", err)
	}
	if len(f.syncer.refreshed) != 1 || f.syncer.refreshed[0] != "w1" {
		t.Fatalf("refreshed = %v", f.syncer.refreshed)
	}

	for what, ctx := range map[string]context.Context{"another user": asUser("u2"), "a NocoDB connection": asUser("u1")} {
		id := "w1"
		if what == "a NocoDB connection" {
			id = "n1"
		}
		_, err := f.srv.GetWasabiOverview(ctx, connect.NewRequest(&neoboxv1.GetWasabiOverviewRequest{ConnectionId: id}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("%s: err = %v, want NotFound", what, err)
		}
	}
}

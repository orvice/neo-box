package wasabi

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveStatsAPI calls the real Stats API. It is skipped unless
// NEOBOX_TEST_WASABI_ACCESS_KEY and NEOBOX_TEST_WASABI_SECRET_KEY are set
// (a read-only sub-user is enough); NEOBOX_TEST_WASABI_ENDPOINT overrides
// the host.
func TestLiveStatsAPI(t *testing.T) {
	ak, sk := os.Getenv("NEOBOX_TEST_WASABI_ACCESS_KEY"), os.Getenv("NEOBOX_TEST_WASABI_SECRET_KEY")
	if ak == "" || sk == "" {
		t.Skip("NEOBOX_TEST_WASABI_ACCESS_KEY / NEOBOX_TEST_WASABI_SECRET_KEY are not set")
	}
	c, err := New(ak, sk, WithEndpoint(os.Getenv("NEOBOX_TEST_WASABI_ENDPOINT")))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	to := DayOf(time.Now())
	from := to.AddDate(0, 0, -7)

	account, err := c.AccountUsage(ctx, from, to)
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	t.Logf("account: %d daily records", len(account))
	for _, u := range account {
		if u.Bucket != "" {
			t.Errorf("account record has bucket %q", u.Bucket)
		}
		t.Logf("  %s active=%d deleted=%d objects=%d download=%d",
			u.Day.Format(DateLayout), u.ActiveStorageBytes(), u.DeletedStorageSizeBytes, u.NumBillableObjects, u.DownloadBytes)
	}

	buckets, err := c.BucketUsage(ctx, from, to)
	if err != nil {
		t.Fatalf("BucketUsage: %v", err)
	}
	t.Logf("buckets: %d daily records", len(buckets))
	for _, u := range buckets {
		if u.Bucket == "" || u.Region == "" {
			t.Errorf("bucket record without bucket or region: %+v", u)
		}
	}
}

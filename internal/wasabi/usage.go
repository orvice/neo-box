package wasabi

import "time"

// Usage is one day of Stats API utilization for a Wasabi account (Bucket
// empty) or one of its buckets. Storage fields are a snapshot at midnight
// UTC; activity fields (API calls, bytes moved) cover that day only.
// Field meanings: https://docs.wasabi.com/apidocs/billing-and-utilization-metrics
type Usage struct {
	// Day is the UTC day the record covers, at midnight UTC.
	Day    time.Time
	Bucket string
	Region string

	NumBillableObjects        int64
	NumBillableDeletedObjects int64
	RawStorageSizeBytes       int64
	PaddedStorageSizeBytes    int64
	MetadataStorageSizeBytes  int64
	// DeletedStorageSizeBytes is deleted data still billed under the
	// minimum storage duration (typically 90 days).
	DeletedStorageSizeBytes  int64
	OrphanedStorageSizeBytes int64
	// MinStorageChargeBytes is what an account under the 1 TB minimum is
	// charged for on top of its data. Account records only.
	MinStorageChargeBytes int64

	NumAPICalls       int64
	UploadBytes       int64
	DownloadBytes     int64
	StorageWroteBytes int64
	StorageReadBytes  int64
	DeleteBytes       int64
	NumGETCalls       int64
	NumPUTCalls       int64
	NumDELETECalls    int64
	NumLISTCalls      int64
	NumHEADCalls      int64
}

// ActiveStorageBytes is the billable active storage: padded object bytes
// plus metadata.
func (u Usage) ActiveStorageBytes() int64 {
	return u.PaddedStorageSizeBytes + u.MetadataStorageSizeBytes
}

// DayOf returns t's UTC day at midnight.
func DayOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

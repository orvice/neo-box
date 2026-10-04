package wasabi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type s3Reply struct {
	status int
	body   string
}

// fakeS3 answers path-style requests from a table keyed "bucket?sub".
type fakeS3 struct {
	mu      sync.Mutex
	replies map[string]s3Reply
	auth    map[string]string // key -> Authorization header seen
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket := strings.Trim(r.URL.Path, "/")
	// The subresource is the first query key the SDK didn't add itself.
	sub := ""
	for _, kv := range strings.Split(r.URL.RawQuery, "&") {
		if k := strings.SplitN(kv, "=", 2)[0]; k != "" && k != "x-id" {
			sub = k
			break
		}
	}
	key := bucket + "?" + sub
	f.mu.Lock()
	f.auth[key] = r.Header.Get("Authorization")
	reply, ok := f.replies[key]
	f.mu.Unlock()
	if !ok {
		reply = s3Reply{501, `<Error><Code>NotImplemented</Code><Message>not here</Message></Error>`}
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(reply.status)
	_, _ = w.Write([]byte(reply.body))
}

func s3Err(status int, code string) s3Reply {
	return s3Reply{status, "<Error><Code>" + code + "</Code><Message>m</Message></Error>"}
}

func ok(body string) s3Reply { return s3Reply{200, body} }

func TestConfigReader(t *testing.T) {
	f := &fakeS3{auth: map[string]string{}, replies: map[string]s3Reply{
		"?": ok(`<ListAllMyBucketsResult><Owner><ID>o1</ID></Owner><Buckets>
			<Bucket><Name>media</Name><CreationDate>2025-01-02T03:04:05.000Z</CreationDate></Bucket>
			<Bucket><Name>bare</Name><CreationDate>2025-02-01T00:00:00.000Z</CreationDate></Bucket>
		</Buckets></ListAllMyBucketsResult>`),

		"media?location":   ok(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">eu-central-1</LocationConstraint>`),
		"media?versioning": ok(`<VersioningConfiguration><Status>Enabled</Status><MfaDelete>Enabled</MfaDelete></VersioningConfiguration>`),
		"media?object-lock": ok(`<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled>
			<Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>30</Days></DefaultRetention></Rule></ObjectLockConfiguration>`),
		"media?compliance": ok(`<BucketComplianceConfiguration><Status>disabled</Status></BucketComplianceConfiguration>`),
		"media?lifecycle": ok(`<LifecycleConfiguration><Rule><ID>r1</ID><Filter><Prefix>tmp/</Prefix></Filter><Status>Enabled</Status>
			<Expiration><Days>30</Days></Expiration><NoncurrentVersionExpiration><NoncurrentDays>7</NoncurrentDays></NoncurrentVersionExpiration>
			<AbortIncompleteMultipartUpload><DaysAfterInitiation>3</DaysAfterInitiation></AbortIncompleteMultipartUpload></Rule></LifecycleConfiguration>`),
		"media?policy": ok(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::media/*"}]}`),
		"media?acl": ok(`<AccessControlPolicy><Owner><ID>o1</ID><DisplayName>me</DisplayName></Owner><AccessControlList>
			<Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>o1</ID><DisplayName>me</DisplayName></Grantee><Permission>FULL_CONTROL</Permission></Grant>
			<Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group"><URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee><Permission>READ</Permission></Grant>
			</AccessControlList></AccessControlPolicy>`),
		"media?logging": ok(`<BucketLoggingStatus><LoggingEnabled><TargetBucket>logs</TargetBucket><TargetPrefix>media/</TargetPrefix></LoggingEnabled></BucketLoggingStatus>`),
		"media?replication": ok(`<ReplicationConfiguration><Role></Role><Rule><ID>rep</ID><Status>Enabled</Status>
			<Destination><Bucket>arn:aws:s3:::media-copy</Bucket></Destination></Rule></ReplicationConfiguration>`),
		"media?tagging": ok(`<Tagging><TagSet><Tag><Key>team</Key><Value>ops</Value></Tag></TagSet></Tagging>`),

		// "bare" has nothing set, can't read its ACL, and the server
		// doesn't support logging.
		"bare?location":    ok(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`),
		"bare?versioning":  ok(`<VersioningConfiguration></VersioningConfiguration>`),
		"bare?object-lock": s3Err(404, "ObjectLockConfigurationNotFoundError"),
		"bare?compliance": ok(`<BucketComplianceConfiguration><Status>enabled</Status><LockTime>2026-01-01T00:00:00Z</LockTime>
			<RetentionDays>365</RetentionDays><ConditionalHold>true</ConditionalHold><DeleteAfterRetention>true</DeleteAfterRetention></BucketComplianceConfiguration>`),
		"bare?lifecycle":   s3Err(404, "NoSuchLifecycleConfiguration"),
		"bare?policy":      s3Err(404, "NoSuchBucketPolicy"),
		"bare?acl":         s3Err(403, "AccessDenied"),
		"bare?replication": s3Err(404, "NoSuchReplicationConfiguration"),
		"bare?tagging":     s3Err(404, "NoSuchTagSet"),
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	r := NewConfigReader("AKID", "SECRET", WithS3Endpoint(srv.URL))
	r.now = func() time.Time { return now }
	ctx := context.Background()

	buckets, err := r.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || buckets[0].Name != "media" || !buckets[0].CreatedAt.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("buckets = %+v", buckets)
	}

	media := r.Read(ctx, buckets[0])
	if media.Region != "eu-central-1" || !media.FetchedAt.Equal(now) || len(media.Errors) != 0 {
		t.Fatalf("media = %+v", media)
	}
	if v := media.Versioning; v == nil || v.Status != "Enabled" || !v.MFADelete {
		t.Fatalf("versioning = %+v", v)
	}
	if ol := media.ObjectLock; ol == nil || ol.Mode != "COMPLIANCE" || ol.Days != 30 {
		t.Fatalf("object lock = %+v", ol)
	}
	if media.Compliance != nil {
		t.Fatalf("disabled compliance = %+v", media.Compliance)
	}
	if lc := media.Lifecycle; len(lc) != 1 || lc[0] != (LifecycleRule{ID: "r1", Enabled: true, Prefix: "tmp/", ExpirationDays: 30, NoncurrentDays: 7, AbortMultipartDays: 3}) {
		t.Fatalf("lifecycle = %+v", lc)
	}
	if p := media.Policy; p == nil || !p.Public {
		t.Fatalf("policy = %+v", p)
	}
	if a := media.ACL; a == nil || !a.Public || a.Owner != "me" || len(a.Grants) != 2 || a.Grants[1].Grantee != allUsersURI || a.Grants[1].Permission != "READ" {
		t.Fatalf("acl = %+v", a)
	}
	if l := media.Logging; l == nil || l.TargetBucket != "logs" || l.TargetPrefix != "media/" {
		t.Fatalf("logging = %+v", l)
	}
	if rep := media.Replication; len(rep) != 1 || rep[0].DestinationBucket != "media-copy" || !rep[0].Enabled {
		t.Fatalf("replication = %+v", rep)
	}
	if media.Tags["team"] != "ops" {
		t.Fatalf("tags = %+v", media.Tags)
	}
	// Bucket calls are signed for the bucket's region.
	if a := f.auth["media?versioning"]; !strings.Contains(a, "/eu-central-1/s3/aws4_request") {
		t.Fatalf("versioning signed as %q", a)
	}
	if a := f.auth["media?compliance"]; !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKID/") || !strings.Contains(a, "/eu-central-1/s3/") {
		t.Fatalf("compliance signed as %q", a)
	}

	bare := r.Read(ctx, buckets[1])
	if bare.Region != DefaultRegion || bare.Versioning != nil || bare.ObjectLock != nil || bare.Lifecycle != nil ||
		bare.Policy != nil || bare.ACL != nil || bare.Replication != nil || bare.Tags != nil {
		t.Fatalf("bare = %+v", bare)
	}
	if c := bare.Compliance; c == nil || c.RetentionDays != 365 || !c.ConditionalHold || !c.DeleteAfterRetention || !c.Locked {
		t.Fatalf("compliance = %+v", c)
	}
	if bare.Errors[SettingACL] != ErrAccessDenied || bare.Errors[SettingLogging] != ErrNotSupported || len(bare.Errors) != 2 {
		t.Fatalf("errors = %+v", bare.Errors)
	}
}

func TestBucketFindings(t *testing.T) {
	codes := func(c BucketConfig) string {
		var out []string
		for _, f := range BucketFindings(c) {
			out = append(out, f.Code)
		}
		return strings.Join(out, ",")
	}
	for name, tc := range map[string]struct {
		cfg  BucketConfig
		want string
	}{
		"nothing set": {BucketConfig{}, ""},
		"public both": {BucketConfig{Policy: &BucketPolicy{Public: true}, ACL: &BucketACL{Public: true}}, "public_policy,public_acl"},
		"versioning without cleanup": {BucketConfig{Versioning: &Versioning{Status: "Enabled"},
			Lifecycle: []LifecycleRule{{Enabled: true, ExpirationDays: 30}}}, "old_versions_kept"},
		"versioning with cleanup": {BucketConfig{Versioning: &Versioning{Status: "Enabled"},
			Lifecycle: []LifecycleRule{{Enabled: true, NoncurrentDays: 7}}}, ""},
		"cleanup rule disabled": {BucketConfig{Versioning: &Versioning{Status: "Enabled"},
			Lifecycle: []LifecycleRule{{Enabled: false, NoncurrentDays: 7}}}, "old_versions_kept"},
		"lifecycle unreadable": {BucketConfig{Versioning: &Versioning{Status: "Enabled"},
			Errors: map[string]string{SettingLifecycle: ErrAccessDenied}}, "settings_unreadable"},
		"suspended":        {BucketConfig{Versioning: &Versioning{Status: "Suspended"}}, "versioning_suspended"},
		"unsupported only": {BucketConfig{Errors: map[string]string{SettingLogging: ErrNotSupported}}, ""},
	} {
		if got := codes(tc.cfg); got != tc.want {
			t.Errorf("%s: findings = %q, want %q", name, got, tc.want)
		}
	}
}

func TestPolicyIsPublic(t *testing.T) {
	for doc, want := range map[string]bool{
		`{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject"}]}`:                   true,
		`{"Statement":[{"Effect":"Allow","Principal":{"AWS":["arn:x","*"]},"Action":"s3:GetObject"}]}`: true,
		`{"Statement":{"Effect":"Allow","Principal":{"AWS":"*"}}}`:                                     true,
		`{"Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*"}]}`:                            false,
		`{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::1:user/a"}}]}`:               false,
		`not json`: false,
	} {
		if got := policyIsPublic(doc); got != want {
			t.Errorf("policyIsPublic(%s) = %v, want %v", doc, got, want)
		}
	}
}

package wasabi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// DefaultRegion is where Wasabi's global endpoint (s3.wasabisys.com) lives.
const DefaultRegion = "us-east-1"

// BucketInfo is a bucket as ListBuckets reports it.
type BucketInfo struct {
	Name      string
	CreatedAt time.Time
}

// ConfigReader reads bucket settings through Wasabi's S3 API with the same
// access key as the Stats API. Everything it does is a read.
type ConfigReader struct {
	creds aws.CredentialsProvider
	http  *http.Client
	// endpoint returns the S3 endpoint of a region.
	endpoint func(region string) string
	now      func() time.Time

	mu      sync.Mutex
	clients map[string]*awss3.Client
}

// ConfigReaderOption customizes a ConfigReader.
type ConfigReaderOption func(*ConfigReader)

// WithS3Endpoint sends every request to one endpoint (tests).
func WithS3Endpoint(url string) ConfigReaderOption {
	return func(r *ConfigReader) { r.endpoint = func(string) string { return url } }
}

// NewConfigReader builds a reader for one account's key.
func NewConfigReader(accessKey, secretKey string, opts ...ConfigReaderOption) *ConfigReader {
	r := &ConfigReader{
		creds:    credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		http:     &http.Client{Timeout: 30 * time.Second},
		endpoint: RegionEndpoint,
		now:      time.Now,
		clients:  map[string]*awss3.Client{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RegionEndpoint is Wasabi's S3 service URL for a region; us-east-1 is the
// global s3.wasabisys.com.
func RegionEndpoint(region string) string {
	if region == "" || region == DefaultRegion {
		return "https://s3.wasabisys.com"
	}
	return "https://s3." + region + ".wasabisys.com"
}

func (r *ConfigReader) client(region string) *awss3.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[region]; ok {
		return c
	}
	c := awss3.New(awss3.Options{
		Region:       region,
		BaseEndpoint: aws.String(r.endpoint(region)),
		Credentials:  r.creds,
		HTTPClient:   r.http,
		UsePathStyle: true,
	})
	r.clients[region] = c
	return c
}

// ListBuckets lists the account's buckets, from the global endpoint.
func (r *ConfigReader) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	var out []BucketInfo
	var token *string
	for {
		resp, err := r.client(DefaultRegion).ListBuckets(ctx, &awss3.ListBucketsInput{ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, b := range resp.Buckets {
			out = append(out, BucketInfo{Name: aws.ToString(b.Name), CreatedAt: aws.ToTime(b.CreationDate)})
		}
		if resp.ContinuationToken == nil || *resp.ContinuationToken == "" {
			return out, nil
		}
		token = resp.ContinuationToken
	}
}

// Read reads every setting of a bucket. It never fails as a whole: a
// setting it can't read is recorded in Errors.
func (r *ConfigReader) Read(ctx context.Context, b BucketInfo) BucketConfig {
	c := BucketConfig{Bucket: b.Name, CreatedAt: b.CreatedAt, FetchedAt: r.now().UTC(), Region: DefaultRegion}
	fail := func(setting string, err error) {
		if c.Errors == nil {
			c.Errors = map[string]string{}
		}
		c.Errors[setting] = ErrorReason(err)
	}

	// Bucket calls go to the bucket's region; GETs still work elsewhere,
	// so a failed lookup keeps the global endpoint.
	if loc, err := r.client(DefaultRegion).GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: &b.Name}); err != nil {
		fail(SettingLocation, err)
	} else if region := string(loc.LocationConstraint); region != "" {
		c.Region = region
	}
	s3c := r.client(c.Region)
	bucket := aws.String(b.Name)

	if v, err := s3c.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: bucket}); err != nil {
		fail(SettingVersioning, err)
	} else if v.Status != "" || v.MFADelete != "" {
		c.Versioning = &Versioning{Status: string(v.Status), MFADelete: v.MFADelete == s3types.MFADeleteStatusEnabled}
	}

	if ol, err := s3c.GetObjectLockConfiguration(ctx, &awss3.GetObjectLockConfigurationInput{Bucket: bucket}); err != nil {
		if !notConfigured(err) {
			fail(SettingObjectLock, err)
		}
	} else if cfg := ol.ObjectLockConfiguration; cfg != nil && cfg.ObjectLockEnabled == s3types.ObjectLockEnabledEnabled {
		c.ObjectLock = &ObjectLock{Enabled: true}
		if cfg.Rule != nil && cfg.Rule.DefaultRetention != nil {
			d := cfg.Rule.DefaultRetention
			c.ObjectLock.Mode = string(d.Mode)
			c.ObjectLock.Days = int(aws.ToInt32(d.Days))
			c.ObjectLock.Years = int(aws.ToInt32(d.Years))
		}
	}

	if comp, err := r.compliance(ctx, c.Region, b.Name); err != nil {
		if !notConfigured(err) {
			fail(SettingCompliance, err)
		}
	} else {
		c.Compliance = comp
	}

	if lc, err := s3c.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{Bucket: bucket}); err != nil {
		if !notConfigured(err) {
			fail(SettingLifecycle, err)
		}
	} else {
		for _, rule := range lc.Rules {
			c.Lifecycle = append(c.Lifecycle, lifecycleRule(rule))
		}
	}

	if p, err := s3c.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: bucket}); err != nil {
		if !notConfigured(err) {
			fail(SettingPolicy, err)
		}
	} else if doc := aws.ToString(p.Policy); doc != "" {
		c.Policy = &BucketPolicy{Document: doc, Public: policyIsPublic(doc)}
	}

	if acl, err := s3c.GetBucketAcl(ctx, &awss3.GetBucketAclInput{Bucket: bucket}); err != nil {
		fail(SettingACL, err)
	} else {
		c.ACL = bucketACL(acl)
	}

	if lg, err := s3c.GetBucketLogging(ctx, &awss3.GetBucketLoggingInput{Bucket: bucket}); err != nil {
		fail(SettingLogging, err)
	} else if le := lg.LoggingEnabled; le != nil && aws.ToString(le.TargetBucket) != "" {
		c.Logging = &BucketLogging{TargetBucket: aws.ToString(le.TargetBucket), TargetPrefix: aws.ToString(le.TargetPrefix)}
	}

	if rep, err := s3c.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{Bucket: bucket}); err != nil {
		if !notConfigured(err) {
			fail(SettingReplication, err)
		}
	} else if rep.ReplicationConfiguration != nil {
		for _, rule := range rep.ReplicationConfiguration.Rules {
			c.Replication = append(c.Replication, replicationRule(rule))
		}
	}

	if tags, err := s3c.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: bucket}); err != nil {
		if !notConfigured(err) {
			fail(SettingTags, err)
		}
	} else if len(tags.TagSet) > 0 {
		c.Tags = map[string]string{}
		for _, t := range tags.TagSet {
			c.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return c
}

func lifecycleRule(r s3types.LifecycleRule) LifecycleRule {
	out := LifecycleRule{ID: aws.ToString(r.ID), Enabled: r.Status == s3types.ExpirationStatusEnabled}
	if r.Filter != nil && r.Filter.Prefix != nil {
		out.Prefix = aws.ToString(r.Filter.Prefix)
	} else if r.Prefix != nil { //nolint:staticcheck // Wasabi may still use the legacy field
		out.Prefix = aws.ToString(r.Prefix) //nolint:staticcheck
	}
	if e := r.Expiration; e != nil {
		out.ExpirationDays = int(aws.ToInt32(e.Days))
		out.ExpiredObjectDeleteMarker = aws.ToBool(e.ExpiredObjectDeleteMarker)
	}
	if n := r.NoncurrentVersionExpiration; n != nil {
		out.NoncurrentDays = int(aws.ToInt32(n.NoncurrentDays))
	}
	if a := r.AbortIncompleteMultipartUpload; a != nil {
		out.AbortMultipartDays = int(aws.ToInt32(a.DaysAfterInitiation))
	}
	return out
}

// Group URIs that make an ACL grant public.
const (
	allUsersURI           = "http://acs.amazonaws.com/groups/global/AllUsers"
	authenticatedUsersURI = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
)

func bucketACL(acl *awss3.GetBucketAclOutput) *BucketACL {
	out := &BucketACL{}
	if acl.Owner != nil {
		out.Owner = firstNonEmpty(aws.ToString(acl.Owner.DisplayName), aws.ToString(acl.Owner.ID))
	}
	for _, g := range acl.Grants {
		if g.Grantee == nil {
			continue
		}
		uri := aws.ToString(g.Grantee.URI)
		if uri == allUsersURI || uri == authenticatedUsersURI {
			out.Public = true
		}
		out.Grants = append(out.Grants, Grant{
			Grantee:    firstNonEmpty(uri, aws.ToString(g.Grantee.DisplayName), aws.ToString(g.Grantee.ID), aws.ToString(g.Grantee.EmailAddress)),
			Permission: string(g.Permission),
		})
	}
	return out
}

func replicationRule(r s3types.ReplicationRule) ReplicationRule {
	out := ReplicationRule{ID: aws.ToString(r.ID), Enabled: r.Status == s3types.ReplicationRuleStatusEnabled}
	if r.Filter != nil && r.Filter.Prefix != nil {
		out.Prefix = aws.ToString(r.Filter.Prefix)
	} else if r.Prefix != nil { //nolint:staticcheck // legacy field
		out.Prefix = aws.ToString(r.Prefix) //nolint:staticcheck
	}
	if r.Destination != nil {
		out.DestinationBucket = strings.TrimPrefix(aws.ToString(r.Destination.Bucket), "arn:aws:s3:::")
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// compliance reads Wasabi's bucket compliance setting. The S3 SDK doesn't
// know ?compliance, so it is a raw SigV4-signed GET.
func (r *ConfigReader) compliance(ctx context.Context, region, bucket string) (*Compliance, error) {
	url := strings.TrimRight(r.endpoint(region), "/") + "/" + bucket + "?compliance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	creds, err := r.creds.Retrieve(ctx)
	if err != nil {
		return nil, err
	}
	empty := sha256.Sum256(nil)
	payload := hex.EncodeToString(empty[:])
	req.Header.Set("X-Amz-Content-Sha256", payload)
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, payload, "s3", region, r.now()); err != nil {
		return nil, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		_ = xml.Unmarshal(body, &e)
		if e.Code == "" {
			e.Code = http.StatusText(resp.StatusCode)
		}
		return nil, &rawAPIError{status: resp.StatusCode, code: e.Code, message: e.Message}
	}
	var out struct {
		Status               string `xml:"Status"`
		RetentionDays        int    `xml:"RetentionDays"`
		ConditionalHold      bool   `xml:"ConditionalHold"`
		DeleteAfterRetention bool   `xml:"DeleteAfterRetention"`
		IsLocked             bool   `xml:"IsLocked"`
		LockTime             string `xml:"LockTime"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode compliance: %w", err)
	}
	if !strings.EqualFold(out.Status, "enabled") {
		return nil, nil
	}
	c := &Compliance{
		Enabled: true, RetentionDays: out.RetentionDays, ConditionalHold: out.ConditionalHold,
		DeleteAfterRetention: out.DeleteAfterRetention, Locked: out.IsLocked,
	}
	if t, err := time.Parse(time.RFC3339, out.LockTime); err == nil {
		c.LockTime = t
		c.Locked = c.Locked || !t.After(r.now())
	}
	return c, nil
}

// rawAPIError is an S3-style error from a request made without the SDK.
type rawAPIError struct {
	status        int
	code, message string
}

func (e *rawAPIError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.status, e.code, e.message)
}
func (e *rawAPIError) ErrorCode() string             { return e.code }
func (e *rawAPIError) ErrorMessage() string          { return e.message }
func (e *rawAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

// notConfiguredCodes are the errors S3 (and Wasabi) return for a setting
// that is simply not set.
var notConfiguredCodes = map[string]bool{
	"NoSuchLifecycleConfiguration":          true,
	"NoSuchBucketPolicy":                    true,
	"NoSuchReplicationConfiguration":        true,
	"ReplicationConfigurationNotFoundError": true,
	"NoSuchTagSet":                          true,
	"NoSuchTagSetError":                     true,
	"ObjectLockConfigurationNotFoundError":  true,
	"NoSuchObjectLockConfiguration":         true,
	"NoSuchComplianceConfiguration":         true,
}

func notConfigured(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && notConfiguredCodes[ae.ErrorCode()]
}

// ErrorReason turns a read error into what BucketConfig.Errors records.
func ErrorReason(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "AccessDenied", "Forbidden":
			return ErrAccessDenied
		case "NotImplemented", "MethodNotAllowed":
			return ErrNotSupported
		}
		if msg := ae.ErrorMessage(); msg != "" {
			return ae.ErrorCode() + ": " + msg
		}
		return ae.ErrorCode()
	}
	return err.Error()
}

package wasabi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BucketConfig is one bucket's settings as read through the S3 API. A nil
// setting is off (or has no rules); a setting that could not be read has an
// entry in Errors instead.
type BucketConfig struct {
	Bucket    string    `json:"bucket"`
	Region    string    `json:"region"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	FetchedAt time.Time `json:"fetched_at,omitzero"`

	Versioning  *Versioning       `json:"versioning,omitempty"`
	ObjectLock  *ObjectLock       `json:"object_lock,omitempty"`
	Compliance  *Compliance       `json:"compliance,omitempty"`
	Lifecycle   []LifecycleRule   `json:"lifecycle,omitempty"`
	Policy      *BucketPolicy     `json:"policy,omitempty"`
	ACL         *BucketACL        `json:"acl,omitempty"`
	Logging     *BucketLogging    `json:"logging,omitempty"`
	Replication []ReplicationRule `json:"replication,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`

	// Errors maps a setting (one of the Setting constants) to why it could
	// not be read: ErrAccessDenied, ErrNotSupported, or a message.
	Errors map[string]string `json:"errors,omitempty"`
}

// Settings a BucketConfig reads, as keys of BucketConfig.Errors.
const (
	SettingVersioning  = "versioning"
	SettingObjectLock  = "object_lock"
	SettingCompliance  = "compliance"
	SettingLifecycle   = "lifecycle"
	SettingPolicy      = "policy"
	SettingACL         = "acl"
	SettingLogging     = "logging"
	SettingReplication = "replication"
	SettingTags        = "tags"
	SettingLocation    = "location"
)

// Reasons a setting could not be read.
const (
	ErrAccessDenied = "access_denied"
	ErrNotSupported = "not_supported"
)

// Versioning is a bucket's versioning state. Status is "Enabled",
// "Suspended", or empty for a bucket that never had versioning.
type Versioning struct {
	Status    string `json:"status"`
	MFADelete bool   `json:"mfa_delete"`
}

// ObjectLock is a bucket's Object Lock setting and default retention.
type ObjectLock struct {
	Enabled bool `json:"enabled"`
	// Mode is "GOVERNANCE" or "COMPLIANCE" when a default retention is
	// set.
	Mode  string `json:"mode,omitempty"`
	Days  int    `json:"days,omitempty"`
	Years int    `json:"years,omitempty"`
}

// Compliance is Wasabi's own bucket retention setting (an alternative to
// Object Lock; a bucket has one or the other).
type Compliance struct {
	Enabled              bool      `json:"enabled"`
	RetentionDays        int       `json:"retention_days"`
	ConditionalHold      bool      `json:"conditional_hold"`
	DeleteAfterRetention bool      `json:"delete_after_retention"`
	Locked               bool      `json:"locked"`
	LockTime             time.Time `json:"lock_time,omitzero"`
}

// MinStorageDurationDays is Wasabi Pay-Go's minimum storage retention window;
// objects deleted earlier remain billed as deleted storage until this age.
const MinStorageDurationDays = 90

// LifecycleRule is one lifecycle rule. Zero day counts mean the action is
// not set.
type LifecycleRule struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix,omitempty"`
	// ObjectSizeGreaterThan and ObjectSizeLessThan bound matching object
	// sizes in bytes; nil means unbounded.
	ObjectSizeGreaterThan *int64 `json:"object_size_greater_than,omitempty"`
	ObjectSizeLessThan    *int64 `json:"object_size_less_than,omitempty"`
	// Tags holds any object-tag filter returned by the S3 API.
	Tags map[string]string `json:"tags,omitempty"`
	// ExpirationDays deletes current versions this many days after
	// creation.
	ExpirationDays int `json:"expiration_days,omitempty"`
	// ExpirationDate deletes current versions on this UTC YYYY-MM-DD date.
	ExpirationDate            string `json:"expiration_date,omitempty"`
	ExpiredObjectDeleteMarker bool   `json:"expired_object_delete_marker,omitempty"`
	// NoncurrentDays deletes old versions this many days after they stop
	// being current.
	NoncurrentDays int `json:"noncurrent_days,omitempty"`
	// NewerNoncurrentVersions keeps at least this many newer noncurrent
	// versions before older ones expire.
	NewerNoncurrentVersions int `json:"newer_noncurrent_versions,omitempty"`
	// AbortMultipartDays aborts incomplete multipart uploads.
	AbortMultipartDays int `json:"abort_multipart_days,omitempty"`
}

// AppliesToAllObjects reports whether the rule has no prefix, size, or tag filter.
func (r LifecycleRule) AppliesToAllObjects() bool {
	return r.Prefix == "" && r.ObjectSizeGreaterThan == nil && r.ObjectSizeLessThan == nil && len(r.Tags) == 0
}

// BucketPolicy is a bucket's policy document.
type BucketPolicy struct {
	Document string `json:"document"`
	// Public reports a statement allowing anyone ("Principal": "*").
	Public bool `json:"public"`
}

// BucketACL is a bucket's (deprecated) access control list.
type BucketACL struct {
	Owner  string  `json:"owner,omitempty"`
	Grants []Grant `json:"grants,omitempty"`
	// Public reports a grant to all users or all authenticated users.
	Public bool `json:"public"`
}

// Grant is one ACL entry. Grantee is a user's display name or ID, or a
// group URI.
type Grant struct {
	Grantee    string `json:"grantee"`
	Permission string `json:"permission"`
}

// BucketLogging is where a bucket's access logs go.
type BucketLogging struct {
	TargetBucket string `json:"target_bucket"`
	TargetPrefix string `json:"target_prefix,omitempty"`
}

// ReplicationRule is one replication rule.
type ReplicationRule struct {
	ID                string `json:"id,omitempty"`
	Enabled           bool   `json:"enabled"`
	Prefix            string `json:"prefix,omitempty"`
	DestinationBucket string `json:"destination_bucket"`
}

// Public reports whether the policy or ACL lets anyone in. Wasabi's
// console "Public Access Override" can't be read through the API, so false
// doesn't rule it out.
func (c BucketConfig) Public() bool {
	return (c.Policy != nil && c.Policy.Public) || (c.ACL != nil && c.ACL.Public)
}

// Severity of a Finding.
const (
	FindingInfo     = "info"
	FindingWarning  = "warning"
	FindingCritical = "critical"
)

// Finding flags a setting worth a look.
type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// BucketFindings flags risky or surprising settings, most severe first.
func BucketFindings(c BucketConfig) []Finding {
	var out []Finding
	// Critical
	if c.Policy != nil && c.Policy.Public {
		out = append(out, Finding{"public_policy", FindingCritical,
			"The bucket policy lets anyone (Principal \"*\") in, for at least some objects or actions."})
	}
	if c.ACL != nil && c.ACL.Public {
		out = append(out, Finding{"public_acl", FindingCritical,
			"The bucket ACL grants access to all users or to all authenticated users."})
	}

	// Warning
	lifecycleReadable := c.Errors[SettingLifecycle] == ""
	versioningEnabled := c.Versioning != nil && c.Versioning.Status == "Enabled"
	noncurrentCov := noncurrentCoverage(c.Lifecycle)
	if versioningEnabled && lifecycleReadable && noncurrentCov == noncurrentNone {
		out = append(out, Finding{"old_versions_kept", FindingWarning,
			"Versioning is on but no lifecycle rule expires old versions, so every overwritten or deleted object stays billed as active storage."})
	}
	if lifecycleReadable {
		if count, minDays := earlyDeletionRules(c.Lifecycle); count > 0 {
			out = append(out, Finding{"early_deletion_lifecycle", FindingWarning,
				fmt.Sprintf("%s %s objects or old versions after as little as %s; Wasabi Pay-Go bills deleted data for the full %d-day minimum storage duration.",
					pluralRules(count), verbDeletes(count), pluralDays(minDays), MinStorageDurationDays)})
		}
	}

	// Info
	if versioningEnabled && lifecycleReadable && noncurrentCov == noncurrentPartial {
		out = append(out, Finding{"old_versions_partially_expired", FindingInfo,
			"Versioning is on, but old-version expiration rules only match filtered objects (by prefix, size, or tag); other objects keep old versions billed as active storage."})
	}
	if lifecycleReadable {
		if f, ok := retentionBlockedFinding(c); ok {
			out = append(out, f)
		}
	}
	if v := c.Versioning; v != nil && v.Status == "Suspended" {
		out = append(out, Finding{"versioning_suspended", FindingInfo,
			"Versioning is suspended: versions written before stay (and are billed) until deleted."})
	}
	// Settings Wasabi doesn't support aren't the key's fault.
	var names []string
	for k, why := range c.Errors {
		if why != ErrNotSupported {
			names = append(names, strings.ReplaceAll(k, "_", " "))
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		out = append(out, Finding{"settings_unreadable", FindingInfo,
			fmt.Sprintf("Could not read: %s.", strings.Join(names, ", "))})
	}
	return out
}

type noncurrentExpiryScope int

const (
	noncurrentNone noncurrentExpiryScope = iota
	noncurrentPartial
	noncurrentAll
)

func noncurrentCoverage(rules []LifecycleRule) noncurrentExpiryScope {
	scope := noncurrentNone
	for _, r := range rules {
		if !r.Enabled || r.NoncurrentDays <= 0 {
			continue
		}
		if r.AppliesToAllObjects() {
			return noncurrentAll
		}
		scope = noncurrentPartial
	}
	return scope
}

func earlyDeletionRules(rules []LifecycleRule) (count, minDays int) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		shortest := 0
		if r.ExpirationDays > 0 && r.ExpirationDays < MinStorageDurationDays {
			shortest = r.ExpirationDays
		}
		if r.NoncurrentDays > 0 && r.NoncurrentDays < MinStorageDurationDays {
			if shortest == 0 || r.NoncurrentDays < shortest {
				shortest = r.NoncurrentDays
			}
		}
		if shortest > 0 {
			count++
			if minDays == 0 || shortest < minDays {
				minDays = shortest
			}
		}
	}
	return count, minDays
}

func retentionBlockedFinding(c BucketConfig) (Finding, bool) {
	if ol := c.ObjectLock; ol != nil && ol.Enabled {
		retDays := ol.Days
		retLabel := pluralDays(ol.Days)
		if retDays <= 0 && ol.Years > 0 {
			retDays = ol.Years * 365
			retLabel = pluralYears(ol.Years)
		}
		if retDays <= 0 {
			return Finding{}, false
		}
		// Object Lock requires versioning; current-version expiration adds a
		// delete marker (allowed), while noncurrent expiration permanently
		// deletes versions and is blocked until retention expires.
		count, minDays := 0, 0
		for _, r := range c.Lifecycle {
			if r.Enabled && r.NoncurrentDays > 0 && r.NoncurrentDays < retDays {
				count++
				if minDays == 0 || r.NoncurrentDays < minDays {
					minDays = r.NoncurrentDays
				}
			}
		}
		if count > 0 {
			return Finding{"lifecycle_blocked_by_retention", FindingInfo,
				fmt.Sprintf("Object Lock default retention (%s) is longer than %s expiring old versions (shortest %s); locked versions cannot be permanently deleted until retention expires.",
					retLabel, pluralRules(count), pluralDays(minDays))}, true
		}
		return Finding{}, false
	}

	if cp := c.Compliance; cp != nil && cp.Enabled && cp.RetentionDays > 0 {
		count, minDays := 0, 0
		for _, r := range c.Lifecycle {
			if !r.Enabled {
				continue
			}
			shortest := 0
			if r.ExpirationDays > 0 && r.ExpirationDays < cp.RetentionDays {
				shortest = r.ExpirationDays
			}
			if r.NoncurrentDays > 0 && r.NoncurrentDays < cp.RetentionDays {
				if shortest == 0 || r.NoncurrentDays < shortest {
					shortest = r.NoncurrentDays
				}
			}
			if shortest > 0 {
				count++
				if minDays == 0 || shortest < minDays {
					minDays = shortest
				}
			}
		}
		if count > 0 {
			return Finding{"lifecycle_blocked_by_retention", FindingInfo,
				fmt.Sprintf("Wasabi compliance retention (%s) is longer than %s (shortest %s); protected objects cannot be deleted until retention expires.",
					pluralDays(cp.RetentionDays), pluralRules(count), pluralDays(minDays))}, true
		}
	}
	return Finding{}, false
}

func pluralRules(n int) string {
	if n == 1 {
		return "1 enabled lifecycle rule"
	}
	return fmt.Sprintf("%d enabled lifecycle rules", n)
}

func verbDeletes(n int) string {
	if n == 1 {
		return "deletes"
	}
	return "delete"
}

func pluralDays(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

func pluralYears(n int) string {
	if n == 1 {
		return "1 year"
	}
	return fmt.Sprintf("%d years", n)
}

// policyIsPublic reports whether a policy document has an Allow statement
// for everyone. It errs towards false for documents it can't parse.
func policyIsPublic(doc string) bool {
	var p struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if json.Unmarshal([]byte(doc), &p) != nil {
		return false
	}
	var statements []struct {
		Effect    string          `json:"Effect"`
		Principal json.RawMessage `json:"Principal"`
	}
	if json.Unmarshal(p.Statement, &statements) != nil {
		// A single statement may be an object rather than a list.
		var one struct {
			Effect    string          `json:"Effect"`
			Principal json.RawMessage `json:"Principal"`
		}
		if json.Unmarshal(p.Statement, &one) != nil {
			return false
		}
		statements = append(statements, one)
	}
	for _, s := range statements {
		if strings.EqualFold(s.Effect, "Allow") && principalIsEveryone(s.Principal) {
			return true
		}
	}
	return false
}

func principalIsEveryone(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == "*"
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	for _, v := range m {
		var one string
		if json.Unmarshal(v, &one) == nil && one == "*" {
			return true
		}
		var many []string
		if json.Unmarshal(v, &many) == nil {
			for _, x := range many {
				if x == "*" {
					return true
				}
			}
		}
	}
	return false
}

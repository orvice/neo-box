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

// LifecycleRule is one lifecycle rule. Zero day counts mean the action is
// not set.
type LifecycleRule struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix,omitempty"`
	// ExpirationDays deletes current versions this many days after
	// creation.
	ExpirationDays            int  `json:"expiration_days,omitempty"`
	ExpiredObjectDeleteMarker bool `json:"expired_object_delete_marker,omitempty"`
	// NoncurrentDays deletes old versions this many days after they stop
	// being current.
	NoncurrentDays int `json:"noncurrent_days,omitempty"`
	// AbortMultipartDays aborts incomplete multipart uploads.
	AbortMultipartDays int `json:"abort_multipart_days,omitempty"`
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
	if c.Policy != nil && c.Policy.Public {
		out = append(out, Finding{"public_policy", FindingCritical,
			"The bucket policy lets anyone (Principal \"*\") in, for at least some objects or actions."})
	}
	if c.ACL != nil && c.ACL.Public {
		out = append(out, Finding{"public_acl", FindingCritical,
			"The bucket ACL grants access to all users or to all authenticated users."})
	}
	if v := c.Versioning; v != nil && v.Status == "Enabled" && c.Errors[SettingLifecycle] == "" && !expiresOldVersions(c.Lifecycle) {
		out = append(out, Finding{"old_versions_kept", FindingWarning,
			"Versioning is on but no lifecycle rule expires old versions, so every overwritten or deleted object stays billed as active storage."})
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

func expiresOldVersions(rules []LifecycleRule) bool {
	for _, r := range rules {
		if r.Enabled && r.NoncurrentDays > 0 {
			return true
		}
	}
	return false
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

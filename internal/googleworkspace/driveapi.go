package googleworkspace

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/oauth2"
)

// The only two Drive API scopes Neo Box requests, and never together in one
// token. See docs/adr/0009.
//
// DriveReadonlyScope is required by drives.list (no read-only metadata scope
// is accepted there) and can read file content; Neo Box calls only
// drives.list with it. DriveMetadataScope cannot read content and is used for
// files.list. Neither is ever used to download or export a file.
const (
	DriveReadonlyScope = "https://www.googleapis.com/auth/drive.readonly"
	DriveMetadataScope = "https://www.googleapis.com/auth/drive.metadata.readonly"
)

const driveURL = "https://www.googleapis.com/drive/v3"

// Action names recorded in the operation log.
const (
	ActionDriveSharedDrivesList = "drive.shared_drives.list"
	ActionDriveExposedFilesList = "drive.files.exposed.list"
)

const (
	maxDrivePageSize   = 100
	defaultDrivePage   = 50
	maxSharedDriveName = 100
	maxMemberFilter    = 100000
)

var orgUnitIDPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,100}$`)

// DriveClient reads Drive metadata. The token's scope fixes what it may do:
// a DriveReadonlyScope client lists shared drives as the tenant's
// administrator, a DriveMetadataScope client lists one user's files as that
// user. Every call is recorded before it is sent; without a Recorder nothing
// is sent.
type DriveClient struct {
	transport *Client
	endpoint  string
	// Subject is the primary email the token's subject is set to.
	Subject string
	Scope   string
	// Domains are the tenant's verified domains, used to tell internal from
	// external domains. Empty means unknown, never "all external".
	Domains  []string
	Recorder WriteRecorder
	// Reason is the caller's access reason, stored with each operation.
	Reason string
}

// NewDriveClient exchanges a token whose subject is the given user and whose
// only scope is the given Drive scope. The caller must already have resolved
// the subject inside the tenant.
func NewDriveClient(ctx context.Context, identity ConnectionConfig, secret ConnectionSecret, customer, subject, scope string) (*DriveClient, error) {
	if scope != DriveReadonlyScope && scope != DriveMetadataScope {
		return nil, errors.New("unsupported Drive scope")
	}
	metadata, cfg, err := ParseKey(secret.ServiceAccountJSON)
	if err != nil {
		return nil, err
	}
	if *metadata != identity {
		return nil, errors.New("Service Account identity mismatch")
	}
	if !ValidCustomerID(customer) || !ValidEmail(subject) {
		return nil, errors.New("invalid Workspace customer or Drive subject")
	}
	cfg.Scopes = []string{scope}
	cfg.Subject = subject
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	hc := oauth2.NewClient(ctx, safeTokenSource{cfg.TokenSource(ctx)})
	hc.Timeout = 30 * time.Second
	return NewDriveClientForHTTP(hc, driveURL, customer, subject, scope), nil
}

// NewDriveClientForHTTP builds a client over an existing HTTP client and
// endpoint, for tests and alternative token sources.
func NewDriveClientForHTTP(hc *http.Client, endpoint, customer, subject, scope string) *DriveClient {
	return &DriveClient{transport: &Client{HTTP: hc, Endpoint: endpoint, CustomerID: customer, AdminEmail: subject}, endpoint: endpoint, Subject: subject, Scope: scope}
}

func (d *DriveClient) SetWriteRecorder(r WriteRecorder) { d.Recorder = r }

func (d *DriveClient) record(ctx context.Context, w Write, run func() (map[string]any, error)) error {
	return recordAccess(ctx, d.Recorder, d.Subject, d.Reason, w, run)
}

// ValidAccessReason checks the single-line, 1-200 character reason every
// privileged Drive read carries, and returns it trimmed.
func ValidAccessReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	switch {
	case reason == "":
		return "", errors.New("an access reason is required")
	case utf8.RuneCountInString(reason) > MaxAccessReason:
		return "", errors.New("must be at most 200 characters")
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", errors.New("must be a single line of text")
		}
	}
	return reason, nil
}

// ---- Shared drives ----

type SharedDriveRestrictions struct {
	DomainUsersOnly, DriveMembersOnly, AdminManaged bool
	CopyRequiresWriterPermission                    bool
	SharingFoldersRequiresOrganizerPermission       bool
}

type SharedDrive struct {
	ID, Name     string
	CreatedTime  time.Time
	OrgUnitID    string
	Restrictions SharedDriveRestrictions
}

type SharedDriveQuery struct {
	NamePrefix  string
	NoOrganizer bool
	// MaxMembers limits to drives with at most this many members. Nil is no limit.
	MaxMembers *int
	OrgUnitID  string
	PageToken  string
	PageSize   int
}

type SharedDrivePage struct {
	Drives        []SharedDrive
	NextPageToken string
	ReadAt        time.Time
}

// Validate normalizes the query and rejects values that could not be safely
// placed in a Drive search string.
func (q *SharedDriveQuery) Validate() error {
	q.NamePrefix = strings.TrimSpace(q.NamePrefix)
	q.OrgUnitID = strings.TrimSpace(q.OrgUnitID)
	if utf8.RuneCountInString(q.NamePrefix) > maxSharedDriveName {
		return invalid("name_prefix", "must be at most 100 characters")
	}
	for _, r := range q.NamePrefix {
		if unicode.IsControl(r) {
			return invalid("name_prefix", "must be a single line of text")
		}
	}
	if q.OrgUnitID != "" && !orgUnitIDPattern.MatchString(q.OrgUnitID) {
		return invalid("org_unit_id", "is not a valid organizational unit ID")
	}
	if q.MaxMembers != nil && (*q.MaxMembers < 0 || *q.MaxMembers > maxMemberFilter) {
		return invalid("max_members", "must be between 0 and 100000")
	}
	if q.PageSize == 0 {
		q.PageSize = defaultDrivePage
	}
	if q.PageSize < 1 || q.PageSize > maxDrivePageSize {
		return invalid("page_size", "must be between 1 and 100")
	}
	return nil
}

func driveQuoted(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// Search is the Drive search string for the query, or empty for no filter.
func (q SharedDriveQuery) Search() string {
	var terms []string
	if q.NamePrefix != "" {
		terms = append(terms, "name contains "+driveQuoted(q.NamePrefix))
	}
	if q.NoOrganizer {
		terms = append(terms, "organizerCount = 0")
	}
	if q.MaxMembers != nil {
		terms = append(terms, "memberCount <= "+strconv.Itoa(*q.MaxMembers))
	}
	if q.OrgUnitID != "" {
		terms = append(terms, "orgUnitId = "+driveQuoted(q.OrgUnitID))
	}
	return strings.Join(terms, " and ")
}

// ListSharedDrives reads one page of the domain's shared drives as the tenant's
// administrator. It is one logged operation; nothing but drives.list is called.
func (d *DriveClient) ListSharedDrives(ctx context.Context, q SharedDriveQuery) (*SharedDrivePage, error) {
	if d.Scope != DriveReadonlyScope {
		return nil, errors.New("shared drives are listed with the Drive read-only scope")
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	requested := map[string]any{"name_prefix": q.NamePrefix != "", "no_organizer": q.NoOrganizer, "org_unit_id": q.OrgUnitID, "next_page": q.PageToken != ""}
	if q.MaxMembers != nil {
		requested["max_members"] = *q.MaxMembers
	}
	var out *SharedDrivePage
	err := d.record(ctx, Write{Method: http.MethodGet, Path: "/drive/shared_drives", Action: ActionDriveSharedDrivesList, Requested: requested}, func() (map[string]any, error) {
		query := url.Values{}
		query.Set("useDomainAdminAccess", "true")
		query.Set("pageSize", strconv.Itoa(q.PageSize))
		query.Set("fields", "nextPageToken,drives(id,name,createdTime,orgUnitId,restrictions(domainUsersOnly,driveMembersOnly,adminManagedRestrictions,copyRequiresWriterPermission,sharingFoldersRequiresOrganizerPermission))")
		if search := q.Search(); search != "" {
			query.Set("q", search)
		}
		if q.PageToken != "" {
			query.Set("pageToken", q.PageToken)
		}
		var raw struct {
			NextPageToken string `json:"nextPageToken"`
			Drives        []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				CreatedTime  string `json:"createdTime"`
				OrgUnitID    string `json:"orgUnitId"`
				Restrictions struct {
					DomainUsersOnly                           bool `json:"domainUsersOnly"`
					DriveMembersOnly                          bool `json:"driveMembersOnly"`
					AdminManagedRestrictions                  bool `json:"adminManagedRestrictions"`
					CopyRequiresWriterPermission              bool `json:"copyRequiresWriterPermission"`
					SharingFoldersRequiresOrganizerPermission bool `json:"sharingFoldersRequiresOrganizerPermission"`
				} `json:"restrictions"`
			} `json:"drives"`
		}
		if err := d.transport.doAt(ctx, d.endpoint, http.MethodGet, "/drives", query, nil, &raw); err != nil {
			return nil, err
		}
		page := &SharedDrivePage{NextPageToken: raw.NextPageToken, ReadAt: time.Now().UTC()}
		for _, r := range raw.Drives {
			t, _ := time.Parse(time.RFC3339, r.CreatedTime)
			page.Drives = append(page.Drives, SharedDrive{ID: r.ID, Name: r.Name, CreatedTime: t, OrgUnitID: r.OrgUnitID, Restrictions: SharedDriveRestrictions{DomainUsersOnly: r.Restrictions.DomainUsersOnly, DriveMembersOnly: r.Restrictions.DriveMembersOnly, AdminManaged: r.Restrictions.AdminManagedRestrictions, CopyRequiresWriterPermission: r.Restrictions.CopyRequiresWriterPermission, SharingFoldersRequiresOrganizerPermission: r.Restrictions.SharingFoldersRequiresOrganizerPermission}})
		}
		out = page
		return map[string]any{"returned": len(page.Drives), "has_more": page.NextPageToken != ""}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- One user's exposed files ----

// Exposure kinds, in the order they are shown.
var exposureVisibility = []struct{ Kind, Visibility string }{
	{"anyone_can_find", "anyoneCanFind"},
	{"anyone_with_link", "anyoneWithLink"},
	{"domain_can_find", "domainCanFind"},
	{"domain_with_link", "domainWithLink"},
}

type FileExposure struct {
	Kind, Role, Domain string
	// External is nil when it can't be told (domains unknown, or not a domain
	// exposure).
	External *bool
}

type ExposedFile struct {
	ID, Name, MimeType string
	ModifiedTime       time.Time
	WebViewLink        string
	Exposures          []FileExposure
}

type ExposedFilesQuery struct {
	// Exposures limits to these kinds. Empty is all four.
	Exposures []string
	PageToken string
	PageSize  int
}

type ExposedFilesPage struct {
	Files            []ExposedFile
	NextPageToken    string
	IncompleteSearch bool
	ReadAt           time.Time
}

// Validate normalizes the query: unknown kinds are refused, duplicates
// dropped and the order made canonical.
func (q *ExposedFilesQuery) Validate() error {
	want := map[string]bool{}
	for _, e := range q.Exposures {
		e = strings.TrimSpace(e)
		ok := false
		for _, k := range exposureVisibility {
			if k.Kind == e {
				ok = true
			}
		}
		if !ok {
			return invalid("exposures", "must be anyone_can_find, anyone_with_link, domain_can_find or domain_with_link")
		}
		want[e] = true
	}
	q.Exposures = nil
	for _, k := range exposureVisibility {
		if len(want) == 0 || want[k.Kind] {
			q.Exposures = append(q.Exposures, k.Kind)
		}
	}
	if q.PageSize == 0 {
		q.PageSize = defaultDrivePage
	}
	if q.PageSize < 1 || q.PageSize > maxDrivePageSize {
		return invalid("page_size", "must be between 1 and 100")
	}
	return nil
}

// Search is the Drive search string: the user's own, untrashed files with one
// of the requested visibilities.
func (q ExposedFilesQuery) Search() string {
	var vis []string
	for _, kind := range q.Exposures {
		for _, k := range exposureVisibility {
			if k.Kind == kind {
				vis = append(vis, "visibility = '"+k.Visibility+"'")
			}
		}
	}
	return "'me' in owners and trashed = false and (" + strings.Join(vis, " or ") + ")"
}

func domainIsExternal(domain string, domains []string) *bool {
	if len(domains) == 0 || domain == "" {
		return nil
	}
	for _, d := range domains {
		if strings.EqualFold(d, domain) {
			f := false
			return &f
		}
	}
	t := true
	return &t
}

// ListExposedFiles reads one page of the subject's own files that are visible
// beyond named people. It is one logged operation. Names and links are shown
// to the caller but never stored in the log.
func (d *DriveClient) ListExposedFiles(ctx context.Context, q ExposedFilesQuery) (*ExposedFilesPage, error) {
	if d.Scope != DriveMetadataScope {
		return nil, errors.New("files are listed with the Drive metadata scope")
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	requested := map[string]any{"exposures": q.Exposures, "next_page": q.PageToken != ""}
	var out *ExposedFilesPage
	err := d.record(ctx, Write{Method: http.MethodGet, Path: "/drive/files/exposed", Action: ActionDriveExposedFilesList, Requested: requested}, func() (map[string]any, error) {
		query := url.Values{}
		query.Set("q", q.Search())
		query.Set("pageSize", strconv.Itoa(q.PageSize))
		query.Set("orderBy", "modifiedTime desc")
		query.Set("fields", "nextPageToken,incompleteSearch,files(id,name,mimeType,modifiedTime,webViewLink,permissions(type,role,domain,allowFileDiscovery))")
		if q.PageToken != "" {
			query.Set("pageToken", q.PageToken)
		}
		var raw struct {
			NextPageToken    string `json:"nextPageToken"`
			IncompleteSearch bool   `json:"incompleteSearch"`
			Files            []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				MimeType     string `json:"mimeType"`
				ModifiedTime string `json:"modifiedTime"`
				WebViewLink  string `json:"webViewLink"`
				Permissions  []struct {
					Type               string `json:"type"`
					Role               string `json:"role"`
					Domain             string `json:"domain"`
					AllowFileDiscovery bool   `json:"allowFileDiscovery"`
				} `json:"permissions"`
			} `json:"files"`
		}
		if err := d.transport.doAt(ctx, d.endpoint, http.MethodGet, "/files", query, nil, &raw); err != nil {
			return nil, err
		}
		page := &ExposedFilesPage{NextPageToken: raw.NextPageToken, IncompleteSearch: raw.IncompleteSearch, ReadAt: time.Now().UTC()}
		anyone, domain, external, noInfo := 0, 0, 0, 0
		for _, r := range raw.Files {
			t, _ := time.Parse(time.RFC3339, r.ModifiedTime)
			f := ExposedFile{ID: r.ID, Name: r.Name, MimeType: r.MimeType, ModifiedTime: t}
			// Only an https address is passed on; the caller opens it in a browser.
			if strings.HasPrefix(r.WebViewLink, "https://") {
				f.WebViewLink = r.WebViewLink
			}
			hasAnyone, hasDomain, hasExternal := false, false, false
			for _, p := range r.Permissions {
				switch p.Type {
				case "anyone":
					kind := "anyone_with_link"
					if p.AllowFileDiscovery {
						kind = "anyone_can_find"
					}
					f.Exposures = append(f.Exposures, FileExposure{Kind: kind, Role: p.Role})
					hasAnyone = true
				case "domain":
					kind := "domain_with_link"
					if p.AllowFileDiscovery {
						kind = "domain_can_find"
					}
					e := domainIsExternal(p.Domain, d.Domains)
					f.Exposures = append(f.Exposures, FileExposure{Kind: kind, Role: p.Role, Domain: p.Domain, External: e})
					hasDomain = true
					if e != nil && *e {
						hasExternal = true
					}
				}
			}
			if len(f.Exposures) == 0 {
				noInfo++
			}
			if hasAnyone {
				anyone++
			}
			if hasDomain {
				domain++
			}
			if hasExternal {
				external++
			}
			page.Files = append(page.Files, f)
		}
		out = page
		return map[string]any{"returned": len(page.Files), "with_anyone": anyone, "with_domain": domain, "with_external_domain": external, "without_permission_info": noInfo, "has_more": page.NextPageToken != "", "incomplete_search": page.IncompleteSearch}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

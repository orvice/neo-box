package cloudflare

import "time"

// Zone is a Cloudflare Zone (a domain) as the API returns it.
type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Paused      bool     `json:"paused"`
	Type        string   `json:"type"`
	NameServers []string `json:"name_servers"`
	Account     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
	// Permissions is Cloudflare's legacy list of what the caller may do in
	// the Zone, e.g. "#dns_records:edit". It may be missing or empty.
	Permissions []string `json:"permissions"`
}

// DNSAccess is what the token may do with a Zone's DNS records.
type DNSAccess string

const (
	// DNSAccessUnknown means it is not known whether the token may change
	// records.
	DNSAccessUnknown DNSAccess = "unknown"
	// DNSAccessRead means records can be read but not changed.
	DNSAccessRead DNSAccess = "read"
)

// DNSAccess reads the Zone's legacy permissions. They describe the token
// owner's membership, not the token, so they can rule edits out (read
// without edit) but never confirm them: anything else is unknown.
func (z Zone) DNSAccess() DNSAccess {
	var read bool
	for _, p := range z.Permissions {
		switch p {
		case "#dns_records:edit":
			return DNSAccessUnknown
		case "#dns_records:read":
			read = true
		}
	}
	if read {
		return DNSAccessRead
	}
	return DNSAccessUnknown
}

// DNSRecord is a DNS record as the API returns it. It is also what the
// operation log keeps as a record's state before and after a change.
type DNSRecord struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	TTL       int    `json:"ttl"`
	Proxied   bool   `json:"proxied"`
	Proxiable bool   `json:"proxiable"`
	// Priority is an MX record's preference.
	Priority *int `json:"priority,omitempty"`
	// Data is the structured content of SRV and CAA records.
	Data       *RecordData `json:"data,omitempty"`
	Comment    string      `json:"comment,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	CreatedOn  time.Time   `json:"created_on"`
	ModifiedOn time.Time   `json:"modified_on"`
}

// RecordData is the part of a record's structured data Neo Box edits: SRV
// (priority, weight, port, target) and CAA (flags, tag, value).
type RecordData struct {
	Priority *int   `json:"priority,omitempty"`
	Weight   *int   `json:"weight,omitempty"`
	Port     *int   `json:"port,omitempty"`
	Target   string `json:"target,omitempty"`
	Flags    *int   `json:"flags,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Value    string `json:"value,omitempty"`
}

// PageInfo is Cloudflare's result_info for paged lists.
type PageInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

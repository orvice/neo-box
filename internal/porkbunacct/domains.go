package porkbunacct

import (
	"context"
	"sort"
	"strings"
	"time"

	"go.orx.me/apps/neo-box/internal/porkbun"
)

const expiringSoonWindow = 30 * 24 * time.Hour

// DomainFilter narrows the domain list.
type DomainFilter int

const (
	FilterAll DomainFilter = iota
	FilterExpiringSoon
	FilterAutoRenewOff
	FilterAPIAccessOff
	FilterExternalNameservers
)

// PageInfo describes one slice of a paginated list.
type PageInfo struct {
	Page       int
	PageSize   int
	TotalPages int
	TotalCount int
}

// PortfolioSummary summarizes all domains in a Porkbun account before search
// or filter is applied.
type PortfolioSummary struct {
	TotalDomains             int
	ExpiringSoonCount        int
	ExpiredCount             int
	AutoRenewOffCount        int
	SecurityLockOffCount     int
	APIAccessOffCount        int
	ExternalNameserversCount int
}

// DomainDetail combines a domain's metadata with its authoritative nameservers.
type DomainDetail struct {
	Domain                porkbun.Domain
	Nameservers           []string
	UsesPorkbunNS         bool
	NameserversIssue      string
}

// Domains reads the account's domains live, computes portfolio-wide summary
// counts, and returns the requested search/filter page.
func (a *Account) Domains(ctx context.Context, query string, filter DomainFilter, page, pageSize int) ([]porkbun.Domain, PageInfo, PortfolioSummary, error) {
	all, err := a.api.ListDomains(ctx)
	if err != nil {
		return nil, PageInfo{}, PortfolioSummary{}, a.classify(ctx, err, "", "list domains")
	}

	now := a.m.now().UTC()
	cutoff := now.Add(expiringSoonWindow)

	var summary PortfolioSummary
	summary.TotalDomains = len(all)

	q := strings.ToLower(strings.TrimSpace(query))
	matched := make([]porkbun.Domain, 0, len(all))
	for _, d := range all {
		expired := !d.ExpiresAt.IsZero() && d.ExpiresAt.Before(now)
		expiringSoon := !d.ExpiresAt.IsZero() && !d.ExpiresAt.After(cutoff)
		if expired {
			summary.ExpiredCount++
		}
		if expiringSoon {
			summary.ExpiringSoonCount++
		}
		if !d.AutoRenew {
			summary.AutoRenewOffCount++
		}
		if !d.SecurityLock {
			summary.SecurityLockOffCount++
		}
		if !d.APIAccess {
			summary.APIAccessOffCount++
		}
		if d.NotLocal {
			summary.ExternalNameserversCount++
		}

		if !matchesDomainFilter(d, filter, expiringSoon) {
			continue
		}
		if q != "" && !matchesDomainQuery(d, q) {
			continue
		}
		matched = append(matched, d)
	}

	if filter == FilterExpiringSoon {
		sort.Slice(matched, func(i, j int) bool {
			if !matched[i].ExpiresAt.Equal(matched[j].ExpiresAt) {
				if matched[i].ExpiresAt.IsZero() {
					return false
				}
				if matched[j].ExpiresAt.IsZero() {
					return true
				}
				return matched[i].ExpiresAt.Before(matched[j].ExpiresAt)
			}
			return matched[i].Domain < matched[j].Domain
		})
	} else {
		sort.Slice(matched, func(i, j int) bool {
			return matched[i].Domain < matched[j].Domain
		})
	}

	items, info := paginate(matched, page, pageSize)
	return items, info, summary, nil
}

// Domain reads one domain's metadata (from the account's domain list) plus its
// nameservers. If nameservers cannot be read because API access is disabled or
// scoped out, the error is surfaced in NameserversIssue instead of failing.
func (a *Account) Domain(ctx context.Context, rawDomain string) (*DomainDetail, error) {
	name, err := porkbun.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	all, err := a.api.ListDomains(ctx)
	if err != nil {
		return nil, a.classify(ctx, err, name, "read domain")
	}
	var found *porkbun.Domain
	for i := range all {
		if strings.EqualFold(all[i].Domain, name) {
			found = &all[i]
			break
		}
	}
	if found == nil {
		return nil, ErrDomainNotFound
	}

	detail := &DomainDetail{Domain: *found}
	ns, err := a.api.GetNameservers(ctx, name)
	if err != nil {
		classified := a.classify(ctx, err, name, "read nameservers")
		var rejected *CredentialsRejectedError
		if isCredentialsRejected(classified, &rejected) {
			return nil, rejected
		}
		detail.NameserversIssue = classified.Error()
		return detail, nil
	}
	detail.Nameservers = ns
	detail.UsesPorkbunNS = porkbun.UsesPorkbunNameservers(ns)
	return detail, nil
}

// DNSRecords reads a domain's DNS records live, returns sorted distinct record
// types present in the zone, and applies search/type filtering and pagination.
func (a *Account) DNSRecords(ctx context.Context, rawDomain, query, recordType string, page, pageSize int) ([]porkbun.DNSRecord, PageInfo, []string, error) {
	name, err := porkbun.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, PageInfo{}, nil, err
	}
	all, err := a.api.ListDNSRecords(ctx, name)
	if err != nil {
		return nil, PageInfo{}, nil, a.classify(ctx, err, name, "list DNS records")
	}

	typeSet := make(map[string]struct{})
	q := strings.ToLower(strings.TrimSpace(query))
	typ := strings.ToUpper(strings.TrimSpace(recordType))
	matched := make([]porkbun.DNSRecord, 0, len(all))
	for _, r := range all {
		if r.Type != "" {
			typeSet[r.Type] = struct{}{}
		}
		if typ != "" && r.Type != typ {
			continue
		}
		if q != "" && !matchesRecordQuery(r, q) {
			continue
		}
		matched = append(matched, r)
	}

	recordTypes := make([]string, 0, len(typeSet))
	for t := range typeSet {
		recordTypes = append(recordTypes, t)
	}
	sort.Strings(recordTypes)

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Name != matched[j].Name {
			return matched[i].Name < matched[j].Name
		}
		if matched[i].Type != matched[j].Type {
			return matched[i].Type < matched[j].Type
		}
		if matched[i].Content != matched[j].Content {
			return matched[i].Content < matched[j].Content
		}
		return matched[i].ID < matched[j].ID
	})

	items, info := paginate(matched, page, pageSize)
	return items, info, recordTypes, nil
}

// URLForwards reads a domain's URL forwarding rules live.
func (a *Account) URLForwards(ctx context.Context, rawDomain string) ([]porkbun.URLForward, error) {
	name, err := porkbun.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	fwds, err := a.api.ListURLForwards(ctx, name)
	if err != nil {
		return nil, a.classify(ctx, err, name, "list URL forwards")
	}
	return fwds, nil
}

// DNSSECRecords reads a domain's DNSSEC DS records live.
func (a *Account) DNSSECRecords(ctx context.Context, rawDomain string) ([]porkbun.DNSSECRecord, error) {
	name, err := porkbun.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	recs, err := a.api.ListDNSSECRecords(ctx, name)
	if err != nil {
		return nil, a.classify(ctx, err, name, "list DNSSEC records")
	}
	return recs, nil
}

// GlueRecords reads a domain's custom nameserver glue records live.
func (a *Account) GlueRecords(ctx context.Context, rawDomain string) ([]porkbun.GlueHost, error) {
	name, err := porkbun.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	hosts, err := a.api.ListGlueRecords(ctx, name)
	if err != nil {
		return nil, a.classify(ctx, err, name, "list glue records")
	}
	return hosts, nil
}

func matchesDomainFilter(d porkbun.Domain, filter DomainFilter, expiringSoon bool) bool {
	switch filter {
	case FilterExpiringSoon:
		return expiringSoon
	case FilterAutoRenewOff:
		return !d.AutoRenew
	case FilterAPIAccessOff:
		return !d.APIAccess
	case FilterExternalNameservers:
		return d.NotLocal
	default:
		return true
	}
}

func matchesDomainQuery(d porkbun.Domain, q string) bool {
	if strings.Contains(strings.ToLower(d.Domain), q) || strings.Contains(strings.ToLower(d.TLD), q) {
		return true
	}
	for _, l := range d.Labels {
		if strings.Contains(strings.ToLower(l.Title), q) {
			return true
		}
	}
	return false
}

func matchesRecordQuery(r porkbun.DNSRecord, q string) bool {
	return strings.Contains(strings.ToLower(r.Name), q) ||
		strings.Contains(strings.ToLower(r.Content), q) ||
		strings.Contains(strings.ToLower(r.Notes), q) ||
		strings.Contains(strings.ToLower(r.Type), q)
}

func paginate[T any](all []T, page, pageSize int) ([]T, PageInfo) {
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	total := len(all)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	start := (page - 1) * pageSize
	if start >= total {
		return nil, PageInfo{Page: page, PageSize: pageSize, TotalPages: totalPages, TotalCount: total}
	}
	end := min(start+pageSize, total)
	return all[start:end], PageInfo{Page: page, PageSize: pageSize, TotalPages: totalPages, TotalCount: total}
}

func isCredentialsRejected(err error, target **CredentialsRejectedError) bool {
	if err == nil {
		return false
	}
	if r, ok := err.(*CredentialsRejectedError); ok {
		*target = r
		return true
	}
	return false
}

package cloudflareacct

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.orx.me/apps/neo-box/internal/cloudflare"
)

const (
	// routeZoneLimit caps the Zones whose Worker routes are read for one
	// page of Workers; Cloudflare has no account-wide route list.
	routeZoneLimit = 50
	// fanOut caps concurrent per-resource calls.
	fanOut = 8
)

// AddressKind is where an address comes from.
type AddressKind string

const (
	KindWorkersDev   AddressKind = "workers_dev"
	KindCustomDomain AddressKind = "custom_domain"
	// KindRoute is a Worker route pattern as configured; it may hold
	// wildcards, so it is not always a URL.
	KindRoute      AddressKind = "route"
	KindPagesDev   AddressKind = "pages_dev"
	KindProduction AddressKind = "production"
)

// Address is one way to reach a Worker or Pages project, as Cloudflare
// reports it; Neo Box never makes one up from a name.
type Address struct {
	URL  string
	Kind AddressKind
	// Status is a Pages custom domain's status, when read.
	Status string
}

// AddressIssue is an address source that could not be read.
type AddressIssue struct {
	Source           string
	Message          string
	PermissionDenied bool
}

// Worker is one Worker with the addresses that could be read.
type Worker struct {
	Name       string
	Addresses  []Address
	Incomplete bool
}

// PagesProject is one Pages project with the addresses that could be read.
type PagesProject struct {
	Name       string
	Addresses  []Address
	Incomplete bool
}

// StatusUnknown is the status of a Pages custom domain whose status could
// not be read.
const StatusUnknown = "unknown"

// Page is one page of Workers or Pages projects. Total counts every match
// of the query, not only this page.
type Page[T any] struct {
	Items  []T
	Total  int
	Issues []AddressIssue
}

// Workers returns one page of the Account's Workers whose name contains
// query, with their workers.dev URL (when enabled), custom domains and
// routes.
func (a *Account) Workers(ctx context.Context, query string, page, perPage int) (*Page[*Worker], error) {
	scripts, err := a.api.ListWorkerScripts(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "list Workers", "Workers Scripts Read")
	}
	scripts = matching(scripts, query, func(s cloudflare.WorkerScript) string { return s.ID })
	out := &Page[*Worker]{Total: len(scripts)}
	scripts = pageOf(scripts, page, perPage)
	if len(scripts) == 0 {
		return out, nil
	}

	var (
		wg     sync.WaitGroup
		issues issueSet
		onDev  = make([]bool, len(scripts))
		devErr = make([]bool, len(scripts))
		sub    string
		subErr error
		doms   []cloudflare.WorkerDomain
		domErr error
		routes []cloudflare.WorkerRoute
		// routesIncomplete: some Zones' routes could not be read.
		routesIncomplete bool
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		if sub, subErr = a.api.WorkersSubdomain(ctx, a.ID); subErr != nil || sub == "" {
			return
		}
		each(len(scripts), func(i int) {
			enabled, err := a.api.ScriptOnWorkersDev(ctx, a.ID, scripts[i].ID)
			if err != nil {
				devErr[i] = true
				issues.add("workers.dev status", err)
			}
			onDev[i] = enabled
		})
	}()
	go func() {
		defer wg.Done()
		doms, domErr = a.api.ListWorkerDomains(ctx, a.ID)
	}()
	go func() {
		defer wg.Done()
		routes, routesIncomplete = a.zoneRoutes(ctx, &issues)
	}()
	wg.Wait()
	if subErr != nil {
		issues.add("workers.dev subdomain", subErr)
	}
	if domErr != nil {
		issues.add("Worker custom domains", domErr)
	}

	for i, s := range scripts {
		w := &Worker{Name: s.ID, Incomplete: subErr != nil || domErr != nil || routesIncomplete || devErr[i]}
		var set addressSet
		if onDev[i] {
			set.add(Address{URL: fmt.Sprintf("https://%s.%s.workers.dev", s.ID, sub), Kind: KindWorkersDev})
		}
		var hosts []string
		for _, d := range doms {
			if d.Service == s.ID && (d.Enabled == nil || *d.Enabled) {
				hosts = append(hosts, d.Hostname)
			}
		}
		sort.Strings(hosts)
		for _, h := range hosts {
			set.add(Address{URL: httpsURL(h), Kind: KindCustomDomain})
		}
		for _, r := range s.Routes {
			set.add(Address{URL: r.Pattern, Kind: KindRoute})
		}
		for _, r := range routes {
			if r.Script == s.ID {
				set.add(Address{URL: r.Pattern, Kind: KindRoute})
			}
		}
		w.Addresses = set.list
		out.Items = append(out.Items, w)
	}
	out.Issues = issues.list
	return out, nil
}

// zoneRoutes reads the Worker routes of the Account's Zones, in Zone name
// order. incomplete is set when some could not be read.
func (a *Account) zoneRoutes(ctx context.Context, issues *issueSet) (routes []cloudflare.WorkerRoute, incomplete bool) {
	const source = "Worker routes"
	zones, info, err := a.api.ListZones(ctx, cloudflare.ZoneQuery{AccountID: a.ID, PerPage: routeZoneLimit})
	if err != nil {
		issues.add(source, err)
		return nil, true
	}
	if info != nil && info.TotalCount > len(zones) {
		incomplete = true
		issues.addMessage(source, fmt.Sprintf("only the routes of the first %d Zones were read", len(zones)), false)
	}
	perZone := make([][]cloudflare.WorkerRoute, len(zones))
	errs := make([]error, len(zones))
	each(len(zones), func(i int) {
		if zones[i].Account.ID != a.ID {
			return
		}
		perZone[i], errs[i] = a.api.ListWorkerRoutes(ctx, zones[i].ID)
	})
	failed, denied := 0, 0
	var last error
	for i, rs := range perZone {
		if errs[i] != nil {
			failed++
			last = errs[i]
			if cloudflare.IsPermissionDenied(errs[i]) {
				denied++
			}
			continue
		}
		routes = append(routes, rs...)
	}
	switch {
	case failed == 0:
	case denied == failed:
		incomplete = true
		issues.addMessage(source, fmt.Sprintf("the API token may not read the routes of %d of %d Zones; grant it \"Workers Routes Read\"", failed, len(zones)), true)
	default:
		incomplete = true
		issues.addMessage(source, fmt.Sprintf("the routes of %d of %d Zones could not be read: %v", failed, len(zones), last), false)
	}
	return routes, incomplete
}

// PagesProjects returns one page of the Account's Pages projects whose
// name contains query, with their pages.dev subdomain, custom domains and
// production deployment URL.
func (a *Account) PagesProjects(ctx context.Context, query string, page, perPage int) (*Page[*PagesProject], error) {
	projects, err := a.api.ListPagesProjects(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "list Pages projects", "Cloudflare Pages Read")
	}
	projects = matching(projects, query, func(p cloudflare.PagesProject) string { return p.Name })
	out := &Page[*PagesProject]{Total: len(projects)}
	projects = pageOf(projects, page, perPage)

	var issues issueSet
	domains := make([][]cloudflare.PagesDomain, len(projects))
	domErrs := make([]error, len(projects))
	each(len(projects), func(i int) {
		domains[i], domErrs[i] = a.api.ListPagesDomains(ctx, a.ID, projects[i].Name)
	})
	for i, p := range projects {
		item := &PagesProject{Name: p.Name}
		var set addressSet
		if p.Subdomain != "" {
			set.add(Address{URL: httpsURL(p.Subdomain), Kind: KindPagesDev})
		}
		if domErrs[i] != nil {
			// The project still names its domains, but not whether they
			// answer yet.
			item.Incomplete = true
			issues.add("Pages custom domains", domErrs[i])
			for _, d := range p.Domains {
				if !strings.HasSuffix(d, ".pages.dev") {
					set.add(Address{URL: httpsURL(d), Kind: KindCustomDomain, Status: StatusUnknown})
				}
			}
		}
		for _, d := range domains[i] {
			if !strings.HasSuffix(d.Name, ".pages.dev") {
				set.add(Address{URL: httpsURL(d.Name), Kind: KindCustomDomain, Status: d.Status})
			}
		}
		if p.CanonicalDeployment != nil && p.CanonicalDeployment.URL != "" {
			set.add(Address{URL: p.CanonicalDeployment.URL, Kind: KindProduction})
		}
		item.Addresses = set.list
		out.Items = append(out.Items, item)
	}
	out.Issues = issues.list
	return out, nil
}

// --- helpers ---

// matching keeps the items whose name contains query (case-insensitive),
// sorted by name.
func matching[T any](items []T, query string, name func(T) string) []T {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []T
	for _, it := range items {
		if q == "" || strings.Contains(strings.ToLower(name(it)), q) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return name(out[i]) < name(out[j]) })
	return out
}

// pageOf returns the 1-based page of items.
func pageOf[T any](items []T, page, perPage int) []T {
	start := (page - 1) * perPage
	if start >= len(items) {
		return nil
	}
	return items[start:min(len(items), start+perPage)]
}

// each runs fn for 0..n-1, at most fanOut at a time.
func each(n int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, fanOut)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

func httpsURL(host string) string {
	if strings.Contains(host, "://") {
		return host
	}
	return "https://" + host
}

// addressSet keeps addresses in order, once each.
type addressSet struct {
	list []Address
	seen map[string]bool
}

func (s *addressSet) add(a Address) {
	key := strings.ToLower(strings.TrimRight(a.URL, "/"))
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	s.list = append(s.list, a)
}

// issueSet keeps one issue per source; it is safe for concurrent use.
type issueSet struct {
	mu   sync.Mutex
	list []AddressIssue
}

func (s *issueSet) add(source string, err error) {
	if cloudflare.IsPermissionDenied(err) {
		s.addMessage(source, "the API token may not read it", true)
		return
	}
	s.addMessage(source, err.Error(), false)
}

func (s *issueSet) addMessage(source, msg string, denied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, is := range s.list {
		if is.Source == source {
			return
		}
	}
	s.list = append(s.list, AddressIssue{Source: source, Message: truncate(msg, maxErrorChars), PermissionDenied: denied})
}

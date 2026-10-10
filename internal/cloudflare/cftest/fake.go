// Package cftest is a fake Cloudflare API for tests: Accounts, Zones, DNS
// records, Workers and Pages projects, with API tokens that see only some
// Accounts and hold only some permissions. It answers in Cloudflare's
// envelope and with Cloudflare's error codes for the cases Neo Box handles.
package cftest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Permissions a Token can hold.
const (
	PermAccountRead = "account:read" // Account Settings Read
	PermZoneRead    = "zone:read"
	PermDNSRead     = "dns:read"
	PermDNSEdit     = "dns:edit"
	PermWorkersRead = "workers:read" // Workers Scripts Read
	PermRoutesRead  = "routes:read"  // Workers Routes Read (zone)
	PermPagesRead   = "pages:read"
)

// Token is an API token known to the fake.
type Token struct {
	// Status is what verify reports; "" means active.
	Status string
	// OwnerAccount makes it an account-owned token, verified only through
	// /accounts/{id}/tokens/verify.
	OwnerAccount string
	// Accounts the token's permissions apply to.
	Accounts []string
	Perms    []string
	// ZonePermissions is what zones report in their legacy permissions.
	ZonePermissions []string
}

func (t *Token) has(perm, account string) bool {
	if !contains(t.Accounts, account) {
		return false
	}
	return contains(t.Perms, perm)
}

// Zone is a Zone in an Account.
type Zone struct {
	ID, Name, Account string
	Status            string
}

// Script is a Worker script.
type Script struct {
	Name           string
	WorkersDev     bool
	RoutesInScript []string
}

// WorkerDomain is a Worker custom domain.
type WorkerDomain struct {
	Hostname, Service, ZoneID string
}

// Route is a Worker route in a Zone.
type Route struct {
	ZoneID, Pattern, Script string
}

// PagesProject is a Pages project.
type PagesProject struct {
	Name, Subdomain    string
	Domains            []string
	CanonicalURL       string
	DomainStatus       map[string]string
	ProductionBranch   string
	FailDomainsRequest bool
}

// Request is one request the fake received.
type Request struct {
	Method, Path, Query string
	Body                map[string]any
}

type Server struct {
	*httptest.Server

	mu         sync.Mutex
	tokens     map[string]*Token
	accounts   map[string]string // id → name
	zones      []*Zone
	records    map[string][]map[string]any // zone ID → records
	nextID     int
	scripts    map[string][]*Script // account → scripts
	subdomains map[string]string    // account → workers.dev subdomain
	domains    map[string][]*WorkerDomain
	routes     []*Route
	projects   map[string][]*PagesProject
	requests   []Request

	// Intercept, when set, sees every request first; returning true means
	// it answered.
	Intercept func(w http.ResponseWriter, r *http.Request) bool
	// DropAnswer, when set and true for a request, makes the fake handle
	// the request and then close the connection without answering, as when
	// a change is made but the answer is lost.
	DropAnswer func(r *http.Request) bool
}

func New() *Server {
	s := &Server{
		tokens: map[string]*Token{}, accounts: map[string]string{}, records: map[string][]map[string]any{},
		scripts: map[string][]*Script{}, subdomains: map[string]string{}, domains: map[string][]*WorkerDomain{},
		projects: map[string][]*PagesProject{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) AddToken(secret string, t *Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[secret] = t
}

func (s *Server) AddAccount(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[id] = name
}

func (s *Server) AddZone(z *Zone) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if z.Status == "" {
		z.Status = "active"
	}
	s.zones = append(s.zones, z)
}

// AddRecord stores a record as Cloudflare would return it; fields not
// given get defaults. It returns the record's ID.
func (s *Server) AddRecord(zoneID string, rec map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertRecord(zoneID, rec)
}

// Record returns a copy of a stored record, or nil.
func (s *Server) Record(zoneID, id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.findRecord(zoneID, id); r != nil {
		return copyMap(r)
	}
	return nil
}

// RecordCount is how many records a Zone holds.
func (s *Server) RecordCount(zoneID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records[zoneID])
}

func (s *Server) AddScript(account string, sc *Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[account] = append(s.scripts[account], sc)
}

func (s *Server) SetWorkersSubdomain(account, sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subdomains[account] = sub
}

func (s *Server) AddWorkerDomain(account string, d *WorkerDomain) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains[account] = append(s.domains[account], d)
}

func (s *Server) AddRoute(r *Route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = append(s.routes, r)
}

func (s *Server) AddProject(account string, p *PagesProject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[account] = append(s.projects[account], p)
}

// Requests returns what the fake received, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Writes returns the non-GET requests the fake received.
func (s *Server) Writes() []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

// --- HTTP ---

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Fail answers with Cloudflare's error envelope.
func Fail(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, map[string]any{
		"success": false, "errors": []apiError{{Code: code, Message: msg}}, "messages": []any{}, "result": nil,
	})
}

func ok(w http.ResponseWriter, result any, info map[string]int) {
	body := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		body["result_info"] = info
	}
	writeJSON(w, http.StatusOK, body)
}

func forbidden(w http.ResponseWriter) {
	Fail(w, http.StatusForbidden, 9109, "Unauthorized to access requested resource")
}

func notFound(w http.ResponseWriter, path string) {
	Fail(w, http.StatusNotFound, 7003, "Could not route to "+path+", perhaps your object identifier is invalid?")
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	var body map[string]any
	if r.Body != nil && r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: path, Query: r.URL.RawQuery, Body: body})
	intercept, drop := s.Intercept, s.DropAnswer
	s.mu.Unlock()
	if intercept != nil && intercept(w, r) {
		return
	}
	if drop != nil && drop(r) {
		s.handle(httptest.NewRecorder(), r, path, body)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
		return
	}
	s.handle(w, r, path, body)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, path string, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if tok == nil || (tok.Status != "" && tok.Status != "active") {
		Fail(w, http.StatusUnauthorized, 1000, "Invalid API Token")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/user/tokens/verify":
		if tok.OwnerAccount != "" {
			Fail(w, http.StatusUnauthorized, 1000, "Invalid API Token")
			return
		}
		ok(w, map[string]any{"id": "tok", "status": "active"}, nil)
	case len(parts) == 4 && parts[0] == "accounts" && parts[2] == "tokens" && parts[3] == "verify":
		if tok.OwnerAccount != parts[1] {
			Fail(w, http.StatusUnauthorized, 1000, "Invalid API Token")
			return
		}
		ok(w, map[string]any{"id": "tok", "status": "active"}, nil)
	case len(parts) == 2 && parts[0] == "accounts":
		s.getAccount(w, tok, parts[1])
	case path == "/zones":
		s.listZones(w, r, tok)
	case len(parts) >= 2 && parts[0] == "zones":
		s.zoneRoutes(w, r, tok, parts, path, body)
	case len(parts) >= 3 && parts[0] == "accounts" && parts[2] == "workers":
		s.workers(w, r, tok, parts, path)
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "pages" && parts[3] == "projects":
		s.pages(w, r, tok, parts, path)
	default:
		notFound(w, path)
	}
}

func (s *Server) getAccount(w http.ResponseWriter, tok *Token, id string) {
	name, exists := s.accounts[id]
	if !exists || !contains(tok.Accounts, id) {
		forbidden(w)
		return
	}
	if !tok.has(PermAccountRead, id) && !tok.has(PermWorkersRead, id) {
		forbidden(w)
		return
	}
	ok(w, map[string]any{"id": id, "name": name}, nil)
}

func (s *Server) zoneVisible(tok *Token, z *Zone) bool {
	return tok.has(PermZoneRead, z.Account) || tok.has(PermDNSRead, z.Account) || tok.has(PermDNSEdit, z.Account)
}

func (s *Server) zoneJSON(tok *Token, z *Zone) map[string]any {
	perms := tok.ZonePermissions
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{
		"id": z.ID, "name": z.Name, "status": z.Status, "paused": false, "type": "full",
		"name_servers": []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"},
		"account":      map[string]any{"id": z.Account, "name": s.accounts[z.Account]},
		"permissions":  perms,
	}
}

func (s *Server) listZones(w http.ResponseWriter, r *http.Request, tok *Token) {
	q := r.URL.Query()
	var visible []*Zone
	anyPerm := false
	for _, acc := range tok.Accounts {
		if tok.has(PermZoneRead, acc) || tok.has(PermDNSRead, acc) || tok.has(PermDNSEdit, acc) {
			anyPerm = true
		}
	}
	if !anyPerm {
		forbidden(w)
		return
	}
	name := q.Get("name")
	for _, z := range s.zones {
		if !s.zoneVisible(tok, z) {
			continue
		}
		if acc := q.Get("account.id"); acc != "" && z.Account != acc {
			continue
		}
		if name != "" && !matchName(z.Name, name) {
			continue
		}
		visible = append(visible, z)
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })
	page, perPage := pageParams(q, 20, 5, 50)
	items, info := paginate(len(visible), page, perPage)
	out := []map[string]any{}
	for _, i := range items {
		out = append(out, s.zoneJSON(tok, visible[i]))
	}
	ok(w, out, info)
}

// matchName applies Cloudflare's name filter: "contains:x", or an exact
// name.
func matchName(name, filter string) bool {
	if v, found := strings.CutPrefix(filter, "contains:"); found {
		return strings.Contains(strings.ToLower(name), strings.ToLower(v))
	}
	return name == filter
}

func (s *Server) zone(id string) *Zone {
	for _, z := range s.zones {
		if z.ID == id {
			return z
		}
	}
	return nil
}

func (s *Server) zoneRoutes(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string, body map[string]any) {
	z := s.zone(parts[1])
	if z == nil || !contains(tok.Accounts, z.Account) {
		notFound(w, path)
		return
	}
	switch {
	case len(parts) == 2:
		if !s.zoneVisible(tok, z) {
			forbidden(w)
			return
		}
		ok(w, s.zoneJSON(tok, z), nil)
	case len(parts) == 4 && parts[2] == "workers" && parts[3] == "routes":
		if !tok.has(PermRoutesRead, z.Account) {
			forbidden(w)
			return
		}
		out := []map[string]any{}
		for i, rt := range s.routes {
			if rt.ZoneID == z.ID {
				out = append(out, map[string]any{"id": fmt.Sprintf("route%d", i), "pattern": rt.Pattern, "script": rt.Script})
			}
		}
		ok(w, out, nil)
	case len(parts) >= 3 && parts[2] == "dns_records":
		s.dns(w, r, tok, z, parts, path, body)
	default:
		notFound(w, path)
	}
}

// --- DNS ---

func (s *Server) dns(w http.ResponseWriter, r *http.Request, tok *Token, z *Zone, parts []string, path string, body map[string]any) {
	write := r.Method != http.MethodGet
	if write && !tok.has(PermDNSEdit, z.Account) {
		Fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	if !write && !tok.has(PermDNSRead, z.Account) && !tok.has(PermDNSEdit, z.Account) {
		Fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	// Cloudflare stores full names: "@" is the Zone, and a name outside the
	// Zone is taken as relative to it.
	if n, ok := body["name"].(string); ok {
		switch {
		case n == "@":
			body["name"] = z.Name
		case n != z.Name && !strings.HasSuffix(n, "."+z.Name):
			body["name"] = n + "." + z.Name
		}
	}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			s.listRecords(w, r, z)
		case http.MethodPost:
			if msg := validateRecord(body); msg != "" {
				Fail(w, http.StatusBadRequest, 9005, msg)
				return
			}
			id := s.insertRecord(z.ID, body)
			ok(w, copyMap(s.findRecord(z.ID, id)), nil)
		default:
			notFound(w, path)
		}
		return
	}
	rec := s.findRecord(z.ID, parts[3])
	if rec == nil {
		Fail(w, http.StatusNotFound, 81044, "Record does not exist.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		ok(w, copyMap(rec), nil)
	case http.MethodPatch:
		merged := copyMap(rec)
		for k, v := range body {
			merged[k] = v
		}
		if msg := validateRecord(merged); msg != "" {
			Fail(w, http.StatusBadRequest, 9005, msg)
			return
		}
		for k := range body {
			rec[k] = merged[k]
		}
		finishRecord(rec)
		rec["modified_on"] = "2026-10-11T00:00:00Z"
		ok(w, copyMap(rec), nil)
	case http.MethodDelete:
		list := s.records[z.ID]
		for i, x := range list {
			if x["id"] == parts[3] {
				s.records[z.ID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		ok(w, map[string]any{"id": parts[3]}, nil)
	default:
		notFound(w, path)
	}
}

func validateRecord(rec map[string]any) string {
	ttl, _ := rec["ttl"].(float64)
	if ttl != 0 && ttl != 1 && (ttl < 60 || ttl > 86400) {
		return "DNS record has an invalid TTL. TTL must be between 60 and 86400 seconds, or 1 for Automatic."
	}
	if p, _ := rec["proxied"].(bool); p && !proxiable(fmt.Sprint(rec["type"])) {
		return "Record type " + fmt.Sprint(rec["type"]) + " cannot be proxied."
	}
	return ""
}

func proxiable(t string) bool { return t == "A" || t == "AAAA" || t == "CNAME" }

func (s *Server) insertRecord(zoneID string, in map[string]any) string {
	s.nextID++
	rec := copyMap(in)
	if rec["id"] == nil {
		rec["id"] = fmt.Sprintf("rec%03d", s.nextID)
	}
	if rec["ttl"] == nil {
		rec["ttl"] = float64(1)
	}
	if rec["proxied"] == nil {
		rec["proxied"] = false
	}
	if rec["created_on"] == nil {
		rec["created_on"] = "2026-10-01T00:00:00Z"
		rec["modified_on"] = "2026-10-01T00:00:00Z"
	}
	finishRecord(rec)
	s.records[zoneID] = append(s.records[zoneID], rec)
	return rec["id"].(string)
}

// finishRecord derives what Cloudflare derives: proxiable, and content
// for SRV and CAA.
func finishRecord(rec map[string]any) {
	t := fmt.Sprint(rec["type"])
	rec["proxiable"] = proxiable(t)
	data, _ := rec["data"].(map[string]any)
	switch t {
	case "SRV":
		if data != nil {
			rec["content"] = fmt.Sprintf("%v %v %v", data["weight"], data["port"], data["target"])
			rec["priority"] = data["priority"]
		}
	case "CAA":
		if data != nil {
			rec["content"] = fmt.Sprintf("%v %v \"%v\"", data["flags"], data["tag"], data["value"])
		}
	}
}

func (s *Server) findRecord(zoneID, id string) map[string]any {
	for _, r := range s.records[zoneID] {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request, z *Zone) {
	q := r.URL.Query()
	search := strings.ToLower(q.Get("search"))
	var match []map[string]any
	for _, rec := range s.records[z.ID] {
		if t := q.Get("type"); t != "" && rec["type"] != t {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(fmt.Sprint(rec["name"])), search) &&
			!strings.Contains(strings.ToLower(fmt.Sprint(rec["content"])), search) {
			continue
		}
		match = append(match, rec)
	}
	sort.SliceStable(match, func(i, j int) bool { return fmt.Sprint(match[i]["name"]) < fmt.Sprint(match[j]["name"]) })
	page, perPage := pageParams(q, 100, 1, 5000000)
	items, info := paginate(len(match), page, perPage)
	out := []map[string]any{}
	for _, i := range items {
		out = append(out, copyMap(match[i]))
	}
	ok(w, out, info)
}

// --- Workers ---

func (s *Server) workers(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string) {
	acc := parts[1]
	if !tok.has(PermWorkersRead, acc) {
		Fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "scripts":
		out := []map[string]any{}
		for _, sc := range s.scripts[acc] {
			item := map[string]any{"id": sc.Name, "created_on": "2026-09-01T00:00:00Z", "modified_on": "2026-09-02T00:00:00Z"}
			if sc.RoutesInScript != nil {
				var routes []map[string]any
				for i, p := range sc.RoutesInScript {
					routes = append(routes, map[string]any{"id": fmt.Sprintf("sr%d", i), "pattern": p, "script": sc.Name})
				}
				item["routes"] = routes
			}
			out = append(out, item)
		}
		ok(w, out, nil)
	case len(parts) == 4 && parts[3] == "subdomain":
		sub, found := s.subdomains[acc]
		if !found {
			Fail(w, http.StatusNotFound, 10007, "This subdomain does not exist.")
			return
		}
		ok(w, map[string]any{"subdomain": sub}, nil)
	case len(parts) == 6 && parts[3] == "scripts" && parts[5] == "subdomain":
		for _, sc := range s.scripts[acc] {
			if sc.Name == parts[4] {
				ok(w, map[string]any{"enabled": sc.WorkersDev, "previews_enabled": false}, nil)
				return
			}
		}
		Fail(w, http.StatusNotFound, 10007, "This Worker does not exist on your account.")
	case len(parts) == 4 && parts[3] == "domains":
		q := r.URL.Query()
		list := s.domains[acc]
		page, perPage := pageParams(q, 20, 1, 100)
		items, info := paginate(len(list), page, perPage)
		out := []map[string]any{}
		for _, i := range items {
			d := list[i]
			out = append(out, map[string]any{"id": fmt.Sprintf("dom%d", i), "hostname": d.Hostname, "service": d.Service, "zone_id": d.ZoneID, "enabled": true})
		}
		ok(w, out, info)
	default:
		notFound(w, path)
	}
}

// --- Pages ---

func (s *Server) pages(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string) {
	acc := parts[1]
	if !tok.has(PermPagesRead, acc) {
		Fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	switch {
	case len(parts) == 4:
		list := s.projects[acc]
		page, perPage := pageParams(r.URL.Query(), 10, 1, 10)
		items, info := paginate(len(list), page, perPage)
		out := []map[string]any{}
		for _, i := range items {
			p := list[i]
			item := map[string]any{
				"name": p.Name, "subdomain": p.Subdomain, "domains": p.Domains,
				"production_branch": p.ProductionBranch, "created_on": "2026-08-01T00:00:00Z",
			}
			if p.CanonicalURL != "" {
				item["canonical_deployment"] = map[string]any{"url": p.CanonicalURL}
			}
			out = append(out, item)
		}
		ok(w, out, info)
	case len(parts) == 6 && parts[5] == "domains":
		for _, p := range s.projects[acc] {
			if p.Name != parts[4] {
				continue
			}
			if p.FailDomainsRequest {
				Fail(w, http.StatusInternalServerError, 8000000, "Internal error")
				return
			}
			out := []map[string]any{}
			for _, d := range p.Domains {
				if strings.HasSuffix(d, ".pages.dev") {
					continue
				}
				status := p.DomainStatus[d]
				if status == "" {
					status = "active"
				}
				out = append(out, map[string]any{"name": d, "status": status})
			}
			ok(w, out, nil)
			return
		}
		notFound(w, path)
	default:
		notFound(w, path)
	}
}

// --- helpers ---

func pageParams(q map[string][]string, def, lo, hi int) (int, int) {
	get := func(k string) int {
		if v := q[k]; len(v) > 0 {
			n, _ := strconv.Atoi(v[0])
			return n
		}
		return 0
	}
	page, perPage := get("page"), get("per_page")
	if page < 1 {
		page = 1
	}
	if perPage == 0 {
		perPage = def
	}
	perPage = max(lo, min(hi, perPage))
	return page, perPage
}

func paginate(n, page, perPage int) ([]int, map[string]int) {
	var idx []int
	for i := (page - 1) * perPage; i < n && i < page*perPage; i++ {
		idx = append(idx, i)
	}
	pages := (n + perPage - 1) / perPage
	return idx, map[string]int{"page": page, "per_page": perPage, "count": len(idx), "total_count": n, "total_pages": pages}
}

func copyMap(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

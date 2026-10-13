package cftest

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// EnvVar is a Pages environment variable. The fake keeps a secret's value
// but never returns it, as Cloudflare does not.
type EnvVar struct{ Type, Value string }

// PagesDeployment is one deployment of a Pages project.
type PagesDeployment struct {
	ID, Environment, Branch string
	// Status is the status of the last stage: "success", "failure" or
	// "active".
	Status string
}

// --- Workers: management ---

func (s *Server) script(account, name string) *Script {
	for _, sc := range s.scripts[account] {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

func (s *Server) zoneName(id string) string {
	if z := s.zone(id); z != nil {
		return z.Name
	}
	return ""
}

func (s *Server) seedVersions(sc *Script, n int) {
	sc.versions, sc.deployments = nil, nil
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("ver-%s-%d", sc.Name, i)
		sc.versions = append(sc.versions, id)
		sc.deployments = append([][]string{{id}}, sc.deployments...)
	}
}

// SetScriptVersions gives a Worker n versions, the last one deployed.
func (s *Server) SetScriptVersions(account, name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc := s.script(account, name); sc != nil {
		s.seedVersions(sc, n)
	}
}

// ScriptState is a copy of a Worker's state.
type ScriptState struct {
	Exists            bool
	WorkersDev        bool
	Previews          bool
	Secrets           []string
	Crons             []string
	Versions          []string
	ServingVersions   []string
	DeploymentsCount  int
	CompatibilityDate string
}

// Script returns a copy of a Worker's state.
func (s *Server) Script(account, name string) ScriptState {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.script(account, name)
	if sc == nil {
		return ScriptState{}
	}
	st := ScriptState{
		Exists: true, WorkersDev: sc.WorkersDev, Previews: sc.Previews,
		Secrets: append([]string(nil), sc.Secrets...), Crons: append([]string(nil), sc.Crons...),
		Versions: append([]string(nil), sc.versions...), DeploymentsCount: len(sc.deployments),
		CompatibilityDate: sc.CompatibilityDate,
	}
	if len(sc.deployments) > 0 {
		st.ServingVersions = append([]string(nil), sc.deployments[0]...)
	}
	return st
}

// WorkerDomains returns copies of an Account's Worker custom domains.
func (s *Server) WorkerDomains(account string) []WorkerDomain {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []WorkerDomain
	for _, d := range s.domains[account] {
		out = append(out, *d)
	}
	return out
}

// Routes returns copies of a Zone's Worker routes.
func (s *Server) Routes(zoneID string) []Route {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Route
	for _, rt := range s.routes {
		if rt.ZoneID == zoneID {
			out = append(out, *rt)
		}
	}
	return out
}

func denied(w http.ResponseWriter) {
	Fail(w, http.StatusForbidden, 10000, "Authentication error")
}

func scriptMissing(w http.ResponseWriter) {
	Fail(w, http.StatusNotFound, 10007, "This Worker does not exist on your account.")
}

func (s *Server) manageWorkers(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string, body map[string]any) bool {
	acc := parts[1]
	write := r.Method != http.MethodGet
	check := func() bool {
		if write && !tok.has(PermWorkersEdit, acc) {
			denied(w)
			return false
		}
		if !write && !tok.has(PermWorkersRead, acc) && !tok.has(PermWorkersEdit, acc) {
			denied(w)
			return false
		}
		return true
	}
	switch {
	// PUT /workers/domains, DELETE /workers/domains/{id}
	case len(parts) == 4 && parts[3] == "domains" && r.Method == http.MethodPut:
		if !check() {
			return true
		}
		host, _ := body["hostname"].(string)
		service, _ := body["service"].(string)
		zoneID, _ := body["zone_id"].(string)
		z := s.zone(zoneID)
		if host == "" || service == "" || z == nil || z.Account != acc {
			Fail(w, http.StatusBadRequest, 10037, "Invalid domain: zone not found")
			return true
		}
		if s.script(acc, service) == nil {
			scriptMissing(w)
			return true
		}
		for _, d := range s.domains[acc] {
			if d.Hostname == host {
				d.Service = service // Cloudflare re-points an attached hostname
				ok(w, s.domainJSON(d), nil)
				return true
			}
		}
		d := &WorkerDomain{Hostname: host, Service: service, ZoneID: zoneID}
		s.insertDomain(acc, d)
		ok(w, s.domainJSON(d), nil)
		return true
	case len(parts) == 5 && parts[3] == "domains" && r.Method == http.MethodDelete:
		if !check() {
			return true
		}
		list := s.domains[acc]
		for i, d := range list {
			if d.ID == parts[4] {
				s.domains[acc] = append(list[:i], list[i+1:]...)
				ok(w, nil, nil)
				return true
			}
		}
		Fail(w, http.StatusNotFound, 10007, "Domain not found")
		return true
	// POST /workers/scripts/{name}/subdomain
	case len(parts) == 6 && parts[3] == "scripts" && parts[5] == "subdomain" && r.Method == http.MethodPost:
		if !check() {
			return true
		}
		sc := s.script(acc, parts[4])
		if sc == nil {
			scriptMissing(w)
			return true
		}
		if _, has := s.subdomains[acc]; !has {
			Fail(w, http.StatusBadRequest, 10054, "You need a workers.dev subdomain first")
			return true
		}
		sc.WorkersDev, _ = body["enabled"].(bool)
		sc.Previews, _ = body["previews_enabled"].(bool)
		ok(w, map[string]any{"enabled": sc.WorkersDev, "previews_enabled": sc.Previews}, nil)
		return true
	case len(parts) >= 5 && parts[3] == "scripts":
		return s.manageScript(w, r, tok, parts, path, body, check)
	}
	return false
}

func (s *Server) domainJSON(d *WorkerDomain) map[string]any {
	return map[string]any{
		"id": d.ID, "hostname": d.Hostname, "service": d.Service, "environment": "production",
		"zone_id": d.ZoneID, "zone_name": s.zoneName(d.ZoneID), "enabled": true, "previews_enabled": false,
	}
}

func (s *Server) manageScript(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string, body map[string]any, check func() bool) bool {
	acc, name := parts[1], parts[4]
	rest := strings.Join(parts[5:], "/")
	// The routes below are the ones this file implements; the subdomain
	// read and the script list are in fake.go.
	switch {
	case rest == "" && r.Method == http.MethodDelete:
	case rest == "versions" && r.Method == http.MethodGet:
	case rest == "deployments" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
	case rest == "schedules" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
	case rest == "settings" && r.Method == http.MethodGet:
	case rest == "secrets" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
	case strings.HasPrefix(rest, "secrets/") && r.Method == http.MethodDelete:
	default:
		return false
	}
	if !check() {
		return true
	}
	sc := s.script(acc, name)
	if sc == nil {
		scriptMissing(w)
		return true
	}
	switch {
	case rest == "":
		list := s.scripts[acc]
		for i, x := range list {
			if x == sc {
				s.scripts[acc] = append(list[:i], list[i+1:]...)
			}
		}
		ok(w, nil, nil)
	case rest == "versions":
		items := []map[string]any{}
		for i := len(sc.versions) - 1; i >= 0; i-- {
			items = append(items, map[string]any{
				"id": sc.versions[i], "number": i + 1,
				"metadata": map[string]any{"author_email": "dev@example.com", "created_on": "2026-09-01T00:00:00Z", "source": "api"},
			})
		}
		ok(w, map[string]any{"items": items}, nil)
	case rest == "deployments" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for i, d := range sc.deployments {
			out = append(out, deploymentJSON(sc, i, d, ""))
		}
		ok(w, map[string]any{"deployments": out}, nil)
	case rest == "deployments":
		vs, _ := body["versions"].([]any)
		var ids []string
		for _, v := range vs {
			m, _ := v.(map[string]any)
			id, _ := m["version_id"].(string)
			if !containsStr(sc.versions, id) {
				Fail(w, http.StatusBadRequest, 10214, "Version "+id+" does not exist")
				return true
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			Fail(w, http.StatusBadRequest, 10214, "at least one version is required")
			return true
		}
		msg := ""
		if ann, _ := body["annotations"].(map[string]any); ann != nil {
			msg, _ = ann["workers/message"].(string)
		}
		sc.deployments = append([][]string{ids}, sc.deployments...)
		ok(w, deploymentJSON(sc, 0, ids, msg), nil)
	case rest == "schedules" && r.Method == http.MethodGet:
		ok(w, map[string]any{"schedules": schedulesJSON(sc.Crons)}, nil)
	case rest == "schedules":
		arr, _ := body["_array"].([]any)
		var crons []string
		for _, e := range arr {
			m, _ := e.(map[string]any)
			c, _ := m["cron"].(string)
			if len(strings.Fields(c)) != 5 {
				Fail(w, http.StatusBadRequest, 10050, "invalid cron expression: "+c)
				return true
			}
			crons = append(crons, c)
		}
		sc.Crons = crons
		ok(w, map[string]any{"schedules": schedulesJSON(crons)}, nil)
	case rest == "settings":
		bindings := []map[string]any{}
		for _, n := range sc.Secrets {
			bindings = append(bindings, map[string]any{"name": n, "type": "secret_text"})
		}
		ok(w, map[string]any{
			"compatibility_date": sc.CompatibilityDate, "compatibility_flags": []string{},
			"usage_model": "standard", "bindings": bindings,
		}, nil)
	case rest == "secrets" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, n := range sc.Secrets {
			out = append(out, map[string]any{"name": n, "type": "secret_text"})
		}
		ok(w, out, nil)
	case rest == "secrets":
		n, _ := body["name"].(string)
		text, _ := body["text"].(string)
		if n == "" || text == "" {
			Fail(w, http.StatusBadRequest, 10021, "secret name and text are required")
			return true
		}
		if !containsStr(sc.Secrets, n) {
			sc.Secrets = append(sc.Secrets, n)
		}
		// A secret change makes a new version and deploys it.
		id := fmt.Sprintf("ver-%s-%d", sc.Name, len(sc.versions)+1)
		sc.versions = append(sc.versions, id)
		sc.deployments = append([][]string{{id}}, sc.deployments...)
		ok(w, map[string]any{"name": n, "type": "secret_text"}, nil)
	default: // secrets/{name} DELETE
		n := strings.TrimPrefix(rest, "secrets/")
		if !containsStr(sc.Secrets, n) {
			Fail(w, http.StatusNotFound, 10056, "Secret not found")
			return true
		}
		var kept []string
		for _, x := range sc.Secrets {
			if x != n {
				kept = append(kept, x)
			}
		}
		sc.Secrets = kept
		ok(w, nil, nil)
	}
	return true
}

func deploymentJSON(sc *Script, index int, versions []string, message string) map[string]any {
	vs := []map[string]any{}
	for _, v := range versions {
		vs = append(vs, map[string]any{"version_id": v, "percentage": 100})
	}
	ann := map[string]any{}
	if message != "" {
		ann["workers/message"] = message
	}
	return map[string]any{
		"id": fmt.Sprintf("dep-%s-%d", sc.Name, len(sc.deployments)-index), "created_on": "2026-09-01T00:00:00Z",
		"source": "api", "strategy": "percentage", "author_email": "dev@example.com",
		"versions": vs, "annotations": ann,
	}
}

func schedulesJSON(crons []string) []map[string]any {
	out := []map[string]any{}
	for _, c := range crons {
		out = append(out, map[string]any{"cron": c, "created_on": "2026-09-01T00:00:00Z", "modified_on": "2026-09-01T00:00:00Z"})
	}
	return out
}

// --- Worker routes (zone) ---

func (s *Server) routesOfZone(w http.ResponseWriter, r *http.Request, tok *Token, z *Zone, parts []string, body map[string]any) {
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		if !tok.has(PermRoutesRead, z.Account) && !tok.has(PermRoutesEdit, z.Account) {
			forbidden(w)
			return
		}
		out := []map[string]any{}
		for _, rt := range s.routes {
			if rt.ZoneID == z.ID {
				out = append(out, map[string]any{"id": rt.ID, "pattern": rt.Pattern, "script": rt.Script})
			}
		}
		ok(w, out, nil)
	case len(parts) == 4 && r.Method == http.MethodPost:
		if !tok.has(PermRoutesEdit, z.Account) {
			denied(w)
			return
		}
		pattern, _ := body["pattern"].(string)
		script, _ := body["script"].(string)
		if pattern == "" {
			Fail(w, http.StatusBadRequest, 10019, "invalid route pattern")
			return
		}
		for _, rt := range s.routes {
			if rt.ZoneID == z.ID && rt.Pattern == pattern {
				Fail(w, http.StatusConflict, 10020, "duplicate route: "+pattern)
				return
			}
		}
		if s.script(z.Account, script) == nil {
			Fail(w, http.StatusBadRequest, 10020, "script "+script+" does not exist")
			return
		}
		rt := &Route{ZoneID: z.ID, Pattern: pattern, Script: script}
		s.insertRoute(rt)
		ok(w, map[string]any{"id": rt.ID, "pattern": rt.Pattern, "script": rt.Script}, nil)
	case len(parts) == 5 && r.Method == http.MethodDelete:
		if !tok.has(PermRoutesEdit, z.Account) {
			denied(w)
			return
		}
		for i, rt := range s.routes {
			if rt.ZoneID == z.ID && rt.ID == parts[4] {
				s.routes = append(s.routes[:i], s.routes[i+1:]...)
				ok(w, map[string]any{"id": rt.ID}, nil)
				return
			}
		}
		Fail(w, http.StatusNotFound, 10009, "route not found")
	default:
		notFound(w, "/zones/"+z.ID+"/workers/routes")
	}
}

// --- Pages: management ---

// AddDeployment adds a deployment to a project (newest last in the list the
// test builds; the API lists newest first).
func (s *Server) AddDeployment(account, project string, d *PagesDeployment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.project(account, project); p != nil {
		if d.Status == "" {
			d.Status = "success"
		}
		p.Deployments = append(p.Deployments, d)
		if d.Environment == "production" && d.Status == "success" {
			p.CanonicalID = d.ID
		}
	}
}

// PagesProjectState is a copy of a project's state.
type PagesProjectState struct {
	Exists                    bool
	ProductionBranch          string
	BuildCommand, DestDir     string
	Domains                   []string
	ProductionEnv, PreviewEnv map[string]EnvVar
	CanonicalID               string
	DeploymentIDs             []string
}

func (s *Server) project(account, name string) *PagesProject {
	for _, p := range s.projects[account] {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Project returns a copy of a Pages project's state.
func (s *Server) Project(account, name string) PagesProjectState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.project(account, name)
	if p == nil {
		return PagesProjectState{}
	}
	st := PagesProjectState{
		Exists: true, ProductionBranch: p.ProductionBranch, BuildCommand: p.BuildCommand, DestDir: p.DestinationDir,
		Domains: append([]string(nil), p.Domains...), CanonicalID: p.CanonicalID,
		ProductionEnv: copyEnv(p.ProductionEnv), PreviewEnv: copyEnv(p.PreviewEnv),
	}
	for _, d := range p.Deployments {
		st.DeploymentIDs = append(st.DeploymentIDs, d.ID)
	}
	return st
}

func copyEnv(in map[string]EnvVar) map[string]EnvVar {
	out := map[string]EnvVar{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func projectMissing(w http.ResponseWriter) {
	Fail(w, http.StatusNotFound, 8000007, "Project not found. The specified project name does not match any of your existing projects.")
}

func (s *Server) managePages(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, path string, body map[string]any) bool {
	acc := parts[1]
	write := r.Method != http.MethodGet
	check := func() bool {
		if write && !tok.has(PermPagesEdit, acc) {
			denied(w)
			return false
		}
		if !write && !tok.has(PermPagesRead, acc) && !tok.has(PermPagesEdit, acc) {
			denied(w)
			return false
		}
		return true
	}
	// POST /pages/projects creates one.
	if len(parts) == 4 && r.Method == http.MethodPost {
		if !check() {
			return true
		}
		name, _ := body["name"].(string)
		branch, _ := body["production_branch"].(string)
		if name == "" {
			Fail(w, http.StatusBadRequest, 8000011, "Project name is required")
			return true
		}
		if s.project(acc, name) != nil {
			Fail(w, http.StatusConflict, 8000002, "A project with this name already exists. Choose a different project name.")
			return true
		}
		p := &PagesProject{Name: name, Subdomain: name + ".pages.dev", Domains: []string{name + ".pages.dev"}, ProductionBranch: branch}
		s.projects[acc] = append(s.projects[acc], p)
		ok(w, s.projectJSON(p), nil)
		return true
	}
	if len(parts) < 5 {
		return false
	}
	// GET one project; other reads are in fake.go (list, domains).
	rest := strings.Join(parts[5:], "/")
	handled := false
	switch {
	case rest == "" && (r.Method == http.MethodGet || r.Method == http.MethodPatch || r.Method == http.MethodDelete):
		handled = true
	case rest == "deployments" && r.Method == http.MethodGet:
		handled = true
	case strings.HasPrefix(rest, "deployments/"):
		handled = true
	case rest == "domains" && r.Method == http.MethodPost:
		handled = true
	case strings.HasPrefix(rest, "domains/") && r.Method == http.MethodDelete:
		handled = true
	}
	if !handled {
		return false
	}
	if !check() {
		return true
	}
	p := s.project(acc, parts[4])
	if p == nil {
		projectMissing(w)
		return true
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		ok(w, s.projectJSON(p), nil)
	case rest == "" && r.Method == http.MethodPatch:
		s.patchProject(w, p, body)
	case rest == "":
		list := s.projects[acc]
		for i, x := range list {
			if x == p {
				s.projects[acc] = append(list[:i], list[i+1:]...)
			}
		}
		ok(w, nil, nil)
	case rest == "deployments":
		var match []*PagesDeployment
		env := r.URL.Query().Get("env")
		for i := len(p.Deployments) - 1; i >= 0; i-- {
			if d := p.Deployments[i]; env == "" || d.Environment == env {
				match = append(match, d)
			}
		}
		page, perPage := pageParams(r.URL.Query(), 25, 1, 100)
		items, info := paginate(len(match), page, perPage)
		out := []map[string]any{}
		for _, i := range items {
			out = append(out, deploymentJSONPages(p, match[i]))
		}
		ok(w, out, info)
	case strings.HasPrefix(rest, "deployments/"):
		s.deploymentRoute(w, r, p, strings.Split(strings.TrimPrefix(rest, "deployments/"), "/"))
	case rest == "domains":
		name, _ := body["name"].(string)
		if name == "" {
			Fail(w, http.StatusBadRequest, 8000018, "domain name is required")
			return true
		}
		if containsStr(p.Domains, name) {
			Fail(w, http.StatusConflict, 8000018, "A custom domain with this name already exists on this project.")
			return true
		}
		p.Domains = append(p.Domains, name)
		if p.DomainStatus == nil {
			p.DomainStatus = map[string]string{}
		}
		p.DomainStatus[name] = "pending"
		ok(w, map[string]any{"name": name, "status": "pending"}, nil)
	default: // domains/{name} DELETE
		name := strings.TrimPrefix(rest, "domains/")
		if !containsStr(p.Domains, name) || strings.HasSuffix(name, ".pages.dev") {
			Fail(w, http.StatusNotFound, 8000018, "Domain not found")
			return true
		}
		var kept []string
		for _, d := range p.Domains {
			if d != name {
				kept = append(kept, d)
			}
		}
		p.Domains = kept
		ok(w, nil, nil)
	}
	return true
}

func (s *Server) deploymentRoute(w http.ResponseWriter, r *http.Request, p *PagesProject, parts []string) {
	var d *PagesDeployment
	for _, x := range p.Deployments {
		if x.ID == parts[0] {
			d = x
		}
	}
	if d == nil {
		Fail(w, http.StatusNotFound, 8000009, "Deployment not found.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		ok(w, deploymentJSONPages(p, d), nil)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if p.CanonicalID == d.ID {
			Fail(w, http.StatusBadRequest, 8000034, "You cannot delete the aliased deployment. Use force to delete it.")
			return
		}
		for i, x := range p.Deployments {
			if x == d {
				p.Deployments = append(p.Deployments[:i], p.Deployments[i+1:]...)
				break
			}
		}
		ok(w, nil, nil)
	case len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost:
		s.nextID++
		nd := &PagesDeployment{ID: fmt.Sprintf("dpl-%d", s.nextID), Environment: d.Environment, Branch: d.Branch, Status: "active"}
		p.Deployments = append(p.Deployments, nd)
		ok(w, deploymentJSONPages(p, nd), nil)
	case len(parts) == 2 && parts[1] == "rollback" && r.Method == http.MethodPost:
		if d.Environment != "production" || d.Status != "success" {
			Fail(w, http.StatusBadRequest, 8000035, "Only a successful production deployment can be rolled back to.")
			return
		}
		p.CanonicalID = d.ID
		ok(w, deploymentJSONPages(p, d), nil)
	default:
		notFound(w, "/pages/deployments")
	}
}

func (s *Server) patchProject(w http.ResponseWriter, p *PagesProject, body map[string]any) {
	if b, has := body["production_branch"].(string); has {
		p.ProductionBranch = b
	}
	if bc, has := body["build_config"].(map[string]any); has {
		p.BuildCommand, _ = bc["build_command"].(string)
		p.DestinationDir, _ = bc["destination_dir"].(string)
		p.RootDir, _ = bc["root_dir"].(string)
	}
	if dc, has := body["deployment_configs"].(map[string]any); has {
		for env, target := range map[string]*map[string]EnvVar{"production": &p.ProductionEnv, "preview": &p.PreviewEnv} {
			cfg, _ := dc[env].(map[string]any)
			vars, _ := cfg["env_vars"].(map[string]any)
			if *target == nil {
				*target = map[string]EnvVar{}
			}
			for k, v := range vars {
				if v == nil {
					delete(*target, k)
					continue
				}
				m, _ := v.(map[string]any)
				typ, _ := m["type"].(string)
				val, _ := m["value"].(string)
				if typ == "" || val == "" {
					Fail(w, http.StatusBadRequest, 8000012, "env var "+k+" needs a type and a value")
					return
				}
				(*target)[k] = EnvVar{Type: typ, Value: val}
			}
		}
	}
	ok(w, s.projectJSON(p), nil)
}

func envJSON(in map[string]EnvVar) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in[k]
		if v.Type == "secret_text" {
			out[k] = map[string]any{"type": v.Type} // the value is never returned
		} else {
			out[k] = map[string]any{"type": v.Type, "value": v.Value}
		}
	}
	return out
}

func (s *Server) projectJSON(p *PagesProject) map[string]any {
	out := map[string]any{
		"name": p.Name, "subdomain": p.Subdomain, "domains": p.Domains,
		"production_branch": p.ProductionBranch, "created_on": "2026-08-01T00:00:00Z",
		"build_config": map[string]any{
			"build_command": p.BuildCommand, "destination_dir": p.DestinationDir, "root_dir": p.RootDir,
			"build_caching": true,
		},
		"deployment_configs": map[string]any{
			"production": map[string]any{"env_vars": envJSON(p.ProductionEnv), "compatibility_date": "2026-01-01"},
			"preview":    map[string]any{"env_vars": envJSON(p.PreviewEnv), "compatibility_date": "2026-01-01"},
		},
	}
	if p.Repo != "" {
		owner, name, _ := strings.Cut(p.Repo, "/")
		out["source"] = map[string]any{"type": "github", "config": map[string]any{
			"owner": owner, "repo_name": name, "production_branch": p.ProductionBranch,
		}}
	}
	if p.CanonicalURL != "" {
		out["canonical_deployment"] = map[string]any{"url": p.CanonicalURL}
	}
	return out
}

func deploymentJSONPages(p *PagesProject, d *PagesDeployment) map[string]any {
	stage := "deploy"
	if d.Status == "active" {
		stage = "build"
	}
	return map[string]any{
		"id": d.ID, "short_id": d.ID[:min(8, len(d.ID))], "project_name": p.Name,
		"url": "https://" + d.ID + "." + p.Subdomain, "aliases": []string{}, "environment": d.Environment,
		"created_on": "2026-09-01T00:00:00Z", "is_skipped": false,
		"deployment_trigger": map[string]any{"type": "ad_hoc", "metadata": map[string]any{
			"branch": d.Branch, "commit_hash": "abc123", "commit_message": "deploy " + d.ID,
		}},
		"latest_stage": map[string]any{"name": stage, "status": d.Status},
	}
}

func containsStr(list []string, v string) bool { return contains(list, v) }

package application

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// workerFixture is a connection with two Workers (api, web) and a
// workers.dev subdomain.
func workerFixture(t *testing.T) (*cfFixture, string) {
	f := newCFFixture(t)
	f.cf.SetWorkersSubdomain(cfAccA, "acme")
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "api", CompatibilityDate: "2026-01-01"})
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "web"})
	return f, f.connect(t, "u1", cfAccA, "full")
}

func wantCode(t *testing.T, what string, err error, code connect.Code) {
	t.Helper()
	if connect.CodeOf(err) != code {
		t.Fatalf("%s: err = %v, want %v", what, err, code)
	}
}

func resourceOps(t *testing.T, f *cfFixture, conn string) []*cfrepo.ResourceOperation {
	t.Helper()
	ops, _, err := f.ops.ListResourceOperations(context.Background(), cfrepo.ResourceFilter{UserID: "u1", ConnectionID: conn, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestCloudflareWorkerManagement(t *testing.T) {
	f, conn := workerFixture(t)
	ctx := asUser("u1")
	srv := f.srv

	got, err := srv.GetCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.GetCloudflareWorkerRequest{ConnectionId: conn, Name: "api"}))
	if err != nil {
		t.Fatalf("GetCloudflareWorker: %v", err)
	}
	w := got.Msg
	if w.GetName() != "api" || w.GetWorkersDevSubdomain() != "acme" || w.GetWorkersDevEnabled() || len(w.GetVersions()) != 2 ||
		w.GetVersions()[0].GetNumber() != 2 || len(w.GetDeployments()) != 2 || w.GetDeployments()[0].GetVersions()[0].GetVersionId() != "ver-api-2" ||
		w.GetCompatibilityDate() != "2026-01-01" || len(w.GetIssues()) != 0 {
		t.Fatalf("worker = %+v", w)
	}
	_, err = srv.GetCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.GetCloudflareWorkerRequest{ConnectionId: conn, Name: "missing"}))
	wantCode(t, "missing worker", err, connect.CodeNotFound)

	// workers.dev: on, then on again (nothing to do).
	on, err := srv.SetCloudflareWorkerWorkersDev(ctx, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: conn, Name: "api", Enabled: true}))
	if err != nil || !on.Msg.GetResult().GetChanged() || on.Msg.GetResult().GetOperation().GetStatus() != neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_SUCCEEDED {
		t.Fatalf("workers.dev on = %+v, %v", on, err)
	}
	if !f.cf.Script(cfAccA, "api").WorkersDev {
		t.Fatal("workers.dev not enabled")
	}
	again, err := srv.SetCloudflareWorkerWorkersDev(ctx, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: conn, Name: "api", Enabled: true}))
	if err != nil || again.Msg.GetResult().GetChanged() || len(resourceOps(t, f, conn)) != 1 {
		t.Fatalf("repeating it = %+v, %v (%d operations)", again, err, len(resourceOps(t, f, conn)))
	}

	// Custom domains.
	attach := func(worker, host string) error {
		_, err := srv.AttachCloudflareWorkerDomain(ctx, connect.NewRequest(&neoboxv1.AttachCloudflareWorkerDomainRequest{ConnectionId: conn, Name: worker, Hostname: host}))
		return err
	}
	if err := attach("api", " App.Alpha.com "); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if ds := f.cf.WorkerDomains(cfAccA); len(ds) != 1 || ds[0].Hostname != "app.alpha.com" || ds[0].Service != "api" {
		t.Fatalf("domains = %+v", ds)
	}
	wantCode(t, "hostname of another Worker", attach("web", "app.alpha.com"), connect.CodeAlreadyExists)
	if ds := f.cf.WorkerDomains(cfAccA); ds[0].Service != "api" {
		t.Fatalf("the hostname was taken over: %+v", ds)
	}
	wantCode(t, "no zone", attach("api", "app.nozone.org"), connect.CodeNotFound)
	wantCode(t, "wildcard", attach("api", "*.alpha.com"), connect.CodeInvalidArgument)
	wantCode(t, "url", attach("api", "https://app.alpha.com/x"), connect.CodeInvalidArgument)
	if err := attach("api", "app.alpha.com"); err != nil || len(f.cf.WorkerDomains(cfAccA)) != 1 {
		t.Fatalf("attach again: %v", err)
	}

	// Routes.
	addRoute := func(worker, pattern string) (*neoboxv1.CloudflareChangeResult, error) {
		r, err := srv.AddCloudflareWorkerRoute(ctx, connect.NewRequest(&neoboxv1.AddCloudflareWorkerRouteRequest{ConnectionId: conn, Name: worker, Pattern: pattern}))
		if err != nil {
			return nil, err
		}
		return r.Msg.GetResult(), nil
	}
	if r, err := addRoute("api", "alpha.com/api/*"); err != nil || !r.GetChanged() {
		t.Fatalf("add route = %+v, %v", r, err)
	}
	if r, err := addRoute("api", "*.bravo.com/*"); err != nil || !r.GetChanged() {
		t.Fatalf("add wildcard route = %+v, %v", r, err)
	}
	if rs := f.cf.Routes("za2"); len(rs) != 1 || rs[0].Script != "api" {
		t.Fatalf("bravo routes = %+v", rs)
	}
	if r, err := addRoute("api", "alpha.com/api/*"); err != nil || r.GetChanged() {
		t.Fatalf("same route again = %+v, %v", r, err)
	}
	_, err = addRoute("web", "alpha.com/api/*")
	wantCode(t, "route of another Worker", err, connect.CodeAlreadyExists)
	for _, bad := range []string{"https://alpha.com/*", "alpha.com", "nozone.org/*", "alpha.com:8080/*", "*/*"} {
		if _, err := addRoute("api", bad); err == nil {
			t.Errorf("route %q was accepted", bad)
		}
	}

	got, _ = srv.GetCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.GetCloudflareWorkerRequest{ConnectionId: conn, Name: "api"}))
	if len(got.Msg.GetRoutes()) != 2 || len(got.Msg.GetDomains()) != 1 || got.Msg.GetDomains()[0].GetZoneName() != "alpha.com" {
		t.Fatalf("routes/domains = %+v / %+v", got.Msg.GetRoutes(), got.Msg.GetDomains())
	}
	var alphaRoute *neoboxv1.CloudflareWorkerRoute
	for _, r := range got.Msg.GetRoutes() {
		if r.GetPattern() == "alpha.com/api/*" {
			alphaRoute = r
		}
	}
	if alphaRoute == nil || alphaRoute.GetZoneId() != "za1" || alphaRoute.GetZoneName() != "alpha.com" {
		t.Fatalf("route = %+v", alphaRoute)
	}
	// A route of another Worker, or of another Zone, cannot be deleted through this one.
	f.cf.AddRoute(&cftest.Route{ZoneID: "za3", Pattern: "charlie.net/*", Script: "web"})
	webRoute := f.cf.Routes("za3")[0]
	_, err = srv.DeleteCloudflareWorkerRoute(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerRouteRequest{ConnectionId: conn, Name: "api", ZoneId: "za3", RouteId: webRoute.ID}))
	wantCode(t, "route of another Worker", err, connect.CodeNotFound)
	_, err = srv.DeleteCloudflareWorkerRoute(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerRouteRequest{ConnectionId: conn, Name: "api", ZoneId: "zb1", RouteId: "x"}))
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
	if _, err := srv.DeleteCloudflareWorkerRoute(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerRouteRequest{ConnectionId: conn, Name: "api", ZoneId: "za1", RouteId: alphaRoute.GetId()})); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if len(f.cf.Routes("za1")) != 0 || len(f.cf.Routes("za3")) != 1 {
		t.Fatalf("routes after delete: %+v / %+v", f.cf.Routes("za1"), f.cf.Routes("za3"))
	}

	// Detach.
	_, err = srv.DetachCloudflareWorkerDomain(ctx, connect.NewRequest(&neoboxv1.DetachCloudflareWorkerDomainRequest{ConnectionId: conn, Name: "web", Hostname: "app.alpha.com"}))
	wantCode(t, "detach from the wrong Worker", err, connect.CodeNotFound)
	if _, err := srv.DetachCloudflareWorkerDomain(ctx, connect.NewRequest(&neoboxv1.DetachCloudflareWorkerDomainRequest{ConnectionId: conn, Name: "api", Hostname: "app.alpha.com"})); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if len(f.cf.WorkerDomains(cfAccA)) != 0 {
		t.Fatal("domain still attached")
	}

	// Cron triggers.
	cron := func(c string) (*neoboxv1.CloudflareChangeResult, error) {
		r, err := srv.AddCloudflareWorkerCron(ctx, connect.NewRequest(&neoboxv1.AddCloudflareWorkerCronRequest{ConnectionId: conn, Name: "api", Cron: c}))
		if err != nil {
			return nil, err
		}
		return r.Msg.GetResult(), nil
	}
	if _, err := cron("*/15  * * * *"); err != nil {
		t.Fatalf("add cron: %v", err)
	}
	if _, err := cron("0 3 * * 1"); err != nil {
		t.Fatalf("add cron: %v", err)
	}
	if r, err := cron("*/15 * * * *"); err != nil || r.GetChanged() {
		t.Fatalf("same cron again = %+v, %v", r, err)
	}
	if _, err := cron("every day"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad cron: %v", err)
	}
	if st := f.cf.Script(cfAccA, "api"); strings.Join(st.Crons, "|") != "*/15 * * * *|0 3 * * 1" {
		t.Fatalf("crons = %v", st.Crons)
	}
	if _, err := srv.DeleteCloudflareWorkerCron(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerCronRequest{ConnectionId: conn, Name: "api", Cron: "*/15 * * * *"})); err != nil {
		t.Fatalf("delete cron: %v", err)
	}
	_, err = srv.DeleteCloudflareWorkerCron(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerCronRequest{ConnectionId: conn, Name: "api", Cron: "1 1 * * *"}))
	wantCode(t, "delete a missing cron", err, connect.CodeNotFound)
	if st := f.cf.Script(cfAccA, "api"); strings.Join(st.Crons, "|") != "0 3 * * 1" {
		t.Fatalf("crons = %v", st.Crons)
	}

	// Secrets: the value reaches Cloudflare and nothing else.
	const secretValue = "s3cret-value-9f2c"
	if _, err := srv.PutCloudflareWorkerSecret(ctx, connect.NewRequest(&neoboxv1.PutCloudflareWorkerSecretRequest{ConnectionId: conn, Name: "api", SecretName: "API_KEY", Value: secretValue})); err != nil {
		t.Fatalf("put secret: %v", err)
	}
	if st := f.cf.Script(cfAccA, "api"); len(st.Secrets) != 1 || st.Secrets[0] != "API_KEY" {
		t.Fatalf("secrets = %v", st.Secrets)
	}
	for _, bad := range []string{"1BAD", "has space", "a-b", ""} {
		_, err := srv.PutCloudflareWorkerSecret(ctx, connect.NewRequest(&neoboxv1.PutCloudflareWorkerSecretRequest{ConnectionId: conn, Name: "api", SecretName: bad, Value: "v"}))
		if err == nil {
			t.Errorf("secret name %q accepted", bad)
		}
	}
	got, _ = srv.GetCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.GetCloudflareWorkerRequest{ConnectionId: conn, Name: "api"}))
	if len(got.Msg.GetSecrets()) != 1 || got.Msg.GetSecrets()[0].GetName() != "API_KEY" {
		t.Fatalf("secrets shown = %+v", got.Msg.GetSecrets())
	}
	_, err = srv.DeleteCloudflareWorkerSecret(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerSecretRequest{ConnectionId: conn, Name: "api", SecretName: "NOPE"}))
	wantCode(t, "delete a missing secret", err, connect.CodeNotFound)
	if _, err := srv.DeleteCloudflareWorkerSecret(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerSecretRequest{ConnectionId: conn, Name: "api", SecretName: "API_KEY"})); err != nil {
		t.Fatalf("delete secret: %v", err)
	}

	// Rollback: the secret put made versions 3, which is serving.
	if st := f.cf.Script(cfAccA, "api"); len(st.Versions) != 3 || st.ServingVersions[0] != "ver-api-3" {
		t.Fatalf("state before rollback = %+v", st)
	}
	rb := func(v string) (*neoboxv1.CloudflareChangeResult, error) {
		r, err := srv.RollbackCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.RollbackCloudflareWorkerRequest{ConnectionId: conn, Name: "api", VersionId: v}))
		if err != nil {
			return nil, err
		}
		return r.Msg.GetResult(), nil
	}
	if r, err := rb("ver-api-1"); err != nil || !r.GetChanged() {
		t.Fatalf("rollback = %+v, %v", r, err)
	}
	if st := f.cf.Script(cfAccA, "api"); len(st.ServingVersions) != 1 || st.ServingVersions[0] != "ver-api-1" {
		t.Fatalf("serving = %v", st.ServingVersions)
	}
	if r, err := rb("ver-api-1"); err != nil || r.GetChanged() {
		t.Fatalf("rollback to what is serving = %+v, %v", r, err)
	}
	if _, err := rb("ver-api-99"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rollback to an unknown version: %v", err)
	}

	// Delete.
	if _, err := srv.DeleteCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareWorkerRequest{ConnectionId: conn, Name: "web"})); err != nil {
		t.Fatalf("delete worker: %v", err)
	}
	if f.cf.Script(cfAccA, "web").Exists || !f.cf.Script(cfAccA, "api").Exists {
		t.Fatal("wrong Worker deleted")
	}

	// The log: newest first, filtered, and without the secret's value.
	list, err := srv.ListCloudflareResourceOperations(ctx, connect.NewRequest(&neoboxv1.ListCloudflareResourceOperationsRequest{ConnectionId: conn, Resource: "api"}))
	if err != nil {
		t.Fatalf("ListCloudflareResourceOperations: %v", err)
	}
	ops := list.Msg.GetOperations()
	if len(ops) == 0 || ops[0].GetAction() != "rollback" || ops[0].GetTarget() != "ver-api-1" ||
		ops[0].GetKind() != neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_WORKER ||
		ops[0].GetBefore().AsMap()["deployment_id"] == nil || ops[0].GetActorId() != "u1" {
		t.Fatalf("newest operation = %+v", ops[0])
	}
	raw, _ := protojson.Marshal(list.Msg)
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("the operation log holds the secret's value: %s", raw)
	}
	for _, op := range resourceOps(t, f, conn) {
		if op.Status != cfrepo.StatusSucceeded {
			t.Errorf("operation %s %s = %s (%s)", op.Action, op.Target, op.Status, op.Error)
		}
	}
	only, _ := srv.ListCloudflareResourceOperations(ctx, connect.NewRequest(&neoboxv1.ListCloudflareResourceOperationsRequest{
		ConnectionId: conn, Kind: neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_PAGES,
	}))
	if len(only.Msg.GetOperations()) != 0 {
		t.Fatalf("pages operations = %d", len(only.Msg.GetOperations()))
	}
	for _, w := range f.cf.Writes() {
		if strings.Contains(w.Path, "workers/scripts/api/secrets") && w.Method == http.MethodPut && w.Body["text"] != secretValue {
			t.Fatalf("secret sent as %v", w.Body["text"])
		}
	}
}

func TestCloudflareManagementIsRefusedWithoutPermission(t *testing.T) {
	f, _ := workerFixture(t)
	conn := f.connect(t, "u1", cfAccA, "manage-read")
	ctx := asUser("u1")

	_, err := f.srv.SetCloudflareWorkerWorkersDev(ctx, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: conn, Name: "api", Enabled: true}))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "Workers Scripts Edit") {
		t.Fatalf("workers.dev without permission: %v", err)
	}
	_, err = f.srv.AddCloudflareWorkerRoute(ctx, connect.NewRequest(&neoboxv1.AddCloudflareWorkerRouteRequest{ConnectionId: conn, Name: "api", Pattern: "alpha.com/*"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "Workers Routes Edit") {
		t.Fatalf("route without permission: %v", err)
	}
	_, err = f.srv.CreateCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.CreateCloudflarePagesProjectRequest{ConnectionId: conn, Name: "site"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "Cloudflare Pages Edit") {
		t.Fatalf("pages without permission: %v", err)
	}
	if st := f.cf.Script(cfAccA, "api"); st.WorkersDev || f.cf.Project(cfAccA, "site").Exists {
		t.Fatalf("something changed: %+v", st)
	}
	// Refused changes are in the log, as failed.
	ops := resourceOps(t, f, conn)
	if len(ops) != 3 {
		t.Fatalf("operations = %d, want the 3 refused ones", len(ops))
	}
	for _, op := range ops {
		if op.Status != cfrepo.StatusFailed || op.Error == "" {
			t.Errorf("operation = %+v", op)
		}
	}
	// Reads still work.
	if _, err := f.srv.GetCloudflareWorker(ctx, connect.NewRequest(&neoboxv1.GetCloudflareWorkerRequest{ConnectionId: conn, Name: "api"})); err != nil {
		t.Fatalf("read: %v", err)
	}

	// Another user's connection is not found, for reads, changes and the log.
	other := asUser("u2")
	_, err = f.srv.SetCloudflareWorkerWorkersDev(other, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: conn, Name: "api", Enabled: true}))
	wantCode(t, "another user's change", err, connect.CodeNotFound)
	_, err = f.srv.ListCloudflareResourceOperations(other, connect.NewRequest(&neoboxv1.ListCloudflareResourceOperationsRequest{ConnectionId: conn}))
	wantCode(t, "another user's log", err, connect.CodeNotFound)
}

func TestCloudflareManagementLogging(t *testing.T) {
	f, conn := workerFixture(t)
	ctx := asUser("u1")
	set := func(on bool) (*connect.Response[neoboxv1.SetCloudflareWorkerWorkersDevResponse], error) {
		return f.srv.SetCloudflareWorkerWorkersDev(ctx, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: conn, Name: "api", Enabled: on}))
	}

	// The answer is lost: unknown, and not sent again.
	f.cf.DropAnswer = func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/subdomain")
	}
	_, err := set(true)
	if connect.CodeOf(err) != connect.CodeUnknown || !strings.Contains(err.Error(), "may or may not") {
		t.Fatalf("lost answer: %v", err)
	}
	if len(f.cf.Writes()) != 1 {
		t.Fatalf("writes = %d, want one", len(f.cf.Writes()))
	}
	ops := resourceOps(t, f, conn)
	if len(ops) != 1 || ops[0].Status != cfrepo.StatusUnknown || ops[0].After != nil {
		t.Fatalf("operation = %+v", ops)
	}
	f.cf.DropAnswer = nil

	// No pending entry, nothing sent.
	f.ops.FailCreate = true
	writes := len(f.cf.Writes())
	_, err = set(false)
	if connect.CodeOf(err) != connect.CodeUnavailable || len(f.cf.Writes()) != writes {
		t.Fatalf("unloggable change: %v, %d writes", err, len(f.cf.Writes())-writes)
	}
	f.ops.FailCreate = false

	// Made, but the outcome can't be logged.
	f.ops.FailFinish = true
	resp, err := set(false)
	if err != nil || !resp.Msg.GetResult().GetChanged() || resp.Msg.GetResult().GetOperationLogError() == "" {
		t.Fatalf("unlogged outcome = %+v, %v", resp, err)
	}
	f.ops.FailFinish = false
	if ops := resourceOps(t, f, conn); ops[0].Status != cfrepo.StatusPending {
		t.Fatalf("logged = %+v", ops[0])
	}

	// A workers.dev subdomain is needed to turn it on.
	f.cf.SetWorkersSubdomain(cfAccB, "")
	f.cf.AddScript(cfAccB, &cftest.Script{Name: "lonely"})
	connB := f.connect(t, "u1", cfAccB, "full")
	_, err = f.srv.SetCloudflareWorkerWorkersDev(ctx, connect.NewRequest(&neoboxv1.SetCloudflareWorkerWorkersDevRequest{ConnectionId: connB, Name: "lonely", Enabled: true}))
	wantCode(t, "no workers.dev subdomain", err, connect.CodeFailedPrecondition)
}

func pagesFixture(t *testing.T) (*cfFixture, string) {
	f := newCFFixture(t)
	f.cf.AddProject(cfAccA, &cftest.PagesProject{
		Name: "site", Subdomain: "site.pages.dev", Domains: []string{"site.pages.dev"}, ProductionBranch: "main",
		BuildCommand: "npm run build", DestinationDir: "dist",
	})
	f.cf.AddDeployment(cfAccA, "site", &cftest.PagesDeployment{ID: "dpl-old", Environment: "production", Branch: "main"})
	f.cf.AddDeployment(cfAccA, "site", &cftest.PagesDeployment{ID: "dpl-prev", Environment: "preview", Branch: "feat"})
	f.cf.AddDeployment(cfAccA, "site", &cftest.PagesDeployment{ID: "dpl-live", Environment: "production", Branch: "main"})
	f.cf.AddDeployment(cfAccA, "site", &cftest.PagesDeployment{ID: "dpl-bad", Environment: "production", Branch: "main", Status: "failure"})
	f.cf.AddProject(cfAccA, &cftest.PagesProject{Name: "gitsite", Subdomain: "gitsite.pages.dev", Domains: []string{"gitsite.pages.dev"}, ProductionBranch: "main", Repo: "acme/gitsite"})
	return f, f.connect(t, "u1", cfAccA, "full")
}

func TestCloudflarePagesManagement(t *testing.T) {
	f, conn := pagesFixture(t)
	ctx := asUser("u1")
	srv := f.srv
	call := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	got, err := srv.GetCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.GetCloudflarePagesProjectRequest{ConnectionId: conn, Name: "site"}))
	call(err, "GetCloudflarePagesProject")
	p := got.Msg
	if p.GetName() != "site" || p.GetProductionBranch() != "main" || p.GetBuildConfig().GetBuildCommand() != "npm run build" ||
		p.GetGitRepository() != "" || p.GetSubdomain() != "site.pages.dev" {
		t.Fatalf("project = %+v", p)
	}
	_, err = srv.GetCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.GetCloudflarePagesProjectRequest{ConnectionId: conn, Name: "nope"}))
	wantCode(t, "missing project", err, connect.CodeNotFound)

	// Create.
	_, err = srv.CreateCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.CreateCloudflarePagesProjectRequest{ConnectionId: conn, Name: "Bad_Name"}))
	wantCode(t, "invalid name", err, connect.CodeInvalidArgument)
	_, err = srv.CreateCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.CreateCloudflarePagesProjectRequest{ConnectionId: conn, Name: "site"}))
	wantCode(t, "existing name", err, connect.CodeInvalidArgument)
	_, err = srv.CreateCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.CreateCloudflarePagesProjectRequest{ConnectionId: conn, Name: "fresh"}))
	call(err, "create")
	if st := f.cf.Project(cfAccA, "fresh"); !st.Exists || st.ProductionBranch != "main" {
		t.Fatalf("created = %+v", st)
	}

	// Settings.
	up := func(m *neoboxv1.UpdateCloudflarePagesProjectRequest) (*neoboxv1.CloudflareChangeResult, error) {
		m.ConnectionId = conn
		r, err := srv.UpdateCloudflarePagesProject(ctx, connect.NewRequest(m))
		if err != nil {
			return nil, err
		}
		return r.Msg.GetResult(), nil
	}
	cmd := "pnpm build"
	if r, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "site", BuildCommand: &cmd}); err != nil || !r.GetChanged() {
		t.Fatalf("update = %+v, %v", r, err)
	}
	if st := f.cf.Project(cfAccA, "site"); st.BuildCommand != "pnpm build" || st.DestDir != "dist" || st.ProductionBranch != "main" {
		t.Fatalf("after update = %+v", st)
	}
	if r, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "site", BuildCommand: &cmd}); err != nil || r.GetChanged() {
		t.Fatalf("same update = %+v, %v", r, err)
	}
	branch := "release"
	if _, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "site", ProductionBranch: &branch}); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if _, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "gitsite", ProductionBranch: &branch}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("branch of a Git project: %v", err)
	}
	if _, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "site"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty update: %v", err)
	}
	bad := "has space"
	if _, err := up(&neoboxv1.UpdateCloudflarePagesProjectRequest{Name: "site", ProductionBranch: &bad}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad branch: %v", err)
	}

	// Environment variables: values go to Cloudflare, never to the log, and a
	// secret's never comes back.
	const secretValue = "pages-secret-7731"
	setVar := func(env neoboxv1.CloudflarePagesEnvironment, name, value string, secret bool) error {
		_, err := srv.SetCloudflarePagesEnvVar(ctx, connect.NewRequest(&neoboxv1.SetCloudflarePagesEnvVarRequest{
			ConnectionId: conn, Name: "site", Environment: env, VarName: name, Value: value, Secret: secret,
		}))
		return err
	}
	prod := neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PRODUCTION
	preview := neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_PREVIEW
	call(setVar(prod, "API_URL", "https://api.example.com", false), "set plain")
	call(setVar(prod, "TOKEN", secretValue, true), "set secret")
	call(setVar(preview, "API_URL", "https://staging.example.com", false), "set preview")
	if err := setVar(neoboxv1.CloudflarePagesEnvironment_CLOUDFLARE_PAGES_ENVIRONMENT_UNSPECIFIED, "X", "v", false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no environment: %v", err)
	}
	if err := setVar(prod, "1bad", "v", false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad name: %v", err)
	}
	st := f.cf.Project(cfAccA, "site")
	if st.ProductionEnv["TOKEN"].Type != "secret_text" || st.ProductionEnv["TOKEN"].Value != secretValue || st.PreviewEnv["API_URL"].Value != "https://staging.example.com" || len(st.PreviewEnv) != 1 {
		t.Fatalf("env = %+v / %+v", st.ProductionEnv, st.PreviewEnv)
	}
	got, _ = srv.GetCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.GetCloudflarePagesProjectRequest{ConnectionId: conn, Name: "site"}))
	raw, _ := protojson.Marshal(got.Msg)
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("project response holds the secret: %s", raw)
	}
	vars := got.Msg.GetProductionEnvVars()
	if len(vars) != 2 || vars[0].GetName() != "API_URL" || vars[0].GetValue() != "https://api.example.com" ||
		vars[1].GetName() != "TOKEN" || vars[1].GetType() != neoboxv1.CloudflarePagesEnvVarType_CLOUDFLARE_PAGES_ENV_VAR_TYPE_SECRET || vars[1].GetValue() != "" {
		t.Fatalf("production vars = %+v", vars)
	}
	delVar := func(env neoboxv1.CloudflarePagesEnvironment, name string) error {
		_, err := srv.DeleteCloudflarePagesEnvVar(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflarePagesEnvVarRequest{ConnectionId: conn, Name: "site", Environment: env, VarName: name}))
		return err
	}
	call(delVar(prod, "API_URL"), "delete var")
	wantCode(t, "delete a missing var", delVar(prod, "API_URL"), connect.CodeNotFound)
	if st := f.cf.Project(cfAccA, "site"); len(st.ProductionEnv) != 1 || len(st.PreviewEnv) != 1 {
		t.Fatalf("env after delete = %+v / %+v", st.ProductionEnv, st.PreviewEnv)
	}

	// Deployments.
	list, err := srv.ListCloudflarePagesDeployments(ctx, connect.NewRequest(&neoboxv1.ListCloudflarePagesDeploymentsRequest{ConnectionId: conn, Name: "site", Environment: preview}))
	if err != nil || len(list.Msg.GetDeployments()) != 1 || list.Msg.GetDeployments()[0].GetId() != "dpl-prev" ||
		list.Msg.GetDeployments()[0].GetEnvironment() != preview {
		t.Fatalf("preview deployments = %+v, %v", list, err)
	}
	all, err := srv.ListCloudflarePagesDeployments(ctx, connect.NewRequest(&neoboxv1.ListCloudflarePagesDeploymentsRequest{ConnectionId: conn, Name: "site", PageSize: 2}))
	if err != nil || len(all.Msg.GetDeployments()) != 2 || all.Msg.GetDeployments()[0].GetId() != "dpl-bad" || all.Msg.GetPageInfo().GetTotalCount() != 4 {
		t.Fatalf("deployments = %+v, %v", all, err)
	}
	roll := func(id string) error {
		_, err := srv.RollbackCloudflarePagesDeployment(ctx, connect.NewRequest(&neoboxv1.RollbackCloudflarePagesDeploymentRequest{ConnectionId: conn, Name: "site", DeploymentId: id}))
		return err
	}
	wantCode(t, "rollback to a preview deployment", roll("dpl-prev"), connect.CodeInvalidArgument)
	wantCode(t, "rollback to a failed deployment", roll("dpl-bad"), connect.CodeInvalidArgument)
	wantCode(t, "rollback to a missing deployment", roll("nope"), connect.CodeNotFound)
	if st := f.cf.Project(cfAccA, "site"); st.CanonicalID != "dpl-live" {
		t.Fatalf("canonical = %s", st.CanonicalID)
	}
	call(roll("dpl-old"), "rollback")
	if st := f.cf.Project(cfAccA, "site"); st.CanonicalID != "dpl-old" {
		t.Fatalf("canonical after rollback = %s", st.CanonicalID)
	}
	_, err = srv.RetryCloudflarePagesDeployment(ctx, connect.NewRequest(&neoboxv1.RetryCloudflarePagesDeploymentRequest{ConnectionId: conn, Name: "site", DeploymentId: "dpl-bad"}))
	call(err, "retry")
	if st := f.cf.Project(cfAccA, "site"); len(st.DeploymentIDs) != 5 {
		t.Fatalf("deployments after retry = %v", st.DeploymentIDs)
	}
	del := func(id string) error {
		_, err := srv.DeleteCloudflarePagesDeployment(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflarePagesDeploymentRequest{ConnectionId: conn, Name: "site", DeploymentId: id}))
		return err
	}
	if err := del("dpl-old"); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("delete the live deployment: %v", err)
	}
	call(del("dpl-bad"), "delete deployment")
	for _, w := range f.cf.Writes() {
		if w.Method == http.MethodDelete && strings.Contains(w.Query, "force") {
			t.Fatalf("a delete forced: %+v", w)
		}
	}

	// Domains, with and without the DNS record.
	addDomain := func(host string, dns bool) (*neoboxv1.AddCloudflarePagesDomainResponse, error) {
		r, err := srv.AddCloudflarePagesDomain(ctx, connect.NewRequest(&neoboxv1.AddCloudflarePagesDomainRequest{ConnectionId: conn, Name: "site", Hostname: host, CreateDns: dns}))
		if err != nil {
			return nil, err
		}
		return r.Msg, nil
	}
	r, err := addDomain("www.alpha.com", true)
	if err != nil || !r.GetResult().GetChanged() || r.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CREATED {
		t.Fatalf("add domain with DNS = %+v, %v", r, err)
	}
	if recs := f.cf.Records("za1"); len(recs) != 1 || recs[0]["type"] != "CNAME" || recs[0]["content"] != "site.pages.dev" || recs[0]["proxied"] != true {
		t.Fatalf("DNS records = %+v", recs)
	}
	r, err = addDomain("docs.alpha.com", false)
	if err != nil || r.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_NOT_REQUESTED || f.cf.RecordCount("za1") != 1 {
		t.Fatalf("add domain without DNS = %+v, %v", r, err)
	}
	if _, err := addDomain("www.alpha.com", false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("add it twice: %v", err)
	}
	// A domain whose name already has another record keeps it.
	f.cf.AddRecord("za2", map[string]any{"type": "A", "name": "app.bravo.com", "content": "192.0.2.1"})
	r, err = addDomain("app.bravo.com", true)
	if err != nil || r.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CONFLICT || f.cf.RecordCount("za2") != 1 {
		t.Fatalf("add domain over an A record = %+v, %v", r, err)
	}
	delDomain := func(host string) error {
		_, err := srv.DeleteCloudflarePagesDomain(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflarePagesDomainRequest{ConnectionId: conn, Name: "site", Hostname: host}))
		return err
	}
	call(delDomain("docs.alpha.com"), "delete domain")
	wantCode(t, "delete pages.dev", delDomain("site.pages.dev"), connect.CodeNotFound)
	wantCode(t, "delete a missing domain", delDomain("nope.alpha.com"), connect.CodeNotFound)
	if f.cf.RecordCount("za1") != 1 {
		t.Fatal("deleting the domain deleted its DNS record")
	}

	// Delete the project.
	_, err = srv.DeleteCloudflarePagesProject(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflarePagesProjectRequest{ConnectionId: conn, Name: "fresh"}))
	call(err, "delete project")
	if f.cf.Project(cfAccA, "fresh").Exists || !f.cf.Project(cfAccA, "site").Exists {
		t.Fatal("wrong project deleted")
	}

	// The log holds no secret value, and every change succeeded.
	logRaw := ""
	for _, op := range resourceOps(t, f, conn) {
		b, _ := protojson.Marshal(resourceOperationToProto(op))
		logRaw += string(b)
		// Cloudflare refused these two (the live deployment, a taken name).
		if (op.Action == "delete_deployment" && op.Target == "dpl-old") || (op.Action == "create_project" && op.Resource == "site") {
			if op.Status != cfrepo.StatusFailed {
				t.Errorf("refused %s = %s", op.Action, op.Status)
			}
			continue
		}
		if op.Status != cfrepo.StatusSucceeded {
			t.Errorf("operation %s %s = %s (%s)", op.Action, op.Target, op.Status, op.Error)
		}
	}
	if strings.Contains(logRaw, secretValue) || strings.Contains(logRaw, "staging.example.com") || strings.Contains(logRaw, "api.example.com") {
		t.Fatalf("the log holds an environment variable's value: %s", logRaw)
	}
}

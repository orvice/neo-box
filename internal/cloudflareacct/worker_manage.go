package cloudflareacct

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

const (
	workersReadPermission = "Workers Scripts Read"
	workersEditPermission = "Workers Scripts Edit"
	routesEditPermission  = "Workers Routes Edit"

	// versionListSize is how many recent versions a Worker's detail and a
	// rollback look at.
	versionListSize = 100
	// recentDeployments and recentVersions are how many Neo Box shows.
	recentDeployments = 10
	recentVersions    = 20
)

// WorkerDetail is one Worker with everything Neo Box manages. Each part
// that could not be read is empty and named in Issues.
type WorkerDetail struct {
	Name       string
	CreatedOn  time.Time
	ModifiedOn time.Time
	// Subdomain is the Account's workers.dev subdomain, "" when it has none.
	Subdomain string
	// WorkersDev is nil when it could not be read.
	WorkersDev  *cloudflare.WorkersDevState
	Domains     []cloudflare.WorkerDomain
	Routes      []cloudflare.WorkerRoute
	Schedules   []cloudflare.WorkerSchedule
	Secrets     []cloudflare.WorkerSecret
	Settings    *cloudflare.WorkerSettings
	Deployments []cloudflare.WorkerDeployment
	Versions    []cloudflare.WorkerVersion
	Issues      []AddressIssue
}

// requireWorker finds the Worker among the Account's.
func (a *Account) requireWorker(ctx context.Context, name string) (*cloudflare.WorkerScript, error) {
	scripts, err := a.api.ListWorkerScripts(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read Workers", workersReadPermission)
	}
	for i := range scripts {
		if scripts[i].ID == name {
			return &scripts[i], nil
		}
	}
	return nil, ErrWorkerNotFound
}

// Worker reads one Worker live.
func (a *Account) Worker(ctx context.Context, name string) (*WorkerDetail, error) {
	script, err := a.requireWorker(ctx, name)
	if err != nil {
		return nil, err
	}
	d := &WorkerDetail{Name: name, CreatedOn: script.CreatedOn, ModifiedOn: script.ModifiedOn}
	var (
		issues issueSet
		wg     sync.WaitGroup
	)
	read := func(source string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				issues.add(source, err)
			}
		}()
	}
	read("workers.dev", func() error {
		sub, err := a.api.WorkersSubdomain(ctx, a.ID)
		if err != nil {
			return err
		}
		d.Subdomain = sub
		st, err := a.api.GetWorkersDev(ctx, a.ID, name)
		d.WorkersDev = st
		return err
	})
	read("Worker custom domains", func() error {
		all, err := a.api.ListWorkerDomains(ctx, a.ID)
		for _, dom := range all {
			if dom.Service == name {
				d.Domains = append(d.Domains, dom)
			}
		}
		return err
	})
	read("Worker routes", func() error {
		var iss issueSet
		routes, _ := a.zoneRoutes(ctx, &iss)
		for _, r := range routes {
			if r.Script == name {
				d.Routes = append(d.Routes, r)
			}
		}
		for _, is := range iss.list {
			issues.addMessage(is.Source, is.Message, is.PermissionDenied)
		}
		return nil
	})
	read("cron triggers", func() (err error) {
		d.Schedules, err = a.api.ListWorkerSchedules(ctx, a.ID, name)
		return err
	})
	read("secrets", func() (err error) {
		d.Secrets, err = a.api.ListWorkerSecrets(ctx, a.ID, name)
		return err
	})
	read("settings", func() (err error) {
		d.Settings, err = a.api.GetWorkerSettings(ctx, a.ID, name)
		return err
	})
	read("deployments", func() (err error) {
		d.Deployments, err = a.api.ListWorkerDeployments(ctx, a.ID, name)
		if len(d.Deployments) > recentDeployments {
			d.Deployments = d.Deployments[:recentDeployments]
		}
		return err
	})
	read("versions", func() (err error) {
		d.Versions, err = a.api.ListWorkerVersions(ctx, a.ID, name, recentVersions)
		if len(d.Versions) > recentVersions {
			d.Versions = d.Versions[:recentVersions]
		}
		return err
	})
	wg.Wait()
	sort.Slice(d.Domains, func(i, j int) bool { return d.Domains[i].Hostname < d.Domains[j].Hostname })
	sort.Slice(d.Routes, func(i, j int) bool { return d.Routes[i].Pattern < d.Routes[j].Pattern })
	d.Issues = issues.list
	return d, nil
}

// SetWorkersDev turns the Worker's workers.dev address on or off.
func (a *Account) SetWorkersDev(ctx context.Context, actor Actor, name string, enabled bool) (*OperationResult, error) {
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	sub, err := a.api.WorkersSubdomain(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read the workers.dev subdomain", workersReadPermission)
	}
	if enabled && sub == "" {
		return nil, ErrNoWorkersSubdomain
	}
	cur, err := a.api.GetWorkersDev(ctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's workers.dev state", workersReadPermission)
	}
	if cur.Enabled == enabled {
		return noChange(), nil
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "set_workers_dev",
		before:    map[string]any{"enabled": cur.Enabled},
		requested: map[string]any{"enabled": enabled},
		what:      "change this Worker's workers.dev address", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			st, err := a.api.SetWorkersDev(ctx, a.ID, name, cloudflare.WorkersDevState{Enabled: enabled, PreviewsEnabled: cur.PreviewsEnabled})
			if err != nil {
				return nil, err
			}
			return map[string]any{"enabled": st.Enabled}, nil
		},
	})
}

// AttachWorkerDomain serves the Worker on a custom domain of one of the
// Account's Zones. A hostname that already belongs to another Worker is
// refused, though Cloudflare would move it.
func (a *Account) AttachWorkerDomain(ctx context.Context, actor Actor, name, hostname string) (*OperationResult, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if err := cloudflare.ValidateDomainName(hostname); err != nil {
		return nil, err
	}
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	zone, err := a.zoneFor(ctx, hostname)
	if err != nil {
		if errors.Is(err, ErrZoneNotFound) {
			return nil, fmt.Errorf("%w: this account has no zone for %s", ErrZoneNotFound, hostname)
		}
		return nil, a.classify(ctx, err, "read Zones", "Zone Read")
	}
	domains, err := a.api.ListWorkerDomains(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read Worker custom domains", workersReadPermission)
	}
	for _, d := range domains {
		if strings.EqualFold(d.Hostname, hostname) {
			if d.Service == name {
				return noChange(), nil
			}
			return nil, fmt.Errorf("%w: %s is attached to the Worker %q", ErrInUse, hostname, d.Service)
		}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "attach_domain", target: hostname,
		requested: map[string]any{"hostname": hostname, "zone": zone.Name},
		what:      "attach custom domains to this Worker", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			d, err := a.api.AttachWorkerDomain(ctx, a.ID, name, hostname, zone.ID, zone.Name)
			if err != nil {
				return nil, err
			}
			return map[string]any{"hostname": d.Hostname, "zone": d.ZoneName}, nil
		},
	})
}

// DetachWorkerDomain removes one of the Worker's custom domains.
func (a *Account) DetachWorkerDomain(ctx context.Context, actor Actor, name, hostname string) (*OperationResult, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	domains, err := a.api.ListWorkerDomains(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read Worker custom domains", workersReadPermission)
	}
	for _, d := range domains {
		if d.Service != name || !strings.EqualFold(d.Hostname, hostname) {
			continue
		}
		return a.operate(ctx, actor, resourceChange{
			kind: cfrepo.KindWorker, resource: name, action: "detach_domain", target: d.Hostname,
			before: map[string]any{"hostname": d.Hostname, "zone": d.ZoneName},
			what:   "detach custom domains from this Worker", permission: workersEditPermission,
			send: func(ctx context.Context) (map[string]any, error) {
				return nil, a.api.DetachWorkerDomain(ctx, a.ID, d.ID)
			},
		})
	}
	return nil, fmt.Errorf("%w: custom domain %s", ErrNotAttached, hostname)
}

// zoneRoutesOf reads one Zone's Worker routes after checking the Zone is
// the Account's.
func (a *Account) zoneRoutesOf(ctx context.Context, zone *cloudflare.Zone) ([]cloudflare.WorkerRoute, error) {
	routes, err := a.api.ListWorkerRoutes(ctx, zone.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's Worker routes", "Workers Routes Read")
	}
	return routes, nil
}

// AddWorkerRoute runs the Worker on a route pattern, in the Zone that holds
// the pattern's host. A pattern that already runs another Worker is
// refused.
func (a *Account) AddWorkerRoute(ctx context.Context, actor Actor, name, pattern string) (*OperationResult, error) {
	pattern = strings.TrimSpace(pattern)
	if err := cloudflare.ValidateRoutePattern(strings.ToLower(pattern)); err != nil {
		return nil, err
	}
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	_, zoneHost := cloudflare.RouteHost(strings.ToLower(pattern))
	zone, err := a.zoneFor(ctx, zoneHost)
	if err != nil {
		if errors.Is(err, ErrZoneNotFound) {
			return nil, fmt.Errorf("%w: this account has no zone for %s", ErrZoneNotFound, zoneHost)
		}
		return nil, a.classify(ctx, err, "read Zones", "Zone Read")
	}
	routes, err := a.zoneRoutesOf(ctx, zone)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if strings.EqualFold(r.Pattern, pattern) {
			if r.Script == name {
				return noChange(), nil
			}
			return nil, fmt.Errorf("%w: %s runs the Worker %q", ErrInUse, pattern, r.Script)
		}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "add_route", target: pattern,
		requested: map[string]any{"pattern": pattern, "zone": zone.Name},
		what:      "add routes for this Worker", permission: routesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			r, err := a.api.CreateWorkerRoute(ctx, zone.ID, pattern, name)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pattern": r.Pattern, "zone": zone.Name}, nil
		},
	})
}

// DeleteWorkerRoute removes one of the Worker's routes.
func (a *Account) DeleteWorkerRoute(ctx context.Context, actor Actor, name, zoneID, routeID string) (*OperationResult, error) {
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	routes, err := a.zoneRoutesOf(ctx, zone)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if r.ID != routeID || r.Script != name {
			continue
		}
		return a.operate(ctx, actor, resourceChange{
			kind: cfrepo.KindWorker, resource: name, action: "delete_route", target: r.Pattern,
			before: map[string]any{"pattern": r.Pattern, "zone": zone.Name},
			what:   "delete routes of this Worker", permission: routesEditPermission,
			send: func(ctx context.Context) (map[string]any, error) {
				return nil, a.api.DeleteWorkerRoute(ctx, zone.ID, r.ID)
			},
		})
	}
	return nil, fmt.Errorf("%w: route", ErrNotAttached)
}

func normalizeCron(c string) string { return strings.Join(strings.Fields(c), " ") }

func (a *Account) workerLock(name string) func() {
	return a.m.lockResource("worker/" + a.ID + "/" + name)
}

// editSchedules reads the Worker's cron triggers, lets edit change them,
// and replaces them, all under the Worker's lock because Cloudflare
// replaces the whole list.
func (a *Account) editSchedules(ctx context.Context, actor Actor, name, action, cron string, edit func(cur []string) (next []string, ok bool)) (*OperationResult, error) {
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	defer a.workerLock(name)()
	cur, err := a.api.ListWorkerSchedules(ctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's cron triggers", workersReadPermission)
	}
	var crons []string
	for _, s := range cur {
		crons = append(crons, s.Cron)
	}
	next, changed := edit(crons)
	if !changed {
		return noChange(), nil
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: action, target: cron,
		before:    map[string]any{"crons": toAny(crons)},
		requested: map[string]any{"crons": toAny(next)},
		what:      "change this Worker's cron triggers", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			got, err := a.api.PutWorkerSchedules(ctx, a.ID, name, next)
			if err != nil {
				return nil, err
			}
			var after []string
			for _, s := range got {
				after = append(after, s.Cron)
			}
			return map[string]any{"crons": toAny(after)}, nil
		},
	})
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// AddWorkerSchedule adds a cron trigger.
func (a *Account) AddWorkerSchedule(ctx context.Context, actor Actor, name, cron string) (*OperationResult, error) {
	cron = normalizeCron(cron)
	if err := cloudflare.ValidateCron(cron); err != nil {
		return nil, err
	}
	return a.editSchedules(ctx, actor, name, "add_schedule", cron, func(cur []string) ([]string, bool) {
		for _, c := range cur {
			if c == cron {
				return nil, false
			}
		}
		return append(append([]string(nil), cur...), cron), true
	})
}

// DeleteWorkerSchedule removes a cron trigger.
func (a *Account) DeleteWorkerSchedule(ctx context.Context, actor Actor, name, cron string) (*OperationResult, error) {
	cron = normalizeCron(cron)
	var missing bool
	res, err := a.editSchedules(ctx, actor, name, "delete_schedule", cron, func(cur []string) ([]string, bool) {
		var next []string
		for _, c := range cur {
			if c != cron {
				next = append(next, c)
			}
		}
		missing = len(next) == len(cur)
		return next, !missing
	})
	if err == nil && missing {
		return nil, fmt.Errorf("%w: cron trigger %q", ErrNotAttached, cron)
	}
	return res, err
}

// PutWorkerSecret sets a secret binding of the Worker. The value is sent
// to Cloudflare and kept nowhere, the log included. Cloudflare makes a new
// version with the secret and deploys it.
func (a *Account) PutWorkerSecret(ctx context.Context, actor Actor, name, secretName, value string) (*OperationResult, error) {
	if err := cloudflare.ValidateBindingName(secretName); err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateSecretValue(value); err != nil {
		return nil, err
	}
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	secrets, err := a.api.ListWorkerSecrets(ctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's secrets", workersReadPermission)
	}
	var exists bool
	for _, s := range secrets {
		exists = exists || s.Name == secretName
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "put_secret", target: secretName,
		before:    map[string]any{"exists": exists},
		requested: map[string]any{"name": secretName, "replaces": exists},
		what:      "change this Worker's secrets", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return map[string]any{"name": secretName}, a.api.PutWorkerSecret(ctx, a.ID, name, secretName, value)
		},
	})
}

// DeleteWorkerSecret removes a secret binding.
func (a *Account) DeleteWorkerSecret(ctx context.Context, actor Actor, name, secretName string) (*OperationResult, error) {
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	secrets, err := a.api.ListWorkerSecrets(ctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's secrets", workersReadPermission)
	}
	for _, s := range secrets {
		if s.Name != secretName {
			continue
		}
		return a.operate(ctx, actor, resourceChange{
			kind: cfrepo.KindWorker, resource: name, action: "delete_secret", target: secretName,
			before: map[string]any{"name": secretName, "type": s.Type},
			what:   "change this Worker's secrets", permission: workersEditPermission,
			send: func(ctx context.Context) (map[string]any, error) {
				return nil, a.api.DeleteWorkerSecret(ctx, a.ID, name, secretName)
			},
		})
	}
	return nil, fmt.Errorf("%w: secret %s", ErrNotAttached, secretName)
}

func servedBy(d cloudflare.WorkerDeployment) []any {
	out := make([]any, 0, len(d.Versions))
	for _, v := range d.Versions {
		out = append(out, map[string]any{"version_id": v.VersionID, "percentage": v.Percentage})
	}
	return out
}

// RollbackWorker serves one of the Worker's earlier versions at 100% of
// the traffic, by creating a deployment of it.
func (a *Account) RollbackWorker(ctx context.Context, actor Actor, name, versionID string) (*OperationResult, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return nil, &cloudflare.FieldError{Field: "version_id", Message: "is required"}
	}
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	versions, err := a.api.ListWorkerVersions(ctx, a.ID, name, versionListSize)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's versions", workersReadPermission)
	}
	var found bool
	for _, v := range versions {
		found = found || v.ID == versionID
	}
	if !found {
		return nil, fmt.Errorf("%w: version %s", ErrDeploymentNotFound, versionID)
	}
	deployments, err := a.api.ListWorkerDeployments(ctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Worker's deployments", workersReadPermission)
	}
	before := map[string]any{}
	if len(deployments) > 0 {
		cur := deployments[0]
		before = map[string]any{"deployment_id": cur.ID, "versions": servedBy(cur)}
		if len(cur.Versions) == 1 && cur.Versions[0].VersionID == versionID && cur.Versions[0].Percentage >= 100 {
			return noChange(), nil
		}
	}
	message := truncate("Rolled back in Neo Box by "+actor.Name, 100)
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "rollback", target: versionID,
		before:    before,
		requested: map[string]any{"version_id": versionID, "message": message},
		what:      "deploy versions of this Worker", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			d, err := a.api.DeployWorkerVersion(ctx, a.ID, name, versionID, message)
			if err != nil {
				return nil, err
			}
			return map[string]any{"deployment_id": d.ID, "versions": servedBy(*d)}, nil
		},
	})
}

// DeleteWorker deletes the Worker script. Cloudflare refuses while other
// Workers depend on it.
func (a *Account) DeleteWorker(ctx context.Context, actor Actor, name string) (*OperationResult, error) {
	if _, err := a.requireWorker(ctx, name); err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindWorker, resource: name, action: "delete",
		before: map[string]any{"name": name},
		what:   "delete this Worker", permission: workersEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return nil, a.api.DeleteWorkerScript(ctx, a.ID, name)
		},
	})
}

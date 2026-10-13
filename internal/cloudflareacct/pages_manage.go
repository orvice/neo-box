package cloudflareacct

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

const (
	pagesReadPermission = "Cloudflare Pages Read"
	pagesEditPermission = "Cloudflare Pages Edit"
)

// Environments of a Pages project.
const (
	PagesProduction = "production"
	PagesPreview    = "preview"
)

// PagesDetail is one Pages project with its settings and custom domains.
// Secret environment variables carry no value.
type PagesDetail struct {
	Project *cloudflare.PagesProject
	// Domains are the custom domains with their status; nil when they
	// could not be read (see Issues).
	Domains []cloudflare.PagesDomain
	Issues  []AddressIssue
}

func (a *Account) pagesError(ctx context.Context, err error, what, permission string) error {
	if cloudflare.IsNotFound(err) {
		return ErrPagesProjectNotFound
	}
	return a.classify(ctx, err, what, permission)
}

// withoutSecrets drops the value of every secret environment variable, so
// that none can leave this package whatever Cloudflare sent.
func withoutSecrets(p *cloudflare.PagesProject) {
	for _, env := range []*cloudflare.PagesEnvironmentConfig{&p.DeploymentConfigs.Production, &p.DeploymentConfigs.Preview} {
		clean := make(map[string]*cloudflare.PagesEnvVar, len(env.EnvVars))
		for k, v := range env.EnvVars {
			if v == nil {
				continue
			}
			cp := *v
			if cp.Type != cloudflare.PagesEnvPlainText {
				cp.Value = ""
			}
			clean[k] = &cp
		}
		env.EnvVars = clean
	}
}

// PagesProjectDetail reads one Pages project live.
func (a *Account) PagesProjectDetail(ctx context.Context, name string) (*PagesDetail, error) {
	p, err := a.api.GetPagesProject(ctx, a.ID, name)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	withoutSecrets(p)
	d := &PagesDetail{Project: p}
	var issues issueSet
	domains, err := a.api.ListPagesDomains(ctx, a.ID, name)
	if err != nil {
		issues.add("Pages custom domains", err)
	} else {
		d.Domains = domains
		sort.Slice(d.Domains, func(i, j int) bool { return d.Domains[i].Name < d.Domains[j].Name })
	}
	d.Issues = issues.list
	return d, nil
}

// PagesDeployments returns one page of a project's deployments, newest
// first. environment is "", "production" or "preview".
func (a *Account) PagesDeployments(ctx context.Context, project, environment string, page, perPage int) ([]cloudflare.PagesDeployment, *cloudflare.PageInfo, error) {
	if environment != "" && environment != PagesProduction && environment != PagesPreview {
		return nil, nil, &cloudflare.FieldError{Field: "environment", Message: "must be production or preview"}
	}
	list, info, err := a.api.ListPagesDeployments(ctx, a.ID, project, environment, page, perPage)
	if err != nil {
		return nil, nil, a.pagesError(ctx, err, "read this project's deployments", pagesReadPermission)
	}
	return list, info, nil
}

func (a *Account) pagesLock(project string) func() {
	return a.m.lockResource("pages/" + a.ID + "/" + project)
}

// CreatePagesProject creates an empty Direct Upload project.
func (a *Account) CreatePagesProject(ctx context.Context, actor Actor, name, productionBranch string) (*OperationResult, error) {
	name = strings.TrimSpace(name)
	productionBranch = strings.TrimSpace(productionBranch)
	if productionBranch == "" {
		productionBranch = "main"
	}
	if err := cloudflare.ValidatePagesProjectName(name); err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateBranch(productionBranch); err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: name, action: "create_project",
		requested: map[string]any{"name": name, "production_branch": productionBranch},
		what:      "create Pages projects", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			p, err := a.api.CreatePagesProject(ctx, a.ID, name, productionBranch)
			if err != nil {
				return nil, err
			}
			return map[string]any{"name": p.Name, "subdomain": p.Subdomain, "production_branch": p.ProductionBranch}, nil
		},
	})
}

// PagesProjectUpdate is a partial change to a project's settings: nil
// fields stay as they are.
type PagesProjectUpdate struct {
	ProductionBranch *string
	BuildCommand     *string
	DestinationDir   *string
	RootDir          *string
}

func (u PagesProjectUpdate) empty() bool {
	return u.ProductionBranch == nil && u.BuildCommand == nil && u.DestinationDir == nil && u.RootDir == nil
}

func settingsOf(p *cloudflare.PagesProject) map[string]any {
	return map[string]any{
		"production_branch": p.ProductionBranch,
		"build_command":     p.BuildConfig.BuildCommand,
		"destination_dir":   p.BuildConfig.DestinationDir,
		"root_dir":          p.BuildConfig.RootDir,
	}
}

// UpdatePagesProject changes a project's production branch and build
// settings. The production branch of a Git project belongs to its
// repository's settings and is refused here.
func (a *Account) UpdatePagesProject(ctx context.Context, actor Actor, name string, in PagesProjectUpdate) (*OperationResult, error) {
	if in.empty() {
		return nil, &cloudflare.FieldError{Field: "settings", Message: "nothing to change"}
	}
	if in.ProductionBranch != nil {
		b := strings.TrimSpace(*in.ProductionBranch)
		in.ProductionBranch = &b
		if err := cloudflare.ValidateBranch(b); err != nil {
			return nil, err
		}
	}
	defer a.pagesLock(name)()
	cur, err := a.api.GetPagesProject(ctx, a.ID, name)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	if in.ProductionBranch != nil && cur.Source != nil && *in.ProductionBranch != cur.ProductionBranch {
		return nil, &cloudflare.FieldError{Field: "production_branch", Message: "is managed by the connected Git repository; change it in the Cloudflare dashboard"}
	}
	// A partial build_config may reset what it leaves out, so the current
	// settings go along with the change.
	bc := cur.BuildConfig
	next := settingsOf(cur)
	patch := map[string]any{}
	if in.ProductionBranch != nil {
		patch["production_branch"] = *in.ProductionBranch
		next["production_branch"] = *in.ProductionBranch
	}
	if in.BuildCommand != nil {
		bc.BuildCommand, next["build_command"] = *in.BuildCommand, *in.BuildCommand
	}
	if in.DestinationDir != nil {
		bc.DestinationDir, next["destination_dir"] = *in.DestinationDir, *in.DestinationDir
	}
	if in.RootDir != nil {
		bc.RootDir, next["root_dir"] = *in.RootDir, *in.RootDir
	}
	before := settingsOf(cur)
	same := true
	for k, v := range next {
		same = same && before[k] == v
	}
	if same {
		return noChange(), nil
	}
	patch["build_config"] = bc
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: name, action: "update_settings",
		before: before, requested: next,
		what: "change this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			p, err := a.api.EditPagesProject(ctx, a.ID, name, patch)
			if err != nil {
				return nil, err
			}
			return settingsOf(p), nil
		},
	})
}

// DeletePagesProject deletes the project and all its deployments.
func (a *Account) DeletePagesProject(ctx context.Context, actor Actor, name string) (*OperationResult, error) {
	p, err := a.api.GetPagesProject(ctx, a.ID, name)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: name, action: "delete_project",
		before: map[string]any{"name": name, "domains": toAny(p.Domains)},
		what:   "delete this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return nil, a.api.DeletePagesProject(ctx, a.ID, name)
		},
	})
}

func (a *Account) pagesDeployment(ctx context.Context, project, id string) (*cloudflare.PagesDeployment, error) {
	d, err := a.api.GetPagesDeployment(ctx, a.ID, project, id)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			// Cloudflare answers the same for a missing project and a
			// missing deployment; tell them apart.
			if _, perr := a.api.GetPagesProject(ctx, a.ID, project); perr != nil && cloudflare.IsNotFound(perr) {
				return nil, ErrPagesProjectNotFound
			}
			return nil, fmt.Errorf("%w: deployment %s", ErrDeploymentNotFound, id)
		}
		return nil, a.classify(ctx, err, "read this deployment", pagesReadPermission)
	}
	return d, nil
}

func deploymentState(d *cloudflare.PagesDeployment) map[string]any {
	return map[string]any{
		"id": d.ID, "environment": d.Environment, "branch": d.Trigger.Metadata.Branch,
		"stage": d.LatestStage.Name, "status": d.LatestStage.Status,
	}
}

// RetryPagesDeployment builds a deployment again as a new deployment.
func (a *Account) RetryPagesDeployment(ctx context.Context, actor Actor, project, id string) (*OperationResult, error) {
	d, err := a.pagesDeployment(ctx, project, id)
	if err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "retry_deployment", target: d.ID,
		before: deploymentState(d),
		what:   "retry deployments of this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			nd, err := a.api.RetryPagesDeployment(ctx, a.ID, project, d.ID)
			if err != nil {
				return nil, err
			}
			return deploymentState(nd), nil
		},
	})
}

// RollbackPagesDeployment makes an earlier successful production
// deployment the live one.
func (a *Account) RollbackPagesDeployment(ctx context.Context, actor Actor, project, id string) (*OperationResult, error) {
	d, err := a.pagesDeployment(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if d.Environment != PagesProduction || !d.Succeeded() {
		return nil, &cloudflare.FieldError{Field: "deployment", Message: "must be a successful production deployment to roll back to"}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "rollback_deployment", target: d.ID,
		before: deploymentState(d),
		what:   "roll back this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			nd, err := a.api.RollbackPagesDeployment(ctx, a.ID, project, d.ID)
			if err != nil {
				return nil, err
			}
			return deploymentState(nd), nil
		},
	})
}

// DeletePagesDeployment deletes one deployment. Cloudflare refuses to
// delete the live production deployment.
func (a *Account) DeletePagesDeployment(ctx context.Context, actor Actor, project, id string) (*OperationResult, error) {
	d, err := a.pagesDeployment(ctx, project, id)
	if err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "delete_deployment", target: d.ID,
		before: deploymentState(d),
		what:   "delete deployments of this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return nil, a.api.DeletePagesDeployment(ctx, a.ID, project, d.ID)
		},
	})
}

// PagesDomainResult is a custom domain added to a project, and what became
// of its DNS record when one was asked for.
type PagesDomainResult struct {
	*OperationResult
	// DNS is nil when no record was asked for.
	DNS *RecordResult
}

// AddPagesDomain adds a custom domain to the project. With createDNS it
// also adds a proxied CNAME to the project's pages.dev address in the
// Account's Zone for the hostname, unless the hostname already has a
// record, which is never replaced.
func (a *Account) AddPagesDomain(ctx context.Context, actor Actor, project, hostname string, createDNS bool) (*PagesDomainResult, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if err := cloudflare.ValidateDomainName(hostname); err != nil {
		return nil, err
	}
	p, err := a.api.GetPagesProject(ctx, a.ID, project)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	for _, d := range p.Domains {
		if strings.EqualFold(d, hostname) {
			return nil, &cloudflare.FieldError{Field: "hostname", Message: "is already a domain of this project"}
		}
	}
	res, err := a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "add_domain", target: hostname,
		requested: map[string]any{"hostname": hostname, "create_dns": createDNS},
		what:      "add custom domains to this Pages project", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			d, err := a.api.AddPagesDomain(ctx, a.ID, project, hostname)
			if err != nil {
				return nil, err
			}
			return map[string]any{"hostname": d.Name, "status": d.Status}, nil
		},
	})
	if err != nil {
		return nil, err
	}
	out := &PagesDomainResult{OperationResult: res}
	if createDNS {
		target := p.Subdomain
		if target == "" {
			target = project + ".pages.dev"
		}
		dns := a.pointHostnameAt(ctx, actor, hostname, target, "this project")
		out.DNS = &dns
	}
	return out, nil
}

// DeletePagesDomain removes a custom domain from the project. Its DNS
// record is left alone.
func (a *Account) DeletePagesDomain(ctx context.Context, actor Actor, project, hostname string) (*OperationResult, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	domains, err := a.api.ListPagesDomains(ctx, a.ID, project)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this project's custom domains", pagesReadPermission)
	}
	for _, d := range domains {
		if !strings.EqualFold(d.Name, hostname) || strings.HasSuffix(d.Name, ".pages.dev") {
			continue
		}
		return a.operate(ctx, actor, resourceChange{
			kind: cfrepo.KindPages, resource: project, action: "delete_domain", target: d.Name,
			before: map[string]any{"hostname": d.Name, "status": d.Status},
			what:   "remove custom domains from this Pages project", permission: pagesEditPermission,
			send: func(ctx context.Context) (map[string]any, error) {
				return nil, a.api.DeletePagesDomain(ctx, a.ID, project, d.Name)
			},
		})
	}
	return nil, fmt.Errorf("%w: custom domain %s", ErrNotAttached, hostname)
}

func validPagesEnv(env string) error {
	if env != PagesProduction && env != PagesPreview {
		return &cloudflare.FieldError{Field: "environment", Message: "must be production or preview"}
	}
	return nil
}

func envPatch(env, name string, v any) map[string]any {
	return map[string]any{"deployment_configs": map[string]any{
		env: map[string]any{"env_vars": map[string]any{name: v}},
	}}
}

// SetPagesEnvVar sets an environment variable of one environment, as plain
// text or as a secret. Values reach Cloudflare only; neither the log nor
// any result holds one.
func (a *Account) SetPagesEnvVar(ctx context.Context, actor Actor, project, environment, name, value string, secret bool) (*OperationResult, error) {
	if err := validPagesEnv(environment); err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateBindingName(name); err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateSecretValue(value); err != nil {
		return nil, err
	}
	typ := cloudflare.PagesEnvPlainText
	if secret {
		typ = cloudflare.PagesEnvSecret
	}
	defer a.pagesLock(project)()
	cur, err := a.api.GetPagesProject(ctx, a.ID, project)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	before := map[string]any{"exists": false}
	if old := cur.DeploymentConfigs.Environment(environment).EnvVars[name]; old != nil {
		before = map[string]any{"exists": true, "type": old.Type}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "set_env_var", target: environment + ":" + name,
		before:    before,
		requested: map[string]any{"environment": environment, "name": name, "type": typ},
		what:      "change this Pages project's environment variables", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			_, err := a.api.EditPagesProject(ctx, a.ID, project, envPatch(environment, name, cloudflare.PagesEnvVar{Type: typ, Value: value}))
			return map[string]any{"environment": environment, "name": name, "type": typ}, err
		},
	})
}

// DeletePagesEnvVar removes an environment variable of one environment.
func (a *Account) DeletePagesEnvVar(ctx context.Context, actor Actor, project, environment, name string) (*OperationResult, error) {
	if err := validPagesEnv(environment); err != nil {
		return nil, err
	}
	defer a.pagesLock(project)()
	cur, err := a.api.GetPagesProject(ctx, a.ID, project)
	if err != nil {
		return nil, a.pagesError(ctx, err, "read this Pages project", pagesReadPermission)
	}
	old := cur.DeploymentConfigs.Environment(environment).EnvVars[name]
	if old == nil {
		return nil, fmt.Errorf("%w: environment variable %s", ErrNotAttached, name)
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindPages, resource: project, action: "delete_env_var", target: environment + ":" + name,
		before: map[string]any{"environment": environment, "name": name, "type": old.Type},
		what:   "change this Pages project's environment variables", permission: pagesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			_, err := a.api.EditPagesProject(ctx, a.ID, project, envPatch(environment, name, nil))
			return nil, err
		},
	})
}

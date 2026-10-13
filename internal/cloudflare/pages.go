package cloudflare

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Pages management: projects, deployments, domains and environment
// variables. Neo Box never uploads a deployment's files.

func pagesPath(accountID, project string) string {
	return "/accounts/" + esc(accountID) + "/pages/projects/" + esc(project)
}

// PagesBuildConfig is how a project builds.
type PagesBuildConfig struct {
	BuildCaching   *bool  `json:"build_caching,omitempty"`
	BuildCommand   string `json:"build_command"`
	DestinationDir string `json:"destination_dir"`
	RootDir        string `json:"root_dir"`
}

// PagesSource is the Git repository a project builds from; nil Source means
// a Direct Upload project.
type PagesSource struct {
	Type   string `json:"type"`
	Config struct {
		Owner            string `json:"owner"`
		RepoName         string `json:"repo_name"`
		ProductionBranch string `json:"production_branch"`
	} `json:"config"`
}

// PagesEnvVar is an environment variable. Value is empty for secrets:
// Neo Box never returns or logs a secret's value.
type PagesEnvVar struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

const (
	PagesEnvPlainText = "plain_text"
	PagesEnvSecret    = "secret_text"
)

// PagesEnvironmentConfig is one environment's (production or preview)
// settings.
type PagesEnvironmentConfig struct {
	EnvVars            map[string]*PagesEnvVar `json:"env_vars"`
	CompatibilityDate  string                  `json:"compatibility_date"`
	CompatibilityFlags []string                `json:"compatibility_flags"`
}

type PagesDeploymentConfigs struct {
	Production PagesEnvironmentConfig `json:"production"`
	Preview    PagesEnvironmentConfig `json:"preview"`
}

// Environment returns "production" or "preview"'s config.
func (d PagesDeploymentConfigs) Environment(env string) PagesEnvironmentConfig {
	if env == "preview" {
		return d.Preview
	}
	return d.Production
}

func (c *Client) GetPagesProject(ctx context.Context, accountID, project string) (*PagesProject, error) {
	var out PagesProject
	if _, err := c.get(ctx, pagesPath(accountID, project), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePagesProject creates an empty Direct Upload project: no repository
// and no deployment yet.
func (c *Client) CreatePagesProject(ctx context.Context, accountID, name, productionBranch string) (*PagesProject, error) {
	body := map[string]any{"name": name, "production_branch": productionBranch}
	var out PagesProject
	if err := c.send(ctx, http.MethodPost, "/accounts/"+esc(accountID)+"/pages/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EditPagesProject sends a partial update (PATCH). Keys left out are
// unchanged; an environment variable set to nil is deleted.
func (c *Client) EditPagesProject(ctx context.Context, accountID, project string, patch map[string]any) (*PagesProject, error) {
	var out PagesProject
	if err := c.send(ctx, http.MethodPatch, pagesPath(accountID, project), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePagesProject deletes the project and its deployments.
func (c *Client) DeletePagesProject(ctx context.Context, accountID, project string) error {
	return c.send(ctx, http.MethodDelete, pagesPath(accountID, project), nil, nil)
}

// PagesStage is one build stage of a deployment.
type PagesStage struct {
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	StartedOn *time.Time `json:"started_on"`
	EndedOn   *time.Time `json:"ended_on"`
}

// PagesDeployment is one deployment of a project.
type PagesDeployment struct {
	ID          string    `json:"id"`
	ShortID     string    `json:"short_id"`
	ProjectName string    `json:"project_name"`
	URL         string    `json:"url"`
	Aliases     []string  `json:"aliases"`
	Environment string    `json:"environment"`
	CreatedOn   time.Time `json:"created_on"`
	IsSkipped   bool      `json:"is_skipped"`
	Trigger     struct {
		Type     string `json:"type"`
		Metadata struct {
			Branch        string `json:"branch"`
			CommitHash    string `json:"commit_hash"`
			CommitMessage string `json:"commit_message"`
		} `json:"metadata"`
	} `json:"deployment_trigger"`
	LatestStage PagesStage `json:"latest_stage"`
}

// Succeeded reports whether the deployment's last stage finished.
func (d PagesDeployment) Succeeded() bool {
	return d.LatestStage.Name == "deploy" && d.LatestStage.Status == "success"
}

func (c *Client) ListPagesDeployments(ctx context.Context, accountID, project, environment string, page, perPage int) ([]PagesDeployment, *PageInfo, error) {
	q := url.Values{}
	if environment != "" {
		q.Set("env", environment)
	}
	setPage(q, page, perPage)
	var out []PagesDeployment
	info, err := c.get(ctx, pagesPath(accountID, project)+"/deployments", q, &out)
	return out, info, err
}

func (c *Client) GetPagesDeployment(ctx context.Context, accountID, project, id string) (*PagesDeployment, error) {
	var out PagesDeployment
	if _, err := c.get(ctx, pagesPath(accountID, project)+"/deployments/"+esc(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RetryPagesDeployment builds the deployment again as a new deployment.
func (c *Client) RetryPagesDeployment(ctx context.Context, accountID, project, id string) (*PagesDeployment, error) {
	var out PagesDeployment
	if err := c.send(ctx, http.MethodPost, pagesPath(accountID, project)+"/deployments/"+esc(id)+"/retry", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RollbackPagesDeployment makes a successful earlier deployment the
// production one.
func (c *Client) RollbackPagesDeployment(ctx context.Context, accountID, project, id string) (*PagesDeployment, error) {
	var out PagesDeployment
	if err := c.send(ctx, http.MethodPost, pagesPath(accountID, project)+"/deployments/"+esc(id)+"/rollback", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePagesDeployment deletes one deployment. It never passes force, so
// Cloudflare refuses to delete the live production deployment.
func (c *Client) DeletePagesDeployment(ctx context.Context, accountID, project, id string) error {
	return c.send(ctx, http.MethodDelete, pagesPath(accountID, project)+"/deployments/"+esc(id), nil, nil)
}

func (c *Client) AddPagesDomain(ctx context.Context, accountID, project, hostname string) (*PagesDomain, error) {
	var out PagesDomain
	if err := c.send(ctx, http.MethodPost, pagesPath(accountID, project)+"/domains", map[string]string{"name": hostname}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeletePagesDomain(ctx context.Context, accountID, project, hostname string) error {
	return c.send(ctx, http.MethodDelete, pagesPath(accountID, project)+"/domains/"+esc(hostname), nil, nil)
}

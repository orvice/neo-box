package cloudflare

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Worker management: everything about a Worker except its code. Neo Box
// never uploads a script.

func scriptPath(accountID, script string) string {
	return "/accounts/" + esc(accountID) + "/workers/scripts/" + esc(script)
}

// WorkerVersion is one uploaded version of a Worker.
type WorkerVersion struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	// Metadata is what Cloudflare knows about who made it and when.
	Metadata struct {
		AuthorEmail string    `json:"author_email"`
		CreatedOn   time.Time `json:"created_on"`
		Source      string    `json:"source"`
	} `json:"metadata"`
}

// ListWorkerVersions returns the newest versions of a Worker, newest first.
func (c *Client) ListWorkerVersions(ctx context.Context, accountID, script string, perPage int) ([]WorkerVersion, error) {
	var res struct {
		Items []WorkerVersion `json:"items"`
	}
	q := url.Values{}
	if perPage > 0 {
		q.Set("per_page", strconv.Itoa(perPage))
	}
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/versions", q, &res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

// WorkerDeploymentVersion is one version of a deployment and its share of
// the traffic.
type WorkerDeploymentVersion struct {
	VersionID  string  `json:"version_id"`
	Percentage float64 `json:"percentage"`
}

// WorkerDeployment says which versions of a Worker serve traffic.
type WorkerDeployment struct {
	ID          string                    `json:"id"`
	CreatedOn   time.Time                 `json:"created_on"`
	Source      string                    `json:"source"`
	Strategy    string                    `json:"strategy"`
	AuthorEmail string                    `json:"author_email"`
	Versions    []WorkerDeploymentVersion `json:"versions"`
	Annotations map[string]any            `json:"annotations"`
}

// Message is the deployment's message, "" when it has none.
func (d WorkerDeployment) Message() string {
	s, _ := d.Annotations["workers/message"].(string)
	return s
}

// ListWorkerDeployments returns a Worker's recent deployments, newest
// first; the first one is serving.
func (c *Client) ListWorkerDeployments(ctx context.Context, accountID, script string) ([]WorkerDeployment, error) {
	var res struct {
		Deployments []WorkerDeployment `json:"deployments"`
	}
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/deployments", nil, &res); err != nil {
		return nil, err
	}
	return res.Deployments, nil
}

// DeployWorkerVersion serves versionID at 100% of the traffic. It is how a
// rollback is made.
func (c *Client) DeployWorkerVersion(ctx context.Context, accountID, script, versionID, message string) (*WorkerDeployment, error) {
	body := map[string]any{
		"strategy": "percentage",
		"versions": []WorkerDeploymentVersion{{VersionID: versionID, Percentage: 100}},
	}
	if message != "" {
		body["annotations"] = map[string]string{"workers/message": message}
	}
	var out WorkerDeployment
	if err := c.send(ctx, http.MethodPost, scriptPath(accountID, script)+"/deployments", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkersDevState is whether a Worker is served on workers.dev.
type WorkersDevState struct {
	Enabled         bool `json:"enabled"`
	PreviewsEnabled bool `json:"previews_enabled"`
}

func (c *Client) GetWorkersDev(ctx context.Context, accountID, script string) (*WorkersDevState, error) {
	var out WorkersDevState
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/subdomain", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetWorkersDev sets both workers.dev switches. Cloudflare takes them
// together, so callers pass the previews value they just read.
func (c *Client) SetWorkersDev(ctx context.Context, accountID, script string, s WorkersDevState) (*WorkersDevState, error) {
	var out WorkersDevState
	if err := c.send(ctx, http.MethodPost, scriptPath(accountID, script)+"/subdomain", s, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AttachWorkerDomain points hostname at the Worker. Cloudflare re-points a
// hostname that already belongs to another Worker, so callers check first.
func (c *Client) AttachWorkerDomain(ctx context.Context, accountID, script, hostname, zoneID, zoneName string) (*WorkerDomain, error) {
	body := map[string]any{
		"hostname": hostname, "service": script, "environment": "production",
		"zone_id": zoneID, "zone_name": zoneName,
	}
	var out WorkerDomain
	if err := c.send(ctx, http.MethodPut, "/accounts/"+esc(accountID)+"/workers/domains", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DetachWorkerDomain(ctx context.Context, accountID, domainID string) error {
	return c.send(ctx, http.MethodDelete, "/accounts/"+esc(accountID)+"/workers/domains/"+esc(domainID), nil, nil)
}

func (c *Client) CreateWorkerRoute(ctx context.Context, zoneID, pattern, script string) (*WorkerRoute, error) {
	var out WorkerRoute
	body := map[string]string{"pattern": pattern, "script": script}
	if err := c.send(ctx, http.MethodPost, "/zones/"+esc(zoneID)+"/workers/routes", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteWorkerRoute(ctx context.Context, zoneID, routeID string) error {
	return c.send(ctx, http.MethodDelete, "/zones/"+esc(zoneID)+"/workers/routes/"+esc(routeID), nil, nil)
}

// WorkerSchedule is one cron trigger.
type WorkerSchedule struct {
	Cron       string    `json:"cron"`
	CreatedOn  time.Time `json:"created_on"`
	ModifiedOn time.Time `json:"modified_on"`
}

func (c *Client) ListWorkerSchedules(ctx context.Context, accountID, script string) ([]WorkerSchedule, error) {
	var res struct {
		Schedules []WorkerSchedule `json:"schedules"`
	}
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/schedules", nil, &res); err != nil {
		return nil, err
	}
	return res.Schedules, nil
}

// PutWorkerSchedules replaces the Worker's cron triggers with crons.
func (c *Client) PutWorkerSchedules(ctx context.Context, accountID, script string, crons []string) ([]WorkerSchedule, error) {
	body := make([]map[string]string, 0, len(crons))
	for _, cr := range crons {
		body = append(body, map[string]string{"cron": cr})
	}
	var res struct {
		Schedules []WorkerSchedule `json:"schedules"`
	}
	if err := c.send(ctx, http.MethodPut, scriptPath(accountID, script)+"/schedules", body, &res); err != nil {
		return nil, err
	}
	return res.Schedules, nil
}

// WorkerSecret is a secret binding: its name and type, never its value.
type WorkerSecret struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (c *Client) ListWorkerSecrets(ctx context.Context, accountID, script string) ([]WorkerSecret, error) {
	var out []WorkerSecret
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/secrets", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PutWorkerSecret sets a secret, creating a new Worker version. The value
// is sent once and kept nowhere.
func (c *Client) PutWorkerSecret(ctx context.Context, accountID, script, name, value string) error {
	body := map[string]string{"name": name, "text": value, "type": "secret_text"}
	return c.send(ctx, http.MethodPut, scriptPath(accountID, script)+"/secrets", body, nil)
}

func (c *Client) DeleteWorkerSecret(ctx context.Context, accountID, script, name string) error {
	return c.send(ctx, http.MethodDelete, scriptPath(accountID, script)+"/secrets/"+esc(name), nil, nil)
}

// WorkerBinding is a binding of the Worker: its name and type.
type WorkerBinding struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// WorkerSettings is the part of a Worker's settings Neo Box shows.
type WorkerSettings struct {
	CompatibilityDate  string          `json:"compatibility_date"`
	CompatibilityFlags []string        `json:"compatibility_flags"`
	UsageModel         string          `json:"usage_model"`
	Bindings           []WorkerBinding `json:"bindings"`
}

func (c *Client) GetWorkerSettings(ctx context.Context, accountID, script string) (*WorkerSettings, error) {
	var out WorkerSettings
	if _, err := c.get(ctx, scriptPath(accountID, script)+"/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWorkerScript deletes the Worker. It never passes force, so
// Cloudflare refuses while another Worker depends on it.
func (c *Client) DeleteWorkerScript(ctx context.Context, accountID, script string) error {
	return c.send(ctx, http.MethodDelete, scriptPath(accountID, script), nil, nil)
}

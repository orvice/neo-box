package nocodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ListHooks returns every webhook of a table, verbatim from v2 meta.
// The notification string can contain secrets; snapshots keep it as is.
func (c *Client) ListHooks(ctx context.Context, tableID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/tables/"+url.PathEscape(tableID)+"/hooks")
}

// CreateHook creates a webhook and returns its ID. Restore callers must
// supply active: false, so the new hook can't fire until enabled by a user.
func (c *Client) CreateHook(ctx context.Context, tableID string, body any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/tables/"+url.PathEscape(tableID)+"/hooks", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("nocodb: create hook in %s: no id in response", tableID)
	}
	return out.ID, nil
}

// CreateHookFilter creates a hook condition or group and returns its ID.
// fk_parent_id identifies the group a condition belongs to.
func (c *Client) CreateHookFilter(ctx context.Context, hookID string, body any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/hooks/"+url.PathEscape(hookID)+"/filters", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("nocodb: create filter in hook %s: no id in response", hookID)
	}
	return out.ID, nil
}

// ListHookFilters returns a hook's top-level conditions. Nested groups
// are read through ListFilterChildren, just like view filters.
func (c *Client) ListHookFilters(ctx context.Context, hookID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/hooks/"+url.PathEscape(hookID)+"/filters")
}

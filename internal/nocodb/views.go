package nocodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Views, their columns, sorts and filters. OSS NocoDB gates the v3 views
// API behind a licence (feature_api_view_v3), so views are read and
// written through v2 meta. v3 filters and sorts exist in OSS but drop v2
// fields such as comparison_sub_op, so v2 is used for those too. Payloads
// are kept verbatim.

// View types as v2 numbers them.
const (
	ViewTypeForm     = 1
	ViewTypeGallery  = 2
	ViewTypeGrid     = 3
	ViewTypeKanban   = 4
	ViewTypeMap      = 5
	ViewTypeCalendar = 6
)

// ViewKind is the v2 path segment of a view type ("grids", "forms", ...),
// or "" for a type v2 can't create.
func ViewKind(viewType int) string {
	switch viewType {
	case ViewTypeForm:
		return "forms"
	case ViewTypeGallery:
		return "galleries"
	case ViewTypeGrid:
		return "grids"
	case ViewTypeKanban:
		return "kanbans"
	case ViewTypeMap:
		return "maps"
	case ViewTypeCalendar:
		return "calendars"
	}
	return ""
}

func (c *Client) getList(ctx context.Context, path string) ([]json.RawMessage, error) {
	var out struct {
		List []json.RawMessage `json:"list"`
	}
	if err := c.getJSON(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// ListViews returns every view of a table, each with its type-specific
// settings under "view".
func (c *Client) ListViews(ctx context.Context, tableID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/tables/"+url.PathEscape(tableID)+"/views")
}

// ListViewColumns returns a view's column settings: one row per field,
// with the settings of the view's type (width for a grid, label for a
// form, ...).
func (c *Client) ListViewColumns(ctx context.Context, viewID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/columns")
}

// ListViewSorts returns a view's sorts in order.
func (c *Client) ListViewSorts(ctx context.Context, viewID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/sorts")
}

// ListViewFilters returns a view's top-level filters; groups' members come
// from ListFilterChildren.
func (c *Client) ListViewFilters(ctx context.Context, viewID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/filters")
}

// ListFilterChildren returns the members of a filter group.
func (c *Client) ListFilterChildren(ctx context.Context, filterID string) ([]json.RawMessage, error) {
	return c.getList(ctx, "/api/v2/meta/filters/"+url.PathEscape(filterID)+"/children")
}

// CreateView creates a view of the given type (see ViewKind) and returns
// its ID.
func (c *Client) CreateView(ctx context.Context, tableID string, viewType int, body any) (string, error) {
	kind := ViewKind(viewType)
	if kind == "" {
		return "", fmt.Errorf("nocodb: view type %d can't be created", viewType)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/tables/"+url.PathEscape(tableID)+"/"+kind, body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("nocodb: create %s view: no id in response", kind)
	}
	return out.ID, nil
}

// UpdateView patches settings every view has: title, description,
// lock_type, meta, show_system_fields.
func (c *Client) UpdateView(ctx context.Context, viewID string, patch any) error {
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/meta/views/"+url.PathEscape(viewID), patch, nil)
}

// UpdateViewSettings patches a view's type-specific settings, e.g. a
// form's heading or a kanban's stacks.
func (c *Client) UpdateViewSettings(ctx context.Context, viewType int, viewID string, patch any) error {
	kind := ViewKind(viewType)
	if kind == "" {
		return fmt.Errorf("nocodb: view type %d has no settings", viewType)
	}
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/meta/"+kind+"/"+url.PathEscape(viewID), patch, nil)
}

// UpdateViewColumn patches a view column's show and order.
func (c *Client) UpdateViewColumn(ctx context.Context, viewID, columnID string, patch any) error {
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/columns/"+url.PathEscape(columnID), patch, nil)
}

// UpdateGridColumn patches a grid column's width, grouping and
// aggregation.
func (c *Client) UpdateGridColumn(ctx context.Context, columnID string, patch any) error {
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/meta/grid-columns/"+url.PathEscape(columnID), patch, nil)
}

// UpdateFormColumn patches a form field's label, help, description and
// required flag.
func (c *Client) UpdateFormColumn(ctx context.Context, columnID string, patch any) error {
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/meta/form-columns/"+url.PathEscape(columnID), patch, nil)
}

// CreateSort adds a sort after a view's existing ones.
func (c *Client) CreateSort(ctx context.Context, viewID string, body any) error {
	return c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/sorts", body, nil)
}

// CreateFilter adds a filter or filter group (fk_parent_id puts it in a
// group) and returns its ID.
func (c *Client) CreateFilter(ctx context.Context, viewID string, body any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/views/"+url.PathEscape(viewID)+"/filters", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("nocodb: create filter in %s: no id in response", viewID)
	}
	return out.ID, nil
}

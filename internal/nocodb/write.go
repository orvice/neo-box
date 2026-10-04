package nocodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The write half of the client, used to restore snapshots. Bases are
// created through v2 (OSS has no workspaces); tables, fields, records and
// links through v3.

// CreateBase creates an empty Base and returns its ID. NocoDB creates no
// tables in it.
func (c *Client) CreateBase(ctx context.Context, title, description string) (string, error) {
	in := map[string]string{"title": title}
	if description != "" {
		in["description"] = description
	}
	var out Base
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v2/meta/bases", in, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("nocodb: create base %q: no id in response", title)
	}
	return out.ID, nil
}

// DeleteBase deletes a Base and everything in it.
func (c *Client) DeleteBase(ctx context.Context, baseID string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v2/meta/bases/"+url.PathEscape(baseID), struct{}{}, nil)
}

// CreateTable creates a table from a v3 table definition ({title, fields,
// ...}) and returns its v3 schema.
func (c *Client) CreateTable(ctx context.Context, baseID string, def any) (*Table, error) {
	var raw json.RawMessage
	path := "/api/v3/meta/bases/" + url.PathEscape(baseID) + "/tables"
	if err := c.sendJSON(ctx, http.MethodPost, path, def, &raw); err != nil {
		return nil, err
	}
	return parseTable(raw)
}

// UpdateTable patches a table, e.g. {"display_field_id": ...}.
func (c *Client) UpdateTable(ctx context.Context, baseID, tableID string, patch any) error {
	path := "/api/v3/meta/bases/" + url.PathEscape(baseID) + "/tables/" + url.PathEscape(tableID)
	return c.sendJSON(ctx, http.MethodPatch, path, patch, nil)
}

// CreateField adds a field from a v3 field definition and returns it.
func (c *Client) CreateField(ctx context.Context, baseID, tableID string, def any) (*Field, error) {
	var out Field
	path := "/api/v3/meta/bases/" + url.PathEscape(baseID) + "/tables/" + url.PathEscape(tableID) + "/fields"
	if err := c.sendJSON(ctx, http.MethodPost, path, def, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, fmt.Errorf("nocodb: create field in %s: no id in response", tableID)
	}
	return &out, nil
}

// UpdateField patches a field, e.g. {"title": ...}.
func (c *Client) UpdateField(ctx context.Context, baseID, fieldID string, patch any) error {
	path := "/api/v3/meta/bases/" + url.PathEscape(baseID) + "/fields/" + url.PathEscape(fieldID)
	return c.sendJSON(ctx, http.MethodPatch, path, patch, nil)
}

// InsertRecords inserts records (field title -> value) and returns their new
// IDs in insertion order. NocoDB caps a request at 10 records by default.
func (c *Client) InsertRecords(ctx context.Context, baseID, tableID string, records []map[string]json.RawMessage) ([]json.RawMessage, error) {
	in := make([]map[string]any, len(records))
	for i, r := range records {
		in[i] = map[string]any{"fields": r}
	}
	var out struct {
		Records []struct {
			ID json.RawMessage `json:"id"`
		} `json:"records"`
	}
	path := "/api/v3/data/" + url.PathEscape(baseID) + "/" + url.PathEscape(tableID) + "/records"
	if err := c.sendJSON(ctx, http.MethodPost, path, in, &out); err != nil {
		return nil, err
	}
	if len(out.Records) != len(records) {
		return nil, fmt.Errorf("nocodb: insert into %s: sent %d records, got %d ids", tableID, len(records), len(out.Records))
	}
	ids := make([]json.RawMessage, len(out.Records))
	for i, r := range out.Records {
		ids[i] = r.ID
	}
	return ids, nil
}

// LinkRecords links recordID to each of ids through the link field. Links
// are added to any existing ones; NocoDB rejects duplicate ids.
func (c *Client) LinkRecords(ctx context.Context, baseID, tableID, fieldID, recordID string, ids []json.RawMessage) error {
	in := make([]map[string]json.RawMessage, len(ids))
	for i, id := range ids {
		in[i] = map[string]json.RawMessage{"id": id}
	}
	path := "/api/v3/data/" + url.PathEscape(baseID) + "/" + url.PathEscape(tableID) +
		"/links/" + url.PathEscape(fieldID) + "/" + url.PathEscape(recordID)
	return c.sendJSON(ctx, http.MethodPost, path, in, nil)
}

// ListBaseUserEmails returns the emails of the Base's members.
func (c *Client) ListBaseUserEmails(ctx context.Context, baseID string) ([]string, error) {
	var out struct {
		Users struct {
			List []struct {
				Email string `json:"email"`
			} `json:"list"`
		} `json:"users"`
	}
	if err := c.getJSON(ctx, "/api/v2/meta/bases/"+url.PathEscape(baseID)+"/users", nil, &out); err != nil {
		return nil, err
	}
	emails := make([]string, 0, len(out.Users.List))
	for _, u := range out.Users.List {
		if u.Email != "" {
			emails = append(emails, u.Email)
		}
	}
	return emails, nil
}

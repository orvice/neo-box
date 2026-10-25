package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The GraphQL Analytics API answers with {"data": ..., "errors": ...}, not
// with the REST envelope, and reports some failures (a token without
// access to a dataset) with status 200.
//
// API: https://developers.cloudflare.com/analytics/graphql-api/

const graphqlPath = "/graphql"

// graphqlError is one entry of a GraphQL errors array.
type graphqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// readGraphQL decodes one GraphQL response into the REST envelope shape, so
// the retry loop and the error helpers apply unchanged. GraphQL errors
// carried by a 2xx response become an APIError: "authz" (no access to the
// dataset or zone) is a 403, any other a 400.
func (c *Client) readGraphQL(resp *http.Response, method, path string) (*envelope, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: read %s %s: %w", method, path, err)
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphqlError  `json:"errors"`
	}
	decodeErr := json.Unmarshal(body, &res)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if ok && decodeErr != nil {
		return nil, fmt.Errorf("cloudflare: decode %s %s: %w", method, path, decodeErr)
	}
	if ok && len(res.Errors) == 0 {
		return &envelope{Success: true, Result: res.Data}, nil
	}
	apiErr := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path}
	if ok {
		apiErr.StatusCode = http.StatusBadRequest
	}
	for _, e := range res.Errors {
		if ok && e.Extensions.Code == "authz" {
			apiErr.StatusCode = http.StatusForbidden
		}
		apiErr.Errors = append(apiErr.Errors, ErrorDetail{Message: e.Message})
	}
	if len(apiErr.Errors) == 0 {
		// Not a GraphQL error body: try the REST shape, then the raw text.
		var rest envelope
		if json.Unmarshal(body, &rest) == nil && len(rest.Errors) > 0 {
			apiErr.Errors = rest.Errors
		} else if msg := strings.TrimSpace(string(body)); msg != "" {
			if len(msg) > maxErrorBody {
				msg = msg[:maxErrorBody]
			}
			apiErr.Errors = []ErrorDetail{{Message: msg}}
		}
	}
	return nil, apiErr
}

// graphql runs one read-only query and decodes its data into out. It is
// retried like a read.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("cloudflare: encode graphql query: %w", err)
	}
	env, err := c.doWith(ctx, http.MethodPost, graphqlPath, nil, payload, false, c.readGraphQL)
	if err != nil {
		return err
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("cloudflare: decode graphql data: %w", err)
		}
	}
	return nil
}

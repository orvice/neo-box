package nocodb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// Attachment files. Records hold attachment metadata; the bytes are
// downloaded separately. Writing attachments goes through v2: a v3 record
// write only accepts a new attachment by URL (NocoDB fetches it itself),
// and the v3 per-cell upload renames the file. v2 storage upload keeps the
// title, and a v2 record update sets the uploaded objects.

// Attachment is one entry of an Attachment field's value. NocoDB's own
// storage gives path and signedPath (relative to the instance root); S3
// storage gives url and signedUrl; a file added by URL has only url.
type Attachment struct {
	URL        string `json:"url,omitempty"`
	SignedURL  string `json:"signedUrl,omitempty"`
	Path       string `json:"path,omitempty"`
	SignedPath string `json:"signedPath,omitempty"`
	Title      string `json:"title,omitempty"`
	Mimetype   string `json:"mimetype,omitempty"`
	Size       int64  `json:"size,omitempty"`
}

// Source identifies the stored file: its path, else its URL. Stored files
// never change (each upload gets a new name), so the source and size
// identify the content.
func (a Attachment) Source() string {
	if a.Path != "" {
		return a.Path
	}
	return a.URL
}

// DownloadRefs lists where the file can be read from, best first: signed
// references work without the API token, and an S3 url may be private.
func (a Attachment) DownloadRefs() []string {
	var out []string
	for _, ref := range []string{a.SignedURL, a.SignedPath, a.URL, a.Path} {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

// ParseAttachments reads an Attachment field's value. Anything that isn't
// a list of attachment objects gives nil.
func ParseAttachments(v json.RawMessage) []Attachment {
	var out []Attachment
	if json.Unmarshal(v, &out) != nil {
		return nil
	}
	return out
}

// Download opens a file by a reference from DownloadRefs: an absolute URL
// or a path relative to the instance root. The API token and the rate
// limiter apply only to the instance itself, never to another host such
// as S3. Retried like other reads until the response starts.
func (c *Client) Download(ctx context.Context, ref string) (io.ReadCloser, error) {
	target, own, err := c.resolve(ref)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if own && c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		if own {
			req.Header.Set("xc-token", c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("nocodb: download %s: %w", redact(ref), err)
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.Body, nil
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			resp.Body.Close()
			apiErr := &APIError{StatusCode: resp.StatusCode, Method: http.MethodGet, Path: redact(ref), Message: errorMessage(body)}
			if !retryable(resp.StatusCode) {
				return nil, apiErr
			}
			lastErr = apiErr
			if attempt < c.maxRetries {
				if err := c.sleep(ctx, retryDelay(resp, attempt)); err != nil {
					return nil, err
				}
				continue
			}
		}
		if attempt >= c.maxRetries {
			return nil, lastErr
		}
		if err := c.sleep(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
}

// resolve turns a download reference into a URL and reports whether it is
// on the instance itself.
func (c *Client) resolve(ref string) (string, bool, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", false, fmt.Errorf("nocodb: invalid file url: %w", err)
		}
		base, _ := url.Parse(c.baseURL)
		own := strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
		return ref, own, nil
	}
	return c.baseURL + "/" + strings.TrimLeft(ref, "/"), true, nil
}

// redact drops the query string, which holds the signature of a signed URL.
func redact(ref string) string {
	if i := strings.IndexByte(ref, '?'); i >= 0 {
		return ref[:i]
	}
	return ref
}

// UploadFile is one file to upload.
type UploadFile struct {
	// Title becomes the attachment's title; NocoDB keeps it as given.
	Title    string
	Mimetype string
	Content  []byte
}

// UploadFiles stores files in NocoDB's storage and returns one attachment
// object per file, in order, ready to be set on an Attachment field with
// UpdateRecordsV2. Retried only on 429, like every write.
func (c *Client) UploadFiles(ctx context.Context, files []UploadFile) ([]json.RawMessage, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename="%s"`, escapeQuotes(f.Title)))
		ct := f.Mimetype
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	var out []json.RawMessage
	if err := c.send(ctx, http.MethodPost, "/api/v2/storage/upload", body.Bytes(), mw.FormDataContentType(), &out); err != nil {
		return nil, err
	}
	if len(out) != len(files) {
		return nil, fmt.Errorf("nocodb: upload: sent %d files, got %d", len(files), len(out))
	}
	return out, nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }

// UpdateRecordsV2 updates records through the v2 data API. Each row maps
// field titles to values and must include the primary key under its field
// title. Used to set attachments, which v3 only accepts by URL.
func (c *Client) UpdateRecordsV2(ctx context.Context, tableID string, rows []map[string]json.RawMessage) error {
	return c.sendJSON(ctx, http.MethodPatch, "/api/v2/tables/"+url.PathEscape(tableID)+"/records", rows, nil)
}

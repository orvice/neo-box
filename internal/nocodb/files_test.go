package nocodb

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachmentSourceAndRefs(t *testing.T) {
	local := Attachment{Path: "download/a.txt", SignedPath: "dltemp/x/a.txt"}
	if local.Source() != "download/a.txt" || strings.Join(local.DownloadRefs(), ",") != "dltemp/x/a.txt,download/a.txt" {
		t.Fatalf("local = %q %v", local.Source(), local.DownloadRefs())
	}
	s3 := Attachment{URL: "https://s3/b", SignedURL: "https://s3/b?sig=1"}
	if s3.Source() != "https://s3/b" || strings.Join(s3.DownloadRefs(), ",") != "https://s3/b?sig=1,https://s3/b" {
		t.Fatalf("s3 = %q %v", s3.Source(), s3.DownloadRefs())
	}
	if got := ParseAttachments(json.RawMessage(`[{"path":"p","title":"t","size":3}]`)); len(got) != 1 || got[0].Size != 3 {
		t.Fatalf("parsed = %+v", got)
	}
	if ParseAttachments(json.RawMessage(`3`)) != nil {
		t.Fatal("non-list parsed")
	}
}

func TestDownloadSendsTokenOnlyToTheInstance(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xc-token") != "" {
			t.Errorf("token sent to another host")
		}
		_, _ = w.Write([]byte("external"))
	}))
	t.Cleanup(other.Close)
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("xc-token") != "tok" {
			t.Errorf("xc-token = %q", r.Header.Get("xc-token"))
		}
		switch r.URL.Path {
		case "/dltemp/x/a.txt":
			if calls == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte("local"))
		default:
			http.NotFound(w, r)
		}
	})
	read := func(ref string) (string, error) {
		rc, err := c.Download(context.Background(), ref)
		if err != nil {
			return "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		return string(b), err
	}
	if got, err := read("dltemp/x/a.txt"); err != nil || got != "local" {
		t.Fatalf("local = %q, %v (a 502 is retried)", got, err)
	}
	if got, err := read(other.URL + "/b?sig=1"); err != nil || got != "external" {
		t.Fatalf("external = %q, %v", got, err)
	}
	_, err := read("download/missing.txt")
	if !IsNotFound(err) {
		t.Fatalf("missing = %v", err)
	}
}

func TestUploadFilesAndUpdateRecordsV2(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/storage/upload":
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			mr := multipart.NewReader(r.Body, params["boundary"])
			var out []map[string]any
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(p)
				if p.FormName() != "files" {
					t.Errorf("form name = %q", p.FormName())
				}
				out = append(out, map[string]any{"title": p.FileName(), "mimetype": p.Header.Get("Content-Type"), "size": len(body)})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/api/v2/tables/m1/records":
			if r.Method != http.MethodPatch {
				t.Errorf("method = %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != `[{"Files":[{"title":"a"}],"Id":4}]` {
				t.Errorf("body = %s", body)
			}
			_, _ = w.Write([]byte(`[{"Id":4}]`))
		default:
			http.NotFound(w, r)
		}
	})
	out, err := c.UploadFiles(context.Background(), []UploadFile{
		{Title: `report "q1".pdf`, Mimetype: "application/pdf", Content: []byte("abc")},
		{Title: "notes", Content: []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || string(out[0]) != `{"mimetype":"application/pdf","size":3,"title":"report \"q1\".pdf"}` || !strings.Contains(string(out[1]), `"mimetype":"application/octet-stream"`) {
		t.Fatalf("uploaded = %s", out)
	}
	err = c.UpdateRecordsV2(context.Background(), "m1", []map[string]json.RawMessage{
		{"Id": json.RawMessage("4"), "Files": json.RawMessage(`[{"title":"a"}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
}

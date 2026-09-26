package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	repo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/repo/connection/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

type testConfig struct {
	URL   string `json:"url"`
	Zone  string `json:"zone,omitempty"`
	Other int    `json:"other,omitempty"`
}

type testSecret struct {
	Token string `json:"token"`
}

// fakeProvider records Verify and Cleanup calls.
type fakeProvider struct {
	verifyErr  error
	cleanupErr error
	verified   []string // secret JSON of each Verify call
	cleaned    []string // connection IDs
}

func (*fakeProvider) Type() string { return "fake" }

func (p *fakeProvider) Verify(_ context.Context, _, secret json.RawMessage) error {
	p.verified = append(p.verified, string(secret))
	return p.verifyErr
}

func (p *fakeProvider) Cleanup(_ context.Context, c *repo.Connection) error {
	p.cleaned = append(p.cleaned, c.ID)
	return p.cleanupErr
}

func newService(t *testing.T) (*Service, *memory.Store, *fakeProvider) {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	r := memory.New()
	s := NewService(r, cipher)
	p := &fakeProvider{}
	s.Register(p)
	return s, r, p
}

func TestCreateVerifiesAndSeals(t *testing.T) {
	s, r, p := newService(t)
	ctx := context.Background()

	c, err := s.Create(ctx, "u1", "fake", "home", testConfig{URL: "https://x"}, testSecret{Token: "s3cret"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(p.verified) != 1 || !strings.Contains(p.verified[0], "s3cret") {
		t.Fatalf("Verify calls = %v", p.verified)
	}
	stored, _ := r.GetByID(ctx, c.ID)
	if strings.Contains(stored.SecretCiphertext, "s3cret") || stored.Status != repo.StatusOK || stored.StatusCheckedAt.IsZero() {
		t.Fatalf("unexpected stored connection: %+v", stored)
	}

	var cfg testConfig
	var sec testSecret
	if err := s.Open(stored, &cfg, &sec); err != nil || cfg.URL != "https://x" || sec.Token != "s3cret" {
		t.Fatalf("Open = %+v %+v %v", cfg, sec, err)
	}
}

func TestCreateFailures(t *testing.T) {
	s, r, p := newService(t)
	ctx := context.Background()

	p.verifyErr = errors.New("401 unauthorized")
	_, err := s.Create(ctx, "u1", "fake", "home", testConfig{}, testSecret{})
	var verr *VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("Create with failing Verify = %v, want VerifyError", err)
	}
	if list, _ := r.List(ctx, repo.Filter{}); len(list) != 0 {
		t.Fatalf("a failed Create stored %d connections", len(list))
	}

	if _, err := s.Create(ctx, "u1", "nope", "x", testConfig{}, testSecret{}); err == nil {
		t.Fatal("Create with an unknown provider: expected error")
	}

	noKey := NewService(memory.New(), nil)
	noKey.Register(&fakeProvider{})
	if _, err := noKey.Create(ctx, "u1", "fake", "x", testConfig{}, testSecret{}); !errors.Is(err, ErrNoCipher) {
		t.Fatalf("Create without cipher = %v, want ErrNoCipher", err)
	}
}

func TestUpdate(t *testing.T) {
	s, r, p := newService(t)
	ctx := context.Background()
	c, err := s.Create(ctx, "u1", "fake", "home", testConfig{URL: "https://x", Zone: "eu"}, testSecret{Token: "old"})
	if err != nil {
		t.Fatal(err)
	}
	sealed := c.SecretCiphertext
	p.verified = nil

	// jsonb hands configs back with keys reordered; that is not a change.
	c.Config = json.RawMessage(`{"zone": "eu", "url": "https://x"}`)
	if err := s.Update(ctx, c, "renamed", testConfig{URL: "https://x", Zone: "eu"}, nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if len(p.verified) != 0 || c.SecretCiphertext != sealed || c.Name != "renamed" {
		t.Fatalf("rename re-verified (%v) or changed the secret", p.verified)
	}

	// A new config is verified with the stored secret.
	if err := s.Update(ctx, c, "renamed", testConfig{URL: "https://y"}, nil); err != nil {
		t.Fatalf("config change: %v", err)
	}
	if len(p.verified) != 1 || !strings.Contains(p.verified[0], "old") || c.SecretCiphertext != sealed {
		t.Fatalf("config change: verified %v", p.verified)
	}

	// A new secret replaces the stored one.
	if err := s.Update(ctx, c, "renamed", testConfig{URL: "https://y"}, testSecret{Token: "new"}); err != nil {
		t.Fatalf("secret change: %v", err)
	}
	stored, _ := r.GetByID(ctx, c.ID)
	var sec testSecret
	if err := s.Open(stored, nil, &sec); err != nil || sec.Token != "new" {
		t.Fatalf("secret after update = %+v, %v", sec, err)
	}

	// A failing check changes nothing.
	p.verifyErr = errors.New("bad token")
	err = s.Update(ctx, c, "again", testConfig{URL: "https://y"}, testSecret{Token: "worse"})
	var verr *VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("Update with failing Verify = %v, want VerifyError", err)
	}
	if after, _ := r.GetByID(ctx, c.ID); after.Name != "renamed" || after.SecretCiphertext != stored.SecretCiphertext {
		t.Fatalf("failed Update changed the connection: %+v", after)
	}
}

func TestTestAndRecordStatus(t *testing.T) {
	s, r, p := newService(t)
	ctx := context.Background()
	c, err := s.Create(ctx, "u1", "fake", "home", testConfig{}, testSecret{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}

	p.verifyErr = errors.New("401 unauthorized")
	if err := s.Test(ctx, c); err != nil {
		t.Fatalf("Test: %v", err)
	}
	stored, _ := r.GetByID(ctx, c.ID)
	if stored.Status != repo.StatusError || stored.StatusMessage != "401 unauthorized" || c.Status != repo.StatusError {
		t.Fatalf("failed Test not recorded: stored %+v, in place %+v", stored, c)
	}

	s.RecordStatus(ctx, c.ID, nil)
	if stored, _ = r.GetByID(ctx, c.ID); stored.Status != repo.StatusOK || stored.StatusMessage != "" {
		t.Fatalf("RecordStatus(nil) not recorded: %+v", stored)
	}
	s.RecordStatus(ctx, c.ID, fmt.Errorf("sync: %w", errors.New("403")))
	if stored, _ = r.GetByID(ctx, c.ID); stored.Status != repo.StatusError || stored.StatusMessage != "sync: 403" {
		t.Fatalf("RecordStatus(err) not recorded: %+v", stored)
	}
}

func TestDeleteRunsCleanupFirst(t *testing.T) {
	s, r, p := newService(t)
	ctx := context.Background()
	c, err := s.Create(ctx, "u1", "fake", "home", testConfig{}, testSecret{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}

	p.cleanupErr = fmt.Errorf("%w: snapshot running", ErrBusy)
	if err := s.Delete(ctx, c); !errors.Is(err, ErrBusy) {
		t.Fatalf("Delete while busy = %v, want ErrBusy", err)
	}
	if _, err := r.GetByID(ctx, c.ID); err != nil {
		t.Fatalf("busy Delete removed the connection: %v", err)
	}

	p.cleanupErr = nil
	if err := s.Delete(ctx, c); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(p.cleaned) != 2 {
		t.Fatalf("Cleanup calls = %v", p.cleaned)
	}
	if _, err := r.GetByID(ctx, c.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("connection still stored after Delete: %v", err)
	}
}

// activatingProvider records Activate calls.
type activatingProvider struct {
	fakeProvider
	activated []string
}

func (p *activatingProvider) Activate(_ context.Context, c *repo.Connection) {
	p.activated = append(p.activated, c.Name)
}

func TestActivateOnCreateAndSettingsChange(t *testing.T) {
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(memory.New(), cipher)
	p := &activatingProvider{}
	s.Register(p)
	ctx := context.Background()

	c, err := s.Create(ctx, "u1", "fake", "created", testConfig{URL: "a"}, testSecret{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, c, "renamed", testConfig{URL: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, c, "new config", testConfig{URL: "b"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(p.activated) != 2 || p.activated[0] != "created" || p.activated[1] != "new config" {
		t.Fatalf("Activate calls = %v, want create and the config change only", p.activated)
	}
}

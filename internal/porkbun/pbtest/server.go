// Package pbtest provides a stateful in-memory fake Porkbun API v3 HTTP server
// for client, manager, and Connect service tests.
package pbtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Domain holds a fake Porkbun domain and its read-only sub-resources.
type Domain struct {
	Domain       string
	Status       string
	TLD          string
	CreateDate   string
	ExpireDate   string
	SecurityLock int
	WhoisPrivacy int
	AutoRenew    int
	NotLocal     int
	APIAccess    int
	Labels       []Label

	Nameservers []string
	DNSRecords  []DNSRecord
	URLForwards []URLForward
	DNSSEC      []DNSSECRecord
	GlueHosts   []GlueHost
}

type Label struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     string `json:"ttl"`
	Prio    string `json:"prio"`
	Notes   string `json:"notes"`
}

type URLForward struct {
	ID           string `json:"id"`
	Subdomain    string `json:"subdomain"`
	Location     string `json:"location"`
	Type         string `json:"type"`
	RedirectType string `json:"redirectType,omitempty"`
	IncludePath  string `json:"includePath"`
	Wildcard     string `json:"wildcard"`
}

type DNSSECRecord struct {
	KeyTag     string `json:"keyTag"`
	Alg        string `json:"alg"`
	DigestType string `json:"digestType"`
	Digest     string `json:"digest"`
}

type GlueHost struct {
	Host string   `json:"host"`
	V4   []string `json:"v4"`
	V6   []string `json:"v6"`
}

// Credential configures one API key pair recognized by the fake server.
type Credential struct {
	APIKey       string
	SecretAPIKey string
	// DeniedPaths maps endpoint path prefixes (e.g. "/dns/retrieve/") to HTTP
	// 403 INSUFFICIENT_SCOPE responses.
	DeniedPaths []string
	// LegacyAuthError sends HTTP 400 with "Invalid API key" instead of 401 when
	// invalid credentials are used.
	Sandbox bool
}

// Server is a stateful fake Porkbun v3 API server.
type Server struct {
	HTTP *httptest.Server

	mu            sync.Mutex
	creds         map[string]Credential
	domains       []*Domain
	byName        map[string]*Domain
	rateLimitOnce int
	useTupleGlue  bool
	useArrayDS    bool
	yourIP        string
}

// New starts a fake Porkbun v3 server. Call Close when done.
func New() *Server {
	s := &Server{
		creds:  make(map[string]Credential),
		byName: make(map[string]*Domain),
		yourIP: "203.0.113.10",
	}
	s.HTTP = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) Close() { s.HTTP.Close() }

func (s *Server) URL() string { return s.HTTP.URL }

func (s *Server) AddCredential(c Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds[c.APIKey] = c
}

func (s *Server) RevokeCredential(apiKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.creds, apiKey)
}

func (s *Server) AddDomain(d *Domain) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.ToLower(d.Domain)
	d.Domain = name
	if _, exists := s.byName[name]; !exists {
		s.domains = append(s.domains, d)
	}
	s.byName[name] = d
}

// FailRateLimit makes the next n requests respond with HTTP 429 + Retry-After: 0.
func (s *Server) FailRateLimit(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimitOnce = n
}

// SetTupleGlue makes /domain/getGlue return Porkbun's classic tuple shape.
func (s *Server) SetTupleGlue(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.useTupleGlue = v
}

// SetArrayDNSSEC makes /dns/getDnssecRecords return an array instead of a map.
func (s *Server) SetArrayDNSSEC(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.useArrayDS = v
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"status":  "ERROR",
			"message": "method not allowed",
		})
		return
	}

	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	secretKey := strings.TrimSpace(r.Header.Get("X-Secret-API-Key"))
	if apiKey == "" {
		apiKey, _ = body["apikey"].(string)
	}
	if secretKey == "" {
		secretKey, _ = body["secretapikey"].(string)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rateLimitOnce > 0 {
		s.rateLimitOnce--
		w.Header().Set("Retry-After", "0")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"status":       "ERROR",
			"code":         "RATE_LIMIT_EXCEEDED",
			"message":      "Rate limit exceeded",
			"ttlRemaining": 0,
		})
		return
	}

	cred, ok := s.creds[apiKey]
	if !ok || cred.SecretAPIKey != secretKey {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"status":  "ERROR",
			"code":    "INVALID_API_KEYS_001",
			"message": "Invalid API key.",
		})
		return
	}

	if cred.Sandbox || strings.HasPrefix(cred.APIKey, "pk1_sb_") {
		w.Header().Set("X-Porkbun-Sandbox", "true")
	}

	path := r.URL.Path
	for _, denied := range cred.DeniedPaths {
		if strings.HasPrefix(path, denied) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"status":  "ERROR",
				"code":    "INSUFFICIENT_SCOPE",
				"message": "API key lacks required scope for " + path,
			})
			return
		}
	}

	switch {
	case path == "/ping":
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "SUCCESS",
			"yourIp":  s.yourIP,
			"sandbox": cred.Sandbox || strings.HasPrefix(cred.APIKey, "pk1_sb_"),
		})
	case path == "/domain/listAll":
		s.handleListDomains(w, body)
	case strings.HasPrefix(path, "/domain/getNs/"):
		s.withDomain(w, strings.TrimPrefix(path, "/domain/getNs/"), func(d *Domain) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "ns": d.Nameservers})
		})
	case strings.HasPrefix(path, "/dns/retrieve/"):
		s.withDomain(w, strings.TrimPrefix(path, "/dns/retrieve/"), func(d *Domain) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "records": d.DNSRecords})
		})
	case strings.HasPrefix(path, "/domain/getUrlForwarding/"):
		s.withDomain(w, strings.TrimPrefix(path, "/domain/getUrlForwarding/"), func(d *Domain) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "forwards": d.URLForwards})
		})
	case strings.HasPrefix(path, "/dns/getDnssecRecords/"):
		s.withDomain(w, strings.TrimPrefix(path, "/dns/getDnssecRecords/"), func(d *Domain) {
			if len(d.DNSSEC) == 0 {
				writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "records": []any{}})
				return
			}
			if s.useArrayDS {
				writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "records": d.DNSSEC})
				return
			}
			byTag := make(map[string]DNSSECRecord, len(d.DNSSEC))
			for _, rec := range d.DNSSEC {
				byTag[rec.KeyTag] = rec
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "records": byTag})
		})
	case strings.HasPrefix(path, "/domain/getGlue/"):
		s.withDomain(w, strings.TrimPrefix(path, "/domain/getGlue/"), func(d *Domain) {
			if s.useTupleGlue {
				tuples := make([][]any, 0, len(d.GlueHosts))
				for _, h := range d.GlueHosts {
					tuples = append(tuples, []any{h.Host, map[string]any{"v4": h.V4, "v6": h.V6}})
				}
				writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "hosts": tuples})
				return
			}
			hosts := d.GlueHosts
			if hosts == nil {
				hosts = []GlueHost{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "hosts": hosts})
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status":  "ERROR",
			"message": "unknown endpoint " + path,
		})
	}
}

func (s *Server) handleListDomains(w http.ResponseWriter, body map[string]any) {
	start := 0
	if raw, ok := body["start"].(string); ok {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			start = n
		}
	} else if raw, ok := body["start"].(float64); ok && raw > 0 {
		start = int(raw)
	}
	end := min(start+1000, len(s.domains))
	if start >= len(s.domains) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "domains": []any{}})
		return
	}
	out := make([]map[string]any, 0, end-start)
	for _, d := range s.domains[start:end] {
		out = append(out, map[string]any{
			"domain":       d.Domain,
			"status":       d.Status,
			"tld":          d.TLD,
			"createDate":   d.CreateDate,
			"expireDate":   d.ExpireDate,
			"securityLock": d.SecurityLock,
			"whoisPrivacy": d.WhoisPrivacy,
			"autoRenew":    d.AutoRenew,
			"notLocal":     d.NotLocal,
			"apiAccess":    d.APIAccess,
			"labels":       d.Labels,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCESS", "domains": out})
}

func (s *Server) withDomain(w http.ResponseWriter, rawDomain string, fn func(*Domain)) {
	d, ok := s.byName[strings.ToLower(rawDomain)]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "ERROR",
			"code":    "INVALID_DOMAIN",
			"message": "Invalid domain.",
		})
		return
	}
	if d.APIAccess == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "ERROR",
			"message": "Domain is not opted in to API access.",
		})
		return
	}
	fn(d)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

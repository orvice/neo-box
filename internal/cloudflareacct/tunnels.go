package cloudflareacct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"go.orx.me/apps/neo-box/internal/cloudflare"
)

const (
	tunnelReadPermission = "Cloudflare Tunnel Read"
	tunnelEditPermission = "Cloudflare Tunnel Edit"
	maxTunnelNameLen     = 256
	// tunnelType is the kind of Tunnel Neo Box manages: cloudflared.
	tunnelType = "cfd_tunnel"
)

var (
	// ErrTunnelNotFound: no such Tunnel in the connection's Account.
	ErrTunnelNotFound = errors.New("tunnel not found in this account")
	// ErrNotRemotelyManaged: the Tunnel's ingress is a local file that
	// cloudflared reads, so Cloudflare holds none to change.
	ErrNotRemotelyManaged = errors.New("this tunnel is configured by a local file, so its hostnames cannot be changed here")
)

// Tunnels returns one page of the Account's cloudflared Tunnels whose name
// contains query, by name.
func (a *Account) Tunnels(ctx context.Context, query string, page, perPage int) (*Page[cloudflare.Tunnel], error) {
	all, err := a.api.ListTunnels(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "list Tunnels", tunnelReadPermission)
	}
	live := all[:0]
	for _, t := range all {
		if a.ownsTunnel(&t) {
			live = append(live, t)
		}
	}
	live = matching(live, query, func(t cloudflare.Tunnel) string { return t.Name })
	return &Page[cloudflare.Tunnel]{Total: len(live), Items: pageOf(live, page, perPage)}, nil
}

// ownsTunnel is a live cloudflared Tunnel of this Account.
func (a *Account) ownsTunnel(t *cloudflare.Tunnel) bool {
	return t.DeletedAt == nil && (t.TunType == "" || t.TunType == tunnelType) &&
		(t.AccountTag == "" || t.AccountTag == a.ID)
}

// Tunnel reads one Tunnel of the Account; one of any other Account, or a
// deleted one, is not found.
func (a *Account) Tunnel(ctx context.Context, tunnelID string) (*cloudflare.Tunnel, error) {
	t, err := a.api.GetTunnel(ctx, a.ID, tunnelID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return nil, ErrTunnelNotFound
		}
		return nil, a.classify(ctx, err, "read this Tunnel", tunnelReadPermission)
	}
	if t.ID != tunnelID || !a.ownsTunnel(t) {
		return nil, ErrTunnelNotFound
	}
	return t, nil
}

// TunnelDetail is a Tunnel with what could be read of its connectors and
// ingress. A part that could not be read is an Issue; the rest still shows.
type TunnelDetail struct {
	Tunnel     *cloudflare.Tunnel
	Connectors []cloudflare.TunnelConnector
	// Config is the ingress; its Version is 0 for a Tunnel never
	// configured. It is nil when the Tunnel is not managed remotely or the
	// read failed.
	Config *cloudflare.TunnelConfiguration
	Issues []AddressIssue
}

// TunnelDetail reads a Tunnel, its connectors and its ingress.
func (a *Account) TunnelDetail(ctx context.Context, tunnelID string) (*TunnelDetail, error) {
	t, err := a.Tunnel(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	d := &TunnelDetail{Tunnel: t}
	var (
		wg     sync.WaitGroup
		issues issueSet
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		if d.Connectors, err = a.api.ListTunnelConnectors(ctx, a.ID, t.ID); err != nil {
			issues.add("Tunnel connectors", err)
			d.Connectors = nil
		}
	}()
	go func() {
		defer wg.Done()
		if !t.RemoteConfig {
			return
		}
		cfg, err := a.readConfig(ctx, t.ID)
		if err != nil {
			issues.add("Tunnel hostnames", err)
			return
		}
		d.Config = cfg
	}()
	wg.Wait()
	d.Issues = issues.list
	return d, nil
}

// readConfig reads a Tunnel's configuration; one never configured has an
// empty one.
func (a *Account) readConfig(ctx context.Context, tunnelID string) (*cloudflare.TunnelConfiguration, error) {
	cfg, err := a.api.GetTunnelConfiguration(ctx, a.ID, tunnelID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return &cloudflare.TunnelConfiguration{TunnelID: tunnelID}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// CreateTunnel creates a remotely managed Tunnel.
func (a *Account) CreateTunnel(ctx context.Context, name string) (*cloudflare.Tunnel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &cloudflare.FieldError{Field: "name", Message: "is required"}
	}
	if len([]rune(name)) > maxTunnelNameLen || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, &cloudflare.FieldError{Field: "name", Message: fmt.Sprintf("must be at most %d characters, without control characters", maxTunnelNameLen)}
	}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	t, err := a.api.CreateTunnel(wctx, a.ID, name)
	if err != nil {
		return nil, a.classify(ctx, err, "create Tunnels", tunnelEditPermission)
	}
	return t, nil
}

// DeleteTunnel deletes a Tunnel. Cloudflare refuses while cloudflared still
// holds connections; with cleanup, stale connections are closed first. DNS
// records that point at the Tunnel are left alone.
func (a *Account) DeleteTunnel(ctx context.Context, tunnelID string, cleanup bool) error {
	t, err := a.Tunnel(ctx, tunnelID)
	if err != nil {
		return err
	}
	unlock := a.m.lockTunnel(a.ID, t.ID)
	defer unlock()
	wctx, cancel := writeContext(ctx)
	defer cancel()
	if cleanup {
		if err := a.api.CleanupTunnelConnections(wctx, a.ID, t.ID); err != nil {
			return a.classify(ctx, err, "clean up Tunnel connections", tunnelEditPermission)
		}
	}
	if err := a.api.DeleteTunnel(wctx, a.ID, t.ID); err != nil {
		if cloudflare.IsNotFound(err) {
			return ErrTunnelNotFound
		}
		return a.classify(ctx, err, "delete Tunnels", tunnelEditPermission)
	}
	return nil
}

// CleanupTunnelConnections closes a Tunnel's stale connections. A
// connector that is still running opens new ones.
func (a *Account) CleanupTunnelConnections(ctx context.Context, tunnelID string) error {
	t, err := a.Tunnel(ctx, tunnelID)
	if err != nil {
		return err
	}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	if err := a.api.CleanupTunnelConnections(wctx, a.ID, t.ID); err != nil {
		return a.classify(ctx, err, "clean up Tunnel connections", tunnelEditPermission)
	}
	return nil
}

// TunnelToken returns the token that runs the Tunnel with cloudflared. It
// is a credential: it is shown to the caller and never stored or logged.
func (a *Account) TunnelToken(ctx context.Context, tunnelID string) (string, error) {
	t, err := a.Tunnel(ctx, tunnelID)
	if err != nil {
		return "", err
	}
	token, err := a.api.TunnelToken(ctx, a.ID, t.ID)
	if err != nil {
		return "", a.classify(ctx, err, "read the Tunnel token", tunnelReadPermission)
	}
	return token, nil
}

// --- hostnames ---

// HostnameInput is a public hostname's route as entered.
type HostnameInput struct {
	Hostname string
	Path     string
	Service  string
}

func (in HostnameInput) normalize() HostnameInput {
	in.Hostname = cloudflare.NormalizeHostname(in.Hostname)
	in.Path = strings.TrimSpace(in.Path)
	in.Service = strings.TrimSpace(in.Service)
	return in
}

func (in HostnameInput) validate(withHostname bool) error {
	if withHostname {
		if err := cloudflare.ValidateTunnelHostname(in.Hostname); err != nil {
			return err
		}
	}
	if err := cloudflare.ValidateTunnelPath(in.Path); err != nil {
		return err
	}
	return cloudflare.ValidateTunnelService(in.Service)
}

// RecordStatus is what became of the DNS record a hostname needs.
type RecordStatus string

const (
	// RecordNotRequested: the caller asked for no DNS change.
	RecordNotRequested RecordStatus = "not_requested"
	// RecordCreated: the CNAME to the Tunnel was created.
	RecordCreated RecordStatus = "created"
	// RecordExists: the CNAME to the Tunnel was already there.
	RecordExists RecordStatus = "exists"
	// RecordDeleted: the CNAME to the Tunnel was deleted.
	RecordDeleted RecordStatus = "deleted"
	// RecordKept: a delete left the record in place, with the reason.
	RecordKept RecordStatus = "kept"
	// RecordNoZone: the Account has no Zone for the hostname, so DNS has
	// to be set up elsewhere.
	RecordNoZone RecordStatus = "no_zone"
	// RecordConflict: the hostname has a record that is not a CNAME to the
	// Tunnel; it was not touched.
	RecordConflict RecordStatus = "conflict"
	// RecordFailed: the DNS change could not be made.
	RecordFailed RecordStatus = "failed"
)

// RecordResult is the outcome of the DNS part of a hostname change. The
// route itself was changed whatever it says.
type RecordResult struct {
	Status   RecordStatus
	Message  string
	RecordID string
}

// HostnameChange is a changed Tunnel ingress and what happened to DNS.
type HostnameChange struct {
	Config *cloudflare.TunnelConfiguration
	Record RecordResult
}

// AddHostname routes a hostname to a service. With createDNS it also points
// the hostname at the Tunnel with a proxied CNAME, unless it already is.
func (a *Account) AddHostname(ctx context.Context, actor Actor, tunnelID string, in HostnameInput, createDNS bool) (*HostnameChange, error) {
	in = in.normalize()
	if err := in.validate(true); err != nil {
		return nil, err
	}
	cfg, err := a.editConfig(ctx, tunnelID, func(c cloudflare.TunnelConfig) (cloudflare.TunnelConfig, error) {
		return c.WithRoute(cloudflare.IngressRule{Hostname: in.Hostname, Path: in.Path, Service: in.Service})
	})
	if err != nil {
		return nil, err
	}
	out := &HostnameChange{Config: cfg, Record: RecordResult{Status: RecordNotRequested}}
	if createDNS {
		out.Record = a.pointHostnameAtTunnel(ctx, actor, tunnelID, in.Hostname)
	}
	return out, nil
}

// UpdateHostname changes the path and service of the route for hostname and
// path. The hostname cannot change: that is a delete and an add.
func (a *Account) UpdateHostname(ctx context.Context, tunnelID, hostname, path string, in HostnameInput) (*cloudflare.TunnelConfiguration, error) {
	hostname = cloudflare.NormalizeHostname(hostname)
	path = strings.TrimSpace(path)
	in = in.normalize()
	if err := in.validate(false); err != nil {
		return nil, err
	}
	return a.editConfig(ctx, tunnelID, func(c cloudflare.TunnelConfig) (cloudflare.TunnelConfig, error) {
		return c.WithRouteChanged(hostname, path, in.Path, in.Service)
	})
}

// DeleteHostname removes the route for hostname and path. With deleteDNS it
// also deletes the hostname's CNAME to this Tunnel, when no route of the
// Tunnel still uses the hostname; a record that is anything else stays.
func (a *Account) DeleteHostname(ctx context.Context, actor Actor, tunnelID, hostname, path string, deleteDNS bool) (*HostnameChange, error) {
	hostname = cloudflare.NormalizeHostname(hostname)
	path = strings.TrimSpace(path)
	cfg, err := a.editConfig(ctx, tunnelID, func(c cloudflare.TunnelConfig) (cloudflare.TunnelConfig, error) {
		return c.WithoutRoute(hostname, path)
	})
	if err != nil {
		return nil, err
	}
	out := &HostnameChange{Config: cfg, Record: RecordResult{Status: RecordNotRequested}}
	if !deleteDNS {
		return out, nil
	}
	for _, r := range cfg.Config.Routes() {
		if strings.EqualFold(r.Hostname, hostname) {
			out.Record = RecordResult{Status: RecordKept, Message: "another route of this tunnel still uses " + hostname}
			return out, nil
		}
	}
	out.Record = a.removeHostnameRecord(ctx, actor, tunnelID, hostname)
	return out, nil
}

// editConfig reads the Tunnel's configuration, applies change, and writes
// the result back. The write replaces the whole configuration, so changes
// to one Tunnel are made one at a time and only the part change touches
// differs from what was read.
func (a *Account) editConfig(ctx context.Context, tunnelID string, change func(cloudflare.TunnelConfig) (cloudflare.TunnelConfig, error)) (*cloudflare.TunnelConfiguration, error) {
	t, err := a.Tunnel(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	if !t.RemoteConfig {
		return nil, ErrNotRemotelyManaged
	}
	unlock := a.m.lockTunnel(a.ID, t.ID)
	defer unlock()

	cur, err := a.readConfig(ctx, t.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read the Tunnel's hostnames", tunnelReadPermission)
	}
	next, err := change(cur.Config)
	if err != nil {
		return nil, err
	}
	wctx, cancel := writeContext(ctx)
	defer cancel()
	out, err := a.api.PutTunnelConfiguration(wctx, a.ID, t.ID, next)
	if err != nil {
		return nil, a.classify(ctx, err, "change the Tunnel's hostnames", tunnelEditPermission)
	}
	return out, nil
}

// --- DNS for hostnames ---

// zoneFor finds the Account's Zone that holds hostname: the one with the
// longest name the hostname ends with.
func (a *Account) zoneFor(ctx context.Context, hostname string) (*cloudflare.Zone, error) {
	labels := strings.Split(strings.TrimPrefix(hostname, "*."), ".")
	for i := 0; i+2 <= len(labels); i++ {
		name := strings.Join(labels[i:], ".")
		zones, _, err := a.api.ListZones(ctx, cloudflare.ZoneQuery{AccountID: a.ID, ExactName: name, PerPage: 5})
		if err != nil {
			return nil, err
		}
		for j := range zones {
			if zones[j].Account.ID == a.ID && strings.EqualFold(zones[j].Name, name) {
				return &zones[j], nil
			}
		}
	}
	return nil, ErrZoneNotFound
}

// hostnameRecords are the Zone's records named hostname.
func (a *Account) hostnameRecords(ctx context.Context, zoneID, hostname string) ([]cloudflare.DNSRecord, error) {
	records, _, err := a.api.ListDNSRecords(ctx, zoneID, cloudflare.RecordQuery{Search: hostname, PerPage: 100})
	if err != nil {
		return nil, err
	}
	var out []cloudflare.DNSRecord
	for _, r := range records {
		if strings.EqualFold(r.Name, hostname) {
			out = append(out, r)
		}
	}
	return out, nil
}

func pointsAtTunnel(r cloudflare.DNSRecord, tunnelID string) bool {
	return r.Type == "CNAME" && strings.EqualFold(strings.TrimSuffix(r.Content, "."), cloudflare.TunnelCNAMETarget(tunnelID))
}

// pointHostnameAtTunnel makes sure hostname has a proxied CNAME to the
// Tunnel. It never replaces a record that is something else.
func (a *Account) pointHostnameAtTunnel(ctx context.Context, actor Actor, tunnelID, hostname string) RecordResult {
	zone, err := a.zoneFor(ctx, hostname)
	switch {
	case errors.Is(err, ErrZoneNotFound):
		return RecordResult{Status: RecordNoZone, Message: fmt.Sprintf("this account has no zone for %s; point it at %s with a CNAME where its DNS is managed", hostname, cloudflare.TunnelCNAMETarget(tunnelID))}
	case err != nil:
		return RecordResult{Status: RecordFailed, Message: dnsProblem(a.classify(ctx, err, "read Zones", "Zone Read"))}
	}
	existing, err := a.hostnameRecords(ctx, zone.ID, hostname)
	if err != nil {
		return RecordResult{Status: RecordFailed, Message: dnsProblem(a.classify(ctx, err, "read DNS records", "DNS Read"))}
	}
	for _, r := range existing {
		if pointsAtTunnel(r, tunnelID) {
			return RecordResult{Status: RecordExists, RecordID: r.ID, Message: hostname + " already points at this tunnel"}
		}
	}
	if len(existing) > 0 {
		r := existing[0]
		return RecordResult{Status: RecordConflict, RecordID: r.ID, Message: fmt.Sprintf("%s already has a %s record (%s); it was left unchanged", hostname, r.Type, r.Content)}
	}
	ch, err := a.CreateRecord(ctx, actor, zone.ID, cloudflare.RecordInput{
		Type: "CNAME", Name: hostname, Content: cloudflare.TunnelCNAMETarget(tunnelID), TTL: cloudflare.TTLAuto, Proxied: true,
	})
	if err != nil {
		return RecordResult{Status: RecordFailed, Message: dnsProblem(err)}
	}
	return RecordResult{Status: RecordCreated, RecordID: ch.Record.ID}
}

// removeHostnameRecord deletes hostname's CNAME to the Tunnel, and only
// that.
func (a *Account) removeHostnameRecord(ctx context.Context, actor Actor, tunnelID, hostname string) RecordResult {
	zone, err := a.zoneFor(ctx, hostname)
	switch {
	case errors.Is(err, ErrZoneNotFound):
		return RecordResult{Status: RecordNoZone, Message: "this account has no zone for " + hostname + ", so no DNS record was changed"}
	case err != nil:
		return RecordResult{Status: RecordFailed, Message: dnsProblem(a.classify(ctx, err, "read Zones", "Zone Read"))}
	}
	existing, err := a.hostnameRecords(ctx, zone.ID, hostname)
	if err != nil {
		return RecordResult{Status: RecordFailed, Message: dnsProblem(a.classify(ctx, err, "read DNS records", "DNS Read"))}
	}
	for _, r := range existing {
		if !pointsAtTunnel(r, tunnelID) {
			continue
		}
		if _, err := a.DeleteRecord(ctx, actor, zone.ID, r.ID); err != nil {
			return RecordResult{Status: RecordFailed, RecordID: r.ID, Message: dnsProblem(err)}
		}
		return RecordResult{Status: RecordDeleted, RecordID: r.ID}
	}
	return RecordResult{Status: RecordKept, Message: hostname + " has no CNAME to this tunnel, so no DNS record was deleted"}
}

func dnsProblem(err error) string { return truncate(err.Error(), maxErrorChars) }

// --- helpers ---

func writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
}

// lockTunnel serializes changes to one Tunnel's configuration, which a
// write replaces as a whole.
func (m *Manager) lockTunnel(accountID, tunnelID string) (unlock func()) {
	v, _ := m.tunnelLocks.LoadOrStore(accountID+"/"+tunnelID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

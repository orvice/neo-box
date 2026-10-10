package cloudflareacct

import (
	"context"

	"go.orx.me/apps/neo-box/internal/cloudflare"
)

// Zones returns one page of the Account's Zones whose name contains query.
func (a *Account) Zones(ctx context.Context, query string, page, perPage int) ([]cloudflare.Zone, *cloudflare.PageInfo, error) {
	zones, info, err := a.api.ListZones(ctx, cloudflare.ZoneQuery{AccountID: a.ID, Name: query, Page: page, PerPage: perPage})
	if err != nil {
		return nil, nil, a.classify(ctx, err, "list Zones", "Zone Read")
	}
	// Cloudflare filters by Account; anything else is dropped all the same.
	out := zones[:0]
	for _, z := range zones {
		if z.Account.ID == a.ID {
			out = append(out, z)
		}
	}
	return out, info, nil
}

// Zone reads a Zone of the Account. A Zone of any other Account is not
// found, even when the token can see it.
func (a *Account) Zone(ctx context.Context, zoneID string) (*cloudflare.Zone, error) {
	z, err := a.api.GetZone(ctx, zoneID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return nil, ErrZoneNotFound
		}
		return nil, a.classify(ctx, err, "read this Zone", "Zone Read")
	}
	if z.Account.ID != a.ID {
		return nil, ErrZoneNotFound
	}
	return z, nil
}

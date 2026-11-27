package cloudflare_test

import (
	"context"
	"testing"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
)

func TestZoneRulesFakeCRUDAndReorder(t *testing.T) {
	f := cftest.New()
	defer f.Close()
	f.AddAccount(accA, "A")
	f.AddToken("tok", &cftest.Token{
		Accounts: []string{accA},
		Perms:    []string{cftest.PermZoneRead, cftest.PermOriginRead, cftest.PermOriginEdit},
	})
	f.AddZone(&cftest.Zone{ID: "z1", Name: "example.com", Account: accA})
	c := newClient(t, f.URL, "tok")
	ctx := context.Background()

	// Absent entrypoint returns nil, nil (not an error).
	got, err := c.GetZonePhaseRuleset(ctx, "z1", cloudflare.ZoneRuleOrigin)
	if err != nil || got != nil {
		t.Fatalf("absent entrypoint = %+v, %v", got, err)
	}
	// Another category without permission returns PermissionDenied.
	if _, err := c.GetZonePhaseRuleset(ctx, "z1", cloudflare.ZoneRuleSingleRedirect); !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("redirect without perm = %v, want permission denied", err)
	}

	// Create ruleset with first rule, then append, patch, reorder, delete.
	rs, err := c.CreateZoneRuleset(ctx, "z1", cloudflare.ZoneRuleset{
		Phase: cloudflare.ZoneRuleOrigin,
		Rules: []cloudflare.ZoneRule{{ID: "r1", Action: "route", Expression: "true"}},
	})
	if err != nil || rs.ID == "" || len(rs.Rules) != 1 {
		t.Fatalf("CreateZoneRuleset = %+v, %v", rs, err)
	}
	rs, err = c.AddZoneRulesetRule(ctx, "z1", rs.ID, cloudflare.ZoneRule{ID: "r2", Action: "route", Expression: "false"})
	if err != nil || len(rs.Rules) != 2 {
		t.Fatalf("AddZoneRulesetRule = %+v, %v", rs, err)
	}
	rs.Rules[0].Description = "changed"
	rs, err = c.UpdateZoneRulesetRule(ctx, "z1", rs.ID, "r1", rs.Rules[0])
	if err != nil || rs.Rules[0].Description != "changed" {
		t.Fatalf("UpdateZoneRulesetRule = %+v, %v", rs, err)
	}
	rs, err = c.ReorderZoneRules(ctx, "z1", *rs, []cloudflare.ZoneRule{rs.Rules[1], rs.Rules[0]})
	if err != nil || rs.Rules[0].ID != "r2" || rs.Rules[1].ID != "r1" {
		t.Fatalf("ReorderZoneRules = %+v, %v", rs, err)
	}
	if err = c.DeleteZoneRule(ctx, "z1", rs.ID, "r2"); err != nil {
		t.Fatalf("DeleteZoneRule = %v", err)
	}
	after, err := c.GetZonePhaseRuleset(ctx, "z1", cloudflare.ZoneRuleOrigin)
	if err != nil || after == nil || len(after.Rules) != 1 || after.Rules[0].ID != "r1" {
		t.Fatalf("after delete = %+v, %v", after, err)
	}
}

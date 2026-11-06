package cloudflare_test

import (
	"context"
	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	"testing"
)

func TestZoneRulesFakeCRUDAndReorder(t *testing.T) {
	f := cftest.New()
	defer f.Close()
	f.AddAccount(accA, "A")
	f.AddToken("tok", &cftest.Token{Accounts: []string{accA}, Perms: []string{cftest.PermZoneRead, cftest.PermZoneRulesRead, cftest.PermZoneRulesEdit}})
	f.AddZone(&cftest.Zone{ID: "z1", Name: "example.com", Account: accA})
	c := newClient(t, f.URL, "tok")
	ctx := context.Background()
	rs, err := c.CreateZoneRuleset(ctx, "z1", cloudflare.ZoneRuleset{Phase: cloudflare.ZoneRuleOrigin, Rules: []cloudflare.ZoneRule{{ID: "r1", Action: "route", Expression: "true"}, {ID: "r2", Action: "route", Expression: "false"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListZoneRulesets(ctx, "z1", cloudflare.ZoneRuleOrigin)
	if err != nil || len(got) != 1 || len(got[0].Rules) != 2 {
		t.Fatalf("list: %#v %v", got, err)
	}
	got[0].Rules[0].Description = "changed"
	if _, err = c.UpdateZoneRule(ctx, "z1", rs.ID, got[0].Rules[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ReorderZoneRules(ctx, "z1", got[0], []cloudflare.ZoneRule{got[0].Rules[1], got[0].Rules[0]}); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteZoneRule(ctx, "z1", rs.ID, "r2"); err != nil {
		t.Fatal(err)
	}
}

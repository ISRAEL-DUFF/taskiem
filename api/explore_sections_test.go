package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Explore never empty (docs/spec/mobile-app-explore-and-offline.md §2,
// migration 00427): Trusted on Taskiem, Popular, Featured and the
// categories, each keeping Explore's rules: findable businesses only,
// never ourselves or one we are linked with, a suspended page never.
func TestExploreSections(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Media = newProjectStorage(t).service()
	admin := w.env.DB.Admin
	ada := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	dangote := w.tenant(t, "Dangote Stores", "d@dangote.test")
	cherry := w.tenant(t, "Cherry Blocks", "c@cherry.test")
	emeka := w.tenant(t, "Emeka Foods", "e@emeka.test")
	adaID, dangoteID, cherryID, emekaID := tenantOf(t, ada), tenantOf(t, dangote), tenantOf(t, cherry), tenantOf(t, emeka)

	// Dangote and Cherry: findable, a logo, a tagline, two photos, verified.
	for i, c := range []*client{dangote, cherry} {
		c.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
		c.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "Blocks, cement and sand", "area": "Ikeja", "about": ""})
		if s, _ := c.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 10+i)); s != 200 {
			t.Fatalf("logo: %d", s)
		}
		for j := range 2 {
			if s, _ := c.rawPost(t, "/v1/business-profile/gallery", "image/jpeg", photoN(t, 20+i*2+j)); s != 201 {
				t.Fatalf("photo: %d", s)
			}
		}
		if _, err := admin.Exec(ctx, `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor) VALUES ($1, $2, 'phone', 'otp', 'test')`, uuid.New(), tenantOf(t, c)); err != nil {
			t.Fatal(err)
		}
	}
	// Someone of Dangote's wrote in last week; Cherry's not for 40 days.
	active := func(tenant string, ago time.Duration, number string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number, last_active_at)
			SELECT user_id, $2, now() - $3::interval FROM memberships WHERE tenant_id = $1 LIMIT 1
			ON CONFLICT (user_id) DO UPDATE SET last_active_at = EXCLUDED.last_active_at`, tenant, number, ago.String()); err != nil {
			t.Fatal(err)
		}
	}
	active(dangoteID, 7*24*time.Hour, "+2348030000001")
	active(cherryID, 40*24*time.Hour, "+2348030000002")

	names := func(cards any) []string {
		out := []string{}
		for _, c := range cards.([]any) {
			out = append(out, c.(map[string]any)["name"].(string))
		}
		return out
	}
	page := ada.must(200, "GET", "/v1/explore", nil)
	if got := names(page["trusted"]); len(got) != 1 || got[0] != "Dangote Stores" {
		t.Fatalf("trusted: %v", got)
	}
	// Featured with no picks: Trusted fill it in.
	if got := names(page["featured"]); len(got) != 1 || got[0] != "Dangote Stores" {
		t.Fatalf("featured, no picks: %v", got)
	}
	if got := names(page["popular"]); len(got) != 0 {
		t.Fatalf("popular with no links: %v", got)
	}
	// Our area not set: Across Nigeria in Near you's place, the fullest first.
	if got := names(page["across"]); len(got) != 2 || page["near"].([]any) == nil || len(page["near"].([]any)) != 0 {
		t.Fatalf("across Nigeria: %v", got)
	}
	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Ikeja"})
	if p := ada.must(200, "GET", "/v1/explore", nil); len(p["near"].([]any)) != 2 || len(p["across"].([]any)) != 0 {
		t.Fatalf("with businesses near us, no Across Nigeria: %v %v", names(p["near"]), names(p["across"]))
	}
	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"area": ""})

	// Categories: one or two of the list; left out of a save, kept.
	cherry.must(400, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja", "categories": []string{"food", "building", "farm"}})
	cherry.must(400, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja", "categories": []string{"rockets"}})
	if got := cherry.must(200, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja", "categories": []string{"building", "building"}})["categories"]; len(got.([]any)) != 1 {
		t.Fatalf("categories saved: %v", got)
	}
	cherry.must(200, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja", "categories": []string{"building", "food"}})
	dangote.must(200, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja", "categories": []string{"building"}})
	if got := cherry.must(200, "PUT", "/v1/business-profile", map[string]any{"tagline": "Blocks, cement and sand", "area": "Ikeja"})["categories"]; len(got.([]any)) != 2 {
		t.Fatalf("categories left out of a save: %v", got)
	}
	// A business not findable is not counted.
	emeka.must(200, "PUT", "/v1/business-profile", map[string]any{"categories": []string{"food"}})
	counts := map[string]float64{}
	page = ada.must(200, "GET", "/v1/explore", nil)
	for _, c := range page["categories"].([]any) {
		m := c.(map[string]any)
		counts[m["key"].(string)] = m["n"].(float64)
	}
	if len(page["categories"].([]any)) != 13 || counts["building"] != 2 || counts["food"] != 1 || counts["farm"] != 0 {
		t.Fatalf("category counts: %v", counts)
	}
	if got := names(ada.must(200, "GET", "/v1/explore?section=category&category=building", nil)["in_category"]); len(got) != 2 || got[0] != "Cherry Blocks" {
		t.Fatalf("a category: %v", got)
	}
	ada.must(400, "GET", "/v1/explore?section=category&category=rockets", nil)
	if c := page["trusted"].([]any)[0].(map[string]any); len(c["categories"].([]any)) != 1 || c["categories"].([]any)[0] != "building" {
		t.Errorf("a card's categories: %v", c["categories"])
	}

	// Popular: Emeka, findable, with four links; never one with fewer than three.
	emeka.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	emekaUUID := uuid.MustParse(emekaID)
	link := func(other uuid.UUID) {
		t.Helper()
		lo, hi := other, emekaUUID
		if hi.String() < lo.String() {
			lo, hi = hi, lo
		}
		if _, err := admin.Exec(ctx, `INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at, answered_at, answered_actor)
			VALUES ($1, $2, $3, 'active', $4, 'user:test', 'search', 'both', now() + interval '30 days', now(), 'user:test')`, uuid.New(), lo, hi, other); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		link(w.env.AddTenant(t))
	}
	page = ada.must(200, "GET", "/v1/explore", nil)
	if got := page["popular"].([]any); len(got) != 1 || got[0].(map[string]any)["name"] != "Emeka Foods" || got[0].(map[string]any)["links"] != float64(4) {
		t.Fatalf("popular: %v", got)
	}
	// One that links with us is in the chats, not in Popular.
	link(uuid.MustParse(adaID))
	if got := names(ada.must(200, "GET", "/v1/explore?section=popular", nil)["popular"]); len(got) != 0 {
		t.Fatalf("a linked business in Popular: %v", got)
	}

	// Featured: a pick comes first, before the Trusted that fill in.
	if _, err := admin.Exec(ctx, `INSERT INTO explore_featured (id, business, until, created_by) VALUES ($1, $2, now() + interval '7 days', 'operator:test')`, uuid.New(), cherryID); err != nil {
		t.Fatal(err)
	}
	if got := names(ada.must(200, "GET", "/v1/explore?section=featured", nil)["featured"]); len(got) != 2 || got[0] != "Cherry Blocks" || got[1] != "Dangote Stores" {
		t.Fatalf("featured, a pick: %v", got)
	}
	// Never ourselves.
	if got := names(cherry.must(200, "GET", "/v1/explore?section=featured", nil)["featured"]); len(got) != 1 || got[0] != "Dangote Stores" {
		t.Fatalf("featured, seen by the pick itself: %v", got)
	}
	// A suspended page falls out at once: of Featured and of the counts.
	if _, err := admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, created_by, suspended_reason, suspended_at, suspended_by, status_before_suspension) VALUES ($1, $2, 'cherry-blocks', 'suspended', 'test', 'Checking a complaint', now(), 'operator:test', 'published')`, uuid.New(), cherryID); err != nil {
		t.Fatal(err)
	}
	page = ada.must(200, "GET", "/v1/explore", nil)
	if got := names(page["featured"]); len(got) != 1 || got[0] != "Dangote Stores" {
		t.Fatalf("featured, suspended: %v", got)
	}
	for _, c := range page["categories"].([]any) {
		if m := c.(map[string]any); m["key"] == "food" && m["n"] != float64(0) {
			t.Fatalf("a suspended business counted: %v", m)
		}
	}
}

// The operators choose the Featured row: a pick until a date, ended early;
// each write proved with a passkey and recorded.
func TestOpsFeatured(t *testing.T) {
	w := opsWorld(t)
	op, a := enrolOperator(t, w, "curator@taskiem.test")
	viewer := w.tenant(t, "Viewer Ltd", "v@viewer.test")
	biz := w.tenant(t, "Bisi Bags", "bisi@bags.test")
	hidden := w.tenant(t, "Hidden Ltd", "h@hidden.test")
	bizID, hiddenID := tenantOf(t, biz), tenantOf(t, hidden)
	biz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	until := time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)

	op.must(403, "POST", "/v1/ops/explore/featured", map[string]any{"business": bizID, "until": until})
	op.must(409, "POST", "/v1/ops/explore/featured", map[string]any{"business": hiddenID, "until": until, "step_up": op.stepUp(a, "ops.explore.feature", hiddenID)})
	op.must(422, "POST", "/v1/ops/explore/featured", map[string]any{"business": bizID, "until": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"step_up": op.stepUp(a, "ops.explore.feature", bizID)})
	id := op.must(201, "POST", "/v1/ops/explore/featured", map[string]any{"business": bizID, "until": until, "note": "Launch week",
		"step_up": op.stepUp(a, "ops.explore.feature", bizID)})["id"].(string)
	picks := op.must(200, "GET", "/v1/ops/explore/featured", nil)["picks"].([]any)
	if len(picks) != 1 || picks[0].(map[string]any)["name"] != "Bisi Bags" || picks[0].(map[string]any)["shown"] != true || picks[0].(map[string]any)["note"] != "Launch week" {
		t.Fatalf("picks: %v", picks)
	}
	if got := viewer.must(200, "GET", "/v1/explore?section=featured", nil)["featured"].([]any); len(got) != 1 || got[0].(map[string]any)["business"] != bizID {
		t.Fatalf("the row: %v", got)
	}
	op.must(204, "POST", "/v1/ops/explore/featured/"+id+"/end", map[string]any{"step_up": op.stepUp(a, "ops.explore.unfeature", id)})
	op.must(404, "POST", "/v1/ops/explore/featured/"+id+"/end", map[string]any{"step_up": op.stepUp(a, "ops.explore.unfeature", id)})
	if got := viewer.must(200, "GET", "/v1/explore?section=featured", nil)["featured"].([]any); len(got) != 0 {
		t.Fatalf("an ended pick: %v", got)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN ('ops.explore.feature', 'ops.explore.unfeature')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audited: %d %v", n, err)
	}
}

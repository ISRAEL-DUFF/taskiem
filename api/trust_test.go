package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
)

// verifyPhone records a phone verification for the client's organisation,
// as linking WhatsApp or an agent's consent would.
func verifyPhone(t *testing.T, w *world, c *client, levels ...string) {
	t.Helper()
	if len(levels) == 0 {
		levels = []string{"phone"}
	}
	for _, l := range levels {
		if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor)
			VALUES (gen_random_uuid(), $1, $2, 'test', 'test')`, tenantOf(t, c), l); err != nil {
			t.Fatal(err)
		}
	}
}

// moderationModel flags anything mentioning Gucci, as the model would a
// branded bag at a market price.
func moderationModel() *ai.Fake {
	return &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		msg := req.Messages[0].Text
		var pieces []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(msg[strings.Index(msg, "["):]), &pieces); err != nil {
			return nil, err
		}
		var out []map[string]any
		for _, p := range pieces {
			v, cat, why := "allowed", "", ""
			if strings.Contains(p.Text, "Gucci") {
				v, cat, why = "flagged", "counterfeit", "branded goods at a market price"
			}
			out = append(out, map[string]any{"id": p.ID, "verdict": v, "category": cat, "reason": why})
		}
		raw, _ := json.Marshal(map[string]any{"results": out})
		return &ai.Response{JSON: raw}, nil
	}}
}

func TestStorefrontTrust(t *testing.T) {
	ctx := context.Background()
	w := opsWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	w.srv.AI = &api.AISettings{Provider: moderationModel()}
	op, a := enrolOperator(t, w, "reviewer@taskiem.test")
	owner := w.tenant(t, "Bisi Bags", "bisi@bags.test")
	tenant := tenantOf(t, owner)
	card := map[string]any{"name": "Bisi Bags", "tagline": "Bags for every day", "phone": "0803 123 4567", "template": "bold", "accent": "orange"}
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "bisi-bags", "draft": card})
	pub := w.env.DB.PublicPool(t, 1)
	var sfID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM storefronts WHERE slug = 'bisi-bags'`).Scan(&sfID); err != nil {
		t.Fatal(err)
	}
	live := func() string {
		t.Helper()
		var status string
		err := pub.QueryRow(ctx, `SELECT status FROM taskiem_public_storefront($1)`, sfID).Scan(&status)
		if err != nil {
			return "not served"
		}
		return status
	}
	review := func(kind string) map[string]any {
		t.Helper()
		for _, x := range op.must(200, "GET", "/v1/ops/storefront-reviews", nil)["reviews"].([]any) {
			if r := x.(map[string]any); r["kind"] == kind && r["tenant_id"] == tenant {
				return r
			}
		}
		t.Fatalf("no pending %s review", kind)
		return nil
	}
	decide := func(id, decision, note string) map[string]any {
		t.Helper()
		return op.must(200, "POST", "/v1/ops/storefront-reviews/"+id+"/"+decision, map[string]any{"note": note,
			"step_up": op.stepUp(a, "ops.storefront.review", id)})
	}

	// A page needs a verified phone; a first publish from a business not
	// yet verified waits for a person, and is not served.
	if r := owner.must(409, "POST", "/v1/storefront/publish", nil); !strings.Contains(r["error"].(string), "verified phone") {
		t.Fatalf("no phone: %v", r)
	}
	verifyPhone(t, w, owner)
	if sf := owner.must(200, "POST", "/v1/storefront/publish", nil); sf["status"] != "in_review" {
		t.Fatalf("first publish: %v", sf)
	}
	if got := live(); got != "not served" {
		t.Fatalf("in review, the page is %s", got)
	}
	trust := owner.must(200, "GET", "/v1/storefront/trust", nil)
	if v := trust["verification"].(map[string]any); v["phone"] != true || v["business"] != false || len(trust["reviews"].([]any)) != 1 {
		t.Fatalf("trust: %v", trust)
	}
	first := review("first_publish")
	op.must(403, "POST", "/v1/ops/storefront-reviews/"+first["id"].(string)+"/approve", map[string]any{})
	op.must(422, "POST", "/v1/ops/storefront-reviews/"+first["id"].(string)+"/reject", map[string]any{"step_up": op.stepUp(a, "ops.storefront.review", first["id"].(string))})
	decide(first["id"].(string), "approve", "")
	if got := live(); got != "published" {
		t.Fatalf("approved, the page is %s", got)
	}

	// The word list blocks: the change is refused, the page stays as it was.
	card["tagline"] = "Double your money in 7 days"
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "bisi-bags", "draft": card})
	if r := owner.must(400, "POST", "/v1/storefront/publish", nil); !strings.Contains(r["error"].(string), "cannot go on a public page") {
		t.Fatalf("blocked: %v", r)
	}
	// The model flags: a live page's change goes up, and waits for a look.
	card["tagline"] = "Gucci bags from ₦5,000"
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "bisi-bags", "draft": card})
	if sf := owner.must(200, "POST", "/v1/storefront/publish", nil); sf["status"] != "published" {
		t.Fatalf("flagged edit: %v", sf)
	}
	edit := review("edit")
	if !strings.Contains(toJSON(edit["findings"]), "counterfeit") {
		t.Fatalf("edit review: %v", edit)
	}
	// Rejected: the page comes down, says why, and cannot be published.
	decide(edit["id"].(string), "reject", "Branded goods at that price look counterfeit.")
	if got := live(); got != "suspended" {
		t.Fatalf("rejected edit, the page is %s", got)
	}
	trust = owner.must(200, "GET", "/v1/storefront/trust", nil)
	if trust["status"] != "suspended" || !strings.Contains(trust["suspended_reason"].(string), "counterfeit") {
		t.Fatalf("suspended: %v", trust)
	}
	owner.must(409, "POST", "/v1/storefront/publish", nil)
	owner.must(409, "POST", "/v1/storefront/reopen", nil)
	// The owner appeals; approved, the page is back as it was.
	owner.must(400, "POST", "/v1/storefront/appeal", map[string]any{"message": " "})
	owner.must(202, "POST", "/v1/storefront/appeal", map[string]any{"message": "They are genuine; I have the receipts."})
	appeal := review("appeal")
	decide(appeal["id"].(string), "approve", "Receipts seen.")
	if got := live(); got != "published" {
		t.Fatalf("appeal approved, the page is %s", got)
	}

	// Reports: three people reporting suspend the page pending review.
	for i := range 3 {
		h := sha256.Sum256([]byte{byte(i)})
		var ok bool
		if err := pub.QueryRow(ctx, `SELECT taskiem_public_report('storefront', $1, 'scam', 'took my money', NULL, $2)`,
			sfID, h[:]).Scan(&ok); err != nil || !ok {
			t.Fatalf("report %d: %v %v", i, ok, err)
		}
	}
	if err := w.srv.TrustTick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := live(); got != "suspended" {
		t.Fatalf("reported, the page is %s", got)
	}
	reports := op.must(200, "GET", "/v1/ops/reports", nil)["reports"].([]any)
	if len(reports) != 3 || reports[0].(map[string]any)["reporters_on_site"] != float64(3) {
		t.Fatalf("reports: %v", reports)
	}
	reported := review("reports")
	detail := op.must(200, "GET", "/v1/ops/storefront-reviews/"+reported["id"].(string), nil)
	if len(detail["reports"].([]any)) != 3 || detail["storefront"].(map[string]any)["slug"] != "bisi-bags" {
		t.Fatalf("review detail: %v", detail)
	}
	decide(reported["id"].(string), "approve", "Checked with the customers: a delivery delay.")
	if got := live(); got != "published" {
		t.Fatalf("reports dismissed, the page is %s", got)
	}
	if n := len(op.must(200, "GET", "/v1/ops/reports", nil)["reports"].([]any)); n != 0 {
		t.Fatalf("%d reports still open", n)
	}

	// Payment when ordering needs a verified business; the owner asks.
	owner.must(409, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "at_order"})
	owner.must(400, "POST", "/v1/storefront/verification", map[string]any{"legal_name": "Bisi Bags Ltd"})
	owner.must(202, "POST", "/v1/storefront/verification", map[string]any{"legal_name": "Bisi Bags Ltd", "address": "14 Balogun St, Lagos", "cac_number": "RC123456"})
	business := review("business")
	decide(business["id"].(string), "approve", "")
	if v := owner.must(200, "GET", "/v1/storefront/trust", nil)["verification"].(map[string]any); v["business"] != true {
		t.Fatalf("verified: %v", v)
	}
	owner.must(409, "POST", "/v1/storefront/verification", map[string]any{"legal_name": "Bisi Bags Ltd", "address": "14 Balogun St"})

	// Operators suspend and restore by hand; everything is in both chains.
	op.must(422, "POST", "/v1/ops/storefronts/"+tenant+"/suspend", map[string]any{"step_up": op.stepUp(a, "ops.storefront.suspend", tenant)})
	op.must(200, "POST", "/v1/ops/storefronts/"+tenant+"/suspend", map[string]any{"reason": "Checking a complaint", "step_up": op.stepUp(a, "ops.storefront.suspend", tenant)})
	op.must(200, "POST", "/v1/ops/storefronts/"+tenant+"/restore", map[string]any{"step_up": op.stepUp(a, "ops.storefront.restore", tenant)})
	op.must(409, "POST", "/v1/ops/storefronts/"+tenant+"/restore", map[string]any{"step_up": op.stepUp(a, "ops.storefront.restore", tenant)})
	platform := toJSON(op.must(200, "GET", "/v1/ops/audit", nil))
	for _, x := range []string{"ops.storefront.review_approved", "ops.storefront.review_rejected", "ops.storefront.suspend", "ops.storefront.restore"} {
		if !strings.Contains(platform, x) {
			t.Errorf("no %s in the platform chain", x)
		}
	}
	own := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, x := range []string{"storefront.review_approved", "storefront.appeal", "storefront.verification_request", "storefront.suspend"} {
		if !strings.Contains(own, x) {
			t.Errorf("no %s in the business's chain", x)
		}
	}
}

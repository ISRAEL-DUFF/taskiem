package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

func TestStorefrontOrders(t *testing.T) {
	w := newWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: "shops.taskiem.test",
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})

	acts := owner.must(200, "GET", "/v1/storefront/actions", nil)["actions"].([]any)
	if len(acts) != 3 || acts[0].(map[string]any)["kind"] != "order" || acts[0].(map[string]any)["enabled"] != false || acts[0].(map[string]any)["workflow_id"] != nil {
		t.Fatalf("%v", acts)
	}
	// No workflow yet: the form cannot be enabled.
	owner.must(409, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "after_accept"})
	owner.must(400, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": false, "label": "see https://evil.test"})
	owner.must(400, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": false, "payment": "later"})

	tpl, _ := templates.Default().Get("storefront-enquiry")
	def, err := tpl.Instantiate(map[string]any{}, "Questions")
	if err != nil {
		t.Fatal(err)
	}
	wf := publishFlow(t, owner, string(def))
	// One workflow per form: a second one for enquiries is refused.
	dup := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "dup", "definition": json.RawMessage(def)})
	owner.must(409, "POST", "/v1/workflows/"+dup["id"].(string)+"/versions/1/publish", nil)
	a := owner.must(200, "PUT", "/v1/storefront/actions/enquiry", map[string]any{"enabled": true, "label": "Ask us"})
	if a["enabled"] != true || a["workflow_id"] != wf || a["payment"] != "none" || a["label"] != "Ask us" {
		t.Fatalf("%v", a)
	}

	// Published, the page lists the form; a submission becomes an order.
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	sfID := uuid.MustParse(pub["id"].(string))
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id FROM storefronts WHERE id = $1`, sfID).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	ref := storefront.NewReference("ada-kitchen")
	_, hash := storefront.NewStatusKey()
	ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "enquiry", Payment: "none", Channel: "direct", Submitted: time.Now(),
		Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567", Email: "bola@example.com", Message: "Do you cater for 50 people?"}})
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(context.Background(), `SELECT taskiem_public_submit($1, 'enquiry', $2, $3, $4, $5)`,
		sfID, ref, bytes.Repeat([]byte{7}, 32), hash, ct).Scan(&got); err != nil || got == nil {
		t.Fatalf("submit: %v %v", got, err)
	}
	if err := w.srv.Orders.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	list := owner.must(200, "GET", "/v1/storefront/orders?status=open", nil)["orders"].([]any)
	if len(list) != 1 {
		t.Fatalf("%v", list)
	}
	o := list[0].(map[string]any)
	c := o["customer"].(map[string]any)
	if o["reference"] != ref || o["kind"] != "enquiry" || o["run_id"] == nil || c["name"] != "B•••" || c["phone"] != "•••4567" ||
		c["email"] != "b•••@example.com" || c["message"] != "•••" {
		t.Fatalf("masked order: %v", o)
	}
	one := owner.must(200, "GET", "/v1/storefront/orders/"+ref+"?reveal=true", nil)
	if one["revealed"] != true || one["order"].(map[string]any)["customer"].(map[string]any)["message"] != "Do you cater for 50 people?" {
		t.Fatalf("revealed: %v", one)
	}
	owner.must(404, "GET", "/v1/storefront/orders/AK-NOPE00", nil)

	// Another organisation sees none of it; an admin without pii.reveal
	// sees it masked only.
	other := w.tenant(t, "Bola Bakes", "bola@bola.test")
	if l := other.must(200, "GET", "/v1/storefront/orders", nil)["orders"].([]any); len(l) != 0 {
		t.Fatalf("%v", l)
	}
	other.must(404, "GET", "/v1/storefront/orders/"+ref, nil)
	admin := addMember(t, w, owner, "adm", "admin")
	admin.must(200, "GET", "/v1/storefront/orders/"+ref, nil)
	admin.must(403, "GET", "/v1/storefront/orders/"+ref+"?reveal=true", nil)
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), "storefront_order:"+ref) {
		t.Error("the reveal was not audited")
	}
	_ = tenant
}

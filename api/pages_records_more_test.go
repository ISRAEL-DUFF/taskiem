package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// addDelivery puts a delivered delivery of an order in the business, to a
// rider whose name is sealed as the Riders screen keeps it.
func addDelivery(t *testing.T, w *tablesWorld, c *client, ref, rider string, feeKobo int64) string {
	t.Helper()
	ctx := context.Background()
	tenant := uuid.MustParse(tenantOf(t, c))
	id := uuid.Must(uuid.NewV7())
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		phone, err := w.env.Vault.SealTx(ctx, tx, tenant, "phone", "+2348099990000")
		if err != nil {
			return err
		}
		name, err := w.env.Vault.SealUnderTx(ctx, tx, tenant, phone["subject"].(string), "name", rider)
		if err != nil {
			return err
		}
		rid := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO riders (id, tenant_id, phone_hash, phone, name, created_by) VALUES ($1, $2, $3, $4, $5, 'test')`,
			rid, tenant, bytes.Repeat([]byte{byte(len(rider))}, 32), phone, name); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO deliveries (id, tenant_id, order_id, rider_id, status, offered_by, expires_at, fee_kobo, delivered_at)
			SELECT $1, $2, o.id, $3, 'delivered', 'owner', now(), $4, now() FROM storefront_orders o WHERE o.reference = $5`, id, tenant, rid, feeKobo, ref)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// addBookCustomer puts a customer in the debtor book, their name sealed.
func addBookCustomer(t *testing.T, w *tablesWorld, c *client, name string) {
	t.Helper()
	ctx := context.Background()
	tenant := uuid.MustParse(tenantOf(t, c))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sealed, err := w.env.Vault.SealTx(ctx, tx, tenant, "name", name)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO invoice_customers (id, tenant_id, subject_id, customer_id, name, search, created_by) VALUES ($1::uuid, $2, $3, 'key:book:' || $1::text, $4, $5, 'test')`,
			uuid.Must(uuid.NewV7()), tenant, sealed["subject"], sealed, strings.ToLower(name))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// The acceptance fixture (pages §17): a page declared to equal the
// hand-built deliveries page, today's deliveries from the record view, the
// amount owed to riders as a sum, Confirm as a signal on each row; and the
// customers and staff views beside it. Each view needs its screen's read
// permission; the customer is masked for everyone; a rider's name shows.
func TestPagesDeliveriesFixture(t *testing.T) {
	w := recordsWorld(t)
	owner := w.tenant(t, "Acme", "owner@deliveries.test")
	cashier := addMember(t, w.world, owner, "cas", "cashier") // order.handle, invoice.read, not member.manage
	viewer := addMember(t, w.world, owner, "vic", "viewer")   // none of the screens'
	addOrder(t, w, owner, "AC-12", "dispatched", "Bayo Ade", "+2348012345678", 990000)
	delivery := addDelivery(t, w, owner, "AC-12", "Musa", 150000)
	addBookCustomer(t, w, owner, "Ngozi Eze")

	// A workflow that waits for each delivery's Confirm, which a page may send.
	confirmWF := publishWF(t, owner, "Confirm deliveries", json.RawMessage(`{"schema":"wd/v1","id":"wf_deliveryok","version":1,"name":"Confirm deliveries",
		"trigger":{"type":"manual"},"inputs":{"schema":{"type":"object","properties":{"delivery":{"type":"string"}}}},
		"steps":[{"id":"wait","type":"signal","config":{"event":"confirm","correlation":"=trigger.body.delivery","pages":true}}]}`))
	run := owner.must(201, "POST", "/v1/workflows/"+confirmWF+"/runs", map[string]any{"input": map[string]any{"delivery": delivery}})["run_id"].(string)
	w.env.Drain(t)

	doc := map[string]any{"page": "page/v1", "name": "deliveries-today", "title": "Deliveries today",
		"filters": []map[string]any{{"id": "when", "type": "dates", "column": "offered_at"}},
		"blocks": []map[string]any{
			{"id": "owed", "type": "figure", "label": "Owed to riders", "value": "sum", "of": "owed", "naira": true, "source": map[string]any{"records": "deliveries"}},
			{"id": "today", "type": "list", "title": "Today's deliveries",
				"source": map[string]any{"records": "deliveries", "uses": []string{"when"}, "order": []map[string]any{{"column": "offered_at", "desc": true}}},
				"item":   map[string]any{"title": "=row.order + ' · ' + row.rider", "sub": "=row.customer", "state": map[string]any{"text": "=row.status"}},
				"actions": []map[string]any{{"label": "Confirm", "when": "=row.status == 'delivered' && row.confirmed_at == ''",
					"signal": map[string]any{"workflow": "Confirm deliveries", "name": "confirm", "to": "=row.id"}}}},
			{"id": "who", "type": "table", "title": "Customers", "source": map[string]any{"records": "customers"},
				"columns": []map[string]any{{"column": "customer"}, {"column": "owed", "naira": true}}},
			{"id": "team", "type": "table", "title": "Staff", "source": map[string]any{"records": "staff"},
				"columns": []map[string]any{{"column": "name"}, {"column": "roles"}}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if pub := owner.must(200, "POST", "/v1/pages/deliveries-today/publish", nil); len(pub["problems"].([]any)) != 0 {
		t.Fatalf("publish: %v", pub["problems"])
	}

	v := viewOf(t, owner, "deliveries-today", map[string]any{"when": "today"})
	if b := blockOf(t, v, "owed"); b["shown"] != "₦1,500" {
		t.Errorf("owed: %v", b)
	}
	rows := blockOf(t, v, "today")["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("today: %v", blockOf(t, v, "today"))
	}
	row := rows[0].(map[string]any)
	if row["title"] != "AC-12 · Musa" || strings.Contains(row["sub"].(string), "Bayo") || len(row["actions"].([]any)) != 1 {
		t.Errorf("a delivery: %v", row)
	}
	if got := toJSON(blockOf(t, v, "who")["rows"]); strings.Contains(got, "Ngozi") || !strings.Contains(got, "•") {
		t.Errorf("customers masked: %s", got)
	}
	if got := toJSON(blockOf(t, v, "team")["rows"]); !strings.Contains(got, "owner") {
		t.Errorf("staff: %s", got)
	}

	// Confirm, as a signal: asks, then moves the run on.
	owner.must(200, "POST", "/v1/pages/deliveries-today/act", map[string]any{"block": "today", "row": row["id"], "action": 0, "yes": true})
	w.env.Drain(t)
	var status string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT status FROM runs WHERE id = $1`, run).Scan(&status)
	if status != "completed" {
		t.Errorf("confirmed run: %s", status)
	}
	// A delivery changed says when (changed_at, for the live notices).
	var changed *time.Time
	_, _ = w.env.DB.Admin.Exec(context.Background(), `UPDATE deliveries SET confirmed_at = now() WHERE id = $1`, delivery)
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT changed_at FROM deliveries WHERE id = $1`, delivery).Scan(&changed)
	if changed == nil {
		t.Errorf("changed_at not kept")
	}

	// Each view needs its screen's permission.
	cv := viewOf(t, cashier, "deliveries-today", nil)
	if blockOf(t, cv, "today")["unavailable"] != nil || blockOf(t, cv, "who")["unavailable"] != nil || blockOf(t, cv, "team")["unavailable"] != "You cannot see staff." {
		t.Errorf("cashier: %v", cv["blocks"])
	}
	vv := viewOf(t, viewer, "deliveries-today", nil)
	if blockOf(t, vv, "today")["unavailable"] != "You cannot see deliveries." || blockOf(t, vv, "owed")["unavailable"] == nil {
		t.Errorf("viewer: %v", vv["blocks"])
	}
	if s, _ := viewer.do("POST", "/v1/pages/deliveries-today/act", map[string]any{"block": "today", "row": row["id"], "action": 0}); s != 403 {
		t.Errorf("viewer pressed a delivery's button: %d", s)
	}

	// An area turned off: deliveries are the online shop's.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false}})
	if b := blockOf(t, viewOf(t, owner, "deliveries-today", nil), "today"); b["unavailable"] == nil {
		t.Errorf("area off: %v", b)
	}
}

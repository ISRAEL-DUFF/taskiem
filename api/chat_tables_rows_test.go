package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// rowByName is a table's row with that name, from the fake's project.
func (w *waTablesWorld) rowByName(t *testing.T, table, name string) map[string]any {
	t.Helper()
	for _, r := range w.rows(t, table) {
		if r["name"] == name {
			return r
		}
	}
	return nil
}

// Rows changed and removed in chat (decision 0060, R4): find numbers its
// rows; change shows the row and asks which detail and its value, several
// before the yes; delete asks yes; a change made elsewhere between the
// show and the yes is never overwritten, and one to another column is
// kept; a replayed yes writes once; a viewer and a kept table refused.
func TestChatChangesAndRemovesRows(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn, cn, vn = "+2348010000009", "+2348010000007", "+2348010000008", "+2348010000005"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bob@acme.test", "builder"), bn)
	w.bind(t, w.member(t, owner, "cat@acme.test", "builder"), cn)
	w.bind(t, w.member(t, owner, "vic@acme.test", "viewer"), vn)
	tbl := owner.must(201, "POST", "/v1/tables", debtorsTable)
	id := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Ada", "phone": "+2348012345678", "balance_kobo": 5000, "paid": false}})
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Bisi"}})
	patch := func(name string, set map[string]any) {
		t.Helper()
		row := w.rowByName(t, "debtors", name)
		owner.must(200, "PATCH", "/v1/tables/"+id+"/rows/"+fmt.Sprint(row["id"]), map[string]any{"set": set})
	}

	// Found rows are numbered, with what can be done with them.
	r := w.say(t, on, "find debtors ada")
	if !strings.Contains(r, "1. #1 · Name: Ada") || !strings.Contains(r, "*change 1*") || strings.Contains(r, "8012345678") {
		t.Fatalf("find: %q", r)
	}
	if r := w.say(t, on, "change 2"); !strings.Contains(r, "no row 2 in the list") {
		t.Errorf("not in the list: %q", r)
	}
	// Change: the row with its details numbered, personal ones masked.
	r = w.say(t, on, "change 1")
	if !strings.Contains(r, "Row 1 in Debtors:") || !strings.Contains(r, "1. Name: Ada") || !strings.Contains(r, "3. balance_kobo: 5000") ||
		!strings.Contains(r, "4. paid: false") || strings.Contains(r, "8012345678") {
		t.Fatalf("change: %q", r)
	}
	if r := w.say(t, on, "9"); !strings.Contains(r, "from 1 to 4") {
		t.Errorf("bad detail: %q", r)
	}
	if r := w.say(t, on, "3"); !strings.Contains(r, "The new value:") || !strings.Contains(r, "balance_kobo") {
		t.Fatalf("ask: %q", r)
	}
	if r := w.say(t, on, "lots"); !strings.Contains(r, "does not fit") {
		t.Errorf("checked: %q", r)
	}
	if r := w.say(t, on, "3000"); !strings.Contains(r, "Save this change to row 1 in Debtors?") || !strings.Contains(r, "balance_kobo: 5000 → 3000") {
		t.Fatalf("read back: %q", r)
	}
	// Another detail before the yes.
	w.say(t, on, "4")
	if r := w.say(t, on, "yes"); !strings.Contains(r, "balance_kobo: 5000 → 3000") || !strings.Contains(r, "paid: false → true") {
		t.Fatalf("two details: %q", r)
	}
	if ada := w.rowByName(t, "debtors", "Ada"); fmt.Sprint(ada["balance_kobo"]) != "5000" {
		t.Fatalf("written before yes: %v", ada)
	}
	// A yes delivered twice by WhatsApp is handled once.
	yes := whatsapptest.Delivery(waPNID, on, "wamid.rows-yes", whatsapptest.Text("yes"))
	for range 2 {
		if st := w.deliver(t, yes, whatsapp.Sign(waSecret, yes)); st != 200 {
			t.Fatal(st)
		}
	}
	if sent := w.graph.Sent(on); !strings.Contains(sent[len(sent)-1].Text, "Saved row 1 in Debtors.") {
		t.Fatalf("saved: %q", sent[len(sent)-1].Text)
	}
	if ada := w.rowByName(t, "debtors", "Ada"); fmt.Sprint(ada["balance_kobo"]) != "3000" || ada["paid"] != true || ada["phone"] != "+2348012345678" {
		t.Fatalf("the row: %v", ada)
	}
	var detail string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action = 'table.row.update'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"channel": "whatsapp"`) || !strings.Contains(detail, `"row_id": 1`) || !strings.Contains(detail, `"columns": 2`) || strings.Contains(detail, "3000") {
		t.Errorf("audit: %v %s", err, detail)
	}

	// Changed on the web between the show and the yes: nothing written,
	// the row shown again as it is, asked again.
	w.say(t, on, "change 1")
	w.say(t, on, "3")
	w.say(t, on, "1000")
	patch("Ada", map[string]any{"balance_kobo": 2500})
	r = w.say(t, on, "yes")
	if !strings.Contains(r, "changed after you saw it") || !strings.Contains(r, "3. balance_kobo: 2500") || !strings.Contains(r, "Which detail") {
		t.Fatalf("changed since: %q", r)
	}
	if ada := w.rowByName(t, "debtors", "Ada"); fmt.Sprint(ada["balance_kobo"]) != "2500" {
		t.Fatalf("overwritten: %v", ada)
	}
	// Changed elsewhere in another column: the change is written and the
	// other kept.
	w.say(t, on, "3")
	w.say(t, on, "1000")
	patch("Ada", map[string]any{"name": "Ada O."})
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Saved row 1") {
		t.Fatalf("other column: %q", r)
	}
	if ada := w.rowByName(t, "debtors", "Ada O."); ada == nil || fmt.Sprint(ada["balance_kobo"]) != "1000" {
		t.Fatalf("both kept: %v", w.rows(t, "debtors"))
	}

	// An empty value is guarded as empty; delete asks yes, refuses a row
	// changed since, and removes it after a yes on the row as it is.
	if r := w.say(t, bn, "find debtors bisi"); !strings.Contains(r, "1. #2 · Name: Bisi") {
		t.Fatalf("find bisi: %q", r)
	}
	w.say(t, bn, "change 1")
	w.say(t, bn, "3")
	w.say(t, bn, "700")
	if r := w.say(t, bn, "yes"); !strings.Contains(r, "Saved row 2") {
		t.Fatalf("from empty: %q", r)
	}
	r = w.say(t, bn, "delete 1")
	if !strings.Contains(r, "Remove row 2 from Debtors?") || !strings.Contains(r, "balance_kobo: 700") {
		t.Fatalf("delete: %q", r)
	}
	patch("Bisi", map[string]any{"paid": true})
	r = w.say(t, bn, "yes")
	if !strings.Contains(r, "changed after you saw it") || !strings.Contains(r, "paid: true") || !strings.Contains(r, "Remove row 2") {
		t.Fatalf("delete changed since: %q", r)
	}
	if w.rowByName(t, "debtors", "Bisi") == nil {
		t.Fatal("removed though changed")
	}
	if r := w.say(t, bn, "yes"); !strings.Contains(r, "Removed row 2 from Debtors.") {
		t.Fatalf("removed: %q", r)
	}
	if w.rowByName(t, "debtors", "Bisi") != nil {
		t.Fatal("still there")
	}
	if r := w.say(t, bn, "change 1"); !strings.Contains(r, "Row 2 is no longer in Debtors") {
		t.Errorf("gone: %q", r)
	}
	// Anything else lets the list go and is read as itself.
	if r := w.say(t, bn, "tables"); !strings.Contains(r, "1. Debtors") {
		t.Errorf("let go: %q", r)
	}
	if r := w.say(t, bn, "change 1"); strings.Contains(r, "Row") {
		t.Errorf("change without a list: %q", r)
	}
	var deletes int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'table.row.delete'`).Scan(&deletes); err != nil || deletes != 1 {
		t.Errorf("delete audits: %d %v", deletes, err)
	}

	// A viewer finds but cannot change.
	if r := w.say(t, vn, "find debtors ada"); !strings.Contains(r, "1. #1") || strings.Contains(r, "*change 1*") {
		t.Errorf("viewer find: %q", r)
	}
	if r := w.say(t, vn, "change 1"); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer change: %q", r)
	}

	// More pages on, five at a time; no leaves the rows as they are.
	for i := range 7 {
		owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": fmt.Sprintf("Bulk %d", i)}})
	}
	r = w.say(t, cn, "find debtors bulk")
	if !strings.Contains(r, "5. #") || !strings.Contains(r, "and 2 more") || !strings.Contains(r, "*more*") {
		t.Fatalf("first page: %q", r)
	}
	r = w.say(t, cn, "more")
	if !strings.Contains(r, "1. #4") || !strings.Contains(r, "2. #3") || strings.Contains(r, "3. #") {
		t.Fatalf("second page: %q", r)
	}
	w.say(t, cn, "delete 1")
	if r := w.say(t, cn, "no"); !strings.Contains(r, "Nothing was changed") {
		t.Errorf("no: %q", r)
	}
	if r := w.say(t, cn, "more"); !strings.Contains(r, "That is all") {
		t.Errorf("last page: %q", r)
	}
	if n := len(w.rows(t, "debtors")); n != 8 {
		t.Errorf("rows: %d", n)
	}

	// A kept table is found but never changed.
	if _, err := w.srv.Tables.EnsureKept(context.Background(), tenantID(t, owner), "money_log", "money_log", moneyLogColumns); err != nil {
		t.Fatal(err)
	}
	w.pg.Begin(w.pg.Projects()[0]).Insert("money_log", map[string]any{"id": 1, "entry_id": "e1", "amount_kobo": 250000, "direction": "in"}).Commit()
	if r := w.say(t, cn, "find money_log e1"); !strings.Contains(r, "1. #1") || strings.Contains(r, "*change 1*") {
		t.Fatalf("kept find: %q", r)
	}
	if r := w.say(t, cn, "delete 1"); !strings.Contains(r, "kept by Taskiem") {
		t.Errorf("kept delete: %q", r)
	}
	if n := len(w.pg.Rows(w.pg.Projects()[0], "money_log")); n != 1 {
		t.Errorf("kept rows: %d", n)
	}
}

// A personal detail's new value comes in a WhatsApp form, sealed until
// it is written, and is masked in the read-back.
func TestChatChangesPersonalValueInForm(t *testing.T) {
	w := newWATablesWorld(t)
	w.flows()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)
	tbl := owner.must(201, "POST", "/v1/tables", debtorsTable)
	owner.must(201, "POST", "/v1/tables/"+tbl["id"].(string)+"/rows", map[string]any{"row": map[string]any{"name": "Ada", "phone": "+2348012345678"}})

	w.say(t, on, "find debtors ada")
	w.say(t, on, "change 1")
	got := w.send(t, on, whatsapptest.Text("2"))
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs || !strings.Contains(got[0].Text, "enter the new value") {
		t.Fatalf("form: %+v", got)
	}
	f := got[0]
	if d := f.FlowData; d["t1_label"] != "phone" || d["t2_on"] != false {
		t.Errorf("one field: %v", d)
	}
	const ep = "/channels/whatsapp/flows"
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"t1": "+2348099990000"})
	if st != 200 || flowToken(t, resp) != f.FlowToken {
		t.Fatalf("answer: %d %v", st, resp)
	}
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM whatsapp_flows`).Scan(&raw); err != nil || strings.Contains(raw, "8099990000") {
		t.Errorf("flow data: %v %s", err, raw)
	}
	got = w.send(t, on, whatsapptest.FlowCompleted(f.FlowToken))
	if len(got) != 1 || !strings.Contains(got[0].Text, "phone: ") || !strings.Contains(got[0].Text, "5678 → ") || !strings.Contains(got[0].Text, "0000") ||
		strings.Contains(got[0].Text, "8099990000") {
		t.Fatalf("read back: %+v", got)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Saved row 1") {
		t.Fatalf("saved: %q", r)
	}
	if ada := w.rowByName(t, "debtors", "Ada"); ada["phone"] != "+2348099990000" {
		t.Errorf("the row: %v", ada)
	}
	var session string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT coalesce(string_agg(data::text, ''), '') FROM chat_sessions`).Scan(&session)
	if strings.Contains(session, "8099990000") || strings.Contains(session, "8012345678") {
		t.Errorf("personal values in the session: %s", session)
	}
}

// The same on Telegram, where a personal value is asked in chat (no row
// form yet): find, change, a change made elsewhere refused, then saved.
func TestTelegramChangesRows(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const acct = 7001
	w.link(t, owner, acct, "ada")
	tbl := owner.must(201, "POST", "/v1/tables", debtorsTable)
	id := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Ada", "phone": "+2348012345678", "balance_kobo": 5000}})

	if r := w.say(t, acct, "find debtors ada").Text; !strings.Contains(r, "1. #1 · Name: Ada") || !strings.Contains(r, "change 1") {
		t.Fatalf("find: %q", r)
	}
	w.say(t, acct, "change 1")
	if r := w.say(t, acct, "2").Text; !strings.Contains(r, "The new value:") {
		t.Fatalf("personal asked in chat: %q", r)
	}
	if r := w.say(t, acct, "+2348099990000").Text; !strings.Contains(r, "5678 → ") || strings.Contains(r, "8099990000") {
		t.Fatalf("read back masked: %q", r)
	}
	owner.must(200, "PATCH", "/v1/tables/"+id+"/rows/1", map[string]any{"set": map[string]any{"phone": "+2348011110000"}})
	if r := w.say(t, acct, "yes").Text; !strings.Contains(r, "changed after you saw it") {
		t.Fatalf("changed since: %q", r)
	}
	if rows := pg.Rows(pg.Projects()[0], "debtors"); rows[0]["phone"] != "+2348011110000" {
		t.Fatalf("overwritten: %v", rows)
	}
	w.say(t, acct, "2")
	w.say(t, acct, "+2348099990000")
	if r := w.say(t, acct, "yes").Text; !strings.Contains(r, "Saved row 1 in Debtors.") {
		t.Fatalf("saved: %q", r)
	}
	if rows := pg.Rows(pg.Projects()[0], "debtors"); rows[0]["phone"] != "+2348099990000" {
		t.Errorf("the row: %v", rows)
	}
}

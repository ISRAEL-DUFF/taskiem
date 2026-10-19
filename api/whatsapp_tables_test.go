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

// Data tables over WhatsApp (docs/whatsapp.md#data-tables): the list, a
// row added column by column or through a form, a search. Each test
// spreads its messages over several numbers: a number gets twenty
// messages, then one every three seconds.

// waTablesWorld is a WhatsApp world with a fake Xenibase as the platform's.
type waTablesWorld struct {
	*waWorld
	pg *xenibasetest.Fake
}

func newWATablesWorld(t *testing.T) *waTablesWorld {
	t.Helper()
	w := newWAWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	return &waTablesWorld{waWorld: w, pg: pg}
}

// rows are the rows of a table in the organisation's project.
func (w *waTablesWorld) rows(t *testing.T, table string) []map[string]any {
	t.Helper()
	projects := w.pg.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects: %v", projects)
	}
	return w.pg.Rows(projects[0], table)
}

var customersTable = map[string]any{"name": "customers", "title": "Customers", "columns": []map[string]any{
	{"name": "name", "title": "Name", "type": "text", "required": true},
	{"name": "email", "type": "email"},
	{"name": "joined", "title": "Joined", "type": "date"},
	{"name": "notes", "type": "text"},
}}

func TestWhatsAppTables(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn, vn, an = "+2348010000009", "+2348010000007", "+2348010000005", "+2348010000006"
	w.bind(t, owner, on)
	builder := w.member(t, owner, "bob@acme.test", "builder")
	w.bind(t, builder, bn)
	viewer := w.member(t, owner, "vic@acme.test", "viewer")
	w.bind(t, viewer, vn)
	approver := w.member(t, owner, "app@acme.test", "approver")
	w.bind(t, approver, an)

	// None yet: the web app makes them. Help offers what the roles allow.
	if r := w.say(t, on, "tables"); !strings.Contains(r, "no data tables yet") || !strings.Contains(r, waPublicURL+"/tables") {
		t.Errorf("no tables: %q", r)
	}
	if r := w.say(t, an, "tables"); !strings.Contains(r, "table.read") {
		t.Errorf("approver: %q", r)
	}
	if r := w.say(t, on, "help"); !strings.Contains(r, "*tables*") || !strings.Contains(r, "*add to*") || !strings.Contains(r, "*find*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, vn, "help"); !strings.Contains(r, "*tables*") || strings.Contains(r, "*add to*") || !strings.Contains(r, "*find*") {
		t.Errorf("viewer help: %q", r)
	}

	// Two tables, one with a row: listed with their counts.
	tbl := owner.must(201, "POST", "/v1/tables", debtorsTable)
	id := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables", customersTable)
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": "Chidi", "balance_kobo": 12000}})
	r := w.say(t, on, "tables")
	if !strings.Contains(r, "1. Customers: 0 row(s)") || !strings.Contains(r, "2. Debtors: 1 row(s)") || !strings.Contains(r, "*add to*") {
		t.Errorf("list: %q", r)
	}
	if r := w.say(t, vn, "tables"); !strings.Contains(r, "2. Debtors: 1 row(s)") || strings.Contains(r, "*add to*") {
		t.Errorf("viewer list: %q", r)
	}

	// A viewer cannot add; the owner adds column by column, each answer
	// checked as the API checks it.
	if r := w.say(t, vn, "add to debtors"); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer add: %q", r)
	}
	if r := w.say(t, on, "add to debt"); !strings.Contains(r, "Adding a row to Debtors (4 column(s))") || !strings.Contains(r, "1/4. Enter Name") {
		t.Fatalf("start: %q", r)
	}
	if r := w.say(t, on, "Ada"); !strings.Contains(r, "2/4. Enter phone") || !strings.Contains(r, "country code") || !strings.Contains(r, "*skip*") {
		t.Fatalf("phone asked: %q", r)
	}
	if r := w.say(t, on, "call me"); !strings.Contains(r, "does not fit") || !strings.Contains(r, "phone number") || !strings.Contains(r, "2/4. Enter phone") {
		t.Errorf("bad phone: %q", r)
	}
	if r := w.say(t, on, "0801 234 5678"); !strings.Contains(r, "3/4. Enter balance_kobo") {
		t.Fatalf("balance asked: %q", r)
	}
	if r := w.say(t, on, "lots"); !strings.Contains(r, "does not fit") || !strings.Contains(r, "3/4") {
		t.Errorf("bad balance: %q", r)
	}
	if r := w.say(t, on, "5,000"); !strings.Contains(r, "4/4. Enter paid (yes or no)") {
		t.Fatalf("paid asked: %q", r)
	}
	confirm := w.send(t, on, whatsapptest.Text("skip"))
	if len(confirm) != 1 || len(confirm[0].Buttons) != 2 || confirm[0].Buttons[0].Title != "Yes, add it" {
		t.Fatalf("confirmation: %+v", confirm)
	}
	ct := confirm[0].Text
	if !strings.Contains(ct, "Add this row to Debtors?") || !strings.Contains(ct, "Name: Ada") || !strings.Contains(ct, "phone: +234") || !strings.Contains(ct, "•5678") ||
		!strings.Contains(ct, "balance_kobo: 5000") || !strings.Contains(ct, "paid: -") || strings.Contains(ct, "8012345678") {
		t.Errorf("confirmation text: %q", ct)
	}
	// The phone number waits sealed.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM chat_sessions WHERE number = $1`, on).Scan(&raw); err != nil || strings.Contains(raw, "8012345678") || !strings.Contains(raw, "Ada") {
		t.Errorf("session data: %v %s", err, raw)
	}
	if n := len(w.rows(t, "debtors")); n != 1 {
		t.Fatalf("rows before yes: %d", n)
	}
	if r := w.say(t, on, "no"); !strings.Contains(r, "Nothing was added") {
		t.Errorf("no: %q", r)
	}
	if n := len(w.rows(t, "debtors")); n != 1 {
		t.Fatalf("rows after no: %d", n)
	}

	// Yes adds the row once; a second yes adds nothing.
	w.say(t, on, "add to debtors")
	w.say(t, on, "Ada")
	w.say(t, on, "+2348012345678")
	w.say(t, on, "5000")
	if r := w.say(t, on, "no"); !strings.Contains(r, "Add this row to Debtors?") || !strings.Contains(r, "paid: false") {
		t.Fatalf("second confirmation: %q", r)
	}
	got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, add it"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Added row 2 to Debtors.") {
		t.Fatalf("added: %+v", got)
	}
	rows := w.rows(t, "debtors")
	if len(rows) != 2 {
		t.Fatalf("rows after yes: %v", rows)
	}
	var ada map[string]any
	for _, r := range rows {
		if r["name"] == "Ada" {
			ada = r
		}
	}
	if ada == nil || ada["phone"] != "+2348012345678" || fmt.Sprint(ada["balance_kobo"]) != "5000" || ada["paid"] != false {
		t.Errorf("the row: %v", ada)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "did not understand") {
		t.Errorf("second yes: %q", r)
	}
	if n := len(w.rows(t, "debtors")); n != 2 {
		t.Errorf("rows after the second yes: %d", n)
	}
	// Audited without the row's values; the connection reads recorded as
	// the chat's.
	var detail string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action = 'table.row.add'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"channel": "whatsapp"`) || !strings.Contains(detail, `"row_id": "2"`) || strings.Contains(detail, "Ada") || strings.Contains(detail, "2348012345678") {
		t.Errorf("audit: %v %s", err, detail)
	}
	var reads int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM secret_reads WHERE kind = 'connection' AND name = 'data_tables' AND purpose = 'tables.chat'`).Scan(&reads); err != nil || reads < 2 {
		t.Errorf("secret reads for the chat: %d %v", reads, err)
	}

	// Find: short lines, personal values masked, how many more.
	r = w.say(t, bn, "find debtors ada")
	if !strings.Contains(r, `In Debtors, matching "ada":`) || !strings.Contains(r, "• #2 · Name: Ada · phone: +234") || !strings.Contains(r, "•5678") ||
		!strings.Contains(r, "balance_kobo: 5000") || strings.Contains(r, "8012345678") || strings.Contains(r, "more") {
		t.Errorf("find: %q", r)
	}
	if r := w.say(t, vn, "find debtors chidi"); !strings.Contains(r, "Name: Chidi") || !strings.Contains(r, "balance_kobo: 12000") {
		t.Errorf("viewer find: %q", r)
	}
	if r := w.say(t, an, "find debtors ada"); !strings.Contains(r, "table.read") {
		t.Errorf("approver find: %q", r)
	}
	if r := w.say(t, bn, "find debtors zzz"); !strings.Contains(r, `Nothing in Debtors matches "zzz"`) {
		t.Errorf("find none: %q", r)
	}
	if r := w.say(t, bn, "find nothing here"); !strings.Contains(r, "No data table matches") {
		t.Errorf("find no table: %q", r)
	}
	if r := w.say(t, bn, "find debtors"); !strings.Contains(r, "what to look for") {
		t.Errorf("find without text: %q", r)
	}
	for i := range 7 {
		owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"name": fmt.Sprintf("Bulk %d", i)}})
	}
	if r := w.say(t, bn, "find debtors bulk"); strings.Count(r, "• #") != 5 || !strings.Contains(r, "and 2 more") {
		t.Errorf("find more: %q", r)
	}

	// Several tables match: a numbered pick. A title of several words is
	// found too.
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "debtors_old", "title": "Old debtors", "columns": []map[string]any{{"name": "name", "type": "text", "required": true}}})
	if r := w.say(t, bn, "add to debt"); !strings.Contains(r, "Which one?") || !strings.Contains(r, "1. Debtors") || !strings.Contains(r, "2. Old debtors") {
		t.Fatalf("pick: %q", r)
	}
	if r := w.say(t, bn, "9"); !strings.Contains(r, "Send a number") {
		t.Errorf("bad pick: %q", r)
	}
	if r := w.say(t, bn, "2"); !strings.Contains(r, "Adding a row to Old debtors (1 column(s))") {
		t.Errorf("picked: %q", r)
	}
	if r := w.say(t, bn, "cancel"); !strings.Contains(r, "Cancelled") {
		t.Errorf("cancel: %q", r)
	}
	if r := w.say(t, bn, "find old debtors ada"); !strings.Contains(r, `Nothing in Old debtors matches "ada"`) {
		t.Errorf("find by title: %q", r)
	}
}

func TestWhatsAppTablesFlow(t *testing.T) {
	w := newWATablesWorld(t)
	w.flows()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn = "+2348010000009", "+2348010000007"
	w.bind(t, owner, on)
	builder := w.member(t, owner, "bob@acme.test", "builder")
	w.bind(t, builder, bn)
	owner.must(201, "POST", "/v1/tables", customersTable)
	const ep = "/channels/whatsapp/flows"

	// The row comes as a form built from the columns: required flags,
	// labels, hints.
	got := w.send(t, on, whatsapptest.Text("add to customers"))
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs || got[0].FlowScreen != "INPUTS" || !strings.Contains(got[0].Text, "Customers: tap *Fill in*") {
		t.Fatalf("form: %+v", got)
	}
	f := got[0]
	d := f.FlowData
	if d["t1_on"] != true || d["t1_label"] != "Name" || d["t1_req"] != true || d["t2_label"] != "email" || d["t2_req"] != false || d["t2_help"] != "An email address" ||
		d["t3_label"] != "Joined" || !strings.Contains(fmt.Sprint(d["t3_help"]), "YYYY-MM-DD") || d["t4_on"] != true || d["t5_on"] != false {
		t.Errorf("form data: %v", d)
	}
	if st, resp := w.flowCall(t, ep, waSecret, "INIT", "", f.FlowToken, nil); st != 200 || screenData(t, resp)["t1_label"] != "Name" {
		t.Fatalf("init: %d %v", st, resp)
	}
	// Values are checked as the API checks them; optional ones may be blank.
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"t1": "Ada", "t2": "not an email", "t3": "", "t4": ""})
	if sd := screenData(t, resp); st != 200 || resp["screen"] != "INPUTS" || sd["has_error"] != true || sd["t2_err"] == "" || sd["t3_err"] != "" {
		t.Fatalf("bad answers: %d %v", st, resp)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"t1": "Ada", "t2": "ada@example.com", "t3": "2026-11-01", "t4": ""})
	if st != 200 || flowToken(t, resp) != f.FlowToken {
		t.Fatalf("good answers: %d %v", st, resp)
	}
	// The email waits sealed.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM whatsapp_flows`).Scan(&raw); err != nil || strings.Contains(raw, "ada@example.com") || !strings.Contains(raw, "2026-11-01") {
		t.Errorf("flow data: %v %s", err, raw)
	}
	// The completed form arrives in the chat: the summary, masked, then yes.
	got = w.send(t, on, whatsapptest.FlowCompleted(f.FlowToken))
	if len(got) != 1 || len(got[0].Buttons) != 2 || !strings.Contains(got[0].Text, "Add this row to Customers?") || !strings.Contains(got[0].Text, "Name: Ada") ||
		!strings.Contains(got[0].Text, "email: a•••@example.com") || !strings.Contains(got[0].Text, "Joined: 2026-11-01") || !strings.Contains(got[0].Text, "notes: -") {
		t.Fatalf("confirmation: %+v", got)
	}
	if n := len(w.rows(t, "customers")); n != 0 {
		t.Fatalf("rows before yes: %d", n)
	}
	if r := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, add it")); len(r) != 1 || !strings.Contains(r[0].Text, "Added row 1 to Customers.") {
		t.Fatalf("added: %+v", r)
	}
	rows := w.rows(t, "customers")
	if len(rows) != 1 || rows[0]["email"] != "ada@example.com" || rows[0]["joined"] != "2026-11-01" || rows[0]["notes"] != nil {
		t.Fatalf("the row: %v", rows)
	}

	// Writing instead of using the form: chat asks column by column.
	got = w.send(t, on, whatsapptest.Text("add to customers"))
	if len(got) != 1 || got[0].Flow == "" {
		t.Fatalf("form: %+v", got)
	}
	if r := w.say(t, on, "Bisi"); !strings.Contains(r, "answer here instead") || !strings.Contains(r, "1/4. Enter Name") {
		t.Errorf("fallback: %q", r)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "INIT", "", got[0].FlowToken, nil); st != 427 {
		t.Errorf("a dropped form still works: %d", st)
	}
	w.say(t, on, "cancel")

	// A table with more columns than a form holds: the form takes the first
	// ten, required ones first, and the rest are asked in chat.
	var cols []map[string]any
	for i := 1; i <= 12; i++ {
		cols = append(cols, map[string]any{"name": fmt.Sprintf("c%d", i), "type": "text", "required": i == 12})
	}
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "big", "title": "Big", "columns": cols})
	got = w.send(t, bn, whatsapptest.Text("add to big"))
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs || !strings.Contains(got[0].Text, "10 detail(s)") {
		t.Fatalf("big form: %+v", got)
	}
	d = got[0].FlowData
	if d["t1_label"] != "c12" || d["t1_req"] != true || d["t2_label"] != "c1" || d["t2_req"] != false || d["t10_label"] != "c9" || d["t10_on"] != true {
		t.Errorf("big form data: %v", d)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", got[0].FlowToken, map[string]any{"t2": "one"})
	if sd := screenData(t, resp); st != 200 || resp["screen"] != "INPUTS" || sd["t1_err"] == "" {
		t.Fatalf("missing required: %d %v", st, resp)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", got[0].FlowToken, map[string]any{"t1": "twelve", "t2": "one"})
	if st != 200 || flowToken(t, resp) != got[0].FlowToken {
		t.Fatalf("big answers: %d %v", st, resp)
	}
	if r := w.send(t, bn, whatsapptest.FlowCompleted(got[0].FlowToken)); len(r) != 1 || !strings.Contains(r[0].Text, "2 more to enter here") || !strings.Contains(r[0].Text, "11/12. Enter c10") {
		t.Fatalf("after the form: %+v", r)
	}
	if r := w.say(t, bn, "ten"); !strings.Contains(r, "12/12. Enter c11") {
		t.Fatalf("c11 asked: %q", r)
	}
	r := w.say(t, bn, "skip")
	if !strings.Contains(r, "Add this row to Big?") || !strings.Contains(r, "c12: twelve") || !strings.Contains(r, "c1: one") || !strings.Contains(r, "c10: ten") || !strings.Contains(r, "c11: -") || !strings.Contains(r, "c2: -") {
		t.Fatalf("big confirmation: %q", r)
	}
	if r := w.say(t, bn, "yes"); !strings.Contains(r, "Added row 1 to Big.") {
		t.Fatalf("big added: %q", r)
	}
	rows = w.rows(t, "big")
	if len(rows) != 1 || rows[0]["c12"] != "twelve" || rows[0]["c1"] != "one" || rows[0]["c10"] != "ten" || rows[0]["c11"] != nil {
		t.Fatalf("the big row: %v", rows)
	}
}

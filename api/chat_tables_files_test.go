package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/googlesheets"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// sendAll sends a message and returns every reply's text, joined.
func (w *waTablesWorld) sendAll(t *testing.T, from string, msg map[string]any) string {
	t.Helper()
	var out []string
	for _, m := range w.send(t, from, msg) {
		out = append(out, m.Text)
	}
	return strings.Join(out, "\n")
}

// xlsxFile is a one-sheet workbook of inline strings and numbers.
func xlsxFile(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var sheet strings.Builder
	sheet.WriteString(`<worksheet><sheetData>`)
	for i, r := range rows {
		fmt.Fprintf(&sheet, `<row r="%d">`, i+1)
		for j, v := range r {
			ref := fmt.Sprintf("%c%d", 'A'+j, i+1)
			if _, err := fmt.Sscanf(v, "%g", new(float64)); err == nil && i > 0 {
				fmt.Fprintf(&sheet, `<c r="%s"><v>%s</v></c>`, ref, v)
				continue
			}
			fmt.Fprintf(&sheet, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, v)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   sheet.String(),
	} {
		f, _ := zw.Create(name)
		_, _ = f.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// A list brought into a table from a file sent on WhatsApp (decision 0060,
// R2): a CSV becomes a new table, its columns typed from its values; the
// same file sent again writes nothing twice; a heading that matches no
// column is asked about; an .xlsx is read; a file too large, too long,
// of another kind or with a row that does not fit is refused with why.
func TestChatBringsAFileIntoATable(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn, cn, vn = "+2348010000009", "+2348010000007", "+2348010000008", "+2348010000005"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bob@acme.test", "builder"), bn)
	w.bind(t, w.member(t, owner, "cat@acme.test", "builder"), cn)
	w.bind(t, w.member(t, owner, "vic@acme.test", "viewer"), vn)
	customers := []byte("Name,Phone number,Amount owed,Joined\nAda,0803 123 4567,\"5,000\",2026-01-05\nBola,+2348031234567,300,2026-02-01\n")
	w.graph.AddMedia("doc-customers", "text/csv", customers, 0)

	if r := w.sendAll(t, vn, whatsapptest.Document("doc-customers", "text/csv", "My customers.csv", "")); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer: %q", r)
	}
	got := w.send(t, on, whatsapptest.Document("doc-customers", "text/csv", "My customers.csv", ""))
	if len(got) != 1 || !strings.Contains(got[0].Text, "*My customers.csv*: 2 row(s) with Name, Phone number, Amount owed, Joined.") || len(got[0].Buttons) != 2 {
		t.Fatalf("summed up: %+v", got)
	}
	// New table: named from the file, typed from the values, read back
	// with the first rows masked.
	r := w.sendAll(t, on, whatsapptest.ButtonReply("file:new", "New table"))
	if !strings.Contains(r, "Make *My customers* with these columns:") || !strings.Contains(r, "2. Phone number: a phone number") ||
		!strings.Contains(r, "3. Amount owed: a number") || !strings.Contains(r, "4. Joined: a date") || !strings.Contains(r, "add its 2 row(s)") ||
		!strings.Contains(r, "• Name: Ada") || strings.Contains(r, "123 4567") || strings.Contains(r, "31234567") {
		t.Fatalf("read back: %q", r)
	}
	r = w.sendAll(t, on, whatsapptest.Text("yes"))
	if !strings.Contains(r, "Setting up your records") || !strings.Contains(r, "Made *My customers* and added 2 row(s) from My customers.csv") || !strings.Contains(r, "not kept") {
		t.Fatalf("made: %q", r)
	}
	rows := w.rows(t, "my_customers")
	if len(rows) != 2 || fmt.Sprint(rows[0]["amount_owed"]) != "5000" || rows[0]["phone_number"] != "08031234567" || rows[1]["joined"] != "2026-02-01" {
		t.Fatalf("rows: %v", rows)
	}
	var detail string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action = 'table.import'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"file": "csv"`) || !strings.Contains(detail, `"rows": 2`) || strings.Contains(detail, "Ada") {
		t.Errorf("audit: %v %s", err, detail)
	}

	// The same file again, added to the table it made: nothing written
	// twice.
	r = w.sendAll(t, bn, whatsapptest.Document("doc-customers", "text/csv", "My customers.csv", ""))
	if !strings.Contains(r, "*Add to table* adds them to My customers") {
		t.Fatalf("offered: %q", r)
	}
	if r := w.sendAll(t, bn, whatsapptest.ButtonReply("file:add", "Add to table")); !strings.Contains(r, "Add 2 row(s) to *My customers*?") {
		t.Fatalf("add read back: %q", r)
	}
	if r := w.sendAll(t, bn, whatsapptest.Text("yes")); !strings.Contains(r, "were already added to *My customers*") {
		t.Fatalf("again: %q", r)
	}
	if n := len(w.rows(t, "my_customers")); n != 2 {
		t.Fatalf("rows after the same file: %d", n)
	}

	// A heading that matches no column is asked about by number.
	w.graph.AddMedia("doc-owes", "text/csv", []byte("Name,Owes\nChidi,700\n"), 0)
	w.sendAll(t, bn, whatsapptest.Document("doc-owes", "text/csv", "owes.csv", ""))
	r = w.sendAll(t, bn, whatsapptest.Text("add to my customers"))
	if !strings.Contains(r, "Which column of *My customers* does *Owes* go in?") || !strings.Contains(r, "3. Amount owed") || !strings.Contains(r, "0. Leave it out") {
		t.Fatalf("asked: %q", r)
	}
	if r := w.sendAll(t, bn, whatsapptest.Text("9")); !strings.Contains(r, "from 0 to 4") {
		t.Errorf("bad number: %q", r)
	}
	if r := w.sendAll(t, bn, whatsapptest.Text("3")); !strings.Contains(r, "Add 1 row(s)") || !strings.Contains(r, "Amount owed: 700") {
		t.Fatalf("mapped: %q", r)
	}
	if r := w.sendAll(t, bn, whatsapptest.Text("yes")); !strings.Contains(r, "Added 1 row(s) to *My customers* from owes.csv") {
		t.Fatalf("added: %q", r)
	}
	if n := len(w.rows(t, "my_customers")); n != 3 {
		t.Errorf("rows: %d", n)
	}

	// Refused, each with why: a row that does not fit, headings that are
	// no column, another kind of file, too large, too many rows.
	w.graph.AddMedia("doc-bad", "text/csv", []byte("Name,Amount owed\nDayo,lots\n"), 0)
	w.sendAll(t, cn, whatsapptest.Document("doc-bad", "text/csv", "bad.csv", ""))
	if r := w.sendAll(t, cn, whatsapptest.Text("add to my customers")); !strings.Contains(r, "Nothing was added: line 2") || !strings.Contains(r, "a number") {
		t.Errorf("bad row: %q", r)
	}
	w.graph.AddMedia("doc-foo", "text/csv", []byte("Foo\nbar\n"), 0)
	w.sendAll(t, cn, whatsapptest.Document("doc-foo", "text/csv", "foo.csv", ""))
	w.sendAll(t, cn, whatsapptest.Text("add to my customers"))
	if r := w.sendAll(t, cn, whatsapptest.Text("0")); !strings.Contains(r, "None of the file's headings") {
		t.Errorf("no columns: %q", r)
	}
	w.graph.AddMedia("doc-pdf", "application/pdf", []byte("%PDF"), 0)
	if r := w.sendAll(t, cn, whatsapptest.Document("doc-pdf", "application/pdf", "list.pdf", "")); !strings.Contains(r, "CSV and Excel (.xlsx)") {
		t.Errorf("pdf: %q", r)
	}
	w.graph.AddMedia("doc-big", "text/csv", []byte("a\n1\n"), 3<<20)
	if r := w.sendAll(t, cn, whatsapptest.Document("doc-big", "text/csv", "big.csv", "")); !strings.Contains(r, "over 2 MB") {
		t.Errorf("big: %q", r)
	}
	var many strings.Builder
	many.WriteString("Name\n")
	for i := range 6000 {
		fmt.Fprintf(&many, "n%d\n", i)
	}
	w.graph.AddMedia("doc-many", "text/csv", []byte(many.String()), 0)
	if r := w.sendAll(t, cn, whatsapptest.Document("doc-many", "text/csv", "many.csv", "")); !strings.Contains(r, "more than 5000 rows") {
		t.Errorf("6,000 rows: %q", r)
	}
	if n := len(w.rows(t, "my_customers")); n != 3 {
		t.Errorf("rows after refusals: %d", n)
	}

	// An .xlsx, and Not now.
	w.graph.AddMedia("doc-xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsxFile(t, [][]string{{"Item", "Qty", "Price"}, {"Rice", "3", "45000"}, {"Beans", "10", "1200.5"}}), 0)
	w.sendAll(t, on, whatsapptest.Document("doc-xlsx", "application/octet-stream", "Stock.xlsx", ""))
	if r := w.sendAll(t, on, whatsapptest.Text("new table")); !strings.Contains(r, "Make *Stock*") || !strings.Contains(r, "3. Price: a number") {
		t.Fatalf("xlsx read back: %q", r)
	}
	if r := w.sendAll(t, on, whatsapptest.Text("yes")); !strings.Contains(r, "Made *Stock* and added 2 row(s)") {
		t.Fatalf("xlsx made: %q", r)
	}
	if rows := w.rows(t, "stock"); len(rows) != 2 || fmt.Sprint(rows[1]["price"]) != "1200.5" {
		t.Errorf("xlsx rows: %v", rows)
	}
	w.sendAll(t, on, whatsapptest.Document("doc-owes", "text/csv", "owes.csv", ""))
	if r := w.sendAll(t, on, whatsapptest.ButtonReply("file:no", "Not now")); !strings.Contains(r, "Nothing was added, and the file was not kept") {
		t.Errorf("not now: %q", r)
	}
	var sessions int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM chat_sessions WHERE data::text LIKE '%Chidi%' OR data::text LIKE '%Ada%'`).Scan(&sessions); err != nil || sessions != 0 {
		t.Errorf("rows kept in a session: %d %v", sessions, err)
	}
}

// The same on Telegram: a document, read with getFile, made a table.
func TestTelegramBringsAFileIntoATable(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const acct = 7002
	w.link(t, owner, acct, "ada")
	w.bot.SetFile("tg-doc", "documents/suppliers.csv", []byte("Supplier,Phone\nMama Put,08031234567\n"), 0)

	got := w.update(t, acct, func(id int64) []byte {
		return telegramtest.DocumentMessage(id, acct, "tg-doc", "suppliers.csv", "text/csv")
	})
	if len(got) != 1 || !strings.Contains(got[0].Text, "suppliers.csv") || !strings.Contains(got[0].Text, "1 row(s)") {
		t.Fatalf("summed up: %+v", got)
	}
	if r := w.say(t, acct, "new table").Text; !strings.Contains(r, "Make Suppliers") && !strings.Contains(r, "Make *Suppliers*") {
		t.Fatalf("read back: %q", r)
	}
	var all []string
	for _, m := range w.update(t, acct, func(id int64) []byte { return telegramtest.Message(id, acct, "", "yes") }) {
		all = append(all, m.Text)
	}
	if !strings.Contains(strings.Join(all, "\n"), "added 1 row(s)") {
		t.Fatalf("made: %q", all)
	}
	if rows := pg.Rows(pg.Projects()[0], "suppliers"); len(rows) != 1 || rows[0]["phone"] != "08031234567" {
		t.Errorf("rows: %v", rows)
	}
}

// A Google Sheet copied into a table once (decision 0060, R2): read with
// the business's Google Sheets connection, brought in as a file is, and
// said to be a copy; a sheet changed between the read-back and the yes
// writes nothing.
func TestChatCopiesASheetOnce(t *testing.T) {
	w := newWATablesWorld(t)
	const sheet = "1SheetSheetSheetSheetSheetSheet0123"
	var values atomic.Value
	values.Store(`[["Customer","Owes"],["Ada","5000"],["Bisi","1200"]]`)
	g := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_, _ = rw.Write([]byte(`{"access_token":"ya29.fake","expires_in":3599,"token_type":"Bearer"}`))
		case strings.HasPrefix(r.URL.Path, "/spreadsheets/"+sheet+"/values/"):
			_, _ = rw.Write([]byte(`{"range":"Owed!A1:B3","majorDimension":"ROWS","values":` + values.Load().(string) + `}`))
		case r.URL.Path == "/spreadsheets/"+sheet:
			_, _ = rw.Write([]byte(`{"spreadsheetId":"` + sheet + `","properties":{"title":"Who owes me"},"sheets":[{"properties":{"sheetId":0,"title":"Owed","index":0}}]}`))
		default:
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`))
		}
	}))
	t.Cleanup(g.Close)
	if err := w.env.Registry.Register(googlesheets.New(googlesheets.Options{BaseURL: g.URL, TokenURL: g.URL + "/token"})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn = "+2348010000009", "+2348010000007"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bob@acme.test", "builder"), bn)
	link := "https://docs.google.com/spreadsheets/d/" + sheet + "/edit#gid=0"

	if r := w.say(t, on, "copy my sheet"); !strings.Contains(r, "the sheet's link") {
		t.Errorf("no link: %q", r)
	}
	if r := w.say(t, on, "copy my sheet "+link); !strings.Contains(r, "Connect Google Sheets first") {
		t.Errorf("no connection: %q", r)
	}
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "googlesheets@1",
		"credentials": map[string]string{"client_id": "app.apps.googleusercontent.com", "client_secret": "s", "refresh_token": "1//r"}})
	if r := w.say(t, on, "copy my sheet https://docs.google.com/spreadsheets/d/1NotThereNotThereNotThere000/edit"); !strings.Contains(r, "cannot open that sheet") {
		t.Errorf("not there: %q", r)
	}
	if r := w.say(t, on, "copy my sheet "+link); !strings.Contains(r, "*Who owes me*: 2 row(s) with Customer, Owes.") {
		t.Fatalf("read: %q", r)
	}
	if r := w.say(t, on, "new table"); !strings.Contains(r, "Make *Who owes me*") || !strings.Contains(r, "2. Owes: a number") {
		t.Fatalf("read back: %q", r)
	}
	r := w.sendAll(t, on, whatsapptest.Text("yes"))
	if !strings.Contains(r, "Made *Who owes me* and added 2 row(s)") || !strings.Contains(r, "This was a copy") {
		t.Fatalf("made: %q", r)
	}
	if rows := w.rows(t, "who_owes_me"); len(rows) != 2 || fmt.Sprint(rows[0]["owes"]) != "5000" {
		t.Fatalf("rows: %v", rows)
	}

	// Changed between the read-back and the yes: nothing written.
	w.say(t, bn, "copy my sheet "+link)
	if r := w.say(t, bn, "add to who owes me"); !strings.Contains(r, "Add 2 row(s) to *Who owes me*?") {
		t.Fatalf("add: %q", r)
	}
	values.Store(`[["Customer","Owes"],["Ada","5000"],["Bisi","1200"],["Chidi","300"]]`)
	if r := w.say(t, bn, "yes"); !strings.Contains(r, "The sheet changed since I read it") {
		t.Fatalf("changed: %q", r)
	}
	if n := len(w.rows(t, "who_owes_me")); n != 2 {
		t.Errorf("rows after a changed sheet: %d", n)
	}
}

// A file in a Slack DM is not read (files:read is not asked for, Q6): the
// conversation says so and points to the Tables page.
func TestSlackFileGoesToTheWeb(t *testing.T) {
	w := newSlackWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@acme.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")
	ev := dm("EvFile", "U0OWNER", "")
	ev["event"].(map[string]any)["subtype"] = "file_share"
	ev["event"].(map[string]any)["files"] = []any{map[string]any{"id": "F1", "name": "debtors.csv"}}
	before := len(w.fake.to("U0OWNER"))
	if st, _ := w.event(t, ev, slackSecret); st != 200 {
		t.Fatalf("event: %d", st)
	}
	got := w.says("U0OWNER", before)
	if len(got) != 1 || !strings.Contains(got[0], "cannot open files sent on Slack") || !strings.Contains(got[0], "/tables") {
		t.Fatalf("answer: %q", got)
	}
}

package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
)

// newBookInvoices is the invoices service the debtor book's chat uses.
func newBookInvoices(w *waWorld) *invoicing.Invoices {
	return &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
}

var clientsTable = map[string]any{"name": "customers", "title": "Customers", "columns": []map[string]any{
	{"name": "name", "title": "Name", "type": "text", "required": true},
	{"name": "area", "title": "Area", "type": "text"},
	{"name": "balance", "title": "Balance", "type": "number"},
	{"name": "phone", "title": "Phone", "type": "phone"},
}}

// Questions over the tables in chat (decision 0060, R6): "how many" and
// "total" need no model; any other question goes to the model with the
// tables' columns and never a row, its spec checked before anything is
// read; the answer is written by code with its reading, personal values
// masked; a spec naming a column the table lacks reads nothing; a question
// the tables cannot answer falls back to the usual reply; Pidgin is read.
func TestChatQuestionsOverTables(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Ada Wears", "owner@adawears.test")
	const on, pn = "+2348010000081", "+2348010000082"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bayo@adawears.test", "builder"), pn)
	id := owner.must(201, "POST", "/v1/tables", clientsTable)["id"].(string)
	for _, r := range []map[string]any{
		{"name": "Ada", "area": "Ikeja", "balance": 15000, "phone": "+2348012345678"},
		{"name": "Bisi", "area": "Yaba", "balance": 5000},
		{"name": "Chidi", "area": "ikeja", "balance": 20000.5},
		{"name": "Dayo", "area": "Lekki"},
	} {
		owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": r})
	}

	// Without a model: the word patterns answer; a question is told once
	// what works.
	if r := w.say(t, on, "how many customers"); !strings.HasSuffix(r, "Customers: 4") {
		t.Fatalf("count: %q", r)
	}
	if r := w.say(t, on, "total balance"); !strings.HasSuffix(r, "Total Balance, Customers: 40,000.50") {
		t.Fatalf("sum: %q", r)
	}
	if r := w.say(t, on, "how many customers in Ikeja?"); !strings.Contains(r, "can't read questions like that here yet") {
		t.Fatalf("no model: %q", r)
	}
	if r := w.say(t, on, "how many customers in Yaba?"); !strings.Contains(r, "couldn't answer that from your tables") {
		t.Fatalf("no model again: %q", r)
	}
	// Stock's "how many" is not taken.
	if r := w.say(t, on, "how many rice"); strings.Contains(r, "Customers") {
		t.Errorf("not a table: %q", r)
	}

	// With a model: it is given the question and the columns, never a row.
	var prompts []string
	answer := map[string]string{
		"in Ikeja":      `{"answerable":true,"table":"customers","filters":[{"column":"area","op":"eq","value":"Ikeja"}],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`,
		"more than 10k": `{"answerable":true,"table":"customers","filters":[{"column":"balance","op":"gt","value":"10000"}],"order":[{"column":"balance","desc":true}],"limit":5,"aggregate":{"fn":"none","column":""}}`,
		"by town":       `{"answerable":true,"table":"customers","filters":[{"column":"town","op":"eq","value":"Yaba"}],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`,
		"dey for Yaba":  `{"answerable":true,"table":"customers","filters":[{"column":"area","op":"eq","value":"Yaba"}],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`,
		"football":      `{"answerable":false,"table":"","filters":[],"order":[],"limit":0,"aggregate":{"fn":"none","column":""}}`,
	}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}, Model: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		prompts = append(prompts, p)
		if !strings.Contains(p, "<question>") {
			return &ai.Response{Text: `{"intent":"unknown","argument":""}`, StopReason: "end_turn"}, nil
		}
		for k, v := range answer {
			if strings.Contains(p, k) {
				return &ai.Response{Text: v, StopReason: "end_turn"}, nil
			}
		}
		return &ai.Response{Text: answer["football"], StopReason: "end_turn"}, nil
	}}}
	if r := w.say(t, on, "how many customers in Ikeja?"); !strings.HasSuffix(r, "Customers where Area is Ikeja: 2") {
		t.Fatalf("question: %q", r)
	}
	last := prompts[len(prompts)-1]
	if !strings.Contains(last, `"balance"`) || strings.Contains(last, "Bisi") || strings.Contains(last, "Lekki") {
		t.Fatalf("the model saw rows or no columns: %s", last)
	}
	// Rows, ordered, personal values masked, ready for change.
	r := w.say(t, on, "who owes more than 10k?")
	if !strings.Contains(r, "Customers where Balance is more than 10000, Balance highest first (2):") || !strings.Contains(r, "1. #3 · Name: Chidi") ||
		!strings.Contains(r, "2. #1 · Name: Ada") || strings.Contains(r, "2345678") || !strings.Contains(r, "*change 1*") {
		t.Fatalf("rows: %q", r)
	}
	if r := w.say(t, on, "change 1"); !strings.Contains(r, "Row 3 in Customers:") || !strings.Contains(r, "Chidi") {
		t.Fatalf("change after a question: %q", r)
	}
	w.say(t, on, "cancel")
	// A spec naming a column the table lacks reads nothing.
	if r := w.say(t, on, "how many customers by town Yaba?"); !strings.Contains(r, "couldn't answer that from your tables") {
		t.Fatalf("refused spec: %q", r)
	}
	var refused int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM ai_interactions WHERE kind = 'table_query' AND outcome = 'refused'`).Scan(&refused); err != nil || refused != 1 {
		t.Errorf("recorded refusal: %d %v", refused, err)
	}
	// Not about the tables: the usual answer.
	if r := w.say(t, on, "who won the football?"); strings.Contains(r, "couldn't answer that from your tables") || !strings.Contains(r, "*find*") {
		t.Errorf("fallback: %q", r)
	}
	// Pidgin.
	w.say(t, pn, "language pidgin")
	if r := w.say(t, pn, "how many customers dey for Yaba?"); !strings.HasSuffix(r, "Customers wey Area na Yaba: 1") {
		t.Errorf("pidgin: %q", r)
	}
}

// The model routes the table and debtor commands in other languages
// (R6): a row of the list just shown changed by its words or the model's
// routing, an owes and a question routed to the English commands; nothing
// is written before the person's own yes.
func TestChatTablesRoutedByModel(t *testing.T) {
	w := newWATablesWorld(t)
	w.srv.Invoices = newBookInvoices(w.waWorld)
	owner := w.tenant(t, "Kano Wears", "owner@kanowears.test")
	const yn, hn = "+2348010000091", "+2348010000092"
	w.bind(t, owner, yn)
	w.bind(t, w.member(t, owner, "musa@kanowears.test", "admin"), hn)
	id := owner.must(201, "POST", "/v1/tables", clientsTable)["id"].(string)
	for _, r := range []map[string]any{{"name": "Ada", "area": "Kano", "balance": 1000}, {"name": "Adamu", "area": "Kano"}} {
		owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": r})
	}
	routes := map[string]string{
		"cire na biyu":        `{"intent":"delete_row","argument":"2"}`,
		"Musa yana bina dubu": `{"intent":"owes","argument":"Musa owes 1000 for shoes"}`,
		"mutane nawa a Kano":  `{"intent":"ask","argument":"how many customers in Kano"}`,
	}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO, lang.HA}, Model: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		if strings.Contains(p, "<question>") {
			return &ai.Response{Text: `{"answerable":true,"table":"customers","filters":[{"column":"area","op":"eq","value":"Kano"}],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`, StopReason: "end_turn"}, nil
		}
		for k, v := range routes {
			if strings.Contains(p, k) {
				return &ai.Response{Text: v, StopReason: "end_turn"}, nil
			}
		}
		return &ai.Response{Text: `{"intent":"unknown","argument":""}`, StopReason: "end_turn"}, nil
	}}}
	w.say(t, yn, "language yoruba")
	w.say(t, hn, "language hausa")

	// Yoruba's words for change, on the list just found.
	w.say(t, yn, "find customers ada")
	if r := w.say(t, yn, "ṣàtúnṣe 1"); !strings.Contains(r, "Customers") || !strings.Contains(r, "Ada") {
		t.Fatalf("yo change: %q", r)
	}
	w.say(t, yn, "cancel")
	// The model's routing of a removal: read back, nothing removed before yes.
	w.say(t, hn, "find customers ada")
	r := w.say(t, hn, "cire na biyu")
	if !strings.Contains(r, "Ada") {
		t.Fatalf("ha delete routed: %q", r)
	}
	if n := len(w.rows(t, "customers")); n != 2 {
		t.Fatalf("removed before the yes: %d rows", n)
	}
	w.say(t, hn, "cancel")
	// A debt, routed as the English words: the phone asked, nothing recorded yet.
	if r := w.say(t, hn, "Musa yana bina dubu"); !strings.Contains(r, "Musa") {
		t.Fatalf("ha owes routed: %q", r)
	}
	w.say(t, hn, "cancel")
	// A question, routed, then read as one.
	if r := w.say(t, hn, "mutane nawa a Kano"); !strings.HasSuffix(r, ": 2") {
		t.Fatalf("ha ask routed: %q", r)
	}
}

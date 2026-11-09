package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
)

// A table naming readers in chat (decision 0065): someone holding none of
// its roles is told who may read it at find, add, add column and rename,
// and nothing of its rows; its row count is not listed; a question over
// the tables does not describe it to the model. A reader, and the owner,
// find and add as with any table.
func TestChatTableReaders(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, bn, hn = "+2348010000031", "+2348010000032", "+2348010000033"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bob@acme.test", "builder"), bn)
	w.bind(t, w.member(t, owner, "hana@acme.test", "builder", "hr"), hn)
	id := owner.must(201, "POST", "/v1/tables", hrTicketsTable)["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+id+"/rows", map[string]any{"row": map[string]any{"reference": "HR-1001", "subject": "My payslip is short"}})
	owner.must(201, "POST", "/v1/tables", clientsTable)

	if r := w.say(t, bn, "tables"); !strings.Contains(r, "HR requests") || strings.Contains(r, "HR requests: 1") {
		t.Errorf("tables: %q", r)
	}
	for _, msg := range []string{"find hr requests pay", "add to hr requests", "add column note to hr requests", "rename hr requests to Mine"} {
		r := w.say(t, bn, msg)
		if !strings.Contains(r, "HR requests is for hr, hr_lead only") || strings.Contains(r, "payslip") || strings.Contains(r, "HR-1001") {
			t.Errorf("%s: %q", msg, r)
		}
	}
	if r := w.say(t, hn, "find hr requests 1001"); !strings.Contains(r, "HR-1001") {
		t.Errorf("a reader's find: %q", r)
	}
	if r := w.say(t, on, "find hr requests 1001"); !strings.Contains(r, "HR-1001") {
		t.Errorf("the owner's find: %q", r)
	}
	// "how many" over a table the person cannot read is not answered from it.
	if r := w.say(t, bn, "how many hr requests"); strings.Contains(r, ": 1") {
		t.Errorf("count: %q", r)
	}
	if r := w.say(t, hn, "how many hr requests"); !strings.HasSuffix(r, "HR requests: 1") {
		t.Errorf("a reader's count: %q", r)
	}

	var prompts []string
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		prompts = append(prompts, p)
		if !strings.Contains(p, "<question>") {
			return &ai.Response{Text: `{"intent":"unknown","argument":""}`, StopReason: "end_turn"}, nil
		}
		return &ai.Response{Text: `{"answerable":true,"table":"hr_tickets","filters":[],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`, StopReason: "end_turn"}, nil
	}}}
	r := w.say(t, bn, "ask how many hr tickets are open?")
	if strings.Contains(r, ": 1") {
		t.Errorf("question: %q", r)
	}
	if len(prompts) == 0 || strings.Contains(prompts[len(prompts)-1], "hr_tickets") || !strings.Contains(prompts[len(prompts)-1], "customers") {
		t.Fatalf("the model was told of a table the person cannot read: %v", prompts)
	}
	if r := w.say(t, hn, "ask how many hr tickets are open?"); !strings.HasSuffix(r, "HR requests: 1") {
		t.Errorf("a reader's question: %q", r)
	}
}

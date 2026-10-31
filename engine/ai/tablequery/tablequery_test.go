package tablequery_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/tablequery"
	"github.com/israel-duff/taskiem/engine/lang"
)

var tables = []tablequery.Table{{Name: "customers", Title: "Customers", Columns: []tablequery.Column{
	{Name: "name", Type: "text"}, {Name: "area", Type: "text"}, {Name: "balance", Type: "number"}}}}

// The model gets the question, the language and the tables' columns, never
// a row; its answer is read as a spec.
func TestAskSendsColumnsNotRows(t *testing.T) {
	fake := &ai.Fake{Script: []ai.Response{{Text: `{"answerable":true,"table":"customers","filters":[{"column":"area","op":"eq","value":"Ikeja"}],"order":[],"limit":0,"aggregate":{"fn":"count","column":""}}`}}}
	pcm, _ := lang.Lookup(lang.PCM)
	s, resp, err := (&tablequery.Asker{Provider: fake}).Ask(context.Background(), "how many customers dey for Ikeja? call 08031234567", pcm, tables)
	if err != nil || resp == nil || s.Table != "customers" || len(s.Filters) != 1 || s.Filters[0].Value != "Ikeja" || s.Aggregate == nil || s.Aggregate.Fn != "count" {
		t.Fatalf("%+v %v", s, err)
	}
	req := fake.Requests()[0]
	prompt := ai.PromptText(req)
	if !strings.Contains(prompt, `"balance"`) || !strings.Contains(prompt, "Nigerian Pidgin") || req.Schema == nil {
		t.Fatalf("prompt: %s", prompt)
	}
	if strings.Contains(prompt, "08031234567") {
		t.Fatalf("a phone number reached the model: %s", prompt)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		answer string
		err    error
		agg    string
	}{
		{`{"answerable":true,"table":"customers","filters":[],"order":[{"column":"balance","desc":true}],"limit":5,"aggregate":{"fn":"none","column":""}}`, nil, ""},
		{`{"answerable":true,"table":"customers","filters":[],"order":[],"limit":0,"aggregate":{"fn":"sum","column":"balance"}}`, nil, "sum"},
		{`{"answerable":false,"table":"","filters":[],"order":[],"limit":0,"aggregate":{"fn":"none","column":""}}`, tablequery.ErrUnanswerable, ""},
		{`{"answerable":true,"table":"","filters":[],"order":[],"limit":0,"aggregate":{"fn":"none","column":""}}`, tablequery.ErrUnusable, ""},
		{`{"answerable":true,"table":"customers","sql":"drop table customers"}`, tablequery.ErrUnusable, ""},
		{`{"table":"customers"}`, tablequery.ErrUnusable, ""},
		{`not json`, tablequery.ErrUnusable, ""},
	}
	for _, c := range cases {
		s, err := tablequery.Parse(&ai.Response{Text: c.answer, StopReason: "end_turn"})
		if !errors.Is(err, c.err) && (err != nil || c.err != nil) {
			t.Errorf("%s: %v", c.answer, err)
			continue
		}
		if err == nil && ((s.Aggregate == nil) != (c.agg == "") || s.Aggregate != nil && s.Aggregate.Fn != c.agg) {
			t.Errorf("%s: aggregate %+v", c.answer, s.Aggregate)
		}
	}
}

package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Sell something with this form maps at most one field to each of the
// buyer's details, the best match; every other field stays the action's
// own question, its reads moved to trigger.answers (review of L1).
func TestSellingDefinitionKeepsOtherFields(t *testing.T) {
	def := `{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"form","config":{"title":"X","access":"public"}},
 "inputs":{"schema":{"type":"object","required":["phone"],"properties":{"phone":{"type":"string","format":"phone-ng","x-pii":"phone"},
  "gift_phone":{"type":"string","format":"phone-ng","title":"Recipient's phone"}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"to":"=trigger.body.gift_phone","from":"=trigger.body.phone"}}}]}`
	out, extra, _, err := sellingDefinition([]byte(def), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := extra["gift_phone"]; !ok || len(extra) != 1 {
		t.Fatalf("the action's questions: %v", extra)
	}
	if s := string(out); !strings.Contains(s, "=trigger.answers.gift_phone") || !strings.Contains(s, "=trigger.body.customer.phone") {
		t.Fatalf("the reads: %s", s)
	}

	// Two phones by format only, none marked: one (the first by name) is
	// the buyer's; a field named phone beats one of its format only.
	def = `{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"form","config":{"title":"X","access":"public"}},
 "inputs":{"schema":{"type":"object","properties":{"alt":{"type":"string","format":"phone-ng"},"mobile":{"type":"string","format":"phone-ng"},
  "address":{"type":"string","title":"Where we meet"},"full_name":{"type":"string"}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"a":"=trigger.body.address","m":"=trigger.body.mobile","n":"=trigger.body.full_name"}}}]}`
	out, extra, _, err = sellingDefinition([]byte(def), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := extra["alt"]; !ok || len(extra) != 1 || !strings.Contains(string(out), "=trigger.body.customer.phone") ||
		!strings.Contains(string(out), "=trigger.body.customer.address") || !strings.Contains(string(out), "=trigger.body.customer.name") {
		t.Fatalf("delivering: %v %s", extra, out)
	}
	// An order form that only takes pickup asks no address: the form's
	// address stays its own question.
	out, extra, _, err = sellingDefinitionFor([]byte(def), uuid.New(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := extra["address"]; !ok || !strings.Contains(string(out), "=trigger.answers.address") || strings.Contains(string(out), "customer.address") {
		t.Fatalf("pickup only: %v %s", extra, out)
	}
}

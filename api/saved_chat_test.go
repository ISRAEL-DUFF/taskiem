package api_test

import (
	"strings"
	"testing"
)

// taskiemSays posts text to the Taskiem chat and answers what Taskiem said.
func taskiemSays(t *testing.T, c *client, text string) string {
	t.Helper()
	var reply string
	for _, x := range c.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": text})["messages"].([]any) {
		if m := x.(map[string]any); m["inbound"] == false {
			reply += m["text"].(string)
		}
	}
	return reply
}

// A message to Taskiem that is only a saved thing's name is answered with
// it (saved objects S1, part 2): the person's own and the business's, the
// account number masked, a text in full; two of one name both, each said
// whose; a name that is no saved thing passes on.
func TestSavedByNameInTaskiemChat(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Bola Stores", "bola@ask.test")
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "bank_name": "Guaranty Trust Bank", "account_number": "2222222222"})
	owner.must(201, "POST", "/v1/saved", map[string]any{"kind": "text", "name": "bank-details", "title": "Our bank details",
		"details": map[string]any{"body": "Pay to Bola Stores\nZenith Bank 1234567890"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"kind": "place", "name": "mum", "title": "Mum's shop",
		"details": map[string]any{"line": "4 Allen Avenue", "area": "Ikeja"}})

	got := taskiemSays(t, owner, "@bank-details")
	if !strings.Contains(got, "@bank-details: Our bank details") || !strings.Contains(got, "Pay to Bola Stores\nZenith Bank 1234567890") {
		t.Fatalf("a text: %q", got)
	}
	got = taskiemSays(t, owner, "show me @MUM?")
	for _, want := range []string{"You have two things called @mum", "Your own:", "Guaranty Trust Bank, account number ••2222",
		"not checked with the bank yet", "Bola Stores's:", "4 Allen Avenue", "Ikeja"} {
		if !strings.Contains(got, want) {
			t.Fatalf("two of one name: %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "2222222222") {
		t.Fatalf("a full account number: %q", got)
	}
	// Not a saved thing: Taskiem reads it as anything else.
	if got := taskiemSays(t, owner, "@nobody"); strings.Contains(got, "@nobody:") {
		t.Fatalf("an unknown name: %q", got)
	}
}

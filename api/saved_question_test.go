package api_test

import (
	"strings"
	"testing"
)

// A question about a saved thing (saved objects S2): the field asked for,
// found by its label; a sensitive one masked; a field with nothing in it
// said; no field named, the thing told whole; an @name no saved thing has
// passes on; a message starting with a linked business stays theirs.
func TestSavedQuestions(t *testing.T) {
	_, owner := savedWorld(t)
	gen := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{
		{"label": "Model", "type": "text"},
		{"label": "Last service date", "type": "date"},
		{"label": "Service", "type": "text"},
		{"label": "Serial", "type": "text", "sensitive": true},
		{"label": "Warranty ends", "type": "date"},
	}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "generator", "title": "Big gen", "kind": "custom", "kind_id": gen["id"],
		"values": map[string]any{"model": "Mikano 20", "last_service_date": "2027-03-03", "service": "Oil change", "serial": "SN-99887766"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum", "title": "Mum", "kind": "person", "details": map[string]any{"phone": "0803 112 0077", "email": "mum@home.ng"}})
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "warehouse", "title": "Warehouse", "kind": "place", "details": map[string]any{"line": "12 Herbert Macaulay Way", "area": "Yaba", "landmark": "opposite the filling station"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Bayo", "bank_name": "Guaranty Trust Bank", "account_number": "0123456789"})

	for q, want := range map[string]string{
		"what's @generator's last service date?": "@generator: Last service date: 3 Mar 2027",
		"what was @generator's service":          "@generator: Service: Oil change",
		"what is @generator's serial?":           "@generator: Serial: ••7766\nIn full on the Saved page.",
		"when does @generator's warranty ends?":  "@generator has no Warranty ends saved.",
		"what's @mum's phone number?":            "@mum: Phone number: ••0077\nIn full on the Saved page.",
		"what is @mum's number":                  "@mum: Phone: ••0077\nIn full on the Saved page.",
		"what's @mum's email":                    "@mum: Email: m••• at home.ng\nIn full on the Saved page.",
		"where is @warehouse?":                   "@warehouse: Address: 12 Herbert Macaulay Way, Yaba",
		"what's the landmark at @warehouse":      "@warehouse: Landmark: opposite the filling station",
		"what's @bayo's account number?":         "@bayo: Account number: ••6789\nIn full on the Saved page.",
	} {
		if said := taskiemSays(t, owner, q); said != want {
			t.Errorf("%q: %q, want %q", q, said, want)
		}
	}
	// No field named: the whole thing, as @name alone.
	if said := taskiemSays(t, owner, "tell me about @generator"); !strings.Contains(said, "Model: Mikano 20") || strings.Contains(said, "SN-99887766") {
		t.Errorf("whole: %q", said)
	}
	// The serial in full is never said.
	if said := taskiemSays(t, owner, "what is @generator's serial"); strings.Contains(said, "SN-99887766") {
		t.Errorf("serial in full: %q", said)
	}
	// Another question that names it is not about it: it goes on.
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "phone-shop", "title": "Phone shop", "kind": "place", "details": map[string]any{"line": "3 Broad Street", "phone": "0803 112 0011"}})
	if said := taskiemSays(t, owner, "how many customers does @generator have"); strings.Contains(said, "@generator") {
		t.Errorf("another question taken: %q", said)
	}
	// A field's word in the name is not a field asked.
	if said := taskiemSays(t, owner, "what's @phone-shop?"); !strings.Contains(said, "3 Broad Street") || strings.Contains(said, "@phone-shop: Phone:") {
		t.Errorf("name read as a field: %q", said)
	}
	// No saved thing by the name: not this.
	if said := taskiemSays(t, owner, "what's @nobody's phone?"); strings.Contains(said, "@nobody:") {
		t.Errorf("nobody: %q", said)
	}
}

package api_test

import (
	"strings"
	"testing"
)

// Tables made and changed in chat (decision 0060, R3): a known table or
// one from the owner's words, read back and made after yes; a column
// added with its type in plain words; a table renamed; dropping and
// deleting sent to the web; a viewer refused; a kept table untouched.
func TestChatMakesAndChangesTables(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on, vn = "+2348010000009", "+2348010000005"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@acme.test", "viewer")
	w.bind(t, viewer, vn)

	if r := w.say(t, vn, "new table"); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "tables"); !strings.Contains(r, "*new table*") {
		t.Errorf("no tables yet offers new table: %q", r)
	}

	// A known one, picked by number.
	if r := w.say(t, on, "new table"); !strings.Contains(r, "1. Customers") || !strings.Contains(r, "2. Staff") || !strings.Contains(r, "3. Something else") {
		t.Fatalf("pick: %q", r)
	}
	r := w.say(t, on, "1")
	if !strings.Contains(r, "*Customers* will keep") || !strings.Contains(r, "Phone: a phone number") || !strings.Contains(r, "Agreed to marketing: yes or no") {
		t.Fatalf("read back: %q", r)
	}
	all := ""
	for _, m := range w.send(t, on, map[string]any{"type": "text", "text": map[string]any{"body": "yes"}}) {
		all += m.Text + "\n"
	}
	if !strings.Contains(all, "Setting up your records") || !strings.Contains(all, "Made *Customers*") || !strings.Contains(all, "*add to customers*") {
		t.Fatalf("made: %s", all)
	}
	cols := tableColumns(t, owner, "customers")
	if strings.Join(cols, ",") != "name,phone,email,area,marketing_opt_in,email_status" {
		t.Errorf("customers: %v", cols)
	}

	// One of the owner's own words: name, then columns typed from words,
	// changed by sending the list again, made on yes.
	if r := w.say(t, on, "new table"); !strings.Contains(r, "1. Staff") || strings.Contains(r, "Customers") {
		t.Fatalf("made ones are not offered again: %q", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "What is the list called?") {
		t.Fatalf("something else: %q", r)
	}
	if r := w.say(t, on, "Debtors"); !strings.Contains(r, "What do you write down for each one in Debtors?") {
		t.Fatalf("name: %q", r)
	}
	if r := w.say(t, on, "name, phone number, amount owed, due date"); !strings.Contains(r, "1. Name: words") || !strings.Contains(r, "2. Phone number: a phone number") ||
		!strings.Contains(r, "3. Amount owed: a number") || !strings.Contains(r, "4. Due date: a date") {
		t.Fatalf("columns: %q", r)
	}
	if r := w.say(t, on, "name, phone, balance, paid"); !strings.Contains(r, "4. Paid: yes or no") {
		t.Fatalf("columns again: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Made *Debtors*") || strings.Contains(r, "Setting up") {
		t.Fatalf("made own: %q", r)
	}
	if got := strings.Join(tableColumns(t, owner, "debtors"), ","); got != "name,phone,balance,paid" {
		t.Errorf("debtors: %v", got)
	}
	if r := w.say(t, on, "new table debtors"); !strings.Contains(r, "already have a table called Debtors") {
		t.Errorf("again: %q", r)
	}

	// A column: the type asked in plain words, or read from the words.
	if r := w.say(t, on, "add column notes to debtors"); !strings.Contains(r, "What goes in *notes*?") || !strings.Contains(r, "1. words") {
		t.Fatalf("add column: %q", r)
	}
	if r := w.say(t, on, "1"); !strings.Contains(r, "Add the column *notes* (words) to *Debtors*?") {
		t.Fatalf("type: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Added *notes* to *Debtors*") {
		t.Fatalf("added: %q", r)
	}
	if r := w.say(t, on, "add column due date to debtors"); !strings.Contains(r, "*due_date* (a date)") {
		t.Fatalf("typed from words: %q", r)
	}
	w.say(t, on, "yes")
	if got := strings.Join(tableColumns(t, owner, "debtors"), ","); got != "name,phone,balance,paid,notes,due_date" {
		t.Errorf("debtors after columns: %v", got)
	}
	if r := w.say(t, on, "add column notes to debtors"); !strings.Contains(r, "already has a column notes") {
		t.Errorf("twice: %q", r)
	}

	// Renamed: the title only.
	if r := w.say(t, on, "rename debtors to People who owe me"); !strings.Contains(r, "Debtors is now called *People who owe me*") {
		t.Fatalf("rename: %q", r)
	}
	if r := w.say(t, on, "tables"); !strings.Contains(r, "People who owe me") {
		t.Errorf("renamed in the list: %q", r)
	}

	// Dropping and deleting are the web's (from another builder's number,
	// since the owner's has sent many messages just now).
	const bn = "+2348010000007"
	w.bind(t, w.member(t, owner, "bob@acme.test", "builder"), bn)
	for _, msg := range []string{"delete table debtors", "drop column notes from debtors"} {
		if r := w.say(t, bn, msg); !strings.Contains(r, "type its name to confirm") || !strings.Contains(r, waPublicURL+"/tables") {
			t.Errorf("%s: %q", msg, r)
		}
	}
	if got := strings.Join(tableColumns(t, owner, "debtors"), ","); got != "name,phone,balance,paid,notes,due_date" {
		t.Errorf("nothing dropped: %v", got)
	}
}

// tableColumns are a table's columns, in order, from the API.
func tableColumns(t *testing.T, c *client, name string) []string {
	t.Helper()
	for _, x := range c.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		m := x.(map[string]any)
		if m["name"] != name {
			continue
		}
		var out []string
		for _, col := range m["columns"].([]any) {
			out = append(out, col.(map[string]any)["name"].(string))
		}
		return out
	}
	t.Fatalf("no table %s", name)
	return nil
}

// The records project could not be set up: the table agreed waits in the
// chat with Try again, and is made when it is tried again (§9.1).
func TestChatTableWaitsForItsProject(t *testing.T) {
	w := newWATablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)
	// The platform's Xenibase organisation is full; room is made later.
	w.pg.ProjectQuota(w.pg.OrgID, 0)

	w.say(t, on, "new table customers")
	all := ""
	for _, m := range w.send(t, on, map[string]any{"type": "text", "text": map[string]any{"body": "yes"}}) {
		all += m.Text + "\n"
	}
	if !strings.Contains(all, "could not set up your records") || !strings.Contains(all, "*try again*") {
		t.Fatalf("failed: %s", all)
	}
	if r := w.say(t, on, "hello there"); !strings.Contains(r, "Reply *try again* to make Customers") {
		t.Fatalf("waiting: %q", r)
	}
	w.pg.ProjectQuota(w.pg.OrgID, 5)
	all = ""
	for _, m := range w.send(t, on, map[string]any{"type": "text", "text": map[string]any{"body": "try again"}}) {
		all += m.Text + "\n"
	}
	if !strings.Contains(all, "Made *Customers*") {
		t.Fatalf("again: %s", all)
	}
	if got := strings.Join(tableColumns(t, owner, "customers"), ","); got != "name,phone,email,area,marketing_opt_in,email_status" {
		t.Errorf("customers: %v", got)
	}
}

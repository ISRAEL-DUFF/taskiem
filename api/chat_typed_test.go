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
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A typed command counts only where it was read as that command, typed by
// the person with nothing else waiting (chat-menus.md §6.1, review of H4):
// words answering a question are not a use; a message the model reads
// after asking is counted once, as the model's pick; "@Bisi …" counts when
// the task is made, not when it is only asked about or refused.
func TestTypedCommandsCountOnlyWhenRead(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		if strings.Contains(p, "something about bills for Kemi") && strings.Contains(p, "15,000 for cloth") {
			return &ai.Response{Text: `{"kind":"action","id":"money.invoice","pick":"","values":[{"field":"who","value":"Kemi"},` +
				`{"field":"amount","value":"15,000"},{"field":"for","value":"cloth"}],"command":"","reply":""}`}, nil
		}
		if strings.Contains(p, "something about bills for Kemi") {
			return &ai.Response{Text: `{"kind":"ask","id":"","pick":"","values":[],"command":"","reply":"How much, and for what?"}`}, nil
		}
		return &ai.Response{Text: "not json"}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const ownerOn, cashOn = "+2348031239531", "+2348031239532"
	w.bind(t, owner, ownerOn)
	cashier := w.member(t, owner, "cash@acme.test", "cashier")
	w.bind(t, cashier, cashOn)
	cashID := cashier.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = 'Bisi' WHERE id = $1`, cashID); err != nil {
		t.Fatal(err)
	}
	uses := func(action string) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(uses), 0) FROM chat_action_uses WHERE action = $1`, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Words answering a question are its answer, not a command typed.
	if r := w.send(t, ownerOn, whatsapptest.ListReply("act:staff.give", "Give someone a task")); len(r) != 1 || !strings.Contains(r[0].Text, "Who is it for?") {
		t.Fatalf("asked: %+v", r)
	}
	w.say(t, ownerOn, "invoice Ada 25,000 for 5 bags of rice")
	w.say(t, ownerOn, "cancel")
	if n := uses("money.invoice"); n != 0 {
		t.Fatalf("an answer counted as typed: %d", n)
	}
	if n := uses("staff.give"); n != 1 {
		t.Fatalf("the press: %d", n)
	}

	// The model asked, the answer read with the message: one use, the model's.
	if r := w.say(t, ownerOn, "something about bills for Kemi"); !strings.Contains(r, "How much, and for what?") {
		t.Fatalf("the model's question: %q", r)
	}
	w.say(t, ownerOn, "invoice 15,000 for cloth")
	w.say(t, ownerOn, "cancel")
	if n := uses("money.invoice"); n != 1 {
		t.Fatalf("counted %d times, want once", n)
	}

	// "@Bisi …": asked what about, nothing; made, one use.
	w.say(t, ownerOn, "@Bisi")
	if n := uses("staff.give"); n != 1 {
		t.Fatalf("a task only asked about: %d", n)
	}
	if r := w.say(t, ownerOn, "@Bisi clean the freezer by 5pm"); !strings.Contains(r, "Make it a task for Bisi") {
		t.Fatalf("proposed: %q", r)
	}
	if n := uses("staff.give"); n != 1 {
		t.Fatalf("counted before it was made: %d", n)
	}
	if r := w.say(t, ownerOn, "yes"); !strings.Contains(r, "Task given to Bisi") {
		t.Fatalf("made: %q", r)
	}
	if n := uses("staff.give"); n != 2 {
		t.Fatalf("a task made: %d", n)
	}
	// Refused: a cashier may not give tasks, so nothing counts.
	w.say(t, cashOn, "@Bisi clean the freezer by 5pm")
	w.say(t, cashOn, "yes")
	if n := uses("staff.give"); n != 2 {
		t.Fatalf("a refused task counted: %d", n)
	}
	// Typed in full with nothing waiting: counted.
	w.say(t, ownerOn, "invoice Ada 25,000 for 5 bags of rice")
	w.say(t, ownerOn, "cancel")
	if n := uses("money.invoice"); n != 2 {
		t.Fatalf("typed: %d", n)
	}
}

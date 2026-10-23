package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Invoices and the money log in the person's language (T8): the draft
// command words and Yes in Yoruba and Hausa, English's still understood.
func TestChatMoneyWordsInOtherLanguages(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO, lang.HA}}
	ctx := context.Background()
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const yn, hn = "+2348010000041", "+2348010000043"
	w.bind(t, owner, yn)
	w.say(t, yn, "language yoruba")
	ha := func(id string, kv ...string) string { return lang.Default().Text(lang.HA, id, kv...) }

	// Yoruba: an invoice with "ìwé owó … fún …", sent on "bẹ́ẹ̀ni".
	if r := w.say(t, yn, "ìwé owó Bola 25,000 fún àpò ìrẹsì 5"); !strings.Contains(r, yo("wa.invoice.contact", "name", "Bola")) {
		t.Fatalf("yo start: %q", r)
	}
	if r := w.say(t, yn, "0803 123 4567"); !strings.Contains(r, "₦25,000") || !strings.Contains(r, "àpò ìrẹsì 5") {
		t.Fatalf("yo confirm: %q", r)
	}
	if r := w.say(t, yn, "bẹ́ẹ̀ni"); !strings.Contains(r, "INV-0001") {
		t.Fatalf("yo sent: %q", r)
	}
	x := owner.must(200, "GET", "/v1/invoices/INV-0001", nil)["invoice"].(map[string]any)
	if x["status"] != "sent" || x["total_kobo"] != float64(2500000) || x["lines"].([]any)[0].(map[string]any)["description"] != "àpò ìrẹsì 5" {
		t.Errorf("invoice %v", x)
	}
	// An expense, typed without accents, recorded on "beeni".
	if r := w.say(t, yn, "mo na 12,000 fun diesel"); !strings.Contains(r, yo("wa.money.confirm_expense", "amount", "₦12,000", "what", "diesel", "category", "fuel")) {
		t.Fatalf("yo expense: %q", r)
	}
	if r := w.say(t, yn, "beeni"); !strings.Contains(r, yo("wa.money.recorded_out", "amount", "₦12,000", "category", "fuel")) {
		t.Fatalf("yo recorded: %q", r)
	}
	// English words still work in Yoruba.
	if r := w.say(t, yn, "cash in 5,000 till"); !strings.Contains(r, yo("wa.money.confirm_cash_in", "amount", "₦5,000", "what", "till")) {
		t.Errorf("English in Yoruba: %q", r)
	}
	if r := w.say(t, yn, "rárá"); !strings.Contains(r, yo("wa.money.not_recorded")) {
		t.Errorf("yo no: %q", r)
	}

	// Hausa: cash in, recorded on "na'am".
	op := w.member(t, owner, "musa@adafoods.test", "operator")
	w.bind(t, op, hn)
	w.say(t, hn, "language hausa")
	if r := w.say(t, hn, "kuɗi ya shigo 20,000 till"); !strings.Contains(r, ha("wa.money.confirm_cash_in", "amount", "₦20,000", "what", "till")) {
		t.Fatalf("ha cash in: %q", r)
	}
	if r := w.say(t, hn, "na'am"); !strings.Contains(r, ha("wa.money.recorded_in", "amount", "₦20,000", "category", "cash")) {
		t.Fatalf("ha recorded: %q", r)
	}
	// Without an amount the words are ordinary chat, not a command.
	if r := w.say(t, hn, "na kashe kudi da yawa"); strings.Contains(r, ha("wa.money.how")) {
		t.Errorf("ha ordinary chat read as a command: %q", r)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE source = 'entered'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("entries: %d", n)
	}
}

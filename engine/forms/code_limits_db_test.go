package forms_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/forms"
)

// TestCodesForStrangersDoNotLockAForm (E10): a few connections asking a
// verified form for codes to strangers' numbers neither lock genuine
// respondents out nor text strangers without end. Written against the
// four-argument function first, where twenty such codes had the next
// respondent refused.
func TestCodesForStrangersDoNotLockAForm(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(intake)
	var endpoint uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM form_endpoints WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	pub := w.env.DB.PublicPool(t, 1)
	scope := endpoint.String()
	asker := func(n int) []byte { h := sha256.Sum256(fmt.Appendf(nil, "asker %d", n)); return h[:] }
	ask := func(who int, phone string) string {
		t.Helper()
		payload, _ := w.svc.Keys.Seal(forms.CodeAAD(scope), forms.CodeRequest{Channel: "sms", Address: phone, Code: "123456"})
		var out string
		if err := pub.QueryRow(ctx, `SELECT taskiem_public_form_code_ask($1, $2, $3, $4, $5, $6)`, endpoint, w.svc.Keys.AddressHash(scope, phone),
			w.svc.Keys.CodeHash(scope, "123456"), payload, asker(who), w.svc.Keys.AddressHash("any form", phone)).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	count := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_codes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Three connections ask for codes to random numbers, ten times each:
	// each gets three, and no more for ten minutes.
	n := 0
	for who := range 3 {
		for range 10 {
			n++
			ask(who, fmt.Sprintf("+23480300000%02d", n))
		}
	}
	if got := count(); got != 9 {
		t.Fatalf("three connections had %d codes sent to strangers; want 9", got)
	}
	if out := ask(0, "+2348030000999"); out != "asker" {
		t.Fatalf("a fourth code for one connection: %q", out)
	}
	// A genuine respondent is served.
	if out := ask(100, "+2348031234567"); out != "ok" {
		t.Fatalf("a genuine respondent: %q", out)
	}

	// Many connections, one code each, none confirmed: past the form's
	// ceiling it is pressed, not locked. Someone new still gets a code;
	// one with a code waiting is told to wait.
	for who := 200; count() < 20; who++ {
		if out := ask(who, fmt.Sprintf("+234803100%04d", who)); out != "ok" {
			t.Fatalf("under the ceiling: %q", out)
		}
	}
	if out := ask(500, "+2348032220001"); out != "pressed" {
		t.Fatalf("someone new, the form over its ceiling: %q", out)
	}
	if out := ask(500, "+2348032220002"); out != "busy" {
		t.Fatalf("the same connection again, the form over its ceiling: %q", out)
	}
	// The old function, which names no asker, keeps a ceiling and nothing
	// past it.
	var ok bool
	payload, _ := w.svc.Keys.Seal(forms.CodeAAD(scope), forms.CodeRequest{Channel: "sms", Address: "+2348032220003", Code: "123456"})
	if err := pub.QueryRow(ctx, `SELECT taskiem_public_form_code($1, $2, $3, $4)`, endpoint, w.svc.Keys.AddressHash(scope, "+2348032220003"),
		w.svc.Keys.CodeHash(scope, "123456"), payload).Scan(&ok); err != nil || ok {
		t.Fatalf("the old function over the ceiling: %v %v", ok, err)
	}
	// Confirmed codes raise the ceiling: a form in real use is not pressed
	// by its own respondents.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_codes SET verified_at = now() WHERE scope = $1`, endpoint); err != nil {
		t.Fatal(err)
	}
	if out := ask(501, "+2348032220004"); out != "ok" {
		t.Fatalf("after the codes were confirmed: %q", out)
	}

	// One number is sent at most six codes a day, whoever asks and on
	// whichever form.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_codes SET created_at = created_at - interval '2 hours', verified_at = now()`); err != nil {
		t.Fatal(err)
	}
	const victim = "+2348035550000"
	sent := 0
	for who := 600; who < 620; who++ {
		if out := ask(who, victim); out == "ok" {
			sent++
			// Past this form's own three in ten minutes for the address.
			if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_codes SET created_at = created_at - interval '11 minutes' WHERE created_at > now() - interval '10 minutes'`); err != nil {
				t.Fatal(err)
			}
		} else if out != "number" {
			t.Fatalf("to one number: %q", out)
		}
	}
	if sent != 6 {
		t.Fatalf("one number was sent %d codes in a day; want 6", sent)
	}
}

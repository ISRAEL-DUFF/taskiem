package api_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// An owner sees November's PAYE and pension schedules, with each person's
// line, and the settings that decided them, without opening Payrolla:
// pension PINs and PFAs masked until a passkey reveals them (recorded),
// a spreadsheet made when asked; payroll.read and no one else; without
// reports:read the owner is asked to reconnect; and no PIN is kept in
// Taskiem's audit, history or logs.
func TestPayrollSettingsAndSchedules(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	var logs bytes.Buffer
	w.srv.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	f := w.fake
	f.ServeReports()
	payrollWorkers(f)
	f.Runs().SeparationOfDuties = true
	pins := map[string]payrollatest.PensionAccount{
		zainabE: {PFA: "STANBIC_IBTC", PIN: "PEN100012345678"},
		musaE:   {PFA: "STANBIC_IBTC", PIN: "PEN100087654321"},
		adaE:    {PFA: "ARM_PENSIONS", PIN: "PEN200011112222"},
	}
	for id, a := range pins {
		f.Reports().Pension[id] = a
	}
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	if back, _ := w.connect(t, owner, map[string]any{"mode": "test"}); back.Get("linked") == "" {
		t.Fatalf("connect: %v", back)
	}
	owner.must(201, "PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"payroll.prepare"}})
	clerk := w.member(t, owner, "clerk@amara.test", "clerk")
	cashier := w.member(t, owner, "cashier@amara.test", "cashier")
	num := "+2348031119101"
	w.bind(t, owner, num) // before a passkey, which binding would then ask for

	// The settings, in plain words.
	got := owner.must(200, "GET", "/v1/payroll/settings", nil)["settings"].(map[string]any)
	words := toJSON(got)
	for _, want := range []string{"Every month", "Lagos State", "On: whoever prepares a payroll can't approve it", "You add money to your Payrolla wallet before pay day",
		"On file", "8% from each person, 10% from you", "2.5% of basic pay", "Not in force"} {
		if !strings.Contains(words, want) {
			t.Fatalf("settings lack %q: %s", want, words)
		}
	}
	if got["separation_of_duties"] != true || !strings.HasSuffix(got["change_url"].(string), "/settings") {
		t.Fatalf("settings: %v", got)
	}
	f.Reports().PAYETINSet = false
	if words := toJSON(clerk.must(200, "GET", "/v1/payroll/settings", nil)); !strings.Contains(words, "Payrolla needs it to file your tax") {
		t.Fatalf("no tax number: %s", words)
	}
	cashier.must(403, "GET", "/v1/payroll/settings", nil)
	cashier.must(403, "GET", "/v1/payroll/schedules", nil)

	// No schedules until a payroll is locked; then PAYE, pension (one a
	// PFA) and NHF, each with its total.
	if list := owner.must(200, "GET", "/v1/payroll/schedules", nil)["schedules"].([]any); len(list) != 0 {
		t.Fatalf("schedules before: %v", list)
	}
	run := f.AddLockedRun("2026-11", "")
	list := owner.must(200, "GET", "/v1/payroll/schedules", nil)["schedules"].([]any)
	if len(list) != 4 {
		t.Fatalf("schedules: %v", list)
	}
	first := list[0].(map[string]any)
	if first["kind"] != "paye" || first["title"] != "Tax (PAYE)" || first["to"] != "Lagos State Internal Revenue Service" || first["period_words"] != "November 2026" ||
		first["state"] != "to_pay" || first["when"] != "Due 10 Dec" || first["people"] != float64(3) || first["run_id"] != run {
		t.Fatalf("PAYE: %v", first)
	}
	var pension map[string]any
	for _, x := range list {
		if m := x.(map[string]any); m["kind"] == "pension" && m["people"] == float64(2) {
			pension = m
		}
	}
	if pension == nil || strings.Contains(toJSON(list), "STANBIC") || strings.Contains(toJSON(list), "PEN1") {
		t.Fatalf("pension in the list: %v", list)
	}

	// One schedule: a line per person; PINs and PFAs masked.
	paye := clerk.must(200, "GET", "/v1/payroll/schedules/"+first["id"].(string), nil)
	if lines := paye["lines"].([]any); len(lines) != 3 || paye["masked"] != false || lines[0].(map[string]any)["pin"] != nil {
		t.Fatalf("PAYE lines: %v", paye)
	}
	pid := pension["id"].(string)
	one := owner.must(200, "GET", "/v1/payroll/schedules/"+pid, nil)
	if one["masked"] != true || strings.Contains(toJSON(one), "PEN100012345678") || strings.Contains(toJSON(one), "STANBIC") ||
		!strings.Contains(toJSON(one), "•••• 5678") || !strings.Contains(toJSON(one), "•••• IBTC") {
		t.Fatalf("masked: %v", one)
	}
	owner.must(404, "GET", "/v1/payroll/schedules/00000000-0000-4000-8000-999999999999", nil)
	cashier.must(403, "GET", "/v1/payroll/schedules/"+pid, nil)

	// In full only after a passkey asked for this schedule; recorded.
	if e := owner.must(403, "POST", "/v1/payroll/schedules/"+pid+"/reveal", nil); e["step_up"] != "passkey" || e["operation"] != "payroll.reveal" || e["target"] != pid {
		t.Fatalf("reveal asks for a passkey: %v", e)
	}
	key := addPasskey(t, owner, "Phone")
	other := challengeFrom(t, owner.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": "payroll.reveal", "target": first["id"]}))
	owner.must(403, "POST", "/v1/payroll/schedules/"+pid+"/reveal", map[string]any{"passkey": key.Assert(other).JSON()})
	owner.must(400, "POST", "/v1/payroll/schedules/"+first["id"].(string)+"/reveal", nil)
	ch := challengeFrom(t, owner.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": "payroll.reveal", "target": pid}))
	full := owner.must(200, "POST", "/v1/payroll/schedules/"+pid+"/reveal", map[string]any{"passkey": key.Assert(ch).JSON()})
	if full["masked"] != false || !strings.Contains(toJSON(full), "PEN100012345678") || !strings.Contains(toJSON(full), "STANBIC_IBTC") {
		t.Fatalf("revealed: %v", full)
	}
	// The same passkey answer works once.
	owner.must(403, "POST", "/v1/payroll/schedules/"+pid+"/reveal", map[string]any{"passkey": key.Assert(ch).JSON()})

	// A spreadsheet: PAYE's when asked; a pension one's with a passkey.
	csv := clerk.must(200, "POST", "/v1/payroll/schedules/"+first["id"].(string)+"/download", nil)
	if csv["filename"] != "tax-paye-november-2026.csv" || !strings.HasPrefix(csv["csv"].(string), "Name,Amount (NGN)\n") || !strings.Contains(csv["csv"].(string), "Zainab Bello,") ||
		!strings.Contains(csv["csv"].(string), "\nTotal,") {
		t.Fatalf("csv: %v", csv)
	}
	owner.must(403, "POST", "/v1/payroll/schedules/"+pid+"/download", nil)
	ch = challengeFrom(t, owner.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": "payroll.reveal", "target": pid}))
	pcsv := owner.must(200, "POST", "/v1/payroll/schedules/"+pid+"/download", map[string]any{"passkey": key.Assert(ch).JSON()})
	if !strings.Contains(pcsv["csv"].(string), "PFA,Pension PIN") || !strings.Contains(pcsv["csv"].(string), "PEN100087654321") {
		t.Fatalf("pension csv: %v", pcsv)
	}

	// Recorded, with no PIN, PFA or amount; and nothing of them kept.
	var reveals, downloads int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action = 'payroll.schedule.revealed'), count(*) FILTER (WHERE action = 'payroll.schedule.downloaded')
		FROM audit_log WHERE target = $1 OR target = $2`, "payrolla-schedule:"+pid, "payrolla-schedule:"+first["id"].(string)).Scan(&reveals, &downloads); err != nil || reveals != 1 || downloads != 2 {
		t.Fatalf("recorded: %d reveals, %d downloads, %v", reveals, downloads, err)
	}
	for _, q := range []string{`SELECT count(*) FROM audit_log WHERE detail::text ~ 'PEN[0-9]|STANBIC|ARM_PENSIONS|amount'`} {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}

	// The chat: "payroll settings", and "pension schedule for november"
	// with its total and a link, never a line.
	w.send(t, num, whatsapptest.Text("payroll settings"))
	if s := lastSent(t, w.graph.Sent(num)); !strings.Contains(s.Text, "How often people are paid: Every month") || !strings.Contains(s.Text, "/payroll/settings") {
		t.Fatalf("settings in the chat: %q", s.Text)
	}
	w.send(t, num, whatsapptest.Text("pension schedule for november"))
	s := lastSent(t, w.graph.Sent(num))
	if !strings.Contains(s.Text, "Your pension schedule for November 2026: ₦") || !strings.Contains(s.Text, "3 people") || !strings.Contains(s.Text, "To pay by 7 Dec") ||
		!strings.Contains(s.Text, "/payroll/schedules") || strings.Contains(s.Text, "PEN") || strings.Contains(s.Text, "Zainab") {
		t.Fatalf("pension schedule in the chat: %q", s.Text)
	}
	w.send(t, num, whatsapptest.Text("nhis schedule for november"))
	if s := lastSent(t, w.graph.Sent(num)); !strings.Contains(s.Text, "There's no NHIS schedule for November 2026 yet") {
		t.Fatalf("no NHIS: %q", s.Text)
	}
	w.send(t, num, whatsapptest.Text("paye schedule"))
	if s := lastSent(t, w.graph.Sent(num)); !strings.Contains(s.Text, "Your PAYE schedule for November 2026") || !strings.Contains(s.Text, "/payroll/schedules/"+first["id"].(string)) {
		t.Fatalf("PAYE in the chat: %q", s.Text)
	}

	// Paid: said as paid.
	f.Reports().Paid[run] = true
	if l := owner.must(200, "GET", "/v1/payroll/schedules", nil)["schedules"].([]any); l[0].(map[string]any)["state"] != "paid" || l[0].(map[string]any)["when"] != "Paid 1 Dec" {
		t.Fatalf("paid: %v", l[0])
	}

	// Without reports:read: the settings still; the schedules in words.
	f.SetScopes(f.OAuthConn(), "payroll:read", "employees:read", "payroll:write", "payroll:approve")
	owner.must(200, "GET", "/v1/payroll/settings", nil)
	if e := owner.must(409, "GET", "/v1/payroll/schedules", nil); e["code"] != "scope_required" ||
		e["error"] != "Payrolla hasn't allowed Taskiem to read your tax and pension schedules. Reconnect Payrolla and allow Read statutory reports." {
		t.Fatalf("owner without reports:read: %v", e)
	}
	if e := clerk.must(409, "GET", "/v1/payroll/schedules/"+pid, nil); e["error"] != "Ask the owner to allow Read statutory reports in Payrolla." {
		t.Fatalf("others without reports:read: %v", e)
	}
	w.send(t, num, whatsapptest.Text("pension schedule for november"))
	if s := lastSent(t, w.graph.Sent(num)); !strings.Contains(s.Text, "Reconnect Payrolla and allow Read statutory reports") {
		t.Fatalf("chat without reports:read: %q", s.Text)
	}

	w.srv.WaitBackground()
	for _, secret := range []string{"PEN100012345678", "PEN100087654321", "PEN200011112222", "STANBIC_IBTC"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("a PIN or PFA logged: %s", logs.String())
		}
	}
}

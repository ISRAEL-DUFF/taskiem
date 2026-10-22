package api_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
)

const failingFlow = `{"schema":"wd/v1","id":"wf_payroll","version":1,"name":"Payroll","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"ok":{"type":"boolean"}}}},
 "steps":[{"id":"check","type":"transform","config":{"output":{"x":"=trigger.body.ok ? 1 : int('not a number')"}}}]}`

// outcomes runs the alerter's pass over how runs ended.
func outcomes(t *testing.T, w *world) {
	t.Helper()
	a := &alerts.Alerter{Pool: w.env.Store.Pool, Logger: slog.New(slog.DiscardHandler), PublicURL: "https://taskiem.test"}
	if err := a.Outcomes(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func notifications(t *testing.T, c *client) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, n := range c.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		out = append(out, n.(map[string]any))
	}
	return out
}

// Notifications (no-technical-steps N12): with nothing set up, a failed run
// tells the owners, whoever started it and whoever published its workflow,
// once each, repeats counted; a run a member started that finished tells
// them; an owner alert is in the owners' bell; each person reads only
// their own, and marks them read.
func TestNotifications(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	kim := addMember(t, w, owner, "kim", "operator")
	viewer := addMember(t, w, owner, "vic", "viewer")
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Payroll", "definition": json.RawMessage(failingFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	// Kim runs it and it fails twice.
	for range 2 {
		kim.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ok": false}})
		w.env.Drain(t)
	}
	outcomes(t, w)
	for name, c := range map[string]*client{"owner": owner, "kim": kim} {
		list := notifications(t, c)
		if len(list) != 1 || list[0]["kind"] != "run_failed" || list[0]["level"] != "problem" || list[0]["count"] != float64(2) ||
			list[0]["title"] != "Payroll failed 2 times" || !strings.HasPrefix(list[0]["link"].(string), "/runs/") {
			t.Fatalf("%s: %v", name, list)
		}
		if got := c.must(200, "GET", "/v1/notifications/count", nil); got["unread"] != float64(1) {
			t.Fatalf("%s count: %v", name, got)
		}
	}
	if list := notifications(t, viewer); len(list) != 0 {
		t.Fatalf("someone not involved was told: %v", list)
	}

	// Kim's run that works tells Kim, not the owners.
	kim.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ok": true}})
	w.env.Drain(t)
	outcomes(t, w)
	if list := notifications(t, kim); len(list) != 2 || list[0]["kind"] != "run_finished" || list[0]["title"] != "Your run of Payroll finished" {
		t.Fatalf("kim after a finished run: %v", list)
	}
	if list := notifications(t, owner); len(list) != 1 {
		t.Fatalf("the owner heard of kim's finished run: %v", list)
	}

	// An owner alert is in the owners' bell.
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := alerts.TellOwners(context.Background(), tx, tenant, alerts.Alert{Kind: alerts.DomainUnverified, Dedup: "shop.example", Title: "shop.example is no longer verified",
			Body: "Add the TXT record again.", Link: "/storefront/domains"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	list := notifications(t, owner)
	if len(list) != 2 || list[0]["kind"] != "setup" || list[0]["level"] != "warning" || list[0]["link"] != "/storefront/domains" {
		t.Fatalf("owner after an alert: %v", list)
	}

	// Read: one, then all; a new failure after reading is a new item.
	owner.must(200, "POST", "/v1/notifications/read", map[string]any{"ids": []string{list[0]["id"].(string)}})
	if got := owner.must(200, "GET", "/v1/notifications/count", nil); got["unread"] != float64(1) {
		t.Fatalf("after reading one: %v", got)
	}
	if got := owner.must(200, "POST", "/v1/notifications/read", map[string]any{"all": true}); got["read"] != float64(1) {
		t.Fatalf("read all: %v", got)
	}
	// Kim's own are untouched by the owner's reading.
	if got := kim.must(200, "GET", "/v1/notifications/count", nil); got["unread"] != float64(2) {
		t.Fatalf("kim's count after the owner read: %v", got)
	}
	kim.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ok": false}})
	w.env.Drain(t)
	outcomes(t, w)
	if list := notifications(t, owner); len(list) != 3 || list[0]["count"] != float64(1) || list[0]["read_at"] != nil {
		t.Fatalf("a failure after reading: %v", list)
	}

	// Settings: defaults, then a change.
	got := toJSON(owner.must(200, "GET", "/v1/me/notification-settings", nil))
	if !strings.Contains(got, `{"chat":true,"email":true,"group":"failures"}`) || !strings.Contains(got, `{"chat":false,"email":false,"group":"finished"}`) {
		t.Fatalf("defaults: %s", got)
	}
	owner.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "failures", "chat": false, "email": true}}})
	if got := toJSON(owner.must(200, "GET", "/v1/me/notification-settings", nil)); !strings.Contains(got, `{"chat":false,"email":true,"group":"failures"}`) {
		t.Fatalf("after the change: %s", got)
	}
	owner.must(400, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "everything", "chat": true}}})

	// Another business sees none of it.
	other := w.tenant(t, "Other", "other@other.test")
	if list := notifications(t, other); len(list) != 0 {
		t.Fatalf("another business: %v", list)
	}
}

// An owner who linked Telegram hears of a failure there, with nothing set
// up, once per workflow and hour; turning chat off for failures stops it.
func TestFailuresReachTheOwnersChat(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	const ada = int64(6101)
	w.link(t, owner, ada, "ada")
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Payroll", "definition": json.RawMessage(failingFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	fail := func() {
		owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ok": false}})
		w.env.Drain(t)
		outcomes(t, w.world)
		w.tick(t)
	}
	before := len(w.bot.Sent(ada))
	fail()
	sent := w.bot.Sent(ada)[before:]
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "Payroll failed in prod") || !strings.Contains(sent[0].Text, "[Ada Stores]") {
		t.Fatalf("the owner's chat: %+v", sent)
	}
	fail()
	if n := len(w.bot.Sent(ada)) - before; n != 1 {
		t.Fatalf("a second failure within the hour: %d messages", n)
	}
	if list := notifications(t, owner); len(list) != 1 || list[0]["count"] != float64(2) {
		t.Fatalf("the bell: %v", list)
	}
	// Chat off for failures: another workflow's failure stays in the app.
	owner.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []map[string]any{{"group": "failures", "chat": false, "email": false}}})
	wf = owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Stock", "definition": json.RawMessage(failingFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	fail()
	if n := len(w.bot.Sent(ada)) - before; n != 1 {
		t.Fatalf("with chat off: %d messages", n)
	}
	if list := notifications(t, owner); len(list) != 2 || list[0]["title"] != "Stock failed" {
		t.Fatalf("the bell with chat off: %v", list)
	}
}

// A rule on failed runs adds its channels, not a second item in the bell.
func TestRuleOnFailuresIsNotTwiceInTheBell(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "email", "name": "Ops", "to": []string{"ada@ada.test"}})
	owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "Failures", "kind": "run_failed", "channel_ids": []string{ch["id"].(string)}})
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Payroll", "definition": json.RawMessage(failingFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ok": false}})
	w.env.Drain(t)
	a := &alerts.Alerter{Pool: w.env.Store.Pool, Logger: slog.New(slog.DiscardHandler), PublicURL: "https://taskiem.test"}
	if err := a.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM alerts WHERE rule_id IS NOT NULL AND kind = 'run_failed'`).Scan(&found); err != nil || found != 1 {
		t.Fatalf("the rule found %d (%v)", found, err)
	}
	if list := notifications(t, owner); len(list) != 1 || list[0]["title"] != "Payroll failed" {
		t.Fatalf("the bell: %v", list)
	}
}

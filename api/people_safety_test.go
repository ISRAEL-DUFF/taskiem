package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

// safetyWorld is the joined people chats with the operator console on.
func safetyWorld(t *testing.T) *peopleWorld {
	t.Helper()
	w := newJoinedWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.Ops = &api.OpsSettings{}
	return w
}

// moderator is an operator signed in with their passkey.
type moderator struct {
	*opsClient
	key *webauthntest.Authenticator
}

func newModerator(t *testing.T, w *peopleWorld, email string) moderator {
	t.Helper()
	c, a := enrolOperator(t, w.world, email)
	return moderator{c, a}
}

// write sends an operator write with a step-up for op on target.
func (m moderator) write(want int, op, target, path string, body map[string]any) map[string]any {
	m.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["step_up"] = m.stepUp(m.key, op, target)
	return m.must(want, "POST", path, body)
}

// get reads an operator page, answering its bytes.
func (m moderator) raw(path string) (int, []byte) {
	m.t.Helper()
	req, _ := http.NewRequest("GET", m.base+path, nil)
	resp, err := m.http.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type auditEntry struct {
	Actor, Target string
	Detail        map[string]any
}

// platformAudits are the platform chain's entries of an action.
func platformAudits(t *testing.T, w *peopleWorld, action string) []auditEntry {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT actor_id, target, detail FROM audit_log WHERE tenant_id = $1 AND action = $2 ORDER BY chain_seq`,
		ops.PlatformChain, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditEntry
	for rows.Next() {
		var e auditEntry
		var raw []byte
		if err := rows.Scan(&e.Actor, &e.Target, &raw); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(raw, &e.Detail)
		out = append(out, e)
	}
	return out
}

func (w *peopleWorld) adminCount(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Reports keep their evidence (§8.3): the last 20 messages sealed under
// the report's own key, the photos and files in them copied and sealed
// with it; the report outlives the chat and the reporter's account,
// detached from them. Moderators list reports (no words), open one only
// saying why (each opening, and each file, recorded with who, what, why
// and when), and mark it reviewed with their passkey; a reviewed report
// is purged a year later. Stage 2's reports of businesses are listed and
// opened the same way.
func TestPeopleReportsKeepEvidence(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031130001", "Ada Obi")
	w.older(t, adaID)
	bola, bolaID := w.person(t, "+2348031130002", "Bola Ade")
	w.older(t, bolaID)
	mod := newModerator(t, w, "mod@taskiem.test")

	bolaChat := chatWith(t, bola, adaID)
	send(t, bola, bolaChat, "Hello Ada, look at this dress")
	adaChat := requestOf(t, ada)
	photo := pngPhoto(t, 60)
	if code, f := bola.postFile(t, "/v1/me/people/"+bolaChat+"/files", "dress.png", photo, map[string]string{"text": "Only 5000 naira"}); code != 201 {
		t.Fatalf("a photo: %d %v", code, f)
	}
	rep := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/report", map[string]any{"reason": "scam", "note": "He wants money first", "consent": true})
	repID := rep["id"].(string)

	// Sealed at rest: no words of the chat, nor the photo's bytes, in the
	// report's rows.
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1 AND messages = '[]' AND sealed IS NOT NULL AND files = 1
		AND position(convert_to('dress', 'UTF8') in sealed) = 0`, repID); n != 1 {
		t.Fatal("the report's messages are not sealed")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_report_files r JOIN people_files f ON f.message_id = r.message_id AND f.person_id = $2
		WHERE r.report_id = $1 AND r.size = f.size AND r.sealed <> f.sealed`, repID, adaID); n != 1 {
		t.Fatal("the report's photo is not kept, sealed")
	}

	// The list says who and why, never the words.
	list := mod.must(200, "GET", "/v1/ops/people/reports", nil)["reports"].([]any)
	if len(list) != 1 {
		t.Fatalf("reports: %v", list)
	}
	row := list[0].(map[string]any)
	if row["reported_name"] != "Bola Ade" || row["files"] != float64(1) || row["sealed"] != true || row["from_request"] != true || row["reports_against"] != float64(1) {
		t.Fatalf("the report's row: %v", row)
	}
	if raw, _ := json.Marshal(list); bytes.Contains(raw, []byte("dress")) {
		t.Fatal("the list carries the messages' words")
	}

	// Opening needs a reason, and is recorded with it.
	if e := mod.must(400, "GET", "/v1/ops/people/reports/"+repID, nil); e["why"] != true {
		t.Fatalf("no reason: %v", e)
	}
	before := len(platformAudits(t, w, "ops.people.report.open"))
	opened := mod.must(200, "GET", "/v1/ops/people/reports/"+repID+"?why=Report+from+the+queue", nil)
	msgs := opened["messages"].([]any)
	if len(msgs) != 2 || !strings.Contains(msgs[0].(map[string]any)["body"].(string), "Only 5000 naira") || msgs[0].(map[string]any)["file"] != true {
		t.Fatalf("opened: %v", opened)
	}
	a := platformAudits(t, w, "ops.people.report.open")
	if len(a) != before+1 || a[len(a)-1].Actor != "operator:mod@taskiem.test" || a[len(a)-1].Detail["why"] != "Report from the queue" || a[len(a)-1].Target != repID {
		t.Fatalf("the opening's record: %v", a)
	}
	file := opened["files"].([]any)[0].(map[string]any)
	code, got := mod.raw("/v1/ops/people/reports/" + repID + "/files/" + file["message_id"].(string) + "?why=Look+at+the+photo")
	if code != 200 || len(got) < 100 {
		t.Fatalf("the photo: %d %d bytes", code, len(got))
	}
	if f := platformAudits(t, w, "ops.people.report.file"); len(f) != 1 || f[0].Detail["why"] != "Look at the photo" {
		t.Fatalf("the file's record: %v", f)
	}
	if code, _ := mod.raw("/v1/ops/people/reports/" + repID + "/files/" + file["message_id"].(string)); code != 400 {
		t.Fatalf("a file without a reason: %d", code)
	}

	// Bola deletes the chat, Ada her account: the report stays, detached
	// from Ada, with its evidence.
	bola.must(204, "DELETE", "/v1/me/people/"+bolaChat, nil)
	w.deleteAccount(t, ada, "+2348031130001")
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1 AND person_id IS NULL AND detached_at IS NOT NULL`, repID); n != 1 {
		t.Fatal("the report did not outlive its reporter, detached")
	}
	again := mod.must(200, "GET", "/v1/ops/people/reports/"+repID+"?why=After+the+account+went", nil)
	if len(again["messages"].([]any)) != 2 || len(again["files"].([]any)) != 1 || again["report"].(map[string]any)["reporter_id"] != nil {
		t.Fatalf("after deletion: %v", again)
	}

	// Reviewed with a passkey (403 without), once.
	body := map[string]any{"note": "Warned him"}
	if e := mod.must(403, "POST", "/v1/ops/people/reports/"+repID+"/review", body); e["step_up"] != "passkey" {
		t.Fatalf("no step-up: %v", e)
	}
	mod.write(200, "ops.people.report.review", repID, "/v1/ops/people/reports/"+repID+"/review", map[string]any{"note": "Warned him"})
	mod.write(409, "ops.people.report.review", repID, "/v1/ops/people/reports/"+repID+"/review", nil)
	if l := mod.must(200, "GET", "/v1/ops/people/reports?status=reviewed", nil)["reports"].([]any); len(l) != 1 ||
		l[0].(map[string]any)["reviewed_by"] != "operator:mod@taskiem.test" {
		t.Fatalf("reviewed: %v", l)
	}

	// Kept a year after review, then purged with its files.
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1`, repID); n != 1 {
		t.Fatal("purged before a year")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_reports SET reviewed_at = now() - interval '366 days' WHERE id = $1`, repID); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT (SELECT count(*) FROM people_reports WHERE id = $1) + (SELECT count(*) FROM people_report_files WHERE report_id = $1)`, repID); n != 0 {
		t.Fatal("a reviewed report outlived its year")
	}
	if p := platformAudits(t, w, "people.safety.purge"); len(p) != 1 {
		t.Fatalf("the purge's record: %v", p)
	}

	// A report kept plain before 00480 is sealed by the safety job.
	old := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_reports (id, person_id, reported_id, reason, consent, messages) VALUES ($1, $2, $3, 'spam', true,
		'[{"from_reporter": false, "kind": "text", "body": "Buy my coins", "at": "2026-11-01T10:00:00Z"}]')`, old, bolaID, bolaID); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_reports WHERE id = $1 AND messages = '[]' AND sealed IS NOT NULL`, old); n != 1 {
		t.Fatal("an old report was not sealed")
	}
	if m := mod.must(200, "GET", "/v1/ops/people/reports/"+old.String()+"?why=Old+report", nil)["messages"].([]any); len(m) != 1 ||
		m[0].(map[string]any)["body"] != "Buy my coins" {
		t.Fatalf("an old report opened: %v", m)
	}

	// Stage 2's reports of businesses: listed, opened with a reason (and
	// recorded), reviewed; kept after the reporter's account goes.
	tolu, toluID := w.person(t, "+2348031130003", "Tolu")
	biz := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+biz+"/messages", map[string]any{"text": "Is the jollof ready?"})
	cr := tolu.must(201, "POST", "/v1/me/chats/"+biz+"/report", map[string]any{"reason": "abuse", "consent": true})["id"].(string)
	cl := mod.must(200, "GET", "/v1/ops/customer-reports", nil)["reports"].([]any)
	if len(cl) != 1 || cl[0].(map[string]any)["business"] != "Mama's Kitchen" || cl[0].(map[string]any)["messages"] == float64(0) {
		t.Fatalf("customer reports: %v", cl)
	}
	mod.must(400, "GET", "/v1/ops/customer-reports/"+cr, nil)
	co := mod.must(200, "GET", "/v1/ops/customer-reports/"+cr+"?why=Customer+report", nil)
	if !strings.Contains(string(asJSON(t, co["report"].(map[string]any)["messages"])), "jollof") {
		t.Fatalf("a customer report opened: %v", co)
	}
	if a := platformAudits(t, w, "ops.customer.report.open"); len(a) != 1 || a[0].Detail["why"] != "Customer report" {
		t.Fatalf("its record: %v", a)
	}
	mod.write(200, "ops.customer_report.review", cr, "/v1/ops/customer-reports/"+cr+"/review", nil)
	w.deleteAccount(t, tolu, "+2348031130003")
	if n := w.adminCount(t, `SELECT count(*) FROM customer_reports WHERE id = $1 AND person_id IS NULL AND status = 'reviewed'`, cr); n != 1 {
		t.Fatal("a customer report did not outlive its reporter")
	}
	_ = toluID

	// No tenant credential reaches any of it.
	req, _ := http.NewRequest("GET", w.base+"/v1/ops/people/reports", nil)
	req.Header.Set("Authorization", "Bearer "+bola.token)
	if code := statusOf(t, req); code != 401 {
		t.Fatalf("a person's session on the moderators' queue: %d", code)
	}
}

// deleteAccount deletes c's account as the person would.
func (w *peopleWorld) deleteAccount(t *testing.T, c *client, number string) {
	t.Helper()
	c.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	c.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, number)})
}

func asJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Held messages are kept for moderators (§8.4): refused to the recipient
// as before, but kept sealed with the checks' verdict; moderators see the
// held and the verdicts (no words), open one saying why (recorded),
// release a wrongly held one (delivered then, as the sender wrote it,
// with its photo) or confirm one. Undecided held messages go 90 days on,
// and with the sender's account.
func TestPeopleHeldForReview(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031130101", "Ada Obi")
	w.older(t, adaID)
	dayo, dayoID := w.person(t, "+2348031130102", "Dayo")
	w.older(t, dayoID)
	mod := newModerator(t, w, "mod@taskiem.test")

	chat := chatWith(t, dayo, adaID)
	words := "Congratulations you have won! Send me the code you received to claim your prize"
	dayo.must(403, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": words})
	if len(listOf(t, ada, "/v1/me/people/requests", "requests")) != 0 {
		t.Fatal("a held message arrived")
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_held WHERE sender_id = $1 AND recipient_id = $2 AND state = 'held' AND 'code' = ANY (reasons)
		AND position(convert_to('Congratulations', 'UTF8') in sealed) = 0`, dayoID, adaID); n != 1 {
		t.Fatal("the held message is not kept, sealed, with its reasons")
	}

	// The list and the verdicts carry no words.
	held := mod.must(200, "GET", "/v1/ops/people/held", nil)["held"].([]any)
	if len(held) != 1 || held[0].(map[string]any)["sender_name"] != "Dayo" || held[0].(map[string]any)["recipient_name"] != "Ada Obi" {
		t.Fatalf("held: %v", held)
	}
	verdicts := mod.must(200, "GET", "/v1/ops/people/verdicts", nil)["verdicts"].([]any)
	if len(verdicts) != 1 || verdicts[0].(map[string]any)["verdict"] != "hold" {
		t.Fatalf("verdicts: %v", verdicts)
	}
	if raw := asJSON(t, []any{held, verdicts}); bytes.Contains(raw, []byte("Congratulations")) {
		t.Fatal("the lists carry the words")
	}
	hid := held[0].(map[string]any)["id"].(string)
	mod.must(400, "GET", "/v1/ops/people/held/"+hid, nil)
	one := mod.must(200, "GET", "/v1/ops/people/held/"+hid+"?why=Checking+a+hold", nil)
	if one["message"].(map[string]any)["body"] != words {
		t.Fatalf("opened: %v", one)
	}
	if a := platformAudits(t, w, "ops.people.held.open"); len(a) != 1 || a[0].Detail["why"] != "Checking a hold" || a[0].Target != hid {
		t.Fatalf("its record: %v", a)
	}
	// Confirmed: nothing is delivered.
	mod.write(200, "ops.people.held.confirm", hid, "/v1/ops/people/held/"+hid+"/confirm", map[string]any{"note": "A scam"})
	mod.write(409, "ops.people.held.release", hid, "/v1/ops/people/held/"+hid+"/release", nil)
	if len(listOf(t, ada, "/v1/me/people/requests", "requests")) != 0 {
		t.Fatal("a confirmed hold arrived")
	}

	// A wrongly held one, with a photo: released, it reaches Ada as Dayo
	// wrote it, photo and all, and is in Dayo's own chat too.
	photo := pngPhoto(t, 77)
	code, e := dayo.postFile(t, "/v1/me/people/"+chat+"/files", "ticket.png", photo, map[string]string{"text": "Send me your PIN for the ticket"})
	if code != 403 {
		t.Fatalf("a held photo: %d %v", code, e)
	}
	var second string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM people_held WHERE state = 'held'`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	code, got := mod.raw("/v1/ops/people/held/" + second + "/file?why=The+photo")
	if code != 200 || len(got) < 100 {
		t.Fatalf("the held photo: %d", code)
	}
	if a := platformAudits(t, w, "ops.people.held.file"); len(a) != 1 {
		t.Fatalf("its record: %v", a)
	}
	rel := mod.write(200, "ops.people.held.release", second, "/v1/ops/people/held/"+second+"/release", map[string]any{"note": "A cinema ticket PIN"})
	if rel["state"] != "released" || rel["message_id"] == nil {
		t.Fatalf("released: %v", rel)
	}
	req := requestOf(t, ada)
	m := messagesOf(t, ada, req)
	if len(m) != 1 || m[0]["body"] != "Send me your PIN for the ticket" || m[0]["attachment"].(map[string]any)["kind"] != "photo" {
		t.Fatalf("delivered: %v", m)
	}
	if mine := messagesOf(t, dayo, chat); len(mine) != 1 || mine[0]["id"] != rel["message_id"] {
		t.Fatalf("Dayo's copy: %v", mine)
	}
	// A release retried (as if its decision had failed to be written after
	// the delivery) delivers it once.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_held SET state = 'held', decided_by = NULL, decided_at = NULL, released_message = NULL WHERE id = $1`, second); err != nil {
		t.Fatal(err)
	}
	again := mod.write(200, "ops.people.held.release", second, "/v1/ops/people/held/"+second+"/release", nil)
	if again["message_id"] != rel["message_id"] || len(messagesOf(t, ada, req)) != 1 {
		t.Fatalf("released twice: %v", again)
	}
	if l := mod.must(200, "GET", "/v1/ops/people/held?state=all", nil)["held"].([]any); len(l) != 2 {
		t.Fatalf("all held: %v", l)
	}

	// Purged 90 days after they were decided, as are the checks'
	// verdicts; an undecided one goes with the sender's account.
	verdicts0 := w.adminCount(t, `SELECT count(*) FROM people_screen_verdicts`)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_held SET decided_at = now() - interval '91 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_screen_verdicts SET at = now() - interval '91 days' WHERE ctid = (SELECT ctid FROM people_screen_verdicts LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_held`); n != 0 {
		t.Fatalf("%d held messages outlived 90 days", n)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM people_screen_verdicts`); verdicts0 == 0 || n != verdicts0-1 {
		t.Fatalf("verdicts: %d before, %d after the purge", verdicts0, n)
	}
	dayo.must(403, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": "Tell me your OTP now"})
	w.deleteAccount(t, dayo, "+2348031130102")
	if n := w.adminCount(t, `SELECT count(*) FROM people_held`); n != 0 {
		t.Fatal("an undecided hold outlived its sender's account")
	}
}

// Moderators' actions (§8.3): warn, limit, suspend and close, each with
// reason, operator and time and a passkey; the person reads their
// standing in plain words, appeals once, and moderators uphold or
// overturn. A suspension is the suspended state the chats already show
// (409 to write to them) and stops the person writing; a closure closes
// the account when its time comes with no appeal open.
func TestPeopleModeration(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031130201", "Ada Obi")
	w.older(t, adaID)
	bola, bolaID := w.person(t, "+2348031130202", "Bola Ade")
	w.older(t, bolaID)
	chidi, chidiID := w.person(t, "+2348031130203", "Chidi")
	w.older(t, chidiID)
	mod := newModerator(t, w, "mod@taskiem.test")
	second := newModerator(t, w, "second@taskiem.test")

	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["state"] != "ok" || len(s["actions"].([]any)) != 0 {
		t.Fatalf("a good standing: %v", s)
	}
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hi Bola")
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)

	// A warning: needs a reason and the passkey; said in plain words.
	target := bolaID.String()
	mod.must(403, "POST", "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "warn", "reason": "Rude messages"})
	mod.write(422, "ops.people.warn", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "warn"})
	mod.write(201, "ops.people.warn", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "warn", "reason": "Rude messages to Ada"})
	s := bola.must(200, "GET", "/v1/me/standing", nil)
	acts := s["actions"].([]any)
	if s["state"] != "warned" || s["unseen"] != float64(1) || len(acts) != 1 || acts[0].(map[string]any)["reason"] != "Rude messages to Ada" ||
		acts[0].(map[string]any)["can_appeal"] != true {
		t.Fatalf("warned: %v", s)
	}
	bola.must(204, "POST", "/v1/me/standing/seen", nil)
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["unseen"] != float64(0) {
		t.Fatalf("seen: %v", s)
	}
	if a := platformAudits(t, w, "ops.people.warn"); len(a) != 1 || a[0].Actor != "operator:mod@taskiem.test" || a[0].Detail["reason"] != "Rude messages to Ada" {
		t.Fatalf("the warning's record: %v", a)
	}

	// A limit: fewer requests (one waiting at once).
	mod.write(201, "ops.people.limit", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "limit", "reason": "Many requests", "days": 3})
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["state"] != "limited" || s["until"] == nil {
		t.Fatalf("limited: %v", s)
	}
	c1 := chatWith(t, bola, chidiID)
	send(t, bola, c1, "Hello Chidi")
	dele, deleID := w.person(t, "+2348031130204", "Dele")
	_ = dele
	c2 := chatWith(t, bola, deleID)
	bola.must(429, "POST", "/v1/me/people/"+c2+"/messages", map[string]any{"text": "Hello Dele"})

	// A suspension: Bola writes to no one; Ada's message to Bola is
	// refused as to a suspended account; Bola still reads.
	sus := mod.write(201, "ops.people.suspend", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "suspend", "reason": "Scam messages", "days": 0})
	if e := bola.must(403, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "Still here"}); !strings.Contains(e["error"].(string), "Your standing") {
		t.Fatalf("a suspended person writes: %v", e)
	}
	_, femiID := w.person(t, "+2348031130205", "Femi")
	bola.must(403, "POST", "/v1/me/people", map[string]any{"person_id": femiID})
	ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Bola?"})
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["suspended"] != true {
		t.Fatalf("Ada's chat: %v", c)
	}
	messagesOf(t, bola, bolaChat)
	// Changing the name does not lift it (the profile's trigger).
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = 'Bola A.' WHERE id = $1`, bolaID); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Bola?"})
	s = bola.must(200, "GET", "/v1/me/standing", nil)
	if s["state"] != "suspended" || !strings.Contains(s["words"].(string), "can't message people") {
		t.Fatalf("suspended: %v", s)
	}

	// An appeal, once; the other moderator overturns it and Bola is back.
	susID := sus["id"].(string)
	bola.must(400, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": susID, "text": " "})
	ap := bola.must(201, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": susID, "text": "Those were jokes between friends"})
	bola.must(409, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": susID, "text": "Again"})
	ada.must(404, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": susID, "text": "Not mine"})
	if a := platformAudits(t, w, "people.appeal"); len(a) != 1 || a[0].Target != susID {
		t.Fatalf("the appeal's record: %v", a)
	}
	appeals := second.must(200, "GET", "/v1/ops/people/appeals", nil)["appeals"].([]any)
	if len(appeals) != 1 || appeals[0].(map[string]any)["text"] != "Those were jokes between friends" || appeals[0].(map[string]any)["action"] != "suspend" {
		t.Fatalf("appeals: %v", appeals)
	}
	apID := ap["appeal"].(map[string]any)["id"].(string)
	// The moderator who suspended Bola does not answer the appeal.
	mod.write(403, "ops.people.appeal.overturn", apID, "/v1/ops/people/appeals/"+apID+"/overturn", map[string]any{"note": "My own decision"})
	second.write(422, "ops.people.appeal.overturn", apID, "/v1/ops/people/appeals/"+apID+"/overturn", nil)
	second.write(200, "ops.people.appeal.overturn", apID, "/v1/ops/people/appeals/"+apID+"/overturn", map[string]any{"note": "We read the chat again: jokes."})
	send(t, ada, adaChat, "Welcome back")
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["state"] != "limited" {
		t.Fatalf("after the appeal: %v", s)
	}
	p := mod.must(200, "GET", "/v1/ops/people/persons/"+target, nil)
	if p["person"].(map[string]any)["suspended"] != false || len(p["actions"].([]any)) != 3 {
		t.Fatalf("the person's page: %v", p)
	}

	// The limit is lifted by hand.
	limitID := ""
	for _, a := range p["actions"].([]any) {
		if a.(map[string]any)["action"] == "limit" {
			limitID = a.(map[string]any)["id"].(string)
		}
	}
	mod.write(200, "ops.people.lift", limitID, "/v1/ops/people/actions/"+limitID+"/lift", map[string]any{"reason": "Done"})
	mod.write(409, "ops.people.lift", limitID, "/v1/ops/people/actions/"+limitID+"/lift", nil)
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["state"] != "warned" {
		t.Fatalf("after the lift: %v", s)
	}

	// A suspension with an end ends by itself.
	timed := mod.write(201, "ops.people.suspend", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "suspend", "reason": "Cool off", "days": 1})
	ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Hm"})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, timed["id"]); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	send(t, ada, adaChat, "Back again")

	// A closure: 30 days for an appeal; upheld, the account is closed when
	// its day comes.
	cl := mod.write(201, "ops.people.close", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "close", "reason": "Repeated scams"})
	s = bola.must(200, "GET", "/v1/me/standing", nil)
	if s["state"] != "closing" || !strings.Contains(s["words"].(string), "close your account") {
		t.Fatalf("closing: %v", s)
	}
	mod.write(409, "ops.people.suspend", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "suspend", "reason": "Also"})
	ap2 := bola.must(201, "POST", "/v1/me/standing/appeals", map[string]any{"action_id": cl["id"], "text": "Please no"})["appeal"].(map[string]any)["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_moderation SET until = now() - interval '1 minute' WHERE id = $1`, cl["id"]); err != nil {
		t.Fatal(err)
	}
	// An open appeal holds the closure.
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NULL`, bolaID); n != 1 {
		t.Fatal("closed while an appeal was open")
	}
	second.write(200, "ops.people.appeal.uphold", ap2, "/v1/ops/people/appeals/"+ap2+"/uphold", map[string]any{"note": "The scams are clear."})
	if err := w.srv.SafetyTick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.adminCount(t, `SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL`, bolaID); n != 1 {
		t.Fatal("the account was not closed")
	}
	if a := platformAudits(t, w, "people.account.close"); len(a) != 1 || a[0].Detail["outcome"] != "closed" {
		t.Fatalf("the closure's record: %v", a)
	}
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["gone"] != true {
		t.Fatalf("closed, seen by Ada: %v", c)
	}
	_ = chidi
}

// Abuse signals (§8.2, §8.4): reports against a sender cut their request
// allowance; a first-week account on a device several accounts used is
// slowed to one request; allowances grow with age.
func TestPeopleAbuseSignals(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	var others []uuid.UUID
	for i := range 5 {
		_, id := w.person(t, "+23480311303"+string(rune('1'+i))+"0", "Person "+string(rune('A'+i)))
		w.older(t, id)
		others = append(others, id)
	}
	// Sends first messages to others until refused; answers how many went.
	requests := func(c *client) int {
		n := 0
		for _, o := range others {
			chat := chatWith(t, c, o)
			code, _ := c.do("POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": "Hello there"})
			if code != 201 {
				return n
			}
			n++
		}
		return n
	}

	// An account three months old: ten waiting at once.
	old, oldID := w.person(t, "+2348031130401", "Old")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET created_at = now() - interval '100 days' WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	if n := requests(old); n != 5 {
		t.Fatalf("a 100-day account sent %d", n)
	}

	// Reported by two people: a third (at least one) of their allowance.
	rep, repID := w.person(t, "+2348031130402", "Reported")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET created_at = now() - interval '100 days' WHERE id = $1`, repID); err != nil {
		t.Fatal(err)
	}
	for _, by := range others[:2] {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_reports (id, person_id, reported_id, reason, consent, messages, peer_wrote) VALUES ($1, $2, $3, 'spam', true, '[]', true)`,
			uuid.New(), by, repID); err != nil {
			t.Fatal(err)
		}
	}
	if n := requests(rep); n != 3 {
		t.Fatalf("a reported account sent %d", n)
	}

	// A first-week account on a phone three accounts used: one request.
	fresh, freshID := w.person(t, "+2348031130403", "Fresh")
	device := uuid.New()
	for _, id := range []uuid.UUID{freshID, others[3], others[4]} {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET device_id = $2, device_platform = 'android' WHERE token_hash = (SELECT token_hash FROM sessions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1)`,
			id, device); err != nil {
			t.Fatal(err)
		}
	}
	if n := requests(fresh); n != 1 {
		t.Fatalf("a first-week account on a shared phone sent %d", n)
	}
	// Alone on its phone, a first-week account sends two.
	alone, _ := w.person(t, "+2348031130404", "Alone")
	if n := requests(alone); n != 2 {
		t.Fatalf("a first-week account sent %d", n)
	}
}

// Lawful requests (0069 §11, §8.5): recorded by one operator with a
// passkey, approved by a second (never the same), exported with exactly
// the scope asked (the dates, the one other person, files only when
// asked), each step recorded; whether the person was told, or why not;
// the public count per half-year.
func TestLawfulRequests(t *testing.T) {
	ctx := context.Background()
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031130501", "Ada Obi")
	w.older(t, adaID)
	bola, bolaID := w.person(t, "+2348031130502", "Bola Ade")
	w.older(t, bolaID)
	chidi, chidiID := w.person(t, "+2348031130503", "Chidi")
	w.older(t, chidiID)
	first := newModerator(t, w, "first@taskiem.test")
	second := newModerator(t, w, "second@taskiem.test")

	ab := chatWith(t, ada, bolaID)
	send(t, ada, ab, "Meet at the market")
	ac := chatWith(t, ada, chidiID)
	send(t, ada, ac, "Chidi, not in scope")
	if code, f := ada.postFile(t, "/v1/me/people/"+ab+"/files", "map.png", pngPhoto(t, 33), nil); code != 201 {
		t.Fatalf("a photo: %d %v", code, f)
	}
	// One message before the dates asked.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET created_at = now() - interval '40 days' WHERE person_id = $1 AND chat_id = $2
		AND id = (SELECT id FROM people_messages WHERE person_id = $1 AND chat_id = $2 ORDER BY created_at LIMIT 1)`, adaID, ab); err != nil {
		t.Fatal(err)
	}
	send(t, ada, ab, "See you at noon")

	if c := (&client{t: t, base: w.base}).must(200, "GET", "/v1/transparency/lawful-requests", nil); len(c["periods"].([]any)) != 0 {
		t.Fatalf("no requests yet: %v", c)
	}
	now := time.Now().UTC()
	body := map[string]any{"authority": "Nigeria Police Force, Lagos command", "reference": "NPF/LAG/2026/0042", "legal_basis": "Cybercrimes Act 2015, s. 38; court order of 10 Nov 2026",
		"person_id": adaID, "from": now.Add(-30 * 24 * time.Hour), "to": now.Add(time.Hour), "with_person_id": bolaID, "files": false, "received_at": now.Add(-time.Hour)}
	first.must(403, "POST", "/v1/ops/lawful-requests", body)
	first.write(422, "ops.lawful.record", adaID.String(), "/v1/ops/lawful-requests", map[string]any{"person_id": adaID})
	rec := first.write(201, "ops.lawful.record", adaID.String(), "/v1/ops/lawful-requests", body)["request"].(map[string]any)
	id := rec["id"].(string)
	if rec["state"] != "recorded" || rec["person_name"] != "Ada Obi" {
		t.Fatalf("recorded: %v", rec)
	}
	// Nothing is exported before a second operator approves; the one who
	// recorded it cannot.
	first.write(409, "ops.lawful.export", id, "/v1/ops/lawful-requests/"+id+"/export", nil)
	first.write(403, "ops.lawful.approve", id, "/v1/ops/lawful-requests/"+id+"/approve", nil)
	second.write(200, "ops.lawful.approve", id, "/v1/ops/lawful-requests/"+id+"/approve", nil)
	out := first.write(200, "ops.lawful.export", id, "/v1/ops/lawful-requests/"+id+"/export", nil)
	chats := out["chats"].([]any)
	if len(chats) != 1 || chats[0].(map[string]any)["with_name"] != "Bola Ade" || out["messages_given"] != float64(2) {
		t.Fatalf("the export: %v", out)
	}
	raw := asJSON(t, out)
	if bytes.Contains(raw, []byte("Chidi, not in scope")) || bytes.Contains(raw, []byte("Meet at the market")) || !bytes.Contains(raw, []byte("See you at noon")) {
		t.Fatalf("the export is not the scope asked: %s", raw)
	}
	for _, m := range chats[0].(map[string]any)["messages"].([]any) {
		if m.(map[string]any)["file"] != nil {
			t.Fatal("files exported when none were asked for")
		}
	}
	a := platformAudits(t, w, "ops.lawful.export")
	if len(a) != 1 || a[0].Actor != "operator:first@taskiem.test" || a[0].Detail["messages"] != float64(2) || !strings.Contains(a[0].Detail["why"].(string), "NPF/LAG/2026/0042") {
		t.Fatalf("the export's record: %v", a)
	}
	if len(platformAudits(t, w, "ops.lawful.record")) != 1 || len(platformAudits(t, w, "ops.lawful.approve")) != 1 {
		t.Fatal("the steps are not recorded")
	}

	// Told, or why not.
	first.write(422, "ops.lawful.told", id, "/v1/ops/lawful-requests/"+id+"/told", nil)
	told := first.write(200, "ops.lawful.told", id, "/v1/ops/lawful-requests/"+id+"/told", map[string]any{"reason": "The court order forbids telling her for 90 days"})["request"].(map[string]any)
	if told["not_told_reason"] != "The court order forbids telling her for 90 days" || told["state"] != "answered" {
		t.Fatalf("not told: %v", told)
	}

	// A second request, with files, refused.
	body["files"] = true
	body["reference"] = "EFCC/2026/7"
	r2 := first.write(201, "ops.lawful.record", adaID.String(), "/v1/ops/lawful-requests", body)["request"].(map[string]any)["id"].(string)
	first.write(200, "ops.lawful.refuse", r2, "/v1/ops/lawful-requests/"+r2+"/refuse", map[string]any{"reason": "No court order"})
	if l := first.must(200, "GET", "/v1/ops/lawful-requests", nil)["requests"].([]any); len(l) != 2 {
		t.Fatalf("requests: %v", l)
	}
	// A third, with files, answered with them.
	body["reference"] = "DSS/2026/1"
	r3 := first.write(201, "ops.lawful.record", adaID.String(), "/v1/ops/lawful-requests", body)["request"].(map[string]any)["id"].(string)
	second.write(200, "ops.lawful.approve", r3, "/v1/ops/lawful-requests/"+r3+"/approve", nil)
	withFiles := first.write(200, "ops.lawful.export", r3, "/v1/ops/lawful-requests/"+r3+"/export", nil)
	if withFiles["files_given"] != float64(1) {
		t.Fatalf("files asked for: %v", withFiles)
	}

	// The public count: only half-years that have ended, so nothing yet;
	// the three show once theirs has ended.
	c := (&client{t: t, base: w.base}).must(200, "GET", "/v1/transparency/lawful-requests", nil)
	if len(c["periods"].([]any)) != 0 || !strings.Contains(c["words"].(string), "once it has ended") {
		t.Fatalf("the current half-year is shown: %v", c)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE lawful_requests SET received_at = received_at - interval '200 days'`); err != nil {
		t.Fatal(err)
	}
	c = (&client{t: t, base: w.base}).must(200, "GET", "/v1/transparency/lawful-requests", nil)
	ps := c["periods"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["received"] != float64(3) || ps[0].(map[string]any)["answered"] != float64(2) || ps[0].(map[string]any)["refused"] != float64(1) {
		t.Fatalf("the count: %v", c)
	}
	if _, ok := ps[0].(map[string]any)["pending"]; ok {
		t.Fatalf("pending is published: %v", c)
	}
	_, _ = bola, chidi
}

// Storage is watched (0069 Consequences, §16): the metrics and the
// console's figure.
func TestPeopleStorageWatched(t *testing.T) {
	w := safetyWorld(t)
	ada, adaID := w.person(t, "+2348031130601", "Ada Obi")
	w.older(t, adaID)
	_, bolaID := w.person(t, "+2348031130602", "Bola Ade")
	send(t, ada, chatWith(t, ada, bolaID), "Hello")
	mod := newModerator(t, w, "mod@taskiem.test")

	reg := prometheus.NewRegistry()
	reg.MustRegister(telemetry.PeopleStorageCollector{Pool: w.env.DB.App})
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			seen[f.GetName()]++
			if f.GetName() == "taskiem_people_storage_bytes" && m.GetGauge().GetValue() <= 0 {
				t.Errorf("no bytes for %v", m.GetLabel())
			}
		}
	}
	if seen["taskiem_people_storage_rows"] != 6 || seen["taskiem_people_storage_bytes"] != 6 {
		t.Fatalf("metrics: %v", seen)
	}
	o := mod.must(200, "GET", "/v1/ops/people/overview", nil)
	if len(o["storage"].([]any)) != 6 || !strings.Contains(o["storage_words"].(string), "People chats and groups take") {
		t.Fatalf("the overview: %v", o)
	}
	if s := mod.must(200, "GET", "/v1/ops/people/storage", nil); len(s["tables"].([]any)) != 6 {
		t.Fatalf("storage: %v", s)
	}
}

// The person is told a moderator's decision on their devices (a push on
// You opening Your standing), in plain words with the reason.
func TestPeopleStandingPush(t *testing.T) {
	w := safetyWorld(t)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	svc := newPushService(t)
	bola, bolaID := w.person(t, "+2348031130701", "Bola Ade")
	br := newPushBrowser(t)
	bola.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/bola", "keys": map[string]any{"p256dh": br.p256dh, "auth": br.au}})
	mod := newModerator(t, w, "mod@taskiem.test")
	target := bolaID.String()
	mod.write(201, "ops.people.suspend", target, "/v1/ops/people/persons/"+target+"/actions", map[string]any{"action": "suspend", "reason": "Scam messages.", "days": 3})
	pushSettle(t, w.srv)
	if svc.count("/bola") != 1 {
		t.Fatalf("pushes: %d", svc.count("/bola"))
	}
	p := br.decrypt(t, svc.bodies["/bola"][0])
	if p.Kind != push.KindYou || p.Title != "Your account can't message people for now" || !strings.HasPrefix(p.Body, "Scam messages. Your account can't message people") ||
		p.Link != "/you/settings/standing" {
		t.Fatalf("the decision's push: %+v", p)
	}
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["unseen"] != float64(1) {
		t.Fatalf("the notice: %v", s)
	}
	bola.must(204, "POST", "/v1/me/standing/seen", nil)
	if s := bola.must(200, "GET", "/v1/me/standing", nil); s["unseen"] != float64(0) {
		t.Fatalf("seen: %v", s)
	}
}

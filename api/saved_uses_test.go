package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// weeklyPrices emails the saved file @price-list every Monday (build plan
// H6, g-saved-wf), and says this week's note from the saved text @intro.
const weeklyPrices = `{"schema":"wd/v1","id":"wf_weekly","version":1,"name":"Weekly prices",
 "trigger":{"type":"schedule","config":{"cron":"0 9 * * 1","timezone":"Africa/Lagos"}},
 "steps":[
  {"id":"note","type":"transform","config":{"output":{"text":"=saved.intro.body"}}},
  {"id":"send","type":"connector","name":"Email the price list","connector":"notify@1","action":"message","needs":["note"],
   "input":{"email":"buyer@ada.test","title":"This week's prices","message":"=steps.note.output.text","file":"=saved.price_list"}}]}`

// A workflow sends a saved file (build plan H6, g-saved-wf): a weekly
// workflow emails @price-list, its name and a link to it at the end of
// the message, the file read at the run's start under the business's
// scope; each saved thing the run read is recorded with the steps that
// read it, listed on the thing's page and on the run; a file written out
// is refused at publish; a person's own file is never a run's.
func TestSavedFileSentByWorkflow(t *testing.T) {
	ctx := context.Background()
	w := newReachWorld(t)
	ps := newProjectStorage(t)
	w.srv.Media = ps.service()
	w.srv.Media.Scanner = chatScanner{}
	owner := w.tenant(t, "Ada Foods", "ada@savedfile.test")
	tenant := tenantID(t, owner)
	code, up := owner.postFile(t, "/v1/saved/uploads", "prices.png", pngPhoto(t, 70), nil)
	if code != 201 {
		t.Fatalf("upload: %d %v", code, up)
	}
	file := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "file",
		"details": map[string]any{"file": up["upload"].(map[string]any)["file"]}})["saved"].(map[string]any)
	intro := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "intro", "title": "Intro", "kind": "text",
		"details": map[string]any{"body": "Here are this week's prices."}})["saved"].(map[string]any)
	// One's own file of the same kind is never a run's.
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mine", "title": "Mine", "kind": "text", "details": map[string]any{"body": "x"}})

	// A link written out, or another expression, is not a saved file.
	for _, f := range []string{`{"file":"https://example.com/x.pdf"}`, `"=trigger.body.url"`, `"=steps.note.output.text"`} {
		bad := strings.Replace(weeklyPrices, `"file":"=saved.price_list"`, `"file":`+f, 1)
		if out := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(bad)}); out["valid"] != false || !strings.Contains(toJSON(out), "saved files") {
			t.Fatalf("%s: %v", f, out)
		}
	}
	wf := publishFlow(t, owner, weeklyPrices)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
	}

	// The run's history holds which file it read, never a link to it.
	if ev := toJSON(got["events"]); !strings.Contains(ev, "saved-file:"+file["id"].(string)) || strings.Contains(ev, "/signed/") || strings.Contains(ev, "saved-files/") {
		t.Fatalf("the run's history: %s", ev)
	}
	// The email: the message, then the file's name and a short link, made
	// when it was sent.
	text := sentText(t, w.world, tenant, "email")
	if !strings.Contains(text, "Here are this week's prices.\n\nprices.png: "+waPublicURL+"/v1/saved-files/") {
		t.Fatalf("the email: %q", text)
	}
	link := text[strings.Index(text, "/v1/saved-files/"):]
	// Opened by whoever got it, signed in or not: the file, from the
	// business's storage.
	anyone := &client{t: t, base: w.base}
	if st, _, body := anyone.rawGet(t, link); st != 200 || !strings.HasPrefix(body, "\xff\xd8\xff") {
		t.Fatalf("the link: %d %q", st, body[:min(len(body), 8)])
	}
	anyone.must(404, "GET", "/v1/saved-files/"+strings.Repeat("x", 43), nil)
	// One token tried again and again is slowed down, whoever asks.
	limited := false
	for range 40 {
		if st, _ := anyone.do("GET", "/v1/saved-files/"+strings.Repeat("y", 43), nil); st == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("a token is not limited")
	}
	// A week on, it opens nothing.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE saved_file_links SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	anyone.must(404, "GET", link, nil)

	// The run's page: the things it read, and which steps.
	reads := toJSON(got["saved"])
	if !strings.Contains(reads, `"name":"price-list"`) || !strings.Contains(reads, `"steps":["send"]`) || !strings.Contains(reads, `"steps":["note"]`) || strings.Contains(reads, "mine") {
		t.Fatalf("the run's reads: %s", reads)
	}
	// The thing's page: the runs that read it.
	uses := owner.must(200, "GET", "/v1/saved/"+file["id"].(string)+"/runs", nil)["runs"].([]any)
	if len(uses) != 1 || uses[0].(map[string]any)["run_id"] != run || uses[0].(map[string]any)["workflow_id"] != wf || uses[0].(map[string]any)["status"] != "completed" {
		t.Fatalf("the price list's runs: %v", uses)
	}
	if uses := owner.must(200, "GET", "/v1/saved/"+intro["id"].(string)+"/runs", nil)["runs"].([]any); len(uses) != 1 {
		t.Fatalf("the intro's runs: %v", uses)
	}
	// Only for those who may read runs; never another business's thing.
	owner.must(201, "PUT", "/v1/roles/no_runs", map[string]any{"permissions": []string{"workflow.read"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "sales@savedfile.test", "password": testPassword, "roles": []string{"no_runs"}})
	w.login(t, "sales@savedfile.test", testPassword).must(403, "GET", "/v1/saved/"+file["id"].(string)+"/runs", nil)
	other := w.tenant(t, "Other", "other@savedfile.test")
	other.must(404, "GET", "/v1/saved/"+file["id"].(string)+"/runs", nil)
	// A key for one environment sees that environment's runs only.
	dev := &client{t: t, base: w.base, token: owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "dev", "permissions": []string{"run.read"}, "environment": "dev"})["key"].(string)}
	if uses := dev.must(200, "GET", "/v1/saved/"+file["id"].(string)+"/runs", nil)["runs"].([]any); len(uses) != 0 {
		t.Fatalf("a dev key sees prod's runs: %v", uses)
	}
	// A saved person's title is their name: left out unless revealed.
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "buyer", "title": "Bola Ade", "kind": "person", "details": map[string]any{"email": "buyer@ada.test"}})
	pwf := publishFlow(t, owner, strings.Replace(strings.Replace(weeklyPrices, `"wf_weekly"`, `"wf_buyer"`, 1), `"=saved.intro.body"`, `"=saved.buyer.email"`, 1))
	prun := owner.must(201, "POST", "/v1/workflows/"+pwf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	if got := toJSON(owner.must(200, "GET", "/v1/runs/"+prun, nil)["saved"]); strings.Contains(got, "Bola") || !strings.Contains(got, `"name":"buyer"`) {
		t.Fatalf("a person's name on the run: %s", got)
	}
	if got := toJSON(owner.must(200, "GET", "/v1/runs/"+prun+"?reveal=true", nil)["saved"]); !strings.Contains(got, "Bola Ade") {
		t.Fatalf("revealed: %s", got)
	}
	// The latest few hundred are kept for each thing; a purged run's go.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO saved_uses (tenant_id, object_id, run_id, workflow_id, version, name, read_at)
		SELECT $1, $2, gen_random_uuid(), $3, 1, 'price-list', now() - make_interval(days => g) FROM generate_series(1, 210) g`, tenant, file["id"], wf); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	var kept int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_uses WHERE object_id = $1`, file["id"]).Scan(&kept); err != nil || kept != 200 {
		t.Fatalf("kept: %d %v", kept, err)
	}
	if err := pgx.BeginFunc(ctx, w.env.DB.Admin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE runs SET retain_until = now() - interval '1 day' WHERE id = $1`, prun); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', '{' || $1::text || '}', true)`, tenant); err != nil {
			return err
		}
		var purged bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_purge_run($1)`, prun).Scan(&purged); err != nil || !purged {
			return fmt.Errorf("purged %v: %w", purged, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_uses WHERE run_id = $1`, prun).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("a purged run's uses: %d %v", kept, err)
	}

	// Removed later: the run still says it read it (what it held then is in
	// the run's history, not here).
	owner.must(204, "DELETE", "/v1/saved/"+intro["id"].(string), nil)
	if got := toJSON(owner.must(200, "GET", "/v1/runs/"+run, nil)["saved"]); !strings.Contains(got, "intro") {
		t.Fatalf("a thing removed since, on the run: %s", got)
	}
}

// sentText is the latest message of a channel the business queued, opened.
func sentText(t *testing.T, w *world, tenant uuid.UUID, channel string) string {
	t.Helper()
	ctx := context.Background()
	var text string
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT body FROM platform_messages WHERE channel = $1 ORDER BY created_at DESC LIMIT 1`, channel).Scan(&raw); err != nil {
			return err
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		v, err := w.env.Vault.OpenTx(ctx, tx, tenant, env)
		if err != nil {
			return err
		}
		text, _ = v.(map[string]any)["text"].(string)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return text
}

// A saved file by SMS (H6 review): the message cut to make room, the
// link kept whole after it.
func TestSavedFileBySMS(t *testing.T) {
	w := newReachWorld(t)
	w.srv.Media = newProjectStorage(t).service()
	w.srv.Media.Scanner = chatScanner{}
	owner := w.tenant(t, "Ada Foods", "ada@savedsms.test")
	tenant := tenantID(t, owner)
	_, up := owner.postFile(t, "/v1/saved/uploads", "prices.png", pngPhoto(t, 70), nil)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "file",
		"details": map[string]any{"file": up["upload"].(map[string]any)["file"]}})
	long := strings.Repeat("Rice is up this week. ", 60)
	def := `{"schema":"wd/v1","id":"wf_sms","version":1,"name":"Prices by SMS","trigger":{"type":"manual"},
	 "steps":[{"id":"send","type":"connector","connector":"notify@1","action":"message",
	   "input":{"phone":"08031234567","message":"` + long + `","file":"=saved.price_list"}}]}`
	wf := publishFlow(t, owner, def)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	text := sentText(t, w.world, tenant, "sms")
	i := strings.Index(text, "prices.png: "+waPublicURL+"/v1/saved-files/")
	if i < 0 || !strings.Contains(text[:i], "…") || len(text[i+len("prices.png: "+waPublicURL+"/v1/saved-files/"):]) != 43 {
		t.Fatalf("the SMS: %q", text)
	}
}

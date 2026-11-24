package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

	// A link written out is not a saved file.
	bad := strings.Replace(weeklyPrices, `"file":"=saved.price_list"`, `"file":{"file":"https://example.com/x.pdf"}`, 1)
	if out := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(bad)}); out["valid"] != false || !strings.Contains(toJSON(out), "saved files") {
		t.Fatalf("a link written out: %v", out)
	}
	wf := publishFlow(t, owner, weeklyPrices)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
	}

	// The email: the message, then the file's name and its link.
	var text string
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT body FROM platform_messages WHERE channel = 'email' ORDER BY created_at DESC LIMIT 1`).Scan(&raw); err != nil {
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
	if !strings.Contains(text, "Here are this week's prices.\n\nprices.png: ") || !strings.Contains(text, "/signed/") {
		t.Fatalf("the email: %q", text)
	}
	link := text[strings.Index(text, "http"):]
	resp, err := http.Get(link) //nolint:gosec,noctx // the fake storage's link
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "\xff\xd8\xff") {
		t.Fatalf("the link: %d %q", resp.StatusCode, body[:min(len(body), 8)])
	}

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
	// Removed later: the run still says it read it (what it held then is in
	// the run's history, not here).
	owner.must(204, "DELETE", "/v1/saved/"+intro["id"].(string), nil)
	if got := toJSON(owner.must(200, "GET", "/v1/runs/"+run, nil)["saved"]); !strings.Contains(got, "intro") {
		t.Fatalf("a thing removed since, on the run: %s", got)
	}
}

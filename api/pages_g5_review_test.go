package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
)

// draftsModel answers every page build with the same draft: a page over
// the business's own jobs table, with tools that write it.
func draftsModel(page, tools string, tables []any) *ai.Fake {
	return &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		env, _ := json.Marshal(map[string]any{"summary": "A page over jobs.", "page": page, "tools": tools, "tables": tables})
		return &ai.Response{Text: string(env)}, nil
	}}
}

func pageBuild(t *testing.T, w *tablesWorld, c *client, goal string) map[string]any {
	t.Helper()
	started := c.must(202, "POST", "/v1/page-builds", map[string]any{"goal": goal})
	w.srv.WaitBackground()
	return c.must(200, "GET", "/v1/page-builds/"+started["id"].(string), nil)
}

const jobsPage = `{"page":"page/v1","name":"jobs-desk","title":"Jobs desk","blocks":[
 {"id":"add","type":"buttons","actions":[{"label":"Log","tool":"log"},{"label":"Log again","tool":"log-2"}]},
 {"id":"list","type":"list","title":"Jobs","source":{"table":"jobs","order":[{"column":"created_at","desc":true}]},"item":{"title":"=row.title","sub":"=row.status"},
  "actions":[{"label":"Close","tool":"close-job","inputs":{"ref":"=row.id","status":"done"}}]}]}`

const jobsTools = `[{"command":"log","name":"Log a job","kind":"add","table":"jobs","asks":["title"],"sets":{"status":"open"}},
 {"command":"log-2","name":"Log a job again","kind":"add","table":"jobs","asks":["title"],"sets":{"status":"open"}},
 {"command":"close-job","name":"Close a job","kind":"change","table":"jobs","asks":["status"],"roles":["admin"]}]`

// Review of G5, findings 1, 4 and 6: a drafted page whose tools write one
// of the business's own tables is made and works (the tools made over the
// table as it is); two of its words, one taken since the draft, are given
// free words at once and its buttons each still open their own; a change
// tool takes only the words the page gives; who may use a tool is said in
// the review and checked (a role the business lacks is refused, as is a
// table Taskiem keeps).
func TestDescribeItOverAnExistingTable(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	w.srv.AI = &api.AISettings{Provider: draftsModel(jobsPage, jobsTools, []any{})}
	owner := w.tenant(t, "Ada Works", "owner@g5-review.test")
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "jobs", "title": "Jobs", "columns": []map[string]any{
		{"name": "title", "title": "Title", "type": "text", "required": true}, {"name": "status", "title": "Status", "type": "text"}}})

	b := pageBuild(t, w, owner, "A page of jobs with buttons to log and close them")
	if p := b["proposal"].(map[string]any); len(p["problems"].([]any)) != 0 {
		t.Fatalf("draft: %v", p["problems"])
	}
	if makes := toJSON(b["review"].(map[string]any)["makes"]); !strings.Contains(makes, "/close-job (Close a job): changes a row of Jobs: Status; owners and admins only") ||
		strings.Contains(makes, "The table") {
		t.Errorf("review: %s", makes)
	}
	// /log is taken between the draft and making it.
	publishWF(t, owner, "Log", toolDef("wf_log", "Log", "log", "", `{}`,
		`[{"name":"x","type":"string","title":"X","description":"X","value":"=steps.s.output.x"}]`, `{"type":"text","text":"ok"}`,
		`{"id":"s","type":"transform","config":{"output":{"x":"y"}}}`))
	made := owner.must(201, "POST", "/v1/page-builds/"+b["id"].(string)+"/make", map[string]any{"publish": true})
	if made["published"] != true || len(made["tables"].([]any)) != 0 {
		t.Fatalf("made: %v", made)
	}
	words := toJSON(made["tools"])
	if !strings.Contains(words, `"asked":"log","changed":true,"command":"log-3"`) || !strings.Contains(words, `"asked":"log-2","changed":false,"command":"log-2"`) {
		t.Errorf("words given: %s", words)
	}
	page := owner.must(200, "GET", "/v1/pages/jobs-desk", nil)
	if doc := toJSON(page["document"]); !strings.Contains(doc, `{"label":"Log","tool":"log-3"}`) || !strings.Contains(doc, `{"label":"Log again","tool":"log-2"}`) {
		t.Errorf("each button opens its own: %s", doc)
	}
	pressTool(t, w, owner, "jobs-desk", map[string]any{"block": "add", "action": 0}, map[string]any{"title": "Fix the gate"})
	list := blockOf(t, viewOf(t, owner, "jobs-desk", nil), "list")
	if strings.Join(titles(list), ",") != "Fix the gate" {
		t.Fatalf("the tool wrote the business's own table: %v", list)
	}
	// Close: only the word the page gives.
	row := list["rows"].([]any)[0].(map[string]any)
	pressTool(t, w, owner, "jobs-desk", map[string]any{"block": "list", "row": row["id"], "action": 0}, nil)
	if r := blockOf(t, viewOf(t, owner, "jobs-desk", nil), "list")["rows"].([]any)[0].(map[string]any); r["sub"] != "done" {
		t.Errorf("closed: %v", r)
	}
	if st, out := owner.do("POST", "/v1/tools/close-job/start", map[string]any{"input": map[string]any{"ref": row["id"], "status": "anything"}}); st < 400 {
		t.Errorf("a word the page never gives: %d %v", st, out)
	}

	// Refused: a table Taskiem keeps, and a role the business lacks.
	tenant := tenantIDNamed(t, w.world, "Ada Works")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO data_tables (id, tenant_id, name, title, created_by, kept_by) VALUES (gen_random_uuid(), $1, 'money_log', 'Money log', 'taskiem', 'money_log')`, tenant); err != nil {
		t.Fatal(err)
	}
	w.srv.AI = &api.AISettings{Provider: draftsModel(
		`{"page":"page/v1","name":"bad-desk","title":"Bad","blocks":[{"id":"b","type":"buttons","actions":[{"label":"A","tool":"to-books"},{"label":"B","tool":"for-customers"}]}]}`,
		`[{"command":"to-books","name":"To the books","kind":"add","table":"money_log","asks":["entry_id"]},
		  {"command":"for-customers","name":"For customers","kind":"add","table":"jobs","asks":["title"],"roles":["customers"]}]`, []any{})}
	bad := pageBuild(t, w, owner, "A page that writes the books")
	problems := toJSON(bad["proposal"].(map[string]any)["problems"])
	for _, want := range []string{"/to-books works on the table money_log, which is neither the business's", "names customers, which is not one of the business's roles"} {
		if !strings.Contains(problems, want) {
			t.Errorf("want %q in %s", want, problems)
		}
	}
	owner.must(409, "POST", "/v1/page-builds/"+bad["id"].(string)+"/make", nil)
}

// Review of G5, findings 2 and 7: a bundle the plan has no room for makes
// nothing (no table, no tool); a build is saved, naming its page, before
// anything is published, so it is made once whatever publishing does; and
// under four-eyes an API key's install leaves drafts and says a person
// asks, with no publish request, as the Publish button refuses it.
func TestBundleRoomAndPublishing(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	w.srv.AI = &api.AISettings{Provider: draftsModel(jobsPage, jobsTools, []any{})}
	owner := w.tenant(t, "Room Ltd", "owner@g5-room.test")
	tenant := tenantIDNamed(t, w.world, "Room Ltd")

	// No room for Tickets desk's two tools: nothing is made.
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"max_workflows": 1}, "operator"); err != nil {
		t.Fatal(err)
	}
	if st, out := owner.do("POST", "/v1/page-starters/tickets-desk/install", map[string]any{"publish": true}); st != 429 || out["limit"] != "max_workflows" {
		t.Fatalf("no room: %d %v", st, out)
	}
	if n := len(owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any)); n != 0 {
		t.Errorf("a table was made: %d", n)
	}
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"max_workflows": 100}, "operator"); err != nil {
		t.Fatal(err)
	}

	// Four-eyes: the tools wait for another person; the build is saved and
	// names its page already; making it again is refused.
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "jobs", "title": "Jobs", "columns": []map[string]any{
		{"name": "title", "title": "Title", "type": "text", "required": true}, {"name": "status", "title": "Status", "type": "text"}}})
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	b := pageBuild(t, w, owner, "A page of jobs")
	made := owner.must(201, "POST", "/v1/page-builds/"+b["id"].(string)+"/make", map[string]any{"publish": true})
	if made["published"] != false || !strings.Contains(fmt.Sprint(made["waiting"]), "another person") {
		t.Errorf("four-eyes: %v", made)
	}
	if got := owner.must(200, "GET", "/v1/page-builds/"+b["id"].(string), nil); got["status"] != "saved" || got["page_name"] != "jobs-desk" {
		t.Errorf("saved: %v", got)
	}
	owner.must(409, "POST", "/v1/page-builds/"+b["id"].(string)+"/make", map[string]any{"publish": true})

	// An API key under four-eyes: drafts, said so, and no publish request.
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"page.edit", "page.publish", "table.read", "table.edit", "workflow.edit", "workflow.publish"}})
	kc := &client{t: t, base: w.base, token: key["key"].(string)}
	var before int
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM publish_requests WHERE tenant_id = $1`, tenant).Scan(&before)
	byKey := kc.must(201, "POST", "/v1/page-starters/waiting-list/install", map[string]any{"publish": true})
	if byKey["published"] != false || !strings.Contains(fmt.Sprint(byKey["waiting"]), "a person asks to publish") || !strings.Contains(toJSON(byKey["tools"]), `"state":"forbidden"`) {
		t.Errorf("a key: %v", byKey)
	}
	var after int
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM publish_requests WHERE tenant_id = $1`, tenant).Scan(&after)
	if after != before {
		t.Errorf("a key asked to publish: %d then %d", before, after)
	}
}

// Review of G5, finding 3: a starter's tool words are given under the
// business's lock on tool words, held to the drafts' insert, so a recipe
// installed at the same moment never gets the same word.
func TestBundleWordsUnderTheLock(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Race Ltd", "owner@g5-race.test")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	count := func(st int) {
		mu.Lock()
		defer mu.Unlock()
		if st == 201 {
			ok++
		}
	}
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Two starters made at once may race for the table's name too:
			// one may then be refused, never given a word twice.
			st, _ := owner.do("POST", "/v1/page-starters/service-log/install", map[string]any{})
			count(st)
		}()
		go func() {
			defer wg.Done()
			st, _ := owner.do("POST", "/v1/templates/tool-add-a-record/instantiate", map[string]any{"params": map[string]any{"command": "log-service", "table": "records"}})
			count(st)
		}()
	}
	wg.Wait()
	tenant := tenantIDNamed(t, w.world, "Race Ltd")
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT v.definition->'tool'->>'command' FROM workflow_versions v JOIN workflows f ON f.id = v.workflow_id
		WHERE f.tenant_id = $1 AND v.definition->'tool' IS NOT NULL`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	n := 0
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		if seen[c] {
			t.Errorf("two tools called /%s", c)
		}
		seen[c] = true
		n++
	}
	if n != ok || n < 4 {
		t.Errorf("tools made: %d of %d installs %v", n, ok, seen)
	}
}

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/gitprovider/gitfake"
)

func flowDoc(id, name, output string) string {
	return `{"schema":"wd/v1","id":"` + id + `","version":1,"name":"` + name + `","trigger":{"type":"manual"},
  "steps":[{"id":"out","type":"transform","config":{"output":"` + output + `"}}]}`
}

const flowTest = `{"schema":"wd-test/v1","workflow":"../flows/double.wd.json","cases":[
  {"name":"doubles","trigger":{"body":{"n":2}},"expect":{"status":"completed","outputs":{"out":4}}}]}`

// deliver posts a webhook to the API as the Git host would.
func deliver(t *testing.T, base, path string, h http.Header, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(body))
	req.Header = h
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func TestGitLedDeploysOnPushWhenTestsPass(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	gh.Repo.Commit("main", map[string]string{
		"flows/double.wd.json":   flowDoc("wf_double", "Double", "=trigger.body.n * 2"),
		"tests/double.test.json": flowTest,
		"README.md":              "not read",
	})

	// Connecting checks the repository; bad credentials are refused.
	status, body := owner.do("PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "git_led", "auth": map[string]any{"type": "token", "token": "wrong"}})
	if status != 422 || !strings.Contains(body["error"].(string), "401") {
		t.Fatalf("%d %v", status, body)
	}
	conn := owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "git_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	secret, hook := conn["webhook_secret"].(string), conn["webhook_url"].(string)
	if secret == "" || !strings.HasPrefix(hook, "/git-hooks/") {
		t.Fatalf("connect: %v", conn)
	}
	// The secret is shown once.
	if again := owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main", "mode": "git_led"}); again["webhook_secret"] != nil {
		t.Error("the webhook secret was shown again")
	}

	// A forged push is refused; a push to another branch is ignored.
	h, b := gitfake.GitHubPush("main", "x", "forged")
	if status, _ := deliver(t, w.base, hook, h, b); status != 401 {
		t.Errorf("forged push: %d", status)
	}
	h, b = gitfake.GitHubPush("feature", "x", secret)
	if status, body := deliver(t, w.base, hook, h, b); status != 202 || body["ignored"] == nil {
		t.Errorf("other branch: %d %v", status, body)
	}

	// A push queues a sync; the sync tests, then creates and publishes.
	commit := gh.Repo.Files("main")
	_ = commit
	head := gh.Repo.Commit("main", map[string]string{"flows/note.txt": "x"})
	h, b = gitfake.GitHubPush("main", head, secret)
	if status, body := deliver(t, w.base, hook, h, b); status != 202 || body["sync_id"] == nil {
		t.Fatalf("push: %d %v", status, body)
	}
	if n, err := w.srv.GitSyncOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("sync: %d %v", n, err)
	}
	syncs := owner.must(200, "GET", "/v1/git/prod/syncs", nil)["syncs"].([]any)
	last := syncs[0].(map[string]any)
	if last["status"] != "deployed" || last["commit"] != head {
		t.Fatalf("sync: %v", last)
	}
	list := owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any)
	wf := list[0].(map[string]any)
	if wf["key"] != "wf_double" || wf["active_version"] != float64(1) {
		t.Fatalf("workflow: %v", wf)
	}
	v1 := owner.must(200, "GET", "/v1/workflows/"+wf["id"].(string), nil)

	// The workflow is read-only outside Git.
	status, body = owner.do("POST", "/v1/workflows/"+wf["id"].(string)+"/versions", map[string]any{"definition": json.RawMessage(flowDoc("wf_double", "Double", "=1"))})
	if status != 409 || !strings.Contains(body["error"].(string), "managed in Git") {
		t.Errorf("edit of a Git-managed workflow: %d %v", status, body)
	}

	// A push that breaks the test deploys nothing.
	bad := gh.Repo.Commit("main", map[string]string{"flows/double.wd.json": flowDoc("wf_double", "Double", "=trigger.body.n * 3")})
	h, b = gitfake.GitHubPush("main", bad, secret)
	deliver(t, w.base, hook, h, b)
	if _, err := w.srv.GitSyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	last = owner.must(200, "GET", "/v1/git/prod/syncs", nil)["syncs"].([]any)[0].(map[string]any)
	report, _ := json.Marshal(last["report"])
	if last["status"] != "failed" || !strings.Contains(string(report), "doubles") {
		t.Errorf("failing test: %v %s", last["status"], report)
	}
	if after := owner.must(200, "GET", "/v1/workflows/"+wf["id"].(string), nil); len(after["versions"].([]any)) != len(v1["versions"].([]any)) {
		t.Error("a failing sync created a version")
	}

	// A policy in the repository is activated, and a workflow may use it.
	gh.Repo.Commit("main", map[string]string{
		"policies/payouts.policy.json": `{"rules":[{"levels":[{"role":"approver"}]}]}`,
		"flows/payout.wd.json": `{"schema":"wd/v1","id":"wf_payout","version":1,"name":"Payout","trigger":{"type":"manual"},
		  "steps":[{"id":"ok","type":"approval","config":{"policy":"payouts"}}]}`,
	})

	// Fixing the test deploys version 2, recording the commit.
	good := gh.Repo.Commit("main", map[string]string{"tests/double.test.json": strings.Replace(flowTest, `"out":4`, `"out":6`, 1)})
	owner.must(202, "POST", "/v1/git/prod/sync", nil)
	if _, err := w.srv.GitSyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	last = owner.must(200, "GET", "/v1/git/prod/syncs", nil)["syncs"].([]any)[0].(map[string]any)
	if last["status"] != "deployed" || last["commit"] != good {
		t.Errorf("fixed: %v", last)
	}
	if p := owner.must(200, "GET", "/v1/policies", nil)["policies"].([]any); len(p) != 1 || p[0].(map[string]any)["created_by"] != "git:prod" {
		t.Errorf("policies: %v", p)
	}
	audit := owner.must(200, "GET", "/v1/audit?action=workflow.publish", nil)
	if raw, _ := json.Marshal(audit); !strings.Contains(string(raw), good) {
		t.Errorf("the publish audit entry should name the commit: %s", raw)
	}
}

func TestPlatformLedPublishOpensRequest(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	gl := gitfake.GitLab(t, "acme/flows", "gltok")
	gl.Repo.Commit("main", map[string]string{"README.md": "flows live here"})
	owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "gitlab", "api_url": gl.URL + "/api/v4", "repo": "acme/flows", "branch": "main",
		"mode": "platform_led", "auth": map[string]any{"type": "token", "token": "gltok"}})

	created := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay", "definition": json.RawMessage(flowDoc("wf_payEmployees", "Pay employees", "=trigger.body.n + 1"))})
	wf := created["id"].(string)
	out := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	git := out["git"].(map[string]any)["prod"].(map[string]any)
	if git["url"] == nil {
		t.Fatalf("publish: %v", out)
	}
	if len(gl.Repo.Requests) != 1 {
		t.Fatalf("%d merge requests", len(gl.Repo.Requests))
	}
	mr := gl.Repo.Requests[0]
	if mr.Base != "main" || mr.Head != "taskiem/wf_payEmployees-v1" || mr.Title != "Publish Pay v1" {
		t.Errorf("request: %+v", mr)
	}
	if !strings.Contains(mr.Files["flows/pay-employees.wd.json"], `"output": "=trigger.body.n + 1"`) ||
		!strings.Contains(mr.Files["flows/pay-employees.flow.ts"], `transform(({ trigger }) => trigger.body.n + 1)`) || mr.Files["README.md"] == "" {
		t.Errorf("files: %v", mr.Files)
	}
	// Platform-led workflows stay editable; pushes do not deploy.
	owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(flowDoc("wf_payEmployees", "Pay employees", "=2"))})
	owner.must(409, "POST", "/v1/git/prod/sync", nil)
	// Retrying finds the open request rather than opening another.
	again := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/git-request", nil)
	if again["git"].(map[string]any)["prod"].(map[string]any)["url"] != git["url"] || len(gl.Repo.Requests) != 1 {
		t.Errorf("retry: %v", again)
	}
}

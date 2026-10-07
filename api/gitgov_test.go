package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/gitprovider/gitfake"
)

// connectGitLed connects prod to a fake GitHub repository in Git-led mode
// and returns the webhook path and secret.
func connectGitLed(t *testing.T, owner *client, gh *gitfake.Server) (hook, secret string) {
	t.Helper()
	conn := owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "git_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	return conn["webhook_url"].(string), conn["webhook_secret"].(string)
}

// With four-eyes on, or on a gated environment, a Git-led connection waits
// for a second person (not an API key); until then nothing changes.
func TestGitLedConnectionNeedsASecondPerson(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	bob := addMember(t, w, owner, "bob", "admin")
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	gh.Repo.Commit("main", map[string]string{"README.md": "x"})
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	settings := map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main", "mode": "git_led",
		"auth": map[string]any{"type": "token", "token": "ghtok"}}

	key := keyFor(t, owner, "", "git.manage")
	key.must(403, "PUT", "/v1/git/prod", settings)
	if out := owner.must(202, "PUT", "/v1/git/prod", settings); out["state"] != "pending_approval" {
		t.Fatalf("connect: %v", out)
	}
	list := owner.must(200, "GET", "/v1/git", nil)
	if len(list["connections"].([]any)) != 0 || len(list["requests"].([]any)) != 1 {
		t.Fatalf("before approval: %v", list)
	}
	owner.must(404, "POST", "/v1/git/prod/sync", nil)
	owner.must(403, "POST", "/v1/git/prod/approve", nil)
	key.must(403, "POST", "/v1/git/prod/approve", nil)
	bob.must(200, "POST", "/v1/git/prod/approve", map[string]any{"comment": "reviewed"})
	conns := owner.must(200, "GET", "/v1/git", nil)["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["mode"] != "git_led" {
		t.Fatalf("after approval: %v", conns)
	}
	owner.must(202, "POST", "/v1/git/prod/sync", nil)
	if _, err := w.srv.GitSyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last := owner.must(200, "GET", "/v1/git/prod/syncs", nil)["syncs"].([]any)[0].(map[string]any); last["status"] == "failed" {
		t.Errorf("sync with the approved credentials: %v", last)
	}

	// Pointing it at another branch waits again; a rejection keeps the old one.
	settings["branch"] = "other"
	delete(settings, "auth")
	gh.Repo.Commit("other", map[string]string{"README.md": "y"})
	owner.must(202, "PUT", "/v1/git/prod", settings)
	bob.must(200, "POST", "/v1/git/prod/reject", nil)
	if c := owner.must(200, "GET", "/v1/git", nil)["connections"].([]any)[0].(map[string]any); c["branch"] != "main" {
		t.Errorf("a rejected change applied: %v", c)
	}

	// The platform's Git secrets are not tenant secrets.
	if raw := toJSON(owner.must(200, "GET", "/v1/secrets", nil)); strings.Contains(raw, "git_") || strings.Contains(raw, "credentials") {
		t.Errorf("git secrets are listed: %s", raw)
	}
}

// A gated environment governs its Git-led connection without four-eyes.
func TestGitLedConnectionToGatedEnvironment(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	gh.Repo.Commit("main", map[string]string{"README.md": "x"})
	owner.must(200, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": "dev"})
	owner.must(202, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "git_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	// Platform-led needs no second person: publishing still governs it.
	owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "platform_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	if reqs := owner.must(200, "GET", "/v1/git", nil)["requests"].([]any); len(reqs) != 0 {
		t.Errorf("the direct change left the request waiting: %v", reqs)
	}
}

// Stored credentials go only to the repository they were given for.
func TestGitCredentialsStayWithTheirHost(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	gh.Repo.Commit("main", map[string]string{"README.md": "x"})
	var seen []string
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(404)
	}))
	defer evil.Close()
	owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main",
		"mode": "platform_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	for _, change := range []map[string]any{{"api_url": evil.URL}, {"repo": "evil/flows"}, {"provider": "gitlab"}} {
		req := map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main", "mode": "platform_led"}
		for k, v := range change {
			req[k] = v
		}
		if st, body := owner.do("PUT", "/v1/git/prod", req); st != 400 || !strings.Contains(body["error"].(string), "auth is required") {
			t.Errorf("%v without auth: %d %v", change, st, body)
		}
	}
	if len(seen) != 0 {
		t.Errorf("the stored token was sent elsewhere: %v", seen)
	}
	// The same repository keeps its stored credentials.
	owner.must(200, "PUT", "/v1/git/prod", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/flows", "branch": "main", "mode": "platform_led", "path": "wf"})
}

// A push deploys only while it is the branch head, and each delivery once;
// unknown hooks and forged ones get the same answer.
func TestGitPushReplayIsIgnored(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	first := gh.Repo.Commit("main", map[string]string{"flows/double.wd.json": flowDoc("wf_double", "Double", "=trigger.body.n * 2")})
	hook, secret := connectGitLed(t, owner, gh)
	push := func(commit, delivery string) (int, map[string]any) {
		h, b := gitfake.GitHubPush("main", commit, secret)
		if delivery != "" {
			h.Set("X-GitHub-Delivery", delivery)
		}
		return deliver(t, w.base, hook, h, b)
	}
	if st, body := push(first, "d1"); st != 202 || body["sync_id"] == nil {
		t.Fatalf("first push: %d %v", st, body)
	}
	if st, body := push(first, "d1"); st != 202 || body["sync_id"] != nil {
		t.Errorf("a redelivery queued a sync: %d %v", st, body)
	}
	second := gh.Repo.Commit("main", map[string]string{"flows/double.wd.json": flowDoc("wf_double", "Double", "=trigger.body.n * 3")})
	if st, body := push(second, "d2"); st != 202 || body["sync_id"] == nil {
		t.Fatalf("second push: %d %v", st, body)
	}
	// The old push, delivered again under a new id, would roll back.
	if st, body := push(first, "d3"); st != 202 || body["sync_id"] != nil || !strings.Contains(toJSON(body), "not the head") {
		t.Errorf("a stale push queued a sync: %d %v", st, body)
	}
	if n, err := w.srv.GitSyncOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("syncs: %d %v", n, err)
	}

	h, b := gitfake.GitHubPush("main", second, secret)
	tenant := strings.Split(hook, "/")[2]
	for _, path := range []string{"/git-hooks/" + tenant + "/nope", "/git-hooks/00000000-0000-0000-0000-000000000000/prod", "/git-hooks/not-a-tenant/prod"} {
		if st, body := deliver(t, w.base, path, h, b); st != 401 {
			t.Errorf("%s: %d %v", path, st, body)
		}
	}
	h, b = gitfake.GitHubPush("main", second, "forged")
	if st, _ := deliver(t, w.base, hook, h, b); st != 401 {
		t.Errorf("forged: %d", st)
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/gitprovider/gitfake"
)

const payoutFlow = `{"schema":"wd/v1","id":"wf_payout","version":1,"name":"Payout","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"role":"approver"}}]}`

// gitWorld is a tenant whose prod deploys from a fake GitHub repository,
// already synced once so later pushes have a known starting commit.
func gitWorld(t *testing.T) (w *world, owner *client, gh *gitfake.Server, hook, secret, base string) {
	t.Helper()
	w = newWorld(t)
	owner = w.tenant(t, "Acme", "owner@acme.test")
	gh = gitfake.GitHub(t, "acme/flows", "ghtok")
	base = gh.Repo.Commit("main", map[string]string{"README.md": "x"})
	hook, secret = connectGitLed(t, owner, gh)
	owner.must(202, "POST", "/v1/git/prod/sync", nil)
	syncGit(t, w)
	return w, owner, gh, hook, secret, base
}

func syncGit(t *testing.T, w *world) {
	t.Helper()
	if _, err := w.srv.GitSyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// pushAs commits files and delivers the push as pusher with a commit by
// author, then runs the sync; it returns the new commit.
func pushAs(t *testing.T, w *world, gh *gitfake.Server, hook, secret, before, pusher, author string, files map[string]string) string {
	t.Helper()
	commit := gh.Repo.Commit("main", files)
	h, b := gitfake.GitHubPushBy("main", before, commit, secret, pusher, author)
	if st, body := deliver(t, w.base, hook, h, b); st != 202 || body["sync_id"] == nil {
		t.Fatalf("push: %d %v", st, body)
	}
	syncGit(t, w)
	return commit
}

func gitWorkflow(t *testing.T, w *world, path string) string {
	t.Helper()
	var id string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id::text FROM workflows WHERE git_path = $1`, path).Scan(&id); err != nil {
		t.Fatalf("workflow at %s: %v", path, err)
	}
	return id
}

// Whoever wrote a commit and whoever pushed or merged it are the makers of
// the versions it deploys: neither approves their runs, however the run
// started.
func TestGitLedDeployKeepsItsMakers(t *testing.T) {
	w, owner, gh, hook, secret, base := gitWorld(t)
	bob := addMember(t, w, owner, "bob", "admin", "approver")
	carol := addMember(t, w, owner, "carol", "approver")
	dave := addMember(t, w, owner, "dave", "approver")

	pushAs(t, w, gh, hook, secret, base, "carol@acme.test", "bob@acme.test", map[string]string{"flows/payout.wd.json": payoutFlow})
	wf := gitWorkflow(t, w, "flows/payout.wd.json")
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	path := "/v1/approvals/" + run + "/ok"
	for name, c := range map[string]*client{"the commit's author": bob, "whoever merged it": carol} {
		if st, body := c.do("POST", path, map[string]any{"decision": "approved"}); st != 403 || !strings.Contains(toJSON(body), "maker") {
			t.Errorf("%s approved a run of the workflow: %d %v", name, st, body)
		}
	}
	if st := dave.must(200, "POST", path, map[string]any{"decision": "approved"})["status"]; st != "approved" {
		t.Errorf("someone who made nothing: %v", st)
	}
	var makers int
	var unknown bool
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT cardinality(makers), maker_unknown FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&makers, &unknown); err != nil || makers != 2 || unknown {
		t.Errorf("version: %d makers, unknown %v, %v", makers, unknown, err)
	}
}

// When Taskiem cannot tell who made a Git-led version, one approval is not
// enough: two different people approve, and a lone approver is told why
// they cannot.
func TestGitLedUnknownMakerNeedsTwoApprovers(t *testing.T) {
	w, owner, gh, hook, secret, base := gitWorld(t)
	// Someone who is not a member wrote the commit.
	pushAs(t, w, gh, hook, secret, base, "owner@acme.test", "stranger@elsewhere.test", map[string]string{"flows/payout.wd.json": payoutFlow})
	wf := gitWorkflow(t, w, "flows/payout.wd.json")
	var unknown bool
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT maker_unknown FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("maker_unknown: %v %v", unknown, err)
	}
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	path := "/v1/approvals/" + run + "/ok"
	dave := addMember(t, w, owner, "dave", "approver")
	erin := addMember(t, w, owner, "erin", "approver")
	if st := dave.must(200, "POST", path, map[string]any{"decision": "approved"})["status"]; st == "approved" {
		t.Fatalf("one approval decided a run whose maker is unknown")
	}
	dave.must(409, "POST", path, map[string]any{"decision": "approved"})
	if st := erin.must(200, "POST", path, map[string]any{"decision": "approved"})["status"]; st != "approved" {
		t.Errorf("the second approver: %v", st)
	}
}

// A lone approver cannot approve a run whose maker is unknown.
func TestGitLedUnknownMakerRefusesASoleApprover(t *testing.T) {
	w, owner, gh, _, _, _ := gitWorld(t)
	gh.Repo.Commit("main", map[string]string{"flows/payout.wd.json": payoutFlow})
	// Deployed by hand: the commits' authors are not known.
	owner.must(202, "POST", "/v1/git/prod/sync", nil)
	syncGit(t, w)
	wf := gitWorkflow(t, w, "flows/payout.wd.json")
	carol := addMember(t, w, owner, "carol", "operator")
	run := carol.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	// The owner deployed it, so is a maker and the only approver.
	if st, body := owner.do("POST", "/v1/approvals/"+run+"/ok", map[string]any{"decision": "approved"}); st != 403 {
		t.Errorf("a sole approver approved an unknown maker's run: %d %v", st, body)
	}
}

// Approval policies are tenant-wide, so a repository deploys them only
// through the connection of prod or of a gated environment.
func TestGitPoliciesComeOnlyFromProdOrAGatedEnvironment(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "PUT", "/v1/policies/payouts", map[string]any{"document": json.RawMessage(`{"rules":[{"levels":[{"role":"approver","count":2}]}]}`)})
	gh := gitfake.GitHub(t, "acme/dev-flows", "ghtok")
	gh.Repo.Commit("main", map[string]string{"policies/payouts.policy.json": `{"rules":[{"levels":[{"role":"viewer"}]}]}`})
	owner.must(200, "PUT", "/v1/git/dev", map[string]any{"provider": "github", "api_url": gh.URL, "repo": "acme/dev-flows", "branch": "main",
		"mode": "git_led", "auth": map[string]any{"type": "token", "token": "ghtok"}})
	owner.must(202, "POST", "/v1/git/dev/sync", nil)
	syncGit(t, w)
	last := owner.must(200, "GET", "/v1/git/dev/syncs", nil)["syncs"].([]any)[0].(map[string]any)
	if last["status"] != "failed" || !strings.Contains(toJSON(last), "prod") {
		t.Errorf("a dev repository's policy: %v", last)
	}
	var doc string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT document::text FROM approval_policies WHERE name = 'payouts' AND state = 'active'`).Scan(&doc); err != nil || strings.Contains(doc, "viewer") {
		t.Errorf("the dev connection replaced the policy governing prod: %s %v", doc, err)
	}
	// The same file, unchanged from the active policy, is no problem.
	gh.Repo.Commit("main", map[string]string{"policies/payouts.policy.json": `{"rules":[{"levels":[{"role":"approver","count":2}]}]}`})
	owner.must(202, "POST", "/v1/git/dev/sync", nil)
	syncGit(t, w)
	if last := owner.must(200, "GET", "/v1/git/dev/syncs", nil)["syncs"].([]any)[0].(map[string]any); last["status"] == "failed" {
		t.Errorf("an unchanged policy failed the sync: %v", last)
	}
}

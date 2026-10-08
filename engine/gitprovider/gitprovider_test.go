package gitprovider_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/gitprovider"
	"github.com/israel-duff/taskiem/engine/gitprovider/gitfake"
)

var ctx = context.Background()

type host struct {
	name string
	srv  *gitfake.Server
	cfg  gitprovider.Config
	push func(branch, commit, secret string) (http.Header, []byte)
}

func hosts(t *testing.T) []host {
	gh := gitfake.GitHub(t, "acme/flows", "ghtok")
	gl := gitfake.GitLab(t, "acme/flows", "gltok")
	bb := gitfake.Bitbucket(t, "acme/flows", "bbtok")
	return []host{
		{"github", gh, gitprovider.Config{Provider: "github", APIURL: gh.URL, Repo: "acme/flows", Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "ghtok"}}, gitfake.GitHubPush},
		{"gitlab", gl, gitprovider.Config{Provider: "gitlab", APIURL: gl.URL + "/api/v4", Repo: "acme/flows", Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "gltok"}}, gitfake.GitLabPush},
		{"bitbucket", bb, bitbucketConfig(bb), gitfake.BitbucketPush},
	}
}

func bitbucketConfig(bb *gitfake.Server) gitprovider.Config {
	return gitprovider.Config{Provider: "bitbucket", APIURL: bb.URL + "/2.0", Repo: "acme/flows", Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "bbtok"}}
}

func TestReadProposeAndPush(t *testing.T) {
	for _, h := range hosts(t) {
		t.Run(h.name, func(t *testing.T) {
			first := h.srv.Repo.Commit("main", map[string]string{
				"flows/a.wd.json": `{"id":"a"}`, "flows/sub/b.wd.json": `{"id":"b"}`, "flows/a.flow.ts": "code",
				"tests/a.test.json": `{"t":1}`, "README.md": "hi",
			})
			p, err := gitprovider.New(h.cfg)
			if err != nil {
				t.Fatal(err)
			}
			head, err := p.Head(ctx)
			if err != nil || head != first {
				t.Fatalf("head %q %v", head, err)
			}
			snap, err := p.Read(ctx, "", []string{"flows", "tests"}, []string{".wd.json", ".test.json"})
			if err != nil {
				t.Fatal(err)
			}
			if snap.Commit != first || len(snap.Files) != 3 || string(snap.Files["flows/sub/b.wd.json"]) != `{"id":"b"}` || snap.Files["flows/a.flow.ts"] != nil {
				t.Errorf("snapshot: %s %v", snap.Commit, keys(snap.Files))
			}
			// A later commit does not change what an older commit reads.
			h.srv.Repo.Commit("main", map[string]string{"flows/a.wd.json": `{"id":"a2"}`})
			old, err := p.Read(ctx, first, []string{"flows"}, []string{".wd.json"})
			if err != nil || string(old.Files["flows/a.wd.json"]) != `{"id":"a"}` {
				t.Errorf("old snapshot: %v %v", old, err)
			}

			url, err := p.Propose(ctx, "taskiem/wf_a-v2", "Publish A v2", "body", []gitprovider.File{
				{Path: "flows/a.wd.json", Content: []byte(`{"id":"a3"}`)}, {Path: "flows/new.wd.json", Content: []byte(`{"id":"n"}`)},
			})
			if err != nil || url == "" {
				t.Fatal(url, err)
			}
			req := h.srv.Repo.Requests[0]
			if req.Base != "main" || req.Head != "taskiem/wf_a-v2" || req.Files["flows/a.wd.json"] != `{"id":"a3"}` || req.Files["flows/new.wd.json"] == "" || req.Files["README.md"] != "hi" {
				t.Errorf("request: %+v", req)
			}
			// Proposing again on the same branch reuses the open request.
			again, err := p.Propose(ctx, "taskiem/wf_a-v2", "Publish A v2", "body", []gitprovider.File{{Path: "flows/a.wd.json", Content: []byte(`{"id":"a4"}`)}})
			if err != nil || again != url || len(h.srv.Repo.Requests) != 1 {
				t.Errorf("again: %s %v (%d requests)", again, err, len(h.srv.Repo.Requests))
			}

			hdr, body := h.push("main", "abc123", "s3cret")
			push, err := p.VerifyPush(hdr, body, "s3cret")
			if err != nil || push == nil || push.Branch != "main" || push.Commit != "abc123" {
				t.Errorf("push: %+v %v", push, err)
			}
			if _, err := p.VerifyPush(hdr, body, "other"); !errors.Is(err, gitprovider.ErrSignature) {
				t.Errorf("a wrong secret must be refused: %v", err)
			}
		})
	}
}

func TestBadCredentialsAreReported(t *testing.T) {
	for _, h := range hosts(t) {
		h.cfg.Auth.Token = "wrong"
		p, _ := gitprovider.New(h.cfg)
		var he *gitprovider.HTTPError
		if _, err := p.Head(ctx); !errors.As(err, &he) || he.Status != 401 {
			t.Errorf("%s: %v", h.name, err)
		}
	}
}

func TestGitHubAppInstallationToken(t *testing.T) {
	gh := gitfake.GitHub(t, "acme/flows", "installation-token")
	gh.Repo.Commit("main", map[string]string{"flows/a.wd.json": "{}"})
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	p, err := gitprovider.New(gitprovider.Config{Provider: "github", APIURL: gh.URL, Repo: "acme/flows", Branch: "main",
		Auth: gitprovider.Auth{Type: "github_app", AppID: "123", InstallationID: "456", PrivateKey: keyPEM}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.Head(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if gh.InstallationTokens != 1 {
		t.Errorf("the installation token should be cached: %d exchanges", gh.InstallationTokens)
	}
	if _, err := gitprovider.New(gitprovider.Config{Provider: "gitlab", Repo: "a/b", Branch: "main", Auth: gitprovider.Auth{Type: "github_app", AppID: "1", InstallationID: "2", PrivateKey: keyPEM}}); err == nil || !strings.Contains(err.Error(), "only with GitHub") {
		t.Errorf("GitHub App on GitLab: %v", err)
	}
}

func TestBitbucketBasicAndBearerAuth(t *testing.T) {
	bb := gitfake.Bitbucket(t, "acme/flows", "bbtok")
	head := bb.Repo.Commit("main", map[string]string{"flows/a.wd.json": "{}"})
	cfg := bitbucketConfig(bb)
	p, _ := gitprovider.New(cfg)
	if got, err := p.Head(ctx); err != nil || got != head {
		t.Fatalf("bearer: %q %v", got, err)
	}

	// An API token is accepted only over Basic auth with its account.
	bb.Repo.User = "me@acme.test"
	var he *gitprovider.HTTPError
	if _, err := p.Head(ctx); !errors.As(err, &he) || he.Status != 401 {
		t.Errorf("bearer for an API token: %v", err)
	}
	cfg.Auth.Username = "me@acme.test"
	p, _ = gitprovider.New(cfg)
	if got, err := p.Head(ctx); err != nil || got != head {
		t.Errorf("basic: %q %v", got, err)
	}
	cfg.Auth.Username = "someone@acme.test"
	p, _ = gitprovider.New(cfg)
	if _, err := p.Head(ctx); !errors.As(err, &he) || he.Status != 401 {
		t.Errorf("basic with another account: %v", err)
	}

	for _, repo := range []string{"flows", "acme/", "/flows", "acme/flows/x"} {
		if _, err := gitprovider.New(gitprovider.Config{Provider: "bitbucket", Repo: repo, Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "t"}}); err == nil {
			t.Errorf("repo %q should be refused", repo)
		}
	}
	if _, err := gitprovider.New(gitprovider.Config{Provider: "bitbucket", Repo: "a/b", Branch: "main", Auth: gitprovider.Auth{Type: "github_app", AppID: "1", InstallationID: "2", PrivateKey: "k"}}); err == nil || !strings.Contains(err.Error(), "only with GitHub") {
		t.Errorf("GitHub App on Bitbucket: %v", err)
	}
}

func TestBitbucketPaginatesListings(t *testing.T) {
	bb := gitfake.Bitbucket(t, "acme/flows", "bbtok")
	bb.PageLen = 2
	files := map[string]string{"README.md": "hi", "flows/deep/er/x.wd.json": `{"id":"x"}`}
	for i := 0; i < 7; i++ {
		files[fmt.Sprintf("flows/f%d.wd.json", i)] = fmt.Sprintf(`{"id":"f%d"}`, i)
		files[fmt.Sprintf("flows/sub/s%d.wd.json", i)] = "{}"
	}
	bb.Repo.Commit("main", files)
	p, _ := gitprovider.New(bitbucketConfig(bb))
	snap, err := p.Read(ctx, "", []string{"flows", "missing"}, []string{".wd.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 15 || string(snap.Files["flows/f6.wd.json"]) != `{"id":"f6"}` || snap.Files["flows/deep/er/x.wd.json"] == nil {
		t.Errorf("snapshot: %v", keys(snap.Files))
	}
	// flows (9 entries), flows/sub (7), flows/deep and flows/deep/er (1 each).
	if bb.Listings != 5+4+1+1 {
		t.Errorf("%d listing pages", bb.Listings)
	}
}

func TestBitbucketReproposeRemakesBranch(t *testing.T) {
	bb := gitfake.Bitbucket(t, "acme/flows", "bbtok")
	bb.Repo.Commit("main", map[string]string{"flows/a.wd.json": `{"id":"a"}`})
	p, _ := gitprovider.New(bitbucketConfig(bb))
	url, err := p.Propose(ctx, "taskiem/wf_a-v2", "Publish A v2", "body", []gitprovider.File{
		{Path: "flows/a.wd.json", Content: []byte(`{"id":"a2"}`)}, {Path: "flows/extra.wd.json", Content: []byte("{}")},
	})
	if err != nil || !strings.HasPrefix(url, "https://bitbucket.example/") {
		t.Fatal(url, err)
	}
	if req := bb.Repo.Requests[0]; req.Base != "main" || req.Title != "Publish A v2" || req.Body != "body" || req.Files["flows/extra.wd.json"] != "{}" {
		t.Errorf("request: %+v", req)
	}

	// The base moves on; proposing again remakes the branch from it and
	// finds the open pull request.
	bb.Repo.Commit("main", map[string]string{"flows/b.wd.json": `{"id":"b"}`})
	again, err := p.Propose(ctx, "taskiem/wf_a-v2", "Publish A v2", "body", []gitprovider.File{{Path: "flows/a.wd.json", Content: []byte(`{"id":"a3"}`)}})
	if err != nil || again != url || len(bb.Repo.Requests) != 1 {
		t.Fatalf("again: %s %v (%d requests)", again, err, len(bb.Repo.Requests))
	}
	files := bb.Repo.Files("taskiem/wf_a-v2")
	if files["flows/a.wd.json"] != `{"id":"a3"}` || files["flows/b.wd.json"] == "" || files["flows/extra.wd.json"] != "" {
		t.Errorf("branch: %v", files)
	}
	if main := bb.Repo.Files("main"); main["flows/a.wd.json"] != `{"id":"a"}` {
		t.Errorf("the base branch changed: %v", main)
	}
}

func TestBitbucketVerifyPush(t *testing.T) {
	bb := gitfake.Bitbucket(t, "acme/flows", "bbtok")
	p, _ := gitprovider.New(bitbucketConfig(bb))
	change := func(n any) map[string]any { return map[string]any{"new": n} }
	ref := func(typ, name, hash string) map[string]any {
		return map[string]any{"type": typ, "name": name, "target": map[string]string{"hash": hash}}
	}
	delivery := func(event string, changes ...any) (http.Header, []byte) {
		body, _ := json.Marshal(map[string]any{"push": map[string]any{"changes": changes}})
		return gitfake.BitbucketHeader(event, body, "s3cret"), body
	}

	h, body := gitfake.BitbucketPush("main", "abc123", "s3cret")
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push == nil || push.Branch != "main" || push.Commit != "abc123" {
		t.Errorf("good: %+v %v", push, err)
	}
	if _, err := p.VerifyPush(h, body, "other"); !errors.Is(err, gitprovider.ErrSignature) {
		t.Errorf("bad signature: %v", err)
	}
	if _, err := p.VerifyPush(h, append([]byte(" "), body...), "s3cret"); !errors.Is(err, gitprovider.ErrSignature) {
		t.Errorf("altered body: %v", err)
	}
	if _, err := p.VerifyPush(http.Header{"X-Event-Key": {"repo:push"}}, body, "s3cret"); !errors.Is(err, gitprovider.ErrSignature) {
		t.Errorf("missing header: %v", err)
	}
	if _, err := p.VerifyPush(gitfake.BitbucketHeader("repo:push", body, ""), body, ""); !errors.Is(err, gitprovider.ErrSignature) {
		t.Errorf("an empty secret must be refused: %v", err)
	}
	h, body = delivery("pullrequest:created", change(ref("branch", "main", "abc")))
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push != nil {
		t.Errorf("other event: %+v %v", push, err)
	}
	h, body = delivery("repo:push", change(nil))
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push != nil {
		t.Errorf("deleted branch: %+v %v", push, err)
	}
	h, body = delivery("repo:push", change(ref("tag", "v1", "abc")))
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push != nil {
		t.Errorf("tag: %+v %v", push, err)
	}
	// One push to several branches: the connected branch's update counts.
	h, body = delivery("repo:push", change(ref("branch", "feature", "f1")), change(ref("branch", "main", "m1")))
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push == nil || push.Branch != "main" || push.Commit != "m1" {
		t.Errorf("several branches: %+v %v", push, err)
	}
	h, body = delivery("repo:push", change(ref("branch", "feature", "f1")))
	if push, err := p.VerifyPush(h, body, "s3cret"); err != nil || push == nil || push.Branch != "feature" {
		t.Errorf("another branch: %+v %v", push, err)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

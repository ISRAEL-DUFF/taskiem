package gitprovider_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
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
	return []host{
		{"github", gh, gitprovider.Config{Provider: "github", APIURL: gh.URL, Repo: "acme/flows", Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "ghtok"}}, gitfake.GitHubPush},
		{"gitlab", gl, gitprovider.Config{Provider: "gitlab", APIURL: gl.URL + "/api/v4", Repo: "acme/flows", Branch: "main", Auth: gitprovider.Auth{Type: "token", Token: "gltok"}}, gitfake.GitLabPush},
	}
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

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

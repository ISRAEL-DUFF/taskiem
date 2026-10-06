package gitprovider

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type github struct {
	c Config

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (g *github) repoURL(format string, a ...any) string {
	return strings.TrimRight(g.c.APIURL, "/") + "/repos/" + g.c.Repo + fmt.Sprintf(format, a...)
}

func (g *github) header(ctx context.Context) (http.Header, error) {
	tok := g.c.Auth.Token
	if g.c.Auth.Type == "github_app" {
		var err error
		if tok, err = g.installationToken(ctx); err != nil {
			return nil, err
		}
	}
	return http.Header{
		"Authorization":        {"Bearer " + tok},
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
	}, nil
}

func (g *github) do(ctx context.Context, method, url string, body, out any) error {
	h, err := g.header(ctx)
	if err != nil {
		return err
	}
	return call(ctx, g.c.HTTP, method, url, h, body, out)
}

// installationToken exchanges a JWT signed with the app's key for an
// installation token, cached until shortly before it expires.
func (g *github) installationToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Until(g.expires) > 5*time.Minute {
		return g.token, nil
	}
	jwt, err := appJWT(g.c.Auth.AppID, g.c.Auth.PrivateKey, time.Now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := strings.TrimRight(g.c.APIURL, "/") + "/app/installations/" + url.PathEscape(g.c.Auth.InstallationID) + "/access_tokens"
	if err := call(ctx, g.c.HTTP, "POST", u, http.Header{"Authorization": {"Bearer " + jwt}, "Accept": {"application/vnd.github+json"}}, map[string]any{}, &out); err != nil {
		return "", fmt.Errorf("GitHub App installation token: %w", err)
	}
	g.token, g.expires = out.Token, out.ExpiresAt
	return g.token, nil
}

// appJWT is the short-lived JWT a GitHub App authenticates as itself with.
func appJWT(appID, keyPEM string, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return "", errors.New("GitHub App private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("GitHub App private key is not RSA")
		}
		key = rk
	} else {
		return "", fmt.Errorf("GitHub App private key: %w", err)
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": appID})
	signing := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (g *github) Head(ctx context.Context) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := g.do(ctx, "GET", g.repoURL("/git/ref/heads/%s", url.PathEscape(g.c.Branch)), nil, &ref); err != nil {
		return "", err
	}
	return ref.Object.SHA, nil
}

func (g *github) Read(ctx context.Context, commit string, dirs, suffixes []string) (*Snapshot, error) {
	if commit == "" {
		var err error
		if commit, err = g.Head(ctx); err != nil {
			return nil, err
		}
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := g.do(ctx, "GET", g.repoURL("/git/trees/%s?recursive=1", url.PathEscape(commit)), nil, &tree); err != nil {
		return nil, err
	}
	if tree.Truncated {
		return nil, errors.New("the repository tree is too large for GitHub to list in one response")
	}
	snap := &Snapshot{Commit: commit, Files: map[string][]byte{}}
	h, err := g.header(ctx)
	if err != nil {
		return nil, err
	}
	h.Set("Accept", "application/vnd.github.raw+json")
	for _, e := range tree.Tree {
		if e.Type != "blob" || !wanted(e.Path, dirs, suffixes) {
			continue
		}
		if len(snap.Files) >= maxFiles {
			return nil, fmt.Errorf("more than %d workflow files", maxFiles)
		}
		var raw []byte
		if err := call(ctx, g.c.HTTP, "GET", g.repoURL("/git/blobs/%s", e.SHA), h, nil, &raw); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		snap.Files[e.Path] = raw
	}
	return snap, nil
}

func (g *github) Propose(ctx context.Context, branch, title, body string, files []File) (string, error) {
	base, err := g.Head(ctx)
	if err != nil {
		return "", err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := g.do(ctx, "GET", g.repoURL("/git/commits/%s", base), nil, &commit); err != nil {
		return "", err
	}
	entries := make([]map[string]any, len(files))
	for i, f := range files {
		entries[i] = map[string]any{"path": f.Path, "mode": "100644", "type": "blob", "content": string(f.Content)}
	}
	var tree, made struct {
		SHA string `json:"sha"`
	}
	if err := g.do(ctx, "POST", g.repoURL("/git/trees"), map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &tree); err != nil {
		return "", err
	}
	if err := g.do(ctx, "POST", g.repoURL("/git/commits"), map[string]any{"message": title, "tree": tree.SHA, "parents": []string{base}}, &made); err != nil {
		return "", err
	}
	err = g.do(ctx, "POST", g.repoURL("/git/refs"), map[string]any{"ref": "refs/heads/" + branch, "sha": made.SHA}, nil)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusUnprocessableEntity {
		// The branch exists from an earlier attempt: point it at this commit.
		err = g.do(ctx, "PATCH", g.repoURL("/git/refs/heads/%s", branch), map[string]any{"sha": made.SHA, "force": true}, nil)
	}
	if err != nil {
		return "", err
	}
	var pr struct {
		URL string `json:"html_url"`
	}
	err = g.do(ctx, "POST", g.repoURL("/pulls"), map[string]any{"title": title, "head": branch, "base": g.c.Branch, "body": body}, &pr)
	if errors.As(err, &he) && he.Status == http.StatusUnprocessableEntity {
		var open []struct {
			URL string `json:"html_url"`
		}
		owner := strings.SplitN(g.c.Repo, "/", 2)[0]
		if err := g.do(ctx, "GET", g.repoURL("/pulls?state=open&head=%s", url.QueryEscape(owner+":"+branch)), nil, &open); err == nil && len(open) > 0 {
			return open[0].URL, nil
		}
	}
	if err != nil {
		return "", err
	}
	return pr.URL, nil
}

func (g *github) VerifyPush(h http.Header, body []byte, secret string) (*Push, error) {
	sig := strings.TrimPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
	want := hmac.New(sha256.New, []byte(secret))
	want.Write(body)
	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want.Sum(nil)) {
		return nil, ErrSignature
	}
	if h.Get("X-GitHub-Event") != "push" {
		return nil, nil
	}
	var p struct {
		Ref     string `json:"ref"`
		After   string `json:"after"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if p.Deleted || !strings.HasPrefix(p.Ref, "refs/heads/") {
		return nil, nil
	}
	return &Push{Branch: strings.TrimPrefix(p.Ref, "refs/heads/"), Commit: p.After}, nil
}

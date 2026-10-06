// Package gitprovider talks to Git hosts for workflow Git sync (spec 10.3):
// reading a branch's files, opening a change request with new files, and
// verifying push webhooks. GitHub (github.com or Enterprise, with a token or
// a GitHub App installation) and GitLab (gitlab.com or self-managed, with a
// project or personal access token) are supported, through their REST APIs.
package gitprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config says where a repository is and how to reach it.
type Config struct {
	Provider string // github | gitlab
	APIURL   string // default https://api.github.com or https://gitlab.com/api/v4
	Repo     string // "owner/name" (GitHub) or a project path or id (GitLab)
	Branch   string
	Auth     Auth
	// HTTP is the client used for every call; callers pass one that goes
	// through the egress guard.
	HTTP *http.Client
}

// Auth is a token, or a GitHub App installation.
type Auth struct {
	Type           string `json:"type"` // token | github_app
	Token          string `json:"token,omitempty"`
	AppID          string `json:"app_id,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"` // PEM
}

// File is a file to write in a change request.
type File struct {
	Path    string
	Content []byte
}

// Snapshot is a branch's files under some directories, at one commit.
type Snapshot struct {
	Commit string
	Files  map[string][]byte // repository path -> content
}

// Push is a verified push webhook.
type Push struct {
	Branch string
	Commit string
}

// Provider is one Git host.
type Provider interface {
	// Head returns the branch's current commit.
	Head(ctx context.Context) (string, error)
	// Read returns the files under dirs whose names end with one of
	// suffixes, at commit (empty: the branch head).
	Read(ctx context.Context, commit string, dirs, suffixes []string) (*Snapshot, error)
	// Propose writes files on a new branch from the base branch and opens a
	// pull or merge request; it returns the request's web URL.
	Propose(ctx context.Context, branch, title, body string, files []File) (string, error)
	// VerifyPush checks a webhook's signature with secret and parses it. A
	// valid delivery that is not a push returns a nil Push.
	VerifyPush(header http.Header, body []byte, secret string) (*Push, error)
}

// ErrSignature is a webhook that does not carry the shared secret's signature.
var ErrSignature = errors.New("webhook signature does not match")

// New returns the provider for c.
func New(c Config) (Provider, error) {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if c.Repo == "" || c.Branch == "" {
		return nil, errors.New("gitprovider: repo and branch are required")
	}
	switch c.Provider {
	case "github":
		if c.APIURL == "" {
			c.APIURL = "https://api.github.com"
		}
		if !strings.Contains(c.Repo, "/") {
			return nil, errors.New("gitprovider: a GitHub repo is owner/name")
		}
		return &github{c: c}, checkAuth(c.Auth, true)
	case "gitlab":
		if c.APIURL == "" {
			c.APIURL = "https://gitlab.com/api/v4"
		}
		return &gitlab{c: c}, checkAuth(c.Auth, false)
	}
	return nil, fmt.Errorf("gitprovider: unknown provider %q (github or gitlab)", c.Provider)
}

func checkAuth(a Auth, app bool) error {
	switch {
	case a.Type == "token" && a.Token != "":
		return nil
	case a.Type == "github_app" && app && a.AppID != "" && a.InstallationID != "" && a.PrivateKey != "":
		return nil
	case a.Type == "github_app" && !app:
		return errors.New("gitprovider: GitHub App credentials work only with GitHub")
	}
	return errors.New("gitprovider: credentials need a token, or app_id, installation_id and private_key for a GitHub App")
}

// HTTPError is an error response from a Git host.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("git host: HTTP %d: %s", e.Status, e.Body) }

// call makes a JSON request; out may be nil, or *[]byte for a raw body.
func call(ctx context.Context, hc *http.Client, method, url string, header http.Header, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &HTTPError{Status: resp.StatusCode, Body: msg}
	}
	switch o := out.(type) {
	case nil:
		return nil
	case *[]byte:
		*o = raw
		return nil
	}
	return json.Unmarshal(raw, out)
}

// wanted reports whether a repository path is a file sync reads.
func wanted(path string, dirs, suffixes []string) bool {
	inDir := len(dirs) == 0
	for _, d := range dirs {
		d = strings.Trim(d, "/")
		if d == "" || strings.HasPrefix(path, d+"/") {
			inDir = true
		}
	}
	if !inDir {
		return false
	}
	for _, s := range suffixes {
		if strings.HasSuffix(path, s) {
			return true
		}
	}
	return len(suffixes) == 0
}

// maxFiles bounds a snapshot: a repository is not a place for thousands of
// workflows, and each file is a request.
const maxFiles = 500

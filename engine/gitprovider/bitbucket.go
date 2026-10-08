package gitprovider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// bitbucket is Bitbucket Cloud's REST API 2.0.
type bitbucket struct{ c Config }

func (b *bitbucket) repoURL(format string, a ...any) string {
	ws, slug, _ := strings.Cut(b.c.Repo, "/")
	return strings.TrimRight(b.c.APIURL, "/") + "/repositories/" + url.PathEscape(ws) + "/" + url.PathEscape(slug) + fmt.Sprintf(format, a...)
}

// escapePath escapes each segment of a repository path or branch name,
// keeping its slashes.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// header authenticates as an access token (Bearer), or with a username as
// an API token or app password (Basic).
func (b *bitbucket) header() http.Header {
	auth := "Bearer " + b.c.Auth.Token
	if b.c.Auth.Username != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(b.c.Auth.Username+":"+b.c.Auth.Token))
	}
	return http.Header{"Authorization": {auth}, "Accept": {"application/json"}}
}

func (b *bitbucket) do(ctx context.Context, method, u string, body, out any) error {
	return call(ctx, b.c.HTTP, method, u, b.header(), body, out)
}

func (b *bitbucket) Head(ctx context.Context) (string, error) {
	var ref struct {
		Target struct {
			Hash string `json:"hash"`
		} `json:"target"`
	}
	if err := b.do(ctx, "GET", b.repoURL("/refs/branches/%s", escapePath(b.c.Branch)), nil, &ref); err != nil {
		return "", err
	}
	return ref.Target.Hash, nil
}

// next checks a pagination link points at the API host before it is
// followed: the host builds it, and credentials go with it.
func (b *bitbucket) next(link string) (string, error) {
	api, err := url.Parse(b.c.APIURL)
	if err != nil {
		return "", err
	}
	n, err := url.Parse(link)
	if err != nil || n.Scheme != api.Scheme || n.Host != api.Host {
		return "", fmt.Errorf("the pagination link %q is not on %s", link, api.Host)
	}
	return link, nil
}

func (b *bitbucket) Read(ctx context.Context, commit string, dirs, suffixes []string) (*Snapshot, error) {
	if commit == "" {
		var err error
		if commit, err = b.Head(ctx); err != nil {
			return nil, err
		}
	}
	snap := &Snapshot{Commit: commit, Files: map[string][]byte{}}
	if len(dirs) == 0 {
		dirs = []string{""}
	}
	for _, dir := range dirs {
		// Directories are listed one level at a time; a trailing slash
		// lists one (and is required for the root).
		queue := []string{strings.Trim(dir, "/")}
		for top := true; len(queue) > 0; top = false {
			d := queue[0]
			queue = queue[1:]
			u := b.repoURL("/src/%s/", url.PathEscape(commit))
			if d != "" {
				u += escapePath(d) + "/"
			}
			u += "?pagelen=100"
			for u != "" {
				var page struct {
					Values []struct {
						Path string `json:"path"`
						Type string `json:"type"`
					} `json:"values"`
					Next string `json:"next"`
				}
				err := b.do(ctx, "GET", u, nil, &page)
				var he *HTTPError
				if top && errors.As(err, &he) && he.Status == http.StatusNotFound {
					break // the directory does not exist at this commit
				}
				if err != nil {
					return nil, err
				}
				for _, e := range page.Values {
					if e.Type == "commit_directory" {
						queue = append(queue, e.Path)
						continue
					}
					if e.Type != "commit_file" || !wanted(e.Path, dirs, suffixes) {
						continue
					}
					if len(snap.Files) >= maxFiles {
						return nil, fmt.Errorf("more than %d workflow files", maxFiles)
					}
					var raw []byte
					if err := b.do(ctx, "GET", b.repoURL("/src/%s/%s", url.PathEscape(commit), escapePath(e.Path)), nil, &raw); err != nil {
						return nil, fmt.Errorf("%s: %w", e.Path, err)
					}
					snap.Files[e.Path] = raw
				}
				u = ""
				if page.Next != "" {
					if u, err = b.next(page.Next); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return snap, nil
}

func (b *bitbucket) Propose(ctx context.Context, branch, title, body string, files []File) (string, error) {
	base, err := b.Head(ctx)
	if err != nil {
		return "", err
	}
	// Bitbucket cannot move a branch, so one left by an earlier attempt is
	// deleted and remade from the base branch's head. An open pull request
	// from it follows the new branch.
	err = b.do(ctx, "DELETE", b.repoURL("/refs/branches/%s", escapePath(branch)), nil, nil)
	var he *HTTPError
	if err != nil && (!errors.As(err, &he) || he.Status != http.StatusNotFound) {
		return "", err
	}
	// With parents and a new branch name, the commit and the branch are
	// made on top of the base commit.
	fields := [][2]string{{"message", title}, {"branch", branch}, {"parents", base}}
	if err := callMultipart(ctx, b.c.HTTP, b.repoURL("/src"), b.header(), fields, files, nil); err != nil {
		return "", err
	}
	if u, err := b.openRequest(ctx, branch); err != nil || u != "" {
		return u, err
	}
	var pr struct {
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	}
	if err := b.do(ctx, "POST", b.repoURL("/pullrequests"), map[string]any{
		"title": title, "description": body,
		"source":      map[string]any{"branch": map[string]string{"name": branch}},
		"destination": map[string]any{"branch": map[string]string{"name": b.c.Branch}},
	}, &pr); err != nil {
		return "", err
	}
	return pr.Links.HTML.Href, nil
}

// openRequest returns the web URL of an open pull request from branch into
// the base branch, or "".
func (b *bitbucket) openRequest(ctx context.Context, branch string) (string, error) {
	q := fmt.Sprintf(`source.branch.name = %q AND destination.branch.name = %q AND state = "OPEN"`, branch, b.c.Branch)
	var open struct {
		Values []struct {
			Links struct {
				HTML struct {
					Href string `json:"href"`
				} `json:"html"`
			} `json:"links"`
		} `json:"values"`
	}
	if err := b.do(ctx, "GET", b.repoURL("/pullrequests?%s", url.Values{"q": {q}}.Encode()), nil, &open); err != nil {
		return "", err
	}
	if len(open.Values) == 0 {
		return "", nil
	}
	return open.Values[0].Links.HTML.Href, nil
}

func (b *bitbucket) VerifyPush(h http.Header, body []byte, secret string) (*Push, error) {
	// A webhook with a secret signs each delivery: X-Hub-Signature is
	// "sha256=" and the hex HMAC-SHA256 of the body.
	sig, ok := strings.CutPrefix(h.Get("X-Hub-Signature"), "sha256=")
	if secret == "" || !ok {
		return nil, ErrSignature
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write(body)
	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want.Sum(nil)) {
		return nil, ErrSignature
	}
	if h.Get("X-Event-Key") != "repo:push" {
		return nil, nil
	}
	var p struct {
		Push struct {
			Changes []struct {
				// New is null when the branch was deleted.
				New *struct {
					Type   string `json:"type"`
					Name   string `json:"name"`
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"new"`
			} `json:"changes"`
		} `json:"push"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	// One push can update several references: the connected branch's
	// update is the one that matters, or else the first branch's.
	var out *Push
	for _, c := range p.Push.Changes {
		if c.New == nil || c.New.Type != "branch" {
			continue // a deleted branch, or a tag
		}
		if c.New.Name == b.c.Branch {
			return &Push{Branch: c.New.Name, Commit: c.New.Target.Hash}, nil
		}
		if out == nil {
			out = &Push{Branch: c.New.Name, Commit: c.New.Target.Hash}
		}
	}
	return out, nil
}

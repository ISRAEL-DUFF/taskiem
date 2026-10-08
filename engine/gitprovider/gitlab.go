package gitprovider

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type gitlab struct{ c Config }

func (g *gitlab) projectURL(format string, a ...any) string {
	return strings.TrimRight(g.c.APIURL, "/") + "/projects/" + url.PathEscape(g.c.Repo) + fmt.Sprintf(format, a...)
}

func (g *gitlab) do(ctx context.Context, method, u string, body, out any) error {
	return call(ctx, g.c.HTTP, method, u, http.Header{"Private-Token": {g.c.Auth.Token}}, body, out)
}

func (g *gitlab) Head(ctx context.Context) (string, error) {
	var b struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := g.do(ctx, "GET", g.projectURL("/repository/branches/%s", url.PathEscape(g.c.Branch)), nil, &b); err != nil {
		return "", err
	}
	return b.Commit.ID, nil
}

func (g *gitlab) Read(ctx context.Context, commit string, dirs, suffixes []string) (*Snapshot, error) {
	if commit == "" {
		var err error
		if commit, err = g.Head(ctx); err != nil {
			return nil, err
		}
	}
	snap := &Snapshot{Commit: commit, Files: map[string][]byte{}}
	if len(dirs) == 0 {
		dirs = []string{""}
	}
	for _, dir := range dirs {
		const per = 100
		for page := 1; ; page++ {
			q := url.Values{"ref": {commit}, "recursive": {"true"}, "per_page": {fmt.Sprint(per)}, "page": {fmt.Sprint(page)}}
			if d := strings.Trim(dir, "/"); d != "" {
				q.Set("path", d)
			}
			var entries []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			}
			err := g.do(ctx, "GET", g.projectURL("/repository/tree?%s", q.Encode()), nil, &entries)
			var he *HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				break // the directory does not exist at this commit
			}
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.Type != "blob" || !wanted(e.Path, dirs, suffixes) {
					continue
				}
				if len(snap.Files) >= maxFiles {
					return nil, fmt.Errorf("more than %d workflow files", maxFiles)
				}
				var raw []byte
				if err := g.do(ctx, "GET", g.projectURL("/repository/files/%s/raw?ref=%s", url.PathEscape(e.Path), url.QueryEscape(commit)), nil, &raw); err != nil {
					return nil, fmt.Errorf("%s: %w", e.Path, err)
				}
				snap.Files[e.Path] = raw
			}
			if len(entries) < per {
				break
			}
		}
	}
	return snap, nil
}

func (g *gitlab) Propose(ctx context.Context, branch, title, body string, files []File) (string, error) {
	actions := make([]map[string]any, len(files))
	for i, f := range files {
		action := "create"
		var meta struct {
			FileName string `json:"file_name"`
		}
		if err := g.do(ctx, "GET", g.projectURL("/repository/files/%s?ref=%s", url.PathEscape(f.Path), url.QueryEscape(g.c.Branch)), nil, &meta); err == nil {
			action = "update"
		}
		actions[i] = map[string]any{"action": action, "file_path": f.Path, "content": string(f.Content)}
	}
	// start_branch with force: the branch is (re)made from the base branch.
	if err := g.do(ctx, "POST", g.projectURL("/repository/commits"), map[string]any{
		"branch": branch, "start_branch": g.c.Branch, "commit_message": title, "actions": actions, "force": true,
	}, nil); err != nil {
		return "", err
	}
	var mr struct {
		URL string `json:"web_url"`
	}
	err := g.do(ctx, "POST", g.projectURL("/merge_requests"), map[string]any{
		"source_branch": branch, "target_branch": g.c.Branch, "title": title, "description": body,
	}, &mr)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusConflict {
		var open []struct {
			URL string `json:"web_url"`
		}
		if err := g.do(ctx, "GET", g.projectURL("/merge_requests?state=opened&source_branch=%s", url.QueryEscape(branch)), nil, &open); err == nil && len(open) > 0 {
			return open[0].URL, nil
		}
	}
	if err != nil {
		return "", err
	}
	return mr.URL, nil
}

func (g *gitlab) VerifyPush(h http.Header, body []byte, secret string) (*Push, error) {
	if subtle.ConstantTimeCompare([]byte(h.Get("X-Gitlab-Token")), []byte(secret)) != 1 {
		return nil, ErrSignature
	}
	if h.Get("X-Gitlab-Event") != "Push Hook" {
		return nil, nil
	}
	var p struct {
		Ref         string `json:"ref"`
		After       string `json:"after"`
		CheckoutSHA string `json:"checkout_sha"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p.Ref, "refs/heads/") || strings.Trim(p.After, "0") == "" {
		return nil, nil // a tag, or a deleted branch
	}
	commit := p.CheckoutSHA
	if commit == "" {
		commit = p.After
	}
	return &Push{Branch: strings.TrimPrefix(p.Ref, "refs/heads/"), Commit: commit}, nil
}

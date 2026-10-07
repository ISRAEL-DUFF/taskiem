// Package gitfake is an in-memory Git host speaking the parts of GitHub's,
// GitLab's and Bitbucket Cloud's REST APIs that gitprovider uses, for tests.
package gitfake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Repo is one repository's history.
type Repo struct {
	mu       sync.Mutex
	commits  map[string]map[string]string // commit -> path -> content
	trees    map[string]map[string]string // tree id -> files (GitHub's data API)
	branches map[string]string            // branch -> commit
	blobs    map[string]string            // blob sha -> content
	n        int
	// Requests lists opened pull or merge requests.
	Requests []Request
	// Token is the token the host accepts.
	Token string
	// User, when set, makes Bitbucket accept the token only with Basic
	// authentication as this user (an API token), not as a Bearer token.
	User string
}

// Request is an opened pull or merge request.
type Request struct {
	Title, Head, Base, Body, URL string
	Files                        map[string]string // the head commit's files
}

// Server is a fake Git host.
type Server struct {
	*httptest.Server
	Repo *Repo
	// InstallationTokens counts GitHub App token exchanges.
	InstallationTokens int
	// PageLen is the most entries Bitbucket returns in one page of a
	// directory listing (default 100, as the real API).
	PageLen int
	// Listings counts the Bitbucket directory listing pages served.
	Listings int
}

func newRepo(token string) *Repo {
	return &Repo{commits: map[string]map[string]string{}, trees: map[string]map[string]string{}, branches: map[string]string{}, blobs: map[string]string{}, Token: token}
}

// Commit makes a commit on branch with files changed (an empty content
// deletes a file), and returns its id.
func (r *Repo) Commit(branch string, files map[string]string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commit(r.branches[branch], branch, files)
}

func (r *Repo) commit(parent, branch string, files map[string]string) string {
	next := map[string]string{}
	for k, v := range r.commits[parent] {
		next[k] = v
	}
	for k, v := range files {
		if v == "" {
			delete(next, k)
		} else {
			next[k] = v
		}
	}
	r.n++
	sum := sha256.Sum256([]byte(fmt.Sprintf("commit-%d", r.n)))
	id := hex.EncodeToString(sum[:20])
	r.commits[id] = next
	for _, v := range next {
		r.blobs[blobSHA(v)] = v
	}
	if branch != "" {
		r.branches[branch] = id
	}
	return id
}

// Files returns a branch's files.
func (r *Repo) Files(branch string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.commits[r.branches[branch]] {
		out[k] = v
	}
	return out
}

func blobSHA(content string) string {
	sum := sha256.Sum256([]byte("blob " + content))
	return hex.EncodeToString(sum[:20])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GitHub starts a fake GitHub API for repo owner/name.
func GitHub(t testing.TB, repo, token string) *Server {
	s := &Server{Repo: newRepo(token)}
	r := s.Repo
	prefix := "/repos/" + repo
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if strings.HasPrefix(req.URL.Path, "/app/installations/") && req.Method == "POST" {
			if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ey") {
				writeJSON(w, 401, map[string]string{"message": "bad JWT"})
				return
			}
			s.InstallationTokens++
			writeJSON(w, 201, map[string]string{"token": r.Token, "expires_at": "2099-01-01T00:00:00Z"})
			return
		}
		if req.Header.Get("Authorization") != "Bearer "+r.Token {
			writeJSON(w, 401, map[string]string{"message": "Bad credentials"})
			return
		}
		p := strings.TrimPrefix(req.URL.Path, prefix)
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		switch {
		case req.Method == "GET" && strings.HasPrefix(p, "/git/ref/heads/"):
			sha, ok := r.branches[strings.TrimPrefix(p, "/git/ref/heads/")]
			if !ok {
				writeJSON(w, 404, map[string]string{"message": "Not Found"})
				return
			}
			writeJSON(w, 200, map[string]any{"object": map[string]string{"sha": sha}})
		case req.Method == "GET" && strings.HasPrefix(p, "/git/trees/"):
			files, ok := r.commits[strings.TrimPrefix(p, "/git/trees/")]
			if !ok {
				writeJSON(w, 404, map[string]string{"message": "Not Found"})
				return
			}
			var entries []map[string]string
			for _, path := range sortedKeys(files) {
				entries = append(entries, map[string]string{"path": path, "type": "blob", "sha": blobSHA(files[path])})
			}
			writeJSON(w, 200, map[string]any{"tree": entries, "truncated": false})
		case req.Method == "GET" && strings.HasPrefix(p, "/git/blobs/"):
			content, ok := r.blobs[strings.TrimPrefix(p, "/git/blobs/")]
			if !ok || !strings.Contains(req.Header.Get("Accept"), "raw") {
				writeJSON(w, 404, map[string]string{"message": "Not Found"})
				return
			}
			_, _ = w.Write([]byte(content))
		case req.Method == "GET" && strings.HasPrefix(p, "/git/commits/"):
			id := strings.TrimPrefix(p, "/git/commits/")
			r.trees["tree-"+id] = r.commits[id]
			writeJSON(w, 200, map[string]any{"tree": map[string]string{"sha": "tree-" + id}})
		case req.Method == "POST" && p == "/git/trees":
			files := map[string]string{}
			for k, v := range r.trees[body["base_tree"].(string)] {
				files[k] = v
			}
			for _, e := range body["tree"].([]any) {
				m := e.(map[string]any)
				files[m["path"].(string)] = m["content"].(string)
			}
			r.n++
			id := fmt.Sprintf("tree-new-%d", r.n)
			r.trees[id] = files
			writeJSON(w, 201, map[string]string{"sha": id})
		case req.Method == "POST" && p == "/git/commits":
			files := r.trees[body["tree"].(string)]
			id := r.commit("", "", files)
			writeJSON(w, 201, map[string]string{"sha": id})
		case req.Method == "POST" && p == "/git/refs":
			name := strings.TrimPrefix(body["ref"].(string), "refs/heads/")
			if _, ok := r.branches[name]; ok {
				writeJSON(w, 422, map[string]string{"message": "Reference already exists"})
				return
			}
			r.branches[name] = body["sha"].(string)
			writeJSON(w, 201, map[string]string{})
		case req.Method == "PATCH" && strings.HasPrefix(p, "/git/refs/heads/"):
			r.branches[strings.TrimPrefix(p, "/git/refs/heads/")] = body["sha"].(string)
			writeJSON(w, 200, map[string]string{})
		case req.Method == "POST" && p == "/pulls":
			head := body["head"].(string)
			for _, pr := range r.Requests {
				if pr.Head == head {
					writeJSON(w, 422, map[string]string{"message": "A pull request already exists"})
					return
				}
			}
			u := fmt.Sprintf("https://github.example/%s/pull/%d", repo, len(r.Requests)+1)
			r.Requests = append(r.Requests, Request{Title: body["title"].(string), Head: head, Base: body["base"].(string), Body: body["body"].(string), URL: u, Files: r.commits[r.branches[head]]})
			writeJSON(w, 201, map[string]string{"html_url": u})
		case req.Method == "GET" && p == "/pulls":
			head := req.URL.Query().Get("head")
			var out []map[string]string
			for _, pr := range r.Requests {
				if strings.HasSuffix(head, ":"+pr.Head) {
					out = append(out, map[string]string{"html_url": pr.URL})
				}
			}
			writeJSON(w, 200, out)
		default:
			writeJSON(w, 404, map[string]string{"message": "fake: no route " + req.Method + " " + req.URL.Path})
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// GitHubPush is a signed push webhook delivery for branch at commit.
func GitHubPush(branch, commit, secret string) (http.Header, []byte) {
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/" + branch, "after": commit, "deleted": false})
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return http.Header{"X-Github-Event": {"push"}, "X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))}, "Content-Type": {"application/json"}}, body
}

// GitLab starts a fake GitLab API for project path.
func GitLab(t testing.TB, project, token string) *Server {
	s := &Server{Repo: newRepo(token)}
	r := s.Repo
	prefix := "/api/v4/projects/" + url.PathEscape(project)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if req.Header.Get("Private-Token") != r.Token {
			writeJSON(w, 401, map[string]string{"message": "401 Unauthorized"}) //nolint:misspell // GitLab's own wording
			return
		}
		raw := req.URL.EscapedPath()
		if !strings.HasPrefix(raw, prefix) {
			writeJSON(w, 404, map[string]string{"message": "404 Project Not Found"})
			return
		}
		p := strings.TrimPrefix(raw, prefix)
		q := req.URL.Query()
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		unescape := func(s string) string { u, _ := url.PathUnescape(s); return u }
		switch {
		case req.Method == "GET" && strings.HasPrefix(p, "/repository/branches/"):
			sha, ok := r.branches[unescape(strings.TrimPrefix(p, "/repository/branches/"))]
			if !ok {
				writeJSON(w, 404, map[string]string{"message": "404 Branch Not Found"})
				return
			}
			writeJSON(w, 200, map[string]any{"commit": map[string]string{"id": sha}})
		case req.Method == "GET" && p == "/repository/tree":
			files := r.commits[q.Get("ref")]
			dir := q.Get("path")
			var all []map[string]string
			for _, path := range sortedKeys(files) {
				if dir == "" || strings.HasPrefix(path, dir+"/") {
					all = append(all, map[string]string{"path": path, "type": "blob"})
				}
			}
			if dir != "" && len(all) == 0 {
				writeJSON(w, 404, map[string]string{"message": "404 Tree Not Found"})
				return
			}
			per, _ := strconv.Atoi(q.Get("per_page"))
			page, _ := strconv.Atoi(q.Get("page"))
			lo := (page - 1) * per
			if lo > len(all) {
				lo = len(all)
			}
			hi := lo + per
			if hi > len(all) {
				hi = len(all)
			}
			out := all[lo:hi]
			if out == nil {
				out = []map[string]string{}
			}
			writeJSON(w, 200, out)
		case req.Method == "GET" && strings.HasPrefix(p, "/repository/files/") && strings.HasSuffix(p, "/raw"):
			path := unescape(strings.TrimSuffix(strings.TrimPrefix(p, "/repository/files/"), "/raw"))
			content, ok := r.commits[q.Get("ref")][path]
			if !ok {
				writeJSON(w, 404, map[string]string{"message": "404 File Not Found"})
				return
			}
			_, _ = w.Write([]byte(content))
		case req.Method == "GET" && strings.HasPrefix(p, "/repository/files/"):
			path := unescape(strings.TrimPrefix(p, "/repository/files/"))
			if _, ok := r.commits[r.branches[q.Get("ref")]][path]; !ok {
				writeJSON(w, 404, map[string]string{"message": "404 File Not Found"})
				return
			}
			writeJSON(w, 200, map[string]string{"file_name": path})
		case req.Method == "POST" && p == "/repository/commits":
			files := map[string]string{}
			for _, a := range body["actions"].([]any) {
				m := a.(map[string]any)
				exists := false
				if _, ok := r.commits[r.branches[body["start_branch"].(string)]][m["file_path"].(string)]; ok {
					exists = true
				}
				if (m["action"] == "create") == exists {
					writeJSON(w, 400, map[string]string{"message": "A file with this name already exists / does not exist"})
					return
				}
				files[m["file_path"].(string)] = m["content"].(string)
			}
			id := r.commit(r.branches[body["start_branch"].(string)], body["branch"].(string), files)
			writeJSON(w, 201, map[string]string{"id": id})
		case req.Method == "POST" && p == "/merge_requests":
			src := body["source_branch"].(string)
			for _, mr := range r.Requests {
				if mr.Head == src {
					writeJSON(w, 409, map[string]any{"message": []string{"Another open merge request already exists for this source branch"}})
					return
				}
			}
			u := fmt.Sprintf("https://gitlab.example/%s/-/merge_requests/%d", project, len(r.Requests)+1)
			r.Requests = append(r.Requests, Request{Title: body["title"].(string), Head: src, Base: body["target_branch"].(string), Body: body["description"].(string), URL: u, Files: r.commits[r.branches[src]]})
			writeJSON(w, 201, map[string]string{"web_url": u})
		case req.Method == "GET" && p == "/merge_requests":
			var out []map[string]string
			for _, mr := range r.Requests {
				if mr.Head == q.Get("source_branch") {
					out = append(out, map[string]string{"web_url": mr.URL})
				}
			}
			writeJSON(w, 200, out)
		default:
			writeJSON(w, 404, map[string]string{"message": "fake: no route " + req.Method + " " + raw})
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// GitLabPush is a push webhook delivery for branch at commit.
func GitLabPush(branch, commit, secret string) (http.Header, []byte) {
	body, _ := json.Marshal(map[string]any{"object_kind": "push", "ref": "refs/heads/" + branch, "after": commit, "checkout_sha": commit})
	return http.Header{"X-Gitlab-Event": {"Push Hook"}, "X-Gitlab-Token": {secret}, "Content-Type": {"application/json"}}, body
}

var (
	bbSource = regexp.MustCompile(`source\.branch\.name = "([^"]*)"`)
	bbDest   = regexp.MustCompile(`destination\.branch\.name = "([^"]*)"`)
)

// Bitbucket starts a fake Bitbucket Cloud API, served under /2.0, for repo
// workspace/repo_slug. Its main branch is "main".
func Bitbucket(t testing.TB, repo, token string) *Server {
	s := &Server{Repo: newRepo(token), PageLen: 100}
	r := s.Repo
	prefix := "/2.0/repositories/" + repo
	fail := func(w http.ResponseWriter, status int, msg string) {
		writeJSON(w, status, map[string]any{"type": "error", "error": map[string]string{"message": msg}})
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		user, pass, basic := req.BasicAuth()
		if r.User == "" && req.Header.Get("Authorization") != "Bearer "+r.Token || r.User != "" && (!basic || user != r.User || pass != r.Token) {
			fail(w, 401, "Unauthorized") //nolint:misspell // Bitbucket's own wording
			return
		}
		if !strings.HasPrefix(req.URL.Path, prefix+"/") {
			fail(w, 404, "Repository not found")
			return
		}
		p := strings.TrimPrefix(req.URL.Path, prefix)
		q := req.URL.Query()
		switch {
		case req.Method == "GET" && strings.HasPrefix(p, "/refs/branches/"):
			name := strings.TrimPrefix(p, "/refs/branches/")
			sha, ok := r.branches[name]
			if !ok {
				fail(w, 404, "Branch not found")
				return
			}
			writeJSON(w, 200, map[string]any{"type": "branch", "name": name, "target": map[string]string{"type": "commit", "hash": sha}})
		case req.Method == "DELETE" && strings.HasPrefix(p, "/refs/branches/"):
			name := strings.TrimPrefix(p, "/refs/branches/")
			if _, ok := r.branches[name]; !ok {
				fail(w, 404, "Branch not found")
				return
			}
			if name == "main" {
				fail(w, 400, "The main branch cannot be deleted")
				return
			}
			delete(r.branches, name)
			w.WriteHeader(204)
		case req.Method == "GET" && strings.HasPrefix(p, "/src/"):
			commit, path, _ := strings.Cut(strings.TrimPrefix(p, "/src/"), "/")
			files, ok := r.commits[commit]
			if !ok {
				fail(w, 404, "Commit not found")
				return
			}
			if content, ok := files[path]; ok {
				_, _ = w.Write([]byte(content))
				return
			}
			// A directory: its direct entries, in order, a page at a time.
			dir := strings.TrimSuffix(path, "/")
			var entries []map[string]string
			seen := map[string]bool{}
			for _, f := range sortedKeys(files) {
				rel := f
				if dir != "" {
					if !strings.HasPrefix(f, dir+"/") {
						continue
					}
					rel = f[len(dir)+1:]
				}
				if sub, _, nested := strings.Cut(rel, "/"); nested {
					d := strings.TrimPrefix(dir+"/"+sub, "/")
					if !seen[d] {
						seen[d] = true
						entries = append(entries, map[string]string{"type": "commit_directory", "path": d})
					}
					continue
				}
				entries = append(entries, map[string]string{"type": "commit_file", "path": f})
			}
			if dir != "" && len(entries) == 0 {
				fail(w, 404, "No such file or directory: "+dir)
				return
			}
			s.Listings++
			per, _ := strconv.Atoi(q.Get("pagelen"))
			if per <= 0 {
				per = 10
			}
			per = min(per, s.PageLen)
			page, _ := strconv.Atoi(q.Get("page"))
			page = max(page, 1)
			lo := min((page-1)*per, len(entries))
			hi := min(lo+per, len(entries))
			out := map[string]any{"pagelen": per, "page": page, "size": len(entries), "values": append([]map[string]string{}, entries[lo:hi]...)}
			if hi < len(entries) {
				out["next"] = fmt.Sprintf("http://%s%s?pagelen=%d&page=%d", req.Host, req.URL.EscapedPath(), per, page+1)
			}
			writeJSON(w, 200, out)
		case req.Method == "POST" && p == "/src":
			// A commit from uploaded files: file fields are named by their
			// path; message, branch and parents are plain fields.
			if mt, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type")); mt != "multipart/form-data" {
				fail(w, 400, "expected multipart/form-data")
				return
			}
			req.Body = http.MaxBytesReader(w, req.Body, 4<<20)
			if err := req.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // bounded by MaxBytesReader above
				fail(w, 400, err.Error())
				return
			}
			field := func(k string) string {
				if v := req.MultipartForm.Value[k]; len(v) > 0 {
					return v[0]
				}
				return ""
			}
			files := map[string]string{}
			for name, fhs := range req.MultipartForm.File {
				f, err := fhs[0].Open()
				if err != nil {
					fail(w, 400, err.Error())
					return
				}
				raw, _ := io.ReadAll(f)
				_ = f.Close()
				files[strings.TrimPrefix(name, "/")] = string(raw)
			}
			branch, parent := field("branch"), field("parents")
			if branch == "" {
				branch = "main"
			}
			if tip, ok := r.branches[branch]; ok {
				if parent != "" && parent != tip {
					fail(w, 409, "parents is not the tip of "+branch)
					return
				}
				parent = tip
			} else if parent == "" {
				parent = r.branches["main"]
			}
			if _, ok := r.commits[parent]; !ok {
				fail(w, 400, "unknown parent "+parent)
				return
			}
			r.commit(parent, branch, files)
			w.WriteHeader(201)
		case req.Method == "POST" && p == "/pullrequests":
			var body struct {
				Title, Description  string
				Source, Destination struct {
					Branch struct {
						Name string `json:"name"`
					} `json:"branch"`
				}
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				fail(w, 400, err.Error())
				return
			}
			src, dst := body.Source.Branch.Name, body.Destination.Branch.Name
			if dst == "" {
				dst = "main"
			}
			if _, ok := r.branches[src]; !ok || body.Title == "" {
				fail(w, 400, "source branch not found, or no title")
				return
			}
			for _, pr := range r.Requests {
				if pr.Head == src && pr.Base == dst {
					fail(w, 400, "There is already an open pull request from "+src+" to "+dst)
					return
				}
			}
			n := len(r.Requests) + 1
			u := fmt.Sprintf("https://bitbucket.example/%s/pull-requests/%d", repo, n)
			r.Requests = append(r.Requests, Request{Title: body.Title, Head: src, Base: dst, Body: body.Description, URL: u, Files: r.commits[r.branches[src]]})
			writeJSON(w, 201, map[string]any{"id": n, "state": "OPEN", "links": map[string]any{"html": map[string]string{"href": u}}})
		case req.Method == "GET" && p == "/pullrequests":
			// Only open requests are kept, as the default state filter.
			filter := q.Get("q")
			if filter != "" && !strings.Contains(filter, `state = "OPEN"`) {
				fail(w, 400, "fake: unsupported query "+filter)
				return
			}
			src, dst := bbSource.FindStringSubmatch(filter), bbDest.FindStringSubmatch(filter)
			values := []map[string]any{}
			for _, pr := range r.Requests {
				if (src == nil || pr.Head == src[1]) && (dst == nil || pr.Base == dst[1]) {
					values = append(values, map[string]any{"state": "OPEN", "links": map[string]any{"html": map[string]string{"href": pr.URL}}})
				}
			}
			writeJSON(w, 200, map[string]any{"values": values, "page": 1, "size": len(values)})
		default:
			fail(w, 404, "fake: no route "+req.Method+" "+req.URL.Path)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// BitbucketHeader is the headers of a webhook delivery of body for event
// (such as "repo:push"), signed with secret.
func BitbucketHeader(event string, body []byte, secret string) http.Header {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return http.Header{"X-Event-Key": {event}, "X-Hub-Signature": {"sha256=" + hex.EncodeToString(mac.Sum(nil))}, "Content-Type": {"application/json"}}
}

// BitbucketPush is a signed push webhook delivery for branch at commit.
func BitbucketPush(branch, commit, secret string) (http.Header, []byte) {
	body, _ := json.Marshal(map[string]any{"push": map[string]any{"changes": []any{map[string]any{
		"new": map[string]any{"type": "branch", "name": branch, "target": map[string]string{"type": "commit", "hash": commit}},
		"old": nil,
	}}}})
	return BitbucketHeader("repo:push", body, secret), body
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

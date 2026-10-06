package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/wddiff"
)

// client talks to a Taskiem API with an API key (spec 10.4: the CLI
// publishes through the API, subject to its permissions and policies).
type client struct {
	base string
	key  string
	http *http.Client
}

// remoteFlags adds --url and --key to a command's flags.
func remoteFlags(fs *flag.FlagSet) func() (*client, error) {
	url := fs.String("url", env("TASKIEM_URL", "http://localhost:8080"), "API base URL (default $TASKIEM_URL)")
	key := fs.String("key", "", "API key (default $TASKIEM_API_KEY)")
	return func() (*client, error) {
		k := *key
		if k == "" {
			k = os.Getenv("TASKIEM_API_KEY")
		}
		if k == "" {
			return nil, errors.New("an API key is required: set TASKIEM_API_KEY (create one under Settings → API keys)")
		}
		return &client{base: strings.TrimRight(*url, "/"), key: k, http: &http.Client{Timeout: 30 * time.Second}}, nil
	}
}

// apiError is an error response from the API.
type apiError struct {
	Status   int
	Message  string
	Problems []struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	}
	Conflicts []string // paths of merge conflicts (409 on saving a version)
}

func (e *apiError) Error() string {
	msg := fmt.Sprintf("API %d: %s", e.Status, e.Message)
	for _, p := range e.Problems {
		msg += fmt.Sprintf("\n      %s: %s", p.Path, p.Message)
	}
	return msg
}

func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	return c.send(ctx, method, path, "application/json", rd, out)
}

// send makes one request with a body already encoded as contentType.
func (c *client) send(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		e := &apiError{Status: resp.StatusCode}
		var m struct {
			Error    string `json:"error"`
			Problems []struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			} `json:"problems"`
			Conflicts []struct {
				Path string `json:"path"`
			} `json:"conflicts"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Error != "" {
			e.Message, e.Problems = m.Error, m.Problems
			for _, c := range m.Conflicts {
				e.Conflicts = append(e.Conflicts, c.Path)
			}
		} else {
			e.Message = strings.TrimSpace(string(raw))
		}
		return e
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type remoteWorkflow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Key           string `json:"key"`
	ActiveVersion *int   `json:"active_version"`
	LatestVersion int    `json:"latest_version"`
}

type remoteVersion struct {
	Version struct {
		Version int    `json:"version"`
		State   string `json:"state"`
		Digest  string `json:"digest"`
	} `json:"version"`
	Definition json.RawMessage `json:"definition"`
}

// localWorkflow is a *.wd.json file to compare or deploy.
type localWorkflow struct {
	path string
	key  string
	name string
	doc  json.RawMessage
}

// findWorkflows reads the *.wd.json files under paths.
func findWorkflows(paths []string) ([]localWorkflow, error) {
	if len(paths) == 0 {
		paths = []string{"flows"}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".wd.json") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	var out []localWorkflow
	seen := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var head struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &head); err != nil || head.ID == "" {
			return nil, fmt.Errorf("%s: not a workflow definition (no id)", f)
		}
		if prev, dup := seen[head.ID]; dup {
			return nil, fmt.Errorf("%s and %s both define %s", prev, f, head.ID)
		}
		seen[head.ID] = f
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, localWorkflow{path: f, key: head.ID, name: head.Name, doc: buf.Bytes()})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no *.wd.json files under %s", strings.Join(paths, ", "))
	}
	return out, nil
}

// plan is what a deploy would do for one local workflow.
type plan struct {
	local   localWorkflow
	remote  *remoteWorkflow
	action  string // create | new_version | publish | unchanged
	against int    // the version compared with (the published one, else the latest)
	changes []string
	parent  string // digest of the latest version: a save made since is merged, not overwritten
}

func (c *client) plan(ctx context.Context, locals []localWorkflow) ([]plan, error) {
	var list struct {
		Workflows []remoteWorkflow `json:"workflows"`
	}
	if err := c.do(ctx, "GET", "/v1/workflows", nil, &list); err != nil {
		return nil, err
	}
	byKey := map[string][]remoteWorkflow{}
	for _, w := range list.Workflows {
		if w.Key != "" {
			byKey[w.Key] = append(byKey[w.Key], w)
		}
	}
	var out []plan
	for _, l := range locals {
		p := plan{local: l}
		switch ws := byKey[l.key]; len(ws) {
		case 0:
			p.action = "create"
			out = append(out, p)
			continue
		case 1:
			p.remote = &ws[0]
		default:
			return nil, fmt.Errorf("%s: %d workflows on the server use the id %s; rename one", l.path, len(ws), l.key)
		}
		// Compare with what runs today; an unpublished latest version that
		// already matches only needs publishing.
		latest, err := c.version(ctx, p.remote.ID, p.remote.LatestVersion)
		if err != nil {
			return nil, err
		}
		if same(latest.Definition, l.doc) {
			p.against = latest.Version.Version
			if latest.Version.State == "published" {
				p.action = "unchanged"
			} else {
				p.action = "publish"
			}
			if p.remote.ActiveVersion != nil && *p.remote.ActiveVersion != p.against {
				active, err := c.version(ctx, p.remote.ID, *p.remote.ActiveVersion)
				if err != nil {
					return nil, err
				}
				p.changes = wddiff.Diff(active.Definition, l.doc)
			}
			out = append(out, p)
			continue
		}
		p.action = "new_version"
		p.parent = latest.Version.Digest
		base := latest
		if p.remote.ActiveVersion != nil {
			if base, err = c.version(ctx, p.remote.ID, *p.remote.ActiveVersion); err != nil {
				return nil, err
			}
		}
		p.against = base.Version.Version
		p.changes = wddiff.Diff(base.Definition, l.doc)
		out = append(out, p)
	}
	return out, nil
}

func (c *client) version(ctx context.Context, wf string, v int) (*remoteVersion, error) {
	var out remoteVersion
	if err := c.do(ctx, "GET", fmt.Sprintf("/v1/workflows/%s/versions/%d", wf, v), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// same compares two definitions as JSON values (the server stores them as
// jsonb, which reorders keys).
func same(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func describe(p plan) string {
	switch p.action {
	case "create":
		return fmt.Sprintf("+ %s  new workflow %q (%s)", p.local.key, p.local.name, p.local.path)
	case "unchanged":
		return fmt.Sprintf("= %s  unchanged (version %d is published)", p.local.key, p.against)
	case "publish":
		return fmt.Sprintf("~ %s  publish version %d, which already matches %s", p.local.key, p.against, p.local.path)
	}
	what := fmt.Sprintf("version %d", p.remote.LatestVersion+1)
	if p.remote.ActiveVersion != nil {
		return fmt.Sprintf("~ %s  %s replaces published version %d", p.local.key, what, *p.remote.ActiveVersion)
	}
	return fmt.Sprintf("~ %s  %s (nothing published yet; compared with version %d)", p.local.key, what, p.against)
}

func diffCmd(args []string, stdout io.Writer) error { return planCmd("diff", args, stdout) }

func deployCmd(args []string, stdout io.Writer) error { return planCmd("deploy", args, stdout) }

// planCmd compares local workflows with the server and, for deploy, saves
// and publishes what changed.
func planCmd(name string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	connect := remoteFlags(fs)
	apply := false
	if name == "deploy" {
		dry := fs.Bool("dry-run", false, "show what would change without changing anything (same as diff)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		apply = !*dry
	} else if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := connect()
	if err != nil {
		return err
	}
	locals, err := findWorkflows(fs.Args())
	if err != nil {
		return err
	}
	ctx := context.Background()
	plans, err := c.plan(ctx, locals)
	if err != nil {
		return err
	}
	failed := 0
	for _, p := range plans {
		fmt.Fprintln(stdout, describe(p))
		for _, ch := range p.changes {
			fmt.Fprintln(stdout, "    "+ch)
		}
		if !apply {
			continue
		}
		if err := c.apply(ctx, p, stdout); err != nil {
			failed++
			fmt.Fprintf(stdout, "    FAILED: %v\n", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("deploy: %d of %d workflows failed", failed, len(plans))
	}
	return nil
}

func (c *client) apply(ctx context.Context, p plan, stdout io.Writer) error {
	var created struct {
		ID       string `json:"id"`
		Version  int    `json:"version"`
		Problems []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"problems"`
	}
	switch p.action {
	case "unchanged":
		return nil
	case "create":
		if err := c.do(ctx, "POST", "/v1/workflows", map[string]any{"name": p.local.name, "definition": p.local.doc}, &created); err != nil {
			return err
		}
	case "new_version":
		body := map[string]any{"definition": p.local.doc, "parent_digest": p.parent}
		if err := c.do(ctx, "POST", "/v1/workflows/"+p.remote.ID+"/versions", body, &created); err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.Status == http.StatusConflict {
				return fmt.Errorf("someone saved a newer version that changes the same parts (%s); run diff and deploy again", strings.Join(ae.Conflicts, ", "))
			}
			return err
		}
	case "publish":
		created.ID, created.Version = p.remote.ID, p.against
	}
	if len(created.Problems) > 0 {
		msg := fmt.Sprintf("version %d saved as a draft but not published:", created.Version)
		for _, pr := range created.Problems {
			msg += fmt.Sprintf("\n      %s: %s", pr.Path, pr.Message)
		}
		return errors.New(msg)
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/v1/workflows/%s/versions/%d/publish", created.ID, created.Version), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "    published version %d\n", created.Version)
	return nil
}

type runEvent struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	StepID     string          `json:"step_id"`
	Attempt    int             `json:"attempt"`
	Payload    json.RawMessage `json:"payload"`
	RecordedAt time.Time       `json:"recorded_at"`
}

type runInfo struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow"`
	Version  int    `json:"version"`
	Status   string `json:"status"`
}

func terminalStatus(s string) bool { return s == "completed" || s == "failed" || s == "cancelled" }

func runsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "tail" {
		return errors.New("usage: taskiem runs tail [--workflow ID] [--interval 2s] [RUN_ID]")
	}
	fs := flag.NewFlagSet("runs tail", flag.ContinueOnError)
	connect := remoteFlags(fs)
	workflow := fs.String("workflow", "", "only runs of this workflow (its id, or its definition id wf_...)")
	interval := fs.Duration("interval", 2*time.Second, "how often to poll")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := connect()
	if err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("runs tail: at most one run id")
	}
	t := &tailer{c: c, out: stdout, seen: map[string]int64{}, done: map[string]bool{}}
	if fs.NArg() == 1 {
		return t.follow(ctx, fs.Arg(0), *interval)
	}
	wf := *workflow
	if strings.HasPrefix(wf, "wf_") {
		if wf, err = c.workflowByKey(ctx, wf); err != nil {
			return err
		}
	}
	return t.all(ctx, wf, *interval)
}

func (c *client) workflowByKey(ctx context.Context, key string) (string, error) {
	var list struct {
		Workflows []remoteWorkflow `json:"workflows"`
	}
	if err := c.do(ctx, "GET", "/v1/workflows", nil, &list); err != nil {
		return "", err
	}
	for _, w := range list.Workflows {
		if w.Key == key {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no workflow with id %s", key)
}

// tailer prints run events as they are recorded.
type tailer struct {
	c    *client
	out  io.Writer
	seen map[string]int64 // run -> last printed seq
	done map[string]bool
}

// follow prints one run's events until it ends.
func (t *tailer) follow(ctx context.Context, run string, every time.Duration) error {
	for {
		st, err := t.poll(ctx, run, false)
		if err != nil {
			return err
		}
		if terminalStatus(st) {
			fmt.Fprintf(t.out, "run %s %s\n", run, st)
			return nil
		}
		if !sleep(ctx, every) {
			return nil
		}
	}
}

// all prints every active run's events until interrupted.
func (t *tailer) all(ctx context.Context, wf string, every time.Duration) error {
	since := time.Now().Add(-time.Minute)
	for {
		q := "/v1/runs?limit=100"
		if wf != "" {
			q += "&workflow=" + wf
		}
		var list struct {
			Runs []struct {
				runInfo
				StartedAt time.Time `json:"started_at"`
			} `json:"runs"`
		}
		if err := t.c.do(ctx, "GET", q, nil, &list); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // interrupted: a normal end of tailing
			}
			return err
		}
		for i := len(list.Runs) - 1; i >= 0; i-- {
			r := list.Runs[i]
			if t.done[r.ID] || (r.StartedAt.Before(since) && terminalStatus(r.Status)) {
				continue
			}
			st, err := t.poll(ctx, r.ID, true)
			if err != nil {
				if ctx.Err() != nil {
					return nil //nolint:nilerr // interrupted: a normal end of tailing
				}
				return err
			}
			if terminalStatus(st) {
				t.done[r.ID] = true
			}
		}
		if !sleep(ctx, every) {
			return nil
		}
	}
}

// poll prints a run's events not yet printed and returns its status.
func (t *tailer) poll(ctx context.Context, run string, prefix bool) (string, error) {
	var got struct {
		Run    runInfo    `json:"run"`
		Events []runEvent `json:"events"`
	}
	if err := t.c.do(ctx, "GET", "/v1/runs/"+run, nil, &got); err != nil {
		return "", err
	}
	for _, e := range got.Events {
		if e.Seq <= t.seen[run] {
			continue
		}
		t.seen[run] = e.Seq
		line := e.RecordedAt.UTC().Format("15:04:05.000") + "  "
		if prefix {
			line += short(run) + " " + got.Run.Workflow + "  "
		}
		line += e.Type
		if e.StepID != "" {
			line += " " + e.StepID
		}
		if e.Attempt > 1 {
			line += fmt.Sprintf(" (attempt %d)", e.Attempt)
		}
		if d := eventDetail(e); d != "" {
			line += "  " + d
		}
		fmt.Fprintln(t.out, line)
	}
	return got.Run.Status, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// eventDetail is a short note for events whose payload matters at a glance.
func eventDetail(e runEvent) string {
	var p map[string]any
	_ = json.Unmarshal(e.Payload, &p)
	switch e.Type {
	case "StepFailed", "RunFailed":
		if er, ok := p["error"].(map[string]any); ok {
			return fmt.Sprintf("%v: %v (next: %v)", er["kind"], er["message"], er["next"])
		}
	case "ApprovalRequested":
		if r, ok := p["role"].(string); ok && r != "" {
			return "role " + r
		}
	case "ApprovalDecided":
		return fmt.Sprintf("%v by %v", p["decision"], p["decided_by"])
	case "StepSkipped", "StepCancelled":
		if r, ok := p["reason"].(string); ok {
			return r
		}
	}
	return ""
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Pages with tools (docs/spec/pages.md G2): buttons on a page and on its
// rows, made tools as a part's source, and a run's status.
//
// A button is pressed here (POST /v1/pages/{name}/act), never worked out
// by the screen: the row is read again as the part shows it to this
// viewer (a personal column masked for one without table.read, before any
// expression sees it), and its values worked out from it. Then:
//
//   - a tool's button gives the tool to open and the values to fill in,
//     as a button on a tool's answer does (tool_buttons, made tools M2): the
//     start is POST /v1/tools/{command}/start with the button, held to the
//     tool's roles and care as the person who pressed; a value shown masked
//     is asked, not filled in; a once button is spent by its first run;
//   - a signal is sent, after a Yes, to one run of the workflow the button
//     names, waiting at a signal step that workflow declares a page may
//     send ("pages": true), held to the workflow's roles; to no other run;
//   - an approval gives the approval to open, if it waits for this person:
//     deciding it is POST /v1/approvals/{run}/{step}, with its own rules.
//
// A tool feeding a part runs as the viewer (it only reads: its care is
// "answers at once", checked at publish and at each view), counted as any
// run; what it answered is kept sixty seconds for that viewer and those
// values (page_source_cache), and read from the run, masked as a tool's
// answer is.

// pageToolEnv is the environment pages read tools and workflows in.
const pageToolEnv = "prod"

// pageSourceFor is how long a tool's answer is kept for a page.
const pageSourceFor = 60 * time.Second

// pageToolWait is how long a view waits for a tool it started (then the
// part says it is still working, and fills in when read again).
var pageToolWait = 3 * time.Second

// pageEnvTools fills env's tools and workflows: what a page's buttons and
// tool sources are checked against.
func pageEnvTools(ctx context.Context, s *Server, tx pgx.Tx, env *pages.Env) {
	env.Tool = func(command string) (pages.ToolInfo, bool) {
		_, info, ok := pageToolInfo(ctx, s, tx, command)
		return info, ok
	}
	env.Workflow = func(name string) bool {
		_, _, _, n, err := pageWorkflow(ctx, s, tx, name)
		return err == nil && n == 1
	}
	env.Signal = func(workflow, event string) string {
		_, _, d, n, err := pageWorkflow(ctx, s, tx, workflow)
		switch {
		case err != nil || n == 0:
			return "the business has no workflow " + workflow + " in use"
		case n > 1:
			return "two workflows are called " + workflow
		case !signalForPages(d, event):
			return workflow + " does not let a page tell it " + event + " (its signal step needs \"pages\": true)"
		}
		return ""
	}
}

// pageToolInfo is a tool as a page names it: members', in prod.
func pageToolInfo(ctx context.Context, s *Server, tx pgx.Tx, command string) (toolRow, pages.ToolInfo, bool) {
	t, err := scanTool(tx.QueryRow(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND command = $2 AND audience = 'members'`, pageToolEnv, command))
	if err != nil {
		return t, pages.ToolInfo{}, false
	}
	var def []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def); err != nil {
		return t, pages.ToolInfo{}, false
	}
	d, err := s.definitionFor(t.Workflow, t.Version, def)
	if err != nil {
		return t, pages.ToolInfo{}, false
	}
	info := pages.ToolInfo{Name: t.Name, Care: t.Asks(), Outputs: map[string]string{}}
	for _, o := range d.Outputs {
		info.Outputs[o.Name] = o.Type
	}
	if d.Tool != nil {
		info.Answer = d.Tool.Answer
	}
	if schema, _ := d.InputsSchema(); schema != nil {
		if m, ok := schema.(map[string]any); ok {
			props, _ := m["properties"].(map[string]any)
			for k, v := range props {
				info.Inputs = append(info.Inputs, k)
				if prop, _ := v.(map[string]any); personalInput(k, prop) {
					info.PersonalInputs = append(info.PersonalInputs, k)
				}
			}
		}
	}
	info.Roles = t.Roles
	return t, info, true
}

// personalInput says whether a tool's input takes personal data, as the
// form's Saved button reads it (docs/tools.md): declared x-pii, a phone
// or an email format, a saved person, phone, email, account or place, or
// words of its name or title that say so.
func personalInput(name string, prop map[string]any) bool {
	if prop == nil {
		return false
	}
	if _, ok := prop["x-pii"]; ok {
		return true
	}
	switch prop["format"] {
	case "phone", "phone-ng", "email":
		return true
	}
	switch prop["x-saved"] {
	case "phone", "email", "person", "bank_account", "place":
		return true
	}
	title, _ := prop["title"].(string)
	words := strings.ToLower(name + " " + title)
	for _, w := range []string{"phone", "email", "account", "address", "customer", "contact"} {
		if strings.Contains(words, w) {
			return true
		}
	}
	return false
}

// pageWorkflow is the workflow a page names, as deployed in prod: its id,
// version and definition, and how many deployed workflows have that name.
func pageWorkflow(ctx context.Context, s *Server, tx pgx.Tx, name string) (uuid.UUID, int, *wd.Definition, int, error) {
	rows, err := tx.Query(ctx, `SELECT w.id, d.version FROM workflows w JOIN deployments d ON d.workflow_id = w.id AND d.environment = $2
		WHERE w.name = $1 ORDER BY w.created_at LIMIT 2`, name, pageToolEnv)
	if err != nil {
		return uuid.Nil, 0, nil, 0, err
	}
	type hit struct {
		id uuid.UUID
		v  int
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.v); err != nil {
			rows.Close()
			return uuid.Nil, 0, nil, 0, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(hits) != 1 {
		return uuid.Nil, 0, nil, len(hits), err
	}
	var def []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, hits[0].id, hits[0].v).Scan(&def); err != nil {
		return uuid.Nil, 0, nil, 0, err
	}
	d, err := s.definitionFor(hits[0].id, hits[0].v, def)
	if err != nil {
		return uuid.Nil, 0, nil, 0, err
	}
	return hits[0].id, hits[0].v, d, 1, nil
}

// signalForPages says whether a workflow declares a page may send event
// to a run waiting at one of its signal steps.
func signalForPages(d *wd.Definition, event string) bool {
	if d == nil {
		return false
	}
	ok := false
	d.Walk(func(st *wd.Step) {
		if st.Signal != nil && st.Signal.Pages && st.Signal.Event == event {
			ok = true
		}
	})
	return ok
}

// stepOfInstance is a step's id in its definition from the id a wait is
// kept under (an instance inside a foreach or a parallel carries more).
func stepOfInstance(d *wd.Definition, id string) *wd.Step {
	if st := d.Step(id); st != nil {
		return st
	}
	for _, sep := range []string{"[", "#", "/", "."} {
		if base, _, ok := strings.Cut(id, sep); ok {
			if st := d.Step(base); st != nil {
				return st
			}
		}
	}
	return nil
}

// --- drawing ---

// drawBlock works out one part for the viewer (drawPart, pages.go), with
// what G2 adds: buttons, a status and a tool's answer drawn here; each
// row's tool buttons greyed for a viewer the tool is not for; a figure
// that is an alert; and a table's ticks, for one who may change its rows.
func (s *Server) drawBlock(ctx context.Context, pc *pageCtx, b pages.Block, cursor pages.Cursor, order *pages.Order) map[string]any {
	if out := s.drawG2(ctx, pc, b); out != nil {
		return out
	}
	out := s.drawPart(ctx, pc, b, cursor, order)
	s.markRows(ctx, pc, out)
	if b.Type == "figure" && b.Alert {
		out["alert"] = true
	}
	if b.Type == "table" && b.Source != nil && b.Source.Table != "" && pc.values.Viewer.Edit {
		if cols, ok := out["columns"].([]map[string]any); ok {
			t := pc.table(b)
			for i, c := range b.Columns {
				if col := columnNamed(t, c.Column); i < len(cols) && c.Edit && col.Type == "boolean" && !col.Personal {
					cols[i]["edit"] = true
					out["table_id"] = t.ID
				}
			}
		}
	}
	return out
}

// drawG2 draws a part G2 added (buttons, a status, a tool's answer), or
// nil for one drawBlock draws.
func (s *Server) drawG2(ctx context.Context, pc *pageCtx, b pages.Block) map[string]any {
	out := map[string]any{"id": b.ID, "type": b.Type}
	switch {
	case b.Type == "buttons":
		out["title"] = b.Title
		out["actions"] = s.markActions(ctx, pc, pages.ShownActions(b, pages.FiltersAct(pc.values)))
		return out
	case b.Type == "status":
		return s.drawStatus(ctx, pc, b, out)
	case b.Source != nil && b.Source.Tool != "":
		return s.drawToolPart(ctx, pc, b, out)
	}
	return nil
}

// markActions greys a tool's button the viewer may not use, saying why.
func (s *Server) markActions(ctx context.Context, pc *pageCtx, as []pages.ShownAction) []pages.ShownAction {
	p := principalFrom(ctx)
	for i, a := range as {
		if a.Kind != pages.ActTool {
			continue
		}
		t, ok := s.pageTool(ctx, pc, a.Tool)
		switch {
		case !ok:
			as[i].Disabled = "This tool is not available any more."
		case !p.Can(PermRunStart):
			as[i].Disabled = "Your role does not use tools."
		case !mayStart(p, t.Roles):
			as[i].Disabled = "Only " + startRolesWords(t.Roles) + " can use " + t.Name + "."
		}
	}
	if as == nil {
		return []pages.ShownAction{}
	}
	return as
}

// markRows greys each row's tool buttons the viewer may not use.
func (s *Server) markRows(ctx context.Context, pc *pageCtx, out map[string]any) {
	rows, ok := out["rows"].([]pages.Shown)
	if !ok {
		return
	}
	for i := range rows {
		if len(rows[i].Actions) > 0 {
			rows[i].Actions = s.markActions(ctx, pc, rows[i].Actions)
		}
	}
}

// pageTool is a tool a page names, read once for the page.
func (s *Server) pageTool(ctx context.Context, pc *pageCtx, command string) (toolRow, bool) {
	if pc.tools == nil {
		pc.tools = map[string]*toolRow{}
	}
	if t, seen := pc.tools[command]; seen {
		return derefTool(t)
	}
	var found *toolRow
	_ = db0(ctx, s, principalFrom(ctx).TenantID, func(tx pgx.Tx) error {
		if t, _, ok := pageToolInfo(ctx, s, tx, command); ok {
			found = &t
		}
		return nil
	})
	pc.tools[command] = found
	return derefTool(found)
}

func derefTool(t *toolRow) (toolRow, bool) {
	if t == nil {
		return toolRow{}, false
	}
	return *t, true
}

// drawStatus draws a run's state in plain words: the run the page's
// address names (?run=), else the declared workflow's latest, and only a
// run of that workflow, in this business.
func (s *Server) drawStatus(ctx context.Context, pc *pageCtx, b pages.Block, out map[string]any) map[string]any {
	p := principalFrom(ctx)
	out["title"] = nonEmpty(b.Title, b.Workflow)
	var run uuid.UUID
	var status string
	var started time.Time
	var ended *time.Time
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		wf, _, _, n, err := pageWorkflow(ctx, s, tx, b.Workflow)
		if err != nil || n != 1 {
			return err
		}
		q := `SELECT id, status, started_at, ended_at FROM runs WHERE workflow_id = $1 AND environment = $2 ORDER BY started_at DESC LIMIT 1`
		args := []any{wf, pageToolEnv}
		if pc.run != uuid.Nil {
			q = `SELECT id, status, started_at, ended_at FROM runs WHERE workflow_id = $1 AND environment = $2 AND id = $3`
			args = append(args, pc.run)
		}
		err = tx.QueryRow(ctx, q, args...).Scan(&run, &status, &started, &ended)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		s.Logger.Warn("pages: a run's status", "page", pc.page.Name, "part", b.ID, "err", err)
		out["error"] = "Taskiem could not read this part just now. Pull to try again."
		return out
	}
	if run == uuid.Nil {
		out["words"], out["tone"] = "Nothing has run yet.", "plain"
		return out
	}
	words, tone := pages.StatusWords(status)
	out["status"], out["words"], out["tone"] = status, words, tone
	if !p.Can(PermRunRead) {
		// The page's viewers see where it is, in words; the run itself
		// (its id, its times, who it waits for) is for those who may read
		// runs.
		return out
	}
	out["run"] = run
	out["started_at"] = started.UTC().Format(time.RFC3339)
	if ended != nil {
		out["ended_at"] = ended.UTC().Format(time.RFC3339)
	}
	if stillGoing(status) {
		viewer := uuid.Nil
		if p.EndUser == nil {
			viewer = p.UserID
		}
		if waits, err := s.waitingFor(ctx, p.TenantID, run, viewer); err == nil && len(waits) > 0 {
			out["waiting_for"] = waits
		}
	}
	return out
}

// drawToolPart draws a part a tool works out: its answer kept for this
// viewer and these values, or the tool started now (as the viewer), waited
// for a moment, and said to be still working past it.
func (s *Server) drawToolPart(ctx context.Context, pc *pageCtx, b pages.Block, out map[string]any) map[string]any {
	if b.Type == "figure" {
		out["label"], out["tone"], out["lead"], out["naira"] = b.Label, nonEmpty(b.Tone, "plain"), b.Lead, b.Naira
	} else {
		out["title"], out["size"], out["empty"] = b.Title, nonEmpty(b.Size, "full"), nonEmpty(b.Empty, "Nothing to show")
		out["has_detail"] = b.Detail != nil
	}
	out["from_tool"] = true
	got, err := s.pageToolAnswer(ctx, pc, b, true)
	if err != nil {
		var bad toolInputError
		var unseen errToolUnseen
		if errors.As(err, &unseen) {
			// As a part over records the viewer may not see: said in its place.
			for _, k := range []string{"tone", "lead", "naira", "size", "empty", "has_detail"} {
				delete(out, k)
			}
			out["unavailable"] = string(unseen)
		} else if le, ok := runtime.IsLimit(err); ok {
			out["error"] = le.Message
		} else if errors.As(err, &bad) {
			out["error"] = "This part's values do not fit its tool: " + strings.Join(bad, "; ") + "."
		} else {
			s.Logger.Warn("pages: a tool's part", "page", pc.page.Name, "part", b.ID, "err", err)
			out["error"] = "Taskiem could not work this part out just now. Pull to try again."
		}
		return out
	}
	switch got.status {
	case "completed":
	case "failed", "cancelled":
		out["error"] = "The tool could not work this out. Refresh to try again."
		return out
	default:
		out["working"] = "Still working…"
		return out
	}
	out["worked_at"] = got.at.UTC().Format(time.RFC3339)
	if b.Type == "figure" {
		shown, v := pages.ToolFigure(b, got.outputs[b.Source.Output])
		out["shown"] = shown
		if v != nil {
			out["value"] = *v
		}
		return out
	}
	rows, n := pages.ShapeTool(b, got.outputs, pc.values)
	out["rows"], out["count"], out["more"] = rows, n, nil
	s.markRows(ctx, pc, out)
	return out
}

// errToolUnseen is a part a tool works out that this viewer may not see:
// drawn as unavailable, in plain words.
type errToolUnseen string

func (e errToolUnseen) Error() string { return string(e) }

// toolNotFor says why p may not use tool t ("" when they may): its roles,
// run.start, and a key kept to another environment.
func toolNotFor(p *Principal, t toolRow) string {
	switch {
	case p.Environment != "" && p.Environment != t.Environment:
		return "This key cannot use this part's tool."
	case !p.Can(PermRunStart):
		return "Your role does not use tools, so this part is not shown."
	case !mayStart(p, t.Roles):
		return "Only " + startRolesWords(t.Roles) + " can see this."
	}
	return ""
}

// pageToolGot is what a tool feeding a part answered.
type pageToolGot struct {
	run     uuid.UUID
	status  string
	at      time.Time
	outputs map[string]any
}

// pageToolAnswer is the answer kept for this tool, viewer and values on the page (two parts one tool feeds with the same values share a run), or
// (start) the tool started for them now. Without start, the last answer
// kept, however old (a button pressed on its rows reads them).
func (s *Server) pageToolAnswer(ctx context.Context, pc *pageCtx, b pages.Block, start bool) (pageToolGot, error) {
	p := principalFrom(ctx)
	// Held to the tool before anything kept is read or anything started:
	// a page opens to its roles, but what a tool answers is for whoever
	// may use that tool (its roles, run.start, a key's environment).
	t, ok := s.pageTool(ctx, pc, b.Source.Tool)
	if !ok {
		return pageToolGot{}, fmt.Errorf("%w: this part's tool is not available any more", errConflict)
	}
	if why := toolNotFor(p, t); why != "" {
		return pageToolGot{}, errToolUnseen(why)
	}
	inputs := pages.ToolInputs(b.Source, pc.values)
	sum := sha256.Sum256(canonicalJSON(inputs))
	digest := sum[:]
	var got pageToolGot
	fresh := false
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var started time.Time
		err := tx.QueryRow(ctx, `SELECT run_id, started_at FROM page_source_cache WHERE page_id = $1 AND block = $2 AND viewer = $3 AND inputs = $4`,
			pc.row.ID, "tool:"+b.Source.Tool, p.Actor(), digest).Scan(&got.run, &started)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		fresh = err == nil && time.Since(started) < pageSourceFor
		return err
	})
	if err != nil {
		return got, err
	}
	if got.run != uuid.Nil && (fresh || !start) {
		return s.toolAnswerOf(ctx, got.run)
	}
	if !start {
		return got, fmt.Errorf("%w: this part has not been worked out yet: open the page again", errConflict)
	}
	if t.Asks() != wdcheck.CareAnswers {
		return got, fmt.Errorf("%w: this part's tool does not answer at once", errConflict)
	}
	var def []byte
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def)
	}); err != nil {
		return got, err
	}
	raw, _ := json.Marshal(inputs)
	if probs := s.inputProblems(t.Workflow, t.Version, def, raw); len(probs) > 0 {
		return got, toolInputError(probs)
	}
	run, _, err := s.runTool(ctx, p, t, inputs, toolChannelAPI, "")
	if err != nil {
		return got, err
	}
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		// Kept answers past their time are let go here, the business's own.
		if _, err := tx.Exec(ctx, `DELETE FROM page_source_cache WHERE started_at < now() - interval '1 day'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO page_source_cache (tenant_id, page_id, block, viewer, inputs, run_id) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, page_id, block, viewer, inputs) DO UPDATE SET run_id = EXCLUDED.run_id, started_at = now()`,
			p.TenantID, pc.row.ID, "tool:"+b.Source.Tool, p.Actor(), digest, run)
		return err
	}); err != nil {
		return got, err
	}
	deadline := time.Now().Add(pageToolWait)
	for {
		got, err = s.toolAnswerOf(ctx, run)
		if err != nil || !stillGoing(got.status) || time.Now().After(deadline) {
			return got, err
		}
		select {
		case <-ctx.Done():
			return got, nil
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// toolAnswerOf reads a run's state and, once completed, its outputs:
// personal data it sealed masked (never opened for a page), an output
// declared personal masked whatever its name, as a tool's answer is.
func (s *Server) toolAnswerOf(ctx context.Context, run uuid.UUID) (pageToolGot, error) {
	got := pageToolGot{run: run}
	p := principalFrom(ctx)
	var wf uuid.UUID
	var version int
	var def []byte
	var ended *time.Time
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT r.workflow_id, r.version, r.status, r.ended_at, wv.definition FROM runs r
			JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, run).
			Scan(&wf, &version, &got.status, &ended, &def); err != nil {
			return err
		}
		if got.status != "completed" {
			return nil
		}
		var payload []byte
		err := tx.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = $2 ORDER BY seq DESC LIMIT 1`, run, history.RunCompleted).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var done history.RunCompletedPayload
		_ = json.Unmarshal(payload, &done)
		got.outputs, _ = maskEnvelopes(any(done.Outputs)).(map[string]any)
		return nil
	})
	if err != nil {
		return got, err
	}
	if ended != nil {
		got.at = *ended
	}
	if got.outputs == nil {
		got.outputs = map[string]any{}
	}
	if d, err := s.definitionFor(wf, version, def); err == nil {
		maskDeclared(d, got.outputs)
	}
	return got, nil
}

// clearPageTools lets go of what tools answered on a page for p (Refresh,
// and a button pressed there: the parts it could have changed are worked
// out again).
func (s *Server) clearPageTools(ctx context.Context, p *Principal, page uuid.UUID) error {
	return db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM page_source_cache WHERE page_id = $1 AND viewer = $2`, page, p.Actor())
		return err
	})
}

// refreshPage is POST /v1/pages/{name}/refresh: what tools answered on
// the page for the caller is let go, so the page is worked out again.
func (s *Server) refreshPage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var page uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		if !mayOpenPage(p, pg) {
			return errNoPage
		}
		page = pg.ID
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.clearPageTools(r.Context(), p, page); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- pressing ---

type pageActReq struct {
	Block  string `json:"block"`
	Row    string `json:"row,omitempty"`
	Action int    `json:"action"`
	// Yes: the person said yes to a signal.
	Yes bool `json:"yes,omitempty"`
}

// actPage is POST /v1/pages/{name}/act (?filters= as the view had them):
// a button pressed on the page or on one of its rows.
func (s *Server) actPage(w http.ResponseWriter, r *http.Request) {
	var req pageActReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	pc, ok := s.openPage(w, r)
	if !ok {
		return
	}
	i := slices.IndexFunc(pc.page.Blocks, func(b pages.Block) bool { return b.ID == req.Block })
	if i < 0 || req.Action < 0 || req.Action >= len(pc.page.Blocks[i].Actions) {
		writeErr(w, http.StatusNotFound, "this page has no such button")
		return
	}
	b := pc.page.Blocks[i]
	if _, broken := pc.broken[i]; broken {
		writeErr(w, http.StatusConflict, "this part needs fixing before its buttons work")
		return
	}
	if why, off := pc.off[i]; off {
		writeErr(w, http.StatusConflict, why)
		return
	}
	a := b.Actions[req.Action]
	act, err := s.pageRowAct(r.Context(), pc, b, req.Row)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !b.ActionShows(req.Action, act) {
		writeErr(w, http.StatusConflict, "this button is not there for this row any more")
		return
	}
	switch a.Kind() {
	case pages.ActTool:
		s.actTool(w, r, pc, b, a, req, act)
	case pages.ActSignal:
		s.actSignal(w, r, pc, b, a, req, act)
	case pages.ActApprove:
		s.actApprove(w, r, pc, b, a, act)
	default:
		writeErr(w, http.StatusNotFound, "this page has no such button")
	}
}

// pageRowAct reads the row a button was pressed on again, as the part
// shows it to this viewer, and gives the names its words read. The page's
// own buttons read the filters alone.
func (s *Server) pageRowAct(ctx context.Context, pc *pageCtx, b pages.Block, row string) (map[string]any, error) {
	gone := fmt.Errorf("%w: this row is not on the page any more", pgx.ErrNoRows)
	switch {
	case b.Type == "buttons":
		return pages.FiltersAct(pc.values), nil
	case b.Type != "list" && b.Type != "table":
		return nil, fmt.Errorf("%w: this part has no buttons", pgx.ErrNoRows)
	case row == "":
		return nil, fmt.Errorf("%w: say which row the button was pressed on", errBadRequest)
	case b.Source.Tool != "":
		got, err := s.pageToolAnswer(ctx, pc, b, false)
		var unseen errToolUnseen
		if errors.As(err, &unseen) {
			return nil, fmt.Errorf("%w: %s", errForbidden, string(unseen))
		}
		if err != nil {
			return nil, err
		}
		item, ok := pages.ToolItem(b, got.outputs, row)
		if !ok {
			return nil, gone
		}
		return pages.ToolAct(got.outputs, item, pc.values), nil
	}
	t := pc.table(b)
	read, err := pc.page.ReadFor(b, t, pc.values, nil)
	if err != nil {
		return nil, err
	}
	if read.None {
		return nil, gone
	}
	if b.Source.Records != "" {
		v, _ := pages.RecordViewNamed(b.Source.Records)
		p := principalFrom(ctx)
		if !p.Can(v.Perm) {
			return nil, fmt.Errorf("%w: %s", errForbidden, v.Unseen())
		}
		read.Filters = append(read.Filters, map[string]any{"column": "id", "op": "eq", "value": row})
		var raw []map[string]any
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			sql, args, err := v.RecordsSelect(read, 1, 0)
			if err != nil {
				return err
			}
			args[0] = p.TenantID
			raw, _, err = s.recordRows(ctx, tx, v, sql, args)
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, gone
		}
		return pages.RowAct(t, raw[0], pc.values), nil
	}
	id, err := strconv.ParseInt(row, 10, 64)
	if err != nil {
		return nil, gone
	}
	tr, err := s.pageReader(ctx, pc, t)
	if err != nil {
		return nil, err
	}
	read.Filters = append(read.Filters, map[string]any{"column": "id", "op": "eq", "value": id})
	res, err := tr.call(ctx, s, "read_rows", map[string]any{"table": t.Name, "filters": read.Filters, "limit": 1}, "")
	if err != nil {
		return nil, err
	}
	for _, x := range rowsOf(res) {
		if m, ok := x.(map[string]any); ok {
			return pages.RowAct(t, m, pc.values), nil
		}
	}
	return nil, gone
}

// actTool gives the tool a button opens and the values it fills in: the
// row's values as this viewer is shown them, a masked one left to be
// asked. The press is a tool_buttons row of the page (a once button is
// spent by the first run started from it).
func (s *Server) actTool(w http.ResponseWriter, r *http.Request, pc *pageCtx, b pages.Block, a pages.Action, req pageActReq, act map[string]any) {
	ctx := r.Context()
	p := principalFrom(ctx)
	t, ok := s.pageTool(ctx, pc, a.Tool)
	if !ok {
		writeErr(w, http.StatusNotFound, "this button's tool is not available any more")
		return
	}
	if !p.Can(PermRunStart) || (p.Environment != "" && p.Environment != t.Environment) {
		s.fail(w, r, fmt.Errorf("%w: your role does not use tools", errForbidden))
		return
	}
	if !mayStart(p, t.Roles) {
		s.toolRefused(w, r, errNotYours(t.Name, t.Roles), t.Command)
		return
	}
	inputs := pages.ActionValues(b, a.Inputs, act)
	for k, v := range inputs {
		if masked(v) {
			delete(inputs, k) // shown masked: asked, not filled in
		}
	}
	version := 0
	if pc.row.Version != nil {
		version = *pc.row.Version
	}
	if pc.draft {
		version = 0
	}
	stored, _ := maskDeep(maskEnvelopes(any(inputs))).(map[string]any)
	place := pageButtonPlace(version, b, req, stored, act)
	var id uuid.UUID
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		raw, _ := json.Marshal(stored)
		if stored == nil {
			raw = []byte("{}")
		}
		// A button not used yet is kept another seven days; one used stays
		// spent. What it was minted for is never changed: another row, or
		// other values, is another place.
		return tx.QueryRow(ctx, `INSERT INTO tool_buttons (id, tenant_id, environment, workflow_id, label, inputs, once, from_page, place, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, from_page, place) WHERE from_page IS NOT NULL DO UPDATE
			SET expires_at = CASE WHEN tool_buttons.used_at IS NULL THEN EXCLUDED.expires_at ELSE tool_buttons.expires_at END,
			    workflow_id = CASE WHEN tool_buttons.used_at IS NULL THEN EXCLUDED.workflow_id ELSE tool_buttons.workflow_id END
			RETURNING id`, uuid.New(), p.TenantID, t.Environment, t.Workflow, a.Label, raw, a.Once, pc.row.ID, place, time.Now().Add(buttonFor)).Scan(&id)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Held as any button: spent, gone or not the presser's.
	if _, err := s.pressButton(ctx, p, id); err != nil {
		s.toolRefused(w, r, err, t.Command)
		return
	}
	_ = s.clearPageTools(ctx, p, pc.row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"kind": pages.ActTool,
		"tool": pressed{Button: id, Command: t.Command, Name: t.Name, Inputs: inputs, Once: a.Once}})
}

// pageButtonPlace is where a page's button is minted: the page's version,
// the part and the button, and a digest of what it is pressed on, the
// same for every viewer: a table's row by its id (values filled from a
// personal column differ by who presses), a tool's row by the item itself,
// masked (its place in the list changes), the page's own buttons by their
// values (which read the filters). A once button is so spent for that row
// or those values alone.
func pageButtonPlace(version int, b pages.Block, req pageActReq, stored map[string]any, act map[string]any) string {
	var ident map[string]any
	switch {
	case b.Source != nil && b.Source.Tool != "":
		ident = map[string]any{"item": maskDeep(maskEnvelopes(act["item"]))}
	case req.Row != "":
		ident = map[string]any{"row": req.Row}
	default:
		ident = map[string]any{"inputs": stored}
	}
	sum := sha256.Sum256(canonicalJSON(ident))
	return fmt.Sprintf("v%d/%s/%d/%x", version, b.ID, req.Action, sum[:12])
}

// actSignal sends a button's signal, after a Yes, to the one run it names:
// a run of the workflow the button names, in this business, waiting at a
// step that declares a page may send it; held to the workflow's roles.
func (s *Server) actSignal(w http.ResponseWriter, r *http.Request, pc *pageCtx, b pages.Block, a pages.Action, req pageActReq, act map[string]any) {
	ctx := r.Context()
	p := principalFrom(ctx)
	sig := a.Signal
	moved := func() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This has moved on: it is not waiting for " + a.Label + " any more.", "code": "moved_on"})
	}
	// A signal moves a run on: as starting one, it needs run.start, and a
	// key kept to another environment never reaches prod's runs.
	if !p.Can(PermRunStart) || (p.Environment != "" && p.Environment != pageToolEnv) {
		s.fail(w, r, fmt.Errorf("%w: your role cannot tell a run something", errForbidden))
		return
	}
	input := pages.ActionValues(b, sig.Input, act)
	for k, v := range input {
		if masked(v) {
			delete(input, k) // a run is never told a mask for a value
		}
	}
	type wait struct {
		run  uuid.UUID
		step string
		wf   uuid.UUID
		ver  int
		def  []byte
	}
	var found *wait
	wfName := sig.Workflow
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		wf, _, _, n, err := pageWorkflow(ctx, s, tx, sig.Workflow)
		if err != nil || n != 1 {
			return err
		}
		var rows pgx.Rows
		if sig.Run != "" {
			run, perr := uuid.Parse(pages.Word(b, sig.Run, act))
			if perr != nil {
				return nil
			}
			rows, err = tx.Query(ctx, `SELECT w.run_id, w.step_id, r.workflow_id, r.version, wv.definition FROM signal_waits w
				JOIN runs r ON r.id = w.run_id JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version
				WHERE w.run_id = $1 AND w.event = $2 AND r.workflow_id = $3 AND r.environment = $4`, run, sig.Name, wf, pageToolEnv)
		} else {
			to := pages.Word(b, sig.To, act)
			if to == "" || strings.Contains(to, "•") {
				return nil
			}
			rows, err = tx.Query(ctx, `SELECT w.run_id, w.step_id, r.workflow_id, r.version, wv.definition FROM signal_waits w
				JOIN runs r ON r.id = w.run_id JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version
				WHERE w.environment = $4 AND w.event = $2 AND w.correlation = $1 AND r.workflow_id = $3 ORDER BY r.started_at LIMIT 2`, to, sig.Name, wf, pageToolEnv)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x wait
			if err := rows.Scan(&x.run, &x.step, &x.wf, &x.ver, &x.def); err != nil {
				return err
			}
			if found == nil {
				found = &x
			}
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if found == nil {
		moved()
		return
	}
	// The run's own version declares the step a page may send it.
	d, err := s.definitionFor(found.wf, found.ver, found.def)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if st := stepOfInstance(d, found.step); st == nil || st.Signal == nil || !st.Signal.Pages || st.Signal.Event != sig.Name {
		moved()
		return
	}
	// Held to the roles of the version the run is of, not of the one
	// deployed now.
	if !mayStart(p, d.Roles) {
		s.fail(w, r, fmt.Errorf("%w: only %s can tell %s this", errForbidden, startRolesWords(d.Roles), wfName))
		return
	}
	if !req.Yes {
		writeJSON(w, http.StatusOK, map[string]any{"kind": pages.ActSignal, "asks": a.Label + "?", "sent": false})
		return
	}
	took, err := s.Store.DeliverSignalToRun(ctx, p.TenantID, found.run, found.step, sig.Name, input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !took {
		moved()
		return
	}
	_ = s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "page.signal", pc.page.Name, map[string]any{"part": b.ID, "button": a.Label, "run": found.run, "signal": sig.Name})
	})
	_ = s.clearPageTools(ctx, p, pc.row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"kind": pages.ActSignal, "sent": true, "run": found.run, "words": "Done: " + a.Label + "."})
}

// actApprove gives the approval a button opens, when it waits for this
// person: a run of the workflow the button names, at the step named.
// Deciding it is the approval's own (POST /v1/approvals/{run}/{step}).
func (s *Server) actApprove(w http.ResponseWriter, r *http.Request, pc *pageCtx, b pages.Block, a pages.Action, act map[string]any) {
	ctx := r.Context()
	p := principalFrom(ctx)
	run, err := uuid.Parse(pages.Word(b, a.Approve.Run, act))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "There is no approval here to open.", "code": "moved_on"})
		return
	}
	var item *approvalItem
	err = s.tx(r, func(tx pgx.Tx) error {
		wf, _, _, n, err := pageWorkflow(ctx, s, tx, a.Approve.Workflow)
		if err != nil || n != 1 {
			return err
		}
		var of uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT workflow_id FROM runs WHERE id = $1`, run).Scan(&of); err != nil || of != wf {
			return nil //nolint:nilerr // another workflow's run, or none: nothing to open
		}
		list, raws, err := s.openApprovalsTx(ctx, tx, p)
		if err != nil {
			return err
		}
		for i := range list {
			if list[i].RunID != run || list[i].StepID != a.Approve.Step {
				continue
			}
			var v any
			if len(raws[i]) > 0 {
				if err := json.Unmarshal(raws[i], &v); err != nil {
					return err
				}
			}
			opened, err := pii.Open(ctx, s.Store.PII, tx, p.TenantID, v, pii.Taint{})
			if err != nil {
				return err
			}
			list[i].Subject = opened
			item = &list[i]
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if item == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This approval is not waiting for you.", "code": "moved_on"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": pages.ActApprove, "approval": item})
}

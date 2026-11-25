package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Describe it for pages (G5; pages §10, 4; engine/ai/builder/pages.go): one
// sentence ("a page of open tickets with how many are waiting, and a button
// to add one") is drafted into a page and what it would make (its tables
// and its tools), shown, and made only when the person says so.
//
// It is the workflow builder's machinery (api/ai.go): a build is a row of
// ai_builds (kind 'page', migration 00710) run in the background with no
// principal, its model calls kept in ai_interactions, held to the same
// monthly budget and rate. The model is told the business's tables (their
// columns' names and types), its tools in use that the person may start
// (their inputs' and outputs' names and types), its kinds of saved things
// (their fields' keys and types) and its pages' names: never a row, a
// value, a saved thing, or anything a person typed but the goal
// (TestDescribeItMakesAWorkingPage reads every prompt).
//
// Making it is installBundle's (pages_starters.go): the tables, the tools
// and the page, as the person, each with its own permission.

func (s *Server) pageDescribeRoutes(r chi.Router) {
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/page-builds", s.describePage)
	r.With(s.need(PermPageEdit)).Get("/page-builds/{id}", s.getPageBuild)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/page-builds/{id}/make", s.makePageBuild)
}

// describePage is POST /v1/page-builds {goal}: a draft, worked out in the
// background; GET /v1/page-builds/{id} reads it.
func (s *Server) describePage(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil || s.AI.Provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "ai_disabled",
			"error": "Describe it is not set up on this deployment: start from a starter or a table instead."})
		return
	}
	p := principalFrom(r.Context())
	if !s.limiter("ai-build:"+p.TenantID.String(), 20*time.Second, 5).Allow() {
		w.Header().Set("Retry-After", "20")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited", "error": "too many AI builds at once; try again shortly"})
		return
	}
	var req struct {
		Goal string `json:"goal"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" || len(req.Goal) > aiGoalMax {
		s.fail(w, r, fmt.Errorf("%w: say what the page is for, in up to %d letters", errBadRequest, aiGoalMax))
		return
	}
	ctx := r.Context()
	limit, used, err := s.aiBudget(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if limit > 0 && used >= limit {
		s.Store.LimitHit(ctx, p.TenantID, "ai_monthly_tokens")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "ai_budget_exhausted", "limit": "ai_monthly_tokens",
			"error": "This month's AI budget is used up: start from a starter or a table instead."})
		return
	}
	who := aiActor{tenant: p.TenantID, actor: p.Actor(), actorType: p.ActorType(), ip: clientIP(r)}
	id := uuid.Must(uuid.NewV7())
	var pctx builder.PageContext
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if pctx, err = s.pageBuildContext(ctx, tx, p); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ai_builds (id, tenant_id, actor, goal, environment, provider, model, channel, kind)
			VALUES ($1, $2, $3, $4, '', $5, $6, 'web', 'page')`, id, p.TenantID, who.actor, pii.Redact(req.Goal), s.AI.Provider.Name(), s.AI.Provider.Model())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.runPageBuild(id, who, *p, builder.PageRequest{Goal: req.Goal, Context: pctx})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "running"})
}

// pageBuildContext is what the model is told of the business: names and
// types only.
func (s *Server) pageBuildContext(ctx context.Context, tx pgx.Tx, p *Principal) (builder.PageContext, error) {
	c := builder.PageContext{Tables: []builder.PageTable{}, Tools: []builder.PageTool{}, SavedKinds: []builder.PageKind{}, Pages: []string{}}
	tables, err := datatables.ListTables(ctx, tx)
	if err != nil {
		return c, err
	}
	for _, t := range tables {
		if t.KeptBy != "" {
			continue // Taskiem's own (stock, the books): not a page's to show
		}
		pt := builder.PageTable{Name: t.Name, Title: t.Title, Columns: []builder.PageField{}}
		for _, col := range t.Columns {
			pt.Columns = append(pt.Columns, builder.PageField{Name: col.Name, Type: col.Type})
		}
		c.Tables = append(c.Tables, pt)
	}
	rows, err := tx.Query(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND audience = 'members' ORDER BY command LIMIT 200`, pageToolEnv)
	if err != nil {
		return c, err
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) { return scanTool(row) })
	if err != nil {
		return c, err
	}
	for _, t := range all {
		if !mayStart(p, t.Roles) {
			continue // a tool the person may not start is not theirs to build on
		}
		var def []byte
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def); err != nil {
			return c, err
		}
		d, err := s.definitionFor(t.Workflow, t.Version, def)
		if err != nil {
			continue
		}
		pt := builder.PageTool{Command: t.Command, Name: t.Name, AnswersAtOnce: t.Asks() == wdcheck.CareAnswers, Inputs: []builder.PageField{}, Outputs: []builder.PageField{}}
		for _, o := range d.Outputs {
			pt.Outputs = append(pt.Outputs, builder.PageField{Name: o.Name, Type: o.Type})
		}
		if schema, _ := d.InputsSchema(); schema != nil {
			if m, ok := schema.(map[string]any); ok {
				props, _ := m["properties"].(map[string]any)
				for k, v := range props {
					prop, _ := v.(map[string]any)
					typ, _ := prop["type"].(string)
					pt.Inputs = append(pt.Inputs, builder.PageField{Name: k, Type: typ})
				}
			}
		}
		slices.SortFunc(pt.Inputs, func(a, b builder.PageField) int { return strings.Compare(a.Name, b.Name) })
		c.Tools = append(c.Tools, pt)
	}
	kinds, _, err := pageSavedKinds(ctx, tx)
	if err != nil {
		return c, err
	}
	for _, k := range kinds {
		pk := builder.PageKind{Name: k.Name, Fields: []builder.PageField{}}
		for _, f := range k.Fields {
			pk.Fields = append(pk.Fields, builder.PageField{Name: f.Key, Type: f.Type})
		}
		c.SavedKinds = append(c.SavedKinds, pk)
	}
	slices.SortFunc(c.SavedKinds, func(a, b builder.PageKind) int { return strings.Compare(a.Name, b.Name) })
	prow, err := tx.Query(ctx, `SELECT name FROM pages ORDER BY name LIMIT 500`)
	if err != nil {
		return c, err
	}
	names, err := pgx.CollectRows(prow, pgx.RowTo[string])
	if err != nil {
		return c, err
	}
	c.Pages = append(c.Pages, names...)
	return c, nil
}

// runPageBuild drafts the page in the background and keeps the proposal.
// The model is called with no principal; the draft is checked as the
// person who asked would find it (asker: their business, its areas).
func (s *Server) runPageBuild(id uuid.UUID, who aiActor, asker Principal, req builder.PageRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), aiBuildTimeout)
	defer cancel()
	if s.Done != nil {
		go func() {
			select {
			case <-s.Done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	inTx := func(fn func(pgx.Tx) error) error {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{who.tenant}, fn)
	}
	b := &builder.PageBuilder{
		Provider: s.AI.Provider,
		Validate: func(d builder.PageDraft) []builder.Problem {
			var out []builder.Problem
			err := inTx(func(tx pgx.Tx) error {
				out = s.checkPageDraft(context.WithValue(ctx, ctxKey{}, &asker), tx, who.tenant, d)
				return nil
			})
			if err != nil {
				out = append(out, builder.Problem{Path: "/", Message: "the draft could not be checked just now"})
			}
			return out
		},
		Meter:     aiMeter{s: s, tenant: who.tenant, purpose: "build", ttl: aiBuildTimeout + time.Minute},
		Recorder:  aiRecorder{s: s, build: id, who: who, provider: s.AI.Provider.Name()},
		Effort:    s.AI.Effort,
		MaxTokens: s.AI.MaxTokens,
		Progress: func(stage string) {
			_ = inTx(func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ai_builds SET stage = $2 WHERE id = $1`, id, stage)
				return err
			})
		},
	}
	prop, err := b.Build(ctx, req)
	ctx = context.WithoutCancel(ctx)
	status, msg := "proposed", ""
	switch {
	case errors.Is(err, ai.ErrBudgetExhausted):
		status, msg = "failed", "This month's AI budget is used up: start from a starter or a table instead."
		s.Store.LimitHit(ctx, who.tenant, "ai_monthly_tokens")
	case errors.Is(err, builder.ErrRefused):
		status, msg = "failed", "The model declined this request: say it another way, or start from a starter."
	case err != nil:
		status, msg = "failed", "Describe it could not draft the page: try again, or start from a starter."
		s.Logger.Error("page build failed", "build", id, "err", err)
	}
	var raw []byte
	rounds, total, model := 0, int64(0), ""
	var valid *bool
	detail := map[string]any{"build": id.String(), "status": status, "ip": who.ip, "provider": s.AI.Provider.Name(), "kind": "page"}
	if prop != nil {
		raw, _ = json.Marshal(prop)
		rounds, total, model = prop.Rounds, prop.Usage.Total(), prop.Model
		v := prop.Valid()
		valid = &v
		detail["model"], detail["co_author"], detail["rounds"], detail["valid"], detail["tokens"] = prop.Model, "ai:"+prop.Model, prop.Rounds, v, total
	}
	audit, _ := json.Marshal(detail)
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{who.tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE ai_builds SET status = $2, stage = 'done', error = NULLIF($3, ''), proposal = $4, rounds = $5, valid = $6,
			total_tokens = $7, model = COALESCE(NULLIF($8, ''), model), finished_at = now() WHERE id = $1`, id, status, msg, raw, rounds, valid, total, model); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, who.tenant, who.actorType, who.actor, "ai.propose", id.String(), audit)
		return err
	}); err != nil {
		s.Logger.Error("recording a page build", "build", id, "err", err)
	}
}

// pageDraftBundle reads a draft as a bundle: its tools as tool specs.
func pageDraftBundle(d builder.PageDraft) (pages.Bundle, []builder.Problem) {
	b := pages.Bundle{Page: d.Page, Tables: []pages.TableSpec{}, Tools: []pages.ToolSpec{}}
	var ps []builder.Problem
	for _, t := range d.Tables {
		spec := pages.TableSpec{Name: t.Name, Title: t.Title}
		for _, c := range t.Columns {
			spec.Columns = append(spec.Columns, pages.ColumnSpec{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required})
		}
		b.Tables = append(b.Tables, spec)
	}
	for i, raw := range d.Tools {
		var t pages.ToolSpec
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&t); err != nil {
			ps = append(ps, builder.Problem{Path: fmt.Sprintf("/tools/%d", i), Message: "this is not a tool Taskiem can make: " + err.Error()})
			continue
		}
		b.Tools = append(b.Tools, t)
	}
	return b, ps
}

// checkPageDraft holds a draft to what making and publishing it will: each
// new table well formed and not one the business has; each new tool an add
// or a change over one of the tables, its command free; and the page,
// through Parse and Check, against the business's tables and tools with
// the new ones beside them.
func (s *Server) checkPageDraft(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, d builder.PageDraft) []builder.Problem {
	b, ps := pageDraftBundle(d)
	tables, err := datatables.ListTables(ctx, tx)
	if err != nil {
		return append(ps, builder.Problem{Path: "/", Message: "the business's tables could not be read"})
	}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return append(ps, builder.Problem{Path: "/", Message: "the business's limits could not be read"})
	}
	// Taskiem's own tables (stock, the books) are not the model's to
	// build on: it is never told of them, and a draft naming one is refused.
	var all []datatables.Table
	specs := map[string]pages.TableSpec{}
	kept := map[string]bool{}
	for _, t := range tables {
		if t.KeptBy != "" {
			kept[t.Name] = true
			continue
		}
		all = append(all, t)
		specs[t.Name] = tableSpecOf(t)
	}
	for i, t := range b.Tables {
		at := fmt.Sprintf("/tables/%d", i)
		if _, has := specs[t.Name]; has || kept[t.Name] {
			ps = append(ps, builder.Problem{Path: at + "/name", Message: fmt.Sprintf("the business has a table called %s already: use it, or name the new one differently", t.Name)})
			continue
		}
		req := tableReq{Name: t.Name, Title: t.Title}
		for _, c := range t.Columns {
			req.Columns = append(req.Columns, columnReq{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required})
		}
		if err := datatables.CheckTableName(t.Name); err != nil {
			ps = append(ps, builder.Problem{Path: at + "/name", Message: err.Error()})
			continue
		}
		if err := datatables.CheckColumns(req.columns(), lim.MaxDataTableColumns); err != nil {
			ps = append(ps, builder.Problem{Path: at + "/columns", Message: err.Error()})
			continue
		}
		specs[t.Name] = t
		all = append(all, t.Table())
	}
	proposed := map[string]pages.ToolInfo{}
	var given []string
	for i, t := range b.Tools {
		at := fmt.Sprintf("/tools/%d", i)
		if t.Kind != "add" && t.Kind != "change" {
			ps = append(ps, builder.Problem{Path: at + "/kind", Message: "Describe it makes tools that add a row (add) or change one (change)"})
			continue
		}
		tbl, ok := specs[t.Table]
		if !ok {
			ps = append(ps, builder.Problem{Path: at + "/table", Message: fmt.Sprintf("the tool /%s works on the table %s, which is neither the business's nor one the draft makes", t.Command, t.Table)})
			continue
		}
		if why := t.Check(tbl); why != "" {
			ps = append(ps, builder.Problem{Path: at, Message: why})
			continue
		}
		if why := memberRolesProblem(ctx, tx, t.Roles); why != "" {
			ps = append(ps, builder.Problem{Path: at + "/roles", Message: fmt.Sprintf("the tool /%s: %s", t.Command, why)})
			continue
		}
		if c, err := s.freeToolCommandTx(ctx, tx, tenant, t.Command, uuid.Nil, given); err != nil || c != t.Command {
			ps = append(ps, builder.Problem{Path: at + "/command", Message: fmt.Sprintf("the word /%s is taken: pick another", t.Command)})
			continue
		}
		given = append(given, t.Command)
		proposed[t.Command] = t.Info(tbl)
	}
	page, parsed := pages.Parse(b.Page)
	for _, p := range parsed {
		ps = append(ps, builder.Problem{Path: "/page" + p.Path, Message: p.Message})
	}
	if page == nil || len(parsed) > 0 {
		return ps
	}
	env, err := s.pageEnv(ctx, tx, uuid.Nil)
	if err != nil {
		return append(ps, builder.Problem{Path: "/", Message: "the page could not be checked just now"})
	}
	env.Tables = all
	taken, tool := env.Taken, env.Tool
	env.Taken = func(name string) string {
		if _, ok := proposed[name]; ok {
			return "A tool the draft makes is called " + name + ": pick another short name for the page."
		}
		return taken(name)
	}
	env.Tool = func(command string) (pages.ToolInfo, bool) {
		if info, ok := proposed[command]; ok {
			return info, true
		}
		return tool(command)
	}
	for _, p := range pages.Check(page, env) {
		ps = append(ps, builder.Problem{Path: "/page" + p.Path, Message: p.Message})
	}
	return ps
}

// memberRolesProblem says why a drafted tool's roles cannot be ("" when
// they can): each must be one of the business's own roles for its people
// (built in, or made), never anyone outside it.
func memberRolesProblem(ctx context.Context, tx pgx.Tx, roles []string) string {
	for _, r := range roles {
		if rolePermissions[r] != nil {
			continue
		}
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, r).Scan(&has); err != nil || !has {
			return fmt.Sprintf("who may use it names %s, which is not one of the business's roles", r)
		}
	}
	return ""
}

// pageBuildView is a page build as the web reads it.
type pageBuildView struct {
	ID         uuid.UUID             `json:"id"`
	Status     string                `json:"status"`
	Stage      string                `json:"stage"`
	Error      *string               `json:"error"`
	Goal       string                `json:"goal"`
	CreatedAt  time.Time             `json:"created_at"`
	FinishedAt *time.Time            `json:"finished_at"`
	Page       *string               `json:"page_name"`
	Proposal   *builder.PageProposal `json:"proposal"`
	Review     map[string]any        `json:"review,omitempty"`
}

// getPageBuild is GET /v1/page-builds/{id}: the draft, and, once drafted,
// what it would make and use, in plain words.
func (s *Server) getPageBuild(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var v pageBuildView
	err = s.tx(r, func(tx pgx.Tx) error {
		var prop []byte
		if err := tx.QueryRow(r.Context(), `SELECT id, status, stage, error, goal, created_at, finished_at, page_name, proposal FROM ai_builds WHERE id = $1 AND kind = 'page'`, id).
			Scan(&v.ID, &v.Status, &v.Stage, &v.Error, &v.Goal, &v.CreatedAt, &v.FinishedAt, &v.Page, &prop); err != nil {
			return err
		}
		if prop == nil {
			return nil
		}
		v.Proposal = &builder.PageProposal{}
		if err := json.Unmarshal(prop, v.Proposal); err != nil {
			return err
		}
		v.Review, err = s.pageBuildReview(r.Context(), tx, v.Proposal)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// pageBuildReview says, in plain words, what making the draft makes (its
// tables and tools), what of the business's it uses, and what the page
// shows.
func (s *Server) pageBuildReview(ctx context.Context, tx pgx.Tx, prop *builder.PageProposal) (map[string]any, error) {
	b, _ := pageDraftBundle(prop.PageDraft)
	tables, err := datatables.ListTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	var own []pages.TableSpec
	for _, t := range tables {
		if t.KeptBy == "" {
			own = append(own, tableSpecOf(t))
		}
	}
	out := map[string]any{"makes": nonNil(b.WordsOver(own)), "uses": []string{}, "shows": []string{}, "title": "", "parts": []string{}}
	page, _ := pages.Parse(b.Page)
	if page == nil {
		return out, nil
	}
	out["title"] = page.Title
	parts := []string{}
	for _, bl := range page.Blocks {
		parts = append(parts, bl.Type)
	}
	out["parts"] = parts
	uses := []string{}
	for _, name := range page.UsesTables() {
		if t, ok := pages.Lookup(tables, name); ok {
			uses = append(uses, "The table "+nonEmpty(t.Title, t.Name))
		}
	}
	made := map[string]bool{}
	for _, t := range b.Tools {
		made[t.Command] = true
	}
	for _, c := range page.ToolsPressed() {
		if !made[c] {
			uses = append(uses, "The tool /"+c)
		}
	}
	for _, k := range page.SavedKinds() {
		uses = append(uses, "Your saved "+k+" things")
	}
	out["uses"] = uses
	all := slices.Clone(tables)
	for _, t := range b.Tables {
		all = append(all, t.Table())
	}
	out["shows"] = nonNil(pages.Shows(page, all))
	return out, nil
}

// makePageBuild is POST /v1/page-builds/{id}/make {publish}: the draft's
// tables, tools and page made, as the person (installBundle).
func (s *Server) makePageBuild(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req bundleReq
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var prop builder.PageProposal
	var refused string
	// Claimed first, so it is made once.
	err = s.tx(r, func(tx pgx.Tx) error {
		var status string
		var raw []byte
		if err := tx.QueryRow(r.Context(), `SELECT status, proposal FROM ai_builds WHERE id = $1 AND kind = 'page' FOR UPDATE`, id).Scan(&status, &raw); err != nil {
			return err
		}
		switch {
		case status == "saved":
			refused = "This page was made already."
			return nil
		case status != "proposed" || raw == nil:
			refused = "This draft is not ready to be made."
			return nil
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			return err
		}
		if !prop.Valid() {
			refused = "The draft still has problems: describe it again, or change the page after making it from a starter."
			return nil
		}
		_, err := tx.Exec(r.Context(), `UPDATE ai_builds SET status = 'saved', saved_at = now() WHERE id = $1`, id)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if refused != "" {
		writeErr(w, http.StatusConflict, refused)
		return
	}
	b, _ := pageDraftBundle(prop.PageDraft)
	made, err := s.makeBundle(r, b, map[string]any{"build": id.String()})
	if err != nil {
		// Not made (the plan had no room, a permission lacked, a table
		// could not be made): the draft may be made again.
		_ = s.tx(r, func(tx pgx.Tx) error {
			_, err := tx.Exec(r.Context(), `UPDATE ai_builds SET status = 'proposed', saved_at = NULL WHERE id = $1 AND page_name IS NULL`, id)
			return err
		})
		s.bundleFail(w, r, made, err)
		return
	}
	// Made: the build names its page before anything is published, so it
	// is never made twice whatever publishing does.
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE ai_builds SET page_name = $2, status = 'saved' WHERE id = $1`, id, made.Page)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Publish {
		made = s.publishBundle(r, made)
	} else {
		made.Waiting = "Saved as drafts: publish the tools and the page when they are right."
	}
	writeJSON(w, http.StatusCreated, made)
}

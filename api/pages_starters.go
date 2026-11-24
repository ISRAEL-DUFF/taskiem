package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/templates"
)

// Starters, and making what a page brings (G5; pages §10, 3 and 4;
// engine/pages/starters.go, make.go). A starter (Tickets desk, Expenses
// this month, Leave, Stock count, Deliveries today, Waiting list, Service
// log) and a page Describe it drafted are each a bundle: the data tables
// it needs, the tools it presses, and the page. Installing one makes them
// together, as the person who asked, each held to its own rules:
//
//   - a table needs table.edit, and is made as the Tables page makes one;
//     a name the business already uses takes the next free one
//     (tickets_2), and the tools and the page are made to read it;
//   - a tool needs workflow.edit, and is saved as a workflow's draft (an
//     add or change of a row, or a recipe of the template library), its
//     command the next free one when taken (new-ticket-2), said so;
//   - the page needs page.edit and table.read, its name the next free one,
//     within the plan's max_pages and max_workflows.
//
// With publish, a person who may publish workflows and pages publishes the
// tools and then the page, through the very checks the Publish buttons
// use (readiness, four-eyes, the page's Check); where one cannot be
// published now (four-eyes asks another person; a business that reviews
// changes in Git publishes in the app) the rest stay drafts and the answer
// says why in plain words.

// pageStarterView is a starter as the web and the app list it.
type pageStarterView struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	About string   `json:"about"`
	Icon  string   `json:"icon"`
	Page  string   `json:"page"`
	Makes []string `json:"makes"`
	// Parts are the kinds of part its page has, in order (figure, board…).
	Parts []string `json:"parts"`
}

func starterView(st pages.Starter) pageStarterView {
	v := pageStarterView{ID: st.ID, Title: st.Title, About: st.About, Icon: st.Icon, Makes: st.Words(), Parts: []string{}}
	if page, _ := pages.Parse(st.Page); page != nil {
		v.Page = page.Title
		for _, b := range page.Blocks {
			v.Parts = append(v.Parts, b.Type)
		}
	}
	return v
}

// pageStarterRoutes mounts the starters (pageRoutes' neighbours).
func (s *Server) pageStarterRoutes(r chi.Router) {
	r.With(s.need(PermPageEdit)).Get("/page-starters", s.listPageStarters)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/page-starters/{id}/install", s.installPageStarter)
}

// listPageStarters is GET /v1/page-starters.
func (s *Server) listPageStarters(w http.ResponseWriter, r *http.Request) {
	out := []pageStarterView{}
	for _, st := range pages.Starters() {
		out = append(out, starterView(st))
	}
	writeJSON(w, http.StatusOK, map[string]any{"starters": out})
}

type bundleReq struct {
	// Publish publishes the tools and the page once made, where the
	// person may and nothing holds it back.
	Publish bool `json:"publish"`
}

// installPageStarter is POST /v1/page-starters/{id}/install.
func (s *Server) installPageStarter(w http.ResponseWriter, r *http.Request) {
	st, ok := pages.StarterNamed(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "Taskiem has no such starter")
		return
	}
	var req bundleReq
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	made, err := s.installBundle(r, st.Bundle, req.Publish, map[string]any{"starter": st.ID})
	if err != nil {
		s.bundleFail(w, r, made, err)
		return
	}
	writeJSON(w, http.StatusCreated, made)
}

// bundleMade is what installing a bundle made.
type bundleMade struct {
	Page   string        `json:"page"`
	Title  string        `json:"title"`
	Tables []bundleTable `json:"tables"`
	Tools  []bundleTool  `json:"tools"`
	// Published: the tools and the page are in use. Otherwise Waiting
	// says why they are drafts, in plain words.
	Published bool            `json:"published"`
	Waiting   string          `json:"waiting,omitempty"`
	Problems  []pages.Problem `json:"problems"`
}

type bundleTable struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Asked   string `json:"asked"`
	Changed bool   `json:"changed"`
	ID      string `json:"id,omitempty"`
}

type bundleTool struct {
	Command  string    `json:"command"`
	Asked    string    `json:"asked"`
	Changed  bool      `json:"changed"`
	Name     string    `json:"name"`
	Workflow uuid.UUID `json:"workflow"`
	// State: draft, published, pending_approval or not_ready.
	State string `json:"state"`
}

// errBundle is a bundle refused before anything was made, in plain words.
type errBundle struct {
	status int
	msg    string
}

func (e errBundle) Error() string { return e.msg }

func (s *Server) bundleFail(w http.ResponseWriter, r *http.Request, made bundleMade, err error) {
	var eb errBundle
	if errors.As(err, &eb) {
		out := map[string]any{"error": eb.msg}
		if len(made.Tables) > 0 {
			out["made"] = made
		}
		writeJSON(w, eb.status, out)
		return
	}
	s.fail(w, r, err)
}

// freeTableName is name, or the next free one like it (tickets_2).
func freeTableName(tables []datatables.Table, name string, also []string) string {
	taken := func(n string) bool {
		return slices.Contains(also, n) || slices.ContainsFunc(tables, func(t datatables.Table) bool { return strings.EqualFold(t.Name, n) })
	}
	given := name
	for i := 2; taken(given) && i < 100; i++ {
		suffix := "_" + strconv.Itoa(i)
		given = name[:min(len(name), 63-len(suffix))] + suffix
	}
	return given
}

// installBundle makes a bundle's tables, tools and page as the request's
// person (see the top of this file).
func (s *Server) installBundle(r *http.Request, b pages.Bundle, publish bool, from map[string]any) (bundleMade, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	made := bundleMade{Tables: []bundleTable{}, Tools: []bundleTool{}, Problems: []pages.Problem{}}
	page, ps := pages.Parse(b.Page)
	if len(ps) > 0 {
		return made, errBundle{http.StatusBadRequest, "The page cannot be read: " + ps[0].Message}
	}
	if len(b.Tables) > 0 && !p.Can(PermTableEdit) {
		return made, errBundle{http.StatusForbidden, "Making its tables needs the right to change tables: ask an owner or an admin."}
	}
	if len(b.Tools) > 0 && !p.Can(PermWorkflowEdit) {
		return made, errBundle{http.StatusForbidden, "Making its tools needs the right to change workflows: ask an owner or an admin."}
	}
	var lib *templates.Library
	for _, t := range b.Tools {
		if t.Kind == "recipe" {
			lib = templates.Default()
			if tpl, ok := lib.Get(t.Recipe); !ok || !tpl.IsRecipe() {
				return made, errBundle{http.StatusBadRequest, "The tool /" + t.Command + " names a ready-made tool Taskiem does not have."}
			}
		}
	}

	// Free names: each table, then each tool's command.
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		tables, err := datatables.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		var given []string
		for i, t := range b.Tables {
			name := freeTableName(tables, t.Name, given)
			given = append(given, name)
			made.Tables = append(made.Tables, bundleTable{Name: name, Title: t.Title, Asked: t.Name, Changed: name != t.Name})
			if name != t.Name {
				page.RenameTable(t.Name, name)
				for j := range b.Tools {
					if b.Tools[j].Table == t.Name {
						b.Tools[j].Table = name
					}
					if b.Tools[j].Params != nil && b.Tools[j].Params["table"] == t.Name {
						b.Tools[j].Params["table"] = name
					}
				}
				b.Tables[i].Name = name
			}
		}
		var commands []string
		for i, t := range b.Tools {
			c, err := s.freeToolCommandTx(ctx, tx, p.TenantID, t.Command, uuid.Nil, commands)
			if errors.Is(err, errCommandsTaken) {
				return errBundle{http.StatusConflict, "Every word like /" + t.Command + " is taken: rename some of your tools first."}
			}
			if err != nil {
				return err
			}
			commands = append(commands, c)
			made.Tools = append(made.Tools, bundleTool{Command: c, Asked: t.Command, Changed: c != t.Command, Name: t.Name, State: "draft"})
			if c != t.Command {
				page.RenameTool(t.Command, c)
				b.Tools[i].Command = c
			}
		}
		return nil
	})
	if err != nil {
		return made, err
	}

	// The tables, as the Tables page makes them (outside a transaction:
	// making one may provision the business's data project).
	for i, t := range b.Tables {
		req := tableReq{Name: t.Name, Title: t.Title}
		for _, c := range t.Columns {
			req.Columns = append(req.Columns, columnReq{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required})
		}
		got, err := s.newDataTable(r, p, req)
		if err != nil {
			made.Tables = made.Tables[:i]
			return made, errBundle{http.StatusConflict, "The table " + t.Title + " could not be made: " + errorText(s, r, err)}
		}
		made.Tables[i].ID = got.ID.String()
	}

	// The tools as drafts, and the page as a draft, together.
	audit := func(tx pgx.Tx) func(action, target string, detail map[string]any) error {
		return func(action, target string, detail map[string]any) error {
			return auditTx(r, tx, action, target, detail)
		}
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		for i, t := range b.Tools {
			def, err := s.bundleToolDefinition(b, t, lib)
			if err != nil {
				return errBundle{http.StatusBadRequest, "The tool /" + t.Command + " cannot be made: " + err.Error() + "."}
			}
			doc, err := canonical(def)
			if err != nil {
				return err
			}
			wf, err := s.createWorkflowTx(ctx, tx, p, t.Name)
			if err != nil {
				return err
			}
			if err := insertVersionTx(ctx, tx, p.TenantID, wf, 1, doc, nil, p.Actor(), ""); err != nil {
				return err
			}
			if t.Kind == "recipe" {
				if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET template_id = $2 WHERE workflow_id = $1 AND version = 1`, wf, t.Recipe); err != nil {
					return err
				}
			}
			detail := map[string]any{"name": t.Name, "state": "draft", "command": t.Command}
			for k, v := range from {
				detail[k] = v
			}
			if err := audit(tx)("workflow.create", wf.String(), detail); err != nil {
				return err
			}
			made.Tools[i].Workflow = wf
		}
		if err := lockPageNames(ctx, tx, p.TenantID); err != nil {
			return err
		}
		if err := s.checkCount(ctx, tx, p.TenantID, "max_pages"); err != nil {
			return err
		}
		env, err := s.pageEnv(ctx, tx, uuid.Nil)
		if err != nil {
			return err
		}
		base := page.Name
		for i := 2; (env.Taken(page.Name) != "" || pages.ReservedName(page.Name) || slices.Contains(commandsOf(made.Tools), page.Name)) && i < 100; i++ {
			suffix := "-" + strconv.Itoa(i)
			page.Name = strings.TrimRight(base[:min(len(base), 24-len(suffix))], "-") + suffix
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pages (id, tenant_id, name, title, icon, draft, draft_by, draft_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $7)`, uuid.Must(uuid.NewV7()), p.TenantID, page.Name, page.Title, page.Icon, page.Marshal(), p.Actor()); err != nil {
			return err
		}
		detail := map[string]any{"title": page.Title, "tables": len(made.Tables), "tools": commandsOf(made.Tools)}
		for k, v := range from {
			detail[k] = v
		}
		made.Page, made.Title = page.Name, page.Title
		return audit(tx)("page.create", page.Name, detail)
	})
	if err != nil {
		return made, err
	}
	if !publish {
		made.Waiting = "Saved as drafts: publish the tools and the page when they are right."
		return made, nil
	}
	return s.publishBundle(r, made, audit)
}

func commandsOf(tools []bundleTool) []string {
	out := []string{}
	for _, t := range tools {
		out = append(out, t.Command)
	}
	return out
}

// bundleToolDefinition is a tool's workflow: an add or a change made over
// its table, or a recipe filled in and named as the bundle names it.
func (s *Server) bundleToolDefinition(b pages.Bundle, t pages.ToolSpec, lib *templates.Library) (json.RawMessage, error) {
	if t.Kind != "recipe" {
		tbl, _ := b.TableNamed(t.Table)
		return t.Definition(tbl)
	}
	tpl, _ := lib.Get(t.Recipe)
	params := map[string]any{}
	for k, v := range t.Params {
		params[k] = v
	}
	params[tpl.CommandParam().Name] = t.Command
	raw, err := tpl.Instantiate(params, t.Name)
	if err != nil {
		return nil, err
	}
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	def["name"] = t.Name
	if tool, ok := def["tool"].(map[string]any); ok {
		tool["name"], tool["command"] = t.Name, t.Command
		if t.About != "" {
			tool["about"] = t.About
		}
		if t.Icon != "" {
			tool["icon"] = t.Icon
		}
	}
	if len(t.Roles) > 0 {
		def["roles"] = t.Roles
	}
	return json.Marshal(def)
}

// publishBundle publishes what installBundle made, the tools first, as
// the person, where they may; it stops at the first that cannot be.
func (s *Server) publishBundle(r *http.Request, made bundleMade, audit func(pgx.Tx) func(string, string, map[string]any) error) (bundleMade, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	switch {
	case len(made.Tools) > 0 && !p.Can(PermWorkflowPublish):
		made.Waiting = "Saved as drafts: publishing the tools needs the right to publish workflows. Ask an owner or an admin to publish them, then the page."
		return made, nil
	case !p.Can(PermPagePublish):
		made.Waiting = "Saved as drafts: publishing the page needs the right to publish pages. Ask an owner or an admin."
		return made, nil
	}
	for i, t := range made.Tools {
		var state string
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			var err error
			state, err = s.publishNewTool(ctx, tx, p, t.Workflow, audit(tx))
			return err
		})
		if err != nil {
			return made, err
		}
		made.Tools[i].State = state
		switch state {
		case publishedNow:
		case publishWaiting:
			made.Waiting = "Saved: /" + t.Command + " waits for another person to approve publishing it (four-eyes). Publish the page once it is."
			return made, nil
		case publishInTheApp:
			made.Waiting = "Saved as drafts: this business publishes through Git, so publish the tools there, then the page."
			return made, nil
		default:
			made.Waiting = "Saved as drafts: /" + t.Command + " is not ready to publish yet. Open it to see what it needs."
			return made, nil
		}
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		refused, _, _, err := s.publishPageTx(ctx, tx, p, made.Page, audit(tx))
		if errors.Is(err, errForbidden) {
			made.Waiting = "The tools are in use. The page waits for another person to publish it (four-eyes)."
			return nil
		}
		if err != nil {
			return err
		}
		if len(refused) > 0 {
			made.Problems = refused
			made.Waiting = "The tools are in use. The page is a draft until this is put right: " + refused[0].Message
			return nil
		}
		made.Published = true
		return nil
	})
	return made, err
}

// publishNewTool publishes version 1 of a tool just made, as the Publish
// button would for p: a business publishing through Git publishes there;
// readiness; what only some may give a workflow; four-eyes asks another
// person. It gives the state it is left in.
func (s *Server) publishNewTool(ctx context.Context, tx pgx.Tx, p *Principal, wf uuid.UUID, audit func(string, string, map[string]any) error) (string, error) {
	var gitLed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM git_connections WHERE mode = 'platform_led')`).Scan(&gitLed); err != nil {
		return "", err
	}
	if gitLed {
		return publishInTheApp, nil
	}
	if err := s.checkReady(ctx, tx, p.TenantID, wf, 1); err != nil {
		var nr *errNotReady
		if errors.As(err, &nr) {
			return publishNotReady, nil
		}
		return "", err
	}
	if err := publishMayRead(ctx, tx, p, wf, 1); err != nil {
		return "", err
	}
	g, err := governanceTx(ctx, tx)
	if err != nil {
		return "", err
	}
	wait, err := publishNeedsApproval(ctx, tx, g, nil)
	if err != nil {
		return "", err
	}
	if wait {
		if p.Human() == uuid.Nil {
			return publishWaiting, nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, 1, $3)
			ON CONFLICT (workflow_id, version) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL`,
			p.TenantID, wf, p.Human()); err != nil {
			return "", err
		}
		return publishWaiting, audit("publish_request.create", fmt.Sprintf("%s/1", wf), nil)
	}
	by := p.id()
	_, digest, _, err := s.publishTx(ctx, tx, p.TenantID, wf, 1, &by, "")
	if errors.Is(err, errInvalid) {
		return publishNotReady, nil
	}
	if err != nil {
		return "", err
	}
	return publishedNow, audit("workflow.publish", fmt.Sprintf("%s/1", wf), map[string]any{"digest": digest})
}

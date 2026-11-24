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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/pages"
)

// Pages (decision 0072, docs/spec/pages.md G0 and G1): screens a business
// makes for itself from parts Taskiem draws, each a page/v1 document kept
// as a draft and published versions. The web and the app both draw a page
// from view, which works out every part's figures and rows here, from the
// business's data tables, masked for the viewer: neither reads a table
// for a page itself, so what a page shows is decided in one place (§8).
//
// Who may open a page is its published roles (owners always; none named,
// everyone in the business), and page.edit holders, who make and preview
// them. A page shows what its maker chose to those allowed to open it,
// reading with the business's connection as a made tool's answer does;
// a column marked personal is masked unless the viewer also holds
// table.read. Making or changing a page needs table.read too: its maker
// chooses what of the tables it shows.

// pagesPurpose is the recorded purpose of the connection reads a page's
// view makes (GET /v1/secrets/reads).
const pagesPurpose = "pages.view"

// maxPinned is how many pages may be pinned to the navigation (§9).
const maxPinned = 5

// pageRoutes mounts the pages API.
func (s *Server) pageRoutes(r chi.Router) {
	r.Get("/pages", s.listPages)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/pages", s.createPage)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/pages/validate", s.validatePage)
	r.Get("/pages/{name}", s.getPage)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Put("/pages/{name}/draft", s.putPageDraft)
	r.With(s.need(PermPagePublish), s.need(PermTableRead)).Post("/pages/{name}/publish", s.publishPage)
	r.With(s.need(PermPageEdit)).Put("/pages/{name}/pin", s.pinPage)
	r.With(s.need(PermPageEdit)).Delete("/pages/{name}", s.deletePage)
	r.Get("/pages/{name}/view", s.viewPage)
	r.Get("/pages/{name}/blocks/{block}", s.pageBlock)
	r.Get("/pages/{name}/live", s.pageLive)                              // pages_live.go
	r.Post("/pages/{name}/act", s.actPage)                               // pages_actions.go
	r.Post("/pages/{name}/refresh", s.refreshPage)                       // pages_actions.go
	r.With(s.need(PermPageEdit)).Get("/page-records", s.listPageRecords) // pages_records.go
	r.With(s.need(PermPageEdit)).Get("/page-choices", s.listPageChoices) // the editor's tools and workflows (pages_choices.go)
	s.pageVersionRoutes(r)                                               // versions, export and import (pages_versions.go)
}

// pageRow is a page as stored.
type pageRow struct {
	ID          uuid.UUID
	Name        string
	Title       string
	Icon        string
	Roles       []string
	Pinned      bool
	PinOrder    int
	Version     *int
	PublishedAt *time.Time
	Published   []byte // the published version's document
	Draft       []byte
	DraftAt     *time.Time
	UpdatedAt   time.Time
}

const pageCols = `p.id, p.name, p.title, p.icon, p.roles, p.pinned, p.pin_order, p.published_version, p.published_at,
	v.document, p.draft, p.draft_at, p.updated_at`

const pageFrom = ` FROM pages p LEFT JOIN page_versions v ON v.page_id = p.id AND v.version = p.published_version`

func scanPage(row pgx.Row) (pageRow, error) {
	var p pageRow
	err := row.Scan(&p.ID, &p.Name, &p.Title, &p.Icon, &p.Roles, &p.Pinned, &p.PinOrder, &p.Version, &p.PublishedAt,
		&p.Published, &p.Draft, &p.DraftAt, &p.UpdatedAt)
	return p, err
}

func loadPage(ctx context.Context, tx pgx.Tx, name string) (pageRow, error) {
	p, err := scanPage(tx.QueryRow(ctx, `SELECT `+pageCols+pageFrom+` WHERE p.name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errNoPage
	}
	return p, err
}

var errNoPage = fmt.Errorf("%w: there is no such page", pgx.ErrNoRows)

// mayOpenPage says whether p may open a page: its editors always; others
// once it is published and its roles let them (owners always may; none
// named, everyone in the business).
func mayOpenPage(p *Principal, pg pageRow) bool {
	if p.Can(PermPageEdit) {
		return true
	}
	return pg.Version != nil && mayStart(p, pg.Roles)
}

// viewerPhone is the person's own phone number (their account's, as they
// sign in by it), for Mine on a phone column; "" for an API key or a
// person with none (pages §6).
func (s *Server) viewerPhone(ctx context.Context, p *Principal) string {
	if p.UserID == uuid.Nil {
		return ""
	}
	n, _ := s.phoneOf(ctx, p.UserID).(string)
	return n
}

// lockPageNames holds the business's page names and count for the rest of
// the transaction: two pages made at once (a file read twice, Show as a
// page pressed twice) cannot take the same free name, nor both pass
// max_pages.
func lockPageNames(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('page-names:' || $1::text, 0))`, tenant)
	return err
}

// pageSummary is a page in the list.
func pageSummary(pg pageRow) map[string]any {
	about, place := "", ""
	if pg.Published != nil {
		var head struct {
			About string       `json:"about"`
			Place *pages.Place `json:"place"`
		}
		_ = json.Unmarshal(pg.Published, &head)
		about = head.About
		if head.Place != nil {
			place = head.Place.Area
		}
	}
	out := map[string]any{"name": pg.Name, "title": pg.Title, "icon": pg.Icon, "about": about, "roles": nonNil(pg.Roles),
		"pinned": pg.Pinned, "published": pg.Version != nil, "has_draft": pg.Draft != nil, "version": pg.Version,
		"updated_at": pg.UpdatedAt.UTC().Format(time.RFC3339)}
	if place != "" {
		// A tab under this area, after Taskiem's own (pages §9).
		out["place"] = place
	}
	if pg.PublishedAt != nil {
		out["published_at"] = pg.PublishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// listPages is GET /v1/pages: the pages the caller may open, pinned
// first in their order, then by name.
func (s *Server) listPages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var list []map[string]any
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+pageCols+pageFrom+` ORDER BY p.pinned DESC, p.pin_order, lower(p.title), p.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			pg, err := scanPage(rows)
			if err != nil {
				return err
			}
			if mayOpenPage(p, pg) {
				list = append(list, pageSummary(pg))
			}
		}
		return rows.Err()
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	limit := 0
	if lim, err := s.Store.LimitsFor(r.Context(), p.TenantID); err == nil {
		limit = lim.MaxPages
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": nonNil(list), "can_edit": p.Can(PermPageEdit), "can_publish": p.Can(PermPagePublish),
		"max_pinned": maxPinned, "max_pages": limit})
}

// pageProblems answers a document refused, its problems in plain words.
func pageProblems(w http.ResponseWriter, status int, msg string, ps []pages.Problem) {
	writeJSON(w, status, map[string]any{"error": msg, "code": "page_problems", "problems": ps})
}

// pageEnv is what a page is checked against: the business's tables, and a
// name another page or a tool's command has.
func (s *Server) pageEnv(ctx context.Context, tx pgx.Tx, self uuid.UUID) (pages.Env, error) {
	tables, err := datatables.ListTables(ctx, tx)
	if err != nil {
		return pages.Env{}, err
	}
	areaOn, err := pageAreas(ctx, tx)
	if err != nil {
		return pages.Env{}, err
	}
	env := pages.Env{Tables: tables, AreaOn: areaOn, Taken: func(name string) string {
		var other bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE name = $1 AND id <> $2)`, name, self).Scan(&other)
		if other {
			return "Another page is called " + name + ": pick another short name."
		}
		var tool bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_tools WHERE command = $1)`, name).Scan(&tool)
		if tool {
			return "A tool's word is " + name + ": pick another short name for the page, so /" + name + " means one thing."
		}
		return ""
	}}
	pageEnvTools(ctx, s, tx, &env) // pages_actions.go
	return env, nil
}

// pageDetail is a page as its editor reads it: the summary, the published
// document, and for editors the draft with its problems and what it
// shows.
func pageDetail(p *Principal, pg pageRow, env *pages.Env) map[string]any {
	out := pageSummary(pg)
	out["document"] = rawOrNil(pg.Published)
	if !p.Can(PermPageEdit) {
		return out
	}
	out["draft"] = rawOrNil(pg.Draft)
	if pg.DraftAt != nil {
		out["draft_at"] = pg.DraftAt.UTC().Format(time.RFC3339)
	}
	doc := pg.Draft
	if doc == nil {
		doc = pg.Published
	}
	ps := []pages.Problem{}
	shows := []string{}
	if page, parsed := pages.Parse(doc); len(parsed) > 0 {
		ps = parsed
	} else if env != nil {
		ps = append(ps, pages.Check(page, *env)...)
		shows = nonNil(pages.Shows(page, env.Tables))
	}
	out["problems"], out["shows"] = ps, shows
	return out
}

func rawOrNil(b []byte) any {
	if b == nil {
		return nil
	}
	return json.RawMessage(b)
}

// getPage is GET /v1/pages/{name}.
func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out map[string]any
	if err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		if !mayOpenPage(p, pg) {
			return errNoPage
		}
		var env *pages.Env
		if p.Can(PermPageEdit) {
			e, err := s.pageEnv(r.Context(), tx, pg.ID)
			if err != nil {
				return err
			}
			env = &e
		}
		out = pageDetail(p, pg, env)
		if p.Can(PermPageEdit) {
			// The page's settings: about how many runs its tools start a
			// day (the draft's tools, as it is being changed).
			doc := pg.Draft
			if doc == nil {
				doc = pg.Published
			}
			page, _ := pages.Parse(doc)
			if out["runs"], err = s.pageRunsEstimate(r.Context(), tx, p.TenantID, pg.ID, page); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type pageDocReq struct {
	Document  json.RawMessage `json:"document"`
	FromTable *uuid.UUID      `json:"from_table"`
	// FromTool: Pin as a page, from a tool's answer (G2): the tool's
	// command and the values it was given.
	FromTool *struct {
		Command string         `json:"command"`
		Inputs  map[string]any `json:"inputs"`
	} `json:"from_tool"`
}

// validatePage is POST /v1/pages/validate: a document's problems, held
// against the business's tables, nothing saved.
func (s *Server) validatePage(w http.ResponseWriter, r *http.Request) {
	var req pageDocReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ps := []pages.Problem{}
	shows := []string{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		page, parsed := pages.Parse(req.Document)
		if len(parsed) > 0 {
			ps = parsed
			return nil
		}
		var self uuid.UUID
		_ = tx.QueryRow(r.Context(), `SELECT id FROM pages WHERE name = $1`, page.Name).Scan(&self)
		env, err := s.pageEnv(r.Context(), tx, self)
		if err != nil {
			return err
		}
		ps = append(ps, pages.Check(page, env)...)
		shows = nonNil(pages.Shows(page, env.Tables))
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": ps, "shows": shows})
}

// createPage is POST /v1/pages: a new page as a draft, from a document or
// from a table (Show as a page, §10). A page made from a table whose name
// is taken gets the next free one (tickets-2).
func (s *Server) createPage(w http.ResponseWriter, r *http.Request) {
	var req pageDocReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	given := 0
	for _, x := range []bool{req.FromTable != nil, len(req.Document) > 0, req.FromTool != nil} {
		if x {
			given++
		}
	}
	if given != 1 {
		writeErr(w, http.StatusBadRequest, "give a page's document, from_table (the table to make it from) or from_tool (a tool's answer to pin): one of them")
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	var refused []pages.Problem
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := lockPageNames(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if err := s.checkCount(r.Context(), tx, p.TenantID, "max_pages"); err != nil {
			return err
		}
		env, err := s.pageEnv(r.Context(), tx, uuid.Nil)
		if err != nil {
			return err
		}
		var page *pages.Page
		if req.FromTable != nil || req.FromTool != nil {
			if req.FromTool != nil {
				t, info, ok := pageToolInfo(r.Context(), s, tx, req.FromTool.Command)
				if !ok {
					return fmt.Errorf("%w: no tool /%s", pgx.ErrNoRows, req.FromTool.Command)
				}
				if !mayStart(p, t.Roles) {
					return errNotYours(t.Name, t.Roles)
				}
				if page, err = pages.FromTool(t.Command, info, req.FromTool.Inputs); err != nil {
					return fmt.Errorf("%w: %s", errBadRequest, err.Error())
				}
			} else {
				t, err := datatables.GetTable(r.Context(), tx, *req.FromTable)
				if err != nil {
					return err
				}
				page = pages.FromTable(t)
			}
			base := page.Name
			for i := 2; env.Taken(page.Name) != "" && i < 100; i++ {
				suffix := "-" + strconv.Itoa(i)
				page.Name = strings.TrimRight(base[:min(len(base), 24-len(suffix))], "-") + suffix
			}
		} else {
			var ps []pages.Problem
			if page, ps = pages.Parse(req.Document); len(ps) > 0 {
				refused = ps
				return nil
			}
			if why := env.Taken(page.Name); why != "" || pages.ReservedName(page.Name) {
				refused = pages.Check(page, env)
				return nil
			}
		}
		doc := page.Marshal()
		id := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(r.Context(), `INSERT INTO pages (id, tenant_id, name, title, icon, draft, draft_by, draft_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $7)`, id, p.TenantID, page.Name, page.Title, page.Icon, doc, p.Actor()); err != nil {
			return err
		}
		detail := map[string]any{"title": page.Title}
		if req.FromTable != nil {
			detail["from_table"] = *req.FromTable
		}
		if req.FromTool != nil {
			detail["from_tool"] = req.FromTool.Command
		}
		if err := auditTx(r, tx, "page.create", page.Name, detail); err != nil {
			return err
		}
		pg, err := loadPage(r.Context(), tx, page.Name)
		if err != nil {
			return err
		}
		if env, err = s.pageEnv(r.Context(), tx, pg.ID); err != nil {
			return err
		}
		out = pageDetail(p, pg, &env)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if refused != nil {
		pageProblems(w, http.StatusBadRequest, "the page cannot be saved as it is", refused)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// putPageDraft is PUT /v1/pages/{name}/draft: the page's draft replaced.
// A draft may still have problems (a table not made yet): they are said,
// and publishing refuses it until they are put right. Its short name does
// not change.
func (s *Server) putPageDraft(w http.ResponseWriter, r *http.Request) {
	var req pageDocReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	var refused []pages.Problem
	err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		page, ps := pages.Parse(req.Document)
		if len(ps) > 0 {
			refused = ps
			return nil
		}
		if page.Name != pg.Name {
			refused = []pages.Problem{{Path: "/name", Message: "A page's short name stays as it was made (" + pg.Name + ")."}}
			return nil
		}
		if _, err := tx.Exec(r.Context(), `UPDATE pages SET draft = $2, draft_by = $3, draft_at = now(), updated_at = now() WHERE id = $1`,
			pg.ID, page.Marshal(), p.Actor()); err != nil {
			return err
		}
		if err := auditTx(r, tx, "page.draft", pg.Name, nil); err != nil {
			return err
		}
		env, err := s.pageEnv(r.Context(), tx, pg.ID)
		if err != nil {
			return err
		}
		if pg, err = loadPage(r.Context(), tx, pg.Name); err != nil {
			return err
		}
		out = pageDetail(p, pg, &env)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if refused != nil {
		pageProblems(w, http.StatusBadRequest, "the page cannot be saved as it is", refused)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// publishPage is POST /v1/pages/{name}/publish: the draft becomes the
// page people open, once it has no problem left. Who may open it and what
// it shows are recorded (§8).
func (s *Server) publishPage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out map[string]any
	var refused []pages.Problem
	var nothing bool
	err := s.tx(r, func(tx pgx.Tx) error {
		// The draft and who saved it are read under the row's lock, so a
		// draft saved by someone else between the two reads cannot be
		// published as another person's (four-eyes).
		if _, err := tx.Exec(r.Context(), `SELECT 1 FROM pages WHERE name = $1 FOR UPDATE`, chi.URLParam(r, "name")); err != nil {
			return err
		}
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		if pg.Draft == nil {
			nothing = true
			return nil
		}
		page, ps := pages.Parse(pg.Draft)
		env, err := s.pageEnv(r.Context(), tx, pg.ID)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			ps = pages.Check(page, env)
		}
		if len(ps) > 0 {
			refused = ps
			return nil
		}
		if err := pagePublishFourEyes(r.Context(), tx, p, pg.ID); err != nil {
			return err
		}
		version := 1
		if pg.Version != nil {
			version = *pg.Version + 1
		}
		doc := page.Marshal()
		if _, err := tx.Exec(r.Context(), `INSERT INTO page_versions (page_id, tenant_id, version, document, published_by) VALUES ($1, $2, $3, $4, $5)`,
			pg.ID, p.TenantID, version, doc, p.Actor()); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE pages SET published_version = $2, published_at = now(), title = $3, icon = $4, roles = $5,
			draft = NULL, draft_by = NULL, draft_at = NULL, updated_at = now() WHERE id = $1`, pg.ID, version, page.Title, page.Icon, nonNil(page.Roles)); err != nil {
			return err
		}
		shows := pages.Shows(page, env.Tables)
		if err := auditTx(r, tx, "page.publish", pg.Name, map[string]any{"version": version, "roles": nonNil(page.Roles), "shows": shows}); err != nil {
			return err
		}
		if pg, err = loadPage(r.Context(), tx, pg.Name); err != nil {
			return err
		}
		out = pageDetail(p, pg, &env)
		return nil
	})
	switch {
	case err != nil:
		s.fail(w, r, err)
	case nothing:
		writeErr(w, http.StatusConflict, "this page has no changes to publish")
	case refused != nil:
		pageProblems(w, http.StatusBadRequest, "the page cannot be published until these are put right", refused)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// pagePublishFourEyes holds publishing a page to four-eyes where the
// business has it (as a workflow's publish is: publishNeedsApproval): a
// page goes live to everyone its roles let open it, so another person
// with page.publish publishes it, never the person who last saved its
// draft (draft_by, a person or their API key). A caller that is no person
// (a workflow's run, an agent) does not publish under four-eyes.
func pagePublishFourEyes(ctx context.Context, tx pgx.Tx, p *Principal, page uuid.UUID) error {
	g, err := governanceTx(ctx, tx)
	if err != nil {
		return err
	}
	wait, err := publishNeedsApproval(ctx, tx, g, nil)
	if err != nil || !wait {
		return err
	}
	me := p.Human()
	if me == uuid.Nil || p.Run != uuid.Nil || p.Agent != nil {
		return fmt.Errorf("%w: with four-eyes publishing, a person publishes a page", errForbidden)
	}
	var by *string
	if err := tx.QueryRow(ctx, `SELECT draft_by FROM pages WHERE id = $1`, page).Scan(&by); err != nil {
		return err
	}
	if by == nil {
		return nil
	}
	saver, _ := uuid.Parse(*by)
	if key, ok := strings.CutPrefix(*by, "key:"); ok {
		if id, err := uuid.Parse(key); err == nil {
			var owner *uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT owner_id FROM api_keys WHERE id = $1`, id).Scan(&owner); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if owner != nil {
				saver = *owner
			}
		}
	}
	if saver == me {
		return fmt.Errorf("%w: with four-eyes publishing, another person with page.publish publishes this page: you saved its last changes", errForbidden)
	}
	return nil
}

// pinPage is PUT /v1/pages/{name}/pin: a published page shown in the
// navigation under Your pages (§9), five at most. Pins are counted under
// a per-business lock, so two at once cannot make a sixth; pin_order is
// unique among pinned pages (migration 00539). Only a change is audited.
func (s *Server) pinPage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pinned bool `json:"pinned"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('page-pins:' || $1::text, 0))`, p.TenantID); err != nil {
			return err
		}
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		changed := req.Pinned != pg.Pinned
		if req.Pinned && !pg.Pinned {
			if pg.Version == nil {
				return fmt.Errorf("%w: publish the page before pinning it", errConflict)
			}
			var n, last int
			if err := tx.QueryRow(r.Context(), `SELECT count(*), coalesce(max(pin_order), 0) FROM pages WHERE pinned`).Scan(&n, &last); err != nil {
				return err
			}
			if n >= maxPinned {
				return fmt.Errorf("%w: %d pages are pinned already: unpin one first", errConflict, maxPinned)
			}
			_, err = tx.Exec(r.Context(), `UPDATE pages SET pinned = true, pin_order = $2, updated_at = now() WHERE id = $1`, pg.ID, last+1)
			if err != nil {
				return err
			}
		} else if !req.Pinned && pg.Pinned {
			if _, err := tx.Exec(r.Context(), `UPDATE pages SET pinned = false, pin_order = 0, updated_at = now() WHERE id = $1`, pg.ID); err != nil {
				return err
			}
		}
		if changed {
			if err := auditTx(r, tx, "page.pin", pg.Name, map[string]any{"pinned": req.Pinned}); err != nil {
				return err
			}
		}
		if pg, err = loadPage(r.Context(), tx, pg.Name); err != nil {
			return err
		}
		out = pageSummary(pg)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deletePage is DELETE /v1/pages/{name}: the page and its versions. A
// page that was ever published is taken away from those who open it, so
// that needs page.publish; page.edit alone deletes a draft never
// published.
func (s *Server) deletePage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		if pg.Version != nil && !p.Can(PermPagePublish) {
			return fmt.Errorf("%w: this page is published: deleting it needs the right to publish pages", errForbidden)
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM pages WHERE id = $1`, pg.ID); err != nil {
			return err
		}
		return auditTx(r, tx, "page.delete", pg.Name, map[string]any{"version": pg.Version})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- drawing a page ---

// pageCtx is a page opened for drawing: its document, the tables it reads,
// the viewer's filter values, and the connection to read with.
type pageCtx struct {
	row     pageRow
	page    *pages.Page
	draft   bool
	tables  []datatables.Table
	broken  map[int]string // a part that cannot be drawn: why, in plain words
	values  pages.Values
	editor  bool
	readers map[string]tableRows
	readErr error
	// off: a part over Taskiem's records in an area the business has off,
	// drawn as unavailable (pages_records.go).
	off map[int]string
	// tools: the tools the page names, read once (pages_actions.go); run:
	// the run the page's address names, for a status part.
	tools map[string]*toolRow
	run   uuid.UUID
}

// openPage loads a page for drawing for the caller: the draft for an
// editor who asks for it, else the published version; its problems
// against today's tables mark the parts that cannot be drawn.
func (s *Server) openPage(w http.ResponseWriter, r *http.Request) (*pageCtx, bool) {
	p := principalFrom(r.Context())
	pc := &pageCtx{editor: p.Can(PermPageEdit), broken: map[int]string{}, off: map[int]string{}}
	var viewer pages.Viewer
	err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		if !mayOpenPage(p, pg) {
			return errNoPage
		}
		pc.row = pg
		doc := pg.Published
		if r.URL.Query().Get("draft") == "1" && pc.editor && pg.Draft != nil {
			doc, pc.draft = pg.Draft, true
		}
		if doc == nil {
			return fmt.Errorf("%w: this page is not published yet", errConflict)
		}
		page, ps := pages.Parse(doc)
		if page == nil {
			return fmt.Errorf("%w: this page cannot be read", errConflict)
		}
		pc.page = page
		if pc.tables, err = datatables.ListTables(r.Context(), tx); err != nil {
			return err
		}
		areaOn, err := pageAreas(r.Context(), tx)
		if err != nil {
			return err
		}
		for i, b := range page.Blocks {
			if v, ok := pages.RecordViewNamed(recordsOf(b)); ok && !areaOn(string(v.Area)) {
				pc.off[i] = v.Unavailable()
			}
		}
		// Checked again at each view: a tool that came to ask before it
		// runs no longer feeds a part, a signal no longer declared for
		// pages is no button (G2).
		env := pages.Env{Tables: pc.tables, AreaOn: areaOn}
		pageEnvTools(r.Context(), s, tx, &env)
		ps = append(ps, pages.Check(page, env)...)
		for _, pr := range ps {
			rest, ok := strings.CutPrefix(pr.Path, "/blocks/")
			if !ok {
				continue
			}
			i, err := strconv.Atoi(strings.SplitN(rest, "/", 2)[0])
			if err == nil {
				_, seen := pc.broken[i]
				if _, off := pc.off[i]; !seen && !off {
					pc.broken[i] = pr.Message
				}
			}
		}
		if p.UserID != uuid.Nil {
			var email *string
			_ = tx.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
			if email != nil {
				viewer.Email = *email
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	viewer.Phone = s.viewerPhone(r.Context(), p)
	viewer.Personal = p.Can(PermTableRead)
	viewer.Edit = p.Can(PermTableEdit)
	if run := r.URL.Query().Get("run"); run != "" {
		pc.run, _ = uuid.Parse(run) // a status part's run, of its own workflow only
	}
	set := map[string]string{}
	if raw := r.URL.Query().Get("filters"); raw != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			writeErr(w, http.StatusBadRequest, "filters is a JSON object of each filter's value")
			return nil, false
		}
		for k, v := range m {
			switch x := v.(type) {
			case string:
				set[k] = x
			case bool:
				if x {
					set[k] = "on"
				}
			case nil:
			default:
				set[k] = fmt.Sprint(x)
			}
		}
	}
	vals, ps := pc.page.FilterValues(set, viewer, time.Now())
	if len(ps) > 0 {
		pageProblems(w, http.StatusBadRequest, ps[0].Message, ps)
		return nil, false
	}
	pc.values = vals
	return pc, true
}

// reader is the connection to read a table with, made once for the page.
func (s *Server) pageReader(ctx context.Context, pc *pageCtx, t datatables.Table) (tableRows, error) {
	if pc.readErr != nil {
		return tableRows{}, pc.readErr
	}
	if pc.readers == nil {
		pc.readers = map[string]tableRows{}
		tr, err := s.tableRowsFor(ctx, principalFrom(ctx).TenantID, t, pagesPurpose)
		if err != nil {
			pc.readErr = err
			return tableRows{}, err
		}
		pc.readers[""] = tr
	}
	tr := pc.readers[""]
	tr.table = t
	return tr, nil
}

func (pc *pageCtx) table(b pages.Block) datatables.Table {
	t, _ := pages.Lookup(pc.tables, b.Source.From())
	return t
}

// viewPage is GET /v1/pages/{name}/view: the page as drawn, each part
// with its figures and rows worked out and masked for the viewer.
func (s *Server) viewPage(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.openPage(w, r)
	if !ok {
		return
	}
	if !pc.draft {
		s.countPageView(r.Context(), principalFrom(r.Context()), pc.row.ID) // pages_runs.go
	}
	blocks := []map[string]any{}
	for i, b := range pc.page.Blocks {
		if why, off := pc.off[i]; off {
			blocks = append(blocks, map[string]any{"id": b.ID, "type": b.Type, "unavailable": why})
			continue
		}
		if why, broken := pc.broken[i]; broken {
			if pc.editor {
				blocks = append(blocks, map[string]any{"id": b.ID, "type": b.Type, "broken": "This part needs fixing: " + why})
			}
			continue // left out for everyone else (§11)
		}
		blocks = append(blocks, s.drawBlock(r.Context(), pc, b, pages.Cursor{}, nil))
	}
	filters := []map[string]any{}
	for _, f := range pc.page.Filters {
		if pc.page.HiddenFilter(f, pc.tables, pc.values.Viewer.Personal) {
			continue
		}
		label := f.Label
		if label == "" {
			label = map[string]string{"search": "Search", "choice": "Show", "dates": "When", "mine": "Mine"}[f.Type]
		}
		fm := map[string]any{"id": f.ID, "type": f.Type, "label": label, "value": pc.values.Set[f.ID]}
		if f.Type == "choice" {
			opts := []map[string]any{}
			for _, o := range f.Options {
				l := o.Label
				if l == "" {
					l = pages.GroupLabel(o.Value)
				}
				opts = append(opts, map[string]any{"value": o.Value, "label": l})
			}
			fm["options"] = opts
		}
		filters = append(filters, fm)
	}
	sum := pageSummary(pc.row)
	sum["title"], sum["icon"], sum["about"] = pc.page.Title, pc.page.Icon, pc.page.About
	writeJSON(w, http.StatusOK, map[string]any{"page": sum, "draft": pc.draft, "filters": filters, "blocks": blocks,
		"can_edit": pc.editor, "drawn_at": time.Now().UTC().Format(time.RFC3339)})
}

// pageBlock is GET /v1/pages/{name}/blocks/{block}: more rows of a list
// or a table (?cursor), or its rows in another order (?order=column,
// &desc=1, a press on a table's column).
func (s *Server) pageBlock(w http.ResponseWriter, r *http.Request) {
	pc, ok := s.openPage(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "block")
	i := slices.IndexFunc(pc.page.Blocks, func(b pages.Block) bool { return b.ID == id })
	if i < 0 {
		writeErr(w, http.StatusNotFound, "this page has no such part")
		return
	}
	b := pc.page.Blocks[i]
	if b.Type != "list" && b.Type != "table" {
		writeErr(w, http.StatusBadRequest, "only a list or a table has more rows")
		return
	}
	if why, off := pc.off[i]; off {
		writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "type": b.Type, "unavailable": why})
		return
	}
	if why, broken := pc.broken[i]; broken {
		if !pc.editor {
			writeErr(w, http.StatusNotFound, "this page has no such part")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "type": b.Type, "broken": "This part needs fixing: " + why})
		return
	}
	var order *pages.Order
	if col := r.URL.Query().Get("order"); col != "" {
		order = &pages.Order{Column: col, Desc: r.URL.Query().Get("desc") == "1"}
		if b.Type == "table" && !slices.ContainsFunc(b.Columns, func(c pages.TableColumn) bool { return c.Column == col }) {
			writeErr(w, http.StatusBadRequest, "a table is put in order by one of the columns it shows")
			return
		}
		if b.Type == "list" {
			writeErr(w, http.StatusBadRequest, "a list keeps its own order")
			return
		}
		if c := columnNamed(pc.table(b), col); c.Type == "" || c.Type == "json" || !recordsOrderable(b, col) {
			writeErr(w, http.StatusBadRequest, "the rows cannot be put in this column's order")
			return
		}
		if columnNamed(pc.table(b), col).Personal && (!pc.values.Viewer.Personal || b.Source.Records != "") {
			writeErr(w, http.StatusBadRequest, "this column is personal: only someone who may read the table can put the rows in order by it")
			return
		}
	}
	var cur pages.Cursor
	var err error
	if b.Source.Records != "" {
		cur, err = pages.ReadRecordCursor(r.URL.Query().Get("cursor"), order)
	} else {
		ranked := pages.RankedByPersonal(b, pc.table(b), order, pc.values.Viewer.Personal)
		cur, err = pages.ReadCursor(r.URL.Query().Get("cursor"), order, ranked)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.drawBlock(r.Context(), pc, b, cur, order))
}

// drawBlock works out one part for the viewer. A part that cannot be read
// just now says so in plain words; the rest of the page still draws.
//
// More's cursor is Taskiem's own (pages.Cursor), taken back only with the
// order it was given for. Rows ranked by a personal column the viewer may
// not read (the part's own order names one) go on by a skip of the rows
// shown, never by the data API's cursor, which may carry the value a row
// was ranked by; such a part shows its first 1,000 rows.
func (s *Server) drawPart(ctx context.Context, pc *pageCtx, b pages.Block, cursor pages.Cursor, order *pages.Order) map[string]any {
	out := map[string]any{"id": b.ID, "type": b.Type}
	if b.Type == "heading" {
		out["title"], out["text"] = b.Title, b.Text
		return out
	}
	t := pc.table(b)
	read, err := pc.page.ReadFor(b, t, pc.values, order)
	if err != nil {
		out["broken"] = "This part needs fixing."
		return out
	}
	if b.Source.Records != "" {
		return s.drawRecords(ctx, pc, b, t, read, cursor, order, out)
	}
	tr, err := s.pageReader(ctx, pc, t)
	if err != nil {
		s.Logger.Warn("pages: the data tables connection", "err", err)
		out["error"] = "Taskiem could not read this part just now. Pull to try again."
		return out
	}
	readRows := func(in map[string]any) (map[string]any, error) {
		in["table"] = t.Name
		return tr.call(ctx, s, "read_rows", in, "")
	}
	failed := func(err error) map[string]any {
		s.Logger.Warn("pages: reading a part", "page", pc.page.Name, "part", b.ID, "err", err)
		out["error"] = "Taskiem could not read this part just now. Pull to try again."
		return out
	}
	switch b.Type {
	case "figure":
		out["label"], out["tone"], out["lead"], out["naira"] = b.Label, nonEmpty(b.Tone, "plain"), b.Lead, b.Naira
		figure := func(extra []any) (float64, bool, error) {
			if read.None {
				return 0, false, nil
			}
			filters := append(slices.Clone(read.Filters), extra...)
			if b.Value == "count" {
				res, err := readRows(map[string]any{"filters": filters, "limit": 1, "select": "id", "count": "exact"})
				if err != nil {
					return 0, false, err
				}
				return float64(countOf(res)), false, nil
			}
			var sum float64
			cur, seen := "", 0
			for {
				in := map[string]any{"filters": filters, "limit": 1000, "select": "id," + b.Of, "order": []any{map[string]any{"column": "id", "desc": true}}}
				if cur != "" {
					in["cursor"] = cur
				}
				res, err := readRows(in)
				if err != nil {
					return 0, false, err
				}
				for _, row := range rowsOf(res) {
					m, _ := row.(map[string]any)
					if f, ok := numberOf(m[b.Of]); ok {
						sum += f
					}
					seen++
				}
				cur, _ = res["next_cursor"].(string)
				if cur == "" {
					return sum, false, nil
				}
				if seen >= datatables.QuerySumRows {
					return sum, true, nil
				}
			}
		}
		var extra, before []any
		if ch := b.Change; ch != nil {
			col := columnNamed(t, ch.Column)
			from, to, prev := pages.Period(ch.Period, pc.values.Now)
			extra, before = pages.Between(col, from, to), pages.Between(col, prev, from)
		}
		v, over, err := figure(extra)
		if err != nil {
			return failed(err)
		}
		out["value"], out["shown"] = v, pages.FigureShown(b, v)
		if over {
			out["over"] = "Added up over the first 5,000 rows."
		}
		if b.Change != nil {
			pv, _, err := figure(before)
			if err != nil {
				return failed(err)
			}
			out["change"] = map[string]any{"before": pv, "words": pages.ChangeWords(b.Change.Period, v, pv),
				"up": v > pv, "down": v < pv}
		}
		return out
	}
	// A list or a table.
	partHead(pc, b, t, order, out)
	shown := []pages.Shown{}
	var more any
	count := int64(0)
	if !read.None {
		ranked := pages.RankedByPersonal(b, t, order, pc.values.Viewer.Personal)
		in := map[string]any{"filters": read.Filters, "order": read.Order, "limit": pages.RowsShown, "count": "exact"}
		if ranked {
			in["limit"] = cursor.Skip + pages.RowsShown
		} else if cursor.After != "" {
			in["cursor"] = cursor.After
		}
		res, err := readRows(in)
		if err != nil {
			return failed(err)
		}
		var rows []map[string]any
		for _, row := range rowsOf(res) {
			if m, ok := row.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
		if ranked {
			rows = rows[min(cursor.Skip, len(rows)):]
		}
		shown = pages.Shape(b, t, rows, pc.values)
		count = countOf(res)
		if c, _ := res["next_cursor"].(string); c != "" {
			next := pages.Cursor{Order: pages.OrderKey(order), After: c}
			if ranked {
				next = pages.Cursor{Order: pages.OrderKey(order), Skip: cursor.Skip + pages.RowsShown}
			}
			if !ranked || next.Skip <= pages.MaxSkip {
				more = next.String()
			}
		}
	}
	out["rows"], out["more"], out["count"] = shown, more, count
	if b.Type == "list" && b.Group != nil {
		groups := []map[string]any{}
		for _, g := range pages.Groups(b, shown) {
			groups = append(groups, map[string]any{"key": g, "label": pages.GroupLabel(g)})
		}
		out["groups"] = groups
	}
	return out
}

func columnNamed(t datatables.Table, name string) datatables.Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	switch name {
	case "created_at":
		return datatables.Column{Name: name, Title: "Added", Type: "datetime"}
	case "updated_at":
		return datatables.Column{Name: name, Title: "Changed", Type: "datetime"}
	case "id":
		return datatables.Column{Name: name, Title: "Number", Type: "integer"}
	}
	return datatables.Column{Name: name}
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// partHead is a list's or a table's head as drawn: its title, size and
// words when empty, and a table's columns (each saying whether this
// viewer may put the rows in its order) and the order it is in.
func partHead(pc *pageCtx, b pages.Block, t datatables.Table, order *pages.Order, out map[string]any) {
	out["title"], out["size"], out["empty"] = b.Title, nonEmpty(b.Size, "full"), nonEmpty(b.Empty, "Nothing to show")
	out["has_detail"] = b.Detail != nil
	if b.Type == "table" {
		cols := []map[string]any{}
		for _, c := range b.Columns {
			label := c.Label
			if label == "" {
				label = columnNamed(t, c.Column).Title
				if label == "" {
					label = c.Column
				}
			}
			// sortable: a press on it puts the rows in its order; never a
			// personal column for a viewer without table.read, nor any of
			// Taskiem's records' personal columns, nor a json column or one
			// the table no longer has (pageBlock refuses them), so the web
			// and the app do not offer it.
			col := columnNamed(t, c.Column)
			sortable := col.Type != "" && col.Type != "json" && recordsOrderable(b, c.Column) && (!col.Personal || pc.values.Viewer.Personal && b.Source.Records == "")
			cols = append(cols, map[string]any{"column": c.Column, "label": label, "naira": c.Naira, "sortable": sortable})
		}
		out["columns"] = cols
		if order != nil {
			out["order"] = map[string]any{"column": order.Column, "desc": order.Desc}
		}
	}
}

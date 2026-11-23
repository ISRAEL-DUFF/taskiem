package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pages"
)

// A page's published versions and bringing one back, and a page taken out
// to a file and read back in as another page (docs/spec/pages.md §18,
// docs/pages.md). Bringing a version back makes it the draft, never what
// people see: publishing it goes through the same checks as any draft
// (against today's tables, the personal-column rules, four-eyes, where the
// person who brought it back is the one who saved the draft).

// pageVersionRoutes mounts the versions, export and import calls.
func (s *Server) pageVersionRoutes(r chi.Router) {
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/pages/import", s.importPage)
	r.With(s.need(PermPageEdit)).Get("/pages/{name}/versions", s.listPageVersions)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Post("/pages/{name}/versions/{version}/bring-back", s.bringBackPageVersion)
	r.With(s.need(PermPageEdit), s.need(PermTableRead)).Get("/pages/{name}/export", s.exportPage)
}

// listPageVersions is GET /v1/pages/{name}/versions: every published
// version, newest first, with who published it, when, and what it changed
// on the one before, in plain words.
func (s *Server) listPageVersions(w http.ResponseWriter, r *http.Request) {
	var out []map[string]any
	if err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT v.version, v.document, v.published_by, v.published_at,
				COALESCE(NULLIF(u.name, ''), NULLIF(split_part(u.email, '@', 1), ''), '')
			FROM page_versions v
			LEFT JOIN users u ON u.id::text = taskiem_actor_human(v.published_by)
			WHERE v.page_id = $1 ORDER BY v.version`, pg.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var prev *pages.Page
		for rows.Next() {
			var (
				version int
				doc     []byte
				by, who string
				at      time.Time
			)
			if err := rows.Scan(&version, &doc, &by, &at, &who); err != nil {
				return err
			}
			cur, _ := pages.Parse(doc)
			changes := pages.Changes(prev, cur)
			if cur == nil {
				changes = []string{"This version cannot be read."}
			}
			if who == "" {
				who = actorWords(by)
			}
			v := map[string]any{"version": version, "published_by": who, "published_at": at.UTC().Format(time.RFC3339),
				"summary": strings.Join(changes, " "), "changes": nonNil(changes), "roles": []string{}, "who": "",
				"current": pg.Version != nil && *pg.Version == version}
			if cur != nil {
				v["roles"], v["who"] = nonNil(cur.Roles), pages.WhoWords(cur.Roles)
				v["title"] = cur.Title
			}
			out = append(out, v)
			if cur != nil {
				prev = cur
			}
		}
		return rows.Err()
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": nonNil(out)})
}

// actorWords names an actor that is no person in the business: an API key,
// a workflow's run, an agent.
func actorWords(actor string) string {
	switch {
	case strings.HasPrefix(actor, "key:"):
		return "An API key"
	case strings.HasPrefix(actor, "workflow:"):
		return "A workflow"
	case strings.HasPrefix(actor, "agent:"):
		return "An assistant"
	}
	return "Someone no longer in the business"
}

// bringBackPageVersion is POST /v1/pages/{name}/versions/{version}/bring-back:
// that version becomes the page's draft (replacing any draft), saved by
// the caller. It is never published by this: publishing it is the same as
// any draft's. Audited as page.bring_back.
func (s *Server) bringBackPageVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		writeErr(w, http.StatusNotFound, "this page has no such version")
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	err = s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		var doc []byte
		if err := tx.QueryRow(r.Context(), `SELECT document FROM page_versions WHERE page_id = $1 AND version = $2`, pg.ID, version).Scan(&doc); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: this page has no version %d", pgx.ErrNoRows, version)
			}
			return err
		}
		if pg.Draft == nil && pg.Version != nil && *pg.Version == version {
			return fmt.Errorf("%w: version %d is the page people see now", errConflict, version)
		}
		page, _ := pages.Parse(doc)
		if page == nil {
			return fmt.Errorf("%w: version %d cannot be read", errConflict, version)
		}
		page.Name = pg.Name
		if _, err := tx.Exec(r.Context(), `UPDATE pages SET draft = $2, draft_by = $3, draft_at = now(), updated_at = now() WHERE id = $1`,
			pg.ID, page.Marshal(), p.Actor()); err != nil {
			return err
		}
		if err := auditTx(r, tx, "page.bring_back", pg.Name, map[string]any{"version": version, "published": pg.Version, "replaced_draft": pg.Draft != nil}); err != nil {
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
	writeJSON(w, http.StatusOK, out)
}

// exportPage is GET /v1/pages/{name}/export: the page's document as a
// file (the published one, or the draft with draft=1, or the draft of a
// page never published). A page holds no rows; a condition on a personal
// column that carries a value is left out (Taskiem-Left-Out says how
// many). Audited as page.export.
func (s *Server) exportPage(w http.ResponseWriter, r *http.Request) {
	var doc []byte
	var left int
	var name string
	if err := s.tx(r, func(tx pgx.Tx) error {
		pg, err := loadPage(r.Context(), tx, chi.URLParam(r, "name"))
		if err != nil {
			return err
		}
		name = pg.Name
		raw, draft := pg.Published, false
		if raw == nil || (r.URL.Query().Get("draft") == "1" && pg.Draft != nil) {
			raw, draft = pg.Draft, true
		}
		page, _ := pages.Parse(raw)
		if page == nil {
			return fmt.Errorf("%w: this page cannot be read", errConflict)
		}
		env, err := s.pageEnv(r.Context(), tx, pg.ID)
		if err != nil {
			return err
		}
		out, n := pages.ForExport(page, env.Tables)
		doc, left = out.Marshal(), n
		return auditTx(r, tx, "page.export", pg.Name, map[string]any{"draft": draft, "version": pg.Version, "left_out": n})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	var pretty map[string]any
	_ = json.Unmarshal(doc, &pretty)
	body, _ := json.MarshalIndent(pretty, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.page.json"`)
	w.Header().Set("Taskiem-Left-Out", strconv.Itoa(left))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

// importPage is POST /v1/pages/import: a page read from a file becomes a
// new page's draft. It must read as a page (Parse; else 400 and nothing is
// saved); a name another page or a tool has takes the next free one; and
// it is held against this business's tables, the tables and columns it
// names that the business lacks said first, in plain words (missing). It
// counts against max_pages. Audited as page.import.
func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	var req pageDocReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Document) == 0 || req.FromTable != nil {
		writeErr(w, http.StatusBadRequest, "give the page's document read from its file")
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	var refused []pages.Problem
	err := s.tx(r, func(tx pgx.Tx) error {
		page, ps := pages.Parse(req.Document)
		if len(ps) > 0 {
			refused = ps
			return nil
		}
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
		asked := page.Name
		page.Name = freePageName(env, page.Name)
		if page.Name == "" {
			refused = []pages.Problem{{Path: "/name", Message: "Taskiem found no free short name for this page: change its name in the file."}}
			return nil
		}
		id := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(r.Context(), `INSERT INTO pages (id, tenant_id, name, title, icon, draft, draft_by, draft_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $7)`, id, p.TenantID, page.Name, page.Title, page.Icon, page.Marshal(), p.Actor()); err != nil {
			return err
		}
		missing := pages.Missing(page, env.Tables, env.AreaOn)
		if err := auditTx(r, tx, "page.import", page.Name, map[string]any{"title": page.Title, "asked_name": asked, "missing": len(missing)}); err != nil {
			return err
		}
		pg, err := loadPage(r.Context(), tx, page.Name)
		if err != nil {
			return err
		}
		if env, err = s.pageEnv(r.Context(), tx, pg.ID); err != nil {
			return err
		}
		var renamed any
		if asked != page.Name {
			renamed = asked
		}
		out = map[string]any{"page": pageDetail(p, pg, &env), "missing": nonNil(missing), "renamed_from": renamed}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if refused != nil {
		pageProblems(w, http.StatusBadRequest, "this file is not a page Taskiem can read", refused)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// freePageName is name, or the next free one (tickets-2) when another
// page, a tool's command or one of Taskiem's own words has it; "" when
// none of a hundred is free.
func freePageName(env pages.Env, name string) string {
	taken := func(n string) bool { return pages.ReservedName(n) || env.Taken(n) != "" }
	if !taken(name) {
		return name
	}
	for i := 2; i < 100; i++ {
		suffix := "-" + strconv.Itoa(i)
		n := strings.TrimRight(name[:min(len(name), 24-len(suffix))], "-") + suffix
		if !taken(n) {
			return n
		}
	}
	return ""
}

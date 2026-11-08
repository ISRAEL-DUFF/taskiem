package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/knowledge"
	"github.com/israel-duff/taskiem/engine/knowledge/extract"
)

// What a business's assistant knows (decision 0062 D12, docs/knowledge.md):
// the owner adds knowledge sources, sees what each gave, approves it, and
// can refresh, pause or delete it; the api role refreshes them daily.

// KnowledgeService builds what the assistant knows with this server's
// readers (the connectors, tables, storefront and egress guard it holds).
func (s *Server) KnowledgeService(pool *pgxpool.Pool) *knowledge.Service {
	return &knowledge.Service{Pool: pool, Readers: s.knowledgeReaders()}
}

// knowledgeRefreshEvery is how often the api role looks for sources due.
const knowledgeRefreshEvery = 5 * time.Minute

// RunKnowledgeRefresh refreshes the sources due (daily, or asked for now)
// until ctx ends.
func (s *Server) RunKnowledgeRefresh(ctx context.Context) {
	if s.Knowledge == nil {
		return
	}
	t := time.NewTicker(knowledgeRefreshEvery)
	defer t.Stop()
	for {
		if read, failed, err := s.Knowledge.RefreshDue(ctx); err != nil {
			s.Logger.Warn("knowledge: a refresh pass did not finish", "err", err)
		} else if read+failed > 0 {
			s.Logger.Info("knowledge: sources refreshed", "read", read, "failed", failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) knowledgeRoutes(r chi.Router) {
	r.Get("/", s.listKnowledge)
	r.Post("/", s.createKnowledge)
	r.Post("/files", s.createKnowledgeFile)
	r.Post("/search", s.searchKnowledge)
	r.Get("/{id}", s.getKnowledge)
	r.Patch("/{id}", s.patchKnowledge)
	r.Delete("/{id}", s.deleteKnowledge)
	r.Post("/{id}/refresh", s.refreshKnowledge)
	r.Post("/{id}/approve", s.approveKnowledge)
	r.Post("/{id}/pause", s.pauseKnowledge)
}

func (s *Server) knowledgeOn(w http.ResponseWriter) bool {
	if s.Knowledge == nil {
		writeErr(w, http.StatusServiceUnavailable, "what the assistant knows is not available on this deployment")
		return false
	}
	return true
}

func (s *Server) failKnowledge(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, knowledge.ErrNotFound):
		writeErr(w, http.StatusNotFound, "no such source")
	case errors.Is(err, knowledge.ErrBadInput):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "knowledge: bad input: "))
	default:
		s.fail(w, r, err)
	}
}

func knowledgeID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: not a source id", knowledge.ErrBadInput)
	}
	return id, nil
}

func (s *Server) listKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	list, err := s.Knowledge.List(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": knowledgeViews(list), "kinds": knowledge.Kinds})
}

// knowledgeView is a source as the API shows it: a file's text and an
// About page's are not repeated in config (the passages show them).
func knowledgeView(src knowledge.Source) map[string]any {
	var cfg map[string]any
	_ = json.Unmarshal(src.Config, &cfg)
	if src.Kind == knowledge.File {
		delete(cfg, "text")
	}
	return map[string]any{"id": src.ID, "kind": src.Kind, "title": src.Title, "config": cfg, "status": src.Status, "outside": knowledge.Outside(src.Kind),
		"approved_at": src.ApprovedAt, "approved_by": src.ApprovedBy, "read_at": src.ReadAt, "changed_at": src.ChangedAt, "error": src.Error,
		"created_by": src.CreatedBy, "created_at": src.CreatedAt, "passages": src.Passages, "pending": src.Pending}
}

func knowledgeViews(list []knowledge.Source) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, src := range list {
		out = append(out, knowledgeView(src))
	}
	return out
}

type knowledgeReq struct {
	Kind   string          `json:"kind"`
	Title  *string         `json:"title"`
	Config json.RawMessage `json:"config"`
}

func (s *Server) createKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	var req knowledgeReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Kind == knowledge.File {
		writeErr(w, http.StatusBadRequest, "a file is added with POST /v1/knowledge/files")
		return
	}
	p := principalFrom(r.Context())
	cfg, err := s.checkKnowledgeConfig(r.Context(), p.TenantID, req.Kind, req.Config)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	title := defaultKnowledgeTitle(req.Kind)
	if req.Title != nil {
		title = *req.Title
	}
	src, err := s.Knowledge.Create(r.Context(), p.TenantID, req.Kind, title, cfg, p.Actor())
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.create", src)
	s.readNow(w, r, src.ID, http.StatusCreated)
}

func defaultKnowledgeTitle(kind string) string {
	switch kind {
	case knowledge.About:
		return "About my business"
	case knowledge.Storefront:
		return "My storefront"
	case knowledge.Website:
		return "My website"
	case knowledge.Books:
		return "Items in my books"
	}
	return kind
}

// checkKnowledgeConfig checks a source's settings for its kind and
// answers them as kept.
func (s *Server) checkKnowledgeConfig(ctx context.Context, tenant uuid.UUID, kind string, raw json.RawMessage) (json.RawMessage, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", knowledge.ErrBadInput, fmt.Sprintf(format, args...))
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, bad("config is an object")
		}
	}
	str := func(k string) string { v, _ := cfg[k].(string); return strings.TrimSpace(v) }
	out := map[string]any{}
	switch kind {
	case knowledge.About:
		text := str("text")
		if text == "" || utf8.RuneCountInString(text) > maxAboutChars {
			return nil, bad("About my business has text of 1 to %d characters", maxAboutChars)
		}
		out["text"] = text
	case knowledge.Storefront:
	case knowledge.Table:
		name := str("table")
		var kept bool
		err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT kept_by IS NOT NULL FROM data_tables WHERE name = $1`, name).Scan(&kept)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, bad("there is no table called %q", name)
		}
		if err != nil {
			return nil, err
		}
		if kept {
			return nil, bad("%q is kept by Taskiem and is not read into what the assistant knows", name)
		}
		out["table"] = name
		if cols, ok := cfg["columns"].([]any); ok {
			names := []string{}
			for _, c := range cols {
				if n, ok := c.(string); ok && n != "" {
					names = append(names, n)
				}
			}
			out["columns"] = names
		}
	case knowledge.Website:
		site, err := uuid.Parse(str("site"))
		if err != nil {
			return nil, bad("a website source names one of the business's verified websites (site)")
		}
		var st embedsites.Site
		err = s.tx0(ctx, tenant, func(tx pgx.Tx) error {
			var err error
			st, err = embedsites.Get(ctx, tx, tenant, site)
			return err
		})
		if err != nil {
			return nil, bad("no such website of the business")
		}
		if st.VerifiedAt == nil || st.UnverifiedAt != nil {
			return nil, bad("%s is not verified as the business's own; verify it first (Settings, Websites)", st.Origin)
		}
		start := str("start")
		if start == "" {
			start = st.Origin + "/"
		}
		u, err := url.Parse(start)
		if err != nil || u.Scheme+"://"+u.Host != st.Origin {
			return nil, bad("the first page must be on %s", st.Origin)
		}
		pages := 20
		if n, ok := cfg["max_pages"].(float64); ok {
			pages = int(n)
		}
		if pages < 1 || pages > 50 {
			return nil, bad("a website source reads 1 to 50 pages")
		}
		out["site"], out["origin"], out["start"], out["max_pages"] = site.String(), st.Origin, u.String(), pages
	case knowledge.Sheet:
		if str("spreadsheet_id") == "" || str("range") == "" {
			return nil, bad("a sheet source has spreadsheet_id and range (Sheet1!A1:F200)")
		}
		out["connection"], out["spreadsheet_id"], out["range"] = str("connection"), str("spreadsheet_id"), str("range")
	case knowledge.Books:
		ref := str("connector")
		if ref == "" {
			return nil, bad("a books source names its connector (quickbooks, zohobooks)")
		}
		out["connector"], out["connection"] = ref, str("connection")
	default:
		return nil, bad("no such kind of source (%s)", strings.Join(knowledge.Kinds, ", "))
	}
	return json.Marshal(out)
}

// tx0 runs fn in the tenant's scope.
func (s *Server) tx0(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, fn)
}

// readNow reads a source and answers it with what it gave: the owner sees
// it before approving. A read that fails is answered too, with its error.
func (s *Server) readNow(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	p := principalFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	_, readErr := s.Knowledge.Refresh(ctx, p.TenantID, id)
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	out := knowledgeView(src)
	pending := src.Pending > 0
	passages, err := s.Knowledge.Passages(r.Context(), p.TenantID, id, pending, 50)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	out["shown"] = passages
	if readErr != nil && errors.Is(readErr, knowledge.ErrBadInput) {
		s.failKnowledge(w, r, readErr)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) getKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	src, err := s.Knowledge.Get(r.Context(), tenant, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	out := knowledgeView(src)
	pending := r.URL.Query().Get("pending") == "true" || (r.URL.Query().Get("pending") == "" && src.Pending > 0)
	passages, err := s.Knowledge.Passages(r.Context(), tenant, id, pending, 200)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	out["shown"] = passages
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	var req knowledgeReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	var cfg json.RawMessage
	if len(req.Config) > 0 && src.Kind != knowledge.File {
		if cfg, err = s.checkKnowledgeConfig(r.Context(), p.TenantID, src.Kind, req.Config); err != nil {
			s.failKnowledge(w, r, err)
			return
		}
	}
	if err := s.Knowledge.Update(r.Context(), p.TenantID, id, req.Title, cfg); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.update", src)
	if cfg != nil {
		s.readNow(w, r, id, http.StatusOK)
		return
	}
	s.getKnowledge(w, r)
}

func (s *Server) refreshKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.readNow(w, r, id, http.StatusOK)
}

func (s *Server) approveKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.Knowledge.Approve(r.Context(), p.TenantID, id, p.Actor()); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.approve", src)
	writeJSON(w, http.StatusOK, knowledgeView(src))
}

func (s *Server) pauseKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	var req struct {
		Paused bool `json:"paused"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.Knowledge.Pause(r.Context(), p.TenantID, id, req.Paused); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.pause", src)
	writeJSON(w, http.StatusOK, knowledgeView(src))
}

func (s *Server) deleteKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	id, err := knowledgeID(r)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	if err := s.Knowledge.Delete(r.Context(), p.TenantID, id); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.delete", src)
	w.WriteHeader(http.StatusNoContent)
}

// searchKnowledge answers what the assistant would find for a question,
// so the owner can try it.
func (s *Server) searchKnowledge(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	var req struct {
		Question string `json:"question"`
		Limit    int    `json:"limit"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	hits, err := s.Knowledge.Search(r.Context(), principalFrom(r.Context()).TenantID, req.Question, req.Limit, 0)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": len(hits) > 0, "passages": hits})
}

// createKnowledgeFile takes a PDF or Word document, keeps its text (never
// the file) as a source and reads it.
func (s *Server) createKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, extract.MaxFileBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("send a PDF or Word document of at most %d MB as the file field", extract.MaxFileBytes>>20))
		return
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send the document as the file field")
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, extract.MaxFileBytes+1))
	if err != nil || len(data) > extract.MaxFileBytes {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("the document is over %d MB", extract.MaxFileBytes>>20))
		return
	}
	text, err := extract.FileText(data)
	switch {
	case errors.Is(err, extract.ErrScanned):
		writeErr(w, http.StatusUnprocessableEntity, "this document is a scan with no text in it; Taskiem cannot read scans yet. Send a document with text you can select, or type it into About my business")
		return
	case errors.Is(err, extract.ErrTooLarge):
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("the document is over %d MB", extract.MaxFileBytes>>20))
		return
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, "Taskiem could not read this document: send a PDF or Word (.docx) file that is not password-protected")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	name := strings.TrimSpace(h.Filename)
	if title == "" {
		title = name
	}
	if utf8.RuneCountInString(title) > 120 {
		title = string([]rune(title)[:120])
	}
	cfg, _ := json.Marshal(map[string]any{"name": name, "text": text})
	p := principalFrom(r.Context())
	src, err := s.Knowledge.Create(r.Context(), p.TenantID, knowledge.File, title, cfg, p.Actor())
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.auditKnowledge(r, "knowledge.source.create", src)
	s.readNow(w, r, src.ID, http.StatusCreated)
}

// auditKnowledge records a change to a source: its kind and title, never
// its text.
func (s *Server) auditKnowledge(r *http.Request, action string, src knowledge.Source) {
	err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, action, src.ID.String(), map[string]any{"kind": src.Kind, "title": src.Title})
	})
	if err != nil {
		s.Logger.Warn("knowledge: audit not recorded", "action", action, "err", err)
	}
}

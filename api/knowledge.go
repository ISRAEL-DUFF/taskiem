package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/datatables"
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
	return &knowledge.Service{Pool: pool, Readers: s.knowledgeReaders(), ReadTimeout: knowledgeWorkerBudget}
}

// knowledgeRefreshEvery is how often the api role looks for sources due.
const knowledgeRefreshEvery = 5 * time.Minute

// Read budgets: one read by the daily refresh, and one while the owner
// waits (it fits the server's write timeout, which readNow extends).
const (
	knowledgeWorkerBudget = 5 * time.Minute
	knowledgeReadBudget   = 90 * time.Second
)

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
	case errors.Is(err, knowledge.ErrChanged):
		writeErr(w, http.StatusConflict, "it was read again since you looked: look at what it gave now, then use it")
	case errors.Is(err, knowledge.ErrBusy):
		writeErr(w, http.StatusConflict, "it is being read right now: try again in a minute")
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
	tenant := principalFrom(r.Context()).TenantID
	list, err := s.Knowledge.List(r.Context(), tenant)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	// What the page offers to pick: the verified websites and the tables
	// that can be read (so a member with secret.manage alone can add them).
	sites := []map[string]any{}
	tables := []map[string]any{}
	err = s.tx0(r.Context(), tenant, func(tx pgx.Tx) error {
		all, err := embedsites.List(r.Context(), tx, tenant)
		if err != nil {
			return err
		}
		for _, st := range all {
			if siteVerified(st) {
				sites = append(sites, map[string]any{"id": st.ID, "origin": st.Origin})
			}
		}
		rows, err := tx.Query(r.Context(), `SELECT name, title FROM data_tables WHERE kept_by IS NULL ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, title string
			if err := rows.Scan(&name, &title); err != nil {
				return err
			}
			tables = append(tables, map[string]any{"name": name, "title": title})
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": knowledgeViews(list), "kinds": knowledge.Kinds, "sites": sites, "tables": tables})
}

// knowledgeView is a source as the API shows it: a file's text is not
// repeated in config (the passages show it).
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

// auditHook records a change to a source in the change's own transaction:
// its kind and title and what changed, never its text.
func auditHook(r *http.Request, action string, detail map[string]any) knowledge.Hook {
	return func(tx pgx.Tx, id uuid.UUID) error {
		return auditTx(r, tx, action, id.String(), detail)
	}
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
		writeErr(w, http.StatusBadRequest, "a document is added by uploading it")
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
	src, err := s.Knowledge.Create(r.Context(), p.TenantID, req.Kind, title, cfg, p.Actor(),
		auditHook(r, "knowledge.source.create", map[string]any{"kind": req.Kind, "title": strings.TrimSpace(title)}))
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
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
	case knowledge.Sheet:
		return "My sheet"
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
			return nil, bad("the settings could not be read")
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
		var t datatables.Table
		err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM data_tables WHERE name = $1`, name).Scan(&id); err != nil {
				return err
			}
			var err error
			t, err = datatables.GetTable(ctx, tx, id)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, bad("there is no table called %q", name)
		}
		if err != nil {
			return nil, err
		}
		if t.KeptBy != "" {
			return nil, bad("%q is kept by Taskiem and is not read into what the assistant knows", name)
		}
		var chosen []string
		if cols, ok := cfg["columns"].([]any); ok {
			for _, c := range cols {
				if n, ok := c.(string); ok && n != "" {
					chosen = append(chosen, n)
				}
			}
		}
		// The columns are fixed now: a column added later (which may hold
		// personal data not yet marked) is not read until the owner picks it.
		names := []string{}
		for _, c := range knowledgeColumns(t, chosen) {
			names = append(names, c.Name)
		}
		if len(names) == 0 {
			return nil, bad("%q has no column to read that is not personal", name)
		}
		out["table"], out["table_id"], out["columns"] = name, t.ID.String(), names
	case knowledge.Website:
		site, err := uuid.Parse(str("site"))
		if err != nil {
			return nil, bad("pick one of the business's verified websites")
		}
		var st embedsites.Site
		err = s.tx0(ctx, tenant, func(tx pgx.Tx) error {
			var err error
			st, err = embedsites.Get(ctx, tx, tenant, site)
			return err
		})
		if err != nil {
			return nil, bad("that is not one of the business's websites")
		}
		if !siteVerified(st) {
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
		id := str("spreadsheet_id")
		// A sheet's link (docs.google.com/spreadsheets/d/<id>/...) is taken
		// as it is pasted.
		if m := sheetLink.FindStringSubmatch(id); m != nil {
			id = m[1]
		}
		if id == "" || str("range") == "" {
			return nil, bad("say which sheet (its link) and which cells (for example Sheet1!A1:F200)")
		}
		if !sheetID.MatchString(id) {
			return nil, bad("that is not a Google Sheet's link")
		}
		out["connection"], out["spreadsheet_id"], out["range"] = str("connection"), id, str("range")
	case knowledge.Books:
		ref := str("connector")
		if ref == "" {
			return nil, bad("say which books to read (QuickBooks or Zoho Books)")
		}
		out["connector"], out["connection"] = ref, str("connection")
	default:
		return nil, bad("no such kind of source (%s)", strings.Join(knowledge.Kinds, ", "))
	}
	return json.Marshal(out)
}

var (
	sheetLink = regexp.MustCompile(`docs\.google\.com/spreadsheets/d/([A-Za-z0-9_-]+)`)
	sheetID   = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)
)

// tx0 runs fn in the tenant's scope.
func (s *Server) tx0(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, fn)
}

// readNow reads a source and answers it with what it gave: the owner sees
// it before approving. A read that fails is answered too, with its error;
// one already being read is answered as it is (reading: true).
func (s *Server) readNow(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	p := principalFrom(r.Context())
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(knowledgeReadBudget + 30*time.Second))
	ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), knowledgeActorKey{}, p.Actor()), knowledgeReadBudget)
	defer cancel()
	_, readErr := s.Knowledge.Refresh(ctx, p.TenantID, id)
	if readErr != nil && errors.Is(readErr, knowledge.ErrBadInput) {
		s.failKnowledge(w, r, readErr)
		return
	}
	s.answerKnowledge(w, r, id, status, errors.Is(readErr, knowledge.ErrBusy))
}

// answerKnowledge answers a source with, in shown, what waits for
// approval when anything does, else what is in use.
func (s *Server) answerKnowledge(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int, reading bool) {
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
	if reading {
		out["reading"] = true
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
	s.answerKnowledge(w, r, id, http.StatusOK, false)
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
	if len(req.Config) > 0 {
		if src.Kind == knowledge.File {
			writeErr(w, http.StatusBadRequest, "a document is changed by uploading it again")
			return
		}
		if cfg, err = s.checkKnowledgeConfig(r.Context(), p.TenantID, src.Kind, req.Config); err != nil {
			s.failKnowledge(w, r, err)
			return
		}
	}
	detail := map[string]any{"kind": src.Kind, "title": src.Title, "settings_changed": cfg != nil}
	if req.Title != nil && strings.TrimSpace(*req.Title) != src.Title {
		detail["title"], detail["was"] = strings.TrimSpace(*req.Title), src.Title
	}
	if err := s.Knowledge.Update(r.Context(), p.TenantID, id, req.Title, cfg, auditHook(r, "knowledge.source.update", detail)); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	// New settings are read at once, unless the source is paused (then
	// when it is resumed and read).
	if cfg != nil && src.Status != knowledge.StatusPaused {
		s.readNow(w, r, id, http.StatusOK)
		return
	}
	s.answerKnowledge(w, r, id, http.StatusOK, false)
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
	p := principalFrom(r.Context())
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	if src.Status == knowledge.StatusPaused {
		writeErr(w, http.StatusBadRequest, "it is paused: resume it first")
		return
	}
	after := s.KnowledgeRereadAfter
	if after == 0 {
		after = time.Minute
	}
	if src.ReadAt != nil && time.Since(*src.ReadAt) < after {
		writeErr(w, http.StatusTooManyRequests, "it was read less than a minute ago: try again in a minute")
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "knowledge.source.refresh", id.String(), map[string]any{"kind": src.Kind, "title": src.Title})
	}); err != nil {
		s.fail(w, r, err)
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
	// read_at is the read the owner looked at: a source read again since
	// is not approved unseen.
	var req struct {
		ReadAt time.Time `json:"read_at"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ReadAt.IsZero() {
		writeErr(w, http.StatusBadRequest, "say which reading you approve (read_at)")
		return
	}
	p := principalFrom(r.Context())
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	if err := s.Knowledge.Approve(r.Context(), p.TenantID, id, req.ReadAt, p.Actor(), auditHook(r, "knowledge.source.approve",
		map[string]any{"kind": src.Kind, "title": src.Title, "read_at": req.ReadAt, "passages": src.Pending})); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.answerKnowledge(w, r, id, http.StatusOK, false)
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
	src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	action := "knowledge.source.resume"
	if req.Paused {
		action = "knowledge.source.pause"
	}
	if err := s.Knowledge.Pause(r.Context(), p.TenantID, id, req.Paused, auditHook(r, action, map[string]any{"kind": src.Kind, "title": src.Title})); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.answerKnowledge(w, r, id, http.StatusOK, false)
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
	if err := s.Knowledge.Delete(r.Context(), p.TenantID, id, auditHook(r, "knowledge.source.delete", map[string]any{"kind": src.Kind, "title": src.Title})); err != nil {
		s.failKnowledge(w, r, err)
		return
	}
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

// knowledgeExtracting bounds how many documents are read at once on one
// server: reading one takes memory and CPU.
var knowledgeExtracting = make(chan struct{}, 2)

// knowledgeUploadBudget is how long a document has to arrive (a slow
// connection sending 25 MB).
const knowledgeUploadBudget = 10 * time.Minute

// createKnowledgeFile takes a PDF or Word document, keeps its text (never
// the file) as a source and reads it. With source (a document source's
// id) it replaces that source's document; what it gives waits for the
// owner's approval again.
func (s *Server) createKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	if !s.knowledgeOn(w) {
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(knowledgeUploadBudget))
	_ = rc.SetWriteDeadline(time.Now().Add(knowledgeUploadBudget + knowledgeReadBudget + time.Minute))
	tooBig := fmt.Sprintf("the document is over %d MB", extract.MaxFileBytes>>20)
	r.Body = http.MaxBytesReader(w, r.Body, extract.MaxFileBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
			return
		}
		writeErr(w, http.StatusBadRequest, "choose a PDF or Word document to add")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, h, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "choose a PDF or Word document to add")
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, extract.MaxFileBytes+1))
	if err != nil || len(data) > extract.MaxFileBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
		return
	}
	p := principalFrom(r.Context())
	var replace *knowledge.Source
	if v := strings.TrimSpace(r.FormValue("source")); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "no such source")
			return
		}
		src, err := s.Knowledge.Get(r.Context(), p.TenantID, id)
		if err != nil {
			s.failKnowledge(w, r, err)
			return
		}
		if src.Kind != knowledge.File {
			writeErr(w, http.StatusBadRequest, "only a document source takes a new document")
			return
		}
		replace = &src
	}
	select {
	case knowledgeExtracting <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	text, err := extract.FileText(data)
	<-knowledgeExtracting
	switch {
	case errors.Is(err, extract.ErrScanned):
		writeErr(w, http.StatusUnprocessableEntity, "this document is a scan with no text in it; Taskiem cannot read scans yet. Send a document with text you can select, or type it into About my business")
		return
	case errors.Is(err, extract.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
		return
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, "Taskiem could not read this document: send a PDF or Word (.docx) file that is not password-protected")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	name := strings.TrimSpace(h.Filename)
	if utf8.RuneCountInString(name) > 200 {
		name = string([]rune(name)[:200])
	}
	cfg, _ := json.Marshal(map[string]any{"name": name, "text": text})
	if replace != nil {
		var t *string
		if title != "" {
			t = &title
		}
		if err := s.Knowledge.Update(r.Context(), p.TenantID, replace.ID, t, cfg, auditHook(r, "knowledge.source.update",
			map[string]any{"kind": knowledge.File, "title": replace.Title, "settings_changed": true, "document": name})); err != nil {
			s.failKnowledge(w, r, err)
			return
		}
		if replace.Status == knowledge.StatusPaused {
			s.answerKnowledge(w, r, replace.ID, http.StatusOK, false)
			return
		}
		s.readNow(w, r, replace.ID, http.StatusOK)
		return
	}
	if title == "" {
		title = name
	}
	if utf8.RuneCountInString(title) > 120 {
		title = string([]rune(title)[:120])
	}
	src, err := s.Knowledge.Create(r.Context(), p.TenantID, knowledge.File, title, cfg, p.Actor(),
		auditHook(r, "knowledge.source.create", map[string]any{"kind": knowledge.File, "title": title}))
	if err != nil {
		s.failKnowledge(w, r, err)
		return
	}
	s.readNow(w, r, src.ID, http.StatusCreated)
}

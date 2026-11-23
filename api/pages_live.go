package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
)

// A page kept up to date (docs/spec/pages.md §9, "Kept up to date"): GET
// /v1/pages/{name}/live, as server-sent events, tells an open page when
// what its parts read has changed, so a change made on the web shows on
// the phone without a reload. It follows the change notices the chat
// tools and the Chat page use (chat_changes.go's lines reach the Chat
// page's stream, chat_stream.go): each connection asks, from indexes
// (migrations 00555 to 00557), what changed since it last looked; nothing
// is pushed through the database. The looks go to the read replica when
// there is one keeping up (Store.ReadTx), as other views that may be
// seconds stale do.
//
// A notice carries no data: only the page's name and the parts that read
// what changed. The client reads those parts again (GET .../view, or a
// part's first rows), as a pull to refresh would; a big table never
// streams its rows.
//
//	ready    on connecting: {"page", "blocks": [the parts watched]}
//	changed  some parts may have changed: {"page", "blocks": [ids], "all"};
//	         every part, all, when the page was published again or who may
//	         open it changed (for a draft's preview, when the draft changed)
//	gone     the viewer may no longer open the page (its roles changed, it
//	         was deleted, or the viewer's session, membership or key ended);
//	         the stream ends
//	ping     at least every ten seconds
//
// Debounced: a connection asks every two seconds (pageLiveEvery), and all
// that changed in between is one notice, so a page is told at most once
// in two seconds however many rows a burst changes.
//
// What is watched: a data table's rows written through Taskiem (the API,
// the chat's tools, a workflow's step, the shop's stock: table_row_changes),
// and the business's orders and invoices (changed_at, never updated_at,
// which the shop's retention reads), for the parts the viewer may see (a
// record view needs the area's read permission). Only a page the viewer
// may open, and only their business's rows: the stream opens as the
// page's view does (mayOpenPage) and reads in the business's scope. Every
// thirty seconds the viewer is signed in again from the request's own
// session or key: a session ended, a membership or key taken away, or the
// page's roles no longer letting them in ends the stream (gone); a
// permission lost (order.handle) stops that view's notices.
const (
	pageLiveEvery = 2 * time.Second
	// A record changed by a transaction that commits a little after it
	// wrote (its statement's clock), or seen late on the replica, is read
	// again within this look-back, by the database's clock; what was told
	// is remembered, so it is told once.
	pageLiveLookBack = 15 * time.Second
	pageLiveMax      = 30 * time.Minute
	pageLivePing     = 10 * time.Second
	// pageLiveRecheck is how often the viewer is signed in again.
	pageLiveRecheck = 30 * time.Second
	// How many page streams one business, and one person or key, may hold
	// open; the server's cap is the Chat page's (chatStreamsTotal).
	pageLivePerTenant = 40
	pageLivePerActor  = 5
	// How many changed records one look reads; past it, everything the
	// view feeds is told and the look-back moves on.
	pageLiveBatch = 200
)

// pageWatch is what an open page watches, and what it has seen.
type pageWatch struct {
	tables  map[string][]string // a data table's name: the parts reading it
	records map[string][]string // a record view's name: the parts reading it
	// tableAt is when each table's rows were last written, as last seen.
	tableAt map[string]time.Time
	// seen are the records changed within the look-back already told,
	// by view, row and when.
	seen    map[string]map[string]time.Time
	started bool
}

// newPageWatch is what a page's parts read, for this viewer: a record
// view only when they may see it and its area is on.
func newPageWatch(page *pages.Page, p *Principal, areaOn func(string) bool) *pageWatch {
	w := &pageWatch{tables: map[string][]string{}, records: map[string][]string{}, tableAt: map[string]time.Time{}, seen: map[string]map[string]time.Time{}}
	for _, b := range page.Blocks {
		if b.Source == nil {
			continue
		}
		if b.Source.Records != "" {
			v, ok := pages.RecordViewNamed(b.Source.Records)
			if ok && p.Can(v.Perm) && (areaOn == nil || areaOn(string(v.Area))) {
				w.records[v.Name] = append(w.records[v.Name], b.ID)
			}
			continue
		}
		if b.Source.Table != "" {
			w.tables[b.Source.Table] = append(w.tables[b.Source.Table], b.ID)
		}
	}
	return w
}

// clone is the watch to look with: a look may be run twice (on the
// replica, then the primary), so what it sees is kept only once it is done.
func (w *pageWatch) clone() *pageWatch {
	c := *w
	c.tableAt = maps.Clone(w.tableAt)
	c.seen = map[string]map[string]time.Time{}
	for k, v := range w.seen {
		c.seen[k] = maps.Clone(v)
	}
	return &c
}

// adopt carries what an earlier watch of the same page had seen to this
// one (the viewer signed in again, the page read again), so nothing is
// told twice and nothing between the two is missed.
func (w *pageWatch) adopt(old *pageWatch) {
	if old == nil || !old.started {
		return
	}
	for n := range w.tables {
		if t, ok := old.tableAt[n]; ok {
			w.tableAt[n] = t
		}
	}
	for n := range w.records {
		if s, ok := old.seen[n]; ok {
			w.seen[n] = maps.Clone(s)
		}
	}
	w.started = true
}

// blocks are every part watched, sorted.
func (w *pageWatch) blocks() []string {
	var out []string
	for _, ids := range w.tables {
		out = append(out, ids...)
	}
	for _, ids := range w.records {
		out = append(out, ids...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// poll is the parts whose sources changed since the last look (none on
// the first, which only sees where things are). tx is the business's. The
// look-back is by the database's own clock, never this host's.
func (w *pageWatch) poll(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]string, error) {
	first := !w.started
	w.started = true
	var changed []string
	if len(w.tables) > 0 {
		names := make([]string, 0, len(w.tables))
		for n := range w.tables {
			names = append(names, n)
		}
		at, err := datatables.RowsChanged(ctx, tx, names)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if t, ok := at[n]; ok && !t.Equal(w.tableAt[n]) {
				w.tableAt[n] = t
				if !first {
					changed = append(changed, w.tables[n]...)
				}
			}
		}
	}
	if len(w.records) > 0 {
		var since time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp() - make_interval(secs => $1)`, pageLiveLookBack.Seconds()).Scan(&since); err != nil {
			return nil, err
		}
		for name, ids := range w.records {
			v, _ := pages.RecordViewNamed(name)
			alias := v.Tenant[:len(v.Tenant)-len(".tenant_id")]
			rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s.id::text, %s FROM %s WHERE %s = $1 AND %s >= $2 ORDER BY %s LIMIT %d`,
				alias, v.Changed, v.From, v.Tenant, v.Changed, v.Changed, pageLiveBatch), tenant, since)
			if err != nil {
				return nil, err
			}
			seen := w.seen[name]
			if seen == nil {
				seen = map[string]time.Time{}
				w.seen[name] = seen
			}
			fresh, n := false, 0
			for rows.Next() {
				var id string
				var at time.Time
				if err := rows.Scan(&id, &at); err != nil {
					rows.Close()
					return nil, err
				}
				n++
				if t, ok := seen[id]; ok && t.Equal(at) {
					continue
				}
				seen[id] = at
				fresh = true
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
			if (fresh || n == pageLiveBatch) && !first {
				changed = append(changed, ids...)
			}
			for id, at := range seen {
				if at.Before(since) {
					delete(seen, id)
				}
			}
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

// pageLiveKey is what of a page, changed, has every part told: the
// version people see and who may open it (and, for a draft's preview, the
// draft). A pin or a draft saved does not change what the page shows.
type pageLiveKey struct {
	version *int
	roles   string
	draftAt *time.Time
}

func (a pageLiveKey) same(b pageLiveKey) bool {
	eqi := (a.version == nil) == (b.version == nil) && (a.version == nil || *a.version == *b.version)
	eqt := (a.draftAt == nil) == (b.draftAt == nil) && (a.draftAt == nil || a.draftAt.Equal(*b.draftAt))
	return eqi && eqt && a.roles == b.roles
}

// pageLiveState is the page as the stream last read it.
type pageLiveState struct {
	key   pageLiveKey
	page  *pages.Page
	watch *pageWatch
}

// errPageGone ends a page's stream: the viewer may no longer open it.
var errPageGone = errors.New("pages: the page cannot be opened")

func pageKeyOf(pg pageRow, draft bool) pageLiveKey {
	k := pageLiveKey{version: pg.Version, roles: fmt.Sprint(pg.Roles)}
	if draft {
		k.draftAt = pg.DraftAt
	}
	return k
}

// loadPageLive reads the page for its stream: what the viewer may open,
// and what it watches.
func loadPageLive(ctx context.Context, tx pgx.Tx, p *Principal, name string, draft bool) (pageLiveState, error) {
	pg, err := loadPage(ctx, tx, name)
	if errors.Is(err, errNoPage) || err == nil && !mayOpenPage(p, pg) {
		return pageLiveState{}, errPageGone
	}
	if err != nil {
		return pageLiveState{}, err
	}
	draft = draft && p.Can(PermPageEdit) && pg.Draft != nil
	doc := pg.Published
	if draft {
		doc = pg.Draft
	}
	if doc == nil {
		return pageLiveState{}, errPageGone
	}
	page, _ := pages.Parse(doc)
	if page == nil {
		return pageLiveState{}, errPageGone
	}
	st, err := pageAreas(ctx, tx)
	if err != nil {
		return pageLiveState{}, err
	}
	return pageLiveState{key: pageKeyOf(pg, draft), page: page, watch: newPageWatch(page, p, st)}, nil
}

func (s *Server) pageLiveEvery() time.Duration {
	if s.PageLiveEvery > 0 {
		return s.PageLiveEvery
	}
	return pageLiveEvery
}

func (s *Server) pageLiveRecheck() time.Duration {
	if s.PageLiveRecheck > 0 {
		return s.PageLiveRecheck
	}
	return pageLiveRecheck
}

// liveActor is who holds a stream, for the per-person cap: the person, or
// the API key.
func liveActor(p *Principal) uuid.UUID {
	if p.UserID != uuid.Nil {
		return p.UserID
	}
	return p.KeyID
}

// pageLive is GET /v1/pages/{name}/live.
func (s *Server) pageLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	name := chi.URLParam(r, "name")
	draft := r.URL.Query().Get("draft") == "1"
	var st pageLiveState
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) (err error) {
		st, err = loadPageLive(ctx, tx, p, name, draft)
		return err
	}); err != nil {
		if errors.Is(err, errPageGone) {
			err = errNoPage
		}
		s.fail(w, r, err)
		return
	}
	release, ok := s.pageGate.acquireN(p.TenantID, pageLivePerTenant, "business")
	if !ok {
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "too many open pages are kept up to date: pull to refresh this one")
		return
	}
	defer release()
	releaseActor, ok := s.pageActorGate.acquireN(liveActor(p), pageLivePerActor, "person")
	if !ok {
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "you have too many pages open that are kept up to date: pull to refresh this one")
		return
	}
	defer releaseActor()
	ping := s.ChatStreamPing
	if ping <= 0 {
		ping = pageLivePing
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(event string, data any) error {
		b, _ := json.Marshal(data)
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		return rc.Flush()
	}
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	// reread reads the page again for p (signed in again, or the page
	// changed), keeping what was seen.
	reread := func() error {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			next, err := loadPageLive(ctx, tx, p, name, draft)
			if err != nil {
				return err
			}
			next.watch.adopt(st.watch)
			st = next
			return nil
		})
	}
	// look asks what changed: on the replica when it keeps up. The first
	// look only sees where things are.
	look := func() ([]string, bool, error) {
		var changed []string
		var key pageLiveKey
		var w2 *pageWatch
		err := s.Store.ReadTx(ctx, p.TenantID, func(tx pgx.Tx) error {
			var version *int
			var roles []string
			var draftAt *time.Time
			err := tx.QueryRow(ctx, `SELECT published_version, roles, draft_at FROM pages WHERE name = $1`, name).Scan(&version, &roles, &draftAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return errPageGone
			}
			if err != nil {
				return err
			}
			key = pageLiveKey{version: version, roles: fmt.Sprint(roles)}
			if st.key.draftAt != nil || draft {
				key.draftAt = draftAt
			}
			w2 = st.watch.clone()
			changed, err = w2.poll(ctx, tx, p.TenantID)
			return err
		})
		if err != nil {
			return nil, false, err
		}
		if !key.same(st.key) {
			// Published again, or who may open it changed: read again, and
			// every part told.
			if err := reread(); err != nil {
				return nil, false, err
			}
			return nil, true, nil
		}
		st.watch = w2
		return changed, false, nil
	}
	if _, _, err := look(); err != nil {
		return
	}
	if write("ready", map[string]any{"page": name, "blocks": nonNil(st.watch.blocks())}) != nil {
		return
	}
	lastWrite, checked := time.Now(), time.Now()
	deadline := time.Now().Add(pageLiveMax)
	tick := time.NewTicker(s.pageLiveEvery())
	defer tick.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-s.done():
			return
		case <-tick.C:
		}
		if time.Since(checked) >= s.pageLiveRecheck() {
			// The viewer signed in again from the request's own session or
			// key: ended, or no longer in this business, is gone; their
			// permissions now decide what is watched.
			checked = time.Now()
			np, err := s.resolve(r)
			if err != nil || np == nil || np.TenantID != p.TenantID || liveActor(np) != liveActor(p) {
				_ = write("gone", map[string]any{"page": name})
				return
			}
			p = np
			if err := reread(); err != nil {
				if errors.Is(err, errPageGone) {
					_ = write("gone", map[string]any{"page": name})
				}
				return
			}
		}
		changed, all, err := look()
		if errors.Is(err, errPageGone) {
			_ = write("gone", map[string]any{"page": name})
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("page live", "tenant", p.TenantID, "err", err)
			}
			return
		}
		if all {
			changed = []string{}
			for _, b := range st.page.Blocks {
				changed = append(changed, b.ID)
			}
		}
		if len(changed) > 0 {
			if write("changed", map[string]any{"page": name, "blocks": changed, "all": all}) != nil {
				return
			}
			lastWrite = time.Now()
		}
		if time.Since(lastWrite) >= ping {
			if write("ping", map[string]any{}) != nil {
				return
			}
			lastWrite = time.Now()
		}
	}
}

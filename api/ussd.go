package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/africastalking"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/ussd"
	"github.com/israel-duff/taskiem/engine/wd"
)

// The USSD fast path (spec 8.4, docs/ussd.md). An aggregator posts each
// step of a session to /channels/ussd/{tenant}/{provider} on the edge
// and must have an answer within seconds. The edge checks the tenant's
// channel token (and address allow-list), pins the session to the
// workflow version deployed for the dialled service code, and walks that
// version's menu inline from what the caller has typed: nothing on the
// way waits for the engine. Confirming seals the caller's number and
// inputs into the session row and answers at once with a reference; the
// run is started in the background (and by RunUSSD if this process dies
// first), idempotently on the session id. Whether it completed or failed
// goes back to the caller by SMS when the menu asks for it.

// USSDSettings tune the fast path; the zero value is the default.
type USSDSettings struct {
	// Budget is how long a callback may take before it is answered
	// regardless (default 2s; aggregators wait a few seconds at most).
	Budget time.Duration
	// Adapters by provider name; nil serves Africa's Talking.
	Adapters map[string]ussd.Adapter
}

// Fast-path limits.
const (
	ussdSessionTTL        = 4 * time.Minute // operators end sessions at 2-3 minutes
	ussdSessionsPerNumber = 20              // new sessions per number per tenant in 10 minutes
	ussdConfirmsPerNumber = 5               // confirmations per number per tenant in an hour
	ussdChannelTTL        = 30 * time.Second
	ussdCacheMax          = 50_000
)

// Caller-facing texts: message ids in engine/lang's catalogues, marked
// so they cannot be mistaken for a menu's own text (menus are plain
// ASCII), and put in the tenant's USSD language as the answer is written
// (ussdLocal).
const (
	ussdTextBusy     = ussdMsg + "ussd.busy"
	ussdTextGone     = ussdMsg + "ussd.gone"
	ussdTextExpired  = ussdMsg + "ussd.expired"
	ussdTextEnded    = ussdMsg + "ussd.ended"
	ussdTextTooMany  = ussdMsg + "ussd.too_many"
	ussdTextNotTaken = ussdMsg + "ussd.not_taken"
	ussdMsg          = "\x00"
)

// ussdLocal turns a caller-facing text id into the tenant's USSD language
// (its default language, when on), in the ASCII USSD carries; a menu's
// own text passes unchanged.
func (s *Server) ussdLocal(ctx context.Context, tenant uuid.UUID, text string) string {
	id, ok := strings.CutPrefix(text, ussdMsg)
	if !ok {
		return text
	}
	l := lang.EN
	if tc, err := s.tenantChannel(ctx, tenant); err == nil && slices.Contains(s.langsOn(tc), tc.Default) {
		l = tc.Default
	}
	return lang.ASCII(tr(l, id))
}

// ussdState is the edge's in-process cache. Everything in it is either
// immutable (a session's pinned workflow version, a version's menu) or
// short-lived (a channel's settings), so replicas never disagree on what
// matters: the session row in Postgres decides confirmation.
type ussdState struct {
	mu       sync.Mutex
	channels map[string]ussdChannelEntry
	sessions map[string]ussdSess
	menus    lru.Cache[string, *ussdMenu] // "workflow/version"; bounded (S34)
	// start replaces StartRun (tests make the engine slow).
	start func(context.Context, runtime.StartRequest) (runtime.RunRef, bool, error)
}

type ussdChannel struct {
	env       string
	tokenHash []byte
	cidrs     []netip.Prefix
	active    bool
}

type ussdChannelEntry struct {
	ch *ussdChannel // nil: none
	at time.Time
}

type ussdSess struct {
	workflow   uuid.UUID
	version    int
	env        string
	code       string
	ref        string
	state      string
	numberHash []byte
	expires    time.Time
	path       []string // incremental adapters only
}

type ussdMenu struct {
	menu *ussd.Menu
	def  *wd.Definition
	doc  []byte
}

// USSDHooks serves aggregators' callbacks (role edge): POST
// /{tenant}/{provider}.
func (s *Server) USSDHooks() http.Handler {
	r := chi.NewRouter()
	r.Use(s.realIP)
	r.Post("/{tenant}/{provider}", s.ussdCallback)
	return r
}

func (s *Server) ussdAdapter(name string) ussd.Adapter {
	if s.USSD.Adapters != nil {
		return s.USSD.Adapters[name]
	}
	if name == africastalking.USSDAdapter.Name() {
		return africastalking.USSDAdapter
	}
	return nil
}

func ussdTokenHash(token string) []byte {
	sum := sha256.Sum256([]byte("ussd-channel\x00" + token))
	return sum[:]
}

func ussdNumberHash(tenant uuid.UUID, number string) []byte {
	sum := sha256.Sum256([]byte("ussd-number\x00" + tenant.String() + "\x00" + number))
	return sum[:]
}

// ussdCallback answers one step of a session within the budget, always:
// a slow database or engine costs the caller a "try again", never a hang.
func (s *Server) ussdCallback(w http.ResponseWriter, r *http.Request) {
	budget := s.USSD.Budget
	if budget <= 0 {
		budget = 2 * time.Second
	}
	// Keep a margin to write the answer.
	ctx, cancel := context.WithTimeout(r.Context(), budget-budget/10)
	defer cancel()
	provider := chi.URLParam(r, "provider")
	ad := s.ussdAdapter(provider)
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	if ad == nil || err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ussd.MaxBody))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	// Authenticate before reading anything of the request into the
	// database: the channel row is looked up by the URL's tenant only.
	ch, err := s.ussdChannelFor(ctx, tenant, provider)
	if err != nil {
		s.Logger.Warn("ussd: channel lookup", "tenant", tenant, "err", err)
		ad.Reply(w, true, s.ussdLocal(ctx, tenant, ussdTextBusy))
		return
	}
	if ch == nil || !ch.active {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !ch.allows(r.URL.Query().Get("token"), clientIP(r)) {
		s.Logger.Warn("ussd: refused a callback", "tenant", tenant, "provider", provider, "from", clientIP(r))
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	q, err := ad.Parse(r, body)
	if err == nil {
		err = ussd.CheckRequest(q)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed callback")
		return
	}
	// Per number and per tenant, in this process; the database counts
	// sessions and confirmations per number across replicas.
	tkey := tenant.String()
	if !s.limiter("ussd-tenant:"+tkey, 5*time.Millisecond, 400).Allow() {
		// Recorded after the answer: the record is a database write.
		s.ussdBackground(func(ctx context.Context) { s.Store.LimitHit(ctx, tenant, "ussd_rate") })
		ad.Reply(w, true, s.ussdLocal(ctx, tenant, ussdTextBusy))
		return
	}
	if !s.limiter("ussd-number:"+tkey+"/"+q.Phone, 500*time.Millisecond, 12).Allow() {
		ad.Reply(w, true, s.ussdLocal(ctx, tenant, ussdTextTooMany))
		return
	}
	end, text := s.ussdStep(ctx, tenant, provider, ad, ch, q)
	ad.Reply(w, end, s.ussdLocal(ctx, tenant, text))
}

// ussdStep walks the session one step and says what to answer.
func (s *Server) ussdStep(ctx context.Context, tenant uuid.UUID, provider string, ad ussd.Adapter, ch *ussdChannel, q ussd.Request) (bool, string) {
	key := tenant.String() + "/" + provider + "/" + q.SessionID
	nh := ussdNumberHash(tenant, q.Phone)
	sess, cached := s.ussdCached(key)
	if !cached || ad.Incremental() {
		var msg string
		var err error
		sess, msg, err = s.ussdLoad(ctx, tenant, provider, ad, ch, q, nh)
		if err != nil {
			s.Logger.Warn("ussd: session", "tenant", tenant, "ref", ussd.Reference(tenant.String(), provider, q.SessionID), "err", err)
			return true, ussdTextBusy
		}
		if msg != "" {
			return true, msg
		}
		if !ad.Incremental() {
			s.ussdRemember(key, sess)
		}
	}
	if subtle.ConstantTimeCompare(sess.numberHash, nh) != 1 {
		return true, ussdTextEnded
	}
	if sess.state == "active" && time.Now().After(sess.expires) {
		return true, ussdTextExpired
	}
	m, err := s.ussdMenuFor(ctx, tenant, sess.workflow, sess.version)
	if err != nil {
		s.Logger.Warn("ussd: menu", "tenant", tenant, "workflow", sess.workflow, "err", err)
		return true, ussdTextBusy
	}
	path := sess.path
	if !ad.Incremental() {
		path = ussd.SplitPath(q.Input)
	}
	res := m.menu.Walk(path)
	switch res.Kind {
	case ussd.Continue:
		return false, res.Text
	case ussd.End:
		s.ussdBackground(func(ctx context.Context) {
			_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ussd_sessions SET state = 'ended', path = NULL, updated_at = now()
					WHERE tenant_id = $1 AND provider = $2 AND session_id = $3 AND state = 'active'`, tenant, provider, q.SessionID)
				return err
			})
		})
		return true, res.Text
	}
	return true, s.ussdConfirm(ctx, tenant, provider, q, sess, m, res)
}

func (ch *ussdChannel) allows(token, ip string) bool {
	if token == "" || subtle.ConstantTimeCompare(ussdTokenHash(token), ch.tokenHash) != 1 {
		return false
	}
	if len(ch.cidrs) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range ch.cidrs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ussdChannelFor reads a tenant's channel, cached briefly.
func (s *Server) ussdChannelFor(ctx context.Context, tenant uuid.UUID, provider string) (*ussdChannel, error) {
	key := tenant.String() + "/" + provider
	st := &s.ussd
	st.mu.Lock()
	if e, ok := st.channels[key]; ok && time.Since(e.at) < ussdChannelTTL {
		st.mu.Unlock()
		return e.ch, nil
	}
	st.mu.Unlock()
	// Unknown tenants cost a lookup each; pace them like other
	// unauthenticated attempts.
	if !s.limiter("ussd-lookup:"+key, 100*time.Millisecond, 20).Allow() {
		return nil, errors.New("ussd: too many channel lookups")
	}
	var ch *ussdChannel
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var c ussdChannel
		var status string
		err := tx.QueryRow(ctx, `SELECT c.environment, c.token_hash, c.allowed_cidrs, c.status FROM ussd_channels c
			JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active' WHERE c.tenant_id = $1 AND c.provider = $2`, tenant, provider).
			Scan(&c.env, &c.tokenHash, &c.cidrs, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		c.active = status == "active"
		ch = &c
		return nil
	})
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	if st.channels == nil || len(st.channels) >= ussdCacheMax {
		st.channels = map[string]ussdChannelEntry{}
	}
	st.channels[key] = ussdChannelEntry{ch: ch, at: time.Now()}
	st.mu.Unlock()
	return ch, nil
}

func (s *Server) ussdForgetChannel(tenant uuid.UUID, provider string) {
	s.ussd.mu.Lock()
	delete(s.ussd.channels, tenant.String()+"/"+provider)
	s.ussd.mu.Unlock()
}

func (s *Server) ussdCached(key string) (ussdSess, bool) {
	st := &s.ussd
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.sessions[key]
	if ok && time.Now().After(sess.expires) {
		delete(st.sessions, key)
		return ussdSess{}, false
	}
	return sess, ok
}

func (s *Server) ussdRemember(key string, sess ussdSess) {
	st := &s.ussd
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sessions == nil {
		st.sessions = map[string]ussdSess{}
	}
	if len(st.sessions) >= ussdCacheMax {
		now := time.Now()
		for k, v := range st.sessions {
			if now.After(v.expires) {
				delete(st.sessions, k)
			}
		}
		if len(st.sessions) >= ussdCacheMax {
			st.sessions = map[string]ussdSess{}
		}
	}
	sess.path = nil
	st.sessions[key] = sess
}

// ussdLoad reads a session, or opens it: the number's recent sessions are
// counted, the dialled service code routed to the workflow version
// deployed for it in the channel's environment, and the row inserted
// (another replica may win the insert; its row is used). For incremental
// adapters the path is kept sealed in the row and extended here.
func (s *Server) ussdLoad(ctx context.Context, tenant uuid.UUID, provider string, ad ussd.Adapter, ch *ussdChannel, q ussd.Request, nh []byte) (ussdSess, string, error) {
	var sess ussdSess
	var msg string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sess, msg = ussdSess{}, ""
		var path []byte
		read := func() error {
			return tx.QueryRow(ctx, `SELECT workflow_id, version, environment, service_code, reference, state, number_hash, expires_at, path
				FROM ussd_sessions WHERE tenant_id = $1 AND provider = $2 AND session_id = $3 FOR UPDATE`, tenant, provider, q.SessionID).
				Scan(&sess.workflow, &sess.version, &sess.env, &sess.code, &sess.ref, &sess.state, &sess.numberHash, &sess.expires, &path)
		}
		err := read()
		if errors.Is(err, pgx.ErrNoRows) {
			var recent int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM ussd_sessions WHERE tenant_id = $1 AND number_hash = $2
				AND created_at > now() - interval '10 minutes' LIMIT $3) x`, tenant, nh, ussdSessionsPerNumber).Scan(&recent); err != nil {
				return err
			}
			if recent >= ussdSessionsPerNumber {
				msg = ussdTextTooMany
				return nil
			}
			var wf uuid.UUID
			var version int
			rerr := tx.QueryRow(ctx, `SELECT workflow_id, version FROM triggers WHERE type = 'ussd' AND environment = $1 AND service_code = $2`,
				ch.env, q.ServiceCode).Scan(&wf, &version)
			if errors.Is(rerr, pgx.ErrNoRows) {
				msg = ussdTextGone
				return nil
			}
			if rerr != nil {
				return rerr
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ussd_sessions (tenant_id, provider, session_id, number_hash, network, environment, service_code, workflow_id, version, reference, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now() + $11::interval) ON CONFLICT DO NOTHING`,
				tenant, provider, q.SessionID, nh, q.Network, ch.env, q.ServiceCode, wf, version,
				ussd.Reference(tenant.String(), provider, q.SessionID), ussdSessionTTL.String()); err != nil {
				return err
			}
			err = read()
		}
		if err != nil {
			return err
		}
		if !ad.Incremental() {
			return nil
		}
		// Incremental: the stored path plus this input.
		if len(path) > 0 {
			v, err := expr.DecodeJSON(path)
			if err != nil {
				return err
			}
			opened, err := pii.Open(ctx, s.Store.PII, tx, tenant, v, pii.Taint{})
			if err != nil {
				return err
			}
			if list, ok := opened.([]any); ok {
				for _, x := range list {
					if str, ok := x.(string); ok {
						sess.path = append(sess.path, str)
					}
				}
			}
		}
		if q.Input == "" || sess.state != "active" || subtle.ConstantTimeCompare(sess.numberHash, nh) != 1 {
			return nil
		}
		sess.path = append(sess.path, q.Input)
		if len(sess.path) > ussd.MaxDepth+1 {
			return nil
		}
		list := make([]any, len(sess.path))
		for i, p := range sess.path {
			list[i] = p
		}
		var stored any = list
		if s.Store.PII != nil {
			sealed, err := s.Store.PII.SealTx(ctx, tx, tenant, "other", list)
			if err != nil {
				return err
			}
			stored = sealed
		}
		raw, _ := json.Marshal(stored)
		_, err = tx.Exec(ctx, `UPDATE ussd_sessions SET path = $4, updated_at = now() WHERE tenant_id = $1 AND provider = $2 AND session_id = $3`,
			tenant, provider, q.SessionID, raw)
		return err
	})
	return sess, msg, err
}

// ussdMenuFor returns a version's menu; versions never change, so it is
// read once per process.
func (s *Server) ussdMenuFor(ctx context.Context, tenant, wf uuid.UUID, version int) (*ussdMenu, error) {
	key := wf.String() + "/" + strconv.Itoa(version)
	if m, ok := s.ussd.menus.Get(key); ok {
		return m, nil
	}
	var doc []byte
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&doc)
	}); err != nil {
		return nil, err
	}
	def, err := s.definitionFor(wf, version, doc)
	if err != nil {
		return nil, err
	}
	if def.Trigger.Type != "ussd" {
		return nil, errors.New("ussd: the version has no ussd trigger")
	}
	menu, err := def.USSDMenu()
	if err != nil {
		return nil, err
	}
	m := &ussdMenu{menu: menu, def: def, doc: doc}
	s.ussd.menus.Put(key, m)
	return m, nil
}

// ussdConfirm records a confirmation: the caller's number and inputs
// sealed into the session, which moves to confirmed once (a retried or
// replayed callback finds it confirmed and gets the same answer). The run
// starts in the background; the caller gets the reference now.
func (s *Server) ussdConfirm(ctx context.Context, tenant uuid.UUID, provider string, q ussd.Request, sess ussdSess, m *ussdMenu, res ussd.Result) string {
	done := m.menu.Render(res.Text, res.Inputs, sess.ref)
	var msg string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		msg = ""
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM ussd_sessions WHERE tenant_id = $1 AND provider = $2 AND session_id = $3 FOR UPDATE`,
			tenant, provider, q.SessionID).Scan(&state); err != nil {
			return err
		}
		switch state {
		case "confirmed", "started":
			msg = done // the same answer for a retry
			return nil
		case "active":
		default:
			msg = ussdTextEnded
			return nil
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM ussd_sessions WHERE tenant_id = $1 AND number_hash = $2
			AND confirmed_at > now() - interval '1 hour' LIMIT $3) x`, tenant, sess.numberHash, ussdConfirmsPerNumber).Scan(&recent); err != nil {
			return err
		}
		if recent >= ussdConfirmsPerNumber {
			msg = ussdTextTooMany
			_, err := tx.Exec(ctx, `UPDATE ussd_sessions SET state = 'refused', reason = 'rate_limited', path = NULL, updated_at = now()
				WHERE tenant_id = $1 AND provider = $2 AND session_id = $3`, tenant, provider, q.SessionID)
			return err
		}
		data, err := s.ussdSeal(ctx, tx, tenant, m.def, q.Phone, res.Inputs)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(data)
		_, err = tx.Exec(ctx, `UPDATE ussd_sessions SET state = 'confirmed', data = $4, notify = $5, path = NULL, confirmed_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND provider = $2 AND session_id = $3`, tenant, provider, q.SessionID, raw, m.menu.SMS())
		return err
	})
	if err != nil {
		// The transaction rolled back: nothing is recorded, nothing starts.
		s.Logger.Warn("ussd: confirming", "tenant", tenant, "ref", sess.ref, "err", err)
		return ussdTextNotTaken
	}
	if msg != done && msg != "" {
		return msg
	}
	// Never wait on the engine: the hand-off runs after the answer.
	s.ussdBackground(func(ctx context.Context) {
		if err := s.ussdHandoff(ctx, tenant, provider, q.SessionID); err != nil {
			s.Logger.Warn("ussd: hand-off (the notifier retries)", "tenant", tenant, "ref", sess.ref, "err", err)
		}
	})
	return done
}

// ussdSeal seals what the hand-off needs: the caller's number (always
// personal) and the inputs, with fields the inputs schema marks x-pii and
// values recognised as personal sealed.
func (s *Server) ussdSeal(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition, phone string, inputs map[string]any) (map[string]any, error) {
	if inputs == nil {
		inputs = map[string]any{}
	}
	var caller any = phone
	var sealedInputs any = inputs
	if c := s.Store.PII; c != nil {
		env, err := c.SealTx(ctx, tx, tenant, "phone", phone)
		if err != nil {
			return nil, err
		}
		caller = env
		schema, types := def.InputsSchema()
		taint := pii.Taint{}
		if sealedInputs, err = pii.Seal(ctx, c, tx, tenant, inputs, pii.SchemaPaths(schema, types), taint); err != nil {
			return nil, err
		}
		if sealedInputs, err = pii.SealDetected(ctx, c, tx, tenant, sealedInputs, taint); err != nil {
			return nil, err
		}
	}
	return map[string]any{"caller": caller, "inputs": sealedInputs}, nil
}

// ussdBackground runs work after the answer, on its own deadline.
func (s *Server) ussdBackground(fn func(context.Context)) {
	s.bg.Add(1)
	s.bgActive.Add(1)
	go func() {
		defer s.bg.Done()
		defer s.bgActive.Add(-1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		fn(ctx)
	}()
}

func (s *Server) ussdStartRun(ctx context.Context, req runtime.StartRequest) (runtime.RunRef, bool, error) {
	if s.ussd.start != nil {
		return s.ussd.start(ctx, req)
	}
	return s.Store.StartRun(ctx, req)
}

// ussdHandoff starts the run of a confirmed session: the version the
// caller walked, if its workflow is still served on USSD there, with the
// inputs checked against its schema, as "ussd:<reference>", deduplicated
// on the session id so retries and replicas start it once. A refusal
// (plan limit, withdrawn workflow, inputs that do not fit) ends the
// session; the caller hears of it by SMS when the menu asks for outcomes.
func (s *Server) ussdHandoff(ctx context.Context, tenant uuid.UUID, provider, sid string) error {
	var (
		wf             uuid.UUID
		version        int
		env, ref, code string
		network, state string
		raw            []byte
		marked         bool
		doc            []byte
		input          any
		caller         any
	)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT workflow_id, version, environment, reference, service_code, network, state, data FROM ussd_sessions
			WHERE tenant_id = $1 AND provider = $2 AND session_id = $3`, tenant, provider, sid).
			Scan(&wf, &version, &env, &ref, &code, &network, &state, &raw)
		if err != nil || state != "confirmed" {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM triggers WHERE type = 'ussd' AND workflow_id = $1 AND environment = $2)`, wf, env).Scan(&marked); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2 AND state IN ('published', 'deprecated')`, wf, version).Scan(&doc)
		if errors.Is(err, pgx.ErrNoRows) {
			marked = false
			return nil
		}
		if err != nil {
			return err
		}
		data, err := expr.DecodeJSON(raw)
		if err != nil {
			return err
		}
		dm, _ := data.(map[string]any)
		caller = dm["caller"]
		input, err = pii.Open(ctx, s.Store.PII, tx, tenant, dm["inputs"], pii.Taint{})
		return err
	})
	if err != nil || state != "confirmed" {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !marked {
		return s.ussdRefuse(ctx, tenant, provider, sid, "withdrawn")
	}
	if input == nil {
		input = map[string]any{}
	}
	body, _ := json.Marshal(input)
	if probs := s.inputProblems(wf, version, doc, body); len(probs) > 0 {
		return s.ussdRefuse(ctx, tenant, provider, sid, "invalid_inputs")
	}
	run, created, err := s.ussdStartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: wf, Version: version, Environment: env,
		Trigger: map[string]any{"type": "ussd", "body": input, "caller": map[string]any{"phone_number": caller},
			"ussd": map[string]any{"reference": ref, "service_code": code, "provider": provider, "network": network}},
		StartedBy: "ussd:" + ref, TriggerID: "ussd/" + wf.String(), DedupKey: provider + ":" + sid,
	})
	if le, ok := runtime.IsLimit(err); ok {
		s.Logger.Info("ussd: a run refused by a plan limit", "tenant", tenant, "ref", ref, "limit", le.Limit)
		return s.ussdRefuse(ctx, tenant, provider, sid, "plan_limit")
	}
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE ussd_sessions SET state = 'started', run_id = $4, data = CASE WHEN notify THEN data END, updated_at = now()
			WHERE tenant_id = $1 AND provider = $2 AND session_id = $3 AND state = 'confirmed'`, tenant, provider, sid, run.ID)
		if err != nil || tag.RowsAffected() == 0 {
			return err // another hand-off recorded it
		}
		return auditSystem(ctx, tx, tenant, "ussd:"+ref, "run.start", run.ID.String(), map[string]any{
			"workflow": wf, "version": version, "environment": env, "created": created, "channel": "ussd", "provider": provider, "service_code": code})
	})
}

func (s *Server) ussdRefuse(ctx context.Context, tenant uuid.UUID, provider, sid, reason string) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ussd_sessions SET state = 'refused', reason = $4, data = CASE WHEN notify THEN data END, updated_at = now()
			WHERE tenant_id = $1 AND provider = $2 AND session_id = $3 AND state = 'confirmed'`, tenant, provider, sid, reason)
		return err
	})
}

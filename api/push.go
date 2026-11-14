package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Chat push (docs/spec/chat-push.md): the browsers a person allowed to be
// notified when the app is closed, and the notification itself. A person
// allows each browser for the business they are in; a push goes to the members
// whose role holds chat.trade when a message comes from a linked business,
// unless the conversation is muted, as the messages to their phones do.

func (s *Server) pushRoutes(r chi.Router) {
	r.Get("/me/push/key", s.pushKey)
	r.Get("/me/push", s.listPush)
	r.Put("/me/push", s.putPush)
	r.Delete("/me/push", s.deletePushEndpoint)
	r.Patch("/me/push/{id}", s.patchPush)
	r.Delete("/me/push/{id}", s.deletePush)
}

func pushErr(err error) error {
	switch {
	case errors.Is(err, push.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "push: invalid: "))
	case errors.Is(err, push.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

// pushPerson is the signed-in person, for a route that is theirs alone; the
// feature being off answers as nothing there.
func (s *Server) pushPerson(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	if !s.Push.Enabled() {
		s.fail(w, r, pgx.ErrNoRows)
		return nil, false
	}
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, fmt.Errorf("%w: a push belongs to a person, not an API key", errForbidden))
		return nil, false
	}
	return p, true
}

func (s *Server) pushKey(w http.ResponseWriter, r *http.Request) {
	if !s.Push.WebPushOn() {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.Push.Config.PublicKey})
}

type pushOut struct {
	Devices []push.Device `json:"devices"`
	// Max is how many browsers a person may allow in a business.
	Max int `json:"max"`
	// Added is the device a PUT just added or updated (so the device knows
	// which of the list it is).
	Added *uuid.UUID `json:"added,omitempty"`
	// Kinds are the kinds of notification a device may take, in order.
	Kinds []string `json:"kinds"`
	// Browsers and Phones say which devices this server can notify.
	Browsers bool `json:"browsers"`
	Phones   bool `json:"phones"`
}

func (s *Server) pushView(r *http.Request, p *Principal) (pushOut, error) {
	out := pushOut{Max: push.MaxDevices, Kinds: push.Kinds, Browsers: s.Push.WebPushOn(), Phones: s.Push.FCMOn()}
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Devices, err = push.ListTx(r.Context(), tx, p.TenantID, p.UserID)
		return err
	})
	return out, err
}

func (s *Server) listPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	out, err := s.pushView(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	var in push.Subscription
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch in.Kind {
	case push.App:
		if !s.Push.FCMOn() {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
		if err := push.ValidToken(in.Token); err != nil {
			s.fail(w, r, pushErr(err))
			return
		}
	default:
		if !s.Push.WebPushOn() {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
		if err := s.Push.Check(in.Endpoint); err != nil {
			s.fail(w, r, pushErr(err))
			return
		}
	}
	// A phone is the install its session is: notified only while signed in.
	if in.Kind == push.App {
		var dev *uuid.UUID
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_session_device($1)`, hashToken(sessionToken(r))).Scan(&dev); err != nil {
			s.fail(w, r, err)
			return
		}
		// Only from the app signed in on that phone: a row with no install
		// behind it could never be stopped by signing the phone out.
		if dev == nil {
			s.fail(w, r, fmt.Errorf("%w: notifications on a phone are turned on in the Taskiem app, signed in on that phone", errBadRequest))
			return
		}
		in.DeviceID = *dev
	}
	// Those who run the business take alerts too, unless they say otherwise.
	defaults := push.DefaultKinds(p.HasRole("owner") || p.HasRole("admin"))
	var added uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		d, err := push.AddTx(r.Context(), tx, p.TenantID, p.UserID, in, defaults)
		if err != nil {
			return err
		}
		added = d.ID
		return auditTx(r, tx, "push.add", d.ID.String(), map[string]any{"label": d.Label, "kind": d.Kind})
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	out, err := s.pushView(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out.Added = &added
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		Label   *string   `json:"label"`
		Preview *bool     `json:"preview"`
		Kinds   *[]string `json:"kinds"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := push.UpdateTx(r.Context(), tx, p.TenantID, p.UserID, id, in.Label, in.Preview, in.Kinds)
		return err
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	s.listPush(w, r)
}

func (s *Server) deletePush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := push.RemoveTx(r.Context(), tx, p.TenantID, p.UserID, id); err != nil {
			return err
		}
		return auditTx(r, tx, "push.remove", id.String(), nil)
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	s.listPush(w, r)
}

// deletePushEndpoint is a browser taking itself off (the switch, signing out):
// by its address, which only it knows. Nothing there is not an error.
func (s *Server) deletePushEndpoint(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		// Token is the app's (it took itself off).
		Token string `json:"token"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		addr, who := in.Endpoint, "this browser"
		if in.Token != "" {
			addr, who = in.Token, "this phone"
		}
		if err := push.RemoveEndpointTx(r.Context(), tx, p.TenantID, p.UserID, addr); err != nil {
			return err
		}
		return auditTx(r, tx, "push.remove", who, nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listPush(w, r)
}

// pushMaxParallel bounds the requests to push services at once.
const pushMaxParallel = 8

// pushMessage queues a message that came from a linked business (fromTenant,
// named from) for the devices of the receiving business's members who write
// in its conversations: the title is the sender's business as the chat list
// shows it, the body the same line the phone messages carry without a
// document's reference (a device that switched the preview off is sent
// "New message"), the link the conversation. It does what tellLinkMessage has
// decided is to be told (the mute is checked there); the bell item that
// follows is not pushed again (migration 00446 skips a chat's).
func (s *Server) pushMessageTx(ctx context.Context, tx pgx.Tx, to, link, fromTenant uuid.UUID, from string, m links.Message) error {
	if !s.Push.Enabled() {
		return nil
	}
	body := whatsapp.SafeText(linkMessageLine(lang.EN, m, false))
	if m.Kind == "card" {
		body = "Sent you a document" // a document says only what it is
	}
	// A muted conversation is not pushed.
	if muted, err := links.MutedTx(ctx, tx, to, link); err != nil || muted {
		return err
	}
	users, err := holdersTx(ctx, tx, PermChatTrade)
	if err != nil || len(users) == 0 {
		return err
	}
	var icon string
	// The business as the chat list shows it: the name and logo it chose.
	if shown, err := links.ShownTx(ctx, tx); err == nil {
		if sh, ok := shown[fromTenant]; ok {
			if sh.Name != "" {
				from = sh.Name
			}
			if sh.HasLogo {
				icon = "/v1/links/" + fromTenant.String() + "/logo?size=320"
			}
		}
	}
	return push.EnqueueTx(ctx, tx, to, users, push.Queued{Kind: push.KindBusiness, Title: from, Body: body,
		Link: "/chat?with=" + link.String(), Tag: link.String(), Icon: icon})
}

// pushEvery is how often the queue is looked at without being told of a push
// (LISTEN push_outbox tells at once).
const pushEvery = 5 * time.Second

// RunPush sends the queued pushes (migration 00446): at once when one is
// queued, and every pushEvery for those waiting to be tried again.
func (s *Server) RunPush(ctx context.Context) {
	if !s.Push.Enabled() {
		// Nothing is sent, but devices left may still have pushes queued:
		// they go after their life, so the queue does not grow.
		for ctx.Err() == nil {
			if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_push_expire($1::interval)`, (push.TTL * time.Second).String()); err != nil && ctx.Err() == nil {
				s.Logger.Warn("push: expiring the queue", "err", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Hour):
			}
		}
		return
	}
	for ctx.Err() == nil {
		if _, err := s.DrainPush(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Warn("push: sending the queue", "err", err)
		}
		s.waitForPush(ctx)
	}
}

// waitForPush waits for a push to be queued (LISTEN), or pushEvery; it
// answers whether it was told.
func (s *Server) waitForPush(ctx context.Context) bool {
	wait, cancel := context.WithTimeout(ctx, pushEvery)
	defer cancel()
	conn, err := s.Store.Pool.Acquire(wait)
	if err != nil {
		<-wait.Done()
		return false
	}
	defer conn.Release()
	if _, err := conn.Exec(wait, "LISTEN push_outbox"); err != nil {
		<-wait.Done()
		return false
	}
	_, err = conn.Conn().WaitForNotification(wait)
	// Back in the pool, not listening.
	_, _ = conn.Exec(context.Background(), "UNLISTEN push_outbox")
	return err == nil
}

// DrainPush sends every push that is due, business by business, in each one's
// own scope; it answers how many were sent to at least one device.
func (s *Server) DrainPush(ctx context.Context) (int, error) {
	if !s.Push.Enabled() {
		return 0, nil
	}
	var tenants []uuid.UUID
	rows, err := s.Store.Pool.Query(ctx, `SELECT taskiem_push_due(200)`)
	if err != nil {
		return 0, err
	}
	if tenants, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, t := range tenants {
		// A business with more than one batch due is drained to the end.
		for round := 0; round < 20 && ctx.Err() == nil; round++ {
			n, claimed, err := s.drainPushOf(ctx, t)
			sent += n
			if err != nil {
				errs = append(errs, err)
				break
			}
			if claimed < pushBatch {
				break
			}
		}
	}
	return sent, errors.Join(errs...)
}

// pushBatch is how many pushes of one business are claimed at once.
const pushBatch = 50

// pushUnread is the icon's count, per person: the conversations with
// something unread that are theirs to see (linked businesses' chats for
// those who chat, customers' for those who answer them).
type pushUnread struct {
	links, customers int
	chat, orders     map[uuid.UUID]bool
}

func (u pushUnread) of(user uuid.UUID) int {
	n := 0
	if u.chat[user] {
		n += u.links
	}
	if u.orders[user] {
		n += u.customers
	}
	return n
}

func pushUnreadTx(ctx context.Context, tx pgx.Tx) (pushUnread, error) {
	var u pushUnread
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(DISTINCT link_id) FROM link_messages WHERE NOT mine AND read_at IS NULL AND hidden_at IS NULL),
		(SELECT count(DISTINCT conversation_id) FROM customer_messages WHERE NOT mine AND read_at IS NULL)`).Scan(&u.links, &u.customers); err != nil {
		return u, err
	}
	set := func(perm string) (map[uuid.UUID]bool, error) {
		users, err := holdersTx(ctx, tx, perm)
		m := map[uuid.UUID]bool{}
		for _, x := range users {
			m[x] = true
		}
		return m, err
	}
	var err error
	if u.chat, err = set(PermChatTrade); err != nil {
		return u, err
	}
	u.orders, err = set(PermOrders)
	return u, err
}

// drainPushOf sends one batch of a business's due pushes; it answers how
// many reached at least one device, and how many were claimed.
func (s *Server) drainPushOf(ctx context.Context, tenant uuid.UUID) (int, int, error) {
	var queued []push.Queued
	var targets []push.Target
	var unread pushUnread
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if queued, err = push.ClaimTx(ctx, tx, pushBatch); err != nil || len(queued) == 0 {
			return err
		}
		var users []uuid.UUID
		for _, q := range queued {
			users = append(users, q.User)
		}
		if targets, err = push.TargetsTx(ctx, tx, tenant, users); err != nil {
			return err
		}
		unread, err = pushUnreadTx(ctx, tx)
		return err
	}); err != nil || len(queued) == 0 {
		return 0, 0, err
	}
	type result struct {
		device uuid.UUID
		out    push.Outcome
	}
	type job struct {
		q       push.Queued
		results []result
	}
	// One notification per conversation on a device: of several pushes for
	// the same person and tag, only the newest is sent (sent at once, an
	// older one could arrive last and replace it); the others are done.
	newest := map[string]int{}
	for i, q := range queued {
		newest[q.User.String()+"\x00"+q.Tag] = i // claimed oldest first
	}
	jobs := make([]*job, len(queued))
	// A batch ends well inside its lease: another replica does not claim
	// it again while it is still being sent.
	sendCtx, cancel := context.WithTimeout(ctx, push.Lease-30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	gate := make(chan struct{}, pushMaxParallel)
	for i, q := range queued {
		j := &job{q: q}
		jobs[i] = j
		if newest[q.User.String()+"\x00"+q.Tag] != i {
			continue
		}
		payload := push.Payload{Title: q.Title, Body: q.Body, Link: q.Link, Tag: q.Tag, Unread: unread.of(q.User), Icon: q.Icon,
			Business: tenant.String(), Kind: q.Kind}
		for _, t := range targets {
			// A retry is not sent again to the devices that took it.
			if t.User != q.User || !t.Takes(q.Kind) || slices.Contains(q.SentTo, t.ID) {
				continue
			}
			wg.Add(1)
			gate <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-gate }()
				start := time.Now()
				out, status, err := s.Push.Send(sendCtx, tenant, t, payload)
				telemetry.PushSeconds.Observe(time.Since(start).Seconds())
				telemetry.PushSent.WithLabelValues(push.OutcomeNames[out]).Inc()
				if err != nil {
					// No address, no keys, no words: the status and what kind of error.
					s.Logger.Warn("push: not sent", "tenant", tenant, "device", t.ID, "kind", t.Kind, "status", status, "gone", out == push.Gone)
				}
				mu.Lock()
				j.results = append(j.results, result{t.ID, out})
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	sent := 0
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for _, j := range jobs {
			failed, went := false, false
			var took []uuid.UUID
			for _, r := range j.results {
				if err := push.RecordTx(ctx, tx, tenant, r.device, r.out); err != nil {
					return err
				}
				failed = failed || r.out == push.Failed || r.out == push.Later
				went = went || r.out == push.Sent
				if r.out == push.Sent {
					took = append(took, r.device)
				}
			}
			if went {
				sent++
			}
			// Done when every device took it, was gone, or none takes it;
			// tried again later when one failed (a device that took it
			// already shows one notification per tag, so a second is the same).
			if !failed {
				if err := push.DoneTx(ctx, tx, j.q.ID); err != nil {
					return err
				}
				continue
			}
			dropped, err := push.RetryTx(ctx, tx, j.q, took)
			if err != nil {
				return err
			}
			if dropped {
				// Said where an operator looks: a push nobody will get.
				s.Logger.Error("push: dropped after its tries", "tenant", tenant, "kind", j.q.Kind, "attempts", j.q.Attempts+1)
				telemetry.PushSent.WithLabelValues("dropped").Inc()
			}
		}
		return nil
	})
	return sent, len(queued), err
}

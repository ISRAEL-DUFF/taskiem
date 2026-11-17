package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
)

// One customer across the app and the business's own WhatsApp number
// (docs/spec/taskiem-for-everyone.md §11, §5.5 item 4; migration 00468).
//
// Inside one business, a person in the app whose phone is verified on
// their account (user_phones) and a customer thread on the business's own
// WhatsApp number that WhatsApp has delivered a message from are one
// customer. WhatsApp threads are only ever made by a message WhatsApp
// delivered (customerThreadTx), so a phone typed on a form, or the phone
// of an app thread made from an order, matches nothing: matching starts
// from the person's verified phone, never from what a thread holds. The
// business's own key makes the subject of that phone and finds the
// thread; nothing is looked up across businesses.
//
// The link is kept on the WhatsApp thread (linked_to, the app thread),
// one WhatsApp thread per app thread. It is made:
//   - when the person starts a chat, or writes in one, in the app
//     (through the customer door, in their own transaction);
//   - when WhatsApp delivers a message from a number that is the verified
//     phone of a person chatting with the business in the app (in the
//     business's transaction, through the way back);
//   - once for the pairs there were before this (the sweep, with the
//     owners' one each minute).
// It is undone when the person changes their phone (the WhatsApp thread
// stays under the old number and says the customer changed their number;
// the app thread is matched again only by a WhatsApp message after the
// change), when they delete their account, and when the business erases
// either line's customer.
//
// The person's copy shows the WhatsApp line's messages, marked, from the
// link on, and those of the 30 days before it (whatsappHistory), never
// from before their phone was verified on their account (a number that
// was someone else's shows none of theirs). They are copied from the
// business's copy, opened from its seal under the customer's subject.

const lineWhatsApp = "whatsapp"

// whatsappHistory is how far back the person's copy shows the WhatsApp
// line when the lines are joined, and whatsappHistoryMax how many
// messages at most.
const (
	whatsappHistory    = 30 * 24 * time.Hour
	whatsappHistoryMax = 200
)

// customerNumberChangedWords is what a WhatsApp thread says once its
// customer moved their app line to another number (§5.5).
const customerNumberChangedWords = "This customer changed their number"

// threadLine is one of a customer's lines in a thread: where a reply on
// it can go, and what stops it.
type threadLine struct {
	Line string    `json:"line"`
	ID   uuid.UUID `json:"id"`
	// Phone is the number on the line ("" once erased).
	Phone string `json:"phone"`
	// WindowOpen: a reply goes as words now (WhatsApp: within 24 hours of
	// the customer's last message; the app: always, unless blocked).
	WindowOpen  bool       `json:"window_open"`
	WindowUntil *time.Time `json:"window_until"`
	// Stopped: on WhatsApp, the customer sent STOP; in the app, they
	// blocked the business, or left Taskiem.
	Stopped bool `json:"stopped"`
	// Blocked: the business blocked them in the app.
	Blocked bool   `json:"blocked"`
	Notice  string `json:"notice,omitempty"`
	// LastInAt is when the customer last wrote on this line.
	LastInAt *time.Time `json:"last_in_at"`
}

func lineOf(v customerChatView) threadLine {
	l := threadLine{Line: v.Line, ID: v.ID, Phone: v.Phone, WindowOpen: v.WindowOpen, WindowUntil: v.WindowUntil, Stopped: v.Stopped,
		Blocked: v.Blocked, Notice: v.Notice, LastInAt: v.lastIn}
	switch {
	case l.Notice != "":
	case v.Line == lineWhatsApp && v.Stopped:
		l.Notice = "This customer sent STOP on WhatsApp"
	case v.Line == lineApp && v.Blocked:
		l.Notice = "You blocked this customer in the app"
	}
	return l
}

// lineOpen is whether a reply can go on a line now (a template counts).
func lineOpen(v customerChatView) bool {
	if v.Phone == "" || v.Stopped {
		return false
	}
	if v.Line == lineApp {
		return !v.Blocked && v.person != nil
	}
	return true
}

// replyLineOf is where a reply goes by default: the line the customer
// last wrote on, or the other when that one cannot take a message and
// the other can.
func replyLineOf(app, wa customerChatView) (customerChatView, customerChatView) {
	last, other := app, wa
	if wa.lastIn != nil && (app.lastIn == nil || wa.lastIn.After(*app.lastIn)) {
		last, other = wa, app
	}
	if !lineOpen(last) && lineOpen(other) {
		return other, last
	}
	return last, other
}

// combineLines is one customer's thread with both lines: the app thread's
// id, name and settings; the latest message of either; unread across
// both; the window, stops and notice of the line a reply goes on.
func combineLines(app, wa customerChatView) customerChatView {
	v := app
	if v.Name == "A customer" && wa.Name != "" {
		v.Name = wa.Name
	}
	if v.Phone == "" {
		v.Phone = wa.Phone
	}
	if wa.LastAt.After(app.LastAt) {
		v.Last, v.LastMine, v.LastAt = wa.Last, wa.LastMine, wa.LastAt
	}
	v.Unread = app.Unread + wa.Unread
	v.Lines = []threadLine{lineOf(app), lineOf(wa)}
	reply, _ := replyLineOf(app, wa)
	v.ReplyLine = reply.Line
	v.WindowOpen, v.WindowUntil, v.Stopped, v.Blocked, v.Notice = reply.WindowOpen, reply.WindowUntil, reply.Stopped, reply.Blocked, reply.Notice
	if wa.lastIn != nil && (v.lastIn == nil || wa.lastIn.After(*v.lastIn)) {
		v.lastIn = wa.lastIn
	}
	return v
}

// customerRow is one line's row of a thread, opened, with its subject.
type customerRow struct {
	view    customerChatView
	subject string
}

// customerThread is one customer's thread as the business sees it: the
// row a request named, each line's row, and the two joined.
type customerThread struct {
	view    customerChatView
	named   *customerRow
	app, wa *customerRow
}

func (t customerThread) ids() []uuid.UUID {
	var out []uuid.UUID
	for _, r := range []*customerRow{t.app, t.wa} {
		if r != nil {
			out = append(out, r.view.ID)
		}
	}
	return out
}

// row is the thread's row on line, nil when the customer has none there.
func (t customerThread) row(line string) *customerRow {
	if line == lineApp {
		return t.app
	}
	return t.wa
}

// lineOfRow maps each of the thread's rows to its line.
func (t customerThread) lineOfRow() map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, r := range []*customerRow{t.app, t.wa} {
		if r != nil {
			out[r.view.ID] = r.view.Line
		}
	}
	return out
}

// customerThreadIn reads the thread a request names, by either line's id:
// errCustomerChatNone when the business has no such thread.
func (s *Server) customerThreadIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idText string) (customerThread, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return customerThread{}, errCustomerChatNone
	}
	rows, err := tx.Query(ctx, `SELECT `+customerChatCols+` FROM customer_conversations c WHERE c.tenant_id = $1
		AND (c.id = $2 OR c.linked_to = $2 OR c.id = (SELECT x.linked_to FROM customer_conversations x WHERE x.tenant_id = $1 AND x.id = $2))`, tenant, id)
	if err != nil {
		return customerThread{}, err
	}
	raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
	if err != nil {
		return customerThread{}, err
	}
	var t customerThread
	for _, x := range raws {
		v, err := s.openCustomerRow(ctx, tx, tenant, x)
		if err != nil {
			return t, err
		}
		r := &customerRow{view: v, subject: x.subject}
		if v.Line == lineApp {
			t.app = r
		} else {
			t.wa = r
		}
		if v.ID == id {
			t.named = r
		}
	}
	if t.named == nil {
		return t, errCustomerChatNone
	}
	switch {
	case t.app != nil && t.wa != nil:
		t.view = combineLines(t.app.view, t.wa.view)
	default:
		t.view = t.named.view
		t.view.Lines, t.view.ReplyLine = []threadLine{lineOf(t.view)}, t.view.Line
	}
	return t, nil
}

// customerLineIn is the row of one line of the thread a request names
// (by either line's id): errCustomerChatNone when there is no thread, and
// the refusal when the customer has no such line.
func (s *Server) customerLineIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idText, line string) (customerChatView, string, error) {
	t, err := s.customerThreadIn(ctx, tx, tenant, idText)
	if err != nil {
		return customerChatView{}, "", err
	}
	r := t.row(line)
	if r == nil {
		return t.named.view, t.named.subject, noLineErr(line)
	}
	return r.view, r.subject, nil
}

func noLineErr(line string) error {
	if line == lineApp {
		return customerSendErr{http.StatusConflict, "This customer does not chat with you in the Taskiem app; they write on WhatsApp."}
	}
	return customerSendErr{http.StatusConflict, "This customer does not write to your WhatsApp number from the phone on their Taskiem account."}
}

// customerThreadsTx lists the business's customers' threads, one per
// customer (both lines joined), the latest of either line first: where
// is more of the condition on each thread's own row (c), args its
// arguments from $2. It also answers, for each row, its thread's id.
func (s *Server) customerThreadsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, where string, args []any, limit int) ([]customerChatView, map[uuid.UUID]uuid.UUID, error) {
	q := `SELECT ` + customerChatCols + ` FROM customer_conversations c WHERE c.tenant_id = $1 AND c.linked_to IS NULL ` + where + `
		ORDER BY GREATEST(c.last_message_at, COALESCE((SELECT w.last_message_at FROM customer_conversations w WHERE w.tenant_id = c.tenant_id AND w.linked_to = c.id), c.last_message_at)) DESC
		LIMIT ` + fmt.Sprint(limit)
	rows, err := tx.Query(ctx, q, append([]any{tenant}, args...)...)
	if err != nil {
		return nil, nil, err
	}
	raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
	if err != nil {
		return nil, nil, err
	}
	var apps []uuid.UUID
	for _, x := range raws {
		if x.v.Line == lineApp {
			apps = append(apps, x.v.ID)
		}
	}
	partners := map[uuid.UUID]customerChatRaw{}
	if len(apps) > 0 {
		rows, err := tx.Query(ctx, `SELECT `+customerChatCols+` FROM customer_conversations c WHERE c.tenant_id = $1 AND c.linked_to = ANY ($2)`, tenant, apps)
		if err != nil {
			return nil, nil, err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
		if err != nil {
			return nil, nil, err
		}
		for _, x := range list {
			partners[*x.v.linkedTo] = x
		}
	}
	out := make([]customerChatView, 0, len(raws))
	threadOf := map[uuid.UUID]uuid.UUID{}
	for _, x := range raws {
		v, err := s.openCustomerChat(ctx, tx, tenant, x)
		if err != nil {
			return nil, nil, err
		}
		threadOf[v.ID] = v.ID
		if p, ok := partners[v.ID]; ok {
			w, err := s.openCustomerRow(ctx, tx, tenant, p)
			if err != nil {
				return nil, nil, err
			}
			threadOf[w.ID] = v.ID
			v = combineLines(v, w)
		}
		out = append(out, v)
	}
	return out, threadOf, nil
}

// --- joining the lines ---

// customerLinkLock serializes joining lines in one business, so two joins
// at once never pair one thread twice.
func customerLinkLock(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('customer-link:' || $1::text, 0))`, tenant)
	return err
}

// joinLinesTx joins an app thread and a WhatsApp thread of the same
// customer, and shows the WhatsApp line in the person's copy from the
// latest of since (when their phone was verified) and whatsappHistory
// ago. asBusiness: the business's transaction, which reaches the person's
// copy through the way back; otherwise the person's own, through the
// door. false when either thread is joined already.
func (s *Server) joinLinesTx(ctx context.Context, tx pgx.Tx, tenant, app, wa, person uuid.UUID, since time.Time, asBusiness bool) (bool, error) {
	if err := customerLinkLock(ctx, tx, tenant); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_conversations SET linked_to = $3, linked_at = now(), number_changed_at = NULL
		WHERE tenant_id = $1 AND id = $2 AND line = 'whatsapp' AND linked_to IS NULL
		  AND NOT EXISTS (SELECT 1 FROM customer_conversations x WHERE x.tenant_id = $1 AND x.linked_to = $3)
		  AND EXISTS (SELECT 1 FROM customer_conversations a WHERE a.tenant_id = $1 AND a.id = $3 AND a.line = 'app' AND a.person_id = $4
		    -- Never an app thread whose customer the business erased.
		    AND NOT EXISTS (SELECT 1 FROM subject_keys k WHERE k.tenant_id = $1 AND k.shredded_at IS NOT NULL
		      AND k.subject_id IN (a.subject_id, a.who -> 'number' ->> 'subject')))`,
		tenant, wa, app, person)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, app); err != nil {
		return false, err
	}
	detail := map[string]any{"linked": true, "whatsapp_thread": wa, "app_thread": app}
	if err := s.lineAuditTx(ctx, tx, tenant, person, detail, asBusiness); err != nil {
		return false, err
	}
	var conv uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id, by_business, whatsapp_since) VALUES ($1, $2, $3, true, now())
		ON CONFLICT (person_id, tenant_id) DO UPDATE SET whatsapp_since = now() RETURNING id`, uuid.Must(uuid.NewV7()), person, tenant).Scan(&conv); err != nil {
		return false, err
	}
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT person_blocked_at IS NOT NULL FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, app).Scan(&blocked); err != nil {
		return false, err
	}
	if blocked {
		// While they block the business, nothing more of it comes into
		// their copy (as with a message).
		return true, nil
	}
	from := time.Now().Add(-whatsappHistory)
	if since.After(from) {
		from = since
	}
	rows, err := tx.Query(ctx, `SELECT id, mine, body, COALESCE(sender_name, ''), read_at, created_at FROM customer_messages
		WHERE tenant_id = $1 AND conversation_id = $2 AND created_at >= $3 ORDER BY created_at DESC LIMIT $4`, tenant, wa, from, whatsappHistoryMax)
	if err != nil {
		return false, err
	}
	type past struct {
		id       uuid.UUID
		mine     bool
		body     []byte
		sender   string
		readAt   *time.Time
		at       time.Time
		openedAs string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (past, error) {
		var x past
		return x, r.Scan(&x.id, &x.mine, &x.body, &x.sender, &x.readAt, &x.at)
	})
	if err != nil {
		return false, err
	}
	for i := range list {
		if list[i].openedAs, err = s.openCustomerText(ctx, tx, tenant, list[i].body); err != nil {
			return false, err
		}
	}
	for _, x := range list {
		if x.openedAs == "" {
			continue // erased
		}
		var readAt *time.Time
		if x.mine {
			// The business's message: they had it on WhatsApp.
			now := time.Now()
			readAt = &now
		} else {
			readAt = x.readAt
		}
		if err := insertWhatsAppCopyTx(ctx, tx, person, conv, tenant, x.id, !x.mine, x.openedAs, x.sender, readAt, x.at); err != nil {
			return false, err
		}
	}
	return true, nil
}

// insertWhatsAppCopyTx keeps a WhatsApp message in the person's copy,
// marked on WhatsApp (once).
func insertWhatsAppCopyTx(ctx context.Context, tx pgx.Tx, person, conv, tenant, id uuid.UUID, fromPerson bool, text, sender string, readAt *time.Time, at time.Time) error {
	if r := []rune(text); len(r) > 4000 {
		text = string(r[:4000])
	}
	if r := []rune(sender); len(r) > 120 {
		sender = string(r[:120])
	}
	if fromPerson {
		sender = ""
	}
	_, err := tx.Exec(ctx, `INSERT INTO person_messages (id, person_id, conversation_id, tenant_id, mine, kind, body, sender_name, read_at, created_at, line)
		VALUES ($1, $2, $3, $4, $5, 'text', $6, $7, $8, $9, 'whatsapp') ON CONFLICT (id) DO NOTHING`,
		id, person, conv, tenant, fromPerson, text, sender, readAt, at)
	return err
}

// lineAuditTx records joining or parting the lines in the business's
// chain: through the way back from the business's transaction (which then
// reaches the person's copy), or as the person from their own.
func (s *Server) lineAuditTx(ctx context.Context, tx pgx.Tx, tenant, person uuid.UUID, detail map[string]any, asBusiness bool) error {
	if asBusiness {
		d := map[string]any{"actor": "system:lines", "actor_type": "system"}
		for k, v := range detail {
			d[k] = v
		}
		return reachTx(ctx, tx, tenant, person, "customer.link", d)
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'person', $2, 'customer.link', $2, $3)`, tenant, person.String(), raw)
	return err
}

// joinAppTx looks for the WhatsApp thread of the person of an app thread:
// the business's thread of their verified phone, which WhatsApp has
// delivered a message from (since their number last changed, when it
// did), and joins the two. false when there is none, or they are joined.
func (s *Server) joinAppTx(ctx context.Context, tx pgx.Tx, tenant, app uuid.UUID, asBusiness bool) (bool, error) {
	var person *uuid.UUID
	var relink *time.Time
	var joined bool
	err := tx.QueryRow(ctx, `SELECT c.person_id, c.relink_after, EXISTS (SELECT 1 FROM customer_conversations x WHERE x.tenant_id = c.tenant_id AND x.linked_to = c.id)
		FROM customer_conversations c WHERE c.tenant_id = $1 AND c.id = $2 AND c.line = 'app'`, tenant, app).Scan(&person, &relink, &joined)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (person == nil || joined)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var some bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = $1 AND line = 'whatsapp' AND linked_to IS NULL)`, tenant).Scan(&some); err != nil || !some {
		return false, err
	}
	var number string
	var verified time.Time
	err = tx.QueryRow(ctx, `SELECT number, verified_at FROM taskiem_customer_link_phone($1, $2)`, tenant, *person).Scan(&number, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // no verified phone: nothing to match
	}
	if err != nil {
		return false, err
	}
	subject, err := s.phoneSubject(ctx, tx, tenant, number)
	if errors.Is(err, pii.ErrErased) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var wa uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM customer_conversations WHERE tenant_id = $1 AND line = 'whatsapp' AND subject_id = $2
		AND last_inbound_at IS NOT NULL AND linked_to IS NULL AND ($3::timestamptz IS NULL OR last_inbound_at > $3)
		ORDER BY last_inbound_at DESC LIMIT 1`, tenant, subject, relink).Scan(&wa)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.joinLinesTx(ctx, tx, tenant, app, wa, *person, verified, asBusiness)
}

// joinWhatsAppTx answers the app thread a WhatsApp thread is joined with,
// joining it first when its number, which WhatsApp just delivered a
// message from, is the verified phone of a person chatting with the
// business in the app. The business's transaction. fresh: joined now
// (the person's copy has the message already).
func (s *Server) joinWhatsAppTx(ctx context.Context, tx pgx.Tx, tenant, wa uuid.UUID, number string) (app, person *uuid.UUID, fresh bool, err error) {
	if err := tx.QueryRow(ctx, `SELECT linked_to FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, wa).Scan(&app); err != nil {
		return nil, nil, false, err
	}
	if app != nil {
		err := tx.QueryRow(ctx, `SELECT person_id FROM customer_conversations WHERE tenant_id = $1 AND id = $2`, tenant, *app).Scan(&person)
		return app, person, false, err
	}
	var p uuid.UUID
	var verified time.Time
	err = tx.QueryRow(ctx, `SELECT person_id, verified_at FROM taskiem_customer_link_person($1, $2)`, tenant, number).Scan(&p, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var a uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM customer_conversations WHERE tenant_id = $1 AND line = 'app' AND person_id = $2
		AND (relink_after IS NULL OR relink_after < now())`, tenant, p).Scan(&a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	ok, err := s.joinLinesTx(ctx, tx, tenant, a, wa, p, verified, true)
	if err != nil || !ok {
		return nil, nil, false, err
	}
	return &a, &p, true, nil
}

// mirrorWhatsAppTx shows a WhatsApp message in the person's copy, marked
// on WhatsApp, through the way back (audited, as the app's messages are):
// a message they sent (fromPerson), or the business's. Nothing while they
// block the business. The business's transaction; a savepoint, so a
// refusal leaves it whole.
func (s *Server) mirrorWhatsAppTx(ctx context.Context, tx pgx.Tx, tenant, person, msg uuid.UUID, fromPerson bool, text, sender, actor, actorType string) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	err = reachTx(ctx, sp, tenant, person, "customer.whatsapp", map[string]any{"actor": actor, "actor_type": actorType, "message": msg})
	var se customerSendErr
	if errors.As(err, &se) {
		return sp.Rollback(ctx) // they block the business
	}
	if err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	var conv uuid.UUID
	if err := sp.QueryRow(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id, by_business, whatsapp_since) VALUES ($1, $2, $3, true, now())
		ON CONFLICT (person_id, tenant_id) DO UPDATE SET last_message_at = now(), whatsapp_since = COALESCE(person_conversations.whatsapp_since, now()) RETURNING id`,
		uuid.Must(uuid.NewV7()), person, tenant).Scan(&conv); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	var readAt *time.Time
	if !fromPerson {
		now := time.Now()
		readAt = &now
	}
	if err := insertWhatsAppCopyTx(ctx, sp, person, conv, tenant, msg, fromPerson, text, sender, readAt, time.Now()); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}

// afterWhatsAppReply is what a business's message sent on WhatsApp does
// to the customer's other line, once it is sent: shown in the person's
// copy, marked on WhatsApp; and, an answer from someone at the business
// (not Taskiem's words, nor an offer), the customer no longer waiting.
func (s *Server) afterWhatsAppReply(ctx context.Context, tenant uuid.UUID, app customerChatView, m customerMessageView, in customerSend) {
	if app.person == nil {
		return
	}
	system := strings.HasPrefix(in.SenderID, "system:")
	err := db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if !system {
			if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET last_reply_at = now(), waiting_since = NULL, wait_check_at = NULL WHERE tenant_id = $1 AND id = $2`,
				tenant, app.ID); err != nil {
				return err
			}
		}
		actorType := "user"
		if system {
			actorType = "system"
		}
		return s.mirrorWhatsAppTx(ctx, tx, tenant, *app.person, m.ID, false, m.Body, m.SenderName, in.SenderID, actorType)
	})
	if err != nil {
		s.Logger.Warn("customer chat: a WhatsApp reply's other line", "tenant", tenant, "err", err)
	}
}

// partLinesTx parts an app thread from the WhatsApp thread joined with it:
// numberChanged when the person changed their phone (the WhatsApp thread
// then says so). The person's copy keeps what it was shown, and shows no
// more. asBusiness as for joinLinesTx; person nil once the account is
// gone (nothing of theirs is reached).
func (s *Server) partLinesTx(ctx context.Context, tx pgx.Tx, tenant, app uuid.UUID, person *uuid.UUID, why string, asBusiness bool) (bool, error) {
	var wa uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE customer_conversations SET linked_to = NULL, linked_at = NULL,
		number_changed_at = CASE WHEN $3 THEN now() ELSE number_changed_at END
		WHERE tenant_id = $1 AND linked_to = $2 RETURNING id`, tenant, app, why == "number_changed").Scan(&wa)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || person == nil {
		return err == nil, err
	}
	if err := s.lineAuditTx(ctx, tx, tenant, *person, map[string]any{"linked": false, "why": why, "whatsapp_thread": wa, "app_thread": app}, asBusiness); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE person_conversations SET whatsapp_since = NULL WHERE person_id = $1 AND tenant_id = $2`, *person, tenant)
	return err == nil, err
}

// phoneChangedLines is §5.5 item 4, after a person changed their phone:
// in each business they chat with, the app line follows them (the
// thread's number is the new one), the WhatsApp thread of the old number
// is parted from it and says "This customer changed their number", and
// the two are matched again only when the new number writes on WhatsApp.
// Failures are logged: the phone has changed.
func (s *Server) phoneChangedLines(ctx context.Context, person uuid.UUID, number string) {
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM person_conversations WHERE person_id = $1`, person)
		if err != nil {
			return err
		}
		businesses, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(businesses) == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(businesses...)); err != nil {
			return err
		}
		for _, b := range businesses {
			var app uuid.UUID
			err := tx.QueryRow(ctx, `UPDATE customer_conversations SET relink_after = now() WHERE tenant_id = $1 AND line = 'app' AND person_id = $2 RETURNING id`, b, person).Scan(&app)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			// The app line's number is the new one.
			env, err := s.Store.PII.SealTx(ctx, tx, b, "phone", number)
			if err != nil && !errors.Is(err, pii.ErrErased) {
				return err
			}
			if err == nil {
				raw, _ := json.Marshal(env)
				if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET who = jsonb_set(who, '{number}', $3::jsonb) WHERE tenant_id = $1 AND id = $2`, b, app, raw); err != nil {
					return err
				}
			}
			if _, err := s.partLinesTx(ctx, tx, b, app, &person, "number_changed", false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Error("customer chat: a changed phone's lines", "err", err)
	}
}

// partErasedLines parts the lines of the customers the business just
// erased (either line's subject): each line keeps its own erasure, and
// nothing more crosses between them.
func (s *Server) partErasedLines(ctx context.Context, tenant uuid.UUID, subjects []string) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id, a.person_id FROM customer_conversations w JOIN customer_conversations a ON a.tenant_id = w.tenant_id AND a.id = w.linked_to
			WHERE w.tenant_id = $1 AND (w.subject_id = ANY ($2) OR a.subject_id = ANY ($2))`, tenant, subjects)
		if err != nil {
			return err
		}
		type pair struct {
			app    uuid.UUID
			person *uuid.UUID
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) {
			var x pair
			return x, r.Scan(&x.app, &x.person)
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			if _, err := s.partLinesTx(ctx, tx, tenant, x.app, x.person, "erased", true); err != nil {
				return err
			}
		}
		return nil
	})
}

// joinSweep looks once at each app thread not yet looked at (those there
// were before lines were joined, then each new one) for its WhatsApp
// thread, each business in its own transaction. A thread that fails is
// logged and marked looked at: the person's next message looks again.
func (s *Server) joinSweep(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, conversation_id FROM taskiem_customer_link_due(200)`)
	if err != nil {
		return err
	}
	type due struct{ tenant, thread uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var x due
		return x, r.Scan(&x.tenant, &x.thread)
	})
	if err != nil {
		return err
	}
	byTenant := map[uuid.UUID][]uuid.UUID{}
	var order []uuid.UUID
	for _, x := range list {
		if _, ok := byTenant[x.tenant]; !ok {
			order = append(order, x.tenant)
		}
		byTenant[x.tenant] = append(byTenant[x.tenant], x.thread)
	}
	mark := func(tenant uuid.UUID, ids []uuid.UUID) error {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = now() WHERE tenant_id = $1 AND id = ANY ($2) AND link_checked_at IS NULL`, tenant, ids)
			return err
		})
	}
	var errs []error
	for _, tenant := range order {
		ids := byTenant[tenant]
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			for _, id := range ids {
				if _, err := s.joinAppTx(ctx, tx, tenant, id, true); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `UPDATE customer_conversations SET link_checked_at = now() WHERE tenant_id = $1 AND id = ANY ($2) AND link_checked_at IS NULL`, tenant, ids)
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", tenant, err))
			if err := mark(tenant, ids); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/speech"
)

// The conversations page (decision 0062 V7, docs/conversations.md): the
// business's assistants, their customers, what was said with each (words
// shown with numbers and emails hidden, unless revealed by someone who may,
// audited), answering a customer by hand through the same connection, and
// the assistant's numbers. Talk to it here is in conversations_test_chat.go.

// Permissions.
const (
	PermConversationRead  = access.ConversationRead
	PermConversationReply = access.ConversationReply
)

func (s *Server) conversationRoutes(r chi.Router) {
	read := s.need(PermConversationRead)
	reply := s.need(PermConversationReply)
	r.With(read).Get("/", s.listConversations)
	r.With(read).Get("/thread", s.getConversation)
	r.With(read).Get("/metrics", s.conversationMetrics)
	r.With(reply).Get("/templates", s.conversationTemplates)
	r.With(reply).Post("/reply", s.replyByHand)
	r.With(reply).Post("/pause", s.pauseCustomer)
	r.With(reply).Post("/resume", s.resumeCustomer)
	r.With(read, s.need(PermRunStart)).Post("/test", s.startTestTurn)
	r.With(read).Get("/test/{run}", s.getTestTurn)
	r.With(read, s.need(PermRunStart)).Delete("/test", s.resetTestChat)
}

// assistantView is a workflow that answers customers on a channel.
type assistantView struct {
	Workflow    uuid.UUID `json:"workflow"`
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
	Connector   string    `json:"connector"`
	Where       string    `json:"where"`
	Channel     string    `json:"channel"`
}

// assistantsOf are the business's guarded workflows, by channel.
func assistantsOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]assistantView, error) {
	rows, err := tx.Query(ctx, `SELECT t.workflow_id, w.name, t.environment, t.connector, COALESCE(t.connection, '')
		FROM triggers t JOIN workflows w ON w.id = t.workflow_id
		WHERE t.tenant_id = $1 AND t.type = 'connector_event' AND t.guard IS NOT NULL ORDER BY w.name, t.environment = 'prod' DESC, t.environment`, tenant)
	if err != nil {
		return nil, err
	}
	var out []assistantView
	for rows.Next() {
		var a assistantView
		var conn string
		if err := rows.Scan(&a.Workflow, &a.Name, &a.Environment, &a.Connector, &conn); err != nil {
			rows.Close()
			return nil, err
		}
		a.Channel = conn // resolved below
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, a := range out {
		name := a.Channel
		if name == "" {
			// The only active connection, as the guard keys it.
			id, _, _ := strings.Cut(a.Connector, "@")
			var names []string
			rs, err := tx.Query(ctx, `SELECT name FROM connections WHERE tenant_id = $1 AND environment = $2 AND connector = $3 AND status = 'active' LIMIT 2`, tenant, a.Environment, id)
			if err != nil {
				return nil, err
			}
			for rs.Next() {
				var n string
				_ = rs.Scan(&n)
				names = append(names, n)
			}
			rs.Close()
			if len(names) == 1 {
				name = names[0]
			}
		}
		out[i].Channel = a.Connector + "/" + name + "/" + a.Environment
		out[i].Where = channelWhere(a.Connector)
	}
	return out, nil
}

// customerView is a customer of an assistant.
type customerView struct {
	Channel    string     `json:"channel"`
	Customer   string     `json:"customer"`
	Hint       string     `json:"hint"`
	Number     string     `json:"number,omitempty"` // revealed only
	State      string     `json:"state"`            // assistant, waiting (a person answers), stopped
	FirstAt    time.Time  `json:"first_at"`
	LastAt     time.Time  `json:"last_at"`
	LastRole   string     `json:"last_role,omitempty"`
	Until      *time.Time `json:"waiting_until,omitempty"`
	WindowOpen bool       `json:"window_open"`
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	only := r.URL.Query().Get("channel")
	out := map[string]any{"assistants": []assistantView{}, "customers": []customerView{}}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		as, err := assistantsOf(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		out["assistants"] = nonNil(as)
		// All the business's customers (an assistant since unpublished
		// too, and the member's own test chats), or one channel's.
		var channels []string
		if only != "" {
			channels = []string{only}
		}
		// From the contact's own columns (V7): no turn read and no
		// envelope opened per customer.
		rows, err := tx.Query(ctx, `SELECT c.channel, c.subject, c.hint, c.first_at, c.last_inbound_at, c.stopped_at, c.handover_until,
			  COALESCE(c.last_turn_at, c.last_inbound_at), COALESCE(c.last_role, 'customer')
			FROM assistant_contacts c
			WHERE c.tenant_id = $1 AND ($2::text[] IS NULL OR c.channel = ANY($2)) AND (c.channel NOT LIKE 'test/%' OR c.channel LIKE $3 || '%')
			ORDER BY COALESCE(c.last_turn_at, c.last_inbound_at) DESC LIMIT 200`, p.TenantID, nilIfEmpty(channels), testChannelPrefix(p.Actor()))
		if err != nil {
			return err
		}
		type row struct {
			v    customerView
			last time.Time
			stop *time.Time
		}
		var list []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.v.Channel, &x.v.Customer, &x.v.Hint, &x.v.FirstAt, &x.last, &x.stop, &x.v.Until, &x.v.LastAt, &x.v.LastRole); err != nil {
				rows.Close()
				return err
			}
			list = append(list, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := time.Now()
		customers := make([]customerView, 0, len(list))
		for _, x := range list {
			x.v.State = "assistant"
			switch {
			case x.stop != nil:
				x.v.State = "stopped"
			case x.v.Until != nil && x.v.Until.After(now):
				x.v.State = "waiting"
			default:
				x.v.Until = nil
			}
			x.v.WindowOpen = !strings.HasPrefix(x.v.Channel, "whatsapp@") || now.Sub(x.last) < assistant.Window
			if x.v.Hint == "" {
				x.v.Hint = s.customerHint(ctx, tx, p.TenantID, x.v.Channel, x.v.Customer, nil)
			}
			customers = append(customers, x.v)
		}
		out["customers"] = customers
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// othersTest: a test chat (Talk to it here) that is another member's,
// which this member does not see.
func othersTest(channel, actor string) bool {
	return strings.HasPrefix(channel, "test/") && !strings.HasPrefix(channel, testChannelPrefix(actor))
}

func nilIfEmpty(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	return list
}

// customerHint shows a customer without who they are: a WhatsApp number's
// last four digits, or a short mark of their pseudonymous id.
func (s *Server) customerHint(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string, sealed map[string]any) string {
	if s.Vault != nil && strings.HasPrefix(channel, "whatsapp@") && sealed != nil {
		if v, err := s.Vault.OpenTx(ctx, tx, tenant, sealed); err == nil {
			if n, ok := v.(string); ok && len(n) > 4 {
				return "Number ending " + n[len(n)-4:]
			}
		}
	}
	if strings.HasPrefix(channel, "test/") {
		return "Test chat"
	}
	return channelWhere(channel) + " customer " + shortMark(subject)
}

// contactOf is a customer's row: their sender (opened) and when they last
// wrote; ErrNoContact when there is none.
func (s *Server) contactOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string) (sender string, last time.Time, sealed map[string]any, err error) {
	err = tx.QueryRow(ctx, `SELECT sender, last_inbound_at FROM assistant_contacts WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
		tenant, channel, subject).Scan(&sealed, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", last, nil, assistant.ErrNoContact
	}
	if err != nil {
		return "", last, nil, err
	}
	v, err := s.Vault.OpenTx(ctx, tx, tenant, sealed)
	if errors.Is(err, pii.ErrErased) {
		return "", last, sealed, fmt.Errorf("%w: this customer was erased", errConflict)
	}
	if err != nil {
		return "", last, sealed, err
	}
	sender, _ = v.(string)
	return sender, last, sealed, nil
}

// turnView is a turn as the page shows it.
type turnView struct {
	ID         uuid.UUID  `json:"id"`
	At         time.Time  `json:"at"`
	Role       string     `json:"role"`
	Kind       string     `json:"kind"`
	Text       string     `json:"text"`
	Transcript string     `json:"transcript,omitempty"`
	File       any        `json:"file,omitempty"`
	Status     string     `json:"status,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	RunID      *uuid.UUID `json:"run_id,omitempty"`
	Step       string     `json:"step,omitempty"`
	By         string     `json:"by,omitempty"`
	Erased     bool       `json:"erased,omitempty"`
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	channel, subject := q.Get("channel"), q.Get("customer")
	reveal := q.Get("reveal") == "true"
	if othersTest(channel, p.Actor()) {
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
		return
	}
	if reveal && !p.Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "showing numbers and emails takes "+PermPIIReveal)
		return
	}
	var before *assistant.Before
	if b := q.Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "before is a time (RFC 3339)")
			return
		}
		before = &assistant.Before{At: t}
		if id := q.Get("before_id"); id != "" {
			if before.ID, err = uuid.Parse(id); err != nil {
				writeErr(w, http.StatusBadRequest, "before_id is a message's id")
				return
			}
		}
	}
	out := map[string]any{}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var sealed map[string]any
		var first, last time.Time
		var stop, until *time.Time
		err := tx.QueryRow(ctx, `SELECT sender, first_at, last_inbound_at, stopped_at, handover_until FROM assistant_contacts WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
			p.TenantID, channel, subject).Scan(&sealed, &first, &last, &stop, &until)
		if errors.Is(err, pgx.ErrNoRows) {
			return assistant.ErrNoContact
		}
		if err != nil {
			return err
		}
		now := time.Now()
		state := "assistant"
		switch {
		case stop != nil:
			state = "stopped"
		case until != nil && until.After(now):
			state = "waiting"
		default:
			until = nil
		}
		c := customerView{Channel: channel, Customer: subject, State: state, FirstAt: first, LastAt: last, Until: until,
			WindowOpen: !strings.HasPrefix(channel, "whatsapp@") || now.Sub(last) < assistant.Window,
			Hint:       s.customerHint(ctx, tx, p.TenantID, channel, subject, sealed)}
		turns, err := assistant.Conversation(ctx, tx, p.TenantID, channel, subject, before, 200)
		if err != nil {
			return err
		}
		views := make([]turnView, 0, len(turns))
		for _, t := range turns {
			v := turnView{ID: t.ID, At: t.At, Role: t.Role, Kind: t.Kind, File: t.File, Status: t.Status, Reason: t.Reason, RunID: t.RunID, Step: t.Step, By: t.By}
			var erased bool
			v.Text, erased = s.openTurnText(ctx, tx, p.TenantID, t.Body, reveal)
			var e2 bool
			v.Transcript, e2 = s.openTurnText(ctx, tx, p.TenantID, t.Transcript, reveal)
			v.Erased = erased || e2
			views = append(views, v)
		}
		if reveal {
			if v, err := s.Vault.OpenTx(ctx, tx, p.TenantID, sealed); err == nil {
				c.Number, _ = v.(string)
			}
			// Revealing a customer is recorded, never what was revealed.
			if err := auditTx(r, tx, "conversation.reveal", subject, map[string]any{"channel": channel}); err != nil {
				return err
			}
		}
		out["customer"], out["turns"] = c, views
		return nil
	})
	switch {
	case errors.Is(err, assistant.ErrNoContact):
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// openTurnText opens a turn's words: with numbers, emails and identity
// numbers hidden unless revealed. erased: the customer was erased.
func (s *Server) openTurnText(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, v any, reveal bool) (string, bool) {
	env, ok := v.(map[string]any)
	if !ok || s.Vault == nil {
		return "", false
	}
	opened, err := pii.Open(ctx, s.Vault, tx, tenant, env, pii.Taint{})
	if errors.Is(err, pii.ErrErased) || err == nil && opened == pii.Erased {
		return "", true
	}
	if err != nil {
		return "", false // not readable now (the vault); not erased
	}
	text, _ := opened.(string)
	if !reveal {
		text = pii.Redact(text)
	}
	return text, false
}

// replyReq is a member's answer to a customer: text (inside WhatsApp's
// window, or on Telegram), or an approved template.
type replyReq struct {
	Channel   string `json:"channel"`
	Customer  string `json:"customer"`
	Text      string `json:"text"`
	RequestID string `json:"request_id"`
	Template  *struct {
		Name       string   `json:"name"`
		Language   string   `json:"language"`
		Parameters []string `json:"parameters"`
		Body       string   `json:"body"` // its words, to record what the customer reads
	} `json:"template"`
}

// replyByHand sends a member's answer through the channel's connection
// and records it. A request id makes it safe to press twice: the same id
// sends once.
func (s *Server) replyByHand(w http.ResponseWriter, r *http.Request) {
	var req replyReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	req.Text = strings.TrimSpace(req.Text)
	if req.Template == nil && (req.Text == "" || utf8.RuneCountInString(req.Text) > 4000) {
		writeErr(w, http.StatusBadRequest, "write a message of 1 to 4,000 characters, or choose a template")
		return
	}
	if req.Template != nil && (strings.TrimSpace(req.Template.Name) == "" || strings.TrimSpace(req.Template.Language) == "") {
		writeErr(w, http.StatusBadRequest, "choose a template")
		return
	}
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	if len(req.RequestID) > 100 {
		writeErr(w, http.StatusBadRequest, "request_id is at most 100 characters")
		return
	}
	ref, name, env, ok := splitChannel(req.Channel)
	if !ok || s.Vault == nil || ref == "test" {
		writeErr(w, http.StatusBadRequest, "no such channel")
		return
	}
	// The send outlives the request: a closed page does not leave a message
	// half sent and its record saying "sending".
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, knowledgeActorKey{}, p.Actor()) // the connection read is recorded as theirs
	var sender string
	var last time.Time
	var stopped bool
	var state string
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sender, last, _, err = s.contactOf(ctx, tx, p.TenantID, req.Channel, req.Customer); err != nil {
			return err
		}
		var tenantStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, p.TenantID).Scan(&tenantStatus); err != nil {
			return err
		}
		if tenantStatus != "active" {
			state = "suspended"
		}
		var stop, until *time.Time
		if err := tx.QueryRow(ctx, `SELECT stopped_at, handover_until FROM assistant_contacts WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
			p.TenantID, req.Channel, req.Customer).Scan(&stop, &until); err != nil {
			return err
		}
		stopped = stop != nil
		return nil
	})
	switch {
	case err != nil:
		s.failConversation(w, r, err)
		return
	case state == "suspended":
		writeErr(w, http.StatusForbidden, "your business is suspended: nothing can be sent")
		return
	case stopped:
		writeErr(w, http.StatusConflict, "they wrote STOP, so nothing is sent to them until they write START")
		return
	}
	whatsapp := strings.HasPrefix(ref, "whatsapp@")
	if whatsapp && req.Template == nil && time.Since(last) >= assistant.Window {
		writeErr(w, http.StatusConflict, "they last wrote more than 24 hours ago: WhatsApp only lets you send an approved template now")
		return
	}
	conn, creds, hc, err := s.connectionIn(ctx, p.TenantID, env, ref, name, "conversation.reply")
	if err != nil {
		writeErr(w, http.StatusConflict, "your "+channelWhere(ref)+" connection cannot be used: check it under Connections")
		return
	}
	spec := replySpec(conn)
	if spec == nil {
		writeErr(w, http.StatusConflict, "this channel cannot be answered by hand")
		return
	}
	action, input, kind, text := spec.Action, map[string]any{spec.To: sender, spec.Text: req.Text}, "text", req.Text
	if req.Template != nil {
		if spec.Template == "" {
			writeErr(w, http.StatusBadRequest, "this channel has no templates")
			return
		}
		params := make([]any, len(req.Template.Parameters))
		for i, v := range req.Template.Parameters {
			if strings.TrimSpace(v) == "" {
				writeErr(w, http.StatusBadRequest, "fill in every blank of the template")
				return
			}
			params[i] = map[string]any{"type": "text", "text": v}
		}
		input = map[string]any{"to": sender, "name": req.Template.Name, "language": req.Template.Language}
		if len(params) > 0 {
			input["components"] = []any{map[string]any{"type": "body", "parameters": params}}
		}
		action, kind = spec.Template, "template"
		// What the customer reads: the template's words, filled in, when the
		// page sent them; else its name.
		text = req.Template.Name
		if b := strings.TrimSpace(req.Template.Body); b != "" {
			for i, v := range req.Template.Parameters {
				b = strings.ReplaceAll(b, fmt.Sprintf("{{%d}}", i+1), v)
			}
			text = b
		}
	}
	// Recorded before it is sent: the same request is sent once. A request
	// that failed may be sent again; one in flight or whose outcome is not
	// known is not.
	sealer, _ := any(s.Vault).(assistant.Sealer)
	err = s.tx(r, func(tx pgx.Tx) error {
		// A person answering: the assistant stays quiet for them, so they
		// do not get two answers. Only once the message is to be sent: a
		// refused reply leaves the assistant as it was.
		var until *time.Time
		if err := tx.QueryRow(ctx, `SELECT handover_until FROM assistant_contacts WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
			p.TenantID, req.Channel, req.Customer).Scan(&until); err != nil {
			return err
		}
		if until == nil || !until.After(time.Now()) {
			if err := assistant.HandOver(ctx, tx, p.TenantID, req.Channel, req.Customer, sender, assistant.SenderCategory(req.Channel), 24, time.Now(), "member:"+p.Actor()); err != nil {
				return err
			}
		}
		err := assistant.RecordSent(ctx, tx, sealer, p.TenantID, req.Channel, req.Customer, assistant.Sent{Role: assistant.RoleMember, Kind: kind, Text: text,
			By: p.Actor(), RequestID: req.RequestID, Status: "sending"})
		if !errors.Is(err, assistant.ErrDuplicate) {
			return err
		}
		var was, wasChannel, wasCustomer string
		var wasBody any
		if err := tx.QueryRow(ctx, `SELECT COALESCE(status, ''), channel, subject, body FROM assistant_turns WHERE tenant_id = $1 AND request_id = $2`,
			p.TenantID, req.RequestID).Scan(&was, &wasChannel, &wasCustomer, &wasBody); err != nil {
			return err
		}
		if wasChannel != req.Channel || wasCustomer != req.Customer {
			return errRequestReused
		}
		if words, _ := s.openTurnText(ctx, tx, p.TenantID, wasBody, true); words != text {
			return errRequestReused // the same id for other words: another message
		}
		if was != "failed" {
			return &replyState{status: was}
		}
		tag, err := tx.Exec(ctx, `UPDATE assistant_turns SET status = 'sending', at = now() WHERE tenant_id = $1 AND request_id = $2 AND status = 'failed'`, p.TenantID, req.RequestID)
		if err == nil && tag.RowsAffected() == 0 {
			return &replyState{status: "sending"}
		}
		return err
	})
	var rs *replyState
	switch {
	case errors.As(err, &rs):
		switch rs.status {
		case "sent":
			writeJSON(w, http.StatusOK, map[string]any{"sent": true, "duplicate": true})
		case "unknown":
			writeErr(w, http.StatusConflict, channelWhere(ref)+" did not say whether this was sent: check with the customer before sending it again (write it again to send a new message)")
		default:
			writeErr(w, http.StatusConflict, "this message is being sent: wait a moment")
		}
		return
	case errors.Is(err, errRequestReused):
		writeErr(w, http.StatusConflict, "request_id was used for another message")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	act, ok := conn.Actions[action]
	status := "sent"
	var sendErr error
	if !ok {
		sendErr = errors.New("the connector cannot send this")
	} else {
		_, sendErr = act.Execute(ctx, connector.Request{Input: input, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	}
	if sendErr != nil {
		status = "failed"
		if effects.Classify(sendErr) == effects.KindUnknownOutcome {
			status = "unknown"
		}
	}
	if err := s.tx0(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE assistant_turns SET status = $3 WHERE tenant_id = $1 AND request_id = $2`, p.TenantID, req.RequestID, status); err != nil {
			return err
		}
		return auditTx(r, tx, "conversation.reply", req.Customer, map[string]any{"channel": req.Channel, "kind": kind, "status": status})
	}); err != nil {
		s.Logger.Warn("conversations: a reply's outcome was not recorded", "tenant", p.TenantID, "status", status, "err", err)
	}
	switch status {
	case "sent":
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
	case "unknown":
		writeErr(w, http.StatusBadGateway, channelWhere(ref)+" did not say whether it was sent: check with the customer before sending it again")
	default:
		msg := "it was not sent: " + channelWhere(ref) + " refused it. Try again, or check your connection under Connections"
		var coded *connector.CodedError
		if errors.As(sendErr, &coded) && coded.Code == "window_closed" {
			msg = "it was not sent: they last wrote more than 24 hours ago, so WhatsApp only takes an approved template"
		}
		s.Logger.Info("conversations: a reply was refused", "tenant", p.TenantID, "err", pii.Redact(sendErr.Error()))
		writeErr(w, http.StatusBadGateway, msg)
	}
}

// replyState is the state of a request sent before (the same request_id).
type replyState struct{ status string }

func (e *replyState) Error() string { return "already " + e.status }

var errRequestReused = errors.New("request id reused")

// replySpec is the connector's way to answer a customer by hand.
func replySpec(conn *connector.Connector) *connector.ReplySpec {
	for _, t := range conn.Manifest.Triggers {
		if t.Conversation != nil && t.Conversation.Reply != nil {
			return t.Conversation.Reply
		}
	}
	return nil
}

// splitChannel reads <connector ref>/<connection>/<environment>.
func splitChannel(channel string) (ref, name, env string, ok bool) {
	parts := strings.Split(channel, "/")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func (s *Server) failConversation(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, assistant.ErrNoContact) {
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
		return
	}
	s.fail(w, r, err)
}

// conversationTemplates lists the channel's approved templates (WhatsApp),
// for answering after the 24-hour window.
func (s *Server) conversationTemplates(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ref, name, env, ok := splitChannel(r.URL.Query().Get("channel"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "no such channel")
		return
	}
	conn, creds, hc, err := s.connectionIn(r.Context(), p.TenantID, env, ref, name, "conversation.templates")
	if err != nil {
		writeErr(w, http.StatusConflict, "your "+channelWhere(ref)+" connection cannot be used: check it under Connections")
		return
	}
	spec := replySpec(conn)
	if spec == nil || spec.Templates == "" || conn.Actions[spec.Templates] == nil {
		writeJSON(w, http.StatusOK, map[string]any{"templates": []any{}})
		return
	}
	resp, err := conn.Actions[spec.Templates].Execute(r.Context(), connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		s.Logger.Info("conversations: templates not read", "tenant", p.TenantID, "err", pii.Redact(err.Error()))
		writeErr(w, http.StatusBadGateway, "your templates could not be read from "+channelWhere(ref)+": try again in a moment")
		return
	}
	out, _ := resp.Output.(map[string]any)
	var list []any
	if ts, ok := out["templates"].([]any); ok {
		for _, x := range ts {
			t, _ := x.(map[string]any)
			if t == nil || (t["status"] != nil && fmt.Sprint(t["status"]) != "APPROVED") {
				continue
			}
			list = append(list, map[string]any{"name": t["name"], "language": t["language"], "category": t["category"], "body": templateBody(t)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": nonNil(list)})
}

// templateBody is a template's body text, with its {{1}} placeholders.
func templateBody(t map[string]any) string {
	comps, _ := t["components"].([]any)
	for _, c := range comps {
		m, _ := c.(map[string]any)
		if strings.EqualFold(fmt.Sprint(m["type"]), "body") {
			s, _ := m["text"].(string)
			return s
		}
	}
	return ""
}

// pauseCustomer stops the assistant for one customer while a member
// answers them (no alert: the member is answering).
func (s *Server) pauseCustomer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel  string `json:"channel"`
		Customer string `json:"customer"`
		Hours    int    `json:"hours"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if othersTest(req.Channel, p.Actor()) {
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
		return
	}
	switch {
	case req.Hours <= 0:
		req.Hours = 24
	case req.Hours > assistant.MaxHandoverHours:
		req.Hours = assistant.MaxHandoverHours
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		sender, _, _, err := s.contactOf(ctx, tx, p.TenantID, req.Channel, req.Customer)
		if err != nil {
			return err
		}
		if err := assistant.HandOver(ctx, tx, p.TenantID, req.Channel, req.Customer, sender, assistant.SenderCategory(req.Channel), req.Hours, time.Now(), "member:"+p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "conversation.pause", req.Customer, map[string]any{"channel": req.Channel, "hours": req.Hours})
	})
	if err != nil {
		s.failConversation(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resumeCustomer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel  string `json:"channel"`
		Customer string `json:"customer"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if othersTest(req.Channel, p.Actor()) {
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
		return
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := assistant.Resume(r.Context(), tx, p.TenantID, req.Channel, req.Customer); err != nil {
			return err
		}
		return auditTx(r, tx, "assistant.handover.resume", req.Customer, map[string]any{"channel": req.Channel})
	})
	if err != nil {
		s.failConversation(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// conversationMetrics is an assistant's month (E5): messages, answers,
// how fast, held replies, hand-overs, by hand, failed runs, and what the
// business's allowances have left.
func (s *Server) conversationMetrics(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	channel := r.URL.Query().Get("channel")
	if othersTest(channel, p.Actor()) {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	out := map[string]any{}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var customers, in, answers, held, byHand, handovers int
		var median, p90 *float64
		if err := tx.QueryRow(ctx, `WITH m AS (SELECT * FROM assistant_turns WHERE tenant_id = $1 AND channel = $2 AND at >= date_trunc('month', now()))
			SELECT (SELECT count(DISTINCT subject) FROM m WHERE role = 'customer'),
			       (SELECT count(*) FROM m WHERE role = 'customer'),
			       (SELECT count(*) FROM m WHERE role = 'assistant'),
			       (SELECT count(*) FROM m WHERE role = 'held'),
			       (SELECT count(*) FROM m WHERE role = 'member' AND status = 'sent'),
			       (SELECT count(DISTINCT subject) FROM m WHERE role = 'customer' AND reason = 'handover')`, p.TenantID, channel).
			Scan(&customers, &in, &answers, &held, &byHand, &handovers); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY secs), percentile_cont(0.9) WITHIN GROUP (ORDER BY secs) FROM (
			SELECT EXTRACT(EPOCH FROM min(a.at) - c.at) AS secs FROM assistant_turns c
			JOIN assistant_turns a ON a.tenant_id = c.tenant_id AND a.run_id = c.run_id AND a.role = 'assistant'
			WHERE c.tenant_id = $1 AND c.channel = $2 AND c.role = 'customer' AND c.at >= date_trunc('month', now()) GROUP BY c.id, c.at) x`, p.TenantID, channel).
			Scan(&median, &p90); err != nil {
			return err
		}
		var failed int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT r.id) FROM assistant_turns c JOIN runs r ON r.id = c.run_id
			WHERE c.tenant_id = $1 AND c.channel = $2 AND c.role = 'customer' AND c.at >= date_trunc('month', now()) AND r.status IN ('failed', 'cancelled')`, p.TenantID, channel).Scan(&failed); err != nil {
			return err
		}
		out["month"] = map[string]any{"customers": customers, "messages": in, "answers": answers, "held": held, "by_hand": byHand,
			"handovers": handovers, "failed": failed, "reply_seconds_median": median, "reply_seconds_p90": p90}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out["allowances"] = s.allowancesUsed(r.Context(), p.TenantID)
	writeJSON(w, http.StatusOK, out)
}

// allowancesUsed is the share of this month's AI-step and voice
// allowances the business has used (all its assistants together), in
// percent; nil for one the plan does not limit.
func (s *Server) allowancesUsed(ctx context.Context, tenant uuid.UUID) map[string]any {
	out := map[string]any{"ai_answers": nil, "voice": nil}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return out
	}
	_ = s.tx0(ctx, tenant, func(tx pgx.Tx) error {
		if lim.AIStepMonthlyTokens > 0 {
			if used, err := runtime.AIStepTokensThisMonth(ctx, tx, tenant); err == nil {
				out["ai_answers"] = min(100, used*100/lim.AIStepMonthlyTokens)
			}
		}
		if lim.SpeechSecondsMonthly > 0 {
			if u, err := speech.UsageThisMonth(ctx, tx, tenant); err == nil {
				out["voice"] = min(100, u.Seconds()*100/lim.SpeechSecondsMonthly)
			}
		}
		return nil
	})
	return out
}

// RunConversationExpiry deletes, every hour, what was said before each
// business's conversation days (capped by its plan), until ctx ends.
func (s *Server) RunConversationExpiry(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := s.ExpireConversations(ctx); err != nil {
			s.Logger.Warn("conversations: expiry did not finish", "err", err)
		} else if n > 0 {
			s.Logger.Info("conversations: turns expired", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ExpireConversations deletes the turns past their business's days (its
// own, capped by its plan), a page of businesses at a time, and answers how
// many went.
func (s *Server) ExpireConversations(ctx context.Context) (int, error) {
	total, after := 0, uuid.Nil
	for {
		rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, days FROM taskiem_conversation_turns_due($1, 200)`, after)
		if err != nil {
			return total, err
		}
		type due struct {
			tenant uuid.UUID
			days   int
		}
		var list []due
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.tenant, &d.days); err != nil {
				rows.Close()
				return total, err
			}
			list = append(list, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		for _, d := range list {
			after = d.tenant
			days := min(d.days, s.maxConversationDays(ctx, d.tenant))
			err := s.tx0(ctx, d.tenant, func(tx pgx.Tx) error {
				n, err := assistant.ExpireTurns(ctx, tx, d.tenant, days)
				total += n
				return err
			})
			if err != nil {
				// One business failing does not hold back the others'
				// deletions: it is tried again next hour.
				s.Logger.Warn("conversations: a business's turns did not expire", "tenant", d.tenant, "err", err)
			}
		}
		if len(list) < 200 || ctx.Err() != nil {
			return total, nil
		}
	}
}

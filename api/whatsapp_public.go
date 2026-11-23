package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Public self-service menus (spec 11.2), now customers' tools (made tools
// T5, tools_customers.go). A tenant with its own number may offer a short
// menu to anyone who writes to it without a bound account: its customers'
// tools (those it deploys in prod for customers, and the entries of its
// public menu carried over); inputs in a WhatsApp form (never field by
// field), checked against the schema; an explicit yes, whatever the tool's
// care; then a run started by the actor "whatsapp_public:<number>", and,
// for a tool with an answer, the answer from its public outputs when the
// run ends (customerToolsNotify). No sign-in means no roles: the menu can
// do nothing but start those tools, under the tenant's plan limits and
// strict per-number and per-tenant rate limits. It is never offered on the
// shared number.

const waPublicTTL = 30 * time.Minute

type pubData struct {
	Workflow uuid.UUID      `json:"workflow"`
	Version  int            `json:"version"`
	Label    string         `json:"label"`
	Flow     string         `json:"flow,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty"` // personal fields sealed
	Nonce    string         `json:"nonce,omitempty"`
	// Tool is the customers' tool chosen, by its command.
	Tool string `json:"tool,omitempty"`
	// Given are the values a button on an answer carried (personal ones
	// sealed): the form's own answers come first.
	Given map[string]any `json:"given,omitempty"`
}

type pubSession struct {
	state string // "" (none), collecting_input, awaiting_confirmation
	data  pubData
}

// waPublic answers an unbound number on a tenant's own number; false when
// the tenant offers no menu.
func (s *Server) waPublic(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) (bool, error) {
	tenant := wa.Tenant
	var on bool
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT public_menu FROM whatsapp_numbers WHERE tenant_id = $1 AND phone_number_id = $2 AND status = 'active'`,
			tenant, wa.Config.PhoneNumberID).Scan(&on)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || !on {
		return false, err
	}
	// Strict limits: a few messages per number, and a ceiling for the
	// tenant's whole menu, before any tool is looked at. Beyond them
	// messages are dropped unanswered.
	key := tenant.String() + "/" + number
	if !s.limiter("wa-pub:"+key, 10*time.Second, 6).Allow() || !s.limiter("wa-pub-tenant:"+tenant.String(), 200*time.Millisecond, 50).Allow() {
		return true, nil
	}
	var items []toolRow
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		items, err = s.customerToolsFit(ctx, tx, tenant)
		return err
	}); err != nil || len(items) == 0 {
		return false, err
	}
	say := func(text string, buttons ...whatsapp.Button) error {
		_, err := wa.Send(ctx, number, whatsapp.Message{Text: text, Buttons: buttons})
		return err
	}
	sess, err := s.pubLoad(ctx, tenant, number)
	if err != nil {
		return true, err
	}
	if in.Flow != "" {
		return true, s.waPublicFlowDone(ctx, wa, number, in, sess)
	}
	// A button on an answer the customer was sent: the tool it opens.
	if token, ok := strings.CutPrefix(in.Reply, buttonReplyPrefix); ok {
		return true, s.waPublicButton(ctx, wa, number, token, say)
	}
	// A row of a list answer the customer was sent.
	if ref, ok := strings.CutPrefix(in.Reply, rowReplyPrefix); ok {
		return true, s.waPublicRow(ctx, wa, number, ref, say)
	}
	// A tapped Yes is for the summary it was shown under.
	if yes, stale := staleYes(in.Reply, pubAsk(sess.data)); yes {
		if stale || sess.state != stateConfirming {
			return true, say(tr(s.chooseLang(ctx, tenant, number, "").Tag, "wa.yes_stale_public"))
		}
		in.Reply = "yes"
	}
	text := in.Text
	if in.Reply != "" {
		text = in.Reply
	}
	cmd := normalizeCommand(text)
	lc := s.chooseLang(ctx, tenant, number, text)
	l := lc.Tag
	if m, ok := lang.MatchIntent(text, lc.Match()); ok {
		switch m.Intent {
		case lang.IntentCancel:
			cmd = "cancel"
		case lang.IntentYes:
			cmd = "yes"
		case lang.IntentNo:
			cmd = "no"
		}
	}
	if cmd == "cancel" || cmd == "stop" {
		if err := s.pubSave(ctx, tenant, number, "", pubData{}); err != nil {
			return true, err
		}
		return true, say(tr(l, "wa.public.cancelled"))
	}
	if sess.state == stateConfirming {
		switch cmd {
		case "yes", "y", "confirm":
			return true, s.waPublicStart(ctx, wa, number, sess.data, say)
		case "no", "n":
			if err := s.pubSave(ctx, tenant, number, "", pubData{}); err != nil {
				return true, err
			}
			return true, say(tr(l, "wa.cancelled_nothing_started"))
		}
		return true, say(tr(l, "wa.public.confirm_reminder"), pubConfirmButtons(l, sess.data)...)
	}
	if n, err := strconv.Atoi(cmd); err == nil && n >= 1 && n <= len(items) {
		return true, s.waPublicChoose(ctx, wa, number, items[n-1], nil, say)
	}
	lines := make([]string, len(items))
	for i, x := range items {
		lines[i] = fmt.Sprintf("%d. %s", i+1, whatsapp.SafeText(x.Name))
	}
	if sess.state == "" {
		// A menu shown: the number chosen from it comes back here.
		if err := s.pubSave(ctx, tenant, number, stateMenu, pubData{}); err != nil {
			return true, err
		}
	}
	return true, say(tr(l, "wa.public.menu", "list", strings.Join(lines, "\n")))
}

// stateMenu is a public menu shown, its choice not yet made.
const stateMenu = "menu"

// waPublicAsked reports whether a customer's message on the business's
// own number is for its public menu: the word menu, a choice from a menu
// shown (a number, or cancel), or a step of a workflow under way. All
// else is the customer's conversation with the business.
func (s *Server) waPublicAsked(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) bool {
	if in.Flow != "" || strings.HasPrefix(in.Reply, buttonReplyPrefix) || strings.HasPrefix(in.Reply, rowReplyPrefix) {
		return true
	}
	text := in.Text
	if in.Reply != "" {
		text = in.Reply
	}
	cmd := normalizeCommand(text)
	if cmd == "menu" {
		return true
	}
	sess, err := s.pubLoad(ctx, wa.Tenant, number)
	if err != nil || sess.state == "" {
		return false
	}
	if sess.state != stateMenu {
		return true
	}
	if _, err := strconv.Atoi(cmd); err == nil {
		return true
	}
	return cmd == "cancel"
}

func (s *Server) pubLoad(ctx context.Context, tenant uuid.UUID, number string) (pubSession, error) {
	var sess pubSession
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT state, data FROM whatsapp_public_sessions WHERE tenant_id = $1 AND number = $2 AND expires_at > now()`, tenant, number).
			Scan(&sess.state, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &sess.data)
	})
	return sess, err
}

func (s *Server) pubSave(ctx context.Context, tenant uuid.UUID, number, state string, data pubData) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if state == "" {
			_, err := tx.Exec(ctx, `DELETE FROM whatsapp_public_sessions WHERE tenant_id = $1 AND number = $2`, tenant, number)
			return err
		}
		raw, _ := json.Marshal(data)
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_public_sessions (tenant_id, number, state, data, expires_at) VALUES ($1, $2, $3, $4, now() + $5::interval)
			ON CONFLICT (tenant_id, number) DO UPDATE SET state = EXCLUDED.state, data = EXCLUDED.data, expires_at = EXCLUDED.expires_at, updated_at = now()`,
			tenant, number, state, raw, waPublicTTL.String())
		return err
	})
}

// waPublicChoose starts the chosen customers' tool: its form, or straight
// to the confirmation when it takes no inputs or a button gave them all.
// given are a button's values.
func (s *Server) waPublicChoose(ctx context.Context, wa *whatsapp.Platform, number string, item toolRow, given map[string]any, say func(string, ...whatsapp.Button) error) error {
	tenant := wa.Tenant
	l := s.chooseLang(ctx, tenant, number, "").Tag
	label := item.Name
	var fields []whatsapp.Field
	data := pubData{Workflow: item.Workflow, Version: item.Version, Label: label, Tool: item.Command}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, d, err := s.customerDefinition(ctx, tx, tenant, item)
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if d.RawInputs != nil {
			raw = d.RawInputs.Schema
		}
		if fields, err = whatsapp.InputFields(raw, d.RawTypes); err != nil {
			return err
		}
		// A button's values, for the fields the form has; personal ones
		// wait sealed, as the form's answers do.
		for _, f := range fields {
			v, ok := given[f.Name]
			if !ok {
				continue
			}
			if f.PII != "" {
				env, err := s.Store.PII.SealTx(ctx, tx, tenant, f.PII, v)
				if err != nil {
					return err
				}
				v = env
			}
			if data.Given == nil {
				data.Given = map[string]any{}
			}
			data.Given[f.Name] = v
		}
		return nil
	})
	switch {
	case errors.Is(err, errCustomerMoney), errors.Is(err, errCustomerRoles):
		return say(tr(l, "wa.public.not_offered", "label", label))
	case errors.Is(err, errCustomerForm), errors.Is(err, whatsapp.ErrInputsUnsupported):
		return say(tr(l, "wa.public.not_here", "label", label))
	case err != nil:
		return err
	}
	if len(data.Given) == len(fields) {
		data.Inputs, data.Given = data.Given, nil
		return s.waPublicConfirm(ctx, wa, number, data, say)
	}
	form, err := whatsapp.FlowForm(label, fields, nil)
	if wa.Config.FlowKey == nil || errors.Is(err, whatsapp.ErrFlowUnsupported) {
		return say(tr(l, "wa.public.not_here", "label", label))
	}
	if err != nil {
		return err
	}
	tok, hash := whatsapp.NewFlowToken(tenant)
	meta, _ := json.Marshal(map[string]any{"title": label})
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, workflow_id, version, data, expires_at)
			VALUES ($1, $2, 'public', $3, $4, $5, $6, $7, now() + $8::interval)`, hash, tenant, number, wa.Config.PhoneNumberID, item.Workflow, item.Version, meta, waPublicTTL.String())
		return err
	}); err != nil {
		return err
	}
	data.Flow = hex.EncodeToString(hash)
	if err := s.pubSave(ctx, tenant, number, stateCollecting, data); err != nil {
		return err
	}
	if _, err := wa.Client.SendFlow(ctx, number, whatsapp.FlowMessage{Flow: whatsapp.FlowInputs, Token: tok, CTA: tr(l, "button.fill_in"), Screen: whatsapp.ScreenInputs, Data: form,
		Body: tr(l, "wa.public.form", "label", label, "cta", tr(l, "button.fill_in"))}); err != nil {
		s.Logger.Warn("whatsapp: sending a public form", "err", err)
		s.waDropFlow(ctx, tenant, data.Flow)
		return say(tr(l, "wa.public.form_failed"))
	}
	return nil
}

// waPublicFlowDone takes a completed public form to the confirmation.
func (s *Server) waPublicFlowDone(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound, sess pubSession) error {
	say := func(text string, buttons ...whatsapp.Button) error {
		_, err := wa.Send(ctx, number, whatsapp.Message{Text: text, Buttons: buttons})
		return err
	}
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	tenant, hash, err := whatsapp.ParseFlowToken(in.FlowToken())
	if err != nil || tenant != wa.Tenant || sess.state != stateCollecting || sess.data.Flow != hex.EncodeToString(hash) {
		return say(tr(l, "wa.public.form_gone"))
	}
	var row flowRow
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		row, err = loadFlow(ctx, tx, tenant, hash, true)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (row.kind != "public" || row.number != number || row.status != "submitted") {
		return say(tr(l, "wa.public.form_gone"))
	}
	if err != nil {
		return err
	}
	data := sess.data
	data.Inputs, _ = row.data["inputs"].(map[string]any)
	// A button's values fill what the form left blank.
	for k, v := range data.Given {
		if _, ok := data.Inputs[k]; !ok {
			if data.Inputs == nil {
				data.Inputs = map[string]any{}
			}
			data.Inputs[k] = v
		}
	}
	data.Flow, data.Given = "", nil
	if err := s.setFlow(ctx, row, "done", map[string]any{}); err != nil {
		return err
	}
	return s.waPublicConfirm(ctx, wa, number, data, say)
}

// pubAsk names a public session's summary for its Yes button: the start
// of the nonce made when the summary is shown.
func pubAsk(d pubData) string {
	if len(d.Nonce) < 12 {
		return ""
	}
	return d.Nonce[:12]
}

// pubConfirmButtons are Yes and Cancel under a public menu's summary.
func pubConfirmButtons(l lang.Tag, d pubData) []whatsapp.Button {
	bs := confirmButtonsIn(l)
	if ask := pubAsk(d); ask != "" {
		bs[0].ID = yesPrefix + ask
	}
	return bs
}

func (s *Server) waPublicConfirm(ctx context.Context, wa *whatsapp.Platform, number string, data pubData, say func(string, ...whatsapp.Button) error) error {
	data.Nonce = uuid.NewString()
	if err := s.pubSave(ctx, wa.Tenant, number, stateConfirming, data); err != nil {
		return err
	}
	lines, err := s.maskedLines(ctx, wa.Tenant, data.Inputs, 12)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	msg := tr(l, "wa.public.confirm", "label", data.Label)
	if len(lines) > 0 {
		msg = tr(l, "wa.public.confirm_inputs", "label", data.Label, "inputs", strings.Join(lines, "\n"))
	}
	return say(msg, pubConfirmButtons(l, data)...)
}

// waPublicStart starts the confirmed customers' tool as
// whatsapp_public:<number>: the tool as deployed now, still the one shown
// and fit for a customer. A tool with an answer sends it back here when
// the run ends.
func (s *Server) waPublicStart(ctx context.Context, wa *whatsapp.Platform, number string, data pubData, say func(string, ...whatsapp.Button) error) error {
	tenant := wa.Tenant
	l := s.chooseLang(ctx, tenant, number, "").Tag
	if err := s.pubSave(ctx, tenant, number, "", pubData{}); err != nil {
		return err
	}
	if !s.limiter("wa-pub-run:"+tenant.String()+"/"+number, 20*time.Minute, 3).Allow() || !s.limiter("wa-pub-run-tenant:"+tenant.String(), 6*time.Second, 30).Allow() {
		return say(tr(l, "wa.public.too_many"))
	}
	var input any
	var def []byte
	var t toolRow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tools, err := customerToolsTx(ctx, tx)
		if err != nil {
			return err
		}
		found := false
		for _, x := range tools {
			// A session from before tools named only its workflow.
			if x.Workflow == data.Workflow && (data.Tool == "" || x.Command == data.Tool) {
				t, found = x, true
			}
		}
		if !found || t.Version != data.Version {
			return pgx.ErrNoRows
		}
		if def, _, err = s.customerDefinition(ctx, tx, tenant, t); err != nil {
			return err
		}
		input, err = pii.Open(ctx, s.Store.PII, tx, tenant, data.Inputs, pii.Taint{})
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return say(tr(l, "wa.public.changed", "label", data.Label))
	case errors.Is(err, errCustomerMoney), errors.Is(err, errCustomerRoles):
		return say(tr(l, "wa.public.not_offered", "label", data.Label))
	case errors.Is(err, errCustomerForm):
		return say(tr(l, "wa.public.not_here", "label", data.Label))
	case err != nil:
		return err
	}
	in, _ := input.(map[string]any)
	run, created, err := s.startForCustomer(ctx, tenant, customerStart{tool: t, def: def, input: in, actor: customerWAActor(number),
		shown: customerWAActor(whatsapp.MaskNumber(number)), kind: "system", channel: "whatsapp_public", dedup: data.Nonce})
	var bad toolInputError
	if errors.As(err, &bad) {
		return say(tr(l, "wa.public.bad_details", "problems", whatsapp.SafeText(strings.Join(bad, "; "))))
	}
	if le, ok := runtime.IsLimit(err); ok {
		s.Logger.Info("whatsapp: a public run refused by a plan limit", "limit", le.Limit)
		return say(tr(l, "wa.public.busy"))
	}
	if err != nil {
		return err
	}
	reference := strings.ToUpper(run.String()[:8])
	if t.AnswerType == "" {
		return say(tr(l, "wa.public.started", "label", data.Label, "reference", reference))
	}
	if created {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO customer_tool_watches (tenant_id, run_id, phone_number_id, number, label) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
				tenant, run, wa.Config.PhoneNumberID, number, data.Label)
			return err
		}); err != nil {
			return err
		}
	}
	return say(tr(l, "wa.public.working", "label", data.Label, "reference", reference))
}

// waPublicButton is a button on an answer a customer was sent, pressed:
// the customers' tool it opens, its form, then yes, as a choice from the
// menu is. Only the customer the answer went to may press it.
func (s *Server) waPublicButton(ctx context.Context, wa *whatsapp.Platform, number, token string, say func(string, ...whatsapp.Button) error) error {
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	id, err := uuid.Parse(token)
	if err != nil {
		return say(tr(l, "wa.public.button_gone"))
	}
	var t toolRow
	var given map[string]any
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{wa.Tenant}, func(tx pgx.Tx) error {
		var err error
		t, given, err = pressCustomerButton(ctx, tx, id, customerWAActor(number))
		return err
	})
	if errors.Is(err, errNotFound) {
		return say(tr(l, "wa.public.button_gone"))
	}
	if err != nil {
		return err
	}
	return s.waPublicChoose(ctx, wa, number, t, given, say)
}

// customerToolsNotify sends each customer whose tool's run ended on the
// business's own number its answer there: the public outputs only, a
// card's buttons as reply buttons (customers' tools only), how a run that
// failed ended in plain words with its reference. Outside WhatsApp's
// 24-hour window, or once the business's number changed, it is dropped;
// a send that failed is tried again on the next ticks, four times.
func (s *Server) customerToolsNotify(ctx context.Context, tenant uuid.UUID) error {
	type ended struct {
		run                 uuid.UUID
		pnid, number, label string
		attempts            int
		status              string
	}
	var done []ended
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// A day on, WhatsApp's window is shut whatever happened: the
		// number is let go.
		if _, err := tx.Exec(ctx, `DELETE FROM customer_tool_watches WHERE created_at < now() - interval '1 day'`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE customer_tool_watches w SET notified_at = now() FROM runs r
			WHERE r.id = w.run_id AND w.notified_at IS NULL AND r.status IN ('completed', 'failed', 'cancelled', 'needs_reconciliation')
			RETURNING w.run_id, w.phone_number_id, w.number, w.label, w.attempts, r.status`)
		if err != nil {
			return err
		}
		done, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ended, error) {
			var e ended
			return e, r.Scan(&e.run, &e.pnid, &e.number, &e.label, &e.attempts, &e.status)
		})
		return err
	})
	if err != nil || len(done) == 0 {
		return err
	}
	var wa *whatsapp.Platform
	if s.WhatsApp != nil {
		if wa, err = s.WhatsApp.ForTenant(ctx, tenant); err != nil {
			return err
		}
	}
	var errs []error
	for _, e := range done {
		var err error
		if wa != nil && wa.Own() && wa.Config.PhoneNumberID == e.pnid {
			err = s.sendCustomerAnswer(ctx, wa, e.run, e.number, e.label, e.status)
		} // else the business's number changed: nowhere to answer
		again := err != nil && !errors.Is(err, whatsapp.ErrWindowClosed) && e.attempts < 4
		if again {
			errs = append(errs, err)
		}
		// Told, or never to be: the row and its number go. Else tried
		// again on a later tick.
		q := `DELETE FROM customer_tool_watches WHERE run_id = $1`
		if again {
			q = `UPDATE customer_tool_watches SET notified_at = NULL, attempts = attempts + 1 WHERE run_id = $1`
		}
		if rerr := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q, e.run)
			return err
		}); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	return errors.Join(errs...)
}

// sendCustomerAnswer sends one customer the answer of the run they
// started.
func (s *Server) sendCustomerAnswer(ctx context.Context, wa *whatsapp.Platform, run uuid.UUID, number, label, status string) error {
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	reference := strings.ToUpper(run.String()[:8])
	m := whatsapp.Message{Tenant: wa.Tenant, Text: tr(l, "wa.public.failed", "label", label, "reference", reference)}
	if status == "completed" {
		a, err := s.customerAnswer(ctx, wa.Tenant, run, customerWAActor(number), chatRows)
		if err != nil {
			return err
		}
		m.Text = customerAnswerWords(l, a)
		if m.Text == "" {
			m.Text = tr(l, "wa.public.done", "label", label, "reference", reference)
		}
		m.Buttons = chatButtons(a.Buttons)
		// A list whose rows have buttons: a list message; a row chosen
		// offers its buttons (waPublicRow), to this customer only.
		if rowsPressable(a) {
			m.Text, m.List = customerListHead(l, a), answerList(l, run, a)
			if m.Text == "" {
				m.Text = whatsapp.SafeText(label)
			}
		}
	}
	_, err := wa.Send(ctx, number, m)
	return err
}

// customerListHead is a customer's list answer's words over its list
// message: its title, and how many more rows there are.
func customerListHead(l lang.Tag, a madeAnswer) string {
	var lines []string
	if a.Title != "" {
		lines = append(lines, "*"+a.Title+"*")
	}
	if n := len(a.Rows) - chatRows; n > 0 {
		lines = append(lines, tr(l, "wa.public.list_more", "count", strconv.Itoa(n)))
	}
	return strings.Join(lines, "\n")
}

// waPublicRow is a row of a list answer chosen by a customer: that row's
// buttons, as reply buttons, for the customer the answer went to only,
// while they last.
func (s *Server) waPublicRow(ctx context.Context, wa *whatsapp.Platform, number, ref string, say func(string, ...whatsapp.Button) error) error {
	l := s.chooseLang(ctx, wa.Tenant, number, "").Tag
	runID, idx, _ := strings.Cut(ref, ":")
	run, err := uuid.Parse(runID)
	i, ierr := strconv.Atoi(idx)
	if err != nil || ierr != nil || i < 0 || i >= chatRows {
		return say(tr(l, "wa.public.button_gone"))
	}
	a, err := s.customerAnswer(ctx, wa.Tenant, run, customerWAActor(number), chatRows)
	if errors.Is(err, pgx.ErrNoRows) {
		return say(tr(l, "wa.tool.row_not_yours"))
	}
	if err != nil {
		return err
	}
	if a.Type != "list" || i >= len(a.Rows) {
		return say(tr(l, "wa.public.button_gone"))
	}
	r := a.Rows[i]
	bs := chatButtons(r.Actions)
	if len(bs) == 0 {
		return say(tr(l, "wa.public.button_gone"))
	}
	text := "*" + whatsapp.SafeText(r.Title) + "*"
	if r.Sub != "" {
		text += " · " + whatsapp.SafeText(r.Sub)
	}
	if r.State != nil && r.State.Text != "" {
		text += "\n" + whatsapp.SafeText(r.State.Text)
	}
	return say(text, bs...)
}

// customerAnswerWords is a customer's answer as a chat message: as a
// member's, but a long list says how many more without sending anyone to
// an app.
func customerAnswerWords(l lang.Tag, a madeAnswer) string {
	if a.Type != "list" || len(a.Rows) <= 10 {
		return toolAnswerWords(l, a)
	}
	more := len(a.Rows) - 10
	a.Rows = a.Rows[:10]
	return toolAnswerWords(l, a) + "\n" + tr(l, "wa.public.list_more", "count", strconv.Itoa(more))
}

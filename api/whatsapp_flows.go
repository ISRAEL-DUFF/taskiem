package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// WhatsApp Flows (spec 11.1, 11.2; docs/whatsapp.md#flows). Taskiem sends
// a Flow, a form inside WhatsApp, with a random flow token whose hash is
// kept in the tenant's whatsapp_flows row. Submitting the form calls the
// data endpoint (POST /channels/whatsapp/flows, or /flows/{pnid} for an
// own number on its own Meta app): signed with the app secret, encrypted
// to the operator's Flows key. The endpoint finds the row by the token,
// checks the answers (inputs against the workflow's schema; the PIN
// against its hash) and completes the Flow; WhatsApp then posts the
// completion to the webhook as an nfm_reply message from the person's
// number, and the conversation goes on there (the inputs' confirmation,
// the decision's outcome).

// HTTP statuses the Flows client understands.
const (
	flowDecryptFailed = 421 // the client fetches the public key again and retries
	flowTokenInvalid  = 427 // the form shows an error and its button is disabled
	flowSignatureBad  = 432
)

// Wrong PINs before the PIN locks, and for how long.
const (
	waPinMaxFailures = 5
	waPinWindow      = 15 * time.Minute
)

// flowRow is a Flow sent.
type flowRow struct {
	hash     []byte
	tenant   uuid.UUID
	kind     string
	number   string
	pnid     string
	user     *uuid.UUID
	workflow *uuid.UUID
	version  *int
	run      *uuid.UUID
	step     *string
	level    *int
	decision *string
	data     map[string]any
	status   string
	expires  time.Time
}

func loadFlow(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hash []byte, lock bool) (flowRow, error) {
	r := flowRow{hash: hash, tenant: tenant}
	q := `SELECT kind, number, phone_number_id, user_id, workflow_id, version, run_id, step_id, level, decision, data, status, expires_at
		FROM whatsapp_flows WHERE token_hash = $1 AND tenant_id = $2`
	if lock {
		q += ` FOR UPDATE`
	}
	var raw []byte
	err := tx.QueryRow(ctx, q, hash, tenant).Scan(&r.kind, &r.number, &r.pnid, &r.user, &r.workflow, &r.version, &r.run, &r.step, &r.level, &r.decision, &raw, &r.status, &r.expires)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(raw, &r.data)
}

func (s *Server) setFlow(ctx context.Context, row flowRow, status string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE whatsapp_flows SET status = $3, data = $4, updated_at = now() WHERE token_hash = $1 AND tenant_id = $2`, row.hash, row.tenant, status, raw)
		return err
	})
}

// waDropFlow forgets a Flow (given by its hex hash) no longer wanted.
func (s *Server) waDropFlow(ctx context.Context, tenant uuid.UUID, hexHash string) {
	h, err := hex.DecodeString(hexHash)
	if err != nil {
		return
	}
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM whatsapp_flows WHERE token_hash = $1 AND tenant_id = $2`, h, tenant)
		return err
	})
}

// --- the data endpoint ---

func (s *Server) waFlowsEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.WhatsApp == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	wa := s.WhatsApp
	if pnid := chi.URLParam(r, "pnid"); pnid != "" {
		var err error
		if wa, err = s.WhatsApp.ForPhoneNumberID(r.Context(), pnid); err != nil || wa == nil || !wa.OwnApp {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
	}
	key := wa.Config.FlowKey
	if key == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	// Signed with the app secret of the app the Flow belongs to, before
	// anything is decrypted.
	if !whatsapp.VerifySignature(wa.Config.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeErr(w, flowSignatureBad, "bad signature")
		return
	}
	req, sess, err := key.Decrypt(body)
	if err != nil {
		// A health check may arrive in the clear; it learns nothing.
		var plain whatsapp.FlowRequest
		if json.Unmarshal(body, &plain) == nil && plain.Action == "ping" {
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"status": "active"}})
			return
		}
		writeErr(w, flowDecryptFailed, "cannot decrypt")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Second)
	defer cancel()
	resp, status := s.waFlowRequest(ctx, wa, req)
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	out, err := sess.Encrypt(resp)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, out) //nolint:gosec // base64 ciphertext, as text/plain
}

// waFlowRequest answers one decrypted request: the response to encrypt,
// or a status for the client.
func (s *Server) waFlowRequest(ctx context.Context, wa *whatsapp.Platform, req whatsapp.FlowRequest) (any, int) {
	if req.Action == "ping" {
		return map[string]any{"data": map[string]any{"status": "active"}}, http.StatusOK
	}
	// An error notification: the client could not use an earlier answer.
	// Taskiem's forms never send a field named "error".
	if _, ok := req.Data["error"]; ok {
		s.Logger.Warn("whatsapp: a Flow reported an error", "error", fmt.Sprint(req.Data["error"]))
		return map[string]any{"data": map[string]any{"acknowledged": true}}, http.StatusOK
	}
	tenant, hash, err := whatsapp.ParseFlowToken(req.FlowToken)
	if err != nil {
		return nil, flowTokenInvalid
	}
	if !s.limiter("wa-flow:"+hex.EncodeToString(hash), 10*time.Second, 10).Allow() {
		return nil, http.StatusTooManyRequests
	}
	var row flowRow
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		row, err = loadFlow(ctx, tx, tenant, hash, false)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, flowTokenInvalid
	case err != nil:
		s.Logger.Error("whatsapp: a Flow request", "err", err)
		return nil, http.StatusInternalServerError
	case row.status != "open" || !time.Now().Before(row.expires):
		return nil, flowTokenInvalid // used or expired
	case row.pnid != wa.Config.PhoneNumberID:
		// Sent from another number: only one on this same app (Taskiem's
		// app serves own numbers connected through it).
		if wa.Own() {
			return nil, flowTokenInvalid
		}
		q, err := s.WhatsApp.ForPhoneNumberID(ctx, row.pnid)
		if err != nil || q == nil || q.OwnApp || q.Tenant != row.tenant {
			return nil, flowTokenInvalid
		}
	}
	if row.kind == "pin" {
		return s.waFlowPin(ctx, row, req), http.StatusOK
	}
	resp, err := s.waFlowInputs(ctx, row, req)
	if err != nil {
		s.Logger.Error("whatsapp: a Flow request", "err", err)
		return nil, http.StatusInternalServerError
	}
	return resp, http.StatusOK
}

func flowScreen(screen string, data map[string]any) map[string]any {
	return map[string]any{"screen": screen, "data": data}
}

func flowDone(token string) map[string]any {
	return map[string]any{"screen": "SUCCESS", "data": map[string]any{"extension_message_response": map[string]any{"params": map[string]any{"flow_token": token}}}}
}

// --- inputs ---

// waSendInputsFlow sends the inputs form for a workflow, when Flows are
// set up and the fields fit one; false leaves them to chat.
func (s *Server) waSendInputsFlow(ctx context.Context, c *chat, data chatData, fields []whatsapp.Field) (bool, error) {
	if c.wa.Config.FlowKey == nil {
		return false, nil
	}
	form, err := whatsapp.FlowForm(data.Name, fields, nil)
	if errors.Is(err, whatsapp.ErrFlowUnsupported) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tok, hash := whatsapp.NewFlowToken(c.tenant.ID)
	meta, _ := json.Marshal(map[string]any{"title": data.Name})
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, user_id, workflow_id, version, data, expires_at)
			VALUES ($1, $2, 'inputs', $3, $4, $5, $6, $7, $8, now() + $9::interval)`, hash, c.tenant.ID, c.number, c.wa.Config.PhoneNumberID, c.p.UserID,
			data.Workflow, data.Version, meta, waPendingTTL.String())
		return err
	}); err != nil {
		return false, err
	}
	data.Flow = hex.EncodeToString(hash)
	if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
		return false, err
	}
	_, err = c.wa.Client.SendFlow(ctx, c.number, whatsapp.FlowMessage{Flow: whatsapp.FlowInputs, Token: tok, CTA: c.t("button.fill_in"), Screen: whatsapp.ScreenInputs, Data: form,
		Body: c.wa.Prefix(c.tenant.Name) + c.t("wa.form.inputs", "workflow", data.Name, "count", strconv.Itoa(len(fields)), "cta", c.t("button.fill_in"))})
	if err != nil {
		// Not delivered (an old app, the window, Meta): ask in chat.
		s.Logger.Warn("whatsapp: sending an inputs form", "err", err)
		s.waDropFlow(ctx, c.tenant.ID, data.Flow)
		return false, nil
	}
	return true, nil
}

// waFlowInputs answers the inputs form: its screen on opening, or the
// submitted answers checked against the workflow's input schema (field by
// field, then as a whole), kept (personal ones sealed) until the person
// confirms in chat.
func (s *Server) waFlowInputs(ctx context.Context, row flowRow, req whatsapp.FlowRequest) (any, error) {
	if row.workflow == nil || row.version == nil {
		return nil, errors.New("an inputs Flow without its workflow")
	}
	title, _ := row.data["title"].(string)
	fields, err := s.waFieldsOf(ctx, row.tenant, *row.workflow, *row.version)
	if err != nil {
		return nil, err
	}
	if req.Action != "data_exchange" {
		d, err := whatsapp.FlowForm(title, fields, nil)
		if err != nil {
			return nil, err
		}
		return flowScreen(whatsapp.ScreenInputs, d), nil
	}
	vals, errs := whatsapp.FlowValues(fields, req.Data)
	general := ""
	if len(errs) == 0 {
		var def []byte
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, *row.workflow, *row.version).Scan(&def)
		}); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(vals)
		if probs := s.inputProblems(*row.workflow, *row.version, def, raw); len(probs) > 0 {
			general = whatsapp.SafeText(strings.Join(probs, "; "))
		}
	}
	if len(errs) > 0 || general != "" {
		d, err := whatsapp.FlowForm(title, fields, errs)
		if err != nil {
			return nil, err
		}
		if general != "" {
			d["error"], d["has_error"] = general, true
		}
		return flowScreen(whatsapp.ScreenInputs, d), nil
	}
	// Personal data waits sealed, as in chat.
	sealed := map[string]any{}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
		for _, f := range fields {
			v := vals[f.Name]
			if f.PII != "" {
				env, err := s.Store.PII.SealTx(ctx, tx, row.tenant, f.PII, v)
				if err != nil {
					return err
				}
				sealed[f.Name] = env
				continue
			}
			sealed[f.Name] = v
		}
		raw, _ := json.Marshal(map[string]any{"title": title, "inputs": sealed})
		_, err := tx.Exec(ctx, `UPDATE whatsapp_flows SET status = 'submitted', data = $3, updated_at = now() WHERE token_hash = $1 AND tenant_id = $2 AND status = 'open'`,
			row.hash, row.tenant, raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	return flowDone(req.FlowToken), nil
}

// waFlowCompleted handles a completed Flow arriving in the chat of a bound
// person: the inputs' confirmation, or the PIN decision's outcome.
func (s *Server) waFlowCompleted(ctx context.Context, c *chat) error {
	invalid := func() error { return c.say(ctx, s, c.t("wa.form.invalid")) }
	tenant, hash, err := whatsapp.ParseFlowToken(c.in.FlowToken())
	if err != nil {
		return invalid()
	}
	i := -1
	for j, t := range c.tenants {
		if t.ID == tenant {
			i = j
		}
	}
	if i < 0 {
		return invalid()
	}
	var row flowRow
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		row, err = loadFlow(ctx, tx, tenant, hash, true)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid()
	}
	if err != nil {
		return err
	}
	// Only the number and the person it was sent to.
	if row.number != c.number || row.user == nil || *row.user != c.p.UserID {
		return invalid()
	}
	switch row.kind {
	case "pin":
		say := func(text string) error { return s.waSay(ctx, c.wa, c.number, c.tenants[i].Name, text) }
		if out, _ := row.data["outcome"].(string); out != "" && (row.status == "done" || row.status == "refused") {
			return say(out)
		}
		return say(c.t("wa.form.not_confirmed"))
	case "inputs":
		if tenant != c.tenant.ID || c.state != stateCollecting || c.data.Flow != hex.EncodeToString(hash) || row.status != "submitted" {
			return c.say(ctx, s, c.t("wa.form.gone"))
		}
		data := c.data
		data.Inputs, _ = row.data["inputs"].(map[string]any)
		if data.Inputs == nil {
			data.Inputs = map[string]any{}
		}
		data.Flow = ""
		if err := s.setFlow(ctx, row, "done", map[string]any{}); err != nil {
			return err
		}
		return s.waAskConfirm(ctx, c, data)
	}
	return invalid()
}

// --- the PIN ---

// waSendPinFlow asks for the WhatsApp PIN in a form bound to this one
// decision, when Flows are set up and the person has a PIN that is not
// locked; false leaves step-up to the web link.
func (s *Server) waSendPinFlow(ctx context.Context, c *chat, tenant tenantRef, p *Principal, cl whatsapp.Claims) (bool, error) {
	if c.wa.Config.FlowKey == nil {
		return false, nil
	}
	pin, err := s.waPinInfo(ctx, p.UserID)
	if err != nil || !pin.Set || pin.LockedUntil != nil {
		return false, err
	}
	var wf, env string
	tok, hash := whatsapp.NewFlowToken(tenant.ID)
	verb := "Approve"
	if cl.Decision == "rejected" {
		verb = "Reject"
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT w.name, r.environment FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE r.id = $1`, cl.Run).Scan(&wf, &env); err != nil {
			return err
		}
		meta, _ := json.Marshal(map[string]any{"title": verb + " " + cl.Step, "summary": whatsapp.SafeText(wf) + " · " + env})
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, user_id, run_id, step_id, level, decision, decision_nonce, data, expires_at)
			VALUES ($1, $2, 'pin', $3, $4, $5, $6, $7, $8, $9, $10, $11, now() + $12::interval)`, hash, tenant.ID, c.number, c.wa.Config.PhoneNumberID, p.UserID,
			cl.Run, cl.Step, cl.Level, cl.Decision, cl.Nonce[:], meta, waHandoffTTL.String())
		return err
	})
	if err != nil {
		return false, err
	}
	if tenant.ID == c.tenant.ID {
		if err := s.saveSession(ctx, c, stateStepUp, chatData{Run: cl.Run, Step: cl.Step, Decision: cl.Decision}, waHandoffTTL); err != nil {
			return false, err
		}
	}
	_, err = c.wa.Client.SendFlow(ctx, c.number, whatsapp.FlowMessage{Flow: whatsapp.FlowPin, Token: tok, CTA: c.t("button.enter_pin"), Screen: whatsapp.ScreenPin,
		Data: whatsapp.PinForm(verb+" "+cl.Step, whatsapp.SafeText(wf)+" · "+env, ""),
		Body: c.wa.Prefix(tenant.Name) + c.t("wa.form.pin_"+strings.ToLower(verb), "step", cl.Step)})
	if err != nil {
		s.Logger.Warn("whatsapp: sending a PIN form", "err", err)
		s.waDropFlow(ctx, tenant.ID, hex.EncodeToString(hash))
		return false, nil
	}
	return true, nil
}

// waFlowPin answers the PIN form: its screen on opening; on submission
// the PIN is checked (wrong ones counted toward the lock), and a right one
// records the decision the form is bound to, through VoteApproval with
// step-up "whatsapp_pin": a policy needing more refuses it there.
func (s *Server) waFlowPin(ctx context.Context, row flowRow, req whatsapp.FlowRequest) any {
	title, _ := row.data["title"].(string)
	summary, _ := row.data["summary"].(string)
	screen := func(problem string) any {
		return flowScreen(whatsapp.ScreenPin, whatsapp.PinForm(title, summary, problem))
	}
	if req.Action != "data_exchange" {
		return screen("")
	}
	user := *row.user
	p, perr := s.principalOf(ctx, row.tenant, user)
	finish := func(status, outcome, problem string) any {
		if err := s.setFlow(ctx, row, status, map[string]any{"title": title, "summary": summary, "outcome": outcome}); err != nil {
			s.Logger.Error("whatsapp: closing a PIN form", "err", err)
		}
		if status == "done" {
			return flowDone(req.FlowToken)
		}
		return screen(problem)
	}
	audit := func(action string, detail map[string]any) {
		if p == nil {
			return
		}
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
			return waAudit(ctx, tx, p, action, row.run.String()+"/"+*row.step, detail)
		})
	}
	if perr != nil || !p.Can(PermApprovalDecide) {
		const m = "You cannot decide approvals in this organisation any more."
		return finish("refused", m, m)
	}
	var hash string
	var locked *time.Time
	err := s.Store.Pool.QueryRow(ctx, `SELECT pin_hash, locked_until FROM taskiem_wa_pin_state($1)`, user).Scan(&hash, &locked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		const m = "You have no WhatsApp PIN set. Decide in Taskiem instead."
		return finish("refused", m, m)
	case err != nil:
		s.Logger.Error("whatsapp: reading a PIN", "err", err)
		return screen("Something went wrong; try again.")
	case locked != nil:
		m := "Your PIN is locked after too many wrong tries, until " + locked.UTC().Format("15:04") + " UTC. Decide in Taskiem instead."
		return finish("refused", m, m)
	}
	pin, _ := req.Data["pin"].(string)
	ok := validPin(pin) && checkPassword(hash, pin)
	var failures int
	var nowLocked bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT failures, locked FROM taskiem_wa_pin_result($1, $2, $3, $4::interval)`, user, ok, waPinMaxFailures, waPinWindow.String()).
		Scan(&failures, &nowLocked); err != nil {
		s.Logger.Error("whatsapp: recording a PIN check", "err", err)
		return screen("Something went wrong; try again.")
	}
	if !ok {
		audit("whatsapp.pin.fail", map[string]any{"failures": failures})
		if nowLocked {
			audit("whatsapp.pin.locked", map[string]any{"minutes": int(waPinWindow.Minutes())})
			m := "Too many wrong PINs: your PIN is locked for 15 minutes. Decide in Taskiem instead."
			return finish("refused", m, m)
		}
		return screen(fmt.Sprintf("That PIN is not right. %d tries left.", waPinMaxFailures-failures))
	}
	// The decision the form is bound to, if the request is still there.
	var status string
	var level int
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status, level FROM approvals WHERE run_id = $1 AND step_id = $2`, *row.run, *row.step).Scan(&status, &level)
	}); err != nil {
		s.Logger.Error("whatsapp: reading an approval", "err", err)
		return screen("Something went wrong; try again.")
	}
	if status != "open" || level != *row.level {
		m := "That request has moved on (it is " + status + " or at another level). Send *approvals* for what is waiting for you."
		return finish("refused", m, m)
	}
	res, err := s.Store.VoteApproval(ctx, runtime.RunRef{ID: *row.run, TenantID: row.tenant}, *row.step,
		runtime.Vote{UserID: user, Roles: p.Roles, Decision: *row.decision, Channel: "whatsapp", StepUp: runtime.StepUpWhatsAppPIN})
	var su *runtime.StepUpError
	switch {
	case errors.As(err, &su), errors.Is(err, runtime.ErrNotAllowed):
		audit("approval.decide.refused", map[string]any{"decision": *row.decision, "reason": err.Error(), "via": "flow_pin"})
		m := "Not recorded: " + strings.TrimSuffix(err.Error(), ": not allowed") + "."
		if su != nil {
			m = "Not recorded: this request needs your " + su.Method + "; decide it in Taskiem."
		}
		return finish("refused", m, m)
	case errors.Is(err, runtime.ErrAlreadyDecided):
		const m = "You have already decided this request."
		return finish("refused", m, m)
	case errors.Is(err, runtime.ErrApprovalClosed):
		const m = "That request is already closed."
		return finish("refused", m, m)
	case err != nil:
		s.Logger.Error("whatsapp: recording a decision", "err", err)
		return screen("Something went wrong; try again.")
	}
	audit("approval.decide", map[string]any{"decision": *row.decision, "result": res.Status, "level": res.Level + 1, "levels": res.Levels,
		"step_up": runtime.StepUpWhatsAppPIN, "via": "flow_pin"})
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM chat_sessions WHERE tenant_id = $1 AND user_id = $2 AND state = $3`, row.tenant, user, stateStepUp)
		return err
	})
	verb := "approved"
	if *row.decision == "rejected" {
		verb = "rejected"
	}
	m := "Recorded with your PIN: you " + verb + " " + *row.step + "."
	switch res.Status {
	case "approved":
		m += " The request is approved and the run continues."
	case "rejected":
		m += " The request is rejected."
	default:
		m += " It is waiting for more approvals."
	}
	return finish("done", m, "")
}

// --- embedded signup ---

// waEmbeddedSignup is where Meta's embedded signup will hand over a
// tenant's newly connected number (spec 11.4). Which Business Solution
// Provider path Taskiem takes is a decision for people (needs-people W3),
// so it is not implemented: a number is connected by hand
// (PUT /v1/whatsapp/number), and this answers 501. When it is built it
// must: verify the signup session against the tenant that started it
// (state bound to the tenant and admin, single use); exchange the code for
// a business token server-side; read the phone number id and WABA id from
// the session's event; store the token in the tenant's vault under
// whatsapp.OwnEnv and the number in whatsapp_numbers with source
// 'embedded_signup' and own_app false (webhooks then come through
// Taskiem's app); register the number and upload the Flows public key.
func (s *Server) waEmbeddedSignup(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusNotImplemented, "embedded signup is not available yet; connect the number by hand (Settings > WhatsApp number)")
}

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

// Public self-service menus (spec 11.2). A tenant with its own number may
// offer a short menu to anyone who writes to it without a bound account:
// only the workflows the tenant marked public, deployed in prod; inputs
// in a WhatsApp form (never field by field), checked against the schema;
// an explicit yes; then a run started by the actor
// "whatsapp_public:<number>". No sign-in means no roles: the menu can do
// nothing but start those workflows, under the tenant's plan limits and
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
	var items []waPublicWorkflow
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT public_menu FROM whatsapp_numbers WHERE tenant_id = $1 AND phone_number_id = $2 AND status = 'active'`,
			tenant, wa.Config.PhoneNumberID).Scan(&on)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil || !on {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT workflow_id, label FROM whatsapp_public_workflows WHERE tenant_id = $1 ORDER BY created_at, label`, tenant)
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (waPublicWorkflow, error) {
			var x waPublicWorkflow
			return x, r.Scan(&x.WorkflowID, &x.Label)
		})
		return err
	})
	if err != nil || !on || len(items) == 0 {
		return false, err
	}
	// Strict limits: a few messages per number, and a ceiling for the
	// tenant's whole menu. Beyond them messages are dropped unanswered.
	key := tenant.String() + "/" + number
	if !s.limiter("wa-pub:"+key, 10*time.Second, 6).Allow() || !s.limiter("wa-pub-tenant:"+tenant.String(), 200*time.Millisecond, 50).Allow() {
		return true, nil
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
		return true, say(tr(l, "wa.public.confirm_reminder"), confirmButtonsIn(l)...)
	}
	if n, err := strconv.Atoi(cmd); err == nil && n >= 1 && n <= len(items) {
		return true, s.waPublicChoose(ctx, wa, number, items[n-1], say)
	}
	lines := make([]string, len(items))
	for i, x := range items {
		lines[i] = fmt.Sprintf("%d. %s", i+1, x.Label)
	}
	return true, say(tr(l, "wa.public.menu", "list", strings.Join(lines, "\n")))
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

// waPublicChoose starts the chosen item: its form, or straight to the
// confirmation when it takes no inputs.
func (s *Server) waPublicChoose(ctx context.Context, wa *whatsapp.Platform, number string, item waPublicWorkflow, say func(string, ...whatsapp.Button) error) error {
	tenant := wa.Tenant
	l := s.chooseLang(ctx, tenant, number, "").Tag
	var version int
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		version, err = deployedVersion(ctx, tx, item.WorkflowID, "prod")
		return err
	})
	if err != nil {
		return err
	}
	if version == 0 {
		return say(tr(l, "wa.public.unavailable", "label", item.Label))
	}
	data := pubData{Workflow: item.WorkflowID, Version: version, Label: item.Label}
	fields, err := s.waFieldsOf(ctx, tenant, item.WorkflowID, version)
	if errors.Is(err, whatsapp.ErrInputsUnsupported) {
		return say(tr(l, "wa.public.not_here", "label", item.Label))
	}
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return s.waPublicConfirm(ctx, wa, number, data, say)
	}
	form, err := whatsapp.FlowForm(item.Label, fields, nil)
	if wa.Config.FlowKey == nil || errors.Is(err, whatsapp.ErrFlowUnsupported) {
		return say(tr(l, "wa.public.not_here", "label", item.Label))
	}
	if err != nil {
		return err
	}
	tok, hash := whatsapp.NewFlowToken(tenant)
	meta, _ := json.Marshal(map[string]any{"title": item.Label})
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, workflow_id, version, data, expires_at)
			VALUES ($1, $2, 'public', $3, $4, $5, $6, $7, now() + $8::interval)`, hash, tenant, number, wa.Config.PhoneNumberID, item.WorkflowID, version, meta, waPublicTTL.String())
		return err
	}); err != nil {
		return err
	}
	data.Flow = hex.EncodeToString(hash)
	if err := s.pubSave(ctx, tenant, number, stateCollecting, data); err != nil {
		return err
	}
	if _, err := wa.Client.SendFlow(ctx, number, whatsapp.FlowMessage{Flow: whatsapp.FlowInputs, Token: tok, CTA: tr(l, "button.fill_in"), Screen: whatsapp.ScreenInputs, Data: form,
		Body: tr(l, "wa.public.form", "label", item.Label, "cta", tr(l, "button.fill_in"))}); err != nil {
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
	data.Flow = ""
	if err := s.setFlow(ctx, row, "done", map[string]any{}); err != nil {
		return err
	}
	return s.waPublicConfirm(ctx, wa, number, data, say)
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
	return say(msg, confirmButtonsIn(l)...)
}

// waPublicStart starts the confirmed run as whatsapp_public:<number>.
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
	deployed, public := 0, false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_public_workflows WHERE tenant_id = $1 AND workflow_id = $2)`, tenant, data.Workflow).Scan(&public); err != nil {
			return err
		}
		var err error
		if deployed, err = deployedVersion(ctx, tx, data.Workflow, "prod"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2 AND state IN ('published', 'deprecated')`,
			data.Workflow, data.Version).Scan(&def); err != nil {
			return err
		}
		input, err = pii.Open(ctx, s.Store.PII, tx, tenant, data.Inputs, pii.Taint{})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!public || deployed != data.Version) {
		return say(tr(l, "wa.public.changed", "label", data.Label))
	}
	if err != nil {
		return err
	}
	if input == nil {
		input = map[string]any{}
	}
	raw, _ := json.Marshal(input)
	if probs := s.inputProblems(data.Workflow, data.Version, def, raw); len(probs) > 0 {
		return say(tr(l, "wa.public.bad_details", "problems", whatsapp.SafeText(strings.Join(probs, "; "))))
	}
	actor := "whatsapp_public:" + number
	ref, created, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: data.Workflow, Version: data.Version, Environment: "prod",
		Trigger:   map[string]any{"type": "manual", "channel": "whatsapp_public", "body": input},
		StartedBy: actor, TriggerID: "whatsapp_public/" + data.Workflow.String(), DedupKey: data.Nonce,
	})
	if le, ok := runtime.IsLimit(err); ok {
		s.Logger.Info("whatsapp: a public run refused by a plan limit", "limit", le.Limit)
		return say(tr(l, "wa.public.busy"))
	}
	if err != nil {
		return err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		detail, _ := json.Marshal(map[string]any{"workflow": data.Workflow, "version": data.Version, "environment": "prod", "created": created, "channel": "whatsapp_public"})
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, 'run.start', $3, $4)`, tenant, "whatsapp_public:"+whatsapp.MaskNumber(number), ref.ID.String(), detail)
		return err
	}); err != nil {
		return err
	}
	return say(tr(l, "wa.public.started", "label", data.Label, "reference", strings.ToUpper(ref.ID.String()[:8])))
}

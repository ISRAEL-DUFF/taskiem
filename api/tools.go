package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Your tools (made tools M1, decision 0070): a workflow deployed with a
// tool block is a tool of its business in that environment, listed in
// workflow_tools by the transaction that deploys it. A member calls it by
// its command: the form, then its care (answers at once with an echo of
// what was read; else yes; else yes and a passkey), then the run, as the
// member, held to the workflow's roles, and its answer drawn from the
// run's outputs when it ends.

// toolChannelAPI is the channel of a start through the API (the app's and
// the web's sheets), which reads its own answer.
const toolChannelAPI = "api"

// toolConfirmFor is how long a start waits for its yes.
const toolConfirmFor = 10 * time.Minute

// toolRow is one deployed tool, as workflow_tools keeps it.
type toolRow struct {
	Workflow    uuid.UUID      `json:"workflow_id"`
	Environment string         `json:"environment"`
	Version     int            `json:"version"`
	Command     string         `json:"command"`
	Name        string         `json:"name"`
	About       string         `json:"about"`
	Says        []string       `json:"says"`
	Icon        string         `json:"icon,omitempty"`
	Audience    string         `json:"-"`
	Roles       []string       `json:"-"`
	Care        wdcheck.Care   `json:"care"`
	AnswerType  string         `json:"answer,omitempty"`
	AlwaysAsk   bool           `json:"always_ask"`
	Inputs      map[string]any `json:"inputs,omitempty"`
}

// Asks is the care the person meets: the steps' care, raised to yes by
// the owner's always ask.
func (t toolRow) Asks() string {
	if t.AlwaysAsk && t.Care.Level == wdcheck.CareAnswers {
		return wdcheck.CareYes
	}
	return t.Care.Level
}

const toolCols = `workflow_id, environment, version, command, name, about, says, icon, audience, roles, care, answer_type, always_ask`

func scanTool(row pgx.Row) (toolRow, error) {
	var t toolRow
	var care []byte
	err := row.Scan(&t.Workflow, &t.Environment, &t.Version, &t.Command, &t.Name, &t.About, &t.Says, &t.Icon, &t.Audience, &t.Roles, &care, &t.AnswerType, &t.AlwaysAsk)
	if err == nil {
		err = json.Unmarshal(care, &t.Care)
	}
	return t, err
}

// writeToolIndex keeps workflow_tools true to a deployment: a version with
// a tool block is the tool (its care worked out from its steps, or read
// as published), one without removes it. The owner's always ask stays.
func (s *Server) writeToolIndex(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, env string, v int, d *wd.Definition) error {
	if d.Tool == nil {
		_, err := tx.Exec(ctx, `DELETE FROM workflow_tools WHERE workflow_id = $1 AND environment = $2`, wf, env)
		return err
	}
	var care []byte
	if err := tx.QueryRow(ctx, `SELECT tool_care FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&care); err != nil {
		return err
	}
	if care == nil {
		// Published before care was kept: worked out now.
		var def []byte
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def); err != nil {
			return err
		}
		if err := s.storeToolCare(ctx, tx, tenant, wf, v, def, d); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT tool_care FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&care); err != nil {
			return err
		}
	}
	t := d.Tool
	says := t.Says
	if says == nil {
		says = []string{}
	}
	roles := d.Roles
	if roles == nil {
		roles = []string{}
	}
	answer := ""
	if t.Answer != nil {
		answer = t.Answer.Type
	}
	var other string
	err := tx.QueryRow(ctx, `SELECT w.name FROM workflow_tools t JOIN workflows w ON w.id = t.workflow_id
		WHERE t.environment = $1 AND t.command = $2 AND t.workflow_id <> $3`, env, t.Command, wf).Scan(&other)
	switch {
	case err == nil:
		return fmt.Errorf("%w: in %s, the command /%s is already %s's; choose another", errConflict, env, t.Command, other)
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflow_tools (tenant_id, workflow_id, environment, version, command, name, about, says, icon, audience, roles, care, answer_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (workflow_id, environment) DO UPDATE SET version = $4, command = $5, name = $6, about = $7, says = $8, icon = $9,
		  audience = $10, roles = $11, care = $12, answer_type = $13, deployed_at = now()`,
		tenant, wf, env, v, t.Command, t.Name, t.About, says, t.Icon, t.Audience(), roles, care, answer)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Taken by a deploy at the same moment.
		return fmt.Errorf("%w: in %s, the command /%s was just taken by another workflow; choose another", errConflict, env, t.Command)
	}
	return err
}

// toolFor finds the tool a member calls by command in env, held to its
// audience (members) and the workflow's roles.
func (s *Server) toolFor(ctx context.Context, tx pgx.Tx, p *Principal, env, command string) (toolRow, error) {
	t, err := scanTool(tx.QueryRow(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND command = $2`, env, command))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && t.Audience != wd.ToolForMembers {
		return t, fmt.Errorf("%w: no tool /%s", errNotFound, command)
	}
	if err != nil {
		return t, err
	}
	if !mayStart(p, t.Roles) {
		return t, errNotYours(t.Name, t.Roles)
	}
	return t, nil
}

// listTools is GET /v1/tools: the caller's tools in the business (those
// whose roles let them start it), with each one's inputs and care.
func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	env, err := environment(p, r.URL.Query().Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []toolRow{}
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND audience = 'members' ORDER BY name`, env)
		if err != nil {
			return err
		}
		all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) { return scanTool(row) })
		if err != nil {
			return err
		}
		for _, t := range all {
			if !mayStart(p, t.Roles) {
				continue
			}
			var def []byte
			if err := tx.QueryRow(r.Context(), `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def); err != nil {
				return err
			}
			if d, err := s.definitionFor(t.Workflow, t.Version, def); err == nil {
				if schema, _ := d.InputsSchema(); schema != nil {
					t.Inputs, _ = schema.(map[string]any)
				}
			}
			out = append(out, t)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

// toolStart is what starting a tool came to: a run, or a yes it waits for.
type toolStart struct {
	Run     uuid.UUID   `json:"run_id,omitzero"`
	Care    string      `json:"care"`
	Confirm *uuid.UUID  `json:"confirm,omitempty"`
	Expires *time.Time  `json:"expires_at,omitempty"`
	Values  []toolValue `json:"values"`
	Created bool        `json:"created,omitempty"`
}

// toolValue is one value as the person sees it before the run: masked.
type toolValue struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// toolValues are a tool's input values in words, masked as messages are:
// the echo of a tool that answers at once, the summary of one that asks.
func toolValues(d *wd.Definition, input map[string]any) []toolValue {
	schema, _ := d.InputsSchema()
	props := map[string]any{}
	if m, ok := schema.(map[string]any); ok {
		props, _ = m["properties"].(map[string]any)
	}
	names := make([]string, 0, len(input))
	for k := range input {
		names = append(names, k)
	}
	slices.Sort(names)
	out := []toolValue{}
	for _, k := range names {
		label, value := k, fmt.Sprint(input[k])
		if p, ok := props[k].(map[string]any); ok {
			if t, ok := p["title"].(string); ok && t != "" {
				label = t
			}
			// An amount the form takes in naira reads as one.
			if x, _ := p["x-form"].(map[string]any); x["unit"] == "naira" {
				if n, ok := input[k].(float64); ok {
					value = invoicing.Naira(int64(math.Round(n * 100)))
				}
			}
		}
		out = append(out, toolValue{Name: k, Label: label, Value: maskWords(value)})
	}
	return out
}

// startTool starts tool t for p with input from channel: at once when its
// care answers, else a confirm it waits for. dedup makes a repeated call
// start one run.
func (s *Server) startTool(ctx context.Context, p *Principal, t toolRow, input map[string]any, channel, dedup string) (toolStart, error) {
	var def []byte
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def)
	}); err != nil {
		return toolStart{}, err
	}
	d, err := s.definitionFor(t.Workflow, t.Version, def)
	if err != nil {
		return toolStart{}, err
	}
	if input == nil {
		input = map[string]any{}
	}
	raw, _ := json.Marshal(input)
	if probs := s.inputProblems(t.Workflow, t.Version, def, raw); len(probs) > 0 {
		return toolStart{}, toolInputError(probs)
	}
	out := toolStart{Care: t.Asks(), Values: toolValues(d, input)}
	if out.Care == wdcheck.CareAnswers {
		out.Run, out.Created, err = s.runTool(ctx, p, t, input, channel, dedup)
		return out, err
	}
	id, _ := uuid.NewV7()
	until := time.Now().Add(toolConfirmFor)
	digest := sha256.Sum256(canonicalJSON(input))
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tool_confirms (id, tenant_id, user_id, workflow_id, version, environment, command, care, input_digest, channel, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, id, p.TenantID, p.UserID, t.Workflow, t.Version, t.Environment, t.Command, out.Care, digest[:], channel, until)
		return err
	}); err != nil {
		return toolStart{}, err
	}
	out.Confirm, out.Expires = &id, &until
	return out, nil
}

// confirmTool starts the run a confirm waited for, once: the same values,
// by the same person, before it expires. A confirm used before answers
// with the run it started.
func (s *Server) confirmTool(ctx context.Context, p *Principal, t toolRow, confirm uuid.UUID, input map[string]any) (uuid.UUID, bool, error) {
	if input == nil {
		input = map[string]any{}
	}
	digest := sha256.Sum256(canonicalJSON(input))
	var c struct {
		user    uuid.UUID
		version int
		digest  []byte
		expires time.Time
		run     *uuid.UUID
		channel string
	}
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT user_id, version, input_digest, expires_at, run_id, channel FROM tool_confirms WHERE id = $1 AND command = $2 AND environment = $3 FOR UPDATE`,
			confirm, t.Command, t.Environment).Scan(&c.user, &c.version, &c.digest, &c.expires, &c.run, &c.channel)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && c.user != p.UserID:
		return uuid.Nil, false, fmt.Errorf("%w: that confirmation is not yours, or has gone; start the tool again", errNotFound)
	case err != nil:
		return uuid.Nil, false, err
	case c.run != nil:
		return *c.run, false, nil
	case time.Now().After(c.expires):
		return uuid.Nil, false, fmt.Errorf("%w: that confirmation has expired; start the tool again", errConflict)
	case c.version != t.Version:
		return uuid.Nil, false, fmt.Errorf("%w: the tool changed since you started it; start it again", errConflict)
	case !bytes.Equal(c.digest, digest[:]):
		return uuid.Nil, false, fmt.Errorf("%w: those are not the values you were shown; start the tool again", errConflict)
	}
	run, created, err := s.runTool(ctx, p, t, input, c.channel, "tool-confirm:"+confirm.String())
	if err != nil {
		return run, created, err
	}
	err = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tool_confirms SET run_id = $2 WHERE id = $1`, confirm, run)
		return err
	})
	return run, created, err
}

// runTool starts tool t's run as p, recorded with the tool's command and
// the channel, under the plan's run limits as any run.
func (s *Server) runTool(ctx context.Context, p *Principal, t toolRow, input map[string]any, channel, dedup string) (uuid.UUID, bool, error) {
	ref, created, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: p.TenantID, WorkflowID: t.Workflow, Version: t.Version, Environment: t.Environment,
		Trigger:   map[string]any{"type": "manual", "body": input, "tool": t.Command, "channel": channel},
		StartedBy: p.Actor(), TriggerID: "tool/" + t.Workflow.String(), DedupKey: dedup,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	err = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		// A start from a chat is told its answer there by the run watch;
		// the app and the web read it themselves (GET /v1/runs/{id}/answer).
		if p.UserID != uuid.Nil && channel != toolChannelAPI {
			if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_run_watches (tenant_id, run_id, user_id, channel) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				p.TenantID, ref.ID, p.UserID, channel); err != nil {
				return err
			}
		}
		return waAuditAs(ctx, tx, channel, p, "run.start", ref.ID.String(), map[string]any{"workflow": t.Workflow, "version": t.Version,
			"environment": t.Environment, "tool": t.Command, "care": t.Asks(), "created": created})
	})
	return ref.ID, created, err
}

// toolInputError is a start whose values do not fit the tool's form.
type toolInputError []string

func (e toolInputError) Error() string {
	return "the values do not fit the tool's form: " + strings.Join(e, "; ")
}

// --- routes ---

// startToolRoute is POST /v1/tools/{command}/start {input}: started at
// once (201 with the run) when the tool only reads, else 200 with the
// confirm to send back with the same values, and what will run.
func (s *Server) startToolRoute(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Input       map[string]any `json:"input"`
		Environment string         `json:"environment,omitempty"`
		// AnswerInChat: the answer's words are also put in the person's
		// Taskiem conversation when the run ends (the web's sheet asks;
		// the app draws its own card).
		AnswerInChat bool `json:"answer_in_chat,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := environment(p, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var t toolRow
	if err := s.tx(r, func(tx pgx.Tx) error {
		t, err = s.toolFor(r.Context(), tx, p, env, chi.URLParam(r, "command"))
		return err
	}); err != nil {
		s.toolRefused(w, r, err, chi.URLParam(r, "command"))
		return
	}
	channel := toolChannelAPI
	if req.AnswerInChat {
		channel = "web"
	}
	out, err := s.startTool(r.Context(), p, t, req.Input, channel, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.toolRefused(w, r, err, t.Command)
		return
	}
	status := http.StatusOK
	if out.Run != uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

// confirmToolRoute is POST /v1/tools/{command}/confirm {confirm, input,
// passkey?}: the run a start waited for. A tool that moves money takes a
// passkey for this tool (operation tool.start, target tool:<command>).
func (s *Server) confirmToolRoute(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Confirm     uuid.UUID      `json:"confirm"`
		Input       map[string]any `json:"input"`
		Environment string         `json:"environment,omitempty"`
		stepUpProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := environment(p, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var t toolRow
	if err := s.tx(r, func(tx pgx.Tx) error {
		t, err = s.toolFor(r.Context(), tx, p, env, chi.URLParam(r, "command"))
		return err
	}); err != nil {
		s.toolRefused(w, r, err, chi.URLParam(r, "command"))
		return
	}
	if t.Asks() == wdcheck.CareStepUp {
		if req.Passkey == nil && req.TOTP != "" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "a tool that moves money is confirmed with a passkey, not an authenticator code", "step_up": "required",
				"methods": []string{"passkey"}, "operation": "tool.start", "target": "tool:" + t.Command})
			return
		}
		method, ok := s.personStepUp(w, r, req.stepUpProof, "tool.start", "tool:"+t.Command, "Tools that move money", "this tool")
		if !ok {
			return
		}
		if method != "passkey" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "a tool that moves money is confirmed with a passkey", "step_up": "required",
				"methods": []string{"passkey"}, "operation": "tool.start", "target": "tool:" + t.Command})
			return
		}
	}
	run, created, err := s.confirmTool(r.Context(), p, t, req.Confirm, req.Input)
	if err != nil {
		s.toolRefused(w, r, err, t.Command)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"run_id": run, "created": created})
}

// toolRefused answers a refused tool: the values that do not fit (422), a
// limit (429), else as any error. A refusal by roles is audited.
func (s *Server) toolRefused(w http.ResponseWriter, r *http.Request, err error, command string) {
	var bad toolInputError
	if errors.Is(err, errNotFound) {
		writeErr(w, http.StatusNotFound, strings.TrimPrefix(err.Error(), errNotFound.Error()+": "))
		return
	}
	if errors.As(err, &bad) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the values do not fit the tool's form", "problems": []string(bad)})
		return
	}
	if le, ok := runtime.IsLimit(err); ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": le.Message})
		return
	}
	if errors.Is(err, errForbidden) {
		_ = s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "run.start.refused", "tool:"+command, map[string]any{"why": "roles"})
		})
	}
	s.fail(w, r, err)
}

// setToolSettings is PUT /v1/tools/{command}/settings {always_ask}: an
// owner makes a tool more careful (always yes), never less.
func (s *Server) setToolSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !p.HasRole("owner") {
		writeErr(w, http.StatusForbidden, "only an owner changes how careful a tool is")
		return
	}
	var req struct {
		AlwaysAsk   bool   `json:"always_ask"`
		Environment string `json:"environment,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := environment(p, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	command := chi.URLParam(r, "command")
	if err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE workflow_tools SET always_ask = $3 WHERE environment = $1 AND command = $2`, env, command, req.AlwaysAsk)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: no tool /%s", errNotFound, command)
		}
		return auditTx(r, tx, "tool.settings", "tool:"+command, map[string]any{"always_ask": req.AlwaysAsk, "environment": env})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errNotFound is a refusal that says what was not found (404).
var errNotFound = errors.New("not found")

// canonicalJSON is v as JSON with its keys in order, for a digest.
func canonicalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// maskWords masks personal data in words shown back, as messages are.
func maskWords(s string) string { return whatsapp.SafeText(s) }

// waAuditAs records action as p (a person, or a key as its owner's
// key), with the channel.
func waAuditAs(ctx context.Context, tx pgx.Tx, channel string, p *Principal, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["channel"] = channel
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, p.TenantID, p.ActorType(), p.Actor(), action, target, raw)
	return err
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Customers' tools (made tools T5, decision 0070 point 6; the build plan's
// M5). A business offers its customers the tools it deploys in prod with
// tool.for "customers", and the entries of its public menu, carried over
// as tools by migration 00560 (each named by its label, with a command
// made from it; a workflow that declares a customers' tool is that tool
// instead). A customer reaches them in two places only: the business's own
// WhatsApp number (menu, then a number) and their chat with the business
// in the app (the customer door). Whatever their care, they always ask
// yes; their inputs come by form only (flat, ten at most); a tool whose
// steps move money, or whose workflow names roles (a customer holds none),
// is never started for a customer; the run is the customer's
// (whatsapp_public:<number>, or customer:<person>), audited in the
// business's chain, under its plan's run limits and the menu's rate
// limits; and its answer shows only the outputs the workflow marks
// public, with buttons only to other customers' tools.

// customerAppActor is who a customer's run in the app is started by.
func customerAppActor(person uuid.UUID) string { return "customer:" + person.String() }

// customerWAActor is who a customer's run on the business's own WhatsApp
// number is started by (the public menu's actor, kept).
func customerWAActor(number string) string { return "whatsapp_public:" + number }

// customerToolsTx are the customers' tools of the business in tx's scope,
// in prod: its deployed customers' tools, then its public menu's entries
// whose workflow is deployed and is no customers' tool of its own, by
// name. A tool whose workflow names roles is left out: a customer holds
// none.
func customerToolsTx(ctx context.Context, tx pgx.Tx) ([]toolRow, error) {
	rows, err := tx.Query(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = 'prod' AND audience = 'customers'`)
	if err != nil {
		return nil, err
	}
	tools, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) { return scanTool(row) })
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `SELECT p.workflow_id, d.version, p.command, p.label FROM whatsapp_public_workflows p
		JOIN deployments d ON d.workflow_id = p.workflow_id AND d.environment = 'prod'
		WHERE NOT EXISTS (SELECT 1 FROM workflow_tools t WHERE t.environment = 'prod' AND t.audience = 'customers' AND (t.workflow_id = p.workflow_id OR t.command = p.command))`)
	if err != nil {
		return nil, err
	}
	menu, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) {
		t := toolRow{Environment: "prod", Audience: wd.ToolForCustomers, Says: []string{}, Roles: []string{}, Care: wdcheck.Care{Level: wdcheck.CareYes}, Menu: true}
		err := row.Scan(&t.Workflow, &t.Version, &t.Command, &t.Name)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	out := []toolRow{}
	for _, t := range append(tools, menu...) {
		if len(t.Roles) == 0 {
			out = append(out, t)
		}
	}
	sortTools(out)
	return out, nil
}

// sortTools orders tools by name, then command.
func sortTools(ts []toolRow) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && (strings.ToLower(ts[j].Name) < strings.ToLower(ts[j-1].Name) ||
			strings.EqualFold(ts[j].Name, ts[j-1].Name) && ts[j].Command < ts[j-1].Command); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

// customerToolTx is one customers' tool by its command; not found when the
// business offers its customers none by that command.
func customerToolTx(ctx context.Context, tx pgx.Tx, command string) (toolRow, error) {
	tools, err := customerToolsTx(ctx, tx)
	if err != nil {
		return toolRow{}, err
	}
	for _, t := range tools {
		if t.Command == command {
			return t, nil
		}
	}
	return toolRow{}, fmt.Errorf("%w: no tool /%s", errNotFound, command)
}

// errCustomerMoney is a customers' tool whose steps move money: never
// started for a customer (made tools §12), whatever was deployed.
var errCustomerMoney = errors.New("this tool moves money, so it is not offered to customers")

// errCustomerForm is a customers' tool whose inputs no form can carry.
var errCustomerForm = errors.New("this tool's details cannot be asked in a form here")

// customerDefinition is a customers' tool's definition as deployed, once
// it is fit for a customer: its care worked out again from its steps
// (a step that moves money refuses it, as the validator does), and its
// inputs flat enough for a form (ten at most).
func (s *Server) customerDefinition(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, t toolRow) ([]byte, *wd.Definition, error) {
	var def []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def); err != nil {
		return nil, nil, err
	}
	d, err := s.definitionFor(t.Workflow, t.Version, def)
	if err != nil {
		return nil, nil, err
	}
	if t.Care.Level == wdcheck.CareStepUp {
		return nil, nil, errCustomerMoney
	}
	reg, err := s.Registry.For(connector.ManifestsOnly(ctx), tenant.String())
	if err != nil {
		return nil, nil, err
	}
	find, _, err := runtime.PinnedTemplates(ctx, tx, t.Workflow, t.Version, runtime.Callables(ctx, tx, templates.Default()))
	if err != nil {
		return nil, nil, err
	}
	care, err := wdcheck.ToolCare(def, reg, find)
	if err != nil {
		return nil, nil, err
	}
	if care.Level == wdcheck.CareStepUp {
		return nil, nil, errCustomerMoney
	}
	var raw json.RawMessage
	if d.RawInputs != nil {
		raw = d.RawInputs.Schema
	}
	if fields, err := whatsapp.InputFields(raw, d.RawTypes); err != nil || len(fields) > whatsapp.FlowSlots {
		return nil, nil, errCustomerForm
	}
	return def, d, nil
}

// customerStart is a customer's run, to be started.
type customerStart struct {
	tool    toolRow
	def     []byte
	input   map[string]any
	actor   string // who started it: whatsapp_public:<number>, customer:<person>
	shown   string // who, in the audit: the number masked, or the person
	kind    string // the audit's actor type: system (a number), person
	channel string // whatsapp_public, customer_app
	dedup   string
}

// startForCustomer starts a customers' tool's run, once (dedup), checked
// against its form as every start is, as the customer, under the plan's
// run limits, and audits it in the business's chain.
func (s *Server) startForCustomer(ctx context.Context, tenant uuid.UUID, c customerStart) (uuid.UUID, bool, error) {
	if c.input == nil {
		c.input = map[string]any{}
	}
	raw, _ := json.Marshal(c.input)
	if probs := s.inputProblems(c.tool.Workflow, c.tool.Version, c.def, raw); len(probs) > 0 {
		return uuid.Nil, false, toolInputError(probs)
	}
	trigger := map[string]any{"type": "manual", "channel": c.channel, "body": c.input, "tool": c.tool.Command}
	ref, created, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: c.tool.Workflow, Version: c.tool.Version, Environment: "prod",
		Trigger: trigger, StartedBy: c.actor, TriggerID: "customer_tool/" + c.tool.Workflow.String(), DedupKey: c.dedup,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	err = db0(ctx, s, tenant, func(tx pgx.Tx) error {
		detail, _ := json.Marshal(map[string]any{"workflow": c.tool.Workflow, "version": c.tool.Version, "environment": "prod", "created": created,
			"channel": c.channel, "tool": c.tool.Command, "for": wd.ToolForCustomers})
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, 'run.start', $4, $5)`, tenant, c.kind, c.shown, ref.ID.String(), detail)
		return err
	})
	return ref.ID, created, err
}

// customerAnswer is a customer's run's answer: only to the customer who
// started it, in prod, drawn from the public outputs alone.
func (s *Server) customerAnswer(ctx context.Context, tenant, run uuid.UUID, actor string, rows int) (madeAnswer, error) {
	return s.answerAs(ctx, tenant, run, func(startedBy, env string) bool { return env == "prod" && startedBy == actor }, rows, true)
}

// pressCustomerButton reads a button on a customer's answer for that
// customer: it must have been drawn on a run they started, still last,
// and open one of the business's customers' tools. Its values, those not
// shown masked, fill the tool's form.
func pressCustomerButton(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor string) (toolRow, map[string]any, error) {
	gone := fmt.Errorf("%w: this button is not available any more", errNotFound)
	var wf uuid.UUID
	var inputs []byte
	var expires time.Time
	var startedBy *string
	var place string
	err := tx.QueryRow(ctx, `SELECT b.workflow_id, b.inputs, b.expires_at, r.started_by, b.place FROM tool_buttons b JOIN runs r ON r.id = b.from_run
		WHERE b.id = $1 AND b.environment = 'prod'`, id).Scan(&wf, &inputs, &expires, &startedBy, &place)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (time.Now().After(expires) || strOr(startedBy) != actor || !strings.HasPrefix(place, wd.ToolForCustomers+"/")) {
		return toolRow{}, nil, gone
	}
	if err != nil {
		return toolRow{}, nil, err
	}
	tools, err := customerToolsTx(ctx, tx)
	if err != nil {
		return toolRow{}, nil, err
	}
	for _, t := range tools {
		if t.Workflow != wf || t.Menu {
			continue
		}
		in := map[string]any{}
		if err := json.Unmarshal(inputs, &in); err != nil {
			return toolRow{}, nil, err
		}
		for k, v := range in {
			if masked(v) {
				delete(in, k)
			}
		}
		return t, in, nil
	}
	return toolRow{}, nil, gone
}

// --- the public menu's entries, as customers' tools ---

var menuCommandWords = regexp.MustCompile(`[^a-z0-9]+`)

// menuCommand is the command a public menu's label is given (as migration
// 00560 gave those carried over): its words joined by dashes, starting
// with a letter, then -2, -3… past those taken.
func menuCommand(label string, taken func(string) bool) string {
	base := strings.Trim(menuCommandWords.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = strings.Trim("menu-"+base, "-")
	}
	if len(base) > 21 {
		base = base[:21]
	}
	base = strings.Trim(base, "-")
	if len(base) < 2 {
		base = "menu"
	}
	c := base
	for n := 2; taken(c); n++ {
		c = base + "-" + strconv.Itoa(n)
	}
	return c
}

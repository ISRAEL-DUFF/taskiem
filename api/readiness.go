package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Readiness (no-technical-steps N3): what a workflow needs before it can
// run where it is published, found from its definition and the business's
// own records, so a missing connection, variable, secret, table or approver
// is fixed before publishing instead of failing a run. The web app's
// publish dialog and AI review, chat and the AI builder share it.

// readyExpr reads expressions' references.
var readyExpr = expr.MustNewWithRoots(expr.Roots...)

// readyNeed is one thing a workflow needs (the AI builder's proposal
// carries the same).
type readyNeed = builder.Need

type readyStep = builder.NeedStep

// readyEnvs are the environments a publish deploys to, and the one whose
// gaps block it: prod when it is among them (dev is a rehearsal), else
// every one.
func readyEnvs(ctx context.Context, tx pgx.Tx) (envs []string, blocking map[string]bool, err error) {
	envs, err = deployTargets(ctx, tx, "")
	if err != nil {
		return nil, nil, err
	}
	blocking = map[string]bool{}
	if slices.Contains(envs, "prod") {
		blocking["prod"] = true
	} else {
		for _, e := range envs {
			blocking[e] = true
		}
	}
	return envs, blocking, nil
}

// readiness lists what def needs in each of envs, in the business's tenant
// scope. l is the language of the messages.
func (s *Server) readiness(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def []byte, envs []string, blocking map[string]bool, l lang.Tag) ([]readyNeed, error) {
	// A definition that does not load needs nothing yet: its own problems
	// are reported by its checks.
	d, loadErr := wd.Load(def)
	if loadErr != nil {
		return []readyNeed{}, nil //nolint:nilerr // reported by the publishing checks instead
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	type key struct{ kind, name, connector, env string }
	found := map[key]*readyNeed{}
	var order []key
	add := func(k key, n readyNeed, st *wd.Step) {
		got := found[k]
		if got == nil {
			n.Kind, n.Name, n.Connector, n.Environment = k.kind, k.name, k.connector, k.env
			n.Steps = []readyStep{}
			n.Blocking = n.Blocking && blocking[k.env]
			found[k] = &n
			order = append(order, k)
			got = &n
		}
		if st != nil && !slices.ContainsFunc(got.Steps, func(x readyStep) bool { return x.ID == st.ID }) {
			got.Steps = append(got.Steps, readyStep{ID: st.ID, Name: stepTitle(st)})
		}
	}

	// What the definition reads: connections by step, variables and
	// secrets by expression, tables by step, roles by approval and form.
	type use struct {
		step *wd.Step
		name string
	}
	var conns []struct {
		step            *wd.Step
		connector, name string
		service         string
	}
	vars, secs := map[string][]*wd.Step{}, map[string][]*wd.Step{}
	var tables, roles []use
	var policies []use
	readExprs := func(st *wd.Step, v any) {
		walkExpressions(v, func(src string) {
			refs, err := readyExpr.References(src)
			if err != nil {
				return
			}
			for n := range refs.Env {
				vars[n] = append(vars[n], st)
			}
			for n := range refs.Secrets {
				secs[n] = append(secs[n], st)
			}
		})
	}
	var trig any
	_ = json.Unmarshal(d.Trigger.RawCfg, &trig)
	readExprs(nil, trig)
	d.Walk(func(st *wd.Step) {
		var raw map[string]any
		b, _ := json.Marshal(st)
		_ = json.Unmarshal(b, &raw)
		readExprs(st, raw)
		if st.Code != nil {
			for _, n := range st.Code.Secrets {
				secs[n] = append(secs[n], st)
			}
		}
		if st.Approval != nil {
			if st.Approval.Role != "" && !expr.IsExpr(st.Approval.Role) {
				roles = append(roles, use{st, st.Approval.Role})
			}
			if st.Approval.Policy != "" {
				policies = append(policies, use{st, st.Approval.Policy})
			}
		}
		if st.Form != nil && st.Form.To.Role != "" {
			roles = append(roles, use{st, st.Form.To.Role})
		}
		if st.Connector == "" {
			return
		}
		c, ok := reg.Get(st.Connector)
		if !ok || c.Manifest.Auth.Type == "" || c.Manifest.Auth.Type == "none" {
			return
		}
		conns = append(conns, struct {
			step            *wd.Step
			connector, name string
			service         string
		}{st, c.Manifest.ID, st.Connection, c.Manifest.Name})
		if c.Manifest.ID == "xenibase" && st.Connection == "data_tables" {
			if name, ok := st.Input["table"].(string); ok && name != "" && !expr.IsExpr(name) {
				tables = append(tables, use{st, name})
			}
		}
	})
	// Roles named by the policies the approvals use.
	for _, u := range policies {
		var doc []byte
		if err := tx.QueryRow(ctx, `SELECT document FROM approval_policies WHERE name = $1 AND state = 'active'`, u.name).Scan(&doc); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // a missing policy is the publish check's
			}
			return nil, err
		}
		if pol, err := policy.Parse(doc); err == nil {
			for _, r := range pol.Rules {
				for _, lv := range r.Levels {
					roles = append(roles, use{u.step, lv.Role})
				}
			}
		}
	}

	for _, env := range envs {
		for _, c := range conns {
			rows, err := tx.Query(ctx, `SELECT name, status FROM connections WHERE environment = $1 AND connector = $2 ORDER BY name`, env, c.connector)
			if err != nil {
				return nil, err
			}
			type row struct{ name, status string }
			have, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
				var x row
				return x, r.Scan(&x.name, &x.status)
			})
			if err != nil {
				return nil, err
			}
			problem := ""
			if c.name != "" {
				i := slices.IndexFunc(have, func(x row) bool { return x.name == c.name })
				switch {
				case i < 0:
					problem = "missing"
				case have[i].status != "active":
					problem = have[i].status // expired or disabled
				}
			} else {
				active := 0
				for _, h := range have {
					if h.status == "active" {
						active++
					}
				}
				switch {
				case active > 1:
					problem = "ambiguous"
				case active == 0 && len(have) > 0:
					problem = have[0].status
				case active == 0:
					problem = "missing"
				}
			}
			if problem == "" {
				continue
			}
			add(key{"connection", c.name, c.connector, env}, readyNeed{Service: c.service, Problem: problem, Blocking: true,
				Message: needMessage(l, "connection", problem, c.service, c.name)}, c.step)
		}
		for n, steps := range vars {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM variables WHERE environment = $1 AND name = $2)`, env, n).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				for _, st := range steps {
					add(key{"variable", n, "", env}, readyNeed{Problem: "missing", Message: needMessage(l, "variable", "missing", "", n)}, st)
				}
			}
		}
		for n, steps := range secs {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM secrets WHERE environment = $1 AND name = $2)`, env, n).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				for _, st := range steps {
					add(key{"secret", n, "", env}, readyNeed{Problem: "missing", Blocking: true, Message: needMessage(l, "secret", "missing", "", n)}, st)
				}
			}
		}
		for _, u := range tables {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM data_tables WHERE name = $1)`, u.name).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				add(key{"table", u.name, "", env}, readyNeed{Problem: "missing", Blocking: true, Message: needMessage(l, "table", "missing", "", u.name)}, u.step)
			}
		}
	}
	// Roles are the business's, not an environment's: reported once, for
	// the environment that blocks (or the first). So are form fields.
	if len(envs) > 0 {
		roleEnv := envs[0]
		for _, e := range envs {
			if blocking[e] {
				roleEnv = e
				break
			}
		}
		// A form asking people for their chat id (N11): nobody knows it.
		for _, f := range chatIDFields(d) {
			add(key{"field", f.name, "", roleEnv}, readyNeed{Problem: "chat_id", Message: tr(l, "ready.chat_id", "field", f.label)}, f.step)
		}
		for _, u := range roles {
			if u.name == "owner" {
				continue
			}
			var held bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.role = $1 AND u.status = 'active')`, u.name).Scan(&held); err != nil {
				return nil, err
			}
			if !held {
				add(key{"role", u.name, "", roleEnv}, readyNeed{Problem: "nobody", Message: needMessage(l, "role", "nobody", "", u.name)}, u.step)
			}
		}
	}
	out := make([]readyNeed, 0, len(order))
	for _, k := range order {
		out = append(out, *found[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Blocking && !out[j].Blocking })
	return out, nil
}

// chatIDWords are what a field asking for a chat app's id or username is
// called, its name or title in lower case with _ and - as spaces.
var chatIDWords = regexp.MustCompile(`\b(chat ?id|telegram (chat |user ?)?(id|name|handle)|telegram username|tg (chat |user ?)?id|whatsapp id)\b`)

type chatIDField struct {
	name, label string
	step        *wd.Step
}

// chatIDFields are the fields of a form trigger or form step that ask for
// a chat id or a Telegram username: people do not know theirs, and a bot
// cannot write to a username. Messages to them go by phone (notify@1
// message), on the app they choose with the business's link.
func chatIDFields(d *wd.Definition) []chatIDField {
	var out []chatIDField
	check := func(schema any, st *wd.Step) {
		m, _ := schema.(map[string]any)
		props, _ := m["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p, _ := props[n].(map[string]any)
			title, _ := p["title"].(string)
			text := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(n + " " + title))
			if !chatIDWords.MatchString(text) {
				continue
			}
			label := title
			if label == "" {
				label = humanName(n)
			}
			out = append(out, chatIDField{name: n, label: label, step: st})
		}
	}
	if d.Trigger.Type == "form" {
		schema, _ := d.InputsSchema()
		check(schema, nil)
	}
	d.Walk(func(st *wd.Step) {
		if st.Form != nil {
			check(st.Form.Schema, st)
		}
	})
	return out
}

// stepTitle is a step's name, or its id.
func stepTitle(st *wd.Step) string {
	if st == nil {
		return ""
	}
	if st.Name != "" {
		return st.Name
	}
	return st.ID
}

// walkExpressions calls fn with every expression string in v.
func walkExpressions(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		if expr.IsExpr(t) {
			fn(t)
		}
	case map[string]any:
		for _, c := range t {
			walkExpressions(c, fn)
		}
	case []any:
		for _, c := range t {
			walkExpressions(c, fn)
		}
	}
}

// needMessage says a need in plain words.
func needMessage(l lang.Tag, kind, problem, service, name string) string {
	switch kind {
	case "connection":
		if service == "" {
			service = name
		}
		switch problem {
		case "expired":
			return tr(l, "ready.connection_expired", "service", service)
		case "disabled":
			return tr(l, "ready.connection_disabled", "service", service)
		case "ambiguous":
			return tr(l, "ready.connection_ambiguous", "service", service)
		}
		return tr(l, "ready.connection_missing", "service", service)
	case "variable":
		return tr(l, "ready.variable", "name", humanName(name))
	case "secret":
		return tr(l, "ready.secret", "name", humanName(name))
	case "table":
		return tr(l, "ready.table", "name", humanName(name))
	case "role":
		return tr(l, "ready.role", "role", humanName(name))
	}
	return name
}

// humanName turns a machine name (head_of_credit) into words (head of
// credit).
func humanName(s string) string { return strings.ReplaceAll(s, "_", " ") }

// blockingNeeds are the needs that stop a publish.
func blockingNeeds(needs []readyNeed) []readyNeed {
	var out []readyNeed
	for _, n := range needs {
		if n.Blocking {
			out = append(out, n)
		}
	}
	return out
}

// errNotReady is a publish stopped by needs; the handler answers 422 with
// them.
type errNotReady struct{ needs []readyNeed }

func (e *errNotReady) Error() string {
	var parts []string
	for _, n := range e.needs {
		parts = append(parts, n.Message)
	}
	return "not ready to publish: " + strings.Join(parts, "; ")
}

func writeNotReady(w http.ResponseWriter, needs []readyNeed) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"code": "not_ready", "error": "this workflow is not ready to publish yet",
		"needs": needs, "problems": []problem{}})
}

// checkReady refuses to publish version v of wf while it has blocking
// needs (a draft only: a published version is already running).
func (s *Server) checkReady(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int) error {
	var def []byte
	var state string
	if err := tx.QueryRow(ctx, `SELECT definition, state FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def, &state); err != nil {
		return err
	}
	if state != "draft" {
		return nil
	}
	envs, blocking, err := readyEnvs(ctx, tx)
	if err != nil {
		return err
	}
	needs, err := s.readiness(ctx, tx, tenant, def, envs, blocking, lang.EN)
	if err != nil {
		return err
	}
	if b := blockingNeeds(needs); len(b) > 0 {
		return &errNotReady{needs: b}
	}
	return nil
}

// versionReadiness answers GET /v1/workflows/{wf}/versions/{v}/readiness.
func (s *Server) versionReadiness(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var needs []readyNeed
	err = s.tx(r, func(tx pgx.Tx) error {
		var def []byte
		if err := tx.QueryRow(r.Context(), `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def); err != nil {
			return err
		}
		envs, blocking, err := readyEnvs(r.Context(), tx)
		if err != nil {
			return err
		}
		needs, err = s.readiness(r.Context(), tx, p.TenantID, def, envs, blocking, lang.EN)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeReadiness(w, needs)
}

// draftReadiness answers POST /v1/readiness for a definition not saved
// yet (the AI review, the canvas).
func (s *Server) draftReadiness(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Definition) == 0 {
		s.fail(w, r, fmt.Errorf("%w: definition is required", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	var needs []readyNeed
	err := s.tx(r, func(tx pgx.Tx) error {
		envs, blocking, err := readyEnvs(r.Context(), tx)
		if err != nil {
			return err
		}
		needs, err = s.readiness(r.Context(), tx, p.TenantID, req.Definition, envs, blocking, lang.EN)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeReadiness(w, needs)
}

func writeReadiness(w http.ResponseWriter, needs []readyNeed) {
	writeJSON(w, http.StatusOK, map[string]any{"ready": len(blockingNeeds(needs)) == 0, "needs": nonNil(needs)})
}

// approverRole is a role an approval or form step can name, for pickers.
type approverRole struct {
	Name string `json:"name"`
	// Builtin: owner or approver; otherwise a business or custom role.
	Builtin bool `json:"builtin"`
	// Holders are the active members who hold it (owners count for every
	// role, but are not counted here).
	Holders int `json:"holders"`
}

// listApproverRoles answers GET /v1/approver-roles: the roles a step can
// name, so nobody types one, and how many active members the business has.
func (s *Server) listApproverRoles(w http.ResponseWriter, r *http.Request) {
	var out []approverRole
	var members int
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT m.user_id) FROM memberships m JOIN users u ON u.id = m.user_id WHERE u.status = 'active'`).Scan(&members); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT m.role, count(DISTINCT m.user_id) FROM memberships m JOIN users u ON u.id = m.user_id
			WHERE u.status = 'active' GROUP BY m.role
			UNION ALL SELECT r.name, 0 FROM roles r WHERE 'approval.decide' = ANY (r.permissions)
			  AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.role = r.name)`)
		if err != nil {
			return err
		}
		type held struct {
			name string
			n    int
		}
		all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (held, error) {
			var h held
			return h, r.Scan(&h.name, &h.n)
		})
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, h := range all {
			counts[h.name] += h.n
		}
		out = append(out, approverRole{Name: "owner", Builtin: true, Holders: counts["owner"]}, approverRole{Name: "approver", Builtin: true, Holders: counts["approver"]})
		var names []string
		for n := range counts {
			if _, builtin := rolePermissions[n]; !builtin {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, approverRole{Name: n, Holders: counts[n]})
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out, "members": members})
}

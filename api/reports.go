package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/wddiff"
)

// Compliance reports (spec 9.6) for examiners and internal audit, over a
// period: GET /v1/reports/{kind}?from=2026-01-01&to=2026-04-01, as JSON or
// with format=csv. Reports hold ids, amounts and actions, never personal
// values.

type report struct {
	Kind    string           `json:"kind"`
	From    time.Time        `json:"from"`
	To      time.Time        `json:"to"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Summary any              `json:"summary,omitempty"`
}

var reportKinds = map[string]func(context.Context, pgx.Tx, *Server, uuid.UUID, time.Time, time.Time) (*report, error){
	"approvals":  approvalsReport,
	"effects":    effectsReport,
	"pii":        piiReport,
	"changes":    changesReport,
	"chain":      chainReport,
	"secret-use": secretUseReport,
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	build, ok := reportKinds[kind]
	if !ok {
		writeErr(w, http.StatusNotFound, "reports: approvals, effects, pii, changes, chain, secret-use")
		return
	}
	q := r.URL.Query()
	to := time.Now().UTC()
	from := to.AddDate(0, -3, 0)
	for _, x := range []struct {
		key string
		dst *time.Time
	}{{"from", &from}, {"to", &to}} {
		if v := q.Get(x.key); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				if t, err = time.Parse(time.RFC3339, v); err != nil {
					s.fail(w, r, fmt.Errorf("%w: %s is a date (2006-01-02) or an RFC 3339 time", errBadRequest, x.key))
					return
				}
			}
			*x.dst = t.UTC()
		}
	}
	if q.Get("to") != "" && !strings.Contains(q.Get("to"), "T") {
		to = to.AddDate(0, 0, 1) // a date "to" includes that whole day
	}
	var rep *report
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		rep, err = build(r.Context(), tx, s, principalFrom(r.Context()).TenantID, from, to)
		if err != nil {
			return err
		}
		return auditTx(r, tx, "report.view", kind, map[string]any{"from": from, "to": to, "format": q.Get("format")})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rep.Kind, rep.From, rep.To = kind, from, to
	if rep.Rows == nil {
		rep.Rows = []map[string]any{}
	}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="taskiem-%s-%s-%s.csv"`, kind, from.Format("20060102"), to.Format("20060102")))
		cw := csv.NewWriter(w)
		_ = cw.Write(rep.Columns)
		for _, row := range rep.Rows {
			rec := make([]string, len(rep.Columns))
			for i, c := range rep.Columns {
				rec[i] = csvCell(row[c])
			}
			_ = cw.Write(rec)
		}
		cw.Flush()
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		// Spreadsheet formula injection: a leading =, +, - or @ is text.
		if t != "" && strings.ContainsRune("=+-@", rune(t[0])) {
			return "'" + t
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case []string:
		return strings.Join(t, "; ")
	}
	return fmt.Sprint(v)
}

// amountOf finds the amount an approval was about, in naira: the first
// number in its subject under a field named for an amount (fields ending
// in _kobo are converted).
func amountOf(subject []byte) (float64, bool) {
	var m map[string]any
	if json.Unmarshal(subject, &m) != nil {
		return 0, false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n, ok := m[k].(float64)
		lk := strings.ToLower(k)
		named := strings.Contains(lk, "amount") || strings.Contains(lk, "total") || strings.HasSuffix(lk, "_kobo")
		if !ok || !named {
			continue
		}
		if strings.HasSuffix(lk, "kobo") {
			n /= 100
		}
		return n, true
	}
	return 0, false
}

func band(naira float64, ok bool) string {
	switch {
	case !ok:
		return "no amount"
	case naira < 100_000:
		return "under ₦100k"
	case naira < 1_000_000:
		return "₦100k – ₦1m"
	case naira < 10_000_000:
		return "₦1m – ₦10m"
	}
	return "₦10m and over"
}

// approvalsReport: every approval decision, with who, for whom, the level,
// step-up, and the amount band; summarised by approver and band.
func approvalsReport(ctx context.Context, tx pgx.Tx, _ *Server, _ uuid.UUID, from, to time.Time) (*report, error) {
	rows, err := tx.Query(ctx, `SELECT d.decided_at, u.email, COALESCE(ob.email, ''), d.decision, d.level + 1, COALESCE(d.step_up, ''), d.channel,
			w.name, d.run_id::text, d.step_id, COALESCE(a.policy, ''), a.subject
		FROM approval_decisions d JOIN approvals a ON a.run_id = d.run_id AND a.step_id = d.step_id
		JOIN runs r ON r.id = d.run_id JOIN workflows w ON w.id = r.workflow_id
		JOIN users u ON u.id = d.user_id LEFT JOIN users ob ON ob.id = d.on_behalf_of
		WHERE d.decided_at >= $1 AND d.decided_at < $2 ORDER BY d.decided_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep := &report{Columns: []string{"decided_at", "approver", "on_behalf_of", "decision", "level", "step_up", "channel", "workflow", "run", "step", "policy", "amount_naira", "band"}}
	type key struct{ approver, decision, band string }
	type agg struct {
		Approver string  `json:"approver"`
		Decision string  `json:"decision"`
		Band     string  `json:"band"`
		Count    int     `json:"count"`
		Total    float64 `json:"total_naira"`
	}
	sum := map[key]*agg{}
	for rows.Next() {
		var at time.Time
		var approver, onBehalf, decision, stepUp, channel, wf, run, step, pol string
		var level int
		var subject []byte
		if err := rows.Scan(&at, &approver, &onBehalf, &decision, &level, &stepUp, &channel, &wf, &run, &step, &pol, &subject); err != nil {
			return nil, err
		}
		amount, ok := amountOf(subject)
		b := band(amount, ok)
		row := map[string]any{"decided_at": at, "approver": approver, "on_behalf_of": onBehalf, "decision": decision, "level": level, "step_up": stepUp,
			"channel": channel, "workflow": wf, "run": run, "step": step, "policy": pol, "band": b}
		if ok {
			row["amount_naira"] = math.Round(amount*100) / 100
		}
		rep.Rows = append(rep.Rows, row)
		k := key{approver, decision, b}
		if sum[k] == nil {
			sum[k] = &agg{Approver: approver, Decision: decision, Band: b}
		}
		sum[k].Count++
		sum[k].Total += amount
	}
	list := make([]*agg, 0, len(sum))
	for _, a := range sum {
		a.Total = math.Round(a.Total*100) / 100
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Approver != list[j].Approver {
			return list[i].Approver < list[j].Approver
		}
		if list[i].Band != list[j].Band {
			return list[i].Band < list[j].Band
		}
		return list[i].Decision < list[j].Decision
	})
	rep.Summary = list
	return rep, rows.Err()
}

// effectsReport: writes that needed a person or a provider check: steps
// parked for an operator, results settled by reconciliation, and operator
// resolutions.
func effectsReport(ctx context.Context, tx pgx.Tx, _ *Server, _ uuid.UUID, from, to time.Time) (*report, error) {
	rows, err := tx.Query(ctx, `SELECT e.recorded_at, w.name, e.run_id::text, e.step_id,
			CASE WHEN e.type = 'StepFailed' THEN 'parked' ELSE 'reconciled' END,
			COALESCE(e.payload->'error'->>'kind', ''), COALESCE(e.payload->'error'->>'message', '')
		FROM run_events e JOIN runs r ON r.id = e.run_id JOIN workflows w ON w.id = r.workflow_id
		WHERE e.recorded_at >= $1 AND e.recorded_at < $2
		  AND ((e.type = 'StepFailed' AND e.payload->'error'->>'next' = 'park') OR (e.type = 'StepCompleted' AND (e.payload->>'reconciled')::boolean))
		ORDER BY e.recorded_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep := &report{Columns: []string{"at", "workflow", "run", "step", "outcome", "error_kind", "detail", "by"}}
	counts := map[string]int{}
	for rows.Next() {
		var at time.Time
		var wf, run, step, outcome, kind, msg string
		if err := rows.Scan(&at, &wf, &run, &step, &outcome, &kind, &msg); err != nil {
			return nil, err
		}
		rep.Rows = append(rep.Rows, map[string]any{"at": at, "workflow": wf, "run": run, "step": step, "outcome": outcome, "error_kind": kind, "detail": msg})
		counts[outcome]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res, err := tx.Query(ctx, `SELECT at, actor_id, target, detail->>'resolution', COALESCE(detail->>'note', '') FROM audit_log
		WHERE action = 'run.resolve' AND at >= $1 AND at < $2 ORDER BY at`, from, to)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	for res.Next() {
		var at time.Time
		var by, target string
		var resolution *string
		var note string
		if err := res.Scan(&at, &by, &target, &resolution, &note); err != nil {
			return nil, err
		}
		run, step, _ := strings.Cut(target, "/")
		outcome := "resolved"
		if resolution != nil {
			outcome = "resolved: " + *resolution
		}
		rep.Rows = append(rep.Rows, map[string]any{"at": at, "run": run, "step": step, "outcome": outcome, "detail": note, "by": by})
		counts["resolved"]++
	}
	sort.SliceStable(rep.Rows, func(i, j int) bool { return rep.Rows[i]["at"].(time.Time).Before(rep.Rows[j]["at"].(time.Time)) })
	rep.Summary = counts
	return rep, res.Err()
}

// piiReport: every access to personal data: reveals in run history, and
// erasures.
func piiReport(ctx context.Context, tx pgx.Tx, _ *Server, _ uuid.UUID, from, to time.Time) (*report, error) {
	rows, err := tx.Query(ctx, `SELECT at, actor_type, actor_id, action, target, COALESCE(detail->>'ip', '') FROM audit_log
		WHERE action IN ('pii.reveal', 'pii.erase') AND at >= $1 AND at < $2 ORDER BY at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep := &report{Columns: []string{"at", "actor_type", "actor", "action", "target", "ip"}}
	counts := map[string]int{}
	for rows.Next() {
		var at time.Time
		var at2, actor, action, target, ip string
		if err := rows.Scan(&at, &at2, &actor, &action, &target, &ip); err != nil {
			return nil, err
		}
		rep.Rows = append(rep.Rows, map[string]any{"at": at, "actor_type": at2, "actor": actor, "action": action, "target": target, "ip": ip})
		counts[actor]++
	}
	rep.Summary = counts
	return rep, rows.Err()
}

// changesReport: every workflow version created in the period, who wrote
// and published it, where it came from (a commit, a pull request), and what
// changed from the version before.
func changesReport(ctx context.Context, tx pgx.Tx, _ *Server, _ uuid.UUID, from, to time.Time) (*report, error) {
	rows, err := tx.Query(ctx, `SELECT v.created_at, w.name, v.workflow_id::text, v.version, v.state, COALESCE(v.created_by, ''), v.published_by::text, v.published_at,
			COALESCE(v.git_commit, ''), COALESCE(v.git_pr, ''), encode(v.digest, 'hex'), v.definition,
			(SELECT p.definition FROM workflow_versions p WHERE p.workflow_id = v.workflow_id AND p.version = v.version - 1)
		FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id
		WHERE v.created_at >= $1 AND v.created_at < $2 ORDER BY v.created_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep := &report{Columns: []string{"created_at", "workflow", "workflow_id", "version", "state", "created_by", "published_by", "published_at", "git_commit", "git_request", "digest", "changes"}}
	for rows.Next() {
		var at time.Time
		var name, wf, state, by, commit, pr, digest string
		var v int
		var publishedBy *string
		var publishedAt *time.Time
		var def, prev []byte
		if err := rows.Scan(&at, &name, &wf, &v, &state, &by, &publishedBy, &publishedAt, &commit, &pr, &digest, &def, &prev); err != nil {
			return nil, err
		}
		changes := []string{"created"}
		if prev != nil {
			changes = wddiff.Diff(prev, def)
		}
		row := map[string]any{"created_at": at, "workflow": name, "workflow_id": wf, "version": v, "state": state, "created_by": by,
			"git_commit": commit, "git_request": pr, "digest": digest, "changes": changes}
		if publishedBy != nil {
			row["published_by"] = *publishedBy
		}
		if publishedAt != nil {
			row["published_at"] = *publishedAt
		}
		rep.Rows = append(rep.Rows, row)
	}
	return rep, rows.Err()
}

// chainReport: the audit chain verified now, in the database, and its
// anchors.
func chainReport(ctx context.Context, tx pgx.Tx, s *Server, tenant uuid.UUID, from, to time.Time) (*report, error) {
	var broken *int64
	if err := tx.QueryRow(ctx, `SELECT taskiem_audit_verify($1)`, tenant).Scan(&broken); err != nil {
		return nil, err
	}
	var entries int64
	_ = tx.QueryRow(ctx, `SELECT COALESCE(chain_seq, 0) FROM audit_chain_heads`).Scan(&entries)
	anchors, err := audit.ListAnchors(ctx, tx)
	if err != nil {
		return nil, err
	}
	// Each anchor must still match the entry it signed, as VerifyWithAnchors
	// checks offline: a chain rewritten after an anchor verifies on its own.
	seqs := make([]int64, len(anchors))
	for i, a := range anchors {
		seqs[i] = a.Seq
	}
	logged := map[int64]string{}
	rows, err := tx.Query(ctx, `SELECT chain_seq, encode(hash, 'hex') FROM audit_log WHERE tenant_id = $1 AND chain_seq = ANY ($2)`, tenant, seqs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seq int64
		var h string
		if err := rows.Scan(&seq, &h); err != nil {
			rows.Close()
			return nil, err
		}
		logged[seq] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mismatched := 0
	for _, a := range anchors {
		if logged[a.Seq] != a.Hash {
			mismatched++
		}
	}
	rep := &report{Columns: []string{"anchored_at", "seq", "hash", "key_id", "signature_valid", "matches_chain"}}
	for _, a := range anchors {
		at, _ := time.Parse(time.RFC3339, a.At)
		if at.Before(from) || !at.Before(to) {
			continue
		}
		row := map[string]any{"anchored_at": at, "seq": a.Seq, "hash": a.Hash, "key_id": a.KeyID, "matches_chain": logged[a.Seq] == a.Hash}
		if s.AnchorKey != nil {
			row["signature_valid"] = a.VerifySignature(s.AnchorKey)
		}
		rep.Rows = append(rep.Rows, row)
	}
	summary := map[string]any{"verified_at": time.Now().UTC(), "entries": entries, "intact": broken == nil && mismatched == 0,
		"anchors_in_period": len(rep.Rows), "anchors_total": len(anchors), "anchors_mismatched": mismatched}
	if broken != nil {
		summary["first_broken"] = *broken
	}
	rep.Summary = summary
	return rep, nil
}

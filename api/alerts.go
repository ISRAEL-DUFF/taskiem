package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Alerts (spec 15.1): channels say where alerts go, rules say what to
// watch. The scheduler role evaluates rules and delivers (engine/alerts).

var slackHookRe = regexp.MustCompile(`^https://hooks\.slack\.com/services/[A-Za-z0-9/_-]+$`)

type alertChannel struct {
	ID         uuid.UUID       `json:"id"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	CreatedAt  time.Time       `json:"created_at"`
	DisabledAt *time.Time      `json:"disabled_at"`
}

func (s *Server) listAlertChannels(w http.ResponseWriter, r *http.Request) {
	var out []alertChannel
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, kind, name, config, created_at, disabled_at FROM alert_channels ORDER BY created_at`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[alertChannel])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": nonNil(out), "email_configured": s.Alerts != nil && s.Alerts.Mailer != nil,
		"whatsapp_configured": s.WhatsApp != nil})
}

// createAlertChannel adds a channel. A Slack webhook URL is a credential
// and goes to the vault; a webhook channel gets a signing key, shown once.
func (s *Server) createAlertChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string   `json:"kind"`
		Name string   `json:"name"`
		To   []string `json:"to,omitempty"`
		URL  string   `json:"url,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.fail(w, r, fmt.Errorf("%w: a channel needs a name", errBadRequest))
		return
	}
	cfg := map[string]any{}
	secret := ""
	switch req.Kind {
	case "email":
		if len(req.To) == 0 || len(req.To) > 20 {
			s.fail(w, r, fmt.Errorf("%w: give 1 to 20 recipients", errBadRequest))
			return
		}
		for _, to := range req.To {
			if at := strings.LastIndex(to, "@"); at < 1 || at == len(to)-1 || strings.ContainsAny(to, " \r\n,<>") {
				s.fail(w, r, fmt.Errorf("%w: %q is not an email address", errBadRequest, to))
				return
			}
		}
		cfg["to"] = req.To
	case "slack":
		if !slackHookRe.MatchString(req.URL) {
			s.fail(w, r, fmt.Errorf("%w: a Slack incoming webhook URL starts https://hooks.slack.com/services/", errBadRequest))
			return
		}
		secret = req.URL
	case "whatsapp":
		// Members, by email: each alert goes to those with a bound number.
		if len(req.To) == 0 || len(req.To) > 20 {
			s.fail(w, r, fmt.Errorf("%w: give 1 to 20 members' emails", errBadRequest))
			return
		}
	case "webhook":
		u, err := url.Parse(req.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			s.fail(w, r, fmt.Errorf("%w: a webhook URL must be https, without credentials", errBadRequest))
			return
		}
		cfg["url"] = u.String()
		secret = "whsec_" + newToken()
	default:
		s.fail(w, r, fmt.Errorf("%w: kind is email, slack, whatsapp or webhook", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	if req.Kind == "whatsapp" {
		err := s.tx(r, func(tx pgx.Tx) error {
			var ids []uuid.UUID
			var emails []string
			for _, to := range req.To {
				var id uuid.UUID
				var email string
				err := tx.QueryRow(r.Context(), `SELECT u.id, u.email FROM users u WHERE lower(u.email) = lower($1)
					AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = $2)`, strings.TrimSpace(to), p.TenantID).Scan(&id, &email)
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: %q is not a member", errBadRequest, to)
				}
				if err != nil {
					return err
				}
				if !slices.Contains(ids, id) {
					ids, emails = append(ids, id), append(emails, email)
				}
			}
			cfg["members"], cfg["emails"] = ids, emails
			return nil
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	id := uuid.Must(uuid.NewV7())
	if secret != "" {
		if _, err := s.Vault.Put(r.Context(), p.TenantID, alerts.VaultEnv, alerts.SecretName(id), []byte(secret), p.Actor()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	raw, _ := json.Marshal(cfg)
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO alert_channels (id, tenant_id, kind, name, config, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, p.TenantID, req.Kind, req.Name, raw, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "alert.channel.create", id.String(), map[string]any{"kind": req.Kind, "name": req.Name, "config": cfg})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"id": id, "kind": req.Kind, "name": req.Name, "config": cfg}
	if req.Kind == "webhook" {
		out["signing_key"] = secret
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) deleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM alert_channels WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(r.Context(), `UPDATE alert_rules SET channel_ids = array_remove(channel_ids, $1) WHERE $1 = ANY (channel_ids)`, id); err != nil {
			return err
		}
		return auditTx(r, tx, "alert.channel.delete", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Vault.Delete(r.Context(), p.TenantID, alerts.VaultEnv, alerts.SecretName(id), p.Actor()); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		s.Logger.Warn("deleting an alert channel's secret", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// testAlertChannel sends a test message now and reports the outcome.
func (s *Server) testAlertChannel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if s.Alerts == nil {
		writeErr(w, http.StatusServiceUnavailable, "alerts are not configured on this server")
		return
	}
	var kind string
	var cfg []byte
	if err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT kind, config FROM alert_channels WHERE id = $1`, id).Scan(&kind, &cfg)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Alerts.SendTest(r.Context(), principalFrom(r.Context()).TenantID, id, kind, cfg); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the test message was not delivered: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivered": true})
}

type alertRuleReq struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Config     alerts.RuleConfig `json:"config"`
	ChannelIDs []uuid.UUID       `json:"channel_ids"`
	Enabled    *bool             `json:"enabled,omitempty"`
}

type alertRule struct {
	ID         uuid.UUID         `json:"id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Config     alerts.RuleConfig `json:"config"`
	ChannelIDs []uuid.UUID       `json:"channel_ids"`
	Enabled    bool              `json:"enabled"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

func (s *Server) listAlertRules(w http.ResponseWriter, r *http.Request) {
	var out []alertRule
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, kind, config, channel_ids, enabled, updated_at FROM alert_rules ORDER BY created_at`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[alertRule])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	kinds := make([]string, 0, len(alerts.Kinds))
	for k := range alerts.Kinds {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	writeJSON(w, http.StatusOK, map[string]any{"rules": nonNil(out), "kinds": kinds})
}

func (s *Server) checkAlertRule(r *http.Request, tx pgx.Tx, req *alertRuleReq) error {
	ctx := r.Context()
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("%w: a rule needs a name", errBadRequest)
	}
	if _, ok := alerts.Kinds[req.Kind]; !ok {
		return fmt.Errorf("%w: unknown kind %q", errBadRequest, req.Kind)
	}
	if _, err := req.Config.ThresholdFor(req.Kind); err != nil {
		return errors.Join(errBadRequest, err)
	}
	if req.Config.WorkflowID != "" {
		if _, err := uuid.Parse(req.Config.WorkflowID); err != nil {
			return fmt.Errorf("%w: bad workflow_id", errBadRequest)
		}
	}
	if req.Config.Environment != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM environments WHERE name = $1)`, req.Config.Environment).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: no environment %q", errBadRequest, req.Config.Environment)
		}
	}
	if len(req.ChannelIDs) == 0 {
		return fmt.Errorf("%w: choose at least one channel", errBadRequest)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_channels WHERE id = ANY ($1)`, req.ChannelIDs).Scan(&n); err != nil {
		return err
	}
	if n != len(slices.Compact(slices.SortedFunc(slices.Values(req.ChannelIDs), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }))) {
		return fmt.Errorf("%w: unknown channel", errBadRequest)
	}
	return nil
}

func (s *Server) createAlertRule(w http.ResponseWriter, r *http.Request) {
	var req alertRuleReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkAlertRule(r, tx, &req); err != nil {
			return err
		}
		cfg, _ := json.Marshal(req.Config)
		if _, err := tx.Exec(r.Context(), `INSERT INTO alert_rules (id, tenant_id, name, kind, config, channel_ids, enabled, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, p.TenantID, req.Name, req.Kind, cfg, req.ChannelIDs, req.Enabled == nil || *req.Enabled, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "alert.rule.create", id.String(), map[string]any{"name": req.Name, "kind": req.Kind, "config": req.Config, "channels": req.ChannelIDs})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req alertRuleReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkAlertRule(r, tx, &req); err != nil {
			return err
		}
		cfg, _ := json.Marshal(req.Config)
		// Re-enabling starts watching from now, not from when it was paused.
		tag, err := tx.Exec(r.Context(), `UPDATE alert_rules SET name = $2, kind = $3, config = $4, channel_ids = $5, enabled = $6, updated_at = now(),
			checked_until = CASE WHEN NOT enabled AND $6 THEN now() ELSE checked_until END WHERE id = $1`,
			id, req.Name, req.Kind, cfg, req.ChannelIDs, req.Enabled == nil || *req.Enabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "alert.rule.update", id.String(), map[string]any{"name": req.Name, "kind": req.Kind, "config": req.Config, "channels": req.ChannelIDs, "enabled": req.Enabled == nil || *req.Enabled})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM alert_rules WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "alert.rule.delete", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listAlerts shows recent alerts and how their deliveries went.
func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	type deliveryInfo struct {
		Channel   string     `json:"channel"`
		Kind      string     `json:"kind"`
		Status    string     `json:"status"`
		Attempts  int        `json:"attempts"`
		LastError *string    `json:"last_error"`
		SentAt    *time.Time `json:"sent_at"`
	}
	type alertInfo struct {
		ID         uuid.UUID      `json:"id"`
		Kind       string         `json:"kind"`
		Rule       *string        `json:"rule"`
		Title      string         `json:"title"`
		Body       string         `json:"body"`
		Link       *string        `json:"link"`
		CreatedAt  time.Time      `json:"created_at"`
		Deliveries []deliveryInfo `json:"deliveries"`
	}
	var out []alertInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		rows, err := tx.Query(ctx, `SELECT a.id, a.kind, r.name, a.title, a.body, a.link, a.created_at FROM alerts a LEFT JOIN alert_rules r ON r.id = a.rule_id
			ORDER BY a.created_at DESC LIMIT 100`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertInfo, error) {
			var a alertInfo
			return a, row.Scan(&a.ID, &a.Kind, &a.Rule, &a.Title, &a.Body, &a.Link, &a.CreatedAt)
		})
		if err != nil {
			return err
		}
		for i := range out {
			rows, err := tx.Query(ctx, `SELECT c.name, c.kind, d.status, d.attempts, d.last_error, d.sent_at FROM alert_deliveries d JOIN alert_channels c ON c.id = d.channel_id
				WHERE d.alert_id = $1 ORDER BY c.name`, out[i].ID)
			if err != nil {
				return err
			}
			if out[i].Deliveries, err = pgx.CollectRows(rows, pgx.RowToStructByPos[deliveryInfo]); err != nil {
				return err
			}
			out[i].Deliveries = nonNil(out[i].Deliveries)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": nonNil(out)})
}

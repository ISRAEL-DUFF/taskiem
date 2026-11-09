package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/connector"
)

// The quality watch on assistants' WhatsApp numbers (decision 0062 V10,
// C23): Meta rates how a business number's customers receive its messages
// and lowers its daily limit, or restricts it, when they block or report
// it. Every few hours Taskiem reads each number an assistant answers on
// (whatsapp@1 get_phone_number), keeps what it read, and tells the owners
// when it gets worse, so a business learns of it before Meta throttles it.

// AlertWhatsAppQuality is the owners' alert for a number getting worse.
const AlertWhatsAppQuality = "whatsapp_quality"

// whatsappQualityEvery is how often each number is read.
const whatsappQualityEvery = 6 * time.Hour

// RunWhatsAppQuality reads the assistants' numbers every few hours until
// ctx ends.
func (s *Server) RunWhatsAppQuality(ctx context.Context) {
	t := time.NewTicker(whatsappQualityEvery)
	defer t.Stop()
	for {
		if n, err := s.CheckWhatsAppQuality(ctx); err != nil {
			s.Logger.Warn("whatsapp quality: the sweep did not finish", "err", err)
		} else if n > 0 {
			s.Logger.Info("whatsapp quality: numbers read", "numbers", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckWhatsAppQuality reads every business's assistant numbers once; it
// answers how many were read. One business failing does not hold back the
// others.
func (s *Server) CheckWhatsAppQuality(ctx context.Context) (int, error) {
	total, after := 0, uuid.Nil
	for {
		rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_whatsapp_quality_due($1, 200)`, after)
		if err != nil {
			return total, err
		}
		tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return total, err
		}
		for _, tenant := range tenants {
			after = tenant
			n, err := s.checkTenantWhatsAppQuality(ctx, tenant)
			total += n
			if err != nil {
				s.Logger.Warn("whatsapp quality: a business's numbers were not read", "tenant", tenant, "err", err)
			}
		}
		if len(tenants) < 200 || ctx.Err() != nil {
			return total, nil
		}
	}
}

// numberHealth is what Meta says of a number.
type numberHealth struct {
	Rating, Tier, Status string
}

// checkTenantWhatsAppQuality reads one business's assistant numbers.
func (s *Server) checkTenantWhatsAppQuality(ctx context.Context, tenant uuid.UUID) (int, error) {
	var channels []string
	fresh := false
	if err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
		// Read in the last few hours (by another server, or before a
		// restart): not read again yet.
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(checked_at) > now() - interval '5 hours', false) FROM whatsapp_number_health WHERE tenant_id = $1`, tenant).Scan(&fresh); err != nil || fresh {
			return err
		}
		as, err := assistantsOf(ctx, tx, tenant)
		for _, a := range as {
			if strings.HasPrefix(a.Connector, "whatsapp@") && !strings.Contains(a.Channel, "//") {
				channels = append(channels, a.Channel)
			}
		}
		return err
	}); err != nil || fresh {
		return 0, err
	}
	seen, n := map[string]bool{}, 0
	for _, ch := range channels {
		if seen[ch] {
			continue
		}
		seen[ch] = true
		ref, name, env, ok := splitChannel(ch)
		if !ok || name == "" {
			continue
		}
		conn, creds, hc, err := s.connectionIn(ctx, tenant, env, ref, name, "whatsapp.quality")
		if err != nil {
			continue // not connected now: readiness and the connection page say so
		}
		act, ok := conn.Actions["get_phone_number"]
		if !ok {
			continue
		}
		resp, err := act.Execute(ctx, connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
		if err != nil {
			s.Logger.Info("whatsapp quality: a number could not be read", "tenant", tenant, "channel", ch, "err", err)
			// One read before and none now (banned, disconnected, a token
			// revoked): the owners are told, once a day.
			if err := s.tx0(ctx, tenant, func(tx pgx.Tx) error { return numberUnread(ctx, tx, tenant, env, name) }); err != nil {
				return n, err
			}
			continue
		}
		out, _ := resp.Output.(map[string]any)
		now := numberHealth{Rating: strings.ToUpper(fmt.Sprint(out["quality_rating"])), Tier: fmt.Sprint(out["messaging_limit_tier"]), Status: strings.ToUpper(fmt.Sprint(out["status"]))}
		number, _ := out["display_phone_number"].(string)
		if err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
			return recordNumberHealth(ctx, tx, tenant, env, name, number, now)
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// recordNumberHealth keeps what was read and tells the owners when the
// number got worse than it was (or, read the first time, is not well).
func recordNumberHealth(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, name, number string, now numberHealth) error {
	var was numberHealth
	first := false
	err := tx.QueryRow(ctx, `SELECT quality_rating, messaging_limit_tier, status FROM whatsapp_number_health WHERE tenant_id = $1 AND environment = $2 AND connection = $3`,
		tenant, env, name).Scan(&was.Rating, &was.Tier, &was.Status)
	switch {
	case err == pgx.ErrNoRows:
		first = true
	case err != nil:
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_number_health (tenant_id, environment, connection, quality_rating, messaging_limit_tier, status, checked_at, changed_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		ON CONFLICT (tenant_id, environment, connection) DO UPDATE SET checked_at = now(),
		  changed_at = CASE WHEN (whatsapp_number_health.quality_rating, whatsapp_number_health.messaging_limit_tier, whatsapp_number_health.status)
		    IS DISTINCT FROM (EXCLUDED.quality_rating, EXCLUDED.messaging_limit_tier, EXCLUDED.status) THEN now() ELSE whatsapp_number_health.changed_at END,
		  quality_rating = EXCLUDED.quality_rating, messaging_limit_tier = EXCLUDED.messaging_limit_tier, status = EXCLUDED.status`,
		tenant, env, name, now.Rating, now.Tier, now.Status); err != nil {
		return err
	}
	why := worse(was, now, first)
	if why == "" || env != "prod" {
		return nil
	}
	if number == "" {
		number = "your WhatsApp number"
	}
	// A number may get worse, better and worse again: once a day per state.
	_, err = alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertWhatsAppQuality,
		Dedup: env + "/" + name + ":" + now.Rating + ":" + now.Tier + ":" + now.Status + ":" + time.Now().UTC().Format("2006-01-02"),
		Title: "Your WhatsApp number needs attention",
		Body: "WhatsApp says " + number + " " + why + ". This happens when customers block or report its messages. " +
			"Look at what your assistant sends under Conversations: shorter answers, fewer messages nobody asked for, and a person when a customer is unhappy help it recover.",
		Link:   "/conversations",
		Detail: map[string]any{"connection": name, "environment": env, "quality_rating": now.Rating, "messaging_limit_tier": now.Tier, "status": now.Status}})
	return err
}

// numberUnread tells the owners that a number read before can no longer
// be read (in prod, once a day).
func numberUnread(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, name string) error {
	var was bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_number_health WHERE tenant_id = $1 AND environment = $2 AND connection = $3)`,
		tenant, env, name).Scan(&was); err != nil || !was || env != "prod" {
		return err
	}
	_, err := alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertWhatsAppQuality,
		Dedup: env + "/" + name + ":unread:" + time.Now().UTC().Format("2006-01-02"),
		Title: "Taskiem cannot reach your WhatsApp number",
		Body:  "Taskiem could not read your WhatsApp number's state from WhatsApp, as it could before: the number may be blocked or disconnected, or the connection's token no longer works. Check the connection under Connections, and your number in WhatsApp Manager.",
		Link:  "/connections", Detail: map[string]any{"connection": name, "environment": env}})
	return err
}

var (
	ratingRank = map[string]int{"GREEN": 0, "YELLOW": 1, "RED": 2}
	tierRank   = map[string]int{"TIER_UNLIMITED": 6, "TIER_100K": 5, "TIER_10K": 4, "TIER_2K": 3, "TIER_1K": 2, "TIER_250": 1, "TIER_50": 0}
	// statusRank: how bad a number's status is, and what it means.
	statusRank  = map[string]int{"CONNECTED": 0, "FLAGGED": 1, "RATE_LIMITED": 2, "RESTRICTED": 3, "DISCONNECTED": 4, "BANNED": 5, "DELETED": 5}
	statusWords = map[string]string{"FLAGGED": "is flagged for low quality: its daily limit may go down",
		"RATE_LIMITED": "is sending too fast: WhatsApp is holding some of its messages back",
		"RESTRICTED":   "is restricted: it can only reply to customers who write first",
		"DISCONNECTED": "is disconnected from WhatsApp: customers' messages are not reaching it",
		"BANNED":       "is banned by WhatsApp: it cannot send messages",
		"DELETED":      "was deleted from your WhatsApp account"}
)

// worse is, in words, how now is worse than was ("" when it is not); read
// the first time, how now is not well.
func worse(was, now numberHealth, first bool) string {
	if r, ok := statusRank[now.Status]; ok && r > 0 {
		if w, okWas := statusRank[was.Status]; first || !okWas || r > w {
			return statusWords[now.Status]
		}
	}
	r, okNow := ratingRank[now.Rating]
	if okNow && r > 0 {
		if w, okWas := ratingRank[was.Rating]; first || !okWas || r > w {
			if r == 2 {
				return "now has a low quality rating (red)"
			}
			return "now has a medium quality rating (yellow)"
		}
	}
	if t, ok := tierRank[now.Tier]; ok && !first {
		if w, ok := tierRank[was.Tier]; ok && t < w {
			return "may now start conversations with fewer customers a day (" + strings.TrimPrefix(now.Tier, "TIER_") + ")"
		}
	}
	return ""
}

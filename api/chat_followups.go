package api

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The chat words follow-ups on and follow-ups off (docs/spec/forms-that-
// sell.md §7): an order form's follow-up of a buyer who left it
// half-filled, turned on or off. With one order form it is that one; with
// more, "follow-ups on for <name>" names it by its button or its page.

var followupsCmd = regexp.MustCompile(`(?i)^\s*follow[\s-]?ups?\s+(on|off)(?:\s+for\s+(.+?))?\s*[.!]?\s*$`)

type followupForm struct {
	id          uuid.UUID
	label, page string
	on          bool
	wait        int
}

// waFollowups answers "follow-ups on" and "follow-ups off".
func (s *Server) waFollowups(ctx context.Context, c *chat, on bool, name string) error {
	var list []followupForm
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id, a.label, COALESCE(f.name, ''), a.followup_on, a.followup_wait FROM storefront_actions a
			LEFT JOIN storefront_fronts f ON f.id = a.front_id WHERE a.kind = 'order' AND a.enabled ORDER BY f.position NULLS FIRST, a.position`)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (followupForm, error) {
			var x followupForm
			return x, r.Scan(&x.id, &x.label, &x.page, &x.on, &x.wait)
		})
		return err
	}); err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.followups.none"))
	}
	how := "off"
	if on {
		how = "on"
	}
	pick := list
	if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
		pick = nil
		for _, x := range list {
			if strings.ToLower(x.label) == name || strings.ToLower(x.page) == name {
				pick = append(pick, x)
			}
		}
	}
	if len(pick) != 1 {
		names := make([]string, 0, len(list))
		for _, x := range list {
			names = append(names, "*"+whatsapp.SafeText(x.label)+"*")
		}
		return c.say(ctx, s, c.t("wa.followups.which", "names", strings.Join(names, ", "), "how", how))
	}
	f := pick[0]
	form := whatsapp.SafeText(f.label)
	if f.on == on {
		return c.say(ctx, s, c.t("wa.followups.already_"+how, "form", form))
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET followup_on = $2, followup_held_at = CASE WHEN $2 THEN followup_held_at END WHERE id = $1`, f.id, on); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.followups", f.id.String(), map[string]any{"on": on, "wait_minutes": f.wait})
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.followups."+how, "form", form, "wait", strconv.Itoa(f.wait)))
}

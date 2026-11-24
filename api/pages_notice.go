package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Pages H5, g-pages-notif (docs/spec/pages.md §9, "From a notification"): a
// Tell me message's button may open one of the business's pages with its
// filters set ({label, page, filters}: "Disk alerts" filtered to db-2).
// It is drawn, in each chat Tell me reaches (WhatsApp, Telegram), only for
// a person the page's roles let open it; pressed, it is held to the page
// again, for whoever pressed it, and answers with the page's short form,
// drawn for them with those filters, and its link with them, which opens
// the page in the app and on the web.

// pageButtonPrefix is a page button's reply payload: "pg:<id>".
const pageButtonPrefix = "pg:"

// mintPageButton keeps a message's page button for the run that sent it,
// once per place; the reply button carries only its id. A filter's value
// that was sealed (personal) or masked is left out, never kept.
func mintPageButton(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, place, label, page string, filters map[string]any) (uuid.UUID, error) {
	var id uuid.UUID
	var expires time.Time
	err := tx.QueryRow(ctx, `SELECT id, expires_at FROM notice_page_buttons WHERE run_id = $1 AND place = $2`, run, place).Scan(&id, &expires)
	switch {
	case err == nil && time.Now().After(expires):
		return uuid.Nil, nil
	case err == nil:
		return id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, err
	}
	// Buttons a week past their end are let go here, the business's own.
	if _, err := tx.Exec(ctx, `DELETE FROM notice_page_buttons WHERE expires_at < now() - interval '7 days'`); err != nil {
		return uuid.Nil, err
	}
	kept := map[string]string{}
	for k, v := range filters {
		if pii.IsEnvelope(v) {
			continue
		}
		var w string
		switch x := v.(type) {
		case string:
			w = strings.TrimSpace(x)
		case float64, bool, int, int64:
			w = fmt.Sprint(x)
		default:
			continue
		}
		if w == "" || strings.Contains(w, "•") || len([]rune(w)) > 100 {
			continue
		}
		kept[k] = w
	}
	f, _ := json.Marshal(kept)
	id = uuid.New()
	err = tx.QueryRow(ctx, `INSERT INTO notice_page_buttons (id, tenant_id, run_id, place, page, label, filters, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, run_id, place) DO UPDATE SET place = EXCLUDED.place
		RETURNING id`, id, tenant, run, place, page, label, f, time.Now().Add(buttonFor)).Scan(&id)
	return id, err
}

// personMayOpenPage says whether a member may open a published page: its
// roles let them (owners always; none named, everyone in the business).
// A page not published, or gone, is no one's to open from a message.
func personMayOpenPage(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, page string) (bool, error) {
	var roles []string
	var published *int
	err := tx.QueryRow(ctx, `SELECT roles, published_version FROM pages WHERE tenant_id = $2 AND name = $1`, page, tenant).Scan(&roles, &published)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || published == nil {
		return false, err
	}
	if len(roles) == 0 {
		return true, nil
	}
	rows, err := tx.Query(ctx, `SELECT role FROM memberships WHERE tenant_id = $2 AND user_id = $1`, user, tenant)
	if err != nil {
		return false, err
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, err
	}
	return slices.Contains(held, "owner") || slices.ContainsFunc(roles, func(r string) bool { return slices.Contains(held, r) }), nil
}

// waPageButton answers a page button pressed in a chat: the page's short
// form with the button's filters, for whoever pressed it, held to the
// page's roles now; anyone else is told they cannot open it.
func (s *Server) waPageButton(ctx context.Context, c *chat, token string) error {
	id, err := uuid.Parse(token)
	if err != nil || c.p == nil {
		return c.say(ctx, s, c.t("wa.tool.button_gone"))
	}
	var page string
	var raw []byte
	var expires time.Time
	err = db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT page, filters, expires_at FROM notice_page_buttons WHERE id = $1`, id).Scan(&page, &raw, &expires)
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && time.Now().After(expires) {
		return c.say(ctx, s, c.t("wa.tool.button_gone"))
	}
	if err != nil {
		return err
	}
	set := map[string]string{}
	_ = json.Unmarshal(raw, &set)
	short, ok, err := s.pageShortForm(context.WithValue(ctx, ctxKey{}, c.p), page, set, c.t)
	if err != nil {
		return err
	}
	if !ok {
		return c.say(ctx, s, c.t("wa.page.cannot_open"))
	}
	return c.say(ctx, s, short)
}

// noticePageButton draws one of a message's page buttons for its person,
// or nothing: the page's roles do not let them open it, or it is gone.
func noticePageButton(ctx context.Context, tx pgx.Tx, it whatsapp.OutboxItem, place, label, page string, filters map[string]any) (*whatsapp.Button, error) {
	ok, err := personMayOpenPage(ctx, tx, it.Tenant, it.User, page)
	if err != nil || !ok {
		return nil, err
	}
	id, err := mintPageButton(ctx, tx, it.Tenant, *it.Run, place, label, page, filters)
	if err != nil || id == uuid.Nil {
		return nil, err
	}
	if r := []rune(label); len(r) > 20 {
		label = string(r[:20])
	}
	return &whatsapp.Button{ID: pageButtonPrefix + id.String(), Title: strings.TrimSpace(label)}, nil
}

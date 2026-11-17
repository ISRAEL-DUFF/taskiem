package chatlinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// What a storefront's chat button offers (§8.2): WhatsApp's chat, the
// Taskiem chat link, or both side by side.
const (
	ButtonWhatsApp = "whatsapp"
	ButtonTaskiem  = "taskiem"
	ButtonBoth     = "both"
)

// ErrInvalid wraps a setting that cannot be kept.
var ErrInvalid = errors.New("chatlinks: invalid")

// Settings are a business's choices about its chat link.
type Settings struct {
	// TakesChats: customers can start a chat from the link (on unless the
	// business turns it off). Off too while the business's Orders area is
	// off: customer chats are part of it (OrdersOff says why).
	TakesChats bool `json:"takes_chats"`
	// OrdersOff: the Orders area is off, so no chats are taken whatever
	// the switch says.
	OrdersOff bool `json:"orders_off,omitempty"`
	// StorefrontButton is what the storefront offers: whatsapp, taskiem
	// or both (the default).
	StorefrontButton string `json:"storefront_button"`
	// LinkInUpdates is whether updates to outsiders on the shared number
	// and bot end "Reply in the Taskiem app: {link}" (§12); nil leaves it
	// to the default, on for a business with no WhatsApp number of its own.
	LinkInUpdates *bool `json:"link_in_updates"`
	// UpdatesLink is LinkInUpdates as it applies now.
	UpdatesLink bool `json:"updates_link"`
}

// SettingsTx reads the business's settings, with the defaults where it
// has said nothing. TakesChats is as it applies now: off while the Orders
// area is off.
func SettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	st := Settings{TakesChats: true, StorefrontButton: ButtonBoth}
	err := tx.QueryRow(ctx, `SELECT takes_chats, storefront_button, link_in_updates FROM customer_chat_link_settings WHERE tenant_id = $1`, tenant).
		Scan(&st.TakesChats, &st.StorefrontButton, &st.LinkInUpdates)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	orders, err := areas.Enabled(ctx, tx, tenant, areas.Orders)
	if err != nil {
		return st, err
	}
	if !orders {
		st.TakesChats, st.OrdersOff = false, true
	}
	if st.LinkInUpdates != nil {
		st.UpdatesLink = *st.LinkInUpdates
		return st, nil
	}
	var own bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_numbers WHERE tenant_id = $1 AND status = 'active')`, tenant).Scan(&own); err != nil {
		return st, err
	}
	st.UpdatesLink = !own
	return st, nil
}

// SettingsIn is what an owner may change; a field left out keeps its value.
type SettingsIn struct {
	TakesChats       *bool   `json:"takes_chats"`
	StorefrontButton *string `json:"storefront_button"`
	// LinkInUpdates: true or false sets it; ResetUpdates goes back to the
	// default.
	LinkInUpdates *bool `json:"link_in_updates"`
	ResetUpdates  bool  `json:"link_in_updates_default"`
}

// SaveSettingsTx changes the business's settings: only the fields given,
// each written in one statement (two owners changing different settings at
// once both stand; nothing read before is written back).
func SaveSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in SettingsIn, by string) (Settings, error) {
	if in.StorefrontButton != nil {
		switch *in.StorefrontButton {
		case ButtonWhatsApp, ButtonTaskiem, ButtonBoth:
		default:
			return Settings{}, fmt.Errorf("%w: the storefront's button is whatsapp, taskiem or both", ErrInvalid)
		}
	}
	setUpdates := in.ResetUpdates || in.LinkInUpdates != nil
	var updates *bool
	if !in.ResetUpdates {
		updates = in.LinkInUpdates
	}
	if _, err := tx.Exec(ctx, `INSERT INTO customer_chat_link_settings (tenant_id, takes_chats, storefront_button, link_in_updates, updated_by)
		VALUES ($1, COALESCE($2::boolean, true), COALESCE($3::text, 'both'), $4::boolean, $6)
		ON CONFLICT (tenant_id) DO UPDATE SET
		  takes_chats = COALESCE($2::boolean, customer_chat_link_settings.takes_chats),
		  storefront_button = COALESCE($3::text, customer_chat_link_settings.storefront_button),
		  link_in_updates = CASE WHEN $5::boolean THEN EXCLUDED.link_in_updates ELSE customer_chat_link_settings.link_in_updates END,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, in.TakesChats, in.StorefrontButton, updates, setUpdates, by); err != nil {
		return Settings{}, err
	}
	return SettingsTx(ctx, tx, tenant)
}

// Card is what anyone with a business's chat link sees of it before
// chatting: its name, photo and hours, and whether it takes chats.
type Card struct {
	Name    string            `json:"name"`
	Tagline string            `json:"tagline,omitempty"`
	Area    string            `json:"area,omitempty"`
	Photo   *links.Photo      `json:"photo,omitempty"`
	Hours   *storefront.Hours `json:"hours,omitempty"`
	// OpenNow is whether it is open by its hours; nil when it gave none.
	OpenNow    *bool `json:"open_now,omitempty"`
	TakesChats bool  `json:"takes_chats"`
	// Shop is its published storefront's address, when it has one.
	Shop string `json:"shop,omitempty"`
}

// CardTx reads the business's card in its own scope (tx): its profile's
// name, tagline, area and logo, else its storefront's; the storefront's
// hours. A photo the moderators rejected is left out.
func CardTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, previewDomain string, now time.Time) (Card, error) {
	var c Card
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&c.Name); err != nil {
		return c, err
	}
	p, err := links.ProfileTx(ctx, tx, tenant)
	if err != nil {
		return c, err
	}
	sf, err := storefront.Get(ctx, tx, tenant)
	hasShop := err == nil
	if err != nil && !errors.Is(err, storefront.ErrNotFound) {
		return c, err
	}
	if hasShop && sf.PublishedVersion > 0 && sf.Status == "published" {
		var m storefront.Model
		if m, err = storefront.PublishedModel(ctx, tx, tenant); err != nil {
			return c, err
		}
		card := m.Card
		if card.Name != "" {
			c.Name = card.Name
		}
		c.Tagline, c.Area = card.Tagline, card.Area
		h := card.Hours
		if open, known := h.OpenNow(now); known {
			c.Hours, c.OpenNow = &h, &open
		}
		c.Shop = sf.URL(previewDomain)
		if len(m.Logo) > 0 {
			c.Photo = &links.Photo{URLs: m.Logo}
		}
	}
	if p.Name != "" {
		c.Name = p.Name
	}
	if p.Tagline != "" {
		c.Tagline = p.Tagline
	}
	if p.Area != "" {
		c.Area = p.Area
	}
	if p.Logo != nil && p.Logo.Moderation != "rejected" {
		c.Photo = p.Logo
	}
	st, err := SettingsTx(ctx, tx, tenant)
	if err != nil {
		return c, err
	}
	c.TakesChats = st.TakesChats
	return c, nil
}

// Notice kinds: "at most once per ..." for what a business sends on a
// customer's behalf.
const (
	// NoticeAnsweredSMS is the browser chat's SMS that the business
	// answered: once per conversation per hour (§9).
	NoticeAnsweredSMS = "answered_sms"
	// NoticeOutsiderLink is "To chat with {business}, open {link}" to an
	// outsider replying to an update: once per business per day (§12).
	NoticeOutsiderLink = "outsider_link"
)

// TakeNoticeTx claims the right to send one notice of kind about key
// (a conversation, an address's hash), in the business's scope: true at
// most once per every.
func TakeNoticeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string, key []byte, every time.Duration) (bool, error) {
	var took bool
	err := tx.QueryRow(ctx, `INSERT INTO customer_chat_notices (tenant_id, kind, key) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, kind, key) DO UPDATE SET last_at = now() WHERE customer_chat_notices.last_at <= now() - $4::interval
		RETURNING true`, tenant, kind, key, every.String()).Scan(&took)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return took, err
}

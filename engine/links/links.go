// Package links is business links (decision 0061;
// docs/spec/business-links.md): two businesses on Taskiem that agreed to
// trade as records. A link is a platform row read only through definer
// functions (migration 00275), so a business sees its links as it may know
// them: a requester never tells a decline or a block from a request still
// waiting, and a request sent blind, by phone number or storefront
// address, from one that reached no business.
//
// L0 is the link itself: search, request, answer, the per-business
// findability, and taskiem_link_enter, the one door into the other side
// that bills (L1) and orders (L2) write through.
package links

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Errors.
var (
	ErrInvalid  = errors.New("links: invalid")
	ErrNotFound = errors.New("links: not found")
	ErrState    = errors.New("links: not possible in the link's state")
	ErrTooMany  = errors.New("links: too many requests today")
)

// Roles: how a business sees the other side.
const (
	RoleSupplier = "supplier" // they supply us
	RoleCustomer = "customer" // they buy from us
	RoleBoth     = "both"
)

// States, as one side sees a link (taskiem_link_view).
const (
	Active      = "active"
	Ended       = "ended"
	Requested   = "requested"    // we asked; waiting, as far as we know
	NotAccepted = "not_accepted" // we asked by search; 30 days and no acceptance
	AskedUs     = "asked_us"     // they asked; ours to answer
	Blocked     = "blocked"      // by us
	Sent        = "sent"         // a blind request of ours: whether anyone has it, we never know
	None        = "none"         // in a search: no link
)

// Answers to a link (taskiem_link_answer).
const (
	Accept  = "accept"
	Decline = "decline"
	Block   = "block"
	Unblock = "unblock"
	Cancel  = "cancel"
	End     = "end"
)

// RequestTTL is how long a request waits.
const RequestTTL = 30 * 24 * time.Hour

// Link is a link as one side sees it.
type Link struct {
	ID       uuid.UUID `json:"id"`
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	// Slug is the other side's storefront address, when it has one.
	Slug  string `json:"slug,omitempty"`
	State string `json:"state"`
	// Role is how we see them: supplier, customer or both.
	Role        string     `json:"role"`
	AskedByUs   bool       `json:"asked_by_us"`
	RequestedAt time.Time  `json:"requested_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AnsweredAt  *time.Time `json:"answered_at,omitempty"`
}

// BlindRequest is a request sent by phone or address, kept on our side
// only: the masked address, never who (if anyone) has it.
type BlindRequest struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Shown     string    `json:"shown"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Item is a published item that matched a search.
type Item struct {
	Name      string `json:"name"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
}

// Card is a business a search found: only what its public page shows.
type Card struct {
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	Slug     string    `json:"slug,omitempty"`
	Area     string    `json:"area,omitempty"`
	Tagline  string    `json:"tagline,omitempty"`
	Items    []Item    `json:"items"`
	// State is our link with it, as we see it (None when there is none).
	State string `json:"state"`
}

// Settings are a business's own link settings.
type Settings struct {
	// Findable: other businesses find us by search. Default (nil): on
	// with a published storefront.
	Findable *bool `json:"findable"`
	// FindableNow is what applies: Findable, or the default.
	FindableNow bool       `json:"findable_now"`
	AskedAt     *time.Time `json:"asked_at,omitempty"`
}

// Actor is who acts, for the audit chains.
type Actor struct {
	Type string // user, api_key, system
	ID   string
}

func pgErr(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "P0002":
		return fmt.Errorf("%w: %s", ErrNotFound, pe.Message)
	case "55000":
		return fmt.Errorf("%w: %s", ErrState, pe.Message)
	case "22023":
		return fmt.Errorf("%w: %s", ErrInvalid, pe.Message)
	case "53400":
		return ErrTooMany
	}
	return err
}

// ValidRole reports whether r is a role a requester may choose.
func ValidRole(r string) bool { return r == RoleSupplier || r == RoleCustomer || r == RoleBoth }

// ListTx is the business in scope's links and requests, its blind ones
// included, those waiting for its answer first.
func ListTx(ctx context.Context, tx pgx.Tx) ([]Link, []BlindRequest, error) {
	rows, err := tx.Query(ctx, `SELECT id, business, name, COALESCE(slug, ''), state, role, asked_by_us, requested_at, expires_at, answered_at FROM taskiem_links_of()`)
	if err != nil {
		return nil, nil, pgErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Link, error) {
		var l Link
		return l, r.Scan(&l.ID, &l.Business, &l.Name, &l.Slug, &l.State, &l.Role, &l.AskedByUs, &l.RequestedAt, &l.ExpiresAt, &l.AnsweredAt)
	})
	if err != nil {
		return nil, nil, pgErr(err)
	}
	rows, err = tx.Query(ctx, `SELECT id, kind, shown, role, created_at, expires_at FROM link_blind_requests
		WHERE cancelled_at IS NULL AND expires_at > now() ORDER BY created_at DESC`)
	if err != nil {
		return nil, nil, err
	}
	blind, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BlindRequest, error) {
		b := BlindRequest{State: Sent}
		return b, r.Scan(&b.ID, &b.Kind, &b.Shown, &b.Role, &b.CreatedAt, &b.ExpiresAt)
	})
	if out == nil {
		out = []Link{}
	}
	if blind == nil {
		blind = []BlindRequest{}
	}
	return out, blind, err
}

// GetTx is one link as the business in scope sees it.
func GetTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Link, error) {
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return Link{}, err
	}
	for _, l := range list {
		if l.ID == id {
			return l, nil
		}
	}
	return Link{}, ErrNotFound
}

var digitsRe = regexp.MustCompile(`\d`)

// LooksLikePhone reports whether q is a phone number rather than words: a
// query the search never takes (it becomes a blind request instead).
func LooksLikePhone(q string) bool {
	return len(digitsRe.FindAllString(q, -1)) >= 7 && strings.Trim(q, "+0123456789 -()") == ""
}

// SearchTx finds businesses that chose to be found, by name, by what they
// sell or do, and by area.
func SearchTx(ctx context.Context, tx pgx.Tx, q string, limit int) ([]Card, error) {
	q = strings.TrimSpace(q)
	out := []Card{}
	if len(q) < 2 || LooksLikePhone(q) {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT business, name, COALESCE(slug, ''), COALESCE(area, ''), COALESCE(tagline, ''), items, state FROM taskiem_link_search($1, $2)`, q, limit)
	if err != nil {
		return nil, pgErr(err)
	}
	out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Card, error) {
		var c Card
		return c, r.Scan(&c.Business, &c.Name, &c.Slug, &c.Area, &c.Tagline, &c.Items, &c.State)
	})
	if out == nil {
		out = []Card{}
	}
	return out, pgErr(err)
}

// RequestTx asks a business a search listed to link, as role. It returns
// the business to tell, or uuid.Nil when no one is to be told (asked
// already, or blocked: the requester sees the same either way).
func RequestTx(ctx context.Context, tx pgx.Tx, target uuid.UUID, role string, by Actor) (uuid.UUID, error) {
	if !ValidRole(role) {
		return uuid.Nil, fmt.Errorf("%w: the role is supplier, customer or both", ErrInvalid)
	}
	var told *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_request($1, NULL, NULL, $2, $3, $4)`, target, role, by.Type, by.ID).Scan(&told); err != nil {
		return uuid.Nil, pgErr(err)
	}
	if told == nil {
		return uuid.Nil, nil
	}
	return *told, nil
}

// MaxBlindPerDay is how many blind requests a business sends a day.
const MaxBlindPerDay = 20

// Blind is a request by phone number (E.164) or storefront address.
type Blind struct {
	Phone string
	Slug  string
	// Shown is the address as the requester sees it, masked.
	Shown string
}

// RequestBlindTx sends a request to whoever is behind a phone number or a
// storefront address, keeping it on the requester's side as sent. It
// returns the business to tell, or uuid.Nil; the caller must answer the
// person the same words either way, and should tell the business off the
// request's path so the answer takes the same time.
func RequestBlindTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, b Blind, role string, by Actor, hashKey []byte) (uuid.UUID, error) {
	if !ValidRole(role) {
		return uuid.Nil, fmt.Errorf("%w: the role is supplier, customer or both", ErrInvalid)
	}
	kind, addr := "phone", b.Phone
	if addr == "" {
		kind, addr = "address", strings.ToLower(b.Slug)
	}
	if addr == "" {
		return uuid.Nil, fmt.Errorf("%w: a phone number or a storefront address", ErrInvalid)
	}
	m := hmac.New(sha256.New, hashKey)
	m.Write([]byte(kind + ":" + addr))
	hash := m.Sum(nil)
	var n int
	var dup bool
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE created_at > now() - interval '1 day'),
		COALESCE(bool_or(address_hash = $1 AND cancelled_at IS NULL AND expires_at > now()), false) FROM link_blind_requests`, hash).Scan(&n, &dup); err != nil {
		return uuid.Nil, err
	}
	if dup {
		return uuid.Nil, nil
	}
	if n >= MaxBlindPerDay {
		return uuid.Nil, ErrTooMany
	}
	shown := b.Shown
	if shown == "" {
		shown = Mask(kind, addr)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO link_blind_requests (id, tenant_id, kind, shown, address_hash, role, requested_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now() + $8::interval)`,
		uuid.Must(uuid.NewV7()), tenant, kind, shown, hash, role, by.Type+":"+by.ID, RequestTTL.String()); err != nil {
		return uuid.Nil, err
	}
	var phone, slug *string
	if kind == "phone" {
		phone = &addr
	} else {
		slug = &addr
	}
	var told *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_request(NULL, $1, $2, $3, $4, $5)`, phone, slug, role, by.Type, by.ID).Scan(&told); err != nil {
		return uuid.Nil, pgErr(err)
	}
	if told == nil {
		return uuid.Nil, nil
	}
	return *told, nil
}

// CancelBlindTx withdraws a blind request from the requester's list. A
// request that reached a business stays with it until it is answered or
// expires: the requester cannot know whether it did.
func CancelBlindTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE link_blind_requests SET cancelled_at = now() WHERE id = $1 AND cancelled_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Mask shows a phone number with its middle hidden (0803…4567), and an
// address as it is.
func Mask(kind, addr string) string {
	if kind != "phone" || len(addr) < 8 {
		return addr
	}
	local := addr
	if strings.HasPrefix(addr, "+234") {
		local = "0" + addr[4:]
	}
	return local[:4] + "…" + local[len(local)-4:]
}

// AnswerTx accepts, declines, blocks, unblocks, cancels or ends a link, as
// the business in scope; it returns the other side.
func AnswerTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, answer string, by Actor) (uuid.UUID, error) {
	switch answer {
	case Accept, Decline, Block, Unblock, Cancel, End:
	default:
		return uuid.Nil, fmt.Errorf("%w: %q is not an answer to a link", ErrInvalid, answer)
	}
	var other uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_answer($1, $2, $3, $4)`, id, answer, by.Type, by.ID).Scan(&other); err != nil {
		return uuid.Nil, pgErr(err)
	}
	return other, nil
}

// EnterTx narrows tx to the other side of an active link for the rest of
// the transaction, audited in both chains as action (link.*). The caller
// writes its own side's rows first.
func EnterTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, action string, detail []byte, by Actor) (uuid.UUID, error) {
	if detail == nil {
		detail = []byte("{}")
	}
	var other uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_enter($1, $2, $3, $4, $5)`, id, by.Type, by.ID, action, detail).Scan(&other); err != nil {
		return uuid.Nil, pgErr(err)
	}
	return other, nil
}

// SettingsTx is the business in scope's link settings.
func SettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	var st Settings
	err := tx.QueryRow(ctx, `SELECT (SELECT findable FROM link_settings WHERE tenant_id = $1), (SELECT asked_at FROM link_settings WHERE tenant_id = $1),
		EXISTS (SELECT 1 FROM storefronts WHERE tenant_id = $1 AND status = 'published')`, tenant).Scan(&st.Findable, &st.AskedAt, &st.FindableNow)
	if st.Findable != nil {
		st.FindableNow = *st.Findable
	}
	return st, err
}

// SetFindableTx sets whether other businesses find the business in scope;
// nil goes back to the default.
func SetFindableTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, findable *bool, by string) (Settings, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, asked_at, updated_by) VALUES ($1, $2, now(), $3)
		ON CONFLICT (tenant_id) DO UPDATE SET findable = $2, asked_at = COALESCE(link_settings.asked_at, now()), updated_by = $3, updated_at = now()`,
		tenant, findable, by); err != nil {
		return Settings{}, err
	}
	return SettingsTx(ctx, tx, tenant)
}

// Package reach is how a business reaches people who are not on Taskiem
// on WhatsApp and Telegram (no-technical-steps N11, decision 0039): its
// staff and customers opt in once with a one-tap link to the platform's
// bot or number, and every workflow that messages their phone reaches them
// there (notify@1 message).
//
// A contact belongs to one business and is keyed by a keyed hash of a
// phone number the person proved: shared through Telegram's own button,
// sent from on WhatsApp, or verified by a form's code. A link made without
// a proven phone reaches the person only about the submission it came
// from, and only for the phones typed in it. Phones and chat addresses
// are sealed under the person's personal-data subject, so erasing the
// person erases them; only keyed hashes are compared.
package reach

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The chat channels a contact is reached on.
const (
	Telegram = "telegram"
	WhatsApp = "whatsapp"
)

// ErrGone: a queued message's contact stopped, was removed or erased.
var ErrGone = errors.New("reach: the person cannot be reached there any more")

// Cipher seals and opens personal data (secrets.Vault).
type Cipher interface {
	pii.Cipher
	SealUnderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, value any) (map[string]any, error)
}

// Book is the businesses' contacts.
type Book struct {
	Pool  *pgxpool.Pool
	Vault Cipher
	// HashKey keys the hashes of phones and chat addresses
	// (TASKIEM_MESSAGE_HASH_KEY, 32 bytes): phone numbers are too few to
	// hash unkeyed.
	HashKey []byte
	// Countries are the calling codes phones may have (default 234).
	Countries []string
}

// Ready reports whether the book can be used.
func (b *Book) Ready() bool {
	return b != nil && b.Pool != nil && b.Vault != nil && len(b.HashKey) == 32
}

func (b *Book) hash(kind, v string) []byte {
	h := hmac.New(sha256.New, b.HashKey)
	h.Write([]byte("reach " + kind + "\x00" + v))
	return h.Sum(nil)
}

// PhoneHash is a phone's (E.164) keyed hash.
func (b *Book) PhoneHash(phone string) []byte { return b.hash("phone", phone) }

// AddressHash is a chat address's keyed hash: tg:<user id> or +E.164.
func (b *Book) AddressHash(channel, addr string) []byte { return b.hash(channel, addr) }

// Phone normalises a phone number to E.164.
func (b *Book) Phone(s string) (string, error) {
	c := b.Countries
	if len(c) == 0 {
		c = []string{"234"}
	}
	return sender.NormalisePhone(s, c)
}

// LinkTx records that a person who proved phone is reached on channel at
// addr, in tx (the tenant's scope). A second link on the same channel
// replaces the first, and a stopped one starts again: linking is asking.
func (b *Book) LinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phone, channel, addr string) (uuid.UUID, error) {
	phone, err := b.Phone(phone)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id, phone FROM reach_contacts WHERE tenant_id = $1 AND phone_hash = $2`, tenant, b.PhoneHash(phone)).Scan(&id, &raw)
	var env map[string]any
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if env, err = b.Vault.SealTx(ctx, tx, tenant, "phone", phone); err != nil {
			return uuid.Nil, err
		}
		id = uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO reach_contacts (id, tenant_id, phone_hash, phone) VALUES ($1, $2, $3, $4)`,
			id, tenant, b.PhoneHash(phone), env); err != nil {
			return uuid.Nil, err
		}
	case err != nil:
		return uuid.Nil, err
	default:
		if err := json.Unmarshal(raw, &env); err != nil {
			return uuid.Nil, err
		}
	}
	subject, _ := env["subject"].(string)
	sealed, err := b.Vault.SealUnderTx(ctx, tx, tenant, subject, category(channel), addr)
	if errors.Is(err, pii.ErrErased) {
		// The person was erased and came back: a new subject.
		if _, err := tx.Exec(ctx, `DELETE FROM reach_contacts WHERE id = $1`, id); err != nil {
			return uuid.Nil, err
		}
		return b.LinkTx(ctx, tx, tenant, phone, channel, addr)
	}
	if err != nil {
		return uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO reach_channels (contact_id, tenant_id, channel, address_hash, address) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (contact_id, channel) DO UPDATE SET address_hash = EXCLUDED.address_hash, address = EXCLUDED.address,
		  linked_at = now(), stopped_at = NULL`, id, tenant, channel, b.AddressHash(channel, addr), sealed)
	return id, err
}

// LinkRequestTx records a link about one submission only: the run of
// reference reaches addr when it messages one of phones.
func (b *Book) LinkRequestTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference, channel, addr string, phones []string) error {
	var hashes [][]byte
	for _, p := range phones {
		if p, err := b.Phone(p); err == nil {
			hashes = append(hashes, b.PhoneHash(p))
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	sealed, err := b.Vault.SealTx(ctx, tx, tenant, category(channel), addr)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO reach_request_links (tenant_id, reference, channel, address_hash, address, phone_hashes) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, reference, channel) DO UPDATE SET address_hash = EXCLUDED.address_hash, address = EXCLUDED.address,
		  phone_hashes = EXCLUDED.phone_hashes, stopped_at = NULL`, tenant, reference, channel, b.AddressHash(channel, addr), sealed, hashes)
	return err
}

// category is the personal-data category of a channel's address.
func category(channel string) string {
	if channel == WhatsApp {
		return "phone"
	}
	return "other"
}

// Target is where a message to a phone goes: a contact's channel, or the
// link of the run's own submission.
type Target struct {
	Channel   string
	Contact   uuid.UUID // a contact's channel
	Reference string    // or a submission's link
}

// FindTx is where a business reaches phone on one of channels (in the
// order a contact's links were made, most recent first): its contact, or
// failing that the link made from run's own submission. false: nowhere.
func (b *Book) FindTx(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, phone string, channels []string) (Target, bool, error) {
	var t Target
	if len(channels) == 0 {
		return t, false, nil
	}
	phone, err := b.Phone(phone)
	if err != nil {
		return t, false, nil //nolint:nilerr // not a phone: no chat to reach it on
	}
	h := b.PhoneHash(phone)
	err = tx.QueryRow(ctx, `SELECT ch.channel, c.id FROM reach_contacts c JOIN reach_channels ch ON ch.contact_id = c.id
		WHERE c.tenant_id = $1 AND c.phone_hash = $2 AND ch.stopped_at IS NULL AND ch.channel = ANY ($3)
		ORDER BY ch.linked_at DESC LIMIT 1`, tenant, h, channels).Scan(&t.Channel, &t.Contact)
	if err == nil {
		return t, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return t, false, err
	}
	if run == uuid.Nil {
		return t, false, nil
	}
	err = tx.QueryRow(ctx, `SELECT l.channel, l.reference FROM reach_request_links l JOIN form_submissions s ON s.reference = l.reference AND s.tenant_id = l.tenant_id
		WHERE s.run_id = $1 AND l.tenant_id = $2 AND $3 = ANY (l.phone_hashes) AND l.stopped_at IS NULL AND l.channel = ANY ($4)
		ORDER BY l.created_at DESC LIMIT 1`, run, tenant, h, channels).Scan(&t.Channel, &t.Reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

// AddressTx opens a target's address to send to it, and notes the send;
// ErrGone when it stopped or is gone.
func (b *Book) AddressTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, t Target) (string, error) {
	var raw []byte
	var err error
	if t.Contact != uuid.Nil {
		err = tx.QueryRow(ctx, `UPDATE reach_channels SET last_sent_at = now() WHERE contact_id = $1 AND channel = $2 AND tenant_id = $3 AND stopped_at IS NULL
			RETURNING address`, t.Contact, t.Channel, tenant).Scan(&raw)
	} else {
		err = tx.QueryRow(ctx, `UPDATE reach_request_links SET last_sent_at = now() WHERE tenant_id = $1 AND reference = $2 AND channel = $3 AND stopped_at IS NULL
			RETURNING address`, tenant, t.Reference, t.Channel).Scan(&raw)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGone
	}
	if err != nil {
		return "", err
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	v, err := b.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return "", ErrGone
	}
	if err != nil {
		return "", err
	}
	addr, _ := v.(string)
	if addr == "" {
		return "", ErrGone
	}
	return addr, nil
}

// --- what a chat address says (no tenant: SECURITY DEFINER functions) ---

// Stop stops the business that reached addr most recently; its name, or
// "" when none reaches it.
func (b *Book) Stop(ctx context.Context, channel, addr string) (string, error) {
	var name *string
	err := b.Pool.QueryRow(ctx, `SELECT taskiem_reach_stop($1, $2)`, channel, b.AddressHash(channel, addr)).Scan(&name)
	if err != nil || name == nil {
		return "", err
	}
	return *name, nil
}

// Resume undoes the latest stop; the business's name, or "".
func (b *Book) Resume(ctx context.Context, channel, addr string) (string, error) {
	var name *string
	err := b.Pool.QueryRow(ctx, `SELECT taskiem_reach_resume($1, $2)`, channel, b.AddressHash(channel, addr)).Scan(&name)
	if err != nil || name == nil {
		return "", err
	}
	return *name, nil
}

// Blocked stops every business reaching addr: the person blocked the bot.
func (b *Book) Blocked(ctx context.Context, channel, addr string) error {
	_, err := b.Pool.Exec(ctx, `SELECT taskiem_reach_blocked($1, $2)`, channel, b.AddressHash(channel, addr))
	return err
}

// Businesses are those reaching addr now, most recent first.
func (b *Book) Businesses(ctx context.Context, channel, addr string) ([]string, error) {
	rows, err := b.Pool.Query(ctx, `SELECT business FROM taskiem_reach_businesses($1, $2)`, channel, b.AddressHash(channel, addr))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// --- link codes ---

// CodeRe is a link code: 24 random bytes, base64url.
var CodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

// NewCode is a fresh code and its hash.
func NewCode() (string, []byte, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	return code, CodeHash(code), nil
}

// CodeHash is how a code is kept.
func CodeHash(code string) []byte {
	h := sha256.Sum256([]byte(code))
	return h[:]
}

// Payload is what a thank-you page's code carries, sealed with the public
// key for the api role: the phones typed in the submission, and the one
// the form verified with a code, if any.
type Payload struct {
	Phones []string `json:"p,omitempty"`
	Proven string   `json:"v,omitempty"`
}

// PayloadAAD binds a sealed payload to its code.
func PayloadAAD(codeHash []byte) string { return "reach code " + hex.EncodeToString(codeHash) }

// Taken is a code taken by a chat address.
type Taken struct {
	Hash      []byte
	Tenant    uuid.UUID
	Business  string
	Reference string
	Payload   []byte
}

// Take takes a live code for addr, once. false: no such live code.
func (b *Book) Take(ctx context.Context, code, channel, addr string) (Taken, bool, error) {
	t := Taken{Hash: CodeHash(code)}
	var ref *string
	err := b.Pool.QueryRow(ctx, `SELECT tenant_id, business, reference, payload FROM taskiem_reach_code_take($1, $2, $3)`,
		t.Hash, channel, b.AddressHash(channel, addr)).Scan(&t.Tenant, &t.Business, &ref, &t.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, false, nil
	}
	if ref != nil {
		t.Reference = *ref
	}
	return t, err == nil, err
}

// Pending is the code addr took in the last hour and has not finished.
func (b *Book) Pending(ctx context.Context, channel, addr string) (Taken, bool, error) {
	var t Taken
	var ref *string
	err := b.Pool.QueryRow(ctx, `SELECT code_hash, tenant_id, business, reference, payload FROM taskiem_reach_code_pending($1, $2)`,
		channel, b.AddressHash(channel, addr)).Scan(&t.Hash, &t.Tenant, &t.Business, &ref, &t.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, false, nil
	}
	if ref != nil {
		t.Reference = *ref
	}
	return t, err == nil, err
}

// Done finishes with a code.
func (b *Book) Done(ctx context.Context, hash []byte) error {
	_, err := b.Pool.Exec(ctx, `SELECT taskiem_reach_code_done($1)`, hash)
	return err
}

// PutCode makes a code for a business (a standing page's: no reference).
func (b *Book) PutCode(ctx context.Context, tenant uuid.UUID) (string, error) {
	code, hash, err := NewCode()
	if err != nil {
		return "", err
	}
	var ok bool
	if err := b.Pool.QueryRow(ctx, `SELECT taskiem_reach_code_put($1, $2, NULL, NULL)`, hash, tenant).Scan(&ok); err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("reach: no code now; try again later")
	}
	return code, nil
}

// SetTargets records where links point (the api role, at start): the
// bot's username and the platform number; "" clears one.
func SetTargets(ctx context.Context, pool *pgxpool.Pool, bot, number string) error {
	for _, t := range [][2]string{{Telegram, bot}, {WhatsApp, number}} {
		var err error
		if t[1] == "" {
			_, err = pool.Exec(ctx, `SELECT taskiem_reach_target_clear($1)`, t[0])
		} else {
			_, err = pool.Exec(ctx, `SELECT taskiem_reach_target_set($1, $2)`, t[0], t[1])
		}
		if err != nil {
			return fmt.Errorf("reach: recording where links point: %w", err)
		}
	}
	return nil
}

// --- the business's view (Settings > People you message) ---

// Contact is one person a business reaches, as its people see it.
type Contact struct {
	ID       uuid.UUID `json:"id"`
	Phone    string    `json:"phone"` // masked
	Channels []Channel `json:"channels"`
	Created  time.Time `json:"created_at"`
}

// Channel is one of a contact's links.
type Channel struct {
	Channel  string     `json:"channel"`
	LinkedAt time.Time  `json:"linked_at"`
	LastSent *time.Time `json:"last_sent_at,omitempty"`
	Stopped  *time.Time `json:"stopped_at,omitempty"`
}

// ListTx lists the business's contacts in tx, newest first.
func (b *Book) ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Contact, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.phone, c.created_at,
		  coalesce(jsonb_agg(jsonb_build_object('channel', ch.channel, 'linked_at', ch.linked_at, 'last_sent_at', ch.last_sent_at, 'stopped_at', ch.stopped_at)
		    ORDER BY ch.channel) FILTER (WHERE ch.channel IS NOT NULL), '[]')
		FROM reach_contacts c LEFT JOIN reach_channels ch ON ch.contact_id = c.id
		WHERE c.tenant_id = $1 GROUP BY c.id ORDER BY c.created_at DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	type row struct {
		c        Contact
		phone    []byte
		channels []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.c.ID, &x.phone, &x.c.Created, &x.channels)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(list))
	for _, x := range list {
		c := x.c
		if err := json.Unmarshal(x.channels, &c.Channels); err != nil {
			return nil, err
		}
		var env map[string]any
		_ = json.Unmarshal(x.phone, &env)
		c.Phone = pii.Erased
		if v, err := b.Vault.OpenTx(ctx, tx, tenant, env); err == nil {
			if p, ok := v.(string); ok {
				c.Phone = MaskPhone(p)
			}
		} else if !errors.Is(err, pii.ErrErased) {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// RemoveTx removes a contact; false when there was none.
func (b *Book) RemoveTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM reach_contacts WHERE id = $1`, id)
	return tag.RowsAffected() == 1, err
}

// MaskPhone shows a phone's country code and last three digits.
func MaskPhone(p string) string {
	if len(p) < 8 {
		return "•••"
	}
	return p[:4] + "•••••" + p[len(p)-3:]
}

// PageTx is the business's standing page's public id (/u/<id> on the
// form host), made the first time; fresh replaces it, and the old link
// stops working.
func PageTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, fresh bool) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT public_id FROM reach_pages WHERE tenant_id = $1`, tenant).Scan(&id)
	if err == nil && !fresh {
		return id, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if id, err = newPageID(); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO reach_pages (tenant_id, public_id) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET public_id = EXCLUDED.public_id, created_at = now()`, tenant, id)
	return id, err
}

// newPageID is 12 random lowercase letters and digits, as form ids are.
func newPageID() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

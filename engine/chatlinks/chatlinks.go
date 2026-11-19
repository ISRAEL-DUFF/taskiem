// Package chatlinks is a business's chat link (docs/spec/taskiem-for-
// everyone.md §8.2): https://<host>/m/{code}, which opens a customer
// conversation with the business in the Taskiem app, or in the phone's
// browser.
//
// A business has one live code: ten characters from an alphabet with no
// look-alikes (no 0 and o, 1, l and i, u and v). It is kept only as a keyed
// hash, with the random nonce it was made from: the code is HMAC(key,
// nonce) spelt in that alphabet, so the owner can read their link back and
// a copy of the database alone gives no one the codes. "Make a new link"
// replaces it; the old code then answers ErrReplaced for 30 days ("This
// link no longer works"), and nothing after that.
//
// Resolve is the one way from a code to its business, for every caller
// (the public card, the browser chat, the customer door): it answers only
// for an active business, never a workflows workspace.
package chatlinks

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Alphabet is what a code is made of: lower-case letters and digits with
// none that look like another (0 o, 1 l i, u v).
const Alphabet = "23456789abcdefghjkmnpqrstwxyz"

// Length is a code's length.
const Length = 10

// KeptFor is how long a replaced code still says the link no longer works.
const KeptFor = 30 * 24 * time.Hour

var (
	// ErrNotFound: no business has this code (or it was replaced more than
	// KeptFor ago, or the business is not active).
	ErrNotFound = errors.New("chatlinks: no such link")
	// ErrReplaced: the business made a new link; this one no longer works.
	ErrReplaced = errors.New("chatlinks: this link no longer works")
	// ErrOff: the server has no key for chat links (TASKIEM_MESSAGE_HASH_KEY).
	ErrOff = errors.New("chatlinks: chat links are not set up on this server")
)

// Resolver finds the business a chat code names. Links is the server's;
// the customer door and the browser chat take one.
type Resolver interface {
	Resolve(ctx context.Context, code string) (uuid.UUID, error)
}

// Links are the businesses' chat links.
type Links struct {
	Pool *pgxpool.Pool
	// Key is the platform's message hash key (TASKIEM_MESSAGE_HASH_KEY, 32
	// bytes): codes are made and hashed with keys derived from it.
	Key []byte
	// Base is where links are served: the web app's address
	// (TASKIEM_PUBLIC_URL), without a trailing slash.
	Base string
}

// Ready reports whether chat links can be made and resolved.
func (l *Links) Ready() bool { return l != nil && l.Pool != nil && len(l.Key) == 32 }

func (l *Links) derive(purpose string) []byte {
	m := hmac.New(sha256.New, l.Key)
	m.Write([]byte("taskiem chat links\x00" + purpose))
	return m.Sum(nil)
}

// Hash is how a code is kept and looked up.
func (l *Links) Hash(code string) []byte {
	m := hmac.New(sha256.New, l.derive("hash"))
	m.Write([]byte(code))
	return m.Sum(nil)
}

// codeOf spells the code a nonce makes: HMAC(key, nonce) read as a
// stream of bytes, each taken only when it falls evenly on the alphabet.
func (l *Links) codeOf(nonce []byte) string {
	k := l.derive("code")
	out := make([]byte, 0, Length)
	limit := byte(256 - 256%len(Alphabet))
	for block := uint32(0); len(out) < Length; block++ {
		m := hmac.New(sha256.New, k)
		m.Write(nonce)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], block)
		m.Write(b[:])
		for _, x := range m.Sum(nil) {
			if x >= limit {
				continue
			}
			out = append(out, Alphabet[int(x)%len(Alphabet)])
			if len(out) == Length {
				break
			}
		}
	}
	return string(out)
}

// Clean reads a typed or pasted code: lower case, trimmed; "" when it
// cannot be a code.
func Clean(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) != Length {
		return ""
	}
	for i := 0; i < len(code); i++ {
		if strings.IndexByte(Alphabet, code[i]) < 0 {
			return ""
		}
	}
	return code
}

// URL is a code's link.
func (l *Links) URL(code string) string { return strings.TrimSuffix(l.Base, "/") + "/m/" + code }

// Link is a business's chat link as its owner sees it.
type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// CurrentTx is the business's live link, made the first time it is asked
// for; tx is in the business's scope.
func (l *Links) CurrentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) (Link, error) {
	if !l.Ready() {
		return Link{}, ErrOff
	}
	var nonce []byte
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT nonce, created_at FROM customer_chat_codes WHERE tenant_id = $1 AND replaced_at IS NULL`, tenant).Scan(&nonce, &at)
	if err == nil {
		c := l.codeOf(nonce)
		return Link{Code: c, URL: l.URL(c), CreatedAt: at}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Link{}, err
	}
	return l.make(ctx, tx, tenant, by)
}

// ReplaceTx makes the business a new link ("Make a new link"); the old
// one says the link no longer works for KeptFor. Codes replaced longer ago
// are deleted.
func (l *Links) ReplaceTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) (Link, error) {
	if !l.Ready() {
		return Link{}, ErrOff
	}
	if _, err := tx.Exec(ctx, `DELETE FROM customer_chat_codes WHERE tenant_id = $1 AND replaced_at < now() - $2::interval`, tenant, KeptFor.String()); err != nil {
		return Link{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_chat_codes SET replaced_at = now() WHERE tenant_id = $1 AND replaced_at IS NULL`, tenant); err != nil {
		return Link{}, err
	}
	return l.make(ctx, tx, tenant, by)
}

// make writes a new live code, trying again on the rare code another
// business already has.
func (l *Links) make(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) (Link, error) {
	for try := 0; try < 5; try++ {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return Link{}, err
		}
		code := l.codeOf(nonce)
		var at time.Time
		// A savepoint, so a clash leaves the transaction usable.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return Link{}, err
		}
		err = sp.QueryRow(ctx, `INSERT INTO customer_chat_codes (id, tenant_id, code_hash, nonce, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
			uuid.Must(uuid.NewV7()), tenant, l.Hash(code), nonce, by).Scan(&at)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			if rerr := sp.Rollback(ctx); rerr != nil {
				return Link{}, rerr
			}
			if pe.ConstraintName == "customer_chat_codes_live" {
				// Made at the same moment by another request: that one.
				var n []byte
				if err := tx.QueryRow(ctx, `SELECT nonce, created_at FROM customer_chat_codes WHERE tenant_id = $1 AND replaced_at IS NULL`, tenant).Scan(&n, &at); err != nil {
					return Link{}, err
				}
				c := l.codeOf(n)
				return Link{Code: c, URL: l.URL(c), CreatedAt: at}, nil
			}
			continue
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			return Link{}, err
		}
		if err := sp.Commit(ctx); err != nil {
			return Link{}, err
		}
		return Link{Code: code, URL: l.URL(code), CreatedAt: at}, nil
	}
	return Link{}, errors.New("chatlinks: no free code")
}

// Resolve is the business a code names: ErrReplaced for a code replaced
// within KeptFor, ErrNotFound for any other that names no active
// business.
func (l *Links) Resolve(ctx context.Context, code string) (uuid.UUID, error) {
	if !l.Ready() {
		return uuid.Nil, ErrOff
	}
	code = Clean(code)
	if code == "" {
		return uuid.Nil, ErrNotFound
	}
	var tenant uuid.UUID
	var replaced *time.Time
	err := l.Pool.QueryRow(ctx, `SELECT tenant_id, replaced_at FROM taskiem_chat_code_find($1)`, l.Hash(code)).Scan(&tenant, &replaced)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, ErrNotFound
	case err != nil:
		return uuid.Nil, err
	case replaced != nil:
		return uuid.Nil, ErrReplaced
	}
	return tenant, nil
}

// LinkFor is the business's live link's address, made when it has none;
// "" when chat links are off. For the words that carry it (updates, the
// storefront, the SMS).
func (l *Links) LinkFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	if !l.Ready() {
		return "", nil
	}
	k, err := l.CurrentTx(ctx, tx, tenant, "system")
	if err != nil {
		return "", fmt.Errorf("chat link: %w", err)
	}
	return k.URL, nil
}

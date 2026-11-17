package people

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
)

// A person's link (§5.2): https://<host>/p/{code}, made as a business's
// chat link is (engine/chatlinks): one live code per person, ten letters
// of chatlinks.Alphabet, kept only as a keyed hash with the nonce it was
// spelt from (so the person can read their link back, and a copy of the
// database alone gives no one the codes). Replacing it leaves the old code
// answering ErrLinkReplaced for LinkKeptFor, then nothing. The keys differ
// from chat links', so a code is a person's or a business's, never both.

// LinkKeptFor is how long a replaced code still says the link no longer
// works.
const LinkKeptFor = chatlinks.KeptFor

var (
	// ErrLinkNotFound: no person has this code (or it was replaced more
	// than LinkKeptFor ago, or the account is not active).
	ErrLinkNotFound = errors.New("people: no such link")
	// ErrLinkReplaced: the person made a new link; this one no longer
	// works.
	ErrLinkReplaced = errors.New("people: this link no longer works")
)

// Link is a person's link as they see it.
type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

func (p *People) linkHash(code string) []byte {
	m := hmac.New(sha256.New, p.derive("link hash"))
	m.Write([]byte(code))
	return m.Sum(nil)
}

// codeOf spells the code a nonce makes, as chatlinks does.
func (p *People) codeOf(nonce []byte) string {
	k := p.derive("link code")
	a := chatlinks.Alphabet
	out := make([]byte, 0, chatlinks.Length)
	limit := byte(256 - 256%len(a))
	for block := uint32(0); len(out) < chatlinks.Length; block++ {
		m := hmac.New(sha256.New, k)
		m.Write(nonce)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], block)
		m.Write(b[:])
		for _, x := range m.Sum(nil) {
			if x >= limit {
				continue
			}
			out = append(out, a[int(x)%len(a)])
			if len(out) == chatlinks.Length {
				break
			}
		}
	}
	return string(out)
}

// LinkURL is a code's link.
func (p *People) LinkURL(code string) string { return strings.TrimSuffix(p.Base, "/") + "/p/" + code }

// LinkOf is the person's live link, made the first time it is asked for.
func (p *People) LinkOf(ctx context.Context, person uuid.UUID) (Link, error) {
	if !p.Keyed() {
		return Link{}, ErrOff
	}
	var out Link
	err := db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		var nonce []byte
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT nonce, created_at FROM person_links WHERE person_id = $1 AND replaced_at IS NULL`, person).Scan(&nonce, &at)
		if err == nil {
			c := p.codeOf(nonce)
			out = Link{Code: c, URL: p.LinkURL(c), CreatedAt: at}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		out, err = p.makeLink(ctx, tx, person)
		return err
	})
	return out, err
}

// ReplaceLink makes the person a new link ("Make a new link"); the old one
// says the link no longer works for LinkKeptFor. Codes replaced longer ago
// are deleted.
func (p *People) ReplaceLink(ctx context.Context, person uuid.UUID) (Link, error) {
	if !p.Keyed() {
		return Link{}, ErrOff
	}
	var out Link
	err := db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM person_links WHERE person_id = $1 AND replaced_at < $2`, person, p.now().Add(-LinkKeptFor)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE person_links SET replaced_at = $2 WHERE person_id = $1 AND replaced_at IS NULL`, person, p.now()); err != nil {
			return err
		}
		var err error
		out, err = p.makeLink(ctx, tx, person)
		return err
	})
	return out, err
}

// makeLink writes a new live code, trying again on the rare code another
// person already has.
func (p *People) makeLink(ctx context.Context, tx pgx.Tx, person uuid.UUID) (Link, error) {
	for try := 0; try < 5; try++ {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return Link{}, err
		}
		code := p.codeOf(nonce)
		sp, err := tx.Begin(ctx)
		if err != nil {
			return Link{}, err
		}
		var at time.Time
		err = sp.QueryRow(ctx, `INSERT INTO person_links (id, person_id, code_hash, nonce) VALUES ($1, $2, $3, $4) RETURNING created_at`,
			uuid.Must(uuid.NewV7()), person, p.linkHash(code), nonce).Scan(&at)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			if rerr := sp.Rollback(ctx); rerr != nil {
				return Link{}, rerr
			}
			if pe.ConstraintName == "person_links_live" {
				// Made at the same moment by another request: that one.
				var n []byte
				if err := tx.QueryRow(ctx, `SELECT nonce, created_at FROM person_links WHERE person_id = $1 AND replaced_at IS NULL`, person).Scan(&n, &at); err != nil {
					return Link{}, err
				}
				c := p.codeOf(n)
				return Link{Code: c, URL: p.LinkURL(c), CreatedAt: at}, nil
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
		return Link{Code: code, URL: p.LinkURL(code), CreatedAt: at}, nil
	}
	return Link{}, errors.New("people: no free link code")
}

// Resolve is the person a link's code names (chatlinks.Resolver's shape,
// for stream A's chats): ErrLinkReplaced for a code replaced within
// LinkKeptFor, ErrLinkNotFound for any other that names no active
// account, ErrOff without a key.
func (p *People) Resolve(ctx context.Context, code string) (uuid.UUID, error) {
	c, err := p.PublicCardOf(ctx, code)
	return c.Person, err
}

// PublicCard is what anyone opening a person's link sees: the name, and
// the photo only when the person shows it to everyone.
type PublicCard struct {
	Person   uuid.UUID `json:"-"`
	Name     string    `json:"name"`
	HasPhoto bool      `json:"-"`
}

// PublicCardOf is the card for anyone of the person a code names, with
// Resolve's refusals.
func (p *People) PublicCardOf(ctx context.Context, code string) (PublicCard, error) {
	if !p.Keyed() {
		return PublicCard{}, ErrOff
	}
	code = chatlinks.Clean(code)
	if code == "" {
		return PublicCard{}, ErrLinkNotFound
	}
	var out PublicCard
	var replaced *time.Time
	err := p.Pool.QueryRow(ctx, `SELECT person_id, replaced_at, name, photo_public FROM taskiem_person_link_find($1)`, p.linkHash(code)).
		Scan(&out.Person, &replaced, &out.Name, &out.HasPhoto)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return PublicCard{}, ErrLinkNotFound
	case err != nil:
		return PublicCard{}, err
	case replaced != nil && p.now().Sub(*replaced) > LinkKeptFor:
		return PublicCard{}, ErrLinkNotFound
	case replaced != nil:
		return PublicCard{}, ErrLinkReplaced
	}
	return out, nil
}

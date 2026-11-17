package people

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Limits against looking people up many at a time (§5.1, §5.3), counted
// per person per day in the database (people_find_counts); the API adds
// limits per address.
const (
	// NumberLookupsPerDay is how many numbers a person may look up a day.
	NumberLookupsPerDay = 50
	// MatchBatch is the most hashes one contact-matching request carries.
	MatchBatch = 500
	// MatchHashesPerDay is how many hashes a person may send a day (an
	// address book of a few thousand, checked once a day).
	MatchHashesPerDay = 5000
	// MatchBatchesPerDay is how many contact-matching requests a person
	// may send a day.
	MatchBatchesPerDay = 20
)

// count adds n to the person's count of kind today and reports whether it
// stays within max; a refused request still counts what it asked for, so
// retrying does not help. tx is in the person's scope.
func (p *People) countTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, kind string, n, max int) (bool, error) {
	var total int
	err := tx.QueryRow(ctx, `INSERT INTO people_find_counts (person_id, kind, day, n) VALUES ($1, $2, ($3::timestamptz AT TIME ZONE 'Africa/Lagos')::date, $4)
		ON CONFLICT (person_id, kind, day) DO UPDATE SET n = people_find_counts.n + EXCLUDED.n RETURNING n`, person, kind, p.now(), n).Scan(&total)
	if err != nil {
		return false, err
	}
	// Yesterday's and older counts are no longer needed.
	if _, err := tx.Exec(ctx, `DELETE FROM people_find_counts WHERE person_id = $1 AND day < ($2::timestamptz AT TIME ZONE 'Africa/Lagos')::date - 1`, person, p.now()); err != nil {
		return false, err
	}
	return total <= max, nil
}

// Found is a person found by number, link or contact matching, as the
// finder may see them, with a token to add them to their contacts.
type Found struct {
	Card
	// Self: the finder found themself.
	Self bool `json:"self"`
	// AddToken lets the finder add them to their contacts (a day).
	AddToken string `json:"add_token,omitempty"`
}

// FindByNumber is the person whose verified phone is e164 (E.164, read by
// engine/phone) when their "find me by my number" lets viewer find them;
// ErrNotFound otherwise (no account, or one hidden from the viewer: the
// same answer), ErrLimit past NumberLookupsPerDay. A block hides them.
func (p *People) FindByNumber(ctx context.Context, viewer uuid.UUID, e164 string) (Found, error) {
	var out Found
	// The count is kept whatever the answer: the transaction commits, and
	// the answer is given after.
	var answer error
	err := db.InPersonTx(ctx, p.Pool, viewer, func(tx pgx.Tx) error {
		ok, err := p.countTx(ctx, tx, viewer, "number", 1, NumberLookupsPerDay)
		if err != nil {
			return err
		}
		if !ok {
			answer = ErrLimit
			return nil
		}
		var id *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT taskiem_people_find_number($1, $2)`, viewer, e164).Scan(&id); err != nil {
			return err
		}
		if id == nil {
			answer = ErrNotFound
			return nil
		}
		b, err := p.blocked(ctx, viewer, *id)
		if err != nil {
			return err
		}
		if b {
			answer = ErrNotFound
			return nil
		}
		cards, err := p.CardsTx(ctx, tx, viewer, []uuid.UUID{*id})
		if err != nil {
			return err
		}
		if len(cards) == 0 || cards[0].Deleted {
			answer = ErrNotFound
			return nil
		}
		out = Found{Card: cards[0], Self: *id == viewer}
		return nil
	})
	if err == nil {
		err = answer
	}
	if err != nil {
		return Found{}, err
	}
	if !out.Self {
		out.AddToken = p.AddToken(viewer, out.ID)
	}
	return out, nil
}

// --- add tokens ---

// addTokenFor is how long an add token lasts.
const addTokenFor = 24 * time.Hour

func (p *People) derive(purpose string) []byte {
	m := hmac.New(sha256.New, p.Key)
	m.Write([]byte("taskiem people\x00" + purpose))
	return m.Sum(nil)
}

func (p *People) addMAC(viewer, person uuid.UUID, until int64) []byte {
	m := hmac.New(sha256.New, p.derive("add"))
	m.Write(viewer[:])
	m.Write(person[:])
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(until)) //nolint:gosec // a time in seconds
	m.Write(b[:])
	return m.Sum(nil)[:16]
}

// AddToken is the proof that viewer found person (by number, link or
// contact matching) and may add them to their contacts, for a day; ""
// when the server has no key.
func (p *People) AddToken(viewer, person uuid.UUID) string {
	if !p.Keyed() {
		return ""
	}
	until := p.now().Add(addTokenFor).Unix()
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(until)) //nolint:gosec // a time in seconds
	return base64.RawURLEncoding.EncodeToString(append(b[:], p.addMAC(viewer, person, until)...))
}

// CheckAddToken reports whether token is viewer's proof for person, still
// good.
func (p *People) CheckAddToken(viewer, person uuid.UUID, token string) bool {
	if !p.Keyed() {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 24 {
		return false
	}
	until := int64(binary.BigEndian.Uint64(raw[:8])) //nolint:gosec // a time in seconds
	if p.now().Unix() > until {
		return false
	}
	return hmac.Equal(raw[8:], p.addMAC(viewer, person, until))
}

// --- contact matching (§5.3) ---

// KeyPeriod is how long a contacts key is given out; KeyGrace is how long
// after it is replaced hashes made with it are still answered.
const (
	KeyPeriod = 7 * 24 * time.Hour
	KeyGrace  = 48 * time.Hour
)

// HashPrefix is what the app puts before each number: a hash is
// SHA-256(key || "taskiem-contacts:" || E.164), sent as 64 hex digits.
const HashPrefix = "taskiem-contacts:"

// ContactsKey is the key the app hashes its contacts' numbers with.
type ContactsKey struct {
	// ID names the key's week; the app sends it back with its hashes.
	ID  string `json:"key_id"`
	Key []byte `json:"-"`
	// ExpiresAt is when a new key is given out; hashes made with this one
	// are answered until KeyGrace after it.
	ExpiresAt time.Time `json:"expires_at"`
}

func (p *People) keyOf(period int64) []byte {
	return p.derive("contacts key " + strconv.FormatInt(period, 10))
}

// CurrentContactsKey is this week's contacts key.
func (p *People) CurrentContactsKey() (ContactsKey, error) {
	if !p.Keyed() {
		return ContactsKey{}, ErrOff
	}
	period := p.now().Unix() / int64(KeyPeriod/time.Second)
	return ContactsKey{ID: "k" + strconv.FormatInt(period, 10), Key: p.keyOf(period), ExpiresAt: time.Unix((period+1)*int64(KeyPeriod/time.Second), 0).UTC()}, nil
}

// contactsKeyByID is the key an id names: this week's, or last week's
// within KeyGrace of its replacement.
func (p *People) contactsKeyByID(id string) ([]byte, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "k"), 10, 64)
	if err != nil || !strings.HasPrefix(id, "k") {
		return nil, false
	}
	secs := int64(KeyPeriod / time.Second)
	now := p.now().Unix()
	current := now / secs
	switch {
	case n == current:
		return p.keyOf(n), true
	case n == current-1 && now-current*secs < int64(KeyGrace/time.Second):
		return p.keyOf(n), true
	}
	return nil, false
}

// ContactHash is the hash the app makes of a number with a contacts key
// (for tests, and for the app's reference).
func ContactHash(key []byte, e164 string) []byte {
	h := sha256.New()
	h.Write(key)
	h.Write([]byte(HashPrefix + e164))
	return h.Sum(nil)
}

var (
	// ErrOldKey: the key id is neither this week's nor last week's in its
	// grace; the app fetches the key again.
	ErrOldKey = errors.New("people: the contacts key has changed")
	// ErrBatch: too many hashes, or one that is not 64 hex digits.
	ErrBatch = fmt.Errorf("%w: at most %d hashes of 64 hex digits", ErrInvalid, MatchBatch)
)

// Match is one of the hashes sent that is a person who lets themselves be
// found from contacts.
type Match struct {
	Hash string `json:"hash"`
	Found
}

// MatchContacts answers which of hashes (hex SHA-256 of the numbers in the
// viewer's phone, made with the contacts key keyID) are people who let
// themselves be found from contacts, each as viewer may see them. Nothing
// of the request is kept: the hashes are compared in one query and
// forgotten, and only how many were sent is counted. ErrOldKey for a key
// no longer answered, ErrBatch, ErrLimit past the day's limits.
func (p *People) MatchContacts(ctx context.Context, viewer uuid.UUID, keyID string, hashes []string) ([]Match, error) {
	if !p.Keyed() {
		return nil, ErrOff
	}
	key, ok := p.contactsKeyByID(keyID)
	if !ok {
		return nil, ErrOldKey
	}
	if len(hashes) > MatchBatch {
		return nil, ErrBatch
	}
	raw := make([][]byte, 0, len(hashes))
	byRaw := map[string]string{}
	for _, h := range hashes {
		h = strings.ToLower(strings.TrimSpace(h))
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != sha256.Size {
			return nil, ErrBatch
		}
		if _, dup := byRaw[string(b)]; !dup {
			raw = append(raw, b)
			byRaw[string(b)] = h
		}
	}
	out := []Match{}
	limited := false
	err := db.InPersonTx(ctx, p.Pool, viewer, func(tx pgx.Tx) error {
		okB, err := p.countTx(ctx, tx, viewer, "contacts_batch", 1, MatchBatchesPerDay)
		if err != nil {
			return err
		}
		okH, err := p.countTx(ctx, tx, viewer, "contacts", len(raw), MatchHashesPerDay)
		if err != nil {
			return err
		}
		if !okB || !okH {
			limited = true
			return nil
		}
		if len(raw) == 0 {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT hash, person_id FROM taskiem_people_match($1, $2, $3)`, viewer, key, raw)
		if err != nil {
			return err
		}
		type hit struct {
			hash []byte
			id   uuid.UUID
		}
		hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hit, error) {
			var x hit
			return x, r.Scan(&x.hash, &x.id)
		})
		if err != nil || len(hits) == 0 {
			return err
		}
		ids := make([]uuid.UUID, len(hits))
		for i, x := range hits {
			ids[i] = x.id
		}
		cards, err := cardsTx(ctx, tx, viewer, ids)
		if err != nil {
			return err
		}
		blocked, err := p.blockedSetTx(ctx, tx, viewer, ids)
		if err != nil {
			return err
		}
		for _, x := range hits {
			c, ok := cards[x.id]
			if !ok || c.Deleted || blocked[x.id] {
				continue
			}
			out = append(out, Match{Hash: byRaw[string(x.hash)], Found: Found{Card: *c, AddToken: p.AddToken(viewer, x.id)}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if limited {
		return nil, ErrLimit
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out, nil
}

// sortContacts orders a contact list by the name shown (the name the
// person gave, else the contact's own), case aside.
func sortContacts(list []Contact) {
	shown := func(c Contact) string {
		if c.ContactName != nil {
			return strings.ToLower(*c.ContactName)
		}
		return strings.ToLower(c.Name)
	}
	sort.SliceStable(list, func(i, j int) bool { return shown(list[i]) < shown(list[j]) })
}

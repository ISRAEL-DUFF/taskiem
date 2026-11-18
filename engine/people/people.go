// Package people is how people find each other on Taskiem, and what each
// may see of the other (docs/spec/people-chatting.md §5, §8.4, §14;
// decision 0069; migration 00475): a person's privacy settings towards
// other people, their contacts, finding by number, a person's link,
// contact matching by keyed hashes, last seen and online, and the
// automated checks on strangers' messages.
//
// The chats between people (stream A) ask it through PeoplePolicy's
// methods: MayMessage, IsContact, NumberShownTo, ReadTicks and
// ScreenStranger, and resolve a person's link with Resolve.
//
// Rules, as built:
//   - "My contacts" in a setting is the owner's own list: the people the
//     owner added (from a lookup, a link, contact matching or a chat), not
//     whether those people added the owner back.
//   - Last seen and online are hidden both ways: a viewer sees an owner's
//     only when the owner's setting lets the viewer see it and the viewer's
//     own setting lets the owner see theirs. Read ticks likewise go between
//     two people only when both have them on.
//   - A person's photo follows the photo setting only (not both ways).
//   - A person's number is shown to another only when they chose to show
//     it, to people they chat with (Chatting) or added to their contacts,
//     and never across a block.
//
// Every read of another person goes through a definer that answers for
// the person in scope only (db.InPersonTx), so a request by one person
// never runs in another's scope.
package people

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Audience is who a setting lets in.
type Audience string

const (
	Everyone Audience = "everyone"
	Contacts Audience = "contacts"
	Nobody   Audience = "nobody"
)

func (a Audience) valid() bool { return a == Everyone || a == Contacts || a == Nobody }

var (
	// ErrInvalid is a setting, name or request the rules refuse.
	ErrInvalid = errors.New("people: not valid")
	// ErrNotFound: no such person (or not one this viewer may reach).
	ErrNotFound = errors.New("people: no such person")
	// ErrLimit: a limit against looking people up many at a time.
	ErrLimit = errors.New("people: too many lookups")
	// ErrOff: the server has no key (TASKIEM_MESSAGE_HASH_KEY) for links,
	// contact matching or add tokens.
	ErrOff = errors.New("people: not set up on this server")
)

// People is the server's: the database and the platform key.
type People struct {
	Pool *pgxpool.Pool
	// Key is TASKIEM_MESSAGE_HASH_KEY (32 bytes): links, the contacts keys
	// and add tokens are made with keys derived from it. Without it,
	// links, contact matching and adding contacts are off.
	Key []byte
	// Base is where links are served (TASKIEM_PUBLIC_URL), without a
	// trailing slash.
	Base string
	// Blocked reports whether either of a and b blocked the other (the
	// people chats' blocks). a is the person the request is for, so it is
	// answered in a's scope. Nil: no person blocks.
	Blocked func(ctx context.Context, a, b uuid.UUID) (bool, error)
	// BlockedSet is Blocked for many at once, in tx (viewer's scope): the
	// others either blocked or blocked by viewer. Nil: Blocked one by one.
	BlockedSet func(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, others []uuid.UUID) (map[uuid.UUID]bool, error)
	// Chatting reports whether a and b have a people chat that is no
	// longer a request, in a's scope as Blocked. Nil: none.
	Chatting func(ctx context.Context, a, b uuid.UUID) (bool, error)
	// ChattingTx is Chatting in tx (a's scope), so a caller inside a
	// transaction opens no other connection. Nil: Chatting.
	ChattingTx func(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error)
	// Now is the clock (tests); time.Now when nil.
	Now func() time.Time
}

// Ready reports whether the settings, contacts and lookups work.
func (p *People) Ready() bool { return p != nil && p.Pool != nil }

// Keyed reports whether links, contact matching and add tokens work.
func (p *People) Keyed() bool { return p.Ready() && len(p.Key) == 32 }

func (p *People) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *People) blocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if p.Blocked == nil || a == b {
		return false, nil
	}
	return p.Blocked(ctx, a, b)
}

// blockedTx is blocked in tx (a's scope): BlockedSet when set, so no
// other connection is opened, else Blocked.
func (p *People) blockedTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	if a == b {
		return false, nil
	}
	if p.BlockedSet != nil {
		set, err := p.BlockedSet(ctx, tx, a, []uuid.UUID{b})
		return set[b], err
	}
	return p.blocked(ctx, a, b)
}

// chattingTx is Chatting in tx (a's scope): ChattingTx when set, else
// Chatting; false without either.
func (p *People) chattingTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	switch {
	case a == b:
		return false, nil
	case p.ChattingTx != nil:
		return p.ChattingTx(ctx, tx, a, b)
	case p.Chatting != nil:
		return p.Chatting(ctx, a, b)
	}
	return false, nil
}

// blockedSetTx is which of others a block stands between viewer and;
// tx is in viewer's scope.
func (p *People) blockedSetTx(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, others []uuid.UUID) (map[uuid.UUID]bool, error) {
	if p.BlockedSet != nil {
		return p.BlockedSet(ctx, tx, viewer, others)
	}
	out := map[uuid.UUID]bool{}
	for _, id := range others {
		b, err := p.blocked(ctx, viewer, id)
		if err != nil {
			return nil, err
		}
		if b {
			out[id] = true
		}
	}
	return out, nil
}

// --- settings ---

// Settings are a person's privacy settings towards other people (§5.4).
type Settings struct {
	// FindByNumber: who finds me by typing my number.
	FindByNumber Audience `json:"find_by_number"`
	// FindFromContacts: whether people who have my number in their phone's
	// contacts find me by contact matching.
	FindFromContacts bool `json:"find_from_contacts"`
	// Message: who can message me. Everyone's first message waits as a
	// request unless I added them; contacts: only people I added; nobody:
	// no one can start a chat with me (chats I accepted go on).
	Message Audience `json:"message"`
	// LastSeen: who sees my last seen and online (both ways).
	LastSeen Audience `json:"last_seen"`
	// ReadTicks: off means others do not see that I read, and I do not
	// see theirs.
	ReadTicks bool `json:"read_ticks"`
	// Photo: who sees my profile photo.
	Photo Audience `json:"photo"`
	// ShowNumber: show my number to people I chat with.
	ShowNumber bool `json:"show_number"`
}

// Defaults are the settings of a person who changed none (§5.4, §17).
func Defaults() Settings {
	return Settings{FindByNumber: Contacts, FindFromContacts: true, Message: Everyone, LastSeen: Contacts, ReadTicks: true, Photo: Contacts, ShowNumber: false}
}

// SettingsIn changes some settings; nil leaves one as it is.
type SettingsIn struct {
	FindByNumber     *Audience `json:"find_by_number"`
	FindFromContacts *bool     `json:"find_from_contacts"`
	Message          *Audience `json:"message"`
	LastSeen         *Audience `json:"last_seen"`
	ReadTicks        *bool     `json:"read_ticks"`
	Photo            *Audience `json:"photo"`
	ShowNumber       *bool     `json:"show_number"`
}

// SettingsTx is the person's settings; tx is in their own scope.
func SettingsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (Settings, error) {
	s := Defaults()
	err := tx.QueryRow(ctx, `SELECT find_by_number, find_from_contacts, message, last_seen, read_ticks, photo, show_number
		FROM person_privacy WHERE person_id = $1`, person).Scan(&s.FindByNumber, &s.FindFromContacts, &s.Message, &s.LastSeen, &s.ReadTicks, &s.Photo, &s.ShowNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	return s, err
}

// SettingsOf is the person's settings.
func (p *People) SettingsOf(ctx context.Context, person uuid.UUID) (Settings, error) {
	var s Settings
	err := db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		var err error
		s, err = SettingsTx(ctx, tx, person)
		return err
	})
	return s, err
}

// SaveSettings changes the person's settings and answers them all;
// ErrInvalid for an audience that is not one.
func (p *People) SaveSettings(ctx context.Context, person uuid.UUID, in SettingsIn) (Settings, error) {
	for _, a := range []*Audience{in.FindByNumber, in.Message, in.LastSeen, in.Photo} {
		if a != nil && !a.valid() {
			return Settings{}, ErrInvalid
		}
	}
	var s Settings
	err := db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		var err error
		if s, err = SettingsTx(ctx, tx, person); err != nil {
			return err
		}
		set := func(dst *Audience, v *Audience) {
			if v != nil {
				*dst = *v
			}
		}
		setB := func(dst *bool, v *bool) {
			if v != nil {
				*dst = *v
			}
		}
		set(&s.FindByNumber, in.FindByNumber)
		setB(&s.FindFromContacts, in.FindFromContacts)
		set(&s.Message, in.Message)
		set(&s.LastSeen, in.LastSeen)
		setB(&s.ReadTicks, in.ReadTicks)
		set(&s.Photo, in.Photo)
		setB(&s.ShowNumber, in.ShowNumber)
		_, err = tx.Exec(ctx, `INSERT INTO person_privacy (person_id, find_by_number, find_from_contacts, message, last_seen, read_ticks, photo, show_number, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (person_id) DO UPDATE SET find_by_number = EXCLUDED.find_by_number, find_from_contacts = EXCLUDED.find_from_contacts,
			  message = EXCLUDED.message, last_seen = EXCLUDED.last_seen, read_ticks = EXCLUDED.read_ticks, photo = EXCLUDED.photo,
			  show_number = EXCLUDED.show_number, updated_at = now()`,
			person, s.FindByNumber, s.FindFromContacts, s.Message, s.LastSeen, s.ReadTicks, s.Photo, s.ShowNumber)
		return err
	})
	return s, err
}

// --- what one person may see of another ---

// Card is what a viewer may see of a person, by that person's settings.
type Card struct {
	ID uuid.UUID `json:"id"`
	// Deleted: the account can't be reached: deleted (shown as "Deleted
	// account") or, with Suspended, suspended.
	Deleted bool `json:"deleted"`
	// Suspended: the account is not active but was not deleted (it can't
	// get messages right now); it keeps its name and shows nothing else.
	Suspended bool `json:"suspended"`
	// Name is the person's own name; empty for a deleted account.
	Name string `json:"name"`
	// ContactName is the name the viewer gave them in their contacts.
	ContactName *string `json:"contact_name"`
	// InContacts: the viewer added them. TheyAddedYou: they added the
	// viewer (what "My contacts" in their settings means).
	InContacts   bool `json:"in_contacts"`
	TheyAddedYou bool `json:"they_added_you"`
	// PhotoAt is when their photo changed, when they have one and their
	// setting lets the viewer see it.
	PhotoAt *time.Time `json:"-"`
	// LastSeenAt and Online, when both settings let them be seen.
	LastSeenAt *time.Time `json:"last_seen_at"`
	Online     *bool      `json:"online"`
	// ReadTicks: read ticks go between them (both have them on).
	ReadTicks bool `json:"read_ticks"`
	// Message is their "who can message me".
	Message Audience `json:"-"`
	// number is their number when they chose to show it; Number decides
	// whether this viewer is shown it.
	number *string
}

// cardsTx reads cards for the viewer; tx is in the viewer's scope (or,
// for one owner, the owner's).
func cardsTx(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, owners []uuid.UUID) (map[uuid.UUID]*Card, error) {
	out := map[uuid.UUID]*Card{}
	if len(owners) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT person_id, active, deleted, name, owner_has_viewer, viewer_has_owner, viewer_named, message,
		photo_at, last_seen_at, online, read_ticks, number FROM taskiem_people_cards($1, $2)`, viewer, owners)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Card
		var active, deleted bool
		if err := rows.Scan(&c.ID, &active, &deleted, &c.Name, &c.TheyAddedYou, &c.InContacts, &c.ContactName, &c.Message,
			&c.PhotoAt, &c.LastSeenAt, &c.Online, &c.ReadTicks, &c.number); err != nil {
			return nil, err
		}
		c.Deleted = !active
		c.Suspended = !active && !deleted
		out[c.ID] = &c
	}
	return out, rows.Err()
}

// CardsTx are the cards of owners as viewer sees them, in viewer's scope
// (tx), in the order asked; ids that name no account are left out. A
// block between them hides the photo, last seen and online.
func (p *People) CardsTx(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, owners []uuid.UUID) ([]Card, error) {
	got, err := cardsTx(ctx, tx, viewer, owners)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, len(got))
	ids := make([]uuid.UUID, 0, len(got))
	for id := range got {
		if id != viewer {
			ids = append(ids, id)
		}
	}
	blocked, err := p.blockedSetTx(ctx, tx, viewer, ids)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range owners {
		c, ok := got[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if blocked[id] {
			c.PhotoAt, c.LastSeenAt, c.Online = nil, nil, nil
		}
		out = append(out, *c)
	}
	return out, nil
}

// CardOf is one person as viewer sees them; ErrNotFound when the id names
// no account.
func (p *People) CardOf(ctx context.Context, viewer, owner uuid.UUID) (Card, error) {
	var out []Card
	err := db.InPersonTx(ctx, p.Pool, viewer, func(tx pgx.Tx) error {
		var err error
		out, err = p.CardsTx(ctx, tx, viewer, []uuid.UUID{owner})
		return err
	})
	if err != nil {
		return Card{}, err
	}
	if len(out) == 0 {
		return Card{}, ErrNotFound
	}
	return out[0], nil
}

// Photo is a person's photo when viewer may see it (viewer uuid.Nil: the
// public, everyone's only); ErrNotFound otherwise.
func (p *People) Photo(ctx context.Context, viewer, owner uuid.UUID) (ctype string, data []byte, at time.Time, err error) {
	q := func(tx pgx.Tx) error {
		var v *uuid.UUID
		if viewer != uuid.Nil {
			v = &viewer
		}
		return tx.QueryRow(ctx, `SELECT type, data, updated_at FROM taskiem_people_photo($1, $2)`, v, owner).Scan(&ctype, &data, &at)
	}
	if viewer == uuid.Nil {
		err = pgx.BeginFunc(ctx, p.Pool, q)
	} else {
		var b bool
		if b, err = p.blocked(ctx, viewer, owner); err != nil {
			return
		}
		if b {
			return "", nil, time.Time{}, ErrNotFound
		}
		err = db.InPersonTx(ctx, p.Pool, viewer, q)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// --- contacts ---

// Contact sources: how a person came to be added.
const (
	FromNumber   = "number"
	FromLink     = "link"
	FromContacts = "contacts"
	FromChat     = "chat"
)

// MaxContacts is how many contacts a person may keep.
const MaxContacts = 5000

// Contact is one of a person's contacts, as their list shows it.
type Contact struct {
	Card
	Source  string    `json:"source"`
	AddedAt time.Time `json:"added_at"`
}

// cleanName reads a name given to a contact: trimmed, 1 to 80 letters;
// "" for none.
func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true
	}
	if utf8.RuneCountInString(name) > 80 || strings.ContainsAny(name, "\x00\n\r") {
		return "", false
	}
	return name, true
}

// AddContact adds contact to person's contacts (again: the name and
// source change), with a name they give it ("" for none). ErrInvalid for
// themself, a bad name, a source that is not one or more than
// MaxContacts; ErrNotFound for no active account. The caller has checked
// that person may reach contact (an add token, or their chat).
func (p *People) AddContact(ctx context.Context, person, contact uuid.UUID, name, source string) error {
	name, ok := cleanName(name)
	if !ok || person == contact || (source != FromNumber && source != FromLink && source != FromContacts && source != FromChat) {
		return ErrInvalid
	}
	return db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		got, err := cardsTx(ctx, tx, person, []uuid.UUID{contact})
		if err != nil {
			return err
		}
		if c, ok := got[contact]; !ok || c.Deleted {
			return ErrNotFound
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM person_contacts WHERE person_id = $1`, person).Scan(&n); err != nil {
			return err
		}
		var named *string
		if name != "" {
			named = &name
		}
		tag, err := tx.Exec(ctx, `INSERT INTO person_contacts (person_id, contact_id, name, source)
			SELECT $1, $2, $3, $4 WHERE $5 OR EXISTS (SELECT 1 FROM person_contacts WHERE person_id = $1 AND contact_id = $2)
			ON CONFLICT (person_id, contact_id) DO UPDATE SET name = COALESCE(EXCLUDED.name, person_contacts.name)`,
			person, contact, named, source, n < MaxContacts)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrInvalid
		}
		return err
	})
}

// RenameContact changes the name person gave contact ("" for none);
// ErrNotFound when contact is not theirs.
func (p *People) RenameContact(ctx context.Context, person, contact uuid.UUID, name string) error {
	name, ok := cleanName(name)
	if !ok {
		return ErrInvalid
	}
	var named *string
	if name != "" {
		named = &name
	}
	return db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE person_contacts SET name = $3 WHERE person_id = $1 AND contact_id = $2`, person, contact, named)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// RemoveContact takes contact out of person's contacts; ErrNotFound when
// it was not there.
func (p *People) RemoveContact(ctx context.Context, person, contact uuid.UUID) error {
	return db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM person_contacts WHERE person_id = $1 AND contact_id = $2`, person, contact)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// Contacts are person's contacts, by the name they gave (else the
// person's own), each as person may see them.
func (p *People) Contacts(ctx context.Context, person uuid.UUID) ([]Contact, error) {
	out := []Contact{}
	err := db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT contact_id, source, added_at FROM person_contacts WHERE person_id = $1 ORDER BY added_at`, person)
		if err != nil {
			return err
		}
		type row struct {
			id     uuid.UUID
			source string
			at     time.Time
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.id, &x.source, &x.at)
		})
		if err != nil {
			return err
		}
		for i := 0; i < len(list); i += 500 {
			end := min(i+500, len(list))
			ids := make([]uuid.UUID, 0, end-i)
			for _, x := range list[i:end] {
				ids = append(ids, x.id)
			}
			cards, err := p.CardsTx(ctx, tx, person, ids)
			if err != nil {
				return err
			}
			byID := map[uuid.UUID]Card{}
			for _, c := range cards {
				byID[c.ID] = c
			}
			for _, x := range list[i:end] {
				if c, ok := byID[x.id]; ok {
					out = append(out, Contact{Card: c, Source: x.source, AddedAt: x.at})
				}
			}
		}
		return nil
	})
	sortContacts(out)
	return out, err
}

// IsContact reports whether owner added other to their contacts: what "My
// contacts" means in owner's settings.
func (p *People) IsContact(ctx context.Context, owner, other uuid.UUID) (bool, error) {
	var ok bool
	err := db.InPersonTx(ctx, p.Pool, owner, func(tx pgx.Tx) error {
		var err error
		ok, err = IsContactTx(ctx, tx, owner, other)
		return err
	})
	return ok, err
}

// IsContactTx is IsContact in a transaction in owner's scope.
func IsContactTx(ctx context.Context, tx pgx.Tx, owner, other uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM person_contacts WHERE person_id = $1 AND contact_id = $2)`, owner, other).Scan(&ok)
	return ok, err
}

// ForgetTx removes the person from every other person's contacts and
// deletes their settings, contacts, link, presence, counts and verdicts
// (deleting the account, §14); tx is in their own scope. Answers how many
// other people's contacts they left.
func ForgetTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT taskiem_people_forget($1)`, person).Scan(&n)
	return n, err
}

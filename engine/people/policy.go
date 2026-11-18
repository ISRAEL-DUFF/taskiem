package people

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// PeoplePolicy is what the people chats (stream A) ask of finding people
// and privacy; *People implements it.
type PeoplePolicy interface {
	// MayMessage decides a first message from one person to another (a
	// chat that to has not accepted).
	MayMessage(ctx context.Context, from, to uuid.UUID) (Decision, error)
	// IsContact reports whether a added b to their contacts.
	IsContact(ctx context.Context, a, b uuid.UUID) (bool, error)
	// NumberShownTo is owner's number when viewer is shown it, else "".
	NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (string, error)
	// ReadTicks reports whether read ticks go between a and b.
	ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error)
	// ScreenStranger checks a stranger's message and keeps the verdict
	// only.
	ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (Verdict, error)
}

var _ PeoplePolicy = (*People)(nil)

// Why a first message is refused. A block is never a reason: it is
// Decision.Blocked, beside the decision the settings make.
const (
	RefusedSelf     = "self"     // writing to themself
	RefusedGone     = "gone"     // no such account, or deleted
	RefusedContacts = "contacts" // to takes messages from their contacts only, and has not added from
	RefusedNobody   = "nobody"   // to takes no new chats
)

// Decision is whether from may write to to, and how.
type Decision struct {
	// Allowed: the message may be sent.
	Allowed bool `json:"allowed"`
	// AsRequest: it waits in to's Requests until accepted (to has not
	// added from to their contacts).
	AsRequest bool `json:"as_request"`
	// Refused says why not, when not allowed (Refused* above).
	Refused string `json:"refused,omitempty"`
	// Blocked: a block stands between them (either blocked the other).
	// Allowed, AsRequest and Refused are then what they would be without
	// it, so that the sender is answered exactly as if there were none;
	// the caller delivers nothing and says nothing of it.
	Blocked bool `json:"-"`
}

// MayMessage decides a first message from from to to, by to's "who can
// message me" (§5.4) and contacts:
//   - everyone (the default): allowed; a request unless to added from;
//   - contacts: allowed, not a request, only when to added from;
//   - nobody: refused (no new chats; chats to accepted go on, which the
//     caller does not ask about).
//
// A block either way does not change the decision (Blocked says it), so a
// blocked sender cannot tell it from the answers. It is for a chat that
// to has not accepted: once to accepted (or started) it, the chat's own
// rules apply.
func (p *People) MayMessage(ctx context.Context, from, to uuid.UUID) (Decision, error) {
	var d Decision
	err := db.InPersonTx(ctx, p.Pool, from, func(tx pgx.Tx) error {
		var err error
		d, err = p.MayMessageTx(ctx, tx, from, to)
		return err
	})
	return d, err
}

// MayMessageTx is MayMessage in a transaction in from's own scope; it
// opens no other connection.
func (p *People) MayMessageTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (Decision, error) {
	if from == to {
		return Decision{Refused: RefusedSelf}, nil
	}
	got, err := cardsTx(ctx, tx, from, []uuid.UUID{to})
	if err != nil {
		return Decision{}, err
	}
	c, ok := got[to]
	if !ok || c.Deleted {
		return Decision{Refused: RefusedGone}, nil
	}
	b, err := p.blockedTx(ctx, tx, from, to)
	if err != nil {
		return Decision{}, err
	}
	known := c.TheyAddedYou
	var d Decision
	switch c.Message {
	case Nobody:
		d = Decision{Refused: RefusedNobody}
	case Contacts:
		if known {
			d = Decision{Allowed: true}
		} else {
			d = Decision{Refused: RefusedContacts}
		}
	default:
		d = Decision{Allowed: true, AsRequest: !known}
	}
	d.Blocked = b
	return d, nil
}

// TheyAddedTx reports whether to added from to their contacts, in from's
// scope (tx): what decides whether from's message is a request.
func (p *People) TheyAddedTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, error) {
	got, err := cardsTx(ctx, tx, from, []uuid.UUID{to})
	if err != nil {
		return false, err
	}
	c := got[to]
	return c != nil && !c.Deleted && c.TheyAddedYou, nil
}

// NumberShownTo is owner's number (E.164) when viewer is shown it: owner
// chose to show their number (off by default, §17 Q5), and viewer is
// someone they chat with (Chatting) or added to their contacts, with no
// block between them; "" otherwise.
func (p *People) NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (string, error) {
	var n string
	err := db.InPersonTx(ctx, p.Pool, viewer, func(tx pgx.Tx) error {
		var err error
		n, err = p.NumberShownToTx(ctx, tx, owner, viewer)
		return err
	})
	return n, err
}

// NumberShownToTx is NumberShownTo in a transaction in viewer's scope; it
// opens no other connection.
func (p *People) NumberShownToTx(ctx context.Context, tx pgx.Tx, owner, viewer uuid.UUID) (string, error) {
	if owner == viewer {
		return "", nil
	}
	got, err := cardsTx(ctx, tx, viewer, []uuid.UUID{owner})
	if err != nil {
		return "", err
	}
	c := got[owner]
	if c == nil || c.Deleted || c.number == nil {
		return "", nil
	}
	if b, err := p.blockedTx(ctx, tx, viewer, owner); err != nil || b {
		return "", err
	}
	if !c.TheyAddedYou {
		chatting, err := p.chattingTx(ctx, tx, viewer, owner)
		if err != nil || !chatting {
			return "", err
		}
	}
	return *c.number, nil
}

// ReadTicks reports whether read ticks go between a and b: both have them
// on (§5.4: turning them off means not seeing others' either).
func (p *People) ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	var on bool
	err := db.InPersonTx(ctx, p.Pool, a, func(tx pgx.Tx) error {
		var err error
		on, err = p.ReadTicksTx(ctx, tx, a, b)
		return err
	})
	return on, err
}

// ReadTicksTx is ReadTicks in a transaction in a's scope.
func (p *People) ReadTicksTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	got, err := cardsTx(ctx, tx, a, []uuid.UUID{b})
	if c := got[b]; c != nil {
		return c.ReadTicks, err
	}
	return false, err
}

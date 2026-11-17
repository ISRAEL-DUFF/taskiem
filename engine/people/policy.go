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

// Why a first message is refused.
const (
	RefusedSelf     = "self"     // writing to themself
	RefusedGone     = "gone"     // no such account, or deleted
	RefusedBlocked  = "blocked"  // either blocked the other: say nothing to the sender (one tick)
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
}

// MayMessage decides a first message from from to to, by to's "who can
// message me" (§5.4) and contacts, and blocks either way:
//   - everyone (the default): allowed; a request unless to added from;
//   - contacts: allowed, not a request, only when to added from;
//   - nobody: refused (no new chats; chats to accepted go on, which the
//     caller does not ask about).
//
// It is for a chat that to has not accepted: once to accepted (or
// started) it, the chat's own rules apply.
func (p *People) MayMessage(ctx context.Context, from, to uuid.UUID) (Decision, error) {
	var d Decision
	err := db.InPersonTx(ctx, p.Pool, from, func(tx pgx.Tx) error {
		var err error
		d, err = p.MayMessageTx(ctx, tx, from, to)
		return err
	})
	return d, err
}

// MayMessageTx is MayMessage in a transaction in from's (or to's) own
// scope.
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
	if b, err := p.blocked(ctx, from, to); err != nil || b {
		return Decision{Refused: RefusedBlocked}, err
	}
	known := c.TheyAddedYou
	switch c.Message {
	case Nobody:
		return Decision{Refused: RefusedNobody}, nil
	case Contacts:
		if !known {
			return Decision{Refused: RefusedContacts}, nil
		}
		return Decision{Allowed: true}, nil
	default:
		return Decision{Allowed: true, AsRequest: !known}, nil
	}
}

// NumberShownTo is owner's number (E.164) when viewer is shown it: owner
// chose to show their number (off by default, §17 Q5), and viewer is
// someone they chat with (Chatting) or added to their contacts, with no
// block between them; "" otherwise.
func (p *People) NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (string, error) {
	if owner == viewer {
		return "", nil
	}
	var c *Card
	err := db.InPersonTx(ctx, p.Pool, viewer, func(tx pgx.Tx) error {
		got, err := cardsTx(ctx, tx, viewer, []uuid.UUID{owner})
		c = got[owner]
		return err
	})
	if err != nil || c == nil || c.Deleted || c.number == nil {
		return "", err
	}
	if b, err := p.blocked(ctx, viewer, owner); err != nil || b {
		return "", err
	}
	if !c.TheyAddedYou {
		chatting := false
		if p.Chatting != nil {
			if chatting, err = p.Chatting(ctx, viewer, owner); err != nil {
				return "", err
			}
		}
		if !chatting {
			return "", nil
		}
	}
	return *c.number, nil
}

// ReadTicks reports whether read ticks go between a and b: both have them
// on (§5.4: turning them off means not seeing others' either).
func (p *People) ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	var on bool
	err := db.InPersonTx(ctx, p.Pool, a, func(tx pgx.Tx) error {
		got, err := cardsTx(ctx, tx, a, []uuid.UUID{b})
		if c := got[b]; c != nil {
			on = c.ReadTicks
		}
		return err
	})
	return on, err
}

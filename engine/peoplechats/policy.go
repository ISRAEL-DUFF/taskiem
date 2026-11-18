// Package peoplechats holds what chats between people (docs/spec/people-
// chatting.md, stage 5a; decision 0069) need beside the API: the questions
// of who may message whom, which finding people answers (stream B), with a
// stub that answers them carefully until it lands; the sealing of each
// person's copy under that person's own key; and the limits on requests.
//
// People chats are kept by Taskiem, sealed at rest under each person's key
// and opened only by Taskiem's servers. They are not end-to-end encrypted,
// and nothing here may say they are.
package peoplechats

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Policy answers who may message whom, who is whose contact, whose number
// is shown to whom, and what the automated checks make of a stranger's
// message (people-chatting §5.4, §8.4). Finding people (stream B: privacy
// settings, contacts, the stranger checks) implements it; until then the
// server uses Stub.
type Policy interface {
	// MayMessage says whether from may write to to at all (to's "Who can
	// message me": everyone, my contacts, nobody), and whether a first
	// message waits as a request. It is asked only when the two have no
	// accepted chat between them.
	MayMessage(ctx context.Context, from, to uuid.UUID) (allowed, asRequest bool, err error)
	// IsContact says whether b is in a's contacts.
	IsContact(ctx context.Context, a, b uuid.UUID) (bool, error)
	// NumberShownTo says whether owner's phone number is shown to viewer
	// (hidden unless the owner chooses to show it, §17 Q5).
	NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (bool, error)
	// ScreenStranger looks at a message from a stranger (a request, or a
	// new chat's first messages) for scams and spam; it keeps nothing but
	// its verdict (§8.4, §17 Q15).
	ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (Verdict, error)
}

// Verdict is what the stranger checks made of a message.
type Verdict struct {
	Action Action
	// Reason is said to the sender when the message is refused; plain
	// words, never the message's own.
	Reason string
	// Reasons are the checks' names for what they found (kept with a held
	// message for moderators).
	Reasons []string
}

// Action is what is done with a screened message.
type Action int

const (
	// Allow delivers it as it is.
	Allow Action = iota
	// Warn delivers it, marked so the person's app warns them about it.
	Warn
	// Refuse does not deliver it; the sender is told it was not sent.
	Refuse
)

// Stub is the policy before finding people lands: anyone may write to
// anyone, always as a request; nobody is anybody's contact; numbers are
// never shown; nothing is screened.
type Stub struct{}

// MayMessage lets everyone write, as a request.
func (Stub) MayMessage(context.Context, uuid.UUID, uuid.UUID) (bool, bool, error) {
	return true, true, nil
}

// IsContact says no one is a contact.
func (Stub) IsContact(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }

// NumberShownTo hides every number.
func (Stub) NumberShownTo(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }

// ScreenStranger lets every message through.
func (Stub) ScreenStranger(context.Context, uuid.UUID, uuid.UUID, string) (Verdict, error) {
	return Verdict{Action: Allow}, nil
}

// LinkResolver finds the person a person's link names (https://<host>/p/
// {code}, §5.2), stream B's. Until it lands, no link is known (NoLinks).
type LinkResolver interface {
	ResolvePersonLink(ctx context.Context, code string) (uuid.UUID, error)
}

// The link resolver's answers.
var (
	// ErrLinkUnknown: no person has this link.
	ErrLinkUnknown = errors.New("no person has this link")
	// ErrLinkReplaced: the person made a new link; this one no longer works.
	ErrLinkReplaced = errors.New("this link was replaced")
)

// NoLinks knows no link.
type NoLinks struct{}

// ResolvePersonLink answers ErrLinkUnknown.
func (NoLinks) ResolvePersonLink(context.Context, string) (uuid.UUID, error) {
	return uuid.Nil, ErrLinkUnknown
}

// RequestAllowance is how many requests a sender may have waiting at once
// and send in a day (§8.2): fewer in an account's first week; more as
// people accept their requests; far fewer when most of those decided were
// deleted or blocked.
func RequestAllowance(newWeek bool, accepted, refused int) (waiting, perDay int) {
	waiting, perDay = 5, 10
	if newWeek {
		waiting, perDay = 2, 3
	}
	waiting += min(accepted/2, 15)
	perDay += min(accepted, 40)
	if refused >= 3 && refused*2 > accepted+refused {
		waiting, perDay = max(1, waiting/3), max(1, perDay/3)
	}
	return waiting, perDay
}

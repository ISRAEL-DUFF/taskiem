package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// The two streams of stage 5a joined (docs/spec/people-chatting.md, "As
// built: stage 5a, the two streams joined"): the people chats ask finding
// people who may message whom, through peopleJoin, which answers
// peoplechats.Policy and peoplechats.LinkResolver over *people.People.

// peopleJoin is finding people as the people chats ask it.
type peopleJoin struct{ p *people.People }

var (
	_ peoplechats.Policy       = peopleJoin{}
	_ peoplechats.LinkResolver = peopleJoin{}
)

// peopleHeldWords is what the sender of a message the checks held is told.
const peopleHeldWords = "This message was not sent: it looks like a scam. Taskiem does not send messages that ask for codes, PINs or money with a prize or a fee."

// MayMessage is to's "who can message me": everyone (a request unless to
// added from), my contacts (only people to added, not as a request),
// nobody (refused). A block is never told to the sender: it is let
// through as a request, and the chats keep it on the sender's side with
// one tick (and the sender's own block refuses them before this asks).
func (j peopleJoin) MayMessage(ctx context.Context, from, to uuid.UUID) (bool, bool, error) {
	d, err := j.p.MayMessage(ctx, from, to)
	if err != nil {
		return false, false, err
	}
	if d.Refused == people.RefusedBlocked {
		return true, true, nil
	}
	return d.Allowed, d.AsRequest, nil
}

// IsContact is whether a added b.
func (j peopleJoin) IsContact(ctx context.Context, a, b uuid.UUID) (bool, error) {
	return j.p.IsContact(ctx, a, b)
}

// NumberShownTo is whether finding people shows owner's number to viewer.
func (j peopleJoin) NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (bool, error) {
	n, err := j.p.NumberShownTo(ctx, owner, viewer)
	return n != "", err
}

// ScreenStranger maps the checks' verdicts: ok delivers, warn delivers
// marked, hold refuses.
func (j peopleJoin) ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (peoplechats.Verdict, error) {
	v, err := j.p.ScreenStranger(ctx, from, to, text)
	if err != nil {
		return peoplechats.Verdict{}, err
	}
	switch v.Verdict {
	case people.VerdictHold:
		return peoplechats.Verdict{Action: peoplechats.Refuse, Reason: peopleHeldWords}, nil
	case people.VerdictWarn:
		return peoplechats.Verdict{Action: peoplechats.Warn}, nil
	}
	return peoplechats.Verdict{Action: peoplechats.Allow}, nil
}

// ReadTicks is whether read ticks go between a and b (both have them on).
func (j peopleJoin) ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	return j.p.ReadTicks(ctx, a, b)
}

// ResolvePersonLink is the person a link names: replaced (410), else
// unknown (404) for no such link or a server without links.
func (j peopleJoin) ResolvePersonLink(ctx context.Context, code string) (uuid.UUID, error) {
	id, err := j.p.Resolve(ctx, code)
	switch {
	case errors.Is(err, people.ErrLinkReplaced):
		return uuid.Nil, peoplechats.ErrLinkReplaced
	case errors.Is(err, people.ErrLinkNotFound), errors.Is(err, people.ErrOff), errors.Is(err, people.ErrNotFound):
		return uuid.Nil, peoplechats.ErrLinkUnknown
	}
	return id, err
}

// readTicker is a policy that also says whether read ticks go between two
// people (peopleJoin does; a test's policy may not, and then they always
// do once a chat is accepted).
type readTicker interface {
	ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// chatPolicy is who may message whom for the people chats: ChatPolicy
// when set (tests), else finding people's.
func (s *Server) chatPolicy() peoplechats.Policy {
	if s.ChatPolicy != nil {
		return s.ChatPolicy
	}
	return peopleJoin{s.people()}
}

// readTicks is whether read ticks go between a and b.
func (s *Server) readTicks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if rt, ok := s.chatPolicy().(readTicker); ok {
		return rt.ReadTicks(ctx, a, b)
	}
	return true, nil
}

// readTicksWith is readTicks with the other person of a chat (true when
// there is none: nothing goes either way).
func (s *Server) readTicksWith(ctx context.Context, person uuid.UUID, peer *uuid.UUID) (bool, error) {
	if peer == nil {
		return true, nil
	}
	return s.readTicks(ctx, person, *peer)
}

// hideRead shows the person's own message as delivered, not read, while
// read ticks do not go between them (either turned them off, §5.4).
func hideRead(m peopleMessage, ticks bool) peopleMessage {
	if !ticks && m.Mine {
		if m.Status == "read" {
			m.Status = "delivered"
		}
		m.ReadAt = nil
	}
	return m
}

// addChatContact adds peer to the person's contacts as someone they chat
// with (people.FromChat), once they accepted the request or wrote back;
// answers whether they are a contact now. A failure (their account gone,
// a full list) leaves the contacts as they were.
func (s *Server) addChatContact(ctx context.Context, person, peer uuid.UUID) bool {
	err := s.people().AddContact(ctx, person, peer, "", people.FromChat)
	if err != nil && !errors.Is(err, people.ErrNotFound) && !errors.Is(err, people.ErrInvalid) {
		s.Logger.Warn("people: adding a contact from a chat", "err", err)
	}
	return err == nil
}

// peopleHere records that the person is here (finding people's presence:
// last seen now, online for people.OnlineFor) when they use their people
// chats, at most every 30 seconds; the app's heartbeat (POST
// /v1/me/presence) does the rest, and says when they leave.
func (s *Server) peopleHere(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if person, err := personOf(r); err == nil && s.limiter("people-here:"+person.String(), 30*time.Second, 1).Allow() {
			if err := s.people().Touch(r.Context(), person); err != nil {
				s.Logger.Warn("people: presence", "err", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// personLinks finds the person a person's link names: PersonLinks when
// set, else finding people's links.
func (s *Server) personLinks() peoplechats.LinkResolver {
	if s.PersonLinks != nil {
		return s.PersonLinks
	}
	return peopleJoin{s.people()}
}

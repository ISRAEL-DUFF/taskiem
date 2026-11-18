package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
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
// nobody (refused). A block changes nothing here: the sender is answered
// as if there were none, and the chats deliver nothing (the sender's own
// block refuses them before this asks).
func (j peopleJoin) MayMessage(ctx context.Context, from, to uuid.UUID) (bool, bool, error) {
	d, err := j.p.MayMessage(ctx, from, to)
	return d.Allowed, d.AsRequest, err
}

// MayMessageTx is MayMessage in tx, from's own transaction.
func (j peopleJoin) MayMessageTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, bool, error) {
	d, err := j.p.MayMessageTx(ctx, tx, from, to)
	return d.Allowed, d.AsRequest, err
}

// IsContact is whether a added b.
func (j peopleJoin) IsContact(ctx context.Context, a, b uuid.UUID) (bool, error) {
	return j.p.IsContact(ctx, a, b)
}

// IsContactTx is IsContact in tx, a's own transaction.
func (j peopleJoin) IsContactTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	return people.IsContactTx(ctx, tx, a, b)
}

// TheyAddedTx is whether to added from, in from's own transaction.
func (j peopleJoin) TheyAddedTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, error) {
	return j.p.TheyAddedTx(ctx, tx, from, to)
}

// NumberShownTo is whether finding people shows owner's number to viewer.
func (j peopleJoin) NumberShownTo(ctx context.Context, owner, viewer uuid.UUID) (bool, error) {
	n, err := j.p.NumberShownTo(ctx, owner, viewer)
	return n != "", err
}

// NumberShownToTx is NumberShownTo in tx, viewer's own transaction.
func (j peopleJoin) NumberShownToTx(ctx context.Context, tx pgx.Tx, owner, viewer uuid.UUID) (bool, error) {
	n, err := j.p.NumberShownToTx(ctx, tx, owner, viewer)
	return n != "", err
}

// verdictOf maps the checks' verdicts: ok delivers, warn delivers marked,
// hold refuses.
func verdictOf(v people.Verdict) peoplechats.Verdict {
	switch v.Verdict {
	case people.VerdictHold:
		return peoplechats.Verdict{Action: peoplechats.Refuse, Reason: peopleHeldWords, Reasons: v.Reasons}
	case people.VerdictWarn:
		return peoplechats.Verdict{Action: peoplechats.Warn, Reasons: v.Reasons}
	}
	return peoplechats.Verdict{Action: peoplechats.Allow}
}

// ScreenStranger is the checks on a stranger's message (verdictOf).
func (j peopleJoin) ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (peoplechats.Verdict, error) {
	v, err := j.p.ScreenStranger(ctx, from, to, text)
	return verdictOf(v), err
}

// ScreenStrangerTx is ScreenStranger in tx, from's own transaction: the
// verdict is kept only if tx commits.
func (j peopleJoin) ScreenStrangerTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID, text string) (peoplechats.Verdict, error) {
	v, err := j.p.ScreenStrangerTx(ctx, tx, from, to, text)
	return verdictOf(v), err
}

// ReadTicks is whether read ticks go between a and b (both have them on).
func (j peopleJoin) ReadTicks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	return j.p.ReadTicks(ctx, a, b)
}

// ReadTicksTx is ReadTicks in tx, a's own transaction.
func (j peopleJoin) ReadTicksTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	return j.p.ReadTicksTx(ctx, tx, a, b)
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

// txPolicy is a policy that answers inside the caller's transaction
// (peopleJoin), so that a request holds one connection at a time: a
// transaction that waited for a second connection from the pool could
// starve it. A test's policy need not; the chats then ask it outside.
type txPolicy interface {
	MayMessageTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (allowed, asRequest bool, err error)
	IsContactTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error)
	TheyAddedTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, error)
	NumberShownToTx(ctx context.Context, tx pgx.Tx, owner, viewer uuid.UUID) (bool, error)
	ScreenStrangerTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID, text string) (peoplechats.Verdict, error)
	ReadTicksTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error)
}

var _ txPolicy = peopleJoin{}

// chatPolicy is who may message whom for the people chats: ChatPolicy
// when set (tests), else finding people's.
func (s *Server) chatPolicy() peoplechats.Policy {
	if s.ChatPolicy != nil {
		return s.ChatPolicy
	}
	return peopleJoin{s.people()}
}

// mayMessageTx asks the policy, in tx (from's) when it can.
func (s *Server) mayMessageTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, bool, error) {
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.MayMessageTx(ctx, tx, from, to)
	}
	return s.chatPolicy().MayMessage(ctx, from, to)
}

// isContactTx is whether a added b, in tx (a's) when it can.
func (s *Server) isContactTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.IsContactTx(ctx, tx, a, b)
	}
	return s.chatPolicy().IsContact(ctx, a, b)
}

// theyAddedTx is whether to added from, in tx (from's) when it can.
func (s *Server) theyAddedTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (bool, error) {
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.TheyAddedTx(ctx, tx, from, to)
	}
	return s.chatPolicy().IsContact(ctx, to, from)
}

// numberShownToTx is whether owner's number is shown to viewer, in tx
// (viewer's) when it can.
func (s *Server) numberShownToTx(ctx context.Context, tx pgx.Tx, owner, viewer uuid.UUID) (bool, error) {
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.NumberShownToTx(ctx, tx, owner, viewer)
	}
	return s.chatPolicy().NumberShownTo(ctx, owner, viewer)
}

// screenTx is the checks on a stranger's message, in tx (from's) when it
// can, so that the verdict is kept only when the message is.
func (s *Server) screenTx(ctx context.Context, tx pgx.Tx, from, to uuid.UUID, text string) (peoplechats.Verdict, error) {
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.ScreenStrangerTx(ctx, tx, from, to, text)
	}
	return s.chatPolicy().ScreenStranger(ctx, from, to, text)
}

// readTicksTx is whether read ticks go between person and the other
// person of a chat (true when there is none: nothing goes either way), in
// tx (person's) when it can.
func (s *Server) readTicksTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, peer *uuid.UUID) (bool, error) {
	if peer == nil {
		return true, nil
	}
	if p, ok := s.chatPolicy().(txPolicy); ok {
		return p.ReadTicksTx(ctx, tx, person, *peer)
	}
	if rt, ok := s.chatPolicy().(readTicker); ok {
		return rt.ReadTicks(ctx, person, *peer)
	}
	return true, nil
}

// peopleTx runs fn in the person's own transaction, again (up to three
// times in all) when the database broke a deadlock or a serialization
// conflict: the people chats lock a conversation before either copy
// (lockConversationTx), so neither should happen, and this is the
// backstop. fn must start from nothing each time.
func (s *Server) peopleTx(ctx context.Context, person uuid.UUID, fn func(tx pgx.Tx) error) error {
	var err error
	for try := 0; try < 3; try++ {
		if err = db.InPersonTx(ctx, s.Store.Pool, person, fn); !retryable(err) {
			return err
		}
	}
	return err
}

// retryable is a deadlock (40P01) or a serialization failure (40001).
func retryable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "40P01" || pe.Code == "40001")
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

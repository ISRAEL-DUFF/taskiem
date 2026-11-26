package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
)

// Chats between people, one to one (docs/spec/people-chatting.md §7, §8,
// §10, §11, §13, §14; stage 5a; decision 0069; migration 00471). Each
// person keeps their own copy of a conversation (people_chats,
// people_messages, people_files), under their own scope, its words, cards,
// places and files sealed under their own key: kept by Taskiem, opened only
// by its servers, and not end-to-end encrypted. A message is written to
// both copies in one transaction, the other's through taskiem_people_reach,
// which puts that one conversation of theirs in reach. A business never
// reaches a people chat; a business session reaches its own person's.
//
// Who may message whom, contacts, numbers shown and the stranger checks
// are finding people's (stream B): Server.People, a peoplechats.Policy,
// whose stub writes everyone as a request.

// The limits of people chats (§8.2): new chats an hour and a day, and
// messages in one chat a minute (halved in an account's first week, as
// limitFor does); a message forwarded to at most this many chats at once,
// and one forwarded many times to one chat at a time.
var (
	peopleChatsPerHour      = 10
	peopleChatsPerDay       = 30
	peopleMessagesPerMinute = 20
	peopleForwardAtOnce     = 5
	// A message forwarded this many times on its way is "forwarded many
	// times".
	peopleForwardedMany = 5
	// Delete for everyone, within this long of sending.
	peopleDeleteForEveryone = 24 * time.Hour
)

// peopleKeptWords is what the app says of people chats (§6.2; wording for
// legal review, §17 Q13): never that they are end-to-end encrypted.
const peopleKeptWords = "Messages are stored securely by Taskiem. Taskiem does not read your chats with people except to keep you safe: when you report them, to stop spam, or when the law requires it. They are not end-to-end encrypted."

// peopleGoneName is how a deleted account is shown in the other's copy.
const peopleGoneName = "Deleted account"

// peopleChatRoutes are the person's routes (peopleChatPersonRoutes lists
// them).
func (s *Server) peopleChatRoutes(r chi.Router) {
	// You's live connection keeps the person's presence itself.
	r.Get("/me/live", s.peopleLive)
	r = r.With(s.peopleHere)
	r.Get("/me/conversations", s.listAllChats)
	r.Get("/me/people", s.listPeopleChats)
	r.Post("/me/people", s.startPeopleChat)
	r.Get("/me/people/requests", s.listPeopleRequests)
	r.Get("/me/people/blocked", s.listPeopleBlocked)
	r.Get("/me/people/search", s.searchPeople)
	r.Get("/me/people/starred", s.starredPeople)
	r.Post("/me/people/forward", s.forwardPeopleMessage)
	r.Get("/me/people/{id}", s.getPeopleChat)
	r.Put("/me/people/{id}", s.putPeopleChat)
	r.Delete("/me/people/{id}", s.deletePeopleChat)
	r.Post("/me/people/{id}/accept", s.acceptPeopleRequest)
	r.Post("/me/people/{id}/decline", s.declinePeopleRequest)
	r.Post("/me/people/{id}/block", s.blockPerson)
	r.Delete("/me/people/{id}/block", s.unblockPerson)
	r.Post("/me/people/{id}/report", s.reportPerson)
	r.Post("/me/people/{id}/read", s.readPeopleChat)
	r.Post("/me/people/{id}/typing", s.typingPeople)
	r.Get("/me/people/{id}/messages", s.peopleMessages)
	r.Post("/me/people/{id}/messages", s.sendPeopleMessage)
	r.Post("/me/people/{id}/files", s.sendPeopleFile)
	r.Get("/me/people/{id}/files/{message}", s.peopleFile)
	r.Get("/me/people/{id}/previews/{message}", s.peoplePreviewImage)
	r.Put("/me/people/{id}/messages/{message}", s.markPeopleMessage)
	r.Delete("/me/people/{id}/messages/{message}", s.deletePeopleMessage)
	r.Post("/me/people/{id}/messages/{message}/paid", s.markSplitPaid)
	r.Get("/me/people/{id}/messages/{message}/card", s.openPeopleCard)
	r.Get("/me/payout-account", s.getPayoutAccount)
	r.Put("/me/payout-account", s.putPayoutAccount)
	r.Delete("/me/payout-account", s.deletePayoutAccount)
}

// peopleChatPersonRoutes are the people chats' routes a personal session
// reaches (personalAllowed).
var peopleChatPersonRoutes = regexp.MustCompile(`^(GET /v1/me/(conversations|live)|(GET|POST) /v1/me/people|GET /v1/me/people/(requests|blocked|search|starred)|POST /v1/me/people/forward|` +
	`(GET|PUT|DELETE) /v1/me/people/[^/]+|POST /v1/me/people/[^/]+/(accept|decline|block|report|read|typing|messages|files)|DELETE /v1/me/people/[^/]+/block|` +
	`GET /v1/me/people/[^/]+/(messages|files/[^/]+|previews/[^/]+|messages/[^/]+/card)|(PUT|DELETE) /v1/me/people/[^/]+/messages/[^/]+|POST /v1/me/people/[^/]+/messages/[^/]+/paid|` +
	`(GET|PUT|DELETE) /v1/me/payout-account)$`)

// keys seal each person's copy under their own key.
func (s *Server) keys() *peoplechats.Keys {
	s.peopleKeysOnce.Do(func() {
		if s.Vault != nil {
			s.peopleKeys = peoplechats.NewKeys(s.Vault)
		}
	})
	return s.peopleKeys
}

// The people chats' refusals, in plain words.
var (
	errPeopleGone       = customerSendErr{http.StatusConflict, "This account was deleted, so this chat takes no more messages."}
	errPeopleSuspended  = customerSendErr{http.StatusConflict, "This account can't get messages right now."}
	errPeopleYouBlocked = customerSendErr{http.StatusConflict, "You blocked this person. Unblock them to write to them."}
	errPeopleNotTaking  = customerSendErr{http.StatusForbidden, "This person is not taking messages from people they don't know."}
	errPeopleNoPerson   = customerSendErr{http.StatusNotFound, "No such person on Taskiem."}
)

// peopleCopy is a person's copy of a conversation, as kept.
type peopleCopy struct {
	ID, Conversation      uuid.UUID
	Peer                  *uuid.UUID
	StartedByMe           bool
	RequestAt, WaitingAt  *time.Time
	AcceptedAt, HiddenAt  *time.Time
	Muted, Archived       bool
	LastAt, CreatedAt     time.Time
	Unread                int
	Blocked               bool
	PeerWroteAt           *time.Time
	Empty                 bool
	lastID                *uuid.UUID
	lastKind, lastCard    *string
	lastAttach            *string
	lastMine, lastDeleted *bool
	lastSealed            []byte
}

const peopleCopyCols = `c.id, c.conversation_id, c.peer_id, c.started_by_me, c.request_at, c.waiting_at, c.accepted_at, c.hidden_at, c.muted, c.archived,
	c.last_message_at, c.created_at,
	(SELECT count(*) FROM people_messages m WHERE m.person_id = c.person_id AND m.chat_id = c.id AND NOT m.mine AND m.read_at IS NULL AND m.deleted_at IS NULL),
	EXISTS (SELECT 1 FROM people_blocks b WHERE b.person_id = c.person_id AND b.blocked_id = c.peer_id),
	l.id, l.kind, l.card_type, l.attachment->>'kind', l.mine, l.deleted_at IS NOT NULL, l.sealed, c.peer_wrote_at, c.empty`

const peopleCopyFrom = ` FROM people_chats c LEFT JOIN LATERAL (SELECT m.id, m.kind, m.card_type, m.attachment, m.mine, m.deleted_at, m.sealed FROM people_messages m
	WHERE m.person_id = c.person_id AND m.chat_id = c.id ORDER BY m.created_at DESC, m.id DESC LIMIT 1) l ON true`

func scanPeopleCopy(r pgx.Row) (peopleCopy, error) {
	var c peopleCopy
	err := r.Scan(&c.ID, &c.Conversation, &c.Peer, &c.StartedByMe, &c.RequestAt, &c.WaitingAt, &c.AcceptedAt, &c.HiddenAt, &c.Muted, &c.Archived,
		&c.LastAt, &c.CreatedAt, &c.Unread, &c.Blocked, &c.lastID, &c.lastKind, &c.lastCard, &c.lastAttach, &c.lastMine, &c.lastDeleted, &c.lastSealed,
		&c.PeerWroteAt, &c.Empty)
	return c, err
}

// peopleCopyTx is one of the person's own copies by its id. forUpdate is
// for a writer: it locks the conversation (lockConversationTx), then holds
// the copy to the end of the transaction (messages sent at once are
// counted one after the other).
func peopleCopyTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, idText string, forUpdate bool) (peopleCopy, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return peopleCopy{}, errDoorNone
	}
	if forUpdate {
		var conv uuid.UUID
		err := tx.QueryRow(ctx, `SELECT conversation_id FROM people_chats WHERE person_id = $1 AND id = $2`, person, id).Scan(&conv)
		if errors.Is(err, pgx.ErrNoRows) {
			return peopleCopy{}, errDoorNone
		}
		if err != nil {
			return peopleCopy{}, err
		}
		if err := lockConversationTx(ctx, tx, conv); err != nil {
			return peopleCopy{}, err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM people_chats WHERE person_id = $1 AND id = $2 FOR UPDATE`, person, id); err != nil {
			return peopleCopy{}, err
		}
	}
	c, err := scanPeopleCopy(tx.QueryRow(ctx, `SELECT `+peopleCopyCols+peopleCopyFrom+` WHERE c.person_id = $1 AND c.id = $2`, person, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errDoorNone
	}
	return c, err
}

// lockConversationTx takes the conversation's lock for the rest of the
// transaction. Every writer that touches both copies of a conversation
// (a message, accept, decline, read, a reaction, delete for everyone,
// mark paid, block) takes it before locking or writing either copy, so two
// people writing to each other at once wait for each other in one order
// instead of each holding their own copy while waiting for the other's (a
// deadlock).
func lockConversationTx(ctx context.Context, tx pgx.Tx, conv uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('people-conversation:' || $1::text, 0))`, conv)
	return err
}

// lockSenderTx takes a lock on one sender's requests and screened messages
// for the rest of the transaction, so that two first messages at once are
// counted one after the other (the request allowance, RepeatHold). It is
// taken after the conversation's lock, never before.
func lockSenderTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('people-sender:' || $1::text, 0))`, person)
	return err
}

// peerAway is the reach's refusal for an account that takes nothing now:
// deleted (errPeopleGone) or suspended (errPeopleSuspended).
func peerAway(err error) bool {
	return errors.Is(err, errPeopleGone) || errors.Is(err, errPeopleSuspended)
}

// theyBlockedTx is whether peer blocked person; the reach must have put
// peer in reach.
func theyBlockedTx(ctx context.Context, tx pgx.Tx, peer, person uuid.UUID) (bool, error) {
	var b bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people_blocks WHERE person_id = $1 AND blocked_id = $2)`, peer, person).Scan(&b)
	return b, err
}

// peopleReachTx puts the other person's copy of c's conversation in reach
// and answers who they are; errPeopleGone when their account was deleted,
// errPeopleSuspended while it is suspended, errPeopleYouSuspended while
// the caller's own is. None of these aborts the transaction
// (taskiem_people_reach_try answers instead of raising), so a caller that
// takes one as "nothing to do on their side" goes on with its own copy.
func peopleReachTx(ctx context.Context, tx pgx.Tx, chat uuid.UUID) (uuid.UUID, error) {
	peer, _, err := reachTryTx(ctx, tx, chat, false)
	return peer, err
}

// peopleForgetReachTx is the reach for forgetting (delete for everyone)
// and accepting a request: past a suspension on either side (a suspension
// stops messaging, not forgetting), never to a deleted account. reached
// says whether the other's copy is in reach; err is only a failure.
func peopleForgetReachTx(ctx context.Context, tx pgx.Tx, chat uuid.UUID) (uuid.UUID, bool, error) {
	peer, reached, err := reachTryTx(ctx, tx, chat, true)
	if reached {
		return peer, true, nil
	}
	if peerAway(err) || errors.Is(err, errPeopleYouSuspended) {
		return uuid.Nil, false, nil
	}
	return uuid.Nil, false, err
}

func reachTryTx(ctx context.Context, tx pgx.Tx, chat uuid.UUID, forget bool) (uuid.UUID, bool, error) {
	var peer *uuid.UUID
	var state string
	var reached bool
	if err := tx.QueryRow(ctx, `SELECT peer, state, reached FROM taskiem_people_reach_try($1, $2)`, chat, forget).Scan(&peer, &state, &reached); err != nil {
		return uuid.Nil, false, err
	}
	if reached && peer != nil {
		return *peer, true, nil
	}
	switch state {
	case "gone":
		return uuid.Nil, false, errPeopleGone
	case "suspended":
		return uuid.Nil, false, errPeopleSuspended
	case "self":
		return uuid.Nil, false, errPeopleYouSuspended
	}
	return uuid.Nil, false, errDoorNone
}

// reachQuiet is a reach that went nowhere for a reason the caller says
// nothing of (a read mark, typing, a number): the other is away, or the
// caller is suspended.
func reachQuiet(err error) bool {
	return peerAway(err) || errors.Is(err, errPeopleYouSuspended)
}

// peopleSealed is what a message's sealed value holds.
type peopleSealed struct {
	Body     string          `json:"b,omitempty"`
	Card     *peopleCard     `json:"c,omitempty"`
	Location *peopleLocation `json:"l,omitempty"`
	// Name is a file's name.
	Name string `json:"n,omitempty"`
	// Preview is its first link's preview (the picture's size only; the
	// picture is a sealed file of kind link_image), sealed with the words.
	Preview *links.Preview `json:"p,omitempty"`
}

func messageRow(id uuid.UUID) string { return "m:" + id.String() }
func fileRow(id uuid.UUID) string    { return "f:" + id.String() }

// linkImageRow seals the picture of a message's link preview.
func linkImageRow(id uuid.UUID) string { return "li:" + id.String() }

// sealFor seals v for its owner's message id.
func sealFor(key []byte, owner uuid.UUID, row string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return peoplechats.Seal(key, owner, row, raw)
}

// openSealed opens a message's sealed value (empty once deleted).
func openSealed(key []byte, owner, id uuid.UUID, sealed []byte) (peopleSealed, error) {
	var v peopleSealed
	if sealed == nil {
		return v, nil
	}
	raw, err := peoplechats.Open(key, owner, messageRow(id), sealed)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v)
}

// peopleChat is one of the person's people chats, as their side shows it.
type peopleChat struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	PersonID uuid.UUID `json:"person_id"`
	// Name is theirs, or "Deleted account" (Gone) once they deleted it.
	Name string `json:"name"`
	Gone bool   `json:"gone"`
	// Suspended: their account can't get messages right now (it was not
	// deleted; its name is shown, and nothing else of them).
	Suspended bool      `json:"suspended,omitempty"`
	Last      string    `json:"last"`
	LastMine  bool      `json:"last_mine"`
	LastAt    time.Time `json:"last_at"`
	Unread    int       `json:"unread"`
	Muted     bool      `json:"muted"`
	Archived  bool      `json:"archived"`
	// Request: a stranger's first messages, waiting in your Requests until
	// you accept them (Accept, Delete, Block and report). Waiting: your
	// first messages wait for them to accept (no read ticks until then).
	Request bool `json:"request"`
	Waiting bool `json:"waiting"`
	// Blocked: you blocked them.
	Blocked bool `json:"blocked"`
	// One chat only: whether they are in your contacts, and their number
	// when they chose to show it (finding people's settings).
	Contact *bool   `json:"contact,omitempty"`
	Phone   *string `json:"phone,omitempty"`
	// Kept says how people chats are kept (one chat, and on starting one).
	Kept string `json:"kept,omitempty"`
	// Person is the other person as their settings let the person see
	// them (finding people's card: the name they were given in the
	// person's contacts, photo, last seen and online, read ticks); none
	// once the account was deleted.
	Person *personCardOut `json:"person,omitempty"`
}

// peopleLastWords is a copy's last message as the list shows it.
func peopleLastWords(key []byte, owner uuid.UUID, c peopleCopy) string {
	if c.lastID == nil {
		return ""
	}
	if c.lastDeleted != nil && *c.lastDeleted {
		return "This message was deleted"
	}
	v, err := openSealed(key, owner, *c.lastID, c.lastSealed)
	if err != nil {
		return ""
	}
	return previewOf(*c.lastKind, c.lastAttach, v)
}

// previewOf is a message in a few words (a list's line, a push's body).
func previewOf(kind string, attach *string, v peopleSealed) string {
	switch kind {
	case "location":
		return "Location"
	case "card":
		if v.Card != nil {
			return v.Card.Title
		}
		return "Card"
	case "media":
		if v.Body != "" {
			return v.Body
		}
		if attach != nil {
			return map[string]string{"photo": "Photo", "voice": "Voice note", "file": "File"}[*attach]
		}
	}
	return v.Body
}

// peopleNames are the names of the people the person chats with, and
// whose accounts were deleted (gone) or are suspended.
type peopleNames struct {
	name            map[uuid.UUID]string
	gone, suspended map[uuid.UUID]bool
}

// peopleNamesTx is the names of the people the person chats with.
func peopleNamesTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (peopleNames, error) {
	out := peopleNames{name: map[uuid.UUID]string{}, gone: map[uuid.UUID]bool{}, suspended: map[uuid.UUID]bool{}}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT person_id, name, gone, suspended FROM taskiem_people_names($1)`, ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n string
		var g, su bool
		if err := rows.Scan(&id, &n, &g, &su); err != nil {
			return out, err
		}
		out.name[id], out.gone[id], out.suspended[id] = n, g, su
	}
	return out, rows.Err()
}

// peopleViewsTx turns copies into what the person's side shows.
func (s *Server) peopleViewsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, copies []peopleCopy) ([]peopleChat, error) {
	out := make([]peopleChat, 0, len(copies))
	if len(copies) == 0 {
		return out, nil
	}
	key, err := s.keys().KeyTx(ctx, tx, person, false)
	if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
		return nil, err
	}
	ids := []uuid.UUID{}
	for _, c := range copies {
		if c.Peer != nil {
			ids = append(ids, *c.Peer)
		}
	}
	names, err := peopleNamesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	cards := map[uuid.UUID]people.Card{}
	if len(ids) > 0 {
		list, err := s.people().CardsTx(ctx, tx, person, ids)
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			cards[c.ID] = c
		}
	}
	for _, c := range copies {
		v := peopleChat{ID: c.ID, Kind: "person", LastAt: c.LastAt, Unread: c.Unread, Muted: c.Muted, Archived: c.Archived,
			Request: c.RequestAt != nil, Waiting: c.WaitingAt != nil && c.AcceptedAt == nil, Blocked: c.Blocked}
		if c.Peer != nil {
			v.PersonID = *c.Peer
			v.Name, v.Gone, v.Suspended = names.name[*c.Peer], names.gone[*c.Peer], names.suspended[*c.Peer]
		} else {
			v.Gone = true
		}
		if card, ok := cards[v.PersonID]; ok && card.Deleted && !card.Suspended && !v.Suspended {
			v.Gone = true
		}
		switch {
		case v.Gone:
			v.Name, v.Suspended = peopleGoneName, false
		case v.Suspended:
			// Their name only: no card while the account is suspended.
			if v.Name == "" {
				v.Name = "Someone"
			}
		default:
			if card, ok := cards[v.PersonID]; ok {
				o := cardOut(card)
				v.Person = &o
				if card.ContactName != nil {
					v.Name = *card.ContactName
				}
			}
			if v.Name == "" {
				v.Name = "Someone"
			}
		}
		if key != nil {
			v.Last = peopleLastWords(key, person, c)
		}
		if c.lastMine != nil {
			v.LastMine = *c.lastMine
		}
		out = append(out, v)
	}
	return out, nil
}

// peopleListPage is how many chats a list answers at once (more: ask
// again with ?before= the answer's next).
var peopleListPage = 200

// listCursor is where a page of a chats list ends: the last chat's time
// and id, newest first ("{RFC 3339 time}_{id}", opaque to clients).
type listCursor struct {
	at time.Time
	id uuid.UUID
}

func (c listCursor) String() string { return c.at.UTC().Format(time.RFC3339Nano) + "_" + c.id.String() }

// parseCursor reads ?before= ("" for the first page).
func parseCursor(s string) (*listCursor, error) {
	if s == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(s, "_")
	t, err1 := time.Parse(time.RFC3339Nano, at)
	u, err2 := uuid.Parse(id)
	if !ok || err1 != nil || err2 != nil {
		return nil, customerSendErr{http.StatusBadRequest, "before is the next of an earlier page"}
	}
	return &listCursor{t, u}, nil
}

// peopleListTx is a page of the person's people chats (limit of them, and
// whether there are more): requests or not, archived or not; never those
// deleted for them, nor one opened that no one has written in yet.
func (s *Server) peopleListTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, requests, archived bool, before *listCursor, limit int) ([]peopleChat, bool, error) {
	q := `SELECT ` + peopleCopyCols + peopleCopyFrom + ` WHERE c.person_id = $1 AND c.hidden_at IS NULL AND NOT c.empty AND (c.request_at IS NOT NULL) = $2`
	args := []any{person, requests}
	if !requests {
		args = append(args, archived)
		q += ` AND c.archived = $3`
	}
	if before != nil {
		args = append(args, before.at, before.id)
		q += fmt.Sprintf(` AND (c.last_message_at, c.id) < ($%d, $%d)`, len(args)-1, len(args))
	}
	rows, err := tx.Query(ctx, q+fmt.Sprintf(` ORDER BY c.last_message_at DESC, c.id DESC LIMIT %d`, limit+1), args...)
	if err != nil {
		return nil, false, err
	}
	copies, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (peopleCopy, error) { return scanPeopleCopy(r) })
	if err != nil {
		return nil, false, err
	}
	more := len(copies) > limit
	if more {
		copies = copies[:limit]
	}
	out, err := s.peopleViewsTx(ctx, tx, person, copies)
	return out, more, err
}

// peopleUnreadTx is how many of the others' messages the person has not
// read in their people chats (archived or not), all of them, not a page.
func peopleUnreadTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, archived bool) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM people_messages m JOIN people_chats c ON c.person_id = m.person_id AND c.id = m.chat_id
		WHERE m.person_id = $1 AND NOT m.mine AND m.read_at IS NULL AND m.deleted_at IS NULL
		  AND c.hidden_at IS NULL AND c.request_at IS NULL AND c.archived = $2`, person, archived).Scan(&n)
	return n, err
}

// peopleOneTx is one of the person's chats as their side shows it.
func (s *Server) peopleOneTx(ctx context.Context, tx pgx.Tx, person, id uuid.UUID) (peopleChat, error) {
	c, err := peopleCopyTx(ctx, tx, person, id.String(), false)
	if err != nil {
		return peopleChat{}, err
	}
	list, err := s.peopleViewsTx(ctx, tx, person, []peopleCopy{c})
	if err != nil {
		return peopleChat{}, err
	}
	return list[0], nil
}

// listPeopleChats is GET /v1/me/people?archived=1&before=: the person's
// chats with people, the latest first, 200 a page (more, and next to pass
// as before for the page after); requests are apart. unread counts every
// chat of the list, not the page alone.
func (s *Server) listPeopleChats(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	archived := r.URL.Query().Get("archived") == "1"
	var out []peopleChat
	var requests, unread int
	more := false
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		if out, more, err = s.peopleListTx(r.Context(), tx, person, false, archived, before, peopleListPage); err != nil {
			return err
		}
		if unread, err = peopleUnreadTx(r.Context(), tx, person, archived); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT count(*) FROM people_chats WHERE person_id = $1 AND request_at IS NOT NULL AND hidden_at IS NULL`, person).Scan(&requests)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"chats": out, "unread": unread, "requests": requests, "more": more}
	if more {
		last := out[len(out)-1]
		body["next"] = listCursor{last.LastAt, last.ID}.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// listPeopleRequests is GET /v1/me/people/requests?before=: strangers'
// first messages waiting for the person (Accept, Delete, Block and
// report), the latest first, 200 a page (more, and next to pass as before
// for the page after).
func (s *Server) listPeopleRequests(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out []peopleChat
	more := false
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		out, more, err = s.peopleListTx(r.Context(), tx, person, true, false, before, peopleListPage)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"requests": out, "more": more}
	if more {
		last := out[len(out)-1]
		body["next"] = listCursor{last.LastAt, last.ID}.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// chatItem is one row of You's one Chats list (§7, §17 Q6): a business
// chat or a people chat, marked by kind.
type chatItem struct {
	// Kind: business (open /v1/me/chats/{id}) or person (open
	// /v1/me/people/{id}).
	Kind       string     `json:"kind"`
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	BusinessID *uuid.UUID `json:"business_id,omitempty"`
	// Photo: a business chat's business's photo (its logo), as Orders on
	// You shows it; none while the business is not taking your messages.
	Photo *orderPhoto `json:"photo,omitempty"`
	// Verified: a business chat's trust line, what the business has
	// proved, as the business chat's head shows it (GET
	// /v1/me/chats/{id}'s verified: a phone, a check of the business, a
	// payment provider, months on Taskiem); none while the business is not
	// taking your messages.
	Verified  *businessTrust `json:"verified,omitempty"`
	PersonID  *uuid.UUID     `json:"person_id,omitempty"`
	Gone      bool           `json:"gone,omitempty"`
	Suspended bool           `json:"suspended,omitempty"`
	Last      string         `json:"last"`
	LastMine  bool           `json:"last_mine"`
	LastAt    time.Time      `json:"last_at"`
	Unread    int            `json:"unread"`
	Muted     bool           `json:"muted"`
	Archived  bool           `json:"archived"`
	Blocked   bool           `json:"blocked"`
	Waiting   bool           `json:"waiting,omitempty"`
	// Person: a people chat's other person, as GET /v1/me/people shows
	// them (photo, last seen and online by their settings).
	Person *personCardOut `json:"person,omitempty"`
	// Group: a group's row (kind group, open /v1/me/groups/{id}), as GET
	// /v1/me/groups shows it (photo, members, role, left, mentioned).
	Group *groupView `json:"group,omitempty"`
}

// listAllChats is GET /v1/me/conversations (?archived=1&before=): You's
// Chats tab, business chats, people chats and groups together, the latest
// first, each marked by kind, 200 a page (more, and next to pass as before
// for the page after); requests are counted (GET /v1/me/people/requests
// lists them), and invites to groups (GET /v1/me/groups/invites), and
// unread counts every chat of the list, not the page alone.
// GET /v1/me/chats stays the business chats alone.
func (s *Server) listAllChats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	archived := r.URL.Query().Get("archived") == "1"
	limit := peopleListPage
	items := []chatItem{}
	var requests, unread, invites int
	morePeople, moreGroups := false, false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		q := `SELECT ` + personChatCols + ` FROM person_conversations c WHERE c.person_id = $1 AND c.archived = $2`
		args := []any{person, archived}
		if before != nil {
			q += ` AND (c.last_message_at, c.id) < ($3, $4)`
			args = append(args, before.at, before.id)
		}
		rows, err := tx.Query(ctx, q+fmt.Sprintf(` ORDER BY c.last_message_at DESC, c.id DESC LIMIT %d`, limit+1), args...)
		if err != nil {
			return err
		}
		biz, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (personChat, error) { return scanPersonChat(r) })
		if err != nil {
			return err
		}
		if err := namesTx(ctx, tx, biz); err != nil {
			return err
		}
		photos, err := businessPhotosTx(ctx, tx, biz)
		if err != nil {
			return err
		}
		trust, err := businessTrustTx(ctx, tx, biz)
		if err != nil {
			return err
		}
		for _, c := range biz {
			b := c.Business
			it := chatItem{Kind: "business", ID: c.ID, Name: c.Name, BusinessID: &b, Last: c.Last, LastMine: c.LastMine, LastAt: c.LastAt,
				Unread: c.Unread, Muted: c.Muted, Archived: c.Archived, Blocked: c.Blocked}
			if !c.BlockedYou {
				it.Photo, it.Verified = photos[b], trust[b]
			}
			items = append(items, it)
		}
		people, peopleMore, err := s.peopleListTx(ctx, tx, person, false, archived, before, limit)
		if err != nil {
			return err
		}
		morePeople = peopleMore
		for _, c := range people {
			p := c.PersonID
			items = append(items, chatItem{Kind: "person", ID: c.ID, Name: c.Name, PersonID: &p, Gone: c.Gone, Suspended: c.Suspended, Last: c.Last, LastMine: c.LastMine,
				LastAt: c.LastAt, Unread: c.Unread, Muted: c.Muted, Archived: c.Archived, Blocked: c.Blocked, Waiting: c.Waiting, Person: c.Person})
		}
		groups, more, err := s.groupListTx(ctx, tx, person, archived, before, limit)
		if err != nil {
			return err
		}
		moreGroups = more
		if err := groupDeliveredTx(ctx, tx, person, groups); err != nil {
			return err
		}
		for i := range groups {
			g := groups[i]
			items = append(items, chatItem{Kind: "group", ID: g.ID, Name: g.Name, Last: g.Last, LastMine: g.LastMine, LastAt: g.LastAt,
				Unread: g.Unread, Muted: g.Muted, Archived: g.Archived, Group: &g})
		}
		groupUnread, err := groupUnreadTx(ctx, tx, person, archived)
		if err != nil {
			return err
		}
		if invites, err = groupInvitesCountTx(ctx, tx, person); err != nil {
			return err
		}
		var bizUnread int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM person_messages m JOIN person_conversations c ON c.person_id = m.person_id AND c.id = m.conversation_id
			WHERE c.person_id = $1 AND c.archived = $2 AND NOT m.mine AND m.read_at IS NULL`, person, archived).Scan(&bizUnread); err != nil {
			return err
		}
		peopleUnread, err := peopleUnreadTx(ctx, tx, person, archived)
		if err != nil {
			return err
		}
		unread = bizUnread + peopleUnread + groupUnread
		return tx.QueryRow(ctx, `SELECT count(*) FROM people_chats WHERE person_id = $1 AND request_at IS NOT NULL AND hidden_at IS NULL`, person).Scan(&requests)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	// Newest first, the same order the cursor reads: time, then id.
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].LastAt.Equal(items[j].LastAt) {
			return items[i].LastAt.After(items[j].LastAt)
		}
		return bytes.Compare(items[i].ID[:], items[j].ID[:]) > 0
	})
	// More: either list had more than a page, or the two together do.
	more := morePeople || moreGroups || len(items) > limit
	if len(items) > limit {
		items = items[:limit]
	}
	body := map[string]any{"chats": items, "unread": unread, "requests": requests, "invites": invites, "more": more}
	if more {
		last := items[len(items)-1]
		body["next"] = listCursor{last.LastAt, last.ID}.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// businessPhotosTx is the photo of each chat's business (its logo, by
// taskiem_business_photo's rule), through taskiem_customer_business_photos.
func businessPhotosTx(ctx context.Context, tx pgx.Tx, chats []personChat) (map[uuid.UUID]*orderPhoto, error) {
	out := map[uuid.UUID]*orderPhoto{}
	if len(chats) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(chats))
	for i, c := range chats {
		ids[i] = c.Business
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id, photo FROM taskiem_customer_business_photos($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var urls map[string]string
		if err := rows.Scan(&id, &urls); err != nil {
			return nil, err
		}
		if len(urls) > 0 {
			out[id] = &orderPhoto{URLs: urls}
		}
	}
	return out, rows.Err()
}

// businessTrustTx is what each chat's business has proved (the trust
// line of the business chat's head, businessHead), through
// taskiem_customer_business_trust.
func businessTrustTx(ctx context.Context, tx pgx.Tx, chats []personChat) (map[uuid.UUID]*businessTrust, error) {
	out := map[uuid.UUID]*businessTrust{}
	if len(chats) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(chats))
	for i, c := range chats {
		ids[i] = c.Business
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id, phone, business, payments, since FROM taskiem_customer_business_trust($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var id uuid.UUID
		var t businessTrust
		var since time.Time
		if err := rows.Scan(&id, &t.Phone, &t.Business, &t.Payments, &since); err != nil {
			return nil, err
		}
		t.Months = monthsSince(since, now)
		out[id] = &t
	}
	return out, rows.Err()
}

// startPeopleChat is POST /v1/me/people {person_id | link_code}: opens the
// person's chat with someone found by finding people (their number, their
// link, their contacts), or answers the one they have. Nothing reaches the
// other person until the first message, which waits as a request unless
// they know each other.
func (s *Server) startPeopleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		PersonID *uuid.UUID `json:"person_id"`
		LinkCode string     `json:"link_code"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var peer uuid.UUID
	switch {
	case in.PersonID != nil && in.LinkCode == "":
		peer = *in.PersonID
	case in.PersonID == nil && in.LinkCode != "":
		p, err := s.personLinks().ResolvePersonLink(ctx, strings.TrimSpace(in.LinkCode))
		switch {
		case errors.Is(err, peoplechats.ErrLinkReplaced):
			writeJSON(w, http.StatusGone, map[string]any{"error": "This link no longer works. Ask them for their new link.", "link_replaced": true})
			return
		case errors.Is(err, peoplechats.ErrLinkUnknown):
			writeErr(w, http.StatusNotFound, "This link does not open a chat with anyone.")
			return
		case err != nil:
			s.fail(w, r, err)
			return
		}
		peer = p
	default:
		writeErr(w, http.StatusBadRequest, "give a person_id or a person's link_code")
		return
	}
	if !s.limiter("people-start:"+person.String(), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	var out peopleChat
	fresh := false
	start := func(tx pgx.Tx) error {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM people_chats WHERE person_id = $1 AND peer_id = $2`, person, peer).Scan(&id)
		if err == nil {
			// Opened again: shown again (deleted for me earlier).
			if _, err := tx.Exec(ctx, `UPDATE people_chats SET hidden_at = NULL WHERE person_id = $1 AND id = $2 AND request_at IS NULL`, person, id); err != nil {
				return err
			}
			out, err = s.peopleOneTx(ctx, tx, person, id)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_people_writable($1)`, peer).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errPeopleNoPerson
		}
		allowed, _, err := s.mayMessageTx(ctx, tx, person, peer)
		if err != nil {
			return err
		}
		if !allowed {
			return errPeopleNotTaking
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('people-start:' || $1::text, 0))`, person); err != nil {
			return err
		}
		st, err := standingTx(ctx, tx, person)
		if err != nil {
			return err
		}
		if st.Suspended {
			return errPeopleYouSuspended
		}
		sig, err := senderSignalsTx(ctx, tx, person, st)
		if err != nil {
			return err
		}
		slowed := peoplesafety.Slowed(sig)
		var hour, day int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE created_at > now() - interval '1 hour'), count(*) FILTER (WHERE created_at > now() - interval '1 day')
			FROM people_chats WHERE person_id = $1 AND started_by_me`, person).Scan(&hour, &day); err != nil {
			return err
		}
		if hour >= limitFor(peopleChatsPerHour, slowed) || day >= limitFor(peopleChatsPerDay, slowed) {
			return customerSendErr{http.StatusTooManyRequests, "You have started many new chats today. Try again later."}
		}
		id = uuid.Must(uuid.NewV7())
		// Empty until the first message: the lists leave it out.
		if _, err := tx.Exec(ctx, `INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, empty) VALUES ($1, $2, $3, taskiem_people_pair($2, $3), true, true)`,
			id, person, peer); err != nil {
			return err
		}
		fresh = true
		out, err = s.peopleOneTx(ctx, tx, person, id)
		return err
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, start)
	if isUnique(err) {
		fresh = false
		err = db.InPersonTx(ctx, s.Store.Pool, person, start)
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out.Kept = peopleKeptWords
	code := http.StatusOK
	if fresh {
		code = http.StatusCreated
	}
	writeJSON(w, code, out)
}

// getPeopleChat is GET /v1/me/people/{id}: one chat, with whether they
// are a contact and their number when they show it.
func (s *Server) getPeopleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out peopleChat
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if out, err = s.peopleOneTx(ctx, tx, person, c.ID); err != nil {
			return err
		}
		if out.Gone {
			return nil
		}
		contact, err := s.isContactTx(ctx, tx, person, out.PersonID)
		if err != nil {
			return err
		}
		out.Contact = &contact
		if out.Suspended {
			return nil
		}
		// Their number, only when they chose to show it; never while
		// either blocked the other.
		shown, err := s.numberShownToTx(ctx, tx, out.PersonID, person)
		if err != nil || !shown || out.Blocked {
			return err
		}
		if _, err := peopleReachTx(ctx, tx, c.ID); err != nil {
			if reachQuiet(err) {
				return nil
			}
			return err
		}
		if theyBlocked, err := theyBlockedTx(ctx, tx, out.PersonID, person); err != nil || theyBlocked {
			return err
		}
		return tx.QueryRow(ctx, `SELECT taskiem_people_phone($1)`, out.PersonID).Scan(&out.Phone)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out.Kept = peopleKeptWords
	writeJSON(w, http.StatusOK, out)
}

// putPeopleChat is PUT /v1/me/people/{id} {muted?, archived?}: the
// person's own settings for the chat.
func (s *Server) putPeopleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Muted    *bool `json:"muted"`
		Archived *bool `json:"archived"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Muted == nil && in.Archived == nil {
		writeErr(w, http.StatusBadRequest, "muted or archived is required")
		return
	}
	var out peopleChat
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE people_chats SET muted = COALESCE($3, muted), archived = COALESCE($4, archived) WHERE person_id = $1 AND id = $2`,
			person, c.ID, in.Muted, in.Archived); err != nil {
			return err
		}
		out, err = s.peopleOneTx(ctx, tx, person, c.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// clearCopyTx deletes every message of the person's copy (and their files).
func clearCopyTx(ctx context.Context, tx pgx.Tx, person, chat uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM people_messages WHERE person_id = $1 AND chat_id = $2`, person, chat)
	return err
}

// deletePeopleChat is DELETE /v1/me/people/{id}: the chat deleted for the
// person: their copy's messages go, and it is hidden until a new message.
// The other person's copy is theirs and stays. A request deleted is
// declined.
func (s *Server) deletePeopleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if c.RequestAt != nil && c.HiddenAt == nil {
			return declineTx(ctx, tx, person, c)
		}
		if err := clearCopyTx(ctx, tx, person, c.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE people_chats SET hidden_at = now(), archived = false WHERE person_id = $1 AND id = $2`, person, c.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// acceptTx accepts a request waiting for the person: it moves to their
// chats, the sender's copy stops waiting, and the read marks the person
// made meanwhile are shown to the sender from now on, when read ticks go
// between them (ticks: both have them on).
func acceptTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, c peopleCopy, ticks bool) error {
	if _, err := tx.Exec(ctx, `UPDATE people_chats SET request_at = NULL, accepted_at = COALESCE(accepted_at, now()), hidden_at = NULL WHERE person_id = $1 AND id = $2`,
		person, c.ID); err != nil {
		return err
	}
	if c.Peer != nil {
		if _, err := tx.Exec(ctx, `SELECT taskiem_people_request_mark($1, 'accepted')`, *c.Peer); err != nil {
			return err
		}
	}
	// The sender's copy shows it accepted even while they are suspended
	// (they still write to no one); a deleted account has no copy.
	peer, ok, err := peopleForgetReachTx(ctx, tx, c.ID)
	if err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE people_chats SET waiting_at = NULL, accepted_at = COALESCE(accepted_at, now()) WHERE person_id = $1 AND conversation_id = $2`,
		peer, c.Conversation); err != nil {
		return err
	}
	if ticks {
		if _, err := tx.Exec(ctx, `UPDATE people_messages p SET read_at = m.read_at FROM people_messages m
			WHERE m.person_id = $1 AND m.chat_id = $2 AND NOT m.mine AND m.read_at IS NOT NULL
			  AND p.person_id = $3 AND p.conversation_id = $4 AND p.id = m.id AND p.mine AND p.read_at IS NULL`, person, c.ID, peer, c.Conversation); err != nil {
			return err
		}
	}
	return nil
}

// declineTx deletes a request: the person's copy goes. The sender is not
// told; their own copy stays as it is, and a later message from them is a
// request again.
func declineTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, c peopleCopy) error {
	if c.Peer != nil {
		if _, err := tx.Exec(ctx, `SELECT taskiem_people_request_mark($1, 'deleted')`, *c.Peer); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM people_chats WHERE person_id = $1 AND id = $2`, person, c.ID); err != nil {
		return err
	}
	return goneTx(ctx, tx, person, c.ID, uuid.Nil)
}

// acceptPeopleRequest is POST /v1/me/people/{id}/accept: the request moves
// to the person's chats.
func (s *Server) acceptPeopleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out peopleChat
	var peer *uuid.UUID
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if c.RequestAt == nil {
			return customerSendErr{http.StatusConflict, "This chat is not a request."}
		}
		// Accepting writes to the sender's copy: not while the person is
		// suspended.
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		ticks, err := s.readTicksTx(ctx, tx, person, c.Peer)
		if err != nil {
			return err
		}
		if err := acceptTx(ctx, tx, person, c, ticks); err != nil {
			return err
		}
		peer = c.Peer
		out, err = s.peopleOneTx(ctx, tx, person, c.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	// Accepting adds them to the person's contacts (from a chat).
	if peer != nil && s.addChatContact(ctx, person, *peer) {
		yes := true
		out.Contact = &yes
	}
	writeJSON(w, http.StatusOK, out)
}

// declinePeopleRequest is POST /v1/me/people/{id}/decline: Delete on a
// request. The sender is not told.
func (s *Server) declinePeopleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if c.RequestAt == nil {
			return customerSendErr{http.StatusConflict, "This chat is not a request."}
		}
		return declineTx(ctx, tx, person, c)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reportInput is a report's body.
type reportInput struct {
	Reason  string `json:"reason"`
	Note    string `json:"note"`
	Consent bool   `json:"consent"`
}

func (in *reportInput) check() error {
	switch in.Reason {
	case "spam", "scam", "abuse", "other":
	default:
		return customerSendErr{http.StatusBadRequest, "reason is spam, scam, abuse or other"}
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 1000 {
		return customerSendErr{http.StatusBadRequest, "a note is up to 1,000 characters"}
	}
	if !in.Consent {
		return customerSendErr{http.StatusBadRequest, "A report sends the last 20 messages to Taskiem's moderators: agree to send them (consent) to report."}
	}
	return nil
}

// reportLimited takes one of the person's reports (shared with reports of
// businesses), or answers false.
func (s *Server) reportLimited(person uuid.UUID) bool {
	return !s.limiter("person-report:"+person.String(), 2*time.Hour, 12).Allow()
}

// reportPerson is POST /v1/me/people/{id}/report {reason, note?, consent}:
// the person, and the chat's last 20 messages from the server's copy (the
// reporter's), to Taskiem's moderators, only with consent.
func (s *Server) reportPerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in reportInput
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.personFail(w, r, err)
		return
	}
	if s.reportLimited(person) {
		writeErr(w, http.StatusTooManyRequests, "You have sent many reports. Try again later.")
		return
	}
	var id uuid.UUID
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		id, err = s.reportTx(ctx, tx, person, c, in)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "messages_sent": true,
		"said": "Thank you. Taskiem's moderators will look at this chat. You can also block this person."})
}

// blockPerson is POST /v1/me/people/{id}/block {report?}: they can no
// longer message the person (their messages show one tick, nothing else
// tells them), nor see their number. Blocking a request removes it, and
// may report them at once ("Block and report").
func (s *Server) blockPerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Report *reportInput `json:"report"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if in.Report != nil {
		if err := in.Report.check(); err != nil {
			s.personFail(w, r, err)
			return
		}
		if s.reportLimited(person) {
			writeErr(w, http.StatusTooManyRequests, "You have sent many reports. Try again later.")
			return
		}
	}
	var reported, removed bool
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		reported, removed = false, false
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if c.Peer == nil {
			return errPeopleGone
		}
		if in.Report != nil {
			if _, err := s.reportTx(ctx, tx, person, c, *in.Report); err != nil {
				return err
			}
			reported = true
		}
		if _, err := tx.Exec(ctx, `INSERT INTO people_blocks (person_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, person, *c.Peer); err != nil {
			return err
		}
		if c.RequestAt != nil && c.HiddenAt == nil {
			// The request goes; the copy stays, hidden, so the block is
			// listed by name, and a later message is still a request.
			if _, err := tx.Exec(ctx, `SELECT taskiem_people_request_mark($1, 'blocked')`, *c.Peer); err != nil {
				return err
			}
			if err := clearCopyTx(ctx, tx, person, c.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE people_chats SET hidden_at = now() WHERE person_id = $1 AND id = $2`, person, c.ID); err != nil {
				return err
			}
			removed = true
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocked": true, "reported": reported, "removed": removed})
}

// unblockPerson is DELETE /v1/me/people/{id}/block. What they sent while
// blocked never arrives.
func (s *Server) unblockPerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM people_blocks WHERE person_id = $1 AND blocked_id = $2`, person, c.Peer)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocked": false})
}

// blockedPerson is someone the person blocked.
type blockedPerson struct {
	PersonID uuid.UUID `json:"person_id"`
	Name     string    `json:"name"`
	ChatID   uuid.UUID `json:"chat_id"`
	At       time.Time `json:"at"`
}

// listPeopleBlocked is GET /v1/me/people/blocked: the people the person
// blocked (unblock with DELETE /v1/me/people/{chat_id}/block).
func (s *Server) listPeopleBlocked(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []blockedPerson{}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT b.blocked_id, c.id, b.created_at FROM people_blocks b JOIN people_chats c ON c.person_id = b.person_id AND c.peer_id = b.blocked_id
			WHERE b.person_id = $1 ORDER BY b.created_at DESC LIMIT 500`, person)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (blockedPerson, error) {
			var b blockedPerson
			return b, r.Scan(&b.PersonID, &b.ChatID, &b.At)
		}); err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(out))
		for i, b := range out {
			ids[i] = b.PersonID
		}
		names, err := peopleNamesTx(ctx, tx, ids)
		for i := range out {
			out[i].Name = names.name[out[i].PersonID]
			if names.gone[out[i].PersonID] {
				out[i].Name = peopleGoneName
			}
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": out})
}

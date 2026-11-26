package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
	"github.com/israel-duff/taskiem/engine/push"
)

// peopleLocation is a place shared in a people chat.
type peopleLocation struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label,omitempty"`
}

// peopleMessage is one message of the person's copy.
type peopleMessage struct {
	ID   uuid.UUID `json:"id"`
	Mine bool      `json:"mine"`
	// Kind: text, media (a photo, voice note or file), card or location.
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Card       *peopleCard     `json:"card,omitempty"`
	Location   *peopleLocation `json:"location,omitempty"`
	Attachment *chatAttachment `json:"attachment,omitempty"`
	// Preview is the first link's preview; its picture is at
	// GET /v1/me/people/{id}/previews/{message}.
	Preview *links.Preview `json:"preview,omitempty"`
	ReplyTo *uuid.UUID     `json:"reply_to,omitempty"`
	// Forwarded, and forwarded many times (five or more on its way here):
	// it can be forwarded to one chat at a time only.
	Forwarded     bool `json:"forwarded,omitempty"`
	ForwardedMany bool `json:"forwarded_many,omitempty"`
	// Status of the person's own message: sent (one tick: not delivered,
	// as while they are blocked), delivered, read (shown only once the
	// chat is accepted).
	Status        string `json:"status,omitempty"`
	Starred       bool   `json:"starred,omitempty"`
	MyReaction    string `json:"my_reaction,omitempty"`
	TheirReaction string `json:"their_reaction,omitempty"`
	// Deleted for everyone: nothing of it is kept.
	Deleted bool `json:"deleted,omitempty"`
	// WarnTransfer: their message asks for a transfer to an account; the
	// app warns before anyone pays (as in business chats).
	WarnTransfer bool `json:"warn_transfer,omitempty"`
	// Warn: the safety checks found it looks like a scam or spam.
	Warn      bool       `json:"warn,omitempty"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
	// EditedAt is when its sender last changed its words ("Edited"), and
	// Edits how many times; an edit names the count it saw.
	EditedAt *time.Time `json:"edited_at,omitempty"`
	Edits    int        `json:"edits"`
}

const peopleMessageCols = `id, mine, kind, sealed, attachment, card_type, reply_to, forwards, delivered_at, read_at, starred_at IS NOT NULL,
	COALESCE(my_reaction, ''), COALESCE(their_reaction, ''), deleted_at IS NOT NULL, COALESCE(screen, ''), paid_at, paid_by_me, created_at, edited_at, edits`

// peopleRow is a message as kept, with its sealed value.
type peopleRow struct {
	peopleMessage
	sealed    []byte
	attach    map[string]any
	cardType  *string
	forwards  int
	delivered *time.Time
	paidAt    *time.Time
	paidByMe  *bool
	// screen is the checks' mark on the other's message: "", warn, or
	// transfer (a share card naming an account, from someone not added).
	screen string
}

// scanPeopleRow scans peopleMessageCols, then any extra columns selected
// after them into extra.
func scanPeopleRow(r pgx.Row, extra ...any) (peopleRow, error) {
	var m peopleRow
	dest := append([]any{&m.ID, &m.Mine, &m.Kind, &m.sealed, &m.attach, &m.cardType, &m.ReplyTo, &m.forwards, &m.delivered, &m.ReadAt, &m.Starred,
		&m.MyReaction, &m.TheirReaction, &m.Deleted, &m.screen, &m.paidAt, &m.paidByMe, &m.CreatedAt, &m.EditedAt, &m.Edits}, extra...)
	err := r.Scan(dest...)
	m.Warn = m.screen != ""
	return m, err
}

// view opens the row under the owner's key and makes what the app shows.
func (m peopleRow) view(key []byte, owner uuid.UUID) (peopleMessage, peopleSealed) {
	out := m.peopleMessage
	out.Forwarded, out.ForwardedMany = m.forwards > 0, m.forwards >= peopleForwardedMany
	if out.Mine {
		switch {
		case m.delivered == nil:
			out.Status = "sent"
		case m.ReadAt != nil:
			out.Status = "read"
		default:
			out.Status = "delivered"
		}
	} else {
		// Read marks of the other's messages are the person's own.
		out.Status = ""
	}
	var v peopleSealed
	if out.Deleted {
		out.Body = "This message was deleted"
		return out, v
	}
	if key != nil {
		v, _ = openSealed(key, owner, m.ID, m.sealed)
	}
	out.Body, out.Card, out.Location, out.Preview = v.Body, v.Card, v.Location, v.Preview
	if m.attach != nil {
		a := &chatAttachment{Name: v.Name, Facts: media.FactsOf(m.attach)}
		a.Kind, _ = m.attach["kind"].(string)
		a.Type, _ = m.attach["type"].(string)
		if n, ok := m.attach["size"].(float64); ok {
			a.Size = int64(n)
		}
		out.Attachment = a
	}
	if out.Card != nil && out.Card.Type == "split" {
		out.Card.Paid = m.paidAt != nil
		out.Card.PaidAt = m.paidAt
		out.Card.PaidByMe = m.paidByMe
	}
	// A saved thing's card: masked in its sender's copy; naming an account,
	// it warns as a transfer does.
	if out.Card != nil && out.Card.Type == "saved" && out.Card.Saved != nil {
		c := *out.Card
		c.Saved = maskMine(out.Mine, c.Saved)
		out.Card = &c
	}
	savedPay := out.Card != nil && out.Card.Type == "saved" && out.Card.Saved != nil && out.Card.Saved.NamesAccount()
	out.WarnTransfer = !out.Mine && ((out.Kind == "text" && warnTransfer(out.Body)) || m.screen == "transfer" || savedPay)
	return out, v
}

// peopleDraft is a message to send.
type peopleDraft struct {
	kind     string
	body     string
	card     *peopleCard
	location *peopleLocation
	file     *sentFile
	// preview is the first link's preview, made before the message is
	// written (link_preview.go).
	preview  *madePreview
	replyTo  *uuid.UUID
	forwards int
	clientID string
	// released: a moderator released this message, which the checks held
	// (people_held): it goes as the sender meant, past the checks and the
	// limits on sending.
	released bool
}

// peoplePush is a push owed to the other person once a message is kept.
type peoplePush struct {
	to, chat     uuid.UUID
	request      bool
	title, words string
}

// screenText is what the checks on strangers' messages read of a draft:
// its words, a share card's what and the account it names, and a place's
// label (cards of products, businesses and orders are made by the server
// from what they are, so carry no words of the sender's).
func (d peopleDraft) screenText() string {
	parts := []string{d.body}
	if d.card != nil && d.card.Type == "split" {
		parts = append(parts, d.card.What)
		if a := d.card.PayTo; a != nil {
			parts = append(parts, "Pay to "+a.Bank+" account "+a.AccountNumber+" "+a.AccountName)
		}
	}
	// A saved thing's card carries the sender's own words: its title and
	// lines are read as a message's are.
	if d.card != nil && d.card.Type == "saved" && d.card.Saved != nil {
		parts = append(parts, d.card.Saved.Words())
	}
	if d.location != nil {
		parts = append(parts, d.location.Label)
	}
	out := []string{}
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// asksToBePaid is a card naming an account to pay: a share card, or a
// saved thing's with a bank account in it.
func (d peopleDraft) asksToBePaid() bool {
	if d.card != nil && d.card.Type == "saved" && d.card.Saved != nil {
		return d.card.Saved.NamesAccount()
	}
	return d.card != nil && d.card.Type == "split" && d.card.PayTo != nil
}

// peopleSend writes the person's message to their copy and, unless it is
// not to be delivered, to the other's copy, in one transaction; fresh is
// false for a repeat of a client_id. A reply to a request accepts it.
//
// Nothing the sender is answered tells them they are blocked: the decision
// (a request or not, refused by "who can message me", the limits) is made
// as if there were no block, and a blocked message, like one into a
// request whose recipient now takes no new chats, is kept on the sender's
// side with one tick.
//
// The transaction locks the conversation first (lockConversationTx, in
// peopleCopyTx), then, for a message the limits on requests or the checks
// count, the sender (lockSenderTx); it reads every answer of finding
// people in the same transaction, so it holds one connection.
func (s *Server) peopleSend(ctx context.Context, me uuid.UUID, chatID string, d peopleDraft) (peopleMessage, bool, error) {
	var out peopleMessage
	var again *peopleMessage
	var owed *peoplePush
	var acceptedFrom *uuid.UUID
	var held error
	// Whose copies of a PDF get its picture, once the send committed.
	var drawFor []uuid.UUID
	keys := s.keys()
	err := s.peopleTx(ctx, me, func(tx pgx.Tx) error {
		out, again, owed, acceptedFrom, held, drawFor = peopleMessage{}, nil, nil, nil, nil, nil
		c, err := peopleCopyTx(ctx, tx, me, chatID, true)
		if err != nil {
			return err
		}
		if again, err = s.peopleRepeatTx(ctx, tx, me, c, d.clientID); err != nil || again != nil {
			return err
		}
		// A send the checks held, retried: answered as it was, nothing
		// written or counted again.
		if !d.released {
			h, err := heldAgainTx(ctx, tx, &c.ID, nil, d.clientID)
			if err != nil {
				return err
			}
			if h != nil {
				if h.state == "released" {
					if again, err = s.peopleRepeatTx(ctx, tx, me, c, releasedClientID(h.id)); err != nil || again != nil {
						return err
					}
				}
				held = customerSendErr{http.StatusForbidden, peopleHeldWords}
				return nil
			}
		}
		if c.Peer == nil {
			return errPeopleGone
		}
		if c.Blocked {
			return errPeopleYouBlocked
		}
		// What Taskiem's moderators decided (people_safety.go): a suspended
		// person writes to no one; a limited one is slowed as in a first
		// week.
		st, err := standingTx(ctx, tx, me)
		if err != nil {
			return err
		}
		if st.Suspended {
			return errPeopleYouSuspended
		}
		newWeek, err := personNewWeek(ctx, tx, me)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM people_messages WHERE person_id = $1 AND chat_id = $2 AND mine AND created_at > now() - interval '1 minute'`,
			me, c.ID).Scan(&n); err != nil {
			return err
		}
		if n >= limitFor(peopleMessagesPerMinute, newWeek || st.Limited) && !d.released {
			return customerSendErr{http.StatusTooManyRequests, "You are sending messages very fast. Wait a moment and try again."}
		}
		if d.replyTo != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people_messages WHERE person_id = $1 AND chat_id = $2 AND id = $3)`, me, c.ID, *d.replyTo).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return customerSendErr{http.StatusBadRequest, "reply_to is not a message of this chat"}
			}
		}
		peer, err := peopleReachTx(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		theyBlocked, err := theyBlockedTx(ctx, tx, peer, me)
		if err != nil {
			return err
		}
		var pc *peopleCopy
		var pcID uuid.UUID
		var pcRequest, pcMuted bool
		if err := tx.QueryRow(ctx, `SELECT id, request_at IS NOT NULL, muted FROM people_chats WHERE person_id = $1 AND conversation_id = $2`, peer, c.Conversation).
			Scan(&pcID, &pcRequest, &pcMuted); err == nil {
			pc = &peopleCopy{ID: pcID}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Whether they have written to me is kept on my copy (a lasting
		// mark: "delete for me" does not make a friend a stranger again).
		peerWrote := c.PeerWroteAt != nil
		deliver := !theyBlocked
		asRequest, newRequest, accepted := false, false, false
		switch {
		case pc != nil && !pcRequest:
		case pc != nil && pcRequest:
			// Still waiting in their Requests: their "who can message me"
			// may have changed meanwhile. Refused now, it stays on my side
			// with one tick, as a blocked message does; a contact now, it
			// stays a request until they accept it.
			asRequest = true
			allowed, _, err := s.mayMessageTx(ctx, tx, me, peer)
			if err != nil {
				return err
			}
			if !allowed {
				deliver = false
			}
		case c.AcceptedAt != nil:
			accepted = true
		default:
			allowed, req, err := s.mayMessageTx(ctx, tx, me, peer)
			if err != nil {
				return err
			}
			if !allowed {
				return errPeopleNotTaking
			}
			asRequest, newRequest, accepted = req, req, !req
		}
		// The automated checks look at strangers' messages: requests, and a
		// new chat until the other has written (§8.4); only the verdict is
		// kept, in this transaction.
		text := d.screenText()
		screen := (asRequest || !peerWrote) && text != "" && !d.released
		if newRequest || screen {
			if err := lockSenderTx(ctx, tx, me); err != nil {
				return err
			}
		}
		if newRequest && !d.released {
			// The sender's requests: waiting at once, and in a day (§8.2),
			// by their account's age, what became of their requests, the
			// reports against them, their device and the moderators'
			// limit (peoplesafety.Allowance).
			var waiting, today int
			if err := tx.QueryRow(ctx, `SELECT waiting, today FROM taskiem_people_request_counts()`).Scan(&waiting, &today); err != nil {
				return err
			}
			sig, err := senderSignalsTx(ctx, tx, me, st)
			if err != nil {
				return err
			}
			maxWaiting, perDay := peoplesafety.Allowance(sig)
			if waiting >= maxWaiting || today >= perDay {
				return customerSendErr{http.StatusTooManyRequests, "You have many messages waiting for people to accept them. Try again once some are answered."}
			}
		}
		warn := ""
		if screen {
			v, err := s.screenTx(ctx, tx, me, peer, text)
			if err != nil {
				return err
			}
			switch v.Action {
			case peoplechats.Refuse:
				why := v.Reason
				if why == "" {
					why = "This message was not sent: it looks like spam."
				}
				// Nothing else is written yet: commit the verdict alone, and
				// the message, sealed, for moderators to release or confirm
				// (people_held); the other person gets nothing.
				held = customerSendErr{http.StatusForbidden, why}
				return s.holdTx(ctx, tx, me, peer, c.ID, d, v.Reasons)
			case peoplechats.Warn:
				warn = "warn"
			}
		}
		// A share card naming an account, from someone the recipient has
		// not added, always warns as a transfer does (§11 A), request or
		// not.
		if d.asksToBePaid() {
			known, err := s.theyAddedTx(ctx, tx, me, peer)
			if err != nil {
				return err
			}
			if !known {
				warn = "transfer"
			}
		}
		// Writing back to a request accepts it.
		if c.RequestAt != nil {
			ticks, err := s.readTicksTx(ctx, tx, me, &peer)
			if err != nil {
				return err
			}
			if err := acceptTx(ctx, tx, me, c, ticks); err != nil {
				return err
			}
			c.RequestAt, c.HiddenAt = nil, nil
			acceptedFrom = &peer
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, me).Scan(&name); err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		content := peopleSealed{Body: d.body, Card: d.card, Location: d.location, Preview: d.preview.view()}
		var attach map[string]any
		if d.file != nil {
			content.Name = d.file.Name
			attach = d.file.stored()
			drawFor = []uuid.UUID{me}
		}
		var cardType *string
		if d.card != nil {
			cardType = &d.card.Type
		}
		myKey, err := keys.KeyTx(ctx, tx, me, true)
		if err != nil {
			return err
		}
		if err := insertPeopleMessage(ctx, tx, myKey, me, c.ID, c.Conversation, id, true, d, content, attach, cardType, ""); err != nil {
			return err
		}
		// A new request is in the ledger whether or not it is delivered, so
		// a blocked sender's allowance runs down as anyone's does.
		if newRequest {
			if _, err := tx.Exec(ctx, `SELECT taskiem_people_request_mark($1, 'waiting')`, peer); err != nil {
				return err
			}
		}
		if deliver {
			if pc == nil {
				pcID = uuid.Must(uuid.NewV7())
				if _, err := tx.Exec(ctx, `INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, request_at, accepted_at, peer_wrote_at)
					VALUES ($1, $2, $3, $4, false, CASE WHEN $5 THEN now() END, CASE WHEN $6 THEN now() END, now())`, pcID, peer, me, c.Conversation, asRequest, accepted); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `UPDATE people_chats SET hidden_at = NULL, last_message_at = now(), empty = false, peer_wrote_at = COALESCE(peer_wrote_at, now())
				WHERE person_id = $1 AND id = $2`, peer, pcID); err != nil {
				return err
			}
			peerKey, err := keys.KeyTx(ctx, tx, peer, true)
			if err != nil {
				return err
			}
			if err := insertPeopleMessage(ctx, tx, peerKey, peer, pcID, c.Conversation, id, false, d, content, attach, cardType, warn); err != nil {
				return err
			}
			if drawFor != nil {
				drawFor = append(drawFor, peer)
			}
			if _, err := tx.Exec(ctx, `UPDATE people_messages SET delivered_at = now() WHERE person_id = $1 AND id = $2`, me, id); err != nil {
				return err
			}
			// A message sent ends its typing on their side.
			if _, err := tx.Exec(ctx, `DELETE FROM people_typing WHERE person_id = $1 AND chat_id = $2`, peer, pcID); err != nil {
				return err
			}
			if !pcMuted {
				owed = &peoplePush{to: peer, chat: pcID, request: asRequest, title: name,
					words: previewOf(d.kind, attachKind(attach), content)}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE people_chats SET last_message_at = now(), hidden_at = NULL, empty = false,
			waiting_at = CASE WHEN $3 AND accepted_at IS NULL THEN COALESCE(waiting_at, now()) ELSE waiting_at END,
			accepted_at = CASE WHEN $4 THEN COALESCE(accepted_at, now()) ELSE accepted_at END WHERE person_id = $1 AND id = $2`,
			me, c.ID, asRequest, accepted); err != nil {
			return err
		}
		row, err := scanPeopleRow(tx.QueryRow(ctx, `SELECT `+peopleMessageCols+` FROM people_messages WHERE person_id = $1 AND id = $2`, me, id))
		if err != nil {
			return err
		}
		out, _ = row.view(myKey, me)
		return nil
	})
	if again != nil {
		return *again, false, nil
	}
	if err == nil && held != nil {
		err = held
	}
	if err != nil {
		if isUnique(err) && d.clientID != "" {
			var m *peopleMessage
			if e := db.InPersonTx(ctx, s.Store.Pool, me, func(tx pgx.Tx) error {
				c, err := peopleCopyTx(ctx, tx, me, chatID, false)
				if err != nil {
					return err
				}
				m, err = s.peopleRepeatTx(ctx, tx, me, c, d.clientID)
				return err
			}); e == nil && m != nil {
				return *m, false, nil
			}
		}
		return out, false, err
	}
	if acceptedFrom != nil {
		s.addChatContact(ctx, me, *acceptedFrom)
	}
	if owed != nil {
		s.pushPeople(ctx, *owed)
	}
	if d.file != nil {
		copies := make([]pdfCopy, 0, len(drawFor))
		for _, who := range drawFor {
			copies = append(copies, s.peoplePDFCopy(who, out.ID, nil))
		}
		s.drawLater(ctx, d.file.Type, d.file.Data, copies...)
	}
	return out, true, nil
}

func attachKind(a map[string]any) *string {
	if a == nil {
		return nil
	}
	k, _ := a["kind"].(string)
	return &k
}

// insertPeopleMessage writes one copy of a message (and its file), sealed
// under that copy's owner's key; screen is the checks' mark on the
// other's copy ("", warn, or transfer).
func insertPeopleMessage(ctx context.Context, tx pgx.Tx, key []byte, owner, chat, conv, id uuid.UUID, mine bool, d peopleDraft, content peopleSealed,
	attach map[string]any, cardType *string, screen string) error {
	sealed, err := sealFor(key, owner, messageRow(id), content)
	if err != nil {
		return err
	}
	var clientID *string
	if mine && d.clientID != "" {
		clientID = &d.clientID
	}
	var mark *string
	if screen != "" {
		mark = &screen
	}
	if _, err := tx.Exec(ctx, `INSERT INTO people_messages (person_id, id, chat_id, conversation_id, mine, kind, sealed, attachment, card_type, reply_to, forwards, client_id, screen)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		owner, id, chat, conv, mine, d.kind, sealed, attach, cardType, d.replyTo, d.forwards, clientID, mark); err != nil {
		return err
	}
	if err := indexPeopleMessageTx(ctx, tx, key, owner, conv, id, content); err != nil {
		return err
	}
	if d.preview != nil && d.preview.img != nil {
		// The link's picture, sealed as a file is.
		img := d.preview.img
		data, err := peoplechats.Seal(key, owner, linkImageRow(id), img.Big)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO people_files (person_id, message_id, conversation_id, kind, type, size, sealed) VALUES ($1, $2, $3, 'link_image', $4, $5, $6)`,
			owner, id, conv, img.Type, len(img.Big), data); err != nil {
			return err
		}
	}
	if d.file == nil {
		return nil
	}
	data, err := peoplechats.Seal(key, owner, fileRow(id), d.file.Data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO people_files (person_id, message_id, conversation_id, type, size, sealed) VALUES ($1, $2, $3, $4, $5, $6)`,
		owner, id, conv, d.file.Type, len(d.file.Data), data)
	return err
}

// peopleRepeatTx is the person's message under clientID, if they sent one,
// shown as their own messages are (read only while read ticks go).
func (s *Server) peopleRepeatTx(ctx context.Context, tx pgx.Tx, me uuid.UUID, c peopleCopy, clientID string) (*peopleMessage, error) {
	if clientID == "" {
		return nil, nil
	}
	row, err := scanPeopleRow(tx.QueryRow(ctx, `SELECT `+peopleMessageCols+` FROM people_messages WHERE person_id = $1 AND chat_id = $2 AND client_id = $3`, me, c.ID, clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := s.keys().KeyTx(ctx, tx, me, false)
	if err != nil {
		return nil, err
	}
	ticks, err := s.readTicksTx(ctx, tx, me, c.Peer)
	if err != nil {
		return nil, err
	}
	m, _ := row.view(key, me)
	m = hideRead(m, ticks)
	return &m, nil
}

// pushPeople tells the other person of a message on their own devices
// (kind person, or request for a stranger's), as their pushes on You and
// each device's preview setting say; nothing when they turned it off.
func (s *Server) pushPeople(ctx context.Context, p peoplePush) {
	if !s.Push.Enabled() {
		return
	}
	kind, link := push.KindPerson, "/you/people/"+p.chat.String()
	if p.request {
		kind, link = push.KindRequest, "/you/people/requests/"+p.chat.String()
	}
	err := db.InPersonTx(ctx, s.Store.Pool, p.to, func(tx pgx.Tx) error {
		n, err := notifyTx(ctx, tx, p.to)
		if err != nil {
			return err
		}
		if (p.request && !n.Requests) || (!p.request && !n.People) {
			return nil
		}
		live, err := push.LiveYouTx(ctx, tx, p.to, s.Push.WebPushOn(), s.Push.FCMOn())
		if err != nil || !live {
			return err
		}
		title, body := p.title, p.words
		if p.request {
			body = "Message request: " + body
		}
		if len([]rune(body)) > 120 {
			body = string([]rune(body)[:120]) + "…"
		}
		return push.EnqueueTx(ctx, tx, uuid.Nil, []uuid.UUID{p.to}, push.Queued{Kind: kind, Title: title, Body: body, Link: link, Tag: "person:" + p.chat.String()})
	})
	if err != nil {
		s.Logger.Warn("people chats: push", "err", err)
	}
}

// sendPeopleMessage is POST /v1/me/people/{id}/messages {text?, client_id?,
// reply_to?, location?, card?}: a text, a place or a card.
func (s *Server) sendPeopleMessage(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	// The share a saved thing's card makes, recorded once it is sent.
	var share *savedShareRecord
	var in struct {
		Text     string          `json:"text"`
		ClientID string          `json:"client_id"`
		ReplyTo  *uuid.UUID      `json:"reply_to"`
		Location *peopleLocation `json:"location"`
		Card     *cardRequest    `json:"card"`
		// Preview: the card the composer showed ({id}), or false for none.
		Preview previewChoice `json:"preview"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.ClientID) > 80 {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters")
		return
	}
	d := peopleDraft{kind: "text", body: strings.TrimSpace(in.Text), replyTo: in.ReplyTo, clientID: in.ClientID}
	if len([]rune(d.body)) > 4000 {
		writeErr(w, http.StatusBadRequest, "a message is up to 4,000 characters")
		return
	}
	switch {
	case in.Location != nil && in.Card == nil:
		l := in.Location
		l.Label = strings.TrimSpace(l.Label)
		if math.IsNaN(l.Lat) || math.IsNaN(l.Lng) || l.Lat < -90 || l.Lat > 90 || l.Lng < -180 || l.Lng > 180 || len([]rune(l.Label)) > 200 {
			writeErr(w, http.StatusBadRequest, "a location is lat (-90 to 90), lng (-180 to 180) and a label of up to 200 characters")
			return
		}
		d.kind, d.location, d.body = "location", l, ""
	case in.Card != nil && in.Location == nil:
		var card *peopleCard
		var err error
		if in.Card.Type == "saved" {
			card, share, err = s.savedPeopleCard(r, person, in.Card.Saved, s.peopleShareTo(r.Context(), person, chi.URLParam(r, "id")))
		} else {
			card, err = s.makeCard(r.Context(), person, *in.Card)
		}
		if shareConfirmFail(w, err) {
			return
		}
		if err != nil {
			s.personFail(w, r, err)
			return
		}
		d.kind, d.card = "card", card
	case in.Card != nil:
		writeErr(w, http.StatusBadRequest, "send a location or a card, not both")
		return
	default:
		if d.body == "" {
			writeErr(w, http.StatusBadRequest, "the message is required (up to 4,000 characters)")
			return
		}
		// The first link's preview, made by the server as it seals the words
		// (and nowhere else), only for a chat the person can write in.
		if previewURL(d.body, s.previewAnyPort) != "" && s.peopleMaySend(r.Context(), person, chi.URLParam(r, "id")) {
			d.preview = s.previewForSend(r.Context(), principalFrom(r.Context()), "people:"+chi.URLParam(r, "id"), "p:"+person.String(), person.String(), d.body, in.Preview)
		}
	}
	m, fresh, err := s.peopleSend(r.Context(), person, chi.URLParam(r, "id"), d)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !fresh {
		code = http.StatusOK
	} else {
		s.recordShare(r, share)
	}
	writeJSON(w, code, m)
}

// peopleShareTo is who a people chat's share goes to, for the question and
// the record: the other person's name, or "them".
func (s *Server) peopleShareTo(ctx context.Context, person uuid.UUID, chat string) string {
	id, err := uuid.Parse(chat)
	if err != nil {
		return ""
	}
	var name string
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT u.name FROM people_chats c JOIN users u ON u.id = c.peer_id WHERE c.person_id = $1 AND c.id = $2`, person, id).Scan(&name)
	})
	return name
}

// sendPeopleFile is POST /v1/me/people/{id}/files (multipart: file; text,
// a caption; client_id; reply_to): a photo, voice note or file, scanned
// for malware and checked (engine/media) before either copy is kept.
func (s *Server) sendPeopleFile(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	f, err := s.readChatFile(w, r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	d := peopleDraft{kind: "media", body: strings.TrimSpace(r.FormValue("text")), file: f, clientID: r.FormValue("client_id")}
	if len([]rune(d.body)) > 4000 || len(d.clientID) > 80 {
		writeErr(w, http.StatusBadRequest, "a caption is up to 4,000 characters, client_id up to 80")
		return
	}
	if rt := r.FormValue("reply_to"); rt != "" {
		id, err := uuid.Parse(rt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "reply_to is a message id")
			return
		}
		d.replyTo = &id
	}
	m, fresh, err := s.peopleSend(r.Context(), person, chi.URLParam(r, "id"), d)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !fresh {
		code = http.StatusOK
	}
	writeJSON(w, code, m)
}

// peopleFile is GET /v1/me/people/{id}/files/{message}: the person's own
// copy of a file, opened.
func (s *Server) peopleFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	var ctype, name, kind string
	var chat, msg uuid.UUID
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if msg, err = uuid.Parse(chi.URLParam(r, "message")); err != nil {
			return errDoorNone
		}
		if chat = c.ID; wantsPreview(r) {
			return nil
		}
		var sealedFile, sealedMsg []byte
		if err := tx.QueryRow(ctx, `SELECT f.sealed, f.type, m.sealed, m.attachment->>'kind' FROM people_files f
			JOIN people_messages m ON m.person_id = f.person_id AND m.id = f.message_id
			WHERE f.person_id = $1 AND m.chat_id = $2 AND f.message_id = $3 AND f.kind = 'file' AND m.deleted_at IS NULL`, person, c.ID, msg).Scan(&sealedFile, &ctype, &sealedMsg, &kind); err != nil {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		if data, err = peoplechats.Open(key, person, fileRow(msg), sealedFile); err != nil {
			return err
		}
		v, err := openSealed(key, person, msg, sealedMsg)
		name = v.Name
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if wantsPreview(r) {
		s.servePDFPreview(w, r, s.peoplePDFCopy(person, msg, &chat), s.personFail)
		return
	}
	serveChatFileFor(w, r, data, ctype, name, kind)
}

// peopleMessages is GET /v1/me/people/{id}/messages?before=&starred=1: a
// page of the person's copy, the latest last: 50 before ?before, or the
// latest 50; ?starred=1 their starred messages only.
func (s *Server) peopleMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []peopleMessage{}
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		q := `SELECT ` + peopleMessageCols + ` FROM people_messages WHERE person_id = $1 AND chat_id = $2`
		args := []any{person, c.ID}
		if r.URL.Query().Get("starred") == "1" {
			q += ` AND starred_at IS NOT NULL`
		}
		if b, err := uuid.Parse(r.URL.Query().Get("before")); err == nil {
			q += ` AND (created_at, id) < (SELECT created_at, id FROM people_messages WHERE person_id = $1 AND id = $3)`
			args = append(args, b)
		}
		rows, err := tx.Query(ctx, q+` ORDER BY created_at DESC, id DESC LIMIT 51`, args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (peopleRow, error) { return scanPeopleRow(r) })
		if err != nil {
			return err
		}
		if len(list) > 50 {
			more, list = true, list[:50]
		}
		var key []byte
		ticks := true
		if len(list) > 0 {
			if key, err = s.keys().KeyTx(ctx, tx, person, false); err != nil {
				return err
			}
			if ticks, err = s.readTicksTx(ctx, tx, person, c.Peer); err != nil {
				return err
			}
		}
		for i := len(list) - 1; i >= 0; i-- {
			m, _ := list[i].view(key, person)
			out = append(out, hideRead(m, ticks))
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "more": more})
}

// readPeopleChat is POST /v1/me/people/{id}/read: the person read the
// other's messages. Their copy shows them read once the chat is accepted
// (never while it is a request, nor while either blocked the other).
func (s *Server) readPeopleChat(w http.ResponseWriter, r *http.Request) {
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
		rows, err := tx.Query(ctx, `UPDATE people_messages SET read_at = now() WHERE person_id = $1 AND chat_id = $2 AND NOT mine AND read_at IS NULL RETURNING id`,
			person, c.ID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(ids) == 0 || c.RequestAt != nil || c.Blocked || c.Peer == nil {
			return err
		}
		// The other away (deleted, suspended) or the person suspended: read
		// on their own copy only.
		peer, err := peopleReachTx(ctx, tx, c.ID)
		if reachQuiet(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if theyBlocked, err := theyBlockedTx(ctx, tx, peer, person); err != nil || theyBlocked {
			return err
		}
		// Read ticks off on either side: the other is not told.
		if ticks, err := s.readTicksTx(ctx, tx, person, &peer); err != nil || !ticks {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE people_messages SET read_at = COALESCE(read_at, now()) WHERE person_id = $1 AND conversation_id = $2 AND id = ANY ($3) AND mine`,
			peer, c.Conversation, ids)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownMessageTx is one message of the person's copy of chat.
func ownMessageTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, c peopleCopy, idText string) (peopleRow, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return peopleRow{}, errDoorNone
	}
	row, err := scanPeopleRow(tx.QueryRow(ctx, `SELECT `+peopleMessageCols+` FROM people_messages WHERE person_id = $1 AND chat_id = $2 AND id = $3`, person, c.ID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errDoorNone
	}
	return row, err
}

// reachIfDeliveredTx puts the other's copy in reach when the message
// reached it and they take changes now (not deleted, not suspended): ok
// says so. Otherwise only the person's own copy changes.
func reachIfDeliveredTx(ctx context.Context, tx pgx.Tx, c peopleCopy, m peopleRow) (uuid.UUID, bool, error) {
	if c.Peer == nil || (m.Mine && m.delivered == nil) {
		return uuid.Nil, false, nil
	}
	peer, err := peopleReachTx(ctx, tx, c.ID)
	if reachQuiet(err) {
		return uuid.Nil, false, nil
	}
	return peer, err == nil, err
}

// markPeopleMessage is PUT /v1/me/people/{id}/messages/{message}
// {starred?, reaction?}: a star is the person's own; a reaction ("" takes
// it away) shows on both copies.
func (s *Server) markPeopleMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Starred  *bool   `json:"starred"`
		Reaction *string `json:"reaction"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Starred == nil && in.Reaction == nil {
		writeErr(w, http.StatusBadRequest, "starred or reaction is required")
		return
	}
	if in.Reaction != nil && (len(*in.Reaction) > 16 || strings.ContainsAny(*in.Reaction, " \t\n")) {
		writeErr(w, http.StatusBadRequest, "a reaction is one emoji")
		return
	}
	var out peopleMessage
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), in.Reaction != nil)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.Deleted && in.Reaction != nil {
			return customerSendErr{http.StatusConflict, "This message was deleted."}
		}
		// A reaction shows on the other's copy: not from a suspended person
		// (a star is their own).
		if in.Reaction != nil {
			if err := notSuspendedTx(ctx, tx, person); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE people_messages SET starred_at = CASE WHEN $3::boolean IS NULL THEN starred_at WHEN $3 THEN COALESCE(starred_at, now()) END,
			my_reaction = CASE WHEN $4::text IS NULL THEN my_reaction ELSE NULLIF($4, '') END WHERE person_id = $1 AND id = $2`, person, m.ID, in.Starred, in.Reaction); err != nil {
			return err
		}
		if in.Reaction != nil && !c.Blocked && c.RequestAt == nil {
			peer, ok, err := reachIfDeliveredTx(ctx, tx, c, m)
			if err != nil {
				return err
			}
			var theyBlocked bool
			if ok {
				if theyBlocked, err = theyBlockedTx(ctx, tx, peer, person); err != nil {
					return err
				}
			}
			if ok && !theyBlocked {
				if _, err := tx.Exec(ctx, `UPDATE people_messages SET their_reaction = NULLIF($4, '') WHERE person_id = $1 AND conversation_id = $2 AND id = $3`,
					peer, c.Conversation, m.ID, *in.Reaction); err != nil {
					return err
				}
			}
		}
		row, err := ownMessageTx(ctx, tx, person, c, m.ID.String())
		if err != nil {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		ticks, err := s.readTicksTx(ctx, tx, person, c.Peer)
		if err != nil {
			return err
		}
		out, _ = row.view(key, person)
		out = hideRead(out, ticks)
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// forgetMessageTx empties one copy of a message deleted for everyone. The
// message is emptied first: that waits for a search backfill holding it
// (searchReady), so its tokens, written by then, are removed after.
func forgetMessageTx(ctx context.Context, tx pgx.Tx, owner, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE people_messages SET deleted_at = now(), sealed = NULL, attachment = NULL, my_reaction = NULL, their_reaction = NULL,
		starred_at = NULL WHERE person_id = $1 AND id = $2`, owner, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM people_files WHERE person_id = $1 AND message_id = $2`, owner, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM people_search WHERE person_id = $1 AND message_id = $2`, owner, id)
	return err
}

// deletePeopleMessage is DELETE /v1/me/people/{id}/messages/{message}
// ?for=me|everyone: for me deletes the person's own copy of it; for
// everyone (their own message, within 24 hours) also removes it from the
// other's copy on the server and their devices. A screenshot or a forward
// made before cannot be undone.
func (s *Server) deletePeopleMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	everyone := r.URL.Query().Get("for") == "everyone"
	if f := r.URL.Query().Get("for"); f != "" && f != "me" && f != "everyone" {
		writeErr(w, http.StatusBadRequest, "for is me or everyone")
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), everyone)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if !everyone {
			if _, err := tx.Exec(ctx, `DELETE FROM people_messages WHERE person_id = $1 AND id = $2`, person, m.ID); err != nil {
				return err
			}
			return goneTx(ctx, tx, person, c.ID, m.ID)
		}
		if !m.Mine {
			return customerSendErr{http.StatusForbidden, "Only your own messages can be deleted for everyone."}
		}
		if m.Deleted {
			return nil
		}
		if time.Since(m.CreatedAt) > peopleDeleteForEveryone {
			return customerSendErr{http.StatusConflict, "Messages can be deleted for everyone only within 24 hours of sending them."}
		}
		if err := forgetMessageTx(ctx, tx, person, m.ID); err != nil {
			return err
		}
		// A suspension (theirs or the person's) stops messaging, not
		// forgetting: the message leaves a suspended account's copy too. A
		// deleted account has no copy.
		if c.Peer == nil || m.delivered == nil {
			return nil
		}
		peer, ok, err := peopleForgetReachTx(ctx, tx, c.ID)
		if err != nil || !ok {
			return err
		}
		// Nothing crosses their block of the person, as with reactions.
		if theyBlocked, err := theyBlockedTx(ctx, tx, peer, person); err != nil || theyBlocked {
			return err
		}
		var theirs bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people_messages WHERE person_id = $1 AND conversation_id = $2 AND id = $3)`, peer, c.Conversation, m.ID).Scan(&theirs); err != nil || !theirs {
			return err
		}
		return forgetMessageTx(ctx, tx, peer, m.ID)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forwardPeopleMessage is POST /v1/me/people/forward {chat_id, message_id,
// to: [chat ids]}: one message of the person's chat to other people chats,
// marked forwarded; at most five chats at once, and one at a time for a
// message forwarded many times. A split card is not forwarded.
func (s *Server) forwardPeopleMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		ChatID    uuid.UUID   `json:"chat_id"`
		MessageID uuid.UUID   `json:"message_id"`
		To        []uuid.UUID `json:"to"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.To) == 0 {
		writeErr(w, http.StatusBadRequest, "to names the chats to forward it to")
		return
	}
	seen := map[uuid.UUID]bool{}
	for _, t := range in.To {
		if seen[t] {
			writeErr(w, http.StatusBadRequest, "a chat is named twice")
			return
		}
		seen[t] = true
	}
	d, err := s.peopleDraftOf(ctx, person, in.ChatID, in.MessageID)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	// The cap follows the mark the person sees: a message shown
	// forwarded_many (forwarded five times or more on its way here) goes
	// to one chat at a time.
	many := d.forwards-1 >= peopleForwardedMany
	if limit := map[bool]int{true: 1, false: peopleForwardAtOnce}[many]; len(in.To) > limit {
		msg := "A message can be forwarded to at most 5 chats at once."
		if many {
			msg = "This message was forwarded many times: it can be forwarded to one chat at a time."
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": msg, "forwarded_many": many})
		return
	}
	type sent struct {
		ChatID  uuid.UUID     `json:"chat_id"`
		Message peopleMessage `json:"message"`
	}
	type failed struct {
		ChatID uuid.UUID `json:"chat_id"`
		Error  string    `json:"error"`
	}
	out := struct {
		Sent   []sent   `json:"sent"`
		Failed []failed `json:"failed"`
	}{Sent: []sent{}, Failed: []failed{}}
	for _, t := range in.To {
		m, _, err := s.peopleSend(ctx, person, t.String(), d)
		if err != nil {
			var se customerSendErr
			switch {
			case errors.As(err, &se):
				out.Failed = append(out.Failed, failed{t, se.msg})
			case errors.Is(err, errDoorNone):
				out.Failed = append(out.Failed, failed{t, "not found"})
			default:
				s.personFail(w, r, err)
				return
			}
			continue
		}
		out.Sent = append(out.Sent, sent{t, m})
	}
	writeJSON(w, http.StatusOK, out)
}

// peopleDraftOf is one message of the person's people chat as a draft to
// forward: marked forwarded once more, its file opened to be sealed
// again; a share card or a deleted message is not forwarded.
func (s *Server) peopleDraftOf(ctx context.Context, person, chat, msg uuid.UUID) (peopleDraft, error) {
	var d peopleDraft
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chat.String(), false)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, msg.String())
		if err != nil {
			return err
		}
		if m.Deleted {
			return customerSendErr{http.StatusConflict, "This message was deleted."}
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		_, v := m.view(key, person)
		d = peopleDraft{kind: m.Kind, body: v.Body, card: v.Card, location: v.Location, forwards: m.forwards + 1}
		if v.Card != nil && (v.Card.Type == "split" || v.Card.Type == "basket") {
			return customerSendErr{http.StatusConflict, "A share card or a shared basket is between the people in it: it is not forwarded."}
		}
		// Someone's saved details, shared with you: theirs to share again.
		if v.Card != nil && v.Card.Type == "saved" {
			return customerSendErr{http.StatusConflict, "Details someone shared with you are not forwarded: save them, or ask them to share them."}
		}
		if m.attach != nil {
			var sealed []byte
			var ctype string
			if err := tx.QueryRow(ctx, `SELECT sealed, type FROM people_files WHERE person_id = $1 AND message_id = $2 AND kind = 'file'`, person, m.ID).Scan(&sealed, &ctype); err != nil {
				return err
			}
			data, err := peoplechats.Open(key, person, fileRow(m.ID), sealed)
			if err != nil {
				return err
			}
			d.file = &sentFile{Type: ctype, Name: v.Name, Data: data, Facts: forwardedFacts(m.attach)}
		}
		return nil
	})
	return d, err
}

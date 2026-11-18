package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/push"
)

// A group's messages (people-chatting §9, §13; 5b A5 to A8): one copy,
// sealed under the group's key, seen by each member within their own time
// in the group; each member's own marks beside it (read and delivered up
// to, starred, deleted for them, their reaction).

// groupMsgRow is a group message as kept.
type groupMsgRow struct {
	id                uuid.UUID
	sender            *uuid.UUID
	kind              string
	sealed            []byte
	evRaw             []byte
	ev                *groupEvent
	attach            map[string]any
	cardType          *string
	replyTo           *uuid.UUID
	forwards          int
	mentions          []uuid.UUID
	deletedAt, paidAt *time.Time
	paidBy            *uuid.UUID
	createdAt         time.Time
	starredAt         *time.Time
	myReaction        *string
}

// groupMsgColsAs are a group message's columns from alias a, each named
// a_..., NULL-safe for a left join.
func groupMsgColsAs(a string) string {
	return a + ".id AS " + a + "_id, " + a + ".sender_id AS " + a + "_sender, COALESCE(" + a + ".kind, '') AS " + a + "_kind, " +
		a + ".sealed AS " + a + "_sealed, " + a + ".event AS " + a + "_event, " + a + ".attachment AS " + a + "_attach, " +
		a + ".card_type AS " + a + "_card, " + a + ".reply_to AS " + a + "_reply, COALESCE(" + a + ".forwards, 0) AS " + a + "_forwards, " +
		a + ".mentions AS " + a + "_mentions, " + a + ".deleted_at AS " + a + "_deleted, " + a + ".paid_at AS " + a + "_paid, " +
		a + ".paid_by AS " + a + "_paid_by, COALESCE(" + a + ".created_at, 'epoch') AS " + a + "_at"
}

func (m *groupMsgRow) dest() []any {
	return []any{&m.id, &m.sender, &m.kind, &m.sealed, &m.evRaw, &m.attach, &m.cardType, &m.replyTo, &m.forwards, &m.mentions,
		&m.deletedAt, &m.paidAt, &m.paidBy, &m.createdAt}
}

func (m *groupMsgRow) decode() {
	if m.evRaw != nil {
		var ev groupEvent
		if json.Unmarshal(m.evRaw, &ev) == nil {
			m.ev = &ev
		}
	}
}

// groupMsgSelect reads a group's messages for the person ($1) in a group
// ($2), with their own star and reaction; never one deleted for them.
// Row-level security keeps it to their time in the group.
const groupMsgSelect = `SELECT ` + "x.id, x.sender_id, x.kind, x.sealed, x.event, x.attachment, x.card_type, x.reply_to, x.forwards, x.mentions, x.deleted_at, x.paid_at, x.paid_by, x.created_at" + `,
	k.starred_at, (SELECT r.reaction FROM group_reactions r WHERE r.group_id = x.group_id AND r.message_id = x.id AND r.person_id = $1)
	FROM group_messages x LEFT JOIN group_marks k ON k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1
	WHERE x.group_id = $2 AND k.hidden_at IS NULL`

func scanGroupMsg(r pgx.Row, extra ...any) (groupMsgRow, error) {
	var m groupMsgRow
	err := r.Scan(append(append(m.dest(), &m.starredAt, &m.myReaction), extra...)...)
	m.decode()
	return m, err
}

// openGroupSealed opens a group message's sealed value.
func openGroupSealed(key []byte, group, id uuid.UUID, sealed []byte) (peopleSealed, error) {
	var v peopleSealed
	if sealed == nil || key == nil {
		return v, nil
	}
	raw, err := peoplechats.Open(key, group, groupMsgKey(id), sealed)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v)
}

// groupMessage is one message of a group as a member sees it.
type groupMessage struct {
	ID      uuid.UUID `json:"id"`
	GroupID uuid.UUID `json:"group_id"`
	Mine    bool      `json:"mine"`
	// SenderID and Sender: who wrote it, named as the person sees them
	// ("Deleted account" once their account was deleted, §17 Q17).
	SenderID *uuid.UUID `json:"sender_id"`
	Sender   string     `json:"sender"`
	// Kind: text, media, card, location, or event (someone joined, left,
	// was added or removed, made an admin; the info changed): its words in
	// body.
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Event      *groupEventOut  `json:"event,omitempty"`
	Card       *peopleCard     `json:"card,omitempty"`
	Location   *peopleLocation `json:"location,omitempty"`
	Attachment *chatAttachment `json:"attachment,omitempty"`
	ReplyTo    *uuid.UUID      `json:"reply_to,omitempty"`
	Forwarded  bool            `json:"forwarded,omitempty"`
	// ForwardedMany: forwarded five or more times on its way; forwarded on
	// to one chat at a time.
	ForwardedMany bool `json:"forwarded_many,omitempty"`
	// Mentions: the members it mentions; MentionsMe: the person is one.
	Mentions   []uuid.UUID `json:"mentions,omitempty"`
	MentionsMe bool        `json:"mentions_me,omitempty"`
	// Status of the person's own message: sent; delivered (every member
	// now in the group has it); read (every one of them with read ticks
	// on, both ways, has read it: A6).
	Status     string          `json:"status,omitempty"`
	Starred    bool            `json:"starred,omitempty"`
	MyReaction string          `json:"my_reaction,omitempty"`
	Reactions  []reactionCount `json:"reactions,omitempty"`
	Deleted    bool            `json:"deleted,omitempty"`
	// WarnTransfer: it asks for a transfer to an account (a share card
	// from someone the person has not added, or words that ask): warn
	// before paying.
	WarnTransfer bool      `json:"warn_transfer,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// groupEventOut is an event message's what and who.
type groupEventOut struct {
	Type     string     `json:"type"`
	ActorID  uuid.UUID  `json:"actor_id"`
	TargetID *uuid.UUID `json:"target_id,omitempty"`
}

type reactionCount struct {
	Reaction string `json:"reaction"`
	Count    int    `json:"count"`
}

// groupMessagesTx makes what the person sees of these messages of a
// group: opened under the group's key (key), named, with reactions
// counted and their own messages' ticks.
func (s *Server) groupMessagesTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID, key []byte, rows []groupMsgRow) ([]groupMessage, error) {
	out := make([]groupMessage, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	var ids, mine, people []uuid.UUID
	for _, m := range rows {
		ids = append(ids, m.id)
		if m.sender != nil {
			people = append(people, *m.sender)
			if *m.sender == person && m.kind != "event" && m.deletedAt == nil {
				mine = append(mine, m.id)
			}
		}
		if m.ev != nil {
			people = append(people, m.ev.Actor)
			if m.ev.Target != nil {
				people = append(people, *m.ev.Target)
			}
		}
	}
	names, cards, err := s.groupNamesTx(ctx, tx, person, people)
	if err != nil {
		return nil, err
	}
	reactions := map[uuid.UUID][]reactionCount{}
	rrows, err := tx.Query(ctx, `SELECT message_id, reaction, count(*)::int FROM group_reactions WHERE group_id = $1 AND message_id = ANY ($2)
		GROUP BY message_id, reaction ORDER BY message_id, count(*) DESC, reaction`, group, ids)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var id uuid.UUID
		var rc reactionCount
		if err := rrows.Scan(&id, &rc.Reaction, &rc.Count); err != nil {
			rrows.Close()
			return nil, err
		}
		reactions[id] = append(reactions[id], rc)
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, err
	}
	status, err := s.groupStatusesTx(ctx, tx, person, group, mine)
	if err != nil {
		return nil, err
	}
	for _, m := range rows {
		v := groupMessage{ID: m.id, GroupID: group, SenderID: m.sender, Kind: m.kind, ReplyTo: m.replyTo, Forwarded: m.forwards > 0,
			ForwardedMany: m.forwards >= peopleForwardedMany, Mentions: m.mentions, Starred: m.starredAt != nil, CreatedAt: m.createdAt}
		v.Mine = m.sender != nil && *m.sender == person
		if m.sender != nil {
			v.Sender = names[*m.sender]
		} else {
			v.Sender = peopleGoneName
		}
		v.MentionsMe = slices.Contains(m.mentions, person)
		if m.myReaction != nil {
			v.MyReaction = *m.myReaction
		}
		if m.ev != nil {
			v.Event = &groupEventOut{Type: m.ev.Type, ActorID: m.ev.Actor, TargetID: m.ev.Target}
			v.Body = eventWords(*m.ev, names)
			out = append(out, v)
			continue
		}
		if v.Mine {
			v.Status = status[m.id]
		}
		if m.deletedAt != nil {
			v.Deleted, v.Body, v.Mentions, v.MentionsMe, v.MyReaction = true, "This message was deleted", nil, false, ""
			out = append(out, v)
			continue
		}
		v.Reactions = reactions[m.id]
		c, _ := openGroupSealed(key, group, m.id, m.sealed)
		v.Body, v.Card, v.Location = c.Body, c.Card, c.Location
		if m.attach != nil {
			a := &chatAttachment{Name: c.Name}
			a.Kind, _ = m.attach["kind"].(string)
			a.Type, _ = m.attach["type"].(string)
			if n, ok := m.attach["size"].(float64); ok {
				a.Size = int64(n)
			}
			v.Attachment = a
		}
		if v.Card != nil && v.Card.Type == "split" {
			v.Card.Paid, v.Card.PaidAt = m.paidAt != nil, m.paidAt
			if m.paidAt != nil && m.paidBy != nil {
				by := *m.paidBy == person
				v.Card.PaidByMe = &by
			}
		}
		if !v.Mine {
			known := false
			if m.sender != nil {
				known = cards[*m.sender].InContacts
			}
			v.WarnTransfer = (v.Kind == "text" && warnTransfer(v.Body)) || (v.Card != nil && v.Card.Type == "split" && v.Card.PayTo != nil && !known)
		}
		out = append(out, v)
	}
	return out, nil
}

// groupTicksTx is which of the group's members read ticks go between
// the person and (both have them on, §5.4): those whose reads the person
// is shown (A6).
func (s *Server) groupTicksTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND person_id <> $2`, group, person)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	out := []uuid.UUID{}
	for start := 0; start < len(ids); start += 500 {
		cards, err := s.people().CardsTx(ctx, tx, person, ids[start:min(start+500, len(ids))])
		if err != nil {
			return nil, err
		}
		for _, c := range cards {
			if c.ReadTicks && !c.Deleted {
				out = append(out, c.ID)
			}
		}
	}
	return out, nil
}

// groupStatusesTx is the ticks of the person's own messages (A6): read
// when every member now in the group (who was when it was sent) with read
// ticks on both ways has read it; else delivered when every one of them
// has it; else sent.
func (s *Server) groupStatusesTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ticks, err := s.groupTicksTx(ctx, tx, person, group)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT x.id, count(m.person_id), count(m.person_id) FILTER (WHERE m.delivered_upto >= x.created_at),
		count(m.person_id) FILTER (WHERE m.person_id = ANY ($3)), count(m.person_id) FILTER (WHERE m.person_id = ANY ($3) AND m.read_upto >= x.created_at)
		FROM group_messages x LEFT JOIN group_members m ON m.group_id = x.group_id AND m.person_id <> x.sender_id AND m.left_at IS NULL AND m.visible @> x.created_at
		WHERE x.group_id = $1 AND x.id = ANY ($2) GROUP BY x.id`, group, ids, ticks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var eligible, delivered, ticked, read int
		if err := rows.Scan(&id, &eligible, &delivered, &ticked, &read); err != nil {
			return nil, err
		}
		switch {
		case ticked > 0 && read == ticked:
			out[id] = "read"
		case eligible > 0 && delivered == eligible:
			out[id] = "delivered"
		default:
			out[id] = "sent"
		}
	}
	return out, rows.Err()
}

// groupMessageTx is one message of the group the person sees (not one
// deleted for them).
func groupMessageTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID, idText string) (groupMsgRow, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return groupMsgRow{}, errDoorNone
	}
	m, err := scanGroupMsg(tx.QueryRow(ctx, groupMsgSelect+` AND x.id = $3`, person, group, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errDoorNone
	}
	return m, err
}

// groupOneMessageTx is one message as the person sees it.
func (s *Server) groupOneMessageTx(ctx context.Context, tx pgx.Tx, person, group, id uuid.UUID) (groupMessage, error) {
	m, err := groupMessageTx(ctx, tx, person, group, id.String())
	if err != nil {
		return groupMessage{}, err
	}
	key, err := s.keys().GroupKeyTx(ctx, tx, group)
	if err != nil {
		return groupMessage{}, err
	}
	list, err := s.groupMessagesTx(ctx, tx, person, group, key, []groupMsgRow{m})
	if err != nil {
		return groupMessage{}, err
	}
	return list[0], nil
}

// --- sending ---

// groupPush is the pushes owed for a group message.
type groupPush struct {
	group           uuid.UUID
	to              []uuid.UUID
	mentioned       map[uuid.UUID]bool
	title, from, wd string
}

// groupSend keeps the person's message in the group, sealed under its key,
// in one transaction with the group's row locked; fresh is false for a
// repeat of a client_id. mentions are member ids (A7).
func (s *Server) groupSend(ctx context.Context, me uuid.UUID, groupID string, d peopleDraft, mentions []uuid.UUID) (groupMessage, bool, error) {
	var out groupMessage
	var again *groupMessage
	var owed *groupPush
	err := s.peopleTx(ctx, me, func(tx pgx.Tx) error {
		out, again, owed = groupMessage{}, nil, nil
		g, err := groupTx(ctx, tx, me, groupID, true)
		if err != nil {
			return err
		}
		if d.clientID != "" {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM group_messages WHERE group_id = $1 AND sender_id = $2 AND client_id = $3`, g.ID, me, d.clientID).Scan(&id)
			if err == nil {
				m, err := s.groupOneMessageTx(ctx, tx, me, g.ID, id)
				again = &m
				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		st, err := standingTx(ctx, tx, me)
		if err != nil {
			return err
		}
		if st.Suspended {
			return errPeopleYouSuspended
		}
		if g.AdminsOnlyPost && g.Role != "admin" {
			return errGroupAdminPost
		}
		newWeek, err := personNewWeek(ctx, tx, me)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_messages WHERE group_id = $1 AND sender_id = $2 AND kind <> 'event' AND created_at > now() - interval '1 minute'`,
			g.ID, me).Scan(&n); err != nil {
			return err
		}
		if n >= limitFor(groupMessagesPerMinute, newWeek || st.Limited) {
			return customerSendErr{http.StatusTooManyRequests, "You are sending messages very fast. Wait a moment and try again."}
		}
		if d.replyTo != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_messages WHERE group_id = $1 AND id = $2 AND kind <> 'event')`, g.ID, *d.replyTo).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return customerSendErr{http.StatusBadRequest, "reply_to is not a message of this group"}
			}
		}
		var tagged []uuid.UUID
		if len(mentions) > 0 {
			rows, err := tx.Query(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND left_at IS NULL AND person_id = ANY ($2) AND person_id <> $3 ORDER BY person_id`,
				g.ID, mentions, me)
			if err != nil {
				return err
			}
			if tagged, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
				return err
			}
		}
		if tagged == nil {
			tagged = []uuid.UUID{}
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		content := peopleSealed{Body: d.body, Card: d.card, Location: d.location}
		var attach map[string]any
		if d.file != nil {
			content.Name = d.file.Name
			a := d.file.view()
			attach = map[string]any{"kind": a.Kind, "type": a.Type, "size": a.Size}
		}
		var cardType *string
		if d.card != nil {
			cardType = &d.card.Type
		}
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		sealed, err := peoplechats.Seal(key, g.ID, groupMsgKey(id), raw)
		if err != nil {
			return err
		}
		var clientID *string
		if d.clientID != "" {
			clientID = &d.clientID
		}
		var at time.Time
		if err := tx.QueryRow(ctx, `INSERT INTO group_messages (group_id, id, sender_id, kind, sealed, attachment, card_type, reply_to, forwards, client_id, mentions)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING created_at`,
			g.ID, id, me, d.kind, sealed, attach, cardType, d.replyTo, d.forwards, clientID, tagged).Scan(&at); err != nil {
			return err
		}
		if toks := peoplechats.Tokens(peoplechats.GroupSearchKey(key, g.ID), searchTexts(content)...); len(toks) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO group_search (group_id, message_id, token) SELECT $1, $2, unnest($3::bytea[]) ON CONFLICT DO NOTHING`, g.ID, id, toks); err != nil {
				return err
			}
		}
		if d.file != nil {
			data, err := peoplechats.Seal(key, g.ID, groupFileRow(id), d.file.Data)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO group_files (group_id, message_id, type, size, sealed) VALUES ($1, $2, $3, $4, $5)`,
				g.ID, id, d.file.Type, len(d.file.Data), data); err != nil {
				return err
			}
		}
		if err := bumpGroupTx(ctx, tx, g.ID); err != nil {
			return err
		}
		// The sender has their own message; it ends their typing.
		if _, err := tx.Exec(ctx, `UPDATE group_members SET read_upto = GREATEST(read_upto, $3), delivered_upto = GREATEST(delivered_upto, $3), hidden_at = NULL
			WHERE group_id = $1 AND person_id = $2`, g.ID, me, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_typing WHERE group_id = $1 AND person_id = $2`, g.ID, me); err != nil {
			return err
		}
		// Who is told (§13): members not muting the group, and, while
		// muted, those mentioned with mention pushes on (A7); never someone
		// who blocked the sender, nor someone the sender blocked.
		rows, err := tx.Query(ctx, `SELECT person_id, muted, mentions FROM group_members WHERE group_id = $1 AND left_at IS NULL AND person_id <> $2`, g.ID, me)
		if err != nil {
			return err
		}
		type rcpt struct {
			id              uuid.UUID
			muted, mentions bool
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rcpt, error) {
			var x rcpt
			return x, r.Scan(&x.id, &x.muted, &x.mentions)
		})
		if err != nil {
			return err
		}
		var others []uuid.UUID
		for _, x := range list {
			others = append(others, x.id)
		}
		blocked := map[uuid.UUID]bool{}
		if p := s.people(); p.BlockedSet != nil && len(others) > 0 {
			if blocked, err = p.BlockedSet(ctx, tx, me, others); err != nil {
				return err
			}
		}
		p := &groupPush{group: g.ID, mentioned: map[uuid.UUID]bool{}}
		for _, x := range list {
			tag := slices.Contains(tagged, x.id)
			if blocked[x.id] || (x.muted && !(tag && x.mentions)) {
				continue
			}
			p.to = append(p.to, x.id)
			p.mentioned[x.id] = tag
		}
		if len(p.to) > 0 {
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, me).Scan(&p.from); err != nil {
				return err
			}
			p.title = openGroupInfo(key, g.ID, g.sealed).Name
			p.wd = previewOf(d.kind, attachKind(attach), content)
			owed = p
		}
		out, err = s.groupOneMessageTx(ctx, tx, me, g.ID, id)
		return err
	})
	if again != nil {
		return *again, false, nil
	}
	if err != nil {
		if isUnique(err) && d.clientID != "" {
			var m *groupMessage
			if e := db.InPersonTx(ctx, s.Store.Pool, me, func(tx pgx.Tx) error {
				g, err := groupTx(ctx, tx, me, groupID, false)
				if err != nil {
					return err
				}
				var id uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT id FROM group_messages WHERE group_id = $1 AND sender_id = $2 AND client_id = $3`, g.ID, me, d.clientID).Scan(&id); err != nil {
					return err
				}
				v, err := s.groupOneMessageTx(ctx, tx, me, g.ID, id)
				m = &v
				return err
			}); e == nil && m != nil {
				return *m, false, nil
			}
		}
		return out, false, err
	}
	if owed != nil {
		s.pushGroup(ctx, *owed)
	}
	return out, true, nil
}

// pushGroup tells the members of a message on their own devices (kind
// group), as their pushes on You (groups) and each device's preview
// setting say.
func (s *Server) pushGroup(ctx context.Context, p groupPush) {
	if !s.Push.Enabled() {
		return
	}
	for _, to := range p.to {
		err := db.InPersonTx(ctx, s.Store.Pool, to, func(tx pgx.Tx) error {
			n, err := notifyTx(ctx, tx, to)
			if err != nil || !n.Groups {
				return err
			}
			live, err := push.LiveYouTx(ctx, tx, to, s.Push.WebPushOn(), s.Push.FCMOn())
			if err != nil || !live {
				return err
			}
			body := p.from + ": " + p.wd
			if p.mentioned[to] {
				body = p.from + " mentioned you: " + p.wd
			}
			if len([]rune(body)) > 120 {
				body = string([]rune(body)[:120]) + "…"
			}
			title := p.title
			if title == "" {
				title = "Group"
			}
			return push.EnqueueTx(ctx, tx, uuid.Nil, []uuid.UUID{to}, push.Queued{Kind: push.KindGroup, Title: title, Body: body,
				Link: "/you/groups/" + p.group.String(), Tag: "group:" + p.group.String()})
		})
		if err != nil {
			s.Logger.Warn("groups: push", "err", err)
		}
	}
}

// sendGroupMessage is POST /v1/me/groups/{id}/messages {text?, client_id?,
// reply_to?, location?, card?, mentions?}: a text, a place or a card.
func (s *Server) sendGroupMessage(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Text     string          `json:"text"`
		ClientID string          `json:"client_id"`
		ReplyTo  *uuid.UUID      `json:"reply_to"`
		Location *peopleLocation `json:"location"`
		Card     *cardRequest    `json:"card"`
		Mentions []uuid.UUID     `json:"mentions"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.ClientID) > 80 || len(in.Mentions) > groupMax {
		writeErr(w, http.StatusBadRequest, "client_id is up to 80 characters, mentions up to 256 people")
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
		card, err := s.makeCard(r.Context(), person, *in.Card)
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
	}
	m, fresh, err := s.groupSend(r.Context(), person, chi.URLParam(r, "id"), d, in.Mentions)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[fresh], m)
}

// sendGroupFile is POST /v1/me/groups/{id}/files (multipart: file; text, a
// caption; client_id; reply_to; mentions, ids separated by commas): a
// photo, voice note or file, checked (engine/media) before it is kept.
func (s *Server) sendGroupFile(w http.ResponseWriter, r *http.Request) {
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
	var mentions []uuid.UUID
	for _, p := range strings.Split(r.FormValue("mentions"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil || len(mentions) >= groupMax {
			writeErr(w, http.StatusBadRequest, "mentions are member ids separated by commas")
			return
		}
		mentions = append(mentions, id)
	}
	m, fresh, err := s.groupSend(r.Context(), person, chi.URLParam(r, "id"), d, mentions)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[fresh], m)
}

// groupFile is GET /v1/me/groups/{id}/files/{message}: a file of the
// group, opened, for a member who sees its message.
func (s *Server) groupFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var data []byte
	var ctype, name, kind string
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.deletedAt != nil || m.attach == nil {
			return errDoorNone
		}
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT sealed, type FROM group_files WHERE group_id = $1 AND message_id = $2`, g.ID, m.id).Scan(&sealed, &ctype); err != nil {
			return err
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		if data, err = peoplechats.Open(key, g.ID, groupFileRow(m.id), sealed); err != nil {
			return err
		}
		v, err := openGroupSealed(key, g.ID, m.id, m.sealed)
		name = v.Name
		kind, _ = m.attach["kind"].(string)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	serveChatFile(w, data, ctype, name, kind)
}

// groupMessages is GET /v1/me/groups/{id}/messages?before=&starred=1: a
// page of the group as the person sees it (only their time in it, §17
// Q16; A5), the latest last: 50 before ?before, or the latest 50;
// starred=1 their starred ones. Reading it marks it delivered to them.
func (s *Server) groupMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []groupMessage{}
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		q := groupMsgSelect
		args := []any{person, g.ID}
		if r.URL.Query().Get("starred") == "1" {
			q += ` AND k.starred_at IS NOT NULL`
		}
		if b, err := uuid.Parse(r.URL.Query().Get("before")); err == nil {
			q += ` AND (x.created_at, x.id) < (SELECT created_at, id FROM group_messages WHERE group_id = $2 AND id = $3)`
			args = append(args, b)
		}
		rows, err := tx.Query(ctx, q+` ORDER BY x.created_at DESC, x.id DESC LIMIT 51`, args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupMsgRow, error) { return scanGroupMsg(r) })
		if err != nil {
			return err
		}
		if len(list) > 50 {
			more, list = true, list[:50]
		}
		slices.Reverse(list)
		if len(list) == 0 {
			return nil
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		if out, err = s.groupMessagesTx(ctx, tx, person, g.ID, key, list); err != nil {
			return err
		}
		if g.current() {
			return deliveredUptoTx(ctx, tx, person, g.ID, list[len(list)-1].createdAt)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "more": more})
}

// readGroup is POST /v1/me/groups/{id}/read: the person read the group up
// to its latest message they see. Shown to senders as "Read by" only while
// the person's read ticks are on (A6).
func (s *Server) readGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		var latest *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(created_at) FROM group_messages WHERE group_id = $1`, g.ID).Scan(&latest); err != nil || latest == nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE group_members SET read_upto = $3, delivered_upto = GREATEST(delivered_upto, $3)
			WHERE group_id = $1 AND person_id = $2 AND read_upto < $3`, g.ID, person, *latest)
		if err != nil || tag.RowsAffected() == 0 || !g.current() {
			return err
		}
		settings, err := people.SettingsTx(ctx, tx, person)
		if err != nil || !settings.ReadTicks {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_reads WHERE group_id = $1 AND person_id = $2 AND at < now() - $3::interval`, g.ID, person, groupReadsKept.String()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO group_reads (group_id, person_id, kind, upto) VALUES ($1, $2, 'read', $3), ($1, $2, 'delivered', $3) ON CONFLICT DO NOTHING`,
			g.ID, person, *latest)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markGroupMessage is PUT /v1/me/groups/{id}/messages/{message} {starred?,
// reaction?}: a star is the person's own; a reaction ("" takes it away)
// is seen by the members, counted on the message.
func (s *Server) markGroupMessage(w http.ResponseWriter, r *http.Request) {
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
	var out groupMessage
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.kind == "event" {
			return errDoorNone
		}
		if in.Starred != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO group_marks (group_id, message_id, person_id, starred_at) VALUES ($1, $2, $3, CASE WHEN $4 THEN now() END)
				ON CONFLICT (group_id, message_id, person_id) DO UPDATE SET starred_at = CASE WHEN $4 THEN COALESCE(group_marks.starred_at, now()) END`,
				g.ID, m.id, person, *in.Starred); err != nil {
				return err
			}
		}
		if in.Reaction != nil {
			if !g.current() {
				return errGroupLeft
			}
			if m.deletedAt != nil {
				return customerSendErr{http.StatusConflict, "This message was deleted."}
			}
			if err := notSuspendedTx(ctx, tx, person); err != nil {
				return err
			}
			if *in.Reaction == "" {
				_, err = tx.Exec(ctx, `DELETE FROM group_reactions WHERE group_id = $1 AND message_id = $2 AND person_id = $3`, g.ID, m.id, person)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO group_reactions (group_id, message_id, person_id, reaction) VALUES ($1, $2, $3, $4)
					ON CONFLICT (group_id, message_id, person_id) DO UPDATE SET reaction = EXCLUDED.reaction, at = clock_timestamp()`, g.ID, m.id, person, *in.Reaction)
			}
			if err != nil {
				return err
			}
			// The message changed for every member's live connection.
			if _, err := tx.Exec(ctx, `UPDATE group_messages SET changed_at = clock_timestamp() WHERE group_id = $1 AND id = $2`, g.ID, m.id); err != nil {
				return err
			}
		}
		out, err = s.groupOneMessageTx(ctx, tx, person, g.ID, m.id)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteGroupMessage is DELETE /v1/me/groups/{id}/messages/{message}
// ?for=me|everyone: for me hides it from the person's own copy; for
// everyone (the sender's own, within 24 hours, while in the group) leaves
// only that it was, for every member.
func (s *Server) deleteGroupMessage(w http.ResponseWriter, r *http.Request) {
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
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), everyone)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if !everyone {
			if _, err := tx.Exec(ctx, `INSERT INTO group_marks (group_id, message_id, person_id, hidden_at) VALUES ($1, $2, $3, now())
				ON CONFLICT (group_id, message_id, person_id) DO UPDATE SET hidden_at = now(), starred_at = NULL`, g.ID, m.id, person); err != nil {
				return err
			}
			return goneTx(ctx, tx, person, g.ID, m.id)
		}
		if m.sender == nil || *m.sender != person || m.kind == "event" {
			return customerSendErr{http.StatusForbidden, "Only your own messages can be deleted for everyone."}
		}
		if m.deletedAt != nil {
			return nil
		}
		if time.Since(m.createdAt) > peopleDeleteForEveryone {
			return customerSendErr{http.StatusConflict, "Messages can be deleted for everyone only within 24 hours of sending them."}
		}
		if _, err := tx.Exec(ctx, `UPDATE group_messages SET deleted_at = now(), sealed = NULL, attachment = NULL, mentions = '{}', paid_at = NULL, paid_by = NULL
			WHERE group_id = $1 AND id = $2`, g.ID, m.id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_files WHERE group_id = $1 AND message_id = $2`, g.ID, m.id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM group_search WHERE group_id = $1 AND message_id = $2`, g.ID, m.id)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// groupReader is one member in a message's "Read by".
type groupReader struct {
	PersonID uuid.UUID  `json:"person_id"`
	Name     string     `json:"name"`
	At       *time.Time `json:"at"`
}

// groupMessageInfo is GET /v1/me/groups/{id}/messages/{message}/info: for
// the sender of the message (A6), who read it, listing only members with
// read ticks on (both ways), with when; who has it (the rest who do);
// and how many have not had it yet.
func (s *Server) groupMessageInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	readBy, delivered := []groupReader{}, []groupReader{}
	waiting := 0
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.sender == nil || *m.sender != person || m.kind == "event" {
			return customerSendErr{http.StatusForbidden, "Only the sender sees who read a message."}
		}
		ticks, err := s.groupTicksTx(ctx, tx, person, g.ID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT x.person_id, x.read_upto >= $3, x.delivered_upto >= $3,
			(SELECT min(r.at) FROM group_reads r WHERE r.group_id = x.group_id AND r.person_id = x.person_id AND r.kind = 'read' AND r.upto >= $3),
			(SELECT min(r.at) FROM group_reads r WHERE r.group_id = x.group_id AND r.person_id = x.person_id AND r.kind = 'delivered' AND r.upto >= $3)
			FROM group_members x WHERE x.group_id = $1 AND x.person_id <> $2 AND x.visible @> $3::timestamptz
			  AND (x.left_at IS NULL OR x.delivered_upto >= $3) ORDER BY x.person_id`, g.ID, person, m.createdAt)
		if err != nil {
			return err
		}
		type st struct {
			id            uuid.UUID
			read, has     bool
			readAt, hasAt *time.Time
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (st, error) {
			var x st
			return x, r.Scan(&x.id, &x.read, &x.has, &x.readAt, &x.hasAt)
		})
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for _, x := range list {
			ids = append(ids, x.id)
		}
		names, _, err := s.groupNamesTx(ctx, tx, person, ids)
		if err != nil {
			return err
		}
		for _, x := range list {
			switch {
			case x.read && slices.Contains(ticks, x.id):
				readBy = append(readBy, groupReader{x.id, names[x.id], x.readAt})
			case x.has || x.read:
				// Read with read ticks off (either side) shows as had.
				at := x.hasAt
				if !slices.Contains(ticks, x.id) && x.readAt != nil {
					at = nil
				}
				delivered = append(delivered, groupReader{x.id, names[x.id], at})
			default:
				waiting++
			}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"read_by": readBy, "delivered_to": delivered, "waiting": waiting})
}

// groupMessageReactions is GET /v1/me/groups/{id}/messages/{message}/
// reactions: who reacted, and with what.
func (s *Server) groupMessageReactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	type reacted struct {
		PersonID uuid.UUID `json:"person_id"`
		Name     string    `json:"name"`
		Reaction string    `json:"reaction"`
		At       time.Time `json:"at"`
	}
	out := []reacted{}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil || m.deletedAt != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT person_id, reaction, at FROM group_reactions WHERE group_id = $1 AND message_id = $2 ORDER BY at`, g.ID, m.id)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (reacted, error) {
			var x reacted
			return x, r.Scan(&x.PersonID, &x.Reaction, &x.At)
		}); err != nil {
			return err
		}
		var ids []uuid.UUID
		for _, x := range out {
			ids = append(ids, x.PersonID)
		}
		names, _, err := s.groupNamesTx(ctx, tx, person, ids)
		for i := range out {
			out[i].Name = names[out[i].PersonID]
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reactions": out})
}

// openGroupCard is GET /v1/me/groups/{id}/messages/{message}/card: a card
// in the group, as shared and as it is now for the person opening it.
func (s *Server) openGroupCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var card *peopleCard
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.kind != "card" || m.deletedAt != nil {
			return errDoorNone
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		list, err := s.groupMessagesTx(ctx, tx, person, g.ID, key, []groupMsgRow{m})
		if err == nil {
			card = list[0].Card
		}
		return err
	})
	if err == nil && card == nil {
		err = errDoorNone
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	s.answerCard(w, r, person, card)
}

// markGroupSplitPaid is POST /v1/me/groups/{id}/messages/{message}/paid
// {paid}: a member marks a share card in the group paid (or not yet).
// Taskiem moves no money and checks no payment.
func (s *Server) markGroupSplitPaid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Paid *bool `json:"paid"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Paid == nil {
		writeErr(w, http.StatusBadRequest, "paid is required")
		return
	}
	var out groupMessage
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.cardType == nil || *m.cardType != "split" || m.deletedAt != nil {
			return customerSendErr{http.StatusConflict, "Only a share card is marked paid."}
		}
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE group_messages SET paid_at = CASE WHEN $3 THEN COALESCE(paid_at, now()) END, paid_by = CASE WHEN $3 THEN $4::uuid END
			WHERE group_id = $1 AND id = $2`, g.ID, m.id, *in.Paid, person); err != nil {
			return err
		}
		out, err = s.groupOneMessageTx(ctx, tx, person, g.ID, m.id)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// typingGroup is POST /v1/me/groups/{id}/typing ({typing?: bool}): the
// person is typing in the group (or stopped), shown to its members' live
// connections for a few seconds. Always 204 (404 for a group that is not
// theirs); passed on at most every 3 seconds.
func (s *Server) typingGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	in := struct {
		Typing *bool `json:"typing"`
	}{}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	on := in.Typing == nil || *in.Typing
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if !g.current() || (g.AdminsOnlyPost && g.Role != "admin") {
			return nil
		}
		if !on {
			_, err := tx.Exec(ctx, `DELETE FROM group_typing WHERE group_id = $1 AND person_id = $2`, g.ID, person)
			return err
		}
		if l, ok := s.ownLimiter("group-typing:"+person.String()+":"+g.ID.String(), peopleTypingEvery, 1); ok && !l.Allow() {
			return nil
		}
		if st, err := standingTx(ctx, tx, person); err != nil || st.Suspended {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO group_typing (group_id, person_id, until) VALUES ($1, $2, now() + $3::interval)
			ON CONFLICT (group_id, person_id) DO UPDATE SET until = EXCLUDED.until WHERE group_typing.until <= EXCLUDED.until - $4::interval`,
			g.ID, person, peopleTypingFor.String(), peopleTypingEvery.String())
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- search and starred across groups ---

// groupFound is one message of a search of the person's groups, or one of
// their starred messages in groups, with its group.
type groupFound struct {
	GroupID   uuid.UUID    `json:"group_id"`
	Group     groupView    `json:"group"`
	Message   groupMessage `json:"message"`
	Snippet   string       `json:"snippet,omitempty"`
	StarredAt *time.Time   `json:"starred_at,omitempty"`
}

// groupFoundTx turns (group, message) rows into results, keeping those
// keep says (a search checks the words).
func (s *Server) groupFoundTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, rows []groupMsgRow, groupOf map[uuid.UUID]uuid.UUID,
	starred map[uuid.UUID]time.Time, keep func(peopleSealed) (string, bool), limit int) ([]groupFound, bool, error) {
	out := []groupFound{}
	views := map[uuid.UUID]*groupView{}
	keys := map[uuid.UUID][]byte{}
	more := false
	for _, m := range rows {
		gid := groupOf[m.id]
		if _, ok := views[gid]; !ok {
			v, err := s.groupOneTx(ctx, tx, person, gid)
			if err != nil {
				return nil, false, err
			}
			views[gid] = &v
			if keys[gid], err = s.keys().GroupKeyTx(ctx, tx, gid); err != nil {
				return nil, false, err
			}
		}
		c, err := openGroupSealed(keys[gid], gid, m.id, m.sealed)
		if err != nil {
			continue
		}
		snippet, ok := keep(c)
		if !ok {
			continue
		}
		if len(out) == limit {
			more = true
			break
		}
		list, err := s.groupMessagesTx(ctx, tx, person, gid, keys[gid], []groupMsgRow{m})
		if err != nil {
			return nil, false, err
		}
		f := groupFound{GroupID: gid, Group: *views[gid], Message: list[0], Snippet: snippet}
		if at, ok := starred[m.id]; ok {
			f.StarredAt = &at
		}
		out = append(out, f)
	}
	return out, more, nil
}

// searchGroups is GET /v1/me/groups/search?q=&group=&before=: the
// messages of the person's groups that they see (from when they joined;
// up to when they left), the newest first, 30 a page. A word is found as
// in people chats (each word of q begins one of the message's words). The
// index holds keyed tokens of each group's own (a key derived from the
// group's key), never words.
func (s *Server) searchGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 200 {
		writeErr(w, http.StatusBadRequest, "q is up to 200 characters")
		return
	}
	if words, _ := peoplechats.Query(make([]byte, 32), q); len(words) == 0 {
		writeErr(w, http.StatusBadRequest, "q needs a word of two letters or more")
		return
	}
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var only *uuid.UUID
	if c := r.URL.Query().Get("group"); c != "" {
		id, err := uuid.Parse(c)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "group is a group's id")
			return
		}
		only = &id
	}
	if !s.limiter("people-search:"+person.String(), 100*time.Millisecond, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Searching very fast. Wait a moment and try again.")
		return
	}
	out := []groupFound{}
	more := false
	var next *listCursor
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT group_id FROM group_members WHERE person_id = $1 AND ($2::uuid IS NULL OR group_id = $2) ORDER BY group_id`, person, only)
		if err != nil {
			return err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(groups) == 0 {
			return err
		}
		var words []string
		var gids []uuid.UUID
		var toks [][]byte
		n := 0
		for _, gid := range groups {
			key, err := s.keys().GroupKeyTx(ctx, tx, gid)
			if errors.Is(err, peoplechats.ErrNoKey) {
				continue
			}
			if err != nil {
				return err
			}
			ws, ts := peoplechats.Query(peoplechats.GroupSearchKey(key, gid), q)
			words, n = ws, len(ts)
			for _, t := range ts {
				gids, toks = append(gids, gid), append(toks, t)
			}
		}
		if n == 0 {
			return nil
		}
		keep := func(v peopleSealed) (string, bool) {
			texts := searchTexts(v)
			if !peoplechats.Matches(words, texts...) {
				return "", false
			}
			for _, t := range texts {
				if sn := peoplechats.Snippet(t, words, 120); sn != "" {
					return sn, true
				}
			}
			return "", true
		}
		cur := before
		for round := 0; round < peopleSearchRounds; round++ {
			sql := `SELECT ` + "x.id, x.sender_id, x.kind, x.sealed, x.event, x.attachment, x.card_type, x.reply_to, x.forwards, x.mentions, x.deleted_at, x.paid_at, x.paid_by, x.created_at" + `,
				k.starred_at, (SELECT r.reaction FROM group_reactions r WHERE r.group_id = x.group_id AND r.message_id = x.id AND r.person_id = $1), x.group_id
				FROM group_messages x LEFT JOIN group_marks k ON k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1
				WHERE k.hidden_at IS NULL AND x.deleted_at IS NULL AND x.sealed IS NOT NULL
				  AND (x.group_id, x.id) IN (SELECT s.group_id, s.message_id FROM group_search s JOIN unnest($2::uuid[], $3::bytea[]) AS q(g, t) ON s.group_id = q.g AND s.token = q.t
				    GROUP BY s.group_id, s.message_id HAVING count(*) = $4)`
			args := []any{person, gids, toks, n}
			if cur != nil {
				sql += ` AND (x.created_at, x.id) < ($5, $6)`
				args = append(args, cur.at, cur.id)
			}
			rows, err := tx.Query(ctx, sql+` ORDER BY x.created_at DESC, x.id DESC LIMIT `+strconv.Itoa(peopleSearchScan), args...)
			if err != nil {
				return err
			}
			groupOf := map[uuid.UUID]uuid.UUID{}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupMsgRow, error) {
				var gid uuid.UUID
				m, err := scanGroupMsg(r, &gid)
				groupOf[m.id] = gid
				return m, err
			})
			if err != nil {
				return err
			}
			found, full, err := s.groupFoundTx(ctx, tx, person, list, groupOf, nil, keep, peopleSearchPage-len(out))
			if err != nil {
				return err
			}
			out = append(out, found...)
			if full {
				more = true
				last := out[len(out)-1].Message
				next = &listCursor{last.CreatedAt, last.ID}
				return nil
			}
			if len(list) < peopleSearchScan {
				return nil
			}
			last := list[len(list)-1]
			cur = &listCursor{last.createdAt, last.id}
			if len(out) == peopleSearchPage {
				more, next = true, cur
				return nil
			}
		}
		more, next = true, cur
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"results": out, "more": more}
	if more && next != nil {
		body["next"] = next.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// starredGroups is GET /v1/me/groups/starred?before=: the person's starred
// messages across their groups, the most recently starred first, 50 a
// page, each with its group.
func (s *Server) starredGroups(w http.ResponseWriter, r *http.Request) {
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
	out := []groupFound{}
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		sql := `SELECT ` + "x.id, x.sender_id, x.kind, x.sealed, x.event, x.attachment, x.card_type, x.reply_to, x.forwards, x.mentions, x.deleted_at, x.paid_at, x.paid_by, x.created_at" + `,
			k.starred_at, (SELECT r.reaction FROM group_reactions r WHERE r.group_id = x.group_id AND r.message_id = x.id AND r.person_id = $1), x.group_id
			FROM group_marks k JOIN group_messages x ON x.group_id = k.group_id AND x.id = k.message_id
			WHERE k.person_id = $1 AND k.starred_at IS NOT NULL AND k.hidden_at IS NULL AND x.deleted_at IS NULL`
		args := []any{person}
		if before != nil {
			sql += ` AND (k.starred_at, x.id) < ($2, $3)`
			args = append(args, before.at, before.id)
		}
		rows, err := tx.Query(ctx, sql+` ORDER BY k.starred_at DESC, x.id DESC LIMIT `+strconv.Itoa(peopleStarredPage+1), args...)
		if err != nil {
			return err
		}
		groupOf := map[uuid.UUID]uuid.UUID{}
		starred := map[uuid.UUID]time.Time{}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupMsgRow, error) {
			var gid uuid.UUID
			m, err := scanGroupMsg(r, &gid)
			groupOf[m.id] = gid
			if m.starredAt != nil {
				starred[m.id] = *m.starredAt
			}
			return m, err
		})
		if err != nil {
			return err
		}
		out, more, err = s.groupFoundTx(ctx, tx, person, list, groupOf, starred, func(peopleSealed) (string, bool) { return "", true }, peopleStarredPage)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"starred": out, "more": more}
	if more && len(out) > 0 {
		last := out[len(out)-1]
		body["next"] = listCursor{*last.StarredAt, last.Message.ID}.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// --- forwarding to and from groups ---

// groupDraftOf is one message of a group the person sees, as a draft to
// forward: marked forwarded once more, its file opened to be sealed again;
// a share card, an event or a deleted message is not forwarded.
func (s *Server) groupDraftOf(ctx context.Context, person, group, msg uuid.UUID) (peopleDraft, error) {
	var d peopleDraft
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, group.String(), false)
		if err != nil {
			return err
		}
		m, err := groupMessageTx(ctx, tx, person, g.ID, msg.String())
		if err != nil {
			return err
		}
		if m.kind == "event" {
			return errDoorNone
		}
		if m.deletedAt != nil {
			return customerSendErr{http.StatusConflict, "This message was deleted."}
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		v, err := openGroupSealed(key, g.ID, m.id, m.sealed)
		if err != nil {
			return err
		}
		if v.Card != nil && v.Card.Type == "split" {
			return customerSendErr{http.StatusConflict, "A share card is not forwarded."}
		}
		d = peopleDraft{kind: m.kind, body: v.Body, card: v.Card, location: v.Location, forwards: m.forwards + 1}
		if m.attach != nil {
			var sealed []byte
			var ctype string
			if err := tx.QueryRow(ctx, `SELECT sealed, type FROM group_files WHERE group_id = $1 AND message_id = $2`, g.ID, m.id).Scan(&sealed, &ctype); err != nil {
				return err
			}
			data, err := peoplechats.Open(key, g.ID, groupFileRow(m.id), sealed)
			if err != nil {
				return err
			}
			d.file = &sentFile{Type: ctype, Name: v.Name, Data: data}
		}
		return nil
	})
	return d, err
}

// forwardMessage is POST /v1/me/forward {chat_id | group_id, message_id,
// to_chats?, to_groups?}: one message of a people chat or a group to
// other people chats and groups, marked forwarded, with the caps of §8.2
// (five at once; one at a time for a message forwarded many times). A
// share card is not forwarded; a file is sealed again for each.
func (s *Server) forwardMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		ChatID    *uuid.UUID  `json:"chat_id"`
		GroupID   *uuid.UUID  `json:"group_id"`
		MessageID uuid.UUID   `json:"message_id"`
		ToChats   []uuid.UUID `json:"to_chats"`
		ToGroups  []uuid.UUID `json:"to_groups"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if (in.ChatID == nil) == (in.GroupID == nil) {
		writeErr(w, http.StatusBadRequest, "give the message's chat_id or group_id")
		return
	}
	n := len(in.ToChats) + len(in.ToGroups)
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "to_chats or to_groups names where to forward it")
		return
	}
	seen := map[uuid.UUID]bool{}
	for _, t := range append(slices.Clone(in.ToChats), in.ToGroups...) {
		if seen[t] {
			writeErr(w, http.StatusBadRequest, "a chat is named twice")
			return
		}
		seen[t] = true
	}
	var d peopleDraft
	if in.ChatID != nil {
		d, err = s.peopleDraftOf(ctx, person, *in.ChatID, in.MessageID)
	} else {
		d, err = s.groupDraftOf(ctx, person, *in.GroupID, in.MessageID)
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	many := d.forwards-1 >= peopleForwardedMany
	if limit := map[bool]int{true: 1, false: peopleForwardAtOnce}[many]; n > limit {
		msg := "A message can be forwarded to at most 5 chats at once."
		if many {
			msg = "This message was forwarded many times: it can be forwarded to one chat at a time."
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": msg, "forwarded_many": many})
		return
	}
	type sent struct {
		Kind      string    `json:"kind"`
		ID        uuid.UUID `json:"id"`
		MessageID uuid.UUID `json:"message_id"`
	}
	type failed struct {
		Kind  string    `json:"kind"`
		ID    uuid.UUID `json:"id"`
		Error string    `json:"error"`
	}
	out := struct {
		Sent   []sent   `json:"sent"`
		Failed []failed `json:"failed"`
	}{Sent: []sent{}, Failed: []failed{}}
	try := func(kind string, id uuid.UUID, fn func() (uuid.UUID, error)) bool {
		msg, err := fn()
		if err != nil {
			var se customerSendErr
			switch {
			case errors.As(err, &se):
				out.Failed = append(out.Failed, failed{kind, id, se.msg})
			case errors.Is(err, errDoorNone), errors.Is(err, pgx.ErrNoRows):
				out.Failed = append(out.Failed, failed{kind, id, "not found"})
			default:
				s.personFail(w, r, err)
				return false
			}
			return true
		}
		out.Sent = append(out.Sent, sent{kind, id, msg})
		return true
	}
	for _, t := range in.ToChats {
		if !try("person", t, func() (uuid.UUID, error) {
			m, _, err := s.peopleSend(ctx, person, t.String(), d)
			return m.ID, err
		}) {
			return
		}
	}
	for _, t := range in.ToGroups {
		if !try("group", t, func() (uuid.UUID, error) {
			m, _, err := s.groupSend(ctx, person, t.String(), d, nil)
			return m.ID, err
		}) {
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

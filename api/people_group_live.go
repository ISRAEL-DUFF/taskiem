package api

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// Groups on You's live connection (GET /v1/me/live; people-chatting §13,
// 5b D): what changed in the person's groups since the connection last
// looked, read in the person's own transaction, so only what they see:
// messages of their own time in each group (row-level security), their
// own memberships, marks and invites. Events (beside people_live.go's):
//
//	group_message   a message of one of their groups, new or changed (a
//	                reaction, deleted for everyone, a share marked paid, their
//	                own star on another device): {"group_id", "message": GroupMessage}
//	group           one of their groups changed (a new message, its info or
//	                settings, their own membership: muted, archived, left,
//	                removed, deleted for them): {"id", "hidden", "group"?: Group}
//	group_receipts  the ticks of their own recent messages in a group moved
//	                (A6): {"group_id", "messages": [{"id", "status"}]}
//	group_invite    an invite to a group waiting for them: {"group_id", "invite": GroupInvite}
//	group_typing    who is typing in their groups now (never themselves):
//	                {"groups": [{"group_id", "people": [ids]}]}
//
// A message deleted for them, or a group whose copy they deleted, is the
// gone event's ({"chat_id": group id, "id": message id or null}).

type liveSeen struct {
	id uuid.UUID
	at int64
}

// groupLive is one connection's place in the person's groups.
type groupLive struct {
	msgs, groups, receipts, invites, marks liveWindow
	seen                                   map[string]map[liveSeen]struct{}
	typing                                 []byte
	status                                 map[uuid.UUID]string
	// ticks: per group, the members whose read ticks go between them and
	// the person (groupTicksTx), kept for groupTicksKept, so a group's
	// marks moving does not read 256 people's cards each second.
	ticks map[uuid.UUID]groupTicksAt
}

// groupTicksAt is a group's ticks as read at a time.
type groupTicksAt struct {
	at  time.Time
	ids []uuid.UUID
}

// groupTicksKept is how long a connection keeps a group's ticks: a member
// turning their read ticks on or off shows within it.
const groupTicksKept = 30 * time.Second

// ticksOf is the group's ticks for this connection (groupTicksTx), read
// again once older than groupTicksKept.
func (gl *groupLive) ticksOf(ctx context.Context, s *Server, tx pgx.Tx, person, group uuid.UUID) ([]uuid.UUID, error) {
	if t, ok := gl.ticks[group]; ok && time.Since(t.at) < groupTicksKept {
		return t.ids, nil
	}
	ids, err := s.groupTicksTx(ctx, tx, person, group)
	if err != nil {
		return nil, err
	}
	gl.ticks[group] = groupTicksAt{time.Now(), ids}
	return ids, nil
}

func newGroupLive(since time.Time) *groupLive {
	return &groupLive{msgs: liveWindow{since}, groups: liveWindow{since}, receipts: liveWindow{since}, invites: liveWindow{since},
		marks: liveWindow{since}, seen: map[string]map[liveSeen]struct{}{}, status: map[uuid.UUID]string{}, ticks: map[uuid.UUID]groupTicksAt{}}
}

// fresh says whether this change was not yet sent, and remembers it.
func (gl *groupLive) fresh(kind string, id uuid.UUID, at time.Time) bool {
	m := gl.seen[kind]
	if m == nil {
		m = map[liveSeen]struct{}{}
		gl.seen[kind] = m
	}
	k := liveSeen{id, at.UnixMicro()}
	if _, done := m[k]; done {
		return false
	}
	m[k] = struct{}{}
	return true
}

// prune forgets changes older than each window.
func (gl *groupLive) prune() {
	for kind, m := range gl.seen {
		since := map[string]time.Time{"msg": gl.msgs.since, "group": gl.groups.since, "receipt": gl.receipts.since, "invite": gl.invites.since, "mark": gl.marks.since}[kind]
		for k := range m {
			if time.UnixMicro(k.at).Before(since) {
				delete(m, k)
			}
		}
	}
}

// liveGroupsTx reads what changed in the person's groups: events with an
// id (to send in order with the people chats'), the typing event (none
// when unchanged), and whether there is more to read at once.
func (s *Server) liveGroupsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, gl *groupLive) ([]liveEvent, []liveEvent, bool, error) {
	var out, now []liveEvent
	more := false
	rows, err := tx.Query(ctx, `SELECT group_id, left_at IS NULL FROM group_members WHERE person_id = $1 ORDER BY group_id`, person)
	if err != nil {
		return nil, nil, false, err
	}
	var groups, current []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var cur bool
		if err := rows.Scan(&id, &cur); err != nil {
			rows.Close()
			return nil, nil, false, err
		}
		groups = append(groups, id)
		if cur {
			current = append(current, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	keys := map[uuid.UUID][]byte{}
	keyOf := func(g uuid.UUID) ([]byte, error) {
		if k, ok := keys[g]; ok {
			return k, nil
		}
		k, err := s.keys().GroupKeyTx(ctx, tx, g)
		if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
			return nil, err
		}
		keys[g] = k
		return k, nil
	}

	if len(groups) > 0 {
		// Messages, new or changed, and the person's own marks.
		mrows, err := tx.Query(ctx, `SELECT `+"x.id, x.sender_id, x.kind, x.sealed, x.event, x.attachment, x.card_type, x.reply_to, x.forwards, x.mentions, x.deleted_at, x.paid_at, x.paid_by, x.created_at"+`,
			k.starred_at, (SELECT r.reaction FROM group_reactions r WHERE r.group_id = x.group_id AND r.message_id = x.id AND r.person_id = $1), x.group_id, x.changed_at
			FROM group_messages x JOIN group_members me ON me.group_id = x.group_id AND me.person_id = $1
			LEFT JOIN group_marks k ON k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1
			WHERE x.group_id = ANY ($2) AND x.changed_at > $3 AND k.hidden_at IS NULL
			  -- Someone who left: nothing changed after (a reaction, a deletion, a share marked paid; A5).
			  AND (me.left_at IS NULL OR x.changed_at <= me.left_at)
			ORDER BY x.changed_at, x.id LIMIT `+strconv.Itoa(liveBatch), person, groups, gl.msgs.since)
		if err != nil {
			return nil, nil, false, err
		}
		type changed struct {
			row   groupMsgRow
			group uuid.UUID
			at    time.Time
		}
		list, err := pgx.CollectRows(mrows, func(r pgx.CollectableRow) (changed, error) {
			var c changed
			row, err := scanGroupMsg(r, &c.group, &c.at)
			c.row = row
			return c, err
		})
		if err != nil {
			return nil, nil, false, err
		}
		var times []time.Time
		for _, c := range list {
			times = append(times, c.at)
		}
		if gl.msgs.slide(times, liveBatch) {
			more = true
		}
		// The person's own marks (a star from another device).
		krows, err := tx.Query(ctx, `SELECT `+"x.id, x.sender_id, x.kind, x.sealed, x.event, x.attachment, x.card_type, x.reply_to, x.forwards, x.mentions, x.deleted_at, x.paid_at, x.paid_by, x.created_at"+`,
			k.starred_at, (SELECT r.reaction FROM group_reactions r WHERE r.group_id = x.group_id AND r.message_id = x.id AND r.person_id = $1), x.group_id, k.changed_at
			FROM group_marks k JOIN group_messages x ON x.group_id = k.group_id AND x.id = k.message_id
			WHERE k.person_id = $1 AND k.changed_at > $2 AND k.hidden_at IS NULL ORDER BY k.changed_at LIMIT `+strconv.Itoa(liveBatch), person, gl.marks.since)
		if err != nil {
			return nil, nil, false, err
		}
		marks, err := pgx.CollectRows(krows, func(r pgx.CollectableRow) (changed, error) {
			var c changed
			row, err := scanGroupMsg(r, &c.group, &c.at)
			c.row = row
			return c, err
		})
		if err != nil {
			return nil, nil, false, err
		}
		times = times[:0]
		for _, c := range marks {
			times = append(times, c.at)
		}
		if gl.marks.slide(times, liveBatch) {
			more = true
		}
		byGroup := map[uuid.UUID][]changed{}
		var order []uuid.UUID
		for i, c := range append(list, marks...) {
			kind := "msg"
			if i >= len(list) {
				kind = "mark"
			}
			if !gl.fresh(kind, c.row.id, c.at) {
				continue
			}
			if _, ok := byGroup[c.group]; !ok {
				order = append(order, c.group)
			}
			byGroup[c.group] = append(byGroup[c.group], c)
		}
		for _, g := range order {
			key, err := keyOf(g)
			if err != nil {
				return nil, nil, false, err
			}
			cs := byGroup[g]
			rowsOf := make([]groupMsgRow, len(cs))
			for i, c := range cs {
				rowsOf[i] = c.row
			}
			views, err := s.groupMessagesTx(ctx, tx, person, g, key, rowsOf)
			if err != nil {
				return nil, nil, false, err
			}
			for i, v := range views {
				if v.Mine && v.Status != "" {
					gl.status[v.ID] = v.Status
				}
				out = append(out, liveEvent{cs[i].at.UnixMicro(), "group_message", mustJSON(map[string]any{"group_id": g, "message": v})})
			}
			if slices.Contains(current, g) && len(cs) > 0 {
				last := cs[0].row.createdAt
				for _, c := range cs {
					if c.row.createdAt.After(last) {
						last = c.row.createdAt
					}
				}
				if err := deliveredUptoTx(ctx, tx, person, g, last); err != nil {
					return nil, nil, false, err
				}
			}
		}

		// Groups changed: a new message, its info, the person's own
		// membership (not the group's changes after they left).
		grows, err := tx.Query(ctx, `SELECT m.group_id, GREATEST(CASE WHEN m.left_at IS NULL THEN g.changed_at ELSE LEAST(g.changed_at, m.left_at) END, m.changed_at) AS at,
			m.hidden_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = m.group_id AND m.visible @> x.created_at)
			FROM group_members m JOIN people_groups g ON g.id = m.group_id
			WHERE m.person_id = $1 AND (m.changed_at > $2 OR (g.changed_at > $2 AND (m.left_at IS NULL OR g.changed_at <= m.left_at)))
			ORDER BY at, m.group_id LIMIT `+strconv.Itoa(liveBatch), person, gl.groups.since)
		if err != nil {
			return nil, nil, false, err
		}
		type gchange struct {
			id     uuid.UUID
			at     time.Time
			hidden bool
		}
		glist, err := pgx.CollectRows(grows, func(r pgx.CollectableRow) (gchange, error) {
			var c gchange
			return c, r.Scan(&c.id, &c.at, &c.hidden)
		})
		if err != nil {
			return nil, nil, false, err
		}
		times = times[:0]
		for _, c := range glist {
			times = append(times, c.at)
			if !gl.fresh("group", c.id, c.at) {
				continue
			}
			if c.hidden {
				out = append(out, liveEvent{c.at.UnixMicro(), "group", mustJSON(map[string]any{"id": c.id, "hidden": true})})
				continue
			}
			v, err := s.groupOneTx(ctx, tx, person, c.id)
			if err != nil {
				return nil, nil, false, err
			}
			out = append(out, liveEvent{c.at.UnixMicro(), "group", mustJSON(map[string]any{"id": c.id, "hidden": false, "group": v})})
		}
		if gl.groups.slide(times, liveBatch) {
			more = true
		}
	}

	if len(current) > 0 {
		// The ticks of the person's own recent messages, when another
		// member's read or delivered marks, or time in the group, moved
		// (A6): marks_at, not changed_at, so another member's mute,
		// archive or mentions wakes no one.
		rrows, err := tx.Query(ctx, `SELECT group_id, max(marks_at) FROM group_members WHERE group_id = ANY ($1) AND person_id <> $2 AND marks_at > $3
			GROUP BY group_id ORDER BY max(marks_at) LIMIT `+strconv.Itoa(liveBatch), current, person, gl.receipts.since)
		if err != nil {
			return nil, nil, false, err
		}
		type rchange struct {
			id uuid.UUID
			at time.Time
		}
		rlist, err := pgx.CollectRows(rrows, func(r pgx.CollectableRow) (rchange, error) {
			var c rchange
			return c, r.Scan(&c.id, &c.at)
		})
		if err != nil {
			return nil, nil, false, err
		}
		var times []time.Time
		for _, c := range rlist {
			times = append(times, c.at)
			if !gl.fresh("receipt", c.id, c.at) {
				continue
			}
			mrows, err := tx.Query(ctx, `SELECT id FROM group_messages WHERE group_id = $1 AND sender_id = $2 AND kind <> 'event' AND deleted_at IS NULL
				ORDER BY created_at DESC LIMIT 50`, c.id, person)
			if err != nil {
				return nil, nil, false, err
			}
			latest, err := pgx.CollectRows(mrows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return nil, nil, false, err
			}
			// Only those whose ticks may still move: one shown read stays so.
			var ids []uuid.UUID
			for _, id := range latest {
				if gl.status[id] != "read" {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				continue
			}
			ticks, err := gl.ticksOf(ctx, s, tx, person, c.id)
			if err != nil {
				return nil, nil, false, err
			}
			s.groupTickRuns.Add(1)
			st, err := groupStatusesWithTx(ctx, tx, person, c.id, ids, ticks)
			if err != nil {
				return nil, nil, false, err
			}
			type tick struct {
				ID     uuid.UUID `json:"id"`
				Status string    `json:"status"`
			}
			var changed []tick
			for _, id := range ids {
				if gl.status[id] != st[id] {
					gl.status[id] = st[id]
					changed = append(changed, tick{id, st[id]})
				}
			}
			if len(changed) > 0 {
				out = append(out, liveEvent{c.at.UnixMicro(), "group_receipts", mustJSON(map[string]any{"group_id": c.id, "messages": changed})})
			}
		}
		if gl.receipts.slide(times, liveBatch) {
			more = true
		}

		// Who is typing in the person's groups now.
		trows, err := tx.Query(ctx, `SELECT group_id, person_id FROM group_typing WHERE group_id = ANY ($1) AND person_id <> $2 AND until > now()
			ORDER BY group_id, person_id`, current, person)
		if err != nil {
			return nil, nil, false, err
		}
		type typing struct {
			GroupID uuid.UUID   `json:"group_id"`
			People  []uuid.UUID `json:"people"`
		}
		list := []typing{}
		for trows.Next() {
			var g, p uuid.UUID
			if err := trows.Scan(&g, &p); err != nil {
				trows.Close()
				return nil, nil, false, err
			}
			if n := len(list); n > 0 && list[n-1].GroupID == g {
				list[n-1].People = append(list[n-1].People, p)
			} else {
				list = append(list, typing{g, []uuid.UUID{p}})
			}
		}
		trows.Close()
		if err := trows.Err(); err != nil {
			return nil, nil, false, err
		}
		data := mustJSON(map[string]any{"groups": list})
		if (gl.typing != nil || len(list) > 0) && string(data) != string(gl.typing) {
			now = append(now, liveEvent{0, "group_typing", data})
		}
		gl.typing = data
	}

	// Invites waiting for the person.
	irows, err := tx.Query(ctx, `SELECT group_id, changed_at FROM group_invites WHERE person_id = $1 AND NOT hidden AND changed_at > $2
		ORDER BY changed_at LIMIT `+strconv.Itoa(liveBatch), person, gl.invites.since)
	if err != nil {
		return nil, nil, false, err
	}
	type ichange struct {
		id uuid.UUID
		at time.Time
	}
	ilist, err := pgx.CollectRows(irows, func(r pgx.CollectableRow) (ichange, error) {
		var c ichange
		return c, r.Scan(&c.id, &c.at)
	})
	if err != nil {
		return nil, nil, false, err
	}
	var times []time.Time
	for _, c := range ilist {
		times = append(times, c.at)
		if !gl.fresh("invite", c.id, c.at) {
			continue
		}
		id := c.id
		inv, err := s.groupInvitesTx(ctx, tx, person, &id)
		if err != nil {
			return nil, nil, false, err
		}
		if len(inv) == 1 {
			out = append(out, liveEvent{c.at.UnixMicro(), "group_invite", mustJSON(map[string]any{"group_id": c.id, "invite": inv[0]})})
		}
	}
	if gl.invites.slide(times, liveBatch) {
		more = true
	}
	return out, now, more, nil
}

package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// You's live connection (people-chatting §13; "As built: stage 5a gaps,
// chat features (server)"): GET /v1/me/live, as server-sent events, tells
// the person's app or browser what changes in their own people chats as it
// happens, so it need not ask every few seconds (polling stays the
// fallback, and the client reconciles with it). It follows the Chat page's
// stream (chat_stream.go): each connection asks every second what changed
// since it last looked, from indexes (migration 00479), in the person's
// own transaction, so it carries nothing but the person's own copy.
//
// Events (message, chat and gone carry an id, the change's time in
// microseconds, so a client that reconnects with Last-Event-ID is sent
// what it missed, going back at most an hour; an event may come twice
// across a reconnection, and clients merge by id):
//
//	message   a message of one of the person's chats, new or changed (delivered,
//	          read, a reaction, a star, deleted for everyone):
//	          {"chat_id", "request", "message": PeopleMessage}
//	chat      one of the person's chats changed (a new request or chat, a new
//	          last message, accepted, muted, archived, deleted for them):
//	          {"id", "hidden", "chat"?: PeopleChat}
//	gone      a message deleted for the person (on another device):
//	          {"chat_id", "id"}; or a request they deleted: {"chat_id", "id": null}
//	typing    the chats whose other person is typing now: {"typing": [chat ids]}
//	presence  the people the person chats with whose online or last seen
//	          changed, as their settings let the person see them:
//	          {"people": [{"person_id", "chat_id", "online", "last_seen_at"}]}
//	          (all of them on connecting)
//
// then a ready event on connecting ({"connection": id}) and a ping event
// at least every ten seconds.
//
// Presence moves onto the connection: while one of the person's live
// connections is in front they are online; when the last closes, or says
// it is away (POST /v1/me/presence {online: false, connection}), they are
// away, last seen then. The heartbeat (POST /v1/me/presence) still works
// for clients without the connection.
const (
	liveEvery         = time.Second
	livePresenceEvery = 5 * time.Second
	liveBeatEvery     = 30 * time.Second
	// liveStale is when a connection's row, not beaten for this long, is
	// taken for one that ended without saying so.
	liveStale     = 2 * time.Minute
	livePerPerson = 10
	liveBatch     = 200
	livePeers     = 200
)

// liveConnect records a connection of the person's (in front or away), and
// says they are here when it is in front.
func (s *Server) liveConnect(ctx context.Context, person, conn uuid.UUID, front bool) error {
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM person_live WHERE person_id = $1 AND seen_at < now() - $2::interval`, person, liveStale.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO person_live (person_id, id, front) VALUES ($1, $2, $3)`, person, conn, front)
		return err
	})
	if err != nil || !front {
		return err
	}
	return s.people().Touch(ctx, person)
}

// liveBeat says the connection is still open, and, while it is in front,
// that the person is here; ok is false once the account can take no more
// (deleted or suspended), which ends the connection.
func (s *Server) liveBeat(ctx context.Context, person, conn uuid.UUID) (bool, error) {
	var front, active bool
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status = 'active' AND deleted_at IS NULL FROM users WHERE id = $1`, person).Scan(&active); err != nil || !active {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO person_live (person_id, id) VALUES ($1, $2)
			ON CONFLICT (person_id, id) DO UPDATE SET seen_at = now() RETURNING front`, person, conn).Scan(&front)
	})
	if err != nil || !active {
		return false, err
	}
	if front {
		return true, s.people().Touch(ctx, person)
	}
	return true, nil
}

// liveFront marks one of the person's connections in front or away (ok:
// it is theirs and open), and the person here, or away when none of their
// connections is in front any more.
func (s *Server) liveFront(ctx context.Context, person, conn uuid.UUID, front bool) (bool, error) {
	var found, others bool
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE person_live SET front = $3, seen_at = now() WHERE person_id = $1 AND id = $2`, person, conn, front)
		if err != nil {
			return err
		}
		found = tag.RowsAffected() == 1
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM person_live WHERE person_id = $1 AND id <> $2 AND front AND seen_at > now() - $3::interval)`,
			person, conn, liveStale.String()).Scan(&others)
	})
	if err != nil || !found {
		return found, err
	}
	if front {
		return true, s.people().Touch(ctx, person)
	}
	if !others {
		return true, s.people().Away(ctx, person)
	}
	return true, nil
}

// liveClose forgets a connection that ended; when it was in front and no
// other of the person's is, they are away, last seen now: also when the
// account was suspended meanwhile (it is shown suspended, not online);
// not once it is deleted (it has no presence left).
func (s *Server) liveClose(person, conn uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var front, others, active bool
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `DELETE FROM person_live WHERE person_id = $1 AND id = $2 RETURNING front`, person, conn).Scan(&front)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT deleted_at IS NULL FROM users WHERE id = $1`, person).Scan(&active); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM person_live WHERE person_id = $1 AND front AND seen_at > now() - $2::interval)`,
			person, liveStale.String()).Scan(&others)
	})
	if err != nil {
		s.Logger.Warn("people: live connection closed", "err", err)
		return
	}
	if front && !others && active {
		if err := s.people().Away(ctx, person); err != nil {
			s.Logger.Warn("people: away", "err", err)
		}
	}
}

// liveAway is whether the person has live connections open and all of
// them away: a request of theirs then does not make them online.
func (s *Server) liveAway(ctx context.Context, person uuid.UUID) bool {
	var open, front bool
	_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) > 0, COALESCE(bool_or(front), false) FROM person_live WHERE person_id = $1 AND seen_at > now() - $2::interval`,
			person, liveStale.String()).Scan(&open, &front)
	})
	return open && !front
}

// liveWindow is how far back one kind of change is read: from since,
// sliding to just before the newest change seen (the look-back covers a
// change that commits a little after its time), or, after a full batch,
// to its last, with more to read at once.
type liveWindow struct{ since time.Time }

func (w *liveWindow) slide(times []time.Time, batch int) (more bool) {
	n := len(times)
	if n == 0 {
		return false
	}
	if n == batch {
		if at := times[n-1]; at.After(w.since) {
			w.since = at.Add(-time.Microsecond)
		}
		return true
	}
	if edge := times[n-1].Add(-chatStreamLookBack); edge.After(w.since) {
		w.since = edge
	}
	return false
}

// livePresence is what the person may see of one other person's presence.
type livePresence struct {
	PersonID   uuid.UUID  `json:"person_id"`
	ChatID     uuid.UUID  `json:"chat_id"`
	Online     *bool      `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

func (p livePresence) same(o livePresence) bool {
	eqB := (p.Online == nil) == (o.Online == nil) && (p.Online == nil || *p.Online == *o.Online)
	eqT := (p.LastSeenAt == nil) == (o.LastSeenAt == nil) && (p.LastSeenAt == nil || p.LastSeenAt.Equal(*o.LastSeenAt))
	return eqB && eqT && p.ChatID == o.ChatID
}

// liveEvent is one event to send.
type liveEvent struct {
	id    int64 // 0: none
	event string
	data  []byte
}

// peopleLive is GET /v1/me/live.
func (s *Server) peopleLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	release, ok := s.liveGate.acquireN(person, livePerPerson, "person")
	if !ok {
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "too many live connections open: close another, or the app will poll instead")
		return
	}
	defer release()
	conn := uuid.New()
	front := r.URL.Query().Get("away") != "1"
	if err := s.liveConnect(ctx, person, conn, front); err != nil {
		s.fail(w, r, err)
		return
	}
	defer s.liveClose(person, conn)
	ping := s.ChatStreamPing
	if ping <= 0 {
		ping = chatStreamPing
	}
	since := time.Now().Add(-chatStreamLookBack)
	if id, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && id > 0 {
		if at := time.UnixMicro(id); at.Before(time.Now().Add(time.Minute)) {
			since = at.Add(-chatStreamLookBack)
		}
		if floor := time.Now().Add(-chatStreamMaxReplay); since.Before(floor) {
			since = floor
		}
	}
	msgs, chats, gone := &liveWindow{since}, &liveWindow{since}, &liveWindow{since}
	groups := newGroupLive(since)
	baskets := newBasketLive(since)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) error {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return rc.Flush()
	}
	if write("retry: 2000\n\nevent: ready\ndata: %s\n\n", mustJSON(map[string]any{"connection": conn})) != nil {
		return
	}

	type seenKey struct {
		id uuid.UUID
		at int64
	}
	seenMsg, seenChat, seenGone := map[seenKey]struct{}{}, map[seenKey]struct{}{}, map[seenKey]struct{}{}
	var typing []uuid.UUID
	shown := map[uuid.UUID]livePresence{}
	var presenceAt time.Time
	beatAt := time.Now()
	deadline := time.Now().Add(chatStreamMax)
	lastWrite := time.Now()
	tick := time.NewTicker(liveEvery)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		if time.Since(beatAt) >= liveBeatEvery {
			ok, err := s.liveBeat(ctx, person, conn)
			if err != nil || !ok {
				if err != nil && ctx.Err() == nil {
					s.Logger.Error("live connection", "err", err)
				}
				return
			}
			beatAt = time.Now()
		}
		var out []liveEvent
		var basketIDs []uuid.UUID
		more := false
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			out = nil
			key, err := s.keys().KeyTx(ctx, tx, person, false)
			if errors.Is(err, peoplechats.ErrNoKey) {
				key = nil // nothing kept yet
			} else if err != nil {
				return err
			}
			// Messages, new or changed.
			rows, err := tx.Query(ctx, `SELECT `+peopleMessageCols+`, chat_id, changed_at FROM people_messages
				WHERE person_id = $1 AND changed_at > $2 ORDER BY changed_at, id LIMIT `+strconv.Itoa(liveBatch), person, msgs.since)
			if err != nil {
				return err
			}
			type changedMsg struct {
				row  peopleRow
				chat uuid.UUID
				at   time.Time
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (changedMsg, error) {
				var c changedMsg
				row, err := scanPeopleRow(r, &c.chat, &c.at)
				c.row = row
				return c, err
			})
			if err != nil {
				return err
			}
			var times []time.Time
			var fresh []changedMsg
			var chatIDs []uuid.UUID
			for _, c := range list {
				times = append(times, c.at)
				k := seenKey{c.row.ID, c.at.UnixMicro()}
				if _, done := seenMsg[k]; done {
					continue
				}
				seenMsg[k] = struct{}{}
				fresh = append(fresh, c)
				if !slices.Contains(chatIDs, c.chat) {
					chatIDs = append(chatIDs, c.chat)
				}
			}
			if msgs.slide(times, liveBatch) {
				more = true
			}
			if len(fresh) > 0 {
				type chatOf struct {
					peer    *uuid.UUID
					request bool
					ticks   bool
				}
				info := map[uuid.UUID]*chatOf{}
				rows, err := tx.Query(ctx, `SELECT id, peer_id, request_at IS NOT NULL FROM people_chats WHERE person_id = $1 AND id = ANY ($2)`, person, chatIDs)
				if err != nil {
					return err
				}
				for rows.Next() {
					var id uuid.UUID
					c := &chatOf{}
					if err := rows.Scan(&id, &c.peer, &c.request); err != nil {
						rows.Close()
						return err
					}
					info[id] = c
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
				for _, c := range info {
					if c.ticks, err = s.readTicksTx(ctx, tx, person, c.peer); err != nil {
						return err
					}
				}
				for _, c := range fresh {
					ci := info[c.chat]
					if ci == nil {
						continue
					}
					m, _ := c.row.view(key, person)
					out = append(out, liveEvent{c.at.UnixMicro(), "message",
						mustJSON(map[string]any{"chat_id": c.chat, "request": ci.request, "message": hideRead(m, ci.ticks)})})
				}
			}

			// Chats, new or changed.
			crow, err := tx.Query(ctx, `SELECT id, changed_at, hidden_at IS NOT NULL, empty FROM people_chats
				WHERE person_id = $1 AND changed_at > $2 ORDER BY changed_at, id LIMIT `+strconv.Itoa(liveBatch), person, chats.since)
			if err != nil {
				return err
			}
			type changedChat struct {
				id            uuid.UUID
				at            time.Time
				hidden, empty bool
			}
			clist, err := pgx.CollectRows(crow, func(r pgx.CollectableRow) (changedChat, error) {
				var c changedChat
				return c, r.Scan(&c.id, &c.at, &c.hidden, &c.empty)
			})
			if err != nil {
				return err
			}
			times = times[:0]
			var show []uuid.UUID
			at := map[uuid.UUID]time.Time{}
			for _, c := range clist {
				times = append(times, c.at)
				k := seenKey{c.id, c.at.UnixMicro()}
				if _, done := seenChat[k]; done {
					continue
				}
				seenChat[k] = struct{}{}
				switch {
				case c.hidden:
					out = append(out, liveEvent{c.at.UnixMicro(), "chat", mustJSON(map[string]any{"id": c.id, "hidden": true})})
				case c.empty:
					// A chat opened that no one has written in: in no list yet.
				default:
					show = append(show, c.id)
					at[c.id] = c.at
				}
			}
			if chats.slide(times, liveBatch) {
				more = true
			}
			if len(show) > 0 {
				rows, err := tx.Query(ctx, `SELECT `+peopleCopyCols+peopleCopyFrom+` WHERE c.person_id = $1 AND c.id = ANY ($2)`, person, show)
				if err != nil {
					return err
				}
				copies, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (peopleCopy, error) { return scanPeopleCopy(r) })
				if err != nil {
					return err
				}
				views, err := s.peopleViewsTx(ctx, tx, person, copies)
				if err != nil {
					return err
				}
				for _, v := range views {
					out = append(out, liveEvent{at[v.ID].UnixMicro(), "chat", mustJSON(map[string]any{"id": v.ID, "hidden": false, "chat": v})})
				}
			}

			// Deleted for the person, elsewhere.
			grow, err := tx.Query(ctx, `SELECT chat_id, message_id, at FROM people_gone WHERE person_id = $1 AND at > $2 ORDER BY at LIMIT `+strconv.Itoa(liveBatch),
				person, gone.since)
			if err != nil {
				return err
			}
			type goneRow struct {
				chat uuid.UUID
				msg  *uuid.UUID
				at   time.Time
			}
			glist, err := pgx.CollectRows(grow, func(r pgx.CollectableRow) (goneRow, error) {
				var g goneRow
				return g, r.Scan(&g.chat, &g.msg, &g.at)
			})
			if err != nil {
				return err
			}
			times = times[:0]
			for _, g := range glist {
				times = append(times, g.at)
				id := g.chat
				if g.msg != nil {
					id = *g.msg
				}
				k := seenKey{id, g.at.UnixMicro()}
				if _, done := seenGone[k]; done {
					continue
				}
				seenGone[k] = struct{}{}
				out = append(out, liveEvent{g.at.UnixMicro(), "gone", mustJSON(map[string]any{"chat_id": g.chat, "id": g.msg})})
			}
			if gone.slide(times, liveBatch) {
				more = true
			}

			// The person's shared baskets (people_baskets_live.go).
			bids, bmore, err := baskets.idsTx(ctx, tx)
			if err != nil {
				return err
			}
			basketIDs = bids
			more = more || bmore

			// The person's groups (people_group_live.go).
			gev, gnow, gmore, err := s.liveGroupsTx(ctx, tx, person, groups)
			if err != nil {
				return err
			}
			out = append(out, gev...)
			more = more || gmore
			defer func() { out = append(out, gnow...) }()

			// Who is typing to the person now: only in their chats that are
			// accepted and not deleted, and not from someone they blocked
			// (typing is only written so in any case).
			trow, err := tx.Query(ctx, `SELECT t.chat_id FROM people_typing t JOIN people_chats c ON c.person_id = t.person_id AND c.id = t.chat_id
				WHERE t.person_id = $1 AND t.until > now() AND c.request_at IS NULL AND c.hidden_at IS NULL AND c.accepted_at IS NOT NULL
				  AND NOT EXISTS (SELECT 1 FROM people_blocks b WHERE b.person_id = c.person_id AND b.blocked_id = c.peer_id)
				ORDER BY t.chat_id`, person)
			if err != nil {
				return err
			}
			now, err := pgx.CollectRows(trow, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			if now == nil {
				now = []uuid.UUID{}
			}
			if typing == nil || !slices.Equal(now, typing) {
				if typing != nil || len(now) > 0 {
					out = append(out, liveEvent{0, "typing", mustJSON(map[string]any{"typing": now})})
				}
				typing = now
			}

			// The presence of the people the person chats with, as their
			// settings (and the person's own) let it be seen.
			if time.Since(presenceAt) >= livePresenceEvery {
				presenceAt = time.Now()
				prow, err := tx.Query(ctx, `SELECT id, peer_id FROM people_chats WHERE person_id = $1 AND peer_id IS NOT NULL AND hidden_at IS NULL AND NOT empty
					ORDER BY last_message_at DESC LIMIT `+strconv.Itoa(livePeers), person)
				if err != nil {
					return err
				}
				chatOfPeer := map[uuid.UUID]uuid.UUID{}
				var peers []uuid.UUID
				for prow.Next() {
					var chat, peer uuid.UUID
					if err := prow.Scan(&chat, &peer); err != nil {
						prow.Close()
						return err
					}
					chatOfPeer[peer] = chat
					peers = append(peers, peer)
				}
				prow.Close()
				if err := prow.Err(); err != nil {
					return err
				}
				var changed []livePresence
				if len(peers) > 0 {
					cards, err := s.people().CardsTx(ctx, tx, person, peers)
					if err != nil {
						return err
					}
					for _, c := range cards {
						p := livePresence{PersonID: c.ID, ChatID: chatOfPeer[c.ID], Online: c.Online, LastSeenAt: c.LastSeenAt}
						if old, ok := shown[c.ID]; ok && old.same(p) {
							continue
						}
						shown[c.ID] = p
						changed = append(changed, p)
					}
				}
				if len(changed) > 0 {
					out = append(out, liveEvent{0, "presence", mustJSON(map[string]any{"people": changed})})
				}
			}
			return nil
		})
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("live connection", "person", person, "err", err)
			}
			return
		}
		out = append(out, s.basketEvents(ctx, person, baskets, basketIDs)...)
		// Changes in the order they were made (typing and presence last).
		k := 0
		for k < len(out) && out[k].id > 0 {
			k++
		}
		slices.SortStableFunc(out[:k], func(a, b liveEvent) int { return cmp.Compare(a.id, b.id) })
		for _, e := range out {
			var werr error
			if e.id > 0 {
				werr = write("id: %d\nevent: %s\ndata: %s\n\n", e.id, e.event, e.data)
			} else {
				werr = write("event: %s\ndata: %s\n\n", e.event, e.data)
			}
			if werr != nil {
				return
			}
			lastWrite = time.Now()
		}
		groups.prune()
		for _, m := range []struct {
			seen  map[seenKey]struct{}
			since time.Time
		}{{seenMsg, msgs.since}, {seenChat, chats.since}, {seenGone, gone.since}} {
			for k := range m.seen {
				if time.UnixMicro(k.at).Before(m.since) {
					delete(m.seen, k)
				}
			}
		}
		if time.Since(lastWrite) >= ping {
			if write("event: ping\ndata: {}\n\n") != nil {
				return
			}
			lastWrite = time.Now()
		}
		if more {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done():
			return
		case <-tick.C:
		}
	}
}

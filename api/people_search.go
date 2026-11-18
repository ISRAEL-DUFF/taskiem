package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// Search, starred messages and typing in people chats (people-chatting
// §6.2, §7, §13; "As built: stage 5a gaps, chat features (server)";
// migration 00479).

// The limits of search and typing.
var (
	// peopleSearchPage is how many results a search answers at once.
	peopleSearchPage = 30
	// peopleSearchScan is how many indexed candidates one round reads, and
	// peopleSearchRounds how many rounds one page may take.
	peopleSearchScan   = 100
	peopleSearchRounds = 10
	// peopleSearchBackfill is how many older messages one transaction
	// indexes on a person's first search.
	peopleSearchBackfill = 500
	// peopleStarredPage is how many starred messages a page answers.
	peopleStarredPage = 50
	// peopleTypingFor is how long "typing" shows after the last time it
	// was said; peopleTypingEvery is how often it is passed on.
	peopleTypingFor   = 6 * time.Second
	peopleTypingEvery = 3 * time.Second
)

// searchTexts are what of a message is searched: its words (a caption),
// a file's name, a card's title, business and what for, and a place's
// label.
func searchTexts(v peopleSealed) []string {
	out := []string{v.Body, v.Name}
	if v.Card != nil {
		out = append(out, v.Card.Title, v.Card.What, v.Card.Business)
	}
	if v.Location != nil {
		out = append(out, v.Location.Label)
	}
	return out
}

// indexPeopleMessageTx indexes one copy of a message for its owner's
// search: keyed tokens of its words' beginnings, never the words
// (peoplechats.Tokens). The other's copy is indexed through the reach.
func indexPeopleMessageTx(ctx context.Context, tx pgx.Tx, key []byte, owner, conv, id uuid.UUID, v peopleSealed) error {
	toks := peoplechats.Tokens(peoplechats.SearchKey(key, owner), searchTexts(v)...)
	if len(toks) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO people_search (person_id, message_id, conversation_id, token) SELECT $1, $2, $3, unnest($4::bytea[]) ON CONFLICT DO NOTHING`,
		owner, id, conv, toks)
	return err
}

// goneTx records a message deleted for the person (or, with id
// uuid.Nil, a chat of theirs gone: a request they deleted), so their
// other devices' live connections drop it; kept an hour.
func goneTx(ctx context.Context, tx pgx.Tx, person, chat, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM people_gone WHERE person_id = $1 AND at < now() - interval '1 hour'`, person); err != nil {
		return err
	}
	var msg *uuid.UUID
	if id != uuid.Nil {
		msg = &id
	}
	_, err := tx.Exec(ctx, `INSERT INTO people_gone (person_id, chat_id, message_id) VALUES ($1, $2, $3)`, person, chat, msg)
	return err
}

// searchReady indexes the person's messages kept before the index was
// (once, on their first search), a batch a transaction. Each batch keeps
// how far it got (people_search_ready.through_at, through_id), so a
// backfill cut short (the request ended, the server stopped) goes on from
// there; done_at is set once all are indexed. The batch's messages are
// held (FOR SHARE) until it commits, so a delete for me or for everyone
// meanwhile waits for it and then removes what it indexed, and one done
// before is not indexed at all.
func (s *Server) searchReady(ctx context.Context, person uuid.UUID) error {
	var ready bool
	after, afterID := time.Time{}, uuid.Nil
	if err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var done *time.Time
		var through *time.Time
		var throughID *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT done_at, through_at, through_id FROM people_search_ready WHERE person_id = $1`, person).Scan(&done, &through, &throughID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ready = done != nil
		if through != nil && throughID != nil {
			after, afterID = *through, *throughID
		}
		return nil
	}); err != nil || ready {
		return err
	}
	for {
		done := false
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			key, err := s.keys().KeyTx(ctx, tx, person, false)
			if errors.Is(err, peoplechats.ErrNoKey) {
				done = true
				_, err := tx.Exec(ctx, `INSERT INTO people_search_ready (person_id, done_at) VALUES ($1, now())
					ON CONFLICT (person_id) DO UPDATE SET done_at = COALESCE(people_search_ready.done_at, now())`, person)
				return err
			}
			if err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT id, conversation_id, sealed, created_at FROM people_messages
				WHERE person_id = $1 AND sealed IS NOT NULL AND deleted_at IS NULL AND (created_at, id) > ($2, $3)
				ORDER BY created_at, id LIMIT $4 FOR SHARE`, person, after, afterID, peopleSearchBackfill)
			if err != nil {
				return err
			}
			type kept struct {
				id, conv uuid.UUID
				sealed   []byte
				at       time.Time
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (kept, error) {
				var k kept
				return k, r.Scan(&k.id, &k.conv, &k.sealed, &k.at)
			})
			if err != nil {
				return err
			}
			for _, k := range list {
				v, err := openSealed(key, person, k.id, k.sealed)
				if err != nil {
					continue // one that does not open is not searched
				}
				if err := indexPeopleMessageTx(ctx, tx, key, person, k.conv, k.id, v); err != nil {
					return err
				}
			}
			at, id := after, afterID
			if len(list) > 0 {
				at, id = list[len(list)-1].at, list[len(list)-1].id
			}
			var doneAt *time.Time
			if len(list) < peopleSearchBackfill {
				done = true
				now := time.Now()
				doneAt = &now
			}
			_, err = tx.Exec(ctx, `INSERT INTO people_search_ready (person_id, done_at, through_at, through_id) VALUES ($1, $2, $3, $4)
				ON CONFLICT (person_id) DO UPDATE SET done_at = COALESCE(people_search_ready.done_at, EXCLUDED.done_at),
				through_at = EXCLUDED.through_at, through_id = EXCLUDED.through_id`, person, doneAt, at, id)
			if err == nil {
				after, afterID = at, id
			}
			return err
		})
		if err != nil || done {
			return err
		}
	}
}

// peopleFound is one message of a search, or one starred message, with
// its chat.
type peopleFound struct {
	ChatID uuid.UUID `json:"chat_id"`
	// Chat is the chat as GET /v1/me/people shows it (its name, the other
	// person's card, whether it is a request).
	Chat    peopleChat    `json:"chat"`
	Message peopleMessage `json:"message"`
	// Snippet: a little of the message around what was searched for
	// (search only).
	Snippet string `json:"snippet,omitempty"`
	// StarredAt: when it was starred (starred only).
	StarredAt *time.Time `json:"starred_at,omitempty"`
}

// foundRow is a message read for a search or the starred list.
type foundRow struct {
	row  peopleRow
	chat uuid.UUID
	at   time.Time // starred_at for the starred list
}

// foundTx turns rows into what the person sees: each message opened and
// shown as their own copy shows it (read only while read ticks go), with
// its chat; snippet makes a search's snippet, and keep says whether the
// opened message is kept (a search checks its words).
func (s *Server) foundTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, key []byte, rows []foundRow,
	keep func(peopleSealed) (string, bool), limit int) ([]peopleFound, bool, error) {
	out := []peopleFound{}
	chats := map[uuid.UUID]*peopleChat{}
	ticks := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, f := range rows {
		if _, ok := chats[f.chat]; !ok {
			chats[f.chat] = nil
			ids = append(ids, f.chat)
		}
	}
	if len(ids) > 0 {
		r, err := tx.Query(ctx, `SELECT `+peopleCopyCols+peopleCopyFrom+` WHERE c.person_id = $1 AND c.id = ANY ($2)`, person, ids)
		if err != nil {
			return nil, false, err
		}
		copies, err := pgx.CollectRows(r, func(r pgx.CollectableRow) (peopleCopy, error) { return scanPeopleCopy(r) })
		if err != nil {
			return nil, false, err
		}
		views, err := s.peopleViewsTx(ctx, tx, person, copies)
		if err != nil {
			return nil, false, err
		}
		for i := range views {
			chats[views[i].ID] = &views[i]
		}
		for _, c := range copies {
			t, err := s.readTicksTx(ctx, tx, person, c.Peer)
			if err != nil {
				return nil, false, err
			}
			ticks[c.ID] = t
		}
	}
	more := false
	for _, f := range rows {
		ch := chats[f.chat]
		if ch == nil {
			continue
		}
		m, v := f.row.view(key, person)
		snippet, ok := keep(v)
		if !ok {
			continue
		}
		if len(out) == limit {
			more = true
			break
		}
		item := peopleFound{ChatID: f.chat, Chat: *ch, Message: hideRead(m, ticks[f.chat]), Snippet: snippet}
		if !f.at.IsZero() {
			at := f.at
			item.StarredAt = &at
		}
		out = append(out, item)
	}
	return out, more, nil
}

// searchPeople is GET /v1/me/people/search?q=&chat=&before=: the person's
// own people chats searched (§6.2), the newest first, peopleSearchPage at
// a time (more, and next for ?before=). A message is found when each word
// of q begins one of its words (accents and case aside), in its words, a
// file's name, a card's title, business or what for, or a place's label.
// Never another person's copy, a chat the person deleted, or a message
// deleted for them or for everyone.
func (s *Server) searchPeople(w http.ResponseWriter, r *http.Request) {
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
	if c := r.URL.Query().Get("chat"); c != "" {
		id, err := uuid.Parse(c)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "chat is a chat's id")
			return
		}
		only = &id
	}
	if !s.limiter("people-search:"+person.String(), 100*time.Millisecond, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Searching very fast. Wait a moment and try again.")
		return
	}
	if err := s.searchReady(ctx, person); err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []peopleFound{}
	more := false
	var next *listCursor
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if errors.Is(err, peoplechats.ErrNoKey) {
			return nil // nothing kept, nothing found
		}
		if err != nil {
			return err
		}
		words, toks := peoplechats.Query(peoplechats.SearchKey(key, person), q)
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
			sql := `SELECT ` + peopleMessageCols + `, chat_id FROM people_messages
				WHERE person_id = $1 AND deleted_at IS NULL AND sealed IS NOT NULL
				  AND chat_id IN (SELECT id FROM people_chats WHERE person_id = $1 AND hidden_at IS NULL AND ($4::uuid IS NULL OR id = $4))
				  AND id IN (SELECT message_id FROM people_search WHERE person_id = $1 AND token = ANY ($2) GROUP BY message_id HAVING count(*) = $3)`
			args := []any{person, toks, len(toks), only}
			if cur != nil {
				sql += ` AND (created_at, id) < ($5, $6)`
				args = append(args, cur.at, cur.id)
			}
			rows, err := tx.Query(ctx, sql+` ORDER BY created_at DESC, id DESC LIMIT `+strconv.Itoa(peopleSearchScan), args...)
			if err != nil {
				return err
			}
			var list []foundRow
			list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (foundRow, error) {
				var f foundRow
				row, err := scanPeopleRow(r, &f.chat)
				f.row = row
				return f, err
			})
			if err != nil {
				return err
			}
			found, full, err := s.foundTx(ctx, tx, person, key, list, keep, peopleSearchPage-len(out))
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
				return nil // nothing more to read
			}
			last := list[len(list)-1].row
			cur = &listCursor{last.CreatedAt, last.ID}
			if len(out) == peopleSearchPage {
				more, next = true, cur
				return nil
			}
		}
		// Rounds spent: the client may ask on from where this stopped.
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

// starredPeople is GET /v1/me/people/starred?before=: the person's starred
// messages across their people chats (§7), the most recently starred
// first, peopleStarredPage at a time (more, and next for ?before=), each
// with its chat. Not those of a chat they deleted.
func (s *Server) starredPeople(w http.ResponseWriter, r *http.Request) {
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
	out := []peopleFound{}
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		sql := `SELECT ` + peopleMessageCols + `, chat_id, starred_at FROM people_messages
			WHERE person_id = $1 AND starred_at IS NOT NULL AND deleted_at IS NULL
			  AND chat_id IN (SELECT id FROM people_chats WHERE person_id = $1 AND hidden_at IS NULL)`
		args := []any{person}
		if before != nil {
			sql += ` AND (starred_at, id) < ($2, $3)`
			args = append(args, before.at, before.id)
		}
		rows, err := tx.Query(ctx, sql+` ORDER BY starred_at DESC, id DESC LIMIT `+strconv.Itoa(peopleStarredPage+1), args...)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (foundRow, error) {
			var f foundRow
			row, err := scanPeopleRow(r, &f.chat, &f.at)
			f.row = row
			return f, err
		})
		if err != nil || len(list) == 0 {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		out, more, err = s.foundTx(ctx, tx, person, key, list, func(peopleSealed) (string, bool) { return "", true }, peopleStarredPage)
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

// typingPeople is POST /v1/me/people/{id}/typing ({typing?: bool}, true
// when left out): the person is typing in this chat (or stopped). It is
// passed to the other person's live connection, for peopleTypingFor,
// only within an accepted chat: never while either copy is a request,
// never to or from a block either way, never to a deleted or suspended
// account. The answer is 204 whatever became of it, so nothing tells the
// person whether the other blocked them; said more often than every
// peopleTypingEvery, it changes nothing.
func (s *Server) typingPeople(w http.ResponseWriter, r *http.Request) {
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
	chat := chi.URLParam(r, "id")
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chat, false)
		if err != nil {
			return err
		}
		if on && !s.limiter("people-typing:"+person.String()+":"+c.ID.String(), peopleTypingEvery, 1).Allow() {
			return nil
		}
		if c.Peer == nil || c.RequestAt != nil || c.AcceptedAt == nil || c.HiddenAt != nil || c.Blocked {
			return nil
		}
		// Nothing from or to a suspended or deleted account, with the same
		// 204.
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
		var theirs uuid.UUID
		var shut bool
		err = tx.QueryRow(ctx, `SELECT id, request_at IS NOT NULL OR hidden_at IS NOT NULL OR accepted_at IS NULL FROM people_chats WHERE person_id = $1 AND conversation_id = $2`,
			peer, c.Conversation).Scan(&theirs, &shut)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && shut) {
			return nil
		}
		if err != nil {
			return err
		}
		if !on {
			_, err := tx.Exec(ctx, `DELETE FROM people_typing WHERE person_id = $1 AND chat_id = $2`, peer, theirs)
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO people_typing (person_id, chat_id, conversation_id, until) VALUES ($1, $2, $3, now() + $4::interval)
			ON CONFLICT (person_id, chat_id) DO UPDATE SET until = EXCLUDED.until`, peer, theirs, c.Conversation, peopleTypingFor.String())
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

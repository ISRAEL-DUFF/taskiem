package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Shared baskets on You's live connection (GET /v1/me/live; 5b C): a
// basket the person reaches that changed (a line, its holder, closed,
// reopened, placed, a share's card sent), and, every few seconds, each
// placed basket's order as it moves (its state, what was paid, its
// delivery: "after placing, the card follows the order live"). Event:
//
//	basket   {"basket": Basket}
//
// sent when what the person sees of it changed; never a basket they no
// longer reach (they left the group, say).

const (
	basketFollowEvery = 5 * time.Second
	basketFollowMax   = 20
)

// basketLive is one connection's place in the person's baskets.
type basketLive struct {
	win      liveWindow
	seen     map[liveSeen]struct{}
	sent     map[uuid.UUID]string
	followAt time.Time
	// movedSince: placed baskets whose order moved after this are read
	// again (taskiem_baskets_moved).
	movedSince time.Time
	// placed are the placed baskets this connection follows: learnt once
	// when it starts (those of the last 14 days), then from the baskets it
	// sends. None: the five-second look is never made (a person with no
	// placed basket costs the database nothing every five seconds).
	placed map[uuid.UUID]struct{}
	learnt bool
}

func newBasketLive(since time.Time) *basketLive {
	return &basketLive{win: liveWindow{since}, seen: map[liveSeen]struct{}{}, sent: map[uuid.UUID]string{}, movedSince: since, placed: map[uuid.UUID]struct{}{}}
}

// idsTx are the baskets to look at again, read in the person's own
// transaction by their memberships (taskiem_baskets_changed: only those
// they reach, without asking of every basket changed on the platform).
func (bl *basketLive) idsTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, bool, error) {
	rows, err := tx.Query(ctx, `SELECT id, changed_at FROM taskiem_baskets_changed($1, $2)`, bl.win.since, liveBatch)
	if err != nil {
		return nil, false, err
	}
	type changed struct {
		id uuid.UUID
		at time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (changed, error) {
		var c changed
		return c, r.Scan(&c.id, &c.at)
	})
	if err != nil {
		return nil, false, err
	}
	var ids []uuid.UUID
	var times []time.Time
	for _, c := range list {
		times = append(times, c.at)
		k := liveSeen{c.id, c.at.UnixMicro()}
		if _, done := bl.seen[k]; done {
			continue
		}
		bl.seen[k] = struct{}{}
		ids = append(ids, c.id)
	}
	more := bl.win.slide(times, liveBatch)
	for k := range bl.seen {
		if time.UnixMicro(k.at).Before(bl.win.since) {
			delete(bl.seen, k)
		}
	}
	if !bl.learnt {
		// Once: the placed baskets to follow (not sent now: the client
		// reads what it shows).
		bl.learnt = true
		rows, err := tx.Query(ctx, `SELECT id FROM taskiem_baskets_moved('-infinity', $1)`, basketFollowMax)
		if err != nil {
			return nil, false, err
		}
		known, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, false, err
		}
		for _, id := range known {
			bl.placed[id] = struct{}{}
		}
		bl.followAt = time.Now()
	}
	if len(bl.placed) > 0 && time.Since(bl.followAt) >= basketFollowEvery {
		// Only placed baskets whose order moved since the last look (a
		// second's overlap: what was sent is compared before sending).
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
			return nil, false, err
		}
		since := bl.movedSince.Add(-time.Second)
		bl.followAt, bl.movedSince = time.Now(), now
		rows, err := tx.Query(ctx, `SELECT id FROM taskiem_baskets_moved($1, $2)`, since, basketFollowMax)
		if err != nil {
			return nil, false, err
		}
		follow, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, false, err
		}
		ids = append(ids, follow...)
	}
	return ids, more, nil
}

// basketEvents are the basket events for these baskets: each read as the
// person sees it now, sent when it differs from what was sent last.
func (s *Server) basketEvents(ctx context.Context, person uuid.UUID, bl *basketLive, ids []uuid.UUID) []liveEvent {
	var out []liveEvent
	done := map[uuid.UUID]bool{}
	for _, id := range ids {
		if done[id] {
			continue
		}
		done[id] = true
		v, err := s.basketView(ctx, person, id)
		if errors.Is(err, errDoorNone) {
			delete(bl.sent, id)
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Warn("live connection: a basket", "basket", id, "err", err)
			}
			continue
		}
		if v.State == "placed" {
			bl.placed[id] = struct{}{}
		}
		raw := mustJSON(map[string]any{"basket": v})
		if bl.sent[id] == string(raw) {
			continue
		}
		bl.sent[id] = string(raw)
		out = append(out, liveEvent{0, "basket", raw})
	}
	return out
}

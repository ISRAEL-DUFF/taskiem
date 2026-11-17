package peoplechats

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/people"
)

// Between is, for the person in tx's scope and up to 500 others, which of
// them a block stands between (either blocked the other) and which they
// have a people chat with that is no longer a request (either copy
// accepted): taskiem_people_between (00476). Nothing else of the others.
func Between(ctx context.Context, tx pgx.Tx, others []uuid.UUID) (blocked, chatting map[uuid.UUID]bool, err error) {
	blocked, chatting = map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	if len(others) == 0 {
		return blocked, chatting, nil
	}
	rows, err := tx.Query(ctx, `SELECT person_id, blocked, chatting FROM taskiem_people_between($1)`, others)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var b, c bool
		if err := rows.Scan(&id, &b, &c); err != nil {
			return nil, nil, err
		}
		if b {
			blocked[id] = true
		}
		if c {
			chatting[id] = true
		}
	}
	return blocked, chatting, rows.Err()
}

// JoinPeople gives finding people (engine/people) what the people chats
// know: whether a block stands between two people (Blocked, BlockedSet)
// and whether they chat (Chatting, a chat no longer a request). Each is
// answered in the scope of the person the request is for (a, or viewer).
func JoinPeople(p *people.People) *people.People {
	if p == nil || p.Pool == nil {
		return p
	}
	one := func(ctx context.Context, a, b uuid.UUID) (blocked, chatting bool, err error) {
		if a == b {
			return false, false, nil
		}
		err = db.InPersonTx(ctx, p.Pool, a, func(tx pgx.Tx) error {
			bs, cs, err := Between(ctx, tx, []uuid.UUID{b})
			blocked, chatting = bs[b], cs[b]
			return err
		})
		return blocked, chatting, err
	}
	p.Blocked = func(ctx context.Context, a, b uuid.UUID) (bool, error) {
		blocked, _, err := one(ctx, a, b)
		return blocked, err
	}
	p.Chatting = func(ctx context.Context, a, b uuid.UUID) (bool, error) {
		_, chatting, err := one(ctx, a, b)
		return chatting, err
	}
	p.BlockedSet = func(ctx context.Context, tx pgx.Tx, _ uuid.UUID, others []uuid.UUID) (map[uuid.UUID]bool, error) {
		out := map[uuid.UUID]bool{}
		for i := 0; i < len(others); i += 500 {
			b, _, err := Between(ctx, tx, others[i:min(i+500, len(others))])
			if err != nil {
				return nil, err
			}
			for id := range b {
				out[id] = true
			}
		}
		return out, nil
	}
	return p
}

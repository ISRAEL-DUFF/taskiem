package people

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// OnlineFor is how long a person counts as online after the app last
// said it is open (its heartbeat, every 30 seconds or so while in front)
// or after any other request of theirs.
const OnlineFor = 75 * time.Second

// Touch records that the person is here now: last seen now, online for
// OnlineFor. Called by the app's heartbeat, by the person's requests, and
// by the people chats' live connection (stream A).
func (p *People) Touch(ctx context.Context, person uuid.UUID) error {
	return db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		now := p.now()
		_, err := tx.Exec(ctx, `INSERT INTO person_presence (person_id, last_seen_at, online_until) VALUES ($1, $2, $3)
			ON CONFLICT (person_id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, online_until = EXCLUDED.online_until`,
			person, now, now.Add(OnlineFor))
		return err
	})
}

// Away records that the person left (the app went to the background, or
// the live connection closed): last seen now, no longer online.
func (p *People) Away(ctx context.Context, person uuid.UUID) error {
	return db.InPersonTx(ctx, p.Pool, person, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO person_presence (person_id, last_seen_at, online_until) VALUES ($1, $2, NULL)
			ON CONFLICT (person_id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, online_until = NULL`, person, p.now())
		return err
	})
}

// Presence is what viewer may see of owner's last seen and online: both
// nil when hidden (by owner's setting, by viewer's own, which hides it
// both ways, or by a block), and LastSeenAt nil when owner was never seen.
type Presence struct {
	LastSeenAt *time.Time `json:"last_seen_at"`
	Online     *bool      `json:"online"`
}

// PresenceOf is owner's last seen and online as viewer may see them.
func (p *People) PresenceOf(ctx context.Context, viewer, owner uuid.UUID) (Presence, error) {
	c, err := p.CardOf(ctx, viewer, owner)
	if err != nil {
		return Presence{}, err
	}
	return Presence{LastSeenAt: c.LastSeenAt, Online: c.Online}, nil
}

package riders

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A rider's business says whether it is free, busy or off (docs/spec/
// chat-riders.md, phase 3): the same presence the rider's words write,
// written for the rider rows linked to the business in scope, so every
// business that uses the rider reads it as before.

// Availability is the rider's business's own view of its state.
type Availability struct {
	Presence
	// Businesses is how many businesses use this business as a rider;
	// 0 means nothing is written or read yet.
	Businesses int `json:"businesses"`
}

// AvailabilityTx reads the business in scope's availability.
func AvailabilityTx(ctx context.Context, tx pgx.Tx) (Availability, error) {
	var out Availability
	var state *string
	var at, since *time.Time
	if err := tx.QueryRow(ctx, `SELECT said, until, said_at, open, open_since, businesses FROM taskiem_rider_availability()`).
		Scan(&state, &out.Until, &at, &out.Open, &since, &out.Businesses); err != nil {
		return out, err
	}
	switch {
	case state != nil && *state == "off":
		out.State, out.Since = "off", at
	case out.Open > 0:
		out.State, out.Since, out.Until = "busy", since, nil
	case state != nil:
		out.State, out.Since = *state, at
	default:
		out.State = "free"
	}
	return out, nil
}

// SayOwnTx records the business in scope's word (on, off, or busy till a
// time) for every business that uses it as a rider, and answers how many
// that is (0: nothing written).
func SayOwnTx(ctx context.Context, tx pgx.Tx, s Said) (int, error) {
	if s.State != "on" && s.State != "off" && s.State != "busy" {
		return 0, fmt.Errorf("%w: on, off, or busy till a time", ErrInvalid)
	}
	var until *time.Time
	if s.State == "busy" {
		if s.Until.IsZero() {
			return 0, fmt.Errorf("%w: busy needs a time", ErrInvalid)
		}
		until = &s.Until
	}
	var n int
	err := tx.QueryRow(ctx, `SELECT taskiem_rider_availability_say($1, $2)`, s.State, until).Scan(&n)
	return n, err
}

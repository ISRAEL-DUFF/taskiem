package publicweb

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrReferenceTaken: the reference chosen for a new submission is already
// in use. The caller picks another and seals again, since the payload is
// bound to its reference.
var ErrReferenceTaken = errors.New("publicweb: the reference is taken")

// Submit records a sealed submission. ref is the reference recorded for
// idem, which is the first one made for it when the submission is a
// repeat; ok is false when the storefront is not published or the form is
// not enabled.
type Submit func(ctx context.Context, storefront uuid.UUID, kind, reference string, idem, statusHash, payload []byte) (ref string, ok bool, err error)

// DBSubmit records submissions with taskiem_public_submit.
func DBSubmit(pool *pgxpool.Pool) Submit {
	return func(ctx context.Context, storefront uuid.UUID, kind, reference string, idem, statusHash, payload []byte) (string, bool, error) {
		var ref *string
		err := pool.QueryRow(ctx, `SELECT taskiem_public_submit($1, $2, $3, $4, $5, $6)`, storefront, kind, reference, idem, statusHash, payload).Scan(&ref)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "storefront_inbox_reference_key" {
			return "", false, ErrReferenceTaken
		}
		if err != nil {
			return "", false, err
		}
		if ref == nil {
			return "", false, nil
		}
		return *ref, true, nil
	}
}

// OrderState is what a customer's status page may show: nothing personal.
type OrderState struct {
	Status     string // processing while the submission is in the inbox
	Kind       string
	TotalKobo  int64
	Payment    string
	PaymentURL string
	Storefront uuid.UUID
}

// OrderStatus reads a submission's state by its reference and the hash of
// its status key; ok is false when either is wrong.
type OrderStatus func(ctx context.Context, reference string, statusHash []byte) (state OrderState, ok bool, err error)

// DBOrderStatus reads it with taskiem_public_order_status.
func DBOrderStatus(pool *pgxpool.Pool) OrderStatus {
	return func(ctx context.Context, reference string, statusHash []byte) (OrderState, bool, error) {
		var o OrderState
		var url *string
		err := pool.QueryRow(ctx, `SELECT status, kind, total_kobo, payment, payment_url, storefront_id FROM taskiem_public_order_status($1, $2)`, reference, statusHash).
			Scan(&o.Status, &o.Kind, &o.TotalKobo, &o.Payment, &url, &o.Storefront)
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderState{}, false, nil
		}
		if err != nil {
			return OrderState{}, false, err
		}
		if url != nil {
			o.PaymentURL = *url
		}
		return o, true, nil
	}
}

package storefront

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The page's trust states (storefronts spec 7.4; docs/moderation.md):
// in_review waits for a person before its first publish goes live;
// suspended is taken down by an operator, or by reports, until restored.

// ErrSuspended refuses changing a suspended page's state.
var ErrSuspended = errors.New("storefront: the page is suspended; reply to Taskiem's message to appeal")

// ErrNotInReview refuses deciding a page that is not waiting.
var ErrNotInReview = errors.New("storefront: the page is not waiting for review")

// Approve puts a page waiting for review live.
func Approve(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Storefront, error) {
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = 'published', review_note = NULL, updated_at = now()
		WHERE tenant_id = $1 AND status = 'in_review' RETURNING `+cols, tenant))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, ErrNotInReview
	}
	return s, err
}

// Reject turns a page waiting for review back into a draft, saying why.
func Reject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, note string) (Storefront, error) {
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = 'draft', review_note = $2, updated_at = now()
		WHERE tenant_id = $1 AND status = 'in_review' RETURNING `+cols, tenant, strings.TrimSpace(note)))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, ErrNotInReview
	}
	return s, err
}

// Suspend takes a live page down: it says it is unavailable and its forms
// take nothing. ok is false when it was not live (or already suspended).
func Suspend(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reason, by string) (Storefront, bool, error) {
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status_before_suspension = status, status = 'suspended', suspended_reason = $2,
		suspended_at = now(), suspended_by = $3, updated_at = now()
		WHERE tenant_id = $1 AND status IN ('published', 'closed') RETURNING `+cols, tenant, strings.TrimSpace(reason), by))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, false, nil
	}
	return s, err == nil, err
}

// Restore lifts a suspension: the page is as it was (published or closed).
func Restore(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Storefront, error) {
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = status_before_suspension, status_before_suspension = NULL, suspended_reason = NULL,
		suspended_at = NULL, suspended_by = NULL, updated_at = now()
		WHERE tenant_id = $1 AND status = 'suspended' RETURNING `+cols, tenant))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, ErrNotPublished
	}
	return s, err
}

// SetOrdersPaused records why orders and bookings are paused ("" lifts
// it). It reports whether that changed anything.
func SetOrdersPaused(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, why string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE storefronts SET orders_paused = NULLIF($2, '') WHERE tenant_id = $1 AND orders_paused IS DISTINCT FROM NULLIF($2, '')`,
		tenant, why)
	return tag.RowsAffected() > 0, err
}

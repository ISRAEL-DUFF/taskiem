package publicweb

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A receipt's verification page, GET /r/{token} on the form host
// (docs/spec/schools.md 9; docs/schools.md#receipts): whoever scans the QR
// on a school's receipt sees Valid or Voided, the school, the amount and
// the date, and nothing else. The token is the receipt's own, unrelated
// to its number: a number, or a guessed token, finds nothing, and the
// page then falls back to the form link that shares the path.

// ReceiptCheck is what the verification page shows.
type ReceiptCheck struct {
	Tenant     uuid.UUID
	Business   string
	AmountKobo int64
	PaidAt     time.Time
	Voided     bool
}

// Receipts reads a receipt by its token's hash; ok is false for an
// unknown token or an inactive business.
type Receipts func(ctx context.Context, tokenHash []byte) (ReceiptCheck, bool, error)

// DBReceipts reads receipts with taskiem_public_school_receipt and the
// school's name with taskiem_public_brand.
func DBReceipts(pool *pgxpool.Pool) Receipts {
	return func(ctx context.Context, tokenHash []byte) (ReceiptCheck, bool, error) {
		var c ReceiptCheck
		var business *string
		err := pool.QueryRow(ctx, `SELECT r.tenant_id, r.amount_kobo, r.paid_at, r.voided, taskiem_public_brand(r.tenant_id)->>'name'
			FROM taskiem_public_school_receipt($1) r`, tokenHash).Scan(&c.Tenant, &c.AmountKobo, &c.PaidAt, &c.Voided, &business)
		if errors.Is(err, pgx.ErrNoRows) {
			return ReceiptCheck{}, false, nil
		}
		if err != nil {
			return ReceiptCheck{}, false, err
		}
		if business != nil {
			c.Business = *business
		}
		return c, true, nil
	}
}

// serveReceipt serves /r/{token} when the token is a receipt's; it
// reports false, having written nothing, when it is not.
func (s *Server) serveReceipt(w http.ResponseWriter, r *http.Request, token string) bool {
	if s.Receipts == nil || !invoiceTokenRe.MatchString(token) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	c, ok, err := s.Receipts(r.Context(), invoicing.ReceiptTokenHash(token))
	if err != nil {
		s.tryLater(w, r, "receipt", err)
		return true
	}
	if !ok {
		return false
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	business := c.Business
	if business == "" {
		business = "the school"
	}
	lagos, _ := time.LoadLocation("Africa/Lagos")
	if lagos == nil {
		lagos = time.UTC
	}
	title, msg := "Valid", "This receipt from "+business+" is valid: "+invoicing.Naira(c.AmountKobo)+" paid on "+c.PaidAt.In(lagos).Format("2 January 2006")+"."
	if c.Voided {
		title, msg = "Voided", "This receipt from "+business+" was voided: it no longer stands for the "+invoicing.Naira(c.AmountKobo)+" paid on "+c.PaidAt.In(lagos).Format("2 January 2006")+"."
	}
	s.renderPrivate(w, r, http.StatusOK, "formclosed.html", closedData{pageData: pageData{Title: title, Lang: "en", Look: s.lookFor(r.Context(), c.Tenant, business)},
		Business: business, Message: msg})
	return true
}

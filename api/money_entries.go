package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The money log, listed (the chat's Money tool; docs/spec/chat-five-more-flows.md,
// phase 2 additions): what came in and went out, newest first, what each
// counts towards, and whether it waits for a person to match it. A payer's
// details are sealed and never listed here; the Money pages reveal them.

// moneyEntry is one entry of the log, as the list shows it.
type moneyEntry struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	Direction   string    `json:"direction"`
	AmountKobo  *int64    `json:"amount_kobo,omitempty"`
	FeeKobo     *int64    `json:"fee_kobo,omitempty"`
	Status      string    `json:"status"`
	Channel     string    `json:"channel,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	When        time.Time `json:"when"`
	Description string    `json:"description,omitempty"`
	Vendor      string    `json:"vendor,omitempty"`
	Category    string    `json:"category,omitempty"`
	// LeftKobo is what is left of money in to count towards an invoice or
	// order; Towards names what it counts towards now ("invoice INV-0104").
	LeftKobo *int64 `json:"left_kobo,omitempty"`
	Towards  string `json:"towards,omitempty"`
	// ToMatch: money matching left for a person (as /v1/money/unmatched).
	ToMatch bool `json:"to_match"`
}

// listMoneyEntries serves GET /v1/money/entries: the newest entries, at
// most limit (100 unless asked, 200 at most), before a time when paging.
func (s *Server) listMoneyEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	before := time.Now().Add(time.Hour)
	if b := r.URL.Query().Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "before is a time (RFC 3339)")
			return
		}
		before = t
	}
	tenant := principalFrom(r.Context()).TenantID
	var list []moneyEntry
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT e.id, e.kind, e.direction, e.amount_kobo, e.fee_kobo, e.status,
				COALESCE(e.channel, ''), COALESCE(e.reference, ''), COALESCE(e.occurred_at, e.recorded_at),
				COALESCE(e.description, ''), COALESCE(e.vendor, ''), COALESCE(e.category, ''),
				CASE WHEN e.direction = 'in' THEN taskiem_money_entry_left(e.id) END,
				COALESCE((SELECT string_agg(what, ', ' ORDER BY what) FROM (
					SELECT 'invoice ' || i.number AS what FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
						WHERE p.tenant_id = e.tenant_id AND p.entry_id = e.id AND p.undoes IS NULL
						  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
					UNION ALL
					SELECT 'order ' || o.reference FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
						WHERE p.tenant_id = e.tenant_id AND p.entry_id = e.id AND p.undoes IS NULL
						  AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)
					UNION ALL
					SELECT 'sale ' || x.reference FROM stock_sales x WHERE x.tenant_id = e.tenant_id AND x.entry_id = e.id) t), ''),
				EXISTS (SELECT 1 FROM money_match_state m WHERE m.entry_id = e.id AND m.state = 'unmatched')
				  AND (taskiem_money_entry_left(e.id) > 0 OR e.amount_kobo IS NULL AND taskiem_money_entry_counted(e.id) = 0)
				  AND NOT EXISTS (SELECT 1 FROM money_entry_customers c WHERE c.entry_id = e.id)
			FROM money_entries e
			WHERE e.tenant_id = $1 AND e.recorded_at < $2
			ORDER BY e.recorded_at DESC, e.id DESC LIMIT $3`, tenant, before, limit)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (moneyEntry, error) {
			var e moneyEntry
			return e, row.Scan(&e.ID, &e.Kind, &e.Direction, &e.AmountKobo, &e.FeeKobo, &e.Status, &e.Channel, &e.Reference, &e.When,
				&e.Description, &e.Vendor, &e.Category, &e.LeftKobo, &e.Towards, &e.ToMatch)
		})
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []moneyEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

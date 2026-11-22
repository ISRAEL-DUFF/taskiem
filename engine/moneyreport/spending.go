package moneyreport

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/spend"
)

// Spending by label (docs/spec/what-a-business-buys.md §5.4): a period's
// spending, grouped by the label each entry carries, found by its id
// when the report is read and followed to the label it was merged into,
// so a rename or a merge regroups past spending without touching the
// log. What a purchase's money bought is read from its order's lines:
//
//   - lines added to stock are Bought to sell or make with, counted as
//     stock bought always was;
//   - lines kept track of are Bought to keep: something the business has,
//     left out of profit;
//   - lines just recorded, and expenses entered by hand, are running
//     costs under their labels.
//
// Spending is each settled expense (kind expense) and each settled payout
// that paid a linked supplier's bill (its bill_payments row names the
// payout's reference). A purchase's expense is shared among its order's
// treatments under its label (spend.EntryPartsTx); a bill's payout among
// the bill's lines by label and treatment (spend.BillPartsTx), at the
// bill's prices.

// LabelSpend is one label's spending in a period; LabelID is nil for Not
// labelled.
type LabelSpend struct {
	LabelID *uuid.UUID `json:"label_id"`
	Name    string     `json:"name"`
	Kobo    int64      `json:"kobo"`
	// ToSellKobo is Bought to sell or make with, ToKeepKobo Bought to
	// keep, RunningKobo running costs; they add up to Kobo.
	ToSellKobo  int64 `json:"bought_to_sell_kobo"`
	ToKeepKobo  int64 `json:"bought_to_keep_kobo"`
	RunningKobo int64 `json:"running_costs_kobo"`
	// Entries is how many money-log entries are in it (an entry shared
	// across labels, a bill's payout, counts in each).
	Entries int `json:"entries"`
}

// Profit is a period's own money in less what running the business cost:
// fees, running costs and stock bought (Bought to sell or make with).
// Bought to keep is left out: it is something the business has.
type Profit struct {
	InKobo     int64 `json:"in_kobo"`
	FeesKobo   int64 `json:"fees_kobo"`
	CostsKobo  int64 `json:"costs_kobo"`
	ProfitKobo int64 `json:"profit_kobo"`
}

// Spending is a period's spending by label.
type Spending struct {
	From        time.Time    `json:"from"`
	To          time.Time    `json:"to"`
	TotalKobo   int64        `json:"total_kobo"`
	ToSellKobo  int64        `json:"bought_to_sell_kobo"`
	ToKeepKobo  int64        `json:"bought_to_keep_kobo"`
	RunningKobo int64        `json:"running_costs_kobo"`
	Labels      []LabelSpend `json:"labels"`
	Profit      Profit       `json:"profit"`
}

// SpendingOf sums up the tenant's spending in [from, to), by label.
func SpendingOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) (Spending, error) {
	out := Spending{From: from, To: to, Labels: []LabelSpend{}}
	// The labels kept, by every label's id.
	kept := map[uuid.UUID]uuid.UUID{}
	names := map[uuid.UUID]string{}
	rows, err := tx.Query(ctx, `SELECT l.id, k.id, k.name FROM spend_labels l JOIN spend_labels k ON k.tenant_id = l.tenant_id AND k.id = COALESCE(l.merged_into, l.id)
		WHERE l.tenant_id = $1`, tenant)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, k uuid.UUID
		var name string
		if err := rows.Scan(&id, &k, &name); err != nil {
			rows.Close()
			return out, err
		}
		kept[id], names[k] = k, name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	// Each movement's first entry carries the label; a later status
	// (a reversal) says whether it still counts.
	type move struct {
		entry  uuid.UUID
		kind   string
		ref    string
		amount int64
		label  *uuid.UUID
	}
	rows, err = tx.Query(ctx, `SELECT m.movement, m.kind, m.reference, m.amount, (SELECT f.label_id FROM money_entries f WHERE f.tenant_id = $1 AND f.id = m.movement)
		FROM (`+latest+`) m WHERE m.currency = 'NGN' AND m.direction = 'out' AND m.status = 'settled'
		AND (m.kind = 'expense' OR (m.kind = 'payout' AND EXISTS (SELECT 1 FROM bill_payments p WHERE p.tenant_id = $1 AND p.payout_ref = m.reference)))
		ORDER BY m.first_at, m.movement`, tenant, from, to)
	if err != nil {
		return out, err
	}
	moves, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (move, error) {
		var x move
		err := r.Scan(&x.entry, &x.kind, &x.ref, &x.amount, &x.label)
		return x, err
	})
	if err != nil {
		return out, err
	}
	at := map[uuid.UUID]int{}
	none := -1
	tally := func(p spend.Part) int {
		var label *uuid.UUID
		if p.LabelID != nil {
			if k, ok := kept[*p.LabelID]; ok {
				label = &k
			}
		}
		i, ok := none, none >= 0
		if label != nil {
			i, ok = at[*label]
		}
		if !ok {
			i = len(out.Labels)
			ls := LabelSpend{LabelID: label, Name: spend.NotLabelled}
			if label != nil {
				ls.Name = names[*label]
				at[*label] = i
			} else {
				none = i
			}
			out.Labels = append(out.Labels, ls)
		}
		ls := &out.Labels[i]
		ls.Kobo += p.Kobo
		out.TotalKobo += p.Kobo
		switch p.Treatment {
		case spend.Stock:
			ls.ToSellKobo += p.Kobo
			out.ToSellKobo += p.Kobo
		case spend.Track:
			ls.ToKeepKobo += p.Kobo
			out.ToKeepKobo += p.Kobo
		default:
			ls.RunningKobo += p.Kobo
			out.RunningKobo += p.Kobo
		}
		return i
	}
	for _, m := range moves {
		parts := []spend.Part{{LabelID: m.label, Treatment: spend.Record, Kobo: m.amount}}
		switch m.kind {
		case "expense":
			if p, ok, err := spend.EntryPartsTx(ctx, tx, tenant, m.entry, m.label, m.amount); err != nil {
				return out, err
			} else if ok {
				parts = p
			}
		case "payout":
			// A payout may pay more than one payment of a bill only in
			// name: each bill_payments row has its own payout.
			var bill uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT bill_id FROM bill_payments WHERE tenant_id = $1 AND payout_ref = $2 ORDER BY created_at LIMIT 1`, tenant, m.ref).Scan(&bill); err != nil {
				return out, err
			}
			weights, err := spend.BillPartsTx(ctx, tx, tenant, bill)
			if err != nil {
				return out, err
			}
			if len(weights) > 0 {
				parts = spend.Split(m.amount, weights)
			}
		}
		counted := map[int]bool{}
		for _, p := range parts {
			if p.Kobo == 0 {
				continue
			}
			if i := tally(p); !counted[i] {
				counted[i] = true
				out.Labels[i].Entries++
			}
		}
	}
	sort.SliceStable(out.Labels, func(i, j int) bool { return out.Labels[i].Kobo > out.Labels[j].Kobo })
	s, err := Summarise(ctx, tx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.Profit = Profit{InKobo: s.OwnInSettled, FeesKobo: s.FeesKobo, CostsKobo: out.TotalKobo - out.ToKeepKobo}
	out.Profit.ProfitKobo = out.Profit.InKobo - out.Profit.FeesKobo - out.Profit.CostsKobo
	return out, nil
}

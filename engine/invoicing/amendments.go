package invoicing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Amendments (docs/invoices.md#amending-a-sent-invoice; docs/spec/
// schools.md 5.4): a sent invoice is never edited, it is amended. An
// amendment is a recorded change with a reason (a line added, an earlier
// line taken out, a discount) and becomes a line of its own at the end of
// the invoice, so the customer's page, matching, the leaks and the exports
// see it as they see every line. The invoice's total is then the sum of
// its lines, its status and what was paid follow from it and its matched
// payments as always: an invoice paid in full whose total rises is
// part-paid again, and nothing sets paid. The database keeps the rules
// (00212): lines only added, each a recorded amendment, once, and a total
// never below what was paid.

// Kinds of amendment.
const (
	AmendAdd      = "add"
	AmendRemove   = "remove"
	AmendDiscount = "discount"
)

// MaxAmendedLines is the most lines an invoice has with its amendments.
const MaxAmendedLines = 100

// AmendmentIn is an amendment to make.
type AmendmentIn struct {
	Kind string `json:"kind"`
	// Description names an added line or a discount ("Lost textbook",
	// "Hardship discount").
	Description string `json:"description,omitempty"`
	// AmountKobo is what an added line or a discount is for.
	AmountKobo int64 `json:"amount_kobo,omitempty"`
	// PassThroughFor makes an added line one collected for someone else.
	PassThroughFor string `json:"pass_through_for,omitempty"`
	// Line is the position (from 0) of the line a removal takes out.
	Line *int `json:"line,omitempty"`
	// Reason says why; required.
	Reason string `json:"reason"`
}

// Amendment is an amendment made.
type Amendment struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	Line       Line      `json:"line"`
	AmountKobo int64     `json:"amount_kobo"`
	Removes    *int      `json:"removes,omitempty"`
	Reason     string    `json:"reason"`
	By         string    `json:"by"`
	CreatedAt  time.Time `json:"created_at"`
}

// Amended is what amending an invoice did. Pass the invoice to Paid once
// the transaction commits when Became.
type Amended struct {
	Invoice   Invoice   `json:"invoice"`
	Amendment Amendment `json:"amendment"`
	// Became is true when a discount made the invoice paid.
	Became bool `json:"-"`
}

// Plan checks an amendment against the invoice and says the line it adds,
// without recording it: what Amend records, and what a caller with rules
// of its own (a school's discount threshold) looks at first.
func (in AmendmentIn) Plan(x Invoice) (Line, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	in.Description = strings.Join(strings.Fields(in.Description), " ")
	in.PassThroughFor = strings.Join(strings.Fields(in.PassThroughFor), " ")
	switch x.Status {
	case "draft":
		return Line{}, fmt.Errorf("%w: invoice %s is a draft: change it instead of amending it", ErrState, x.Number)
	case "void":
		return Line{}, fmt.Errorf("%w: invoice %s is void", ErrState, x.Number)
	}
	if in.Reason == "" || len(in.Reason) > 300 {
		return Line{}, invalid("say why the invoice is amended (up to 300 characters)")
	}
	if len(in.Description) > 200 || len(in.PassThroughFor) > 60 {
		return Line{}, invalid("a description has up to 200 characters and pass_through_for up to 60")
	}
	if len(x.Lines) >= MaxAmendedLines {
		return Line{}, invalid("an invoice has at most %d lines with its amendments", MaxAmendedLines)
	}
	var l Line
	switch in.Kind {
	case AmendAdd:
		if in.Description == "" || in.AmountKobo < 100 || in.AmountKobo > MaxTotalKobo || in.Line != nil {
			return Line{}, invalid("an added line needs a description and an amount of at least ₦1")
		}
		l = Line{Description: in.Description, Quantity: 1, UnitKobo: in.AmountKobo, PassThroughFor: in.PassThroughFor}
	case AmendDiscount:
		if in.AmountKobo < 1 || in.AmountKobo > MaxTotalKobo || in.Line != nil || in.PassThroughFor != "" {
			return Line{}, invalid("a discount needs an amount, and is never collected for someone else")
		}
		if in.Description == "" {
			in.Description = "Discount"
		}
		l = Line{Description: in.Description, Quantity: 1, UnitKobo: -in.AmountKobo, Kind: LineDiscount}
	case AmendRemove:
		if in.Line == nil || *in.Line < 0 || *in.Line >= len(x.Lines) || in.AmountKobo != 0 || in.PassThroughFor != "" {
			return Line{}, invalid("a removal names the line to take out (line, from 0)")
		}
		old := x.Lines[*in.Line]
		if old.Kind != "" || old.AmountKobo() <= 0 {
			return Line{}, invalid("only an item can be taken out; a discount is taken back by adding a line")
		}
		for _, a := range x.Amendments {
			if a.Removes != nil && *a.Removes == *in.Line {
				return Line{}, fmt.Errorf("%w: line %d was taken out already", ErrState, *in.Line)
			}
		}
		desc := removedPrefix + old.Description
		if len(desc) > 200 {
			desc = desc[:200]
		}
		l = Line{Description: desc, Quantity: 1, UnitKobo: -old.AmountKobo(), PassThroughFor: old.PassThroughFor, Kind: LineRemoval}
	default:
		return Line{}, invalid("kind is add, remove or discount")
	}
	l.Reason = in.Reason
	total := x.TotalKobo + l.AmountKobo()
	switch {
	case total > MaxTotalKobo:
		return Line{}, invalid("the total would be more than ₦1,000,000,000")
	case total < x.PaidKobo:
		return Line{}, fmt.Errorf("%w: the total cannot go below what was paid, %s", ErrState, Naira(x.PaidKobo))
	case total < 100:
		return Line{}, fmt.Errorf("%w: the total must stay at least ₦1; void the invoice instead", ErrState)
	}
	if own, _ := PassThrough(append(append([]Line{}, x.Lines...), l)); own < 0 {
		return Line{}, fmt.Errorf("%w: a discount cannot be more than the business's own items; money collected for someone else is not discounted", ErrState)
	}
	return l, nil
}

const removedPrefix = "Taken out: "

// Amend records an amendment of a sent (or part-paid, overdue or paid)
// invoice and adds its line. A discount that makes the invoice paid
// queues the workflows that start on it, as a payment does.
func (s *Invoices) Amend(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, in AmendmentIn, by string) (Amended, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Amended{}, err
	}
	// One amendment at a time: the invoice's row until commit.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM customer_invoices WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, cur.ID); err != nil {
		return Amended{}, err
	}
	if cur, err = s.Get(ctx, tx, tenant, cur.ID.String()); err != nil {
		return Amended{}, err
	}
	l, err := in.Plan(cur)
	if err != nil {
		return Amended{}, err
	}
	id := uuid.New()
	l.Amendment = id.String()
	raw, _ := json.Marshal(l)
	var removes *int
	if in.Kind == AmendRemove {
		removes = in.Line
	}
	if _, err := tx.Exec(ctx, `INSERT INTO customer_invoice_amendments (id, tenant_id, invoice_id, kind, line, amount_kobo, removes, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, tenant, cur.ID, in.Kind, raw, l.AmountKobo(), removes, l.Reason, by); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return Amended{}, fmt.Errorf("%w: that line was taken out already", ErrState)
		}
		return Amended{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET lines = lines || jsonb_build_array($3::jsonb), total_kobo = total_kobo + $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, cur.ID, raw, l.AmountKobo()); err != nil {
		return Amended{}, stateErr(err)
	}
	if err := audit(ctx, tx, tenant, by, "invoice.amended", cur.Number, map[string]any{"kind": in.Kind, "amount_kobo": l.AmountKobo(),
		"description": l.Description, "reason": l.Reason, "pass_through_for": l.PassThroughFor, "amendment": id.String()}); err != nil {
		return Amended{}, err
	}
	x, err := s.Get(ctx, tx, tenant, cur.ID.String())
	if err != nil {
		return Amended{}, err
	}
	out := Amended{Invoice: x, Became: x.Status == "paid" && cur.Status != "paid"}
	for _, a := range x.Amendments {
		if a.ID == id {
			out.Amendment = a
		}
	}
	if out.Became {
		if err := queuePaidStarts(ctx, tx, tenant, x.ID); err != nil {
			return Amended{}, err
		}
	}
	return out, nil
}

func amendments(ctx context.Context, tx pgx.Tx, invoice uuid.UUID) ([]Amendment, error) {
	rows, err := tx.Query(ctx, `SELECT id, kind, line, amount_kobo, removes, reason, created_by, created_at FROM customer_invoice_amendments
		WHERE invoice_id = $1 ORDER BY created_at, id`, invoice)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Amendment, error) {
		var a Amendment
		var line []byte
		err := r.Scan(&a.ID, &a.Kind, &line, &a.AmountKobo, &a.Removes, &a.Reason, &a.By, &a.CreatedAt)
		_ = json.Unmarshal(line, &a.Line)
		return a, err
	})
	if out == nil {
		out = []Amendment{}
	}
	return out, err
}

func pgCode(err error) (string, string) {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pe.Code, pe.ConstraintName
	}
	return "", ""
}

// stateErr is the database refusing an amendment (a check of 00212) as
// ErrState with its words.
func stateErr(err error) error {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23514" {
		return fmt.Errorf("%w: %s", ErrState, pe.Message)
	}
	return err
}

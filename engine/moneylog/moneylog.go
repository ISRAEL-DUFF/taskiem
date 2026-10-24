// Package moneylog keeps a business's money log (decision 0045 point 6;
// docs/spec/invoices-and-money-log.md 7, T2; docs/money-log.md): one entry
// per movement of money on its connected rails, and per later status of
// one, written once and never changed.
//
// Entries are written to money_entries in Taskiem's database, where a
// unique key makes each movement one row however often it is heard of
// (pushed, polled, looked up), and copied from there to the business's
// kept data table money_log, where the owner reads, searches and exports
// them.
package moneylog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Entry is one row of the money log.
type Entry struct {
	ID         uuid.UUID  `json:"entry_id"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	RecordedAt time.Time  `json:"recorded_at"`
	Direction  string     `json:"direction"` // in or out
	// Kind: payment, account_credit, payout, refund, pos_sale, fee,
	// statement, cash, expense or correction.
	Kind       string `json:"kind"`
	AmountKobo *int64 `json:"amount_kobo,omitempty"`
	FeeKobo    *int64 `json:"fee_kobo,omitempty"`
	Currency   string `json:"currency"`
	// Status as the rail reported it then: settled, pending, failed or
	// reversed. A later status is a new entry naming this one.
	Status    string `json:"status"`
	Channel   string `json:"channel"` // paystack/main, upload, cash
	Reference string `json:"reference"`
	Event     string `json:"event,omitempty"`
	// PayerName and PayerAccount are personal data: sealed when written.
	PayerName    string `json:"payer_name,omitempty"`
	PayerAccount string `json:"payer_account,omitempty"`
	Source       string `json:"source"` // event, lookup, step, upload, entered
	Note         string `json:"note,omitempty"`
	// What a person typed or a statement said (T6): the line's
	// description, and an expense's vendor and category; EnteredBy is who
	// entered or uploaded it.
	Description string     `json:"description,omitempty"`
	Vendor      string     `json:"vendor,omitempty"`
	Category    string     `json:"category,omitempty"`
	EnteredBy   string     `json:"entered_by,omitempty"`
	Corrects    *uuid.UUID `json:"corrects,omitempty"`
	// Credit is the rail's id for one credit to a customer's permanent
	// account (virtual_account.credited's credit_id): Reference names the
	// account, which takes many credits, so the credit is part of the key.
	// It is kept only in the key.
	Credit string `json:"-"`

	// Net says AmountKobo is what landed, the rail's fee (FeeKobo) taken
	// before it: what the payer sent is the two together (CountableKobo).
	Net bool `json:"net,omitempty"`
	// AccountNumber is the account a credit was paid into (a customer's
	// permanent account), when the rail says.
	AccountNumber string `json:"account_number,omitempty"`
}

// CountableKobo is what an entry counts for towards invoices and orders:
// what the payer sent (decision 0046 point 7; docs/spec/schools.md 7.4).
// It reads AmountKobo, and adds FeeKobo when Net says the amount is what
// landed with the rail's fee already taken; 0 without an amount. The
// database says the same (taskiem_money_entry_countable, 00210), and
// checks the parts of an entry against it.
func CountableKobo(e Entry) int64 {
	if e.AmountKobo == nil {
		return 0
	}
	n := *e.AmountKobo
	if e.Net && e.FeeKobo != nil {
		n += *e.FeeKobo
	}
	return n
}

// Key is what makes an entry one movement (and status): where, what and
// the rail's reference.
func (e Entry) Key() string {
	if e.Kind == "statement" {
		// A line is the same line when its channel, date, amount and
		// reference are (spec 7.4).
		day := ""
		if e.OccurredAt != nil {
			day = e.OccurredAt.Format("2006-01-02")
		}
		var amount int64
		if e.AmountKobo != nil {
			amount = *e.AmountKobo
		}
		return fmt.Sprintf("%s|statement|%s|%s%d|%s", e.Channel, day, e.Direction, amount, e.Reference)
	}
	what := e.Event
	if what == "" {
		what = e.Kind + "." + e.Status
	}
	if e.Credit != "" {
		return e.Channel + "|" + what + "|" + e.Reference + "|" + e.Credit
	}
	return e.Channel + "|" + what + "|" + e.Reference
}

// Log writes and reads money log entries.
type Log struct {
	Pool *pgxpool.Pool
	PII  pii.Cipher
	Now  func() time.Time
}

func (l *Log) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// ErrInvalid is an entry the log refuses.
var ErrInvalid = errors.New("moneylog: invalid entry")

func (e Entry) check() error {
	switch {
	case e.Direction != "in" && e.Direction != "out":
		return fmt.Errorf("%w: direction %q", ErrInvalid, e.Direction)
	case e.Channel == "" || e.Reference == "":
		return fmt.Errorf("%w: a channel and a reference are needed", ErrInvalid)
	case e.AmountKobo != nil && *e.AmountKobo < 0, e.FeeKobo != nil && *e.FeeKobo < 0:
		return fmt.Errorf("%w: amounts are not negative; direction says which way", ErrInvalid)
	}
	return nil
}

// Record writes an entry once: fresh is false when the movement was
// already in the log, and id is then the entry already there.
func (l *Log) Record(ctx context.Context, tenant uuid.UUID, e Entry) (id uuid.UUID, fresh bool, err error) {
	err = db.InTenantTx(ctx, l.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		id, fresh, err = l.RecordTx(ctx, tx, tenant, e)
		return err
	})
	return id, fresh, err
}

// RecordTx is Record in the caller's transaction.
func (l *Log) RecordTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, e Entry) (uuid.UUID, bool, error) {
	if err := e.check(); err != nil {
		return uuid.Nil, false, err
	}
	if e.Currency == "" {
		e.Currency = "NGN"
	}
	key := e.Key()
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND entry_key = $2`, tenant, key).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	var payer map[string]any
	if e.PayerName != "" || e.PayerAccount != "" {
		if l.PII == nil {
			return uuid.Nil, false, errors.New("moneylog: no cipher for the payer")
		}
		p := map[string]any{}
		if e.PayerName != "" {
			p["name"] = e.PayerName
		}
		if e.PayerAccount != "" {
			p["account"] = e.PayerAccount
		}
		if payer, err = l.PII.SealTx(ctx, tx, tenant, "name", p); err != nil {
			return uuid.Nil, false, err
		}
	}
	if e.Corrects == nil && e.Credit == "" && e.Event != "" && (e.Source == "event" || e.Source == "lookup" || e.Source == "step") {
		// A later status of a movement names the first entry for it.
		var first uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND reference = $3 AND kind = $4
			ORDER BY recorded_at, id LIMIT 1`, tenant, e.Channel, e.Reference, e.Kind).Scan(&first)
		if err == nil {
			e.Corrects = &first
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	id = uuid.New()
	tag, err := tx.Exec(ctx, `INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, recorded_at, direction, kind, amount_kobo, fee_kobo, currency,
		status, channel, reference, event, payer, source, note, corrects, description, vendor, category, entered_by, account_number, net)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, ''), $15, $16, NULLIF($17, ''), $18,
		NULLIF($19, ''), NULLIF($20, ''), NULLIF($21, ''), NULLIF($22, ''), NULLIF($23, ''), $24)
		ON CONFLICT (tenant_id, entry_key) DO NOTHING`,
		id, tenant, key, e.OccurredAt, l.now(), e.Direction, e.Kind, e.AmountKobo, e.FeeKobo, e.Currency,
		e.Status, e.Channel, e.Reference, e.Event, payer, e.Source, e.Note, e.Corrects,
		clip(e.Description, 300), clip(e.Vendor, 120), clip(e.Category, 60), e.EnteredBy, clip(e.AccountNumber, 40), e.Net && e.FeeKobo != nil)
	if err != nil {
		return uuid.Nil, false, err
	}
	if tag.RowsAffected() == 0 {
		// Written by a concurrent delivery of the same movement.
		err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND entry_key = $2`, tenant, key).Scan(&id)
		return id, false, err
	}
	return id, true, nil
}

// FromEvent is the entry for a rail's normalised event (connector/v1 rule
// 16) heard on channel ("paystack/main"); ok is false for an event the
// log does not record.
func FromEvent(channel, event string, fields map[string]any) (Entry, bool) {
	e := Entry{Channel: channel, Event: event, Source: "event", Currency: str(fields, "currency")}
	switch event {
	case "payment.received":
		e.Direction, e.Kind, e.Status, e.Reference = "in", "payment", "settled", str(fields, "payment_id")
		e.OccurredAt = when(fields, "paid_at")
	case "payment.returned":
		// Money that came in and was sent back to the payer by the rail.
		e.Direction, e.Kind, e.Status, e.Reference = "in", "payment", "reversed", str(fields, "payment_id")
		e.Note = returned[str(fields, "reason")]
	case "virtual_account.credited":
		// Reference is the account's, which takes many credits: each is
		// its own movement (credit_id). amount_kobo is what the payer
		// sent, fee_kobo the rail's fee where it reports one.
		e.Direction, e.Kind, e.Status, e.Reference = "in", "account_credit", "settled", str(fields, "reference")
		e.Credit = clip(str(fields, "credit_id"), 200)
		e.OccurredAt = when(fields, "occurred_at")
		e.FeeKobo = kobo(fields, "fee_kobo")
		e.PayerName = str(fields, "payer_name")
		e.PayerAccount = str(fields, "payer_account")
		// The account it was paid into: the permanent-account rule needs it
		// to agree with the reference (engine/matching).
		e.AccountNumber = str(fields, "account_number")
	case "payout.settled":
		e.Direction, e.Kind, e.Status, e.Reference = "out", "payout", "settled", str(fields, "payout_id")
		e.OccurredAt = when(fields, "settled_at")
	case "payout.failed", "payout.reversed":
		e.Direction, e.Kind, e.Reference = "out", "payout", str(fields, "payout_id")
		e.Status = strings.TrimPrefix(event, "payout.")
		e.Note = clip(str(fields, "reason"), 300)
	case "refund.pending", "refund.processed", "refund.failed":
		// Money out of the business back to a customer: its own movement,
		// known by the payment refunded (the rail's refund reference is
		// not in every status's event), named in the note.
		e.Direction, e.Kind, e.Reference = "out", "refund", str(fields, "payment_id")
		e.Status = refundStatus[event]
		e.Note = "Refund of payment " + clip(e.Reference, 200)
		if id := str(fields, "refund_id"); id != "" {
			e.Note = clip(e.Note+", refund "+id, 300)
		}
		if r := str(fields, "reason"); r != "" {
			e.Note = clip(e.Note+": "+r, 300)
		}
	case "pos.sale":
		e.Direction, e.Kind, e.Reference = "in", "pos_sale", str(fields, "reference")
		e.Status = posStatus[str(fields, "status")]
		if e.Status == "" {
			e.Status = "settled"
		}
		// Each status of a sale is its own entry.
		e.Event = "pos.sale." + e.Status
		e.OccurredAt = when(fields, "occurred_at")
		e.FeeKobo = kobo(fields, "fee_kobo")
		e.PayerName = str(fields, "payer_name")
		if t := str(fields, "terminal"); t != "" {
			e.Note = "Terminal " + clip(t, 100)
		}
	default:
		return Entry{}, false
	}
	e.AmountKobo = kobo(fields, "amount_kobo")
	if e.Reference == "" {
		return Entry{}, false
	}
	return e, true
}

var returned = map[string]string{
	"late":         "Returned to the payer: paid after the account expired",
	"wrong_amount": "Returned to the payer: not the amount asked",
	"other":        "Returned to the payer",
}

var refundStatus = map[string]string{"refund.pending": "pending", "refund.processed": "settled", "refund.failed": "failed"}

// FromPayout is the entry for a payout a payment step's answer says
// settled when it was made (pay_bank answering status settled), on
// channel: amount and currency from the step's input, the fee and the
// rail's payout_id from its output. It is keyed as the rail's
// payout.settled event for the payout would be, so that event, if it
// comes, is the same entry. ok is false for any other answer: a pending
// payout is recorded when its event comes, and a failed one moved no
// money.
func FromPayout(channel string, input, output map[string]any) (Entry, bool) {
	if str(output, "status") != "settled" || str(output, "payout_id") == "" {
		return Entry{}, false
	}
	return Entry{Direction: "out", Kind: "payout", Status: "settled", Channel: channel, Reference: str(output, "payout_id"),
		Event: "payout.settled", Source: "step", Currency: str(input, "currency"),
		AmountKobo: kobo(input, "amount_kobo"), FeeKobo: kobo(output, "fee_kobo")}, true
}

var posStatus = map[string]string{"approved": "settled", "declined": "failed", "reversed": "reversed", "pending": "pending"}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func kobo(m map[string]any, k string) *int64 {
	var n int64
	switch v := m[k].(type) {
	case int64:
		n = v
	case int:
		n = int64(v)
	case float64:
		n = int64(v)
	case string:
		x, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil
		}
		n = x
	default:
		return nil
	}
	if n < 0 {
		n = -n
	}
	return &n
}

func when(m map[string]any, k string) *time.Time {
	s := str(m, k)
	if s == "" {
		return nil
	}
	for _, f := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return &t
		}
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Channel names a connection as the log does: "paystack/main". An empty
// connection is the tenant's only active one of that connector in env.
func (l *Log) Channel(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connector, connection string) (string, error) {
	if connection == "" {
		rows, err := tx.Query(ctx, `SELECT name FROM connections WHERE tenant_id = $1 AND environment = $2 AND connector = $3 AND status = 'active' LIMIT 2`,
			tenant, env, connector)
		if err != nil {
			return "", err
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return "", err
		}
		connection = "main"
		if len(names) == 1 {
			connection = names[0]
		}
	}
	return connector + "/" + connection, nil
}

// RecordEvent writes the entry for a normalised event heard on a
// connection of a rail; ok is false for an event the log does not record.
func (l *Log) RecordEvent(ctx context.Context, tenant uuid.UUID, env, connector, connection, event string, fields map[string]any) (ok bool, err error) {
	err = db.InTenantTx(ctx, l.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		channel, err := l.Channel(ctx, tx, tenant, env, connector, connection)
		if err != nil {
			return err
		}
		e, yes := FromEvent(channel, event, fields)
		if !yes {
			return nil
		}
		ok = true
		_, _, err = l.RecordTx(ctx, tx, tenant, e)
		return err
	})
	return ok, err
}

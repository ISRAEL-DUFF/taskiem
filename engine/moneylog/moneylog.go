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

	"github.com/israel-duff/taskiem/engine/alerts"
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
	// Credit is the rail's id for one credit to an account number that
	// takes more than one: a customer's permanent account
	// (virtual_account.credited's credit_id), or a one-time number whose
	// rail refuses no second transfer (payment.received's credit_id).
	// Reference names the account, so the credit is part of the key. It
	// is also kept on its own (credit_id), so a refund or a return naming
	// the credit finds it.
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
// the rail's reference, and the rail's id for the credit where the
// reference is an account number's that takes more than one (Credit).
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

// Naira is the currency invoices, orders and every total are in. Money in
// another currency is recorded under its own and counted nowhere.
const Naira = "NGN"

// MaxEnteredKobo is the most a person enters by hand as one amount of
// cash or one expense: ₦1,000,000,000, the most an invoice takes. A
// larger amount is a slip of the keyboard, and amounts near the largest
// number a sum can hold would overflow every total.
const MaxEnteredKobo = 100_000_000_000

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

// SameCredit is how far apart, by the rail's own clock, a credit and an
// entry written without the rail's id for it may be and still be taken as
// one movement (samePayment).
const SameCredit = 5 * time.Minute

// samePayment finds the entry already in the log for a payment into an
// account number (payment.received) heard another way, when its own key
// is not there. A payment is heard with the rail's id for the credit (its
// webhook, the look-back) or without it (asked by its reference alone; an
// event written before credits had ids), and both are one movement:
//
//   - Without an id, it is the first credit recorded for the reference:
//     the rail was asked about the payment, not about one transfer.
//   - With an id, it is the entry written without one, unless the rail's
//     own times say this credit came later than that entry's (more than
//     SameCredit after): then it is another transfer into the same
//     number, and its own entry. An entry or a credit without the rail's
//     time is taken as the same, so no credit is ever recorded twice.
//
// ok is false when the payment is new.
func samePayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, e Entry) (id uuid.UUID, ok bool, err error) {
	if e.Credit == "" {
		err = tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND reference = $3 AND kind = 'payment'
			AND event = 'payment.received' AND credit_id IS NOT NULL ORDER BY COALESCE(occurred_at, recorded_at), recorded_at, id LIMIT 1`,
			tenant, e.Channel, e.Reference).Scan(&id)
	} else {
		var at *time.Time
		bare := e
		bare.Credit = ""
		err = tx.QueryRow(ctx, `SELECT id, occurred_at FROM money_entries WHERE tenant_id = $1 AND entry_key = $2`, tenant, bare.Key()).Scan(&id, &at)
		if err == nil && at != nil && e.OccurredAt != nil && e.OccurredAt.After(at.Add(SameCredit)) {
			return uuid.Nil, false, nil
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
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
	if e.Kind == "payment" && e.Event == "payment.received" {
		// One writer at a time per payment reference, so the same credit
		// heard with and without its id at once is still one entry.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('taskiem money payment ' || $1::text || ' ' || $2 || ' ' || $3, 0))`,
			tenant, e.Channel, e.Reference); err != nil {
			return uuid.Nil, false, err
		}
		err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND entry_key = $2`, tenant, key).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
		if same, ok, err := samePayment(ctx, tx, tenant, e); err != nil || ok {
			return same, false, err
		}
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
	if e.Corrects == nil && e.Event != "" && (e.Source == "event" || e.Source == "lookup" || e.Source == "step") {
		// A later status of a movement names the first entry for it. A
		// credit is its own movement; a later status naming the credit (a
		// one-time number's transfer the rail took back) names that
		// credit's entry, else the one written without an id.
		var first uuid.UUID
		err := pgx.ErrNoRows
		switch {
		case e.Credit == "":
			err = tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND reference = $3 AND kind = $4
				ORDER BY recorded_at, id LIMIT 1`, tenant, e.Channel, e.Reference, e.Kind).Scan(&first)
		case e.Kind == "payment" && e.Status != "settled":
			err = tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND channel = $2 AND reference = $3 AND kind = $4
				AND status = 'settled' AND (credit_id = $5 OR credit_id IS NULL) ORDER BY credit_id IS NULL, recorded_at, id LIMIT 1`,
				tenant, e.Channel, e.Reference, e.Kind, e.Credit).Scan(&first)
		}
		if err == nil {
			e.Corrects = &first
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	id = uuid.New()
	tag, err := tx.Exec(ctx, `INSERT INTO money_entries (id, tenant_id, entry_key, occurred_at, recorded_at, direction, kind, amount_kobo, fee_kobo, currency,
		status, channel, reference, event, payer, source, note, corrects, description, vendor, category, entered_by, account_number, net, credit_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, ''), $15, $16, NULLIF($17, ''), $18,
		NULLIF($19, ''), NULLIF($20, ''), NULLIF($21, ''), NULLIF($22, ''), NULLIF($23, ''), $24, NULLIF($25, ''))
		ON CONFLICT (tenant_id, entry_key) DO NOTHING`,
		id, tenant, key, e.OccurredAt, l.now(), e.Direction, e.Kind, e.AmountKobo, e.FeeKobo, e.Currency,
		e.Status, e.Channel, e.Reference, e.Event, payer, e.Source, e.Note, e.Corrects,
		clip(e.Description, 300), clip(e.Vendor, 120), clip(e.Category, 60), e.EnteredBy, clip(e.AccountNumber, 40), e.Net && e.FeeKobo != nil, clip(e.Credit, 200))
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
		// What the payer wrote, where the rail passes it on: matching reads
		// an invoice number in it (docs/matching.md).
		e.Description = clip(str(fields, "narration"), 300)
		// The rail's fee where it settles net and says so (iswallet's
		// one-time numbers): amount_kobo stays what the payer sent.
		e.FeeKobo = kobo(fields, "fee_kobo")
		// The rail's id for this one transfer, where the number it was
		// paid into takes more than one (iswallet's one-time numbers):
		// each transfer is then its own movement, as a permanent
		// account's credits are.
		e.Credit = clip(str(fields, "credit_id"), 200)
	case "payment.returned":
		// Money that came in and was sent back to the payer by the rail:
		// the one transfer, where the rail names it.
		e.Direction, e.Kind, e.Status, e.Reference = "in", "payment", "reversed", str(fields, "payment_id")
		e.Note = returned[str(fields, "reason")]
		e.Credit = clip(str(fields, "credit_id"), 200)
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
		e.Description = clip(str(fields, "narration"), 300)
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
	noteNegative(&e, negativeNote(fields, "amount_kobo", "fee_kobo"))
	if e.Reference == "" {
		return Entry{}, false
	}
	if e.Kind == "account_credit" && e.Credit == "" {
		// Keyed by the account alone, the next credit to it would be
		// dropped as this one: refused, and RecordEvent says so.
		return Entry{}, false
	}
	return e, true
}

// FromCredit is the entry for one credit list_credits read back (the
// look-back, docs/spec/schools.md 6.5), on channel: the entry the rail's
// virtual_account.credited event for it would be, with the same key
// (connection, event, the account's reference and credit_id), so a credit
// heard by webhook and read back is one entry. ok is false for a credit
// without a reference or credit_id.
func FromCredit(channel string, credit map[string]any) (Entry, bool) {
	if str(credit, "event") == "payment.received" {
		// A payment into a one-time number (iswallet's tot_ references):
		// the same movement, by the same key, as the webhook's
		// payment.received (the number's reference and the credit's own
		// id), so heard both ways it is one entry, and a second transfer
		// into the number is a second.
		e, ok := FromEvent(channel, "payment.received", map[string]any{"payment_id": credit["reference"], "credit_id": credit["credit_id"],
			"amount_kobo": credit["amount_kobo"], "fee_kobo": credit["fee_kobo"], "currency": credit["currency"], "paid_at": credit["occurred_at"]})
		if !ok {
			return Entry{}, false
		}
		e.Source = "lookup"
		return e, true
	}
	e, ok := FromEvent(channel, "virtual_account.credited", credit)
	if !ok {
		return Entry{}, false
	}
	e.Source = "lookup"
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
	e := Entry{Direction: "out", Kind: "payout", Status: "settled", Channel: channel, Reference: str(output, "payout_id"),
		Event: "payout.settled", Source: "step", Currency: str(input, "currency"),
		AmountKobo: kobo(input, "amount_kobo"), FeeKobo: kobo(output, "fee_kobo"), Description: clip(str(input, "narration"), 300)}
	noteNegative(&e, negativeNote(input, "amount_kobo"))
	noteNegative(&e, negativeNote(output, "fee_kobo"))
	return e, true
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
	n, _ := signedKobo(m, k)
	if n == nil || *n < 0 {
		// A negative amount is not turned positive: which way money went
		// is the event's to say, so the sign means the rail (or its
		// manifest) is wrong about this movement. negativeNote says so
		// on the entry, which is recorded with no amount.
		return nil
	}
	return n
}

// signedKobo reads an amount as the rail gave it; ok is false when there
// is none or it is not a number.
func signedKobo(m map[string]any, k string) (*int64, bool) {
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
			return nil, false
		}
		n = x
	default:
		return nil, false
	}
	return &n, true
}

// negativeNote is the note of an entry whose rail gave a negative amount
// or fee (in keys of m): what was sent, and that nothing was counted. It
// is "" when none is negative. Such an entry has no amount, so it is in
// no total and no invoice counts it: it waits for a person, who checks
// the movement with the rail.
func negativeNote(m map[string]any, keys ...string) string {
	var bad []string
	for _, k := range keys {
		if n, ok := signedKobo(m, k); ok && *n < 0 {
			bad = append(bad, fmt.Sprintf("%s %d", k, *n))
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return "The service sent a negative amount (" + strings.Join(bad, ", ") + "): recorded with no amount and not counted. Check this movement with the service."
}

// noteNegative adds negativeNote to an entry's note.
func noteNegative(e *Entry, why string) {
	if why == "" {
		return
	}
	if e.Note != "" {
		why = e.Note + ". " + why
	}
	e.Note = clip(why, 300)
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

// AlertCreditWithoutID is the kind of the alert the owners get, once per
// connection a day, when a rail's virtual_account.credited event carries
// no credit_id: the credit is not recorded (keyed by the account alone,
// it would hide the account's next credit), so the look-back or a hand
// entry has to bring it.
const AlertCreditWithoutID = "money_credit_without_id"

// RecordEvent writes the entry for a normalised event heard on a
// connection of a rail; ok is false for an event the log does not record.
// A credit to a customer's account without its credit_id is not recorded
// and raises AlertCreditWithoutID.
func (l *Log) RecordEvent(ctx context.Context, tenant uuid.UUID, env, connector, connection, event string, fields map[string]any) (ok bool, err error) {
	err = db.InTenantTx(ctx, l.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		channel, err := l.Channel(ctx, tx, tenant, env, connector, connection)
		if err != nil {
			return err
		}
		e, yes := FromEvent(channel, event, fields)
		if !yes {
			if event == "virtual_account.credited" && str(fields, "reference") != "" && str(fields, "credit_id") == "" {
				_, err := alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertCreditWithoutID, Dedup: channel + ":" + l.now().In(lagos).Format(time.DateOnly),
					Title: "A payment into a customer's account number was not recorded",
					Body: fmt.Sprintf("%s told Taskiem of a payment into account %s without the id that tells one payment from the next, so Taskiem did not record it. "+
						"Check the payment on %s and enter it by hand on Money if the look-back does not find it.", connector, clip(str(fields, "reference"), 100), connector),
					Link: "/money", Detail: map[string]any{"channel": channel, "reference": clip(str(fields, "reference"), 200)}})
				return err
			}
			return nil
		}
		ok = true
		_, _, err = l.RecordTx(ctx, tx, tenant, e)
		return err
	})
	return ok, err
}

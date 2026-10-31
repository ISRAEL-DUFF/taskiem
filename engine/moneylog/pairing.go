package moneylog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Pairing a statement line with a transfer entered by hand (decision
// 0060; docs/spec/no-sheet-needed.md §6.4, R5e; migration 00275). A
// transfer a person said went into an account Taskiem does not read
// ("Ada paid 2,000 transfer" into gtbank/shop) is entered at once, kind
// statement and source entered, under that account's channel. The bank's
// statement of that account carries the same money later, and is the
// record of it: a money-in line on that channel of the same amount and
// within PairDays of the entry is recorded correcting it (one movement,
// counted once, and never matched again), when exactly one entry fits.
// When several fit, or one alone of another amount near one line alone,
// the line waits (money_statement_waiting), out of the log, for a person
// to say Same money or Different money (Decide). An entry no statement
// confirms stays as the person's word.

// PairDays is how far apart, in Lagos days, a statement line and the
// transfer entered by hand it confirms may be.
const PairDays = 3

// ErrNoWaiting is a waiting line that does not exist or was decided.
var ErrNoWaiting = errors.New("moneylog: no such line waiting")

// linePlan is what an import does with one line.
type linePlan struct {
	skip     bool        // in the log already, or waiting, or twice in the file
	confirms uuid.UUID   // the entry it is recorded as the bank's confirmation of
	ask      []uuid.UUID // the entries a person picks from
}

// pair plans each line of an import: skipped, paired, asked about, or
// (the zero plan) recorded as money of its own.
func (l *Log) pair(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string, entries []Entry) ([]linePlan, error) {
	plans := make([]linePlan, len(entries))
	seen := map[string]bool{}
	taken := map[uuid.UUID]bool{} // paired or asked about in this import
	var plain []int
	for i, e := range entries {
		key := e.Key()
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_entries WHERE tenant_id = $1 AND entry_key = $2)
			OR EXISTS (SELECT 1 FROM money_statement_waiting WHERE tenant_id = $1 AND entry_key = $2)`, tenant, key).Scan(&exists); err != nil {
			return nil, err
		}
		if exists || seen[key] {
			plans[i].skip = true
			continue
		}
		seen[key] = true
		if e.Direction != "in" || e.AmountKobo == nil || e.OccurredAt == nil {
			continue
		}
		same, err := entered(ctx, tx, tenant, channel, *e.OccurredAt, e.AmountKobo, taken)
		if err != nil {
			return nil, err
		}
		switch len(same) {
		case 0:
			plain = append(plain, i)
		case 1:
			plans[i].confirms = same[0]
			taken[same[0]] = true
		default:
			plans[i].ask = same
			for _, id := range same {
				taken[id] = true
			}
		}
	}
	// One line alone near one entry alone, of another amount: asked about.
	near := map[uuid.UUID][]int{}
	lineNear := map[int][]uuid.UUID{}
	for _, i := range plain {
		e := entries[i]
		list, err := entered(ctx, tx, tenant, channel, *e.OccurredAt, nil, taken)
		if err != nil {
			return nil, err
		}
		lineNear[i] = list
		for _, id := range list {
			near[id] = append(near[id], i)
		}
	}
	for _, i := range plain {
		if list := lineNear[i]; len(list) == 1 && len(near[list[0]]) == 1 {
			plans[i].ask = list
		}
	}
	return plans, nil
}

// entered lists the transfers entered by hand on channel within PairDays
// of when, of amount when given, that no statement line confirms and no
// waiting line names, leaving out those in taken.
func entered(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string, when time.Time, amount *int64, taken map[uuid.UUID]bool) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT e.id FROM money_entries e
		WHERE e.tenant_id = $1 AND e.channel = $2 AND e.direction = 'in' AND e.kind = 'statement' AND e.source = 'entered' AND e.status = 'settled'
		  AND e.corrects IS NULL AND ($3::bigint IS NULL OR e.amount_kobo = $3)
		  AND abs((COALESCE(e.occurred_at, e.recorded_at) AT TIME ZONE 'Africa/Lagos')::date - ($4::timestamptz AT TIME ZONE 'Africa/Lagos')::date) <= $5
		  AND NOT EXISTS (SELECT 1 FROM money_entries c WHERE c.tenant_id = $1 AND c.corrects = e.id)
		  AND NOT EXISTS (SELECT 1 FROM money_statement_waiting w WHERE w.tenant_id = $1 AND w.decision IS NULL AND e.id = ANY (w.candidates))
		ORDER BY COALESCE(e.occurred_at, e.recorded_at), e.id LIMIT 20`, tenant, channel, amount, when, PairDays)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := ids[:0]
	for _, id := range ids {
		if !taken[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// confirm records a statement line as the bank's confirmation of an entry
// entered by hand: correcting it, and matched already, since the entry
// was counted when it was entered.
func (l *Log) confirm(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, e *Entry, entry uuid.UUID) error {
	e.Corrects = &entry
	id, fresh, err := l.RecordTx(ctx, tx, tenant, *e)
	if err != nil || !fresh {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'matched', $3) ON CONFLICT (entry_id) DO NOTHING`,
		id, tenant, l.now())
	return err
}

// waitLine is a waiting line's own words, kept until it is decided.
type waitLine struct {
	OccurredAt  time.Time `json:"occurred_at"`
	AmountKobo  int64     `json:"amount_kobo"`
	Reference   string    `json:"reference"`
	Description string    `json:"description,omitempty"`
}

// wait keeps a line for a person to decide, once.
func (l *Log) wait(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, by string, e Entry, candidates []uuid.UUID) error {
	raw, _ := json.Marshal(waitLine{OccurredAt: *e.OccurredAt, AmountKobo: *e.AmountKobo, Reference: e.Reference, Description: clip(e.Description, 300)})
	_, err := tx.Exec(ctx, `INSERT INTO money_statement_waiting (id, tenant_id, channel, entry_key, line, candidates, uploaded_by) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, entry_key) DO NOTHING`, uuid.New(), tenant, channel, e.Key(), raw, candidates, by)
	return err
}

// Candidate is a transfer entered by hand a waiting line may confirm.
type Candidate struct {
	EntryID     uuid.UUID `json:"entry_id"`
	AmountKobo  int64     `json:"amount_kobo"`
	OccurredAt  time.Time `json:"occurred_at"`
	Description string    `json:"description,omitempty"`
	EnteredBy   string    `json:"entered_by,omitempty"`
}

// Waiting is a statement line that waits for a person: is it the money a
// transfer entered by hand already counts (Same money), or money of its
// own (Different money)?
type Waiting struct {
	ID          uuid.UUID   `json:"id"`
	Channel     string      `json:"channel"`
	OccurredAt  time.Time   `json:"occurred_at"`
	AmountKobo  int64       `json:"amount_kobo"`
	Description string      `json:"description,omitempty"`
	Candidates  []Candidate `json:"candidates"`
	CreatedAt   time.Time   `json:"created_at"`
}

// WaitingLines lists the lines waiting for a person, by upload, then by
// the line's date.
func WaitingLines(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Waiting, error) {
	rows, err := tx.Query(ctx, `SELECT id, channel, line, candidates, created_at FROM money_statement_waiting
		WHERE tenant_id = $1 AND decision IS NULL ORDER BY created_at, (line->>'occurred_at')::timestamptz, id LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	type raw struct {
		w     Waiting
		line  waitLine
		cands []uuid.UUID
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var x raw
		var line []byte
		err := r.Scan(&x.w.ID, &x.w.Channel, &line, &x.cands, &x.w.CreatedAt)
		_ = json.Unmarshal(line, &x.line)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Waiting, 0, len(list))
	for _, x := range list {
		w := x.w
		w.OccurredAt, w.AmountKobo, w.Description = x.line.OccurredAt, x.line.AmountKobo, x.line.Description
		rows, err := tx.Query(ctx, `SELECT id, COALESCE(amount_kobo, 0), COALESCE(occurred_at, recorded_at), COALESCE(description, ''), COALESCE(entered_by, '')
			FROM money_entries WHERE tenant_id = $1 AND id = ANY ($2) ORDER BY COALESCE(occurred_at, recorded_at), id`, tenant, x.cands)
		if err != nil {
			return nil, err
		}
		if w.Candidates, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Candidate, error) {
			var c Candidate
			return c, r.Scan(&c.EntryID, &c.AmountKobo, &c.OccurredAt, &c.Description, &c.EnteredBy)
		}); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// Decide records a waiting line: with same, as the bank's confirmation of
// entry (one of its candidates, not confirmed by another line since);
// without, as money of its own, which matching then looks at. It answers
// the line's entry in the log.
func (l *Log) Decide(ctx context.Context, tenant, id uuid.UUID, same bool, entry uuid.UUID, by string) (uuid.UUID, error) {
	var out uuid.UUID
	err := db.InTenantTx(ctx, l.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var channel, key string
		var raw []byte
		var cands []uuid.UUID
		err := tx.QueryRow(ctx, `SELECT channel, entry_key, line, candidates FROM money_statement_waiting WHERE tenant_id = $1 AND id = $2 AND decision IS NULL FOR UPDATE`,
			tenant, id).Scan(&channel, &key, &raw, &cands)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoWaiting
		}
		if err != nil {
			return err
		}
		var line waitLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return err
		}
		amount, when := line.AmountKobo, line.OccurredAt
		e := Entry{Direction: "in", Kind: "statement", Status: "settled", Channel: channel, Reference: line.Reference, AmountKobo: &amount,
			OccurredAt: &when, Source: "upload", Description: line.Description, EnteredBy: by}
		decision := "different"
		if same {
			decision = "same"
			ok := false
			for _, c := range cands {
				ok = ok || c == entry
			}
			if !ok {
				return fmt.Errorf("%w: that transfer is not one this line may be", ErrInvalid)
			}
			var confirmed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_entries WHERE tenant_id = $1 AND corrects = $2)`, tenant, entry).Scan(&confirmed); err != nil {
				return err
			}
			if confirmed {
				return fmt.Errorf("%w: another statement line already confirms that transfer", ErrInvalid)
			}
			if err := l.confirm(ctx, tx, tenant, &e, entry); err != nil {
				return err
			}
		} else if _, _, err := l.RecordTx(ctx, tx, tenant, e); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND entry_key = $2`, tenant, key).Scan(&out); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE money_statement_waiting SET decision = $3, entry_id = $4, decided_by = $5, decided_at = $6 WHERE tenant_id = $1 AND id = $2`,
			tenant, id, decision, out, by, l.now())
		return err
	})
	return out, err
}

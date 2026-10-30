package moneylog_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A statement line confirms a transfer entered by hand (decision 0060,
// R5e): the one entry of the same amount on the same account within
// three days pairs at once, counted once and never matched again; two
// such entries, or one of another amount near one line alone, wait for a
// person, out of the log and not imported twice; Same money and Different
// money record the line; lines far off or on another account are money of
// their own.
func TestStatementPairsEnteredTransfers(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	lagos, _ := time.LoadLocation("Africa/Lagos")
	day := func(d int) *time.Time {
		t := time.Date(2026, 10, d, 12, 0, 0, 0, lagos)
		return &t
	}
	enter := func(ref string, kobo int64, d int) uuid.UUID {
		t.Helper()
		id, _, err := log.Record(ctx, e.Tenant, moneylog.Entry{Direction: "in", Kind: "statement", Status: "settled", Channel: "gtbank/shop",
			Reference: "chat:" + ref, Source: "entered", AmountKobo: &kobo, OccurredAt: day(d), Description: "Paid by Ada", EnteredBy: "user:owner"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ada := enter("a", 200000, 20)    // the bank shows it two days later: pairs
	bola1 := enter("b1", 500000, 20) // two of 5,000 the same day: asked
	bola2 := enter("b2", 500000, 20) //
	chidi := enter("c", 300000, 25)  // the bank shows 2,950 (a fee?): asked
	_ = enter("d", 100000, 1)        // far from every line: stays as entered
	cols := moneylog.CommonColumns
	csv := "date,description,amount,reference\n" +
		"22/10/2026,TRF ADA,2000,FT1\n" + // pairs with ada
		"21/10/2026,TRF BOLA,5000,FT2\n" + // two candidates
		"26/10/2026,TRF CHIDI,2950,FT3\n" + // one near, another amount
		"10/10/2026,TRF EMEKA,2000,FT4\n" + // nothing near: money of its own
		"22/10/2026,Charges,-50,FT5\n"
	lines, err := moneylog.ParseStatement(strings.NewReader(csv), cols)
	if err != nil {
		t.Fatal(err)
	}
	got, err := log.ImportStatement(ctx, e.Tenant, "gtbank/shop", "user:owner", cols, lines)
	if err != nil || got != (moneylog.Imported{Added: 2, Paired: 1, Waiting: 2}) {
		t.Fatalf("import: %+v %v", got, err)
	}
	// Again: nothing twice, the waiting lines included.
	if again, err := log.ImportStatement(ctx, e.Tenant, "gtbank/shop", "user:owner", cols, lines); err != nil || again != (moneylog.Imported{Skipped: 5}) {
		t.Fatalf("again: %+v %v", again, err)
	}
	// The paired line corrects ada's entry and is matched already; the
	// one of its own waits for matching.
	var corrects *uuid.UUID
	var state *string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT e.corrects, s.state FROM money_entries e LEFT JOIN money_match_state s ON s.entry_id = e.id
		WHERE e.tenant_id = $1 AND e.reference = 'FT1'`, e.Tenant).Scan(&corrects, &state); err != nil || corrects == nil || *corrects != ada || state == nil || *state != "matched" {
		t.Errorf("paired: %v %v %v", corrects, state, err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT s.state FROM money_entries e LEFT JOIN money_match_state s ON s.entry_id = e.id
		WHERE e.tenant_id = $1 AND e.reference = 'FT4'`, e.Tenant).Scan(&state); err != nil || state != nil {
		t.Errorf("own money: %v %v", state, err)
	}

	var waiting []moneylog.Waiting
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	in(func(tx pgx.Tx) error {
		var err error
		waiting, err = moneylog.WaitingLines(ctx, tx, e.Tenant, 10)
		return err
	})
	if len(waiting) != 2 || waiting[0].AmountKobo != 500000 || len(waiting[0].Candidates) != 2 || waiting[1].AmountKobo != 295000 ||
		len(waiting[1].Candidates) != 1 || waiting[1].Candidates[0].EntryID != chidi || waiting[1].Candidates[0].Description != "Paid by Ada" {
		t.Fatalf("waiting: %+v", waiting)
	}

	// Same money: only one of its candidates.
	if _, err := log.Decide(ctx, e.Tenant, waiting[0].ID, true, chidi, "user:owner"); !errors.Is(err, moneylog.ErrInvalid) {
		t.Errorf("not a candidate: %v", err)
	}
	id, err := log.Decide(ctx, e.Tenant, waiting[0].ID, true, bola2, "user:owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT corrects FROM money_entries WHERE id = $1`, id).Scan(&corrects); err != nil || corrects == nil || *corrects != bola2 {
		t.Errorf("same money: %v %v", corrects, err)
	}
	if _, err := log.Decide(ctx, e.Tenant, waiting[0].ID, false, uuid.Nil, "user:owner"); !errors.Is(err, moneylog.ErrNoWaiting) {
		t.Errorf("decided twice: %v", err)
	}
	// Different money: its own, for matching.
	if id, err = log.Decide(ctx, e.Tenant, waiting[1].ID, false, uuid.Nil, "user:owner"); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT corrects FROM money_entries WHERE id = $1`, id).Scan(&corrects); err != nil || corrects != nil {
		t.Errorf("different money: %v %v", corrects, err)
	}
	in(func(tx pgx.Tx) error {
		waiting, err = moneylog.WaitingLines(ctx, tx, e.Tenant, 10)
		return err
	})
	if len(waiting) != 0 {
		t.Errorf("still waiting: %+v", waiting)
	}
	// bola1 is still the person's word; a later line of 5,000 pairs with it.
	more, _ := moneylog.ParseStatement(strings.NewReader("date,description,amount,reference\n23/10/2026,TRF BOLA,5000,FT6\n"), cols)
	if got, err := log.ImportStatement(ctx, e.Tenant, "gtbank/shop", "user:owner", cols, more); err != nil || got != (moneylog.Imported{Paired: 1}) {
		t.Fatalf("later: %+v %v", got, err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT corrects FROM money_entries WHERE tenant_id = $1 AND reference = 'FT6'`, e.Tenant).Scan(&corrects); err != nil || corrects == nil || *corrects != bola1 {
		t.Errorf("later pairs: %v %v", corrects, err)
	}
	// On another account nothing pairs.
	other, _ := moneylog.ParseStatement(strings.NewReader("date,description,amount,reference\n20/10/2026,TRF,2000,FT7\n"), cols)
	if got, err := log.ImportStatement(ctx, e.Tenant, "access/main", "user:owner", cols, other); err != nil || got != (moneylog.Imported{Added: 1}) {
		t.Fatalf("other account: %+v %v", got, err)
	}
}

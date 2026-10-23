package moneylog

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Statements and entries people add (spec 7.4, T6): a bank statement
// uploaded as a CSV in one common format (date, description, amount, and
// optionally reference and balance), and cash and expenses entered from a
// form or chat.

// StatementFormat names a file's columns for each field of the common format.
// Amount is signed (money in positive); a file with separate columns for
// money in and money out names In and Out instead.
type StatementFormat struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      string `json:"amount,omitempty"`
	In          string `json:"in,omitempty"`
	Out         string `json:"out,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Balance     string `json:"balance,omitempty"`
}

// CommonColumns is the common format's own header.
var CommonColumns = StatementFormat{Date: "date", Description: "description", Amount: "amount", Reference: "reference", Balance: "balance"}

// Line is one line of a statement.
type Line struct {
	Row         int       `json:"row"` // in the file, counting the header as 1
	Date        time.Time `json:"date"`
	Description string    `json:"description"`
	AmountKobo  int64     `json:"amount_kobo"` // signed: money in positive
	Reference   string    `json:"reference,omitempty"`
}

// Problem is a line the file could not be read at.
type Problem struct {
	Row int    `json:"row"`
	Why string `json:"why"`
}

// Limits on an upload.
const (
	MaxStatementLines = 5000
	MaxStatementBytes = 5 << 20
)

// ErrStatement is a file that cannot be read as a statement; Problems
// says where.
var ErrStatement = errors.New("moneylog: the statement cannot be read")

// StatementError lists what is wrong with a file.
type StatementError struct {
	Missing  []string  // columns the file does not have
	Problems []Problem // lines that cannot be read (the first 20)
	Message  string
}

func (e *StatementError) Error() string { return e.Message }
func (e *StatementError) Unwrap() error { return ErrStatement }

// ParseStatement reads a CSV statement with the given columns (zero:
// the common format's). A file with any line it cannot read is refused
// whole, so a wrong match of columns adds nothing.
func ParseStatement(r io.Reader, cols StatementFormat) ([]Line, error) {
	common := cols.Date == "" && cols.Description == ""
	if common {
		cols = CommonColumns
	}
	cr := csv.NewReader(io.LimitReader(r, MaxStatementBytes+1))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return nil, &StatementError{Message: "the file is empty or not a CSV"}
	}
	at := map[string]int{}
	for i, h := range header {
		at[norm(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	idx := func(name string) int {
		if name == "" {
			return -1
		}
		if i, ok := at[norm(name)]; ok {
			return i
		}
		return -2
	}
	date, desc, amount, in, out, ref := idx(cols.Date), idx(cols.Description), idx(cols.Amount), idx(cols.In), idx(cols.Out), idx(cols.Reference)
	if common && ref == -2 {
		ref = -1 // the common format's reference is optional
	}
	var missing []string
	for name, i := range map[string]int{cols.Date: date, cols.Description: desc, cols.Amount: amount, cols.In: in, cols.Out: out, cols.Reference: ref} {
		if i == -2 {
			missing = append(missing, name)
		}
	}
	if date < 0 || desc < 0 || (amount < 0 && in < 0 && out < 0) {
		if date == -1 {
			missing = append(missing, "date")
		}
		if desc == -1 {
			missing = append(missing, "description")
		}
		if amount == -1 && in == -1 && out == -1 {
			missing = append(missing, "amount")
		}
	}
	if len(missing) > 0 {
		return nil, &StatementError{Missing: missing, Message: "the file has no column " + strings.Join(quoteAll(missing), ", ") + "; match its columns to the common format"}
	}
	var lines []Line
	var problems []Problem
	row := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			problems = append(problems, Problem{Row: row, Why: "not a CSV line"})
			continue
		}
		if blank(rec) {
			continue
		}
		if len(lines) >= MaxStatementLines {
			return nil, &StatementError{Message: fmt.Sprintf("the file has more than %d lines; upload it in parts", MaxStatementLines)}
		}
		get := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		l := Line{Row: row, Description: clip(get(desc), 300), Reference: clip(get(ref), 200)}
		d, ok := parseDate(get(date))
		if !ok {
			problems = append(problems, Problem{Row: row, Why: fmt.Sprintf("the date %q is not one Taskiem reads (use 2026-10-23 or 23/10/2026)", get(date))})
			continue
		}
		l.Date = d
		switch {
		case amount >= 0:
			k, ok := parseKobo(get(amount))
			if !ok {
				problems = append(problems, Problem{Row: row, Why: fmt.Sprintf("the amount %q is not a number of naira", get(amount))})
				continue
			}
			l.AmountKobo = k
		default:
			ki, okIn := parseKobo(get(in))
			ko, okOut := parseKobo(get(out))
			if get(in) == "" {
				ki, okIn = 0, true
			}
			if get(out) == "" {
				ko, okOut = 0, true
			}
			if !okIn || !okOut || (ki != 0) == (ko != 0) {
				problems = append(problems, Problem{Row: row, Why: "a line needs an amount in money in or in money out, not both"})
				continue
			}
			l.AmountKobo = abs(ki) - abs(ko)
		}
		if l.AmountKobo == 0 {
			problems = append(problems, Problem{Row: row, Why: "the amount is zero"})
			continue
		}
		lines = append(lines, l)
	}
	if len(problems) > 0 {
		n := len(problems)
		if len(problems) > 20 {
			problems = problems[:20]
		}
		return nil, &StatementError{Problems: problems, Message: fmt.Sprintf("%d lines cannot be read; nothing was added", n)}
	}
	if len(lines) == 0 {
		return nil, &StatementError{Message: "the file has no lines under its header"}
	}
	return lines, nil
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = strconv.Quote(x)
	}
	return out
}

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

var dateFormats = []string{
	"2006-01-02", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05",
	"02/01/2006", "2/1/2006", "02/01/2006 15:04:05", "02/01/2006 15:04", "02-01-2006", "02.01.2006",
	"02-Jan-2006", "2-Jan-2006", "02 Jan 2006", "2 Jan 2006", "02-Jan-06", "Jan 2, 2006", "02/01/06",
}

// parseDate reads the dates Nigerian bank statements use: ISO, and
// day-first (23/10/2026, 23-Oct-2026). Lagos time.
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, f := range dateFormats {
		if t, err := time.ParseInLocation(f, s, lagos); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var lagos = func() *time.Location {
	if l, err := time.LoadLocation("Africa/Lagos"); err == nil {
		return l
	}
	return time.FixedZone("WAT", 3600)
}()

var koboRe = regexp.MustCompile(`^(\d+)(?:\.(\d{1,2}))?$`)

// parseKobo reads naira as written on statements: "1,234.50", "-1234.5",
// "(1,234.50)", "₦1,234", "NGN 1,234.50", "1,234.50 CR" or "DR".
func parseKobo(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	up := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(up, "DR"):
		neg, s = true, strings.TrimSpace(s[:len(s)-2])
	case strings.HasSuffix(up, "CR"):
		s = strings.TrimSpace(s[:len(s)-2])
	}
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "NGN"), "₦"), "N"))
	if strings.HasPrefix(s, "-") {
		neg, s = !neg, s[1:]
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "₦"))
	s = strings.ReplaceAll(s, ",", "")
	m := koboRe.FindStringSubmatch(s)
	if m == nil || len(m[1]) > 13 {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	k := n * 100
	if m[2] != "" {
		f := m[2]
		if len(f) == 1 {
			f += "0"
		}
		x, _ := strconv.ParseInt(f, 10, 64)
		k += x
	}
	if neg {
		k = -k
	}
	return k, true
}

// Imported is what an upload added.
type Imported struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"` // lines already in the log
}

// ImportStatement adds a statement's lines to the log as statement
// entries on channel ("gtbank/current"), skipping lines already there,
// and remembers the columns for the channel's next upload.
func (l *Log) ImportStatement(ctx context.Context, tenant uuid.UUID, channel, by string, cols StatementFormat, lines []Line) (Imported, error) {
	if !channelRe.MatchString(channel) {
		return Imported{}, fmt.Errorf("%w: name the account the statement is for, such as gtbank/current", ErrInvalid)
	}
	var out Imported
	err := db.InTenantTx(ctx, l.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for _, ln := range lines {
			amount, dir := ln.AmountKobo, "in"
			if amount < 0 {
				amount, dir = -amount, "out"
			}
			d := ln.Date
			e := Entry{Direction: dir, Kind: "statement", Status: "settled", Channel: channel, Reference: ln.Reference, AmountKobo: &amount,
				OccurredAt: &d, Source: "upload", Description: ln.Description, EnteredBy: by}
			if e.Reference == "" {
				// The key needs something: the line's own words.
				e.Reference = clip(ln.Description, 200)
			}
			if e.Reference == "" {
				e.Reference = "-"
			}
			_, fresh, err := l.RecordTx(ctx, tx, tenant, e)
			if err != nil {
				return err
			}
			if fresh {
				out.Added++
			} else {
				out.Skipped++
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO money_statement_formats (tenant_id, channel, columns) VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, channel) DO UPDATE SET columns = $3, updated_at = now()`, tenant, channel, cols)
		return err
	})
	return out, err
}

// StatementColumns is the columns last used for a channel's statements
// (the common format's when none were).
func StatementColumns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string) (StatementFormat, error) {
	var c StatementFormat
	err := tx.QueryRow(ctx, `SELECT columns FROM money_statement_formats WHERE tenant_id = $1 AND channel = $2`, tenant, channel).Scan(&c)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommonColumns, nil
	}
	return c, err
}

// channelRe is a name for an account statements are uploaded for, or
// that cash moves through: "gtbank/current", "cash", "petty-cash".
var channelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,40}(/[a-z0-9][a-z0-9_.-]{0,40})?$`)

// Entered is cash or an expense a person enters.
type Entered struct {
	Kind        string    `json:"kind"`      // cash or expense
	Direction   string    `json:"direction"` // cash: in or out; an expense is out
	AmountKobo  int64     `json:"amount_kobo"`
	When        time.Time `json:"occurred_at"`
	Description string    `json:"description"`
	Vendor      string    `json:"vendor,omitempty"`
	Category    string    `json:"category,omitempty"`
	// Channel is where the money moved: "cash" (the default) or an
	// account ("gtbank/current").
	Channel string `json:"channel,omitempty"`
	// Key makes a retry the same entry (a form's or chat's request id).
	Key string `json:"-"`
}

// Enter writes cash or an expense a person entered, once per key.
func (l *Log) Enter(ctx context.Context, tenant uuid.UUID, by string, in Entered) (uuid.UUID, bool, error) {
	switch in.Kind {
	case "cash":
		if in.Direction != "in" && in.Direction != "out" {
			return uuid.Nil, false, fmt.Errorf("%w: cash goes in or out", ErrInvalid)
		}
	case "expense":
		in.Direction = "out"
	default:
		return uuid.Nil, false, fmt.Errorf("%w: kind is cash or expense", ErrInvalid)
	}
	if in.AmountKobo <= 0 {
		return uuid.Nil, false, fmt.Errorf("%w: the amount is more than zero", ErrInvalid)
	}
	if strings.TrimSpace(in.Description) == "" {
		return uuid.Nil, false, fmt.Errorf("%w: say what it was for", ErrInvalid)
	}
	if in.Channel == "" {
		in.Channel = "cash"
	}
	if !channelRe.MatchString(in.Channel) {
		return uuid.Nil, false, fmt.Errorf("%w: the channel is cash or an account such as gtbank/current", ErrInvalid)
	}
	if in.When.IsZero() {
		in.When = l.now()
	}
	if in.When.After(l.now().Add(24 * time.Hour)) {
		return uuid.Nil, false, fmt.Errorf("%w: the date is in the future", ErrInvalid)
	}
	ref := in.Key
	if ref == "" {
		ref = uuid.NewString()
	}
	amount := in.AmountKobo
	when := in.When
	return l.Record(ctx, tenant, Entry{Direction: in.Direction, Kind: in.Kind, Status: "settled", Channel: in.Channel, Reference: "entered:" + clip(ref, 180),
		AmountKobo: &amount, OccurredAt: &when, Source: "entered", Description: strings.TrimSpace(in.Description), Vendor: strings.TrimSpace(in.Vendor),
		Category: strings.ToLower(strings.TrimSpace(in.Category)), EnteredBy: by})
}

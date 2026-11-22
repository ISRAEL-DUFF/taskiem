// Package spend is what a business's money out was for
// (docs/spec/what-a-business-buys.md §5.4, §8; decision 0070 points 3 and
// 4): its own labels, made by typing them, renamed and merged without
// touching the money log; and the expense of a purchase or a linked
// supplier's bill split by label.
//
// A label is never built in and never required: an expense with none is
// Not labelled. Every expense carries its label's id and, as its
// category, the label's name when it was made; reports find each entry's
// label by id when they are read and follow merged_into to the label kept,
// so a rename or a merge regroups all past spending at once.
package spend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxLabel is the longest a label's name is, in characters: the money
// log's limit for a category (spend_labels' CHECK; stock.MaxLabel).
const MaxLabel = 60

// StockBought is the label a line added to stock takes when it has none
// (§5.2), found or made when the order is paid.
const StockBought = "Stock bought"

// NotLabelled is how spending with no label reads.
const NotLabelled = "Not labelled"

// Errors.
var (
	ErrLabel   = errors.New("spend: label")
	ErrNoLabel = errors.New("spend: no such label")
)

// Label is one of the business's own labels (a row of spend_labels).
// A merged label names the label kept; Uses is how many expenses and
// purchase lines carry it, its merged labels' included (lists only).
type Label struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	MergedInto *uuid.UUID `json:"merged_into,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	Uses       int        `json:"uses"`
}

// CleanName is a label's name as kept: spaces trimmed and single, at most
// MaxLabel characters.
func CleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", fmt.Errorf("%w: say the label", ErrLabel)
	case utf8.RuneCountInString(name) > MaxLabel:
		return "", fmt.Errorf("%w: a label is at most %d characters", ErrLabel, MaxLabel)
	}
	return name, nil
}

// NameFrom is a label's name made from words typed for something else (an
// expense's description, an item's name): spaces single, the first letter
// a capital, cut to MaxLabel characters. "diesel" is Diesel.
func NameFrom(words string) string {
	words = strings.Join(strings.Fields(words), " ")
	if r := []rune(words); len(r) > MaxLabel {
		words = strings.TrimSpace(string(r[:MaxLabel]))
	}
	r, n := utf8.DecodeRuneInString(words)
	if n == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + words[n:]
}

const labelCols = `id, name, merged_into, created_by, created_at`

func scanLabel(r pgx.Row) (Label, error) {
	var l Label
	err := r.Scan(&l.ID, &l.Name, &l.MergedInto, &l.CreatedBy, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNoLabel
	}
	return l, err
}

// LabelTx is one label, by id, as it is (merged or not).
func LabelTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Label, error) {
	return scanLabel(tx.QueryRow(ctx, `SELECT `+labelCols+` FROM spend_labels WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// KeptTx is the label an id stands for now: the label itself, or the one
// it was merged into.
func KeptTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Label, error) {
	return scanLabel(tx.QueryRow(ctx, `SELECT k.id, k.name, k.merged_into, k.created_by, k.created_at FROM spend_labels l
		JOIN spend_labels k ON k.tenant_id = l.tenant_id AND k.id = COALESCE(l.merged_into, l.id) WHERE l.tenant_id = $1 AND l.id = $2`, tenant, id))
}

// FindTx is the label kept for a name typed, without case: a merged
// label's name finds the label it was merged into. ErrNoLabel when there
// is none.
func FindTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) (Label, error) {
	name, err := CleanName(name)
	if err != nil {
		return Label{}, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM spend_labels WHERE tenant_id = $1 AND lower(name) = lower($2)`, tenant, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrNoLabel
	}
	if err != nil {
		return Label{}, err
	}
	return KeptTx(ctx, tx, tenant, id)
}

// FindOrMakeTx is the label kept for a name typed (FindTx), made when the
// business has none of that name. made says it is new.
func FindOrMakeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, actor string) (Label, bool, error) {
	name, err := CleanName(name)
	if err != nil {
		return Label{}, false, err
	}
	l, err := FindTx(ctx, tx, tenant, name)
	if !errors.Is(err, ErrNoLabel) {
		return l, false, err
	}
	// Two people typing the same new label at once make it once.
	tag, err := tx.Exec(ctx, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, lower(name)) DO NOTHING`, uuid.Must(uuid.NewV7()), tenant, name, cut(actor, 200))
	if err != nil {
		return Label{}, false, err
	}
	l, err = FindTx(ctx, tx, tenant, name)
	return l, err == nil && tag.RowsAffected() == 1, err
}

// ListTx is the business's labels that are not merged, each with its
// uses (its merged labels' included), the most used first, then by name:
// what a picker offers.
func ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Label, error) {
	rows, err := tx.Query(ctx, `WITH uses AS (
			SELECT label_id FROM money_entries WHERE tenant_id = $1 AND label_id IS NOT NULL
			UNION ALL SELECT label_id FROM purchase_lines WHERE tenant_id = $1 AND label_id IS NOT NULL
		), counted AS (
			SELECT COALESCE(l.merged_into, l.id) AS kept, count(*)::int AS n FROM uses u JOIN spend_labels l ON l.tenant_id = $1 AND l.id = u.label_id GROUP BY 1
		)
		SELECT l.id, l.name, l.merged_into, l.created_by, l.created_at, COALESCE(c.n, 0) FROM spend_labels l LEFT JOIN counted c ON c.kept = l.id
		WHERE l.tenant_id = $1 AND l.merged_into IS NULL ORDER BY COALESCE(c.n, 0) DESC, lower(l.name), l.id`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Label, error) {
		var l Label
		err := r.Scan(&l.ID, &l.Name, &l.MergedInto, &l.CreatedBy, &l.CreatedAt, &l.Uses)
		return l, err
	})
	if out == nil {
		out = []Label{}
	}
	return out, err
}

// Suggestion is a label offered for something bought: one of the
// business's own (LabelID), or a new name.
type Suggestion struct {
	Name    string     `json:"name"`
	LabelID *uuid.UUID `json:"label_id,omitempty"`
}

// Suggest is what to offer for an item (§5.4: the business's past labels
// first, then a suggestion from the item's name and the supplier's
// category), at most n, deterministic:
//
//  1. its own labels sharing a word with the item's name or the category
//     (without case), the most used first;
//  2. then its other labels, the most used first;
//  3. then one new name, when no label of the business has it already:
//     the supplier's category when given, else the last word of the
//     item's name ("Singer sewing machine" is Machine), with a capital.
//
// list is ListTx's.
func Suggest(list []Label, item, category string, n int) []Suggestion {
	if n <= 0 {
		return []Suggestion{}
	}
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(item+" "+category), notWord) {
		words[w] = true
	}
	shares := func(l Label) bool {
		for _, w := range strings.FieldsFunc(strings.ToLower(l.Name), notWord) {
			if words[w] {
				return true
			}
		}
		return false
	}
	// list is the most used first already; a stable sort keeps that within
	// each half.
	ordered := append([]Label(nil), list...)
	sort.SliceStable(ordered, func(i, j int) bool { return shares(ordered[i]) && !shares(ordered[j]) })
	out := []Suggestion{}
	have := map[string]bool{}
	for _, l := range ordered {
		have[strings.ToLower(l.Name)] = true
		if len(out) < n {
			out = append(out, Suggestion{Name: l.Name, LabelID: &l.ID})
		}
	}
	fresh := NameFrom(category)
	if fresh == "" {
		if f := strings.FieldsFunc(item, notWord); len(f) > 0 {
			fresh = NameFrom(f[len(f)-1])
		}
	}
	if fresh != "" && !have[strings.ToLower(fresh)] {
		if len(out) == n {
			out = out[:n-1]
		}
		out = append(out, Suggestion{Name: fresh})
	}
	return out
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// RenameTx renames a label. A name another label has (without case) is
// refused: merge them instead. A merged label is not renamed: the label
// it was merged into is the one reports show. Money-log entries keep the
// name they were made with as their category, and are never changed.
func RenameTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, name string) (Label, error) {
	name, err := CleanName(name)
	if err != nil {
		return Label{}, err
	}
	l, err := scanLabel(tx.QueryRow(ctx, `SELECT `+labelCols+` FROM spend_labels WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, id))
	if err != nil {
		return l, err
	}
	if l.MergedInto != nil {
		return l, fmt.Errorf("%w: %s is merged into another label; rename that one", ErrLabel, l.Name)
	}
	if l.Name == name {
		return l, nil
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = $1 AND id <> $2 AND lower(name) = lower($3))`, tenant, id, name).Scan(&taken); err != nil {
		return l, err
	}
	if taken {
		return l, fmt.Errorf("%w: there is a label called %s already; merge the two instead", ErrLabel, name)
	}
	if _, err := tx.Exec(ctx, `UPDATE spend_labels SET name = $3 WHERE tenant_id = $1 AND id = $2`, tenant, id, name); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return l, fmt.Errorf("%w: there is a label called %s already; merge the two instead", ErrLabel, name)
		}
		return l, err
	}
	l.Name = name
	return l, nil
}

// MergeTx merges a label into another, which is the one kept: reports show
// all of the merged label's spending under it from now on. A label
// merged into one that is itself merged goes to the label that one is
// kept as, and the labels merged into the label being merged move first,
// so a merge stays one step (the database refuses chains). Nothing in the
// money log changes.
func MergeTx(ctx context.Context, tx pgx.Tx, tenant, id, into uuid.UUID) (Label, error) {
	// Lock both, in id order, so two merges at once wait for each other.
	rows, err := tx.Query(ctx, `SELECT `+labelCols+` FROM spend_labels WHERE tenant_id = $1 AND id = ANY ($2) ORDER BY id FOR UPDATE`, tenant, []uuid.UUID{id, into})
	if err != nil {
		return Label{}, err
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Label, error) { return scanLabel(r) })
	if err != nil {
		return Label{}, err
	}
	var l, k Label
	for _, x := range got {
		if x.ID == id {
			l = x
		}
		if x.ID == into {
			k = x
		}
	}
	if l.ID == uuid.Nil || k.ID == uuid.Nil {
		return l, ErrNoLabel
	}
	if k.MergedInto != nil {
		if k, err = KeptTx(ctx, tx, tenant, into); err != nil {
			return l, err
		}
	}
	if k.ID == l.ID || (l.MergedInto != nil && *l.MergedInto == k.ID) {
		if k.ID == l.ID {
			return l, fmt.Errorf("%w: a label is not merged into itself", ErrLabel)
		}
		return l, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE spend_labels SET merged_into = $3 WHERE tenant_id = $1 AND merged_into = $2`, tenant, l.ID, k.ID); err != nil {
		return l, err
	}
	if _, err := tx.Exec(ctx, `UPDATE spend_labels SET merged_into = $3 WHERE tenant_id = $1 AND id = $2`, tenant, l.ID, k.ID); err != nil {
		return l, err
	}
	l.MergedInto = &k.ID
	return l, nil
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

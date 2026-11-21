package saved

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Importing saved things from a file (§9.6, saved objects S3): a CSV a
// business keeps of its suppliers' accounts, its branches, its people, as
// students and products are imported. Its first line names the columns;
// each other line is one saved thing, checked as one typed in is. A file
// with any line that cannot be taken saves nothing: each line and why is
// said, so it can be put right and sent again.

// Import limits.
const (
	// MaxImportBytes is the largest file taken.
	MaxImportBytes = 256 << 10
	// MaxImportRows is the most things one file saves: a bank account is
	// checked with its bank as it is saved, one by one.
	MaxImportRows = 200
)

// ImportRow is one line of a file, read as a saved thing.
type ImportRow struct {
	// Line is its line in the file, the column names' being 1.
	Line  int    `json:"line"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Hint is what the Saved list will show of it (masked).
	Hint string `json:"hint"`
	// Details are its fields, for every kind but a bank account.
	Details map[string]any `json:"-"`
	// Account is a bank account's.
	Account BankAccount `json:"-"`
}

// ImportProblem is a line that cannot be taken, and why.
type ImportProblem struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ImportError is a file that cannot be taken: what is missing from it, or
// its lines' problems.
type ImportError struct {
	Message  string          `json:"error"`
	Problems []ImportProblem `json:"problems,omitempty"`
	Total    int             `json:"total"`
}

func (e *ImportError) Error() string { return "saved: import: " + e.Message }

// ImportHash is a file's hash, which a plan is confirmed by: the file
// saved is the file shown.
func ImportHash(file string) string {
	h := sha256.Sum256([]byte(file))
	return hex.EncodeToString(h[:])
}

// importColumns are the names a column may go by, and the field it is.
var importColumns = map[string]string{
	"name": "name", "@name": "name", "short_name": "name",
	"title": "title", "full_name": "title", "business": "title", "label": "title",
	"kind": "kind", "type_of_thing": "kind",
	"phone": "phone", "phone_number": "phone", "mobile": "phone",
	"email": "email", "email_address": "email",
	"address": "address", "birthday": "birthday", "date_of_birth": "birthday",
	"street": "line", "line": "line", "street_and_number": "line",
	"area": "area", "town": "area", "city": "area",
	"landmark": "landmark",
	"bank":     "bank_name", "bank_name": "bank_name",
	"account_number": "account_number", "account_no": "account_number", "account": "account_number", "nuban": "account_number",
	"note": "note", "notes": "note",
	"text": "body", "body": "body", "words": "body",
	"provider": "provider", "provider_type": "type", "type": "type",
	"number": "number", "meter": "number", "smartcard": "number",
	"name_on_it": "name_on_it", "name_on_account": "name_on_it",
}

// importKinds are the words a kind column may hold.
var importKinds = map[string]string{
	"bank_account": KindBankAccount, "bank": KindBankAccount, "account": KindBankAccount,
	"person": KindPerson, "people": KindPerson, "contact": KindPerson, "customer": KindPerson, "supplier": KindPerson,
	"place": KindPlace, "address": KindPlace, "branch": KindPlace, "location": KindPlace,
	"text": KindText, "note": KindText,
	"provider_account": KindProviderAccount, "provider": KindProviderAccount, "utility": KindProviderAccount,
}

// columnKey is a column's name as importColumns knows it: lower case,
// spaces and "-" as "_".
func columnKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\uFEFF")))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return '_'
		}
		return r
	}, s)
}

// ReadImport reads a file into the saved things it holds; an *ImportError
// when any line cannot be taken (or the file as a whole), with every
// line's problem. Names are the file's own (or made from the title) and
// unique in it; whether one is taken already is the caller's to say.
func ReadImport(file string) ([]ImportRow, error) {
	if len(file) > MaxImportBytes {
		return nil, &ImportError{Message: fmt.Sprintf("The file is too big: up to %d KB.", MaxImportBytes>>10)}
	}
	r := csv.NewReader(strings.NewReader(file))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	head, err := r.Read()
	if err != nil {
		return nil, &ImportError{Message: "The file is empty or not a CSV: save it from the spreadsheet as CSV."}
	}
	cols := make([]string, len(head))
	known := false
	for i, h := range head {
		cols[i] = importColumns[columnKey(h)]
		known = known || cols[i] != ""
	}
	if !known || !hasColumn(cols, "title") && !hasColumn(cols, "name") {
		return nil, &ImportError{Message: "The first line names the columns: at least title (or name), then what each thing holds, such as phone, bank and account_number, street and area."}
	}
	var rows []ImportRow
	var problems []ImportProblem
	names := map[string]int{}
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			problems = append(problems, ImportProblem{line, "This line could not be read: check its quotes."})
			continue
		}
		v := map[string]string{}
		blank := true
		for i, cell := range rec {
			if i < len(cols) && cols[i] != "" && strings.TrimSpace(cell) != "" {
				v[cols[i]] = strings.TrimSpace(cell)
				blank = false
			}
		}
		if blank {
			continue
		}
		if len(rows)+len(problems) >= MaxImportRows {
			return nil, &ImportError{Message: fmt.Sprintf("A file saves up to %d things: split it in two.", MaxImportRows), Total: len(rows) + len(problems) + 1}
		}
		x, err := importRow(line, v)
		if err != nil {
			problems = append(problems, ImportProblem{line, Plain(err)})
			continue
		}
		if at, ok := names[x.Name]; ok {
			problems = append(problems, ImportProblem{line, fmt.Sprintf("@%s is on line %d too: give each its own name.", x.Name, at)})
			continue
		}
		names[x.Name] = line
		rows = append(rows, x)
	}
	total := len(rows) + len(problems)
	switch {
	case len(problems) > 0:
		return nil, &ImportError{Message: "Some lines cannot be saved: put them right and send the file again.", Problems: problems, Total: total}
	case total == 0:
		return nil, &ImportError{Message: "The file has no things to save under its column names."}
	}
	return rows, nil
}

func hasColumn(cols []string, c string) bool {
	for _, x := range cols {
		if x == c {
			return true
		}
	}
	return false
}

// importRow reads one line's values into a saved thing.
func importRow(line int, v map[string]string) (ImportRow, error) {
	x := ImportRow{Line: line}
	title := v["title"]
	if title == "" {
		title = v["name"]
	}
	var err error
	if x.Title, err = Title(title); err != nil {
		return x, err
	}
	n := v["name"]
	if n == "" {
		n = NameFor(x.Title)
	}
	if x.Name, err = Name(n); err != nil {
		return x, err
	}
	x.Kind = importKind(v)
	if x.Kind == "" {
		return x, invalid("Say what it is in a kind column: bank_account, person, place, text or provider_account.")
	}
	if x.Kind == KindBankAccount {
		x.Account, err = BankAccount{BankName: v["bank_name"], AccountNumber: v["account_number"], Note: v["note"]}.Clean()
		if err != nil {
			return x, err
		}
		x.Hint = x.Account.BankName + " ••" + Last4(x.Account.AccountNumber)
		return x, nil
	}
	in := map[string]any{}
	for k, s := range v {
		switch k {
		case "name", "title", "kind", "bank_name", "account_number":
		default:
			in[k] = s
		}
	}
	if x.Kind == KindProviderAccount {
		if t, ok := in["type"].(string); ok {
			in["type"] = strings.ToLower(t)
		}
	}
	c, err := Clean(x.Kind, in)
	if err != nil {
		return x, err
	}
	x.Details, x.Hint = c.Details, c.MaskedHint(x.Kind)
	return x, nil
}

// importKind is the line's kind: its kind column, else what it holds.
func importKind(v map[string]string) string {
	if k := v["kind"]; k != "" {
		return importKinds[columnKey(k)]
	}
	switch {
	case v["account_number"] != "":
		return KindBankAccount
	case v["provider"] != "" || v["number"] != "":
		return KindProviderAccount
	case v["line"] != "":
		return KindPlace
	case v["body"] != "":
		return KindText
	case v["phone"] != "" || v["email"] != "" || v["address"] != "" || v["birthday"] != "":
		return KindPerson
	}
	return ""
}

// NameFor is a name made from a title: its words in lower case joined by
// "-", as long as a name may be; "" when none fits.
func NameFor(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	n := b.String()
	if len(n) > 32 {
		n = strings.TrimRight(n[:32], "-")
	}
	if _, err := Name(n); err != nil {
		return ""
	}
	return n
}

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

	"golang.org/x/text/unicode/norm"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// Importing saved things from a file (§9.6, saved objects S3): a CSV a
// business keeps of its suppliers' accounts, its branches, its people, as
// students and products are imported. Its first line names the columns;
// each other line is one saved thing, checked as one typed in is. A file
// with any line that cannot be taken saves nothing: each line and why is
// said, so it can be put right and sent again.
//
// Files come as spreadsheets save them: with Excel's byte-order mark,
// separated by commas, semicolons (where a comma is the decimal point) or
// tabs, with column names as people write them ("Account No.", "Phone #",
// "E-mail").

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
	// Line is its line in the file, the column names' being 1 (a cell
	// over several lines counts each).
	Line  int    `json:"line"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Hint is what the Saved list will show of it (masked).
	Hint string `json:"hint"`
	// Warning is what the owner should know before saving it, which does
	// not keep it from being saved (a bank Taskiem does not know by that
	// name); the caller's to set.
	Warning string `json:"warning,omitempty"`
	// Skip is a thing saved already as this kind (from an earlier send of
	// the file): not saved again. The caller's to set.
	Skip bool `json:"skip,omitempty"`
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

// importColumns are the names a column may go by (as columnKey makes
// them), and the field it is. "name" is the @name only beside a title
// column; alone, it holds the things' own names and is their title.
var importColumns = map[string]string{
	"name": "name", "@name": "name", "short_name": "name",
	"title": "title", "full_name": "title", "business": "title", "label": "title",
	"company": "title", "supplier_name": "title", "customer_name": "title", "branch": "title", "branch_name": "title",
	"kind": "kind", "type_of_thing": "kind",
	"phone": "phone", "phone_number": "phone", "mobile": "phone", "phone_no": "phone", "tel": "phone",
	"telephone": "phone", "mobile_number": "phone", "whatsapp": "phone",
	"email": "email", "email_address": "email", "e_mail": "email",
	"address": "address", "location": "address", "address_line": "address",
	"birthday": "birthday", "date_of_birth": "birthday",
	"street": "line", "line": "line", "street_and_number": "line",
	"area": "area", "town": "area", "city": "area", "state": "area",
	"landmark": "landmark",
	"bank":     "bank_name", "bank_name": "bank_name",
	"account_number": "account_number", "account_no": "account_number", "account": "account_number", "nuban": "account_number",
	"acct_no": "account_number", "account_num": "account_number", "bank_account_number": "account_number",
	"note": "note", "notes": "note",
	"text": "body", "body": "body", "words": "body",
	"provider": "provider", "provider_type": "type", "type": "type",
	"number": "number", "meter": "number", "smartcard": "number",
	"name_on_it": "name_on_it", "name_on_account": "name_on_it",
}

// importKinds are the words a kind column (or a type column without a
// provider) may hold.
var importKinds = map[string]string{
	"bank_account": KindBankAccount, "bank": KindBankAccount, "account": KindBankAccount,
	"person": KindPerson, "people": KindPerson, "contact": KindPerson, "customer": KindPerson, "supplier": KindPerson,
	"staff": KindPerson, "vendor": KindPerson,
	"place": KindPlace, "address": KindPlace, "branch": KindPlace, "location": KindPlace,
	"shop": KindPlace, "store": KindPlace, "office": KindPlace, "warehouse": KindPlace,
	"text": KindText, "note": KindText,
	"provider_account": KindProviderAccount, "provider": KindProviderAccount, "utility": KindProviderAccount,
}

// columnKey is a column's name as importColumns knows it: lower case,
// the punctuation people put in names (". # / ( ) : '") left out, spaces
// and "-" as one "_", none at either end. "Account No." is account_no,
// "Phone #" phone, "E-mail" e_mail.
func columnKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\uFEFF")))
	s = strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(".#/():'’", r):
			return -1
		case unicode.IsSpace(r) || r == '-' || r == '_':
			return '_'
		}
		return r
	}, s)
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return strings.Trim(s, "_")
}

// separator is the file's separator, read from its first line: a
// semicolon or a tab when the line has more of it than commas (outside
// quotes), else a comma.
func separator(file string) rune {
	n := map[rune]int{}
	quoted := false
head:
	for _, r := range file {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '\n':
			break head
		default:
			n[r]++
		}
	}
	switch {
	case n[';'] > n[','] && n[';'] >= n['\t']:
		return ';'
	case n['\t'] > n[',']:
		return '\t'
	}
	return ','
}

// ReadImport reads a file into the saved things it holds; an *ImportError
// when any line cannot be taken (or the file as a whole), with every
// line's problem. Names are the file's own (or made from the title) and
// unique in it; whether one is taken already is the caller's to say.
func ReadImport(file string) ([]ImportRow, error) {
	if len(file) > MaxImportBytes {
		return nil, &ImportError{Message: fmt.Sprintf("The file is too big: up to %d KB.", MaxImportBytes>>10)}
	}
	// Excel saves "CSV UTF-8" with a byte-order mark, which would keep a
	// quoted first column name from being read.
	file = strings.TrimPrefix(file, "\uFEFF")
	r := csv.NewReader(strings.NewReader(file))
	r.Comma = separator(file)
	r.FieldsPerRecord = -1
	// Leading spaces are trimmed but for a tab file, where trimming them
	// would eat the tabs of empty cells.
	r.TrimLeadingSpace = r.Comma != '\t'
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
	// A "name" column with no title column beside it holds the things'
	// names as people write them ("Mrs. Ade", "Mama Ngozi & Sons"): it is
	// their title, and each @name is made from it.
	if !hasColumn(cols, "title") {
		for i, h := range head {
			if columnKey(h) == "name" {
				cols[i] = "title"
			}
		}
	}
	var rows []ImportRow
	var problems []ImportProblem
	names := map[string]int{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			line := 0
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				line = pe.StartLine
			}
			problems = append(problems, ImportProblem{line, "This line could not be read: check its quotes."})
			continue
		}
		// The line the record starts on in the file: blank lines and cells
		// over several lines are counted as the spreadsheet shows them.
		line, _ := r.FieldPos(0)
		v := map[string]string{}
		blank := true
		for i, cell := range rec {
			if i < len(cols) && cols[i] != "" && strings.TrimSpace(cell) != "" && v[cols[i]] == "" {
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
		// As the Saved list shows it: the bank's short name.
		x.Hint = x.Account.Hint()
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
	switch x.Kind {
	case KindProviderAccount:
		if t, ok := in["type"].(string); ok {
			in["type"] = strings.ToLower(t)
		}
	case KindPerson, KindPlace:
		// A "number" on a person's or a place's line is their phone.
		if v["phone"] == "" && isPhone(v["number"]) {
			in["phone"] = v["number"]
		}
		if x.Kind == KindPlace && v["line"] == "" {
			in["line"] = v["address"]
		}
		// A person keeps one address: the street and the area together.
		if x.Kind == KindPerson && v["address"] == "" {
			in["address"] = strings.Trim(v["line"]+", "+v["area"], ", ")
		}
	}
	c, err := Clean(x.Kind, in)
	if err != nil {
		return x, err
	}
	x.Details, x.Hint = c.Details, c.MaskedHint(x.Kind)
	return x, nil
}

// importKind is the line's kind: its kind column (or a type column that
// holds a kind word and no provider beside it, as "supplier" or
// "branch"), else what it holds. An account number makes a bank account,
// a provider a provider's account (a "number" alone does not: it is a
// person's or a place's phone when it is one), a body a text, a phone, an
// email or a birthday a person, and an address, a street or an area with
// none of those a place.
func importKind(v map[string]string) string {
	k := v["kind"]
	if k == "" && v["provider"] == "" && importKinds[columnKey(v["type"])] != "" {
		k = v["type"]
	}
	if k != "" {
		return importKinds[columnKey(k)]
	}
	switch {
	case v["account_number"] != "":
		return KindBankAccount
	case v["provider"] != "":
		return KindProviderAccount
	case v["body"] != "":
		return KindText
	case v["phone"] != "" || v["email"] != "" || v["birthday"] != "":
		return KindPerson
	case v["address"] != "" || v["line"] != "" || v["area"] != "":
		return KindPlace
	case isPhone(v["number"]):
		return KindPerson
	}
	return ""
}

// isPhone is a value that is a phone number.
func isPhone(s string) bool {
	if s == "" {
		return false
	}
	_, err := phonenum.Normalise(s)
	return err == nil
}

// NameFor is a name made from a title, by this rule (the web's
// savedNameFor and the app's match it, so a name offered is the name
// saved):
//
//  1. accents are taken off: the title decomposed (Unicode NFD) and its
//     combining marks dropped, so "Ọlá" is "Ola" and "Ìfẹ́" "Ife";
//  2. it is lower-cased;
//  3. ASCII letters and digits are kept, and each run of anything else
//     (spaces, punctuation, letters with no ASCII form) is one "-";
//  4. "-" is trimmed from both ends;
//  5. it is cut to 32 bytes and "-" trimmed from its end again.
//
// It is "" when what is left is shorter than 2.
func NameFor(title string) string {
	var plain strings.Builder
	for _, r := range norm.NFD.String(title) {
		if !unicode.Is(unicode.Mn, r) {
			plain.WriteRune(r)
		}
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(plain.String()) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
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
	if len(n) < 2 {
		return ""
	}
	return n
}

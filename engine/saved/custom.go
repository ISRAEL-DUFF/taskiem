package saved

import (
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// Kinds an owner makes (saved-objects §4.2; S1 part 5): when none of the
// kinds Taskiem knows fits, a person or a business makes one ("Car",
// "Generator", "Tenant") with its fields. Each field has a label, a type,
// and says whether it is required, sensitive (sealed, masked in lists,
// shown in full only on a fresh unlock, asked before sharing) or never
// shared (kept out of every card). A kind can change later: objects keep
// what they had in the fields kept, new fields start empty, and a field
// removed takes its values with it. An object of a made-up kind
// is of kind custom, its kind's id beside it.

// KindCustom is an object of a kind its owner made.
const KindCustom = "custom"

// The limits (§8): 40 made-up kinds an owner, 30 fields a kind.
const (
	MaxCustomKinds = 40
	MaxFields      = 30
	MaxKindName    = 40
	MaxLabel       = 60
	MaxOptions     = 30
)

// The types a made-up kind's field may have: a place is its address in one
// line; a bank account its bank and 10-digit number (always sensitive); a
// file a photo or document of the owner's (its upload's id, the file kept
// apart).
var FieldTypes = []string{"text", "number", "money", "date", "phone", "email", "choice", "place", "bank_account", "file"}

// Field is one field of a made-up kind.
type Field struct {
	// Key is how the field's value is kept: made from its label once, and
	// kept when the label changes, so objects keep what they had.
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	Required   bool     `json:"required"`
	Sensitive  bool     `json:"sensitive"`
	NeverShare bool     `json:"never_share"`
	Options    []string `json:"options,omitempty"`
}

var fieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// keyOf is a field's key made from its label: "Plate number" is
// plate_number.
func keyOf(label string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r == '\'' || r == '’':
			// "Vet's phone" is vets_phone.
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if b.Len() == 0 && r >= '0' && r <= '9' {
				b.WriteString("f_")
			}
			b.WriteRune(r)
			under = false
		case b.Len() > 0 && !under:
			b.WriteByte('_')
			under = true
		}
	}
	k := strings.TrimRight(b.String(), "_")
	if len(k) > 40 {
		k = strings.TrimRight(k[:40], "_")
	}
	if k == "" {
		k = "field"
	}
	return k
}

// CleanKind checks a made-up kind: its name, and its fields (up to 30,
// each labelled once, of a known type, a choice with its options); a field
// without a key gets one from its label.
func CleanKind(name string, fields []Field) (string, []Field, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", nil, invalid("Give the kind a name, such as Car or Generator.")
	case utf8.RuneCountInString(name) > MaxKindName:
		return "", nil, invalid(fmt.Sprintf("A kind's name is up to %d characters.", MaxKindName))
	case slices.ContainsFunc(Kinds, func(k string) bool { return strings.EqualFold(strings.ReplaceAll(k, "_", " "), name) }):
		return "", nil, invalid("Taskiem has that kind already: choose it when you save one.")
	}
	if len(fields) == 0 {
		return "", nil, invalid("Give the kind at least one field.")
	}
	if len(fields) > MaxFields {
		return "", nil, invalid(fmt.Sprintf("A kind has up to %d fields.", MaxFields))
	}
	out := make([]Field, 0, len(fields))
	labels, keys := map[string]bool{}, map[string]bool{}
	// The keys given (fields kept from before) first, so a new field's key
	// made from its label never takes one of theirs.
	given := map[string]bool{}
	for _, f := range fields {
		if f.Key != "" {
			if given[f.Key] {
				return "", nil, invalid("A field's key is taken or not one Taskiem makes.")
			}
			given[f.Key] = true
		}
	}
	for _, f := range fields {
		f.Label = strings.Join(strings.Fields(f.Label), " ")
		if f.Label == "" {
			return "", nil, invalid("Give each field a label.")
		}
		if utf8.RuneCountInString(f.Label) > MaxLabel {
			return "", nil, invalid(fmt.Sprintf("A field's label is up to %d characters.", MaxLabel))
		}
		if labels[strings.ToLower(f.Label)] {
			return "", nil, invalid(fmt.Sprintf("Two fields are called %s: give each its own label.", f.Label))
		}
		labels[strings.ToLower(f.Label)] = true
		if !slices.Contains(FieldTypes, f.Type) {
			return "", nil, invalid(fmt.Sprintf("Choose what %s is: text, a number, money, a date, a phone, an email, a choice, a place, a bank account or a file.", f.Label))
		}
		if f.Key == "" {
			f.Key = keyOf(f.Label)
			for n := 2; keys[f.Key] || given[f.Key]; n++ {
				f.Key = fmt.Sprintf("%s_%d", keyOf(f.Label), n)
			}
		}
		if !fieldKey.MatchString(f.Key) || keys[f.Key] {
			return "", nil, invalid("A field's key is taken or not one Taskiem makes.")
		}
		keys[f.Key] = true
		if f.Type == "choice" {
			var opts []string
			for _, o := range f.Options {
				o = strings.Join(strings.Fields(o), " ")
				if o != "" && !slices.Contains(opts, o) {
					opts = append(opts, o)
				}
			}
			if len(opts) < 2 || len(opts) > MaxOptions {
				return "", nil, invalid(fmt.Sprintf("Give %s from 2 to %d choices.", f.Label, MaxOptions))
			}
			f.Options = opts
		} else {
			f.Options = nil
		}
		// What is never shared is sensitive too, and an account number
		// always is: sealed and masked.
		if f.NeverShare || f.Type == "bank_account" {
			f.Sensitive = true
		}
		// A file is shown only to whoever reaches the saved thing; it is
		// not sealed with the values (the file is kept apart).
		if f.Type == "file" {
			f.Sensitive = false
		}
		out = append(out, f)
	}
	return name, out, nil
}

var (
	moneyText  = regexp.MustCompile(`^\d+(\.\d{1,2})?$`)
	numberText = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
	nairaMark  = regexp.MustCompile(`(?i)^(₦|ngn|naira|n(?:\s|\d))`)
	nairaAfter = regexp.MustCompile(`(?i)\s*(naira|ngn)$`)
)

// moneyAmount is an amount in naira as typed ("₦ 25,000", "N25000", "NGN
// 25,000", "25000 naira") without its mark, commas and spaces.
func moneyAmount(v string) string {
	v = strings.TrimSpace(v)
	if m := nairaMark.FindString(v); m != "" {
		v = strings.TrimPrefix(v, strings.TrimRight(m, "0123456789 \t"))
	}
	v = nairaAfter.ReplaceAllString(v, "")
	return strings.NewReplacer(",", "", " ", "").Replace(v)
}

// cleanValue checks one field's value as its type takes it.
func cleanValue(f Field, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch f.Type {
	case "text":
		if utf8.RuneCountInString(v) > MaxLine {
			return "", invalid(fmt.Sprintf("%s is up to %d characters.", f.Label, MaxLine))
		}
		return v, nil
	case "number":
		n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || !numberText.MatchString(strings.ReplaceAll(v, ",", "")) {
			return "", invalid(f.Label + " should be a number.")
		}
		return strconv.FormatFloat(n, 'f', -1, 64), nil
	case "money":
		m := moneyAmount(v)
		if !moneyText.MatchString(m) {
			return "", invalid(f.Label + " should be an amount in naira, such as 25,000.")
		}
		return m, nil
	case "date":
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			return "", invalid(f.Label + " should be a date.")
		}
		return v, nil
	case "phone":
		p, err := phonenum.Normalise(v)
		if err != nil {
			return "", invalid(f.Label + " does not look like a phone number.")
		}
		return p, nil
	case "email":
		a, err := mail.ParseAddress(v)
		if err != nil || a.Address != v {
			return "", invalid(f.Label + " does not look like an email address.")
		}
		return strings.ToLower(v), nil
	case "choice":
		if !slices.Contains(f.Options, v) {
			return "", invalid(fmt.Sprintf("Choose %s from its list.", f.Label))
		}
		return v, nil
	case "place":
		v = strings.Join(strings.Fields(v), " ")
		if utf8.RuneCountInString(v) > MaxLine {
			return "", invalid(fmt.Sprintf("%s is up to %d characters.", f.Label, MaxLine))
		}
		return v, nil
	case "bank_account":
		bank, number := SplitBankValue(v)
		if bank == "" || len(number) != 10 {
			return "", invalid(f.Label + " should be a bank and its 10-digit account number.")
		}
		return bank + " · " + number, nil
	case "file":
		if _, err := uuid.Parse(v); err != nil {
			return "", invalid("Choose the photo or document for " + f.Label + ".")
		}
		return v, nil
	}
	return "", invalid("That field's type is not known.")
}

// CleanValues checks an object's values for its kind's fields: those in
// in (a string, or nil to remove it) over those it had (was, sensitive
// ones in full); every required field filled. Values of fields the kind no
// longer has are let go (KeepFields), and one file is in one field only.
func CleanValues(fields []Field, in map[string]any, was map[string]string) (map[string]string, error) {
	out := KeepFields(fields, was)
	byKey := map[string]Field{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for k, raw := range in {
		f, ok := byKey[k]
		if !ok {
			return nil, invalid("That kind has no field " + k + ".")
		}
		if raw == nil {
			delete(out, k)
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return nil, invalid(f.Label + " should be words.")
		}
		if strings.TrimSpace(s) == "" {
			delete(out, k)
			continue
		}
		v, err := cleanValue(f, s)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	files := map[string]bool{}
	for _, f := range fields {
		if f.Required && out[f.Key] == "" {
			return nil, invalid(f.Label + " is needed.")
		}
		if f.Type == "file" && out[f.Key] != "" {
			if files[out[f.Key]] {
				return nil, invalid("One photo or document is in two fields: choose it for one.")
			}
			files[out[f.Key]] = true
		}
	}
	return out, nil
}

// KeepFields are the values an object keeps when its kind is as fields
// say: those of fields it still has, each still good for its field's type
// (a field changed from text to a date keeps a value only if it is a
// date); the rest are let go, never kept unseen.
func KeepFields(fields []Field, was map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range fields {
		v, ok := was[f.Key]
		if !ok || v == "" {
			continue
		}
		if c, err := cleanValue(f, v); err == nil {
			out[f.Key] = c
		}
	}
	return out
}

// Desensitised are the fields of a kind's change that show what was kept
// hidden: made not sensitive, made shareable, or a sensitive one given
// another type; by key, with its label.
func Desensitised(was, now []Field) map[string]string {
	old := map[string]Field{}
	for _, f := range was {
		old[f.Key] = f
	}
	out := map[string]string{}
	for _, f := range now {
		o, ok := old[f.Key]
		if !ok {
			continue
		}
		if (o.Sensitive && !f.Sensitive) || (o.NeverShare && !f.NeverShare) || (o.Sensitive && o.Type != f.Type) {
			out[f.Key] = f.Label
		}
	}
	return out
}

var lastDigits = regexp.MustCompile(`(\d[\d \-]*\d)\s*$`)

// SplitBankValue is a bank account field's bank and account number, from
// "Zenith Bank · 0123456789" (or the number after the bank, spaced).
func SplitBankValue(v string) (bank, number string) {
	m := lastDigits.FindStringSubmatchIndex(v)
	if m == nil {
		return "", ""
	}
	number = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, v[m[2]:m[3]])
	bank = strings.Trim(strings.TrimSpace(v[:m[2]]), "·,:- ")
	return strings.Join(strings.Fields(bank), " "), number
}

// MaskValue is a sensitive value as lists show it: its last four, or dots.
func MaskValue(v string) string {
	r := []rune(v)
	if len(r) <= 6 {
		return "••••"
	}
	return "••" + string(r[len(r)-4:])
}

// SplitValues parts an object's values: those kept as they are (plain),
// those sealed (secret), and the sealed ones masked as lists show them.
// Values of fields the kind no longer has are left out: a sensitive one is
// never made plain by its field being removed.
func SplitValues(fields []Field, all map[string]string) (plain, secret, masked map[string]string) {
	plain, secret, masked = map[string]string{}, map[string]string{}, map[string]string{}
	sensitive, has := map[string]bool{}, map[string]bool{}
	for _, f := range fields {
		sensitive[f.Key], has[f.Key] = f.Sensitive, true
	}
	bankField := map[string]bool{}
	for _, f := range fields {
		bankField[f.Key] = f.Type == "bank_account"
	}
	for k, v := range all {
		if !has[k] {
			continue
		}
		if sensitive[k] {
			secret[k], masked[k] = v, MaskValue(v)
			if bankField[k] {
				// "GTBank ••6789": the bank stays readable.
				if bank, number := SplitBankValue(v); bank != "" {
					masked[k] = ShortBank(bank) + " " + Masked(number)
				}
			}
		} else {
			plain[k] = v
		}
	}
	return plain, secret, masked
}

// ShownValue is a value as a person reads it: money as naira, a date as a
// day.
func ShownValue(f Field, v string) string {
	switch f.Type {
	case "money":
		return "₦" + groupThousands(v)
	case "date":
		if t, err := time.Parse(time.DateOnly, v); err == nil {
			return t.Format("2 Jan 2006")
		}
	}
	return v
}

func groupThousands(v string) string {
	whole, frac, _ := strings.Cut(v, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteString("." + frac)
	}
	return b.String()
}

// CustomHint is what a list shows for an object of a made-up kind: its
// first two filled fields, a sensitive one masked, a file by its name.
func CustomHint(kindName string, fields []Field, all map[string]string, files map[string]FileMeta) string {
	_, _, masked := SplitValues(fields, all)
	parts := []string{}
	for _, f := range fields {
		v := all[f.Key]
		if v == "" {
			continue
		}
		switch {
		case f.Type == "file":
			v = files[f.Key].Name
			if v == "" {
				continue
			}
		case f.Sensitive:
			v = masked[f.Key]
		default:
			v = ShownValue(f, v)
		}
		parts = append(parts, v)
		if len(parts) == 2 {
			break
		}
	}
	h := strings.Join(parts, " · ")
	if h == "" {
		h = kindName
	}
	if utf8.RuneCountInString(h) > 120 {
		h = string([]rune(h)[:119]) + "…"
	}
	return h
}

// MakeCustomCard is the card for an object of a made-up kind: each filled
// field but those never shared, sensitive ones in full (the sender says
// yes first).
func MakeCustomCard(kindName, name, title string, fields []Field, all map[string]string, files map[string]FileMeta) Card {
	c := Card{Kind: KindCustom, KindName: kindName, Title: title, Name: name, Lines: []CardLine{}, Details: map[string]any{}}
	for _, f := range fields {
		v := all[f.Key]
		if v == "" || f.NeverShare {
			continue
		}
		if f.Type == "file" {
			// A file goes by its name on a card; it is sent as itself.
			if v = files[f.Key].Name; v == "" {
				continue
			}
		} else {
			v = ShownValue(f, v)
		}
		c.Lines = append(c.Lines, CardLine{Label: f.Label, Value: v, Sensitive: f.Sensitive})
		if f.Sensitive {
			c.Sensitive = true
		}
	}
	// Save this keeps a made-up kind's card as a text of its lines: the
	// receiver has no such kind.
	body := []string{}
	for _, l := range c.Lines {
		body = append(body, l.Label+": "+l.Value)
	}
	c.Details["body"] = strings.Join(body, "\n")
	return c
}

// Starter is a made-up kind to start from.
type Starter struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

// Starters are the kinds offered to copy (§4.2): a car, a generator, a pet,
// a medical card.
var Starters = []Starter{
	{Name: "Car", Fields: []Field{
		{Key: "plate_number", Label: "Plate number", Type: "text", Required: true},
		{Key: "make_and_model", Label: "Make and model", Type: "text"},
		{Key: "colour", Label: "Colour", Type: "text"},
		{Key: "insurance_due", Label: "Insurance due", Type: "date"},
		{Key: "papers_due", Label: "Papers due", Type: "date"},
		{Key: "chassis_number", Label: "Chassis number", Type: "text", Sensitive: true},
	}},
	{Name: "Generator", Fields: []Field{
		{Key: "make", Label: "Make", Type: "text", Required: true},
		{Key: "size_kva", Label: "Size (kVA)", Type: "number"},
		{Key: "fuel", Label: "Fuel", Type: "choice", Options: []string{"Petrol", "Diesel", "Gas"}},
		{Key: "last_service", Label: "Last service", Type: "date"},
		{Key: "next_service", Label: "Next service", Type: "date"},
		{Key: "technician_phone", Label: "Technician's phone", Type: "phone"},
	}},
	{Name: "Pet", Fields: []Field{
		{Key: "animal", Label: "Animal", Type: "choice", Options: []string{"Dog", "Cat", "Bird", "Other"}},
		{Key: "breed", Label: "Breed", Type: "text"},
		{Key: "birthday", Label: "Birthday", Type: "date"},
		{Key: "next_vaccine", Label: "Next vaccine", Type: "date"},
		{Key: "vet_phone", Label: "Vet's phone", Type: "phone"},
	}},
	{Name: "Medical card", Fields: []Field{
		{Key: "provider", Label: "Health insurer (HMO)", Type: "text", Required: true},
		{Key: "member_number", Label: "Member number", Type: "text", Required: true, Sensitive: true},
		{Key: "plan", Label: "Plan", Type: "text"},
		{Key: "hospital", Label: "Hospital", Type: "text"},
		{Key: "expires", Label: "Expires", Type: "date"},
		{Key: "insurer_phone", Label: "Insurer's phone", Type: "phone"},
	}},
}

// Note is an owner's note as kept: its spaces folded, up to MaxNote.
func Note(v string) (string, error) {
	return field(map[string]any{"note": v}, "note", "note", MaxNote, false)
}

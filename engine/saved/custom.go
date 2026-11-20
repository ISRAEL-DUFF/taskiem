package saved

import (
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// Kinds an owner makes (saved-objects §4.2; S1 part 5): when none of the
// kinds Taskiem knows fits, a person or a business makes one ("Car",
// "Generator", "Tenant") with its fields. Each field has a label, a type,
// and says whether it is required, sensitive (sealed, masked in lists,
// shown in full only on a fresh unlock, asked before sharing) or never
// shared (kept out of every card). A kind can change later: objects keep
// what they had, and new fields start empty. An object of a made-up kind
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

// The types a made-up kind's field may have. Places, bank accounts and
// files as fields come later; a place's words fit a text for now.
var FieldTypes = []string{"text", "number", "money", "date", "phone", "email", "choice"}

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
			return "", nil, invalid(fmt.Sprintf("Choose what %s is: text, a number, money, a date, a phone, an email or a choice.", f.Label))
		}
		if f.Key == "" {
			f.Key = keyOf(f.Label)
			for n := 2; keys[f.Key]; n++ {
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
		// What is never shared is sensitive too: sealed and masked.
		if f.NeverShare {
			f.Sensitive = true
		}
		out = append(out, f)
	}
	return name, out, nil
}

var moneyText = regexp.MustCompile(`^\d+(\.\d{1,2})?$`)

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
		if err != nil {
			return "", invalid(f.Label + " should be a number.")
		}
		return strconv.FormatFloat(n, 'f', -1, 64), nil
	case "money":
		m := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(v), "₦"), ",", "")
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
	}
	return "", invalid("That field's type is not known.")
}

// CleanValues checks an object's values for its kind's fields: those in
// in (a string, or nil to remove it) over those it had (was, sensitive
// ones in full); every required field filled. Values of fields the kind no
// longer has are kept as they were, unseen.
func CleanValues(fields []Field, in map[string]any, was map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range was {
		out[k] = v
	}
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
	for _, f := range fields {
		if f.Required && out[f.Key] == "" {
			return nil, invalid("Give it " + strings.ToLower(f.Label[:1]) + f.Label[1:] + ".")
		}
	}
	return out, nil
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
// Values of fields the kind no longer has are kept plain if they ever
// were, so nothing is lost.
func SplitValues(fields []Field, all map[string]string) (plain, secret, masked map[string]string) {
	plain, secret, masked = map[string]string{}, map[string]string{}, map[string]string{}
	sensitive := map[string]bool{}
	for _, f := range fields {
		sensitive[f.Key] = f.Sensitive
	}
	for k, v := range all {
		if sensitive[k] {
			secret[k], masked[k] = v, MaskValue(v)
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
// first two filled fields, a sensitive one masked.
func CustomHint(kindName string, fields []Field, all map[string]string) string {
	parts := []string{}
	for _, f := range fields {
		v := all[f.Key]
		if v == "" {
			continue
		}
		if f.Sensitive {
			v = MaskValue(v)
		} else {
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
func MakeCustomCard(kindName, name, title string, fields []Field, all map[string]string) Card {
	c := Card{Kind: KindCustom, KindName: kindName, Title: title, Name: name, Details: map[string]any{}}
	for _, f := range fields {
		v := all[f.Key]
		if v == "" || f.NeverShare {
			continue
		}
		c.Lines = append(c.Lines, CardLine{Label: f.Label, Value: ShownValue(f, v)})
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

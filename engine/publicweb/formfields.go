package publicweb

import (
	"fmt"
	"math"
	"mime"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// A hosted form's fields (docs/spec/forms.md 6.2): each schema property
// becomes a widget, and what the browser sends back is parsed into the
// JSON value the run receives, with a message in plain words when it does
// not fit.

// Limits on what a form takes (forms 6.5, 7.3).
const (
	maxAnswersBody = 256 << 10 // a submission without its files
	maxFiles       = 10
	defaultFileMB  = 10
	maxFileMB      = 25
	maxTextField   = 5000 // when the schema sets no maxLength
	maxRadios      = 6
)

// field is one schema property as the form shows it.
type field struct {
	Name     string
	ID       string // the input's id
	Label    string
	Help     string
	Place    string
	Required bool
	// Widget: text, textarea, number, email, tel, url, date, time,
	// datetime, radio, select, checkboxes, checkbox, consent or file.
	Widget  string
	Hidden  bool
	Prefill bool
	Unit    string // ₦ for naira
	Min     string
	Max     string
	Step    string
	MaxLen  int
	Accept  string
	Consent string // the text agreed to
	Options []option
	Error   string
	// Value is what the input shows; Checked a checkbox's state.
	Value   string
	Checked bool
	// Held is a file kept from an earlier sending of the page.
	Held string

	schema map[string]any
	typ    string
	format string
	enum   []any
	items  []any // an array's enum
	pii    string
	maxMB  float64
}

type option struct {
	Value, Label string
	Checked      bool
}

// fields builds the fields of a schema, by name.
func fields(schema map[string]any) map[string]*field {
	props, _ := schema["properties"].(map[string]any)
	req := map[string]bool{}
	if r, ok := schema["required"].([]any); ok {
		for _, x := range r {
			if s, ok := x.(string); ok {
				req[s] = true
			}
		}
	}
	out := map[string]*field{}
	n := 0
	for _, name := range sortedKeys(props) {
		p, _ := props[name].(map[string]any)
		if p == nil {
			continue
		}
		h, _ := p["x-form"].(map[string]any)
		f := &field{Name: name, ID: fmt.Sprintf("f-%d", n), Required: req[name], schema: p}
		n++
		f.typ, _ = p["type"].(string)
		f.format, _ = p["format"].(string)
		f.pii, _ = p["x-pii"].(string)
		f.enum, _ = p["enum"].([]any)
		if it, ok := p["items"].(map[string]any); ok {
			f.items, _ = it["enum"].([]any)
		}
		f.Label = str(h["label"])
		if f.Label == "" {
			f.Label = str(p["title"])
		}
		if f.Label == "" {
			f.Label = humanise(name)
		}
		f.Help = str(h["help"])
		if f.Help == "" {
			f.Help = str(p["description"])
		}
		f.Place = str(h["placeholder"])
		f.Hidden = h["hidden"] == true
		f.Prefill = h["prefill"] == true
		widget := str(h["widget"])
		switch {
		case widget == "consent":
			f.Widget, f.Consent = "consent", str(h["text"])
			if f.Consent == "" {
				f.Consent = f.Label
			}
		case f.format == "file":
			f.Widget = "file"
			f.maxMB = defaultFileMB
			if mb, ok := number(h["max_mb"]); ok && mb > 0 {
				f.maxMB = min(mb, maxFileMB)
			}
			if acc, ok := h["accept"].([]any); ok {
				var list []string
				for _, a := range acc {
					if s := str(a); s != "" {
						list = append(list, s)
					}
				}
				f.Accept = strings.Join(list, ",")
			}
		case f.typ == "array" && len(f.items) > 0:
			f.Widget = "checkboxes"
			f.Options = options(f.items, h)
		case len(f.enum) > 0:
			f.Widget = "select"
			if len(f.enum) <= maxRadios {
				f.Widget = "radio"
			}
			f.Options = options(f.enum, h)
		case f.typ == "boolean":
			f.Widget = "checkbox"
		case f.typ == "integer" || f.typ == "number":
			f.Widget = "number"
			if str(h["unit"]) == "naira" {
				f.Unit = "₦"
			}
			if v, ok := number(p["minimum"]); ok {
				f.Min = fmtNum(v)
			}
			if v, ok := number(p["maximum"]); ok {
				f.Max = fmtNum(v)
			}
			f.Step = "any"
			if f.typ == "integer" {
				f.Step = "1"
			}
		case f.format == "email":
			f.Widget = "email"
		case f.format == "phone-ng" || f.pii == "phone":
			f.Widget = "tel"
			if f.Place == "" {
				f.Place = "0803 123 4567"
			}
		case f.format == "uri":
			f.Widget = "url"
		case f.format == "date":
			f.Widget = "date"
		case f.format == "time":
			f.Widget = "time"
		case f.format == "date-time":
			f.Widget = "datetime"
		case widget == "textarea":
			f.Widget = "textarea"
		default:
			f.Widget = "text"
		}
		if v, ok := number(p["maxLength"]); ok && v > 0 {
			f.MaxLen = int(v)
		}
		out[name] = f
	}
	return out
}

func options(vals []any, h map[string]any) []option {
	labels, _ := h["labels"].(map[string]any)
	out := make([]option, 0, len(vals))
	for _, v := range vals {
		s := enumString(v)
		l := str(labels[s])
		if l == "" {
			l = s
		}
		out = append(out, option{Value: s, Label: l})
	}
	return out
}

func enumString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return fmtNum(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case bool:
		return strconv.FormatBool(n)
	}
	return fmt.Sprint(v)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// humanise makes a field name a label: full_name is "Full name".
func humanise(name string) string {
	s := strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(name))
	s = strings.TrimSuffix(s, " kobo")
	s = strings.TrimSuffix(s, " naira")
	if s == "" {
		return name
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Input is the field's input name: its own under "a.", so no field can
// be mistaken for the form's token, state or buttons.
func (f *field) Input() string { return "a." + f.Name }

// InputType is a plain input's type.
func (f *field) InputType() string {
	switch f.Widget {
	case "email", "tel", "url", "date", "time":
		return f.Widget
	case "datetime":
		return "datetime-local"
	}
	return "text"
}

// isKobo is a naira field whose schema keeps kobo (its name ends _kobo):
// the respondent types naira, the run receives kobo.
func (f *field) isKobo() bool { return f.Unit == "₦" && strings.HasSuffix(f.Name, "_kobo") }

// freeText is a field a respondent types words into, checked for card
// numbers and identifiers.
func (f *field) freeText() bool {
	return (f.Widget == "text" || f.Widget == "textarea") && f.pii != "bvn" && f.pii != "nin"
}

// prefillValue is a value from a link or a run as the input's text.
func (f *field) prefillValue(v any) {
	switch f.Widget {
	case "checkbox", "consent":
		f.Checked = v == true || v == "true" || v == "on" || v == "1"
	case "checkboxes":
		set := map[string]bool{}
		switch l := v.(type) {
		case []any:
			for _, x := range l {
				set[enumString(x)] = true
			}
		case string:
			for _, x := range strings.Split(l, ",") {
				set[strings.TrimSpace(x)] = true
			}
		}
		for i := range f.Options {
			f.Options[i].Checked = set[f.Options[i].Value]
		}
	case "radio", "select":
		s := enumString(v)
		for i := range f.Options {
			f.Options[i].Checked = f.Options[i].Value == s
		}
	case "file":
	case "number":
		if n, ok := number(v); ok {
			if f.isKobo() {
				n /= 100
			}
			f.Value = fmtNum(n)
		} else {
			f.Value = enumString(v)
		}
	default:
		f.Value = enumString(v)
	}
}

// show sets an input from what the browser sent, so a refusal loses
// nothing typed.
func (f *field) show(v url.Values) {
	switch f.Widget {
	case "checkbox", "consent":
		f.Checked = v.Get(f.Input()) != ""
	case "checkboxes", "radio", "select":
		got := map[string]bool{}
		for _, x := range v[f.Input()] {
			got[x] = true
		}
		for i := range f.Options {
			f.Options[i].Checked = got[f.Options[i].Value]
		}
	case "file":
	default:
		f.Value = v.Get(f.Input())
	}
}

// parse reads a field's answer from what the browser sent. present is
// false when the field was left empty; msg says what is wrong.
func (f *field) parse(v url.Values) (val any, present bool, msg string) {
	switch f.Widget {
	case "consent":
		// Consent is always required: the box is what is agreed to.
		if v.Get(f.Input()) == "" {
			return nil, false, "Please tick the box to agree: " + f.Consent
		}
		return true, true, ""
	case "checkbox":
		if v.Get(f.Input()) == "" {
			return false, true, ""
		}
		return true, true, ""
	case "checkboxes":
		var out []any
		for _, x := range v[f.Input()] {
			i := slices.IndexFunc(f.items, func(e any) bool { return enumString(e) == x })
			if i < 0 {
				return nil, false, "Please choose from the list for " + f.Label + "."
			}
			if !slices.Contains(out, f.items[i]) {
				out = append(out, f.items[i])
			}
		}
		if len(out) == 0 {
			if f.Required {
				return nil, false, "Please choose at least one for " + f.Label + "."
			}
			return nil, false, ""
		}
		if n, ok := number(f.schema["minItems"]); ok && float64(len(out)) < n {
			return nil, false, fmt.Sprintf("Please choose at least %s for %s.", fmtNum(n), f.Label)
		}
		if n, ok := number(f.schema["maxItems"]); ok && float64(len(out)) > n {
			return nil, false, fmt.Sprintf("Please choose at most %s for %s.", fmtNum(n), f.Label)
		}
		return out, true, ""
	}
	raw, bad := text(v.Get(f.Input()), max(f.MaxLen, maxTextField), f.Widget == "textarea")
	if bad {
		return nil, false, "Please check " + f.Label + ": it is too long or holds characters a form does not take."
	}
	if raw == "" {
		if f.Required {
			if f.Widget == "radio" || f.Widget == "select" {
				return nil, false, "Please choose an answer for " + f.Label + "."
			}
			return nil, false, "Please fill in " + f.Label + "."
		}
		return nil, false, ""
	}
	switch f.Widget {
	case "radio", "select":
		i := slices.IndexFunc(f.enum, func(e any) bool { return enumString(e) == raw })
		if i < 0 {
			return nil, false, "Please choose from the list for " + f.Label + "."
		}
		return f.enum[i], true, ""
	case "number":
		return f.parseNumber(raw)
	case "email":
		e, err := sender.NormaliseEmail(raw)
		if err != nil {
			return nil, false, "Please check the email address for " + f.Label + "."
		}
		return strings.ToLower(e), true, ""
	case "tel":
		p, err := sender.NormalisePhone(raw, phoneCountries)
		if err != nil {
			return nil, false, "Please enter a Nigerian mobile number for " + f.Label + ", like 0803 123 4567."
		}
		return p, true, ""
	case "url":
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, false, "Please enter a web address for " + f.Label + ", starting https://."
		}
		return raw, true, ""
	case "date":
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return nil, false, "Please enter a date for " + f.Label + "."
		}
		return raw, true, ""
	case "time":
		t, err := time.Parse("15:04", raw)
		if err != nil {
			if t, err = time.Parse("15:04:05", raw); err != nil {
				return nil, false, "Please enter a time for " + f.Label + "."
			}
		}
		return t.Format("15:04:05"), true, ""
	case "datetime":
		// A native picker sends local time without a zone: the
		// organisation's (Lagos).
		t, err := time.ParseInLocation("2006-01-02T15:04", raw, storefront.Lagos)
		if err != nil {
			if t, err = time.ParseInLocation("2006-01-02T15:04:05", raw, storefront.Lagos); err != nil {
				return nil, false, "Please enter a date and time for " + f.Label + "."
			}
		}
		return t.Format(time.RFC3339), true, ""
	}
	// Text.
	n := len([]rune(raw))
	if l, ok := number(f.schema["minLength"]); ok && float64(n) < l {
		return nil, false, fmt.Sprintf("Please write at least %s characters for %s.", fmtNum(l), f.Label)
	}
	if f.MaxLen > 0 && n > f.MaxLen {
		return nil, false, fmt.Sprintf("Please keep %s to %d characters.", f.Label, f.MaxLen)
	}
	if pat := str(f.schema["pattern"]); pat != "" {
		if re, err := regexp.Compile(pat); err == nil && !re.MatchString(raw) {
			msg := "Please check " + f.Label + ": it is not in the form expected."
			if f.Help != "" {
				msg += " " + f.Help
			}
			return nil, false, msg
		}
	}
	if f.freeText() && sensitive(raw) {
		return nil, false, "Please do not put card numbers, BVNs or NINs in " + f.Label + "."
	}
	return raw, true, ""
}

func (f *field) parseNumber(raw string) (any, bool, string) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(strings.ReplaceAll(raw, ",", ""), " ", ""), "₦"))
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, false, "Please enter a number for " + f.Label + "."
	}
	if f.isKobo() {
		n = math.Round(n * 100)
	}
	check := func(key string, cmp func(a, b float64) bool, words string) string {
		lim, ok := number(f.schema[key])
		if !ok || !cmp(n, lim) {
			return ""
		}
		if f.isKobo() {
			lim /= 100
		}
		return fmt.Sprintf("%s must be %s %s%s.", f.Label, words, f.Unit, fmtNum(lim))
	}
	for _, c := range []string{
		check("minimum", func(a, b float64) bool { return a < b }, "at least"),
		check("maximum", func(a, b float64) bool { return a > b }, "at most"),
		check("exclusiveMinimum", func(a, b float64) bool { return a <= b }, "more than"),
		check("exclusiveMaximum", func(a, b float64) bool { return a >= b }, "less than"),
	} {
		if c != "" {
			return nil, false, c
		}
	}
	if f.typ == "integer" {
		if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
			return nil, false, "Please enter a whole number for " + f.Label + "."
		}
		return int64(n), true, ""
	}
	return n, true, ""
}

// acceptsType reports whether a sniffed content type is one a file
// field's accept list takes: a type, a type/* family or an .ext.
func (f *field) acceptsType(ctype string) bool {
	if f.Accept == "" {
		return true
	}
	for _, a := range strings.Split(f.Accept, ",") {
		a = strings.ToLower(strings.TrimSpace(a))
		if strings.HasPrefix(a, ".") {
			if t, _, _ := mime.ParseMediaType(mime.TypeByExtension(a)); t != "" {
				a = t
			}
		}
		if a == ctype || (strings.HasSuffix(a, "/*") && strings.HasPrefix(ctype, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

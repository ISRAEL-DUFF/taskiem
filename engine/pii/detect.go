package pii

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Detection (spec 9.3): personal data a schema did not declare is found by
// validators for Nigerian identifiers and sealed like declared data, before
// it is written. Detectors are conservative, because a step's data is full
// of digit strings (amounts, references) that are not personal:
//
//   - only strings are examined, and numbers only under a field name that
//     says what they are (bvn, nin, phone, account ...);
//   - phone numbers must be Nigerian mobile numbers (+234 or 0, then a
//     70x/80x/81x/90x/91x prefix);
//   - card numbers must pass the Luhn check and match a card scheme's
//     prefix and length;
//   - a 10-digit account number (NUBAN) is recognised by its field name, or
//     by a bank code beside it that its check digit agrees with;
//   - 11 digits are a BVN (starting 22) or a NIN, unless they are a phone.

// Detected is one detection, for tests and reports.
type Detected struct {
	Path     string
	Category string
}

var (
	emailRe = regexp.MustCompile(`^[^@\s"<>]+@[^@\s"<>]+\.[A-Za-z]{2,}$`)
	digits  = regexp.MustCompile(`^[0-9]+$`)
)

// hint maps a field name to the category it announces, if any.
func hint(key string) string {
	tokens := keyTokens(key)
	has := func(ws ...string) bool {
		for _, t := range tokens {
			for _, w := range ws {
				if t == w {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has("bvn"):
		return "bvn"
	case has("nin"):
		return "nin"
	case has("phone", "msisdn", "mobile", "tel", "telephone"):
		return "phone"
	case has("nuban", "acct") || (has("account") && has("number", "no", "num")):
		return "account_number"
	case has("pan") || (has("card") && has("number", "no", "num")):
		return "card"
	case has("email", "mail"):
		return "email"
	}
	return ""
}

// keyTokens splits snake_case, kebab-case and camelCase names.
func keyTokens(key string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	prevLower := false
	for _, r := range key {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
			prevLower = false
		case unicode.IsUpper(r) && prevLower:
			flush()
			cur.WriteRune(r)
			prevLower = false
		default:
			cur.WriteRune(r)
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		}
	}
	flush()
	return out
}

// Classify says whether a value under key is personal, and its category.
// siblings are the other fields of the object holding it.
func Classify(key string, v any, siblings map[string]any) (string, bool) {
	h := hint(key)
	var s string
	switch t := v.(type) {
	case string:
		s = strings.TrimSpace(t)
	case int64, float64, int:
		if h == "" || h == "email" {
			return "", false // a bare number is an amount until its name says otherwise
		}
		return h, true
	default:
		return "", false
	}
	if s == "" {
		return "", false
	}
	if emailRe.MatchString(s) {
		return "email", true
	}
	compact := strings.NewReplacer(" ", "", "-", "").Replace(s)
	if isPhone(compact) {
		return "phone", true
	}
	if !digits.MatchString(compact) {
		if h != "" && h != "email" && digits.MatchString(strings.TrimPrefix(compact, "+")) {
			return h, true
		}
		return "", false
	}
	switch n := len(compact); {
	case n == 11:
		switch {
		case h == "nin":
			return "nin", true
		case h == "bvn" || strings.HasPrefix(compact, "22"):
			return "bvn", true
		}
		return "nin", true
	case n == 10:
		if h == "account_number" || validNUBAN(compact, siblings) {
			return "account_number", true
		}
	case n >= 13 && n <= 19:
		if isCard(compact) {
			return "card", true
		}
	}
	if h != "" && h != "email" {
		return h, true
	}
	return "", false
}

func isPhone(s string) bool {
	switch {
	case strings.HasPrefix(s, "+234"):
		s = "0" + s[4:]
	case strings.HasPrefix(s, "234") && len(s) == 13:
		s = "0" + s[3:]
	}
	if len(s) != 11 || !digits.MatchString(s) || s[0] != '0' {
		return false
	}
	switch s[1:3] {
	case "70", "71", "80", "81", "90", "91":
		return true
	}
	return false
}

// luhn reports whether a digit string passes the Luhn check.
func luhn(s string) bool {
	sum, double := 0, false
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// isCard checks the Luhn digit and a scheme's prefix and length: Visa,
// Mastercard, Verve, American Express, Discover.
func isCard(s string) bool {
	if !luhn(s) {
		return false
	}
	n := len(s)
	p2, p4 := s[:2], s[:4]
	switch {
	case s[0] == '4':
		return n == 13 || n == 16 || n == 19
	case p2 >= "51" && p2 <= "55", p4 >= "2221" && p4 <= "2720":
		return n == 16
	case p4 == "5060" || p4 == "5061" || p4 == "5078" || p4 == "5079" || p4 == "6500":
		return n >= 16 && n <= 19 // Verve
	case p2 == "34" || p2 == "37":
		return n == 15
	case p4 == "6011" || p2 == "65":
		return n >= 16 && n <= 19
	}
	return false
}

// validNUBAN checks a 10-digit account number against a bank code beside
// it, with the CBN check digit (weights 3,7,3 over the 6-digit bank code
// and 9-digit serial; a 3-digit code is padded with zeros).
func validNUBAN(acct string, siblings map[string]any) bool {
	for _, k := range []string{"bank_code", "bankCode", "bank", "sort_code"} {
		code, ok := siblings[k].(string)
		if !ok || !digits.MatchString(code) || (len(code) != 3 && len(code) != 5 && len(code) != 6) {
			continue
		}
		code = strings.Repeat("0", 6-len(code)) + code
		in := code + acct[:9]
		weights := []int{3, 7, 3}
		sum := 0
		for i := range in {
			sum += int(in[i]-'0') * weights[i%3]
		}
		check := (10 - sum%10) % 10
		if check == int(acct[9]-'0') {
			return true
		}
	}
	return false
}

// Detect lists the personal values found in v, by path.
func Detect(v any) []Detected {
	var out []Detected
	walkDetect(v, "", "", nil, func(path, cat string, _ any) any {
		out = append(out, Detected{Path: path, Category: cat})
		return nil
	})
	return out
}

func walkDetect(v any, path, key string, siblings map[string]any, found func(path, cat string, v any) any) any {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return t
		}
		for k, x := range t {
			if r := walkDetect(x, path+"/"+k, k, t, found); r != nil {
				t[k] = r
			}
		}
		return nil
	case []any:
		for i, x := range t {
			// An item takes its list's name: "phones": ["+234..."].
			if r := walkDetect(x, path+"/"+itoa(i), key, nil, found); r != nil {
				t[i] = r
			}
		}
		return nil
	}
	if cat, ok := Classify(key, v, siblings); ok {
		return found(path, cat, v)
	}
	return nil
}

func itoa(i int) string { return strconv.Itoa(i) }

// SealDetected seals the personal values Detect finds in v (a fresh copy
// is returned) and records them in taint, so copies elsewhere are sealed
// too.
func SealDetected(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, taint Taint) (any, error) {
	if c == nil {
		return v, nil
	}
	v = deepCopy(v)
	var err error
	r := walkDetect(v, "", "", nil, func(_, cat string, x any) any {
		if err != nil {
			return nil
		}
		taint.Add(x, cat)
		sealed, e := c.SealTx(ctx, tx, tenant, cat, x)
		if e != nil {
			err = e
			return nil
		}
		return sealed
	})
	if err != nil {
		return nil, err
	}
	if r != nil {
		return r, nil // v itself was a personal scalar
	}
	return v, nil
}

// Free text cannot be sealed field by field, so personal values inside it
// are masked: code step logs and provider error messages.
var (
	textEmail = regexp.MustCompile(`[^\s@"<>(),;:]+@[^\s@"<>(),;:]+\.[A-Za-z]{2,}`)
	textPhone = regexp.MustCompile(`(?:\+234|\b234|\b0)[789][01]\d{8}\b`)
	textDigit = regexp.MustCompile(`\b\d(?:[ -]?\d){9,18}\b`)
)

// Redact masks personal values in free text.
func Redact(s string) string {
	s = textEmail.ReplaceAllString(s, "[email]")
	s = textPhone.ReplaceAllString(s, "[phone]")
	return textDigit.ReplaceAllStringFunc(s, func(m string) string {
		d := strings.NewReplacer(" ", "", "-", "").Replace(m)
		switch {
		case len(d) == 11 && strings.HasPrefix(d, "22"):
			return "[bvn]"
		case len(d) == 11:
			return "[nin]"
		case len(d) >= 13 && isCard(d):
			return "[card]"
		}
		return m
	})
}

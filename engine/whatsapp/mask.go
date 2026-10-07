package whatsapp

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/pii"
)

// Numbers.

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// ErrBadNumber: not a phone number in international form.
var ErrBadNumber = errors.New("enter the number in international form, e.g. +2348012345678")

// Normalise puts a number in E.164 form. A national number starting with 0
// takes the default country code (234: Nigeria); spaces, dashes, dots and
// brackets are dropped; 00 is read as +.
func Normalise(s, defaultCountry string) (string, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "+"):
	case strings.HasPrefix(s, "00"):
		s = "+" + s[2:]
	case strings.HasPrefix(s, "0") && defaultCountry != "":
		s = "+" + defaultCountry + s[1:]
	default:
		s = "+" + s
	}
	if !e164.MatchString(s) {
		return "", ErrBadNumber
	}
	return s, nil
}

// FromWaID turns a sender's WhatsApp ID (digits) into E.164.
func FromWaID(id string) (string, error) { return Normalise("+"+strings.TrimPrefix(id, "+"), "") }

// MaskNumber shows a number's country code and last four digits only.
func MaskNumber(n string) string {
	if len(n) < 8 {
		return "••••"
	}
	return n[:4] + strings.Repeat("•", len(n)-8) + n[len(n)-4:]
}

// Masking what messages show (spec 11.5): never secrets, never
// unredacted personal data; account and card numbers show their last four
// digits only.

// MaskValue masks a personal value of a category.
func MaskValue(category string, v any) string {
	s := strings.TrimSpace(fmt.Sprint(v))
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	last4 := func() string {
		if len(digits) < 4 {
			return "••••"
		}
		return "••••" + digits[len(digits)-4:]
	}
	switch category {
	case "account_number", "card", "nuban", "iban":
		return last4()
	case "phone":
		if n, err := Normalise(s, "234"); err == nil {
			return MaskNumber(n)
		}
		return last4()
	case "email":
		at := strings.LastIndex(s, "@")
		if at < 1 {
			return "[email]"
		}
		return string([]rune(s)[0]) + "•••" + s[at:]
	case "bvn", "nin":
		return "[" + category + "]"
	case "name":
		r := []rune(s)
		if len(r) == 0 {
			return "[name]"
		}
		return string(r[0]) + "•••"
	}
	return "[hidden]"
}

// secretKey matches field names that may hold credentials: never shown.
var secretKey = regexp.MustCompile(`(?i)(secret|password|passwd|token|api_?key|private|credential|pin|otp|cvv)`)

// nameKey matches field names that hold a person's name.
var nameKey = regexp.MustCompile(`(?i)(^|_)(name|first_?name|last_?name|surname|full_?name|beneficiary|recipient)$`)

// Opener opens a sealed value: its plaintext and category.
type Opener func(envelope map[string]any) (any, error)

// SummaryLines renders a value (an approval's subject, a run's input) as
// at most max "field: value" lines, every personal value masked: sealed
// ones (opened only to mask them, by their category), ones the detectors
// recognise, names by their field, and anything under a field that looks
// like a credential hidden.
func SummaryLines(v any, open Opener, max int) []string {
	var out []string
	more := 0
	var walk func(prefix, key string, v any, siblings map[string]any)
	add := func(path, val string) {
		if len(out) >= max {
			more++
			return
		}
		out = append(out, path+": "+clip(val, 80))
	}
	walk = func(path, key string, v any, siblings map[string]any) {
		if key != "" && secretKey.MatchString(key) {
			add(path, "[hidden]")
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if pii.IsEnvelope(t) {
				cat, _ := t["$pii"].(string)
				if open == nil {
					add(path, MaskValue(cat, nil))
					return
				}
				plain, err := open(t)
				if err != nil {
					add(path, "[hidden]")
					return
				}
				add(path, MaskValue(cat, plain))
				return
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(p, k, t[k], t)
			}
		case []any:
			if len(t) == 0 {
				add(path, "none")
				return
			}
			add(path, strconv.Itoa(len(t))+" items")
		case nil:
			add(path, "-")
		case string:
			if cat, ok := pii.Classify(key, t, siblings); ok {
				add(path, MaskValue(cat, t))
				return
			}
			if key != "" && nameKey.MatchString(key) {
				add(path, MaskValue("name", t))
				return
			}
			add(path, pii.Redact(t))
		default:
			if cat, ok := pii.Classify(key, t, siblings); ok {
				add(path, MaskValue(cat, t))
				return
			}
			add(path, fmt.Sprint(t))
		}
	}
	walk("", "", v, nil)
	if len(out) == 0 && more == 0 {
		return nil
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("… and %d more", more))
	}
	return out
}

// SafeText masks personal values in free text: the last filter on every
// message the platform sends.
func SafeText(s string) string { return pii.Redact(s) }

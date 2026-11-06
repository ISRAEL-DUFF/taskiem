package pii

import (
	"strings"
)

// Canonical is the form of a value its subject is keyed by, so the same
// person sealed in another format is one subject: a phone in E.164
// ('0803 123 4567' and '+2348031234567' alike), an email trimmed and in
// lower case, a BVN, NIN, account or card number its digits. The sealed
// ciphertext keeps the value as it was. Other categories (names, free
// text) are left as they are: making them alike could merge two people.
func Canonical(category string, v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch category {
	case "phone":
		if p, ok := canonicalPhone(s); ok {
			return p
		}
	case "email":
		return strings.ToLower(strings.TrimSpace(s))
	case "bvn", "nin", "account_number", "card":
		if d := numberDigits(s); d != "" {
			return d
		}
	}
	return v
}

// canonicalPhone is a phone number in E.164: separators dropped, a
// leading 00 or a Nigerian trunk 0 (alone, or after +234 as "(0)")
// handled; ok is false for what is not a phone number.
func canonicalPhone(s string) (string, bool) {
	d := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')', '.', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(d, "+"):
		d = d[1:]
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	case strings.HasPrefix(d, "0") && len(d) == 11:
		d = "234" + d[1:]
	}
	if strings.HasPrefix(d, "2340") && len(d) == 14 {
		d = "234" + d[4:]
	}
	if len(d) < 8 || len(d) > 15 || d[0] == '0' || strings.Trim(d, "0123456789") != "" {
		return "", false
	}
	return "+" + d, true
}

// numberDigits is s without spaces and hyphens when the rest are all digits,
// else "".
func numberDigits(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		default:
			return ""
		}
	}
	return b.String()
}

// Formats are the ways a value was commonly written before subject ids
// were canonical (Canonical), the value as given first: subjects keyed by
// those forms then may still hold data, so erasing by value looks for
// them too.
func Formats(category string, v any) []any {
	out := []any{v}
	s, ok := v.(string)
	if !ok {
		return out
	}
	add := func(x string) {
		for _, y := range out {
			if y == any(x) {
				return
			}
		}
		out = append(out, x)
	}
	add(strings.TrimSpace(s))
	switch category {
	case "phone":
		p, ok := canonicalPhone(s)
		if !ok {
			break
		}
		add(p)
		add(p[1:])
		if strings.HasPrefix(p, "+234") && len(p) == 14 {
			n := p[4:] // 10 digits
			local := "0" + n
			add(local)
			add(local[:4] + " " + local[4:7] + " " + local[7:])
			add(local[:4] + "-" + local[4:7] + "-" + local[7:])
			add("+234 " + n[:3] + " " + n[3:6] + " " + n[6:])
			add("+234 (0)" + n[:3] + " " + n[3:6] + " " + n[6:])
		}
	case "email":
		add(strings.ToLower(strings.TrimSpace(s)))
	case "bvn", "nin", "account_number", "card":
		if d := numberDigits(s); d != "" {
			add(d)
		}
	}
	return out
}

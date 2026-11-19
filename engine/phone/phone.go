// Package phone reads a phone number a person typed as E.164, by one rule
// everywhere a person's own number is read for signing in: the phone way
// in, changing one's phone and chat sign-in
// (docs/spec/taskiem-for-everyone.md §5.1, §5.5). The web (web/src/lib/
// phone.ts) and the app read numbers by the same table (phone_test.go).
package phone

import (
	"errors"
	"strings"
)

// DefaultCountry is the calling code a number with no country is read in.
const DefaultCountry = "234"

// ErrInvalid is a number that is not one.
var ErrInvalid = errors.New("that is not a phone number: give your number, with its country code if it is not Nigerian")

// Normalise reads a typed number:
//   - spaces, dashes, dots and brackets are dropped;
//   - a leading 00 means +;
//   - +234 followed by a 0 drops that 0 (+234 0803… is +234 803…);
//   - a local number, 0 and 10 digits, is Nigerian: +234 and the 10;
//   - a bare 10-digit number starting 7, 8 or 9 is Nigerian too;
//   - a Nigerian number is exactly +234 and 10 digits starting 7, 8 or 9;
//   - any other country's is + and 8 to 15 digits in all, not starting 0.
//
// Anything else is ErrInvalid.
func Normalise(typed string) (string, error) {
	d := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', ' ', '\t':
			return -1
		}
		return r
	}, strings.TrimSpace(typed))
	switch {
	case strings.HasPrefix(d, "+"):
	case strings.HasPrefix(d, "00"):
		d = "+" + d[2:]
	case len(d) == 11 && d[0] == '0' && digits(d):
		d = "+" + DefaultCountry + d[1:]
	case len(d) == 10 && strings.ContainsRune("789", rune(d[0])) && digits(d):
		d = "+" + DefaultCountry + d
	default:
		return "", ErrInvalid
	}
	if strings.HasPrefix(d, "+"+DefaultCountry+"0") {
		d = "+" + DefaultCountry + d[len(DefaultCountry)+2:]
	}
	n := d[1:]
	if !digits(n) || n == "" {
		return "", ErrInvalid
	}
	if strings.HasPrefix(n, DefaultCountry) {
		if len(n) != len(DefaultCountry)+10 || !strings.ContainsRune("789", rune(n[len(DefaultCountry)])) {
			return "", ErrInvalid
		}
		return d, nil
	}
	if len(n) < 8 || len(n) > 15 || n[0] == '0' {
		return "", ErrInvalid
	}
	return d, nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

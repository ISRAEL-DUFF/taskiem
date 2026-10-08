// Package money converts between Taskiem's amounts (integers in minor
// units: kobo, pesewas, cents) and providers' decimal amounts ("2000.00",
// 2000.5) without floating-point arithmetic.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// ErrAmount marks an amount that cannot be represented exactly.
var ErrAmount = errors.New("amount")

// Minor reads an integer amount in minor units from a decoded JSON input
// (float64, json.Number, or Go integer types). Fractions are refused: an
// input of 4500.5 kobo is a mistake, not something to round.
func Minor(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return 0, fmt.Errorf("%w: %v is not a whole number of minor units", ErrAmount, x)
		}
		return int64(x), nil
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: %s is not a whole number of minor units", ErrAmount, x)
		}
		return n, nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q is not a whole number of minor units", ErrAmount, x)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%w: %v (%T) is not an amount", ErrAmount, v, v)
}

// Format renders minor units as a decimal with scale places: 450000, 2 ->
// "4500.00".
func Format(minor int64, scale int) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := strconv.FormatInt(minor, 10)
	if scale > 0 {
		if len(s) <= scale {
			s = strings.Repeat("0", scale-len(s)+1) + s
		}
		s = s[:len(s)-scale] + "." + s[len(s)-scale:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Number is Format as a JSON number, for providers that take numbers.
func Number(minor int64, scale int) json.Number { return json.Number(Format(minor, scale)) }

// Parse reads a provider's decimal amount (a string like "2000.00" or a
// JSON number) as minor units with scale places. More decimal places than
// scale are refused unless they are zeros.
func Parse(v any, scale int) (int64, error) {
	var s string
	switch x := v.(type) {
	case string:
		s = strings.TrimSpace(x)
	case json.Number:
		s = x.String()
	case float64:
		// encoding/json gives the shortest representation that round-trips.
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		s = strconv.Itoa(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case nil:
		return 0, fmt.Errorf("%w: missing", ErrAmount)
	default:
		return 0, fmt.Errorf("%w: %v (%T) is not an amount", ErrAmount, v, v)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || s == "" {
		return 0, fmt.Errorf("%w: %q is not a decimal", ErrAmount, s)
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("%w: %q has more than %d decimal places", ErrAmount, s, scale)
	}
	return r.Num().Int64(), nil
}

// Reader converts a provider's amounts into minor units for one response,
// keeping the first failure: an amount that cannot be read exactly is an
// error, never a zero. A missing (null) amount reads as zero.
type Reader struct{ Err error }

// Minor reads an exact amount.
func (r *Reader) Minor(v any, scale int) int64 {
	if v == nil {
		return 0
	}
	n, err := Parse(v, scale)
	if err != nil && r.Err == nil {
		r.Err = err
	}
	return n
}

// Ceil reads an amount rounded up to the minor unit, for charges such as
// fees that providers quote more finely (26.875 naira is 2688 kobo).
func (r *Reader) Ceil(v any, scale int) int64 {
	if v == nil {
		return 0
	}
	n, err := Parse(v, scale+6)
	if err != nil {
		if r.Err == nil {
			r.Err = err
		}
		return 0
	}
	const unit = 1_000_000
	q := n / unit
	if n%unit > 0 {
		q++
	}
	return q
}

// Floor reads an amount rounded down to the minor unit, for balances that
// providers report more finely: what can actually be spent.
func (r *Reader) Floor(v any, scale int) int64 {
	if v == nil {
		return 0
	}
	n, err := Parse(v, scale+8)
	if err != nil {
		if r.Err == nil {
			r.Err = err
		}
		return 0
	}
	const unit = 100_000_000
	q := n / unit
	if n%unit < 0 {
		q--
	}
	return q
}

// Decimal renders a provider's number as exact decimal text ("0.00025406"),
// for crypto quantities and rates that have no minor unit.
func Decimal(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

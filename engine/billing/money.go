package billing

import (
	"fmt"
	"time"
)

// Amounts are whole kobo (1 naira = 100 kobo), never floats.

// Line is one invoice line. Credits are negative.
type Line struct {
	Kind        string `json:"kind"` // plan, proration_credit, credit, whatsapp_overage, ai_overage
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitKobo    int64  `json:"unit_kobo"`
	AmountKobo  int64  `json:"amount_kobo"`
}

// Totals are an invoice's amounts: the subtotal of its lines (never below
// zero: credit left over carries to the next invoice), VAT on it, the
// total, and any credit not used.
type Totals struct {
	Subtotal, VAT, Total, CreditLeft int64
	VATBasisPoints                   int
}

// Compute totals lines with VAT at rateBP basis points, rounded half up to
// the kobo.
func Compute(lines []Line, rateBP int) Totals {
	var sum int64
	for _, l := range lines {
		sum += l.AmountKobo
	}
	t := Totals{VATBasisPoints: rateBP}
	if sum < 0 {
		t.CreditLeft = -sum
		sum = 0
	}
	t.Subtotal = sum
	t.VAT = VAT(sum, rateBP)
	t.Total = t.Subtotal + t.VAT
	return t
}

// VAT is amount * rateBP / 10000, rounded half up.
func VAT(amount int64, rateBP int) int64 {
	if amount <= 0 || rateBP <= 0 {
		return 0
	}
	return (amount*int64(rateBP) + 5000) / 10000
}

// Unused is the credit for the unused part of a period paid at price: the
// remaining time over the period's length, rounded down to the kobo (in
// the platform's favour by under a kobo). Nothing is owed outside the
// period.
func Unused(price int64, start, end, at time.Time) int64 {
	total := end.Sub(start)
	left := end.Sub(at)
	if price <= 0 || total <= 0 || left <= 0 {
		return 0
	}
	if left > total {
		left = total
	}
	// Seconds keep the product within int64 for any sane price.
	return price * int64(left/time.Second) / int64(total/time.Second)
}

// PeriodEnd is the end of a billing period starting at start: one calendar
// month or year later (Jan 31 + 1 month is Mar 3 in Go; clamp to the
// month's last day instead).
func PeriodEnd(start time.Time, interval string) time.Time {
	start = start.UTC()
	months := 1
	if interval == IntervalAnnual {
		months = 12
	}
	y, m, d := start.Date()
	first := time.Date(y, m+time.Month(months), 1, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), time.UTC)
}

// Naira renders kobo as naira for people: ₦12,500.00.
func Naira(kobo int64) string {
	neg := kobo < 0
	if neg {
		kobo = -kobo
	}
	whole, frac := kobo/100, kobo%100
	s := fmt.Sprintf("%d", whole)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	out := fmt.Sprintf("₦%s.%02d", s, frac)
	if neg {
		return "-" + out
	}
	return out
}

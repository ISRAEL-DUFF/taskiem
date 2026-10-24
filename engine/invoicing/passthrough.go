package invoicing

// Pass-through lines (decision 0046 point 11; docs/spec/schools.md 5.2;
// docs/money-reports.md#pass-through): a line collected on behalf of
// someone else (an exam body's fee) names them in PassThroughFor. What a
// customer pays towards such an invoice is the business's own first and
// the bodies' last: a payment counts towards the business's own lines
// until they are paid, then towards the pass-through lines in the order
// they appear. The rule is whole kobo with nothing to round, and a part
// payment is never reported as money held for someone else while the
// business's own fees are still unpaid.

// BodyAmount is what one body's lines come to, or one body's share of a
// payment.
type BodyAmount struct {
	Body string `json:"body"`
	Kobo int64  `json:"kobo"`
}

// PassThrough splits lines into the business's own total and each body's
// total, bodies in the order their first line appears. A line taking out
// an earlier pass-through line names the same body, so it nets out.
func PassThrough(lines []Line) (own int64, bodies []BodyAmount) {
	at := map[string]int{}
	for _, l := range lines {
		if l.PassThroughFor == "" {
			own += l.AmountKobo()
			continue
		}
		i, ok := at[l.PassThroughFor]
		if !ok {
			i = len(bodies)
			at[l.PassThroughFor] = i
			bodies = append(bodies, BodyAmount{Body: l.PassThroughFor})
		}
		bodies[i].Kobo += l.AmountKobo()
	}
	return own, bodies
}

// HasPassThrough reports whether any line is collected for someone else.
func HasPassThrough(lines []Line) bool {
	for _, l := range lines {
		if l.PassThroughFor != "" {
			return true
		}
	}
	return false
}

// PassThroughCollected is how much of paid (what was paid towards an
// invoice with these lines) is each body's, own lines first.
func PassThroughCollected(lines []Line, paid int64) []BodyAmount {
	own, bodies := PassThrough(lines)
	left := paid - max(own, 0)
	out := make([]BodyAmount, 0, len(bodies))
	for _, b := range bodies {
		take := min(max(left, 0), max(b.Kobo, 0))
		left -= take
		out = append(out, BodyAmount{Body: b.Body, Kobo: take})
	}
	return out
}

// PassThroughShare is each body's share of one payment of amount, paid
// after paidBefore had been: what the bodies had collected after it less
// what they had before. The shares of an invoice's payments add up to
// PassThroughCollected of their sum.
func PassThroughShare(lines []Line, paidBefore, amount int64) []BodyAmount {
	before := PassThroughCollected(lines, paidBefore)
	after := PassThroughCollected(lines, paidBefore+amount)
	out := []BodyAmount{}
	for i := range after {
		if d := after[i].Kobo - before[i].Kobo; d != 0 {
			out = append(out, BodyAmount{Body: after[i].Body, Kobo: d})
		}
	}
	return out
}

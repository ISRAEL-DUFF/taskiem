package stock

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// A sale in chat (docs/spec/stock.md §5): "sold 3 bags of rice 45,000
// cash", read by a parser, never the model.

// Sold is a sale sentence read: its items (as said; matched to the
// catalogue by the caller), the amount when one was said, and how it was
// paid ("" when not said).
type Sold struct {
	Items      []SoldItem
	AmountKobo *int64
	PaidHow    string
}

// SoldItem is one item of a sale as said: how many, the unit, the name.
type SoldItem struct {
	Quantity int64
	Unit     string
	Name     string
}

// PayWords are the words that say how a sale was paid, in any language:
// the trade's own words.
var PayWords = map[string]string{
	"cash": Cash, "kash": Cash, "owo": Cash, "kudi": Cash, "ego": Cash,
	"transfer": Transfer, "trf": Transfer, "tf": Transfer, "transferred": Transfer, "bank": Transfer,
	"pos": POS, "card": POS, "terminal": POS, "atm": POS,
}

var segmentSplit = regexp.MustCompile(`(?i)\s*(?:,|;|\s+and\s+|\s+&\s+|\s+plus\s+|\s+with\s+)\s*`)

// ParseSale reads what follows the sale word ("3 bags of rice 45,000
// cash", "2 cartons indomie, 1 bag rice 60,000 transfer", "indomie 500").
// Each item starts with how many (1 when none is said); the amount is a
// price at the end (after "for" or "at", or alone); the payment word may
// come last. ok is false when no item can be read.
func ParseSale(text string) (Sold, bool) {
	var out Sold
	f := strings.Fields(strings.Trim(strings.TrimSpace(text), " .!"))
	// How it was paid: the last word, or "in cash", "by transfer", "on pos".
	if n := len(f); n > 0 {
		if how, ok := PayWords[strings.ToLower(strings.Trim(f[n-1], ".,"))]; ok {
			out.PaidHow = how
			f = f[:n-1]
			if m := len(f); m > 0 {
				switch strings.ToLower(f[m-1]) {
				case "in", "by", "on", "with", "via", "through":
					f = f[:m-1]
				}
			}
		}
	}
	// The amount: a price at the end, after the item's name ("indomie
	// 500"), or written as money ("45,000", "2k"), or after two words or
	// more ("3 rice 1500"); "3 rice" is three of rice.
	if n := len(f); n >= 2 {
		last := f[n-1]
		_, firstIsCount := wholeNumber(f[0])
		if kobo, ok := storefront.ParsePrice(last); ok && (looksLikeMoney(last) || n >= 3 || !firstIsCount) {
			out.AmountKobo, f = &kobo, f[:n-1]
			if m := len(f); m > 0 {
				switch strings.ToLower(f[m-1]) {
				case "for", "at", "@", "total", "=":
					f = f[:m-1]
				}
			}
		}
	}
	rest := strings.Join(f, " ")
	if rest == "" {
		return Sold{}, false
	}
	for _, seg := range segmentSplit.Split(rest, -1) {
		seg = strings.Trim(seg, " -:")
		if seg == "" {
			continue
		}
		it := SoldItem{Quantity: 1}
		w := strings.Fields(seg)
		if n, ok := wholeNumber(strings.TrimSuffix(strings.ToLower(w[0]), "x")); ok && len(w) > 1 {
			it.Quantity, w = n, w[1:]
		}
		if it.Quantity <= 0 || it.Quantity > MaxDelta {
			return Sold{}, false
		}
		it.Unit, it.Name = unitAndName(strings.Join(w, " "))
		it.Name = strings.Trim(it.Name, " -:,.")
		if !hasLetter(it.Name) || len([]rune(it.Name)) > 120 {
			return Sold{}, false
		}
		out.Items = append(out.Items, it)
	}
	if len(out.Items) == 0 || len(out.Items) > MaxSaleLines {
		return Sold{}, false
	}
	return out, true
}

// looksLikeMoney says a word is written as money rather than a count:
// "45,000", "45k", "N45000", "₦500".
func looksLikeMoney(w string) bool {
	l := strings.ToLower(w)
	return strings.Contains(l, ",") || strings.HasPrefix(l, "₦") || strings.HasPrefix(l, "n") || strings.HasPrefix(l, "#") ||
		strings.HasSuffix(l, "k") || strings.HasSuffix(l, "m") || strings.Contains(l, ".")
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

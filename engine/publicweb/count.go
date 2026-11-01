package publicweb

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Tickets and Classes (docs/spec/fronts.md §6, F6): the count field. The
// customer picks an event or a class from the front's list and how many;
// the server prices it from the list's copy, and the database takes the
// claim with the submission, refusing more than its tickets or places
// less those taken. A class's fee may be paid in instalments, the first
// now; a full class may take a name for its waiting list instead.

// countField is the field for a front's list of events or classes: those
// on sale (available, priced, with some to sell, not yet started).
func countField(items []storefront.Item, act storefront.PublicAction, now time.Time) *hireView {
	h := &hireView{Count: true, Qty: "1", MaxPerOrder: maxPerOrder(act)}
	for _, it := range items {
		if !onSale(it, now) {
			continue
		}
		label := it.Name
		if it.Starts != nil {
			label += ", " + storefront.When(*it.Starts)
		}
		if it.Venue != "" {
			label += ", " + it.Venue
		}
		h.Items = append(h.Items, hireItem{ID: it.ID, Label: label + ", " + storefront.FormatNaira(*it.PriceKobo)})
	}
	if act.Kind == "class" && act.Instalments > 1 {
		h.Plans = []hourOption{{Value: "1", Label: "Pay in full", Selected: true},
			{Value: strconv.Itoa(act.Instalments), Label: fmt.Sprintf("Pay in %d instalments, the first now", act.Instalments)}}
	}
	return h
}

func maxPerOrder(act storefront.PublicAction) int {
	if act.MaxPerOrder > 0 {
		return act.MaxPerOrder
	}
	return 10
}

// onSale reports whether an event or a class can be taken now.
func onSale(it storefront.Item, now time.Time) bool {
	return it.Available && it.PriceKobo != nil && *it.PriceKobo > 0 && it.Capacity > 0 && (it.Starts == nil || it.Starts.After(now))
}

// keepCount copies the count field's answers back into the form.
func (h *hireView) keepCount(v url.Values) {
	for i := range h.Plans {
		h.Plans[i].Selected = h.Plans[i].Value == v.Get("plan")
	}
}

// checkCount reads the count field into the submission: the event or the
// class from the list's copy, how many, the price, and for a class paid
// in instalments the first one as what is paid now; msg says what to fix.
func checkCount(v url.Values, items []storefront.Item, act storefront.PublicAction, now time.Time, sub *storefront.Submission) string {
	var it *storefront.Item
	for i := range items {
		if items[i].ID == v.Get("hire_item") {
			it = &items[i]
		}
	}
	what := "event"
	if act.Kind == "class" {
		what = "class"
	}
	if it == nil || !onSale(*it, now) {
		return "Choose the " + what + "."
	}
	qty, err := strconv.Atoi(strings.TrimSpace(v.Get("hire_qty")))
	if err != nil || qty < 1 {
		return "Say how many."
	}
	if limit := min(maxPerOrder(act), it.Capacity); qty > limit {
		return "At most " + strconv.Itoa(limit) + " in one order."
	}
	price := *it.PriceKobo
	total := price * int64(qty)
	sub.Kind = act.Kind
	h := storefront.Hired{ItemID: it.ID, Item: it.Name, Qty: qty}
	if it.Starts != nil {
		h.From = *it.Starts
	}
	sub.Hire = &h
	sub.Basket = []storefront.Line{{ID: it.ID, Name: it.Name, Quantity: qty, PriceKobo: price, LineKobo: total}}
	sub.TotalKobo = total
	sub.DepositKobo = 0
	sub.Instalments = 1
	if n, err := strconv.Atoi(v.Get("plan")); err == nil && act.Kind == "class" && n == act.Instalments && n > 1 {
		parts := storefront.InstalmentAmounts(total, n)
		sub.Instalments, sub.DepositKobo = n, parts[0]
	}
	if v.Get("waitlist") == "1" && act.Kind == "class" && act.WaitingList {
		sub.Waitlist, sub.DepositKobo, sub.Instalments = true, 0, 1
	}
	return ""
}

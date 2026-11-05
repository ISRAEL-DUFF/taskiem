package publicweb

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// An order has two steps, the bag and then the details (decision: the
// storefront's shop pages). The bag is not kept anywhere on the server:
// it is in the address (?q.<id>=2&o.<id>.0=1) and, once it is the order's
// details being filled in, in the form's own hidden fields. That keeps the
// page free of cookies and working without a script, and the server prices
// the order from the catalogue copy exactly as before, never from these.

const (
	stepBag     = "bag"
	stepDetails = "details"
)

// bagLine is one line of the bag as the page shows it.
type bagLine struct {
	N        int
	ID       string
	Name     string
	Qty      int
	Max      int
	Choices  string // the choices taken, "Large, Plantain"
	Unit     string
	Line     string
	UnitKobo int64
	// Small is the item's thumbnail; Remove is the bag's address without
	// this line (the way home when it was the last).
	Small  string
	Remove string
	// Hidden are the line's choices, carried as fields.
	Hidden []bagField
}

type bagField struct{ Name, Value string }

// fillBag reads the bag from the quantities and choices the form has,
// pricing each line as the item plus its choices, and totals it. The total
// is only for showing: the order is priced again on the server.
func (f *formData) fillBag() {
	f.Bag, f.Subtotal, f.BagCount, f.Deposit, f.Balance = nil, "", 0, "", ""
	var total int64
	for _, it := range f.Items {
		if it.Qty <= 0 || !it.Orderable {
			continue
		}
		unit := it.PriceKobo
		var names []string
		var hidden []bagField
		for _, o := range it.Options {
			for _, c := range o.Choices {
				if c.Selected {
					unit += c.PriceKobo
					names = append(names, c.Name)
					hidden = append(hidden, bagField{Name: o.Input, Value: c.Value})
				}
			}
		}
		line := unit * int64(it.Qty)
		total += line
		f.BagCount += it.Qty
		f.Bag = append(f.Bag, bagLine{N: it.N, ID: it.ID, Name: it.Name, Qty: it.Qty, Max: it.Max, Choices: strings.Join(names, ", "),
			Unit: storefront.FormatNaira(unit), Line: storefront.FormatNaira(line), UnitKobo: unit, Small: it.Small, Hidden: hidden})
	}
	if len(f.Bag) > 0 {
		f.Subtotal = storefront.FormatNaira(total)
		if f.DepositPercent > 0 {
			dep := storefront.ActionOptions{DepositPercent: f.DepositPercent}.Deposit(total)
			f.Deposit, f.Balance = storefront.FormatNaira(dep), storefront.FormatNaira(total-dep)
		}
	}
}

// bagQuery is the bag as an address's query, for a step to go back to.
func (f *formData) bagQuery(step string) string {
	v := url.Values{"step": {step}}
	for _, l := range f.Bag {
		v.Set(quantityField+l.ID, strconv.Itoa(l.Qty))
		for _, h := range l.Hidden {
			v.Set(h.Name, h.Value)
		}
	}
	return v.Encode()
}

// stepFor puts an order on its bag or details step when its address asks
// for one and the bag has something in it; otherwise the order stays the
// one page it always was, with the whole menu to choose from.
func (f *formData) stepFor(q url.Values) {
	if f.Kind != "order" || len(f.Items) == 0 {
		return
	}
	step := q.Get("step")
	if step != stepBag && step != stepDetails {
		return
	}
	f.keepBasket(q)
	f.fillBag()
	if len(f.Bag) == 0 {
		return
	}
	f.Step = step
	f.setLinks()
}

// setLinks sets where to add more and where the bag is changed from.
func (f *formData) setLinks() {
	f.MoreHref = f.Home
	f.ChangeHref = f.Action + "?" + f.bagQuery(stepBag)
	for i := range f.Bag {
		f.Bag[i].Remove = f.removeHref(f.Bag[i].ID)
	}
}

// removeHref is the bag's address without the line of item id.
func (f *formData) removeHref(id string) string {
	v := url.Values{"step": {stepBag}}
	left := 0
	for _, l := range f.Bag {
		if l.ID == id {
			continue
		}
		left++
		v.Set(quantityField+l.ID, strconv.Itoa(l.Qty))
		for _, h := range l.Hidden {
			v.Set(h.Name, h.Value)
		}
	}
	if left == 0 {
		return f.Home
	}
	return f.Action + "?" + v.Encode()
}

// detailsAgain keeps a refused submission on its details step, with its
// bag as a summary, when the bag still has something in it.
func (f *formData) detailsAgain() {
	if f.Kind != "order" || len(f.Items) == 0 {
		return
	}
	f.fillBag()
	if len(f.Bag) > 0 {
		f.Step = stepDetails
		f.setLinks()
	}
}

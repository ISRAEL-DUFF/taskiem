package api

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Sizes and colours in the chat apps (docs/spec/chat-categories-and-
// variants.md, "WhatsApp, Telegram and Slack", phase 5). A size is found
// by its full name ("air max 90 42 black"), or by the product's name and
// the values said after it in any order ("air max 90 size 42 black",
// "air max 90 black"). What is not said is asked one choice at a time, the
// same numbered pick used everywhere ("Which size of Air Max 90? 1. 40
// 2. 41 …"), a value said as a word as good as its number; a choice with
// one value left is not asked. Once one size is left the stock word goes
// on: shown, goods in, a change, a sale.

// sizeAsk is a size being picked: the product, what is said so far, and
// the choice asked now with its values on offer.
type sizeAsk struct {
	Product string            `json:"product"`
	Name    string            `json:"name"`
	Have    map[string]string `json:"have,omitempty"`
	Choice  string            `json:"choice"`
	Values  []string          `json:"values"`
}

// saleAsk is a sale waiting on a size: the sale as said, its customer,
// the sizes chosen for its items so far (by the item's place in it), and
// the item asked about now.
type saleAsk struct {
	Said        stock.Sold     `json:"said"`
	Customer    string         `json:"customer,omitempty"`
	CustomerKey string         `json:"customer_key,omitempty"`
	CustomerID  string         `json:"customer_id,omitempty"`
	Chosen      map[int]string `json:"chosen,omitempty"`
	At          int            `json:"at"`
}

// sizeWords are words said with values that are none ("size 42", "in
// black").
var sizeWords = map[string]bool{"size": true, "sizes": true, "colour": true, "color": true, "colours": true, "colors": true,
	"in": true, "of": true, "the": true, "and": true, "number": true, "no": true}

// sizesOnSale are a product's sizes and colours on sale.
func sizesOnSale(items []stock.Item, product string) []stock.Item {
	var out []stock.Item
	for _, it := range items {
		if it.VariantOf == product && !it.Retired() && it.Variant != nil {
			out = append(out, it)
		}
	}
	return out
}

// narrowSizes are those of the product's sizes on sale with the values
// said.
func narrowSizes(items []stock.Item, product string, have map[string]string) []stock.Item {
	var out []stock.Item
	for _, it := range sizesOnSale(items, product) {
		ok := true
		for k, v := range have {
			got := ""
			for kk, vv := range it.Variant {
				if strings.EqualFold(kk, k) {
					got = vv
				}
			}
			if !strings.EqualFold(got, v) {
				ok = false
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

// valuesSaid reads the values of a product's choices in the words said
// after its name, in any order: each choice's longest value whose words
// are among them. Words that are none are left.
func valuesSaid(product stock.Item, words string) map[string]string {
	return valuesIn(product.Choices, words)
}

// valuesIn is valuesSaid for any choices.
func valuesIn(choices []variants.Choice, words string) map[string]string {
	said := " " + matchKey(words) + " "
	have := map[string]string{}
	for _, c := range choices {
		best := ""
		for _, v := range c.Values {
			k := matchKey(v)
			if k != "" && strings.Contains(said, " "+k+" ") && len(k) > len(matchKey(best)) {
				best = v
			}
		}
		if best != "" {
			have[c.Name] = best
		}
	}
	return have
}

// findSized is the product sold in sizes and colours whose name the words
// start with (the longest such), and the values said after it.
func findSized(items []stock.Item, phrase string) (stock.Item, map[string]string, bool) {
	said := matchKey(phrase)
	var best stock.Item
	for _, it := range items {
		if !it.HasSizes() || it.Retired() {
			continue
		}
		k := matchKey(it.Name)
		if k != "" && (said == k || strings.HasPrefix(said, k+" ")) && len(k) > len(matchKey(best.Name)) {
			best = it
		}
	}
	if best.ID == "" {
		return stock.Item{}, nil, false
	}
	rest := strings.TrimPrefix(said, matchKey(best.Name))
	return best, valuesSaid(best, rest), true
}

// unknownWords are the words said after a product's name that are none
// of its values ("xl" of a kaftan sold in S and M), filler words aside.
func unknownWords(product stock.Item, phrase string) string {
	rest := " " + strings.TrimPrefix(matchKey(phrase), matchKey(product.Name)) + " "
	for _, v := range valuesSaid(product, rest) {
		rest = strings.Replace(rest, " "+matchKey(v)+" ", " ", 1)
	}
	var left []string
	for _, w := range strings.Fields(rest) {
		if !sizeWords[w] {
			left = append(left, w)
		}
	}
	return strings.Join(left, " ")
}

// nextChoice is the first of the product's choices the sizes left differ
// in, with its values among them in the product's order.
func nextChoice(product stock.Item, left []stock.Item, have map[string]string) (string, []string) {
	for _, c := range product.Choices {
		if _, said := have[c.Name]; said {
			continue
		}
		var vals []string
		for _, v := range c.Values {
			if slices.ContainsFunc(left, func(it stock.Item) bool { return strings.EqualFold(it.Variant[c.Name], v) }) {
				vals = append(vals, v)
			}
		}
		if len(vals) > 1 {
			return c.Name, vals
		}
	}
	return "", nil
}

// askSize asks the next choice of a product's size, or goes on with the
// one size left.
func (s *Server) askSize(ctx context.Context, c *chat, sh *chatShop, product stock.Item, have map[string]string) error {
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	left := narrowSizes(items, product.ID, have)
	switch len(left) {
	case 0:
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.size.none", "name", whatsapp.SafeText(product.Name), "said", whatsapp.SafeText(variants.Label(have, product.Choices)),
			"sizes", whatsapp.SafeText(sizesSummary(product, sizesOnSale(items, product.ID)))))
	case 1:
		sh.Size = nil
		return s.sizeChosen(ctx, c, sh, left[0])
	}
	choice, vals := nextChoice(product, left, have)
	if choice == "" { // two the same: offer them by name
		sh.Size = nil
		for _, it := range left[:min(shopPickMax, len(left))] {
			sh.Pick = append(sh.Pick, shopChoice{Row: it.ID, Name: it.Name})
		}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.which_item", "list", numberedChoices(sh.Pick)))
	}
	sh.Pick = nil
	sh.Size = &sizeAsk{Product: product.ID, Name: product.Name, Have: have, Choice: choice, Values: vals}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.size.which", "choice", whatsapp.SafeText(strings.ToLower(choice)), "name", whatsapp.SafeText(product.Name),
		"list", numberedValues(vals)))
}

func numberedValues(vals []string) string {
	lines := make([]string, len(vals))
	for i, v := range vals {
		lines[i] = strconv.Itoa(i+1) + ". " + whatsapp.SafeText(v)
	}
	return strings.Join(lines, "\n")
}

// waSizeAnswer takes the value asked for: its number, or the value said.
func (s *Server) waSizeAnswer(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	ask := sh.Size
	text = strings.TrimSpace(text)
	chosen := ""
	if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(ask.Values) {
		chosen = ask.Values[n-1]
	} else {
		for _, v := range ask.Values {
			if matchKey(v) == matchKey(text) {
				chosen = v
			}
		}
	}
	if chosen == "" {
		return c.say(ctx, s, c.t("wa.size.again", "choice", whatsapp.SafeText(strings.ToLower(ask.Choice)), "list", numberedValues(ask.Values)))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	var product stock.Item
	for _, it := range items {
		if it.ID == ask.Product {
			product = it
		}
	}
	if product.ID == "" {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(ask.Name)))
	}
	have := map[string]string{}
	for k, v := range ask.Have {
		have[k] = v
	}
	have[ask.Choice] = chosen
	return s.askSize(ctx, c, sh, product, have)
}

// sizeChosen goes on with the stock word once its size is known.
func (s *Server) sizeChosen(ctx context.Context, c *chat, sh *chatShop, it stock.Item) error {
	switch sh.Action {
	case "stock_add":
		return s.stockAskAdd(ctx, c, sh, &it)
	case "stock_adjust":
		return s.stockAskAdjust(ctx, c, sh, it)
	case "stock_sale_size":
		ask := sh.StockChat.SaleAsk
		if ask.Chosen == nil {
			ask.Chosen = map[int]string{}
		}
		ask.Chosen[ask.At] = it.ID
		return s.saleFrom(ctx, c, ask)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return s.stockSay(ctx, c, it)
}

// sizedPick asks for the size when the matches are several sizes of one
// product (the values said in name narrowing them first); false when they
// are not.
func (s *Server) sizedPick(ctx context.Context, c *chat, sh *chatShop, name string, match []stock.Item) (bool, error) {
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return true, err
	}
	product, one := sizesOfOne(items, match)
	if !one {
		return false, nil
	}
	have := map[string]string{}
	if p, said, found := findSized(items, name); found && p.ID == product.ID {
		have = said
		// A value it does not have ("xl"): none on sale like that.
		if unknown := unknownWords(product, name); unknown != "" {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return true, err
			}
			told := strings.TrimSpace(unknown + " " + variants.Label(have, product.Choices))
			return true, c.say(ctx, s, c.t("wa.size.none", "name", whatsapp.SafeText(product.Name), "said", whatsapp.SafeText(told),
				"sizes", whatsapp.SafeText(sizesSummary(product, sizesOnSale(items, product.ID)))))
		}
	}
	return true, s.askSize(ctx, c, sh, product, have)
}

// sizesSummary says a product's sizes and colours on sale by their
// choices: "40 to 46 in Black and White".
func sizesSummary(product stock.Item, on []stock.Item) string {
	parts := make([]string, 0, len(product.Choices))
	for _, c := range product.Choices {
		var vals []string
		for _, v := range c.Values {
			if slices.ContainsFunc(on, func(it stock.Item) bool { return strings.EqualFold(it.Variant[c.Name], v) }) {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			parts = append(parts, strings.Join(vals, ", "))
		}
	}
	return strings.Join(parts, " · ")
}

// sizesGrid answers "stock Air Max 90" for a product sold in sizes and
// colours: its sizes' counts in words, by its last choice's values within
// the others ("Black: 40 × 3, 41 × 0 (low), 42 × 5"), and their total.
func sizesGrid(c *chat, product stock.Item, on []stock.Item) string {
	cs := product.Choices
	on = inChoiceOrder(product, on)
	total, counted := int64(0), true
	rows := map[string][]string{}
	var order []string
	for _, it := range on {
		head, cell := "", variants.Label(it.Variant, cs)
		if len(cs) > 1 {
			// The first choice is the cell, the others the row.
			cell = it.Variant[cs[0].Name]
			rest := make(map[string]string, len(it.Variant))
			for k, v := range it.Variant {
				if k != cs[0].Name {
					rest[k] = v
				}
			}
			head = variants.Label(rest, cs[1:])
		}
		count := "?"
		if it.Stock != nil {
			count = strconv.FormatInt(*it.Stock, 10)
			total += *it.Stock
		} else {
			counted = false
		}
		if it.Low() {
			count += " " + c.t("wa.stock.low_tag")
		}
		if _, ok := rows[head]; !ok {
			order = append(order, head)
		}
		rows[head] = append(rows[head], fmt.Sprintf("%s × %s", cell, count))
	}
	lines := make([]string, 0, len(order))
	for _, h := range order {
		line := strings.Join(rows[h], ", ")
		if h != "" {
			line = h + ": " + line
		}
		lines = append(lines, whatsapp.SafeText(line))
	}
	all := "?"
	if counted {
		all = product.Quantity(total)
	}
	return c.t("wa.stock.sizes", "name", whatsapp.SafeText(product.Name), "count", strconv.Itoa(len(on)), "total", whatsapp.SafeText(all),
		"lines", strings.Join(lines, "\n"))
}

// pricedSizes finds what a buyer's words name on a supplier's list, a
// product sold in sizes and colours by its sizes: one size by its full
// name, or by the product's name and its values in any order ("air max
// 90 42 black"); several sizes of one product when the values say too
// little (the product given, to ask by). Other items as before.
func pricedSizes(listed []links.PriceItem, words string) ([]links.PriceItem, *links.PriceItem) {
	all := orderable(listed)
	key := matchKey(words)
	for _, it := range all {
		if matchKey(it.Name) == key {
			return []links.PriceItem{it}, nil
		}
	}
	var product *links.PriceItem
	for i, it := range listed {
		k := matchKey(it.Name)
		if len(it.Choices) > 0 && k != "" && (key == k || strings.HasPrefix(key, k+" ")) && (product == nil || len(k) > len(matchKey(product.Name))) {
			product = &listed[i]
		}
	}
	if product == nil {
		var hits []links.PriceItem
		for _, it := range matchPriceItems(all, words) {
			if it.VariantOf == "" {
				hits = append(hits, it)
			}
		}
		return hits, nil
	}
	have := valuesIn(product.Choices, strings.TrimPrefix(key, matchKey(product.Name)))
	var left []links.PriceItem
	for _, it := range all {
		if it.VariantOf != product.ItemID || !it.Available {
			continue
		}
		ok := true
		for k, v := range have {
			if !strings.EqualFold(it.Variant[k], v) {
				ok = false
			}
		}
		if ok {
			left = append(left, it)
		}
	}
	if len(left) == 1 {
		return left, nil
	}
	return left, product
}

// inChoiceOrder are sizes in the product's order: by its last choice's
// values (the rows), then its first's (the cells), as the grid has them.
func inChoiceOrder(product stock.Item, sizes []stock.Item) []stock.Item {
	at := func(it stock.Item, c variants.Choice) int {
		return slices.IndexFunc(c.Values, func(v string) bool { return strings.EqualFold(v, it.Variant[c.Name]) })
	}
	out := slices.Clone(sizes)
	slices.SortStableFunc(out, func(a, b stock.Item) int {
		for i := len(product.Choices) - 1; i >= 0; i-- {
			if d := at(a, product.Choices[i]) - at(b, product.Choices[i]); d != 0 {
				return d
			}
		}
		return 0
	})
	return out
}

// Package read reads an order out of a customer's message (chat-five-more-
// flows.md, flow 1, phase 2): the model says what the words ask for, and
// this package matches that to the business's own catalogue. It proposes;
// it never decides. A word that names two things, a product sold in sizes
// with no size said, or an order with no quantity becomes a question,
// never a guess; a word that names nothing is kept as written, for staff to
// pick. Prices are only ever the catalogue's.
package read

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Limits on what one order holds and one question offers.
const (
	MaxLines    = 30
	MaxChoices  = 9
	MaxQuantity = 10_000
	// MaxNames is how many of the catalogue's names the model is shown.
	MaxNames = 400
	maxWords = 200
)

// ErrUnread is a model answer that could not be read.
var ErrUnread = errors.New("read: the model's answer could not be read")

// Said is what the model read in the words, before anything is matched.
type Said struct {
	// Orderable is false for words that ask for nothing ("hello", "how
	// much is rice?", "thank you").
	Orderable  bool       `json:"orderable"`
	Lines      []SaidLine `json:"lines"`
	Fulfilment string     `json:"fulfilment"` // delivery | pickup | ""
	Address    string     `json:"address"`
	When       string     `json:"when"`
	Note       string     `json:"note"`
	// Answers are the customer's answers to questions asked before.
	Answers []SaidAnswer `json:"answers"`
}

// SaidLine is one thing asked for.
type SaidLine struct {
	// Words are the customer's own, for this line ("2 jolof").
	Words string `json:"words"`
	// Item is the catalogue's name the words mean, exactly as listed, or ""
	// when none is clearly meant.
	Item     string       `json:"item"`
	Quantity int64        `json:"quantity"`
	Choices  []SaidChoice `json:"choices"`
}

// SaidChoice is a size, colour or other choice said with a line.
type SaidChoice struct {
	Choice string `json:"choice"`
	Value  string `json:"value"`
}

// SaidAnswer is an answer to a question asked before (by its number).
type SaidAnswer struct {
	Question int    `json:"question"`
	Answer   string `json:"answer"`
	Quantity int64  `json:"quantity"`
}

// Draft is the order as read: what staff see on its card and change.
type Draft struct {
	Lines      []Line `json:"lines"`
	Fulfilment string `json:"fulfilment,omitempty"`
	Address    string `json:"address,omitempty"`
	When       string `json:"when,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Line is one line of a draft: matched to an item, or kept as written.
type Line struct {
	Said      string `json:"said,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Quantity  int64  `json:"quantity,omitempty"`
	PriceKobo int64  `json:"price_kobo,omitempty"`
	// NoPrice: the item has no price in the catalogue; staff give one.
	NoPrice bool `json:"no_price,omitempty"`
	// Out: the item is not on sale now, or fewer are left than asked.
	Out  bool   `json:"out,omitempty"`
	Left *int64 `json:"left,omitempty"`
	// Ask is the question this line waits on.
	Ask *Question `json:"ask,omitempty"`
}

// Question kinds.
const (
	AskWhich   = "which"    // the words fit several items
	AskSize    = "size"     // a product sold in sizes, none said
	AskHowMany = "how_many" // no quantity said
)

// Question is one thing the order waits on, with the answers to tap.
type Question struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Choices []Choice `json:"choices,omitempty"`
}

// Choice is one answer to a question: an item, at its price.
type Choice struct {
	ItemID    string `json:"item_id"`
	Name      string `json:"name"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
}

// Complete reports whether every line is an item, with a quantity and a
// price, and nothing is asked: the order can be confirmed.
func (d Draft) Complete() bool {
	if len(d.Lines) == 0 {
		return false
	}
	for _, l := range d.Lines {
		if l.Ask != nil || l.ItemID == "" || l.Quantity < 1 || l.NoPrice {
			return false
		}
	}
	return true
}

// Total is what the lines that are items come to.
func (d Draft) Total() int64 {
	var t int64
	for _, l := range d.Lines {
		if l.ItemID != "" && l.Quantity > 0 {
			t += l.PriceKobo * l.Quantity
		}
	}
	return t
}

// Open are the draft's questions, in line order, with their lines.
func (d Draft) Open() []int {
	var out []int
	for i, l := range d.Lines {
		if l.Ask != nil {
			out = append(out, i)
		}
	}
	return out
}

// --- the model ---

// Schema is the model's answer's shape.
var Schema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"orderable", "lines", "fulfilment", "address", "when", "note", "answers"},
	"properties": map[string]any{
		"orderable": map[string]any{"type": "boolean", "description": "true when the words ask to buy something, or answer a question asked"},
		"lines": map[string]any{"type": "array", "description": "each thing asked for, in the order said",
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"words", "item", "quantity", "choices"},
				"properties": map[string]any{
					"words":    map[string]any{"type": "string", "description": "the customer's own words for this thing, as written"},
					"item":     map[string]any{"type": "string", "description": "the catalogue's name it clearly means, copied exactly from the list; empty when none is clearly meant or several could be"},
					"quantity": map[string]any{"type": "integer", "description": "how many (two is 2, a dozen is 12); 0 when not said"},
					"choices": map[string]any{"type": "array", "description": "a size, colour or other choice said with it (size 42, black, large); empty if none",
						"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"choice", "value"},
							"properties": map[string]any{
								"choice": map[string]any{"type": "string"},
								"value":  map[string]any{"type": "string"},
							}}},
				}}},
		"fulfilment": map[string]any{"type": "string", "enum": []string{"delivery", "pickup", ""}, "description": "delivery when they ask for it brought, pickup when they will come for it, empty when not said"},
		"address":    map[string]any{"type": "string", "description": "where to deliver, as written; empty if not said"},
		"when":       map[string]any{"type": "string", "description": "when they want it, as written (by 1pm, tomorrow morning); empty if not said"},
		"note":       map[string]any{"type": "string", "description": "anything else they ask about the order (no pepper, call when you arrive); empty if none"},
		"answers": map[string]any{"type": "array", "description": "answers to the numbered questions asked, if any",
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"question", "answer", "quantity"},
				"properties": map[string]any{
					"question": map[string]any{"type": "integer", "description": "the question's number"},
					"answer":   map[string]any{"type": "string", "description": "the choice they picked, copied from the question's choices, or their words"},
					"quantity": map[string]any{"type": "integer", "description": "a number they gave for a how-many question; 0 otherwise"},
				}}},
	},
}

// Prompt is the reader's instructions.
const Prompt = `You read a message a customer sent to a business on WhatsApp and say what they want to order.
The business sells only what is in its catalogue, listed below. For each thing asked for, give the customer's own words, how many, any size or colour said, and the catalogue's name it clearly means, copied exactly from the list.
Customers write briefly, misspell names ("jolof" for "Jollof rice"), use Nigerian Pidgin ("abeg send me 2 pack of fried rice"), abbreviations ("pls", "2pcs", "ctn" for carton) and quantities in words ("two", "a dozen").
Leave item empty when the words could mean more than one catalogue name, or none: never pick one of two, and never name something not in the list.
Leave quantity 0 when no number is said; never assume one.
A greeting, a thank-you or a question ("how much is rice?", "are you open?") orders nothing: orderable is false and lines is empty.
Give where to deliver, when, and any other request about the order only as the words say them. Never invent anything the words do not say.`

// Request is the model request reading text against the catalogue, with
// the questions asked before (numbered from 1), when the text may answer
// them.
func Request(text string, items []storefront.Item, asked []Question) ai.Request {
	var b strings.Builder
	b.WriteString("Catalogue:\n")
	for _, n := range catalogueNames(items) {
		b.WriteString("- ")
		b.WriteString(n)
		b.WriteByte('\n')
	}
	system := []ai.SystemBlock{{Text: Prompt}, {Text: b.String(), Cache: true}}
	msg := text
	if len(asked) > 0 {
		var q strings.Builder
		q.WriteString("Questions the business asked them:\n")
		for i, a := range asked {
			fmt.Fprintf(&q, "%d. %s", i+1, a.Text)
			if len(a.Choices) > 0 {
				names := make([]string, len(a.Choices))
				for j, c := range a.Choices {
					names[j] = c.Name
				}
				fmt.Fprintf(&q, " (choices: %s)", strings.Join(names, "; "))
			}
			q.WriteByte('\n')
		}
		q.WriteString("\nTheir message:\n")
		msg = q.String() + text
	}
	return ai.Request{System: system, Messages: []ai.Message{{Role: "user", Text: msg}}, Schema: Schema, MaxTokens: 1200, Effort: "low"}
}

// Parse reads the model's answer.
func Parse(resp *ai.Response) (Said, error) {
	var s Said
	if resp == nil || len(resp.JSON) == 0 || json.Unmarshal(resp.JSON, &s) != nil {
		return Said{}, ErrUnread
	}
	if len(s.Lines) > MaxLines {
		s.Lines = s.Lines[:MaxLines]
	}
	return s, nil
}

// catalogueNames are the names the model may copy: each product, and each
// size's full name, those on sale first, at most MaxNames.
func catalogueNames(items []storefront.Item) []string {
	var out []string
	for _, it := range items {
		if !it.Available || len(out) >= MaxNames {
			continue
		}
		out = append(out, it.Name)
		for _, z := range it.Sizes {
			if len(out) >= MaxNames {
				break
			}
			out = append(out, z.Name(it))
		}
	}
	return out
}

// MightOrder is a cheap check before the model is asked: some word of the
// text starts like a word of an item's name (three letters, so "jolof"
// finds "Jollof"). A message that names nothing sold is not read.
func MightOrder(text string, items []storefront.Item) bool {
	stems := map[string]bool{}
	for _, it := range items {
		if !it.Available {
			continue
		}
		for _, w := range Words(it.Name) {
			if len([]rune(w)) >= 3 && !filler[w] {
				stems[string([]rune(w)[:3])] = true
			}
		}
	}
	for _, w := range Words(text) {
		if len([]rune(w)) >= 3 && !filler[w] && stems[string([]rune(w)[:3])] {
			return true
		}
	}
	return false
}

// filler are words too common to say an item is named.
var filler = map[string]bool{"the": true, "and": true, "for": true, "with": true, "pls": true, "please": true, "abeg": true,
	"want": true, "send": true, "give": true, "pack": true, "packs": true, "piece": true, "pieces": true, "pcs": true, "plate": true, "plates": true}

var wordSplit = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// Words are s's words, lowercase.
func Words(s string) []string {
	var out []string
	for _, w := range wordSplit.Split(strings.ToLower(s), -1) {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// --- matching ---

// entry is something a line can name: a product without sizes, a product
// with sizes (its sizes then picked), or one size.
type entry struct {
	item    storefront.Item
	product *storefront.Item // a size's product
}

func (e entry) name() string { return e.item.Name }

func entries(items []storefront.Item) []entry {
	var out []entry
	for i := range items {
		it := items[i]
		if !it.Available {
			continue
		}
		out = append(out, entry{item: it})
		for _, z := range storefront.SizeItems(it) {
			out = append(out, entry{item: z, product: &items[i]})
		}
	}
	return out
}

// Match turns what the model read into a draft, against the catalogue.
func Match(s Said, items []storefront.Item) Draft {
	d := Draft{Fulfilment: s.Fulfilment, Address: clip(s.Address, 300), When: clip(s.When, 120), Note: clip(s.Note, 500)}
	if d.Fulfilment != "delivery" && d.Fulfilment != "pickup" {
		d.Fulfilment = ""
	}
	es := entries(items)
	for _, sl := range s.Lines {
		if len(d.Lines) >= MaxLines {
			break
		}
		d.Lines = append(d.Lines, matchLine(sl, es, items))
	}
	return d
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// matchLine matches one line: the model's catalogue name when it is one
// exactly, else the customer's words; one item, or a question.
func matchLine(sl SaidLine, es []entry, items []storefront.Item) Line {
	l := Line{Said: clip(sl.Words, maxWords), Quantity: sl.Quantity}
	if l.Said == "" {
		l.Said = clip(sl.Item, maxWords)
	}
	if l.Quantity < 0 || l.Quantity > MaxQuantity {
		l.Quantity = 0
	}
	var found []entry
	if sl.Item != "" {
		for _, e := range es {
			if strings.EqualFold(strings.TrimSpace(sl.Item), e.name()) {
				found = append(found, e)
			}
		}
	}
	if len(found) == 0 {
		found = byWords(l.Said, es)
	}
	switch {
	case len(found) == 0:
		// Kept as written, for staff to pick.
	case len(found) == 1:
		l = sized(l, found[0], sl.Choices, items)
	case sameProduct(found):
		// Several sizes of one product: which size, of those.
		l.Ask = sizeQuestion(*found[0].product, found)
	default:
		// The words fit several: their products, once each.
		l.Ask = which(l.Said, found)
	}
	if l.Ask == nil && l.ItemID != "" && l.Quantity < 1 {
		l.Ask = &Question{Kind: AskHowMany, Text: fmt.Sprintf("How many %s?", l.Name)}
	}
	return l
}

// sized puts an entry on a line: a product sold in sizes is narrowed by
// the choices said, to one size or a question.
func sized(l Line, e entry, said []SaidChoice, items []storefront.Item) Line {
	if e.product == nil && len(e.item.Sizes) > 0 {
		sizes := storefront.SizeItems(e.item)
		var fit []storefront.Item
		for _, z := range sizes {
			if fitsChoices(z.Variant, said) {
				fit = append(fit, z)
			}
		}
		if len(fit) == 1 {
			return put(l, fit[0])
		}
		if len(fit) == 0 {
			fit = sizes
		}
		es := make([]entry, len(fit))
		for i, z := range fit {
			es[i] = entry{item: z, product: &e.item}
		}
		l.Ask = sizeQuestion(e.item, es)
		return l
	}
	return put(l, e.item)
}

// sameProduct: every entry is a size of one product.
func sameProduct(es []entry) bool {
	for _, e := range es {
		if e.product == nil || e.product.ID != es[0].product.ID {
			return false
		}
	}
	return len(es) > 0
}

// sizeQuestion asks which of a product's sizes, at most MaxChoices.
func sizeQuestion(product storefront.Item, sizes []entry) *Question {
	q := &Question{Kind: AskSize, Text: fmt.Sprintf("Which %s?", product.Name)}
	for _, z := range sizes {
		if len(q.Choices) >= MaxChoices {
			break
		}
		q.Choices = append(q.Choices, Choice{ItemID: z.item.ID, Name: z.item.Name, PriceKobo: z.item.PriceKobo})
	}
	return q
}

// fitsChoices: every choice said is one of the size's values.
func fitsChoices(v map[string]string, said []SaidChoice) bool {
	for _, c := range said {
		val := strings.TrimSpace(c.Value)
		if val == "" {
			continue
		}
		ok := false
		for _, have := range v {
			if strings.EqualFold(have, val) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// put makes a line of an item: its name, its price, what is left.
func put(l Line, it storefront.Item) Line {
	l.ItemID, l.Name, l.Ask = it.ID, it.Name, nil
	l.NoPrice = it.PriceKobo == nil
	l.PriceKobo = 0
	if it.PriceKobo != nil {
		l.PriceKobo = *it.PriceKobo
	}
	l.Left = it.Stock
	l.Out = !it.Available || it.Stock != nil && (*it.Stock <= 0 || l.Quantity > *it.Stock)
	return l
}

// which is the question for words that fit several entries: their
// products (a size's product, once), at most MaxChoices.
func which(said string, found []entry) *Question {
	q := &Question{Kind: AskWhich, Text: fmt.Sprintf("Which one: %s?", said)}
	seen := map[string]bool{}
	for _, e := range found {
		it := e.item
		if e.product != nil {
			it = *e.product
		}
		if seen[it.ID] || len(q.Choices) >= MaxChoices {
			continue
		}
		seen[it.ID] = true
		q.Choices = append(q.Choices, Choice{ItemID: it.ID, Name: it.Name, PriceKobo: it.PriceKobo})
	}
	if len(q.Choices) > 1 {
		parts := make([]string, len(q.Choices))
		for i, c := range q.Choices {
			parts[i] = c.Name
			if c.PriceKobo != nil {
				parts[i] += " (" + storefront.FormatNaira(*c.PriceKobo) + ")"
			}
		}
		q.Text = fmt.Sprintf("%s: %s?", capital(said), orList(parts))
	}
	return q
}

func capital(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func orList(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

// numberish are a line's words that are quantities, not its item's.
var numberish = regexp.MustCompile(`^\d+(x|pcs|pc)?$|^x\d+$`)

// byWords are the entries whose names hold every word of said (a word of
// the name starting with it), quantities and fillers aside: the products
// before their sizes, the ones with the fewest other words first; all
// that fit as well, so two are never guessed between.
func byWords(said string, es []entry) []entry {
	var want []string
	for _, w := range Words(said) {
		if numberish.MatchString(w) || filler[w] || quantityWords[w] {
			continue
		}
		want = append(want, w)
	}
	if len(want) == 0 {
		return nil
	}
	best, bestExtra := []entry(nil), -1
	for _, e := range es {
		name := Words(e.name())
		if !allIn(want, name) {
			continue
		}
		extra := len(name) - len(want)
		if e.product != nil {
			// A size fits only when its own values were said.
			extra += 100
		}
		switch {
		case bestExtra < 0 || extra < bestExtra:
			best, bestExtra = []entry{e}, extra
		case extra == bestExtra:
			best = append(best, e)
		}
	}
	return best
}

func allIn(want, name []string) bool {
	for _, w := range want {
		ok := false
		for _, n := range name {
			if strings.HasPrefix(n, w) || oneOff(w, n) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// quantityWords are numbers and units in words, not an item's.
var quantityWords = map[string]bool{
	"one": true, "two": true, "three": true, "four": true, "five": true, "six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "dozen": true, "half": true, "a": true, "an": true, "of": true, "x": true,
	"carton": true, "cartons": true, "ctn": true, "ctns": true, "bag": true, "bags": true, "bottle": true, "bottles": true,
}

// --- answers ---

// Answer puts an answer on the line's question: a choice (by its number,
// or words that fit one choice only) or, for how many, a quantity. false
// when the answer settles nothing.
func Answer(d *Draft, line int, answer string, quantity int64, items []storefront.Item) bool {
	if line < 0 || line >= len(d.Lines) || d.Lines[line].Ask == nil {
		return false
	}
	l := &d.Lines[line]
	q := l.Ask
	if q.Kind == AskHowMany {
		if quantity < 1 {
			if n, err := strconv.ParseInt(strings.TrimSpace(answer), 10, 64); err == nil {
				quantity = n
			}
		}
		if quantity < 1 || quantity > MaxQuantity {
			return false
		}
		l.Quantity = quantity
		*l = put(*l, itemOf(items, l.ItemID, l.Name, l.PriceKobo))
		return true
	}
	c, ok := ChoiceIn(answer, *q)
	if !ok {
		return false
	}
	return Choose(d, line, c.ItemID, items)
}

// ChoiceIn is the choice an answer names: "2" the second, else the one
// choice whose name holds every word of the answer.
func ChoiceIn(answer string, q Question) (Choice, bool) {
	a := strings.TrimSpace(answer)
	if n, err := strconv.Atoi(strings.TrimSuffix(a, ".")); err == nil {
		if n >= 1 && n <= len(q.Choices) {
			return q.Choices[n-1], true
		}
		return Choice{}, false
	}
	for _, c := range q.Choices {
		if strings.EqualFold(a, c.Name) {
			return c, true
		}
	}
	want := Words(a)
	var fit []Choice
	for _, c := range q.Choices {
		if len(want) > 0 && allIn(want, Words(c.Name)) {
			fit = append(fit, c)
		}
	}
	if len(fit) == 1 {
		return fit[0], true
	}
	return Choice{}, false
}

// Choose puts an item on a line (staff picking, or an answer): a product
// sold in sizes asks its size next; no quantity asks how many.
func Choose(d *Draft, line int, itemID string, items []storefront.Item) bool {
	if line < 0 || line >= len(d.Lines) {
		return false
	}
	for _, e := range entries(items) {
		if e.item.ID != itemID {
			continue
		}
		l := d.Lines[line]
		l.Ask = nil
		l = sized(l, e, nil, items)
		if l.Ask == nil && l.Quantity < 1 {
			l.Ask = &Question{Kind: AskHowMany, Text: fmt.Sprintf("How many %s?", l.Name)}
		}
		d.Lines[line] = l
		return true
	}
	return false
}

// itemOf is the item a line names, as the catalogue has it now (or as the
// line has it when it is gone from the catalogue).
func itemOf(items []storefront.Item, id, name string, price int64) storefront.Item {
	for _, e := range entries(items) {
		if e.item.ID == id {
			return e.item
		}
	}
	return storefront.Item{ID: id, Name: name, PriceKobo: &price}
}

// Refresh puts the catalogue's prices and what is left on the lines that
// are items (before a card is shown or confirmed): a price staff set is
// kept when keep says so.
func Refresh(d *Draft, items []storefront.Item, keep func(i int) bool) {
	for i, l := range d.Lines {
		if l.ItemID == "" {
			continue
		}
		price, noPrice := l.PriceKobo, l.NoPrice
		ask := l.Ask
		nl := put(l, itemOf(items, l.ItemID, l.Name, l.PriceKobo))
		nl.Ask = ask
		if keep != nil && keep(i) {
			nl.PriceKobo, nl.NoPrice = price, noPrice
		}
		d.Lines[i] = nl
	}
}

// ApplyAnswers puts the customer's answers (as the model read them) on the
// open questions, asked in the order Open gives them; new lines the
// message asks for are added. It reports whether anything changed.
func ApplyAnswers(d *Draft, s Said, items []storefront.Item) bool {
	open := d.Open()
	changed := false
	for _, a := range s.Answers {
		i := a.Question - 1
		if i < 0 || i >= len(open) {
			continue
		}
		if Answer(d, open[i], a.Answer, a.Quantity, items) {
			changed = true
		}
	}
	if d.Address == "" && s.Address != "" {
		d.Address, changed = clip(s.Address, 300), true
	}
	if d.Fulfilment == "" && (s.Fulfilment == "delivery" || s.Fulfilment == "pickup") {
		d.Fulfilment, changed = s.Fulfilment, true
	}
	if d.When == "" && s.When != "" {
		d.When, changed = clip(s.When, 120), true
	}
	more := Match(Said{Lines: s.Lines}, items)
	for _, l := range more.Lines {
		if len(d.Lines) >= MaxLines {
			break
		}
		d.Lines = append(d.Lines, l)
		changed = true
	}
	return changed
}

// Questions are the open questions as one message to the customer,
// numbered when there are several, each choice numbered too.
func Questions(d Draft) string {
	open := d.Open()
	var b strings.Builder
	for n, i := range open {
		q := d.Lines[i].Ask
		if len(open) > 1 {
			fmt.Fprintf(&b, "%d. ", n+1)
		}
		b.WriteString(q.Text)
		if len(q.Choices) > 1 && q.Kind == AskSize {
			parts := make([]string, len(q.Choices))
			for j, c := range q.Choices {
				parts[j] = c.Name
				if c.PriceKobo != nil {
					parts[j] += " (" + storefront.FormatNaira(*c.PriceKobo) + ")"
				}
			}
			b.WriteString(" " + orList(parts))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// oneOff: a word of four letters or more one letter from another (one
// added, missing or changed): "jolof" is "jollof", "chapmn" "chapman".
// Two items it fits are still a question.
func oneOff(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 4 || len(rb) < 4 {
		return false
	}
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	if len(rb)-len(ra) > 1 {
		return false
	}
	i, j, diff := 0, 0, 0
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		diff++
		if diff > 1 {
			return false
		}
		if len(ra) == len(rb) {
			i++
		}
		j++
	}
	return diff+(len(rb)-j)+(len(ra)-i) <= 1
}

package api

// Chat tools beyond the web (docs/spec/chat-tools.md §4): on WhatsApp,
// Telegram and Slack, "tools" (or "/tools", "/products") answers with links
// that open each tool on the web; and a typed request ("change the price
// of rice 50kg to 80k", "20 bags of rice came in", "add a service: rice
// milling, 30 min, 2500") is read by the model and answered with a link
// to the form, filled in. The web's chip asks the same reader
// (POST /v1/chat-tools/request) when its own patterns find nothing.
// Nothing is ever saved from words: the form's Save does it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
)

// chatTool is a tool as the chat apps name it.
type chatTool struct {
	key, title, perm string
}

var chatTools = []chatTool{
	// Staff's (phase 5): a counter sale, and a shift's handover (a shift
	// manager's only, staffTools).
	{"sell", "Sell", PermTableEdit},
	{"handover", "Handover", PermTableRead},
	{"products", "Products", PermTableRead},
	{"services", "Services", PermStorefrontManage},
	{"stock", "Stock", PermTableRead},
	{"prices", "Price lists", PermStorefrontManage},
	{"orders", "Orders", PermOrders},
	{"tables", "Tables", PermTableRead},
	// A buyer's (phase 3): the orders sent to suppliers, and their bills.
	{"purchases", "Your orders", PermTableRead},
	{"bills", "Bills", PermInvoiceRead},
	// The money a business is owed and has: its invoices, and the money log.
	{"invoices", "Invoices", PermInvoiceRead},
	{"money", "Money", PermInvoiceRead},
}

// toolRequest is a typed request: the tool, the row it is about (or a new
// one), and what the form starts with.
type toolRequest struct {
	Tool    string     `json:"tool"`
	RowID   string     `json:"row_id,omitempty"`
	Add     bool       `json:"add,omitempty"`
	Prefill toolFilled `json:"prefill"`
	Words   string     `json:"words"`
}

type toolFilled struct {
	PriceKobo *int64 `json:"price_kobo,omitempty"`
	Count     *int64 `json:"count,omitempty"`
	Name      string `json:"name,omitempty"`
	Minutes   *int64 `json:"minutes,omitempty"`
}

// toolLink opens a tool on the web, in the Taskiem conversation, at a
// request's form when there is one.
func (s *Server) toolLink(tool string, r *toolRequest) string {
	q := url.Values{"with": {"taskiem"}, "tool": {tool}}
	if r != nil {
		if r.RowID != "" {
			q.Set("row", r.RowID)
		}
		if r.Add {
			q.Set("add", "1")
		}
		if r.Prefill.PriceKobo != nil {
			q.Set("price", strconv.FormatInt(*r.Prefill.PriceKobo, 10))
		}
		if r.Prefill.Count != nil {
			q.Set("count", strconv.FormatInt(*r.Prefill.Count, 10))
		}
		if r.Prefill.Name != "" {
			q.Set("name", r.Prefill.Name)
		}
		if r.Prefill.Minutes != nil {
			q.Set("minutes", strconv.FormatInt(*r.Prefill.Minutes, 10))
		}
	}
	return strings.TrimRight(s.PublicURL, "/") + "/chat?" + q.Encode()
}

// waToolsCommand answers "tools", "/tools" and "/<tool>" with chat tools
// on: links to the tools the person may open (false: not one).
func (s *Server) waToolsCommand(ctx context.Context, c *chat, cmd string) (bool, error) {
	if !c.tools || c.state != stateIdle {
		return false, nil
	}
	want := ""
	switch {
	case cmd == "tools" || cmd == "/tools" || cmd == "/":
	case strings.HasPrefix(cmd, "/") && !strings.Contains(cmd, " "):
		want = strings.TrimPrefix(cmd, "/")
	default:
		return false, nil
	}
	var lines []string
	only := staffTools(c.p.Roles)
	for _, t := range chatTools {
		if !c.p.Can(t.perm) || (only != nil && !only[t.key]) || (only == nil && t.key == "handover") {
			continue
		}
		if want != "" && !strings.HasPrefix(t.key, want) && !strings.HasPrefix(strings.ToLower(t.title), want) {
			continue
		}
		lines = append(lines, fmt.Sprintf("• %s: %s", t.title, s.toolLink(t.key, nil)))
	}
	// "/start" and the like are not tools: left to what reads them.
	if len(lines) == 0 {
		return false, nil
	}
	head := "Your tools, on the web:"
	if want != "" {
		head = "Open it on the web:"
	}
	return true, c.say(ctx, s, head+"\n"+strings.Join(lines, "\n"))
}

// roleTools are the tools of staff who work from chat (as the web's
// lib/tools.ts ROLE_TOOLS): a cashier sells, keeps the stock and works the
// orders; a shift manager also hands the shift over.
var roleTools = map[string][]string{
	"cashier":       {"sell", "orders", "stock"},
	"shift_manager": {"handover", "sell", "orders", "stock", "bills"},
}

// staffTools are the tools a person's roles shape them to, or nil when one
// of their roles is not a staff role (they see every tool they may).
func staffTools(roles []string) map[string]bool {
	if len(roles) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, r := range roles {
		keys, ok := roleTools[r]
		if !ok {
			return nil
		}
		for _, k := range keys {
			out[k] = true
		}
	}
	return out
}

// mightBeToolRequest is whether words may ask for something a tool does,
// before the model is asked.
var mightBeToolRequest = regexp.MustCompile(`(?i)\b(price|came in|arrived|received|stock in|service|accept)\b`)

// waToolRequest reads a typed request no command claimed and answers with
// a link to its form, filled in (false: not one).
func (s *Server) waToolRequest(ctx context.Context, c *chat, text string) (bool, error) {
	if !c.tools || c.state != stateIdle || !mightBeToolRequest.MatchString(text) {
		return false, nil
	}
	if !s.limiter("tool-request:"+c.tenant.ID.String()+":"+c.addr, time.Minute, 10).Allow() {
		return false, nil
	}
	r, ok := s.readToolRequest(ctx, c.p, text)
	if !ok {
		return false, nil
	}
	return true, c.say(ctx, s, fmt.Sprintf("%s?\nOpen the form, filled in, and Save there: %s", r.Words, s.toolLink(r.Tool, &r)))
}

// postChatToolRequest answers POST /v1/chat-tools/request with {"text":
// …}: the request it makes, or none.
func (s *Server) postChatToolRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	text := strings.TrimSpace(in.Text)
	if text == "" || len([]rune(text)) > 300 {
		s.fail(w, r, fmt.Errorf("%w: the words are 1 to 300 characters", errBadRequest))
		return
	}
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, err = chatToolsOn(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !on || p.UserID == uuid.Nil {
		writeJSON(w, http.StatusOK, map[string]any{"request": nil})
		return
	}
	if !s.limiter("tool-request:"+p.UserID.String(), time.Minute, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down: a moment between requests")
		return
	}
	req, ok := s.readToolRequest(r.Context(), p, text)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"request": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": req})
}

// toolAnswer is what the model reads out of the words.
type toolAnswer struct {
	Tool       string  `json:"tool"`
	Thing      string  `json:"thing"`
	PriceNaira float64 `json:"price_naira"`
	Count      int64   `json:"count"`
	Name       string  `json:"name"`
	Minutes    int64   `json:"minutes"`
	// Choices are a size, a colour or another choice heard with the
	// product ("size 42", "black"), as the words say them.
	Choices []toolChoice `json:"choices"`
}

type toolChoice struct {
	Choice string `json:"choice"`
	Value  string `json:"value"`
}

var toolSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"tool", "thing", "price_naira", "count", "name", "minutes", "choices"},
	"properties": map[string]any{
		"tool":        map[string]any{"type": "string", "enum": []string{"price", "stock_in", "new_service", "none"}},
		"thing":       map[string]any{"type": "string", "description": "the product named, as the words name it; empty if none"},
		"price_naira": map[string]any{"type": "number", "description": "a new price in naira (80k is 80000); 0 if none"},
		"count":       map[string]any{"type": "integer", "description": "how many came in; 0 if none"},
		"name":        map[string]any{"type": "string", "description": "a new service's name; empty if none"},
		"minutes":     map[string]any{"type": "integer", "description": "how long a new service takes in minutes; 0 if none"},
		"choices": map[string]any{"type": "array", "description": "a size, colour or other choice said with the product (size 42, black, XL), each with what it is; empty if none",
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"choice", "value"},
				"properties": map[string]any{
					"choice": map[string]any{"type": "string", "description": "Size, Colour, or the choice's name as said"},
					"value":  map[string]any{"type": "string", "description": "its value as said: 42, Black, XL"},
				}}},
	},
}

const toolPrompt = `You read a short message a business owner typed in a chat and say whether it asks to:
- change a product's price ("price": the product, the new price),
- record goods that came in ("stock_in": the product, how many),
- add a new service ("new_service": its name, minutes it takes, its price).
A product may be named with its category ("the Nike sneakers") and with a size, colour or other choice ("Air Max 90 size 42 in black"): the thing is the product's name without them, and each choice said goes in choices.
Otherwise "none". A question ("how much is rice?") is "none". Never invent a product, a number or a name the words do not give.`

// readToolRequest asks the model what a request asks for and matches the
// thing it names to one of the person's products (never guessing between
// two). Nothing without a model, the budget, or the chat tools' tools the
// person may open.
func (s *Server) readToolRequest(ctx context.Context, p *Principal, text string) (toolRequest, bool) {
	model := s.chatModel()
	if model == nil {
		return toolRequest{}, false
	}
	limit, used, err := s.aiBudget(ctx, p.TenantID)
	if err != nil || (limit > 0 && used >= limit) {
		return toolRequest{}, false
	}
	req := ai.Request{System: []ai.SystemBlock{{Text: toolPrompt, Cache: true}}, Messages: []ai.Message{{Role: "user", Text: text}}, Schema: toolSchema, MaxTokens: 300}
	release, err := s.reserveAI(ctx, p.TenantID, "tool_request", req.Estimate(), chatAIReservation)
	if err != nil {
		return toolRequest{}, false
	}
	resp, err := model.Complete(ctx, req)
	var a toolAnswer
	outcome, errText := "valid", ""
	switch {
	case err != nil:
		outcome, errText = "error", err.Error()
	case resp == nil || len(resp.JSON) == 0 || json.Unmarshal(resp.JSON, &a) != nil:
		outcome = "unparseable"
	case a.Tool == "none":
		outcome = "unknown"
	}
	s.recordModelCallFor(ctx, p.TenantID, p.Actor(), "tool_request", "toolrequest", model, resp, outcome, errText)
	release()
	if outcome != "valid" {
		if outcome == "error" && !errors.Is(err, context.Canceled) {
			s.Logger.Warn("chat tools: reading a request", "err", err)
		}
		return toolRequest{}, false
	}
	return s.matchToolAnswer(ctx, p, a)
}

// matchToolAnswer turns the model's answer into a request the person may
// act on: the product found among theirs, the numbers in range.
func (s *Server) matchToolAnswer(ctx context.Context, p *Principal, a toolAnswer) (toolRequest, bool) {
	switch a.Tool {
	case "new_service":
		if !p.Can(PermStorefrontManage) || !p.Can(PermTableEdit) {
			return toolRequest{}, false
		}
		name := strings.TrimSpace(a.Name)
		if name == "" || len([]rune(name)) > 80 || a.Minutes < 5 || a.Minutes > 480 {
			return toolRequest{}, false
		}
		r := toolRequest{Tool: "services", Add: true, Prefill: toolFilled{Name: name, Minutes: &a.Minutes}}
		r.Words = fmt.Sprintf("Add the service %s, %d min", name, a.Minutes)
		if a.PriceNaira > 0 && a.PriceNaira < 1e9 {
			k := int64(math.Round(a.PriceNaira * 100))
			r.Prefill.PriceKobo = &k
			r.Words += ", " + naira(k)
		}
		return r, true
	case "price", "stock_in":
		if !p.Can(PermTableEdit) {
			return toolRequest{}, false
		}
		_, items, err := s.stockItems(ctx, p.TenantID)
		if err != nil {
			return toolRequest{}, false
		}
		it, ok := matchStockThing(items, a)
		if !ok {
			return toolRequest{}, false
		}
		if a.Tool == "price" {
			if a.PriceNaira <= 0 || a.PriceNaira >= 1e9 {
				return toolRequest{}, false
			}
			k := int64(math.Round(a.PriceNaira * 100))
			return toolRequest{Tool: "products", RowID: it.id, Prefill: toolFilled{PriceKobo: &k}, Words: fmt.Sprintf("Change the price of %s to %s", it.name, naira(k))}, true
		}
		if a.Count <= 0 || a.Count > 1e6 {
			return toolRequest{}, false
		}
		return toolRequest{Tool: "stock", RowID: it.id, Prefill: toolFilled{Count: &a.Count}, Words: fmt.Sprintf("Record %d of %s coming in", a.Count, it.name)}, true
	}
	return toolRequest{}, false
}

type namedThing struct{ id, name string }

var wordSplit = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func thingWords(s string) []string {
	var out []string
	for _, w := range wordSplit.Split(strings.ToLower(s), -1) {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// matchThing is the thing a few words name: every word of them found at
// the start of a word of its name, the one with the fewest other words
// first; none when two fit as well (never a guess).
func matchThing(said string, things []namedThing) (namedThing, bool) {
	var want []string
	for _, w := range thingWords(said) {
		switch w {
		case "the", "of", "a", "an", "my", "our":
		default:
			want = append(want, w)
		}
	}
	if len(want) == 0 {
		return namedThing{}, false
	}
	type fit struct {
		t namedThing
		n int
	}
	var fits []fit
	for _, t := range things {
		have := thingWords(t.name)
		all := true
		for _, w := range want {
			found := false
			for _, h := range have {
				if strings.HasPrefix(h, w) || (len(w) > 3 && strings.HasPrefix(h, strings.TrimSuffix(w, "s"))) {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if all {
			fits = append(fits, fit{t, len(have)})
		}
	}
	if len(fits) == 0 {
		return namedThing{}, false
	}
	sort.SliceStable(fits, func(i, j int) bool { return fits[i].n < fits[j].n })
	if len(fits) > 1 && fits[0].n == fits[1].n {
		return namedThing{}, false
	}
	return fits[0].t, true
}

// matchStockThing is the stock item a request names: a product's size by
// its full name, or by the product and the choices heard (the model's, and
// any values in the words); a product's category words count with its
// name. A product sold in sizes whose size is not said is the product
// (its form or grid; never a guess between sizes). Never a guess between
// two others either.
func matchStockThing(items []stock.Item, a toolAnswer) (namedThing, bool) {
	named := make([]namedThing, 0, len(items))
	for _, it := range items {
		if it.Retired() {
			continue
		}
		named = append(named, namedThing{id: it.ID, name: strings.TrimSpace(it.Name + " " + it.Category)})
	}
	said := a.Thing
	for _, c := range a.Choices {
		said += " " + c.Value
	}
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	// The product and its values, when it is one sold in sizes.
	if product, have, ok := findSized(items, said); ok {
		// Each choice heard: by its name, else wherever its value is one
		// ("color" for Colour, "shoe size" for Size).
		for _, c := range a.Choices {
			val := strings.TrimSpace(c.Value)
			named := slices.IndexFunc(product.Choices, func(pc variants.Choice) bool { return strings.EqualFold(pc.Name, strings.TrimSpace(c.Choice)) })
			for i, pc := range product.Choices {
				if named >= 0 && i != named {
					continue
				}
				for _, v := range pc.Values {
					if strings.EqualFold(v, val) {
						have[pc.Name] = v
					}
				}
			}
		}
		// A choice heard that it does not have ("XL" of a kaftan in S and
		// M): the product itself, never the one size the rest leave.
		if unknownWords(product, said) != "" {
			return namedThing{id: product.ID, name: product.Name}, true
		}
		left := narrowSizes(items, product.ID, have)
		if len(left) == 1 {
			return namedThing{id: left[0].ID, name: left[0].Name}, true
		}
		return namedThing{id: product.ID, name: product.Name}, true
	}
	t, ok := matchThing(said, named)
	if !ok {
		return namedThing{}, false
	}
	return namedThing{id: t.id, name: byID[t.id].Name}, true
}

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
)

// chatTool is a tool as the chat apps name it.
type chatTool struct {
	key, title, perm string
}

var chatTools = []chatTool{
	{"products", "Products", PermTableRead},
	{"services", "Services", PermStorefrontManage},
	{"stock", "Stock", PermTableRead},
	{"prices", "Price lists", PermStorefrontManage},
	{"orders", "Orders", PermOrders},
	{"tables", "Tables", PermTableRead},
	// A buyer's (phase 3): the orders sent to suppliers, and their bills.
	{"purchases", "Your orders", PermTableRead},
	{"bills", "Bills", PermInvoiceRead},
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
	for _, t := range chatTools {
		if !c.p.Can(t.perm) {
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
}

var toolSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"tool", "thing", "price_naira", "count", "name", "minutes"},
	"properties": map[string]any{
		"tool":        map[string]any{"type": "string", "enum": []string{"price", "stock_in", "new_service", "none"}},
		"thing":       map[string]any{"type": "string", "description": "the product named, as the words name it; empty if none"},
		"price_naira": map[string]any{"type": "number", "description": "a new price in naira (80k is 80000); 0 if none"},
		"count":       map[string]any{"type": "integer", "description": "how many came in; 0 if none"},
		"name":        map[string]any{"type": "string", "description": "a new service's name; empty if none"},
		"minutes":     map[string]any{"type": "integer", "description": "how long a new service takes in minutes; 0 if none"},
	},
}

const toolPrompt = `You read a short message a business owner typed in a chat and say whether it asks to:
- change a product's price ("price": the product, the new price),
- record goods that came in ("stock_in": the product, how many),
- add a new service ("new_service": its name, minutes it takes, its price).
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
		named := make([]namedThing, 0, len(items))
		for _, it := range items {
			named = append(named, namedThing{id: it.ID, name: it.Name})
		}
		it, ok := matchThing(a.Thing, named)
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

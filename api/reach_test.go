package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/reach"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const reachFormHost = "forms.taskiem.test"

// reachWorld is a deployment with the WhatsApp number, the Telegram bot,
// the form host and updates for people outside a business (N11).
type reachWorld struct {
	*world
	graph *whatsapptest.Graph
	bot   *telegramtest.Bot
	forms *forms.Service
	host  http.Handler
	wa    string // the WhatsApp webhook
	tg    string // the Telegram webhook
	seq   atomic.Int64
}

func newReachWorld(t *testing.T) *reachWorld {
	e := rt.New(t)
	ctx := context.Background()
	g := whatsapptest.New(t, waPNID, waToken)
	wa := whatsapp.New(e.Store.Pool, whatsapp.Config{PhoneNumberID: waPNID, AccessToken: waToken, AppSecret: waSecret, VerifyToken: waVerify,
		TokenKey: waKey, GraphURL: g.URL, DisplayNumber: "+15550001111"}, e.Egress, slog.New(slog.DiscardHandler))
	wa.Secrets = e.Vault
	wa.Meter = &whatsapp.UsageMeter{Pool: e.Store.Pool, Allowance: func(ctx context.Context, t uuid.UUID) (int64, error) {
		l, err := e.Store.LimitsFor(ctx, t)
		return l.WhatsAppTemplatesMonthly, err
	}}
	bot := telegramtest.New(t, tgToken)
	bot.Username = tgBot
	tg := telegram.New(e.Store.Pool, telegram.Config{BotToken: tgToken, WebhookSecret: tgSecret, BotUsername: tgBot, APIURL: bot.URL},
		e.Egress, slog.New(slog.DiscardHandler))
	keys, err := forms.NewKeys(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{8}, 32)
	msgs := &sender.Queue{Vault: e.Vault, HashKey: hash}
	book := &reach.Book{Pool: e.Store.Pool, Vault: e.Vault, HashKey: hash}
	if err := e.Registry.Register(notify.New(&notify.Notifier{Pool: e.Store.Pool, Chat: []string{"whatsapp", "telegram"}, Messages: msgs, Reach: book})); err != nil {
		t.Fatal(err)
	}
	svc := &forms.Service{Pool: e.DB.App, Vault: e.Vault, Keys: keys, Messages: msgs, Start: e.Store.Start, Signal: e.Store.DeliverSignalOnce, FormHost: reachFormHost}
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, Connectors: e.Connectors, AllowSignup: true, Egress: e.Egress,
		Logger: slog.New(slog.DiscardHandler), WhatsApp: wa, Telegram: tg, PublicURL: waPublicURL, Forms: svc,
		Reach: book, ReachKeys: &keys, ReachHost: reachFormHost, Messages: msgs}
	mux := http.NewServeMux()
	hooks := http.StripPrefix("/channels/whatsapp", srv.WhatsAppHooks())
	mux.Handle("/channels/whatsapp", hooks)
	mux.HandleFunc("/channels/telegram", srv.TelegramHooks().ServeHTTP)
	mux.Handle("/", specCheck(t, srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := reach.SetTargets(ctx, e.Store.Pool, tgBot, "+15550001111"); err != nil {
		t.Fatal(err)
	}
	pp := e.DB.PublicPool(t, 2)
	host := &publicweb.Server{Lookup: publicweb.DBLookup(pp), Origins: publicweb.DBOrigins(pp), Forms: publicweb.DBForms(pp), Reach: publicweb.DBReach(pp),
		FormHost: reachFormHost, FormKeys: &keys, Logger: slog.New(slog.DiscardHandler), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		FormSubmissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	return &reachWorld{world: &world{env: e, base: ts.URL, srv: srv}, graph: g, bot: bot, forms: svc, host: host.Handler(),
		wa: ts.URL + "/channels/whatsapp", tg: ts.URL + "/channels/telegram"}
}

// page asks the form host for path (a GET, or a POST of v).
func (w *reachWorld) page(t *testing.T, path string, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if v == nil {
		r = httptest.NewRequest("GET", path, nil)
	} else {
		r = httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Host = reachFormHost
	rec := httptest.NewRecorder()
	w.host.ServeHTTP(rec, r)
	return rec
}

var (
	reachTokenRe = regexp.MustCompile(`name="token" value="([^"]+)"`)
	reachStateRe = regexp.MustCompile(`name="state" value="([A-Za-z0-9_-]*)"`)
	tgLinkRe     = regexp.MustCompile(`href="https://t\.me/` + tgBot + `\?start=u_([A-Za-z0-9_-]{32})"`)
	waLinkRe     = regexp.MustCompile(`href="(https://wa\.me/15550001111\?text=[^"]+)"`)
)

// fill fills in a one-page form and returns the thank-you page.
func (w *reachWorld) fill(t *testing.T, public string, answers url.Values) string {
	t.Helper()
	first := w.page(t, "/f/"+public, nil)
	tm, sm := reachTokenRe.FindStringSubmatch(first.Body.String()), reachStateRe.FindStringSubmatch(first.Body.String())
	if first.Code != 200 || tm == nil || sm == nil {
		t.Fatalf("the form: %d %s", first.Code, first.Body.String())
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond) // a person takes a few seconds
	v := url.Values{"token": {tm[1]}, "state": {sm[1]}, "website": {""}, "do": {"send"}}
	for k, x := range answers {
		v["a."+k] = x
	}
	done := w.page(t, "/f/"+public, v)
	if done.Code != 200 || !strings.Contains(done.Body.String(), "Thank you") {
		t.Fatalf("sending the form: %d %s", done.Code, done.Body.String())
	}
	return done.Body.String()
}

// started drains the form inbox and the engine.
func (w *reachWorld) started(t *testing.T) {
	t.Helper()
	time.Sleep(2100 * time.Millisecond) // the inbox waits 2 s for a submission's files
	if err := w.forms.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
}

func (w *reachWorld) post(t *testing.T, hook string, body []byte, hdr ...string) {
	t.Helper()
	req, _ := http.NewRequest("POST", hook, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d", hook, resp.StatusCode)
	}
}

// telegram delivers an update from an account and returns what the bot
// sent it meanwhile.
func (w *reachWorld) telegram(t *testing.T, account int64, build func(id int64) []byte) []telegramtest.Call {
	t.Helper()
	before := len(w.bot.Sent(account))
	w.post(t, w.tg, build(w.seq.Add(1)), "X-Telegram-Bot-Api-Secret-Token", tgSecret)
	return w.bot.Sent(account)[before:]
}

// whatsapp delivers a text from a number and returns what went back.
func (w *reachWorld) whatsapp(t *testing.T, from, text string) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(waPNID, from, fmt.Sprintf("wamid.reach%d", w.seq.Add(1)), whatsapptest.Text(text))
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	return w.graph.Sent(from)[before:]
}

func (w *reachWorld) tick(t *testing.T) {
	t.Helper()
	if err := w.srv.WhatsAppTick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// texts counts SMS queued to people outside the business.
func (w *reachWorld) texts(t *testing.T) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages WHERE channel = 'sms' AND purpose = 'notice'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const leaveFlow = `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave request",
 "trigger":{"type":"form","config":{"title":"Leave request","access":"public","after":{"mode":"message"}}},
 "inputs":{"schema":{"type":"object","required":["name","phone"],"properties":{
   "name":{"type":"string","x-pii":"name"},"phone":{"type":"string","x-pii":"phone"}}}},
 "steps":[
  {"id":"decide","type":"approval","name":"Approve the leave","config":{"role":"manager"}},
  {"id":"tell","type":"connector","name":"Tell them","connector":"notify@1","action":"message","needs":["decide"],
   "input":{"phone":"=trigger.body.phone","about":"your leave request","title":"Leave request","message":"=steps.decide.output.decision == 'approved' ? 'Your leave is approved.' : 'Your leave was declined.'"}}]}`

const shiftFlow = `{"schema":"wd/v1","id":"wf_shift","version":1,"name":"Shift reminder","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","required":["phone"],"properties":{"phone":{"type":"string","x-pii":"phone"}}}},
 "steps":[{"id":"tell","type":"connector","connector":"notify@1","action":"message",
   "input":{"phone":"=trigger.body.phone","about":"your shift","message":"Your shift starts at 8am tomorrow."}}]}`

// TestReachingPeopleOutside (no-technical-steps N11): staff who are not on
// Taskiem get a workflow's messages on the WhatsApp or Telegram they chose
// with one tap, and nobody types a chat id.
func TestReachingPeopleOutside(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	leave := publishFlow(t, owner, leaveFlow)
	var public string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM form_endpoints WHERE workflow_id = $1`, leave).Scan(&public); err != nil {
		t.Fatal(err)
	}

	// Bola asks for leave: the thank-you page offers both apps.
	done := w.fill(t, public, url.Values{"name": {"Bola Ade"}, "phone": {"0803 123 4567"}})
	tgm, wam := tgLinkRe.FindStringSubmatch(done), waLinkRe.FindStringSubmatch(done)
	if tgm == nil || wam == nil || !strings.Contains(done, "Get updates on your phone") {
		t.Fatalf("the thank-you page: %s", done)
	}
	w.started(t)

	// Bola taps Telegram: updates about this request come there, and the
	// bot asks for the number to send all of them.
	const bola = int64(7001)
	got := w.telegram(t, bola, func(id int64) []byte { return telegramtest.Start(id, bola, "", "u_"+tgm[1]) })
	if len(got) != 1 || !got[0].AskContact || !strings.Contains(got[0].Text, "Ada Stores about your request") {
		t.Fatalf("opening the link: %+v", got)
	}
	// The link is used up.
	got = w.telegram(t, bola, func(id int64) []byte { return telegramtest.Start(id, bola, "", "u_"+tgm[1]) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "expired or was used") {
		t.Fatalf("the link again: %+v", got)
	}

	// The manager approves: the decision reaches Bola on Telegram.
	runs := owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(runs) != 1 {
		t.Fatalf("approvals: %v", runs)
	}
	run := runs[0].(map[string]any)["run_id"].(string)
	owner.must(200, "POST", "/v1/approvals/"+run+"/decide", map[string]any{"decision": "approved"})
	w.env.Drain(t)
	w.tick(t)
	if last := w.bot.Last(t, bola); !strings.Contains(last.Text, "Your leave is approved") || !strings.Contains(last.Text, "Ada Stores") {
		t.Fatalf("the decision: %+v", last)
	}
	if w.texts(t) != 0 {
		t.Fatal("texted as well")
	}

	// A shift reminder to the same phone, in another workflow: the link was
	// about the leave request only, so it goes by SMS.
	shift := publishFlow(t, owner, shiftFlow)
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": "08031234567"}})
	w.env.Drain(t)
	if w.texts(t) != 1 {
		t.Fatalf("texts: %d", w.texts(t))
	}

	// Someone else's number is refused; Bola's own links every workflow.
	got = w.telegram(t, bola, func(id int64) []byte { return telegramtest.ContactMessage(id, bola, "+2348090000000", 9999) })
	if len(got) != 1 || !got[0].AskContact || !strings.Contains(got[0].Text, "your own number") {
		t.Fatalf("another's number: %+v", got)
	}
	got = w.telegram(t, bola, func(id int64) []byte { return telegramtest.ContactMessage(id, bola, "+234 803 123 4567", bola) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "You'll get updates from Ada Stores here") {
		t.Fatalf("sharing the number: %+v", got)
	}
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": "+2348031234567"}})
	w.env.Drain(t)
	w.tick(t)
	if last := w.bot.Last(t, bola); !strings.Contains(last.Text, "Your shift starts") || w.texts(t) != 1 {
		t.Fatalf("the shift on Telegram: %+v, %d texts", last, w.texts(t))
	}

	// Chidi taps WhatsApp on the business's standing page: WhatsApp proves
	// the number, so every workflow reaches them there at once.
	page := owner.must(200, "GET", "/v1/reach", nil)
	pageURL, _ := page["page_url"].(string)
	if page["telegram"] != true || page["whatsapp"] != true || !strings.HasPrefix(pageURL, "https://"+reachFormHost+"/u/") {
		t.Fatalf("reach: %v", page)
	}
	path := strings.TrimPrefix(pageURL, "https://"+reachFormHost)
	if body := w.page(t, path, nil).Body.String(); !strings.Contains(body, "Get updates from Ada Stores") || !strings.Contains(body, path+"/whatsapp") {
		t.Fatalf("the standing page: %s", body)
	}
	to := w.page(t, path+"/whatsapp", nil)
	loc, _ := url.Parse(to.Header().Get("Location"))
	if to.Code != 303 || loc.Host != "wa.me" || !strings.Contains(loc.Query().Get("text"), "Send me updates from Ada Stores here. Code U-") {
		t.Fatalf("the WhatsApp button: %d %v", to.Code, loc)
	}
	const chidi = "+2348039998888"
	sent := w.whatsapp(t, chidi, loc.Query().Get("text"))
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "You'll get updates from Ada Stores here") {
		t.Fatalf("Chidi links: %+v", sent)
	}
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": "0803 999 8888"}})
	w.env.Drain(t)
	w.tick(t)
	if last := w.graph.Last(t, chidi); last.Type != "text" || !strings.Contains(last.Text, "Your shift starts") {
		t.Fatalf("Chidi's shift: %+v", last)
	}

	// A day later, outside WhatsApp's window, the update template carries it.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, chidi); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": chidi}})
	w.env.Drain(t)
	w.tick(t)
	if last := w.graph.Last(t, chidi); last.Template != "taskiem_update" || !strings.Contains(last.Text, "[Ada Stores] Update on your shift: Your shift starts") {
		t.Fatalf("the template: %+v", last)
	}
	// Before Meta approves the template, the message goes by SMS.
	w.graph.SetFailTo(chidi, 132001)
	before := w.texts(t)
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": chidi}})
	w.env.Drain(t)
	w.tick(t)
	if w.texts(t) != before+1 {
		t.Fatalf("no SMS when the template is refused: %d", w.texts(t)-before)
	}
	w.graph.SetFailTo(chidi, 0)

	// STOP stops the business; START brings it back; anything else gets
	// who sends updates there.
	if sent := w.whatsapp(t, chidi, "STOP"); len(sent) != 1 || !strings.Contains(sent[0].Text, "no longer get updates from Ada Stores") {
		t.Fatalf("STOP: %+v", sent)
	}
	before = w.texts(t)
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": chidi}})
	w.env.Drain(t)
	if w.texts(t) != before+1 {
		t.Fatal("a stopped contact was not texted instead")
	}
	if sent := w.whatsapp(t, chidi, "start"); len(sent) != 1 || !strings.Contains(sent[0].Text, "from Ada Stores here again") {
		t.Fatalf("START: %+v", sent)
	}
	if sent := w.whatsapp(t, chidi, "hello?"); len(sent) != 1 || !strings.Contains(sent[0].Text, "updates from Ada Stores. Send STOP") {
		t.Fatalf("anything else: %+v", sent)
	}

	// Bola blocks the bot: the next message goes by SMS, and no more to
	// Telegram.
	w.bot.Block(bola, true)
	before = w.texts(t)
	owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": "08031234567"}})
	w.env.Drain(t)
	w.tick(t)
	if w.texts(t) != before+1 {
		t.Fatal("blocked: not texted")
	}
	var stopped int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM reach_channels WHERE channel = 'telegram' AND stopped_at IS NOT NULL`).Scan(&stopped); err != nil || stopped != 1 {
		t.Fatalf("stopped: %d %v", stopped, err)
	}

	// The owner sees who they reach, masked, and can remove someone.
	list := owner.must(200, "GET", "/v1/reach", nil)["contacts"].([]any)
	if len(list) != 2 || strings.Contains(toJSON(list), "8031234567") || !strings.Contains(toJSON(list), "+234•••••567") {
		t.Fatalf("contacts: %s", toJSON(list))
	}
	id := list[0].(map[string]any)["id"].(string)
	owner.must(204, "DELETE", "/v1/reach/contacts/"+id, nil)
	if n := len(owner.must(200, "GET", "/v1/reach", nil)["contacts"].([]any)); n != 1 {
		t.Fatalf("after removing: %d", n)
	}
	// A new standing link retires the old one.
	fresh := owner.must(200, "POST", "/v1/reach/page", nil)["page_url"].(string)
	if fresh == pageURL || w.page(t, path, nil).Code != 404 {
		t.Fatalf("new link: %s", fresh)
	}
	// Other businesses never see Ada's contacts.
	other := w.tenant(t, "Other", "other@other.test")
	if n := len(other.must(200, "GET", "/v1/reach", nil)["contacts"].([]any)); n != 0 {
		t.Fatalf("another business sees %d contacts", n)
	}
}

// A form asking for a chat id is flagged before publishing: nobody knows
// theirs (N11). It does not stop the publish.
func TestReadinessFlagsChatIDFields(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	doc := `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave",
	 "trigger":{"type":"form","config":{"title":"Leave","access":"public"}},
	 "inputs":{"schema":{"type":"object","properties":{"name":{"type":"string"},"telegram_chat_id":{"type":"string","title":"Telegram Chat ID"}}}},
	 "steps":[{"id":"n","type":"transform","config":{"output":{"x":"=trigger.body.name"}}}]}`
	r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})
	needs := r["needs"].([]any)
	if len(needs) != 1 {
		t.Fatalf("needs: %v", needs)
	}
	n := needs[0].(map[string]any)
	if n["kind"] != "field" || n["name"] != "telegram_chat_id" || n["problem"] != "chat_id" || n["blocking"] != false ||
		!strings.Contains(n["message"].(string), "Telegram Chat ID, which people don't know") {
		t.Fatalf("the need: %v", n)
	}
	// A phone field is fine.
	ok := strings.Replace(doc, `"telegram_chat_id":{"type":"string","title":"Telegram Chat ID"}`, `"phone":{"type":"string","x-pii":"phone"}`, 1)
	if r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(ok)}); len(r["needs"].([]any)) != 0 {
		t.Fatalf("a phone field: %v", r["needs"])
	}
}

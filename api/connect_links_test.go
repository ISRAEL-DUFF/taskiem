package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

var connectLinkRe = regexp.MustCompile(`https://taskiem\.test/c/([A-Za-z0-9_-]{32})`)

// connectPage calls the connect page's API as a browser with no session.
func connectPage(t *testing.T, w *world, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return (&client{t: t, base: w.base}).do(method, path, body)
}

// keyKept reports whether a value is anywhere Taskiem keeps the chat: the
// sessions, the audit log, the builds and the sign-ups.
func keyKept(t *testing.T, w *world, value string) bool {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM chat_sessions WHERE data::text LIKE '%' || $1 || '%') +
		(SELECT count(*) FROM audit_log WHERE detail::text LIKE '%' || $1 || '%' OR target LIKE '%' || $1 || '%') +
		(SELECT count(*) FROM ai_builds WHERE goal LIKE '%' || $1 || '%') +
		(SELECT count(*) FROM chat_signups WHERE data::text LIKE '%' || $1 || '%')`, value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// Paystack connected from WhatsApp through a connect link: a public key
// and a key the service refuses come back, and the link works again; the
// right key connects it as the person, in prod, once; the chat is told. A
// key sent as a message is never kept; nor, while a link waits, is any
// token-shaped message.
func TestConnectFromWhatsApp(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	ps := fakePaystack(t, "sk_test_good0000000000")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	const ada = "+2348031239001"
	w.bind(t, owner, ada)

	// A key typed into the chat: dropped, and the person told.
	if got := w.say(t, ada, "here it is sk_live_abcdef1234567890"); !strings.Contains(got, "did not keep or use it") {
		t.Fatalf("a key in chat: %q", got)
	}
	if keyKept(t, w.world, "sk_live_abcdef1234567890") {
		t.Fatal("the key was kept")
	}
	if got := w.say(t, ada, "connect nosuchthing"); !strings.Contains(got, "no service called") {
		t.Fatalf("unknown: %q", got)
	}
	got := w.say(t, ada, "connect Paystack")
	m := connectLinkRe.FindStringSubmatch(got)
	if m == nil || !strings.Contains(got, "15 minutes") {
		t.Fatalf("link: %q", got)
	}
	tok := m[1]
	// While the link waits, a bare token-shaped message is a key too.
	if got := w.say(t, ada, "abcdefghijklmnopqrstuvwxyz0123"); !strings.Contains(got, "did not keep or use it") {
		t.Fatalf("a token while connecting: %q", got)
	}

	st, view := connectPage(t, w.world, "GET", "/v1/connect/"+tok, nil)
	if st != 200 || view["business"] != "Ada Foods" || view["connector"].(map[string]any)["id"] != "paystack" || view["back"] != "https://wa.me/15550001111" {
		t.Fatalf("view: %d %v", st, view)
	}
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+tok, map[string]any{"credentials": map[string]string{"secret_key": "pk_test_public000000"}}); st != 400 || !strings.Contains(fmt.Sprint(out["error"]), "public key") {
		t.Fatalf("a public key: %d %v", st, out)
	}
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+tok, map[string]any{"credentials": map[string]string{"secret_key": "sk_test_wrong000000000"}}); st != 400 && st != 502 {
		t.Fatalf("a key the service refuses: %d %v", st, out)
	}
	before := len(w.graph.Sent(ada))
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+tok, map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}}); st != 201 || out["connected"] != "Paystack" || out["name"] != "main" {
		t.Fatalf("connected: %d %v", st, out)
	}
	if st, _ := connectPage(t, w.world, "POST", "/v1/connect/"+tok, map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}}); st != 410 {
		t.Fatalf("used twice: %d", st)
	}
	if st, _ := connectPage(t, w.world, "GET", "/v1/connect/"+tok, nil); st != 410 {
		t.Fatalf("read after use: %d", st)
	}
	w.srv.WaitBackground()
	told := w.graph.Sent(ada)[before:]
	if len(told) != 1 || !strings.Contains(told[0].Text, "Paystack is connected to *Ada Foods*") {
		t.Fatalf("told: %+v", told)
	}
	conns := owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["connector"] != "paystack" || conns[0].(map[string]any)["environment"] != "prod" {
		t.Fatalf("connections: %v", conns)
	}
	var via string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT detail->>'via' FROM audit_log WHERE action = 'connection.from_chat'`).Scan(&via); err != nil || via != "connect_link" {
		t.Fatalf("audit: %q %v", via, err)
	}

	// A link past its 15 minutes, and one for a member who no longer
	// manages connections, make nothing.
	got = w.say(t, ada, "connect paystack")
	tok2 := connectLinkRe.FindStringSubmatch(got)[1]
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE connect_links SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if st, _ := connectPage(t, w.world, "POST", "/v1/connect/"+tok2, map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}}); st != 410 {
		t.Fatalf("expired: %d", st)
	}
	member := w.member(t, owner, "bisi@foods.test", "operator")
	const bisi = "+2348031239002"
	w.bind(t, member, bisi)
	if got := w.say(t, bisi, "connect paystack"); !strings.Contains(got, "Only someone who manages connections") {
		t.Fatalf("a member: %q", got)
	}
	if st, _ := connectPage(t, w.world, "GET", "/v1/connect/"+strings.Repeat("A", 32), nil); st != 410 {
		t.Fatalf("an unknown link: %d", st)
	}
}

// The WhatsApp key form: a value refused by its start comes back in the
// form; the right one connects, and the chat is told.
func TestConnectFormOnWhatsApp(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	ps := fakePaystack(t, "sk_test_good0000000000")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	const ada = "+2348031239011"
	w.bind(t, owner, ada)
	form := w.send(t, ada, whatsapptest.Text("connect paystack"))
	if len(form) != 1 || form[0].Flow != whatsapp.FlowConnect || form[0].FlowScreen != whatsapp.ScreenConnect || form[0].FlowData["p1_on"] != true {
		t.Fatalf("form: %+v", form)
	}
	const ep = "/channels/whatsapp/flows"
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", whatsapp.ScreenConnect, form[0].FlowToken, map[string]any{"p1": "pk_test_public000000"})
	if st != 200 || !strings.Contains(fmt.Sprint(screenData(t, resp)["p1_err"]), "public key") {
		t.Fatalf("a public key: %d %v", st, resp)
	}
	before := len(w.graph.Sent(ada))
	if st, resp := w.flowCall(t, ep, waSecret, "data_exchange", whatsapp.ScreenConnect, form[0].FlowToken, map[string]any{"p1": "sk_test_good0000000000"}); st != 200 || flowToken(t, resp) != form[0].FlowToken {
		t.Fatalf("connected: %d %v", st, resp)
	}
	w.srv.WaitBackground()
	if told := w.graph.Sent(ada)[before:]; len(told) != 1 || !strings.Contains(told[0].Text, "Paystack is connected") {
		t.Fatalf("told: %+v", told)
	}
	if got := w.send(t, ada, whatsapptest.FlowCompleted(form[0].FlowToken)); len(got) != 0 {
		t.Fatalf("completion says nothing more: %+v", got)
	}
	if conns := owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any); len(conns) != 1 {
		t.Fatalf("connections: %v", conns)
	}
}

// On Telegram the connect page opens as a Mini App: a key is taken only
// with Telegram's signed initData for the account the link was sent to.
func TestConnectFromTelegram(t *testing.T) {
	w := newTGWorld(t)
	w.srv.PublicURL = waPublicURL
	ps := fakePaystack(t, "sk_test_good0000000000")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	const acct = int64(8201)
	w.link(t, owner, acct, "ada")
	got := w.say(t, acct, "connect paystack")
	var link string
	for _, row := range got.Keyboard {
		for _, b := range row {
			if b.WebApp {
				link = b.URL
			}
		}
	}
	m := connectLinkRe.FindStringSubmatch(link)
	if m == nil {
		t.Fatalf("Mini App button: %+v", got)
	}
	body := func(init string) map[string]any {
		return map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}, "init_data": init}
	}
	if st, _ := connectPage(t, w.world, "POST", "/v1/connect/"+m[1], body("")); st != 403 {
		t.Fatalf("no initData: %d", st)
	}
	if st, _ := connectPage(t, w.world, "POST", "/v1/connect/"+m[1], body(telegram.SignInitData(tgToken, 9999, time.Now()))); st != 403 {
		t.Fatalf("another account: %d", st)
	}
	before := len(w.bot.Sent(acct))
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+m[1], body(telegram.SignInitData(tgToken, acct, time.Now()))); st != 201 || out["back"] != "https://t.me/"+tgBot {
		t.Fatalf("connected: %d %v", st, out)
	}
	w.srv.WaitBackground()
	if told := w.bot.Sent(acct)[before:]; len(told) != 1 || !strings.Contains(told[0].Text, "Paystack is connected") {
		t.Fatalf("told: %+v", told)
	}
	_ = telegramtest.Message
}

// A service that signs in, connected through a connect link: the consent
// starts from the link with 0034's checks, the callback ends on the
// connect page instead of Connections, spends the link and tells the chat.
func TestConnectByOAuthFromChat(t *testing.T) {
	w := newWAWorld(t)
	f := xenibasetest.New(t)
	f.AddProject(oauthProject, "shop")
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: f.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.OAuthClients = map[string]api.OAuthClient{"xenibase": {ID: "xbapp_taskiem", Secret: "xbc_secret"}}
	f.RegisterOAuthClient(xenibasetest.OAuthClient{ID: "xbapp_taskiem", Secret: "xbc_secret", RedirectURI: waPublicURL + "/v1/connections/oauth/callback"})
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	const ada = "+2348031239021"
	w.bind(t, owner, ada)
	tok := connectLinkRe.FindStringSubmatch(w.say(t, ada, "connect xenibase"))[1]

	raw, _ := json.Marshal(map[string]any{})
	req, _ := http.NewRequest("POST", w.base+"/v1/connect/"+tok+"/oauth", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	var browser *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "taskiem_oauth" {
			browser = ck
		}
	}
	if resp.StatusCode != 200 || browser == nil {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	consent, _ := url.Parse(out["url"].(string))
	q := consent.Query()
	f.AddOAuthCode(xenibasetest.OAuthCode{Code: "code-chat", Challenge: q.Get("code_challenge"), Scopes: []string{"read", "write"}, ProjectIDs: []string{oauthProject}})
	before := len(w.graph.Sent(ada))
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cb, _ := http.NewRequest("GET", w.base+"/v1/connections/oauth/callback?"+url.Values{"code": {"code-chat"}, "state": {q.Get("state")}}.Encode(), nil)
	cb.AddCookie(&http.Cookie{Name: browser.Name, Value: browser.Value})
	back, err := hc.Do(cb)
	if err != nil {
		t.Fatal(err)
	}
	_ = back.Body.Close()
	loc, _ := url.Parse(back.Header.Get("Location"))
	if back.StatusCode != http.StatusFound || loc.Path != "/c/done" || loc.Query().Get("connected") != "1" || loc.Query().Get("back") != "https://wa.me/15550001111" {
		t.Fatalf("callback: %d %s", back.StatusCode, loc)
	}
	w.srv.WaitBackground()
	if told := w.graph.Sent(ada)[before:]; len(told) != 1 || !strings.Contains(told[0].Text, "is connected to *Ada Foods*") {
		t.Fatalf("told: %+v", told)
	}
	if st, _ := connectPage(t, w.world, "GET", "/v1/connect/"+tok, nil); st != 410 {
		t.Fatalf("the link is spent: %d", st)
	}
}

// A draft that needs a service carries on once it is connected from the
// chat: "publish" says what it waits for and how to connect it here, the
// link remembers the draft, and when it is connected the chat offers to
// publish it.
func TestConnectCarriesOn(t *testing.T) {
	w := newWAWorld(t)
	ps := fakePaystack(t, "sk_test_good0000000000")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	const ada = "+2348031239031"
	w.bind(t, owner, ada)
	def := `{"schema":"wd/v1","id":"wf_balance","version":1,"name":"Morning balance",
	 "trigger":{"type":"schedule","config":{"cron":"0 8 * * *","timezone":"Africa/Lagos"}},
	 "steps":[{"id":"bal","type":"connector","name":"Check the balance","connector":"paystack@1","action":"check_balance","input":{}}]}`
	owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Morning balance", "definition": json.RawMessage(def)})
	got := w.say(t, ada, "publish Morning balance")
	if !strings.Contains(got, "*connect paystack*") {
		t.Fatalf("not ready: %q", got)
	}
	tok := connectLinkRe.FindStringSubmatch(w.say(t, ada, "connect paystack"))[1]
	before := len(w.graph.Sent(ada))
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+tok, map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}}); st != 201 {
		t.Fatalf("connected: %d %v", st, out)
	}
	w.srv.WaitBackground()
	told := w.graph.Sent(ada)[before:]
	if len(told) != 1 || !strings.Contains(told[0].Text, "Carrying on with *Morning balance*") {
		t.Fatalf("carried on: %+v", told)
	}
	if got := w.say(t, ada, "yes"); !strings.Contains(strings.ToLower(got), "published") {
		t.Fatalf("published: %q", got)
	}
}

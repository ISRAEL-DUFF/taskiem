package publicweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/storefront"
)

const formHost = "forms.shops.test"

// fakeForms stands in for the taskiem_public_form* functions.
type fakeForms struct {
	mu       sync.Mutex
	forms    map[string]PublicForm
	links    map[string]FormLink    // by token
	requests map[string]FormRequest // by token
	writes   []FormWrite
	byIdem   map[string]string
	states   map[string]FormState
	hashes   map[string][]byte
	once     map[string]bool
	used     map[uuid.UUID]bool
	answered map[uuid.UUID]bool
	codes    []fakeCode
	limited  bool
	closed   bool
	taken    int
}

type fakeCode struct {
	scope         uuid.UUID
	address, code []byte
	payload       []byte
	verified      bool
}

func newFakeForms() *fakeForms {
	return &fakeForms{forms: map[string]PublicForm{}, links: map[string]FormLink{}, requests: map[string]FormRequest{}, byIdem: map[string]string{},
		states: map[string]FormState{}, hashes: map[string][]byte{}, once: map[string]bool{}, used: map[uuid.UUID]bool{}, answered: map[uuid.UUID]bool{}}
}

func (f *fakeForms) Form(_ context.Context, id string) (PublicForm, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.forms[id]
	return p, ok, nil
}

func (f *fakeForms) Link(_ context.Context, hash []byte) (FormLink, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for tok, l := range f.links {
		if bytes.Equal(forms.TokenHash(tok), hash) {
			l.Usable = l.Usable && !f.used[l.ID]
			return l, true, nil
		}
	}
	return FormLink{}, false, nil
}

func (f *fakeForms) Request(_ context.Context, hash []byte) (FormRequest, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for tok, r := range f.requests {
		if bytes.Equal(forms.TokenHash(tok), hash) {
			return r, true, nil
		}
	}
	return FormRequest{}, false, nil
}

func (f *fakeForms) Submit(_ context.Context, w FormWrite) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byIdem[hex.EncodeToString(w.Idem)]; ok {
		return true, nil
	}
	if f.closed {
		return false, nil
	}
	if f.taken > 0 {
		f.taken--
		return false, ErrReferenceTaken
	}
	if w.Link != nil {
		if f.used[*w.Link] {
			return false, ErrLinkUsed
		}
		f.used[*w.Link] = true
	}
	if w.Request != nil {
		if f.answered[*w.Request] {
			return false, ErrAnswered
		}
		f.answered[*w.Request] = true
	}
	if w.Once != nil {
		k := w.Endpoint.String() + hex.EncodeToString(w.Once)
		if f.once[k] {
			return false, ErrOnceSent
		}
		f.once[k] = true
	}
	f.byIdem[hex.EncodeToString(w.Idem)] = w.Reference
	f.writes = append(f.writes, w)
	f.states[w.Reference] = FormState{Status: "received", Created: time.Now()}
	f.hashes[w.Reference] = w.StatusHash
	return true, nil
}

func (f *fakeForms) Code(_ context.Context, scope uuid.UUID, address, code, payload []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.limited {
		return false, nil
	}
	f.codes = append(f.codes, fakeCode{scope: scope, address: address, code: code, payload: payload})
	return true, nil
}

func (f *fakeForms) CheckCode(_ context.Context, scope uuid.UUID, address, code []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.codes) - 1; i >= 0; i-- {
		c := &f.codes[i]
		if c.scope == scope && bytes.Equal(c.address, address) {
			if c.verified || !bytes.Equal(c.code, code) {
				return false, nil
			}
			c.verified = true
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeForms) Status(_ context.Context, ref string, hash []byte) (FormState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[ref]
	if !ok || !bytes.Equal(f.hashes[ref], hash) {
		return FormState{}, false, nil
	}
	return st, true, nil
}

func (f *fakeForms) last(t *testing.T) FormWrite {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		t.Fatal("nothing was written")
	}
	return f.writes[len(f.writes)-1]
}

type hostEnv struct {
	t    *testing.T
	s    *Server
	h    http.Handler
	f    *fakeForms
	keys forms.Keys
	now  *time.Time
}

func newFormHost(t *testing.T) *hostEnv {
	t.Helper()
	keys, err := forms.NewKeys(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeForms()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, storefront.Lagos)
	e := &hostEnv{t: t, f: f, keys: keys, now: &now}
	e.s = &Server{Lookup: (&fakeHosts{}).lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		FormHost: formHost, Forms: f, FormKeys: &keys, AppURL: "https://app.taskiem.test",
		Origins:         func(context.Context, uuid.UUID) ([]string, error) { return []string{"https://ada.example"}, nil },
		FormSubmissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	e.s.now = func() time.Time { return *e.now }
	e.h = e.s.Handler()
	return e
}

func (e *hostEnv) tick(d time.Duration) { *e.now = e.now.Add(d) }

// add registers a form from taskiem_public_form's document.
func (e *hostEnv) add(id, trigger, schema string) PublicForm {
	e.t.Helper()
	doc := fmt.Sprintf(`{"endpoint_id":%q,"tenant_id":%q,"environment":"prod","version":1,"status":"live","open":true,"business":"Ada <Loans>",
		"trigger":{"type":"form","config":%s},"inputs":{"schema":%s}}`, uuid.NewString(), uuid.NewString(), trigger, schema)
	p, err := ParsePublicForm(id, []byte(doc))
	if err != nil {
		e.t.Fatal(err)
	}
	e.f.forms[id] = p
	return p
}

func (e *hostEnv) get(path string) *httptest.ResponseRecorder {
	return get(e.h, "GET", formHost, path)
}

var (
	stateRe = regexp.MustCompile(`name="state" value="([A-Za-z0-9_-]*)"`)
	refRe   = regexp.MustCompile(`Reference <strong>(F-[A-Z0-9]{8})</strong>`)
)

type upload struct {
	field, name string
	data        []byte
}

// next posts the page in body with v, carrying its token and state.
func (e *hostEnv) next(path, body string, v url.Values, files ...upload) *httptest.ResponseRecorder {
	e.t.Helper()
	tm, sm := tokenRe.FindStringSubmatch(body), stateRe.FindStringSubmatch(body)
	if tm == nil || sm == nil {
		e.t.Fatalf("no token or state in %s", body)
	}
	all := url.Values{"token": {tm[1]}, "state": {sm[1]}, "website": {""}}
	for k, x := range v {
		all[k] = x
	}
	return e.post(path, all, files...)
}

func (e *hostEnv) post(path string, v url.Values, files ...upload) *httptest.ResponseRecorder {
	var r *http.Request
	if len(files) > 0 {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, xs := range v {
			for _, x := range xs {
				_ = mw.WriteField(k, x)
			}
		}
		for _, f := range files {
			fw, _ := mw.CreateFormFile(f.field, f.name)
			_, _ = fw.Write(f.data)
		}
		_ = mw.Close()
		r = httptest.NewRequest("POST", path, &buf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
	} else {
		r = httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Host = formHost
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *hostEnv) submission(t *testing.T, w FormWrite) forms.Submission {
	t.Helper()
	var sub forms.Submission
	if err := e.keys.Open(forms.SubmissionAAD(w.Reference), w.Payload, &sub); err != nil {
		t.Fatal(err)
	}
	return sub
}

func must(t *testing.T, w *httptest.ResponseRecorder, code int, wants ...string) string {
	t.Helper()
	body := w.Body.String()
	if w.Code != code {
		t.Fatalf("status %d, want %d: %s", w.Code, code, body)
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("no %q in %s", want, body)
		}
	}
	return body
}

const loanTrigger = `{"title":"Apply for a loan","description":"Takes about 5 minutes.","access":"public",
 "pages":[{"title":"About you","fields":["full_name","phone","email","plan"]},
  {"title":"The loan","fields":["amount_naira","purpose","employed","agree"]},
  {"title":"Your employer","fields":["employer","payslip"],"show_if":"answers.employed == true"}],
 "after":{"mode":"message","message":"Thank you. We will **reply** within a day. [Our site](https://ada.example/loans) or [elsewhere](https://evil.example/x).","status":true}}`

const loanSchema = `{"type":"object","required":["full_name","phone","amount_naira","purpose","employer","payslip"],"properties":{
 "full_name":{"type":"string","title":"Full name","maxLength":100},
 "phone":{"type":"string","format":"phone-ng","x-pii":"phone"},
 "email":{"type":"string","format":"email"},
 "plan":{"type":"string","x-form":{"prefill":true}},
 "campaign":{"type":"string","x-form":{"hidden":true,"prefill":true}},
 "amount_naira":{"type":"integer","minimum":10000,"x-form":{"unit":"naira","help":"In naira"}},
 "purpose":{"type":"string","enum":["school","rent","business"]},
 "employed":{"type":"boolean","title":"I am employed"},
 "agree":{"type":"boolean","x-form":{"widget":"consent","text":"I agree to a credit check"}},
 "employer":{"type":"string"},
 "payslip":{"type":"string","format":"file","x-form":{"accept":["application/pdf","image/*"],"max_mb":1}}}}`

var pdf = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 100)...)

func pageOne() url.Values {
	return url.Values{"do": {"next"}, "a.full_name": {"Bola Ade"}, "a.phone": {"0803 123 4567"}, "a.email": {"Bola@Example.com"}, "a.plan": {"gold"}}
}

func pageTwo(employed bool) url.Values {
	v := url.Values{"do": {"next"}, "a.amount_naira": {"250,000"}, "a.purpose": {"rent"}, "a.agree": {"true"}}
	if employed {
		v.Set("a.employed", "true")
	}
	return v
}

func TestHostedFormPage(t *testing.T) {
	e := newFormHost(t)
	e.add("loan00000001", loanTrigger, loanSchema)
	w := e.get("/f/loan00000001")
	body := must(t, w, 200, "<h1>Apply for a loan</h1>", "From Ada &lt;Loans&gt;", "Takes about 5 minutes.", "<h2>About you</h2>",
		`>Full name <span class="req"`, `maxlength="100"`, `type="tel"`, `placeholder="0803 123 4567"`, `type="email"`,
		"Plan <span class=\"hint\">(optional)</span>", "Your answers go to Ada &lt;Loans&gt;, for: Takes about 5 minutes.",
		"Never enter a password or card PIN", `action="/report"`, "Report this form", `name="token"`, `name="state"`, `value="next">Next`,
		`enctype="multipart/form-data"`)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("headers: %v", w.Header())
	}
	for _, re := range policyBreaks {
		if re.MatchString(body) {
			t.Errorf("%s in the page", re)
		}
	}
	for _, not := range []string{`name="a.campaign"`, `name="a.amount_naira"`, "Back</button>"} {
		if strings.Contains(body, not) {
			t.Errorf("page one shows %q", not)
		}
	}
	// Other hosts are untouched; unknown form paths are not found.
	must(t, get(e.h, "GET", "nobody.shops.test", "/f/loan00000001"), 404)
	must(t, e.get("/f/nosuchform00"), 404)
	must(t, e.get("/f/BAD"), 404)
	must(t, e.get("/"), 404)
	if w := get(e.h, "DELETE", formHost, "/f/loan00000001"); w.Code != 405 {
		t.Fatalf("DELETE: %d", w.Code)
	}
	must(t, e.post("/report", url.Values{"form": {"f:loan00000001"}, "reason": {"scam"}}), 204)
}

// widgetSchema has one field of each kind.
const widgetSchema = `{"type":"object","required":["name","colour"],"properties":{
 "name":{"type":"string","x-form":{"label":"Your name","placeholder":"Ada","help":"As on your ID"}},
 "story":{"type":"string","x-form":{"widget":"textarea"},"maxLength":500},
 "age":{"type":"integer","minimum":18,"maximum":120},
 "price_kobo":{"type":"integer","x-form":{"unit":"naira"}},
 "email":{"type":"string","format":"email"},
 "phone":{"type":"string","format":"phone-ng"},
 "site":{"type":"string","format":"uri"},
 "day":{"type":"string","format":"date"},
 "at":{"type":"string","format":"time"},
 "when":{"type":"string","format":"date-time"},
 "colour":{"type":"string","enum":["red","green","blue"]},
 "state":{"type":"string","enum":["Lagos","Oyo","Kano","Rivers","Enugu","Kaduna","Abuja"]},
 "langs":{"type":"array","items":{"type":"string","enum":["en","yo","ha"]}},
 "news":{"type":"boolean","x-form":{"label":"Send me news"}},
 "ok":{"type":"boolean","x-form":{"widget":"consent","text":"I agree to the terms"}},
 "id_card":{"type":"string","format":"file","x-form":{"accept":["image/*"]}},
 "secret_ref":{"type":"string","x-form":{"hidden":true}}}}`

func TestHostedFormWidgets(t *testing.T) {
	e := newFormHost(t)
	e.add("widgets00001", `{"title":"Everything"}`, widgetSchema)
	body := must(t, e.get("/f/widgets00001"), 200,
		`>Your name <span class="req"`, `placeholder="Ada"`, "As on your ID",
		`<textarea id=`, `maxlength="500"`,
		`type="number" min="18" max="120" step="1" name="a.age"`,
		`<span class="ff-unit">₦</span><input id=`, `type="text" inputmode="decimal" name="a.price_kobo"`,
		`type="email" name="a.email"`, `type="tel" name="a.phone"`, `type="url" name="a.site"`,
		`type="date" name="a.day"`, `type="time" name="a.at"`, `type="datetime-local" name="a.when"`,
		`type="radio" name="a.colour" value="red" required`, `<select id=`, `<option value="Kaduna">Kaduna</option>`,
		`type="checkbox" name="a.langs" value="yo"`, `name="a.news" value="true"> Send me news`,
		`name="a.ok" value="true" required> I agree to the terms`, `type="file" name="a.id_card" accept="image/*"`, `value="send">Send`)
	if strings.Contains(body, "a.secret_ref") {
		t.Error("a hidden field is shown")
	}
	if strings.Contains(body, `type="radio" name="a.state"`) {
		t.Error("seven choices are radios")
	}

	// Typed values become the schema's types.
	e.tick(10 * time.Second)
	w := e.next("/f/widgets00001", body, url.Values{"do": {"send"}, "a.name": {"Ada"}, "a.colour": {"green"}, "a.age": {"30"}, "a.price_kobo": {"1,500.50"},
		"a.day": {"2026-11-01"}, "a.at": {"09:30"}, "a.when": {"2026-11-01T09:30"}, "a.langs": {"en", "ha"}, "a.news": {"true"}, "a.ok": {"true"},
		"a.site": {"https://ada.example"}, "a.phone": {"08031234567"}}, upload{"a.id_card", "me.png", pngBytes()})
	must(t, w, 200, "Thank you")
	sub := e.submission(t, e.f.last(t))
	a := sub.Answers
	if a["age"] != float64(30) || a["price_kobo"] != float64(150050) || a["at"] != "09:30:00" || a["when"] != "2026-11-01T09:30:00+01:00" ||
		a["news"] != true || a["ok"] != true || a["phone"] != "+2348031234567" || fmt.Sprint(a["langs"]) != "[en ha]" {
		t.Fatalf("answers: %v", a)
	}
	if _, ok := a["id_card"]; ok {
		t.Fatal("a file is in the answers")
	}
	agreed := sha256.Sum256([]byte("I agree to the terms"))
	if len(sub.Files) != 1 || sub.Files[0].Type != "image/png" || sub.Files[0].Name != "me.png" || len(sub.Consents) != 1 || sub.Consents[0].Field != "ok" ||
		sub.Consents[0].TextHash != hex.EncodeToString(agreed[:]) || !sub.Consents[0].At.Equal(*e.now) {
		t.Fatalf("files and consents: %+v %+v", sub.Files, sub.Consents)
	}
	up := e.f.last(t).Uploads
	got, err := e.keys.OpenBytes(forms.UploadAAD(e.f.last(t).Reference, "id_card", 0), up[0].Data)
	if err != nil || !bytes.Equal(got, pngBytes()) {
		t.Fatalf("upload: %v", err)
	}
}

func pngBytes() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
}

func TestHostedFormPages(t *testing.T) {
	e := newFormHost(t)
	e.add("loan00000001", loanTrigger, loanSchema)
	// Prefill: only x-form.prefill fields take query parameters.
	body := must(t, e.get("/f/loan00000001?plan=gold&full_name=Mallory&campaign=radio"), 200, `name="a.plan" value="gold"`)
	if strings.Contains(body, "Mallory") {
		t.Fatal("a field without prefill took a query parameter")
	}
	body = must(t, e.next("/f/loan00000001", body, pageOne()), 200, "<h2>The loan</h2>", `value="back"`, `type="radio" name="a.purpose"`, "I agree to a credit check", "In naira")
	// Back keeps what was typed.
	back := must(t, e.next("/f/loan00000001", body, url.Values{"do": {"back"}}), 200, "<h2>About you</h2>", `value="Bola Ade"`)
	body = must(t, e.next("/f/loan00000001", back, pageOne()), 200, "<h2>The loan</h2>")

	// Not employed: the employer page is not shown, and its required field
	// is not required.
	e.tick(10 * time.Second)
	done := must(t, e.next("/f/loan00000001", body, pageTwo(false)), 200, "Thank you", "We will <strong>reply</strong> within a day.",
		`<a href="https://ada.example/loans" rel="noopener noreferrer" target="_top">Our site</a>`, "See what happens next")
	if strings.Contains(done, "evil.example") {
		t.Fatal("a link off the verified websites")
	}
	w := e.f.last(t)
	sub := e.submission(t, w)
	if sub.Answers["full_name"] != "Bola Ade" || sub.Answers["phone"] != "+2348031234567" || sub.Answers["email"] != "bola@example.com" ||
		sub.Answers["amount_naira"] != float64(250000) || sub.Answers["employed"] != false || sub.Answers["campaign"] != "radio" || sub.Answers["plan"] != "gold" {
		t.Fatalf("answers: %v", sub.Answers)
	}
	if _, ok := sub.Answers["employer"]; ok || sub.Respondent.Kind != "anonymous" || sub.Channel != "direct" || sub.StatusKey == "" {
		t.Fatalf("submission: %+v", sub)
	}
	if !bytes.Equal(w.StatusHash, storefront.StatusHash(sub.StatusKey)) || !formRefRe.MatchString(w.Reference) || w.Endpoint == nil || w.Link != nil || w.Once != nil {
		t.Fatalf("write: %+v", w)
	}

	// Employed: the page shows, and a file goes with it.
	body = must(t, e.get("/f/loan00000001"), 200)
	body = must(t, e.next("/f/loan00000001", body, pageOne()), 200)
	body = must(t, e.next("/f/loan00000001", body, pageTwo(true)), 200, "<h2>Your employer</h2>", `accept="application/pdf,image/*"`, `value="send">Send`)
	e.tick(10 * time.Second)
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"send"}}), 422, "Please fill in Employer.", "Please attach Payslip.")
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"send"}, "a.employer": {"Dangote"}}, upload{"a.payslip", "big.pdf", append(pdf, bytes.Repeat([]byte("x"), 1<<20)...)}),
		422, "Payslip is larger than 1 MB")
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"send"}, "a.employer": {"Dangote"}}, upload{"a.payslip", "fake.pdf", []byte("just some text, not a pdf at all")}),
		422, "not a type this question takes")
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"send"}, "a.employer": {"Dangote"}}, upload{"a.payslip", "slip.pdf", pdf}), 200, "Thank you")
	sub = e.submission(t, e.f.last(t))
	if sub.Answers["employer"] != "Dangote" || sub.Answers["employed"] != true || len(sub.Files) != 1 || sub.Files[0].Type != "application/pdf" {
		t.Fatalf("employed: %+v", sub)
	}
}

func TestHostedFormValidation(t *testing.T) {
	e := newFormHost(t)
	e.add("loan00000001", loanTrigger, loanSchema)
	body := must(t, e.get("/f/loan00000001"), 200)
	for _, c := range []struct {
		v    url.Values
		want string
	}{
		{url.Values{"do": {"next"}, "a.phone": {"0803 123 4567"}}, "Please fill in Full name."},
		{url.Values{"do": {"next"}, "a.full_name": {"Bola"}, "a.phone": {"+44 7700 900123"}}, "Please enter a Nigerian mobile number for Phone"},
		{url.Values{"do": {"next"}, "a.full_name": {"Bola"}, "a.phone": {"08031234567"}, "a.email": {"not an email"}}, "Please check the email address for Email."},
		{url.Values{"do": {"next"}, "a.full_name": {strings.Repeat("a", 101)}, "a.phone": {"08031234567"}}, "Please keep Full name to 100 characters."},
		{url.Values{"do": {"next"}, "a.full_name": {"Bola"}, "a.phone": {"08031234567"}, "a.plan": {"my card 4111 1111 1111 1111"}}, "Please do not put card numbers"},
	} {
		w := e.next("/f/loan00000001", body, c.v)
		must(t, w, 422, c.want, `class="ff has-error"`)
	}
	body = must(t, e.next("/f/loan00000001", body, pageOne()), 200)
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"next"}, "a.amount_naira": {"5000"}, "a.purpose": {"rent"}, "a.agree": {"true"}}), 422, "Amount must be at least ₦10000.")
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"next"}, "a.amount_naira": {"20000"}, "a.purpose": {"holiday"}, "a.agree": {"true"}}), 422, "Please choose from the list for Purpose.")
	must(t, e.next("/f/loan00000001", body, url.Values{"do": {"next"}, "a.amount_naira": {"20000"}, "a.purpose": {"rent"}}), 422, "Please tick the box to agree: I agree to a credit check")

	// A tampered state, a token for another form, the honeypot: start again.
	must(t, e.next("/f/loan00000001", body, url.Values{"state": {"AAAA"}}), 422, "could not be read")
	other := e.add("other0000001", `{"title":"Other"}`, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	ob := must(t, e.get("/f/"+other.PublicID), 200)
	tm := tokenRe.FindStringSubmatch(ob)
	must(t, e.next("/f/loan00000001", body, url.Values{"token": {tm[1]}}), 422, "could not be read")
	must(t, e.next("/f/loan00000001", body, url.Values{"website": {"http://spam"}}), 422, "could not be sent")
	if len(e.f.writes) != 0 {
		t.Fatal("a refused form was written")
	}
	// Sent too fast: the same token works in a moment.
	b := must(t, e.get("/f/other0000001"), 200)
	b = must(t, e.next("/f/other0000001", b, url.Values{"do": {"send"}, "a.x": {"y"}}), 422, "Please check your answers and send again.")
	e.tick(5 * time.Second)
	must(t, e.next("/f/other0000001", b, url.Values{"do": {"send"}, "a.x": {"y"}}), 200, "Thank you")
	// Too large.
	r := httptest.NewRequest("POST", "/f/other0000001", strings.NewReader("x="+strings.Repeat("a", 400<<10)))
	r.Host = formHost
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	must(t, rec, 400, "too large")
}

func TestHostedFormIdempotent(t *testing.T) {
	e := newFormHost(t)
	e.add("other0000001", `{"title":"Other"}`, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	b := must(t, e.get("/f/other0000001"), 200)
	e.tick(5 * time.Second)
	e.f.taken = 1 // the first reference drawn is taken
	first := refRe.FindStringSubmatch(must(t, e.next("/f/other0000001", b, url.Values{"do": {"send"}, "a.x": {"y"}}), 200))
	again := refRe.FindStringSubmatch(must(t, e.next("/f/other0000001", b, url.Values{"do": {"send"}, "a.x": {"y"}}), 200))
	if first == nil || again == nil || first[1] != again[1] || len(e.f.writes) != 1 {
		t.Fatalf("a double tap: %v %v %d", first, again, len(e.f.writes))
	}
	e.f.closed = true
	b = must(t, e.get("/f/other0000001"), 200)
	e.tick(5 * time.Second)
	must(t, e.next("/f/other0000001", b, url.Values{"do": {"send"}, "a.x": {"y"}}), 409, "not taking answers")
	e.f.forms["closed000001"] = PublicForm{PublicID: "closed000001", Business: "Ada", Trigger: e.f.forms["other0000001"].Trigger, ClosedMessage: "Back in May."}
	must(t, e.get("/f/closed000001"), 200, "not taking answers right now. Back in May.")
}

// codeOf opens the code the host asked to send.
func (e *hostEnv) codeOf(t *testing.T) (forms.CodeRequest, fakeCode) {
	t.Helper()
	c := e.f.codes[len(e.f.codes)-1]
	var req forms.CodeRequest
	if err := e.keys.Open(forms.CodeAAD(c.scope.String()), c.payload, &req); err != nil {
		t.Fatal(err)
	}
	return req, c
}

func TestHostedFormVerify(t *testing.T) {
	e := newFormHost(t)
	pub := e.add("verify000001", `{"title":"Register","verify":"phone","once_per":"phone"}`, `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`)
	body := must(t, e.get("/f/verify000001"), 200, "Your phone number", `formaction="/f/verify000001/code"`, "Send code")
	if strings.Contains(body, `name="a.name"`) {
		t.Fatal("the questions before the code")
	}
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"12345"}}), 422, "Please enter a Nigerian mobile number")
	e.f.limited = true
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4567"}}), 429, "Too many codes")
	e.f.limited = false
	// Skipping the code does not reach the questions.
	must(t, e.next("/f/verify000001", body, url.Values{"do": {"send"}, "a.name": {"x"}}), 422, "Please confirm your phone number first.")
	body = must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4567"}}), 200, "We sent a code to •••4567", `name="code"`)
	req, c := e.codeOf(t)
	if req.Channel != "sms" || req.Address != "+2348031234567" || len(req.Code) != 6 || c.scope != pub.Endpoint ||
		!bytes.Equal(c.address, e.keys.AddressHash(pub.Endpoint.String(), "+2348031234567")) || !bytes.Equal(c.code, e.keys.CodeHash(pub.Endpoint.String(), req.Code)) {
		t.Fatalf("code: %+v %+v", req, c)
	}
	wrong := "000000"
	if req.Code == wrong {
		wrong = "111111"
	}
	must(t, e.next("/f/verify000001", body, url.Values{"do": {"verify"}, "code": {wrong}}), 422, "That code is not right")
	body = must(t, e.next("/f/verify000001", body, url.Values{"do": {"verify"}, "code": {req.Code}}), 200, "that is confirmed", `name="a.name"`)
	e.tick(10 * time.Second)
	must(t, e.next("/f/verify000001", body, url.Values{"do": {"send"}, "a.name": {"Bola"}}), 200, "Thank you")
	w := e.f.last(t)
	sub := e.submission(t, w)
	if sub.Respondent.Kind != "verified" || sub.Respondent.Phone != "+2348031234567" ||
		!bytes.Equal(w.Once, e.keys.AddressHash(pub.Endpoint.String(), "once:+2348031234567")) {
		t.Fatalf("verified: %+v %x", sub.Respondent, w.Once)
	}

	// once_per: a second time from the same phone.
	body = must(t, e.get("/f/verify000001"), 200)
	body = must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"08031234567"}}), 200)
	req, _ = e.codeOf(t)
	body = must(t, e.next("/f/verify000001", body, url.Values{"do": {"verify"}, "code": {req.Code}}), 200)
	e.tick(10 * time.Second)
	must(t, e.next("/f/verify000001", body, url.Values{"do": {"send"}, "a.name": {"Bola"}}), 409, "You have already sent this form")
}

func TestHostedFormAccess(t *testing.T) {
	e := newFormHost(t)
	pub := e.add("link00000001", `{"title":"Survey","access":"link"}`, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	e.add("members00001", `{"title":"Leave request","access":"members"}`, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	must(t, e.get("/f/link00000001"), 403, "This form opens from the link you were sent")
	must(t, e.get("/f/members00001"), 200, "It opens in the Taskiem app", `href="https://app.taskiem.test/forms/members00001"`)
	if w := e.get("/embed/f/members00001"); !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("a members form may be framed")
	}

	token := forms.NewLinkToken()
	link := FormLink{ID: uuid.New(), PublicID: pub.PublicID, Usable: true}
	e.f.links[token] = link
	must(t, e.get("/r/"+forms.NewLinkToken()), 404, "This link is not valid")
	body := must(t, e.get("/r/"+token), 200, "<h1>Survey</h1>", `action="/r/`+token+`"`)
	e.tick(10 * time.Second)
	must(t, e.next("/r/"+token, body, url.Values{"do": {"send"}, "a.x": {"y"}}), 200, "Thank you")
	w := e.f.last(t)
	if w.Link == nil || *w.Link != link.ID || *w.Endpoint != pub.Endpoint || e.submission(t, w).Respondent.Kind != "link" {
		t.Fatalf("link write: %+v", w)
	}
	must(t, e.get("/r/"+token), 410, "used already")

	// A link bound to a person asks for the code sent there.
	bound := forms.NewLinkToken()
	bl := FormLink{ID: uuid.New(), PublicID: pub.PublicID, Usable: true}
	bl.BoundView, _ = e.keys.Seal(forms.LinkViewAAD(bl.ID.String()), forms.LinkView{Channel: "email", Address: "bola@example.com", Hint: "b•••@example.com"})
	e.f.links[bound] = bl
	body = must(t, e.get("/r/"+bound), 200, "we will send a 6-digit code to b•••@example.com")
	if strings.Contains(body, `name="address"`) {
		t.Fatal("a bound link asks for the address")
	}
	body = must(t, e.next("/r/"+bound+"/code", body, nil), 200, "We sent a code to b•••@example.com")
	req, c := e.codeOf(t)
	if c.scope != bl.ID || req.Address != "bola@example.com" || !bytes.Equal(c.address, e.keys.AddressHash(bl.ID.String(), "bola@example.com")) {
		t.Fatalf("bound code: %+v", req)
	}
	body = must(t, e.next("/r/"+bound, body, url.Values{"do": {"verify"}, "code": {req.Code}}), 200, `name="a.x"`)
	e.tick(10 * time.Second)
	must(t, e.next("/r/"+bound, body, url.Values{"do": {"send"}, "a.x": {"y"}}), 200, "Thank you")
	if r := e.submission(t, e.f.last(t)).Respondent; r.Kind != "link" || r.Email != "bola@example.com" {
		t.Fatalf("bound respondent: %+v", r)
	}
}

func TestHostedFormAfter(t *testing.T) {
	e := newFormHost(t)
	schema := `{"type":"object","properties":{"x":{"type":"string"}}}`
	send := func(id string) *httptest.ResponseRecorder {
		b := must(t, e.get("/f/"+id+"?next=https://evil.example"), 200)
		e.tick(5 * time.Second)
		return e.next("/f/"+id, b, url.Values{"do": {"send"}, "a.x": {"y"}, "url": {"https://evil.example"}})
	}
	e.add("redirect0001", `{"title":"R","after":{"mode":"redirect","url":"https://ada.example/thanks?from=form"}}`, schema)
	if w := send("redirect0001"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://ada.example/thanks?from=form" {
		t.Fatalf("redirect: %d %v", w.Code, w.Header())
	}
	// Hostile values never redirect off the verified websites.
	for i, u := range []string{"https://evil.example/", "http://ada.example/", "https://ada.example@evil.example/", "//evil.example/", "https://ada.example.evil.example/",
		"javascript:alert(1)", "https://evil.example\\@ada.example/"} {
		id := fmt.Sprintf("hostile%05d", i)
		raw, _ := json.Marshal(u)
		e.add(id, `{"title":"H","after":{"mode":"redirect","url":`+string(raw)+`}}`, schema)
		w := send(id)
		if w.Code != 200 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "Thank you") {
			t.Errorf("%s: %d %v", u, w.Code, w.Header())
		}
	}
	if _, ok := safeRedirect("https://ADA.example/x", []string{"https://ada.example"}); !ok {
		t.Error("an origin's case")
	}

	// Result: the page waits for the workflow's message.
	e.add("result000001", `{"title":"Q","after":{"mode":"result","message":"We will text you."}}`, schema)
	body := must(t, send("result000001"), 200, "Just a moment", `<meta http-equiv="refresh" content="2; url=/f/result000001/done/F-`)
	ref := refRe.FindStringSubmatch(body)[1]
	key := e.submission(t, e.f.last(t)).StatusKey
	done := "/f/result000001/done/" + ref + "?k=" + url.QueryEscape(key)
	e.f.states[ref] = FormState{Status: "started", Created: *e.now}
	must(t, e.get(done), 200, `<meta http-equiv="refresh" content="2">`, "Just a moment")
	e.f.states[ref] = FormState{Status: "started", Created: *e.now, Message: "Approved: ₦50,000 <today>"}
	b := must(t, e.get(done), 200, "Approved: ₦50,000 &lt;today&gt;")
	if strings.Contains(b, "refresh") {
		t.Fatal("still refreshing with a message")
	}
	e.f.states[ref] = FormState{Status: "started", Created: e.now.Add(-31 * time.Second)}
	b = must(t, e.get(done), 200, "We will text you.", `href="/s/`+ref+`?k=`)
	if strings.Contains(b, "refresh") {
		t.Fatal("waited more than 30 seconds")
	}
	must(t, e.get("/f/result000001/done/"+ref+"?k=wrong"), 404)

	// The status link: plain words and the message, nothing else.
	must(t, e.get("/s/"+ref+"?k="+url.QueryEscape(key)), 200, "being handled", "Reference <strong>"+ref)
	e.f.states[ref] = FormState{Status: "started", Created: *e.now, Message: "With our credit team"}
	must(t, e.get("/s/"+ref+"?k="+url.QueryEscape(key)), 200, "With our credit team")
	must(t, e.get("/s/"+ref+"?k=nope"), 404)
	must(t, e.get("/s/nonsense?k=x"), 404)
}

func TestHostedFormEmbed(t *testing.T) {
	e := newFormHost(t)
	e.add("embed0000001", `{"title":"E","after":{"mode":"redirect","url":"https://ada.example/thanks"}}`, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	w := e.get("/embed/f/embed0000001")
	body := must(t, w, 200, `<script src="/_static/form-embed.`, `data-origins="https://ada.example"`, `action="/embed/f/embed0000001"`, `data-site`)
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors https://ada.example") || w.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("embed headers: %v", w.Header())
	}
	if strings.Contains(get(e.h, "GET", formHost, "/f/embed0000001").Header().Get("Content-Security-Policy"), "ada.example") {
		t.Fatal("the plain form may be framed")
	}
	e.tick(5 * time.Second)
	// Framed, a redirect would move the frame: the page says thank you
	// and tells the website the reference.
	w = e.next("/embed/f/embed0000001", body, url.Values{"do": {"send"}, "a.x": {"y"}, "site": {"https://ada.example"}})
	must(t, w, 200, "Thank you", `data-submitted="F-`)
	sub := e.submission(t, e.f.last(t))
	if sub.Channel != "embed" || sub.Site != "https://ada.example" {
		t.Fatalf("embed submission: %+v", sub)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors https://ada.example") {
		t.Fatal("the thank-you page cannot be framed")
	}
	// A site that is not verified is not recorded.
	b := must(t, e.get("/embed/f/embed0000001"), 200)
	e.tick(5 * time.Second)
	e.next("/embed/f/embed0000001", b, url.Values{"do": {"send"}, "a.x": {"z"}, "site": {"https://evil.example"}})
	if e.submission(t, e.f.last(t)).Site != "" {
		t.Fatal("an unverified site was recorded")
	}
}

func TestHostedFormRequest(t *testing.T) {
	e := newFormHost(t)
	id := uuid.New()
	view, _ := e.keys.Seal(forms.RequestViewAAD(id.String()), forms.RequestView{Prefill: map[string]any{"name": "Bola Ade", "loan": "L-1"}, Channel: "sms", Hint: "•••4567"})
	schema := map[string]any{"type": "object", "required": []any{"bank"}, "properties": map[string]any{
		"name": map[string]any{"type": "string"}, "bank": map[string]any{"type": "string"},
		"loan": map[string]any{"type": "string", "x-form": map[string]any{"hidden": true}}}}
	req := FormRequest{ID: id, Title: "Confirm your bank", Business: "Ada Loans", Schema: schema, Verify: true, Status: "pending", PublicView: view}
	token := e.keys.RequestToken(id.String())
	e.f.requests[token] = req

	body := must(t, e.get("/q/"+token), 200, "we will send a 6-digit code to •••4567")
	body = must(t, e.next("/q/"+token+"/code", body, nil), 200, "We sent a code to •••4567")
	cr, c := e.codeOf(t)
	if c.scope != id || c.address != nil || cr.Address != "" || cr.Channel != "sms" {
		t.Fatalf("a request's code: %+v %+v", cr, c)
	}
	body = must(t, e.next("/q/"+token, body, url.Values{"do": {"verify"}, "code": {cr.Code}}), 200, `value="Bola Ade"`, `name="a.bank"`)
	if strings.Contains(body, "L-1") {
		t.Fatal("a hidden prefill is shown")
	}
	e.tick(10 * time.Second)
	must(t, e.next("/q/"+token, body, url.Values{"do": {"send"}, "a.name": {"Bola A. Ade"}, "a.bank": {"GTBank"}}), 200, "Your answers have been sent to Ada Loans.")
	w := e.f.last(t)
	sub := e.submission(t, w)
	if w.Request == nil || *w.Request != id || w.Endpoint != nil || sub.Respondent.Kind != "request" || sub.Answers["name"] != "Bola A. Ade" || sub.Answers["loan"] != "L-1" {
		t.Fatalf("request write: %+v %+v", w, sub)
	}
	for status, want := range map[string]string{"answered": "answered already", "cancelled": "no longer needed", "expired": "has expired"} {
		req.Status = status
		e.f.requests[token] = req
		must(t, e.get("/q/"+token), map[string]int{"answered": 200, "cancelled": 410, "expired": 410}[status], want)
	}
	must(t, e.get("/q/"+e.keys.RequestToken(uuid.NewString())), 404)
}

func TestFormMessage(t *testing.T) {
	got := string(formMessage("Hello <b>you</b>.\n**Bold** [ok](https://ada.example/a?b=1&c=2) [no](https://evil.example)\n\nSecond [js](javascript:alert(1))", []string{"https://ada.example"}))
	want := `<p>Hello &lt;b&gt;you&lt;/b&gt;.<br><strong>Bold</strong> <a href="https://ada.example/a?b=1&amp;c=2" rel="noopener noreferrer" target="_top">ok</a> no</p><p>Second js)</p>`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := string(formMessage("", nil)); got != "<p>Thank you. We have your answers.</p>" {
		t.Fatal(got)
	}
}

func TestFormsNotOpen(t *testing.T) {
	s := &Server{Lookup: (&fakeHosts{}).lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), FormHost: formHost}
	must(t, get(s.Handler(), "GET", formHost, "/f/loan00000001"), 503, "Forms are not open yet")
}

func TestFormHostTLSAsk(t *testing.T) {
	e := newFormHost(t)
	for host, code := range map[string]int{formHost: 200, "FORMS.shops.test": 200, "other.shops.test": 404} {
		w := httptest.NewRecorder()
		e.s.TLSAsk(w, httptest.NewRequest("GET", "/tls/ask?domain="+host, nil))
		if w.Code != code {
			t.Errorf("%s: %d", host, w.Code)
		}
	}
}

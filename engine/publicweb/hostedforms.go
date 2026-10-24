package publicweb

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Hosted forms on the form host (docs/spec/forms.md 5.2, 5.3, 6, 8.3):
//
//	GET/POST /f/{public_id}         a form trigger's form (access public)
//	POST     /f/{public_id}/code    asks for a verification code
//	GET      /f/{public_id}/done/{reference}?k=   the result page (after.mode result)
//	GET/POST /embed/f/{public_id}…  the same, framed by the organisation's verified websites
//	GET/POST /r/{token}             a link form, through its one-time link
//	GET/POST /q/{token}             a form step's request
//	GET      /s/{reference}?k=      a submission's status link
//	GET      /i/{token}             an invoice's page (invoice.go)
//	POST     /report                reports a form
//
// Pages are rendered on the server and work without JavaScript. A
// multi-page form keeps the answers so far in the page itself, sealed
// under the inbox key and bound to the form and its one-time token: no
// cookie and no session in the database. Conditions (show_if) are
// evaluated here between pages.

// Defaults for form submissions per client address (forms 7.3): 50 an
// hour, generous because mobile networks share addresses.
var FormSubmissionsPerIP = formguard.Bucket{Rate: rate.Every(time.Hour / 50), Burst: 20}

// maxCarried is what files on earlier pages may hold together: they are
// carried, sealed, in the page through the later ones.
const maxCarried = 10 << 20

// formCEL evaluates show_if conditions over the answers so far.
var formCEL = expr.MustNewWithRoots("answers")

var (
	publicIDRe  = regexp.MustCompile(`^[a-z0-9]{12}$`)
	formRefRe   = regexp.MustCompile(`^F-[A-Z2-9]{8}$`)
	linkTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)
	answerRefRe = regexp.MustCompile(`answers(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[["']([^"']+)["']\])`)
	codeRe      = regexp.MustCompile(`^[0-9]{6}$`)
)

// formsReady reports whether hosted forms can take answers.
func (s *Server) formsReady() bool {
	return s.Forms != nil && s.FormKeys != nil && s.fguard != nil
}

// hform is the form a request is for: a live form (perhaps through a
// one-time link) or a form step's request.
type hform struct {
	pub      PublicForm
	link     *FormLink
	linkView *forms.LinkView
	req      *FormRequest
	reqView  forms.RequestView

	title, description, business string
	// tenant is the business's, for its look.
	tenant        uuid.UUID
	schema, types map[string]any
	pages         []wd.FormPage
	fields        map[string]*field
	// verify is the channel a code goes to (sms or email), empty for none;
	// fixed is true when the address is known (a bound link, a request).
	verify string
	fixed  bool
	hint   string
	scope  uuid.UUID
	// guardID is the formguard form id; base is where the form posts.
	guardID string
	base    string
	embed   bool
	origins []string
	report  string
	// site is the embedding website, when it is a verified one.
	site string
	// done is where the result page is; keys seal and hash.
	done string
	keys *forms.Keys
}

// hstate is what a form's pages carry between them, sealed.
type hstate struct {
	Answers  map[string]any       `json:"a,omitempty"`
	Raw      map[string][]string  `json:"r,omitempty"` // as typed, to show again
	Files    map[string]heldFile  `json:"f,omitempty"`
	Consents map[string]time.Time `json:"c,omitempty"`
	// Path is the pages shown, in order; the last is the current one.
	Path     []int    `json:"p,omitempty"`
	Pending  *address `json:"w,omitempty"` // a code was sent here
	Verified *address `json:"v,omitempty"`
}

type address struct {
	Channel string `json:"c"` // sms or email
	Address string `json:"a"`
}

type heldFile struct {
	Name string `json:"n"`
	Type string `json:"t"`
	Data []byte `json:"d"`
}

// hformData is the hosted form template's input.
type hformData struct {
	pageData
	Business    string
	Description string
	Action      string
	CodeAction  string
	Token       string
	State       string
	Honeypot    string
	Step        string // verify, code or page
	PageTitle   string
	PageNo      int
	Fields      []*field
	Error       string
	Notice      string
	Channel     string
	Hint        string
	Fixed       bool
	Address     string
	CanBack     bool
	Last        bool
	Multipart   bool
	Embed       bool
	Origins     string
	Report      string
	Home        string // none: a form has no page behind it
	Purpose     string // the description, as the privacy notice's purpose
}

// hdoneData is the thank-you, result and status pages' input.
type hdoneData struct {
	pageData
	Business  string
	Reference string
	Headline  string
	Message   any // template.HTML from formMessage, or a string
	Lines     []string
	StatusURL string
	Refresh   string
	Embed     bool
	Origins   string
	Home      string
	// Updates are the buttons for getting updates on WhatsApp or Telegram.
	Updates []updateLink
}

// appData is the page of a members form: it opens in the app.
type appData struct {
	closedData
	App string
}

func (s *Server) formLimiter() *formguard.Limiter { return s.FormSubmissions }

// routeForms serves the form host.
func (s *Server) routeForms(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	embed := strings.HasPrefix(p, "/embed/")
	if embed {
		p = strings.TrimPrefix(p, "/embed")
	}
	head, rest, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	key, sub, _ := strings.Cut(rest, "/")
	// An invoice's page needs none of the forms' keys.
	if !embed && head == "i" {
		s.serveInvoice(w, r, key, sub)
		return
	}
	// A school receipt's verification page (receipt.go) shares /r/ with
	// form links and needs no keys either: a receipt's token answers first.
	if !embed && head == "r" && sub == "" && s.serveReceipt(w, r, key) {
		return
	}
	// A school family's page (family.go) shares /s/ with status links and
	// needs the inbox key only for its forms: a family link answers first.
	if !embed && head == "s" && s.serveFamily(w, r, key, sub) {
		return
	}
	if !s.formsReady() && head != "" {
		s.renderPrivate(w, r, http.StatusServiceUnavailable, "formclosed.html", closedData{pageData: pageData{Title: "Not open yet", Lang: "en"},
			Business: "Taskiem", Message: "Forms are not open yet. Please try again later."})
		return
	}
	switch {
	case !embed && p == "/report":
		s.formReport(w, r)
	case head == "f" && publicIDRe.MatchString(key):
		switch {
		case sub == "" || sub == "code":
			s.serveFormTrigger(w, r, key, sub, embed)
		case strings.HasPrefix(sub, "done/") && r.Method != http.MethodPost:
			s.serveFormDone(w, r, key, strings.TrimPrefix(sub, "done/"), embed)
		default:
			s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		}
	case !embed && head == "r" && linkTokenRe.MatchString(key) && (sub == "" || sub == "code"):
		s.serveFormLink(w, r, key, sub)
	case !embed && head == "q" && linkTokenRe.MatchString(key) && (sub == "" || sub == "code"):
		s.serveFormRequest(w, r, key, sub)
	case !embed && head == "s" && sub == "" && r.Method != http.MethodPost:
		s.serveFormStatus(w, r, key)
	case !embed && head == "u" && publicIDRe.MatchString(key) && (sub == "" || reachAppRe.MatchString(sub)):
		s.serveReach(w, r, key, sub)
	default:
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
	}
}

// formEntry caches a form by its public id.
type formEntry struct {
	f  PublicForm
	ok bool
	at time.Time
}

func (s *Server) publicForm(ctx context.Context, id string) (PublicForm, bool, error) {
	if e, hit := s.formCache.Get(id); hit && (e.ok || s.now().Sub(e.at) < MissTTL) {
		return e.f, e.ok, nil
	}
	f, ok, err := s.Forms.Form(ctx, id)
	if err != nil {
		return PublicForm{}, false, err
	}
	s.formCache.Put(id, formEntry{f: f, ok: ok, at: s.now()})
	return f, ok, nil
}

func (s *Server) tryLater(w http.ResponseWriter, r *http.Request, what string, err error) {
	if !errors.Is(err, context.Canceled) {
		s.Logger.Error("form host: "+what, "err", err)
	}
	s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
}

func (s *Server) formNotice(w http.ResponseWriter, r *http.Request, status int, title string, tenant uuid.UUID, business, msg string) {
	if business == "" {
		business = "Taskiem"
	}
	s.renderPrivate(w, r, status, "formclosed.html", closedData{pageData: pageData{Title: title, Lang: "en", Look: s.lookFor(r.Context(), tenant, business)}, Business: business, Message: msg})
}

func allowed(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	if slices.Contains(methods, r.Method) {
		return true
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	w.WriteHeader(http.StatusMethodNotAllowed)
	return false
}

// serveFormTrigger serves /f/{public_id} and /embed/f/{public_id}.
func (s *Server) serveFormTrigger(w http.ResponseWriter, r *http.Request, id, sub string, embed bool) {
	if !allowed(w, r, http.MethodGet, http.MethodHead, http.MethodPost) {
		return
	}
	pub, ok, err := s.publicForm(r.Context(), id)
	if err != nil {
		s.tryLater(w, r, "form", err)
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	switch pub.Trigger.Access {
	case "members":
		// Served by the app, under its session; never framed.
		msg := pub.Trigger.Title + " is for members of " + pub.Business + ". It opens in the Taskiem app, where you sign in."
		d := appData{closedData: closedData{pageData: pageData{Title: pub.Trigger.Title, Lang: "en", Look: s.lookFor(r.Context(), pub.Tenant, pub.Business)}, Business: pub.Business, Message: msg}}
		if s.AppURL != "" {
			d.App = strings.TrimRight(s.AppURL, "/") + "/forms/" + pub.PublicID
		}
		s.renderPrivate(w, r, http.StatusOK, "formapp.html", d)
		return
	case "link":
		s.formNotice(w, r, http.StatusForbidden, pub.Trigger.Title, pub.Tenant, pub.Business, "This form opens from the link you were sent. Please use that link.")
		return
	}
	hf := s.triggerForm(pub, "/f/"+id, "f:"+id)
	if embed {
		hf.base, hf.done = "/embed"+hf.base, "/embed"+hf.done
		hf.embed = true
		origins, err := s.embedOrigins(r.Context(), pub.Tenant)
		if err != nil {
			s.Logger.Error("form host: embed origins", "tenant", pub.Tenant, "err", err)
			origins = nil // fail closed: nobody may frame it
		}
		hf.origins = origins
		httpsec.SetPublicEmbed(w.Header(), origins)
	} else if pub.Trigger.After.Mode == "redirect" || strings.Contains(pub.Trigger.After.Message, "](") {
		origins, err := s.embedOrigins(r.Context(), pub.Tenant)
		if err != nil {
			s.Logger.Error("form host: embed origins", "tenant", pub.Tenant, "err", err)
		}
		hf.origins = origins
	}
	if !pub.Open {
		msg := pub.Trigger.Title + " is not taking answers right now."
		if pub.ClosedMessage != "" {
			msg += " " + pub.ClosedMessage
		}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusConflict
		}
		s.formNotice(w, r, status, "Closed", pub.Tenant, pub.Business, msg)
		return
	}
	s.serveHosted(w, r, hf, sub, s.prefillFromQuery(hf, r.URL.Query()))
}

// triggerForm is a live form's hform.
func (s *Server) triggerForm(pub PublicForm, base, guardID string) *hform {
	t := pub.Trigger
	hf := &hform{pub: pub, tenant: pub.Tenant, title: t.Title, description: t.Description, business: pub.Business, schema: pub.Schema, types: pub.Types,
		pages: wd.FormPages(t.Pages, pub.Schema), fields: fields(pub.Schema), guardID: guardID, base: base, scope: pub.Endpoint,
		report: "f:" + pub.PublicID, done: "/f/" + pub.PublicID, keys: s.FormKeys}
	switch t.Verify {
	case "phone":
		hf.verify = "sms"
	case "email":
		hf.verify = "email"
	}
	return hf
}

// prefillFromQuery takes a link's query parameters for the fields marked
// x-form.prefill, and nothing else: a link cannot put words in the
// respondent's mouth (forms 6.3).
func (s *Server) prefillFromQuery(hf *hform, q url.Values) map[string]any {
	out := map[string]any{}
	for name, f := range hf.fields {
		if !f.Prefill || f.Widget == "file" {
			continue
		}
		vals, ok := q[name]
		if !ok || len(vals) == 0 {
			continue
		}
		if f.Widget == "checkboxes" {
			var l []any
			for _, v := range vals {
				l = append(l, v)
			}
			out[name] = l
			continue
		}
		out[name] = vals[0]
	}
	return out
}

// serveFormLink serves /r/{token}: a link form through its one-time link.
func (s *Server) serveFormLink(w http.ResponseWriter, r *http.Request, token, sub string) {
	if !allowed(w, r, http.MethodGet, http.MethodHead, http.MethodPost) {
		return
	}
	link, ok, err := s.Forms.Link(r.Context(), forms.TokenHash(token))
	if err != nil {
		s.tryLater(w, r, "form link", err)
		return
	}
	if !ok {
		s.formNotice(w, r, http.StatusNotFound, "Link not found", uuid.Nil, "", "This link is not valid. Please check you have the whole link.")
		return
	}
	if !link.Usable {
		s.formNotice(w, r, http.StatusGone, "Link used", uuid.Nil, "", "This link has been used already, or it has expired. Please ask for a new one.")
		return
	}
	pub, ok, err := s.publicForm(r.Context(), link.PublicID)
	if err != nil {
		s.tryLater(w, r, "form", err)
		return
	}
	if !ok {
		s.formNotice(w, r, http.StatusNotFound, "Form not found", uuid.Nil, "", "This form is no longer available.")
		return
	}
	if !pub.Open {
		msg := pub.Trigger.Title + " is not taking answers right now."
		if pub.ClosedMessage != "" {
			msg += " " + pub.ClosedMessage
		}
		s.formNotice(w, r, http.StatusOK, "Closed", pub.Tenant, pub.Business, msg)
		return
	}
	hf := s.triggerForm(pub, "/r/"+token, "f:"+pub.PublicID+"/l:"+link.ID.String())
	hf.link = &link
	if len(link.BoundView) > 0 {
		var v forms.LinkView
		if err := s.FormKeys.Open(forms.LinkViewAAD(link.ID.String()), link.BoundView, &v); err != nil || v.Address == "" {
			s.Logger.Error("form host: a link's bound view does not open", "link", link.ID)
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		hf.linkView = &v
		hf.verify, hf.fixed, hf.hint, hf.scope = channelOf(v.Channel), true, v.Hint, link.ID
	}
	if pub.Trigger.After.Mode == "redirect" || strings.Contains(pub.Trigger.After.Message, "](") {
		hf.origins, _ = s.embedOrigins(r.Context(), pub.Tenant)
	}
	s.serveHosted(w, r, hf, sub, s.prefillFromQuery(hf, r.URL.Query()))
}

func channelOf(c string) string {
	if c == "email" {
		return "email"
	}
	return "sms"
}

// serveFormRequest serves /q/{token}: a form step's request.
func (s *Server) serveFormRequest(w http.ResponseWriter, r *http.Request, token, sub string) {
	if !allowed(w, r, http.MethodGet, http.MethodHead, http.MethodPost) {
		return
	}
	req, ok, err := s.Forms.Request(r.Context(), forms.TokenHash(token))
	if err != nil {
		s.tryLater(w, r, "form request", err)
		return
	}
	if !ok {
		s.formNotice(w, r, http.StatusNotFound, "Link not found", uuid.Nil, "", "This link is not valid. Please check you have the whole link.")
		return
	}
	switch req.Status {
	case "pending":
	case "answered":
		s.formNotice(w, r, http.StatusOK, "Already answered", req.Tenant, req.Business, "Thank you. This has been answered already.")
		return
	case "cancelled":
		s.formNotice(w, r, http.StatusGone, "No longer needed", req.Tenant, req.Business, "This request is no longer needed. You do not need to do anything.")
		return
	default:
		s.formNotice(w, r, http.StatusGone, "Expired", req.Tenant, req.Business, "This request has expired. If you still need to answer it, please contact "+req.Business+".")
		return
	}
	hf := &hform{req: &req, tenant: req.Tenant, title: req.Title, description: req.Description, business: req.Business, schema: req.Schema,
		pages: wd.FormPages(nil, req.Schema), fields: fields(req.Schema), guardID: "q:" + req.ID.String(), base: "/q/" + token,
		scope: req.ID, report: "q:" + req.ID.String(), keys: s.FormKeys}
	if len(req.PublicView) > 0 {
		if err := s.FormKeys.Open(forms.RequestViewAAD(req.ID.String()), req.PublicView, &hf.reqView); err != nil {
			s.Logger.Error("form host: a request's view does not open", "request", req.ID)
		}
	}
	if req.Verify {
		hf.verify, hf.fixed, hf.hint = channelOf(hf.reqView.Channel), true, hf.reqView.Hint
		if hf.hint == "" {
			hf.hint = "the phone or email this link was sent to"
		}
	}
	s.serveHosted(w, r, hf, sub, hf.reqView.Prefill)
}

// stateAAD binds a form's sealed state to the form and its token.
func stateAAD(guardID, token string) string { return "form state " + guardID + " " + token }

// serveHosted runs a form's steps: the code, the pages, the submission.
func (s *Server) serveHosted(w http.ResponseWriter, r *http.Request, hf *hform, sub string, prefill map[string]any) {
	if r.Method != http.MethodPost {
		if sub != "" {
			w.Header().Set("Allow", "POST")
			s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
			return
		}
		st := &hstate{}
		hf.seed(st, prefill)
		if hf.verify == "" {
			st.Path = []int{0}
		}
		s.showHosted(w, r, http.StatusOK, hf, st, "", "", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, hf.bodyLimit())
	var perr error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		perr = r.ParseMultipartForm(32 << 20) //nolint:gosec // G120: bounded by http.MaxBytesReader just above; gosec's rule knows no sanitiser
	} else {
		perr = r.ParseForm()
	}
	if err := perr; err != nil {
		st := &hstate{}
		hf.seed(st, prefill)
		if hf.verify == "" {
			st.Path = []int{0}
		}
		msg := "This form could not be read. Please check your answers and send again."
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			msg = "What you sent is too large. Please send smaller files."
		}
		s.showHosted(w, r, http.StatusBadRequest, hf, st, "", msg, "")
		return
	}
	v := r.PostForm
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	restart := func(msg string) {
		st := &hstate{}
		hf.seed(st, prefill)
		if hf.verify == "" {
			st.Path = []int{0}
		}
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, "", msg, "")
	}
	if formguard.HoneypotTripped(v) {
		restart("This form could not be sent. Please check your answers and send again.")
		return
	}
	token := v.Get(tokenField)
	if _, err := s.fstep.Verify(token, hf.guardID, s.now()); err != nil {
		if errors.Is(err, formguard.ErrExpired) {
			restart("This page was open for a long time. Please fill it in again.")
		} else {
			restart("This form could not be read. Please fill it in again.")
		}
		return
	}
	st := &hstate{}
	if raw, err := base64.RawURLEncoding.DecodeString(v.Get("state")); err != nil || s.FormKeys.Open(stateAAD(hf.guardID, token), raw, st) != nil {
		restart("This form could not be read. Please fill it in again.")
		return
	}
	if hf.embed {
		if site := v.Get("site"); slices.Contains(hf.origins, site) {
			hf.site = site
		}
	}
	do := v.Get("do")
	if sub == "code" {
		do = "code"
	}
	needVerify := hf.verify != "" && st.Verified == nil
	switch {
	case do == "code" && hf.verify != "" && st.Verified == nil:
		s.formCode(w, r, hf, st, token, v)
	case do == "verify" && hf.verify != "" && st.Verified == nil:
		s.formVerify(w, r, hf, st, token, v)
	case needVerify || len(st.Path) == 0:
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, "Please confirm your "+channelWord(hf.verify)+" first.", "")
	case do == "back":
		hf.keepPage(st, v, r, s.now())
		if len(st.Path) > 1 {
			st.Path = st.Path[:len(st.Path)-1]
		}
		s.showHosted(w, r, http.StatusOK, hf, st, token, "", "")
	default:
		s.formNext(w, r, hf, st, token, v)
	}
}

func channelWord(c string) string {
	if c == "email" {
		return "email address"
	}
	return "phone number"
}

// bodyLimit is the most a page's POST may carry: the answers, the files
// carried from earlier pages, and this form's files.
func (hf *hform) bodyLimit() int64 {
	n := int64(maxAnswersBody) + 64<<10
	if hf.hasFiles() {
		n += 2 * maxCarried
		files := 0
		for _, f := range hf.fields {
			if f.Widget == "file" && files < maxFiles {
				n += int64(f.maxMB * (1 << 20))
				files++
			}
		}
	}
	return n
}

func (hf *hform) hasFiles() bool {
	for _, f := range hf.fields {
		if f.Widget == "file" {
			return true
		}
	}
	return false
}

// seed puts prefilled values in a new state: hidden fields as answers,
// the others as what their inputs show first.
func (hf *hform) seed(st *hstate, prefill map[string]any) {
	for name, val := range prefill {
		f, ok := hf.fields[name]
		if !ok || f.Widget == "file" {
			continue
		}
		if f.Hidden {
			if st.Answers == nil {
				st.Answers = map[string]any{}
			}
			st.Answers[name] = coerce(f, val)
			continue
		}
		g := *f
		g.Options = slices.Clone(f.Options)
		g.prefillValue(val)
		if st.Raw == nil {
			st.Raw = map[string][]string{}
		}
		st.Raw[name] = g.rawValues()
	}
}

// coerce makes a prefilled hidden value the schema's type where it can.
func coerce(f *field, v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch f.typ {
	case "integer", "number":
		if x, _, msg := f.parseNumber(s); msg == "" {
			return x
		}
	case "boolean":
		return s == "true" || s == "1" || s == "on"
	}
	return v
}

// rawValues is what an input shows, as form values.
func (f *field) rawValues() []string {
	switch f.Widget {
	case "checkbox", "consent":
		if f.Checked {
			return []string{"true"}
		}
		return nil
	case "checkboxes", "radio", "select":
		var out []string
		for _, o := range f.Options {
			if o.Checked {
				out = append(out, o.Value)
			}
		}
		return out
	}
	return []string{f.Value}
}

// current is the page being shown.
func (st *hstate) current() int {
	if len(st.Path) == 0 {
		return -1
	}
	return st.Path[len(st.Path)-1]
}

// pageFields are a page's fields, without the hidden ones.
func (hf *hform) pageFields(i int) []*field {
	var out []*field
	if i < 0 || i >= len(hf.pages) {
		return nil
	}
	for _, name := range hf.pages[i].Fields {
		if f, ok := hf.fields[name]; ok && !f.Hidden {
			out = append(out, f)
		}
	}
	return out
}

// shownAnswers are the answers on the pages shown so far, before page
// upTo (all of them for -1), with the hidden fields: what conditions read
// and what is sent.
func (hf *hform) shownAnswers(st *hstate, upTo int) map[string]any {
	out := map[string]any{}
	for name, f := range hf.fields {
		if f.Hidden {
			if v, ok := st.Answers[name]; ok {
				out[name] = v
			}
		}
	}
	for i, p := range st.Path {
		if upTo >= 0 && i >= upTo {
			break
		}
		for _, f := range hf.pageFields(p) {
			if v, ok := st.Answers[f.Name]; ok && f.Widget != "file" {
				out[f.Name] = v
			}
		}
	}
	return out
}

// shows evaluates a page's condition; one that cannot be evaluated (it
// reads an answer not given) hides the page.
func (hf *hform) shows(i int, answers map[string]any) bool {
	c := wd.FormCondition(hf.pages[i].ShowIf)
	if i == 0 || c == "" {
		return true
	}
	ok, err := formCEL.EvalBool(c, map[string]any{"answers": answers})
	return err == nil && ok
}

// next is the page after the current one that shows, or -1.
func (hf *hform) next(st *hstate) int {
	answers := hf.shownAnswers(st, -1)
	for j := st.current() + 1; j < len(hf.pages); j++ {
		if hf.shows(j, answers) {
			return j
		}
	}
	return -1
}

// lastPage reports whether no page can follow the current one, whatever
// it is answered: then its button sends the form.
func (hf *hform) lastPage(st *hstate) bool {
	cur := st.current()
	known := hf.shownAnswers(st, len(st.Path)-1)
	here := map[string]bool{}
	for _, f := range hf.pageFields(cur) {
		here[f.Name] = true
	}
	for j := cur + 1; j < len(hf.pages); j++ {
		c := wd.FormCondition(hf.pages[j].ShowIf)
		if c == "" {
			return false
		}
		for _, m := range answerRefRe.FindAllStringSubmatch(c, -1) {
			if here[m[1]+m[2]] {
				return false // depends on this page's answers
			}
		}
		if hf.shows(j, known) {
			return false
		}
	}
	return true
}

// showHosted renders a form's current step with a token: a fresh one
// unless token is given.
func (s *Server) showHosted(w http.ResponseWriter, r *http.Request, status int, hf *hform, st *hstate, token, errMsg, notice string) {
	if token == "" {
		t, err := s.fguard.Issue(hf.guardID, s.now(), 0)
		if err != nil {
			s.Logger.Error("form host: form token", "err", err)
			s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		token = string(t)
	}
	sealed, err := s.FormKeys.Seal(stateAAD(hf.guardID, token), st)
	if err != nil {
		s.Logger.Error("form host: seal state", "err", err)
		s.page(w, r, http.StatusInternalServerError, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	d := hformData{pageData: pageData{Title: hf.title, Lang: "en", Look: s.lookFor(r.Context(), hf.tenant, hf.business)}, Business: hf.business, Description: hf.description,
		Action: hf.base, CodeAction: hf.base + "/code", Token: token, State: base64.RawURLEncoding.EncodeToString(sealed), Honeypot: formguard.HoneypotField,
		Error: errMsg, Notice: notice, Channel: hf.verify, Hint: hf.hint, Fixed: hf.fixed, Embed: hf.embed, Origins: strings.Join(hf.origins, " "),
		Report: hf.report, Multipart: hf.hasFiles(), Purpose: strings.TrimRight(strings.TrimSpace(hf.description), ".")}
	switch {
	case hf.verify != "" && st.Verified == nil && st.Pending == nil:
		d.Step = "verify"
		if raw := st.Raw["\x00address"]; len(raw) > 0 {
			d.Address = raw[0]
		}
	case hf.verify != "" && st.Verified == nil:
		d.Step = "code"
		if d.Notice == "" && errMsg == "" {
			d.Notice = "We sent a code to " + hf.hintFor(st.Pending) + "."
		}
	default:
		d.Step = "page"
		cur := st.current()
		if cur < 0 {
			cur, st.Path = 0, []int{0}
		}
		d.PageTitle, d.PageNo, d.CanBack, d.Last = hf.pages[cur].Title, len(st.Path), len(st.Path) > 1, hf.lastPage(st)
		for _, f := range hf.pageFields(cur) {
			g := *f
			g.Options = slices.Clone(f.Options)
			if raw, ok := st.Raw[f.Name]; ok {
				g.show(url.Values{f.Input(): raw})
			}
			if h, ok := st.Files[f.Name]; ok {
				g.Held = h.Name
			}
			d.Fields = append(d.Fields, &g)
		}
	}
	s.renderPrivate(w, r, status, "hostedform.html", d)
}

// hintFor is where a code went, masked.
func (hf *hform) hintFor(a *address) string {
	if hf.fixed || a == nil {
		return hf.hint
	}
	if a.Channel == "email" {
		return forms.MaskEmail(a.Address)
	}
	return forms.MaskPhone(a.Address)
}

// formCode sends a verification code (POST …/code).
func (s *Server) formCode(w http.ResponseWriter, r *http.Request, hf *hform, st *hstate, token string, v url.Values) {
	a := &address{Channel: hf.verify}
	switch {
	case hf.linkView != nil:
		a.Address = hf.linkView.Address
	case hf.req != nil:
		// The api role sends it to the request's recipient.
	default:
		raw := strings.TrimSpace(v.Get("address"))
		if st.Raw == nil {
			st.Raw = map[string][]string{}
		}
		st.Raw["\x00address"] = []string{raw}
		var err error
		if hf.verify == "email" {
			a.Address, err = sender.NormaliseEmail(raw)
			a.Address = strings.ToLower(a.Address)
		} else {
			a.Address, err = sender.NormalisePhone(raw, phoneCountries)
		}
		if err != nil {
			st.Pending = nil
			msg := "Please enter a Nigerian mobile number, like 0803 123 4567."
			if hf.verify == "email" {
				msg = "Please check your email address."
			}
			s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, msg, "")
			return
		}
	}
	if !s.formLimiter().Allow(s.ipKey(r)) {
		w.Header().Set("Retry-After", "60")
		s.showHosted(w, r, http.StatusTooManyRequests, hf, st, token, "Too many requests from this connection. Please wait a few minutes and try again.", "")
		return
	}
	code, err := sixDigits()
	if err != nil {
		s.tryLater(w, r, "code", err)
		return
	}
	scope := hf.scope.String()
	payload, err := s.FormKeys.Seal(forms.CodeAAD(scope), forms.CodeRequest{Channel: a.Channel, Address: a.Address, Code: code})
	if err != nil {
		s.tryLater(w, r, "seal code", err)
		return
	}
	ok, err := s.Forms.Code(r.Context(), hf.scope, hf.addressHash(a), s.FormKeys.CodeHash(scope, code), payload)
	if err != nil {
		s.tryLater(w, r, "code", err)
		return
	}
	if !ok {
		s.showHosted(w, r, http.StatusTooManyRequests, hf, st, token, "Too many codes have been asked for. Please wait 10 minutes and try again.", "")
		return
	}
	st.Pending = a
	s.showHosted(w, r, http.StatusOK, hf, st, token, "", "We sent a code to "+hf.hintFor(a)+". It works for 10 minutes.")
}

// addressHash is a code's address under its scope: none for a request,
// whose code goes to its recipient.
func (hf *hform) addressHash(a *address) []byte {
	if hf.req != nil || a == nil || a.Address == "" {
		return nil
	}
	return hf.keys.AddressHash(hf.scope.String(), a.Address)
}

func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// formVerify checks a code (do=verify).
func (s *Server) formVerify(w http.ResponseWriter, r *http.Request, hf *hform, st *hstate, token string, v url.Values) {
	if st.Pending == nil {
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, "Please ask for a code first.", "")
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(v.Get("code")), " ", "")
	if !codeRe.MatchString(code) {
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, "The code is the 6 digits we sent you.", "")
		return
	}
	ok, err := s.Forms.CheckCode(r.Context(), hf.scope, hf.addressHash(st.Pending), s.FormKeys.CodeHash(hf.scope.String(), code))
	if err != nil {
		s.tryLater(w, r, "code check", err)
		return
	}
	if !ok {
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token,
			"That code is not right, or it has expired. Please check it, or ask for a new one.", "")
		return
	}
	st.Verified, st.Pending = st.Pending, nil
	st.Path = []int{0}
	s.showHosted(w, r, http.StatusOK, hf, st, token, "", "Thank you, that is confirmed.")
}

// readPage parses the current page's answers into the state. It returns
// the fields with their errors and the first message.
func (hf *hform) readPage(st *hstate, v url.Values, r *http.Request, now time.Time, strict bool) ([]*field, string) {
	if st.Answers == nil {
		st.Answers = map[string]any{}
	}
	if st.Raw == nil {
		st.Raw = map[string][]string{}
	}
	var out []*field
	first := ""
	carried := 0
	for name, h := range st.Files {
		if !slices.ContainsFunc(hf.pageFields(st.current()), func(f *field) bool { return f.Name == name }) {
			carried += len(h.Data)
		}
	}
	for _, f := range hf.pageFields(st.current()) {
		g := *f
		g.Options = slices.Clone(f.Options)
		if g.Widget == "file" {
			msg := hf.readFile(st, &g, r, &carried)
			if h, ok := st.Files[g.Name]; ok {
				g.Held = h.Name
			}
			if msg != "" && strict {
				g.Error = msg
			}
			if msg != "" && first == "" && strict {
				first = msg
			}
			out = append(out, &g)
			continue
		}
		g.show(v)
		st.Raw[g.Name] = g.rawValues()
		val, present, msg := g.parse(v)
		switch {
		case msg != "":
			delete(st.Answers, g.Name)
			if strict {
				g.Error = msg
				if first == "" {
					first = msg
				}
			}
		case present:
			st.Answers[g.Name] = val
		default:
			delete(st.Answers, g.Name)
		}
		if g.Widget == "consent" {
			if st.Consents == nil {
				st.Consents = map[string]time.Time{}
			}
			if present && msg == "" {
				if _, ok := st.Consents[g.Name]; !ok {
					st.Consents[g.Name] = now.UTC()
				}
			} else {
				delete(st.Consents, g.Name)
			}
		}
		out = append(out, &g)
	}
	return out, first
}

// keepPage saves what a page holds without refusing it (going back).
func (hf *hform) keepPage(st *hstate, v url.Values, r *http.Request, now time.Time) {
	hf.readPage(st, v, r, now, false)
}

// readFile takes a file field's upload, checking its size and its type by
// its content (forms 6.5).
func (hf *hform) readFile(st *hstate, f *field, r *http.Request, carried *int) string {
	var h *multipart.FileHeader
	if r.MultipartForm != nil {
		for _, x := range r.MultipartForm.File[f.Input()] {
			if x.Filename != "" && x.Size > 0 {
				h = x
				break
			}
		}
	}
	if h == nil {
		if _, ok := st.Files[f.Name]; !ok && f.Required {
			return "Please attach " + f.Label + "."
		}
		return ""
	}
	limit := int64(f.maxMB * (1 << 20))
	if h.Size > limit {
		return fmt.Sprintf("%s is larger than %s MB. Please send a smaller file.", f.Label, fmtNum(f.maxMB))
	}
	file, err := h.Open()
	if err != nil {
		return "The file for " + f.Label + " could not be read. Please attach it again."
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return "The file for " + f.Label + " could not be read. Please attach it again."
	}
	ctype := strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0])
	if !f.acceptsType(ctype) {
		return "The file for " + f.Label + " is not a type this question takes (" + strings.ReplaceAll(f.Accept, ",", ", ") + ")."
	}
	n := 0
	for name := range st.Files {
		if name != f.Name {
			n++
		}
	}
	if n >= maxFiles {
		return fmt.Sprintf("A form takes at most %d files.", maxFiles)
	}
	if !hf.isLastPossible(st) && *carried+len(data) > maxCarried {
		return "Files before the last page can be at most 10 MB together. Please send a smaller file."
	}
	*carried += len(data)
	if st.Files == nil {
		st.Files = map[string]heldFile{}
	}
	st.Files[f.Name] = heldFile{Name: clipFileName(h.Filename), Type: ctype, Data: data}
	return ""
}

// isLastPossible is lastPage for files: when no page can follow, a file
// is sent with this page and never carried.
func (hf *hform) isLastPossible(st *hstate) bool { return hf.lastPage(st) }

func clipFileName(n string) string {
	n = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, n))
	if r := []rune(n); len(r) > 120 {
		n = string(r[:120])
	}
	if n == "" {
		n = "file"
	}
	return n
}

// formNext takes a page and shows the next, or sends the form.
func (s *Server) formNext(w http.ResponseWriter, r *http.Request, hf *hform, st *hstate, token string, v url.Values) {
	shown, msg := hf.readPage(st, v, r, s.now(), true)
	if msg != "" {
		s.showPage(w, r, http.StatusUnprocessableEntity, hf, st, token, msg, shown)
		return
	}
	if j := hf.next(st); j >= 0 {
		st.Path = append(st.Path, j)
		s.showHosted(w, r, http.StatusOK, hf, st, token, "", "")
		return
	}
	s.formSubmit(w, r, hf, st, token, v)
}

// showPage renders the current page with fields already checked.
func (s *Server) showPage(w http.ResponseWriter, r *http.Request, status int, hf *hform, st *hstate, token, msg string, shown []*field) {
	byName := map[string]*field{}
	for _, f := range shown {
		byName[f.Name] = f
	}
	saved := hf.fields
	hf.fields = map[string]*field{}
	for k, f := range saved {
		if g, ok := byName[k]; ok {
			hf.fields[k] = g
		} else {
			hf.fields[k] = f
		}
	}
	defer func() { hf.fields = saved }()
	s.showHosted(w, r, status, hf, st, token, msg, "")
}

// ipKey is the client address's keyed hash, for limits.
func (s *Server) ipKey(r *http.Request) string {
	m := hmac.New(sha256.New, s.FormKeys.Guard)
	m.Write([]byte("form client address"))
	ip := formguard.HashIP(m.Sum(nil), s.ClientIP(r), s.now())
	return hex.EncodeToString(ip[:])
}

// formStatusKey is a submission's status link secret, derived from the
// token's idempotency key as storefronts' is, so a repeat gets the same.
func (s *Server) formStatusKey(idem [32]byte) string {
	m := hmac.New(sha256.New, s.FormKeys.Guard)
	m.Write([]byte("form status key\x00"))
	m.Write(idem[:])
	return statusEncoding.EncodeToString(m.Sum(nil)[:18])
}

var refEncoding = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// formReference is a submission's reference: drawn from the idempotency
// key under the guard key, so a repeat of a submission (a double tap, a
// retry) draws the same one, and the n-th draw is another when one is
// taken.
func (s *Server) formReference(idem [32]byte, n int) string {
	m := hmac.New(sha256.New, s.FormKeys.Guard)
	m.Write([]byte("form reference\x00"))
	m.Write(idem[:])
	m.Write([]byte{byte(n)}) //nolint:gosec // n is an attempt number, below 3
	return "F-" + refEncoding.EncodeToString(m.Sum(nil)[:5])
}

// formSubmit checks the whole form and writes it to the inbox.
func (s *Server) formSubmit(w http.ResponseWriter, r *http.Request, hf *hform, st *hstate, token string, v url.Values) {
	claims, err := s.fguard.Verify(token, hf.guardID, s.now())
	switch {
	case errors.Is(err, formguard.ErrTooFast):
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, "Please check your answers and send again.", "")
		return
	case err != nil:
		st2 := &hstate{}
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st2, "", "This page was open for a long time. Please fill it in again.", "")
		return
	}
	if !s.formLimiter().Allow(s.ipKey(r)) {
		w.Header().Set("Retry-After", "60")
		s.showHosted(w, r, http.StatusTooManyRequests, hf, st, token, "Too many forms have been sent from this connection. Please wait a few minutes and send again.", "")
		return
	}
	answers := hf.shownAnswers(st, -1)
	if msg := hf.validate(st, answers); msg != "" {
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, msg, "")
		return
	}
	now := s.now().UTC()
	sub := forms.Submission{Answers: answers, Respondent: hf.respondent(st), Channel: "direct", Submitted: now}
	if hf.embed {
		sub.Channel, sub.Site = "embed", hf.site
	}
	var files []heldFile
	shown := map[string]bool{}
	for _, p := range st.Path {
		for _, f := range hf.pageFields(p) {
			shown[f.Name] = true
			if f.Widget == "consent" {
				if at, ok := st.Consents[f.Name]; ok && answers[f.Name] == true {
					sum := sha256.Sum256([]byte(f.Consent))
					sub.Consents = append(sub.Consents, forms.Consent{Field: f.Name, TextHash: hex.EncodeToString(sum[:]), At: at})
				}
			}
			if h, ok := st.Files[f.Name]; ok && f.Widget == "file" {
				sub.Files = append(sub.Files, forms.FileMeta{Field: f.Name, Name: h.Name, Type: h.Type, Size: int64(len(h.Data))})
				files = append(files, h)
			}
		}
	}
	if raw, _ := json.Marshal(answers); len(raw) > maxAnswersBody {
		s.showHosted(w, r, http.StatusUnprocessableEntity, hf, st, token, "Your answers are too long. Please shorten them and send again.", "")
		return
	}
	idem := claims.IdempotencyKey()
	sub.StatusKey = s.formStatusKey(idem)
	fw := FormWrite{Idem: idem[:], StatusHash: storefront.StatusHash(sub.StatusKey)}
	switch {
	case hf.req != nil:
		fw.Request = &hf.req.ID
	default:
		fw.Endpoint = &hf.pub.Endpoint
		if hf.link != nil {
			fw.Link = &hf.link.ID
		}
		fw.Once = hf.once(st)
	}
	ref, ok, err := s.recordForm(r.Context(), fw, sub, files)
	switch {
	case errors.Is(err, ErrOnceSent):
		s.formNotice(w, r, http.StatusConflict, "Already sent", hf.tenant, hf.business, "You have already sent this form. Thank you.")
		return
	case errors.Is(err, ErrLinkUsed):
		s.formNotice(w, r, http.StatusConflict, "Link used", hf.tenant, hf.business, "This link has been used already. Please ask for a new one.")
		return
	case errors.Is(err, ErrAnswered):
		s.formNotice(w, r, http.StatusConflict, "Already answered", hf.tenant, hf.business, "Thank you. This has been answered already.")
		return
	case err != nil:
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("form host: submit", "form", hf.guardID, "err", err)
		}
		s.showHosted(w, r, http.StatusServiceUnavailable, hf, st, token, "Something went wrong on our side. Please send again.", "")
		return
	case !ok:
		s.formNotice(w, r, http.StatusConflict, "Closed", hf.tenant, hf.business, hf.title+" is not taking answers right now.")
		return
	}
	var updates []updateLink
	if len(hf.pub.Reach) > 0 && hf.req == nil {
		phones, proven := hf.reachPhones(st, answers)
		if len(phones) > 0 {
			updates = s.updateLinks(r.Context(), hf.pub.Tenant, hf.business, ref, phones, proven)
		}
	}
	s.formAfter(w, r, hf, ref, sub.StatusKey, updates)
}

// recordForm seals a submission and writes it with its files, drawing
// another reference if the one drawn is taken. A repeat of the same
// submission answers with its reference, writing nothing.
func (s *Server) recordForm(ctx context.Context, fw FormWrite, sub forms.Submission, files []heldFile) (string, bool, error) {
	var idem [32]byte
	copy(idem[:], fw.Idem)
	// A repeat is found by its status, under any of the references it
	// may have drawn: the inbox answers a repeated idempotency key with
	// its first row, not its reference.
	for n := range 3 {
		ref := s.formReference(idem, n)
		if _, seen, err := s.Forms.Status(ctx, ref, fw.StatusHash); err != nil {
			return "", false, err
		} else if seen {
			return ref, true, nil
		}
	}
	for n := range 3 {
		ref := s.formReference(idem, n)
		payload, err := s.FormKeys.Seal(forms.SubmissionAAD(ref), sub)
		if err != nil {
			return "", false, err
		}
		fw.Reference, fw.Payload, fw.Uploads = ref, payload, nil
		count := map[string]int{}
		for i, f := range files {
			field := sub.Files[i].Field
			data, err := s.FormKeys.SealBytes(forms.UploadAAD(ref, field, count[field]), f.Data)
			if err != nil {
				return "", false, err
			}
			count[field]++
			fw.Uploads = append(fw.Uploads, SealedUpload{Field: field, Data: data})
		}
		ok, err := s.Forms.Submit(ctx, fw)
		switch {
		case errors.Is(err, ErrReferenceTaken):
			continue
		case errors.Is(err, ErrRepeat):
			return ref, true, nil
		}
		return ref, ok, err
	}
	return "", false, ErrReferenceTaken
}

// respondent is who filled the form in (forms 8.2).
func (hf *hform) respondent(st *hstate) forms.Respondent {
	r := forms.Respondent{Kind: "anonymous"}
	switch {
	case hf.req != nil:
		return forms.Respondent{Kind: "request"}
	case hf.link != nil:
		r.Kind = "link"
	case st.Verified != nil:
		r.Kind = "verified"
	}
	if a := st.Verified; a != nil {
		if a.Channel == "email" {
			r.Email = a.Address
		} else {
			r.Phone = a.Address
		}
	}
	return r
}

// once is the once_per key of a verified respondent, when the form asks
// for one and the respondent verified that kind of address.
func (hf *hform) once(st *hstate) []byte {
	a := st.Verified
	if a == nil {
		return nil
	}
	switch hf.pub.Trigger.OncePer {
	case "phone":
		if a.Channel != "sms" {
			return nil
		}
	case "email":
		if a.Channel != "email" {
			return nil
		}
	default:
		return nil
	}
	return hf.keys.AddressHash(hf.pub.Endpoint.String(), "once:"+a.Address)
}

// validate checks the answers against the schema, with the fields of
// pages not shown no longer required, as the api role checks them again.
func (hf *hform) validate(st *hstate, answers map[string]any) string {
	onShown := map[string]bool{}
	for _, p := range st.Path {
		for _, name := range hf.pages[p].Fields {
			onShown[name] = true
		}
	}
	probe := map[string]any{}
	for k, v := range answers {
		probe[k] = v
	}
	for name, f := range hf.fields {
		if f.Hidden {
			onShown[name] = true
		}
		if _, ok := st.Files[name]; ok && f.Widget == "file" && onShown[name] {
			probe[name] = "file"
		}
	}
	root := map[string]any{}
	for k, v := range hf.schema {
		root[k] = v
	}
	if req, ok := hf.schema["required"].([]any); ok {
		var keep []any
		for _, x := range req {
			if name, _ := x.(string); onShown[name] {
				keep = append(keep, x)
			}
		}
		root["required"] = keep
		if len(keep) == 0 {
			delete(root, "required")
		}
	}
	if len(hf.types) > 0 {
		root["types"] = hf.types
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return "This form cannot take answers right now."
	}
	body, _ := json.Marshal(probe)
	msgs := schemacheck.New("https://schemas.taskiem.dev/forms/host.json", raw).Validate(body)
	if len(msgs) == 0 {
		return ""
	}
	m := msgs[0]
	loc, why, _ := strings.Cut(m, ": ")
	name := strings.TrimPrefix(loc, "/")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name = name[:i]
	}
	if f, ok := hf.fields[name]; ok {
		if f.Hidden {
			return "This form's link is missing something it needs. Please ask for the link again."
		}
		return "Please check " + f.Label + ": " + why + "."
	}
	return "Please check your answers: " + why + "."
}

// formAfter shows what follows a submission (forms 6.6).
func (s *Server) formAfter(w http.ResponseWriter, r *http.Request, hf *hform, ref, key string, updates []updateLink) {
	d := hdoneData{pageData: pageData{Title: "Thank you", Lang: "en", Look: s.lookFor(r.Context(), hf.tenant, hf.business)}, Business: hf.business, Reference: ref,
		Headline: "Thank you", Embed: hf.embed, Origins: strings.Join(hf.origins, " "), Updates: updates}
	statusURL := "/s/" + url.PathEscape(ref) + "?k=" + url.QueryEscape(key)
	if hf.req != nil {
		d.Message = "Your answers have been sent to " + hf.business + "."
		s.renderPrivate(w, r, http.StatusOK, "formdone.html", d)
		return
	}
	after := hf.pub.Trigger.After
	if after.Status {
		d.StatusURL = statusURL
	}
	switch after.Mode {
	case "redirect":
		if target, ok := safeRedirect(after.URL, hf.origins); ok && !hf.embed && len(updates) == 0 {
			// The URL is the form's own config, on one of the organisation's
			// verified websites: never one the request names.
			w.Header().Set("Location", target) //nolint:gosec // G710: the form's own after.url, only on a verified website (TestHostedFormAfter)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
	case "result":
		d.Headline = "Thank you"
		d.Message = "Just a moment while we check your answers…"
		d.Refresh = "2; url=" + hf.done + "/done/" + url.PathEscape(ref) + "?k=" + url.QueryEscape(key)
		d.StatusURL = ""
		if len(updates) > 0 {
			// The page stays, so the updates can be asked for; the answer
			// is a tap away.
			d.Message, d.Refresh = "Your answers have been sent.", ""
			d.StatusURL = hf.done + "/done/" + url.PathEscape(ref) + "?k=" + url.QueryEscape(key)
		}
		s.renderPrivate(w, r, http.StatusOK, "formdone.html", d)
		return
	}
	d.Message = formMessage(after.Message, hf.origins)
	s.renderPrivate(w, r, http.StatusOK, "formdone.html", d)
}

// serveFormDone is the result page: it waits up to 30 seconds for the
// workflow's message, refreshing every 2, then falls back to the
// thank-you message and the status link.
func (s *Server) serveFormDone(w http.ResponseWriter, r *http.Request, id, ref string, embed bool) {
	key := r.URL.Query().Get("k")
	if !formRefRe.MatchString(ref) || key == "" || len(key) > 64 {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	pub, ok, err := s.publicForm(r.Context(), id)
	if err != nil {
		s.tryLater(w, r, "form", err)
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	st, ok, err := s.Forms.Status(r.Context(), ref, storefront.StatusHash(key))
	if err != nil {
		s.tryLater(w, r, "form status", err)
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	var origins []string
	if embed {
		origins, err = s.embedOrigins(r.Context(), pub.Tenant)
		if err != nil {
			origins = nil
		}
		httpsec.SetPublicEmbed(w.Header(), origins)
	} else if strings.Contains(pub.Trigger.After.Message, "](") {
		origins, _ = s.embedOrigins(r.Context(), pub.Tenant)
	}
	d := hdoneData{pageData: pageData{Title: "Thank you", Lang: "en", Look: s.lookFor(r.Context(), pub.Tenant, pub.Business)}, Business: pub.Business, Reference: ref,
		Headline: "Thank you", Embed: embed, Origins: strings.Join(origins, " ")}
	statusURL := "/s/" + url.PathEscape(ref) + "?k=" + url.QueryEscape(key)
	switch {
	case st.Message != "":
		d.Message = st.Message
	case st.Status == "refused":
		d.Headline = "Not accepted"
		d.Message = "Your answers could not be accepted. Please contact " + pub.Business + "."
	case s.now().Sub(st.Created) < 30*time.Second:
		d.Message = "Just a moment while we check your answers…"
		d.Refresh = "2"
	default:
		d.Message = formMessage(pub.Trigger.After.Message, origins)
		d.StatusURL = statusURL
	}
	s.renderPrivate(w, r, http.StatusOK, "formdone.html", d)
}

// serveFormStatus is a status link, /s/{reference}?k=: the submission's
// state in plain words and the workflow's latest message, nothing else.
func (s *Server) serveFormStatus(w http.ResponseWriter, r *http.Request, ref string) {
	key := r.URL.Query().Get("k")
	if !formRefRe.MatchString(ref) || key == "" || len(key) > 64 {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	st, ok, err := s.Forms.Status(r.Context(), ref, storefront.StatusHash(key))
	if err != nil {
		s.tryLater(w, r, "form status", err)
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	d := hdoneData{pageData: pageData{Title: "Your form", Lang: "en"}, Business: "Taskiem", Reference: ref, Headline: "Your form"}
	switch st.Status {
	case "refused":
		d.Lines = []string{"Your answers could not be accepted. Please contact the organisation that gave you the form."}
	case "started", "answered":
		d.Lines = []string{"Your answers have been received and are being handled."}
	default:
		d.Lines = []string{"Your answers have arrived. They will be handled shortly."}
	}
	if !st.Created.IsZero() {
		d.Lines = append(d.Lines, "Sent "+st.Created.In(storefront.Lagos).Format("2 January 2006 at 3:04pm")+".")
	}
	if st.Message != "" {
		d.Message = st.Message
	}
	s.renderPrivate(w, r, http.StatusOK, "formdone.html", d)
}

// formReport takes a report of a form (POST /report). For now it is
// logged for the operators; the operator console's queue comes later.
func (s *Server) formReport(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !s.formLimiter().Allow(s.ipKey(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	form := r.PostForm.Get("form")
	if len(form) > 60 {
		form = form[:60]
	}
	// A hosted form's report ("f:<public id>") reaches the operator console
	// (report.go); a form step's ("q:<request>") is only logged.
	if id, ok := strings.CutPrefix(form, "f:"); ok {
		if r.PostForm.Get("reason") == "" {
			r.PostForm.Set("reason", "other")
		}
		if _, err := s.takeReport(r, "form", id); err != nil && !errors.Is(err, errReportInvalid) {
			s.Logger.Error("form host: a report", "form", form, "err", err)
		}
	} else {
		s.Logger.Warn("form host: a form step was reported", "form", form)
	}
	w.WriteHeader(http.StatusNoContent)
}

// safeRedirect is after.url when it is https on one of the organisation's
// verified websites (forms 6.6): never anywhere else, so a form cannot be
// made an open redirect.
func safeRedirect(raw string, origins []string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", false
	}
	origin := "https://" + strings.ToLower(u.Host)
	for _, o := range origins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return u.String(), true
		}
	}
	return "", false
}

package publicweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// A family's page, /s/{token} on the form host (decision 0046 point 8;
// docs/spec/schools.md 10 and 11.3; docs/schools.md#the-family-page): a
// school's family reaches it from a link in any message, with no account
// or password. It shows each child (switchable, the choice in the
// address), this term's fees line by line, what was paid and each
// receipt, the balance, family credit, and each child's own account
// number; a parent can say "I have paid" some other way, which is
// recorded as a claim for the bursar and never as a payment, and ask for
// a payment plan. An expired link says "ask the school for a new link"
// and offers to send one to the guardians' numbers on file, never "not
// found".
//
// The public role reads the record through narrow functions
// (taskiem_public_school_family_*), none of which answer a name: the
// children's names, the account names and the links of invoice pages and
// receipts are in the link's sealed view, opened with the token itself.
// What a parent sends goes to the api role through an inbox
// (taskiem_public_school_family_ask), sealed under the inbox key.

// FamilyLink is a link's row: the family, when the link expires, and the
// sealed view.
type FamilyLink struct {
	Tenant, Family, Link uuid.UUID
	Business             string
	ExpiresAt            time.Time
	View                 []byte
	SiblingsShare        bool
	LinkDays             int
}

// FamilyStudent is one of the family's students as the record gives
// them to the page (no name: the view has it).
type FamilyStudent struct {
	ID              uuid.UUID
	AdmissionNumber string
	Class           string
	Status          string
	CreditKobo      int64
	AccountNumber   string
	BankName        string
}

// FamilyInvoice is a student's term invoice as the page shows it.
type FamilyInvoice struct {
	ID, StudentID, TermID uuid.UUID
	Term                  string
	Current               bool
	Number, Status        string
	Lines                 []invoicing.Line
	TotalKobo, PaidKobo   int64
	DueOn                 *time.Time
}

// FamilyReceipt is a receipt as the page lists it.
type FamilyReceipt struct {
	ID, StudentID, InvoiceID uuid.UUID
	Number                   string
	AmountKobo               int64
	PaidAt                   time.Time
	Voided                   bool
}

// FamilyRequest is a claim or a plan request as the page shows it back
// to the family.
type FamilyRequest struct {
	Kind               string // claim or plan
	ID, StudentID      uuid.UUID
	AmountKobo         int64
	PaidOn             *time.Time
	How, Status        string
	Instalments        []schools.Instalment
	DecidedInstalments []schools.Instalment
	DecisionNote       string
	CreatedAt          time.Time
	DecidedAt          *time.Time
}

// Families reads a family's page and leaves what its forms send.
type Families interface {
	// Link finds a link by its token's hash, expired or not; ok is false
	// for a token nobody minted.
	Link(ctx context.Context, tokenHash []byte) (FamilyLink, bool, error)
	Students(ctx context.Context, tokenHash []byte) ([]FamilyStudent, error)
	Invoices(ctx context.Context, tokenHash []byte) ([]FamilyInvoice, error)
	Receipts(ctx context.Context, tokenHash []byte) ([]FamilyReceipt, error)
	Requests(ctx context.Context, tokenHash []byte) ([]FamilyRequest, error)
	// Ask leaves a sealed ask for the api role: "ok", "expired",
	// "pending" or "limited", "" for an unknown token.
	Ask(ctx context.Context, tokenHash []byte, kind string, student *uuid.UUID, idem, payload []byte) (string, error)
}

// DBFamilies is Families over the public role's functions.
type DBFamilies struct{ Pool *pgxpool.Pool }

// Link implements Families.
func (d DBFamilies) Link(ctx context.Context, h []byte) (FamilyLink, bool, error) {
	var l FamilyLink
	var business *string
	err := d.Pool.QueryRow(ctx, `SELECT f.tenant_id, f.family_id, f.link_id, f.expires_at, f.view, f.siblings_share, f.link_days, taskiem_public_brand(f.tenant_id)->>'name'
		FROM taskiem_public_school_family($1) f`, h).
		Scan(&l.Tenant, &l.Family, &l.Link, &l.ExpiresAt, &l.View, &l.SiblingsShare, &l.LinkDays, &business)
	if errors.Is(err, pgx.ErrNoRows) {
		return FamilyLink{}, false, nil
	}
	if business != nil {
		l.Business = *business
	}
	return l, err == nil, err
}

// Students implements Families.
func (d DBFamilies) Students(ctx context.Context, h []byte) ([]FamilyStudent, error) {
	rows, err := d.Pool.Query(ctx, `SELECT student_id, admission_number, class, status, credit_kobo, COALESCE(account_number, ''), COALESCE(bank_name, '') FROM taskiem_public_school_family_students($1)`, h)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FamilyStudent, error) {
		var s FamilyStudent
		return s, r.Scan(&s.ID, &s.AdmissionNumber, &s.Class, &s.Status, &s.CreditKobo, &s.AccountNumber, &s.BankName)
	})
}

// Invoices implements Families.
func (d DBFamilies) Invoices(ctx context.Context, h []byte) ([]FamilyInvoice, error) {
	rows, err := d.Pool.Query(ctx, `SELECT invoice_id, student_id, term_id, term, current, number, status, lines, total_kobo, paid_kobo, due_on FROM taskiem_public_school_family_invoices($1)`, h)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FamilyInvoice, error) {
		var x FamilyInvoice
		var lines []byte
		if err := r.Scan(&x.ID, &x.StudentID, &x.TermID, &x.Term, &x.Current, &x.Number, &x.Status, &lines, &x.TotalKobo, &x.PaidKobo, &x.DueOn); err != nil {
			return x, err
		}
		return x, json.Unmarshal(lines, &x.Lines)
	})
}

// Receipts implements Families.
func (d DBFamilies) Receipts(ctx context.Context, h []byte) ([]FamilyReceipt, error) {
	rows, err := d.Pool.Query(ctx, `SELECT receipt_id, student_id, invoice_id, number, amount_kobo, paid_at, voided FROM taskiem_public_school_family_receipts($1)`, h)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FamilyReceipt, error) {
		var x FamilyReceipt
		return x, r.Scan(&x.ID, &x.StudentID, &x.InvoiceID, &x.Number, &x.AmountKobo, &x.PaidAt, &x.Voided)
	})
}

// Requests implements Families.
func (d DBFamilies) Requests(ctx context.Context, h []byte) ([]FamilyRequest, error) {
	rows, err := d.Pool.Query(ctx, `SELECT kind, id, student_id, COALESCE(amount_kobo, 0), paid_on, COALESCE(how, ''), status, instalments, decided_instalments, COALESCE(decision_note, ''), created_at, decided_at
		FROM taskiem_public_school_family_requests($1)`, h)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FamilyRequest, error) {
		var x FamilyRequest
		var ins, decided []byte
		if err := r.Scan(&x.Kind, &x.ID, &x.StudentID, &x.AmountKobo, &x.PaidOn, &x.How, &x.Status, &ins, &decided, &x.DecisionNote, &x.CreatedAt, &x.DecidedAt); err != nil {
			return x, err
		}
		if len(ins) > 0 {
			_ = json.Unmarshal(ins, &x.Instalments)
		}
		if len(decided) > 0 {
			_ = json.Unmarshal(decided, &x.DecidedInstalments)
		}
		return x, nil
	})
}

// Ask implements Families.
func (d DBFamilies) Ask(ctx context.Context, h []byte, kind string, student *uuid.UUID, idem, payload []byte) (string, error) {
	var out *string
	if err := d.Pool.QueryRow(ctx, `SELECT taskiem_public_school_family_ask($1, $2, $3, $4, $5)`, h, kind, student, idem, payload).Scan(&out); err != nil {
		return "", err
	}
	if out == nil {
		return "", nil
	}
	return *out, nil
}

// --- the page ---

type familyChild struct {
	ID, Name, Admission, Class, URL string
	Current                         bool
	Status                          string // "" for active, else withdrawn or graduated
}

type familyInvoiceView struct {
	ID, Term, Number, Status string
	Current                  bool
	Lines                    []invoiceLine
	Total, Paid, Due, DueOn  string
	Open                     bool
	// PayLink is the invoice's own page, where a one-time account number
	// can be asked for: given where the child has no permanent account.
	PayLink  string
	Receipts []familyReceiptView
}

type familyReceiptView struct {
	Number, Date, Amount, State, Link string
	Voided                            bool
}

type familyRequestView struct {
	Line, State, Note string
	Open              bool
	Instalments       []string
}

type familyData struct {
	pageData
	Business string
	Home     string
	Action   string // the page's own address, for forms and child links
	Children []familyChild
	Child    *familyChild
	// Account: the child's own account number, when issued.
	Account      *accountView
	AccountNote  string
	Credit       string
	CreditNote   string
	Balance      string
	BalanceNote  string
	Invoices     []familyInvoiceView
	PastOpen     bool
	Receipts     []familyReceiptView
	Claims       []familyRequestView
	Plans        []familyRequestView
	Notice       string
	Error        string
	ErrorFor     string // paid or plan
	Idem         string
	Today        string
	Hows         []howOption
	PlanRows     []int
	LinkDays     int
	Expires      string
	CopyScript   string
	ClaimLimited bool
}

type howOption struct{ Value, Label string }

type familyExpiredData struct {
	pageData
	Business string
	Home     string
	Message  string
	// Action is the "Send me a new link" form's, "" when the link is
	// unknown (there is no family to send one to).
	Action string
	Notice string
	Idem   string
}

// familyNotice says what a form did, by the done= code the page is sent
// back with.
var familyNotice = map[string]string{
	"paid": "Thank you. The school will look for your payment and match it to the fees; this page shows it below once it is recorded, and your receipt when the payment is found. Nothing changes on the fees until then.",
	"plan": "Thank you. The school will look at your request and tell you what it decides.",
	"link": "If your number is on file, a new link is on its way to it.",
}

var familyErrors = map[string]string{
	"amount":      "Type the amount in naira, such as 25000 or 25,000.50.",
	"date":        "Give the date you paid (within the last year).",
	"how":         "Say how you paid.",
	"instalments": "Give one to six instalments, each an amount and a date from today on, in date order.",
	"message":     "The message is too long (up to 500 characters).",
	"limited":     "Too many were sent from this page in the last hour. Please try again later, or contact the school.",
	"pending":     "The school has not decided your last request yet.",
	"expired":     "This link has expired. Ask the school for a new one.",
	"child":       "Choose a child first.",
}

// serveFamily serves /s/{token} and its forms. It reports false, having
// written nothing, when the token is not a family link's form, so the
// form status page that shares the path can answer.
func (s *Server) serveFamily(w http.ResponseWriter, r *http.Request, token, sub string) bool {
	if s.Families == nil || !invoiceTokenRe.MatchString(token) {
		return false
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	switch sub {
	case "":
		if !allowed(w, r, http.MethodGet, http.MethodHead) {
			return true
		}
		s.serveFamilyPage(w, r, token)
	case "paid", "plan", "link":
		if !allowed(w, r, http.MethodPost) {
			return true
		}
		s.serveFamilyAsk(w, r, token, sub)
	default:
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
	}
	return true
}

// familyNoLink is the page for a token nobody minted, or one that
// expired: the same words, with the offer of a new link only where there
// is a family to send it to.
func (s *Server) familyNoLink(w http.ResponseWriter, r *http.Request, token string, l *FamilyLink, notice string) {
	d := familyExpiredData{pageData: pageData{Title: "Ask the school for a new link", Lang: "en"}, Business: "the school",
		Message: "This link no longer works. Links to a family's fees page work for a few days; ask the school for a new link."}
	if l != nil {
		business := familyBusiness(*l)
		d.Business, d.Look = business, s.lookFor(r.Context(), l.Tenant, business)
		d.Message = fmt.Sprintf("This link has expired: %s's links to a family's fees page work for %s. Ask the school for a new link, or have one sent to the phone number the school has for you.", business, days(l.LinkDays))
		d.Action = "/s/" + token + "/link"
		d.Idem = newIdem()
	}
	d.Notice = notice
	s.renderPrivate(w, r, http.StatusGone, "familyexpired.html", d)
}

func days(n int) string {
	if n == 1 {
		return "one day"
	}
	return strconv.Itoa(n) + " days"
}

func familyBusiness(l FamilyLink) string {
	if l.Business != "" {
		return l.Business
	}
	return "the school"
}

func newIdem() string {
	return strings.TrimRight(hex.EncodeToString([]byte(uuid.New().String()))[:40], "=")
}

// familyIdem is the inbox's idempotency key for a form's idem field and
// kind: a double tap or a retry leaves one ask.
func familyIdem(token, kind, idem string) []byte {
	h := sha256.Sum256([]byte("taskiem family ask " + token + "\x00" + kind + "\x00" + idem))
	return h[:]
}

// serveFamilyPage draws the page.
func (s *Server) serveFamilyPage(w http.ResponseWriter, r *http.Request, token string) {
	ctx := r.Context()
	hash := schools.FamilyTokenHash(token)
	l, ok, err := s.Families.Link(ctx, hash)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	if !ok {
		s.familyNoLink(w, r, token, nil, "")
		return
	}
	if !l.ExpiresAt.After(s.now()) {
		s.familyNoLink(w, r, token, &l, familyNotice[r.URL.Query().Get("done")])
		return
	}
	view, err := schools.OpenFamilyView(token, l.Link, l.View)
	if err != nil {
		// A view that does not open is a token that is not this link's.
		s.familyNoLink(w, r, token, nil, "")
		return
	}
	students, err := s.Families.Students(ctx, hash)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	invoices, err := s.Families.Invoices(ctx, hash)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	receipts, err := s.Families.Receipts(ctx, hash)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	requests, err := s.Families.Requests(ctx, hash)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	business := familyBusiness(l)
	q := r.URL.Query()
	d := familyData{pageData: pageData{Title: "School fees", Lang: "en", Look: s.lookFor(ctx, l.Tenant, business)}, Business: business,
		Action: "/s/" + token, Idem: newIdem(), Today: s.now().In(lagos).Format(time.DateOnly), LinkDays: l.LinkDays,
		Expires: l.ExpiresAt.In(lagos).Format("2 January 2006 at 3:04pm"), PlanRows: []int{1, 2, 3, 4, 5, 6}, CopyScript: assetNames["copy.js"]}
	for _, h := range schools.ClaimHows {
		d.Hows = append(d.Hows, howOption{Value: h, Label: schools.ClaimHowWords[h]})
	}
	d.Notice = familyNotice[q.Get("done")]
	if e := q.Get("err"); e != "" {
		d.Error, d.ErrorFor = familyErrors[e], q.Get("for")
		if d.Error == "" {
			d.Error = "This could not be sent. Please try again."
		}
	}
	chosen := q.Get("child")
	var child *FamilyStudent
	for i := range students {
		st := &students[i]
		name := view.Names[st.ID.String()]
		if name == "" {
			name = "Student " + st.AdmissionNumber
		}
		c := familyChild{ID: st.ID.String(), Name: name, Admission: st.AdmissionNumber, Class: st.Class, URL: "/s/" + token + "?child=" + st.ID.String()}
		if st.Status != "active" {
			c.Status = st.Status
		}
		if st.ID.String() == chosen || (chosen == "" && child == nil) {
			c.Current, child = true, st
		}
		d.Children = append(d.Children, c)
	}
	for i := range d.Children {
		if d.Children[i].Current {
			d.Child = &d.Children[i]
		}
	}
	if child != nil {
		s.fillChild(&d, token, view, *child, l, invoices, receipts, requests)
	}
	s.renderPrivate(w, r, http.StatusOK, "family.html", d)
}

// fillChild fills the chosen child's fees, payments, receipts, account
// and requests.
func (s *Server) fillChild(d *familyData, token string, view schools.FamilyView, child FamilyStudent, l FamilyLink, invoices []FamilyInvoice, receipts []FamilyReceipt, requests []FamilyRequest) {
	if child.AccountNumber != "" {
		d.Account = &accountView{Number: child.AccountNumber, Bank: child.BankName, Name: view.AccountNames[child.ID.String()]}
		d.AccountNote = "Any transfer to this number lands on " + d.Child.Name + "'s fees by itself, oldest term first, from any bank or app."
	} else {
		d.AccountNote = d.Child.Name + " has no account number yet. Pay from an invoice below, or ask " + d.Business + " how to pay."
	}
	if child.CreditKobo > 0 {
		d.Credit = invoicing.Naira(child.CreditKobo)
		d.CreditNote = "Money paid that no invoice counts yet. It counts towards the next invoice."
		if l.SiblingsShare {
			d.CreditNote = "Money paid that no invoice counts yet. It counts towards the next invoice, and towards brothers' and sisters' fees."
		}
	}
	byInvoice := map[uuid.UUID][]familyReceiptView{}
	for _, rc := range receipts {
		if rc.StudentID != child.ID {
			continue
		}
		v := familyReceiptView{Number: rc.Number, Date: rc.PaidAt.In(lagos).Format("2 Jan 2006"), Amount: invoicing.Naira(rc.AmountKobo), State: "Valid", Link: view.ReceiptLinks[rc.ID.String()], Voided: rc.Voided}
		if rc.Voided {
			v.State = "Voided"
		}
		d.Receipts = append(d.Receipts, v)
		byInvoice[rc.InvoiceID] = append(byInvoice[rc.InvoiceID], v)
	}
	var balance int64
	for _, x := range invoices {
		if x.StudentID != child.ID {
			continue
		}
		v := familyInvoiceView{ID: x.ID.String(), Term: x.Term, Current: x.Current, Number: x.Number, Status: invoiceWords[x.Status], Total: invoicing.Naira(x.TotalKobo), Receipts: byInvoice[x.ID]}
		for _, ln := range x.Lines {
			v.Lines = append(v.Lines, invoiceLineView(ln))
		}
		if x.PaidKobo > 0 {
			v.Paid = invoicing.Naira(x.PaidKobo)
		}
		switch x.Status {
		case "sent", "part_paid", "overdue":
			v.Open = true
			due := x.TotalKobo - x.PaidKobo
			if due > 0 {
				v.Due = invoicing.Naira(due)
				balance += due
			}
			if x.DueOn != nil {
				v.DueOn = "Due " + x.DueOn.Format("2 Jan 2006")
			}
			if child.AccountNumber == "" {
				v.PayLink = view.InvoiceLinks[x.ID.String()]
			}
			if !x.Current {
				d.PastOpen = true
			}
		case "paid":
			v.Status = "Paid in full"
		}
		d.Invoices = append(d.Invoices, v)
	}
	sort.SliceStable(d.Invoices, func(i, j int) bool { return d.Invoices[i].Current && !d.Invoices[j].Current })
	d.Balance = invoicing.Naira(balance)
	switch {
	case balance == 0 && len(d.Invoices) > 0:
		d.BalanceNote = "Nothing is owed for " + d.Child.Name + "."
	case len(d.Invoices) == 0:
		d.Balance, d.BalanceNote = "", "No fees have been sent for "+d.Child.Name+" yet."
	case d.PastOpen:
		d.BalanceNote = "This includes fees from an earlier term still open."
	}
	for _, rq := range requests {
		if rq.StudentID != child.ID {
			continue
		}
		switch rq.Kind {
		case "claim":
			v := familyRequestView{Line: invoicing.Naira(rq.AmountKobo) + " by " + strings.ToLower(schools.ClaimHowWords[rq.How]), Open: rq.Status == "open"}
			if rq.PaidOn != nil {
				v.Line += " on " + rq.PaidOn.Format("2 Jan 2006")
			}
			switch rq.Status {
			case "open":
				v.State = "The school is looking for it"
			case "matched":
				v.State = "Found and counted"
			default:
				v.State = "Not found by the school"
			}
			d.Claims = append(d.Claims, v)
		case "plan":
			v := familyRequestView{Line: "Asked on " + rq.CreatedAt.In(lagos).Format("2 Jan 2006") + ": " + invoicing.Naira(schools.TotalKobo(rq.Instalments)) + " in " + plural(len(rq.Instalments), "instalment"), Open: rq.Status == "requested", Note: rq.DecisionNote}
			list := rq.Instalments
			switch rq.Status {
			case "requested":
				v.State = "Waiting for the school"
			case "approved":
				v.State, list = "Approved", rq.DecidedInstalments
			default:
				v.State = "Declined"
			}
			for i, in := range list {
				if t, err := time.Parse(time.DateOnly, in.DueOn); err == nil {
					v.Instalments = append(v.Instalments, fmt.Sprintf("%d. %s by %s", i+1, invoicing.Naira(in.AmountKobo), t.Format("2 Jan 2006")))
				}
			}
			d.Plans = append(d.Plans, v)
		}
	}
	_ = token
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// serveFamilyAsk takes the page's forms: POST /s/{token}/paid, /plan and
// /link. What passes the checks is sealed for the api role's inbox; the
// parent is sent back to the page with what happened.
func (s *Server) serveFamilyAsk(w http.ResponseWriter, r *http.Request, token, kind string) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "This could not be read. Please try again.", http.StatusBadRequest)
		return
	}
	if len(s.InboxKey) == 0 {
		s.formNotice(w, r, http.StatusServiceUnavailable, "Not open yet", uuid.Nil, "", "This page cannot take messages yet. Please contact the school.")
		return
	}
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		s.formNotice(w, r, http.StatusTooManyRequests, "Too many requests", uuid.Nil, "", "Too many requests from this connection. Please wait a few minutes and try again.")
		return
	}
	hash := schools.FamilyTokenHash(token)
	child := strings.TrimSpace(r.PostForm.Get("child"))
	back := func(code, done string) {
		q := url.Values{}
		if child != "" {
			q.Set("child", child)
		}
		if code != "" {
			q.Set("err", code)
			q.Set("for", kind)
		}
		if done != "" {
			q.Set("done", done)
		}
		http.Redirect(w, r, "/s/"+token+"?"+q.Encode(), http.StatusSeeOther) //nolint:gosec // G710: a relative path; the token was checked against invoiceTokenRe
	}
	idem := strings.TrimSpace(r.PostForm.Get("idem"))
	if idem == "" || len(idem) > 64 {
		back("", "")
		return
	}
	ask := schools.FamilyAsk{Kind: kind}
	var student *uuid.UUID
	if kind != "link" {
		id, err := uuid.Parse(child)
		if err != nil {
			back("child", "")
			return
		}
		student, ask.StudentID = &id, id
	}
	switch kind {
	case "paid":
		ask.Kind = "claim"
		amount, ok := parseNaira(r.PostForm.Get("amount"))
		if !ok {
			back("amount", "")
			return
		}
		ask.AmountKobo = amount
		if _, err := time.Parse(time.DateOnly, r.PostForm.Get("date")); err != nil {
			back("date", "")
			return
		}
		ask.PaidOn = r.PostForm.Get("date")
		ask.How = r.PostForm.Get("how")
		if _, ok := schools.ClaimHowWords[ask.How]; !ok {
			back("how", "")
			return
		}
		ask.Reference = clip(r.PostForm.Get("reference"), 80)
		ask.Note = clip(r.PostForm.Get("note"), 500)
	case "plan":
		for i := 1; i <= 6; i++ {
			a, dt := strings.TrimSpace(r.PostForm.Get("amount"+strconv.Itoa(i))), strings.TrimSpace(r.PostForm.Get("date"+strconv.Itoa(i)))
			if a == "" && dt == "" {
				continue
			}
			kobo, ok := parseNaira(a)
			if _, err := time.Parse(time.DateOnly, dt); !ok || err != nil {
				back("instalments", "")
				return
			}
			ask.Instalments = append(ask.Instalments, schools.Instalment{AmountKobo: kobo, DueOn: dt})
		}
		if _, err := schools.CheckInstalments(ask.Instalments, s.now()); err != nil {
			back("instalments", "")
			return
		}
		ask.Message = strings.TrimSpace(r.PostForm.Get("message"))
		if len(ask.Message) > 500 {
			back("message", "")
			return
		}
	}
	key := familyIdem(token, ask.Kind, idem)
	sealed, err := schools.SealFamilyAsk(s.InboxKey, key, ask)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	// The idem travels with the payload, so the api role needs nothing
	// of the inbox row but the payload.
	payload := append(append([]byte{}, key...), sealed...)
	res, err := s.Families.Ask(ctx, hash, ask.Kind, student, key, payload)
	if err != nil {
		s.tryLater(w, r, "family page", err)
		return
	}
	switch res {
	case "ok":
		back("", kind)
	case "":
		s.familyNoLink(w, r, token, nil, "")
	case "expired":
		// Sent back to the page, which says the link expired and offers
		// a new one.
		back("", "")
	default:
		if kind == "link" {
			// The page is expired: say nothing of the limit, as of the
			// number; the same words either way.
			back("", "link")
			return
		}
		back(res, "")
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

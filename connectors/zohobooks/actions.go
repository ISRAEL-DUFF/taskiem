package zohobooks

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
)

// The records as Books answers them, with the fields the actions read.

type contact struct {
	ID         zid         `json:"contact_id"`
	Name       string      `json:"contact_name"`
	Type       string      `json:"contact_type"`
	Email      string      `json:"email"`
	Phone      string      `json:"phone"`
	Receivable json.Number `json:"outstanding_receivable_amount"`
	Payable    json.Number `json:"outstanding_payable_amount"`
}

func (c contact) out() map[string]any {
	o := map[string]any{"contact_id": string(c.ID), "contact_name": c.Name, "contact_type": c.Type}
	if c.Email != "" {
		o["email"] = c.Email
	}
	if c.Phone != "" {
		o["phone"] = c.Phone
	}
	if c.Type == "vendor" {
		o["outstanding_kobo"] = kobo(c.Payable)
	} else {
		o["outstanding_kobo"] = kobo(c.Receivable)
	}
	return o
}

type invoice struct {
	ID        zid         `json:"invoice_id"`
	Number    string      `json:"invoice_number"`
	Reference string      `json:"reference_number"`
	Customer  zid         `json:"customer_id"`
	Status    string      `json:"status"`
	Date      string      `json:"date"`
	DueDate   string      `json:"due_date"`
	Currency  string      `json:"currency_code"`
	Total     json.Number `json:"total"`
	TaxTotal  json.Number `json:"tax_total"`
	Balance   json.Number `json:"balance"`
	Created   string      `json:"created_time"`
	Modified  string      `json:"last_modified_time"`
}

func (i invoice) out() map[string]any {
	o := map[string]any{"invoice_id": string(i.ID), "invoice_number": i.Number, "reference_number": i.Reference, "customer_id": string(i.Customer),
		"status": i.Status, "date": i.Date, "total_kobo": kobo(i.Total), "balance_kobo": kobo(i.Balance), "paid": i.Status == "paid"}
	for k, v := range map[string]string{"due_date": i.DueDate, "currency": i.Currency, "modified_at": i.Modified} {
		if v != "" {
			o[k] = v
		}
	}
	if i.TaxTotal != "" {
		o["tax_kobo"] = kobo(i.TaxTotal)
	}
	return o
}

type payment struct {
	ID        zid         `json:"payment_id"`
	Customer  zid         `json:"customer_id"`
	Amount    json.Number `json:"amount"`
	Reference string      `json:"reference_number"`
	Date      string      `json:"date"`
	Currency  string      `json:"currency_code"`
	Created   string      `json:"created_time"`
	Invoices  []struct {
		ID zid `json:"invoice_id"`
	} `json:"invoices"`
}

func paymentOut(p payment, created bool) map[string]any {
	return map[string]any{"payment_id": string(p.ID), "created": created, "customer_id": string(p.Customer), "amount_kobo": kobo(p.Amount)}
}

// --- organisations ---

func (c *client) listOrganizations(ctx context.Context, req connector.Request) (connector.Response, error) {
	s := c.open(req)
	orgs, err := s.organizations(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, map[string]any{"organization_id": string(o.ID), "name": o.Name, "currency_code": o.Currency, "country": o.Country, "is_default_org": o.Default})
	}
	res := map[string]any{"organizations": out}
	if id := s.Creds["organization_id"]; id != "" {
		res["current"] = id
	}
	return connector.Response{Output: res}, nil
}

// --- contacts ---

// contactBy is the first contact a filter finds.
func (s *session) contactBy(ctx context.Context, field, value, kind string) (*contact, error) {
	q := url.Values{field: {value}}
	if kind != "" {
		q.Set("contact_type", kind)
	}
	var resp struct {
		Contacts []contact `json:"contacts"`
	}
	if err := s.do(ctx, "find_contact", http.MethodGet, "/contacts", q, nil, &resp, true); err != nil {
		return nil, err
	}
	for _, ct := range resp.Contacts {
		// Books' filters on name match loosely: the same name only.
		if field != "contact_name" || strings.EqualFold(ct.Name, value) {
			return &ct, nil
		}
	}
	return nil, nil
}

// findContact looks by email, then phone, then display name.
func (s *session) findContact(ctx context.Context, email, phone, name, kind string) (*contact, error) {
	for _, f := range [][2]string{{"email", email}, {"phone", phone}, {"contact_name", name}} {
		if f[1] == "" {
			continue
		}
		if ct, err := s.contactBy(ctx, f[0], f[1], kind); ct != nil || err != nil {
			return ct, err
		}
	}
	return nil, nil
}

func (c *client) findContact(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	email, phone, name := str(in, "email"), str(in, "phone"), str(in, "contact_name")
	if email == "" && phone == "" && name == "" {
		return connector.Response{}, fatalf("find_contact: give an email, a phone or a name")
	}
	found, err := c.open(req).findContact(ctx, email, phone, name, str(in, "contact_type"))
	if err != nil || found == nil {
		return connector.Response{Output: map[string]any{"found": false}}, err
	}
	return connector.Response{Output: map[string]any{"found": true, "contact": found.out()}}, nil
}

func (c *client) createContact(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	name := str(in, "contact_name")
	if name == "" {
		return connector.Response{}, fatalf("create_contact: contact_name is required")
	}
	kind := str(in, "contact_type")
	if kind == "" {
		kind = "customer"
	}
	s := c.open(req)
	found, err := s.findContact(ctx, str(in, "email"), str(in, "phone"), name, kind)
	if err != nil {
		return connector.Response{}, err
	}
	if found != nil {
		return connector.Response{Output: map[string]any{"contact_id": string(found.ID), "created": false, "contact": found.out()}}, nil
	}
	ct, err := s.addContact(ctx, in, name, kind)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"contact_id": string(ct.ID), "created": true, "contact": ct.out()}}, nil
}

func (s *session) addContact(ctx context.Context, in map[string]any, name, kind string) (*contact, error) {
	rec := map[string]any{"contact_name": name, "contact_type": kind}
	for k, f := range map[string]string{"company_name": "company_name", "notes": "notes"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	if e, p := str(in, "email"), str(in, "phone"); e != "" || p != "" {
		person := map[string]any{"is_primary_contact": true}
		if e != "" {
			person["email"] = e
		}
		if p != "" {
			person["phone"] = p
		}
		rec["contact_persons"] = []any{person}
	}
	var resp struct {
		Contact contact `json:"contact"`
	}
	if err := s.do(ctx, "create_contact", http.MethodPost, "/contacts", nil, rec, &resp, true); err != nil {
		return nil, err
	}
	return &resp.Contact, nil
}

// --- lines ---

// saleLines are invoice and sales receipt lines: rate is each line's
// amount, in the organisation's currency.
func saleLines(op string, in []map[string]any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(in))
	for i, l := range in {
		each, ok := integer(l, "amount_kobo")
		if !ok || each < 0 {
			return nil, fatalf("%s: line %d: amount_kobo must be a whole number of kobo, not negative", op, i+1)
		}
		qty := number(l, "quantity", 1)
		if qty <= 0 {
			return nil, fatalf("%s: line %d: quantity must be more than 0", op, i+1)
		}
		desc := str(l, "description")
		line := map[string]any{"name": truncate(desc, 100), "description": desc, "rate": amount(each), "quantity": qty}
		for k, f := range map[string]string{"item_id": "item_id", "tax_id": "tax_id"} {
			if v := str(l, f); v != "" {
				line[k] = v
			}
		}
		out = append(out, line)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// --- invoices ---

func (s *session) invoicesBy(ctx context.Context, op string, q url.Values) ([]invoice, error) {
	var resp struct {
		Invoices []invoice `json:"invoices"`
	}
	err := s.do(ctx, op, http.MethodGet, "/invoices", q, nil, &resp, true)
	return resp.Invoices, err
}

// invoiceByReference is the invoice carrying a reference exactly.
func (s *session) invoiceByReference(ctx context.Context, op, ref string) (*invoice, error) {
	list, err := s.invoicesBy(ctx, op, url.Values{"reference_number": {ref}})
	if err != nil {
		return nil, err
	}
	for _, i := range list {
		if i.Reference == ref {
			return &i, nil
		}
	}
	return nil, nil
}

func (c *client) createInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, cust := str(in, "reference_number"), str(in, "customer_id")
	if ref == "" || cust == "" {
		return connector.Response{}, fatalf("create_invoice: reference_number and customer_id are required")
	}
	lines, err := saleLines("create_invoice", objects(in, "lines"))
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_invoice: an invoice needs a line")
	}
	s := c.open(req)
	if found, err := s.invoiceByReference(ctx, "create_invoice", ref); err != nil || found != nil {
		if found != nil {
			return connector.Response{Output: map[string]any{"invoice_id": string(found.ID), "created": false, "invoice": found.out()}}, nil
		}
		return connector.Response{}, err
	}
	rec := map[string]any{"customer_id": cust, "reference_number": ref, "line_items": lines, "is_inclusive_tax": boolean(in, "is_inclusive_tax")}
	for k, f := range map[string]string{"date": "date", "due_date": "due_date", "notes": "notes"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	q := url.Values{}
	if n := str(in, "invoice_number"); n != "" {
		rec["invoice_number"] = n
		q.Set("ignore_auto_number_generation", "true")
	}
	var resp struct {
		Invoice invoice `json:"invoice"`
	}
	if err := s.do(ctx, "create_invoice", http.MethodPost, "/invoices", q, rec, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"invoice_id": string(resp.Invoice.ID), "created": true, "invoice": resp.Invoice.out()}}, nil
}

func (c *client) getInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "invoice_id")
	if id == "" {
		return connector.Response{}, fatalf("get_invoice: invoice_id is required")
	}
	var resp struct {
		Invoice invoice `json:"invoice"`
	}
	if err := c.open(req).do(ctx, "get_invoice", http.MethodGet, "/invoices/"+url.PathEscape(id), nil, nil, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: resp.Invoice.out()}, nil
}

func (c *client) findInvoices(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	q := url.Values{"per_page": {"200"}}
	for k, f := range map[string]string{"reference_number": "reference_number", "invoice_number": "invoice_number", "customer_id": "customer_id", "status": "status"} {
		if v := str(in, f); v != "" {
			q.Set(k, v)
		}
	}
	if t := str(in, "changed_since"); t != "" {
		at, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return connector.Response{}, fatalf("find_invoices: changed_since must be a date and time (RFC 3339)")
		}
		q.Set("last_modified_time", zohoTime(at))
	}
	list, err := c.open(req).invoicesBy(ctx, "find_invoices", q)
	if err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(list))
	for _, i := range list {
		out = append(out, i.out())
	}
	return connector.Response{Output: map[string]any{"invoices": out}}, nil
}

// --- payments ---

func (c *client) recordPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref := str(in, "reference")
	amt, ok := integer(in, "amount_kobo")
	if ref == "" || !ok || amt < 1 {
		return connector.Response{}, fatalf("record_payment: reference and an amount_kobo of at least 1 are required")
	}
	s := c.open(req)
	cust, inv := str(in, "customer_id"), str(in, "invoice_id")
	if inv != "" && cust == "" {
		var resp struct {
			Invoice invoice `json:"invoice"`
		}
		if err := s.do(ctx, "record_payment", http.MethodGet, "/invoices/"+url.PathEscape(inv), nil, nil, &resp, true); err != nil {
			return connector.Response{}, err
		}
		cust = string(resp.Invoice.Customer)
	}
	if cust == "" {
		return connector.Response{}, fatalf("record_payment: give the customer_id, or the invoice_id it pays")
	}
	// Recorded already, by an earlier try: the customer's payment with
	// this reference.
	var found struct {
		Payments []payment `json:"customerpayments"`
	}
	if err := s.do(ctx, "record_payment", http.MethodGet, "/customerpayments", url.Values{"customer_id": {cust}, "reference_number": {ref}}, nil, &found, true); err != nil {
		return connector.Response{}, err
	}
	for _, p := range found.Payments {
		if p.Reference == ref {
			return connector.Response{Output: paymentOut(p, false)}, nil
		}
	}
	date := str(in, "date")
	if date == "" {
		date = c.today()
	}
	mode := str(in, "payment_mode")
	if mode == "" {
		mode = "bank_transfer"
	}
	rec := map[string]any{"customer_id": cust, "payment_mode": mode, "amount": amount(amt), "date": date, "reference_number": ref, "invoices": []any{}}
	if inv != "" {
		rec["invoices"] = []any{map[string]any{"invoice_id": inv, "amount_applied": amount(amt)}}
	}
	for k, f := range map[string]string{"account_id": "account_id", "description": "description"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	var resp struct {
		Payment payment `json:"payment"`
	}
	if err := s.do(ctx, "record_payment", http.MethodPost, "/customerpayments", nil, rec, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: paymentOut(resp.Payment, true)}, nil
}

// --- sales, expenses and bills ---

// walkIn is the customer a sale without one is the walk-in customer's.
const walkIn = "Walk-in Customer"

func (c *client) createSalesReceipt(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	receiptNo := str(in, "receipt_number")
	if receiptNo == "" {
		return connector.Response{}, fatalf("create_sales_receipt: receipt_number is required")
	}
	lines, err := saleLines("create_sales_receipt", objects(in, "lines"))
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_sales_receipt: a sale needs a line")
	}
	s := c.open(req)
	type receipt struct {
		ID     zid         `json:"sales_receipt_id"`
		Number string      `json:"receipt_number"`
		Total  json.Number `json:"total"`
	}
	out := func(r receipt, created bool) map[string]any {
		return map[string]any{"sales_receipt_id": string(r.ID), "created": created, "receipt_number": r.Number, "total_kobo": kobo(r.Total)}
	}
	var found struct {
		Receipts []receipt `json:"salesreceipts"`
	}
	if err := s.do(ctx, "create_sales_receipt", http.MethodGet, "/salesreceipts", url.Values{"receipt_number": {receiptNo}}, nil, &found, true); err != nil {
		return connector.Response{}, err
	}
	for _, r := range found.Receipts {
		if r.Number == receiptNo {
			return connector.Response{Output: out(r, false)}, nil
		}
	}
	cust := str(in, "customer_id")
	if cust == "" {
		ct, err := s.contactBy(ctx, "contact_name", walkIn, "customer")
		if err != nil {
			return connector.Response{}, err
		}
		if ct == nil {
			if ct, err = s.addContact(ctx, map[string]any{}, walkIn, "customer"); err != nil {
				return connector.Response{}, err
			}
		}
		cust = string(ct.ID)
	}
	mode := str(in, "payment_mode")
	if mode == "" {
		mode = "cash"
	}
	rec := map[string]any{"customer_id": cust, "payment_mode": mode, "receipt_number": receiptNo, "line_items": lines}
	for k, f := range map[string]string{"date": "date", "notes": "notes"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	var resp struct {
		Receipt receipt `json:"salesreceipt"`
	}
	if err := s.do(ctx, "create_sales_receipt", http.MethodPost, "/salesreceipts", url.Values{"ignore_auto_number_generation": {"true"}}, rec, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(resp.Receipt, true)}, nil
}

func (c *client) createExpense(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ref, acct, from := str(in, "reference_number"), str(in, "account_id"), str(in, "paid_through_account_id")
	total, ok := integer(in, "amount_kobo")
	if ref == "" || acct == "" || from == "" || !ok || total < 1 {
		return connector.Response{}, fatalf("create_expense: reference_number, account_id, paid_through_account_id and amount_kobo are required")
	}
	vat, _ := integer(in, "vat_kobo")
	if vat < 0 || vat > total {
		return connector.Response{}, fatalf("create_expense: vat_kobo must be between 0 and amount_kobo")
	}
	s := c.open(req)
	type expense struct {
		ID        zid         `json:"expense_id"`
		Reference string      `json:"reference_number"`
		Total     json.Number `json:"total"`
	}
	out := func(e expense, created bool) map[string]any {
		return map[string]any{"expense_id": string(e.ID), "created": created, "total_kobo": kobo(e.Total)}
	}
	var found struct {
		Expenses []expense `json:"expenses"`
	}
	if err := s.do(ctx, "create_expense", http.MethodGet, "/expenses", url.Values{"reference_number": {ref}}, nil, &found, true); err != nil {
		return connector.Response{}, err
	}
	for _, e := range found.Expenses {
		if e.Reference == ref {
			return connector.Response{Output: out(e, false)}, nil
		}
	}
	date := str(in, "date")
	if date == "" {
		date = c.today()
	}
	desc := str(in, "description")
	rec := map[string]any{"account_id": acct, "paid_through_account_id": from, "date": date, "amount": amount(total), "reference_number": ref}
	if desc != "" {
		rec["description"] = desc
	}
	if t := str(in, "tax_id"); t != "" {
		rec["tax_id"], rec["is_inclusive_tax"] = t, true
	}
	if v := str(in, "vendor_id"); v != "" {
		rec["vendor_id"] = v
	}
	if vat > 0 {
		if desc == "" {
			desc = "Expense"
		}
		rec["line_items"] = []any{
			map[string]any{"account_id": acct, "amount": amount(total - vat), "description": desc},
			map[string]any{"account_id": acct, "amount": amount(vat), "description": "VAT"},
		}
	}
	var resp struct {
		Expense expense `json:"expense"`
	}
	if err := s.do(ctx, "create_expense", http.MethodPost, "/expenses", nil, rec, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(resp.Expense, true)}, nil
}

func (c *client) createBill(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	ven, billNo := str(in, "vendor_id"), str(in, "bill_number")
	if ven == "" || billNo == "" {
		return connector.Response{}, fatalf("create_bill: vendor_id and bill_number are required")
	}
	var lines []map[string]any
	for i, l := range objects(in, "lines") {
		acct := str(l, "account_id")
		n, ok := integer(l, "amount_kobo")
		qty := number(l, "quantity", 1)
		if acct == "" || !ok || n < 0 || qty <= 0 {
			return connector.Response{}, fatalf("create_bill: line %d needs an account_id, an amount_kobo (not negative) and a quantity over 0", i+1)
		}
		line := map[string]any{"account_id": acct, "rate": amount(n), "quantity": qty}
		for k, f := range map[string]string{"description": "description", "tax_id": "tax_id"} {
			if v := str(l, f); v != "" {
				line[k] = v
			}
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_bill: a bill needs a line")
	}
	s := c.open(req)
	type bill struct {
		ID      zid         `json:"bill_id"`
		Number  string      `json:"bill_number"`
		Vendor  zid         `json:"vendor_id"`
		Total   json.Number `json:"total"`
		Balance json.Number `json:"balance"`
	}
	out := func(b bill, created bool) map[string]any {
		return map[string]any{"bill_id": string(b.ID), "created": created, "total_kobo": kobo(b.Total), "balance_kobo": kobo(b.Balance)}
	}
	var found struct {
		Bills []bill `json:"bills"`
	}
	if err := s.do(ctx, "create_bill", http.MethodGet, "/bills", url.Values{"vendor_id": {ven}, "bill_number": {billNo}}, nil, &found, true); err != nil {
		return connector.Response{}, err
	}
	for _, b := range found.Bills {
		if b.Number == billNo && string(b.Vendor) == ven {
			return connector.Response{Output: out(b, false)}, nil
		}
	}
	date := str(in, "date")
	if date == "" {
		date = c.today()
	}
	rec := map[string]any{"vendor_id": ven, "bill_number": billNo, "date": date, "line_items": lines, "is_inclusive_tax": boolean(in, "is_inclusive_tax")}
	for k, f := range map[string]string{"due_date": "due_date", "notes": "notes"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	var resp struct {
		Bill bill `json:"bill"`
	}
	if err := s.do(ctx, "create_bill", http.MethodPost, "/bills", nil, rec, &resp, true); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(resp.Bill, true)}, nil
}

// --- pickers ---

func (c *client) listItems(ctx context.Context, req connector.Request) (connector.Response, error) {
	var resp struct {
		Items []struct {
			ID    zid          `json:"item_id"`
			Name  string       `json:"name"`
			Rate  *json.Number `json:"rate"`
			TaxID zid          `json:"tax_id"`
		} `json:"items"`
	}
	if err := c.open(req).do(ctx, "list_items", http.MethodGet, "/items", url.Values{"per_page": {"200"}}, nil, &resp, true); err != nil {
		return connector.Response{}, err
	}
	items := make([]any, 0, len(resp.Items))
	for _, it := range resp.Items {
		o := map[string]any{"item_id": string(it.ID), "name": it.Name}
		if it.Rate != nil {
			o["rate_kobo"] = kobo(*it.Rate)
		}
		if it.TaxID != "" {
			o["tax_id"] = string(it.TaxID)
		}
		items = append(items, o)
	}
	return connector.Response{Output: map[string]any{"items": items}}, nil
}

// accountKind is Taskiem's kind of a Books account type.
func accountKind(t string) string {
	switch t {
	case "income", "other_income":
		return "income"
	case "expense", "other_expense", "cost_of_goods_sold":
		return "expense"
	case "bank":
		return "bank"
	case "cash":
		return "cash"
	case "credit_card":
		return "credit_card"
	case "accounts_receivable":
		return "receivable"
	case "accounts_payable":
		return "payable"
	}
	return "other"
}

func (c *client) listAccounts(ctx context.Context, req connector.Request) (connector.Response, error) {
	want := str(req.Input, "type")
	var resp struct {
		Accounts []struct {
			ID       zid    `json:"account_id"`
			Name     string `json:"account_name"`
			Type     string `json:"account_type"`
			Currency string `json:"currency_code"`
		} `json:"chartofaccounts"`
	}
	if err := c.open(req).do(ctx, "list_accounts", http.MethodGet, "/chartofaccounts", url.Values{"filter_by": {"AccountType.Active"}}, nil, &resp, true); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		kind := accountKind(a.Type)
		if want != "" && kind != want {
			continue
		}
		o := map[string]any{"account_id": string(a.ID), "account_name": a.Name, "type": kind, "account_type": a.Type}
		if a.Currency != "" {
			o["currency"] = a.Currency
		}
		out = append(out, o)
	}
	return connector.Response{Output: map[string]any{"accounts": out}}, nil
}

func (c *client) listTaxes(ctx context.Context, req connector.Request) (connector.Response, error) {
	var resp struct {
		Taxes []struct {
			ID      zid          `json:"tax_id"`
			Name    string       `json:"tax_name"`
			Percent *json.Number `json:"tax_percentage"`
			Type    string       `json:"tax_type"`
		} `json:"taxes"`
	}
	if err := c.open(req).do(ctx, "list_taxes", http.MethodGet, "/settings/taxes", url.Values{"per_page": {"200"}}, nil, &resp, true); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(resp.Taxes))
	for _, t := range resp.Taxes {
		o := map[string]any{"tax_id": string(t.ID), "tax_name": t.Name}
		if t.Type != "" {
			o["tax_type"] = t.Type
		}
		if t.Percent != nil {
			if f, err := t.Percent.Float64(); err == nil {
				o["tax_percentage"] = math.Round(f*10000) / 10000
			}
		}
		out = append(out, o)
	}
	return connector.Response{Output: map[string]any{"taxes": out}}, nil
}

// --- what changed ---

// changesSince lists invoices changed since a time, each as the change it
// was (made, paid, or changed otherwise), and the payments recorded since
// (Books filters payments by no time: the latest made, read until older).
func (c *client) changesSince(ctx context.Context, req connector.Request) (connector.Response, error) {
	since, err := time.Parse(time.RFC3339, str(req.Input, "since"))
	if err != nil {
		return connector.Response{}, fatalf("changes_since: since must be a date and time (RFC 3339)")
	}
	s := c.open(req)
	invs, err := s.invoicesBy(ctx, "changes_since", url.Values{"last_modified_time": {zohoTime(since)}, "per_page": {"200"}})
	if err != nil {
		return connector.Response{}, err
	}
	changes := []any{}
	for _, i := range invs {
		event := "invoice.updated"
		switch {
		case i.Status == "paid":
			event = "invoice.paid"
		case after(i.Created, since):
			event = "invoice.created"
		}
		ch := map[string]any{"event": event, "kind": "invoice", "id": string(i.ID), "number": i.Number, "customer_id": string(i.Customer),
			"total_kobo": kobo(i.Total), "status": i.Status, "modified_at": i.Modified}
		if i.Reference != "" {
			ch["reference"] = i.Reference
		}
		if i.Currency != "" {
			ch["currency"] = i.Currency
		}
		changes = append(changes, ch)
	}
	var pays struct {
		Payments []payment `json:"customerpayments"`
	}
	if err := s.do(ctx, "changes_since", http.MethodGet, "/customerpayments", url.Values{"sort_column": {"created_time"}, "sort_order": {"D"}, "per_page": {"200"}}, nil, &pays, true); err != nil {
		return connector.Response{}, err
	}
	for _, p := range pays.Payments {
		if !after(p.Created, since) {
			continue
		}
		ch := map[string]any{"event": "payment.created", "kind": "payment", "id": string(p.ID), "customer_id": string(p.Customer), "amount_kobo": kobo(p.Amount), "modified_at": p.Created}
		if p.Reference != "" {
			ch["reference"] = p.Reference
		}
		if p.Currency != "" {
			ch["currency"] = p.Currency
		}
		if len(p.Invoices) > 0 {
			ch["invoice_id"] = string(p.Invoices[0].ID)
		}
		changes = append(changes, ch)
	}
	return connector.Response{Output: map[string]any{"changes": changes}}, nil
}

// after says whether a Books time is at or after t.
func after(s string, t time.Time) bool {
	at, ok := parseZohoTime(s)
	return ok && !at.Before(t)
}

package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// The records as Intuit answers them, with the fields the actions read.

type emailAddr struct {
	Address string `json:"Address,omitempty"`
}

type phone struct {
	FreeFormNumber string `json:"FreeFormNumber,omitempty"`
}

type customer struct {
	ID               string      `json:"Id,omitempty"`
	DisplayName      string      `json:"DisplayName,omitempty"`
	CompanyName      string      `json:"CompanyName,omitempty"`
	GivenName        string      `json:"GivenName,omitempty"`
	FamilyName       string      `json:"FamilyName,omitempty"`
	Notes            string      `json:"Notes,omitempty"`
	PrimaryEmailAddr *emailAddr  `json:"PrimaryEmailAddr,omitempty"`
	PrimaryPhone     *phone      `json:"PrimaryPhone,omitempty"`
	Balance          json.Number `json:"Balance,omitempty"`
	Active           *bool       `json:"Active,omitempty"`
}

func (c customer) out() map[string]any {
	o := map[string]any{"id": c.ID, "display_name": c.DisplayName, "balance_kobo": kobo(c.Balance), "active": c.Active == nil || *c.Active}
	if c.PrimaryEmailAddr != nil && c.PrimaryEmailAddr.Address != "" {
		o["email"] = c.PrimaryEmailAddr.Address
	}
	if c.PrimaryPhone != nil && c.PrimaryPhone.FreeFormNumber != "" {
		o["phone"] = c.PrimaryPhone.FreeFormNumber
	}
	return o
}

type vendor struct {
	ID               string      `json:"Id,omitempty"`
	DisplayName      string      `json:"DisplayName,omitempty"`
	CompanyName      string      `json:"CompanyName,omitempty"`
	PrimaryEmailAddr *emailAddr  `json:"PrimaryEmailAddr,omitempty"`
	PrimaryPhone     *phone      `json:"PrimaryPhone,omitempty"`
	Balance          json.Number `json:"Balance,omitempty"`
}

func (v vendor) out() map[string]any {
	o := map[string]any{"id": v.ID, "display_name": v.DisplayName, "balance_kobo": kobo(v.Balance)}
	if v.PrimaryEmailAddr != nil && v.PrimaryEmailAddr.Address != "" {
		o["email"] = v.PrimaryEmailAddr.Address
	}
	return o
}

type taxDetail struct {
	TotalTax json.Number `json:"TotalTax,omitempty"`
}

type invoice struct {
	ID           string      `json:"Id"`
	DocNumber    string      `json:"DocNumber"`
	CustomerRef  *ref        `json:"CustomerRef"`
	TxnDate      string      `json:"TxnDate"`
	DueDate      string      `json:"DueDate"`
	CurrencyRef  *ref        `json:"CurrencyRef"`
	TotalAmt     json.Number `json:"TotalAmt"`
	Balance      json.Number `json:"Balance"`
	TxnTaxDetail *taxDetail  `json:"TxnTaxDetail"`
	EmailStatus  string      `json:"EmailStatus"`
}

func (i invoice) out() map[string]any {
	o := map[string]any{"invoice_id": i.ID, "doc_number": i.DocNumber, "customer_id": i.CustomerRef.id(), "txn_date": i.TxnDate,
		"total_kobo": kobo(i.TotalAmt), "balance_kobo": kobo(i.Balance), "paid": kobo(i.Balance) == 0 && kobo(i.TotalAmt) > 0}
	if i.DueDate != "" {
		o["due_date"] = i.DueDate
	}
	if c := i.CurrencyRef.id(); c != "" {
		o["currency"] = c
	}
	if i.TxnTaxDetail != nil {
		o["tax_kobo"] = kobo(i.TxnTaxDetail.TotalTax)
	}
	if i.EmailStatus != "" {
		o["email_status"] = i.EmailStatus
	}
	return o
}

// txn is the part of a sales receipt, purchase, bill or payment the
// actions answer with.
type txn struct {
	ID            string      `json:"Id"`
	DocNumber     string      `json:"DocNumber"`
	TotalAmt      json.Number `json:"TotalAmt"`
	Balance       json.Number `json:"Balance"`
	UnappliedAmt  json.Number `json:"UnappliedAmt"`
	CustomerRef   *ref        `json:"CustomerRef"`
	VendorRef     *ref        `json:"VendorRef"`
	PrivateNote   string      `json:"PrivateNote"`
	PaymentRefNum string      `json:"PaymentRefNum"`
}

// --- the company ---

func (c *client) getCompany(ctx context.Context, req connector.Request) (connector.Response, error) {
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var resp struct {
		CompanyInfo struct {
			CompanyName string     `json:"CompanyName"`
			LegalName   string     `json:"LegalName"`
			Country     string     `json:"Country"`
			Email       *emailAddr `json:"Email"`
		} `json:"CompanyInfo"`
	}
	if err := s.do(ctx, "get_company", http.MethodGet, "companyinfo/"+url.PathEscape(s.realm), nil, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	ci := resp.CompanyInfo
	out := map[string]any{"company_id": s.realm, "company_name": ci.CompanyName, "legal_name": ci.LegalName, "country": ci.Country}
	if ci.Email != nil && ci.Email.Address != "" {
		out["email"] = ci.Email.Address
	}
	return connector.Response{Output: out}, nil
}

// --- customers and suppliers ---

func (s *session) customerBy(ctx context.Context, field, value string) (*customer, error) {
	var rows []customer
	if err := s.query(ctx, "find_customer", "Customer", "SELECT * FROM Customer WHERE "+field+" = "+quote(value), &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// findCustomer looks a customer up by email, then by display name.
func (s *session) findCustomer(ctx context.Context, email, name string) (*customer, error) {
	if email != "" {
		if c, err := s.customerBy(ctx, "PrimaryEmailAddr", email); c != nil || err != nil {
			return c, err
		}
	}
	if name != "" {
		return s.customerBy(ctx, "DisplayName", name)
	}
	return nil, nil
}

func (c *client) findCustomer(ctx context.Context, req connector.Request) (connector.Response, error) {
	email, name := str(req.Input, "email"), str(req.Input, "display_name")
	if email == "" && name == "" {
		return connector.Response{}, fatalf("find_customer: give an email or a display name")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	found, err := s.findCustomer(ctx, email, name)
	if err != nil || found == nil {
		return connector.Response{Output: map[string]any{"found": false}}, err
	}
	return connector.Response{Output: map[string]any{"found": true, "customer": found.out()}}, nil
}

func (c *client) createCustomer(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	name := str(in, "display_name")
	if name == "" {
		return connector.Response{}, fatalf("create_customer: display_name is required")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	found, err := s.findCustomer(ctx, str(in, "email"), name)
	if err != nil {
		return connector.Response{}, err
	}
	if found != nil {
		return connector.Response{Output: map[string]any{"customer_id": found.ID, "created": false, "customer": found.out()}}, nil
	}
	rec := customer{DisplayName: name, CompanyName: str(in, "company_name"), GivenName: str(in, "given_name"), FamilyName: str(in, "family_name"), Notes: str(in, "notes")}
	if e := str(in, "email"); e != "" {
		rec.PrimaryEmailAddr = &emailAddr{Address: e}
	}
	if p := str(in, "phone"); p != "" {
		rec.PrimaryPhone = &phone{FreeFormNumber: p}
	}
	var made customer
	if err := s.create(ctx, "create_customer", "Customer", rec, &made); err != nil {
		if faultCode(err) == "6240" { // the name is taken (by a customer, supplier or employee)
			if again, ferr := s.customerBy(ctx, "DisplayName", name); ferr == nil && again != nil {
				return connector.Response{Output: map[string]any{"customer_id": again.ID, "created": false, "customer": again.out()}}, nil
			}
		}
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"customer_id": made.ID, "created": true, "customer": made.out()}}, nil
}

func (s *session) vendorByName(ctx context.Context, name string) (*vendor, error) {
	var rows []vendor
	if err := s.query(ctx, "find_vendor", "Vendor", "SELECT * FROM Vendor WHERE DisplayName = "+quote(name), &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (c *client) findVendor(ctx context.Context, req connector.Request) (connector.Response, error) {
	name := str(req.Input, "display_name")
	if name == "" {
		return connector.Response{}, fatalf("find_vendor: display_name is required")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	v, err := s.vendorByName(ctx, name)
	if err != nil || v == nil {
		return connector.Response{Output: map[string]any{"found": false}}, err
	}
	return connector.Response{Output: map[string]any{"found": true, "vendor": v.out()}}, nil
}

func (c *client) createVendor(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	name := str(in, "display_name")
	if name == "" {
		return connector.Response{}, fatalf("create_vendor: display_name is required")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	if v, err := s.vendorByName(ctx, name); err != nil || v != nil {
		if v != nil {
			return connector.Response{Output: map[string]any{"vendor_id": v.ID, "created": false, "vendor": v.out()}}, nil
		}
		return connector.Response{}, err
	}
	rec := vendor{DisplayName: name, CompanyName: str(in, "company_name")}
	if e := str(in, "email"); e != "" {
		rec.PrimaryEmailAddr = &emailAddr{Address: e}
	}
	if p := str(in, "phone"); p != "" {
		rec.PrimaryPhone = &phone{FreeFormNumber: p}
	}
	var made vendor
	if err := s.create(ctx, "create_vendor", "Vendor", rec, &made); err != nil {
		if faultCode(err) == "6240" {
			if again, ferr := s.vendorByName(ctx, name); ferr == nil && again != nil {
				return connector.Response{Output: map[string]any{"vendor_id": again.ID, "created": false, "vendor": again.out()}}, nil
			}
		}
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"vendor_id": made.ID, "created": true, "vendor": made.out()}}, nil
}

// --- lines ---

// saleLines are invoice and sales receipt lines: the quantity times each
// line's amount, in the company's currency.
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
		detail := map[string]any{"Qty": qty, "UnitPrice": amount(each)}
		if id := str(l, "item_id"); id != "" {
			detail["ItemRef"] = ref{Value: id}
		}
		if t := str(l, "tax_code_id"); t != "" {
			detail["TaxCodeRef"] = ref{Value: t}
		}
		out = append(out, map[string]any{
			"DetailType":          "SalesItemLineDetail",
			"Amount":              amount(int64(math.Round(qty * float64(each)))),
			"Description":         str(l, "description"),
			"SalesItemLineDetail": detail,
		})
	}
	return out, nil
}

// expenseLines are purchase and bill lines, each to an account. vat is
// VAT included in the first line's amount, taken out as its own line.
func expenseLines(op string, in []map[string]any, vat int64, note string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(in)+1)
	for i, l := range in {
		acct := str(l, "account_id")
		n, ok := integer(l, "amount_kobo")
		if acct == "" || !ok || n < 0 {
			return nil, fatalf("%s: line %d needs an account_id and an amount_kobo, not negative", op, i+1)
		}
		if i == 0 && vat > 0 {
			if vat > n {
				return nil, fatalf("%s: vat_kobo is more than the first line's amount", op)
			}
			n -= vat
		}
		desc := str(l, "description")
		if desc == "" {
			desc = note
		}
		detail := map[string]any{"AccountRef": ref{Value: acct}}
		if t := str(l, "tax_code_id"); t != "" {
			detail["TaxCodeRef"] = ref{Value: t}
		}
		out = append(out, map[string]any{"DetailType": "AccountBasedExpenseLineDetail", "Amount": amount(n), "Description": desc, "AccountBasedExpenseLineDetail": detail})
	}
	if vat > 0 && len(in) > 0 {
		out = append(out, map[string]any{"DetailType": "AccountBasedExpenseLineDetail", "Amount": amount(vat), "Description": "VAT",
			"AccountBasedExpenseLineDetail": map[string]any{"AccountRef": ref{Value: str(in[0], "account_id")}}})
	}
	return out, nil
}

// common sets the fields every transaction may take.
func common(rec map[string]any, in map[string]any, lines []map[string]any) {
	for k, f := range map[string]string{"TxnDate": "txn_date", "PrivateNote": "private_note"} {
		if v := str(in, f); v != "" {
			rec[k] = v
		}
	}
	if c := str(in, "currency"); c != "" {
		rec["CurrencyRef"] = ref{Value: c}
	}
	if g := taxCalculation(in, lines); g != "" {
		rec["GlobalTaxCalculation"] = g
	}
}

// --- invoices ---

func (s *session) invoiceBy(ctx context.Context, op, doc string) (*invoice, error) {
	var rows []invoice
	if err := s.query(ctx, op, "Invoice", "SELECT * FROM Invoice WHERE DocNumber = "+quote(doc), &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (c *client) createInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	doc, cust := docNumber(str(in, "doc_number")), str(in, "customer_id")
	if doc == "" || cust == "" {
		return connector.Response{}, fatalf("create_invoice: doc_number and customer_id are required")
	}
	in0 := objects(in, "lines")
	lines, err := saleLines("create_invoice", in0)
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_invoice: an invoice needs a line")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	if found, err := s.invoiceBy(ctx, "create_invoice", doc); err != nil || found != nil {
		if found != nil {
			return connector.Response{Output: map[string]any{"invoice_id": found.ID, "created": false, "invoice": found.out()}}, nil
		}
		return connector.Response{}, err
	}
	rec := map[string]any{"DocNumber": doc, "CustomerRef": ref{Value: cust}, "Line": lines}
	common(rec, in, in0)
	if d := str(in, "due_date"); d != "" {
		rec["DueDate"] = d
	}
	if e := str(in, "email"); e != "" {
		rec["BillEmail"] = emailAddr{Address: e}
	}
	if m := str(in, "customer_memo"); m != "" {
		rec["CustomerMemo"] = map[string]string{"value": m}
	}
	var made invoice
	if err := s.create(ctx, "create_invoice", "Invoice", rec, &made); err != nil {
		if faultCode(err) == "6140" { // the number is taken: made by an earlier try
			if again, ferr := s.invoiceBy(ctx, "create_invoice", doc); ferr == nil && again != nil {
				return connector.Response{Output: map[string]any{"invoice_id": again.ID, "created": false, "invoice": again.out()}}, nil
			}
		}
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"invoice_id": made.ID, "created": true, "invoice": made.out()}}, nil
}

func (c *client) getInvoice(ctx context.Context, req connector.Request) (connector.Response, error) {
	id := str(req.Input, "invoice_id")
	if id == "" {
		return connector.Response{}, fatalf("get_invoice: invoice_id is required")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var resp struct {
		Invoice invoice `json:"Invoice"`
	}
	if err := s.do(ctx, "get_invoice", http.MethodGet, "invoice/"+url.PathEscape(id), nil, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: resp.Invoice.out()}, nil
}

func (c *client) queryInvoices(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	var where []string
	if d := str(in, "doc_number"); d != "" {
		where = append(where, "DocNumber = "+quote(docNumber(d)))
	}
	if id := str(in, "customer_id"); id != "" {
		where = append(where, "CustomerRef = "+quote(id))
	}
	if t := str(in, "changed_since"); t != "" {
		at, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return connector.Response{}, fatalf("query_invoices: changed_since must be a date and time (RFC 3339)")
		}
		where = append(where, "MetaData.LastUpdatedTime >= "+quote(at.Format(time.RFC3339)))
	}
	if b, _ := in["unpaid"].(bool); b {
		where = append(where, "Balance > '0'")
	}
	maxN, ok := integer(in, "max")
	if !ok || maxN < 1 || maxN > 1000 {
		maxN = 100
	}
	q := "SELECT * FROM Invoice"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDERBY MetaData.LastUpdatedTime DESC MAXRESULTS %d", maxN)
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var rows []invoice
	if err := s.query(ctx, "query_invoices", "Invoice", q, &rows); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.out())
	}
	return connector.Response{Output: map[string]any{"invoices": out}}, nil
}

// --- payments ---

// refMarker is how a payment carries its reference in full, in its note:
// PaymentRefNum takes only 21 characters.
func refMarker(reference string) string { return "Taskiem reference: " + reference }

func (c *client) createPayment(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	reference := str(in, "reference")
	amt, ok := integer(in, "amount_kobo")
	if reference == "" || !ok || amt < 1 {
		return connector.Response{}, fatalf("create_payment: reference and an amount_kobo of at least 1 are required")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	cust, inv := str(in, "customer_id"), str(in, "invoice_id")
	if inv != "" && cust == "" {
		var resp struct {
			Invoice invoice `json:"Invoice"`
		}
		if err := s.do(ctx, "create_payment", http.MethodGet, "invoice/"+url.PathEscape(inv), nil, nil, &resp); err != nil {
			return connector.Response{}, err
		}
		cust = resp.Invoice.CustomerRef.id()
	}
	if cust == "" {
		return connector.Response{}, fatalf("create_payment: give the customer_id, or the invoice_id it pays")
	}
	date := str(in, "txn_date")
	if date == "" {
		date = today(c)
	}
	// Recorded already, by an earlier try: the customer's payments that
	// day, by the reference in their note.
	var rows []txn
	if err := s.query(ctx, "create_payment", "Payment", "SELECT * FROM Payment WHERE CustomerRef = "+quote(cust)+" AND TxnDate = "+quote(date), &rows); err != nil {
		return connector.Response{}, err
	}
	for _, p := range rows {
		if strings.Contains(p.PrivateNote, refMarker(reference)) {
			return connector.Response{Output: paymentOut(p, false)}, nil
		}
	}
	note := refMarker(reference)
	if m := str(in, "memo"); m != "" {
		note = m + "\n" + note
	}
	rec := map[string]any{"CustomerRef": ref{Value: cust}, "TotalAmt": amount(amt), "TxnDate": date, "PaymentRefNum": docNumber(reference), "PrivateNote": note}
	if inv != "" {
		rec["Line"] = []map[string]any{{"Amount": amount(amt), "LinkedTxn": []map[string]string{{"TxnId": inv, "TxnType": "Invoice"}}}}
	}
	if a := str(in, "deposit_account_id"); a != "" {
		rec["DepositToAccountRef"] = ref{Value: a}
	}
	if m := str(in, "payment_method_id"); m != "" {
		rec["PaymentMethodRef"] = ref{Value: m}
	}
	if cur := str(in, "currency"); cur != "" {
		rec["CurrencyRef"] = ref{Value: cur}
	}
	var made txn
	if err := s.create(ctx, "create_payment", "Payment", rec, &made); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: paymentOut(made, true)}, nil
}

func paymentOut(p txn, created bool) map[string]any {
	return map[string]any{"payment_id": p.ID, "created": created, "customer_id": p.CustomerRef.id(), "amount_kobo": kobo(p.TotalAmt), "unapplied_kobo": kobo(p.UnappliedAmt)}
}

// --- sales, expenses and bills ---

// txnBy finds transactions of an entity by their number.
func (s *session) txnBy(ctx context.Context, op, entity, doc string) ([]txn, error) {
	var rows []txn
	err := s.query(ctx, op, entity, "SELECT * FROM "+entity+" WHERE DocNumber = "+quote(doc), &rows)
	return rows, err
}

func (c *client) createSalesReceipt(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	doc := docNumber(str(in, "doc_number"))
	if doc == "" {
		return connector.Response{}, fatalf("create_sales_receipt: doc_number is required")
	}
	in0 := objects(in, "lines")
	lines, err := saleLines("create_sales_receipt", in0)
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_sales_receipt: a sale needs a line")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	out := func(t txn, created bool) map[string]any {
		return map[string]any{"sales_receipt_id": t.ID, "created": created, "doc_number": t.DocNumber, "total_kobo": kobo(t.TotalAmt)}
	}
	found, err := s.txnBy(ctx, "create_sales_receipt", "SalesReceipt", doc)
	if err != nil {
		return connector.Response{}, err
	}
	if len(found) > 0 {
		return connector.Response{Output: out(found[0], false)}, nil
	}
	rec := map[string]any{"DocNumber": doc, "Line": lines}
	common(rec, in, in0)
	if id := str(in, "customer_id"); id != "" {
		rec["CustomerRef"] = ref{Value: id}
	}
	if a := str(in, "deposit_account_id"); a != "" {
		rec["DepositToAccountRef"] = ref{Value: a}
	}
	if m := str(in, "payment_method_id"); m != "" {
		rec["PaymentMethodRef"] = ref{Value: m}
	}
	var made txn
	if err := s.create(ctx, "create_sales_receipt", "SalesReceipt", rec, &made); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(made, true)}, nil
}

var paymentTypes = map[string]string{"": "Cash", "cash": "Cash", "check": "Check", "credit_card": "CreditCard"}

func (c *client) createPurchase(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	doc, from := docNumber(str(in, "doc_number")), str(in, "paid_from_account_id")
	if doc == "" || from == "" {
		return connector.Response{}, fatalf("create_purchase: doc_number and paid_from_account_id are required")
	}
	pt, ok := paymentTypes[str(in, "payment_type")]
	if !ok {
		return connector.Response{}, fatalf("create_purchase: payment_type is cash, check or credit_card")
	}
	vat, _ := integer(in, "vat_kobo")
	note := str(in, "private_note")
	if note == "" {
		note = "Expense"
	}
	in0 := objects(in, "lines")
	lines, err := expenseLines("create_purchase", in0, vat, note)
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_purchase: an expense needs a line")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	out := func(t txn, created bool) map[string]any {
		return map[string]any{"purchase_id": t.ID, "created": created, "total_kobo": kobo(t.TotalAmt)}
	}
	found, err := s.txnBy(ctx, "create_purchase", "Purchase", doc)
	if err != nil {
		return connector.Response{}, err
	}
	if len(found) > 0 {
		return connector.Response{Output: out(found[0], false)}, nil
	}
	rec := map[string]any{"DocNumber": doc, "PaymentType": pt, "AccountRef": ref{Value: from}, "Line": lines}
	common(rec, in, in0)
	if v := str(in, "vendor_id"); v != "" {
		rec["EntityRef"] = map[string]string{"value": v, "type": "Vendor"}
	}
	var made txn
	if err := s.create(ctx, "create_purchase", "Purchase", rec, &made); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(made, true)}, nil
}

func (c *client) createBill(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := req.Input
	doc, ven := docNumber(str(in, "doc_number")), str(in, "vendor_id")
	if doc == "" || ven == "" {
		return connector.Response{}, fatalf("create_bill: vendor_id and doc_number are required")
	}
	in0 := objects(in, "lines")
	lines, err := expenseLines("create_bill", in0, 0, str(in, "private_note"))
	if err != nil {
		return connector.Response{}, err
	}
	if len(lines) == 0 {
		return connector.Response{}, fatalf("create_bill: a bill needs a line")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	out := func(t txn, created bool) map[string]any {
		return map[string]any{"bill_id": t.ID, "created": created, "total_kobo": kobo(t.TotalAmt), "balance_kobo": kobo(t.Balance)}
	}
	// Two suppliers may use the same numbers: this supplier's.
	found, err := s.txnBy(ctx, "create_bill", "Bill", doc)
	if err != nil {
		return connector.Response{}, err
	}
	for _, b := range found {
		if b.VendorRef.id() == ven {
			return connector.Response{Output: out(b, false)}, nil
		}
	}
	rec := map[string]any{"DocNumber": doc, "VendorRef": ref{Value: ven}, "Line": lines}
	common(rec, in, in0)
	if d := str(in, "due_date"); d != "" {
		rec["DueDate"] = d
	}
	var made txn
	if err := s.create(ctx, "create_bill", "Bill", rec, &made); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out(made, true)}, nil
}

// --- pickers ---

func (c *client) listItems(ctx context.Context, req connector.Request) (connector.Response, error) {
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var rows []struct {
		ID               string       `json:"Id"`
		Name             string       `json:"Name"`
		Type             string       `json:"Type"`
		UnitPrice        *json.Number `json:"UnitPrice"`
		IncomeAccountRef *ref         `json:"IncomeAccountRef"`
	}
	if err := s.query(ctx, "list_items", "Item", "SELECT * FROM Item WHERE Active = true MAXRESULTS 1000", &rows); err != nil {
		return connector.Response{}, err
	}
	items := make([]any, 0, len(rows))
	for _, r := range rows {
		it := map[string]any{"id": r.ID, "name": r.Name, "type": r.Type}
		if r.UnitPrice != nil {
			it["unit_price_kobo"] = kobo(*r.UnitPrice)
		}
		if a := r.IncomeAccountRef.id(); a != "" {
			it["income_account_id"] = a
		}
		items = append(items, it)
	}
	return connector.Response{Output: map[string]any{"items": items}}, nil
}

// accountKind is Taskiem's kind of a QuickBooks account type.
func accountKind(t string) string {
	switch t {
	case "Income", "Other Income":
		return "income"
	case "Expense", "Other Expense", "Cost of Goods Sold":
		return "expense"
	case "Bank":
		return "bank"
	case "Credit Card":
		return "credit_card"
	case "Accounts Receivable":
		return "receivable"
	case "Accounts Payable":
		return "payable"
	}
	return "other"
}

func (c *client) listAccounts(ctx context.Context, req connector.Request) (connector.Response, error) {
	want := str(req.Input, "type")
	if want == "cash" {
		want = "bank" // QuickBooks keeps cash on hand in Bank accounts
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var rows []struct {
		ID          string `json:"Id"`
		Name        string `json:"Name"`
		AccountType string `json:"AccountType"`
		CurrencyRef *ref   `json:"CurrencyRef"`
	}
	if err := s.query(ctx, "list_accounts", "Account", "SELECT * FROM Account WHERE Active = true MAXRESULTS 1000", &rows); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		kind := accountKind(r.AccountType)
		if want != "" && kind != want {
			continue
		}
		a := map[string]any{"id": r.ID, "name": r.Name, "type": kind, "account_type": r.AccountType}
		if cur := r.CurrencyRef.id(); cur != "" {
			a["currency"] = cur
		}
		out = append(out, a)
	}
	return connector.Response{Output: map[string]any{"accounts": out}}, nil
}

func (c *client) listTaxCodes(ctx context.Context, req connector.Request) (connector.Response, error) {
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var codes []struct {
		ID               string `json:"Id"`
		Name             string `json:"Name"`
		Description      string `json:"Description"`
		SalesTaxRateList *struct {
			TaxRateDetail []struct {
				TaxRateRef *ref `json:"TaxRateRef"`
			} `json:"TaxRateDetail"`
		} `json:"SalesTaxRateList"`
	}
	if err := s.query(ctx, "list_tax_codes", "TaxCode", "SELECT * FROM TaxCode WHERE Active = true MAXRESULTS 1000", &codes); err != nil {
		return connector.Response{}, err
	}
	var rates []struct {
		ID        string      `json:"Id"`
		RateValue json.Number `json:"RateValue"`
	}
	if err := s.query(ctx, "list_tax_codes", "TaxRate", "SELECT * FROM TaxRate MAXRESULTS 1000", &rates); err != nil {
		return connector.Response{}, err
	}
	rate := map[string]float64{}
	for _, r := range rates {
		if f, err := r.RateValue.Float64(); err == nil {
			rate[r.ID] = f
		}
	}
	out := make([]any, 0, len(codes))
	for _, tc := range codes {
		t := map[string]any{"id": tc.ID, "name": tc.Name}
		if tc.Description != "" {
			t["description"] = tc.Description
		}
		if tc.SalesTaxRateList != nil && len(tc.SalesTaxRateList.TaxRateDetail) > 0 {
			sum, known := 0.0, false
			for _, d := range tc.SalesTaxRateList.TaxRateDetail {
				if f, ok := rate[d.TaxRateRef.id()]; ok {
					sum, known = sum+f, true
				}
			}
			if known {
				t["percent"] = math.Round(sum*10000) / 10000
			}
		}
		out = append(out, t)
	}
	return connector.Response{Output: map[string]any{"tax_codes": out}}, nil
}

// --- any record, and what changed ---

// entities are the records get_entity and changes_since read, as their
// lower-case names (the notifications') map to Intuit's.
var entities = map[string]string{
	"invoice": "Invoice", "payment": "Payment", "customer": "Customer", "vendor": "Vendor", "salesreceipt": "SalesReceipt",
	"purchase": "Purchase", "bill": "Bill", "billpayment": "BillPayment", "item": "Item", "account": "Account",
	"estimate": "Estimate", "creditmemo": "CreditMemo", "refundreceipt": "RefundReceipt", "deposit": "Deposit",
	"journalentry": "JournalEntry", "taxcode": "TaxCode",
}

func (c *client) getEntity(ctx context.Context, req connector.Request) (connector.Response, error) {
	name, id := strings.ToLower(str(req.Input, "entity")), str(req.Input, "id")
	entity, ok := entities[name]
	if !ok || id == "" {
		return connector.Response{}, fatalf("get_entity: give one of the record kinds and an id")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	resp := map[string]json.RawMessage{}
	if err := s.do(ctx, "get_entity", http.MethodGet, name+"/"+url.PathEscape(id), nil, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	var rec map[string]any
	if err := json.Unmarshal(resp[entity], &rec); err != nil || rec == nil {
		return connector.Response{}, fmt.Errorf("quickbooks get_entity: the answer has no %s: %w", entity, effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"entity": name, "record": rec}}, nil
}

func (c *client) changesSince(ctx context.Context, req connector.Request) (connector.Response, error) {
	since, err := time.Parse(time.RFC3339, str(req.Input, "since"))
	if err != nil {
		return connector.Response{}, fatalf("changes_since: since must be a date and time (RFC 3339)")
	}
	if c.now().Sub(since) > 30*24*time.Hour {
		return connector.Response{}, fatalf("changes_since: QuickBooks keeps changes for 30 days; since is further back")
	}
	list, _ := req.Input["entities"].([]any)
	var names []string
	for _, v := range list {
		n, _ := v.(string)
		e, ok := entities[strings.ToLower(n)]
		if !ok || e == "TaxCode" {
			return connector.Response{}, fatalf("changes_since: %q is not a record kind it reads", n)
		}
		names = append(names, e)
	}
	if len(names) == 0 {
		return connector.Response{}, fatalf("changes_since: name at least one record kind")
	}
	s, err := c.open(req)
	if err != nil {
		return connector.Response{}, err
	}
	var resp struct {
		CDCResponse []struct {
			QueryResponse []map[string]json.RawMessage `json:"QueryResponse"`
		} `json:"CDCResponse"`
	}
	q := url.Values{"entities": {strings.Join(names, ",")}, "changedSince": {since.Format(time.RFC3339)}}
	if err := s.do(ctx, "changes_since", http.MethodGet, "cdc", q, nil, &resp); err != nil {
		return connector.Response{}, err
	}
	changes := []any{}
	for _, cdc := range resp.CDCResponse {
		for _, qr := range cdc.QueryResponse {
			for lower, entity := range entities {
				raw, ok := qr[entity]
				if !ok {
					continue
				}
				var recs []map[string]any
				if json.Unmarshal(raw, &recs) != nil {
					continue
				}
				for _, r := range recs {
					id, _ := r["Id"].(string)
					status, _ := r["status"].(string)
					changes = append(changes, map[string]any{"entity": lower, "id": id, "deleted": status == "Deleted", "record": r})
				}
			}
		}
	}
	return connector.Response{Output: map[string]any{"changes": changes}}, nil
}

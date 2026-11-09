package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
)

// References and their cards (docs/spec/chat-five-more-flows.md, phase 2
// additions). "/invoice", "/bill", "/order", "/purchase" and "/customer"
// in the composer offer things to tag, each row saying who it is with,
// when, how much and where it stands; a thing tagged shows as a card read
// live, so a payment made since shows as paid.
//
// In the Taskiem chat everything the person may see is offered. In a
// business's conversation only what is between the two businesses:
// invoices we sent them and bills they sent us, orders they placed with us
// and orders we placed with them; never a customer. A message's tags are
// checked against the same list (checkMentions).

// chatRef is one thing that may be tagged.
type chatRef struct {
	// Kind: invoice (we sent it), bill (sent to us), order (placed with
	// us), purchase (we placed it) or customer.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Name is its number or reference, or a customer's name.
	Name string `json:"name"`
	// Party is who it is with: a customer (masked) or a business; for a
	// customer, their number, masked.
	Party     string    `json:"party,omitempty"`
	When      time.Time `json:"when"`
	TotalKobo *int64    `json:"total_kobo,omitempty"`
	Status    string    `json:"status,omitempty"`
}

// refFamily is the kind as both sides of a conversation say it: our
// invoice is their bill, our order their purchase.
func refFamily(kind string) string {
	switch kind {
	case "invoice", "bill":
		return "invoice"
	case "order", "purchase":
		return "order"
	}
	return kind
}

// partyName is a name as a list shows it: a business's in full, a
// person's masked.
func partyName(name string, business bool) string {
	if business {
		return name
	}
	return maskName(name)
}

func koboPtr(v int64) *int64 { return &v }

// linkRefsTx is what the conversation with l may name, newest first, of
// what the person may see: invoices and bills with invoice.read, orders
// placed with us with order handling, our own orders with table.read.
func linkRefsTx(ctx context.Context, tx pgx.Tx, p *Principal, l links.Link) ([]chatRef, error) {
	tenant := p.TenantID
	var out []chatRef
	for _, s := range []struct {
		kind, sql string
		args      []any
		may       bool
	}{
		{"invoice", `SELECT id, number, total_kobo, status, COALESCE(sent_at, created_at) FROM customer_invoices
			WHERE tenant_id = $1 AND customer_key = $2 AND kind = 'invoice' AND status <> 'draft'
			ORDER BY created_at DESC LIMIT 100`, []any{tenant, links.KeyPrefix + l.Business.String()}, p.Can(PermInvoiceRead)},
		{"bill", `SELECT id, reference, amount_kobo, status, created_at FROM bills
			WHERE tenant_id = $1 AND link_id = $2 ORDER BY created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermInvoiceRead)},
		{"order", `SELECT o.id, o.reference, o.total_kobo, o.status, o.created_at FROM storefront_orders o JOIN link_orders x ON x.order_id = o.id
			WHERE x.tenant_id = $1 AND x.link_id = $2 ORDER BY o.created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermOrders)},
		{"purchase", `SELECT o.id, o.reference, o.total_kobo, o.status, o.created_at FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
			WHERE o.tenant_id = $1 AND s.link_id = $2 AND o.status <> 'drafted' ORDER BY o.created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermTableRead)},
	} {
		if !s.may {
			continue
		}
		rows, err := tx.Query(ctx, s.sql, s.args...)
		if err != nil {
			return nil, err
		}
		got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (chatRef, error) {
			x := chatRef{Kind: s.kind, Party: l.Name}
			var id uuid.UUID
			var total int64
			err := r.Scan(&id, &x.Name, &total, &x.Status, &x.When)
			x.ID, x.TotalKobo = id.String(), &total
			return x, err
		})
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if out == nil {
		out = []chatRef{}
	}
	return out, nil
}

// allRefsTx is everything the person may tag in their Taskiem chat.
func (s *Server) allRefsTx(ctx context.Context, tx pgx.Tx, p *Principal) ([]chatRef, error) {
	tenant := p.TenantID
	out := []chatRef{}
	if p.Can(PermInvoiceRead) && s.Invoices != nil {
		list, err := s.Invoices.ListOf(ctx, tx, tenant, "", "invoice", 200)
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			_, linked := links.BuyerOf(x.Customer.Key)
			when := x.CreatedAt
			if x.SentAt != nil {
				when = *x.SentAt
			}
			out = append(out, chatRef{Kind: "invoice", ID: x.ID.String(), Name: x.Number, Party: partyName(x.Customer.Name, linked),
				When: when, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status})
		}
		bills, err := links.ListBillsTx(ctx, tx, "all", 200)
		if err != nil {
			return nil, err
		}
		for _, b := range bills {
			out = append(out, chatRef{Kind: "bill", ID: b.ID.String(), Name: b.Reference, Party: b.SupplierName,
				When: b.CreatedAt, TotalKobo: koboPtr(b.AmountKobo), Status: b.Status})
		}
	}
	if p.Can(PermOrders) && s.Orders != nil {
		list, err := s.Orders.ListOrders(ctx, tx, tenant, "", "", 200)
		if err != nil {
			return nil, err
		}
		for _, o := range list {
			out = append(out, chatRef{Kind: "order", ID: o.ID.String(), Name: o.Reference, Party: partyName(o.Customer.Name, o.Kind == "trade"),
				When: o.CreatedAt, TotalKobo: koboPtr(o.TotalKobo), Status: o.Status})
		}
	}
	if p.Can(PermTableRead) {
		list, err := stock.PurchasesTx(ctx, tx, tenant, stock.PurchaseFilter{Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			out = append(out, chatRef{Kind: "purchase", ID: x.ID.String(), Name: x.Reference, Party: x.Supplier,
				When: x.CreatedAt, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status})
		}
	}
	if p.Can(PermOrders) {
		rows, err := tx.Query(ctx, `SELECT `+customerChatCols+` FROM customer_conversations c
			WHERE c.tenant_id = $1 ORDER BY c.last_message_at DESC LIMIT 100`, tenant)
		if err != nil {
			return nil, err
		}
		raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
		if err != nil {
			return nil, err
		}
		for _, raw := range raws {
			v, err := s.openCustomerChat(ctx, tx, tenant, raw)
			if err != nil {
				return nil, err
			}
			out = append(out, chatRef{Kind: "customer", ID: v.ID.String(), Name: v.Name, Party: maskPhone(v.Phone), When: v.LastAt})
		}
	}
	return out, nil
}

// chatRefs is GET /v1/chat-refs: what the Taskiem chat may tag.
func (s *Server) chatRefs(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out []chatRef
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.allRefsTx(r.Context(), tx, p)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": out})
}

// linkChatRefs is GET /v1/link-chats/{link}/refs.
func (s *Server) linkChatRefs(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out []chatRef
	if err := s.tx(r, func(tx pgx.Tx) error {
		l, err := links.GetTx(r.Context(), tx, link)
		if err != nil {
			return err
		}
		out, err = linkRefsTx(r.Context(), tx, p, l)
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": out})
}

// cardLine is one line of a card.
type cardLine struct {
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	TotalKobo int64  `json:"total_kobo"`
}

// cardOrder is a customer's latest order, on their card.
type cardOrder struct {
	Reference string    `json:"reference"`
	TotalKobo int64     `json:"total_kobo"`
	Status    string    `json:"status"`
	When      time.Time `json:"when"`
}

// chatCard is a thing tagged, as its card shows it, read now.
type chatCard struct {
	chatRef
	DueOn    string     `json:"due_on,omitempty"`
	PaidKobo *int64     `json:"paid_kobo,omitempty"`
	DueKobo  *int64     `json:"due_kobo,omitempty"`
	Lines    []cardLine `json:"lines,omitempty"`
	// More is how many lines are not shown (a card shows three).
	More int `json:"more,omitempty"`
	// A customer's: their orders, what they owe on invoices, the latest.
	Orders    *int       `json:"orders,omitempty"`
	OwesKobo  *int64     `json:"owes_kobo,omitempty"`
	LastOrder *cardOrder `json:"last_order,omitempty"`
	// Action is the one thing the card offers besides opening it, for
	// those who may: send (a draft), remind (owed), pay (a bill owed),
	// message (a customer).
	Action string `json:"action,omitempty"`
}

var errNoCard = errors.New("not found")

func (c *chatCard) lines(all []cardLine) {
	if len(all) > 3 {
		c.More = len(all) - 3
		all = all[:3]
	}
	c.Lines = all
}

// chatCardTx reads one thing's card, by its kind and id, checking the
// person may see it.
func (s *Server) chatCardTx(ctx context.Context, tx pgx.Tx, p *Principal, kind, id string) (chatCard, error) {
	tenant := p.TenantID
	owing := func(status string) bool {
		return status == "sent" || status == "part_paid" || status == "overdue" || status == "recorded" || status == "open" || status == "paying" || status == "disputed"
	}
	switch kind {
	case "invoice":
		if !p.Can(PermInvoiceRead) || s.Invoices == nil {
			return chatCard{}, errForbidden
		}
		if _, err := uuid.Parse(id); err != nil {
			return chatCard{}, errNoCard
		}
		x, err := s.Invoices.Get(ctx, tx, tenant, id)
		if err != nil {
			return chatCard{}, invoiceErr(err)
		}
		_, linked := links.BuyerOf(x.Customer.Key)
		when := x.CreatedAt
		if x.SentAt != nil {
			when = *x.SentAt
		}
		c := chatCard{chatRef: chatRef{Kind: kind, ID: x.ID.String(), Name: x.Number, Party: partyName(x.Customer.Name, linked),
			When: when, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status}, DueOn: x.DueOn, PaidKobo: koboPtr(x.PaidKobo), DueKobo: koboPtr(x.DueKobo)}
		var ls []cardLine
		for _, l := range x.Lines {
			ls = append(ls, cardLine{Name: l.Description, Quantity: l.Quantity, TotalKobo: l.Quantity * l.UnitKobo})
		}
		c.lines(ls)
		if p.Can(PermInvoiceManage) {
			switch {
			case x.Status == "draft":
				c.Action = "send"
			case owing(x.Status) && x.DueKobo > 0 && x.Kind != "credit":
				c.Action = "remind"
			}
		}
		return c, nil
	case "bill":
		if !p.Can(PermInvoiceRead) {
			return chatCard{}, errForbidden
		}
		if _, err := uuid.Parse(id); err != nil {
			return chatCard{}, errNoCard
		}
		b, err := links.GetBillTx(ctx, tx, id)
		if err != nil {
			return chatCard{}, err
		}
		c := chatCard{chatRef: chatRef{Kind: kind, ID: b.ID.String(), Name: b.Reference, Party: b.SupplierName,
			When: b.CreatedAt, TotalKobo: koboPtr(b.AmountKobo), Status: b.Status}, DueOn: b.DueOn, PaidKobo: koboPtr(b.PaidKobo), DueKobo: koboPtr(b.DueKobo)}
		var raw []struct {
			Description string `json:"description"`
			Quantity    int64  `json:"quantity"`
			UnitKobo    int64  `json:"unit_kobo"`
		}
		_ = json.Unmarshal(b.Lines, &raw)
		var ls []cardLine
		for _, l := range raw {
			ls = append(ls, cardLine{Name: l.Description, Quantity: l.Quantity, TotalKobo: l.Quantity * l.UnitKobo})
		}
		c.lines(ls)
		if p.Can(PermInvoiceManage) && b.DueKobo > 0 && b.Status != "void" {
			c.Action = "pay"
		}
		return c, nil
	case "order":
		if !p.Can(PermOrders) || s.Orders == nil {
			return chatCard{}, errForbidden
		}
		var ref string
		if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND id::text = $2`, tenant, id).Scan(&ref); err != nil {
			return chatCard{}, err
		}
		o, err := s.Orders.GetOrder(ctx, tx, tenant, ref)
		if err != nil {
			return chatCard{}, err
		}
		c := chatCard{chatRef: chatRef{Kind: kind, ID: o.ID.String(), Name: o.Reference, Party: partyName(o.Customer.Name, o.Kind == "trade"),
			When: o.CreatedAt, TotalKobo: koboPtr(o.TotalKobo), Status: o.Status}}
		var ls []cardLine
		for _, l := range o.Basket {
			ls = append(ls, cardLine{Name: l.Name, Quantity: int64(l.Quantity), TotalKobo: l.LineKobo})
		}
		c.lines(ls)
		return c, nil
	case "purchase":
		if !p.Can(PermTableRead) {
			return chatCard{}, errForbidden
		}
		pid, err := uuid.Parse(id)
		if err != nil {
			return chatCard{}, errNoCard
		}
		x, err := stock.PurchaseTx(ctx, tx, tenant, pid, false)
		if err != nil {
			return chatCard{}, err
		}
		c := chatCard{chatRef: chatRef{Kind: kind, ID: x.ID.String(), Name: x.Reference, Party: x.Supplier,
			When: x.CreatedAt, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status}, PaidKobo: koboPtr(x.PaidKobo)}
		var ls []cardLine
		for _, l := range x.Lines {
			ls = append(ls, cardLine{Name: l.Item, Quantity: l.Quantity, TotalKobo: l.Quantity * l.CostKobo})
		}
		c.lines(ls)
		return c, nil
	case "customer":
		if !p.Can(PermOrders) {
			return chatCard{}, errForbidden
		}
		v, subject, err := s.customerChatIn(ctx, tx, tenant, id)
		if err != nil {
			return chatCard{}, err
		}
		c := chatCard{chatRef: chatRef{Kind: kind, ID: v.ID.String(), Name: v.Name, Party: maskPhone(v.Phone), When: v.LastAt}}
		if p.Can(PermInvoiceRead) {
			due, _, err := owedBy(ctx, tx, tenant, subject)
			if err != nil {
				return chatCard{}, err
			}
			c.OwesKobo = &due
		}
		var n int
		var last cardOrder
		err = tx.QueryRow(ctx, `SELECT count(*) OVER (), reference, total_kobo, status, created_at FROM storefront_orders
			WHERE tenant_id = $1 AND subject_id = $2 ORDER BY created_at DESC LIMIT 1`, tenant, subject).
			Scan(&n, &last.Reference, &last.TotalKobo, &last.Status, &last.When)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			n = 0
		case err != nil:
			return chatCard{}, err
		default:
			c.LastOrder = &last
		}
		c.Orders = &n
		if !v.Stopped {
			c.Action = "message"
		}
		return c, nil
	}
	return chatCard{}, errNoCard
}

// cardErr answers a card that cannot be read.
func (s *Server) cardFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNoCard), errors.Is(err, pgx.ErrNoRows), errors.Is(err, errCustomerChatNone), errors.Is(err, links.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	default:
		s.fail(w, r, err)
	}
}

// chatCardGet is GET /v1/chat-card?kind=&id=: one thing's card, live.
func (s *Server) chatCardGet(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	var c chatCard
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		c, err = s.chatCardTx(r.Context(), tx, p, q.Get("kind"), q.Get("id"))
		return err
	}); err != nil {
		s.cardFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// linkChatCard is GET /v1/link-chats/{link}/card?kind=&name=: a thing a
// message in the conversation named, by its number, as this side has it
// (their invoice is our bill), read live; only what is between the two.
func (s *Server) linkChatCard(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	family, name := refFamily(r.URL.Query().Get("kind")), strings.TrimSpace(r.URL.Query().Get("name"))
	var c chatCard
	if err := s.tx(r, func(tx pgx.Tx) error {
		l, err := links.GetTx(r.Context(), tx, link)
		if err != nil {
			return err
		}
		refs, err := linkRefsTx(r.Context(), tx, p, l)
		if err != nil {
			return err
		}
		for _, x := range refs {
			if refFamily(x.Kind) == family && strings.EqualFold(x.Name, name) {
				c, err = s.chatCardTx(r.Context(), tx, p, x.Kind, x.ID)
				return err
			}
		}
		return errNoCard
	}); err != nil {
		s.cardFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

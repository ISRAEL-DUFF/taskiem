package api

// Searching what may be tagged (docs/spec/menus-everywhere.md §7, E1): the
// picker's search box asks GET /v1/chat-refs (the Taskiem chat) or
// GET /v1/link-chats/{link}/refs (a business's conversation) with q, kind
// and cursor, and is answered from every record, not only the newest.
//
// Names of customers are sealed, so no index can find them: each list is
// read a page at a time, newest first, each row opened and matched, until
// enough are found or refSearchScan rows were read. The cursor says where
// each kind stopped, so "Show more" goes on from there.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

const (
	// refSearchPage is how many rows of a list are read at once.
	refSearchPage = 200
	// refSearchScan is the most rows of one kind a request reads.
	refSearchScan = 2000
	// refSearchLimit is how many are answered when none is asked.
	refSearchLimit = 20
)

// refKinds are the kinds that may be searched, in the picker's order;
// services and saved things (search everywhere, g-search-saved) only when
// asked for by name.
var (
	refKinds        = []string{"invoice", "bill", "order", "purchase", "customer", "service", "saved"}
	refDefaultKinds = refKinds[:5]
)

// refKindWords are a kind as typed, so "account" or "service" finds it.
var refKindWords = map[string]string{"invoice": "invoice", "bill": "bill", "order": "order", "purchase": "purchase order", "customer": "customer",
	"service": "service", "saved": "saved"}

// savedKindSearchWords are a saved thing's kind as typed.
var savedKindSearchWords = map[string]string{"bank_account": "bank account", "person": "person people", "place": "place address",
	"text": "text note", "file": "file photo document", "provider_account": "provider account bill"}

// refStatusWords are a status as the picker says it, so "unpaid" or
// "overdue" typed finds it.
var refStatusWords = map[string]string{
	"sent": "sent unpaid", "part_paid": "part paid unpaid", "overdue": "overdue unpaid", "paid": "paid",
	"open": "open unpaid", "paying": "paying unpaid", "disputed": "disputed", "void": "void cancelled",
	"draft": "draft", "received": "new received", "awaiting_payment": "awaiting payment", "accepted": "accepted",
	"dispatched": "dispatched on the way", "on_the_way": "on the way", "arrived": "arrived", "delivered": "delivered",
	"completed": "completed delivered", "cancelled": "cancelled", "declined": "declined",
}

// refWordsOf is what a ref's words are matched against.
func refWordsOf(r chatRef) string {
	kind := refKindWords[r.Kind]
	status := refStatusWords[r.Status]
	if status == "" {
		status = strings.ReplaceAll(r.Status, "_", " ")
	}
	words := []string{r.Name, r.Party, r.find, kind, status}
	if r.Kind == "saved" {
		words = append(words, savedKindSearchWords[r.SavedKind], r.KindName, map[string]string{"mine": "mine yours", "business": "business"}[r.Owner])
	}
	return strings.ToLower(strings.Join(words, " "))
}

// refMatches is whether every word typed is in the ref's words: at the
// start of one of them, or, for a reference like INV-0104, anywhere in it.
func refMatches(r chatRef, words []string) bool {
	if len(words) == 0 {
		return true
	}
	hay := refWordsOf(r)
	fields := strings.FieldsFunc(hay, func(c rune) bool { return unicode.IsSpace(c) || c == '·' || c == ',' })
	for _, w := range words {
		ok := false
		for _, f := range fields {
			if strings.HasPrefix(f, w) || (len(w) >= 2 && strings.Contains(f, w) && strings.ContainsAny(f, "0123456789-")) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// refSearch is a search asked for: words, kinds, where each kind is up to.
type refSearch struct {
	words []string
	kinds []string
	from  map[string]int
	limit int
	// paged: a cursor was given; a kind not in it has no more.
	paged bool
}

// refSearchOf reads q, kind (one or more, comma separated), cursor and
// limit; ok is false when none is given (the whole newest list, as
// before).
func refSearchOf(r *http.Request) (refSearch, bool, error) {
	v := r.URL.Query()
	if v.Get("q") == "" && v.Get("kind") == "" && v.Get("cursor") == "" && v.Get("limit") == "" {
		return refSearch{}, false, nil
	}
	s := refSearch{words: strings.Fields(strings.ToLower(v.Get("q"))), from: map[string]int{}, limit: refSearchLimit}
	if len(s.words) > 8 || len(v.Get("q")) > 100 {
		return s, true, fmt.Errorf("%w: search for a few words", errBadRequest)
	}
	for k := range strings.SplitSeq(v.Get("kind"), ",") {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if !slices.Contains(refKinds, k) {
			return s, true, fmt.Errorf("%w: kind is one of %s", errBadRequest, strings.Join(refKinds, ", "))
		}
		if !slices.Contains(s.kinds, k) {
			s.kinds = append(s.kinds, k)
		}
	}
	if len(s.kinds) == 0 {
		s.kinds = refDefaultKinds
	}
	if c := v.Get("cursor"); c != "" {
		for part := range strings.SplitSeq(c, ",") {
			k, n, ok := strings.Cut(part, ":")
			at, err := strconv.Atoi(n)
			if !ok || !slices.Contains(refKinds, k) || err != nil || at < 0 {
				return s, true, fmt.Errorf("%w: that cursor is not one this gave", errBadRequest)
			}
			s.from[k] = at
		}
		s.paged = true
	}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 50 {
			return s, true, fmt.Errorf("%w: limit is 1 to 50", errBadRequest)
		}
		s.limit = n
	}
	return s, true, nil
}

// refPage reads one page of a kind's list from an offset; nil when the
// person may not see that kind here.
type refPage func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error)

// found is a kind's matches from where it was asked, each with the
// position after it in that kind's list, and whether the list goes on.
type found struct {
	refs  []chatRef
	after []int
	more  bool
	end   int
}

// searchRefs reads each kind's pages from where the cursor left it,
// keeping what matches, and answers the newest of all kinds (each kind's
// in its own order) with the cursor for the rest.
func searchRefs(ctx context.Context, tx pgx.Tx, s refSearch, pages map[string]refPage) ([]chatRef, string, error) {
	got := map[string]*found{}
	for _, k := range s.kinds {
		page := pages[k]
		if page == nil {
			continue
		}
		if _, left := s.from[k]; s.paged && !left {
			continue
		}
		f := &found{}
		at := s.from[k]
		for read := 0; ; {
			if read >= refSearchScan {
				f.more = true
				break
			}
			rows, err := page(ctx, tx, at)
			if err != nil {
				return nil, "", err
			}
			for i, r := range rows {
				if refMatches(r, s.words) {
					f.refs = append(f.refs, r)
					f.after = append(f.after, at+i+1)
				}
			}
			at += len(rows)
			read += len(rows)
			if len(rows) < refSearchPage {
				break
			}
			if len(f.refs) > s.limit {
				f.more = true
				break
			}
		}
		f.end = at
		got[k] = f
	}
	// Newest first across kinds, each kind's taken from its front.
	out := []chatRef{}
	next := map[string]int{}
	taken := map[string]int{}
	for len(out) < s.limit {
		best := ""
		for _, k := range s.kinds {
			f := got[k]
			if f == nil || taken[k] >= len(f.refs) {
				continue
			}
			if best == "" || f.refs[taken[k]].When.After(got[best].refs[taken[best]].When) {
				best = k
			}
		}
		if best == "" {
			break
		}
		f := got[best]
		out = append(out, f.refs[taken[best]])
		next[best] = f.after[taken[best]]
		taken[best]++
	}
	var cursor []string
	for _, k := range s.kinds {
		f := got[k]
		if f == nil {
			continue
		}
		switch {
		case taken[k] < len(f.refs):
			// Found but not answered: from the first of those.
			from := s.from[k]
			if taken[k] > 0 {
				from = next[k]
			}
			cursor = append(cursor, k+":"+strconv.Itoa(from))
		case f.more:
			cursor = append(cursor, k+":"+strconv.Itoa(f.end))
		}
	}
	return out, strings.Join(cursor, ","), nil
}

// allRefPages are the Taskiem chat's lists, of what the person may see;
// own are the person's own saved things that may match words, read before
// (nil: saved things were not asked for).
func (s *Server) allRefPages(p *Principal, own *[]chatRef, words []string) map[string]refPage {
	tenant := p.TenantID
	pages := map[string]refPage{}
	if p.Can(PermInvoiceRead) && s.Invoices != nil {
		pages["invoice"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			list, err := s.Invoices.ListPage(ctx, tx, tenant, "invoice", refSearchPage, offset)
			out := make([]chatRef, 0, len(list))
			for _, x := range list {
				_, linked := links.BuyerOf(x.Customer.Key)
				when := x.CreatedAt
				if x.SentAt != nil {
					when = *x.SentAt
				}
				out = append(out, chatRef{Kind: "invoice", ID: x.ID.String(), Name: x.Number, Party: partyName(x.Customer.Name, linked),
					When: when, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status, find: x.Customer.Name})
			}
			return out, err
		}
		pages["bill"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			list, err := links.ListBillsPageTx(ctx, tx, "all", refSearchPage, offset)
			out := make([]chatRef, 0, len(list))
			for _, b := range list {
				out = append(out, chatRef{Kind: "bill", ID: b.ID.String(), Name: b.Reference, Party: b.SupplierName,
					When: b.CreatedAt, TotalKobo: koboPtr(b.AmountKobo), Status: b.Status})
			}
			return out, err
		}
	}
	if p.Can(PermOrders) && s.Orders != nil {
		pages["order"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			list, err := s.Orders.ListOrdersPage(ctx, tx, tenant, refSearchPage, offset)
			out := make([]chatRef, 0, len(list))
			for _, o := range list {
				out = append(out, chatRef{Kind: "order", ID: o.ID.String(), Name: o.Reference, Party: partyName(o.Customer.Name, o.Kind == "trade"),
					When: o.CreatedAt, TotalKobo: koboPtr(o.TotalKobo), Status: o.Status, find: o.Customer.Name})
			}
			return out, err
		}
	}
	if p.Can(PermTableRead) {
		pages["purchase"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			list, err := stock.PurchasesTx(ctx, tx, tenant, stock.PurchaseFilter{Limit: refSearchPage, Offset: offset})
			out := make([]chatRef, 0, len(list))
			for _, x := range list {
				out = append(out, chatRef{Kind: "purchase", ID: x.ID.String(), Name: x.Reference, Party: x.Supplier,
					When: x.CreatedAt, TotalKobo: koboPtr(x.TotalKobo), Status: x.Status})
			}
			return out, err
		}
	}
	// Our services, as their list (GET /v1/storefront/services).
	if p.Can(PermStorefrontManage) {
		var all []chatRef
		read := false
		pages["service"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			if !read {
				read = true
				items, err := storefront.Services(ctx, tx, tenant)
				if err != nil {
					return nil, err
				}
				for _, it := range items {
					all = append(all, chatRef{Kind: "service", ID: it.ID, Name: it.Name, TotalKobo: it.PriceKobo, Minutes: it.DurationMinutes,
						Status: map[bool]string{true: "available", false: "hidden"}[it.Available]})
				}
			}
			return pageOf(all, offset), nil
		}
	}
	// Saved things: the person's own (read before this transaction; own
	// nil when not asked) and their business's, by name and kind only.
	if person := savedSearcher(p); person != uuid.Nil && own != nil {
		pages["saved"] = savedPage(*own, tenant, person, words)
	}
	if p.Can(PermOrders) {
		pages["customer"] = func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			rows, err := tx.Query(ctx, `SELECT `+customerChatCols+` FROM customer_conversations c
				WHERE c.tenant_id = $1 AND c.linked_to IS NULL ORDER BY c.last_message_at DESC, c.id DESC LIMIT $2 OFFSET $3`, tenant, refSearchPage, offset)
			if err != nil {
				return nil, err
			}
			raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customerChatRaw, error) { return scanCustomerChatRaw(r) })
			if err != nil {
				return nil, err
			}
			out := make([]chatRef, 0, len(raws))
			for _, raw := range raws {
				v, err := s.openCustomerChat(ctx, tx, tenant, raw)
				if err != nil {
					return nil, err
				}
				out = append(out, chatRef{Kind: "customer", ID: v.ID.String(), Name: v.Name, Party: maskPhone(v.Phone), When: v.LastAt})
			}
			return out, nil
		}
	}
	return pages
}

// linkRefPages are a business's conversation's lists: only what is
// between the two businesses, as linkRefsTx.
func linkRefPages(p *Principal, l links.Link) map[string]refPage {
	tenant := p.TenantID
	sql := func(kind, q string, args []any) refPage {
		return func(ctx context.Context, tx pgx.Tx, offset int) ([]chatRef, error) {
			rows, err := tx.Query(ctx, q, append(slices.Clone(args), refSearchPage, offset)...)
			if err != nil {
				return nil, err
			}
			return pgx.CollectRows(rows, func(r pgx.CollectableRow) (chatRef, error) {
				x := chatRef{Kind: kind, Party: l.Name}
				var id uuid.UUID
				var total int64
				err := r.Scan(&id, &x.Name, &total, &x.Status, &x.When)
				x.ID, x.TotalKobo = id.String(), &total
				return x, err
			})
		}
	}
	pages := map[string]refPage{}
	if p.Can(PermInvoiceRead) {
		pages["invoice"] = sql("invoice", `SELECT id, number, total_kobo, status, COALESCE(sent_at, created_at) FROM customer_invoices
			WHERE tenant_id = $1 AND customer_key = $2 AND kind = 'invoice' AND status <> 'draft'
			ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, []any{tenant, links.KeyPrefix + l.Business.String()})
		pages["bill"] = sql("bill", `SELECT id, reference, amount_kobo, status, created_at FROM bills
			WHERE tenant_id = $1 AND link_id = $2 ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, []any{tenant, l.ID})
	}
	if p.Can(PermOrders) {
		pages["order"] = sql("order", `SELECT o.id, o.reference, o.total_kobo, o.status, o.created_at FROM storefront_orders o JOIN link_orders x ON x.order_id = o.id
			WHERE x.tenant_id = $1 AND x.link_id = $2 ORDER BY o.created_at DESC, o.id LIMIT $3 OFFSET $4`, []any{tenant, l.ID})
	}
	if p.Can(PermTableRead) {
		pages["purchase"] = sql("purchase", `SELECT o.id, o.reference, o.total_kobo, o.status, o.created_at FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
			WHERE o.tenant_id = $1 AND s.link_id = $2 AND o.status <> 'drafted' ORDER BY o.created_at DESC, o.id LIMIT $3 OFFSET $4`, []any{tenant, l.ID})
	}
	return pages
}

// answerRefSearch answers a search of the refs: {refs, cursor}, the cursor
// empty when there is no more.
func (s *Server) answerRefSearch(w http.ResponseWriter, r *http.Request, q refSearch, pagesOf func(pgx.Tx) (map[string]refPage, error)) {
	var out []chatRef
	var cursor string
	if err := s.tx(r, func(tx pgx.Tx) error {
		pages, err := pagesOf(tx)
		if err != nil {
			return err
		}
		out, cursor, err = searchRefs(r.Context(), tx, q, pages)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": out, "cursor": cursor})
}

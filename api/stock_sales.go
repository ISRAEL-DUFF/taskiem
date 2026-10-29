package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Sales (docs/spec/stock.md §5, K2): the web's and the API's door to
// recording a sale and reading a day's, the evening total sent in chat,
// and the sales in the morning's money summary. Chat's door is
// whatsapp_stock.go.

// saleItem is a line of a sale asked for: an item by its row id, how
// many, and its price (the catalogue's when nil).
type saleItem struct {
	ItemID    string `json:"item_id"`
	Quantity  int64  `json:"quantity"`
	PriceKobo *int64 `json:"price_kobo"`
}

// errNoPrice: an item with no price, in a sale with no amount.
var errNoPrice = errors.New("stock: an item has no price")

// saleLines prices a sale's lines from the catalogue's items: names and
// units from the table, prices as given or the item's. With no amount,
// every line needs a price.
func saleLines(items []stock.Item, asked []saleItem, amount *int64) ([]stock.SaleLine, error) {
	byID := make(map[string]stock.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	lines := make([]stock.SaleLine, 0, len(asked))
	for _, a := range asked {
		it, ok := byID[a.ItemID]
		if !ok {
			return nil, errNoItem
		}
		price := int64(0)
		switch {
		case a.PriceKobo != nil:
			price = *a.PriceKobo
		case it.PriceKobo != nil:
			price = *it.PriceKobo
		case amount == nil:
			return nil, fmt.Errorf("%w: %s", errNoPrice, it.Name)
		}
		lines = append(lines, stock.SaleLine{ItemID: it.ID, Item: it.Name, Quantity: a.Quantity, Unit: it.Unit, PriceKobo: price})
	}
	return lines, nil
}

// recordSale is POST /v1/stock/sales, with an Idempotency-Key: items, the
// amount (their prices when absent), and how it was paid; entry_id gives a
// terminal's sale its items.
func (s *Server) recordSale(w http.ResponseWriter, r *http.Request) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Lines      []saleItem `json:"lines"`
		AmountKobo *int64     `json:"amount_kobo"`
		PaidHow    string     `json:"paid_how"`
		Note       string     `json:"note"`
		EntryID    *uuid.UUID `json:"entry_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	_, items, err := s.stockItems(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	lines, err := saleLines(items, in.Lines, in.AmountKobo)
	if errors.Is(err, errNoPrice) {
		s.fail(w, r, fmt.Errorf("%w: %s has no price: give the amount", errBadRequest, strings.TrimPrefix(err.Error(), errNoPrice.Error()+": ")))
		return
	}
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	x, err := s.Stock.RecordSale(r.Context(), p.TenantID, stock.SaleIn{Lines: lines, AmountKobo: in.AmountKobo, PaidHow: in.PaidHow, Note: in.Note,
		Actor: p.Actor(), Key: "api-" + key, Entry: in.EntryID}, s.moneyLog())
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "stock.sale", x.Reference, map[string]any{"sale_id": x.ID, "amount_kobo": x.AmountKobo, "paid_how": x.PaidHow})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sale": x})
}

// lagosDay reads ?day=2026-10-29, today in Lagos when absent.
func lagosDay(v string) (time.Time, error) {
	if v == "" {
		now := time.Now().In(storefront.Lagos)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos), nil
	}
	return time.ParseInLocation("2006-01-02", v, storefront.Lagos)
}

// getSales is GET /v1/stock/sales?day=: a day's sales added up, with the
// terminal's sales that have no items.
func (s *Server) getSales(w http.ResponseWriter, r *http.Request) {
	day, err := lagosDay(r.URL.Query().Get("day"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "day is a date like 2026-10-29")
		return
	}
	var d stock.Day
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		d, err = stock.DayTx(r.Context(), tx, principalFrom(r.Context()).TenantID, day)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// --- in chat: the evening total and the morning's line ---

// salesStale is how long a queued evening total may wait.
const salesStale = 12 * time.Hour

// salesBy says a day's takings by how they were paid, the ways with none
// left out: "cash ₦45,000 · transfer ₦12,000".
func salesBy(l lang.Tag, d stock.Day) string {
	var parts []string
	for _, how := range []string{stock.Cash, stock.Transfer, stock.POS} {
		if d.ByHow[how] > 0 {
			parts = append(parts, tr(l, "sales.by_"+how)+" "+storefront.FormatNaira(d.ByHow[how]))
		}
	}
	return strings.Join(parts, " · ")
}

// EveningText is the evening total in words: the total, by how paid, what
// sold most, what waits for its money, the terminal's sales with no items,
// and what is low now.
func EveningText(l lang.Tag, d stock.Day, low []stock.Item) string {
	lines := []string{tr(l, "sales.evening.title", "total", storefront.FormatNaira(d.TotalKobo), "count", strconv.Itoa(d.Count))}
	if by := salesBy(l, d); by != "" {
		lines = append(lines, by)
	}
	if len(d.Top) > 0 {
		var top []string
		for _, t := range d.Top[:min(3, len(d.Top))] {
			top = append(top, (stock.Item{Unit: t.Unit}).Quantity(t.Quantity)+" "+whatsapp.SafeText(t.Item))
		}
		lines = append(lines, tr(l, "sales.evening.top", "list", strings.Join(top, ", ")))
	}
	if d.Waiting > 0 {
		lines = append(lines, tr(l, "sales.evening.waiting", "count", strconv.Itoa(d.Waiting)))
	}
	if len(d.Unsold) > 0 {
		var sum int64
		for _, u := range d.Unsold {
			sum += u.AmountKobo
		}
		lines = append(lines, tr(l, "sales.evening.unsold", "count", strconv.Itoa(len(d.Unsold)), "amount", storefront.FormatNaira(sum)))
	}
	if len(low) > 0 {
		var names []string
		for _, it := range low[:min(5, len(low))] {
			names = append(names, whatsapp.SafeText(it.Name)+" ("+it.Quantity(*it.Stock)+")")
		}
		lines = append(lines, tr(l, "sales.evening.low", "list", strings.Join(names, ", ")))
	}
	return strings.Join(lines, "\n")
}

// sendSalesEveningItem sends a queued evening total (stock.OutboxEvening)
// to its person, written from the day's sales now; outside WhatsApp's
// 24-hour window as the alert template. Dropped when the person no longer
// gets the money summary, or it waited too long.
func (s *Server) sendSalesEveningItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Digest == nil || it.User == uuid.Nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business string
	var d stock.Day
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var period, sentAt time.Time
		if err := tx.QueryRow(ctx, `SELECT period, sent_at FROM money_digests WHERE id = $1 AND kind = $2`, *it.Digest, stock.EveningKind).Scan(&period, &sentAt); err != nil {
			return err
		}
		if time.Since(sentAt) > salesStale {
			return fmt.Errorf("%w: the evening total waited too long", whatsapp.ErrNothingToSend)
		}
		ok, err := moneydigest.Gets(ctx, tx, it.Tenant, it.User, moneydigest.Daily)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: the person no longer gets the money summary", whatsapp.ErrNothingToSend)
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		d, err = stock.DayTx(ctx, tx, it.Tenant, time.Date(period.Year(), period.Month(), period.Day(), 12, 0, 0, 0, storefront.Lagos))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the evening total is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	var low []stock.Item
	if _, items, err := s.stockItems(ctx, it.Tenant); err == nil {
		low = stock.Low(items)
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	text := ch.Prefix(business) + EveningText(l, d, low)
	title := tr(l, "sales.evening.title", "total", storefront.FormatNaira(d.TotalKobo), "count", strconv.Itoa(d.Count))
	link := "-"
	if s.PublicURL != "" {
		link = s.PublicURL + "/storefront/stock"
	}
	alert := whatsapp.TplAlert
	_, err = ch.Send(ctx, addr, whatsapp.Message{Text: text, Template: &alert, Vars: map[string]string{"tenant": business, "title": whatsapp.SafeText(title), "link": link},
		Tenant: it.Tenant})
	if errors.Is(err, telegram.ErrBlocked) {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return err
}

// salesDailyLine is the sales recorded on a day, for the morning's money
// summary; "" when there were none.
func salesDailyLine(l lang.Tag, d stock.Day) string {
	if d.Count == 0 {
		return ""
	}
	return tr(l, "money.daily.sales", "total", storefront.FormatNaira(d.TotalKobo), "count", strconv.Itoa(d.Count), "by", salesBy(l, d))
}

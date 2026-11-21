package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Saving after an action (saved objects S3, begun in S2's review): once an
// invoice is sent to a customer typed by hand, "Save Acme Ltd as @acme-ltd
// for next time?" with Save, Keep it to myself and Cancel; any other
// message lets the offer go and is read as itself.

// offerSaveCustomer says what was done and offers to save its customer as
// a business's saved person: after the first invoice to them, and, as a
// suggestion, after the third (saved objects S3: suggest, never surprise);
// at no other time. False (nothing said) then, or when no name fits or one
// by the name is saved already.
func (s *Server) offerSaveCustomer(ctx context.Context, c *chat, customerID, title, addr string, email bool, said string) (bool, error) {
	var count int
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM customer_invoices WHERE tenant_id = $1 AND customer_id = $2 AND voided_at IS NULL`, c.tenant.ID, customerID).Scan(&count)
	}); err != nil {
		return false, err
	}
	if count != 1 && count != suggestAfter {
		return false, nil
	}
	name := saved.NameFor(title)
	if name == "" || c.p == nil {
		return false, nil
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil || len(got) > 0 {
		return false, err
	}
	key := "phone"
	if email {
		key = "email"
	}
	sealed, err := s.sealChatSave(ctx, c, true, map[string]string{key: addr})
	if err != nil {
		return false, err
	}
	sv := &chatSave{Name: name, Title: title, Kind: saved.KindPerson, Business: true, Sealed: sealed, Offer: true}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Save: sv}, waPendingTTL); err != nil {
		return false, err
	}
	ask := c.t("wa.saved.offer", "title", title, "name", name)
	if count == suggestAfter {
		ask = c.t("wa.saved.suggest", "title", title, "count", strconv.Itoa(count), "name", name)
	}
	return true, c.say(ctx, s, said+"\n\n"+ask, saveButtons(c, sv)...)
}

// suggestAfter is how many times something is done before saving it is
// suggested.
const suggestAfter = 3

// saveOfferWord says whether a reply answers a save waiting for one.
func saveOfferWord(cmd string) bool {
	switch cmd {
	case "yes", "y", "save", "confirm", "mine", "keep it to myself", "keep it to me", "mine only", "no", "n":
		return true
	}
	return false
}

// saveOrderCustomer is POST /v1/storefront/orders/{reference}/save-customer
// {name?}: the order's customer kept as one of the business's saved people
// (saved objects S3: after a new customer's first order), with their name
// as its title, their phone, email and address; read here, so the page
// that offers it never holds them in full. The name is the one given, else
// made from theirs. 409 when it is taken.
func (s *Server) saveOrderCustomer(w http.ResponseWriter, r *http.Request) {
	if s.Orders == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var o storefront.Order
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		o, err = s.Orders.GetOrder(r.Context(), tx, p.TenantID, chi.URLParam(r, "reference"))
		if errors.Is(err, storefront.ErrNoOrder) {
			return pgx.ErrNoRows
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	cu := o.Customer
	title := strings.TrimSpace(cu.Name)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = saved.NameFor(title)
	}
	address := strings.TrimSpace(cu.Address)
	if l := strings.TrimSpace(cu.Landmark); l != "" && address != "" {
		address += " (" + l + ")"
	}
	details := map[string]any{}
	for k, v := range map[string]string{"phone": cu.Phone, "email": cu.Email, "address": address} {
		if v = strings.TrimSpace(v); v != "" {
			details[k] = v
		}
	}
	if a, ok := details["address"].(string); ok && len([]rune(a)) > saved.MaxLine {
		details["address"] = string([]rune(a)[:saved.MaxLine])
	}
	ow := savedOwner{tenant: p.TenantID, by: p.UserID}
	x, _, err := s.addSavedNew(r.Context(), savedWhoOf(r), ow, savedIn{Name: &name, Title: &title, Kind: saved.KindPerson, Details: details})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"saved": x.view(ow, true)})
}

// customerOrdersTx is how many orders the customer of the order with ref
// has made with the business, this one with them.
func customerOrdersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM storefront_orders o WHERE o.tenant_id = $1
		AND o.subject_id = (SELECT subject_id FROM storefront_orders WHERE tenant_id = $1 AND reference = $2)`, tenant, ref).Scan(&n)
	return n, err
}

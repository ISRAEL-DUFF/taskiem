package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/wd"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Saving after an action (saved objects S3, begun in S2's review): once an
// invoice is sent to a customer typed by hand, "Save Acme Ltd as @acme-ltd
// for next time?" with Save, Keep it to myself and Cancel; any other
// message lets the offer go and is read as itself.

// offerSaveCustomer says what was done and offers to save its customer as
// a saved person: the business's for those who may manage its saved
// things, else the person's own (never on a business's own app). After the
// first invoice sent to them, and, as a suggestion, after the third (saved
// objects S3: suggest, never surprise); at no other time. False (nothing
// said) then, or when no name fits, one by the name is saved already, or
// their phone or email is saved already under another.
func (s *Server) offerSaveCustomer(ctx context.Context, c *chat, customerID, title, addr string, email bool, said string) (bool, error) {
	var count int
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM customer_invoices WHERE tenant_id = $1 AND customer_id = $2 AND voided_at IS NULL AND sent_at IS NOT NULL`, c.tenant.ID, customerID).Scan(&count)
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
	business := c.p.Can(PermSavedManage)
	if !business && ownAppChat(c) {
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
	// Their phone or email saved already, under another name: no second.
	for _, o := range []savedOwner{{tenant: c.tenant.ID, by: c.p.UserID}, {person: c.p.UserID, by: c.p.UserID}} {
		if o.mine() && ownAppChat(c) {
			continue
		}
		var n int
		err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			return tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE `+where+` AND kind = 'person' AND details->>$2 <> '' AND lower(details->>$2) = lower($3)`,
				owner, key, addr).Scan(&n)
		})
		if err != nil || n > 0 {
			return false, err
		}
	}
	sealed, err := s.sealChatSave(ctx, c, business, map[string]string{key: addr})
	if err != nil {
		return false, err
	}
	sv := &chatSave{Name: name, Title: title, Kind: saved.KindPerson, Business: business, Sealed: sealed, Offer: true}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Save: sv}, waPendingTTL); err != nil {
		return false, err
	}
	ask := c.t("wa.saved.offer", "title", title, "name", name)
	if count == suggestAfter {
		ask = c.t("wa.saved.suggest", "title", title, "count", strconv.Itoa(count), "name", name)
	}
	return true, c.say(ctx, s, said+"\n\n"+ask, saveButtons(c, sv)...)
}

// An offer's buttons.
const (
	offerSave = "offer:save"
	offerMine = "offer:mine"
)

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
	ref := chi.URLParam(r, "reference")
	var o storefront.Order
	var savedAs string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		o, err = s.Orders.GetOrder(r.Context(), tx, p.TenantID, ref)
		if errors.Is(err, storefront.ErrNoOrder) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		savedAs, err = customerSavedTx(r.Context(), tx, p.TenantID, o.Customer)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	refuse := func(code, msg string, extra ...any) {
		body := map[string]any{"error": msg, "code": code}
		for i := 0; i+1 < len(extra); i += 2 {
			body[extra[i].(string)] = extra[i+1]
		}
		writeJSON(w, http.StatusConflict, body)
	}
	cu := o.Customer
	// A linked business is not a saved thing's to copy (§11); an erased
	// customer leaves nothing to keep.
	if o.Kind == "trade" {
		refuse("not_a_customer", "This order is from a business linked with yours: it is in your links, not your saved things.")
		return
	}
	phone, _ := phonenum.Normalise(cu.Phone)
	email := strings.TrimSpace(cu.Email)
	if strings.TrimSpace(cu.Name) == "" && phone == "" && email == "" && strings.TrimSpace(cu.Address) == "" {
		refuse("nothing_to_save", "This customer's details are gone, so there is nothing to save.")
		return
	}
	if savedAs != "" {
		refuse("saved_already", "This customer is saved already as @"+savedAs+".", "saved_as", savedAs)
		return
	}
	title := strings.TrimSpace(cu.Name)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = saved.NameFor(title)
	}
	if name == "" {
		refuse("name_needed", "Give it a name to call it up by.", "name_needed", true)
		return
	}
	if title == "" {
		title = name
	}
	// The address as the customer gave it; the landmark is the rider's.
	details := map[string]any{}
	for k, v := range map[string]string{"phone": phone, "email": email, "address": strings.TrimSpace(cu.Address)} {
		if v != "" {
			details[k] = v
		}
	}
	if a, ok := details["address"].(string); ok && len([]rune(a)) > saved.MaxLine {
		details["address"] = string([]rune(a)[:saved.MaxLine])
	}
	if t := []rune(title); len(t) > saved.MaxTitle {
		title = string(t[:saved.MaxTitle])
	}
	ow := savedOwner{tenant: p.TenantID, by: p.UserID}
	x, _, err := s.addSavedNew(r.Context(), savedWhoOf(r), ow, savedIn{Name: &name, Title: &title, Kind: saved.KindPerson, Details: details})
	if errors.Is(err, saved.ErrInvalid) && details["email"] != nil {
		// An email the customer typed that is not one: kept without it.
		delete(details, "email")
		x, _, err = s.addSavedNew(r.Context(), savedWhoOf(r), ow, savedIn{Name: &name, Title: &title, Kind: saved.KindPerson, Details: details})
	}
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	// Their details, masked on the order for those who may not reveal them,
	// are kept in full: recorded as a reveal.
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "pii.reveal", "storefront_order:"+ref, map[string]any{"saved_as": x.Name})
	}); err != nil {
		s.Logger.Warn("saved: recording a customer saved", "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"saved": x.view(ow, true)})
}

// customerOrdersTx is how many orders the customer of the order with ref
// has made with the business, this one with them.
// An order forgotten under the retention rules has no customer: 0.
func customerOrdersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM storefront_orders o WHERE o.tenant_id = $1
		AND o.subject_id = (SELECT subject_id FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 AND subject_id NOT LIKE 'retention:%')`, tenant, ref).Scan(&n)
	return n, err
}

// customerSavedTx is the name of the business's saved person the customer
// is (the same phone, else email), "" when none: the order page offers to
// save only a customer not saved yet.
func customerSavedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, cu storefront.Customer) (string, error) {
	phone, _ := phonenum.Normalise(cu.Phone)
	email := strings.ToLower(strings.TrimSpace(cu.Email))
	if phone == "" && email == "" {
		return "", nil
	}
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM saved_objects WHERE tenant_id = $1 AND person_id IS NULL AND archived_at IS NULL AND kind = 'person'
		AND ((details->>'phone' <> '' AND details->>'phone' = $2) OR (details->>'email' <> '' AND lower(details->>'email') = $3)) ORDER BY created_at LIMIT 1`, tenant, phone, email).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// publishMayRead refuses publishing a workflow that lists the business's
// customers, with their phones and emails (storefront@1 recent_customers;
// saved objects S3), by someone who may not see orders: what it reads, it
// can send anywhere.
func publishMayRead(ctx context.Context, tx pgx.Tx, p *Principal, wf uuid.UUID, v int) error {
	if p.Can(PermStorefrontManage) || p.Can(PermOrderHandle) {
		return nil
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&raw); err != nil {
		return err
	}
	def, err := wd.Load(raw)
	if err != nil {
		return nil //nolint:nilerr // a definition that does not load is refused by its own checks
	}
	lists := false
	def.Walk(func(st *wd.Step) {
		if st.Type == "connector" && strings.HasPrefix(st.Connector, "storefront@") && st.Action == "recent_customers" {
			lists = true
		}
	})
	if lists {
		return fmt.Errorf("%w: a workflow that lists your customers (storefront recent_customers) is published by someone who may handle orders", errForbidden)
	}
	return nil
}

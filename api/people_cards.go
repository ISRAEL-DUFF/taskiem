package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Businesses inside people chats (people-chatting §10.1) and money between
// people (§11 A). A product, a business, an order or a delivery is shared
// as a card carrying only what is needed to show it and open it; opening it
// reads the live data through the doors as the person opening it (an order
// only when it is theirs, else only what was shared). A "your share" card
// carries an amount, what it is for and the payer's bank account as they
// typed or saved it, never checked by Taskiem, which moves no money; either
// side marks it paid.

// payoutAccount is a person's own bank account for being paid back.
type payoutAccount struct {
	Bank          string `json:"bank"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

var nubanOnly = regexp.MustCompile(`^[0-9]{10}$`)

func (a *payoutAccount) check() error {
	a.Bank, a.AccountName = strings.TrimSpace(a.Bank), strings.TrimSpace(a.AccountName)
	a.AccountNumber = strings.NewReplacer(" ", "", "-", "").Replace(a.AccountNumber)
	if a.Bank == "" || len([]rune(a.Bank)) > 80 || a.AccountName == "" || len([]rune(a.AccountName)) > 120 || !nubanOnly.MatchString(a.AccountNumber) {
		return customerSendErr{http.StatusBadRequest, "A bank account is the bank's name, a 10-digit account number and the name on the account."}
	}
	return nil
}

// peopleCard is a card in a people chat.
type peopleCard struct {
	// Type: product, business, order, delivery or split ("your share").
	Type  string `json:"type"`
	Title string `json:"title"`
	// The business shared, or the business of the product, order or
	// delivery.
	BusinessID *uuid.UUID `json:"business_id,omitempty"`
	Business   string     `json:"business,omitempty"`
	Area       string     `json:"area,omitempty"`
	// A product: its id, and its price when shared.
	ItemID    string `json:"item_id,omitempty"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
	// An order or a delivery: its reference and state when shared, and a
	// delivery's progress then.
	Reference string            `json:"reference,omitempty"`
	State     string            `json:"state,omitempty"`
	Delivery  *deliveryProgress `json:"delivery,omitempty"`
	// A share: how much, what for, and the account to pay it to, as the
	// one who paid typed or saved it.
	AmountKobo *int64         `json:"amount_kobo,omitempty"`
	What       string         `json:"what,omitempty"`
	PayTo      *payoutAccount `json:"pay_to,omitempty"`
	// A share marked paid (by either side), and whether by you.
	Paid     bool       `json:"paid,omitempty"`
	PaidAt   *time.Time `json:"paid_at,omitempty"`
	PaidByMe *bool      `json:"paid_by_me,omitempty"`
}

// cardRequest is a card to share.
type cardRequest struct {
	Type       string         `json:"type"`
	BusinessID *uuid.UUID     `json:"business_id"`
	ItemID     string         `json:"item_id"`
	Reference  string         `json:"reference"`
	AmountKobo int64          `json:"amount_kobo"`
	What       string         `json:"what"`
	PayTo      *payoutAccount `json:"pay_to"`
}

// enterCardTx goes through the card door into a business, as the person.
func enterCardTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, what string) error {
	_, err := tx.Exec(ctx, `SELECT taskiem_people_card_enter($1, $2, $3)`, person, business, what)
	return doorErr(err)
}

// productTx is one product of a business's published shop, through the
// card door.
func (s *Server) productTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, item string) (shopItem, chatlinks.Card, error) {
	if err := enterCardTx(ctx, tx, person, business, "product"); err != nil {
		return shopItem{}, chatlinks.Card{}, err
	}
	card, err := chatlinks.CardTx(ctx, tx, business, s.previewDomain(), time.Now())
	if err != nil {
		return shopItem{}, card, err
	}
	shop, err := theirShopTx(ctx, tx, business, card.Name)
	if err != nil {
		return shopItem{}, card, err
	}
	for _, it := range shop.Items {
		if it.ID == item {
			return it, card, nil
		}
	}
	return shopItem{}, card, customerSendErr{http.StatusNotFound, "This product is not in their shop now."}
}

// orderOfTx is one of the person's own orders (Orders on You's row), or
// nil when it is not theirs.
func (s *Server) orderOfTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, ref string) (*personOrder, error) {
	list, err := s.personOrdersTx(ctx, tx, person, &business, &ref, nil, nil, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// deliveryOfTx is the rider's progress on one of the person's orders,
// through the order door.
func (s *Server) deliveryOfTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, ref string) (*deliveryProgress, error) {
	if err := s.enterOrderTx(ctx, tx, person, business, ref, "customer.order_detail"); err != nil {
		return nil, err
	}
	if s.Orders == nil {
		return nil, nil
	}
	o, err := s.Orders.GetOrder(ctx, tx, business, ref)
	if errors.Is(err, storefront.ErrNoOrder) {
		return nil, errDoorNone
	}
	if err != nil || o.Fulfilment != "delivery" {
		return nil, err
	}
	return deliveryProgressTx(ctx, tx, business, o.ID)
}

func orderTitle(o personOrder) string {
	if o.Kind == "booking" {
		return "Booking " + o.Reference
	}
	return "Order " + o.Reference
}

// makeCard makes the card the person shares, from what they can see
// themself: a product of a business's published shop, a business that
// shows itself, one of their own orders, its delivery, or a share.
func (s *Server) makeCard(ctx context.Context, person uuid.UUID, in cardRequest) (*peopleCard, error) {
	c := &peopleCard{Type: in.Type}
	needBusiness := func() error {
		if in.BusinessID == nil {
			return customerSendErr{http.StatusBadRequest, "business_id is required for this card"}
		}
		b := *in.BusinessID
		c.BusinessID = &b
		return nil
	}
	switch in.Type {
	case "product":
		if err := needBusiness(); err != nil {
			return nil, err
		}
		if in.ItemID == "" || len(in.ItemID) > 100 {
			return nil, customerSendErr{http.StatusBadRequest, "item_id is required for a product card"}
		}
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			it, card, err := s.productTx(ctx, tx, person, *in.BusinessID, in.ItemID)
			if err != nil {
				return err
			}
			c.ItemID, c.Title, c.PriceKobo, c.Business, c.Area = it.ID, it.Name, it.PriceKobo, card.Name, card.Area
			return nil
		})
		return c, err
	case "business":
		if err := needBusiness(); err != nil {
			return nil, err
		}
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			if err := enterCardTx(ctx, tx, person, *in.BusinessID, "business"); err != nil {
				return err
			}
			card, err := chatlinks.CardTx(ctx, tx, *in.BusinessID, s.previewDomain(), time.Now())
			c.Title, c.Business, c.Area = card.Name, card.Name, card.Area
			return err
		})
		return c, err
	case "order", "delivery":
		if err := needBusiness(); err != nil {
			return nil, err
		}
		if in.Reference == "" || len(in.Reference) > 80 {
			return nil, customerSendErr{http.StatusBadRequest, "reference is required for an order or delivery card"}
		}
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			o, err := s.orderOfTx(ctx, tx, person, *in.BusinessID, in.Reference)
			if err != nil {
				return err
			}
			if o == nil {
				return customerSendErr{http.StatusNotFound, "Only your own orders can be shared."}
			}
			c.Business, c.Reference, c.State, c.Title = o.Business, o.Reference, o.State, orderTitle(*o)
			if in.Type == "order" {
				return nil
			}
			if o.Earlier {
				return errOrderEarlier
			}
			if c.Delivery, err = s.deliveryOfTx(ctx, tx, person, *in.BusinessID, in.Reference); err != nil {
				return err
			}
			if c.Delivery == nil {
				return customerSendErr{http.StatusConflict, "No rider has this order yet: share the order instead."}
			}
			c.Title = "Delivery of " + strings.ToLower(orderTitle(*o)[:1]) + orderTitle(*o)[1:]
			return nil
		})
		return c, err
	case "split":
		in.What = strings.TrimSpace(in.What)
		if in.AmountKobo < 100 || in.AmountKobo > 100_000_000_000 {
			return nil, customerSendErr{http.StatusBadRequest, "amount_kobo is from ₦1 to ₦1,000,000,000"}
		}
		if in.What == "" || len([]rune(in.What)) > 120 {
			return nil, customerSendErr{http.StatusBadRequest, "what it is for is required (up to 120 characters)"}
		}
		amount := in.AmountKobo
		c.AmountKobo, c.What = &amount, in.What
		c.Title = "Your share: " + storefront.FormatNaira(amount)
		if in.PayTo != nil {
			if err := in.PayTo.check(); err != nil {
				return nil, err
			}
			c.PayTo = in.PayTo
			return c, nil
		}
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			a, err := s.payoutAccountTx(ctx, tx, person)
			if err != nil {
				return err
			}
			if a == nil {
				return customerSendErr{http.StatusBadRequest, "Add the bank account to be paid to (pay_to), or save yours first."}
			}
			c.PayTo = a
			return nil
		})
		return c, err
	}
	return nil, customerSendErr{http.StatusBadRequest, "a card's type is product, business, order, delivery or split"}
}

// liveCard is what a card shows when opened now.
type liveCard struct {
	// Yours: an order or a delivery that is the person's own, read live;
	// otherwise only what was shared is shown (live is null).
	Yours bool `json:"yours"`
	// A product as its shop has it now, or a business as it shows itself.
	Product  *shopItem       `json:"product,omitempty"`
	Business *chatlinks.Card `json:"business,omitempty"`
	// An order of the person's own, as Orders on You has it.
	Order *personOrder `json:"order,omitempty"`
	// Its rider's progress now.
	Delivery *deliveryProgress `json:"delivery,omitempty"`
}

// openPeopleCard is GET /v1/me/people/{id}/messages/{message}/card: the
// card as shared, and as it is now when the person opening it may see it:
// a product's current price, a business's card, an order only when it is
// theirs. 403 blocked when the business blocked them.
func (s *Server) openPeopleCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var card *peopleCard
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.Kind != "card" || m.Deleted {
			return errDoorNone
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		v, _ := m.view(key, person)
		card = v.Card
		return nil
	})
	if err == nil && card == nil {
		err = errDoorNone
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	live := liveCard{}
	// Each look at a business through a door is in its audit chain:
	// limited per person, as looks at orders are.
	if card.BusinessID != nil && !s.orderLookAllowed(w, person) {
		return
	}
	switch card.Type {
	case "product":
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			it, _, err := s.productTx(ctx, tx, person, *card.BusinessID, card.ItemID)
			var se customerSendErr
			if errors.As(err, &se) && se.code == http.StatusNotFound {
				return nil // no longer sold: what was shared only
			}
			if err == nil {
				live.Product = &it
			}
			return err
		})
	case "business":
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			if err := enterCardTx(ctx, tx, person, *card.BusinessID, "business"); err != nil {
				return err
			}
			b, err := chatlinks.CardTx(ctx, tx, *card.BusinessID, s.previewDomain(), time.Now())
			live.Business = &b
			return err
		})
	case "order", "delivery":
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			o, err := s.orderOfTx(ctx, tx, person, *card.BusinessID, card.Reference)
			if err != nil || o == nil {
				return err // not theirs: what was shared only
			}
			live.Yours, live.Order = true, o
			if card.Type == "delivery" && !o.Earlier {
				live.Delivery, err = s.deliveryOfTx(ctx, tx, person, *card.BusinessID, card.Reference)
			}
			return err
		})
	}
	if errors.Is(err, errDoorNone) {
		err = nil // the business no longer shows itself: what was shared only
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"card": card, "live": live})
}

// markSplitPaid is POST /v1/me/people/{id}/messages/{message}/paid
// {paid}: either side marks a share paid (or not yet); both copies show
// it. Taskiem moves no money and checks no payment.
func (s *Server) markSplitPaid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Paid bool `json:"paid"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var out peopleMessage
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		c, err := peopleCopyTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		m, err := ownMessageTx(ctx, tx, person, c, chi.URLParam(r, "message"))
		if err != nil {
			return err
		}
		if m.cardType == nil || *m.cardType != "split" || m.Deleted {
			return customerSendErr{http.StatusConflict, "Only a share card is marked paid."}
		}
		if _, err := tx.Exec(ctx, `UPDATE people_messages SET paid_at = CASE WHEN $3 THEN now() END, paid_by_me = CASE WHEN $3 THEN true END
			WHERE person_id = $1 AND id = $2`, person, m.ID, in.Paid); err != nil {
			return err
		}
		peer, ok, err := reachIfDeliveredTx(ctx, tx, c, m)
		if err != nil {
			return err
		}
		if ok {
			if _, err := tx.Exec(ctx, `UPDATE people_messages SET paid_at = CASE WHEN $4 THEN now() END, paid_by_me = CASE WHEN $4 THEN false END
				WHERE person_id = $1 AND conversation_id = $2 AND id = $3`, peer, c.Conversation, m.ID, in.Paid); err != nil {
				return err
			}
		}
		row, err := ownMessageTx(ctx, tx, person, c, m.ID.String())
		if err != nil {
			return err
		}
		key, err := s.keys().KeyTx(ctx, tx, person, false)
		if err != nil {
			return err
		}
		out, _ = row.view(key, person)
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// payoutAccountTx is the person's saved account, opened; nil without one.
func (s *Server) payoutAccountTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (*payoutAccount, error) {
	var sealed []byte
	err := tx.QueryRow(ctx, `SELECT sealed FROM person_payout_accounts WHERE person_id = $1`, person).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := s.keys().KeyTx(ctx, tx, person, false)
	if err != nil {
		return nil, err
	}
	raw, err := peoplechats.Open(key, person, "payout", sealed)
	if err != nil {
		return nil, err
	}
	var a payoutAccount
	return &a, json.Unmarshal(raw, &a)
}

// getPayoutAccount is GET /v1/me/payout-account: the bank account the
// person saved for being paid back ({account: null} without one).
func (s *Server) getPayoutAccount(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var a *payoutAccount
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		a, err = s.payoutAccountTx(r.Context(), tx, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": a, "checked": false,
		"words": "Taskiem does not check this account and moves no money: friends pay you with their own bank app."})
}

// putPayoutAccount is PUT /v1/me/payout-account {bank, account_number,
// account_name}: saved on the person's side, sealed under their key.
func (s *Server) putPayoutAccount(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in payoutAccount
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		key, err := s.keys().KeyTx(r.Context(), tx, person, true)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(in)
		sealed, err := peoplechats.Seal(key, person, "payout", raw)
		if err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO person_payout_accounts (person_id, sealed) VALUES ($1, $2)
			ON CONFLICT (person_id) DO UPDATE SET sealed = EXCLUDED.sealed, updated_at = now()`, person, sealed)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": in, "checked": false,
		"words": "Taskiem does not check this account and moves no money: friends pay you with their own bank app."})
}

// deletePayoutAccount is DELETE /v1/me/payout-account.
func (s *Server) deletePayoutAccount(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `DELETE FROM person_payout_accounts WHERE person_id = $1`, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

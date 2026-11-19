package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Placing a basket and paying for it (people-chatting-5b C, C1, C2).

// basketNoteMax is how long the note to the business may be.
const basketNoteMax = 1000

// placeBasket is POST /v1/me/baskets/{id}/place {payment: all|each,
// address?, landmark?, address_id?, note?, total_kobo?}: the holder
// places the closed basket through the door as their own order (one order
// from them, with a note of whose lines are whose; the business never
// sees the group or the chat). The shop is read again first: an item gone,
// sold out or short, or an area not chosen, answers 409 with the
// problems; a total_kobo that is not the total now (prices changed since
// the holder looked) answers 409 changed, with nothing placed. A business
// that takes no payment in the app takes "all" only. Placing again after a
// failure is safe: the order is placed once (its key is the basket's).
func (s *Server) placeBasket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Payment   string     `json:"payment"`
		Address   string     `json:"address"`
		Landmark  string     `json:"landmark"`
		AddressID *uuid.UUID `json:"address_id"`
		Note      string     `json:"note"`
		TotalKobo int64      `json:"total_kobo"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Address, in.Landmark, in.Note = strings.TrimSpace(in.Address), strings.TrimSpace(in.Landmark), strings.TrimSpace(in.Note)
	switch {
	case in.Payment != "all" && in.Payment != "each":
		writeErr(w, http.StatusBadRequest, "payment is all (you pay for everyone) or each (each pays their share)")
		return
	case len([]rune(in.Address)) > 300, len([]rune(in.Landmark)) > 300, len([]rune(in.Note)) > 500:
		writeErr(w, http.StatusBadRequest, "the address and landmark are up to 300 characters each, a note up to 500")
		return
	}
	to := deliverTo{Address: in.Address, Landmark: in.Landmark}
	if in.AddressID != nil {
		a, err := s.savedAddress(ctx, person, *in.AddressID)
		if err != nil {
			s.personFail(w, r, err)
			return
		}
		to = a.deliverTo()
	}
	if s.Orders == nil || len(s.Orders.InboxKey) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Orders cannot be placed here yet.", "code": "orders_closed"})
		return
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, person).Scan(&phone); err != nil {
		s.fail(w, r, err)
		return
	}
	if phone == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Add your phone number first: the business needs it for your order.", "code": "phone_needed"})
		return
	}

	// 1. Hold it: closed → placing (with the way it is paid), the lines of
	// people no longer in the group or chat taken out; the names for the
	// business's note. An order already placed for it (a retry after a
	// failure, or after the placing was cut off) is found first and
	// finished, nothing checked again; a second place while the first is
	// at work is refused.
	key := "basket-" + chi.URLParam(r, "id")
	var d basketData
	var found *doorOrder
	realName := map[uuid.UUID]string{}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		b, err := basketTx(ctx, tx, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		key = "basket-" + b.ID.String()
		if d, err = s.basketDataTx(ctx, tx, person, b); err != nil {
			return err
		}
		if err := d.holder(person); err != nil {
			return err
		}
		switch b.State {
		case "closed", "placing":
		case "open":
			return customerSendErr{http.StatusConflict, "Close the basket first, so no one changes it while you place it."}
		default:
			return errBasketDone
		}
		if found, err = basketPlacedTx(ctx, tx, person, b.Business, key); err != nil || found != nil {
			return err
		}
		if b.State == "placing" && b.PlacingAt != nil && time.Since(*b.PlacingAt) < basketPlacingBusy {
			return customerSendErr{http.StatusConflict, "This basket's order is being placed. Wait a moment, then look at the basket again."}
		}
		if err := d.purgeTx(ctx, tx); err != nil {
			return err
		}
		if len(d.lines) == 0 {
			return customerSendErr{http.StatusConflict, "Nothing in the basket yet."}
		}
		var ids []uuid.UUID
		for _, l := range d.lines {
			if l.Person != person && !slices.Contains(ids, l.Person) {
				ids = append(ids, l.Person)
			}
		}
		if len(ids) > 0 {
			cards, err := s.people().CardsTx(ctx, tx, person, ids)
			if err != nil {
				return err
			}
			for _, c := range cards {
				realName[c.ID] = c.Name
			}
		}
		var mine string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, person).Scan(&mine); err != nil {
			return err
		}
		realName[person] = mine
		// placing_at is set afresh (the guard), so a second place waits.
		if _, err := tx.Exec(ctx, `UPDATE people_baskets SET state = 'closed' WHERE id = $1 AND state = 'placing'`, b.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE people_baskets SET state = 'placing', payment = $2 WHERE id = $1`, b.ID, in.Payment)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	b := d.b
	if found != nil {
		// Placed before: finished now, as it was chosen then.
		payment := in.Payment
		if b.Payment != nil {
			payment = *b.Payment
		}
		s.basketPlacedAnswer(w, r, person, d, *found, payment)
		return
	}
	// back puts it back to closed when it was not placed.
	back := func() {
		_ = db.InPersonTx(context.WithoutCancel(ctx), s.Store.Pool, person, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.WithoutCancel(ctx), `UPDATE people_baskets SET state = 'closed' WHERE id = $1 AND state = 'placing'`, b.ID)
			return err
		})
	}

	// 2. The shop now: every line in it and enough of it, the way of
	// having it offered, the payment offered, the total as the holder saw.
	var shop theirShop
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		shop, err = basketShopTx(ctx, tx, b, "shop")
		return err
	})
	if err != nil {
		back()
		s.personFail(w, r, err)
		return
	}
	d.shop = &shop
	view := d.view(person)
	if !shop.Open {
		back()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "They do not take orders in the app right now. Ask them in a chat with them.", "code": "orders_closed"})
		return
	}
	if len(view.Problems) > 0 {
		back()
		writeJSON(w, http.StatusConflict, map[string]any{"error": strings.Join(view.Problems, " "), "code": "basket_problems", "problems": view.Problems, "basket": view})
		return
	}
	if in.Payment == "each" && !eachOffered(shop) {
		back()
		writeJSON(w, http.StatusConflict, map[string]any{"error": b.BusinessName + " takes no payment in the app, so you pay for everyone: choose \"I'll pay all\".", "code": "pay_all_only"})
		return
	}
	if in.TotalKobo > 0 && in.TotalKobo != view.TotalKobo {
		back()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Prices or delivery changed since you looked: check the basket and place it again.", "code": "changed", "basket": view})
		return
	}

	// 3. Place it through the door, as the holder's own order, from their
	// own chat with the business (started now if they have none). "Each
	// pays" places it paid in shares: no page of its own for the whole,
	// and a day for the shares (storefront.ShareTTL).
	chat, _, err := s.openBusinessChat(ctx, person, b.Business, "basket", map[string]any{})
	if err != nil {
		back()
		s.personFail(w, r, err)
		return
	}
	lines := make([]orderLine, 0, len(d.lines))
	for _, l := range d.lines {
		lines = append(lines, orderLine{ItemID: l.ItemID, Quantity: l.Quantity})
	}
	note := basketNote(view, realName, in.Note)
	var placed doorOrder
	for range 3 {
		placed, _, err = s.placeAppOrder(ctx, person, chat.ID.String(), key, *phone, lines, b.str(b.Fulfilment), b.str(b.Zone), to, in.AddressID != nil, note, in.Payment == "each")
		if !errors.Is(err, errReferenceTaken) {
			break
		}
	}
	var se customerSendErr
	switch {
	case errors.Is(err, errAppOrdersClosed):
		back()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "They do not take orders in the app right now. Ask them in a chat with them.", "code": "orders_closed"})
		return
	case errors.As(err, &se):
		back()
		writeJSON(w, se.code, map[string]string{"error": se.msg, "code": "order_refused"})
		return
	case err != nil:
		back()
		s.personFail(w, r, err)
		return
	}
	s.basketPlacedAnswer(w, r, person, d, placed, in.Payment)
}

// basketPlacedTx is the order already placed for the basket's key from
// the person's own chat with the business (its card there), or nil.
func basketPlacedTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, key string) (*doorOrder, error) {
	var chat uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM person_conversations WHERE person_id = $1 AND tenant_id = $2`, person, business).Scan(&chat)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	again, err := personRepeat(ctx, tx, person, chat, "order:"+key)
	if err != nil || again == nil || again.Card == nil || again.Card.Reference == "" {
		return nil, err
	}
	out := doorOrder{Reference: again.Card.Reference, Kind: "order", Status: "sent", Summary: again.Card.Line, CreatedAt: again.CreatedAt}
	if again.Card.TotalKobo != nil {
		out.TotalKobo = *again.Card.TotalKobo
	}
	return &out, nil
}

// basketPlacedAnswer is step 4 of placing, and its answer: the basket
// placed, each member's share kept (their items and an equal part of the
// delivery; what is left over, and any difference the order came to, the
// placer's, who carries it even with no items of their own). It runs to
// the end even when the request is cut off (the order exists), and again
// on a retry.
func (s *Server) basketPlacedAnswer(w http.ResponseWriter, r *http.Request, person uuid.UUID, d basketData, placed doorOrder, payment string) {
	ctx := context.WithoutCancel(r.Context())
	b := d.b
	view := d.view(person)
	if d.shop == nil {
		// Found placed on a retry: the prices as they are now, unchecked.
		_ = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			shop, err := basketShopTx(ctx, tx, b, "look")
			if err == nil {
				d.shop = &shop
				view = d.view(person)
			}
			return nil
		})
	}
	shares := basketShares(view, person, placed.TotalKobo)
	err := s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		cur, err := basketTx(ctx, tx, b.ID.String(), true)
		if err != nil {
			return err
		}
		if cur.State == "placed" {
			return nil
		}
		if cur.State != "placing" {
			if _, err := tx.Exec(ctx, `UPDATE people_baskets SET state = 'placing' WHERE id = $1`, b.ID); err != nil {
				return err
			}
		}
		// The lines keep the prices they were placed at.
		for _, p := range view.People {
			for _, l := range p.Lines {
				if _, err := tx.Exec(ctx, `UPDATE people_basket_lines SET price_kobo = $3 WHERE basket_id = $1 AND id = $2 AND price_kobo <> $3`, b.ID, l.ID, l.PriceKobo); err != nil {
					return err
				}
			}
		}
		for _, x := range shares {
			if _, err := tx.Exec(ctx, `INSERT INTO people_basket_shares (basket_id, person_id, key, items_kobo, delivery_kobo, amount_kobo) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (basket_id, person_id) DO NOTHING`, b.ID, x.Person, x.Key, x.Items, x.Delivery, x.Total); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE people_baskets SET state = 'placed', payment = $2, order_reference = $3, placed_by = $4, placed_kobo = $5,
			delivery_kobo = $6, placed_at = now() WHERE id = $1`, b.ID, payment, placed.Reference, person, placed.TotalKobo, view.DeliveryKobo)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out, err := s.basketView(ctx, person, b.ID)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"basket": out, "order": placed})
}

// basketNote is the note on the order for the business: whose lines are
// whose, by each person's own name, and the placer's note; nothing of the
// group or the chat.
func basketNote(v basketOut, names map[uuid.UUID]string, note string) string {
	parts := []string{fmt.Sprintf("Ordered together in Taskiem by %d people.", len(v.People))}
	if len(v.People) == 1 {
		parts[0] = "Ordered together in Taskiem."
	}
	for _, p := range v.People {
		n := names[p.PersonID]
		if n == "" {
			n = "Someone"
		}
		items := make([]string, 0, len(p.Lines))
		for _, l := range p.Lines {
			items = append(items, fmt.Sprintf("%d × %s", l.Quantity, l.Name))
		}
		parts = append(parts, n+": "+strings.Join(items, ", ")+".")
	}
	out := strings.Join(parts, " ")
	if note != "" {
		out += "\n" + note
	}
	if r := []rune(out); len(r) > basketNoteMax {
		out = string(r[:basketNoteMax-1]) + "…"
	}
	return out
}

// basketShares splits what was placed: each person's items, and an equal
// part of the delivery among those with items; the kobo left over, and a
// total the order came to that differs (a price moved as it was placed),
// are the placer's to carry, who has a share for it even with no items of
// their own.
func basketShares(v basketOut, placer uuid.UUID, placed int64) []basketShare {
	n := int64(len(v.People))
	if n == 0 {
		return nil
	}
	part := v.DeliveryKobo / n
	left := v.DeliveryKobo - part*n
	out := make([]basketShare, 0, n+1)
	extra := -1
	var sum int64
	for i, p := range v.People {
		if p.PersonID == placer {
			extra = i
		}
		out = append(out, basketShare{Person: p.PersonID, Key: uuid.New(), Items: p.ItemsKobo, Delivery: part})
	}
	if extra < 0 {
		out = append(out, basketShare{Person: placer, Key: uuid.New()})
		extra = len(out) - 1
	}
	out[extra].Delivery += left
	for i := range out {
		out[i].Total = out[i].Items + out[i].Delivery
		sum += out[i].Total
	}
	if placed > 0 && placed != sum {
		out[extra].Total = max(out[extra].Total+placed-sum, 0)
	}
	return out
}

// payBasketShare is GET /v1/me/baskets/{id}/pay?for={person}: the pay
// link for what the person pays now. "Each pays their share": their own
// share's link (or, for the placer, anyone's who has not paid: the same
// page, whoever pays it). "I'll pay all": the order's own link, for the
// placer only.
func (s *Server) payBasketShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if s.Orders == nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	if !s.orderLookAllowed(w, person) {
		return
	}
	whom := person
	if f := r.URL.Query().Get("for"); f != "" {
		if whom, err = uuid.Parse(f); err != nil {
			writeErr(w, http.StatusBadRequest, "for is a person's id")
			return
		}
	}
	var d basketData
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		b, err := basketTx(ctx, tx, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		d, err = s.basketDataTx(ctx, tx, person, b)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	b := d.b
	if b.State != "placed" || b.Reference == nil || b.PlacedBy == nil {
		s.personFail(w, r, customerSendErr{http.StatusConflict, "The order is not placed yet."})
		return
	}
	placer := *b.PlacedBy == person
	if whom != person && !placer {
		s.personFail(w, r, customerSendErr{http.StatusForbidden, "Only the one who placed the order pays someone else's share."})
		return
	}
	payable := func() error {
		o := d.order
		switch {
		case o == nil:
			return errDoorNone
		case storefront.PayableNow(o.Status, o.Payment, max(o.DueKobo-o.PaidKobo, 0)):
			return nil
		case o.Status == "received" && o.Payment == "after_accept":
			return customerSendErr{http.StatusConflict, "You pay once the business accepts the order."}
		case o.Payment == "none":
			return customerSendErr{http.StatusConflict, "They take payment for this outside the app. The one who placed it pays them."}
		case o.DueKobo-o.PaidKobo <= 0:
			return customerSendErr{http.StatusConflict, "Nothing is due on this order."}
		}
		return customerSendErr{http.StatusConflict, errOrderClosed}
	}
	if *b.Payment == "all" {
		if !placer || whom != person {
			s.personFail(w, r, customerSendErr{http.StatusConflict, "The one who placed the order pays for everyone: pay them your share."})
			return
		}
		if err := payable(); err != nil {
			s.personFail(w, r, err)
			return
		}
		var link string
		var due int64
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			if err := s.enterOrderTx(ctx, tx, person, b.Business, *b.Reference, "customer.pay"); err != nil {
				return err
			}
			var err error
			link, due, err = s.payLinkTx(ctx, tx, b.Business, *b.Reference)
			return err
		})
		if err != nil {
			s.personFail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reference": *b.Reference, "url": link, "amount_kobo": due, "for": person})
		return
	}
	i := slices.IndexFunc(d.shares, func(x basketShare) bool { return x.Person == whom })
	if i < 0 {
		s.personFail(w, r, customerSendErr{http.StatusNotFound, "No share of this order is theirs."})
		return
	}
	sh := d.shares[i]
	if d.order != nil && d.order.Shares[sh.Key.String()] != nil {
		s.personFail(w, r, customerSendErr{http.StatusConflict, "This share is paid."})
		return
	}
	if d.order != nil && d.order.Refunds[sh.Key.String()] != nil {
		s.personFail(w, r, customerSendErr{http.StatusConflict, "This share was paid after the order could take it: the business is to send it back."})
		return
	}
	if sh.Total < 100 {
		s.personFail(w, r, customerSendErr{http.StatusConflict, "Nothing is due on this share."})
		return
	}
	if err := payable(); err != nil {
		s.personFail(w, r, err)
		return
	}
	// Through the basket door: audited in the business's chain, refused
	// when it blocked them.
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_basket_enter($1, 'pay')`, b.ID)
		return doorErr(err)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	link, err := s.Orders.ShareLink(ctx, b.Business, *b.Reference, sh.Key, sh.Total)
	switch {
	case errors.Is(err, storefront.ErrNoShareLinks):
		s.personFail(w, r, customerSendErr{http.StatusConflict, "This order can't be paid in parts in the app. The one who placed it pays them."})
		return
	case errors.Is(err, storefront.ErrDecision):
		s.personFail(w, r, customerSendErr{http.StatusConflict, "This share can't be paid now: the order has less left to pay, or can't be paid."})
		return
	case err != nil:
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reference": *b.Reference, "url": link.URL, "amount_kobo": link.AmountKobo, "for": whom, "paid": link.PaidAt != nil})
}

// sendBasketShareCards is POST /v1/me/baskets/{id}/share-cards {pay_to?}:
// "I'll pay all" placed, the placer sends each member a "Your share" card
// in one step (§11 A): their share, with the placer's account (as typed,
// or their saved one), in the group (one card each, naming them) or the
// chat. A member sent one before is not sent another.
func (s *Server) sendBasketShareCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		PayTo *payoutAccount `json:"pay_to"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var d basketData
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		b, err := basketTx(ctx, tx, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		d, err = s.basketDataTx(ctx, tx, person, b)
		return err
	})
	if err == nil {
		switch b := d.b; {
		case b.State != "placed" || b.PlacedBy == nil || *b.PlacedBy != person:
			err = customerSendErr{http.StatusForbidden, "Only the one who placed the order sends share cards."}
		case *b.Payment != "all":
			err = customerSendErr{http.StatusConflict, "Each pays their own share of this order: no share cards are needed."}
		case d.members == nil:
			err = customerSendErr{http.StatusConflict, "You are no longer in the group or the chat."}
		}
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	b := d.b
	sent := []uuid.UUID{}
	failed := []map[string]any{}
	for _, x := range d.shares {
		if x.Person == person || x.CardAt != nil || x.Total < 100 || !slices.Contains(d.members, x.Person) {
			continue
		}
		card, err := s.makeCard(ctx, person, cardRequest{Type: "split", AmountKobo: x.Total, What: fmt.Sprintf("Your share of order %s from %s", *b.Reference, b.BusinessName), PayTo: in.PayTo})
		if err != nil {
			s.personFail(w, r, err)
			return
		}
		whom, basket := x.Person, b.ID
		card.ForID, card.For, card.BasketID = &whom, d.names[x.Person], &basket
		dr := peopleDraft{kind: "card", card: card, clientID: "share:" + b.ID.String() + ":" + x.Person.String()}
		if b.Group != nil {
			_, _, err = s.groupSend(ctx, person, b.Group.String(), dr, []uuid.UUID{x.Person})
		} else {
			_, _, err = s.peopleSend(ctx, person, d.chat.String(), dr)
		}
		if err != nil {
			failed = append(failed, map[string]any{"person_id": x.Person, "error": err.Error()})
			continue
		}
		sent = append(sent, x.Person)
		if err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE people_basket_shares SET card_at = now() WHERE basket_id = $1 AND person_id = $2`, b.ID, x.Person)
			return err
		}); err != nil {
			s.personFail(w, r, err)
			return
		}
	}
	out, err := s.basketView(ctx, person, b.ID)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "failed": failed, "basket": out})
}

package api

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"

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

package api

import (
	"context"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Saving after an action (saved objects S3, begun in S2's review): once an
// invoice is sent to a customer typed by hand, "Save Acme Ltd as @acme-ltd
// for next time?" with Save, Keep it to myself and Cancel; any other
// message lets the offer go and is read as itself.

// offerSaveCustomer says what was done and offers to save its customer as
// a business's saved person; false (nothing said) when no name fits or one
// by the name is saved already.
func (s *Server) offerSaveCustomer(ctx context.Context, c *chat, title, addr string, email bool, said string) (bool, error) {
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
	return true, c.say(ctx, s, said+"\n\n"+c.t("wa.saved.offer", "title", title, "name", name), saveButtons(c, sv)...)
}

// saveOfferWord says whether a reply answers a save waiting for one.
func saveOfferWord(cmd string) bool {
	switch cmd {
	case "yes", "y", "save", "confirm", "mine", "keep it to myself", "keep it to me", "mine only", "no", "n":
		return true
	}
	return false
}

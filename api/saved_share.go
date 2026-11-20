package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Sharing a saved thing in a chat (saved-objects §6.2; S1 part 3): the
// message carries a card, a copy of it as it is now (saved.MakeCard), made
// here from the sender's own saved thing or their business's, never from
// what a client says it holds. Sharing anything sensitive (an account
// number, a phone, an email) needs the sender's yes (confirm), asked first
// in the words the client shows; each share is recorded, in the person's
// own log or the business's audit, with what kind and to whom, never the
// values.

// savedShare is a saved thing a message shares, as a client sends it.
type savedShare struct {
	ID uuid.UUID `json:"id"`
	// Owner: mine (the sender's own) or business (their business's).
	Owner string `json:"owner"`
	// Confirm: the sender said yes to sharing what is sensitive in it.
	Confirm bool `json:"confirm"`
}

// errShareConfirm asks the sender to confirm sharing what is sensitive; the
// handler answers it as 409 {confirm_share, error: the question}.
type errShareConfirm struct{ question string }

func (e errShareConfirm) Error() string { return e.question }

// shareConfirmFail answers a share that needs the sender's yes, and
// reports whether err was one.
func shareConfirmFail(w http.ResponseWriter, err error) bool {
	var c errShareConfirm
	if !errors.As(err, &c) {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": c.question, "confirm_share": true})
	return true
}

// savedCardFor makes the card a message shares: the saved thing must be the
// sender's own (person) or their business's (tenant, which may be Nil
// outside a business), live; sensitive values need sh.Confirm. to names who
// it goes to, for the question and the record ("Bola Stores").
func (s *Server) savedCardFor(r *http.Request, person, tenant uuid.UUID, sh savedShare, to string) (saved.Card, error) {
	ctx := r.Context()
	var o savedOwner
	switch sh.Owner {
	case "mine":
		o = savedOwner{person: person, by: person}
	case "business":
		if tenant == uuid.Nil {
			return saved.Card{}, customerSendErr{http.StatusBadRequest, "Only your own saved things can be shared here."}
		}
		o = savedOwner{tenant: tenant, by: person}
	default:
		return saved.Card{}, customerSendErr{http.StatusBadRequest, "Say whose saved thing it is: yours or the business's."}
	}
	var card saved.Card
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		x, err := savedRowTx(ctx, tx, o, sh.ID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return customerSendErr{http.StatusNotFound, "That saved thing is gone. Choose another."}
		}
		if err != nil {
			return err
		}
		number := ""
		if x.Kind == saved.KindBankAccount {
			if number, err = s.openSaved(ctx, tx, o, x.ID, x.Sealed); err != nil {
				return err
			}
		}
		card = saved.MakeCard(x.Kind, x.Name, x.Title, x.Details, number)
		if card.Sensitive && !sh.Confirm {
			return errShareConfirm{question: shareQuestion(x, to)}
		}
		return s.savedEvent(r, tx, o, x.ID, "shared", map[string]any{"kind": x.Kind, "to": to, "sensitive": card.Sensitive})
	})
	return card, err
}

// shareQuestion is what the sender is asked before sharing what is
// sensitive: "Share Mum's account details with Bola Stores?".
func shareQuestion(x savedRow, to string) string {
	what := "details"
	switch x.Kind {
	case saved.KindBankAccount:
		what = "account details"
	case saved.KindPerson, saved.KindPlace:
		what = "phone number and details"
	}
	if to == "" {
		to = "them"
	}
	return "Share " + x.Title + "'s " + what + " with " + to + "?"
}

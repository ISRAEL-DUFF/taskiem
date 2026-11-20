package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
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
	// namedIn, when set, is the message's words, which must name the
	// saved thing by its @name: checked before anything is asked.
	namedIn string
}

// namesSaved says whether text names @name as a whole word: "@mum" is
// not named by "@mummy" or "x@mum".
func namesSaved(text, name string) bool {
	if name == "" {
		return false
	}
	lower, want := strings.ToLower(text), "@"+strings.ToLower(name)
	part := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.'
	}
	for i := 0; ; {
		j := strings.Index(lower[i:], want)
		if j < 0 {
			return false
		}
		at, end := i+j, i+j+len(want)
		before, _ := utf8.DecodeLastRuneInString(lower[:at])
		after, _ := utf8.DecodeRuneInString(lower[end:])
		// A trailing full stop ends the sentence, not the name.
		afterNext, _ := utf8.DecodeRuneInString(lower[min(end+1, len(lower)):])
		okBefore := at == 0 || !part(before) || before == '.'
		okAfter := end == len(lower) || !part(after) || (after == '.' && (end+1 == len(lower) || !part(afterNext)))
		if okBefore && okAfter {
			return true
		}
		i = at + 1
	}
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

// savedShareRecord is a share to record once its message is kept: in the
// person's own log or the business's audit, with what kind and to whom,
// never the values. Recorded only after the send, so a refused or failed
// message records nothing.
type savedShareRecord struct {
	o         savedOwner
	id        uuid.UUID
	kind      string
	to        string
	sensitive bool
}

// recordShare records a share whose message was kept.
func (s *Server) recordShare(r *http.Request, rec *savedShareRecord) {
	s.recordShareBy(r.Context(), savedWhoOf(r), rec)
}

// recordShareBy records a share whose message was kept, by who (the
// Taskiem chat's, where there is no request).
func (s *Server) recordShareBy(ctx context.Context, who savedWho, rec *savedShareRecord) {
	if rec == nil {
		return
	}
	err := s.savedTx(ctx, rec.o, func(tx pgx.Tx) error {
		return savedEventBy(ctx, tx, who, rec.o, rec.id, "shared", map[string]any{"kind": rec.kind, "to": rec.to, "sensitive": rec.sensitive})
	})
	if err != nil {
		s.Logger.Warn("saved: a share not recorded", "err", err)
	}
}

// savedCardFor makes the card a message shares: the saved thing must be the
// sender's own (person) or their business's (tenant, which may be Nil
// outside a business), live; sensitive values need sh.Confirm, and a
// business's sensitive values someone who may manage its saved things
// (sharing is no way round reveal's passkey). A text is not shared as a
// card: its words go into the message. to names who it goes to, for the
// question and the record ("Bola Stores"). The share is recorded by the
// caller once the message is kept (recordShare).
func (s *Server) savedCardFor(r *http.Request, person, tenant uuid.UUID, sh savedShare, to string) (saved.Card, *savedShareRecord, error) {
	return s.savedCardOf(r.Context(), principalFrom(r.Context()), person, tenant, sh, to)
}

// savedCardOf is savedCardFor for p, from a context (the Taskiem chat's).
func (s *Server) savedCardOf(ctx context.Context, p *Principal, person, tenant uuid.UUID, sh savedShare, to string) (saved.Card, *savedShareRecord, error) {
	var o savedOwner
	switch sh.Owner {
	case "mine":
		o = savedOwner{person: person, by: person}
	case "business":
		if tenant == uuid.Nil {
			return saved.Card{}, nil, customerSendErr{http.StatusBadRequest, "Only your own saved things can be shared here."}
		}
		o = savedOwner{tenant: tenant, by: person}
	default:
		return saved.Card{}, nil, customerSendErr{http.StatusBadRequest, "Say whose saved thing it is: yours or the business's."}
	}
	var card saved.Card
	var rec *savedShareRecord
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		x, err := savedRowTx(ctx, tx, o, sh.ID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return customerSendErr{http.StatusNotFound, "That saved thing is gone. Choose another."}
		}
		if err != nil {
			return err
		}
		if sh.namedIn != "" && !namesSaved(sh.namedIn, x.Name) {
			return customerSendErr{http.StatusBadRequest, "Write @" + x.Name + " in the message to share it."}
		}
		number := ""
		switch x.Kind {
		case saved.KindBankAccount:
			if number, err = s.openSaved(ctx, tx, o, x.ID, x.Sealed); err != nil {
				return err
			}
			card = saved.MakeCard(x.Kind, x.Name, x.Title, x.Details, number)
		case saved.KindFile:
			return customerSendErr{http.StatusBadRequest, "A file is sent as itself: attach it to the message."}
		case saved.KindText:
			return customerSendErr{http.StatusBadRequest, "A text goes into your message as words: pick it with @ to put them in the box."}
		case saved.KindCustom:
			// A made-up kind's: its fields but those never shared.
			all, err := s.customValuesTx(ctx, tx, o, x)
			if err != nil {
				return err
			}
			card = saved.MakeCustomCard(x.KindName, x.Name, x.Title, x.Fields, all, filesOf(x.Details))
		default:
			card = saved.MakeCard(x.Kind, x.Name, x.Title, x.Details, number)
		}
		if card.Sensitive && !o.mine() && (p == nil || !p.Can(PermSavedManage)) {
			return customerSendErr{http.StatusForbidden, "Only those who manage the business's saved things share its account numbers and other sensitive details."}
		}
		if card.Sensitive && !sh.Confirm {
			return errShareConfirm{question: shareQuestion(x, to)}
		}
		rec = &savedShareRecord{o: o, id: x.ID, kind: x.Kind, to: to, sensitive: card.Sensitive}
		return nil
	})
	if err != nil {
		return saved.Card{}, nil, err
	}
	return card, rec, nil
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

// savedPeopleCard is the card a people chat or a group shares: one of the
// sender's own saved things only (a business's are not shared with people).
func (s *Server) savedPeopleCard(r *http.Request, person uuid.UUID, sh *savedShare, to string) (*peopleCard, *savedShareRecord, error) {
	if sh == nil {
		return nil, nil, customerSendErr{http.StatusBadRequest, "Choose the saved thing to share."}
	}
	if sh.Owner == "" {
		sh.Owner = "mine"
	}
	if sh.Owner != "mine" {
		return nil, nil, customerSendErr{http.StatusBadRequest, "Only your own saved things are shared with people."}
	}
	c, rec, err := s.savedCardFor(r, person, uuid.Nil, *sh, to)
	if err != nil {
		return nil, nil, err
	}
	return &peopleCard{Type: "saved", Title: c.Title, Saved: &c}, rec, nil
}

// maskMine is a message's saved card as its sender sees it: masked
// (saved.Card.Masked). Everyone else sees what was shared with them.
func maskMine(mine bool, c *saved.Card) *saved.Card {
	if !mine || c == nil {
		return c
	}
	m := c.Masked()
	return &m
}

// shareSensitive says whether sharing x asks first: what saved.MakeCard and
// MakeCustomCard mark sensitive, worked out from what lists show (an
// account's number, a person's phone or email, a place's phone, a made-up
// kind's sensitive field that may be shared and has a value).
func shareSensitive(x savedRow) bool {
	str := func(k string) string { v, _ := x.Details[k].(string); return v }
	switch x.Kind {
	case saved.KindBankAccount:
		return str("last4") != ""
	case saved.KindPerson:
		return str("phone") != "" || str("email") != ""
	case saved.KindPlace:
		return str("phone") != ""
	case saved.KindCustom:
		masked, _ := x.Details["masked"].(map[string]any)
		for _, f := range x.Fields {
			if f.Sensitive && !f.NeverShare && masked[f.Key] != nil {
				return true
			}
		}
	}
	return false
}

// savedForCustomer is the card a business's reply to its customer shares:
// the business's saved thing or the member's own; and whether it goes as a
// card (the customer chats in the app, and line is not whatsapp) or as
// plain lines on WhatsApp.
func (s *Server) savedForCustomer(r *http.Request, thread string, sh *savedShare, line string) (saved.Card, *savedShareRecord, bool, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	person, err := personOf(r)
	if err != nil {
		return saved.Card{}, nil, false, err
	}
	if sh == nil {
		return saved.Card{}, nil, false, customerSendErr{http.StatusBadRequest, "Choose the saved thing to share."}
	}
	inApp := false
	if line != lineWhatsApp {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			_, _, err := s.customerLineIn(ctx, tx, p.TenantID, thread, lineApp)
			if se := (customerSendErr{}); errors.As(err, &se) {
				return nil // no app line: WhatsApp
			}
			inApp = err == nil
			return err
		})
		if err != nil {
			return saved.Card{}, nil, false, err
		}
	}
	c, rec, err := s.savedCardFor(r, person, p.TenantID, *sh, "this customer")
	return c, rec, inApp, err
}

// maskCustomerCard is a customer chat's card as its reader sees it: a
// saved thing's masked in its sender's copy.
func maskCustomerCard(mine bool, c *customerCard) *customerCard {
	if c == nil || c.Saved == nil || !mine {
		return c
	}
	out := *c
	out.Saved = maskMine(true, c.Saved)
	return &out
}

// savedNamesAccount says whether a customer chat's card is a saved thing
// naming an account to pay: the customer is warned, as a message asking
// for a transfer warns them (§6.2, §15).
func savedNamesAccount(c *customerCard) bool {
	return c != nil && c.Saved != nil && c.Saved.NamesAccount()
}

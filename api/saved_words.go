package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/google/uuid"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Saving in words (saved objects S1, part 4; saved-objects §9.4): "save
// mum: GTBank 0123456789" or "save @warehouse: 12 Herbert Macaulay Way,
// Yaba, opposite the filling station", to Taskiem on WhatsApp, Telegram,
// Slack or in the app. The words are read (saved.Read), what was understood
// is shown, a business's account with the bank's name for it, and it is
// saved on Save: in the business's saved things for whoever may manage
// them, with Keep it to myself a tap away, else in the person's own. While
// it waits, its details are sealed. Nothing here goes to the AI.

var savedSaveWords = regexp.MustCompile(`(?is)^\s*save\s+@?([a-z0-9][a-z0-9_-]{0,31})\s*[:,\-–]\s*(.+?)\s*$`)

// chatSave is a saved thing waiting for Save.
type chatSave struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// Business: it goes in the business's saved things, not the person's.
	Business bool `json:"business"`
	// Sealed are its details (saved.Reading's), sealed while they wait:
	// under the business's key for the business's, under the person's own
	// for theirs ({"person": base64}), so the business never holds them.
	Sealed map[string]any `json:"sealed"`
}

// chatSaveRow is the row a person's waiting save is sealed for.
func chatSaveRow(person uuid.UUID) string { return "chat-save:" + person.String() }

// ownAppChat says whether c is on a business's own WhatsApp app, whose
// sender is only as sure as the secret the business chose (F2): a person's
// own saved things are neither saved nor looked up there.
func ownAppChat(c *chat) bool { return c.wa != nil && c.wa.OwnApp }

// sealChatSave seals a waiting save's details for whose it will be.
func (s *Server) sealChatSave(ctx context.Context, c *chat, business bool, details map[string]string) (map[string]any, error) {
	if business {
		var sealed map[string]any
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			var err error
			sealed, err = s.Store.PII.SealTx(ctx, tx, c.tenant.ID, "saved_details", details)
			return err
		})
		return sealed, err
	}
	raw, _ := json.Marshal(details)
	var sealed map[string]any
	err := db.InPersonTx(ctx, s.Store.Pool, c.p.UserID, func(tx pgx.Tx) error {
		key, err := s.keys().KeyTx(ctx, tx, c.p.UserID, true)
		if err != nil {
			return err
		}
		ct, err := peoplechats.Seal(key, c.p.UserID, chatSaveRow(c.p.UserID), raw)
		if err != nil {
			return err
		}
		sealed = map[string]any{"person": base64.StdEncoding.EncodeToString(ct)}
		return nil
	})
	return sealed, err
}

// openChatSave opens what sealChatSave sealed.
func (s *Server) openChatSave(ctx context.Context, c *chat, sealed map[string]any) (map[string]string, error) {
	var details map[string]string
	if b64, ok := sealed["person"].(string); ok {
		ct, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, err
		}
		err = db.InPersonTx(ctx, s.Store.Pool, c.p.UserID, func(tx pgx.Tx) error {
			key, err := s.keys().KeyTx(ctx, tx, c.p.UserID, false)
			if err != nil {
				return err
			}
			raw, err := peoplechats.Open(key, c.p.UserID, chatSaveRow(c.p.UserID), ct)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &details)
		})
		return details, err
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		v, err := s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, sealed)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(v)
		return json.Unmarshal(raw, &details)
	})
	return details, err
}

// titleOf is a title made from a name: "mum" is Mum, "bank-details" Bank
// details.
func titleOf(name string) string {
	t := strings.ReplaceAll(strings.ReplaceAll(name, "-", " "), "_", " ")
	if t == "" {
		return t
	}
	return strings.ToUpper(t[:1]) + t[1:]
}

// waSaveWords starts saving a thing said in words; false when the words
// are not "save name: details".
func (s *Server) waSaveWords(ctx context.Context, c *chat, text string) (bool, error) {
	m := savedSaveWords.FindStringSubmatch(text)
	if m == nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	name := savedNameOr(m[1])
	if name == "" {
		return false, nil
	}
	read := saved.Read(m[2])
	if read.Kind == saved.KindBankAccount && read.Details["bank_name"] == "" {
		return true, c.say(ctx, s, c.t("wa.saved.need_bank", "number", read.Details["account_number"], "name", name))
	}
	business := c.p.Can(PermSavedManage)
	if ownAppChat(c) && !business {
		return true, c.say(ctx, s, c.t("wa.saved.own_app", "org", c.tenant.Name))
	}
	o := s.chatSavedOwner(c, business)
	taken := false
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := ownerWhere(o)
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM saved_objects WHERE `+where+` AND name = $2)`, owner, name).Scan(&taken)
	}); err != nil {
		return true, err
	}
	if taken {
		return true, c.say(ctx, s, c.t("wa.saved.taken", "name", name))
	}
	title := read.Title
	if title == "" {
		title = titleOf(name)
	}
	sealed, err := s.sealChatSave(ctx, c, business, read.Details)
	if err != nil {
		return true, err
	}
	sv := &chatSave{Name: name, Title: title, Kind: read.Kind, Business: business, Sealed: sealed}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Save: sv}, waPendingTTL); err != nil {
		return true, err
	}
	return true, s.waSaveAsk(ctx, c, sv, read.Details)
}

// savedNameOr is a name as saved things take it, or "" for one they
// refuse (the words are then not a save).
func savedNameOr(typed string) string {
	if n, err := saved.Name(typed); err == nil {
		return n
	}
	return ""
}

// chatSavedOwner is where a thing said in words is saved.
func (s *Server) chatSavedOwner(c *chat, business bool) savedOwner {
	if business {
		return savedOwner{tenant: c.tenant.ID, by: c.p.UserID}
	}
	return savedOwner{person: c.p.UserID, by: c.p.UserID}
}

// waSaveAsk shows what was understood, a business's account with the
// bank's name for it, and asks for Save.
func (s *Server) waSaveAsk(ctx context.Context, c *chat, sv *chatSave, details map[string]string) error {
	card := saved.MakeCard(sv.Kind, sv.Name, sv.Title, anyMap(details), details["account_number"])
	lines := []string{card.Plain()}
	if sv.Kind == saved.KindBankAccount && sv.Business {
		acct, err := (saved.BankAccount{BankName: details["bank_name"], AccountNumber: details["account_number"]}).Clean()
		if err == nil {
			check, problem, err := s.checkSavedAccount(ctx, c.tenant.ID, acct)
			switch {
			case err == nil && check != nil:
				lines = append(lines, c.t("wa.saved.bank_name", "name", check.AccountName))
			case refusedWords(err) != "":
				// The bank does not know it: never saved.
				if e := s.saveSession(ctx, c, stateIdle, chatData{}, 0); e != nil {
					return e
				}
				return c.say(ctx, s, c.t("wa.saved.not_saved", "reason", refusedWords(err)))
			case err != nil:
				return err
			case problem.Words != "":
				lines = append(lines, problem.Words)
			default:
				lines = append(lines, c.t("wa.saved.not_checked"))
			}
		}
	}
	where := c.t("wa.saved.where_mine")
	if sv.Business {
		where = c.t("wa.saved.where_business", "org", c.tenant.Name)
	}
	return c.say(ctx, s, c.t("wa.saved.save_ask", "name", sv.Name, "card", strings.Join(lines, "\n"), "where", where), saveButtons(c, sv)...)
}

// saveButtons: Save, and for the business's, Keep it to myself; Cancel.
func saveButtons(c *chat, sv *chatSave) []whatsapp.Button {
	out := []whatsapp.Button{{ID: "yes", Title: c.t("button.save")}}
	if sv.Business && !ownAppChat(c) {
		out = append(out, whatsapp.Button{ID: "mine", Title: c.t("button.keep_mine")})
	}
	return append(out, whatsapp.Button{ID: "cancel", Title: c.t("button.cancel")})
}

// waSaveReply answers Save, Keep it to myself or No.
func (s *Server) waSaveReply(ctx context.Context, c *chat, cmd string) error {
	sv := c.data.Save
	switch cmd {
	case "yes", "y", "save", "confirm":
	case "mine", "keep it to myself", "keep it to me", "mine only":
		if ownAppChat(c) {
			return c.say(ctx, s, c.t("wa.saved.own_app", "org", c.tenant.Name), saveButtons(c, sv)...)
		}
		sv.Business = false
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.saved.save_left"))
	default:
		return c.say(ctx, s, c.t("wa.saved.save_reminder", "name", sv.Name), saveButtons(c, sv)...)
	}
	details, err := s.openChatSave(ctx, c, sv.Sealed)
	if err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	in := savedIn{Name: &sv.Name, Title: &sv.Title, Kind: sv.Kind}
	if sv.Kind == saved.KindBankAccount {
		bank, number := details["bank_name"], details["account_number"]
		in.BankName, in.AccountNumber = &bank, &number
	} else {
		in.Details = anyMap(details)
	}
	o := s.chatSavedOwner(c, sv.Business)
	_, problem, err := s.addSavedNew(ctx, savedWho{p: c.p, where: c.ch.Name()}, o, in)
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &pe) && pe.Code == "23505":
		return c.say(ctx, s, c.t("wa.saved.taken", "name", sv.Name))
	case refusedWords(err) != "":
		return c.say(ctx, s, c.t("wa.saved.not_saved", "reason", refusedWords(err)))
	case err != nil:
		return err
	}
	said := c.t("wa.saved.saved", "name", sv.Name)
	if problem.Words != "" {
		said += "\n" + problem.Words
	}
	return c.say(ctx, s, said)
}

// refusedWords are a refusal's words to say back (a field that does not
// fit, an account the bank does not know), or "" for any other failure.
func refusedWords(err error) string {
	var se customerSendErr
	switch {
	case errors.Is(err, saved.ErrInvalid):
		return saved.Plain(err)
	case errors.As(err, &se):
		return se.msg
	}
	return ""
}

// anyMap is a reading's fields as saved.Clean takes them.
func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

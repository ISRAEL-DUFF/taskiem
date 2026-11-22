package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Adding a saved thing, wherever it comes from: the Saved page (POST
// /v1/saved, /v1/me/saved) or words to Taskiem ("save mum: GTBank
// 0123456789", saved_words.go). One way, so each is checked, sealed and
// recorded alike.

// readSavedWords is POST /v1/me/saved/read {text}: what a message's words
// hold for saving (Save as…, saved-objects §9.1): the kind, a title when
// they give one, and the fields filled in, to show in the sheet that saves
// one. Nothing is kept, and nothing goes to the AI.
func (s *Server) readSavedWords(w http.ResponseWriter, r *http.Request) {
	if _, err := personOf(r); err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Text string `json:"text"`
		// From is "photo" for text the phone read from a photo: letters
		// read in place of digits are put right first.
		From string `json:"from"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(in.Text) == "" || len([]rune(in.Text)) > saved.MaxText {
		writeErr(w, http.StatusBadRequest, "Give the words to read.")
		return
	}
	// After the length check: a text too long is refused unread.
	if in.From == "photo" {
		in.Text = saved.PhotoText(in.Text)
	}
	writeJSON(w, http.StatusOK, saved.Read(in.Text))
}

// savedWho is who adds or changes a saved thing, and from where (an IP
// address, or the chat it was said in), for the record.
type savedWho struct {
	p     *Principal
	where string
}

// savedWhoOf is the request's caller.
func savedWhoOf(r *http.Request) savedWho {
	return savedWho{p: principalFrom(r.Context()), where: clientIP(r)}
}

// savedEventBy records a change: in the business's audit chain, or the
// person's own log (a person's own things are not the business's).
func savedEventBy(ctx context.Context, tx pgx.Tx, who savedWho, o savedOwner, id uuid.UUID, action string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	if !o.mine() {
		detail["ip"] = who.where
		raw, _ := json.Marshal(detail)
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, who.p.TenantID, who.p.ActorType(), who.p.Actor(), "saved."+action, id.String(), raw)
		return err
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO saved_object_events (id, person_id, object_id, action, detail) VALUES ($1, $2, $3, $4, $5)`,
		uuid.Must(uuid.NewV7()), o.person, id, action, raw)
	return err
}

// addSavedNew adds one saved thing (in.Name, in.Title and, for a bank
// account, its bank and number given): a business's account checked with
// the bank first (one the bank does not know is never saved); the problem
// when its name could not be checked just now.
func (s *Server) addSavedNew(ctx context.Context, who savedWho, o savedOwner, in savedIn) (savedRow, savedProblem, error) {
	var x savedRow
	name, err := saved.Name(*in.Name)
	if err != nil {
		return x, savedProblem{}, err
	}
	title, err := saved.Title(*in.Title)
	if err != nil {
		return x, savedProblem{}, err
	}
	id := uuid.Must(uuid.NewV7())
	if in.Kind == saved.KindCustom {
		return s.addSavedCustom(ctx, who, o, id, name, title, in)
	}
	if in.Kind == saved.KindFile {
		x, err := s.addSavedFileObject(ctx, who, o, id, name, title, in)
		return x, savedProblem{}, err
	}
	if in.Kind != saved.KindBankAccount {
		c, err := saved.Clean(in.Kind, in.Details)
		if err != nil {
			return x, savedProblem{}, err
		}
		err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
			// A person's place is one of their addresses (person_addresses.go):
			// counted with them, and the default when they have none.
			address := o.mine() && in.Kind == saved.KindPlace
			if address {
				if err := lockAddressesTx(ctx, tx, o.person); err != nil {
					return err
				}
			}
			if err := lockSavedTx(ctx, tx, o); err != nil {
				return err
			}
			if err := s.savedRoomTx(ctx, tx, o, name); err != nil {
				return err
			}
			isDefault := false
			if address {
				var n int
				var hasDefault bool
				if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(default_place), false) FROM saved_objects WHERE `+placesWhere, o.person).Scan(&n, &hasDefault); err != nil {
					return err
				}
				if n >= maxAddresses {
					return saved.Refuse(fmt.Sprintf("You can keep up to %d places. Remove one you no longer use first.", maxAddresses))
				}
				isDefault = !hasDefault
			}
			if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, details, sealed, hint,
					check_state, default_place, created_by, updated_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8, $9, $10, $11, $11) RETURNING `+savedCols,
				id, nullable(o.person), nullable(o.tenant), name, title, in.Kind, c.Details, savedHint(o, in.Kind, c), saved.NoCheck, isDefault, o.by)); err != nil {
				return err
			}
			return savedEventBy(ctx, tx, who, o, id, "added", map[string]any{"name": name, "kind": in.Kind})
		})
		return x, savedProblem{}, err
	}
	acct, err := in.account(saved.BankAccount{})
	if err != nil {
		return x, savedProblem{}, err
	}
	if acct, err = s.settleBank(ctx, o, acct); err != nil {
		return x, savedProblem{}, err
	}
	// A name taken or the limit reached is told before the bank is asked.
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error { return s.savedRoomTx(ctx, tx, o, name) }); err != nil {
		return x, savedProblem{}, err
	}
	// The name check, outside the transaction: a business's account the
	// bank does not know is never saved.
	var check *savedCheckResult
	var problem savedProblem
	if !o.mine() {
		if check, problem, err = s.checkSavedAccount(ctx, o.tenant, acct); err != nil {
			return x, problem, err
		}
	}
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		if err := s.savedRoomTx(ctx, tx, o, name); err != nil {
			return err
		}
		sealed, err := s.sealSaved(ctx, tx, o, id, acct.AccountNumber)
		if err != nil {
			return err
		}
		state, cname, cvia, cat := checkCols(check)
		if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, details, sealed, hint,
				check_state, checked_name, checked_via, checked_at, check_problem, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15) RETURNING `+savedCols,
			id, nullable(o.person), nullable(o.tenant), name, title, saved.KindBankAccount, acct.Details(), sealed, acct.Hint(), state, cname, cvia, cat, problem.lasting(), o.by)); err != nil {
			return err
		}
		detail := map[string]any{"name": name, "kind": saved.KindBankAccount, "bank": acct.BankName, "account_last4": saved.Last4(acct.AccountNumber), "check": state}
		return savedEventBy(ctx, tx, who, o, id, "added", detail)
	})
	return x, problem, err
}

// savedHint is what a list shows of c: a business's person with their
// phone and email masked (saved.Cleaned.MaskedHint); a person's own as
// it is.
func savedHint(o savedOwner, kind string, c saved.Cleaned) string {
	if o.mine() {
		return c.Hint
	}
	return c.MaskedHint(kind)
}

// eraseSavedContacts lets go of the business's saved things that hold a
// phone or an email being erased (POST /v1/pii/erase {category, value}):
// a person or a place with it, or a thing of the business's own kind with
// it as a field's value. Each is removed as a removal does (savedScrub),
// its files with it, and recorded. Answers how many.
func (s *Server) eraseSavedContacts(r *http.Request, category string, value any) (int, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	v, _ := value.(string)
	v = strings.TrimSpace(v)
	switch category {
	case "phone":
		// Not a phone this reads: no saved thing holds it as one.
		n, perr := phonenum.Normalise(v)
		if perr != nil {
			return 0, nil //nolint:nilerr // nothing to erase, not a failure
		}
		v = n
	case "email":
		v = strings.ToLower(v)
	default:
		return 0, nil
	}
	if v == "" {
		return 0, nil
	}
	o := savedOwner{tenant: p.TenantID, by: p.UserID}
	var gone []string
	n := 0
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM saved_objects WHERE tenant_id = $1 AND archived_at IS NULL AND (
				details->>'phone' = $2 OR lower(details->>'email') = $2
				OR EXISTS (SELECT 1 FROM jsonb_each_text(CASE WHEN jsonb_typeof(details->'values') = 'object' THEN details->'values' ELSE '{}' END) e WHERE lower(e.value) = $2))
			FOR UPDATE`, p.TenantID, v)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		// A sensitive field's value is sealed: each thing of the business's
		// own kinds with sealed values is opened to look.
		rows, err = tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE tenant_id = $1 AND archived_at IS NULL AND kind = 'custom' AND sealed <> '{}' AND NOT (id = ANY ($2)) FOR UPDATE`, p.TenantID, ids)
		if err != nil {
			return err
		}
		sealed, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		for _, x := range sealed {
			all, err := s.customValuesTx(ctx, tx, o, x)
			if err != nil {
				return err
			}
			for _, val := range all {
				if strings.EqualFold(strings.TrimSpace(val), v) {
					ids = append(ids, x.ID)
					break
				}
			}
		}
		for _, id := range ids {
			if _, err := tx.Exec(ctx, `UPDATE saved_objects SET `+savedScrub+`, updated_by = $3 WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, o.by); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM saved_reminders WHERE object_id = $1`, id); err != nil {
				return err
			}
			paths, err := dropObjectFilesTx(ctx, tx, o, id)
			if err != nil {
				return err
			}
			gone = append(gone, paths...)
			if err := savedEventBy(ctx, tx, savedWhoOf(r), o, id, "removed", map[string]any{"for": "pii.erase", "category": category}); err != nil {
				return err
			}
		}
		n = len(ids)
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.dropSavedPaths(ctx, o, gone)
	return n, nil
}

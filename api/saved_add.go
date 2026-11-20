package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(in.Text) == "" || len([]rune(in.Text)) > saved.MaxText {
		writeErr(w, http.StatusBadRequest, "Give the words to read.")
		return
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
			if err := lockSavedTx(ctx, tx, o); err != nil {
				return err
			}
			if err := savedRoomTx(ctx, tx, o, name); err != nil {
				return err
			}
			if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, details, sealed, hint,
					check_state, created_by, updated_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8, $9, $10, $10) RETURNING `+savedCols,
				id, nullable(o.person), nullable(o.tenant), name, title, in.Kind, c.Details, c.Hint, saved.NoCheck, o.by)); err != nil {
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
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error { return savedRoomTx(ctx, tx, o, name) }); err != nil {
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
		if err := savedRoomTx(ctx, tx, o, name); err != nil {
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

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// A staff member's account for payments outside payroll (payroll is
// Payrolla's): one of the business's saved bank accounts, linked to them
// by an owner with a passkey (where the money goes changes), so Pay on
// their page knows it. Taskiem keeps no account number on the staff
// member: the saved account keeps it, sealed, and changing its number
// takes a passkey of its own.

// staffOpAccount is the step-up operation for linking a staff member's
// account, bound to the staff member.
const staffOpAccount = "staff.account"

// staffPayAccount is a staff member's linked account, as their page shows
// it: masked.
type staffPayAccount struct {
	SavedID  uuid.UUID `json:"saved_id"`
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	BankName string    `json:"bank_name"`
	Last4    string    `json:"last4"`
	Checked  *string   `json:"account_name,omitempty"`
	LinkedBy string    `json:"linked_by,omitempty"`
	// LinkedByName: who linked it, by name.
	LinkedByName string     `json:"linked_by_name,omitempty"`
	LinkedAt     *time.Time `json:"linked_at,omitempty"`
}

// staffPayAccountTx is the staff member's linked account, or nil (none
// linked, or the saved account linked was deleted since: not linked).
func staffPayAccountTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (*staffPayAccount, error) {
	var id *uuid.UUID
	var by *string
	var at *time.Time
	err := tx.QueryRow(ctx, `SELECT saved_id, saved_linked_by, saved_linked_at FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2`, tenant, user).Scan(&id, &by, &at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && id == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	x, err := scanSaved(tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE tenant_id = $1 AND id = $2 AND archived_at IS NULL`, tenant, *id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b := saved.FromDetails(x.Details)
	l4, _ := x.Details["last4"].(string)
	out := &staffPayAccount{SavedID: x.ID, Name: x.Name, Title: x.Title, BankName: b.BankName, Last4: l4, Checked: x.CheckedName, LinkedAt: at}
	if by != nil {
		out.LinkedBy = *by
		_ = tx.QueryRow(ctx, `SELECT COALESCE((SELECT name FROM users WHERE id::text = taskiem_actor_human($1)), '')`, strings.TrimPrefix(*by, "user:")).Scan(&out.LinkedByName)
	}
	return out, nil
}

// putStaffAccount is PUT /v1/staff/{user}/pay-account {saved_id | null,
// passkey}: an owner links one of the business's saved bank accounts to a
// staff member (null unlinks it), with a passkey for that person.
func (s *Server) putStaffAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		SavedID *uuid.UUID `json:"saved_id"`
		stepUpProof
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !isOwnerOf(p) {
		s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only an owner says where a staff member is paid."))
		return
	}
	// Checked first, so no one is asked to confirm a link that is refused.
	if err := s.tx(r, func(tx pgx.Tx) error {
		var staff bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, p.TenantID, user).Scan(&staff); err != nil {
			return err
		}
		if !staff {
			return pgx.ErrNoRows
		}
		if in.SavedID == nil {
			return nil
		}
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM saved_objects WHERE tenant_id = $1 AND id = $2 AND archived_at IS NULL`, p.TenantID, *in.SavedID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return refusePay(http.StatusNotFound, "no_payee", "There is no such saved account in the business.")
		}
		if err != nil {
			return err
		}
		if kind != saved.KindBankAccount {
			return refusePay(http.StatusBadRequest, "not_an_account", "That saved thing is not a bank account.")
		}
		return nil
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	method, ok := s.personStepUp(w, r, in.stepUpProof, staffOpAccount, user.String(), "changes to where a staff member is paid", "changing where a staff member is paid")
	if !ok {
		return
	}
	var out *staffPayAccount
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO staff_profiles (tenant_id, user_id, saved_id, saved_linked_by, saved_linked_at, updated_by)
			VALUES ($1, $2, $3, $4, now(), $4)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET saved_id = EXCLUDED.saved_id, saved_linked_by = EXCLUDED.saved_linked_by,
				saved_linked_at = EXCLUDED.saved_linked_at`, p.TenantID, user, in.SavedID, p.Actor()); err != nil {
			return err
		}
		var err error
		if out, err = staffPayAccountTx(ctx, tx, p.TenantID, user); err != nil {
			return err
		}
		detail := map[string]any{"confirmed": method}
		if out != nil {
			detail["saved"], detail["last4"] = out.Name, out.Last4
		}
		return auditTx(r, tx, "staff.pay_account", user.String(), detail)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pay_account": out})
}

// staffPayee is the saved account linked to a staff member, and the
// staff member's name, for a payment to them.
func (s *Server) staffPayee(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (*uuid.UUID, error) {
	acct, err := staffPayAccountTx(ctx, tx, tenant, user)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, refusePay(http.StatusConflict, "no_bank", "No account is linked to this staff member: an owner links one of your saved accounts on their page.")
	}
	var staff bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, tenant, user).Scan(&staff); err != nil {
		return nil, err
	}
	if !staff {
		return nil, refusePay(http.StatusNotFound, "no_payee", "They are not staff of this business any more.")
	}
	id := acct.SavedID
	return &id, nil
}

// payStaff is GET /v1/payments/staff (money.pay): the business's staff,
// each with where payments to them go (masked), for those who may pay
// but not manage people (Pay several people's Staff).
func (s *Server) payStaff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	type person struct {
		UserID     uuid.UUID        `json:"user_id"`
		Name       string           `json:"name"`
		PayAccount *staffPayAccount `json:"pay_account"`
	}
	out := []person{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT u.id, u.name, lower(u.name) FROM memberships m JOIN users u ON u.id = m.user_id
			WHERE m.tenant_id = $1 ORDER BY lower(u.name)`, p.TenantID)
		if err != nil {
			return err
		}
		type row struct {
			ID    uuid.UUID
			Name  string
			Lower string
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
		if err != nil {
			return err
		}
		for _, x := range list {
			acct, err := staffPayAccountTx(ctx, tx, p.TenantID, x.ID)
			if err != nil {
				return err
			}
			if acct != nil {
				acct.LinkedBy = ""
			}
			out = append(out, person{UserID: x.ID, Name: x.Name, PayAccount: acct})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": out})
}

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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Collecting people to pay from several messages (paying people, P3;
// docs/spec/chat-pay-people.md, Pay several people): "Add to the list" on
// a message keeps a row on the person's own list, across every chat, so
// "3 to pay" can stay at the foot of the chat until the list is paid as a
// batch or cleared. Each row is checked as a payment's payee is when it is
// added (staff add the business's saved accounts only), and again, with
// the bank, when the list becomes a batch. A row in a batch waiting to be
// sent is set aside; once sent it leaves the list, and if the batch is
// cancelled it comes back.

// collectedRow is a row of the list, as the foot and the list show it:
// the account masked.
type collectedRow struct {
	ID         uuid.UUID  `json:"id"`
	SavedID    *uuid.UUID `json:"saved_id"`
	Payee      string     `json:"payee"`
	Title      string     `json:"title"`
	Name       string     `json:"name"`
	BankName   string     `json:"bank_name"`
	Last4      string     `json:"last4"`
	AmountKobo *int64     `json:"amount_kobo"`
	Purpose    string     `json:"purpose"`
	Note       string     `json:"note"`
	Source     *msgSource `json:"source"`
	AddedAt    time.Time  `json:"added_at"`
}

// msgSource is the message a row came from.
type msgSource struct {
	Kind    string `json:"kind"`
	Chat    string `json:"chat"`
	Message string `json:"message"`
	// From is who sent it, as the chat names them.
	From string `json:"from,omitempty"`
}

var sourceKinds = map[string]bool{"taskiem": true, "link": true, "customer": true, "person": true, "group": true, "photo": true}

// collection is the person's list: the rows to pay, and the batch some
// of them wait in, if any.
type collection struct {
	Rows      []collectedRow `json:"rows"`
	Count     int            `json:"count"`
	TotalKobo int64          `json:"total_kobo"`
	// NoAmount: rows that still need an amount.
	NoAmount int `json:"no_amount"`
	// InBatch: a batch made from the list that is waiting to be sent.
	InBatch *uuid.UUID `json:"in_batch"`
}

// collectionTx is the person's list. Rows of a batch that was sent leave
// it; rows of a cancelled batch come back.
func collectionTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (collection, error) {
	out := collection{Rows: []collectedRow{}}
	// Sent: gone. Cancelled: gone too where the row's own payment went
	// before the rest was cancelled, so nobody is paid twice from it.
	if _, err := tx.Exec(ctx, `DELETE FROM pay_collected c USING payment_batches b WHERE c.tenant_id = $1 AND c.user_id = $2 AND c.batch_id = b.id
		AND (b.status = 'sent' OR b.status = 'cancelled' AND EXISTS (SELECT 1 FROM payments p WHERE p.id = c.payment_id AND p.status NOT IN ('prepared', 'cancelled', 'rejected')))`,
		tenant, user); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE pay_collected c SET batch_id = NULL, payment_id = NULL FROM payment_batches b WHERE c.tenant_id = $1 AND c.user_id = $2 AND c.batch_id = b.id AND b.status = 'cancelled'`,
		tenant, user); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `SELECT batch_id FROM pay_collected WHERE tenant_id = $1 AND user_id = $2 AND batch_id IS NOT NULL ORDER BY created_at DESC LIMIT 1`,
		tenant, user).Scan(&out.InBatch); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT c.id, c.saved_id, COALESCE(o.name, ''), c.title, c.name, c.bank_name, c.last4, c.amount_kobo, c.purpose, c.note,
		c.source_kind, c.source_chat, c.source_message, c.source_from, c.created_at
		FROM pay_collected c LEFT JOIN saved_objects o ON o.id = c.saved_id
		WHERE c.tenant_id = $1 AND c.user_id = $2 AND c.batch_id IS NULL ORDER BY c.created_at, c.id`, tenant, user)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r collectedRow
		var kind, chat, msg, from string
		if err := rows.Scan(&r.ID, &r.SavedID, &r.Payee, &r.Title, &r.Name, &r.BankName, &r.Last4, &r.AmountKobo, &r.Purpose, &r.Note, &kind, &chat, &msg, &from, &r.AddedAt); err != nil {
			return out, err
		}
		if msg != "" {
			r.Source = &msgSource{Kind: kind, Chat: chat, Message: msg, From: from}
		}
		if r.AmountKobo == nil {
			out.NoAmount++
		} else {
			out.TotalKobo += *r.AmountKobo
		}
		out.Rows = append(out.Rows, r)
	}
	out.Count = len(out.Rows)
	return out, rows.Err()
}

// getCollection is GET /v1/pay-collection: the person's list.
func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out collection
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		out, err = collectionTx(r.Context(), tx, p.TenantID, p.UserID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// collectIn is a row to add: a saved account (saved_id or payee) or an
// account number with its bank; the amount and what it is for if the
// message says; the message it came from.
type collectIn struct {
	SavedID       *uuid.UUID `json:"saved_id"`
	Payee         string     `json:"payee"`
	AccountNumber string     `json:"account_number"`
	BankName      string     `json:"bank_name"`
	Name          string     `json:"name"`
	AmountKobo    *int64     `json:"amount_kobo"`
	Purpose       string     `json:"purpose"`
	Note          string     `json:"note"`
	Source        *msgSource `json:"source"`
}

// addCollected is POST /v1/pay-collection: a row added to the list. The
// same message twice is refused (409 already, with the list); up to 100.
func (s *Server) addCollected(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var in collectIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := checkCollected(in.AmountKobo, in.Purpose, in.Note); err != nil {
		s.payFail(w, r, err)
		return
	}
	src := msgSource{}
	if in.Source != nil {
		src = *in.Source
		src.From = strings.TrimSpace(src.From)
		if r := []rune(src.From); len(r) > 80 {
			src.From = string(r[:80])
		}
		if !sourceKinds[src.Kind] || len(src.Chat) > 80 || len(src.Message) > 80 || src.Message == "" {
			s.payFail(w, r, refusePay(http.StatusBadRequest, "source", "Say which message it came from."))
			return
		}
	}
	// Checked as a payment's payee is: staff add saved accounts only.
	payee, err := s.payeeOf(ctx, p.TenantID, p.UserID, isOwnerOf(p), payIn{SavedID: in.SavedID, Payee: in.Payee, AccountNumber: in.AccountNumber, BankName: in.BankName})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	var out collection
	err = s.tx(r, func(tx pgx.Tx) error {
		// Rows of a batch sent (or cancelled) since the list was last read
		// are let go first, so they neither fill the list nor refuse their
		// message again.
		if _, err := collectionTx(ctx, tx, p.TenantID, p.UserID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pay_collected WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, p.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= maxBatchLen {
			return refusePay(http.StatusConflict, "full", fmt.Sprintf("The list has %d people already: pay it first.", maxBatchLen))
		}
		var sealed any
		if payee.saved == nil {
			if sealed, err = s.Store.PII.SealTx(ctx, tx, p.TenantID, "account_number", payee.bank.AccountNumber); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO pay_collected (id, tenant_id, user_id, saved_id, account_sealed, bank_name, last4, title, name, amount_kobo, purpose, note,
			source_kind, source_chat, source_message, source_from) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			uuid.Must(uuid.NewV7()), p.TenantID, p.UserID, payee.saved, sealed, payee.bank.BankName, saved.Last4(payee.bank.AccountNumber), payee.title, name,
			in.AmountKobo, strings.TrimSpace(in.Purpose), strings.TrimSpace(in.Note), src.Kind, src.Chat, src.Message, src.From)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return refusePay(http.StatusConflict, "already", "That message is on your list already.")
		}
		if err != nil {
			return err
		}
		out, err = collectionTx(ctx, tx, p.TenantID, p.UserID)
		return err
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// checkCollected checks a row's amount, purpose and note as a payment's
// are checked (preparePayment), so a row the list takes is not held out
// of its batch for them.
func checkCollected(amount *int64, purpose, note string) error {
	purpose = strings.TrimSpace(purpose)
	switch {
	case amount != nil && *amount < 100:
		return refusePay(http.StatusBadRequest, "amount", "Pay at least ₦1.")
	case amount != nil && *amount > 1_000_000_000_00:
		return refusePay(http.StatusBadRequest, "amount", "That is more than one payment may be.")
	case purpose != "" && !slices.Contains(paymentPurposes, purpose):
		return refusePay(http.StatusBadRequest, "purpose", "Say what it is for: a bill, wages, a delivery, a refund, stock bought, or other.")
	case len([]rune(strings.TrimSpace(note))) > 100:
		return refusePay(http.StatusBadRequest, "note", "The note is too long.")
	}
	return nil
}

// patchCollected is PATCH /v1/pay-collection/{id} {amount_kobo, purpose,
// note}: a row's amount and what it is for, as the list is checked.
func (s *Server) patchCollected(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.payFail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		AmountKobo *int64  `json:"amount_kobo"`
		Purpose    *string `json:"purpose"`
		Note       *string `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	purpose, note := "", ""
	if in.Purpose != nil {
		purpose = *in.Purpose
	}
	if in.Note != nil {
		note = *in.Note
	}
	if err := checkCollected(in.AmountKobo, purpose, note); err != nil {
		s.payFail(w, r, err)
		return
	}
	var out collection
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE pay_collected SET amount_kobo = COALESCE($4, amount_kobo),
			purpose = CASE WHEN $5::boolean THEN $6 ELSE purpose END, note = CASE WHEN $7::boolean THEN $8 ELSE note END
			WHERE id = $1 AND tenant_id = $2 AND user_id = $3 AND batch_id IS NULL`,
			id, p.TenantID, p.UserID, in.AmountKobo, in.Purpose != nil, strings.TrimSpace(purpose), in.Note != nil, strings.TrimSpace(note))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		out, err = collectionTx(ctx, tx, p.TenantID, p.UserID)
		return err
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// removeCollected is DELETE /v1/pay-collection/{id}: one row off the
// list. DELETE /v1/pay-collection clears it (rows waiting in a batch stay
// with the batch).
func (s *Server) removeCollected(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	one := chi.URLParam(r, "id")
	var out collection
	err := s.tx(r, func(tx pgx.Tx) error {
		if one != "" {
			id, err := uuid.Parse(one)
			if err != nil {
				return pgx.ErrNoRows
			}
			tag, err := tx.Exec(ctx, `DELETE FROM pay_collected WHERE id = $1 AND tenant_id = $2 AND user_id = $3 AND batch_id IS NULL`, id, p.TenantID, p.UserID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM pay_collected WHERE tenant_id = $1 AND user_id = $2 AND batch_id IS NULL`, p.TenantID, p.UserID); err != nil {
			return err
		}
		var err error
		out, err = collectionTx(ctx, tx, p.TenantID, p.UserID)
		return err
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// payCollection is POST /v1/pay-collection/batch {title?, purpose?}: the
// list becomes a batch (kind messages), each row prepared and checked as
// one payment is, those failing held out with why; nothing is sent. Every
// row needs an amount; one purpose fills the rows without one. The rows
// wait with the batch: sent, they leave the list; cancelled, they come
// back.
func (s *Server) payCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var in struct {
		Title   string `json:"title"`
		Purpose string `json:"purpose"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := checkCollected(nil, in.Purpose, ""); err != nil {
		s.payFail(w, r, err)
		return
	}
	var ids []uuid.UUID
	var rows []batchRow
	err := s.tx(r, func(tx pgx.Tx) error {
		c, err := collectionTx(ctx, tx, p.TenantID, p.UserID)
		if err != nil {
			return err
		}
		if c.InBatch != nil {
			return refusePay(http.StatusConflict, "in_batch", "Some of your list waits in a batch: send it or cancel it first.")
		}
		if c.Count == 0 {
			return refusePay(http.StatusBadRequest, "rows", "Your list is empty: add someone to pay.")
		}
		if c.NoAmount > 0 {
			return refusePay(http.StatusBadRequest, "amount", "Say how much for each person first.")
		}
		list, err := tx.Query(ctx, `SELECT id, saved_id, account_sealed, bank_name, amount_kobo, purpose, note FROM pay_collected
			WHERE tenant_id = $1 AND user_id = $2 AND batch_id IS NULL ORDER BY created_at, id`, p.TenantID, p.UserID)
		if err != nil {
			return err
		}
		type raw struct {
			id      uuid.UUID
			saved   *uuid.UUID
			sealed  map[string]any
			bank    string
			amount  int64
			purpose string
			note    string
		}
		got, err := pgx.CollectRows(list, func(row pgx.CollectableRow) (raw, error) {
			var x raw
			return x, row.Scan(&x.id, &x.saved, &x.sealed, &x.bank, &x.amount, &x.purpose, &x.note)
		})
		if err != nil {
			return err
		}
		for _, x := range got {
			in2 := payIn{SavedID: x.saved, AmountKobo: x.amount, Purpose: x.purpose, Note: x.note}
			if in2.Purpose == "" {
				in2.Purpose = strings.TrimSpace(in.Purpose)
			}
			if x.saved == nil {
				v, err := s.Store.PII.OpenTx(ctx, tx, p.TenantID, x.sealed)
				if err != nil {
					return err
				}
				in2.AccountNumber, in2.BankName = fmt.Sprint(v), x.bank
			}
			ids = append(ids, x.id)
			rows = append(rows, batchRow{payIn: in2})
		}
		return nil
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "From messages"
	}
	b, made, err := s.prepareBatchView(r, rows, title, "messages", nil, "message")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if b.Status != "cancelled" {
		// Only the rows that became payments wait with the batch: one held
		// out stays on the list. Rows another Check took meanwhile (a
		// second tap, another device) are not paid twice: this batch is
		// let go instead.
		var claim, pays []uuid.UUID
		for i, pid := range made {
			if pid != uuid.Nil {
				claim, pays = append(claim, ids[i]), append(pays, pid)
			}
		}
		taken := false
		if err := s.tx(r, func(tx pgx.Tx) error {
			free, err := tx.Query(ctx, `SELECT id FROM pay_collected WHERE tenant_id = $1 AND user_id = $2 AND id = ANY ($3) AND batch_id IS NULL FOR UPDATE`,
				p.TenantID, p.UserID, claim)
			if err != nil {
				return err
			}
			got, err := pgx.CollectRows(free, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			if len(got) != len(claim) {
				taken = true
				if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'cancelled', finished_at = now() WHERE batch_id = $1 AND status = 'prepared'`, b.ID); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE payment_batches SET status = 'cancelled' WHERE id = $1 AND status = 'prepared'`, b.ID)
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE pay_collected c SET batch_id = $3, payment_id = x.pid FROM unnest($2::uuid[], $4::uuid[]) AS x(id, pid)
				WHERE c.tenant_id = $1 AND c.id = x.id`, p.TenantID, claim, b.ID, pays)
			return err
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if taken {
			s.payFail(w, r, refusePay(http.StatusConflict, "in_batch", "Some of your list waits in a batch: send it or cancel it first."))
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"batch": b})
}

package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Paying from a message (paying people, phase 2): account details in a
// message ("0123456789 GTBank Chidi Okeke", "Acct: 0123456789, Access,
// send 45k") are read for a payment: the number, the bank and its code in
// the payments account's list (or the banks it can be at), the amount
// when the words say one, and the name they give; and whether the business
// paid that account before, so a chip already paid says so. Nothing is
// prepared: the Pay sheet opens filled in, and the person confirms.

// payAmountWords is an amount in a message: naira or k ("45k", "₦45,000",
// "N45000", "45,000 naira"), not a bare number.
var payAmountWords = regexp.MustCompile(`(?i)(?:₦|\bngn\s?|\bn)\s?(\d[\d,]*(?:\.\d{1,2})?)\s?(k)?\b|\b(\d[\d,]*(?:\.\d{1,2})?)\s?(k)\b|\b(\d[\d,]*(?:\.\d{1,2})?)\s?naira\b`)

type payReading struct {
	Found         bool       `json:"found"`
	AccountNumber string     `json:"account_number,omitempty"`
	BankName      string     `json:"bank_name,omitempty"`
	BankCode      string     `json:"bank_code,omitempty"`
	AmountKobo    int64      `json:"amount_kobo,omitempty"`
	Name          string     `json:"name,omitempty"`
	Likely        []bankItem `json:"likely,omitempty"`
	// Paid: the business's last payment to this account, when it made one.
	Paid *payReadPaid `json:"paid,omitempty"`
	// Payee: the business's saved account with this number, by its name
	// (staff pay saved accounts only).
	Payee string `json:"payee,omitempty"`
}

type payReadPaid struct {
	ID         uuid.UUID `json:"id"`
	AmountKobo int64     `json:"amount_kobo"`
	Status     string    `json:"status"`
	At         time.Time `json:"at"`
}

// readPayment is POST /v1/payments/read {text}: what a message says to pay.
func (s *Server) readPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.Text) > 4000 {
		in.Text = in.Text[:4000]
	}
	reading := saved.Read(in.Text)
	number := reading.Details["account_number"]
	if reading.Kind != saved.KindBankAccount || number == "" {
		writeJSON(w, http.StatusOK, payReading{})
		return
	}
	out := payReading{Found: true, AccountNumber: number, BankName: reading.Details["bank_name"], Name: reading.Title}
	// The amount, with the account number and phone numbers set aside.
	rest := strings.ReplaceAll(in.Text, number, " ")
	if m := payAmountWords.FindStringSubmatch(rest); m != nil {
		tok := ""
		for _, g := range [][2]int{{1, 2}, {3, 4}, {5, 0}} {
			if m[g[0]] != "" {
				tok = m[g[0]]
				if g[1] > 0 {
					tok += m[g[1]]
				}
				break
			}
		}
		if kobo, ok := parseKobo(tok); ok {
			out.AmountKobo = kobo
		}
	}
	tenant := principalFrom(ctx).TenantID
	if rl, err := s.payRail(ctx, tenant); err == nil && rl != nil {
		if items, err := s.payBanks(ctx, tenant, *rl, number); err == nil {
			if out.BankName != "" {
				if m, ok := matchBank(items, out.BankName); ok {
					out.BankName, out.BankCode = m.Name, m.Code
				}
			}
			if out.BankCode == "" {
				for _, it := range items {
					if it.Likely && len(out.Likely) < 5 {
						out.Likely = append(out.Likely, it)
					}
				}
			}
		}
	}
	// Saved already: the business's saved account with this number.
	o := savedOwner{tenant: tenant, by: principalFrom(ctx).UserID}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, who := ownerWhere(o)
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND kind = $2 AND details->>'last4' = $3`, who, saved.KindBankAccount, saved.Last4(number))
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		for _, x := range list {
			n, err := s.openSaved(ctx, tx, o, x.ID, x.Sealed)
			if err != nil {
				return err
			}
			if n == number {
				out.Payee = x.Name
				return nil
			}
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	// Paid before: the newest payment to this number (by its last four,
	// then opened to be sure).
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, amount_kobo, status, COALESCE(confirmed_at, created_at), account_sealed FROM payments
			WHERE tenant_id = $1 AND last4 = $2 AND status IN ('sending', 'waiting_ok', 'paid', 'unknown') ORDER BY created_at DESC LIMIT 20`, tenant, saved.Last4(number))
		if err != nil {
			return err
		}
		type row struct {
			p      payReadPaid
			sealed map[string]any
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.p.ID, &x.p.AmountKobo, &x.p.Status, &x.p.At, &x.sealed)
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			v, err := s.Store.PII.OpenTx(ctx, tx, tenant, x.sealed)
			if err != nil {
				return err
			}
			if strings.TrimSpace(toString(v)) == number {
				p := x.p
				out.Paid = &p
				return nil
			}
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

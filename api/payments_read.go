package api

import (
	"net/http"
	"regexp"
	"slices"
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
	if !s.limiter("pay-read:"+principalFrom(ctx).Actor(), time.Minute/30, 30).Allow() {
		s.payFail(w, r, refusePay(http.StatusTooManyRequests, "too_many", "Too many messages read just now: wait a minute."))
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
	// The amount: the account number (however it was spaced) and phone
	// numbers set aside; one amount, or the one after pay, send or
	// transfer; none when it is not clear which.
	out.AmountKobo = readPayAmount(in.Text, number)
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
		// An owner's own payments are theirs to see; one waiting for the
		// owner's OK is not paid.
		rows, err := tx.Query(ctx, `SELECT id, amount_kobo, status, COALESCE(confirmed_at, created_at), account_sealed FROM payments
			WHERE tenant_id = $1 AND last4 = $2 AND status IN ('sending', 'paid', 'unknown') AND (NOT personal OR $3)
			ORDER BY created_at DESC LIMIT 20`, tenant, saved.Last4(number), slices.Contains(principalFrom(ctx).Roles, "owner"))
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

var (
	payDigitRun  = regexp.MustCompile(`\d(?:[\s-]?\d){5,15}`)
	payPhone     = regexp.MustCompile(`(?:\+\s?234|\b0)[\s-]?[789](?:[\s-]?\d){9}\b`)
	payWordsNear = regexp.MustCompile(`(?i)\b(?:pay|send|transfer|sent)\b\W*(?:\w+\W+){0,3}$`)
)

// readPayAmount is the amount a message says to pay, in kobo (0: none, or
// not clear which).
func readPayAmount(text, number string) int64 {
	text = payPhone.ReplaceAllString(text, " ")
	text = payDigitRun.ReplaceAllStringFunc(text, func(run string) string {
		if strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, run) == number {
			return " "
		}
		return run
	})
	type found struct {
		kobo int64
		near bool
	}
	var all []found
	for _, m := range payAmountWords.FindAllStringSubmatchIndex(text, -1) {
		tok := ""
		for _, g := range [][2]int{{1, 2}, {3, 4}, {5, 0}} {
			if m[2*g[0]] >= 0 {
				tok = text[m[2*g[0]]:m[2*g[0]+1]]
				if g[1] > 0 && m[2*g[1]] >= 0 {
					tok += text[m[2*g[1]]:m[2*g[1]+1]]
				}
				break
			}
		}
		if kobo, ok := parseKobo(tok); ok {
			all = append(all, found{kobo, payWordsNear.MatchString(text[:m[0]])})
		}
	}
	switch {
	case len(all) == 1:
		return all[0].kobo
	case len(all) > 1:
		var near []int64
		for _, f := range all {
			if f.near {
				near = append(near, f.kobo)
			}
		}
		if len(near) == 1 {
			return near[0]
		}
	}
	return 0
}

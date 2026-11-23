package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/templates"
)

// Paying one person (P1; docs/spec/chat-pay-people.md). A payment is a
// record first and a workflow run second: it is prepared (from Tools, the
// chat, or a workflow's payments@1 prepare) to one of the business's saved
// bank accounts, or an owner's own; a person who may pay (money.pay)
// confirms it with a passkey; then the business's managed pay-person
// workflow approves it under the taskiem_payments policy (the payer's own
// confirmation within their limit, else the owner's), checks the name,
// pays with the payment's own key, waits for the bank and records how it
// ended (payments@1 finish).

const (
	payPersonTemplate = "pay-person"
	payPolicy         = "taskiem_payments"
	// payerRole is who decides a payment within the payer's limit: anyone
	// who may pay, voting with the passkey they confirmed it with.
	payerRole = "money_pay"
	// The step-up operations, each for "payment:<id>".
	payOpConfirm = "money.pay"
	payOpApprove = "money.pay.approve"
)

// paymentPurposes are what a payment may be for, as the money log keeps it.
var paymentPurposes = []string{"bill", "wages", "delivery", "refund", "stock", "other"}

// paymentRoutes are the payments' routes.
func (s *Server) paymentRoutes(r chi.Router) {
	pay := s.need(PermMoneyPay)
	r.With(pay).Get("/payments", s.listPayments)
	// The banks to pay to, for those who may pay but not manage saved
	// things: the list the Saved page offers.
	r.With(pay).Get("/payments/banks", s.listPayBanks)
	r.With(pay).Get("/payments/staff", s.payStaff)
	r.With(pay, s.tenantWide).Post("/payments/name-check", s.payNameCheck) // owners only, checked inside
	r.With(pay).Post("/payments/read", s.readPayment)
	r.With(pay, s.tenantWide).Post("/payments", s.createPayment)
	r.With(pay).Get("/payments/{id}", s.getPayment)
	r.With(pay, s.tenantWide).Post("/payments/{id}/confirm", s.confirmPayment)
	r.With(pay, s.tenantWide).Post("/payments/{id}/cancel", s.cancelPayment)
	r.With(pay).Get("/pay-limits", s.getPayLimits)
	r.With(pay, s.tenantWide).Put("/pay-limits/{role}", s.putPayLimit)
	// Collecting from several messages: the person's own list (pay_collect.go).
	r.With(pay).Get("/pay-collection", s.getCollection)
	r.With(pay, s.tenantWide).Post("/pay-collection", s.addCollected)
	r.With(pay, s.tenantWide).Delete("/pay-collection", s.removeCollected)
	r.With(pay, s.tenantWide).Post("/pay-collection/batch", s.payCollection)
	r.With(pay, s.tenantWide).Patch("/pay-collection/{id}", s.patchCollected)
	r.With(pay, s.tenantWide).Delete("/pay-collection/{id}", s.removeCollected)
}

// payment is a payment as the API shows it: the account masked.
type payment struct {
	ID             uuid.UUID  `json:"id"`
	SavedID        *uuid.UUID `json:"saved_id"`
	Personal       bool       `json:"personal"`
	Payee          string     `json:"payee"`
	BankName       string     `json:"bank_name"`
	Account        string     `json:"account"`
	AccountName    *string    `json:"account_name"`
	AmountKobo     int64      `json:"amount_kobo"`
	Purpose        string     `json:"purpose"`
	PurposeNote    string     `json:"purpose_note"`
	Note           string     `json:"note"`
	FirstPayment   bool       `json:"first_payment"`
	ChangedAccount bool       `json:"changed_account"`
	NeedsOK        string     `json:"needs_ok"`
	Status         string     `json:"status"`
	Reason         *string    `json:"reason"`
	FeeKobo        *int64     `json:"fee_kobo"`
	RunID          *uuid.UUID `json:"run_id"`
	StartedFrom    string     `json:"started_from"`
	PreparedBy     string     `json:"prepared_by"`
	ConfirmedBy    *string    `json:"confirmed_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ConfirmedAt    *time.Time `json:"confirmed_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	// Again: the same amount to the same payee in the last day; confirming
	// needs again: true.
	Again bool `json:"again"`
	// AwaitsYourOK: it was confirmed by the person asking and its run
	// still waits for their own OK, which did not reach it: confirming it
	// again gives it.
	AwaitsYourOK bool `json:"awaits_your_ok"`

	bankCode string
	sealed   map[string]any
	// provider: the payments account (its channel) it was prepared on,
	// whose list its bank code is from.
	provider string
	// RiderID: the rider paid, when it was one; BatchID the list it was
	// paid in, when it was.
	RiderID *uuid.UUID `json:"rider_id"`
	BatchID *uuid.UUID `json:"batch_id"`
	// StaffID: the staff member paid, when it was one.
	StaffID *uuid.UUID `json:"staff_id"`
}

const paymentCols = `id, saved_id, personal, payee_title, bank_name, last4, account_name, amount_kobo, purpose, purpose_note, note, first_payment,
	changed_account, needs_ok, status, reason, fee_kobo, run_id, started_from, prepared_by, confirmed_by, created_at, confirmed_at, finished_at, bank_code, account_sealed, rider_id, batch_id, staff_id, provider_channel`

func scanPayment(r pgx.Row) (payment, error) {
	var x payment
	var last4 string
	err := r.Scan(&x.ID, &x.SavedID, &x.Personal, &x.Payee, &x.BankName, &last4, &x.AccountName, &x.AmountKobo, &x.Purpose, &x.PurposeNote, &x.Note,
		&x.FirstPayment, &x.ChangedAccount, &x.NeedsOK, &x.Status, &x.Reason, &x.FeeKobo, &x.RunID, &x.StartedFrom, &x.PreparedBy, &x.ConfirmedBy,
		&x.CreatedAt, &x.ConfirmedAt, &x.FinishedAt, &x.bankCode, &x.sealed, &x.RiderID, &x.BatchID, &x.StaffID, &x.provider)
	x.Account = "••" + last4
	return x, err
}

// payIn is a payment to prepare.
type payIn struct {
	// SavedID or Payee (its @name) is the business's saved bank account;
	// Mine, an owner's own (only an owner's, recorded as personal).
	SavedID *uuid.UUID `json:"saved_id"`
	Payee   string     `json:"payee"`
	Mine    bool       `json:"mine"`
	// RiderID: one of the business's riders, to the bank account kept for
	// their payouts.
	RiderID *uuid.UUID `json:"rider_id"`
	// StaffID: a staff member, to the saved account linked to them.
	StaffID *uuid.UUID `json:"staff_id"`
	// AccountNumber and BankName: an account not saved (pay.account),
	// its name checked with the bank as it is prepared.
	AccountNumber string `json:"account_number"`
	BankName      string `json:"bank_name"`
	AmountKobo    int64  `json:"amount_kobo"`
	Purpose       string `json:"purpose"`
	PurposeNote   string `json:"purpose_note"`
	Note          string `json:"note"`
	From          string `json:"from"`
}

// payRefused is a payment refused in words, with a code for the clients.
type payRefused struct {
	status int
	code   string
	msg    string
}

func (e payRefused) Error() string { return e.msg }

func refusePay(status int, code, msg string) error { return payRefused{status, code, msg} }

// payFail answers a payment's refusal, or any other failure.
func (s *Server) payFail(w http.ResponseWriter, r *http.Request, err error) {
	var pr payRefused
	if errors.As(err, &pr) {
		writeJSON(w, pr.status, map[string]any{"error": pr.msg, "code": pr.code})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.fail(w, r, err)
}

// payRail is the payments account people are paid from: the first that
// can pay bank accounts and check names, in the order names are checked
// (the Taskiem wallet first); nil when the business has none.
func (s *Server) payRail(ctx context.Context, tenant uuid.UUID) (*invoicing.Rail, error) {
	rails := s.accountRails()
	if rails == nil {
		return nil, nil
	}
	list, err := rails.Capable(ctx, tenant, "pay_bank")
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(rl invoicing.Rail) bool { return !rl.Maps("resolve_account") })
	if len(list) == 0 {
		return nil, nil
	}
	slices.SortStableFunc(list, func(a, b invoicing.Rail) int { return railRank(a) - railRank(b) })
	return &list[0], nil
}

// payeeAccount is where a payment goes: the saved account it is made
// from, opened.
type payeeAccount struct {
	saved    *uuid.UUID
	rider    *uuid.UUID
	staff    *uuid.UUID
	personal bool
	title    string
	bank     saved.BankAccount
	checked  *string
}

// payeeOf reads the saved account a payment is made to: the business's,
// by id or name; or, for an owner, their own.
func (s *Server) payeeOf(ctx context.Context, tenant, person uuid.UUID, owner bool, in payIn) (payeeAccount, error) {
	if in.StaffID != nil {
		if in.SavedID != nil || in.Payee != "" || in.Mine || in.AccountNumber != "" || in.RiderID != nil {
			return payeeAccount{}, refusePay(http.StatusBadRequest, "payee", "Pay a staff member, a rider, a saved account or an account number: one of them.")
		}
		var id *uuid.UUID
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			id, err = s.staffPayee(ctx, tx, tenant, *in.StaffID)
			return err
		}); err != nil {
			return payeeAccount{}, err
		}
		staff := *in.StaffID
		in.SavedID, in.StaffID = id, nil
		out, err := s.payeeOf(ctx, tenant, person, owner, in)
		out.staff = &staff
		return out, err
	}
	if in.RiderID != nil {
		if in.SavedID != nil || in.Payee != "" || in.Mine || in.AccountNumber != "" {
			return payeeAccount{}, refusePay(http.StatusBadRequest, "payee", "Pay a rider, a saved account or an account number: one of them.")
		}
		return s.riderPayee(ctx, tenant, *in.RiderID)
	}
	if in.AccountNumber != "" {
		if !owner {
			return payeeAccount{}, refusePay(http.StatusForbidden, "saved_only", "Staff pay the business's saved accounts: ask an owner to save this one first.")
		}
		if in.SavedID != nil || in.Payee != "" || in.Mine {
			return payeeAccount{}, refusePay(http.StatusBadRequest, "payee", "Pay a saved account or an account number, not both.")
		}
		b, err := saved.BankAccount{BankName: in.BankName, AccountNumber: in.AccountNumber}.Clean()
		if err != nil {
			return payeeAccount{}, refusePay(http.StatusBadRequest, "account", refusedWords(err))
		}
		return payeeAccount{title: "Account " + saved.Masked(b.AccountNumber), bank: b}, nil
	}
	o := savedOwner{tenant: tenant, by: person}
	if in.Mine {
		if !owner || person == uuid.Nil {
			return payeeAccount{}, refusePay(http.StatusForbidden, "not_yours", "Only an owner pays their own saved account from the business.")
		}
		o = savedOwner{person: person, by: person}
	}
	var out payeeAccount
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, who := ownerWhere(o)
		var row pgx.Row
		switch {
		case in.SavedID != nil:
			row = tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND id = $2`, who, *in.SavedID)
		default:
			name, err := saved.Name(in.Payee)
			if err != nil {
				return refusePay(http.StatusBadRequest, "no_payee", "Say who to pay: one of your saved accounts, like @bayo.")
			}
			row = tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND name = $2`, who, name)
		}
		x, err := scanSaved(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return refusePay(http.StatusNotFound, "no_payee", "There is no saved account by that name.")
		}
		if err != nil {
			return err
		}
		if x.Kind != saved.KindBankAccount {
			return refusePay(http.StatusBadRequest, "not_an_account", "@"+x.Name+" is not a bank account.")
		}
		number, err := s.openSaved(ctx, tx, o, x.ID, x.Sealed)
		if err != nil {
			return err
		}
		b := saved.FromDetails(x.Details)
		b.AccountNumber = number
		id := x.ID
		out = payeeAccount{title: x.Title, bank: b, checked: x.CheckedName, personal: in.Mine, saved: &id}
		return nil
	})
	return out, err
}

// riderPayee is a rider's bank account, as their payouts use it.
func (s *Server) riderPayee(ctx context.Context, tenant, rider uuid.UUID) (payeeAccount, error) {
	if s.Dispatch == nil || s.Dispatch.Vault == nil || !s.Riders.Ready() {
		return payeeAccount{}, refusePay(http.StatusConflict, "no_riders", "Riders are not set up here.")
	}
	var out payeeAccount
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rd, err := s.Riders.GetTx(ctx, tx, tenant, rider)
		if errors.Is(err, riders.ErrNoRider) {
			return refusePay(http.StatusNotFound, "no_payee", "There is no such rider.")
		}
		if err != nil {
			return err
		}
		name := rd.Name
		b, ok, err := s.Dispatch.BankOfTx(ctx, tx, tenant, rider)
		if err != nil {
			return err
		}
		if !ok {
			return refusePay(http.StatusConflict, "no_bank", name+" has no bank account kept: add it on their page under Riders > Pay.")
		}
		bank := b.BankName
		if bank == "" {
			bank = b.BankCode
		}
		// The name is checked with the bank as the payment is prepared,
		// not taken from what was kept (a payout may have left another
		// account's name there).
		id := rider
		out = payeeAccount{title: name, rider: &id, bank: saved.BankAccount{BankName: bank, BankCode: b.BankCode, AccountNumber: b.AccountNumber}}
		return nil
	})
	return out, err
}

// bankCodeFor is the payee's bank's code in the list rl pays to.
func (s *Server) bankCodeFor(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail, b saved.BankAccount, isSaved bool) (string, error) {
	if b.BankCode != "" && b.CodeFrom == rl.Channel() {
		return b.BankCode, nil
	}
	banks, err := s.savedBanks(ctx, tenant, rl)
	if err != nil {
		return "", err
	}
	if code, ok := links.BankCode(banks, b.BankName); ok {
		return code, nil
	}
	// As a file's bank is matched ("GTBank", "First Bank").
	if m, ok := matchBank(bankItems(banks), b.BankName); ok && m.Code != "" {
		return m.Code, nil
	}
	// A code kept with the account (a rider's) that the list has.
	if b.BankCode != "" && slices.ContainsFunc(bankItems(banks), func(it bankItem) bool { return it.Code == b.BankCode }) {
		return b.BankCode, nil
	}
	again := "choose the bank again on the account in Saved."
	if !isSaved {
		again = "choose the bank from its list."
	}
	return "", refusePay(http.StatusConflict, "bank_unknown", fmt.Sprintf("%s does not pay to %s by that name: %s", railTitle(rl), b.BankName, again))
}

// preparePayment makes a prepared payment: who, where and how much, with
// the checks a person confirming it sees (a first payment, an account
// changed since it was last paid, the same payment again). by is who (or
// which run) prepared it.
func (s *Server) preparePayment(ctx context.Context, tenant, person uuid.UUID, owner bool, in payIn, by string) (payment, error) {
	in.Purpose = strings.TrimSpace(in.Purpose)
	if in.Purpose == "" {
		in.Purpose = "other"
	}
	switch {
	case !slices.Contains(paymentPurposes, in.Purpose):
		return payment{}, refusePay(http.StatusBadRequest, "purpose", "Say what it is for: a bill, wages, a delivery, a refund, stock bought, or other.")
	case in.AmountKobo < 100:
		return payment{}, refusePay(http.StatusBadRequest, "amount", "Pay at least ₦1.")
	case in.AmountKobo > 1_000_000_000_00:
		return payment{}, refusePay(http.StatusBadRequest, "amount", "That is more than one payment may be.")
	case len([]rune(in.PurposeNote)) > 80 || len([]rune(in.Note)) > 100:
		return payment{}, refusePay(http.StatusBadRequest, "note", "The note is too long.")
	}
	from := in.From
	switch from {
	case "", "tools":
		from = "tools"
	case "chat", "message", "card", "workflow":
	default:
		return payment{}, refusePay(http.StatusBadRequest, "from", "That is not where a payment starts.")
	}
	rl, err := s.payRail(ctx, tenant)
	if err != nil {
		return payment{}, err
	}
	if rl == nil {
		return payment{}, refusePay(http.StatusConflict, "no_provider", "Connect a payments account first: people are paid from it.")
	}
	payee, err := s.payeeOf(ctx, tenant, person, owner, in)
	if err != nil {
		return payment{}, err
	}
	code, err := s.bankCodeFor(ctx, tenant, *rl, payee.bank, in.AccountNumber == "")
	if err != nil {
		return payment{}, err
	}
	if in.AccountNumber != "" || in.RiderID != nil {
		// An account not saved (or a rider's): its name checked with the
		// bank now, as the person confirming must see it.
		payee.bank.BankCode, payee.bank.CodeFrom = code, rl.Channel()
		res, problem, err := s.checkWithRail(ctx, tenant, *rl, payee.bank)
		if w := refusedWords(err); w != "" {
			return payment{}, refusePay(http.StatusBadRequest, "no_account", w)
		}
		if err != nil {
			return payment{}, err
		}
		if res == nil {
			// Said as it is for this payment: it is not prepared, and
			// nothing checks it again later (as a saved account is).
			why := "The bank did not say the account's name just now: try again in a moment."
			switch {
			case problem.Limited:
				why = "Too many accounts to check at once: add it again in a minute."
			case problem.Lasting && problem.Words != "":
				why = problem.Words
			}
			return payment{}, refusePay(http.StatusConflict, "name_not_checked", why)
		}
		payee.checked = &res.AccountName
		if in.RiderID == nil {
			payee.title = res.AccountName
		}
	}
	var x payment
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// A first payment to the account, as Approvals says it.
		// A first payment to the account: never paid by the business, as
		// the money log or its own payments know it. Staff need the owner's
		// OK for one (an account saved again under an old name with another
		// number is one).
		paid := false
		if subjects, ok := s.Store.PII.(moneylog.Subjects); ok {
			was, known, err := moneylog.PaidBefore(ctx, tx, subjects, tenant, payee.bank.AccountNumber, code)
			if err != nil {
				return err
			}
			paid = known && was
		}
		if !paid {
			var err error
			if paid, err = s.paidThroughPayments(ctx, tx, tenant, code, payee.bank.AccountNumber); err != nil {
				return err
			}
		}
		first := !paid
		// The account changed since this payee was last paid: the owner's
		// OK, whoever pays.
		changed := false
		if payee.saved != nil || payee.rider != nil || payee.staff != nil {
			// The payee's last payment: to the staff member (whichever
			// account they were linked to), else the rider, else the saved
			// account.
			var last map[string]any
			var lastCode, lastProvider string
			err := tx.QueryRow(ctx, `SELECT account_sealed, bank_code, provider_channel FROM payments WHERE tenant_id = $1
				AND CASE WHEN $4::uuid IS NOT NULL THEN staff_id = $4 WHEN $3::uuid IS NOT NULL THEN rider_id = $3 ELSE saved_id = $2 END
				AND status IN ('paid', 'sending', 'unknown') ORDER BY created_at DESC LIMIT 1`, tenant, payee.saved, payee.rider, payee.staff).Scan(&last, &lastCode, &lastProvider)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			default:
				was, err := s.Store.PII.OpenTx(ctx, tx, tenant, last)
				if err != nil {
					return err
				}
				// Bank codes are each payments account's own: compared
				// only when the last payment went through the same one.
				changed = fmt.Sprint(was) != payee.bank.AccountNumber || lastProvider == rl.Channel() && lastCode != code
			}
		}
		sealed, err := s.Store.PII.SealTx(ctx, tx, tenant, "account_number", payee.bank.AccountNumber)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		row := tx.QueryRow(ctx, `INSERT INTO payments (id, tenant_id, saved_id, personal, payee_title, bank_name, bank_code, account_sealed, last4, account_name,
			amount_kobo, purpose, purpose_note, note, first_payment, changed_account, started_from, prepared_by, rider_id, staff_id, provider_channel)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21) RETURNING `+paymentCols,
			id, tenant, payee.saved, payee.personal, payee.title, payee.bank.BankName, code, sealed, saved.Last4(payee.bank.AccountNumber), payee.checked,
			in.AmountKobo, in.Purpose, strings.TrimSpace(in.PurposeNote), strings.TrimSpace(in.Note), first, changed, from, by, payee.rider, payee.staff, rl.Channel())
		if x, err = scanPayment(row); err != nil {
			return err
		}
		x.Again, err = paidAgainTx(ctx, tx, tenant, x)
		return err
	})
	return x, err
}

// paidThroughPayments says whether the business paid the account through
// Taskiem's own payments before (sent, paid, or not known yet).
func (s *Server) paidThroughPayments(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, code, number string) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT account_sealed FROM payments WHERE tenant_id = $1 AND bank_code = $2 AND last4 = $3
		AND status IN ('sending', 'paid', 'unknown') ORDER BY created_at DESC LIMIT 50`, tenant, code, saved.Last4(number))
	if err != nil {
		return false, err
	}
	sealed, err := pgx.CollectRows(rows, pgx.RowTo[map[string]any])
	if err != nil {
		return false, err
	}
	for _, x := range sealed {
		v, err := s.Store.PII.OpenTx(ctx, tx, tenant, x)
		if err != nil {
			return false, err
		}
		if fmt.Sprint(v) == number {
			return true, nil
		}
	}
	return false, nil
}

// paidAgainTx says whether the same amount went (or is going) to the same
// payee in the last day.
func paidAgainTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x payment) (bool, error) {
	// The same payee: a saved account by its id; an account number not
	// saved by its bank and last four digits (its number is sealed).
	var again bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments p WHERE p.tenant_id = $1 AND p.amount_kobo = $3 AND p.id <> $4
		AND p.status IN ('sending', 'waiting_ok', 'paid', 'unknown') AND p.created_at > now() - interval '1 day'
		AND CASE WHEN $2::uuid IS NOT NULL THEN p.saved_id = $2 ELSE p.saved_id IS NULL AND NOT p.personal AND p.bank_code = $5 AND p.last4 = $6 END)`,
		tenant, x.SavedID, x.AmountKobo, x.ID, x.bankCode, strings.TrimPrefix(x.Account, "••")).Scan(&again)
	return again, err
}

// createPayment is POST /v1/payments: a payment prepared, with what the
// person confirming it should know.
func (s *Server) createPayment(w http.ResponseWriter, r *http.Request) {
	var in payIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	x, err := s.preparePayment(r.Context(), p.TenantID, p.UserID, slices.Contains(p.Roles, "owner"), in, p.Actor())
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "payment.prepare", x.ID.String(), map[string]any{"amount_kobo": x.AmountKobo, "purpose": x.Purpose, "payee": x.Payee, "personal": x.Personal})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"payment": x})
}

// payLimitOf is what the person may pay without the owner's OK, by their
// roles' limits (the largest of them): one payment and a day's total. An
// owner has none.
func payLimitOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, roles []string) (one, day int64, err error) {
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(one_kobo), 0), COALESCE(max(day_kobo), 0) FROM pay_limits WHERE tenant_id = $1 AND role = ANY($2)`,
		tenant, roles).Scan(&one, &day)
	return one, day, err
}

type confirmIn struct {
	Passkey *credentialJSON `json:"passkey"`
	TOTP    string          `json:"totp"`
	// Pin: the person's PIN, the passkey's fallback within the PIN's own
	// limits (payments_pin.go), typed in the app's own pad.
	Pin string `json:"pin"`
	// Again confirms the same payment as one made in the last day.
	Again bool `json:"again"`
}

// payPasskey is the step-up for paying: a passkey for op on target, never
// an authenticator code (docs/spec/chat-pay-people.md, Safety: a passkey
// for every payment; the PIN, its fallback for one payment, is
// payments_pin.go's, and never a batch's). Asked for with no proof, it
// names the passkey as the only way; a code given is refused (403
// passkey) without being spent.
func (s *Server) payPasskey(w http.ResponseWriter, r *http.Request, proof stepUpProof, op, target, what, doing string) (string, bool) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": what + " are made by a person who confirms with a passkey, not with an API key", "step_up": "required"})
		return "", false
	}
	if proof.Passkey == nil && proof.TOTP != "" {
		s.payFail(w, r, refusePay(http.StatusForbidden, "passkey", "Paying is confirmed with a passkey, not an authenticator code."))
		return "", false
	}
	if proof.Passkey == nil {
		f, _, err := s.factorsOf(r.Context(), p.TenantID, p.UserID)
		if err != nil {
			s.fail(w, r, err)
			return "", false
		}
		methods, msg := []string{}, "add a passkey under Account first: "+doing+" needs one"
		if f.Passkey {
			methods, msg = []string{"passkey"}, "confirm with your passkey"
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": msg, "step_up": "required", "methods": methods, "operation": op, "target": target})
		return "", false
	}
	method, ok := s.personStepUp(w, r, stepUpProof{Passkey: proof.Passkey}, op, target, what, doing)
	if ok && method != "passkey" {
		s.payFail(w, r, refusePay(http.StatusForbidden, "passkey", "Paying is confirmed with a passkey."))
		return "", false
	}
	return method, ok
}

// confirmPayment is POST /v1/payments/{id}/confirm {passkey, again?}: a
// person who may pay sends it, with a passkey for this payment (an
// authenticator code does not pay). Within their limit (an owner has none)
// it goes at once; over it, or to an account changed since it was last
// paid, it waits for the owner's OK in Approvals. An owner's passkey is
// the OK for a changed account they pay themselves: the payment said so
// (changed_account) when it was prepared. A payment whose account, or the
// business's payments account, changed since it was prepared is refused
// (409 account_changed, provider_changed) and cancelled, to be prepared
// again. One sent whose payer's own OK did not reach its run is confirmed
// again here, by that payer, to give it.
func (s *Server) confirmPayment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in confirmIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Pin != "" {
		s.confirmPaymentPin(w, r, id, in)
		return
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	owner := slices.Contains(p.Roles, "owner")
	// Checked before the passkey is asked (the same payment again is asked
	// first), and again as the payment is claimed once it is given.
	var x payment
	revote := false
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, _, err = confirmCheckTx(ctx, tx, p, owner, id, in.Again, false)
		var pr payRefused
		if errors.As(err, &pr) && pr.code == "not_prepared" {
			if revote, err = awaitsPayerTx(ctx, tx, x, p); err != nil {
				return err
			}
			if revote {
				return nil
			}
			return pr
		}
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	if !revote {
		if _, err := s.stillAsPrepared(ctx, p.TenantID, x); err != nil {
			s.payFail(w, r, s.dropChanged(r, x.ID, err))
			return
		}
	}
	if in.Passkey == nil && in.TOTP == "" && s.payOfferPin(w, r, x, revote) {
		return
	}
	method, ok := s.payPasskey(w, r, stepUpProof{TOTP: in.TOTP, Passkey: in.Passkey}, payOpConfirm, "payment:"+id.String(), "Payments", "paying")
	if !ok {
		return
	}
	// Once claimed, the payment is sent (or given back) even if the person
	// goes away meanwhile.
	r = r.WithContext(context.WithoutCancel(ctx))
	ctx = r.Context()
	var run uuid.UUID
	if revote {
		run = *x.RunID
		if err := s.payerVote(r, run, p, method, "payment.confirm_again"); err != nil {
			s.Logger.Warn("payments: the payer's OK again", "payment", id, "err", err)
			s.payFail(w, r, errPayerOK)
			return
		}
	} else {
		var needs string
		if err := s.tx(r, func(tx pgx.Tx) error {
			var err error
			x, needs, err = confirmCheckTx(ctx, tx, p, owner, id, in.Again, true)
			return err
		}); err != nil {
			s.payFail(w, r, err)
			return
		}
		run, err = s.sendPayment(r, x, needs, p, method)
		if err != nil {
			if run != uuid.Nil {
				s.Logger.Warn("payments: the payer's OK", "payment", id, "err", err)
				err = errPayerOK
			}
			s.payFail(w, r, err)
			return
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1`, id))
		if err != nil {
			return err
		}
		return auditTx(r, tx, "payment.confirm", id.String(), map[string]any{"amount_kobo": x.AmountKobo, "needs_ok": x.NeedsOK, "run": run,
			"changed_account": x.ChangedAccount, "again": revote})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"payment": x})
}

// errPayerOK: the run started but the payer's own OK did not reach it, so
// nothing is paid yet; the payer confirms it again to give it.
var errPayerOK = refusePay(http.StatusConflict, "not_approved", "Nothing was paid yet: your OK did not reach the payment. Confirm it again in a moment.")

// awaitsPayerTx says whether x was sent by p within their limit and its run
// still waits for p's own OK (none was recorded): p confirming it again
// gives it.
func awaitsPayerTx(ctx context.Context, tx pgx.Tx, x payment, p *Principal) (bool, error) {
	if x.Status != "sending" || x.NeedsOK != "" || x.RunID == nil || x.ConfirmedBy == nil || *x.ConfirmedBy != p.Actor() {
		return false, nil
	}
	var waits bool
	err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM approval_decisions WHERE run_id = $1 AND step_id = 'approve')
		AND NOT EXISTS (SELECT 1 FROM approvals WHERE run_id = $1 AND step_id = 'approve' AND status <> 'open')`, *x.RunID).Scan(&waits)
	return waits, err
}

// markAwaitingTx sets AwaitsYourOK on the payments whose run waits for
// the asking person's own OK (a batch row that did not get it).
func markAwaitingTx(ctx context.Context, tx pgx.Tx, list []payment, p *Principal) error {
	for i := range list {
		w, err := awaitsPayerTx(ctx, tx, list[i], p)
		if err != nil {
			return err
		}
		list[i].AwaitsYourOK = w
	}
	return nil
}

// payerVote gives the payer's own OK to a payment's run (within their
// limit, their passkey is its approval), trying once more if the first
// does not take (the run may not have reached its approval yet).
func (s *Server) payerVote(r *http.Request, run uuid.UUID, p *Principal, method, via string) error {
	ctx := r.Context()
	channel := "web"
	if p.AuthMethod == "whatsapp" {
		channel = "whatsapp" // the PIN in WhatsApp's form (payments_pin.go)
	}
	vote := runtime.Vote{UserID: p.UserID, Roles: []string{payerRole}, Decision: "approved", Channel: channel, IP: clientIP(r), StepUp: method,
		Audit: func(tx pgx.Tx, res runtime.VoteResult) error {
			return auditTx(r, tx, "approval.decide", run.String()+"/approve", map[string]any{"decision": "approved", "result": res.Status, "via": via})
		}}
	ref := runtime.RunRef{ID: run, TenantID: p.TenantID}
	_, err := s.Store.VoteApproval(ctx, ref, "approve", vote)
	if err == nil || errors.Is(err, runtime.ErrApprovalClosed) || errors.Is(err, runtime.ErrAlreadyDecided) {
		return err
	}
	select {
	case <-time.After(700 * time.Millisecond):
	case <-ctx.Done():
		return err
	}
	_, err = s.Store.VoteApproval(ctx, ref, "approve", vote)
	return err
}

// accountChanged and providerChanged refuse a payment prepared to an
// account, or on a payments account, that is not the one now.
var (
	accountChanged  = refusePay(http.StatusConflict, "account_changed", "The account changed since this was prepared: prepare it again.")
	providerChanged = refusePay(http.StatusConflict, "provider_changed", "Your payments account changed since this was prepared: prepare it again.")
)

// stillAsPrepared checks a payment still goes where it was prepared to: on
// the same payments account (its bank code is that account's), and to the
// payee's account as it is now (the saved account's number and bank, the
// account a staff member is linked to, a rider's bank) being the one
// sealed in the payment. It answers the payments account to send from.
func (s *Server) stillAsPrepared(ctx context.Context, tenant uuid.UUID, x payment) (*invoicing.Rail, error) {
	rl, err := s.payRail(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if rl == nil {
		return nil, refusePay(http.StatusConflict, "no_provider", "Connect a payments account first: people are paid from it.")
	}
	if x.provider != rl.Channel() {
		return rl, providerChanged
	}
	var in payIn
	switch {
	case x.StaffID != nil:
		in.StaffID = x.StaffID
	case x.RiderID != nil:
		in.RiderID = x.RiderID
	case x.SavedID != nil:
		in.SavedID, in.Mine = x.SavedID, x.Personal
	default:
		// An account number typed in: the payment's own is the account.
		return rl, nil
	}
	person := uuid.Nil
	if x.Personal {
		// An owner's own account, prepared by them.
		if person, err = uuid.Parse(x.PreparedBy); err != nil {
			return rl, accountChanged
		}
	}
	now, err := s.payeeOf(ctx, tenant, person, true, in)
	var pr payRefused
	if errors.As(err, &pr) {
		// Gone: deleted, unlinked, no bank kept, no longer staff.
		return rl, accountChanged
	}
	if err != nil {
		return rl, err
	}
	code, err := s.bankCodeFor(ctx, tenant, *rl, now.bank, x.RiderID == nil)
	if errors.As(err, &pr) {
		return rl, accountChanged
	}
	if err != nil {
		return rl, err
	}
	var number string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		v, err := s.Store.PII.OpenTx(ctx, tx, tenant, x.sealed)
		number = fmt.Sprint(v)
		return err
	}); err != nil {
		return rl, err
	}
	if number != now.bank.AccountNumber || code != x.bankCode {
		return rl, accountChanged
	}
	return rl, nil
}

// lockPayBatchTx holds payment id's batch, if it has one, before the
// payment is changed: batches are held before their rows, as cancelBatch
// and the batch's result (chatBatchClosedTx) hold them.
func lockPayBatchTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*uuid.UUID, error) {
	var batch *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT batch_id FROM payments WHERE id = $1`, id).Scan(&batch); err != nil || batch == nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		return nil, err
	}
	_, err := tx.Exec(ctx, `SELECT 1 FROM payment_batches WHERE id = $1 FOR UPDATE`, *batch)
	return batch, err
}

// isChanged says whether err refuses a payment for its account, or the
// payments account, having changed since it was prepared.
func isChanged(err error) bool {
	var pr payRefused
	return errors.As(err, &pr) && (pr.code == "account_changed" || pr.code == "provider_changed")
}

// dropChanged cancels a payment not sent (prepared, or claimed with no run
// yet) when err says its account or payments account changed since it was
// prepared, with why; it answers err.
func (s *Server) dropChanged(r *http.Request, id uuid.UUID, err error) error {
	if !isChanged(err) {
		return err
	}
	var pr payRefused
	errors.As(err, &pr)
	r = r.WithContext(context.WithoutCancel(r.Context()))
	ctx := r.Context()
	if e := s.tx(r, func(tx pgx.Tx) error {
		batch, err := lockPayBatchTx(ctx, tx, id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE payments SET status = 'cancelled', reason = $2, needs_ok = '', finished_at = now()
			WHERE id = $1 AND status IN ('prepared', 'sending', 'waiting_ok') AND run_id IS NULL`, id, pr.msg)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if err := auditTx(r, tx, "payment.cancel", id.String(), map[string]any{"why": pr.code}); err != nil {
			return err
		}
		if batch != nil {
			// Maybe the last of a chat batch's rows: its result told.
			return chatBatchClosedTx(ctx, tx, principalFrom(ctx).TenantID, *batch)
		}
		return nil
	}); e != nil {
		s.Logger.Warn("payments: cancelling a changed payment", "payment", id, "err", e)
	}
	return err
}

// confirmCheckTx checks a payment can be confirmed by p, and why the
// owner's OK is asked (over p's limit, a changed account), if it is. With
// claim it is claimed for p as it will be sent (sending, or waiting_ok),
// under a lock on p's payments, so a payment is confirmed once and two at
// once cannot both fit a day's limit meant for one.
func confirmCheckTx(ctx context.Context, tx pgx.Tx, p *Principal, owner bool, id uuid.UUID, again, claim bool) (payment, string, error) {
	if claim {
		// One business's confirms one at a time: a payer's day total, and
		// the same payment again by two people at once, both hold.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "payments:"+p.TenantID.String()); err != nil {
			return payment{}, "", err
		}
	}
	x, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return x, "", err
	}
	if x.Personal && !owner {
		return x, "", refusePay(http.StatusForbidden, "not_yours", "An owner's own payment is theirs to confirm.")
	}
	if !owner && x.SavedID == nil && x.RiderID == nil {
		// Staff pay the business's saved accounts (and its riders) only.
		return x, "", refusePay(http.StatusForbidden, "saved_only", "Staff pay the business's saved accounts: an owner confirms this one.")
	}
	if x.Status != "prepared" {
		return x, "", refusePay(http.StatusConflict, "not_prepared", "This payment was "+paymentStatusWords(x.Status)+" already.")
	}
	if x.Again, err = paidAgainTx(ctx, tx, p.TenantID, x); err != nil {
		return x, "", err
	}
	if x.Again && !again {
		return x, "", refusePay(http.StatusConflict, "pay_again", fmt.Sprintf("You paid %s %s in the last day. Pay again?", x.Payee, invoicing.Naira(x.AmountKobo)))
	}
	var needs []string
	// An owner's own passkey is the owner's OK: a changed account they pay
	// was said (changed_account) before they confirmed, and is in the
	// audit with the confirmation.
	if !owner {
		one, day, err := payLimitOf(ctx, tx, p.TenantID, p.Roles)
		if err != nil {
			return x, "", err
		}
		var today int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM payments WHERE confirmed_by = $1
			AND status IN ('sending', 'waiting_ok', 'paid', 'unknown') AND confirmed_at > date_trunc('day', now() AT TIME ZONE 'Africa/Lagos') AT TIME ZONE 'Africa/Lagos'`,
			p.Actor()).Scan(&today); err != nil {
			return x, "", err
		}
		if x.AmountKobo > one || today+x.AmountKobo > day {
			needs = append(needs, "limit")
		}
		if x.ChangedAccount {
			needs = append(needs, "changed")
		}
		// An account never paid before (a saved account deleted and saved
		// again with another number is one): the owner's OK.
		if x.FirstPayment {
			needs = append(needs, "first")
		}
	}
	need := strings.Join(needs, ",")
	if claim {
		x.Status = "sending"
		if need != "" {
			x.Status = "waiting_ok"
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status = $2, needs_ok = $3, confirmed_by = $4, confirmed_at = now() WHERE id = $1`,
			x.ID, x.Status, need, p.Actor()); err != nil {
			return x, "", err
		}
	}
	return x, need, nil
}

// sendPayment starts the payment's run: the business's pay-person
// workflow (made, or remade for another payments account, first), and,
// within the payer's limit, the payer's own confirmation as its approval.
// The payment is checked first to go where it was prepared to
// (stillAsPrepared); if not, it is cancelled. A run started whose
// approval the payer's vote did not reach answers its id with the error.
func (s *Server) sendPayment(r *http.Request, x payment, needs string, p *Principal, method string) (run uuid.UUID, err error) {
	ctx := r.Context()
	// Claimed for this run: given back, to confirm again, if no run starts;
	// cancelled instead if it changed, or if its batch was cancelled
	// meanwhile (never prepared again in a cancelled batch, where nobody
	// would see it). Only a claim is given back: a payment ended or
	// cancelled meanwhile stays so.
	defer func() {
		if run != uuid.Nil || err == nil {
			return
		}
		if isChanged(err) {
			err = s.dropChanged(r, x.ID, err)
			return
		}
		bg := context.WithoutCancel(ctx)
		if e := db.InTenantTx(bg, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			batch, err := lockPayBatchTx(bg, tx, x.ID)
			if err != nil {
				return err
			}
			var status string
			err = tx.QueryRow(bg, `UPDATE payments SET
				status = CASE WHEN b.status = 'cancelled' THEN 'cancelled' ELSE 'prepared' END,
				finished_at = CASE WHEN b.status = 'cancelled' THEN now() END,
				needs_ok = '', confirmed_by = NULL, confirmed_at = NULL, confirmed_with = NULL
				FROM (SELECT (SELECT status FROM payment_batches WHERE id = (SELECT batch_id FROM payments WHERE id = $1)) AS status) b
				WHERE payments.id = $1 AND payments.run_id IS NULL AND payments.status IN ('sending', 'waiting_ok')
				RETURNING payments.status`, x.ID).Scan(&status)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil || status != "cancelled" || batch == nil {
				return err
			}
			// The batch was let go meanwhile: maybe this was its last row.
			return chatBatchClosedTx(bg, tx, p.TenantID, *batch)
		}); e != nil {
			s.Logger.Warn("payments: giving a payment back", "payment", x.ID, "err", e)
		}
	}()
	rl, err := s.stillAsPrepared(ctx, p.TenantID, x)
	if err != nil {
		return uuid.Nil, err
	}
	var wf uuid.UUID
	var version int
	var number string
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if wf, err = s.payWorkflowTx(r, tx, *rl); err != nil {
			return err
		}
		version, err = deployedVersion(ctx, tx, wf, "prod")
		if errors.Is(err, pgx.ErrNoRows) {
			return refusePay(http.StatusConflict, "waiting_publish", "Paying people waits for a second person to approve publishing it.")
		}
		if err != nil {
			return err
		}
		v, err := s.Store.PII.OpenTx(ctx, tx, p.TenantID, x.sealed)
		number = fmt.Sprint(v)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	narration := "Taskiem: " + paymentPurposeWords(x.Purpose)
	if x.Note != "" {
		narration += ", " + x.Note
	}
	if r := []rune(narration); len(r) > 100 {
		narration = string(r[:100])
	}
	ref, _, err := s.Store.StartRun(ctx, runtime.StartRequest{TenantID: p.TenantID, WorkflowID: wf, Version: version, Environment: "prod",
		Trigger: map[string]any{"type": "manual", "body": map[string]any{"payment_id": x.ID.String(), "payee": x.Payee, "bank_code": x.bankCode,
			"account_number": number, "amount_kobo": x.AmountKobo, "narration": narration, "purpose": x.Purpose, "needs_ok": needs != "",
			"needs": needs, "account": x.Account, "account_name": deref(x.AccountName)}},
		StartedBy: p.Actor(), TriggerID: "payments/pay", DedupKey: "payment:" + x.ID.String()})
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE payments SET run_id = $2 WHERE id = $1`, x.ID, ref.ID)
		return err
	}); err != nil {
		return ref.ID, err
	}
	if needs != "" {
		return ref.ID, nil
	}
	// Within the limit: the payer's passkey is the approval.
	return ref.ID, s.payerVote(r, ref.ID, p, method, "payment.confirm")
}

// payWorkflowTx is the business's pay-person workflow on rl, made (or a
// new version for another account) with its approval under the
// taskiem_payments policy.
func (s *Server) payWorkflowTx(r *http.Request, tx pgx.Tx, rl invoicing.Rail) (uuid.UUID, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	var have *uuid.UUID
	var conn, funding string
	err := tx.QueryRow(ctx, `SELECT workflow_id, connection, funding_account FROM pay_settings WHERE tenant_id = $1`, p.TenantID).Scan(&have, &conn, &funding)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	want, wallet := rl.Connection, ""
	if rl.Channel() == "iswallet/"+secrets.PlatformName {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(wallet_id, '') FROM receive_money WHERE tenant_id = $1`, p.TenantID).Scan(&wallet); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
		want = secrets.PlatformName
	}
	if have != nil && conn == want && funding == wallet {
		return *have, nil
	}
	tpl, ok := templates.Default().Get(payPersonTemplate)
	if !ok {
		return uuid.Nil, refusePay(http.StatusConflict, "unavailable", "Paying people is not available here.")
	}
	raw, err := tpl.Instantiate(map[string]any{"rail_connection": want, "funding_account": wallet}, "Pay one person")
	if err != nil {
		return uuid.Nil, err
	}
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return uuid.Nil, err
	}
	steps, _ := def["steps"].([]any)
	for _, st := range steps {
		if m, _ := st.(map[string]any); m["type"] == "approval" {
			if cfg, ok := m["config"].(map[string]any); ok {
				cfg["policy"] = payPolicy
			}
		}
	}
	patched, err := json.Marshal(def)
	if err != nil {
		return uuid.Nil, err
	}
	doc, err := canonical(patched)
	if err != nil {
		return uuid.Nil, err
	}
	g, err := governanceTx(ctx, tx)
	if err != nil {
		return uuid.Nil, err
	}
	fourEyes, err := publishNeedsApproval(ctx, tx, g, nil)
	if err != nil {
		return uuid.Nil, err
	}
	if err := writePayPolicyTx(r, tx, p.TenantID, fourEyes, p.Actor()); err != nil {
		return uuid.Nil, err
	}
	wf, _, err := s.managedWorkflowTx(r, tx, managedWorkflow{Name: "Pay one person", Template: payPersonTemplate, What: "how people are paid"}, doc, have, fourEyes)
	if err != nil {
		return uuid.Nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO pay_settings (tenant_id, workflow_id, connection, funding_account, updated_by) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id) DO UPDATE SET workflow_id = $2, connection = $3, funding_account = $4, updated_by = $5, updated_at = now()`,
		p.TenantID, wf, want, wallet, p.Actor())
	return wf, err
}

// writePayPolicyTx keeps the taskiem_payments approval policy: a payment
// that needs the owner's OK is approved by an owner; any other by its
// payer's own confirmation (money_pay), who started the run.
func writePayPolicyTx(r *http.Request, tx pgx.Tx, tenant uuid.UUID, fourEyes bool, by string) error {
	ctx := r.Context()
	no, yes := false, true
	pol := policy.Policy{Constraints: policy.Constraints{ForbidSelfApproval: &no, DistinctApprovers: &yes, AllowSoleApprover: &yes}, Timeout: "48h", OnTimeout: "reject",
		Description: "Paying one person: the owner's OK over a payer's limit or to a changed account; else the payer's passkey"}
	pol.Rules = []policy.Rule{{When: "=subject.needs_ok == true", Levels: []policy.Level{{Role: "owner"}}, StepUp: "passkey"}, {Levels: []policy.Level{{Role: payerRole}}}}
	raw, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	if _, err := policy.Parse(raw); err != nil {
		return err
	}
	doc, err := canonical(raw)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy:' || $1))`, payPolicy); err != nil {
		return err
	}
	var same bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_policies WHERE name = $1 AND state IN ('active', 'pending') AND document = $2::jsonb
		AND version = (SELECT max(version) FROM approval_policies WHERE name = $1))`, payPolicy, doc).Scan(&same); err != nil || same {
		return err
	}
	var v int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM approval_policies WHERE name = $1`, payPolicy).Scan(&v); err != nil {
		return err
	}
	state := "active"
	if fourEyes {
		state = "pending"
	} else if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, payPolicy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO approval_policies (tenant_id, name, version, document, state, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, payPolicy, v, doc, state, by); err != nil {
		return err
	}
	return auditTx(r, tx, "policy.write", fmt.Sprintf("%s/%d", payPolicy, v), map[string]any{"state": state, "via": "payments"})
}

// cancelPayment is POST /v1/payments/{id}/cancel: a payment not sent yet
// is let go. Who may see it (getPayment: an owner's own only to owners)
// may cancel it if they prepared it; an owner cancels any.
func (s *Server) cancelPayment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	owner := isOwnerOf(p)
	var x payment
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE payments SET status = 'cancelled', finished_at = now() WHERE id = $1 AND status = 'prepared'
			AND (NOT personal OR $2) AND ($2 OR prepared_by = $3)`, id, owner, p.Actor())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			was, err := scanPayment(tx.QueryRow(r.Context(), `SELECT `+paymentCols+` FROM payments WHERE id = $1 AND (NOT personal OR $2)`, id, owner))
			if err != nil {
				return err
			}
			if !owner && was.PreparedBy != p.Actor() {
				return refusePay(http.StatusForbidden, "not_yours", "Only who prepared this payment, or an owner, cancels it.")
			}
			return refusePay(http.StatusConflict, "not_prepared", "Only a payment not sent yet is cancelled; one waiting for the owner's OK is refused in Approvals.")
		}
		if x, err = scanPayment(tx.QueryRow(r.Context(), `SELECT `+paymentCols+` FROM payments WHERE id = $1`, id)); err != nil {
			return err
		}
		// A batch's last open row let go: its result is told now.
		if x.BatchID != nil {
			if err := chatBatchClosedTx(r.Context(), tx, p.TenantID, *x.BatchID); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "payment.cancel", id.String(), nil)
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment": x})
}

// listPayments is GET /v1/payments (?status=): the newest first.
func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var out []payment
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+paymentCols+` FROM payments WHERE ($1 = '' OR status = $1) AND (NOT personal OR $2) ORDER BY created_at DESC LIMIT 100`,
			status, slices.Contains(principalFrom(r.Context()).Roles, "owner"))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (payment, error) { return scanPayment(r) })
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out == nil {
		out = []payment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": out})
}

// getPayment is GET /v1/payments/{id}.
func (s *Server) getPayment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var x payment
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = scanPayment(tx.QueryRow(r.Context(), `SELECT `+paymentCols+` FROM payments WHERE id = $1 AND (NOT personal OR $2)`,
			id, slices.Contains(principalFrom(r.Context()).Roles, "owner"))); err != nil {
			return err
		}
		if x.Status == "prepared" {
			x.Again, err = paidAgainTx(r.Context(), tx, principalFrom(r.Context()).TenantID, x)
		}
		if err == nil {
			x.AwaitsYourOK, err = awaitsPayerTx(r.Context(), tx, x, principalFrom(r.Context()))
		}
		return err
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment": x})
}

// payLimit is one role's limit.
type payLimit struct {
	Role    string `json:"role"`
	OneKobo int64  `json:"one_kobo"`
	DayKobo int64  `json:"day_kobo"`
}

// getPayLimits is GET /v1/pay-limits: each role's limit (a role with none
// set pays nothing without the owner's OK).
func (s *Server) getPayLimits(w http.ResponseWriter, r *http.Request) {
	var out []payLimit
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT role, one_kobo, day_kobo FROM pay_limits ORDER BY role`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (payLimit, error) {
			var l payLimit
			return l, r.Scan(&l.Role, &l.OneKobo, &l.DayKobo)
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out == nil {
		out = []payLimit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": out})
}

// putPayLimit is PUT /v1/pay-limits/{role} {one_kobo, day_kobo}: the
// owner's alone.
func (s *Server) putPayLimit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "Only an owner sets what others may pay.")
		return
	}
	role := chi.URLParam(r, "role")
	var in payLimit
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if role == "owner" || role == "" || in.OneKobo < 0 || in.DayKobo < 0 || in.OneKobo > in.DayKobo && in.DayKobo > 0 {
		writeErr(w, http.StatusBadRequest, "A limit is a role's, in kobo, and one payment is no more than a day's total.")
		return
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO pay_limits (tenant_id, role, one_kobo, day_kobo, updated_by) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, role) DO UPDATE SET one_kobo = $3, day_kobo = $4, updated_by = $5, updated_at = now()`,
			p.TenantID, role, in.OneKobo, in.DayKobo, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "payment.limit", role, map[string]any{"one_kobo": in.OneKobo, "day_kobo": in.DayKobo})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in.Role = role
	writeJSON(w, http.StatusOK, in)
}

// paymentPurposeWords is what a payment's purpose says.
func paymentPurposeWords(p string) string {
	return map[string]string{"bill": "a bill", "wages": "wages", "delivery": "a delivery", "refund": "a refund", "stock": "stock bought"}[p] + map[bool]string{true: "other"}[p == "other"]
}

// paymentStatusWords is a payment's status in words.
func paymentStatusWords(st string) string {
	return map[string]string{"sending": "sent", "waiting_ok": "sent for the owner's OK", "paid": "paid", "failed": "not paid", "rejected": "refused",
		"unknown": "sent", "cancelled": "cancelled"}[st]
}

// --- the payments@1 connector's operations ---

// PaymentsOps are the payments@1 connector's operations for the runs of
// store, with the payments accounts invoices pays through: set wherever
// runs run (the API, the workers).
func PaymentsOps(store *runtime.Store, inv *invoicing.Invoices, reg *connector.Registry, vault *secrets.Vault) payconn.Ops {
	return &payOps{s: &Server{Store: store, Invoices: inv, Registry: reg, Vault: vault}}
}

// PaymentsOps are the payments@1 connector's operations through this
// server (tests).
func (s *Server) PaymentsOps() payconn.Ops { return &payOps{s: s} }

type payOps struct{ s *Server }

// Prepare makes a payment a business's own workflow asks for: prepared,
// for a person who may pay to confirm; it sends nothing.
func (o *payOps) Prepare(ctx context.Context, tenant, run uuid.UUID, in payconn.PrepareIn) (map[string]any, error) {
	x, err := o.s.preparePayment(ctx, tenant, uuid.Nil, false, payIn{Payee: strings.TrimPrefix(in.Payee, "@"), AmountKobo: in.AmountKobo, Purpose: in.Purpose,
		PurposeNote: in.PurposeNote, Note: in.Note, From: "workflow"}, "workflow:"+run.String())
	var pr payRefused
	if errors.As(err, &pr) {
		return nil, fmt.Errorf("%w: %s", payconn.ErrRefused, pr.msg)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"payment_id": x.ID.String(), "status": x.Status, "payee": x.Payee, "amount_kobo": x.AmountKobo, "first_payment": x.FirstPayment,
		"needs_ok": map[bool]string{true: "changed"}[x.ChangedAccount]}, nil
}

// Check holds the pay-person run to its payment, before it pays: the
// payment was sent with this very run (only sendPayment starts one and
// records it), the run's input is the payment's own, and it was approved
// by whom it must be: within the payer's limit the payer, by the
// passkey they confirmed it with; else an owner in their own right (not
// acting for one) who is not the payer. Anything else fails the run
// before money moves, whoever started it and however the workflow or its
// policy was changed.
func (o *payOps) Check(ctx context.Context, tenant, run, id uuid.UUID, in payconn.CheckIn) (map[string]any, error) {
	refused := func(why string) error { return fmt.Errorf("%w: %s", payconn.ErrRefused, why) }
	var out map[string]any
	err := db.InTenantTx(ctx, o.s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		x, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1 AND run_id = $2 FOR UPDATE`, id, run))
		if errors.Is(err, pgx.ErrNoRows) {
			return refused("this run was not started for that payment")
		}
		if err != nil {
			return err
		}
		if x.Status != "sending" && x.Status != "waiting_ok" {
			return refused("the payment was " + paymentStatusWords(x.Status) + " already")
		}
		number, err := o.s.Store.PII.OpenTx(ctx, tx, tenant, x.sealed)
		if err != nil {
			return err
		}
		if in.AmountKobo != x.AmountKobo || in.BankCode != x.bankCode || in.AccountNumber != fmt.Sprint(number) || in.NeedsOK != (x.NeedsOK != "") {
			return refused("the run's input is not the payment's")
		}
		if x.ConfirmedBy == nil {
			return refused("the payment was not confirmed")
		}
		type decision struct {
			User     string
			Behalf   *string
			Decision string
		}
		rows, err := tx.Query(ctx, `SELECT user_id::text, on_behalf_of::text, decision FROM approval_decisions WHERE run_id = $1 AND step_id = 'approve'`, run)
		if err != nil {
			return err
		}
		ds, err := pgx.CollectRows(rows, pgx.RowToStructByPos[decision])
		if err != nil {
			return err
		}
		ok := false
		for _, d := range ds {
			if d.Decision != "approved" {
				return refused("the payment was not approved")
			}
			switch {
			case x.NeedsOK == "":
				ok = ok || d.User == *x.ConfirmedBy && d.Behalf == nil
			case d.Behalf == nil && d.User != *x.ConfirmedBy:
				var owner bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id::text = $2 AND role = 'owner')`, tenant, d.User).Scan(&owner); err != nil {
					return err
				}
				ok = ok || owner
			}
		}
		if !ok {
			return refused("the payment was not approved by whom it must be")
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'sending' WHERE id = $1`, id); err != nil {
			return err
		}
		out = map[string]any{"payment_id": id.String(), "status": "sending"}
		return nil
	})
	return out, err
}

// Finish records how the pay-person workflow's payment ended.
func (o *payOps) Finish(ctx context.Context, tenant, run, id uuid.UUID, status, accountName, payoutID, reason string, feeKobo *int64) (map[string]any, error) {
	if !slices.Contains([]string{"paid", "failed", "unknown", "rejected"}, status) {
		return nil, fmt.Errorf("%w: a payment ends paid, failed, unknown or rejected", payconn.ErrRefused)
	}
	var out map[string]any
	err := db.InTenantTx(ctx, o.s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var now string
		err := tx.QueryRow(ctx, `UPDATE payments SET status = $2, account_name = COALESCE(NULLIF($3, ''), account_name), payout_ref = NULLIF($4, ''),
			reason = NULLIF($5, ''), fee_kobo = $6, finished_at = now()
			WHERE id = $1 AND run_id = $7 AND (status IN ('sending', 'waiting_ok') OR status = 'unknown' AND $2 IN ('paid', 'failed')) RETURNING status`, id, status, accountName, payoutID, reason, feeKobo, run).Scan(&now)
		if errors.Is(err, pgx.ErrNoRows) {
			// Ended already: the same outcome again changes nothing (one
			// not known yet is settled by the bank's answer, above).
			if err := tx.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1 AND run_id = $2`, id, run).Scan(&now); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: no such payment", payconn.ErrRefused)
				}
				return err
			}
		} else if err != nil {
			return err
		}
		out = map[string]any{"payment_id": id.String(), "status": now}
		// Told in the chat it was prepared from, with its end (once).
		return chatPayResultTx(ctx, tx, tenant, id)
	})
	return out, err
}

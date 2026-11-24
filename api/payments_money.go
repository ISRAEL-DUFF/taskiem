package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The money around a payment (docs/spec/chat-pay-people.md, Settled 2, 4
// and 7; build-plan-gaps H7): the balance it is paid from and how to add
// money to it, the fee before paying, and the route Taskiem chooses.
//
//   - The balance is the payments account's own (its balance capability);
//     a Taskiem wallet's Add money is the wallet's own account number.
//   - The fee is quoted by the payments account where it can (quote_fee);
//     where it cannot, the payment goes and the sheet says "The fee shows
//     after paying", the fee recorded when the provider reports it.
//   - The balance check counts the fee; limits count the amount only.
//   - Taskiem wallet to Taskiem wallet whenever the account paid is
//     another business's Taskiem wallet: at once and with no fee, falling
//     back to the bank if that route fails (the pay-person workflow).

// isTaskiemWallet says whether rl is the business's Taskiem wallet.
func isTaskiemWallet(rl invoicing.Rail) bool { return rl.Channel() == "iswallet/"+secrets.PlatformName }

// payFunding is the wallet payments come out of on rl: the business's own
// Taskiem wallet; "" on any other payments account.
func (s *Server) payFunding(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail) (string, error) {
	if !isTaskiemWallet(rl) {
		return "", nil
	}
	var wallet string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT COALESCE(wallet_id, '') FROM receive_money WHERE tenant_id = $1`, tenant).Scan(&wallet)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return wallet, err
}

// payBalance is what can be spent now on rl; ok false when it cannot say
// (no balance capability, or no answer just now: the payment is not
// stopped for a balance not known).
func (s *Server) payBalance(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail) (int64, bool) {
	if !rl.Maps("balance") {
		return 0, false
	}
	in := map[string]any{"currency": "NGN"}
	if rl.Needs("balance", "account") {
		w, err := s.payFunding(ctx, tenant, rl)
		if err != nil || w == "" {
			return 0, false
		}
		in["account"] = w
	}
	out, err := s.accountRails().Call(ctx, tenant, rl, "balance", in, effects.KeyInput{})
	if err != nil {
		s.Logger.Warn("payments: reading the balance", "tenant", tenant, "err", err)
		return 0, false
	}
	n, ok := koboOK(out["available_kobo"])
	return n, ok
}

// koboOK reads a whole number of kobo from a decoded answer.
func koboOK(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), x == float64(int64(x))
	}
	return 0, false
}

// payFeeQuote is the fee rl quotes for paying amount to the bank code;
// nil when it quotes none (or not just now).
func (s *Server) payFeeQuote(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail, amount int64, code string) *int64 {
	if !rl.Maps("quote_fee") {
		return nil
	}
	in := map[string]any{"amount_kobo": amount, "currency": "NGN", "bank_code": code}
	if rl.Takes("quote_fee", "source") {
		if w, err := s.payFunding(ctx, tenant, rl); err == nil && w != "" {
			in["source"] = w
		}
	}
	out, err := s.accountRails().Call(ctx, tenant, rl, "quote_fee", in, effects.KeyInput{})
	if err != nil {
		s.Logger.Warn("payments: asking the fee", "tenant", tenant, "err", err)
		return nil
	}
	if n, ok := koboOK(out["fee_kobo"]); ok && n >= 0 {
		return &n
	}
	return nil
}

// walletRoute is the Taskiem wallet an account number is, when it is
// another business's and rl can pay wallet to wallet: its wallet's id,
// never shown; "" for the bank.
func (s *Server) walletRoute(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail, number string) string {
	if !isTaskiemWallet(rl) || !rl.Maps("pay_wallet") {
		return ""
	}
	var wallet *string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_wallet_for_account($1, $2)`, tenant, number).Scan(&wallet)
	}); err != nil {
		s.Logger.Warn("payments: looking for a Taskiem wallet", "tenant", tenant, "err", err)
		return ""
	}
	if wallet == nil {
		return ""
	}
	return *wallet
}

// shortWords say a balance that does not cover a payment and its fee.
func shortWords(balance, amount int64, fee *int64) string {
	need := amount
	what := invoicing.Naira(amount)
	if fee != nil && *fee > 0 {
		need += *fee
		what += " + " + invoicing.Naira(*fee) + " fee"
	}
	return fmt.Sprintf("Your balance is %s: %s short of %s. Add money first.", invoicing.Naira(balance), invoicing.Naira(need-balance), what)
}

// payBalanceView is GET /v1/payments/balance: what the business pays
// from, its balance when it can say, and, for a Taskiem wallet, the
// wallet's own account number to add money to (Settled 2).
type payBalanceView struct {
	// From is the payments account as the owner knows it ("your Taskiem
	// wallet", "Paystack"); "" with none connected (the sheet offers the
	// step to connect).
	From      string `json:"from"`
	Available *int64 `json:"available_kobo"`
	// Wallet: the business pays from its Taskiem wallet.
	Wallet bool `json:"wallet"`
	// QuotesFee: the fee is known before paying; else it shows after.
	QuotesFee bool        `json:"quotes_fee"`
	AddMoney  *addMoneyTo `json:"add_money,omitempty"`
}

// addMoneyTo is the account to transfer to, to add money to the balance.
type addMoneyTo struct {
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

func (s *Server) getPayBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	rl, err := s.payRail(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := payBalanceView{}
	if rl == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.From, out.Wallet, out.QuotesFee = railTitle(*rl), isTaskiemWallet(*rl), rl.Maps("quote_fee")
	if n, ok := s.payBalance(ctx, p.TenantID, *rl); ok {
		out.Available = &n
	}
	if out.Wallet {
		if err := s.tx(r, func(tx pgx.Tx) error {
			var a addMoneyTo
			var number, bank, name *string
			err := tx.QueryRow(ctx, `SELECT account_number, bank_name, account_name FROM receive_money WHERE tenant_id = $1`, p.TenantID).Scan(&number, &bank, &name)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && (number == nil || *number == "") {
				return nil
			}
			if err != nil {
				return err
			}
			a.AccountNumber, a.BankName, a.AccountName = *number, deref(bank), deref(name)
			out.AddMoney = &a
			return nil
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// balanceCovers refuses a payment whose amount and quoted fee the balance
// no longer covers (409 short), as it is confirmed; a balance not known
// lets it go (the provider refuses it then, and it ends failed).
func (s *Server) balanceCovers(ctx context.Context, tenant uuid.UUID, x payment) error {
	rl, err := s.payRail(ctx, tenant)
	if err != nil || rl == nil {
		return err
	}
	n, ok := s.payBalance(ctx, tenant, *rl)
	if !ok {
		return nil
	}
	need := x.AmountKobo
	if x.FeeQuoteKobo != nil {
		need += *x.FeeQuoteKobo
	}
	if need > n {
		return refusePay(http.StatusConflict, "short", shortWords(n, x.AmountKobo, x.FeeQuoteKobo))
	}
	return nil
}

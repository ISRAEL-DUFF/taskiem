package moneylog

// Accounts a business has paid (docs/money-log.md#accounts-paid), for the
// approvals warning "First payment to this account": a payout to a bank
// account that completes in prod remembers the account, and an approval
// whose subject names an account asks whether it was paid before.
//
// An account is remembered by the pseudonym sealing derives from its
// digits, keyed by the business's own key, so the table says nothing
// without it; no account number is stored. Where both sides know the
// bank's code they must agree (the same ten digits at two banks are two
// accounts); where either does not, the digits decide.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Subjects derives the pseudonym a value would be sealed under, without
// sealing it (secrets.Vault).
type Subjects interface {
	SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error)
}

// accountCategory is the pseudonym's category: apart from every sealed
// category, so it matches nothing else a business holds.
const accountCategory = "payout_account"

// AccountDigits is an account as remembered: its digits, or "" when it has
// fewer than six (not an account number).
func AccountDigits(account string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, account)
	if len(d) < 6 {
		return ""
	}
	return d
}

func accountKey(ctx context.Context, tx pgx.Tx, s Subjects, tenant uuid.UUID, account string) (string, error) {
	d := AccountDigits(account)
	if d == "" || s == nil {
		return "", nil
	}
	return s.SubjectForTx(ctx, tx, tenant, accountCategory, d)
}

// RememberAccount records a payment to account (with its bank's code, if
// known) at the time given.
func RememberAccount(ctx context.Context, tx pgx.Tx, s Subjects, tenant uuid.UUID, account, bankCode string, at time.Time) error {
	key, err := accountKey(ctx, tx, s, tenant, account)
	if err != nil || key == "" {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payout_accounts (tenant_id, account_key, bank_code, first_paid_at, last_paid_at) VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (tenant_id, account_key, bank_code) DO UPDATE SET times = payout_accounts.times + 1, last_paid_at = GREATEST(payout_accounts.last_paid_at, EXCLUDED.last_paid_at)`,
		tenant, key, strings.TrimSpace(bankCode), at)
	return err
}

// PaidBefore reports whether the business has paid account before; known
// is false when the account cannot be told (no digits, no pseudonyms).
func PaidBefore(ctx context.Context, tx pgx.Tx, s Subjects, tenant uuid.UUID, account, bankCode string) (paid, known bool, err error) {
	key, err := accountKey(ctx, tx, s, tenant, account)
	if err != nil || key == "" {
		return false, false, err
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payout_accounts WHERE tenant_id = $1 AND account_key = $2 AND ($3 = '' OR bank_code = '' OR bank_code = $3))`,
		tenant, key, strings.TrimSpace(bankCode)).Scan(&paid)
	return paid, true, err
}

// SubjectAccount is the bank account an approval's subject names, as the
// payment steps name it (account_number, else nuban or account), with the
// bank's code when it has one; "" when it names none.
func SubjectAccount(subject any) (account, bankCode string) {
	m, ok := subject.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range []string{"account_number", "nuban", "account"} {
		var v string
		switch x := m[k].(type) {
		case string:
			v = x
		case float64:
			v = strconv.FormatFloat(x, 'f', 0, 64)
		case int64:
			v = strconv.FormatInt(x, 10)
		}
		if AccountDigits(v) != "" {
			account = v
			break
		}
	}
	bankCode, _ = m["bank_code"].(string)
	return account, bankCode
}

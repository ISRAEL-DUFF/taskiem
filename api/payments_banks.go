package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Choosing the bank for an account number (paying people, phase 2): the
// banks the payments account pays to, with those the number can belong to
// (its check digit fits their code) and those the business pays most put
// first and marked likely; and the name checked at up to three likely
// banks at once ("Check the likely banks for me"), never paid without a
// pick.

// payBanks is the payments account's banks, for number (when given) the
// likely ones first: those its check digit fits, the business's
// most-paid among them first, then the rest.
func (s *Server) payBanks(ctx context.Context, tenant uuid.UUID, rl invoicing.Rail, number string) ([]bankItem, error) {
	banks, err := s.savedBanks(ctx, tenant, rl)
	if err != nil {
		return nil, err
	}
	items := bankItems(banks)
	if len(items) == 0 {
		return builtInBanks(), nil
	}
	most := map[string]int{}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT bank_code, count(*) FROM payments WHERE tenant_id = $1 AND status IN ('paid', 'sending', 'unknown')
			GROUP BY bank_code ORDER BY count(*) DESC LIMIT 10`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for i := 0; rows.Next(); i++ {
			var code string
			var n int
			if err := rows.Scan(&code, &n); err != nil {
				return err
			}
			most[code] = 10 - i
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	if number, err := saved.AccountNumber(number); err == nil {
		for i := range items {
			fits, _ := saved.NUBANFits(items[i].Code, number)
			items[i].Likely = fits
		}
	} else {
		for i := range items {
			items[i].Likely = most[items[i].Code] > 0
		}
	}
	slices.SortStableFunc(items, func(a, b bankItem) int {
		if a.Likely != b.Likely {
			if a.Likely {
				return -1
			}
			return 1
		}
		return most[b.Code] - most[a.Code]
	})
	return items, nil
}

// listPayBanks is GET /v1/payments/banks[?account=]: the banks to pay an
// account number at, the likely ones first for the account given.
func (s *Server) listPayBanks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := principalFrom(ctx).TenantID
	rl, err := s.payRail(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"banks": builtInBanks(), "from": ""})
		return
	}
	items, err := s.payBanks(ctx, tenant, *rl, r.URL.Query().Get("account"))
	if err != nil || len(items) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"banks": builtInBanks(), "from": ""})
		return
	}
	from := rl.Channel()
	if items[0].Code == "" {
		from = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"banks": items, "from": from})
}

// payNameCheck is POST /v1/payments/name-check {account_number,
// bank_codes?}: the bank's name for the account at up to three banks at
// once (the likely ones, when none are named), each answered: its name,
// or that it has no such account, or that it could not say just now.
func (s *Server) payNameCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		AccountNumber string   `json:"account_number"`
		BankCodes     []string `json:"bank_codes"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	number, err := saved.AccountNumber(in.AccountNumber)
	if err != nil {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "account", saved.Plain(err)))
		return
	}
	tenant := principalFrom(ctx).TenantID
	rl, err := s.payRail(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rl == nil {
		s.payFail(w, r, refusePay(http.StatusConflict, "no_provider", "Connect a payments account first: names are checked through it."))
		return
	}
	items, err := s.payBanks(ctx, tenant, *rl, number)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var pick []bankItem
	if len(in.BankCodes) > 0 {
		for _, it := range items {
			if slices.Contains(in.BankCodes, it.Code) {
				pick = append(pick, it)
			}
		}
	} else {
		for _, it := range items {
			if it.Likely && it.Code != "" {
				pick = append(pick, it)
			}
		}
	}
	if len(pick) > 3 {
		pick = pick[:3]
	}
	if len(pick) == 0 {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "no_banks", "Choose the bank: none fits that number."))
		return
	}
	type answer struct {
		BankName    string `json:"bank_name"`
		BankCode    string `json:"bank_code"`
		AccountName string `json:"account_name,omitempty"`
		Problem     string `json:"problem,omitempty"`
	}
	out := make([]answer, len(pick))
	for i, b := range pick {
		out[i] = answer{BankName: b.Name, BankCode: b.Code}
		res, problem, err := s.checkWithRail(ctx, tenant, *rl, saved.BankAccount{BankName: b.Name, BankCode: b.Code, CodeFrom: rl.Channel(), AccountNumber: number})
		switch {
		case refusedWords(err) != "":
			out[i].Problem = "No account " + saved.Masked(number) + " at " + b.Name + "."
		case err != nil:
			s.fail(w, r, err)
			return
		case res != nil:
			out[i].AccountName = res.AccountName
		default:
			out[i].Problem = strings.TrimSpace(problem.Words)
			if out[i].Problem == "" {
				out[i].Problem = "The bank did not say just now."
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": saved.Masked(number), "banks": out})
}

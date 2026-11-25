package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/pii"
)

// The Transactions page (docs/spec/transactions-page.md): the money log
// one movement to a row, filtered, searched and summed up, and each
// movement on its own page. invoice.read, as the rest of the money log.

// movementFilter reads the Transactions page's filters: direction (in,
// out), channel, from and to (Lagos days, to inclusive), matched (yes,
// no) and q (words or an amount).
func movementFilter(r *http.Request) (moneyreport.Filter, error) {
	q := r.URL.Query()
	f := moneyreport.Filter{Direction: q.Get("direction"), Channel: strings.TrimSpace(q.Get("channel")), Matched: q.Get("matched"), Search: q.Get("q")}
	if v := q.Get("from"); v != "" {
		t, err := moneyreport.ParseDay(v)
		if err != nil {
			return f, fmt.Errorf("%w: from is a date like 2026-10-01", errBadRequest)
		}
		f.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := moneyreport.ParseDay(v)
		if err != nil {
			return f, fmt.Errorf("%w: to is a date like 2026-10-31", errBadRequest)
		}
		t = t.AddDate(0, 0, 1)
		f.To = &t
	}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		return f, fmt.Errorf("%w: to is on or after from", errBadRequest)
	}
	return f, nil
}

// badFilter words a filter the list refused as a 400.
func badFilter(err error) error {
	if errors.Is(err, moneyreport.ErrBadFilter) {
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), moneyreport.ErrBadFilter.Error()+": "))
	}
	return err
}

// listMovements serves GET /v1/money/entries?movements=true: the money
// log one movement to a row (its latest status, as the Money page and
// the log export count it), newest first by when it happened, filtered
// and searched, limit at a time (50 unless asked, 200 at most), the next
// page from ?cursor= (the next the last page gave). The first page
// carries the totals for the whole filter and the channels the log has.
func (s *Server) listMovements(w http.ResponseWriter, r *http.Request) {
	f, err := movementFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var after *moneyreport.Cursor
	if c := r.URL.Query().Get("cursor"); c != "" {
		x, err := moneyreport.ParseCursor(c)
		if err != nil {
			s.fail(w, r, badFilter(err))
			return
		}
		after = &x
	}
	tenant := principalFrom(r.Context()).TenantID
	out := map[string]any{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		list, next, err := moneyreport.Movements(r.Context(), tx, tenant, f, after, limit)
		if err != nil {
			return err
		}
		out["entries"] = list
		if next != nil {
			out["next"] = next.String()
		}
		if after != nil {
			return nil
		}
		if out["totals"], err = moneyreport.SumMovements(r.Context(), tx, tenant, f); err != nil {
			return err
		}
		out["channels"], err = moneyreport.MovementChannels(r.Context(), tx, tenant)
		return err
	}); err != nil {
		s.fail(w, r, badFilter(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getMoneyEntry serves GET /v1/money/entries/{id}: one movement of the
// log on its own (any of its entries finds it), with every status it had,
// what it counts towards, and who paid as the rail said, masked unless
// someone with pii.reveal asks with ?reveal=true, which is audited.
func (s *Server) getMoneyEntry(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	reveal, ok := revealing(w, r)
	if !ok {
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var d moneyreport.Detail
	var payer, account string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var found bool
		if d, found, err = moneyreport.MovementOf(r.Context(), tx, tenant, id); err != nil {
			return err
		}
		if !found {
			return pgx.ErrNoRows
		}
		if d.Payer == nil || s.Store.PII == nil {
			return nil
		}
		v, err := s.Store.PII.OpenTx(r.Context(), tx, tenant, d.Payer)
		if errors.Is(err, pii.ErrErased) {
			return nil
		}
		if err != nil {
			return err
		}
		if m, ok := v.(map[string]any); ok {
			payer, _ = m["name"].(string)
			account, _ = m["account"].(string)
		}
		if !reveal || payer == "" && account == "" {
			return nil
		}
		return auditTx(r, tx, "pii.reveal", "money_entry", map[string]any{"entry": d.MovementID})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		payer, account = maskName(payer), maskAccount(account)
	}
	out := map[string]any{"entry": d, "revealed": reveal}
	if payer != "" {
		out["payer"] = payer
	}
	if account != "" {
		out["payer_account"] = account
	}
	writeJSON(w, http.StatusOK, out)
}

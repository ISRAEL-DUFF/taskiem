package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The PIN as the fallback to a passkey for confirming one payment
// (docs/spec/chat-pay-people.md, Settled row 3 and Safety). It is the
// person's one PIN (whatsapp_pin.go), typed only in the app's own pad
// (POST /v1/payments/{id}/confirm {pin}) or in WhatsApp's secure form
// bound to that one payment (a whatsapp_flows row of the kind pay_pin),
// never in a message. Both go through payByPin:
//
//   - the PIN's own limits: ₦100,000 a payment and ₦200,000 a day across
//     the person's PIN-confirmed payments in every business (Lagos's day,
//     as pay limits count it); the role's limits apply too, and over
//     those the owner's OK is asked, which a PIN never leads to;
//   - passkey only: an account changed since it was last paid, anything
//     waiting for the owner's OK, and every batch (payment_batches.go
//     takes a passkey and nothing else);
//   - wrong PINs are counted with the approvals' (taskiem_wa_pin_result:
//     five in 15 minutes lock it for 15 minutes); there is no reset by
//     text, and a PIN set or changed in the last 24 hours cannot pay yet.
//
// Then everything confirmPayment does: still as prepared, claimed under
// the lock, the limits, sent, and the payer's own OK with step-up "pin".

const (
	pinPayOneKobo = 100_000_00 // ₦100,000
	pinPayDayKobo = 200_000_00 // ₦200,000
	payStepUpPIN  = "pin"
)

// pinPayState is the person's PIN as paying sees it.
type pinPayState struct {
	hash     string
	setAt    time.Time
	locked   *time.Time
	paysFrom time.Time
}

// pinForPay reads the person's PIN: no_pin, pin_locked or pin_new when it
// cannot pay now.
func (s *Server) pinForPay(ctx context.Context, user uuid.UUID) (pinPayState, error) {
	var st pinPayState
	err := s.Store.Pool.QueryRow(ctx, `SELECT s.pin_hash, s.set_at, s.locked_until, taskiem_pin_pays_from($1) FROM taskiem_wa_pin_state($1) s`, user).Scan(&st.hash, &st.setAt, &st.locked, &st.paysFrom)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return st, refusePay(http.StatusForbidden, "no_pin", "You have no PIN set: confirm with your passkey.")
	case err != nil:
		return st, err
	case st.locked != nil:
		return st, refusePay(http.StatusForbidden, "pin_locked", "Your PIN is locked after too many wrong tries, until "+st.locked.In(lagosTZ).Format("15:04")+
			". Use your passkey instead.")
	case time.Now().Before(st.paysFrom):
		// Set without a passkey (a first PIN, or reset by signing in
		// again): it pays 24 hours on (pin_me.go).
		return st, refusePay(http.StatusForbidden, "pin_new", "Your PIN was set without your passkey in the last 24 hours, so it can pay from "+
			st.paysFrom.In(lagosTZ).Format("15:04 on 2 Jan")+". Use your passkey until then.")
	}
	return st, nil
}

// pinPaidTodayTx is what the person confirmed with their PIN today, in
// every business.
func pinPaidTodayTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT taskiem_pin_paid_today($1)`, user).Scan(&n)
	return n, err
}

// pinFits checks a payment (with why the owner's OK would be asked) is
// one the PIN may confirm, given what the PIN paid today; it answers how
// much the PIN may still pay in one payment now.
func pinFits(x payment, needs string, today int64) (int64, error) {
	if x.BatchID != nil {
		return 0, refusePay(http.StatusForbidden, "pin_passkey_only", "This one is part of a list: confirm it with your passkey.")
	}
	if x.ChangedAccount {
		return 0, refusePay(http.StatusForbidden, "pin_passkey_only", "The account changed since it was last paid: confirm this one with your passkey.")
	}
	if needs != "" {
		return 0, refusePay(http.StatusForbidden, "pin_passkey_only", "This one needs the owner's OK: confirm it with your passkey.")
	}
	left := min(int64(pinPayOneKobo), pinPayDayKobo-today)
	if x.AmountKobo > pinPayOneKobo {
		return left, refusePay(http.StatusForbidden, "pin_limit", "Over your PIN's limit of "+invoicing.Naira(pinPayOneKobo)+" a payment: use your passkey for this one.")
	}
	if x.AmountKobo > left {
		return left, refusePay(http.StatusForbidden, "pin_limit", "Over your PIN's limit of "+invoicing.Naira(pinPayDayKobo)+" a day ("+invoicing.Naira(max(left, 0))+
			" left today): use your passkey for this one.")
	}
	return left, nil
}

// payPinCheck answers whether the payment can be confirmed with p's PIN
// now (before any PIN is typed), and how much the PIN may still pay.
func (s *Server) payPinCheck(ctx context.Context, p *Principal, x payment, again bool) (int64, error) {
	if _, err := s.pinForPay(ctx, p.UserID); err != nil {
		return 0, err
	}
	var left int64
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		_, needs, err := confirmCheckTx(ctx, tx, p, slices.Contains(p.Roles, "owner"), x.ID, again, false)
		if err != nil {
			return err
		}
		today, err := pinPaidTodayTx(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		left, err = pinFits(x, needs, today)
		return err
	})
	return left, err
}

// payOfferPin answers confirmPayment's step-up when the person's PIN can
// confirm this payment: 403 step_up with "pin" among the methods and the
// PIN's limit for it now (pin_limit_kobo). False leaves the answer to
// payPasskey (the passkey only).
func (s *Server) payOfferPin(w http.ResponseWriter, r *http.Request, x payment, revote bool) bool {
	p := principalFrom(r.Context())
	if revote || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil {
		return false
	}
	left, err := s.payPinCheck(r.Context(), p, x, true)
	if err != nil {
		var pr payRefused
		if !errors.As(err, &pr) {
			s.Logger.Warn("payments: offering the PIN", "payment", x.ID, "err", err)
		}
		return false
	}
	f, _, err := s.factorsOf(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		s.Logger.Warn("payments: offering the PIN", "payment", x.ID, "err", err)
		return false
	}
	methods, msg := []string{payStepUpPIN}, "confirm with your PIN"
	if f.Passkey {
		methods, msg = []string{"passkey", payStepUpPIN}, "confirm with your passkey, or your PIN"
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": msg, "step_up": "required", "methods": methods, "operation": payOpConfirm,
		"target": "payment:" + x.ID.String(), "pin_limit_kobo": left})
	return true
}

// confirmPaymentPin is POST /v1/payments/{id}/confirm {pin, again?}: the
// app's own PIN pad.
func (s *Server) confirmPaymentPin(w http.ResponseWriter, r *http.Request, id uuid.UUID, in confirmIn) {
	if in.Passkey != nil || in.TOTP != "" {
		writeErr(w, http.StatusBadRequest, "confirm with a passkey or a PIN, not both")
		return
	}
	x, err := s.payByPin(r, id, in.Pin, in.Again, in)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"payment": x})
}

// payByPin confirms payment id for the person in r with their PIN: the
// app's pad and WhatsApp's form both. A wrong PIN is refused pin_wrong
// (tries left said) or pin_locked; a payment the PIN may not confirm,
// pin_limit, pin_passkey_only, pin_new or no_pin, before the PIN is
// checked; anything else as confirmPayment refuses it.
// ticks are the sheet's Tell them in the chat and Send them a receipt,
// kept only once the PIN is right and the payment is claimed, as the
// passkey's confirm keeps them.
func (s *Server) payByPin(r *http.Request, id uuid.UUID, pin string, again bool, ticks confirmIn) (payment, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if p == nil || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil || p.Run != uuid.Nil {
		return payment{}, refusePay(http.StatusForbidden, "passkey", "Payments are confirmed by a person, not an API key.")
	}
	owner := slices.Contains(p.Roles, "owner")
	var x payment
	revote := false
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, _, err = confirmCheckTx(ctx, tx, p, owner, id, again, false)
		var pr payRefused
		if errors.As(err, &pr) && pr.code == "not_prepared" {
			// Sent with this PIN, and its run still waits for the payer's
			// own OK: the PIN gives it again.
			var with *string
			if err := tx.QueryRow(ctx, `SELECT confirmed_with FROM payments WHERE id = $1`, id).Scan(&with); err != nil {
				return err
			}
			if with != nil && *with == payStepUpPIN {
				if revote, err = awaitsPayerTx(ctx, tx, x, p); err != nil || revote {
					return err
				}
			}
			return pr
		}
		return err
	}); err != nil {
		return x, err
	}
	if _, err := s.pinForPay(ctx, p.UserID); err != nil {
		return x, err
	}
	if !revote {
		if _, err := s.payPinCheck(ctx, p, x, again); err != nil {
			return x, err
		}
		if _, err := s.stillAsPrepared(ctx, p.TenantID, x); err != nil {
			return x, s.dropChanged(r, x.ID, err)
		}
		if err := s.balanceCovers(ctx, p.TenantID, x); err != nil {
			return x, err
		}
	}
	// The PIN, counted with the approvals' tries, under its row lock.
	t, err := s.tryPin(ctx, p.UserID, pin)
	switch {
	case err != nil:
		return x, err
	case t.None:
		return x, refusePay(http.StatusForbidden, "no_pin", "You have no PIN set. Use your passkey.")
	case t.LockedUntil != nil:
		return x, refusePay(http.StatusForbidden, "pin_locked", "Your PIN is locked after too many wrong tries: use your passkey instead.")
	}
	ok, failures, locked := t.Right, t.Failures, t.NowLocked
	if !ok {
		if err := s.tx(r, func(tx pgx.Tx) error {
			if err := auditTx(r, tx, "payment.pin.fail", id.String(), map[string]any{"failures": failures, "via": p.AuthMethod}); err != nil || !locked {
				return err
			}
			return auditTx(r, tx, "payment.pin.locked", id.String(), map[string]any{"minutes": int(waPinWindow.Minutes()), "via": p.AuthMethod})
		}); err != nil {
			return x, err
		}
		if locked {
			return x, refusePay(http.StatusForbidden, "pin_locked", "Too many wrong PINs: your PIN is locked for 15 minutes. Use your passkey instead.")
		}
		return x, refusePay(http.StatusForbidden, "pin_wrong", fmt.Sprintf("That PIN is not right. %d tries left.", waPinMaxFailures-failures))
	}
	// Once claimed, the payment is sent (or given back) even if the person
	// goes away meanwhile.
	r = r.WithContext(context.WithoutCancel(ctx))
	ctx = r.Context()
	var run uuid.UUID
	if revote {
		run = *x.RunID
		if err := s.payerVote(r, run, p, payStepUpPIN, "payment.confirm_again"); err != nil {
			s.Logger.Warn("payments: the payer's OK again, by PIN", "payment", id, "err", err)
			return x, errPayerOK
		}
	} else {
		if err := s.tx(r, func(tx pgx.Tx) error {
			// One person's PIN payments one at a time, in every business:
			// the PIN's day total holds.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "pin-pay:"+p.UserID.String()); err != nil {
				return err
			}
			today, err := pinPaidTodayTx(ctx, tx, p.UserID)
			if err != nil {
				return err
			}
			var needs string
			if x, needs, err = confirmCheckTx(ctx, tx, p, owner, id, again, true); err != nil {
				return err
			}
			if _, err := pinFits(x, needs, today); err != nil {
				return err // the claim is rolled back
			}
			if _, err = tx.Exec(ctx, `UPDATE payments SET confirmed_with = 'pin' WHERE id = $1`, id); err != nil {
				return err
			}
			return setTellTx(ctx, tx, id, ticks)
		}); err != nil {
			return x, err
		}
		if run, err = s.sendPayment(r, x, "", p, payStepUpPIN); err != nil {
			if run != uuid.Nil {
				s.Logger.Warn("payments: the payer's OK, by PIN", "payment", id, "err", err)
				err = errPayerOK
			}
			return x, err
		}
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM payments WHERE id = $1`, id)); err != nil {
			return err
		}
		return auditTx(r, tx, "payment.confirm", id.String(), map[string]any{"amount_kobo": x.AmountKobo, "needs_ok": x.NeedsOK, "run": run,
			"changed_account": x.ChangedAccount, "again": revote, "step_up": payStepUpPIN, "via": p.AuthMethod})
	})
	if err == nil {
		s.paymentSent(ctx, p, x)
	}
	return x, err
}

// --- WhatsApp's form ---

// waOfferPayPin sends, after a payment prepared on WhatsApp, a PIN form
// bound to it, when Flows are set up, the person's PIN can pay and the
// payment is within its limits; the link stays the passkey's way.
func (s *Server) waOfferPayPin(ctx context.Context, c *chat, x payment) error {
	if c.wa == nil || c.wa.Config.FlowKey == nil || c.p == nil || x.Again {
		return nil
	}
	if _, err := s.payPinCheck(ctx, c.p, x, false); err != nil {
		var pr payRefused
		if errors.As(err, &pr) {
			return nil
		}
		return err
	}
	to := x.Payee
	if x.AccountName != nil && *x.AccountName != "" {
		to = *x.AccountName
	}
	what := paymentPurposeWords(x.Purpose)
	if x.PurposeNote != "" {
		what = x.PurposeNote
	}
	title := "Pay " + invoicing.Naira(x.AmountKobo)
	summary := whatsapp.SafeText(to) + " · " + whatsapp.SafeText(x.BankName) + " " + x.Account + " · for " + whatsapp.SafeText(what)
	tok, hash := whatsapp.NewFlowToken(c.tenant.ID)
	meta, _ := json.Marshal(map[string]any{"payment": x.ID.String(), "title": title, "summary": summary})
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, user_id, data, expires_at)
			VALUES ($1, $2, 'pay_pin', $3, $4, $5, $6, now() + $7::interval)`, hash, c.tenant.ID, c.addr, c.wa.Config.PhoneNumberID, c.p.UserID, meta, waHandoffTTL.String())
		return err
	}); err != nil {
		return err
	}
	_, err := c.wa.Client.SendFlow(ctx, c.addr, whatsapp.FlowMessage{Flow: whatsapp.FlowPin, Token: tok, CTA: c.t("button.enter_pin"), Screen: whatsapp.ScreenPin,
		Data: whatsapp.PinForm(title, summary, ""),
		Body: c.wa.Prefix(c.tenant.Name) + c.t("wa.pay.pin_form", "amount", invoicing.Naira(x.AmountKobo), "to", whatsapp.SafeText(to))})
	if err != nil {
		// Not delivered: the link is still there.
		s.Logger.Warn("whatsapp: sending a payment's PIN form", "err", err)
		s.waDropFlow(ctx, c.tenant.ID, hex.EncodeToString(hash))
	}
	return nil
}

// waFlowPayPin answers a payment's PIN form: its screen on opening; on
// submission the shared PIN confirm (payByPin). A wrong PIN keeps the form
// open with the tries left; anything else closes it with the outcome,
// said in the chat as the form completes.
func (s *Server) waFlowPayPin(ctx context.Context, row flowRow, req whatsapp.FlowRequest) any {
	title, _ := row.data["title"].(string)
	summary, _ := row.data["summary"].(string)
	screen := func(problem string) any {
		return flowScreen(whatsapp.ScreenPin, whatsapp.PinForm(title, summary, problem))
	}
	if req.Action != "data_exchange" {
		return screen("")
	}
	finish := func(status, outcome string) any {
		if err := s.setFlow(ctx, row, status, map[string]any{"payment": row.data["payment"], "title": title, "summary": summary, "outcome": outcome}); err != nil {
			s.Logger.Error("whatsapp: closing a payment's PIN form", "err", err)
		}
		if status == "done" {
			return flowDone(req.FlowToken)
		}
		return screen(outcome)
	}
	id, err := uuid.Parse(fmt.Sprint(row.data["payment"]))
	if err != nil || row.user == nil {
		return finish("refused", "This form is not for a payment any more.")
	}
	p, err := s.principalOf(ctx, row.tenant, *row.user)
	if err != nil || !p.Can(PermMoneyPay) {
		return finish("refused", "You cannot pay for this business any more.")
	}
	r, err := http.NewRequestWithContext(context.WithValue(ctx, ctxKey{}, p), http.MethodPost, "/v1/payments/"+id.String()+"/confirm", http.NoBody)
	if err != nil {
		return screen("Something went wrong; try again.")
	}
	pin, _ := req.Data["pin"].(string)
	x, err := s.payByPin(r, id, pin, false, confirmIn{})
	var pr payRefused
	switch {
	case errors.As(err, &pr) && pr.code == "pin_wrong":
		return screen(pr.msg)
	case errors.As(err, &pr):
		return finish("refused", "Not sent: "+pr.msg)
	case errors.Is(err, pgx.ErrNoRows):
		return finish("refused", "Not sent: that payment is gone.")
	case err != nil:
		s.Logger.Error("whatsapp: a payment by PIN", "payment", id, "err", err)
		return screen("Something went wrong; try again.")
	}
	to := x.Payee
	if x.AccountName != nil && *x.AccountName != "" {
		to = *x.AccountName
	}
	return finish("done", "Sent: "+whatsapp.SafeText(to)+" "+invoicing.Naira(x.AmountKobo)+". You will hear when the bank has it.")
}

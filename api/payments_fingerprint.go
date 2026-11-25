package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Paying with the fingerprint (docs/spec/fingerprint-key.md): a key on
// the person's phone (fingerprint_me.go) signs a challenge bound to one
// payment, after the phone's own fingerprint or face check, and the server
// checks the signature with the key it keeps.
//
//	POST /v1/payments/{id}/fingerprint {key_id, again?}
//	  -> {challenge, expires_in, limit_kobo}: the words to sign;
//	POST /v1/payments/{id}/fingerprint/confirm {key_id, challenge,
//	  signature, again?, tell_payee?, send_receipt?} -> 202 {payment}.
//
// What it may pay sits between the PIN and the passkey:
//
//   - its own limits: ₦500,000 a payment and ₦1,000,000 a day across the
//     person's fingerprint payments in every business (Lagos's day, as pay
//     limits count it); the role's limits apply too, and over those the
//     owner's OK is asked, which the fingerprint never leads to;
//   - as for the PIN, the passkey only for an account changed since it was
//     last paid, anything waiting for the owner's OK, and every batch;
//   - a challenge works once, for two minutes, for that payment and that
//     key; a signature that does not check is refused and audited. There
//     are no tries to count: the phone's own check guards the key.
//
// Then everything confirmPayment does: still as prepared, claimed under
// the lock, the limits, sent, and the payer's own OK with step-up
// "fingerprint".

const (
	fpPayOneKobo         = 500_000_00   // ₦500,000
	fpPayDayKobo         = 1_000_000_00 // ₦1,000,000
	payStepUpFingerprint = "fingerprint"
	fpChallengeTTL       = 2 * time.Minute
	fpChallengePrefix    = "Taskiem payment"
)

func (s *Server) fingerprintPayRoutes(r chi.Router) {
	pay := s.need(PermMoneyPay)
	r.With(pay, s.tenantWide).Post("/payments/{id}/fingerprint", s.fingerprintChallenge)
	r.With(pay, s.tenantWide).Post("/payments/{id}/fingerprint/confirm", s.confirmPaymentFingerprint)
}

// fingerprintFits checks a payment (with why the owner's OK would be
// asked) is one the fingerprint may confirm, given what it paid today; it
// answers how much the fingerprint may still pay in one payment now.
func fingerprintFits(x payment, needs string, today int64) (int64, error) {
	if x.BatchID != nil {
		return 0, refusePay(http.StatusForbidden, "fingerprint_passkey_only", "This one is part of a list: confirm it with your passkey.")
	}
	if x.ChangedAccount {
		return 0, refusePay(http.StatusForbidden, "fingerprint_passkey_only", "The account changed since it was last paid: confirm this one with your passkey.")
	}
	if needs != "" {
		return 0, refusePay(http.StatusForbidden, "fingerprint_passkey_only", "This one needs the owner's OK: confirm it with your passkey.")
	}
	left := min(int64(fpPayOneKobo), fpPayDayKobo-today)
	if x.AmountKobo > fpPayOneKobo {
		return left, refusePay(http.StatusForbidden, "fingerprint_limit", "Over your fingerprint's limit of "+invoicing.Naira(fpPayOneKobo)+
			" a payment: use your passkey for this one.")
	}
	if x.AmountKobo > left {
		return left, refusePay(http.StatusForbidden, "fingerprint_limit", "Over your fingerprint's limit of "+invoicing.Naira(fpPayDayKobo)+" a day ("+
			invoicing.Naira(max(left, 0))+" left today): use your passkey for this one.")
	}
	return left, nil
}

func fingerprintPaidTodayTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT taskiem_fingerprint_paid_today($1)`, user).Scan(&n)
	return n, err
}

// fingerprintCheck answers whether the payment can be confirmed with p's
// fingerprint now, and how much it may still pay.
func (s *Server) fingerprintCheck(ctx context.Context, p *Principal, x payment, again bool) (int64, error) {
	var left int64
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		_, needs, err := confirmCheckTx(ctx, tx, p, slices.Contains(p.Roles, "owner"), x.ID, again, false)
		if err != nil {
			return err
		}
		today, err := fingerprintPaidTodayTx(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		left, err = fingerprintFits(x, needs, today)
		return err
	})
	return left, err
}

// fingerprintPayer refuses what is not a person paying.
func fingerprintPayer(p *Principal) error {
	if p == nil || p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil || p.Agent != nil || p.Run != uuid.Nil {
		return refusePay(http.StatusForbidden, "passkey", "Payments are confirmed by a person, not an API key.")
	}
	return nil
}

// fingerprintPrepared reads payment id as p may confirm it now: revote
// when it was sent with the fingerprint and its run still waits for p's
// own OK.
func (s *Server) fingerprintPrepared(r *http.Request, p *Principal, id uuid.UUID, again bool) (payment, bool, error) {
	ctx := r.Context()
	var x payment
	revote := false
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, _, err = confirmCheckTx(ctx, tx, p, slices.Contains(p.Roles, "owner"), id, again, false)
		var pr payRefused
		if errors.As(err, &pr) && pr.code == "not_prepared" {
			var with *string
			if err := tx.QueryRow(ctx, `SELECT confirmed_with FROM payments WHERE id = $1`, id).Scan(&with); err != nil {
				return err
			}
			if with != nil && *with == payStepUpFingerprint {
				if revote, err = awaitsPayerTx(ctx, tx, x, p); err != nil || revote {
					return err
				}
			}
			return pr
		}
		return err
	})
	return x, revote, err
}

// fingerprintWords are the words the phone signs for one payment: what it
// is and a one-use nonce.
func fingerprintWords(x payment, nonce []byte) string {
	return fpChallengePrefix + "\n" + x.ID.String() + "\n" + strconv.FormatInt(x.AmountKobo, 10) + "\n" + base64.RawURLEncoding.EncodeToString(nonce)
}

// fingerprintChallenge is POST /v1/payments/{id}/fingerprint {key_id,
// again?}: when the fingerprint may confirm this payment, the words for
// that key to sign, good once for two minutes.
func (s *Server) fingerprintChallenge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		KeyID uuid.UUID `json:"key_id"`
		Again bool      `json:"again"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if err := fingerprintPayer(p); err != nil {
		s.payFail(w, r, err)
		return
	}
	x, revote, err := s.fingerprintPrepared(r, p, id, in.Again)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	left := int64(fpPayOneKobo)
	if !revote {
		if left, err = s.fingerprintCheck(ctx, p, x, in.Again); err != nil {
			s.payFail(w, r, err)
			return
		}
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		s.fail(w, r, err)
		return
	}
	words := fingerprintWords(x, nonce)
	hash := sha256.Sum256([]byte(words))
	var ok bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_pay_key_challenge($1, $2, $3, $4, $5::interval)`, p.UserID, in.KeyID, id, hash[:], fpChallengeTTL.String()).Scan(&ok); err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		s.payFail(w, r, errNoFingerprint)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge": words, "expires_in": int(fpChallengeTTL.Seconds()), "limit_kobo": left})
}

// errNoFingerprint: the key named is not one of the person's (removed, or
// replaced by turning it on again).
var errNoFingerprint = refusePay(http.StatusForbidden, "no_fingerprint", "Paying by fingerprint is not on for this phone any more: turn it on again in Settings, or use your passkey or PIN.")

// fingerprintIn is POST /v1/payments/{id}/fingerprint/confirm's body.
type fingerprintIn struct {
	KeyID     uuid.UUID `json:"key_id"`
	Challenge string    `json:"challenge"`
	Signature string    `json:"signature"`
	Again     bool      `json:"again"`
	// The sheet's ticks, as confirmPayment takes them.
	TellPayee   *bool `json:"tell_payee"`
	SendReceipt *bool `json:"send_receipt"`
}

// confirmPaymentFingerprint is POST /v1/payments/{id}/fingerprint/confirm:
// the payment sent with the phone's signature of its challenge.
func (s *Server) confirmPaymentFingerprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in fingerprintIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.KeyID == uuid.Nil || in.Challenge == "" || in.Signature == "" {
		s.fail(w, r, fmt.Errorf("%w: key_id, challenge and signature are needed", errBadRequest))
		return
	}
	x, err := s.payByFingerprint(r, id, in)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"payment": x})
}

// payByFingerprint confirms payment id for the person in r with their
// phone's signature: fingerprint_limit, fingerprint_passkey_only before the
// signature is looked at; fingerprint_expired for a challenge that is not
// this payment's and this key's, or was used or is old; fingerprint_wrong
// for a signature that does not check; anything else as confirmPayment
// refuses it.
func (s *Server) payByFingerprint(r *http.Request, id uuid.UUID, in fingerprintIn) (payment, error) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if err := fingerprintPayer(p); err != nil {
		return payment{}, err
	}
	owner := slices.Contains(p.Roles, "owner")
	x, revote, err := s.fingerprintPrepared(r, p, id, in.Again)
	if err != nil {
		return x, err
	}
	if !revote {
		if _, err := s.fingerprintCheck(ctx, p, x, in.Again); err != nil {
			return x, err
		}
		if _, err := s.stillAsPrepared(ctx, p.TenantID, x); err != nil {
			return x, s.dropChanged(r, x.ID, err)
		}
		if err := s.balanceCovers(ctx, p.TenantID, x); err != nil {
			return x, err
		}
	}
	// The challenge, taken once whatever the signature.
	hash := sha256.Sum256([]byte(in.Challenge))
	var der []byte
	err = s.Store.Pool.QueryRow(ctx, `SELECT taskiem_pay_key_take($1, $2, $3, $4)`, p.UserID, in.KeyID, id, hash[:]).Scan(&der)
	switch {
	case err != nil:
		return x, err
	case der == nil:
		return x, refusePay(http.StatusForbidden, "fingerprint_expired", "That fingerprint check was for another moment: try again.")
	}
	if !fingerprintSigned(der, in.Challenge, in.Signature) || !strings.HasPrefix(in.Challenge, fpChallengePrefix+"\n"+id.String()+"\n") {
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "payment.fingerprint.fail", id.String(), map[string]any{"key": in.KeyID, "via": p.AuthMethod})
		}); err != nil {
			return x, err
		}
		return x, refusePay(http.StatusForbidden, "fingerprint_wrong", "Your phone's fingerprint check did not match: try again, or use your passkey or PIN.")
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_pay_key_used($1, $2)`, p.UserID, in.KeyID); err != nil {
		return x, err
	}
	// Once claimed, the payment is sent (or given back) even if the person
	// goes away meanwhile.
	r = r.WithContext(context.WithoutCancel(ctx))
	ctx = r.Context()
	var run uuid.UUID
	if revote {
		run = *x.RunID
		if err := s.payerVote(r, run, p, payStepUpFingerprint, "payment.confirm_again"); err != nil {
			s.Logger.Warn("payments: the payer's OK again, by fingerprint", "payment", id, "err", err)
			return x, errPayerOK
		}
	} else {
		if err := s.tx(r, func(tx pgx.Tx) error {
			// One person's fingerprint payments one at a time, in every
			// business: its day total holds.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "fingerprint-pay:"+p.UserID.String()); err != nil {
				return err
			}
			today, err := fingerprintPaidTodayTx(ctx, tx, p.UserID)
			if err != nil {
				return err
			}
			var needs string
			if x, needs, err = confirmCheckTx(ctx, tx, p, owner, id, in.Again, true); err != nil {
				return err
			}
			if _, err := fingerprintFits(x, needs, today); err != nil {
				return err // the claim is rolled back
			}
			if _, err = tx.Exec(ctx, `UPDATE payments SET confirmed_with = 'fingerprint' WHERE id = $1`, id); err != nil {
				return err
			}
			return setTellTx(ctx, tx, id, confirmIn{TellPayee: in.TellPayee, SendReceipt: in.SendReceipt})
		}); err != nil {
			return x, err
		}
		if run, err = s.sendPayment(r, x, "", p, payStepUpFingerprint); err != nil {
			if run != uuid.Nil {
				s.Logger.Warn("payments: the payer's OK, by fingerprint", "payment", id, "err", err)
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
			"changed_account": x.ChangedAccount, "again": revote, "step_up": payStepUpFingerprint, "key": in.KeyID, "via": p.AuthMethod})
	})
	if err == nil {
		s.paymentSent(ctx, p, x)
	}
	return x, err
}

// fingerprintSigned checks sig (base64 DER ECDSA over SHA-256, as the
// phone's secure hardware signs) of words with the kept key.
func fingerprintSigned(der []byte, words, sig string) bool {
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return false
	}
	ec, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(words))
	return ecdsa.VerifyASN1(ec, sum[:], raw)
}

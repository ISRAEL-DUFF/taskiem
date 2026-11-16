package api

// The phone way in (docs/spec/taskiem-for-everyone.md §5.1, migration
// 00448). A person types their phone number; Taskiem sends a six-digit code
// by WhatsApp from its own number, else by SMS ("Send by SMS instead" asks
// for SMS). A number that belongs to an account signs in to it, to the
// person's own side (a personal session); a new number gets a short-lived
// ticket to make a personal account with a name, agreeing to the terms and
// confirming they are 18 or over. Five codes an hour per number, limits
// per address, and five wrong codes lock the number for an hour.
//
// Changing one's phone (§5.5) is here too: a code to the new number, and
// either a device that has proved itself or a code to the old number.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	phoneCodeTTL         = 10 * time.Minute
	phoneCodeMaxSends    = 5 // an hour, per number
	phoneCodeMaxFailures = 5 // then the number is locked for an hour
	phoneTicketTTL       = 15 * time.Minute
)

// phoneCodeWrong is a wrong or expired code's answer.
const phoneCodeWrong = "That code is not right, or it has expired. Ask for a new one."

var errSMSUnavailable = errors.New("codes by SMS are not available here")

// phoneCodeText is the SMS a code goes in.
func phoneCodeText(code string) string {
	return "Your Taskiem code is " + code + ". It expires in 10 minutes. Never share it with anyone."
}

// sendPhoneCode sends a code by WhatsApp, else (or when asked) by SMS, and
// says how it went.
func (s *Server) sendPhoneCode(ctx context.Context, number, via, code string) (string, error) {
	if via != "sms" && s.WhatsApp != nil {
		// From Taskiem's own number, counted against no business: the code
		// is for a person.
		_, err := s.WhatsApp.SendTemplate(ctx, uuid.Nil, number, whatsapp.TplOTP, map[string]string{"code": code}, nil)
		if err == nil {
			return "whatsapp", nil
		}
		if s.CodeSMS == nil {
			return "", err
		}
		s.Logger.Warn("phone code: WhatsApp refused, sending by SMS", "err", err)
	}
	if s.CodeSMS == nil {
		return "", errSMSUnavailable
	}
	if err := s.CodeSMS.SendCode(ctx, number, phoneCodeText(code)); err != nil {
		return "", err
	}
	return "sms", nil
}

// issuePhoneCode issues and sends one code, answering the request itself
// when it cannot: 429 for the hour's limit or a locked number, 503 when no
// way of sending is set up, 502 when sending failed. It reports how the
// code went, "" when it answered.
func (s *Server) issuePhoneCode(w http.ResponseWriter, r *http.Request, number, purpose string, user *uuid.UUID, via string) string {
	ctx := r.Context()
	if via == "sms" && s.CodeSMS == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": errSMSUnavailable.Error(), "sms_unavailable": true})
		return ""
	}
	if s.WhatsApp == nil && s.CodeSMS == nil {
		writeErr(w, http.StatusServiceUnavailable, "this server cannot send codes to phones")
		return ""
	}
	planned := "whatsapp"
	if via == "sms" || s.WhatsApp == nil {
		planned = "sms"
	}
	code := otpCode()
	var outcome string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_code_issue($1, $2, $3, $4, $5, $6::interval, $7)`,
		number, purpose, user, code, planned, phoneCodeTTL.String(), phoneCodeMaxSends).Scan(&outcome); err != nil {
		s.fail(w, r, err)
		return ""
	}
	switch outcome {
	case "too_many":
		writeErr(w, http.StatusTooManyRequests, "We have sent this number several codes. Wait an hour, then ask again.")
		return ""
	case "locked":
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many wrong codes: this number is locked for an hour.", "locked": true})
		return ""
	}
	sent, err := s.sendPhoneCode(ctx, number, via, code)
	if errors.Is(err, errSMSUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "sms_unavailable": true})
		return ""
	}
	if err != nil {
		s.Logger.Warn("phone code not sent", "err", err, "number", whatsapp.MaskNumber(number))
		writeErr(w, http.StatusBadGateway, "The code could not be sent just now. Try again in a moment, or ask for it by SMS.")
		return ""
	}
	return sent
}

// phoneNumber reads a typed number as E.164, or answers 400.
func (s *Server) phoneNumber(w http.ResponseWriter, r *http.Request, typed string) (string, bool) {
	number, err := whatsapp.Normalise(typed, s.defaultCountry())
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: give your phone number, with its country code if it is not Nigerian", errBadRequest))
		return "", false
	}
	return number, true
}

// phoneStart is POST /v1/auth/phone/start ({phone, via?}): a code to the
// number, whoever it belongs to.
func (s *Server) phoneStart(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.limiter("phone-start:"+limitIP(r), time.Hour, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Via   string `json:"via"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Via != "" && req.Via != "whatsapp" && req.Via != "sms" {
		s.fail(w, r, fmt.Errorf("%w: via is whatsapp or sms", errBadRequest))
		return
	}
	number, ok := s.phoneNumber(w, r, req.Phone)
	if !ok {
		return
	}
	sent := s.issuePhoneCode(w, r, number, "sign_in", nil, req.Via)
	if sent == "" {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_via": sent, "expires_in": int(phoneCodeTTL.Seconds())})
}

// checkPhoneCode checks a code, answering 401 or 429 itself when it is not
// right. ticket, when given, is kept for a new account.
func (s *Server) checkPhoneCode(w http.ResponseWriter, r *http.Request, number, purpose string, user *uuid.UUID, code string, ticket []byte) bool {
	var outcome string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_phone_code_check($1, $2, $3, $4, $5, $6)`,
		number, purpose, user, strings.TrimSpace(code), phoneCodeMaxFailures, ticket).Scan(&outcome); err != nil {
		s.fail(w, r, err)
		return false
	}
	switch outcome {
	case "ok":
		return true
	case "locked":
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many wrong codes: this number is locked for an hour.", "locked": true})
	default:
		writeErr(w, http.StatusUnauthorized, phoneCodeWrong)
	}
	return false
}

// phoneVerify is POST /v1/auth/phone/verify ({phone, code, bearer?,
// device?}): signed in to the account the number belongs to, on the
// person's own side ({status: signed_in}), or a ticket to make one
// ({status: new, ticket}).
func (s *Server) phoneVerify(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("phone-verify:" + limitIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req struct {
		Phone  string     `json:"phone"`
		Code   string     `json:"code"`
		Bearer bool       `json:"bearer"`
		Device *deviceReq `json:"device"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	r, ok := askDevice(w, r, req.Device, req.Bearer)
	if !ok {
		return
	}
	number, err := whatsapp.Normalise(req.Phone, s.defaultCountry())
	if err != nil {
		writeErr(w, http.StatusUnauthorized, phoneCodeWrong)
		return
	}
	if !s.loginLimiter("phone-verify-number:" + number).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	ticket := newToken()
	if !s.checkPhoneCode(w, r, number, "sign_in", nil, req.Code, hashToken(ticket)) {
		return
	}
	ctx := r.Context()
	var user uuid.UUID
	var status string
	var on, hasPhone bool
	err = s.Store.Pool.QueryRow(ctx, `SELECT user_id, status, chat_signin, has_phone FROM taskiem_auth_phone_user($1)`, number).Scan(&user, &status, &on, &hasPhone)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "new", "ticket": ticket, "expires_in": int(phoneTicketTTL.Seconds())})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if status != "active" {
		writeErr(w, http.StatusUnauthorized, errAccountOff.Error())
		return
	}
	if !on {
		// The person closed the doors a number someone else may now hold
		// opens (chat_signin.go): sign-in by code included.
		s.chatSigninRefused(ctx, user, "phone", "code", "off", clientIP(r), r.UserAgent())
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "signing in with a code to your phone is off for this account: sign in with your email",
			"phone_signin_off": true})
		return
	}
	if !hasPhone {
		// Found by their WhatsApp or Telegram: that number is now their phone.
		var claimed string
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_claim($1, $2)`, user, number).Scan(&claimed); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.phoneSignedIn(w, r, user, req.Bearer, map[string]any{"status": "signed_in"})
	_ = s.auditEverywhere(ctx, user, "auth.phone_signin", map[string]any{"number": whatsapp.MaskNumber(number), "ip": clientIP(r)})
}

// phoneSignedIn remembers the device and starts the personal session.
func (s *Server) phoneSignedIn(w http.ResponseWriter, r *http.Request, user uuid.UUID, bearer bool, out map[string]any) {
	key := s.signinDeviceKey(w, r, bearer)
	var proved bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_device_signin($1, $2, true)`, user, key).Scan(&proved); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.startPersonalSession(w, r, user, "phone", bearer, out); errors.Is(err, errPhoneBusy) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	} else if errors.Is(err, errAccountOff) {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	code := http.StatusOK
	if out["status"] == "created" {
		code = http.StatusCreated
	}
	writeJSON(w, code, out)
}

// personPlaceholderEmail is the sign-in name of an account made by phone:
// users.email is required, so it has one that receives no mail (.invalid),
// as chat-born owners have (ownerPlaceholderEmail), until the person adds
// a real one. It names the account, not the number, so a number changed or
// reissued never matches it.
func personPlaceholderEmail(user uuid.UUID) string {
	h := sha256.Sum256([]byte("taskiem-person:" + user.String()))
	return "person+" + hex.EncodeToString(h[:8]) + "@phone.taskiem.invalid"
}

// phoneCreate is POST /v1/auth/phone/create ({ticket, name, adult, terms,
// bearer?, device?}): a personal account on the number the ticket proved,
// signed in to its own side.
func (s *Server) phoneCreate(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	if !s.limiter("phone-create:"+limitIP(r), time.Hour, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many accounts made from here; try again later")
		return
	}
	var req struct {
		Ticket string     `json:"ticket"`
		Name   string     `json:"name"`
		Adult  bool       `json:"adult"`
		Terms  bool       `json:"terms"`
		Bearer bool       `json:"bearer"`
		Device *deviceReq `json:"device"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	r, ok := askDevice(w, r, req.Device, req.Bearer)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 80:
		s.fail(w, r, fmt.Errorf("%w: your name is 1 to 80 characters", errBadRequest))
		return
	case !req.Adult:
		s.fail(w, r, fmt.Errorf("%w: Taskiem accounts are for people 18 or over", errBadRequest))
		return
	case !req.Terms:
		s.fail(w, r, fmt.Errorf("%w: agree to the terms and privacy notice to make an account", errBadRequest))
		return
	}
	ctx := r.Context()
	user := uuid.Must(uuid.NewV7())
	channel := "web"
	if req.Device != nil {
		channel = "app"
	}
	var number string
	err := db.InPersonTx(ctx, s.Store.Pool, user, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_phone_ticket_use($1)`, hashToken(strings.TrimSpace(req.Ticket))).Scan(&number); err != nil {
			return errTicket
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, adult_confirmed_at) VALUES ($1, $2, $3, now())`,
			user, personPlaceholderEmail(user), name); err != nil {
			return err
		}
		var claimed string
		if err := tx.QueryRow(ctx, `SELECT taskiem_auth_phone_claim($1, $2)`, user, number).Scan(&claimed); err != nil {
			return err
		}
		if claimed != "ok" {
			return errNumberTaken
		}
		if s.Terms.On() {
			if _, err := tx.Exec(ctx, `SELECT taskiem_terms_accept($1, $2, $3, $4)`, user, s.Terms.Version, s.Terms.PrivacyVersion, channel); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errTicket):
		writeErr(w, http.StatusUnauthorized, "This sign-up has expired. Ask for a new code.")
		return
	case errors.Is(err, errNumberTaken):
		writeErr(w, http.StatusConflict, "This number belongs to an account now: sign in with it.")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.phoneSignedIn(w, r, user, req.Bearer, map[string]any{"status": "created"})
}

var (
	errTicket      = errors.New("no such ticket")
	errNumberTaken = errors.New("that number belongs to another account")
)

// --- changing one's phone (§5.5) ---

// needsOldCode is whether a phone change needs a code to the old number: a
// person who has one, in a session made by a code to their phone or chat,
// on a device that has not proved itself another way.
func (s *Server) needsOldCode(r *http.Request, p *Principal) (bool, string, error) {
	var old *string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_phone_of($1)`, p.UserID).Scan(&old); err != nil {
		return false, "", err
	}
	if old == nil {
		return false, "", nil
	}
	if p.AuthMethod != "phone" && p.AuthMethod != "chat" {
		return false, *old, nil
	}
	st, err := s.proofStateOf(r, p)
	if err != nil {
		return false, *old, err
	}
	return !st.deviceProved, *old, nil
}

// phoneChangeStart is POST /v1/me/phone/start ({phone, via?, confirm_old?}):
// a code to the new number and, with confirm_old, one to the old.
func (s *Server) phoneChangeStart(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.Agent != nil {
		writeErr(w, http.StatusForbidden, "a phone belongs to a person, not an API key")
		return
	}
	if !s.limiter("phone-change:"+p.UserID.String(), time.Hour, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	var req struct {
		Phone      string `json:"phone"`
		Via        string `json:"via"`
		ConfirmOld bool   `json:"confirm_old"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Via != "" && req.Via != "whatsapp" && req.Via != "sms" {
		s.fail(w, r, fmt.Errorf("%w: via is whatsapp or sms", errBadRequest))
		return
	}
	number, ok := s.phoneNumber(w, r, req.Phone)
	if !ok {
		return
	}
	needsOld, old, err := s.needsOldCode(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if number == old {
		s.fail(w, r, fmt.Errorf("%w: that is your phone number already", errConflict))
		return
	}
	var taken bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_phone_taken($1)`, number).Scan(&taken); err != nil {
		s.fail(w, r, err)
		return
	}
	if taken {
		// The person's own WhatsApp binding to it is no one else's.
		var owner uuid.UUID
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT user_id FROM taskiem_auth_phone_user($1)`, number).Scan(&owner); err != nil || owner != p.UserID {
			s.fail(w, r, fmt.Errorf("%w: that number belongs to another account", errConflict))
			return
		}
	}
	user := p.UserID
	sent := s.issuePhoneCode(w, r, number, "change_new", &user, req.Via)
	if sent == "" {
		return
	}
	out := map[string]any{"sent_via": sent, "expires_in": int(phoneCodeTTL.Seconds()), "needs_old_code": needsOld}
	if req.ConfirmOld && old != "" {
		oldSent := s.issuePhoneCode(w, r, old, "change_old", &user, req.Via)
		if oldSent == "" {
			return
		}
		out["old_sent_via"] = oldSent
	}
	writeJSON(w, http.StatusAccepted, out)
}

// phoneChangeVerify is POST /v1/me/phone/verify ({code, old_code?, phone?}):
// the new number becomes the person's phone. old_code is needed where
// needs_old_code said so.
func (s *Server) phoneChangeVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.Agent != nil {
		writeErr(w, http.StatusForbidden, "a phone belongs to a person, not an API key")
		return
	}
	if !s.loginLimiter("phone-change-verify:" + p.UserID.String()).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many tries; wait a little")
		return
	}
	var req struct {
		Phone   string `json:"phone"`
		Code    string `json:"code"`
		OldCode string `json:"old_code"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var number string
	if req.Phone != "" {
		n, ok := s.phoneNumber(w, r, req.Phone)
		if !ok {
			return
		}
		number = n
	} else {
		var pending *string
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_change_pending($1)`, p.UserID).Scan(&pending); err != nil {
			s.fail(w, r, err)
			return
		}
		if pending == nil {
			writeErr(w, http.StatusUnauthorized, phoneCodeWrong)
			return
		}
		number = *pending
	}
	needsOld, old, err := s.needsOldCode(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	user := p.UserID
	if needsOld {
		if strings.TrimSpace(req.OldCode) == "" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm with a code to your old number (ask with confirm_old), or prove this device another way first",
				"old_code_required": true})
			return
		}
		if !s.checkPhoneCode(w, r, old, "change_old", &user, req.OldCode, nil) {
			return
		}
	}
	if !s.checkPhoneCode(w, r, number, "change_new", &user, req.Code, nil) {
		return
	}
	if needsOld {
		var used bool
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_old_proven($1)`, p.UserID).Scan(&used); err != nil || !used {
			writeErr(w, http.StatusUnauthorized, phoneCodeWrong)
			return
		}
	}
	var outcome string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_change($1, $2)`, p.UserID, number).Scan(&outcome); err != nil {
		s.fail(w, r, err)
		return
	}
	switch outcome {
	case "taken":
		s.fail(w, r, fmt.Errorf("%w: that number belongs to another account", errConflict))
		return
	case "unproven":
		writeErr(w, http.StatusUnauthorized, phoneCodeWrong)
		return
	}
	detail := map[string]any{"number": whatsapp.MaskNumber(number), "ip": clientIP(r)}
	if old != "" {
		detail["old"] = whatsapp.MaskNumber(old)
	}
	_ = s.auditEverywhere(ctx, p.UserID, "auth.phone_change", detail)
	writeJSON(w, http.StatusOK, map[string]any{"phone": number})
}

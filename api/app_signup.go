package api

// Signing up in the phone app as on WhatsApp (docs/spec/mobile-app-m4.md
// N7): the person proves their number with a code sent on WhatsApp, then
// answers the WhatsApp sign-up's questions (chat_signup.go) in the app's own
// chat, a third channel, "app", whose messages are this request's answer
// rather than sent anywhere. At the end the business is made as on WhatsApp,
// its owner bound to the number, and this phone signed in with a device
// session.
//
//	POST /v1/signup/app/code    {phone}                      a code on WhatsApp
//	POST /v1/signup/app/verify  {phone, code}                the sign-up's token, and its first question
//	POST /v1/signup/app/turn    {phone, token, text|reply, device}  the next question; at the end a session
//
// It is chat sign-up's rules: off unless the operator lists "app" in
// TASKIEM_CHAT_SIGNUP (with WhatsApp to send the codes, and terms), the
// countries it is open to, and its limits per number, hour and day.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	appSignupCodeTTL    = 10 * time.Minute
	appSignupCodeSends  = 3 // a number, while a code lives
	appSignupCodeTries  = 5
	appSignupTokenBytes = 24
)

// appChannel is the app's sign-up conversation: what Taskiem says is kept
// for the turn's answer.
type appChannel struct {
	said []appSaid
}

type appSaid struct {
	Text    string      `json:"text"`
	Buttons []appButton `json:"buttons,omitempty"`
}

type appButton struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func (*appChannel) Name() string         { return "app" }
func (*appChannel) OwnTenant() uuid.UUID { return uuid.Nil }
func (*appChannel) Prefix(string) string { return "" }
func (a *appChannel) Send(_ context.Context, _ string, m whatsapp.Message) (string, error) {
	said := appSaid{Text: m.Text}
	for _, b := range m.Buttons {
		// A template to start is the chat's, after the sign-up: the app
		// lands in Taskiem's conversation instead.
		if strings.HasPrefix(b.ID, signupTplPrefix) {
			continue
		}
		said.Buttons = append(said.Buttons, appButton{ID: b.ID, Title: b.Title})
	}
	a.said = append(a.said, said)
	return "", nil
}
func (*appChannel) Contact(context.Context, string) (whatsapp.Contact, error) {
	return whatsapp.Contact{}, nil
}
func (*appChannel) Touch(context.Context, string, bool, bool, *uuid.UUID) error { return nil }

// appSignupOn reports whether the app may sign people up here.
func (s *Server) appSignupOn() bool {
	return s.WhatsApp != nil && s.Terms.On() && slices.Contains(s.ChatSignup.Channels, "app")
}

func appAddr(number string) string { return "app:" + number }

// appCode is a code's state, kept with the sign-ups; checked in the
// database (taskiem_app_signup_code_check).
type appCode struct {
	Hash  string `json:"hash"`
	Tries int    `json:"tries"`
	Sends int    `json:"sends"`
}

func appCodeKey(number string) []byte { return signupKey("app-code:" + number) }

func digest(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func (s *Server) appCodeLoad(ctx context.Context, number string) (*appCode, error) {
	var step string
	var raw []byte
	err := s.Store.Pool.QueryRow(ctx, `SELECT step, data FROM taskiem_chat_signup_get($1)`, appCodeKey(number)).Scan(&step, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c appCode
	return &c, json.Unmarshal(raw, &c)
}

func (s *Server) appCodeSave(ctx context.Context, number string, c *appCode) error {
	raw, _ := json.Marshal(c)
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_signup_put($1, 'app', 'code', $2, $3::interval)`, appCodeKey(number), raw, appSignupCodeTTL.String())
	return err
}

// appSignupNumber reads and checks the number a request is for.
func (s *Server) appSignupNumber(w http.ResponseWriter, raw string) (string, bool) {
	number, err := whatsapp.Normalise(raw, s.defaultCountry())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "That number does not look right. Type it with the country code, e.g. +2348012345678.")
		return "", false
	}
	return number, true
}

// appSignupCode sends a code on WhatsApp to the number signing up.
func (s *Server) appSignupCode(w http.ResponseWriter, r *http.Request) {
	if !s.appSignupOn() {
		writeErr(w, http.StatusNotFound, "signing up in the app is not open on this server")
		return
	}
	if !jsonOnly(w, r) {
		return
	}
	if !s.limiter("app-signup:"+clientIP(r), time.Hour, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many codes asked for; try again later")
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, ok := s.appSignupNumber(w, req.Phone)
	if !ok {
		return
	}
	ctx := r.Context()
	sc := &signupChat{ch: &appChannel{}, addr: appAddr(number), lang: lang.EN}
	if msg, err := s.signupOpen(ctx, sc, number); err != nil {
		s.fail(w, r, err)
		return
	} else if msg != "" {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	c, err := s.appCodeLoad(ctx, number)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if c == nil {
		c = &appCode{}
	}
	if c.Sends >= appSignupCodeSends {
		writeErr(w, http.StatusTooManyRequests, "We have sent this number several codes. Wait ten minutes, then ask again.")
		return
	}
	code := otpCode()
	// The tries so far are kept: a new code is not five more guesses.
	c.Hash, c.Sends = digest(number+":"+code), c.Sends+1
	if err := s.appCodeSave(ctx, number, c); err != nil {
		s.fail(w, r, err)
		return
	}
	// From Taskiem's own number, whatever the window: the authentication
	// template, counted against no business (there is none yet).
	if _, err := s.WhatsApp.SendTemplate(ctx, uuid.Nil, number, whatsapp.TplOTP, map[string]string{"code": code}, nil); err != nil {
		s.Logger.Warn("app sign-up: sending the code", "err", err)
		writeErr(w, http.StatusBadGateway, "The code could not be sent to WhatsApp just now. Try again in a moment.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "We sent a code to " + number + " on WhatsApp."})
}

// appSignupVerify checks the code and starts the sign-up's conversation.
func (s *Server) appSignupVerify(w http.ResponseWriter, r *http.Request) {
	if !s.appSignupOn() {
		writeErr(w, http.StatusNotFound, "signing up in the app is not open on this server")
		return
	}
	if !jsonOnly(w, r) {
		return
	}
	if !s.loginLimiter("app-signup-verify:" + clientIP(r)).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many tries; wait a little")
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, ok := s.appSignupNumber(w, req.Phone)
	if !ok {
		return
	}
	// Guesses at one number from many addresses are counted too.
	if !s.loginLimiter("app-signup-verify-number:" + number).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many tries; wait a little")
		return
	}
	ctx := r.Context()
	// Checked and counted in one step, so guesses sent together are each
	// counted; the right code leaves the number proven for the binding.
	var outcome string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_app_signup_code_check($1, $2, $3)`,
		number, digest(number+":"+strings.TrimSpace(req.Code)), appSignupCodeTries).Scan(&outcome); err != nil {
		s.fail(w, r, err)
		return
	}
	if outcome != "ok" {
		writeErr(w, http.StatusUnauthorized, chatCodeWrong)
		return
	}
	var err error
	// A number already on an account signs in instead (now that it is
	// proven, saying so tells nobody else anything).
	var user uuid.UUID
	err = s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_chat_user($1)`, number).Scan(&user)
	switch {
	case err == nil:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This number is already on a Taskiem account. Sign in with WhatsApp instead.", "sign_in": true})
		return
	case !errors.Is(err, pgx.ErrNoRows):
		s.fail(w, r, err)
		return
	}
	token := make([]byte, appSignupTokenBytes)
	_, _ = rand.Read(token)
	tok := hex.EncodeToString(token)
	ch := &appChannel{}
	sc := &signupChat{ch: ch, addr: appAddr(number), lang: lang.EN}
	if err := s.signupBegin(ctx, sc); err != nil {
		s.fail(w, r, err)
		return
	}
	// The sign-up is this phone's: its token proves each answer.
	st, err := s.signupLoad(ctx, sc.addr)
	if err != nil || st == nil {
		if err == nil {
			err = errors.New("app sign-up: not begun")
		}
		s.fail(w, r, err)
		return
	}
	st.Proof = digest(tok)
	if err := s.signupSave(ctx, sc, st); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "messages": ch.said})
}

// appSignupTurn takes an answer and gives the next question; the last
// answer makes the business and signs this phone in.
func (s *Server) appSignupTurn(w http.ResponseWriter, r *http.Request) {
	if !s.appSignupOn() {
		writeErr(w, http.StatusNotFound, "signing up in the app is not open on this server")
		return
	}
	if !jsonOnly(w, r) {
		return
	}
	var req struct {
		Phone  string     `json:"phone"`
		Token  string     `json:"token"`
		Text   string     `json:"text"`
		Reply  string     `json:"reply"`
		Bearer bool       `json:"bearer"`
		Device *deviceReq `json:"device"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Text) > 500 || len(req.Reply) > 100 {
		writeErr(w, http.StatusBadRequest, "That answer is too long.")
		return
	}
	r, ok := askDevice(w, r, req.Device, req.Bearer)
	if !ok {
		return
	}
	number, ok := s.appSignupNumber(w, req.Phone)
	if !ok {
		return
	}
	ctx := r.Context()
	addr := appAddr(number)
	st, err := s.signupLoad(ctx, addr)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if st == nil || st.Proof == "" || subtle.ConstantTimeCompare([]byte(st.Proof), []byte(digest(req.Token))) != 1 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "This sign-up has ended. Start again.", "ended": true})
		return
	}
	// Counted once the token is known: nobody else uses up the person's turns.
	if !s.limiter("app-signup-turn:"+req.Token, time.Minute, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many answers at once; wait a little")
		return
	}
	ch := &appChannel{}
	sc := &signupChat{ch: ch, addr: addr, lang: lang.EN}
	if err := s.signupStep(ctx, sc, st, req.Text, req.Reply); err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"messages": ch.said}
	after, err := s.signupLoad(ctx, addr)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if after != nil {
		// Still answering: the number's proof lasts as long as the sign-up.
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_signup_put($1, 'app', 'proven', '{}', $2::interval)`,
			signupKey("app-proven:"+number), (chatSignupTTL + 10*time.Minute).String()); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Ended: the business made (this request made it: signupMake says
	// which, and whose), else cancelled, declined or refused.
	if sc.madeTenant == uuid.Nil || sc.madeOwner == uuid.Nil {
		out["ended"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	user := sc.madeOwner
	sess, err := s.createSession(r, user, sc.madeTenant, "chat")
	if err != nil {
		// Made, but not signed in here: signing in with WhatsApp works.
		s.Logger.Warn("app sign-up: signing the new owner in", "err", err)
		out["done"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["done"] = true
	out["tenant_id"] = sess.tenant
	out["user_id"] = user
	s.deliverSession(w, req.Bearer, sess.token, out)
	writeJSON(w, http.StatusOK, out)
}

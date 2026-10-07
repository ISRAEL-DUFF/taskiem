package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Self-serve signup and onboarding (Phase 4, P4-2; docs/onboarding.md).
//
// Signup is off unless the operator turns it on (TASKIEM_ALLOW_SIGNUP).
// When on, it takes JSON only and refuses: more than a few signups a day
// from one address (counted in the database, so every replica shares the
// count), throwaway email domains (a built-in list plus the operator's),
// names that read as links or addresses (they appear in invitation
// emails), and anything in the hidden form field a person never sees. The
// new owner is signed in at once, the trial starts (billing on), and a
// link to confirm the email address is sent. Until it is confirmed, the
// tenant cannot invite people or make API keys: the two ways a throwaway
// account reaches people or automation beyond its own browser. Building,
// publishing and running are open from the start, so the first run does
// not wait for the mailbox.
//
// The onboarding checklist is derived from what the tenant has done, not
// from flags; only the time of the first successful run is stored, once,
// to measure gate G4 (taskiem_onboarding_first_run_seconds).

const (
	verifyTTL           = 24 * time.Hour
	verifyLinkBadMsg    = "this confirmation link is not valid: it may have expired, been used, or belong to another account. Ask for a new one from Get started."
	defaultSignupPerDay = 5
	unverifiedMsg       = "confirm your email address first: open the link we sent you, or ask for a new one from Get started"
)

// disposableDomains are throwaway mailbox services. Signups from them, and
// their subdomains, are refused; operators add more with
// TASKIEM_SIGNUP_BLOCKED_DOMAINS.
var disposableDomains = map[string]bool{
	"10minutemail.com": true, "10minutemail.net": true, "1secmail.com": true, "1secmail.net": true, "1secmail.org": true,
	"burnermail.io": true, "crazymailing.com": true, "discard.email": true, "dispostable.com": true, "emailfake.com": true,
	"emailondeck.com": true, "fakeinbox.com": true, "getairmail.com": true, "getnada.com": true, "grr.la": true,
	"guerrillamail.biz": true, "guerrillamail.com": true, "guerrillamail.de": true, "guerrillamail.net": true, "guerrillamail.org": true,
	"guerrillamailblock.com": true, "harakirimail.com": true, "inboxkitten.com": true, "mail.gw": true, "mail.tm": true,
	"mailcatch.com": true, "maildrop.cc": true, "mailinator.com": true, "mailinator.net": true, "mailnesia.com": true,
	"mailpoof.com": true, "minuteinbox.com": true, "mintemail.com": true, "moakt.com": true, "mohmal.com": true,
	"mytemp.email": true, "nada.email": true, "sharklasers.com": true, "spambox.us": true, "spamgourmet.com": true,
	"tempail.com": true, "tempinbox.com": true, "tempmail.com": true, "tempmail.net": true, "temp-mail.io": true,
	"temp-mail.org": true, "tempr.email": true, "throwawaymail.com": true, "tmpmail.net": true, "tmpmail.org": true,
	"trashmail.com": true, "trashmail.de": true, "trashmail.net": true, "yopmail.com": true, "yopmail.fr": true, "yopmail.net": true,
}

// blockedDomain reports whether signups from domain are refused: a
// throwaway service, the operator's list, or a name that cannot receive
// mail.
func (s *Server) blockedDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if strings.HasSuffix(domain, ".invalid") || strings.HasSuffix(domain, ".localhost") || domain == "localhost" {
		return true
	}
	for d := domain; d != ""; {
		if disposableDomains[d] {
			return true
		}
		for _, b := range s.SignupBlockedDomains {
			if strings.EqualFold(strings.TrimSpace(b), d) {
				return true
			}
		}
		_, rest, ok := strings.Cut(d, ".")
		if !ok {
			break
		}
		d = rest
	}
	return false
}

// signupEmail checks an address: one plain address (no display name), at
// most 254 characters, a domain with a dot.
func signupEmail(raw string) (email, domain string, ok bool) {
	email = strings.TrimSpace(raw)
	if len(email) > 254 {
		return "", "", false
	}
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || a.Name != "" {
		return "", "", false
	}
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" || len(local) > 64 || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", "", false
	}
	return email, domain, true
}

// plainName checks a tenant or person name: printable, at most max
// characters, and not a link or an address. Names appear in invitation
// emails, so a name like "Verify your bank at evil.example" is refused.
func plainName(raw string, minLen, maxLen int) (string, bool) {
	n := strings.Join(strings.Fields(raw), " ")
	if c := utf8.RuneCountInString(n); c < minLen || c > maxLen {
		return "", false
	}
	for _, r := range n {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	l := strings.ToLower(n)
	for _, bad := range []string{"://", "www.", "@", "<", ">", "http:", "https:"} {
		if strings.Contains(l, bad) {
			return "", false
		}
	}
	// A dotted word that reads as a domain (evil.example, bit.ly/x).
	for _, w := range strings.Fields(l) {
		if i := strings.LastIndexByte(w, '.'); i > 0 && i < len(w)-2 && !strings.ContainsAny(w[i+1:], "0123456789") {
			return "", false
		}
	}
	return n, true
}

type signupReq struct {
	Tenant   string `json:"tenant"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	// Website is the form's hidden field: people leave it empty.
	Website string `json:"website,omitempty"`
}

// signupOptions tells the web app whether to offer signup.
func (s *Server) signupOptions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.AllowSignup})
}

// signup creates a tenant with its owner, a default workspace, and the dev
// and prod environments, and signs the owner in.
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	refuse := func(outcome string, status int, msg string) {
		telemetry.Signups.WithLabelValues(outcome).Inc()
		writeErr(w, status, msg)
	}
	ip := clientIP(r)
	if !s.loginLimiter("login:" + ip).Allow() {
		refuse("rate_limited", http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req signupReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Website != "" {
		refuse("honeypot", http.StatusBadRequest, "signup refused")
		return
	}
	email, domain, ok := signupEmail(req.Email)
	if !ok {
		refuse("invalid", http.StatusBadRequest, "a valid email address is required")
		return
	}
	if s.blockedDomain(domain) {
		refuse("blocked_domain", http.StatusBadRequest, "use a work or personal email address you keep: throwaway mailboxes cannot sign up")
		return
	}
	tenantName, ok := plainName(req.Tenant, 2, 80)
	if !ok {
		refuse("invalid", http.StatusBadRequest, "the organisation name needs 2 to 80 characters, and no links or email addresses")
		return
	}
	userName := ""
	if strings.TrimSpace(req.Name) != "" {
		if userName, ok = plainName(req.Name, 1, 80); !ok {
			refuse("invalid", http.StatusBadRequest, "your name needs at most 80 characters, and no links or email addresses")
			return
		}
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > 1024 || strings.EqualFold(req.Password, email) {
		refuse("invalid", http.StatusBadRequest, "a password needs 12 to 1024 characters and must not be your email")
		return
	}
	if limit := s.SignupPerAddress; limit >= 0 {
		if limit == 0 {
			limit = defaultSignupPerDay
		}
		addr := sha256.Sum256([]byte(ip))
		var admitted bool
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_signup_admit($1, '24 hours'::interval, $2)`, addr[:], limit).Scan(&admitted); err != nil {
			s.fail(w, r, err)
			return
		}
		if !admitted {
			refuse("rate_limited", http.StatusTooManyRequests, "too many signups from this address today: try again tomorrow, or ask to be invited")
			return
		}
	}
	var token string
	mailOn := s.mailOn()
	tenant, user, err := createTenant(r.Context(), s.Store.Pool, tenantName, email, userName, req.Password, func(tx pgx.Tx, tenant, user uuid.UUID) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_onboarding (tenant_id, source, signed_up_by) VALUES ($1, 'signup', $2)`, tenant, user); err != nil {
			return err
		}
		if !mailOn {
			return nil
		}
		var err error
		token, err = issueVerification(r.Context(), tx, tenant, user, email)
		return err
	})
	if errors.Is(err, errConflict) {
		refuse("exists", http.StatusConflict, "that email already has an account: sign in, or reset its password")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	telemetry.Signups.WithLabelValues("created").Inc()
	// With billing on, a new tenant starts its trial (docs/billing.md).
	if err := s.Billing.Ensure(r.Context(), tenant, "user:"+user.String()); err != nil {
		s.fail(w, r, err)
		return
	}
	if token != "" {
		s.background(r.Context(), "email verification", func(ctx context.Context) { s.sendVerification(ctx, email, token, ip) })
	} else {
		s.Logger.Warn("signup without email verification: this deployment cannot send email (TASKIEM_SMTP_URL, TASKIEM_ALERT_FROM, TASKIEM_PUBLIC_URL)")
	}
	out := map[string]any{"tenant_id": tenant, "user_id": user, "verification_sent": token != ""}
	// Signed in at once: the first run should not wait for anything.
	if sess, err := s.createSession(r, user, tenant, "password"); err == nil {
		s.setSessionCookie(w, sess.token)
		out["token"] = sess.token
	} else {
		s.Logger.Warn("signup: not signed in", "err", err)
	}
	writeJSON(w, http.StatusCreated, out)
}

// issueVerification stores a new confirmation link for user's email in
// tenant, replacing their unused ones, and returns its token.
func issueVerification(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, email string) (string, error) {
	token, selector, secretHash := newResetToken()
	if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = $1 AND used_at IS NULL`, user); err != nil {
		return "", err
	}
	_, err := tx.Exec(ctx, `INSERT INTO email_verifications (selector, secret_hash, tenant_id, user_id, email, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + $6::interval)`, selector, secretHash, tenant, user, email, verifyTTL.String())
	return token, err
}

func (s *Server) sendVerification(ctx context.Context, email, token, ip string) {
	// The token is in the fragment, which browsers send to no server.
	link := strings.TrimRight(s.PublicURL, "/") + "/verify-email#token=" + token
	body := fmt.Sprintf(`Welcome to Taskiem.

Confirm that %s is your email address by opening this link. It works once, for 24 hours, while you are signed in:

%s

Until you confirm it, you can build and run workflows, but not invite people or make API keys.

If you did not sign up, ignore this email: nothing more will be sent.
The signup came from the address %s.
`, email, link, ip)
	if err := s.sendMail(ctx, email, "Confirm your email for Taskiem", body); err != nil {
		s.Logger.Error("verification email not sent", "err", err)
	}
}

// verifyEmail confirms the signed-in person's email with a link's token.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "emails belong to people, not API keys")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	selector, secretHash, ok := parseResetToken(req.Token)
	if !ok {
		writeErr(w, http.StatusBadRequest, verifyLinkBadMsg)
		return
	}
	if !s.limiter("verify-token:"+hex.EncodeToString(selector), 6*time.Second, 10).Allow() || !s.limiter("verify-user:"+p.UserID.String(), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	verified := false
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var user uuid.UUID
		var hash []byte
		var email string
		var usable bool
		err := tx.QueryRow(ctx, `SELECT user_id, secret_hash, email, used_at IS NULL AND expires_at > now() AND failures < 5
			FROM email_verifications WHERE selector = $1 FOR UPDATE`, selector).Scan(&user, &hash, &email, &usable)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !usable {
			return nil
		}
		// A wrong secret, or someone else's link, counts toward the lock.
		if subtle.ConstantTimeCompare(hash, secretHash) != 1 || user != p.UserID {
			_, err := tx.Exec(ctx, `UPDATE email_verifications SET failures = failures + 1 WHERE selector = $1`, selector)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE email_verifications SET used_at = now() WHERE selector = $1`, selector); err != nil {
			return err
		}
		// Only the address the link was sent to: an email changed since
		// is not confirmed by it.
		tag, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1 AND lower(email) = lower($2)`, user, email)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		verified = true
		return auditTx(r, tx, "user.email.verify", user.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !verified {
		writeErr(w, http.StatusBadRequest, verifyLinkBadMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email_verified": true})
}

// resendVerification sends the signed-in person a new confirmation link.
func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		writeErr(w, http.StatusForbidden, "emails belong to people, not API keys")
		return
	}
	if !s.mailOn() {
		writeErr(w, http.StatusServiceUnavailable, "this deployment cannot send email; ask your operator")
		return
	}
	var email string
	var verified bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).Scan(&email, &verified)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if verified {
		writeJSON(w, http.StatusOK, map[string]any{"email_verified": true})
		return
	}
	// Three links, then one every twenty minutes.
	if !s.limiter("verify-resend:"+p.UserID.String(), 20*time.Minute, 3).Allow() {
		writeErr(w, http.StatusTooManyRequests, "a link was sent a moment ago: check your inbox and spam folder, or try again in a while")
		return
	}
	var token string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		token, err = issueVerification(r.Context(), tx, p.TenantID, p.UserID, email)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	ip := clientIP(r)
	s.background(r.Context(), "email verification", func(ctx context.Context) { s.sendVerification(ctx, email, token, ip) })
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "A new link is on its way to " + email + ". It works once, for 24 hours."})
}

// needVerified refuses, in a tenant that signed itself up, what reaches
// people or automation beyond the browser (inviting members, API keys)
// until the person who signed up has confirmed their email. Deployments
// that cannot send email cannot confirm one, so nothing is held back there.
func (s *Server) needVerified(r *http.Request) error {
	if !s.mailOn() {
		return nil
	}
	var pending bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM tenant_onboarding o JOIN users u ON u.id = o.signed_up_by
			WHERE o.source = 'signup' AND u.email_verified_at IS NULL)`).Scan(&pending)
	}); err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("%w: %s", errForbidden, unverifiedMsg)
	}
	return nil
}

// checklistItem is one step of getting started; the web app words it.
type checklistItem struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

// getOnboarding is the tenant's getting-started checklist, from what it
// has done, and how long its first successful run took.
func (s *Server) getOnboarding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var (
		source                            *string
		signedUpAt, firstRunAt, dismissed *time.Time
		verified, hasPerson               bool
		conn, wf, published, run, invited bool
	)
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		err := tx.QueryRow(ctx, `SELECT source, signed_up_at, first_run_at, dismissed_at FROM tenant_onboarding WHERE tenant_id = $1`, p.TenantID).
			Scan(&source, &signedUpAt, &firstRunAt, &dismissed)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if p.UserID != uuid.Nil && p.KeyID == uuid.Nil {
			hasPerson = true
			if err := tx.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).Scan(&verified); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `SELECT
			EXISTS (SELECT 1 FROM connections),
			EXISTS (SELECT 1 FROM workflows),
			EXISTS (SELECT 1 FROM workflow_versions WHERE state = 'published'),
			EXISTS (SELECT 1 FROM runs WHERE status = 'completed'),
			(SELECT count(DISTINCT user_id) FROM memberships) > 1 OR EXISTS (SELECT 1 FROM member_invitations)`).
			Scan(&conn, &wf, &published, &run, &invited)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	run = run || firstRunAt != nil
	var items []checklistItem
	if hasPerson {
		items = append(items, checklistItem{"verify_email", verified})
	}
	items = append(items, checklistItem{"connection", conn}, checklistItem{"workflow", wf}, checklistItem{"publish", published},
		checklistItem{"first_run", run}, checklistItem{"invite", invited})
	done := 0
	for _, it := range items {
		if it.Done {
			done++
		}
	}
	out := map[string]any{"items": items, "done": done, "total": len(items), "complete": done == len(items),
		"dismissed": dismissed != nil, "self_serve": source != nil && *source == "signup",
		"email_verified": verified, "can_send_email": s.mailOn(), "docs_url": s.DocsURL,
		"signed_up_at": nil, "first_run_at": nil, "seconds_to_first_run": nil}
	if source != nil && *source == "signup" {
		out["signed_up_at"] = signedUpAt
		if firstRunAt != nil {
			out["first_run_at"] = firstRunAt
			out["seconds_to_first_run"] = int64(firstRunAt.Sub(*signedUpAt).Seconds())
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// dismissOnboarding hides the checklist for the tenant; it stays at
// /start.
func (s *Server) dismissOnboarding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO tenant_onboarding (tenant_id, source, dismissed_at) VALUES ($1, 'existing', now())
			ON CONFLICT (tenant_id) DO UPDATE SET dismissed_at = now()`, p.TenantID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

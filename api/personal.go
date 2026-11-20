package api

// Personal sessions (docs/spec/taskiem-for-everyone.md §5.2, §5.4, §6;
// migration 00448). A person's session may be in no business: their own
// side ("You"). It reaches only the person's own routes; a business session
// reaches them too. Switching into a business checks that business's rules
// first: its single sign-on, passkeys for whom it holds to them, and a
// second proof for an account in a business that signed in by a code to
// its phone on a device that has not proved itself another way.

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
)

// personalRoutes are all a personal session may reach: the person's own
// account, switching, starting something (stream B's /v1/me/start), their
// phone, second proofs, invitations and signing out.
var personalRoutes = regexp.MustCompile(`^(GET /v1/me|POST /v1/me/terms|GET /v1/me/businesses|POST /v1/me/switch|POST /v1/me/start|` +
	`POST /v1/me/phone/(start|verify)|POST /v1/auth/second-proof/(email/start|email/verify|pin|password|passkey/options|passkey)|` +
	`GET /v1/me/invitations|POST /v1/me/invitations/[^/]+/(accept|decline)|POST /v1/auth/logout|` +
	// A person's chats with businesses (customer_door.go, stage 2).
	`(GET|POST) /v1/me/chats|(GET|PUT) /v1/me/chats/[^/]+|GET /v1/me/chats/[^/]+/(messages|files/[^/]+|shop|services|services/[^/]+/times|orders|orders/[^/]+/pay)|` +
	`POST /v1/me/chats/[^/]+/(messages|files|read|typing|block|report|orders|bookings)|DELETE /v1/me/chats/[^/]+/block|` +
	// The person's own devices for pushes on You (push.go, 00460).
	`GET /v1/me/push(/key)?|(PUT|DELETE) /v1/me/push|(PATCH|DELETE) /v1/me/push/[^/]+|` +
	// Orders on You and saved addresses (person_orders.go, person_addresses.go, stage 3).
	`GET /v1/me/orders(/[^/]+/[^/]+(/pay)?)?|(GET|POST) /v1/me/addresses|(PUT|DELETE) /v1/me/addresses/[^/]+|` +
	// The person's own saved things (saved.go, saved objects S0).
	`(GET|POST) /v1/me/saved|GET /v1/me/saved/resolve|POST /v1/me/saved/step-up/options|(GET|PUT|DELETE) /v1/me/saved/[^/]+|POST /v1/me/saved/[^/]+/reveal|GET /v1/me/banks|POST /v1/me/saved/read|(GET|POST) /v1/me/saved-kinds|(PUT|DELETE) /v1/me/saved-kinds/[^/]+|` +
	// The person's account: deleting it, their photo, pushes, privacy (person_account.go).
	`POST /v1/me/delete(/start|/passkey/options)?|(GET|PUT|DELETE) /v1/me/photo|(GET|PUT) /v1/me/notifications|GET /v1/me/privacy|` +
	// Finding people: privacy towards people, contacts, the person's link,
	// presence (people.go, stage 5a).
	`(GET|PUT) /v1/me/privacy/people|(GET|POST) /v1/me/contacts|(PUT|DELETE) /v1/me/contacts/[^/]+|GET /v1/me/contacts/key|POST /v1/me/contacts/match|` +
	`(GET|POST) /v1/me/link|GET /v1/me/link/qr|POST /v1/me/presence|` +

	`GET /v1/me/storefront-orders|POST /v1/me/storefront-orders/confirm)$`)

// discoverPersonRoutes are Discover's routes for a person (discover.go,
// stage 3).
var discoverPersonRoutes = regexp.MustCompile(`^(POST /v1/discover/search|GET /v1/discover/[^/]+|POST /v1/discover/[^/]+/report)$`)

// peoplePersonRoutes are finding and seeing other people (people.go,
// stage 5a).
var peoplePersonRoutes = regexp.MustCompile(`^(POST /v1/people/lookup|GET /v1/people/links/[^/]+|GET /v1/people/[^/]+(/photo)?)$`)

func personalAllowed(method, path string) bool {
	route := method + " " + strings.TrimSuffix(path, "/")
	return personalRoutes.MatchString(route) || discoverPersonRoutes.MatchString(route) || peoplePersonRoutes.MatchString(route) || peopleChatPersonRoutes.MatchString(route) || groupPersonRoutes.MatchString(route) || basketPersonRoutes.MatchString(route) || safetyPersonRoutes.MatchString(route)
}

// deviceCookie names a browser, so a second proof made there holds for it
// (§5.4). It carries nothing but a random name, kept as a hash.
const deviceCookie = "taskiem_device"

// deviceHashOf is the device a request comes from, as user_devices keeps it:
// the phone app's install, else the browser's device cookie; nil for a
// client that is neither (an API or CLI client), which is never proved.
func deviceHashOf(r *http.Request, app uuid.UUID) []byte {
	if app != uuid.Nil {
		h := sha256.Sum256([]byte("app:" + app.String()))
		return h[:]
	}
	if c, err := r.Cookie(deviceCookie); err == nil && len(c.Value) >= 32 && len(c.Value) <= 64 {
		h := sha256.Sum256([]byte("web:" + c.Value))
		return h[:]
	}
	return nil
}

// signinDeviceKey is the device a sign-in comes from; a browser signing in
// for a cookie session is given a device cookie when it has none.
func (s *Server) signinDeviceKey(w http.ResponseWriter, r *http.Request, bearer bool) []byte {
	if d := deviceOf(r.Context()); d != nil {
		return deviceHashOf(r, d.ID)
	}
	if bearer {
		return nil
	}
	if k := deviceHashOf(r, uuid.Nil); k != nil {
		return k
	}
	v := newToken()
	//nolint:gosec // Secure is configuration: off only for plain-HTTP local development.
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: v, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: 400 * 24 * 60 * 60})
	h := sha256.Sum256([]byte("web:" + v))
	return h[:]
}

// sessionDeviceKey is the device the request's session is on.
func (s *Server) sessionDeviceKey(r *http.Request, p *Principal) []byte {
	if p.Device {
		var id *uuid.UUID
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_auth_session_device($1)`, hashToken(sessionToken(r))).Scan(&id); err == nil && id != nil {
			return deviceHashOf(r, *id)
		}
		return nil
	}
	if p.viaCookie {
		return deviceHashOf(r, uuid.Nil)
	}
	return nil
}

// startPersonalSession signs a person in to their own side and adds the
// session to out (a cookie, or a token for a bearer client).
func (s *Server) startPersonalSession(w http.ResponseWriter, r *http.Request, user uuid.UUID, method string, bearer bool, out map[string]any) error {
	ctx := r.Context()
	tok := newToken()
	ttl := sessionTTL
	var devID *uuid.UUID
	var devName, devPlatform *string
	if d := deviceOf(ctx); d != nil {
		ttl, devID, devName, devPlatform = deviceTTL, &d.ID, &d.Name, &d.Platform
	}
	var ok bool
	err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_personal_start($1, $2, $3, $4::interval, $5, $6, $7, $8)`,
		hashToken(tok), user, method, ttl.String(), clientIP(r), devID, devName, devPlatform).Scan(&ok)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		return errPhoneBusy
	}
	if err != nil {
		return err
	}
	if !ok {
		return errAccountOff
	}
	tenants, err := s.userTenants(ctx, user)
	if err != nil {
		return err
	}
	out["user_id"], out["tenant_id"], out["personal"] = user, nil, true
	out["tenants"] = nonNil(tenants)
	if len(tenants) == 0 {
		// Kept for clients that know invitee sessions (invitations.go).
		var invited []uuid.UUID
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_invited_tenants($1)`, user).Scan(&invited); err == nil && len(invited) > 0 {
			out["invitations_only"] = true
		}
	}
	s.deliverSession(w, bearer, tok, out)
	return nil
}

var errAccountOff = errors.New("this account is not active")

// --- the business's rules, on switching ---

// secondProofWays are the second proofs a person could give (§5.4): a
// passkey, a link to their real email (confirmed or not), the chat PIN,
// or their password (where it is usable: a business of theirs lets them
// sign in with it, and their email is confirmed where email can be sent,
// as proveFactor asks).
type secondProofWays struct {
	Passkey  bool `json:"passkey"`
	Email    bool `json:"email"`
	Pin      bool `json:"pin"`
	Password bool `json:"password"`
}

func (w secondProofWays) any() bool { return w.Passkey || w.Email || w.Pin || w.Password }

func (s *Server) secondProofsOf(ctx context.Context, user uuid.UUID) (secondProofWays, error) {
	var out secondProofWays
	var password, verified bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT passkey, email, pin, password, email_verified FROM taskiem_auth_second_proofs($1)`, user).
		Scan(&out.Passkey, &out.Email, &out.Pin, &password, &verified); err != nil {
		return out, err
	}
	if password && (verified || !s.mailOn()) {
		usable, err := s.passwordUsable(ctx, user)
		if err != nil {
			return out, err
		}
		out.Password = usable
	}
	return out, nil
}

// codeSession is whether a session was made by a code to the person's
// phone or chat: a code proves only that someone holds the number today.
func codeSession(p *Principal) bool { return p.AuthMethod == "phone" || p.AuthMethod == "chat" }

// proofState is whether the session's person must give a second proof
// before entering a business: a session made by a code to their phone or
// chat, on a device without one. unprovable is such a session of someone
// with no way to give one (a chat-born owner with no email, passkey, PIN
// or password): they are let in, and it is recorded, rather than locked
// out of their own business.
type proofState struct {
	needed, unprovable, deviceProved bool
	ways                             secondProofWays
}

func (s *Server) proofStateOf(r *http.Request, p *Principal) (proofState, error) {
	ctx := r.Context()
	var st proofState
	ways, err := s.secondProofsOf(ctx, p.UserID)
	if err != nil {
		return st, err
	}
	st.ways = ways
	if key := s.sessionDeviceKey(r, p); key != nil {
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_device_proved($1, $2)`, p.UserID, key).Scan(&st.deviceProved); err != nil {
			return st, err
		}
	}
	if codeSession(p) && !st.deviceProved {
		st.needed, st.unprovable = ways.any(), !ways.any()
	}
	return st, nil
}

// secondProofRequiredOut is the 401 that asks for a second proof first.
func secondProofRequiredOut(w http.ResponseWriter, ways secondProofWays) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "this is a new device: prove it is you another way first (a passkey, a link to your email, your chat PIN or your password)",
		"second_proof_required": true, "ways": ways})
}

// refuseUnproved answers 401 second_proof_required, and reports true, for
// a session made by a code on a device without a second proof, of someone
// who could give one: such a session adds no way of signing in (a passkey,
// an authenticator, a password, an email, a chat or a PIN), or a holder of
// a reissued number would make the account theirs (§5.4). The session's
// method and device carry over switching and Start something
// (taskiem_auth_session_move), so a business session made from one is
// held the same.
func (s *Server) refuseUnproved(w http.ResponseWriter, r *http.Request) bool {
	p := principalFrom(r.Context())
	if p == nil || !codeSession(p) {
		return false
	}
	st, err := s.proofStateOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return true
	}
	if st.needed {
		secondProofRequiredOut(w, st.ways)
		return true
	}
	return false
}

// businessLock is what stands between the session and a business of the
// person's: "" when nothing does, else sso_required, passkey_required or
// second_proof_required.
func (s *Server) businessLock(ctx context.Context, p *Principal, tenant uuid.UUID, proof proofState) (string, error) {
	if !p.Personal && tenant == p.TenantID {
		return "", nil
	}
	if p.AuthMethod == "sso" {
		return "sso_required", nil // single sign-on vouches for one business (switch.go)
	}
	var enforced bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_sso_enforced($1, $2)`, p.UserID, tenant).Scan(&enforced); err != nil {
		return "", err
	}
	if enforced {
		return "sso_required", nil
	}
	// Held to passkeys there, with one: only a passkey session enters (the
	// same test a password sign-in meets, passkey_rule.go). Held without
	// one, the person enters to add one (EnrolOnly), as on signing in.
	if p.AuthMethod != "passkey" {
		if _, err := s.passwordSigninTenant(ctx, p.UserID, tenant); errors.Is(err, errPasskeyRequired) {
			return "passkey_required", nil
		} else if err != nil {
			return "", err
		}
	}
	if proof.needed {
		return "second_proof_required", nil
	}
	return "", nil
}

// businessesWithLocks is the person's businesses, each with what locks it
// for this session (§6's switcher).
func (s *Server) businessesWithLocks(r *http.Request, p *Principal, proof proofState) ([]map[string]any, error) {
	ts, err := s.tenantsOf(r.Context(), p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		b := map[string]any{"id": t.ID, "name": t.Name, "kind": t.Kind, "locked": false, "lock": nil}
		if !p.Personal && t.ID == p.TenantID {
			b["current"] = true
		} else {
			lock, err := s.businessLock(r.Context(), p, t.ID, proof)
			if err != nil {
				return nil, err
			}
			if lock != "" {
				b["locked"], b["lock"] = true, lock
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// personalMe is GET /v1/me for a personal session.
func (s *Server) personalMe(w http.ResponseWriter, r *http.Request, p *Principal) {
	ctx := r.Context()
	var email, name string
	var verified, chatSignin, passkeyOnly bool
	if err := db.InPersonTx(ctx, s.Store.Pool, p.UserID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email, name, email_verified_at IS NOT NULL, chat_signin, passkey_only FROM users WHERE id = $1`, p.UserID).
			Scan(&email, &name, &verified, &chatSignin, &passkeyOnly)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	user := map[string]any{"id": p.UserID, "email": email, "name": name, "email_verified": verified, "email_placeholder": emailPlaceholder(email),
		"chat_signin": chatSignin, "passkey_only": passkeyOnly, "phone": s.phoneOf(ctx, p.UserID), "photo": photoOut(s.photoOf(ctx, p.UserID))["photo"]}
	proof, err := s.proofStateOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	businesses, err := s.businessesWithLocks(r, p, proof)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"tenant_id": nil, "personal": true, "roles": []string{}, "permissions": []string{}, "auth_method": p.AuthMethod,
		"user": user, "businesses": businesses, "second_proof": secondProofOut(proof, len(businesses) > 0)}
	if len(businesses) == 0 {
		var invited []uuid.UUID
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_invited_tenants($1)`, p.UserID).Scan(&invited); err == nil && len(invited) > 0 {
			out["invitations_only"] = true
		}
	}
	if t := s.meTerms(ctx, p.UserID); t != nil {
		out["terms"] = t
	}
	writeJSON(w, http.StatusOK, out)
}

// secondProofOut is what /v1/me says of second proofs: whether one is
// needed to enter the person's businesses from this device, whether the
// device has one, and which the person could give.
func secondProofOut(st proofState, inBusiness bool) map[string]any {
	return map[string]any{"required": st.needed && inBusiness, "device_proved": st.deviceProved, "ways": st.ways}
}

// phoneOf is the person's phone, "" when they have none.
func (s *Server) phoneOf(ctx context.Context, user uuid.UUID) any {
	var n *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, user).Scan(&n); err != nil || n == nil {
		return nil
	}
	return *n
}

// enterJoined moves a personal session into a business the person has just
// joined, unless its rules ask for another sign-in ({sign_in: lock}, and
// the personal session goes on): its single sign-on, a passkey, or a
// second proof for a session made by a code on an unproved device
// ({sign_in: second_proof_required, ways}).
func (s *Server) enterJoined(w http.ResponseWriter, r *http.Request, p *Principal, tenant uuid.UUID) (map[string]any, error) {
	ctx := r.Context()
	proof, err := s.proofStateOf(r, p)
	if err != nil {
		return nil, err
	}
	lock, err := s.businessLock(ctx, p, tenant, proof)
	if err != nil {
		return nil, err
	}
	if lock == "second_proof_required" {
		// Joined, but entered only once this device is proved: the person
		// proves it, then switches (§5.4).
		return map[string]any{"sign_in": lock, "ways": proof.ways}, nil
	}
	if lock != "" {
		return map[string]any{"sign_in": lock}, nil
	}
	next := newToken()
	var moved bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_session_move($1, $2, $3, $4)`, hashToken(sessionToken(r)), hashToken(next), tenant, clientIP(r)).Scan(&moved); err != nil {
		return nil, err
	}
	if !moved {
		return map[string]any{"sign_in": "session_ended"}, nil
	}
	tenants, err := s.userTenants(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"tenants": tenants, "personal": false}
	s.deliverSession(w, !p.viaCookie, next, out)
	return out, nil
}

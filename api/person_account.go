package api

// The person's own account on You (docs/spec/taskiem-for-everyone.md §7,
// §14; migration 00467): deleting it, their photo, which pushes they get,
// and what the privacy page shows.
//
// Deleting an account needs a fresh proof: a code sent to the account's
// phone for that alone (purpose delete), the person's passkey for a
// challenge asked for it, or their password; and, for a session made by a
// code on a device with no second proof of someone who could give one,
// that second proof first (§5.4). It is refused while the person owns a
// business (they close it or hand it over first) or holds a role in one
// (its owner removes them first). Then, in one transaction: each business
// the person chatted with gets "This customer left Taskiem" in its thread
// and nothing else, audited in its chain; the phone index loses every row
// under their phone and for their app orders, which are marked so they are
// never another holder's; the businesses' orders and threads no longer
// name the person, and stay the businesses' records; every session ends;
// their phone, devices, proofs, codes, passkeys and chat bindings go; their
// own side goes (chats, files, saved addresses, devices for pushes, photo,
// settings); the account's row is emptied (no name, email, password) and
// kept as a tombstone, so the businesses' records that name who did what
// keep their keys. Audited in the platform's chain.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/ops"
)

// customerLeftWords is what a business's thread says once its customer
// deleted their account (§14): the only thing it is told.
const customerLeftWords = "This customer left Taskiem"

// deleteScope binds a passkey challenge to deleting the account.
const deleteScope = "account.delete"

// personAccountRoutes are the person's routes (personalRoutes lists them).
func (s *Server) personAccountRoutes(r chi.Router) {
	r.Post("/me/delete/start", s.deleteAccountStart)
	r.Post("/me/delete/passkey/options", s.deleteAccountPasskeyOptions)
	r.Post("/me/delete", s.deleteAccount)
	r.Get("/me/photo", s.myPhoto)
	r.Put("/me/photo", s.putMyPhoto)
	r.Delete("/me/photo", s.deleteMyPhoto)
	r.Get("/me/notifications", s.myNotifications)
	r.Put("/me/notifications", s.putMyNotifications)
	r.Get("/me/privacy", s.myPrivacy)
}

// --- deleting the account ---

// deleteBlockers answers 409 and reports true while the person owns a
// business or holds a role in one: {owns_business: true} or
// {in_business: true}, with the businesses' names.
func (s *Server) deleteBlockers(w http.ResponseWriter, r *http.Request, person uuid.UUID) bool {
	ctx := r.Context()
	ts, err := s.tenantsOf(ctx, person)
	if err != nil {
		s.fail(w, r, err)
		return true
	}
	if len(ts) == 0 {
		return false
	}
	ids := make([]uuid.UUID, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	owned := map[uuid.UUID]bool{}
	if err := db.InTenantTx(ctx, s.Store.Pool, ids, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM memberships WHERE user_id = $1 AND role = 'owner'`, person)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		for _, id := range list {
			owned[id] = true
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return true
	}
	var names, ownedNames []string
	for _, t := range ts {
		names = append(names, t.Name)
		if owned[t.ID] {
			ownedNames = append(ownedNames, t.Name)
		}
	}
	if len(ownedNames) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"owns_business": true, "businesses": ownedNames,
			"error": "You own a business on Taskiem. Close it, or make someone else its owner and ask them to remove you, before you delete your account."})
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]any{"in_business": true, "businesses": names,
		"error": "You are part of a business on Taskiem. Ask its owner to remove you, then delete your account."})
	return true
}

// deleteAccountStart is POST /v1/me/delete/start ({via?}): a code to the
// account's phone, good only for deleting it. 409 {no_phone} for an
// account with no phone (it proves itself with its passkey or password).
func (s *Server) deleteAccountStart(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Via string `json:"via"`
	}
	if err := decodeOptional(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Via != "" && req.Via != "whatsapp" && req.Via != "sms" {
		writeErr(w, http.StatusBadRequest, "via is whatsapp or sms")
		return
	}
	if s.deleteBlockers(w, r, person) {
		return
	}
	number, _ := s.phoneOf(r.Context(), person).(string)
	if number == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"no_phone": true,
			"error": "Your account has no phone to send a code to. Confirm it is you with your passkey or your password."})
		return
	}
	sent := s.issuePhoneCode(w, r, number, "delete", &person, req.Via)
	if sent == "" {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent_via": sent, "expires_in": int(phoneCodeTTL.Seconds())})
}

// deleteAccountPasskeyOptions is POST /v1/me/delete/passkey/options: a
// challenge for one of the person's passkeys, good only for deleting the
// account.
func (s *Server) deleteAccountPasskeyOptions(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysOn(w) {
		return
	}
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.limiter("challenge:"+limitIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	ctx := r.Context()
	var ids [][]byte
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_passkey_ids($1)`, person).Scan(&ids); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "You have no passkey: use a code to your phone, or your password.", "no_passkey": true})
		return
	}
	ch, err := s.issueChallenge(ctx, "step_up", &person, deleteScope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allow := make([]any, len(ids))
	for i, id := range ids {
		allow[i] = map[string]any{"type": "public-key", "id": b64.EncodeToString(id)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge": b64.EncodeToString(ch), "rpId": s.WebAuthn.RPID, "allowCredentials": allow, "userVerification": "required", "timeout": 60000}})
}

// deleteAccount is POST /v1/me/delete ({confirm: true, code? | credential?
// | password?}): the account deleted at once, with a fresh proof (a code
// from /v1/me/delete/start, a passkey for a challenge from
// /v1/me/delete/passkey/options, or the password). 200 {deleted: true,
// businesses_told}; the session's cookie is cleared, and every session
// ends.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Confirm    bool            `json:"confirm"`
		Code       string          `json:"code"`
		Password   string          `json:"password"`
		Credential *credentialJSON `json:"credential"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "Confirm that you want your account deleted (confirm: true). It cannot be undone.")
		return
	}
	if s.deleteBlockers(w, r, person) || s.refuseUnproved(w, r) {
		return
	}
	if !s.limiter("account-delete:"+person.String(), time.Minute, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	how := ""
	switch {
	case req.Code != "":
		number, _ := s.phoneOf(ctx, person).(string)
		if number == "" {
			codeWrongOut(w)
			return
		}
		if !s.checkPhoneCode(w, r, number, "delete", &person, req.Code, nil) {
			return
		}
		how = "phone_code"
	case req.Credential != nil:
		if !s.passkeysOn(w) {
			return
		}
		if _, err := s.verifyPasskey(ctx, *req.Credential, "step_up", &person, deleteScope); err != nil {
			s.passkeyFail(w, r, err)
			return
		}
		how = "passkey"
	case req.Password != "":
		if !s.loginLimiter("reauth:" + person.String()).Allow() {
			writeErr(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		var hash *string
		if err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, person).Scan(&hash)
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if hash == nil || !checkPassword(*hash, req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "That password is not right.", "password_wrong": true})
			return
		}
		how = "password"
	default:
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Confirm it is you first: a code to your phone, your passkey or your password.",
			"reauth": []string{"phone_code", "passkey", "password"}})
		return
	}
	told, err := s.deletePerson(ctx, person, how, clientIP(r))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && (pe.Hint == "owns_business" || pe.Hint == "in_business") {
		// A role taken between the check and the deletion.
		s.deleteBlockers(w, r, person)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode}) //nolint:gosec // as in logout
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "businesses_told": told})
}

// deletedEmail is the emptied account's sign-in name: it receives no mail
// and names no one.
func deletedEmail(person uuid.UUID) string {
	h := sha256.Sum256([]byte("deleted:" + person.String()))
	return "deleted+" + hex.EncodeToString(h[:12]) + "@deleted.taskiem.invalid"
}

// deletePerson deletes the person's account in one transaction (see the
// file's comment) and answers how many businesses were told.
func (s *Server) deletePerson(ctx context.Context, person uuid.UUID, how, ip string) (int, error) {
	told := 0
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id = $1 FOR UPDATE`, person).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return errDoorNone
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM person_conversations WHERE person_id = $1`, person)
		if err != nil {
			return err
		}
		businesses, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		// The businesses the person chatted with, and the platform's chain,
		// in scope for the rest of the transaction.
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(append(businesses, ops.PlatformChain)...)); err != nil {
			return err
		}
		for _, b := range businesses {
			threads, err := tx.Query(ctx, `SELECT id FROM customer_conversations WHERE tenant_id = $1 AND line = 'app' AND person_id = $2`, b, person)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(threads, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			for _, id := range ids {
				// Taskiem's line in the business's thread: not the customer's
				// words, so not sealed under them (openCustomerText reads a
				// plain string as it is).
				if _, err := tx.Exec(ctx, `INSERT INTO customer_messages (id, tenant_id, conversation_id, mine, kind, body, sender_id, sender_name)
					VALUES ($1, $2, $3, true, 'text', to_jsonb($4::text), 'system:left', 'Taskiem')`, uuid.Must(uuid.NewV7()), b, id, customerLeftWords+"."); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE customer_conversations SET last_message_at = now(), waiting_since = NULL, wait_check_at = NULL WHERE id = $1`, id); err != nil {
					return err
				}
			}
			if len(ids) > 0 {
				told++
				if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'person', $2, 'customer.left', $2, '{}')`, b, person.String()); err != nil {
					return err
				}
			}
		}
		var removed int64
		if err := tx.QueryRow(ctx, `SELECT taskiem_person_delete($1, $2)`, person, s.PhoneIndex.IndexKey()).Scan(&removed); err != nil {
			return err
		}
		// The person's own side.
		for _, q := range []string{
			`DELETE FROM person_files WHERE person_id = $1`,
			`DELETE FROM person_messages WHERE person_id = $1`,
			`DELETE FROM person_conversations WHERE person_id = $1`,
			`DELETE FROM person_addresses WHERE person_id = $1`,
			`DELETE FROM person_photos WHERE person_id = $1`,
			`DELETE FROM person_notify WHERE person_id = $1`,
			`DELETE FROM push_outbox WHERE tenant_id IS NULL AND user_id = $1`,
			`DELETE FROM push_subscriptions WHERE tenant_id IS NULL AND user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, person); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET name = '', email = $2, password_hash = NULL, email_verified_at = NULL, chat_signin = false,
			status = 'disabled', deleted_at = now() WHERE id = $1`, person, deletedEmail(person)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'person', $2, 'account.delete', $2, jsonb_build_object('how', $3::text, 'businesses_told', $4::int, 'index_rows', $5::bigint, 'ip', $6::text))`,
			ops.PlatformChain, person.String(), how, told, removed, ip)
		return err
	})
	return told, err
}

// --- the person's photo ---

// myPhoto is GET /v1/me/photo: the person's own photo (JPEG or PNG), 404
// when they have none. Shown on their own side only: businesses are not
// shown it.
func (s *Server) myPhoto(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var ctype string
	var data []byte
	var at time.Time
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT type, data, updated_at FROM person_photos WHERE person_id = $1`, person).Scan(&ctype, &data, &at)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Last-Modified", at.UTC().Format(http.TimeFormat))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// photoOut is what /v1/me/photo answers once changed.
func photoOut(at *time.Time) map[string]any {
	if at == nil {
		return map[string]any{"photo": nil}
	}
	return map[string]any{"photo": map[string]any{"url": "/v1/me/photo?v=" + strconv.FormatInt(at.UnixMilli(), 10), "updated_at": at}}
}

// putMyPhoto is PUT /v1/me/photo (a multipart form with a file field): the
// person's photo, checked and cleaned by engine/media (a JPEG, PNG or WebP
// up to 10 MB, re-encoded at most 800 pixels wide with no metadata), kept
// in their own rows. 400 for a file that is not a photo.
func (s *Server) putMyPhoto(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.limiter("person-photo:"+person.String(), 10*time.Second, 6).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		writeErr(w, http.StatusBadRequest, "Send the photo as a form with a file field, at most 10 MB.")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "The form has no file.")
		return
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "The photo could not be read.")
		return
	}
	ctype, clean, err := media.CheckProfilePhoto(data)
	if err != nil {
		s.personFail(w, r, photoErr(err))
		return
	}
	var at time.Time
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `INSERT INTO person_photos (person_id, type, data) VALUES ($1, $2, $3)
			ON CONFLICT (person_id) DO UPDATE SET type = EXCLUDED.type, data = EXCLUDED.data, updated_at = now() RETURNING updated_at`,
			person, ctype, clean).Scan(&at)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, photoOut(&at))
}

// photoErr says why a photo was refused, in plain words.
func photoErr(err error) error {
	if errors.Is(err, media.ErrImage) {
		msg := err.Error()
		for range 2 {
			if i := strings.Index(msg, ": "); i >= 0 {
				msg = msg[i+2:]
			}
		}
		return customerSendErr{http.StatusBadRequest, "This photo cannot be used: " + msg + "."}
	}
	return err
}

// deleteMyPhoto is DELETE /v1/me/photo: no photo.
func (s *Server) deleteMyPhoto(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `DELETE FROM person_photos WHERE person_id = $1`, person)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// photoOf is when the person's photo was last changed, nil without one.
func (s *Server) photoOf(ctx context.Context, person uuid.UUID) *time.Time {
	var at time.Time
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT updated_at FROM person_photos WHERE person_id = $1`, person).Scan(&at)
	})
	if err != nil {
		return nil
	}
	return &at
}

// --- which pushes the person gets ---

// notifySettings are the kinds of push the person gets on You.
type notifySettings struct {
	// Messages: someone at a business wrote to them.
	Messages bool `json:"messages"`
	// Updates: a business's automatic words (an order's or a booking's
	// news).
	Updates bool `json:"updates"`
	// Offers: a business's offers (only from those they said yes to).
	Offers bool `json:"offers"`
}

func notifyTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (notifySettings, error) {
	n := notifySettings{Messages: true, Updates: true, Offers: true}
	err := tx.QueryRow(ctx, `SELECT messages, updates, offers FROM person_notify WHERE person_id = $1`, person).Scan(&n.Messages, &n.Updates, &n.Offers)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return n, err
}

// personNotifyOn is whether the person gets pushes of kind (chatlinks'
// KindMessages, KindUpdates, KindOffers).
func (s *Server) personNotifyOn(ctx context.Context, person uuid.UUID, kind string) (bool, error) {
	var n notifySettings
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		n, err = notifyTx(ctx, tx, person)
		return err
	})
	switch kind {
	case chatlinks.KindMessages:
		return n.Messages, err
	case chatlinks.KindUpdates:
		return n.Updates, err
	case chatlinks.KindOffers:
		return n.Offers, err
	}
	return true, err
}

// myNotifications is GET /v1/me/notifications: which pushes the person
// gets on You.
func (s *Server) myNotifications(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var n notifySettings
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		n, err = notifyTx(r.Context(), tx, person)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// putMyNotifications is PUT /v1/me/notifications ({messages?, updates?,
// offers?}): the kinds given change. A kind turned off is neither pushed
// nor sent by SMS.
func (s *Server) putMyNotifications(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Messages *bool `json:"messages"`
		Updates  *bool `json:"updates"`
		Offers   *bool `json:"offers"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var n notifySettings
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `INSERT INTO person_notify (person_id, messages, updates, offers)
			VALUES ($1, COALESCE($2, true), COALESCE($3, true), COALESCE($4, true))
			ON CONFLICT (person_id) DO UPDATE SET messages = COALESCE($2, person_notify.messages), updates = COALESCE($3, person_notify.updates),
			  offers = COALESCE($4, person_notify.offers), updated_at = now()
			RETURNING messages, updates, offers`, person, req.Messages, req.Updates, req.Offers).Scan(&n.Messages, &n.Updates, &n.Offers)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// --- the privacy page ---

// privacyBusiness is a business that has the person's number.
type privacyBusiness struct {
	BusinessID uuid.UUID  `json:"business_id"`
	Business   string     `json:"business"`
	ChatID     *uuid.UUID `json:"chat_id"`
	// Orders: how many of the person's orders it has (Orders on You's).
	Orders      int        `json:"orders"`
	LastOrderAt *time.Time `json:"last_order_at"`
}

// eraseWords is how a person asks a business to erase what it holds about
// them: today's process, each business's own (§14, NDPA).
const eraseWords = "Each business keeps its own record of your orders and your messages with it, and your number. " +
	"To have a business erase what it holds about you, ask it in your chat with it. Under Nigeria's Data Protection Act it must do so, " +
	"unless the law makes it keep a record (for tax, say). If it does not answer you, use Report in your chat with it and Taskiem will follow it up. " +
	"Deleting your Taskiem account removes your side at once, but not the businesses' records."

// myPrivacy is GET /v1/me/privacy: what businesses see of the person
// (their name and phone, not their photo), which businesses have their
// number (through a chat with them or an order), and how to ask a
// business to erase what it holds.
func (s *Server) myPrivacy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var name string
	byID := map[uuid.UUID]*privacyBusiness{}
	var order []uuid.UUID
	add := func(id uuid.UUID, business string) *privacyBusiness {
		if b, ok := byID[id]; ok {
			return b
		}
		b := &privacyBusiness{BusinessID: id, Business: business}
		byID[id] = b
		order = append(order, id)
		return b
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, person).Scan(&name); err != nil {
			return err
		}
		// Through orders (a nil key: those made in the app only).
		rows, err := tx.Query(ctx, `SELECT business_id, business, orders, last_order_at FROM taskiem_person_businesses($1, $2)`, person, s.PhoneIndex.IndexKey())
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var biz string
			var n int
			var last time.Time
			if err := rows.Scan(&id, &biz, &n, &last); err != nil {
				rows.Close()
				return err
			}
			b := add(id, biz)
			b.Orders, b.LastOrderAt = n, &last
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// Through a chat with them.
		rows, err = tx.Query(ctx, `SELECT tenant_id, id FROM person_conversations WHERE person_id = $1 ORDER BY last_message_at DESC NULLS LAST`, person)
		if err != nil {
			return err
		}
		chats := map[uuid.UUID]uuid.UUID{}
		var ids []uuid.UUID
		for rows.Next() {
			var b, c uuid.UUID
			if err := rows.Scan(&b, &c); err != nil {
				rows.Close()
				return err
			}
			chats[b] = c
			ids = append(ids, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) > 0 {
			names, err := tx.Query(ctx, `SELECT tenant_id, name FROM taskiem_customer_business_names($1)`, ids)
			if err != nil {
				return err
			}
			got := map[uuid.UUID]string{}
			for names.Next() {
				var id uuid.UUID
				var n string
				if err := names.Scan(&id, &n); err != nil {
					names.Close()
					return err
				}
				got[id] = n
			}
			names.Close()
			if err := names.Err(); err != nil {
				return err
			}
			for _, id := range ids {
				if n, ok := got[id]; ok {
					c := chats[id]
					add(id, n).ChatID = &c
				}
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list := make([]privacyBusiness, 0, len(order))
	for _, id := range order {
		list = append(list, *byID[id])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// What a business the person chats with or orders from sees.
		"shown_to_businesses": map[string]any{"name": name, "phone": s.phoneOf(ctx, person), "photo": false},
		"businesses":          list,
		"erase":               map[string]any{"words": eraseWords, "how": "ask_in_chat"},
	})
}

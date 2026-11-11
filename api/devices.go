package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
)

// Device sessions: staying signed in on a phone (docs/spec/mobile-app.md
// §5.2, S2, migration 00424). The phone app signs in with `bearer: true` and
// a `device` naming the phone; its session lasts 30 days without use,
// renewed by use (taskiem_auth_session_touch, at most once an hour) up to
// 180 days from signing in. Signing in again on the same phone ends its
// earlier session. The phones are listed under Your phones
// (GET /v1/me/devices) and signed out from there (DELETE /v1/me/devices/{id}).
// Everything that ends a person's sessions ends these too.

// deviceTTL is how long a device session lasts without use.
const deviceTTL = 30 * 24 * time.Hour

// deviceReq is the phone a sign-in is on, as the app describes it.
type deviceReq struct {
	// ID is the app's install, a random UUID it keeps; a new install is a
	// new phone.
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
}

type deviceKey struct{}

// askDevice checks a sign-in's device, if it names one, and carries it to
// createSession with the request. A device session is only ever a bearer
// token: a page's cookie session cannot be made to last 180 days. On a bad
// device it answers 400 and reports false.
func askDevice(w http.ResponseWriter, r *http.Request, d *deviceReq, bearer bool) (*http.Request, bool) {
	if d == nil {
		return r, true
	}
	d.Name = strings.TrimSpace(d.Name)
	switch {
	case !bearer:
		writeErr(w, http.StatusBadRequest, "a device session is a bearer token: send bearer: true")
		return r, false
	case d.ID == uuid.Nil:
		writeErr(w, http.StatusBadRequest, "device.id is required")
		return r, false
	case d.Platform != "android" && d.Platform != "ios":
		writeErr(w, http.StatusBadRequest, "device.platform is android or ios")
		return r, false
	case d.Name == "" || utf8.RuneCountInString(d.Name) > 80:
		writeErr(w, http.StatusBadRequest, "device.name is 1 to 80 characters")
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), deviceKey{}, d)), true
}

func deviceOf(ctx context.Context) *deviceReq {
	d, _ := ctx.Value(deviceKey{}).(*deviceReq)
	return d
}

// endPhone ends a person's live sessions on one phone, in every business
// they belong to: signing in again on a phone replaces its session wherever
// the earlier one was (a copied token does not outlive a fresh sign-in).
func (s *Server) endPhone(ctx context.Context, user, device uuid.UUID) error {
	return s.inPersonsTenants(ctx, user, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE device_id = $1 AND user_id = $2 AND revoked_at IS NULL`, device, user)
		return err
	})
}

// errPhoneBusy is two sign-ins on one phone at once: the second loses
// (sessions_one_per_phone) and is asked to try again.
var errPhoneBusy = errors.New("this phone is already signing in: try again")

// insertSession stores a new session in tx: a device session when the
// request carries a device (askDevice), its earlier sessions ended first by
// createSession (endPhone).
func insertSession(ctx context.Context, tx pgx.Tx, r *http.Request, tok string, user, tenant uuid.UUID, method string) error {
	d := deviceOf(ctx)
	if d == nil {
		_, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, ip, auth_method) VALUES ($1, $2, $3, now() + $4::interval, $5, $6)`,
			hashToken(tok), user, tenant, sessionTTL.String(), clientIP(r), method)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, ip, auth_method, device_id, device_name, device_platform, last_used_at)
		VALUES ($1, $2, $3, now() + $4::interval, $5, $6, $7, $8, $9, now())`,
		hashToken(tok), user, tenant, deviceTTL.String(), clientIP(r), method, d.ID, d.Name, d.Platform)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		return errPhoneBusy
	}
	return err
}

// touchDevice renews a device session found by its token while it is in use.
func (s *Server) touchDevice(ctx context.Context, tok string, lastUsed *time.Time) {
	if lastUsed != nil && time.Since(*lastUsed) < time.Hour {
		return
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_session_touch($1)`, hashToken(tok)); err != nil {
		s.Logger.Warn("device session not renewed", "err", err)
	}
}

// phone is one of a person's phones, as Your phones lists it.
type phone struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform"`
	SignedInAt time.Time `json:"signed_in_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	// This is the phone asking.
	This bool `json:"this,omitempty"`
}

// listDevices is GET /v1/me/devices: the person's signed-in phones, in
// every business they belong to, the most recently used first.
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "only a person has phones")
		return
	}
	ctx := r.Context()
	this := uuid.Nil
	if tok := sessionToken(r); tok != "" {
		_ = s.inPersonsTenants(ctx, p.UserID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT device_id FROM sessions WHERE token_hash = $1 AND device_id IS NOT NULL`, hashToken(tok)).Scan(&this)
		})
	}
	out := []phone{}
	err := s.inPersonsTenants(ctx, p.UserID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT ON (device_id) device_id, device_name, device_platform, created_at, coalesce(last_used_at, created_at)
			  FROM sessions
			 WHERE user_id = $1 AND device_id IS NOT NULL AND revoked_at IS NULL AND expires_at > now()
			 ORDER BY device_id, coalesce(last_used_at, created_at) DESC`, p.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ph phone
			if err := rows.Scan(&ph.ID, &ph.Name, &ph.Platform, &ph.SignedInAt, &ph.LastUsedAt); err != nil {
				return err
			}
			ph.This = ph.ID == this
			out = append(out, ph)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slices.SortFunc(out, func(a, b phone) int { return b.LastUsedAt.Compare(a.LastUsedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// removeDevice is DELETE /v1/me/devices/{id}: that phone is signed out,
// in every business, and told so on its next request. Audited.
func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "only a person has phones")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such phone")
		return
	}
	ctx := r.Context()
	var ended int64
	err = s.inPersonsTenants(ctx, p.UserID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND device_id = $2 AND revoked_at IS NULL`, p.UserID, id)
		if err != nil {
			return err
		}
		ended = tag.RowsAffected()
		if ended == 0 {
			return errNoPhone
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.device_removed', $3, jsonb_build_object('device', $4::text))`,
			p.TenantID, p.UserID.String(), p.UserID.String(), id.String())
		return err
	})
	if errors.Is(err, errNoPhone) {
		writeErr(w, http.StatusNotFound, "no such phone")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errNoPhone = errors.New("no such phone")

// inPersonsTenants runs fn in a transaction scoped to every business the
// person belongs to: their phones' sessions may be in any of them.
func (s *Server) inPersonsTenants(ctx context.Context, user uuid.UUID, fn func(pgx.Tx) error) error {
	var tenants []uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_tenant_scope($1, false)`, user).Scan(&tenants); err != nil {
		return err
	}
	if len(tenants) == 0 {
		return errNoMembership
	}
	return db.InTenantTx(ctx, s.Store.Pool, tenants, fn)
}

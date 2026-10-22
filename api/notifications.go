package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/notices"
)

// Notifications (no-technical-steps N12): what each person needs to know,
// for the web app's bell. They are a person's own: an API key has none.

var errNoPerson = fmt.Errorf("%w: notifications belong to a person, not an API key", errForbidden)

// listNotifications is the caller's notifications, newest first:
// ?unread=true for the unread only, ?before=<updated_at> for the next page.
func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var before *time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: before is a time (RFC 3339)", errBadRequest))
			return
		}
		before = &t
	}
	var list []notices.Item
	var unread int
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = notices.ListTx(r.Context(), tx, p.UserID, r.URL.Query().Get("unread") == "true", before, 50); err != nil {
			return err
		}
		unread, err = notices.UnreadTx(r.Context(), tx, p.UserID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": nonNil(list), "unread": unread})
}

// notificationCount is what the bell shows: unread notifications, and the
// approvals waiting for the caller.
func (s *Server) notificationCount(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var unread, waiting int
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if unread, err = notices.UnreadTx(r.Context(), tx, p.UserID); err != nil {
			return err
		}
		if p.mayDecideAny() {
			items, _, err := s.openApprovalsTx(r.Context(), tx, p)
			if err != nil {
				return err
			}
			waiting = len(items)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread, "approvals": waiting})
}

// readNotifications marks the caller's notifications read: {"ids": [...]}
// or {"all": true}.
func (s *Server) readNotifications(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var req struct {
		IDs []uuid.UUID `json:"ids"`
		All bool        `json:"all"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.IDs) > 500 {
		s.fail(w, r, fmt.Errorf("%w: at most 500 ids", errBadRequest))
		return
	}
	var n int64
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		n, err = notices.MarkReadTx(r.Context(), tx, p.UserID, req.IDs, req.All)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"read": n})
}

// getNotificationSettings is how the caller hears beyond the app.
func (s *Server) getNotificationSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var out []notices.Setting
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = notices.SettingsTx(r.Context(), tx, p.UserID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out})
}

// putNotificationSettings saves the caller's choices.
func (s *Server) putNotificationSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var req struct {
		Settings []notices.Setting `json:"settings"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var out []notices.Setting
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := notices.SetTx(r.Context(), tx, p.TenantID, p.UserID, req.Settings); err != nil {
			if errors.Is(err, notices.ErrUnknownGroup) {
				return fmt.Errorf("%w: a setting's group is failures, setup or finished", errBadRequest)
			}
			return err
		}
		var err error
		out, err = notices.SettingsTx(r.Context(), tx, p.UserID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out})
}

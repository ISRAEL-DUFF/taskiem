package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
)

// Kinds an owner makes (saved objects S1, part 5; saved-objects §4.2): a
// person's own on You (/v1/me/saved-kinds), a business's in its Settings
// (/v1/saved-kinds, changed by whoever may manage its saved things). An
// object of one is of kind custom: its values in details (values, and the
// sensitive ones masked), the sensitive ones sealed together as the account
// number is, shown in full only on a fresh unlock (reveal). Changing a kind
// keeps what objects had; a field made sensitive, or no longer, moves its
// values between the two at once. A kind with live objects is not removed.

type savedKindView struct {
	ID     uuid.UUID     `json:"id"`
	Name   string        `json:"name"`
	Fields []saved.Field `json:"fields"`
	// Count is how many live saved things are of it.
	Count     int       `json:"count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type savedKindIn struct {
	Name   string        `json:"name"`
	Fields []saved.Field `json:"fields"`
}

const savedKindCols = `id, name, fields, (SELECT count(*) FROM saved_objects o WHERE o.kind_id = saved_kinds.id AND o.archived_at IS NULL), created_at, updated_at`

func scanSavedKind(r pgx.Row) (savedKindView, error) {
	var k savedKindView
	var raw []byte
	err := r.Scan(&k.ID, &k.Name, &raw, &k.Count, &k.CreatedAt, &k.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &k.Fields)
	}
	return k, err
}

func kindWhere(o savedOwner) (string, uuid.UUID) {
	if o.mine() {
		return `person_id = $1 AND archived_at IS NULL`, o.person
	}
	return `tenant_id = $1 AND archived_at IS NULL`, o.tenant
}

// savedKindTx is one of the owner's live kinds.
func savedKindTx(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, lock bool) (savedKindView, error) {
	where, owner := kindWhere(o)
	q := `SELECT ` + savedKindCols + ` FROM saved_kinds WHERE ` + where + ` AND id = $2`
	if lock {
		q += ` FOR UPDATE`
	}
	k, err := scanSavedKind(tx.QueryRow(ctx, q, owner, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return k, saved.ErrInvalid
	}
	return k, err
}

// savedKindRoutes are a business's kinds; personSavedKindRoutes a
// person's own.
func (s *Server) savedKindRoutes(r chi.Router) {
	r.Get("/saved-kinds", s.savedHandler(false, s.listSavedKinds))
	r.With(s.need(PermSavedManage), s.tenantWide).Post("/saved-kinds", s.savedHandler(false, s.addSavedKind))
	r.With(s.need(PermSavedManage), s.tenantWide).Put("/saved-kinds/{id}", s.savedHandler(false, s.changeSavedKind))
	r.With(s.need(PermSavedManage), s.tenantWide).Delete("/saved-kinds/{id}", s.savedHandler(false, s.removeSavedKind))
}

func (s *Server) personSavedKindRoutes(r chi.Router) {
	r.Get("/me/saved-kinds", s.savedHandler(true, s.listSavedKinds))
	r.Post("/me/saved-kinds", s.savedHandler(true, s.addSavedKind))
	r.Put("/me/saved-kinds/{id}", s.savedHandler(true, s.changeSavedKind))
	r.Delete("/me/saved-kinds/{id}", s.savedHandler(true, s.removeSavedKind))
}

// listSavedKinds is GET /v1/saved-kinds and /v1/me/saved-kinds: the
// owner's kinds by name, the starters to copy, and the limit.
func (s *Server) listSavedKinds(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	out := []savedKindView{}
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := kindWhere(o)
		rows, err := tx.Query(ctx, `SELECT `+savedKindCols+` FROM saved_kinds WHERE `+where+` ORDER BY lower(name)`, owner)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedKindView, error) { return scanSavedKind(r) })
		return err
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	if out == nil {
		out = []savedKindView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": out, "starters": saved.Starters, "limit": saved.MaxCustomKinds, "to_change": savedToChange(r, o)})
}

// kindEvent records a change to a business's kinds in its audit chain; a
// person's own are theirs alone.
func kindEvent(r *http.Request, tx pgx.Tx, o savedOwner, id uuid.UUID, action string, detail map[string]any) error {
	if o.mine() {
		return nil
	}
	return auditTx(r, tx, "saved.kind."+action, id.String(), detail)
}

// addSavedKind is POST /v1/saved-kinds and /v1/me/saved-kinds {name,
// fields}: a new kind, up to 40.
func (s *Server) addSavedKind(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	var in savedKindIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	name, fields, err := saved.CleanKind(in.Name, in.Fields)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	raw, _ := json.Marshal(fields)
	var k savedKindView
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		where, owner := kindWhere(o)
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_kinds WHERE `+where, owner).Scan(&n); err != nil {
			return err
		}
		if n >= saved.MaxCustomKinds {
			return saved.Refuse(fmt.Sprintf("You have %d kinds of your own, the most you can make.", saved.MaxCustomKinds))
		}
		id := uuid.Must(uuid.NewV7())
		if k, err = scanSavedKind(tx.QueryRow(ctx, `INSERT INTO saved_kinds (id, person_id, tenant_id, name, fields, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $6) RETURNING `+savedKindCols, id, nullable(o.person), nullable(o.tenant), name, raw, o.by)); err != nil {
			return err
		}
		return kindEvent(r, tx, o, id, "added", map[string]any{"name": name, "fields": len(fields)})
	})
	if err != nil {
		s.kindFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"kind": k})
}

// kindFail answers a kind's refusals: a name taken, like an object's.
func (s *Server) kindFail(w http.ResponseWriter, r *http.Request, err error) {
	if isUnique(err) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "You have a kind by that name: choose another.", "name_taken": true})
		return
	}
	if errors.Is(err, saved.ErrInvalid) && saved.Plain(err) == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.savedFail(w, r, err)
}

// changeSavedKind is PUT /v1/saved-kinds/{id} {name, fields}: the kind as
// it is to be. A field kept keeps its key; one made sensitive (or no
// longer) moves its values in every object of the kind at once.
func (s *Server) changeSavedKind(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in savedKindIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	name, fields, err := saved.CleanKind(in.Name, in.Fields)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	raw, _ := json.Marshal(fields)
	var k savedKindView
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		was, err := savedKindTx(ctx, tx, o, id, true)
		if err != nil {
			return err
		}
		where, owner := kindWhere(o)
		if k, err = scanSavedKind(tx.QueryRow(ctx, `UPDATE saved_kinds SET name = $3, fields = $4, updated_by = $5, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedKindCols, owner, id, name, raw, o.by)); err != nil {
			return err
		}
		if err := s.resealKindTx(ctx, tx, o, k); err != nil {
			return err
		}
		return kindEvent(r, tx, o, id, "changed", map[string]any{"name": name, "was": was.Name, "fields": len(fields)})
	})
	if err != nil {
		s.kindFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": k})
}

// resealKindTx keeps each live object of a kind as the kind now says:
// values of a field made sensitive sealed and masked, of one no longer
// sensitive kept plain; its hint made again.
func (s *Server) resealKindTx(ctx context.Context, tx pgx.Tx, o savedOwner, k savedKindView) error {
	where, owner := ownerWhere(o)
	rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND kind_id = $2 FOR UPDATE`, owner, k.ID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
	if err != nil {
		return err
	}
	for _, x := range list {
		all, err := s.customValuesTx(ctx, tx, o, x)
		if err != nil {
			return err
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, x.ID, k.Name, k.Fields, all, x.Details)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE saved_objects SET details = $3, sealed = $4, hint = $5 WHERE `+where+` AND id = $2`, owner, x.ID, details, sealed, hint); err != nil {
			return err
		}
	}
	return nil
}

// removeSavedKind is DELETE /v1/saved-kinds/{id}: archived, once no live
// saved thing is of it (409 {in_use} while some are).
func (s *Server) removeSavedKind(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var inUse int
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		k, err := savedKindTx(ctx, tx, o, id, true)
		if err != nil {
			return err
		}
		if k.Count > 0 {
			inUse = k.Count
			return nil
		}
		where, owner := kindWhere(o)
		if _, err := tx.Exec(ctx, `UPDATE saved_kinds SET archived_at = now(), updated_by = $3 WHERE `+where+` AND id = $2`, owner, id, o.by); err != nil {
			return err
		}
		return kindEvent(r, tx, o, id, "removed", map[string]any{"name": k.Name})
	})
	if err != nil {
		s.kindFail(w, r, err)
		return
	}
	if inUse > 0 {
		things := "saved things are"
		if inUse == 1 {
			things = "saved thing is"
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": fmt.Sprintf("%d %s of this kind: remove or change them first.", inUse, things), "in_use": inUse})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- objects of a made-up kind ---

// customValuesTx is an object's values in full: those kept plain, and the
// sealed ones opened.
func (s *Server) customValuesTx(ctx context.Context, tx pgx.Tx, o savedOwner, x savedRow) (map[string]string, error) {
	all := map[string]string{}
	plain, _ := x.Details["values"].(map[string]any)
	for k, v := range plain {
		if str, ok := v.(string); ok {
			all[k] = str
		}
	}
	if len(x.Sealed) == 0 {
		return all, nil
	}
	raw, err := s.openSaved(ctx, tx, o, x.ID, x.Sealed)
	if err != nil {
		return nil, err
	}
	var secret map[string]string
	if err := json.Unmarshal([]byte(raw), &secret); err != nil {
		return nil, err
	}
	for k, v := range secret {
		all[k] = v
	}
	return all, nil
}

// customStoreTx is how an object of a made-up kind is kept: its details
// (the note kept from was, the plain values, the sealed ones masked), its
// sealed values, and its hint.
func (s *Server) customStoreTx(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, kindName string, fields []saved.Field, all map[string]string, was map[string]any) (map[string]any, map[string]any, string, error) {
	plain, secret, masked := saved.SplitValues(fields, all)
	details := map[string]any{"values": plain, "masked": masked}
	if note, _ := was["note"].(string); note != "" {
		details["note"] = note
	}
	sealed := map[string]any{}
	if len(secret) > 0 {
		raw, _ := json.Marshal(secret)
		var err error
		if sealed, err = s.sealSavedAs(ctx, tx, o, id, "saved_details", string(raw)); err != nil {
			return nil, nil, "", err
		}
	}
	return details, sealed, saved.CustomHint(kindName, fields, all), nil
}

// addSavedCustom adds an object of one of the owner's made-up kinds.
func (s *Server) addSavedCustom(ctx context.Context, who savedWho, o savedOwner, id uuid.UUID, name, title string, in savedIn) (savedRow, savedProblem, error) {
	var x savedRow
	if in.KindID == nil {
		return x, savedProblem{}, saved.Refuse("Choose which of your kinds it is.")
	}
	note := map[string]any{}
	if in.Note != nil && *in.Note != "" {
		note["note"] = *in.Note
	}
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		k, err := savedKindTx(ctx, tx, o, *in.KindID, false)
		if errors.Is(err, saved.ErrInvalid) {
			return saved.Refuse("That kind is gone. Choose another.")
		}
		if err != nil {
			return err
		}
		all, err := saved.CleanValues(k.Fields, in.Values, nil)
		if err != nil {
			return err
		}
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		if err := savedRoomTx(ctx, tx, o, name); err != nil {
			return err
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, id, k.Name, k.Fields, all, note)
		if err != nil {
			return err
		}
		if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, kind_id, details, sealed, hint,
				check_state, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12) RETURNING `+savedCols,
			id, nullable(o.person), nullable(o.tenant), name, title, saved.KindCustom, k.ID, details, sealed, hint, saved.NoCheck, o.by)); err != nil {
			return err
		}
		return savedEventBy(ctx, tx, who, o, id, "added", map[string]any{"name": name, "kind": k.Name})
	})
	return x, savedProblem{}, err
}

// changeSavedCustom changes an object of a made-up kind: its name, title,
// note, and the values given (null removes one); sensitive ones kept
// sealed. No passkey: nothing of these moves money. Refused when someone
// else changed it since it was read.
func (s *Server) changeSavedCustom(w http.ResponseWriter, r *http.Request, o savedOwner, was savedRow, in savedIn) {
	ctx := r.Context()
	if in.AccountNumber != nil || in.BankName != nil || in.Details != nil {
		writeErr(w, http.StatusBadRequest, "A thing of your own kind is changed by its values.")
		return
	}
	name, title := was.Name, was.Title
	var err error
	if in.Name != nil {
		if name, err = saved.Name(*in.Name); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	if in.Title != nil {
		if title, err = saved.Title(*in.Title); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	keep := map[string]any{}
	if note, _ := was.Details["note"].(string); note != "" {
		keep["note"] = note
	}
	if in.Note != nil {
		keep["note"] = *in.Note
	}
	var x savedRow
	var changed []string
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		cur, err := savedRowTx(ctx, tx, o, was.ID, true)
		if err != nil {
			return err
		}
		if !cur.UpdatedAt.Equal(was.UpdatedAt) {
			return errSavedChanged
		}
		before, err := s.customValuesTx(ctx, tx, o, cur)
		if err != nil {
			return err
		}
		all, err := saved.CleanValues(cur.Fields, in.Values, before)
		if err != nil {
			return err
		}
		for _, f := range cur.Fields {
			if all[f.Key] != before[f.Key] {
				changed = append(changed, f.Label)
			}
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, cur.ID, cur.KindName, cur.Fields, all, keep)
		if err != nil {
			return err
		}
		where, owner := ownerWhere(o)
		if x, err = scanSaved(tx.QueryRow(ctx, `UPDATE saved_objects SET name = $3, title = $4, details = $5, sealed = $6, hint = $7, updated_by = $8, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedCols, owner, cur.ID, name, title, details, sealed, hint, o.by)); err != nil {
			return err
		}
		return s.savedEvent(r, tx, o, cur.ID, "changed", map[string]any{"changed": append(changedFields(was, x), changed...)})
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": x.view(o, true)})
}

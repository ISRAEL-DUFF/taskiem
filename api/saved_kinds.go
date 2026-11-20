package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
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
	// Passkey confirms showing what was kept hidden: a field with values
	// made not sensitive, made shareable, or a sensitive one given another
	// type (saved.kind on the kind).
	Passkey *credentialJSON `json:"passkey,omitempty"`
}

// savedOpKind confirms a kind's change that shows what was kept hidden.
const savedOpKind = "saved.kind"

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
	return scanSavedKind(tx.QueryRow(ctx, q, owner, id))
}

// savedKindSharedTx is one of the owner's live kinds, held as it is (FOR
// SHARE) while an object of it is saved: a change to the kind waits.
func savedKindSharedTx(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID) (savedKindView, error) {
	where, owner := kindWhere(o)
	return scanSavedKind(tx.QueryRow(ctx, `SELECT `+savedKindCols+` FROM saved_kinds WHERE `+where+` AND id = $2 FOR SHARE`, owner, id))
}

// kindShownTx are the fields of a kind's change (saved.Desensitised) that
// some live object has a hidden value in: those the change would show.
func kindShownTx(ctx context.Context, tx pgx.Tx, o savedOwner, kind uuid.UUID, was, now []saved.Field) (map[string]string, error) {
	out := map[string]string{}
	where, owner := ownerWhere(o)
	for key, label := range saved.Desensitised(was, now) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE `+where+` AND kind_id = $2 AND details->'masked' ? $3`, owner, kind, key).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out[key] = label
		}
	}
	return out, nil
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
	// Showing what was kept hidden is confirmed with a passkey first.
	var shown map[string]string
	var read time.Time
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		was, err := savedKindTx(ctx, tx, o, id, false)
		if err != nil {
			return err
		}
		read = was.UpdatedAt
		shown, err = kindShownTx(ctx, tx, o, id, was.Fields, fields)
		return err
	})
	if err != nil {
		s.kindFail(w, r, err)
		return
	}
	confirmed := ""
	if len(shown) > 0 {
		if !s.savedPasskey(w, r, o, in.Passkey, savedOpKind, id) {
			return
		}
		confirmed = "passkey"
	}
	var k savedKindView
	var gone []string
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		was, err := savedKindTx(ctx, tx, o, id, true)
		if err != nil {
			return err
		}
		// Changed since the passkey was asked for: what it confirmed may
		// not be what this would show.
		if !was.UpdatedAt.Equal(read) {
			return errSavedChanged
		}
		where, owner := kindWhere(o)
		if k, err = scanSavedKind(tx.QueryRow(ctx, `UPDATE saved_kinds SET name = $3, fields = $4, updated_by = $5, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedKindCols, owner, id, name, raw, o.by)); err != nil {
			return err
		}
		labels := make([]string, 0, len(shown))
		for _, l := range shown {
			labels = append(labels, l)
		}
		slices.Sort(labels)
		if gone, err = s.resealKindTx(r, tx, o, k, labels, confirmed); err != nil {
			return err
		}
		detail := map[string]any{"name": name, "was": was.Name, "fields": len(fields)}
		if len(labels) > 0 {
			detail["shown"], detail["confirmed"] = labels, confirmed
		}
		return kindEvent(r, tx, o, id, "changed", detail)
	})
	if err != nil {
		s.kindFail(w, r, err)
		return
	}
	s.dropSavedPaths(ctx, o, gone)
	writeJSON(w, http.StatusOK, map[string]any{"kind": k})
}

// resealKindTx keeps each live object of a kind as the kind now says:
// values of a field made sensitive sealed and masked, of one no longer
// sensitive kept plain; values of a field removed (or no longer good for
// its type) deleted, never kept unseen, and its file let go; its hint made
// again. An object whose hidden values the change shows (shown, by label)
// records that, and how it was confirmed. The paths of files let go are
// returned, to drop once committed.
func (s *Server) resealKindTx(r *http.Request, tx pgx.Tx, o savedOwner, k savedKindView, shown []string, confirmed string) ([]string, error) {
	ctx := r.Context()
	where, owner := ownerWhere(o)
	rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND kind_id = $2 FOR UPDATE`, owner, k.ID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
	if err != nil {
		return nil, err
	}
	fileField := map[string]bool{}
	for _, f := range k.Fields {
		fileField[f.Key] = f.Type == "file"
	}
	var gone []string
	for _, x := range list {
		had, err := s.customValuesTx(ctx, tx, o, x)
		if err != nil {
			return nil, err
		}
		all := saved.KeepFields(k.Fields, had)
		files, claims := map[string]saved.FileMeta{}, map[string]string{}
		for key, meta := range filesOf(x.Details) {
			if fileField[key] && all[key] != "" {
				files[key] = meta
			} else {
				claims[key] = ""
			}
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, x.ID, k.Name, k.Fields, all, x.Details, files)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE saved_objects SET details = $3, sealed = $4, hint = $5 WHERE `+where+` AND id = $2`, owner, x.ID, details, sealed, hint); err != nil {
			return nil, err
		}
		paths, err := claimFilesTx(ctx, tx, o, x.ID, claims)
		if err != nil {
			return nil, err
		}
		gone = append(gone, paths...)
		masked, _ := x.Details["masked"].(map[string]any)
		var its []string
		for _, f := range k.Fields {
			if masked[f.Key] != nil && slices.Contains(shown, f.Label) {
				its = append(its, f.Label)
			}
		}
		if len(its) > 0 {
			if err := s.savedEvent(r, tx, o, x.ID, "changed", map[string]any{"shown": its, "confirmed": confirmed, "by_kind": k.Name}); err != nil {
				return nil, err
			}
		}
	}
	return gone, nil
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
func (s *Server) customStoreTx(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, kindName string, fields []saved.Field, all map[string]string, was map[string]any, files map[string]saved.FileMeta) (map[string]any, map[string]any, string, error) {
	plain, secret, masked := saved.SplitValues(fields, all)
	details := map[string]any{"values": plain, "masked": masked}
	if len(files) > 0 {
		details["files"] = files
	}
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
	return details, sealed, saved.CustomHint(kindName, fields, all, files), nil
}

// filesOf are an object's file fields as it keeps them: each one's name,
// type and size.
func filesOf(details map[string]any) map[string]saved.FileMeta {
	out := map[string]saved.FileMeta{}
	raw, _ := json.Marshal(details["files"])
	_ = json.Unmarshal(raw, &out)
	return out
}

// customFilesTx are an object's file fields after a save: those kept as
// they were, and each new upload checked as one the object may take; and
// which to claim ("" lets one go).
func customFilesTx(ctx context.Context, tx pgx.Tx, o savedOwner, object uuid.UUID, fields []saved.Field, all, before map[string]string, had map[string]saved.FileMeta) (map[string]saved.FileMeta, map[string]string, error) {
	files, claims := map[string]saved.FileMeta{}, map[string]string{}
	for _, f := range fields {
		if f.Type != "file" {
			continue
		}
		v := all[f.Key]
		switch v {
		case "":
			if before[f.Key] != "" {
				claims[f.Key] = ""
			}
		case before[f.Key]:
			files[f.Key] = had[f.Key]
		default:
			meta, err := fileToClaimTx(ctx, tx, o, v, object, f.Key)
			if err != nil {
				return nil, nil, err
			}
			files[f.Key], claims[f.Key] = meta, v
		}
	}
	return files, claims, nil
}

// addSavedCustom adds an object of one of the owner's made-up kinds.
func (s *Server) addSavedCustom(ctx context.Context, who savedWho, o savedOwner, id uuid.UUID, name, title string, in savedIn) (savedRow, savedProblem, error) {
	var x savedRow
	if in.KindID == nil {
		return x, savedProblem{}, saved.Refuse("Choose which of your kinds it is.")
	}
	note := map[string]any{}
	if in.Note != nil {
		n, err := saved.Note(*in.Note)
		if err != nil {
			return x, savedProblem{}, err
		}
		if n != "" {
			note["note"] = n
		}
	}
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		// The kind held as it is while its object is saved.
		k, err := savedKindSharedTx(ctx, tx, o, *in.KindID)
		if errors.Is(err, pgx.ErrNoRows) {
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
		files, claims, err := customFilesTx(ctx, tx, o, id, k.Fields, all, nil, nil)
		if err != nil {
			return err
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, id, k.Name, k.Fields, all, note, files)
		if err != nil {
			return err
		}
		if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, kind_id, details, sealed, hint,
				check_state, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12) RETURNING `+savedCols,
			id, nullable(o.person), nullable(o.tenant), name, title, saved.KindCustom, k.ID, details, sealed, hint, saved.NoCheck, o.by)); err != nil {
			return err
		}
		// The object is there: its files are claimed for it.
		if _, err := claimFilesTx(ctx, tx, o, id, claims); err != nil {
			return err
		}
		return savedEventBy(ctx, tx, who, o, id, "added", map[string]any{"name": name, "kind": k.Name})
	})
	return x, savedProblem{}, err
}

// changeSavedCustom changes an object of a made-up kind: its name, title,
// note, and the values given (null removes one); sensitive ones kept
// sealed. An account in it changed or removed needs a passkey (saved.change)
// and is recorded masked; nothing else of these moves money. Refused when
// someone else changed it since it was read.
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
		n, err := saved.Note(*in.Note)
		if err != nil {
			s.savedFail(w, r, err)
			return
		}
		keep["note"] = n
	}
	// An account in it changed or removed is where money would go:
	// confirmed with a passkey, as a saved account's is, and recorded
	// masked, before and after.
	var moved []map[string]any
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		before, err := s.customValuesTx(ctx, tx, o, was)
		if err != nil {
			return err
		}
		all, err := saved.CleanValues(was.Fields, in.Values, before)
		if err != nil {
			return err
		}
		moved = accountsMoved(was.Fields, before, all)
		return nil
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	confirmed := ""
	if len(moved) > 0 {
		if !s.savedPasskey(w, r, o, in.Passkey, savedOpChange, was.ID) {
			return
		}
		confirmed = "passkey"
	}
	var x savedRow
	var changed, gone []string
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
		files, claims, err := customFilesTx(ctx, tx, o, cur.ID, cur.Fields, all, before, filesOf(cur.Details))
		if err != nil {
			return err
		}
		details, sealed, hint, err := s.customStoreTx(ctx, tx, o, cur.ID, cur.KindName, cur.Fields, all, keep, files)
		if err != nil {
			return err
		}
		if gone, err = claimFilesTx(ctx, tx, o, cur.ID, claims); err != nil {
			return err
		}
		where, owner := ownerWhere(o)
		if x, err = scanSaved(tx.QueryRow(ctx, `UPDATE saved_objects SET name = $3, title = $4, details = $5, sealed = $6, hint = $7, updated_by = $8, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedCols, owner, cur.ID, name, title, details, sealed, hint, o.by)); err != nil {
			return err
		}
		detail := map[string]any{"changed": append(changedFields(was, x), changed...)}
		if len(moved) > 0 {
			detail["accounts"], detail["confirmed"] = moved, confirmed
		}
		return s.savedEvent(r, tx, o, cur.ID, "changed", detail)
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	s.dropSavedPaths(ctx, o, gone)
	writeJSON(w, http.StatusOK, map[string]any{"saved": x.view(o, true)})
}

// accountsMoved are an object's account fields that a change moves or
// removes (one added where there was none is not): each one's label and
// its value masked, before and after.
func accountsMoved(fields []saved.Field, before, after map[string]string) []map[string]any {
	var out []map[string]any
	for _, f := range fields {
		if f.Type != "bank_account" || before[f.Key] == "" || before[f.Key] == after[f.Key] {
			continue
		}
		out = append(out, map[string]any{"field": f.Label, "was": maskAccount(before[f.Key]), "now": maskAccount(after[f.Key])})
	}
	return out
}

// maskAccount is an account field's value as a record keeps it: "Zenith
// Bank ••6789", or "" for none.
func maskAccount(v string) string {
	if v == "" {
		return ""
	}
	bank, number := saved.SplitBankValue(v)
	if len(number) < 4 {
		return saved.MaskValue(v)
	}
	return strings.TrimSpace(bank + " ••" + number[len(number)-4:])
}

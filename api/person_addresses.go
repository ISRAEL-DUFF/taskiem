package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	phonenum "github.com/israel-duff/taskiem/engine/phone"
	"github.com/israel-duff/taskiem/engine/saved"
)

// A person's saved addresses (docs/spec/taskiem-for-everyone.md §13, §14;
// migration 00465). Since saved objects S1 (part 6; migration 00494) they
// are the person's own place objects: the same rows the Saved page lists,
// called up by @name, the label their title, the default marked
// (default_place). /v1/me/addresses reads and writes them as it always
// did. They are read and written in the person's own scope only; once a
// door names a business in a transaction, the list cannot be read there. One is offered when
// ordering: POST /v1/me/chats/{id}/orders takes address_id and copies the
// address into the order, so a business sees only the address on its
// order, never the saved list. Deleted with the account.

// maxAddresses is how many addresses a person may keep.
const maxAddresses = 20

// savedAddress is one of the person's saved addresses.
type savedAddress struct {
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
	Line  string    `json:"line"`
	Area  string    `json:"area"`
	// Landmark: "opposite the Shoprite gate".
	Landmark string `json:"landmark"`
	// Phone: a number for the rider, when it is not the person's own.
	Phone     *string   `json:"phone"`
	Default   bool      `json:"default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// deliverTo is where an order goes, as the order keeps it: the address
// (with its area) and the landmark, which the rider holding it sees.
type deliverTo struct {
	Address  string
	Landmark string
}

// deliverTo is the address as copied into an order: the line and the
// area as its address; the landmark, and the phone for the rider when
// there is one, as its landmark.
func (a savedAddress) deliverTo() deliverTo {
	out := deliverTo{Address: a.Line, Landmark: a.Landmark}
	if a.Area != "" {
		out.Address += ", " + a.Area
	}
	if a.Phone != nil {
		call := "Phone for delivery: " + *a.Phone
		if out.Landmark != "" {
			out.Landmark += ". " + call
		} else {
			out.Landmark = call
		}
	}
	return out
}

// savedAddressCols are a place object's columns as an address.
const savedAddressCols = `id, title, details->>'line', coalesce(details->>'area', ''), coalesce(details->>'landmark', ''), details->>'phone', default_place, created_at, updated_at`

// placesWhere names the person's live places ($1 the person).
const placesWhere = `person_id = $1 AND kind = 'place' AND archived_at IS NULL`

func scanSavedAddress(r pgx.Row) (savedAddress, error) {
	var a savedAddress
	err := r.Scan(&a.ID, &a.Label, &a.Line, &a.Area, &a.Landmark, &a.Phone, &a.Default, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// personAddressRoutes are the person's routes (personalRoutes lists them).
func (s *Server) personAddressRoutes(r chi.Router) {
	r.Get("/me/addresses", s.listAddresses)
	r.Post("/me/addresses", s.addAddress)
	r.Put("/me/addresses/{id}", s.editAddress)
	r.Delete("/me/addresses/{id}", s.deleteAddress)
}

// addressesTx are the person's saved addresses, the default first, then
// the oldest first.
func addressesTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) ([]savedAddress, error) {
	rows, err := tx.Query(ctx, `SELECT `+savedAddressCols+` FROM saved_objects WHERE `+placesWhere+` ORDER BY default_place DESC, created_at, id`, person)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedAddress, error) { return scanSavedAddress(r) })
}

// lockAddressesTx holds the person's addresses until the transaction ends,
// so the default and the count stay right under requests sent at once.
func lockAddressesTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('person-addresses:' || $1::text, 0))`, person)
	return err
}

// savedAddress is one of the person's saved addresses, read in their own
// scope (no door open).
func (s *Server) savedAddress(ctx context.Context, person, id uuid.UUID) (savedAddress, error) {
	var a savedAddress
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		a, err = scanSavedAddress(tx.QueryRow(ctx, `SELECT `+savedAddressCols+` FROM saved_objects WHERE `+placesWhere+` AND id = $2`, person, id))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, customerSendErr{http.StatusNotFound, "That saved address is gone. Choose another, or type the address."}
	}
	return a, err
}

// addressIn is a saved address as the person writes it; on an edit, what
// is left out stays as it was.
type addressIn struct {
	Label    *string `json:"label"`
	Line     *string `json:"line"`
	Area     *string `json:"area"`
	Landmark *string `json:"landmark"`
	// Phone: "" removes it.
	Phone   *string `json:"phone"`
	Default *bool   `json:"default"`
}

// apply checks the fields given and writes them over a.
func (in addressIn) apply(a *savedAddress) error {
	text := func(p *string, to *string, max int, what string, need bool) error {
		if p == nil {
			return nil
		}
		v := strings.Join(strings.Fields(*p), " ")
		switch {
		case need && v == "":
			return customerSendErr{http.StatusBadRequest, "Give the address its " + what + "."}
		case len([]rune(v)) > max:
			return customerSendErr{http.StatusBadRequest, "The " + what + " is too long."}
		}
		*to = v
		return nil
	}
	if err := text(in.Label, &a.Label, 40, "name (Home, Work)", true); err != nil {
		return err
	}
	if err := text(in.Line, &a.Line, 200, "street and number", true); err != nil {
		return err
	}
	if err := text(in.Area, &a.Area, 80, "area", false); err != nil {
		return err
	}
	if err := text(in.Landmark, &a.Landmark, 200, "landmark", false); err != nil {
		return err
	}
	if in.Phone != nil {
		if strings.TrimSpace(*in.Phone) == "" {
			a.Phone = nil
		} else {
			n, err := phonenum.Normalise(*in.Phone)
			if err != nil {
				return errAddressPhone
			}
			a.Phone = &n
		}
	}
	if a.Label == "" || a.Line == "" {
		return customerSendErr{http.StatusBadRequest, "Give the address a name (Home, Work) and its street and number."}
	}
	return nil
}

var errAddressPhone = errors.New("the phone for delivery is not a number")

// addressFail answers the addresses' refusals.
func (s *Server) addressFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errAddressPhone) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "The phone for delivery doesn't look like a phone number.", "phone_invalid": true})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.personFail(w, r, err)
}

// listAddresses is GET /v1/me/addresses: the person's saved addresses, the
// default first.
func (s *Server) listAddresses(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out []savedAddress
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		out, err = addressesTx(r.Context(), tx, person)
		return err
	})
	if err != nil {
		s.addressFail(w, r, err)
		return
	}
	if out == nil {
		out = []savedAddress{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"addresses": out})
}

// addAddress is POST /v1/me/addresses {label, line, area?, landmark?,
// phone?, default?}: a new saved address. The first is the default; one
// marked default takes it from the others. 409 past maxAddresses.
func (s *Server) addAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in addressIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	a := savedAddress{ID: uuid.Must(uuid.NewV7())}
	if err := in.apply(&a); err != nil {
		s.addressFail(w, r, err)
		return
	}
	o := savedOwner{person: person, by: person}
	c, err := a.place("")
	if err != nil {
		s.addressFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := lockAddressesTx(ctx, tx, person); err != nil {
			return err
		}
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		var n int
		var hasDefault bool
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(default_place), false) FROM saved_objects WHERE `+placesWhere, person).Scan(&n, &hasDefault); err != nil {
			return err
		}
		if n >= maxAddresses {
			return customerSendErr{http.StatusConflict, "You can keep up to 20 addresses. Delete one you no longer use first."}
		}
		name, err := placeNameTx(ctx, tx, person, a.Label)
		if err != nil {
			return err
		}
		if err := savedRoomTx(ctx, tx, o, name); err != nil {
			return err
		}
		a.Default = !hasDefault || (in.Default != nil && *in.Default)
		if a.Default && hasDefault {
			if _, err := tx.Exec(ctx, `UPDATE saved_objects SET default_place = false WHERE `+placesWhere+` AND default_place`, person); err != nil {
				return err
			}
		}
		if a, err = scanSavedAddress(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, name, title, kind, details, sealed, hint, check_state, default_place, created_by, updated_by)
			VALUES ($1, $2, $3, $4, 'place', $5, '{}', $6, 'none', $7, $2, $2) RETURNING `+savedAddressCols, a.ID, person, name, a.Label, c.Details, c.Hint, a.Default)); err != nil {
			return err
		}
		return savedEventBy(ctx, tx, savedWhoOf(r), o, a.ID, "added", map[string]any{"name": name, "kind": saved.KindPlace, "from": "addresses"})
	})
	if err != nil {
		s.addressFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// editAddress is PUT /v1/me/addresses/{id}: what is given is changed;
// default: true makes it the default (false is ignored: choose another to
// be the default instead).
func (s *Server) editAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in addressIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var a savedAddress
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := lockAddressesTx(ctx, tx, person); err != nil {
			return err
		}
		var err error
		if a, err = scanSavedAddress(tx.QueryRow(ctx, `SELECT `+savedAddressCols+` FROM saved_objects WHERE `+placesWhere+` AND id = $2 FOR UPDATE`, person, id)); err != nil {
			return err
		}
		// The place's note (written on Saved) is kept: an address has none.
		var note string
		if err := tx.QueryRow(ctx, `SELECT coalesce(details->>'note', '') FROM saved_objects WHERE `+placesWhere+` AND id = $2`, person, id).Scan(&note); err != nil {
			return err
		}
		if err := in.apply(&a); err != nil {
			return err
		}
		c, err := a.place(note)
		if err != nil {
			return err
		}
		if in.Default != nil && *in.Default && !a.Default {
			if _, err := tx.Exec(ctx, `UPDATE saved_objects SET default_place = false WHERE `+placesWhere+` AND default_place`, person); err != nil {
				return err
			}
			a.Default = true
		}
		if a, err = scanSavedAddress(tx.QueryRow(ctx, `UPDATE saved_objects SET title = $3, details = $4, hint = $5, default_place = $6, updated_by = $1, updated_at = now()
			WHERE `+placesWhere+` AND id = $2 RETURNING `+savedAddressCols, person, id, a.Label, c.Details, c.Hint, a.Default)); err != nil {
			return err
		}
		return savedEventBy(ctx, tx, savedWhoOf(r), savedOwner{person: person, by: person}, id, "changed", map[string]any{"from": "addresses"})
	})
	if err != nil {
		s.addressFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// deleteAddress is DELETE /v1/me/addresses/{id}. When it was the default,
// the newest one left becomes the default.
func (s *Server) deleteAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := lockAddressesTx(ctx, tx, person); err != nil {
			return err
		}
		var wasDefault bool
		if err := tx.QueryRow(ctx, `SELECT default_place FROM saved_objects WHERE `+placesWhere+` AND id = $2 FOR UPDATE`, person, id).Scan(&wasDefault); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE saved_objects SET `+savedScrub+`, updated_by = $1 WHERE `+placesWhere+` AND id = $2`, person, id); err != nil {
			return err
		}
		if err := savedEventBy(ctx, tx, savedWhoOf(r), savedOwner{person: person, by: person}, id, "removed", map[string]any{"from": "addresses"}); err != nil {
			return err
		}
		if !wasDefault {
			return nil
		}
		return newestPlaceDefaultTx(ctx, tx, person)
	})
	if err != nil {
		s.addressFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newestPlaceDefaultTx makes the person's newest place the default, when
// the default went.
func newestPlaceDefaultTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE saved_objects SET default_place = true
		WHERE id = (SELECT id FROM saved_objects WHERE `+placesWhere+` ORDER BY created_at DESC, id DESC LIMIT 1)`, person)
	return err
}

// place is the address as a place object keeps it, with the place's note.
func (a savedAddress) place(note string) (saved.Cleaned, error) {
	d := map[string]any{"line": a.Line, "area": a.Area, "landmark": a.Landmark, "note": note}
	if a.Phone != nil {
		d["phone"] = *a.Phone
	}
	return saved.Clean(saved.KindPlace, d)
}

var placeNameNot = regexp.MustCompile(`[^a-z0-9]+`)

// placeNameTx is a name to call a new address up by, made from its label
// ("Mum's place" is @mums-place), with -2, -3 when the person has the
// name already.
func placeNameTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, label string) (string, error) {
	base := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(label))
	base = strings.Trim(placeNameNot.ReplaceAllString(base, "-"), "-")
	if len(base) > 28 {
		base = strings.Trim(base[:28], "-")
	}
	if len(base) < 2 {
		base = "place"
	}
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM saved_objects WHERE person_id = $1 AND name = $2 AND archived_at IS NULL)`, person, name).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return name, nil
		}
	}
}

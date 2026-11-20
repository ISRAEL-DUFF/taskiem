package api

import (
	"context"
	"encoding/base64"
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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Saved objects, S0: the store for saved accounts
// (docs/spec/saved-objects.md; docs/spec/build-plan-saved-tools-payments.md
// S0; decision 0071; migration 00491).
//
// A person keeps their own (Mine, on You: /v1/me/saved), private to them;
// a business keeps its own (/v1/saved), which everyone in it may see and
// use, masked, and those given saved.manage add, change and remove. S0 has
// one kind, the bank account:
//
//   - the account number is sealed (under the person's key, or the
//     business's subject keys) and shown as ••6789; showing it in full,
//     and changing the account (its number or bank), need a fresh passkey
//     for that object (POST …/saved/step-up/options), and each is recorded
//     (the person's own log; the business's audit chain);
//   - the bank's name for it is checked through the business's payments
//     connection when it has one (saved_check.go), else it is kept
//     "Name not checked yet" and checked once one is connected;
//   - @name finds it (…/saved/resolve), the business's and, in a
//     business, the person's own.
//
// The AI is never given an account number: an object reaches it, from S2,
// as its name, title and kind only.

// savedOwner is whose objects a request is about: a person's own (person
// set) or a business's (tenant set), never both.
type savedOwner struct {
	person uuid.UUID
	tenant uuid.UUID
	// by is the person acting: the owner themselves, or a member of the
	// business.
	by uuid.UUID
}

func (o savedOwner) mine() bool { return o.person != uuid.Nil }

func (o savedOwner) label() string {
	if o.mine() {
		return "mine"
	}
	return "business"
}

// savedTx runs fn in the owner's scope: the person's own (no door open),
// or the business's.
func (s *Server) savedTx(ctx context.Context, o savedOwner, fn func(pgx.Tx) error) error {
	if o.mine() {
		return db.InPersonTx(ctx, s.Store.Pool, o.person, fn)
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{o.tenant}, fn)
}

// savedOwnerOf is the owner a request is for: the person themselves on
// /v1/me/saved, else the person's business. API keys, field agents and
// embed apps' users have no saved things.
func savedOwnerOf(r *http.Request, mine bool) (savedOwner, error) {
	person, err := personOf(r)
	if err != nil {
		return savedOwner{}, err
	}
	if mine {
		return savedOwner{person: person, by: person}, nil
	}
	p := principalFrom(r.Context())
	if p.TenantID == uuid.Nil {
		return savedOwner{}, customerSendErr{http.StatusForbidden, "Open a business to see its saved things."}
	}
	return savedOwner{tenant: p.TenantID, by: person}, nil
}

// savedView is one object as its owner's people see it: never the full
// account number.
type savedView struct {
	ID    uuid.UUID `json:"id"`
	Owner string    `json:"owner"`
	Name  string    `json:"name"`
	Title string    `json:"title"`
	Kind  string    `json:"kind"`
	// Hint is what a list shows: "GTBank ••6789".
	Hint string `json:"hint"`
	// Details are the kind's fields that are not sensitive; a bank
	// account's number is there only masked (account_number: "••6789").
	Details map[string]any `json:"details"`
	Check   savedCheckView `json:"check"`
	// A made-up kind's: its id and name, and its values by field (each
	// sensitive one masked, ••3456).
	KindID   *uuid.UUID        `json:"kind_id,omitempty"`
	KindName string            `json:"kind_name,omitempty"`
	Values   map[string]string `json:"values,omitempty"`
	// Files are a made-up kind's file fields: each file's name, type and
	// size (its value the file's id; GET …/file?field= opens it).
	Files map[string]saved.FileMeta `json:"files,omitempty"`
	// ShareSensitive says whether sharing it shares what is sensitive, so
	// the sender is asked first (shareSensitive).
	ShareSensitive bool `json:"share_sensitive,omitempty"`
	// ToChange says whether the person may change or remove it.
	ToChange  bool      `json:"to_change"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// savedCheckView is the bank's name for an account, once a payments
// connection gave it.
type savedCheckView struct {
	// State: checked, or unchecked ("Name not checked yet").
	State       string     `json:"state"`
	AccountName *string    `json:"account_name"`
	Via         *string    `json:"via"`
	At          *time.Time `json:"at"`
	// Problem: why it will not be checked until the account changes (the
	// bank has no such account; the connection does not list the bank by
	// that name). Null while it is checked, or only waiting.
	Problem *string `json:"problem"`
}

// savedRow is a row of saved_objects.
type savedRow struct {
	ID           uuid.UUID
	Name         string
	Title        string
	Kind         string
	Details      map[string]any
	Sealed       map[string]any
	Hint         string
	CheckState   string
	CheckedName  *string
	CheckedVia   *string
	CheckedAt    *time.Time
	CheckProblem *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// A made-up kind's (saved_kinds.go): its id, name and fields as they
	// are now.
	KindID   *uuid.UUID
	KindName string
	Fields   []saved.Field
}

// savedCols are an object's columns, with its made-up kind's name and
// fields when it has one.
const savedCols = `id, name, title, kind, details, sealed, hint, check_state, checked_name, checked_via, checked_at, check_problem, created_at, updated_at,
	kind_id, (SELECT jsonb_build_object('name', k.name, 'fields', k.fields) FROM saved_kinds k WHERE k.id = kind_id)`

func scanSaved(r pgx.Row) (savedRow, error) {
	var o savedRow
	var kind []byte
	err := r.Scan(&o.ID, &o.Name, &o.Title, &o.Kind, &o.Details, &o.Sealed, &o.Hint, &o.CheckState, &o.CheckedName, &o.CheckedVia, &o.CheckedAt, &o.CheckProblem, &o.CreatedAt, &o.UpdatedAt,
		&o.KindID, &kind)
	if err == nil && len(kind) > 0 {
		var k struct {
			Name   string        `json:"name"`
			Fields []saved.Field `json:"fields"`
		}
		if json.Unmarshal(kind, &k) == nil {
			o.KindName, o.Fields = k.Name, k.Fields
		}
	}
	return o, err
}

// ownerWhere is the condition naming the owner's live rows ($1 the owner).
func ownerWhere(o savedOwner) (string, uuid.UUID) {
	if o.mine() {
		return `person_id = $1 AND archived_at IS NULL`, o.person
	}
	return `tenant_id = $1 AND archived_at IS NULL`, o.tenant
}

func (o savedRow) view(owner savedOwner, toChange bool) savedView {
	d := map[string]any{}
	for k, v := range o.Details {
		if k != "last4" {
			d[k] = v
		}
	}
	if last4, _ := o.Details["last4"].(string); last4 != "" {
		d["account_number"] = "••" + last4
	}
	v := savedView{ID: o.ID, Owner: owner.label(), Name: o.Name, Title: o.Title, Kind: o.Kind, Hint: o.Hint, Details: d,
		Check:          savedCheckView{State: o.CheckState, AccountName: o.CheckedName, Via: o.CheckedVia, At: o.CheckedAt, Problem: o.CheckProblem},
		ShareSensitive: shareSensitive(o), ToChange: toChange, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
	if o.Kind == saved.KindCustom {
		delete(d, "values")
		delete(d, "masked")
		delete(d, "files")
		if f := filesOf(o.Details); len(f) > 0 {
			v.Files = f
		}
		v.KindID, v.KindName, v.Values = o.KindID, o.KindName, map[string]string{}
		for _, part := range []string{"values", "masked"} {
			m, _ := o.Details[part].(map[string]any)
			for _, f := range o.Fields {
				if s, _ := m[f.Key].(string); s != "" {
					v.Values[f.Key] = s
				}
			}
		}
	}
	return v
}

// savedToChange says whether the request's person may change the owner's
// objects: their own always; a business's with saved.manage.
func savedToChange(r *http.Request, o savedOwner) bool {
	return o.mine() || principalFrom(r.Context()).Can(PermSavedManage)
}

// --- routes ---

// savedRoutes are a business's saved things, and the bank list and name
// check S0 shares with payments.
func (s *Server) savedRoutes(r chi.Router) {
	r.Get("/saved", s.savedHandler(false, s.listSaved))
	r.Get("/saved/resolve", s.resolveSaved)
	r.Get("/saved/provider", s.savedProvider)
	r.Post("/saved/step-up/options", s.savedHandler(false, s.savedStepUpOptions))
	r.With(s.need(PermSavedManage), s.tenantWide).Post("/saved", s.savedHandler(false, s.addSaved))
	r.With(s.need(PermSavedManage), s.tenantWide).Post("/saved/check", s.checkAllSaved)
	r.Get("/saved/{id}", s.savedHandler(false, s.getSaved))
	r.With(s.need(PermSavedManage), s.tenantWide).Put("/saved/{id}", s.savedHandler(false, s.changeSaved))
	r.With(s.need(PermSavedManage), s.tenantWide).Delete("/saved/{id}", s.savedHandler(false, s.removeSaved))
	r.With(s.need(PermSavedManage), s.tenantWide).Post("/saved/{id}/reveal", s.savedHandler(false, s.revealSaved))
	r.With(s.need(PermSavedManage)).Get("/banks", s.listSavedBanks)
	r.With(s.need(PermSavedManage)).Post("/name-check", s.nameCheck)
}

// personSavedRoutes are the person's own saved things (personalRoutes
// lists them).
func (s *Server) personSavedRoutes(r chi.Router) {
	r.Get("/me/saved", s.savedHandler(true, s.listSaved))
	r.Post("/me/saved", s.savedHandler(true, s.addSaved))
	r.Get("/me/saved/resolve", s.resolveSaved)
	r.Post("/me/saved/step-up/options", s.savedHandler(true, s.savedStepUpOptions))
	r.Get("/me/saved/{id}", s.savedHandler(true, s.getSaved))
	r.Put("/me/saved/{id}", s.savedHandler(true, s.changeSaved))
	r.Delete("/me/saved/{id}", s.savedHandler(true, s.removeSaved))
	r.Post("/me/saved/{id}/reveal", s.savedHandler(true, s.revealSaved))
	r.Get("/me/banks", s.listMyBanks)
	r.Post("/me/saved/read", s.readSavedWords)
}

// savedHandler finds the owner, then hands over.
func (s *Server) savedHandler(mine bool, h func(http.ResponseWriter, *http.Request, savedOwner)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := savedOwnerOf(r, mine)
		if err != nil {
			s.savedFail(w, r, err)
			return
		}
		h(w, r, o)
	}
}

// savedFail answers the saved things' refusals.
func (s *Server) savedFail(w http.ResponseWriter, r *http.Request, err error) {
	var pe *pgconn.PgError
	switch {
	case errors.Is(err, saved.ErrInvalid):
		writeErr(w, http.StatusBadRequest, saved.Plain(err))
	case errors.As(err, &pe) && pe.Code == "23505":
		writeJSON(w, http.StatusConflict, map[string]any{"error": "That name is taken: choose another.", "name_taken": true})
	default:
		s.personFail(w, r, err)
	}
}

// --- list, one, resolve ---

// listSaved is GET /v1/saved and /v1/me/saved (?kind=, ?q=): the owner's
// objects by title. can_check: a payments connection is there to check
// names not checked yet (POST /v1/saved/check), other than those with a
// lasting problem.
func (s *Server) listSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	kind, q := r.URL.Query().Get("kind"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if kind != "" && !slices.Contains(saved.Kinds, kind) && kind != saved.KindCustom {
		writeErr(w, http.StatusBadRequest, "That is not a kind of saved thing.")
		return
	}
	where, owner := ownerWhere(o)
	var out []savedView
	unchecked := 0
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+`
			AND ($2 = '' OR kind = $2)
			AND ($3 = '' OR strpos(name, $3) > 0 OR strpos(lower(title), $3) > 0)
			ORDER BY lower(title), name`, owner, kind, strings.TrimPrefix(q, "@"))
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		toChange := savedToChange(r, o)
		out = make([]savedView, 0, len(list))
		for _, x := range list {
			out = append(out, x.view(o, toChange))
			// Those with a lasting problem wait for a change, not a check.
			if x.Kind == saved.KindBankAccount && x.CheckState == saved.Unchecked && x.CheckProblem == nil {
				unchecked++
			}
		}
		return nil
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	res := map[string]any{"saved": out, "limit": savedLimit(o)}
	if !o.mine() {
		rl, err := s.savedRail(ctx, o.tenant)
		if err != nil {
			s.savedFail(w, r, err)
			return
		}
		res["can_check"] = rl != nil && unchecked > 0 && savedToChange(r, o)
	}
	writeJSON(w, http.StatusOK, res)
}

func savedLimit(o savedOwner) int {
	if o.mine() {
		return saved.MaxPerson
	}
	return saved.MaxBusiness
}

// savedRowTx is one of the owner's live objects; pgx.ErrNoRows when there
// is none. lock holds it until the transaction ends.
func savedRowTx(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, lock bool) (savedRow, error) {
	where, owner := ownerWhere(o)
	q := `SELECT ` + savedCols + ` FROM saved_objects WHERE ` + where + ` AND id = $2`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanSaved(tx.QueryRow(ctx, q, owner, id))
}

func savedID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	return id, err == nil
}

// getSaved is GET /v1/saved/{id} and /v1/me/saved/{id}.
func (s *Server) getSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var x savedRow
	err := s.savedTx(r.Context(), o, func(tx pgx.Tx) error {
		var err error
		x, err = savedRowTx(r.Context(), tx, o, id, false)
		return err
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, x.view(o, savedToChange(r, o)))
}

// resolveSaved is GET /v1/saved/resolve?name= and /v1/me/saved/resolve:
// what @name stands for. In a business, both the person's own and the
// business's are looked in, the person's first; two matches are both
// answered, for the person to choose ("Which @mum?"). None is
// {matches: []}.
func (s *Server) resolveSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	name, err := saved.Name(r.URL.Query().Get("name"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"name": "", "matches": []savedView{}})
		return
	}
	owners := []savedOwner{{person: person, by: person}}
	if p := principalFrom(ctx); p.TenantID != uuid.Nil && !strings.HasPrefix(r.URL.Path, "/v1/me/") {
		owners = append(owners, savedOwner{tenant: p.TenantID, by: person})
	}
	matches := []savedView{}
	for _, o := range owners {
		where, owner := ownerWhere(o)
		err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			x, err := scanSaved(tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND name = $2`, owner, name))
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err == nil {
				matches = append(matches, x.view(o, savedToChange(r, o)))
			}
			return err
		})
		if err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "matches": matches})
}

// --- add, change, remove ---

// savedIn is an object as its owner writes it. On a change, what is left
// out stays as it was.
type savedIn struct {
	Name  *string `json:"name"`
	Title *string `json:"title"`
	Kind  string  `json:"kind"`
	// A bank account's fields.
	BankName      *string `json:"bank_name"`
	BankCode      *string `json:"bank_code"`
	CodeFrom      *string `json:"code_from"`
	AccountNumber *string `json:"account_number"`
	Note          *string `json:"note"`
	// Details are the fields of every other kind (saved.Clean): a person's
	// phone, a place's line, a text's body. On a change, the fields given
	// replace those kept; null removes one.
	Details map[string]any `json:"details"`
	// A made-up kind's (kind custom): which, and its values by field key; on
	// a change, those given replace those kept, and null removes one.
	KindID *uuid.UUID     `json:"kind_id"`
	Values map[string]any `json:"values"`
	// Passkey confirms changing the account (its number or its bank), for
	// a challenge asked for saved.change on this object.
	Passkey *credentialJSON `json:"passkey,omitempty"`
}

// account is the bank account in, over what it was (was).
func (in savedIn) account(was saved.BankAccount) (saved.BankAccount, error) {
	b := was
	if in.BankName != nil {
		b.BankName = *in.BankName
		// A bank chosen again comes with its code, or none.
		b.BankCode, b.CodeFrom = "", ""
	}
	if in.BankCode != nil {
		b.BankCode = *in.BankCode
	}
	if in.CodeFrom != nil {
		b.CodeFrom = *in.CodeFrom
	}
	if in.AccountNumber != nil {
		b.AccountNumber = *in.AccountNumber
	}
	if in.Note != nil {
		b.Note = *in.Note
	}
	return b.Clean()
}

// bankGiven says whether the request names a bank (its name or its code).
func (in savedIn) bankGiven() bool {
	return in.BankName != nil || in.BankCode != nil || in.CodeFrom != nil
}

// accountMoved says whether b sends money elsewhere than was: another
// number, another bank, or another bank code (a code is a bank).
func accountMoved(was, b saved.BankAccount) bool {
	return b.AccountNumber != was.AccountNumber || b.BankName != was.BankName || b.BankCode != was.BankCode || b.CodeFrom != was.CodeFrom
}

// addSaved is POST /v1/saved and /v1/me/saved {name, title, kind:
// bank_account, bank_name, bank_code?, code_from?, account_number, note?}:
// a new saved account. A business's is checked with its bank at once when
// the business has a payments connection: an account the bank does not
// know is refused; one it could not check just now is kept unchecked
// (check_problem says why). 409 when the name is taken or past the limit.
func (s *Server) addSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	var in savedIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Kind == "" {
		in.Kind = saved.KindBankAccount
	}
	if !slices.Contains(saved.Kinds, in.Kind) && in.Kind != saved.KindCustom {
		writeErr(w, http.StatusBadRequest, "That is not a kind of saved thing.")
		return
	}
	if in.Name == nil || in.Title == nil {
		writeErr(w, http.StatusBadRequest, "Give it a name to call it up by and a title.")
		return
	}
	if in.Kind != saved.KindBankAccount {
		s.addSavedThing(w, r, o, in)
		return
	}
	if in.AccountNumber == nil || in.BankName == nil {
		writeErr(w, http.StatusBadRequest, "Give the account a name to call it up by, a title, its bank and its 10-digit number.")
		return
	}
	x, problem, err := s.addSavedNew(ctx, savedWhoOf(r), o, in)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	out := map[string]any{"saved": x.view(o, true)}
	if problem.Words != "" {
		out["check_problem"] = problem.Words
	}
	writeJSON(w, http.StatusCreated, out)
}

// lasting is the problem as an account keeps it: its words when it lasts
// until the account changes, else nil.
func (p savedProblem) lasting() *string {
	if !p.Lasting || p.Words == "" {
		return nil
	}
	return &p.Words
}

// savedRoomTx refuses a new object whose name the owner already uses, or
// past the owner's limit.
func savedRoomTx(ctx context.Context, tx pgx.Tx, o savedOwner, name string) error {
	where, owner := ownerWhere(o)
	var n int
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(name = $2), false) FROM saved_objects WHERE `+where, owner, name).Scan(&n, &taken); err != nil {
		return err
	}
	if taken {
		return &pgconn.PgError{Code: "23505"}
	}
	if n >= savedLimit(o) {
		return customerSendErr{http.StatusConflict, fmt.Sprintf("You can keep up to %d saved things. Remove one you no longer use first.", savedLimit(o))}
	}
	return nil
}

// checkCols are a check's columns: unchecked without one.
func checkCols(c *savedCheckResult) (state string, name, via *string, at *time.Time) {
	if c == nil {
		return saved.Unchecked, nil, nil, nil
	}
	now := time.Now().UTC()
	return saved.Checked, &c.AccountName, &c.Via, &now
}

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// lockSavedTx holds the owner's objects until the transaction ends, so the
// count stays right under requests sent at once.
func lockSavedTx(ctx context.Context, tx pgx.Tx, o savedOwner) error {
	key := "saved:tenant:" + o.tenant.String()
	if o.mine() {
		key = "saved:person:" + o.person.String()
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// changeSaved is PUT /v1/saved/{id} and /v1/me/saved/{id}: what is given
// is changed. Changing the account itself (its bank or number) needs a
// passkey for this object (saved.change), checks the name again (a
// business's), and is recorded; a title, name or note does not.
func (s *Server) changeSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in savedIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	// What it is now, to know whether the account changes: the number
	// opened only when a new one is given.
	var was savedRow
	var wasAcct saved.BankAccount
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		var err error
		if was, err = savedRowTx(ctx, tx, o, id, false); err != nil {
			return err
		}
		if was.Kind != saved.KindBankAccount {
			return nil
		}
		wasAcct = saved.FromDetails(was.Details)
		wasAcct.AccountNumber, err = s.openSaved(ctx, tx, o, id, was.Sealed)
		return err
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	if in.Kind != "" && in.Kind != was.Kind {
		writeErr(w, http.StatusBadRequest, "A saved thing's kind does not change.")
		return
	}
	if was.Kind == saved.KindCustom {
		s.changeSavedCustom(w, r, o, was, in)
		return
	}
	if was.Kind == saved.KindFile {
		s.changeSavedFileObject(w, r, o, was, in)
		return
	}
	if was.Kind != saved.KindBankAccount {
		s.changeSavedThing(w, r, o, was, in)
		return
	}
	acct, err := in.account(wasAcct)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	// A bank chosen again is named as the payments connection names it; a
	// bank left as it was keeps its code.
	if in.bankGiven() {
		if acct, err = s.settleBank(ctx, o, acct); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	accountChanged := accountMoved(wasAcct, acct)
	name, title := was.Name, was.Title
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
	// A changed account is checked again (a business's), before the
	// passkey: an account the bank does not have is refused without asking
	// anyone to confirm it.
	var check *savedCheckResult
	var problem savedProblem
	if accountChanged && !o.mine() {
		if check, problem, err = s.checkSavedAccount(ctx, o.tenant, acct); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	// Changing where money would go is confirmed with a passkey, last.
	confirmed := ""
	if accountChanged {
		if !s.savedPasskey(w, r, o, in.Passkey, savedOpChange, id) {
			return
		}
		confirmed = "passkey"
	}
	var x savedRow
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		cur, err := savedRowTx(ctx, tx, o, id, true)
		if err != nil {
			return err
		}
		// Changed by someone else since it was read: what is written here
		// was worked out from the account as it was, so nothing is.
		if !cur.UpdatedAt.Equal(was.UpdatedAt) {
			return errSavedChanged
		}
		sealed := cur.Sealed
		if accountChanged {
			if sealed, err = s.sealSaved(ctx, tx, o, id, acct.AccountNumber); err != nil {
				return err
			}
		}
		where, owner := ownerWhere(o)
		set := `name = $3, title = $4, details = $5, sealed = $6, hint = $7, updated_by = $8, updated_at = now()`
		args := []any{owner, id, name, title, acct.Details(), sealed, acct.Hint(), o.by}
		if accountChanged {
			state, cname, cvia, cat := checkCols(check)
			set += `, check_state = $9, checked_name = $10, checked_via = $11, checked_at = $12, check_problem = $13`
			args = append(args, state, cname, cvia, cat, problem.lasting())
		}
		if x, err = scanSaved(tx.QueryRow(ctx, `UPDATE saved_objects SET `+set+` WHERE `+where+` AND id = $2 RETURNING `+savedCols, args...)); err != nil {
			return err
		}
		detail := map[string]any{"changed": changedFields(was, x)}
		if accountChanged {
			detail["account"] = map[string]any{"before": wasAcct.BankName + " " + saved.Masked(wasAcct.AccountNumber), "after": acct.BankName + " " + saved.Masked(acct.AccountNumber)}
			detail["confirmed"] = confirmed
			detail["check"] = x.CheckState
		}
		return s.savedEvent(r, tx, o, id, "changed", detail)
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	out := map[string]any{"saved": x.view(o, true)}
	if problem.Words != "" {
		out["check_problem"] = problem.Words
	}
	writeJSON(w, http.StatusOK, out)
}

// errSavedChanged is a change to an object someone else changed since it
// was read.
var errSavedChanged = customerSendErr{http.StatusConflict, "Someone changed this account just now. Open it again to see it, then make your change."}

// changedFields names what changed between two rows, without values that
// are sensitive.
func changedFields(a, b savedRow) []string {
	out := []string{}
	if a.Name != b.Name {
		out = append(out, "name")
	}
	if a.Title != b.Title {
		out = append(out, "title")
	}
	da, _ := json.Marshal(a.Details)
	dbb, _ := json.Marshal(b.Details)
	if string(da) != string(dbb) {
		out = append(out, "details")
	}
	return out
}

// removeSaved is DELETE /v1/saved/{id} and /v1/me/saved/{id}: it is
// archived, its name free again.
func (s *Server) removeSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var gone []string
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := ownerWhere(o)
		var name string
		var wasDefault bool
		if err := tx.QueryRow(ctx, `SELECT default_place FROM saved_objects WHERE `+where+` AND id = $2 FOR UPDATE`, owner, id).Scan(&wasDefault); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE saved_objects SET archived_at = now(), default_place = false, updated_by = $3, updated_at = now() WHERE `+where+` AND id = $2 RETURNING name`,
			owner, id, o.by).Scan(&name); err != nil {
			return err
		}
		// A person's default address gone: their newest place is it now.
		if wasDefault {
			if err := newestPlaceDefaultTx(ctx, tx, o.person); err != nil {
				return err
			}
		}
		// Its files go with it.
		var err error
		if gone, err = dropObjectFilesTx(ctx, tx, o, id); err != nil {
			return err
		}
		return s.savedEvent(r, tx, o, id, "removed", map[string]any{"name": name})
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	s.dropSavedPaths(ctx, o, gone)
	w.WriteHeader(http.StatusNoContent)
}

// revealSaved is POST /v1/saved/{id}/reveal and /v1/me/saved/{id}/reveal
// {passkey}: the account number in full, for a passkey asked for
// saved.reveal on this object; recorded. A business's: those who manage
// its saved things (from payments, those who may pay).
func (s *Server) revealSaved(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Passkey *credentialJSON `json:"passkey,omitempty"`
	}
	if err := decodeOptional(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		x, err := savedRowTx(ctx, tx, o, id, false)
		if err == nil && x.Kind != saved.KindBankAccount && (x.Kind != saved.KindCustom || len(x.Sealed) == 0) {
			return customerSendErr{http.StatusBadRequest, "Nothing of it is kept hidden: its details are shown in full."}
		}
		return err
	}); err != nil {
		s.savedFail(w, r, err)
		return
	}
	if !s.savedPasskey(w, r, o, in.Passkey, savedOpReveal, id) {
		return
	}
	var number string
	var values map[string]string
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		x, err := savedRowTx(ctx, tx, o, id, false)
		if err != nil {
			return err
		}
		if x.Kind == saved.KindCustom {
			// A made-up kind's sensitive values, in full.
			all, err := s.customValuesTx(ctx, tx, o, x)
			if err != nil {
				return err
			}
			_, values, _ = saved.SplitValues(x.Fields, all)
			return s.savedEvent(r, tx, o, id, "revealed", map[string]any{"fields": len(values), "confirmed": "passkey"})
		}
		if number, err = s.openSaved(ctx, tx, o, id, x.Sealed); err != nil {
			return err
		}
		return s.savedEvent(r, tx, o, id, "revealed", map[string]any{"field": "account_number", "confirmed": "passkey"})
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if values != nil {
		writeJSON(w, http.StatusOK, map[string]any{"values": values})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_number": number})
}

// --- sealing and the record ---

// sealSaved seals an account number for its owner: under the person's own
// key, bound to the object; or under the business's subject keys.
func (s *Server) sealSaved(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, number string) (map[string]any, error) {
	return s.sealSavedAs(ctx, tx, o, id, "account_number", number)
}

// sealSavedAs seals a value for its owner, a business's under category.
func (s *Server) sealSavedAs(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, category, number string) (map[string]any, error) {
	if o.mine() {
		key, err := s.keys().KeyTx(ctx, tx, o.person, true)
		if err != nil {
			return nil, err
		}
		ct, err := peoplechats.Seal(key, o.person, savedRowKey(id), []byte(number))
		if err != nil {
			return nil, err
		}
		return map[string]any{"person": base64.StdEncoding.EncodeToString(ct)}, nil
	}
	return s.Store.PII.SealTx(ctx, tx, o.tenant, category, number)
}

// openSaved opens what sealSaved sealed.
func (s *Server) openSaved(ctx context.Context, tx pgx.Tx, o savedOwner, id uuid.UUID, sealed map[string]any) (string, error) {
	if o.mine() {
		raw, _ := sealed["person"].(string)
		ct, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", err
		}
		key, err := s.keys().KeyTx(ctx, tx, o.person, false)
		if err != nil {
			return "", err
		}
		plain, err := peoplechats.Open(key, o.person, savedRowKey(id), ct)
		return string(plain), err
	}
	v, err := s.Store.PII.OpenTx(ctx, tx, o.tenant, sealed)
	if err != nil {
		return "", err
	}
	n, _ := v.(string)
	return n, nil
}

func savedRowKey(id uuid.UUID) string { return "saved:" + id.String() }

// savedEvent records what was done with an object: in the person's own log
// for theirs, in the business's audit chain for its own (as saved.<action>).
// detail never holds a full account number.
func (s *Server) savedEvent(r *http.Request, tx pgx.Tx, o savedOwner, id uuid.UUID, action string, detail map[string]any) error {
	return savedEventBy(r.Context(), tx, savedWhoOf(r), o, id, action, detail)
}

// --- step-up: a passkey for one object ---

// The step-up operations for saved things, each bound to one object.
const (
	savedOpChange = "saved.change"
	savedOpReveal = "saved.reveal"
)

// savedStepUpOptions is POST /v1/saved/step-up/options and
// /v1/me/saved/step-up/options {operation: saved.change | saved.reveal,
// target: the object's id}: a passkey challenge good for that alone. 409
// {no_passkey} when the person has none.
func (s *Server) savedStepUpOptions(w http.ResponseWriter, r *http.Request, o savedOwner) {
	if !s.passkeysOn(w) {
		return
	}
	ctx := r.Context()
	var req struct {
		Operation string `json:"operation"`
		Target    string `json:"target"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := uuid.Parse(req.Target)
	if (req.Operation != savedOpChange && req.Operation != savedOpReveal) || err != nil {
		writeErr(w, http.StatusBadRequest, "Name what the passkey confirms: saved.change or saved.reveal, and the saved thing's id.")
		return
	}
	if !s.limiter("challenge:"+limitIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	// Only for an object the person may do this to.
	if !savedToChange(r, o) {
		writeErr(w, http.StatusForbidden, "requires "+PermSavedManage)
		return
	}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		_, err := savedRowTx(ctx, tx, o, id, false)
		return err
	}); err != nil {
		s.savedFail(w, r, err)
		return
	}
	var ids [][]byte
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_passkey_ids($1)`, o.by).Scan(&ids); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Add a passkey first: changing a saved account, or seeing its number in full, needs one.", "no_passkey": true})
		return
	}
	ch, err := s.issueChallenge(ctx, "step_up", &o.by, savedScope(o, req.Operation, id))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allow := make([]any, len(ids))
	for i, c := range ids {
		allow[i] = map[string]any{"type": "public-key", "id": b64.EncodeToString(c)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": map[string]any{
		"challenge": b64.EncodeToString(ch), "rpId": s.WebAuthn.RPID, "allowCredentials": allow, "userVerification": "required", "timeout": 60000}})
}

// savedScope binds a passkey to one operation on one object of one owner.
func savedScope(o savedOwner, op string, id uuid.UUID) string {
	owner := "business:" + o.tenant.String()
	if o.mine() {
		owner = "person:" + o.person.String()
	}
	return stepUpScope(op, owner+"/"+id.String())
}

// savedPasskey holds an operation on an object to the person confirming it
// with a passkey asked for it (S0: a passkey only, no authenticator code
// and no PIN). It answers the request and returns false when the proof is
// missing or wrong.
func (s *Server) savedPasskey(w http.ResponseWriter, r *http.Request, o savedOwner, proof *credentialJSON, op string, id uuid.UUID) bool {
	if proof == nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Confirm with your passkey.", "step_up": "passkey", "operation": op, "target": id.String()})
		return false
	}
	if !s.passkeysOn(w) {
		return false
	}
	if _, err := s.verifyPasskey(r.Context(), *proof, "step_up", &o.by, savedScope(o, op, id)); err != nil {
		if passkeyRefused(err) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "That passkey did not confirm this. Try again.", "step_up": "passkey", "operation": op, "target": id.String()})
			return false
		}
		s.fail(w, r, err)
		return false
	}
	return true
}

// --- the kinds other than the bank account ---

// addSavedThing saves a person, a place, a text or an account with a
// provider (saved.Clean): nothing sealed, no name check, recorded as every
// object is (without its details, which may be a person's data).
func (s *Server) addSavedThing(w http.ResponseWriter, r *http.Request, o savedOwner, in savedIn) {
	x, _, err := s.addSavedNew(r.Context(), savedWhoOf(r), o, in)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"saved": x.view(o, true)})
}

// changeSavedThing changes a person, a place, a text or an account with a
// provider: the fields given in details replace those kept (null removes
// one), then the whole is checked again. No passkey: nothing of these moves
// money. Refused when someone else changed it since it was read.
func (s *Server) changeSavedThing(w http.ResponseWriter, r *http.Request, o savedOwner, was savedRow, in savedIn) {
	ctx := r.Context()
	if in.AccountNumber != nil || in.BankName != nil || in.BankCode != nil || in.CodeFrom != nil {
		writeErr(w, http.StatusBadRequest, "Only a bank account has a bank and an account number.")
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
	merged := map[string]any{}
	for k, v := range was.Details {
		merged[k] = v
	}
	for k, v := range in.Details {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	if in.Note != nil {
		merged["note"] = *in.Note
	}
	c, err := saved.Clean(was.Kind, merged)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	var x savedRow
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		cur, err := savedRowTx(ctx, tx, o, was.ID, true)
		if err != nil {
			return err
		}
		if !cur.UpdatedAt.Equal(was.UpdatedAt) {
			return errSavedChanged
		}
		where, owner := ownerWhere(o)
		if x, err = scanSaved(tx.QueryRow(ctx, `UPDATE saved_objects SET name = $3, title = $4, details = $5, hint = $6, updated_by = $7, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedCols, owner, was.ID, name, title, c.Details, c.Hint, o.by)); err != nil {
			return err
		}
		return s.savedEvent(r, tx, o, was.ID, "changed", map[string]any{"changed": changedFields(was, x)})
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": x.view(o, true)})
}

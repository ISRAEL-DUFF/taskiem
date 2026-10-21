package publicweb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Hosted forms' reads and writes (docs/spec/forms.md 8.3), through the
// taskiem_public_form* functions of migration 00182.

// PublicForm is a live form behind /f/{public_id}.
type PublicForm struct {
	Endpoint      uuid.UUID
	Tenant        uuid.UUID
	PublicID      string
	Status        string
	Open          bool
	ClosedMessage string
	Business      string
	Trigger       wd.FormTrigger
	// Schema is the inputs schema and Types the named types it may
	// reference, as JSON values (expr.DecodeJSON).
	Schema map[string]any
	Types  map[string]any
	// Reach are the fields holding phones the workflow messages
	// (ingest.ReachFields): the thank-you page offers updates for them.
	Reach []string
}

// FormLink is a one-time link to a link form.
type FormLink struct {
	ID        uuid.UUID
	PublicID  string
	BoundView []byte // sealed forms.LinkView, nil when not bound to a person
	Usable    bool
}

// FormRequest is a form step's request behind /q/{token}.
type FormRequest struct {
	ID          uuid.UUID
	Title       string
	Description string
	Schema      map[string]any
	Verify      bool
	Status      string // pending, answered, cancelled or expired
	Business    string
	PublicView  []byte // sealed forms.RequestView
	ExpiresAt   time.Time
}

// FormWrite is one submission for the inbox, with its sealed files in
// Submission.Files order.
type FormWrite struct {
	Endpoint, Request, Link *uuid.UUID
	Once                    []byte
	Reference               string
	Idem, StatusHash        []byte
	Payload                 []byte
	Uploads                 []SealedUpload
}

// SealedUpload is one file, sealed under forms.UploadAAD.
type SealedUpload struct {
	Field string
	Data  []byte
}

// FormState is what a status link shows: nothing personal.
type FormState struct {
	Status  string // received, started, answered or refused
	Message string
	Created time.Time
}

// Why a submission was not written.
var (
	// ErrLinkUsed: the one-time link was used by another submission.
	ErrLinkUsed = errors.New("publicweb: the link was used")
	// ErrAnswered: the form step was answered already.
	ErrAnswered = errors.New("publicweb: the request was answered")
	// ErrOnceSent: once_per, and this respondent sent the form already.
	ErrOnceSent = errors.New("publicweb: sent already")
	// ErrRepeat: the same idempotency key was written by a submission
	// running at the same moment (a double tap).
	ErrRepeat = errors.New("publicweb: a repeat")
)

// FormStore is what the form host reads and writes.
type FormStore interface {
	Form(ctx context.Context, publicID string) (PublicForm, bool, error)
	Link(ctx context.Context, tokenHash []byte) (FormLink, bool, error)
	Request(ctx context.Context, tokenHash []byte) (FormRequest, bool, error)
	// Submit writes a submission and its files in one transaction; ok is
	// false when the form or request takes nothing now.
	Submit(ctx context.Context, w FormWrite) (ok bool, err error)
	// Code records a verification code to send; false when limited.
	Code(ctx context.Context, scope uuid.UUID, addressHash, codeHash, payload []byte) (bool, error)
	CheckCode(ctx context.Context, scope uuid.UUID, addressHash, codeHash []byte) (bool, error)
	Status(ctx context.Context, reference string, statusHash []byte) (FormState, bool, error)
}

// DBForms is the FormStore on a pool connected as taskiem_public.
func DBForms(pool *pgxpool.Pool) FormStore { return dbForms{pool} }

type dbForms struct{ pool *pgxpool.Pool }

// ParsePublicForm reads taskiem_public_form's document.
func ParsePublicForm(publicID string, raw []byte) (PublicForm, error) {
	var x struct {
		Endpoint      uuid.UUID       `json:"endpoint_id"`
		Tenant        uuid.UUID       `json:"tenant_id"`
		Status        string          `json:"status"`
		Open          bool            `json:"open"`
		ClosedMessage *string         `json:"closed_message"`
		Business      string          `json:"business"`
		Trigger       json.RawMessage `json:"trigger"`
		Inputs        json.RawMessage `json:"inputs"`
		Types         json.RawMessage `json:"types"`
		Reach         []string        `json:"reach"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return PublicForm{}, err
	}
	f := PublicForm{Endpoint: x.Endpoint, Tenant: x.Tenant, PublicID: publicID, Status: x.Status, Open: x.Open, Business: x.Business, Reach: x.Reach}
	if x.ClosedMessage != nil {
		f.ClosedMessage = *x.ClosedMessage
	}
	doc := map[string]json.RawMessage{"trigger": x.Trigger}
	if len(x.Inputs) > 0 && string(x.Inputs) != "null" {
		doc["inputs"] = x.Inputs
	}
	if len(x.Types) > 0 && string(x.Types) != "null" {
		doc["types"] = x.Types
	}
	b, _ := json.Marshal(doc)
	var d wd.Definition
	if err := json.Unmarshal(b, &d); err != nil {
		return PublicForm{}, err
	}
	var err error
	if f.Trigger, err = d.FormTriggerConfig(); err != nil {
		return PublicForm{}, err
	}
	schema, types := d.InputsSchema()
	f.Schema, _ = schema.(map[string]any)
	f.Types = types
	if f.Schema == nil {
		f.Schema = map[string]any{}
	}
	return f, nil
}

func (d dbForms) Form(ctx context.Context, publicID string) (PublicForm, bool, error) {
	var raw []byte
	if err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form($1)`, publicID).Scan(&raw); err != nil {
		return PublicForm{}, false, err
	}
	if raw == nil {
		return PublicForm{}, false, nil
	}
	f, err := ParsePublicForm(publicID, raw)
	return f, err == nil, err
}

func (d dbForms) Link(ctx context.Context, tokenHash []byte) (FormLink, bool, error) {
	var l FormLink
	err := d.pool.QueryRow(ctx, `SELECT link_id, public_id, bound_view, usable FROM taskiem_public_form_link($1)`, tokenHash).
		Scan(&l.ID, &l.PublicID, &l.BoundView, &l.Usable)
	if errors.Is(err, pgx.ErrNoRows) {
		return FormLink{}, false, nil
	}
	return l, err == nil, err
}

func (d dbForms) Request(ctx context.Context, tokenHash []byte) (FormRequest, bool, error) {
	var raw []byte
	if err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_request($1)`, tokenHash).Scan(&raw); err != nil {
		return FormRequest{}, false, err
	}
	if raw == nil {
		return FormRequest{}, false, nil
	}
	var x struct {
		ID          uuid.UUID       `json:"request_id"`
		Title       string          `json:"title"`
		Description *string         `json:"description"`
		Schema      json.RawMessage `json:"schema"`
		Verify      bool            `json:"verify"`
		Status      string          `json:"status"`
		Business    string          `json:"business"`
		PublicView  *string         `json:"public_view"`
		ExpiresAt   time.Time       `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return FormRequest{}, false, err
	}
	r := FormRequest{ID: x.ID, Title: x.Title, Verify: x.Verify, Status: x.Status, Business: x.Business, ExpiresAt: x.ExpiresAt}
	if x.Description != nil {
		r.Description = *x.Description
	}
	if v, err := expr.DecodeJSON(x.Schema); err == nil {
		r.Schema, _ = v.(map[string]any)
	}
	if r.Schema == nil {
		r.Schema = map[string]any{}
	}
	if x.PublicView != nil {
		// Postgres's base64 breaks lines every 76 characters.
		b, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(*x.PublicView))
		if err != nil {
			return FormRequest{}, false, err
		}
		r.PublicView = b
	}
	return r, true, nil
}

func (d dbForms) Submit(ctx context.Context, w FormWrite) (bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inbox *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT taskiem_public_form_submit($1, $2, $3, $4, $5, $6, $7, $8)`,
		w.Endpoint, w.Request, w.Link, w.Once, w.Reference, w.Idem, w.StatusHash, w.Payload).Scan(&inbox)
	if err != nil {
		return false, submitError(err)
	}
	if inbox == nil {
		return false, nil
	}
	for _, u := range w.Uploads {
		if _, err := tx.Exec(ctx, `SELECT taskiem_public_form_upload($1, $2, $3)`, *inbox, u.Field, u.Data); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, submitError(err)
	}
	return true, nil
}

// submitError names the refusals taskiem_public_form_submit raises.
func submitError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return err
	}
	switch pe.ConstraintName {
	case "form_inbox_reference_key":
		return ErrReferenceTaken
	case "form_links_used":
		return ErrLinkUsed
	case "form_requests_answered":
		return ErrAnswered
	case "form_once_pkey":
		return ErrOnceSent
	case "form_inbox_idem_key":
		return ErrRepeat
	}
	return err
}

func (d dbForms) Code(ctx context.Context, scope uuid.UUID, addressHash, codeHash, payload []byte) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_code($1, $2, $3, $4)`, scope, addressHash, codeHash, payload).Scan(&ok)
	return ok, err
}

func (d dbForms) CheckCode(ctx context.Context, scope uuid.UUID, addressHash, codeHash []byte) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_code_check($1, $2, $3)`, scope, addressHash, codeHash).Scan(&ok)
	return ok, err
}

func (d dbForms) Status(ctx context.Context, reference string, statusHash []byte) (FormState, bool, error) {
	var st FormState
	var msg *string
	err := d.pool.QueryRow(ctx, `SELECT status, message, created_at FROM taskiem_public_form_status($1, $2) LIMIT 1`, reference, statusHash).
		Scan(&st.Status, &msg, &st.Created)
	if errors.Is(err, pgx.ErrNoRows) {
		return FormState{}, false, nil
	}
	if err != nil {
		return FormState{}, false, err
	}
	if msg != nil {
		st.Message = *msg
	}
	return st, true, nil
}

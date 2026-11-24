package api

import (
	"bytes"
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
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Customers' tools in the app (made tools T5): in a person's chat with a
// business, behind the tools button, the business's customers' tools
// (tools_customers.go). Each reaches the business through the customer
// door (customer.tools to list them, customer.tool for the rest), so a
// business that blocked the person is closed to them and every entry is
// in its chain. A start never runs anything: it shows what will run and
// waits for yes (the confirm), whatever the tool's care. The run is the
// person's as a customer (customer:<person>), never a member's, and its
// answer is drawn for them alone, from the public outputs.

// customerToolChannel is a customer's start in the app.
const customerToolChannel = "customer_app"

// customerToolPersonRoutes are the person's routes for customers' tools
// (personalAllowed).
var customerToolPersonRoutes = regexp.MustCompile(`^(GET /v1/me/chats/[^/]+/tools|POST /v1/me/chats/[^/]+/tools/[^/]+/(start|confirm)|` +
	`POST /v1/me/chats/[^/]+/tools/buttons/[^/]+|GET /v1/me/chats/[^/]+/runs/[^/]+/answer)$`)

func (s *Server) customerToolRoutes(r chi.Router) {
	r.Get("/me/chats/{id}/tools", s.doorTools)
	r.Post("/me/chats/{id}/tools/buttons/{token}", s.doorToolButton)
	r.Post("/me/chats/{id}/tools/{command}/start", s.doorToolStart)
	r.Post("/me/chats/{id}/tools/{command}/confirm", s.doorToolConfirm)
	r.Get("/me/chats/{id}/runs/{run}/answer", s.doorToolAnswer)
}

// doorToolLimited answers 429 when the person has asked too much of one
// chat's tools of late (what: start, answer, button), each its own limit:
// the door's checks and its chain are not for every tap. True when it did.
func (s *Server) doorToolLimited(w http.ResponseWriter, person uuid.UUID, chat, what string, every time.Duration, burst int) bool {
	if s.limiter("cust-tool-"+what+":"+person.String()+"/"+chat, every, burst).Allow() {
		return false
	}
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many tries just now. Try again in a while."})
	return true
}

// customerTool is a customers' tool as a customer sees it: no care, no
// roles, nothing of its steps.
type customerTool struct {
	Command string         `json:"command"`
	Name    string         `json:"name"`
	About   string         `json:"about,omitempty"`
	Icon    string         `json:"icon,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	// Answer is text, card or list when the tool answers; empty when it
	// declares no answer and tells only that it started.
	Answer string `json:"answer,omitempty"`
}

// customerToolsFit are the business's customers' tools that a customer
// may start: those whose steps move no money and whose inputs a form can
// carry.
func (s *Server) customerToolsFit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]toolRow, error) {
	all, err := customerToolsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []toolRow{}
	for _, t := range all {
		_, d, err := s.customerDefinition(ctx, tx, tenant, t)
		if notOffered(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if schema, _ := d.InputsSchema(); schema != nil {
			t.Inputs, _ = schema.(map[string]any)
		}
		out = append(out, t)
	}
	return out, nil
}

// doorToolFail answers a refused customers' tool.
func (s *Server) doorToolFail(w http.ResponseWriter, r *http.Request, err error) {
	var bad toolInputError
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the values do not fit the tool's form", "problems": []string(bad)})
	case notOffered(err):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This can't be done from here. Ask the business in the chat.", "not_offered": true})
	case errors.Is(err, errNotFound):
		writeErr(w, http.StatusNotFound, strings.TrimPrefix(err.Error(), errNotFound.Error()+": "))
	default:
		if _, ok := runtime.IsLimit(err); ok {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "This business is busy right now. Try again later."})
			return
		}
		s.personFail(w, r, err)
	}
}

// doorTools is GET /v1/me/chats/{id}/tools: the business's tools for its
// customers, with each one's form.
func (s *Server) doorTools(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []customerTool{}
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.tools", map[string]any{}, func(tx pgx.Tx, c personChat) error {
		tools, err := s.customerToolsFit(r.Context(), tx, c.Business)
		for _, t := range tools {
			out = append(out, customerTool{Command: t.Command, Name: t.Name, About: t.About, Icon: t.Icon, Inputs: t.Inputs, Answer: t.AnswerType})
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

// doorToolStart is POST /v1/me/chats/{id}/tools/{command}/start {input}:
// what will run, and the confirm to send back with the same values. It
// never starts a run.
func (s *Server) doorToolStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Input map[string]any `json:"input"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	command := chi.URLParam(r, "command")
	if s.doorToolLimited(w, person, chi.URLParam(r, "id"), "start", 6*time.Second, 10) {
		return
	}
	var out toolStart
	err = s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.tool", map[string]any{"tool": command, "step": "start"}, func(tx pgx.Tx, c personChat) error {
		t, err := customerToolTx(ctx, tx, command)
		if err != nil {
			return err
		}
		def, d, err := s.customerDefinition(ctx, tx, c.Business, t)
		if err != nil {
			return err
		}
		raw := canonicalJSON(req.Input)
		if probs := s.inputProblems(t.Workflow, t.Version, def, raw); len(probs) > 0 {
			return toolInputError(probs)
		}
		id, _ := uuid.NewV7()
		until := time.Now().Add(toolConfirmFor)
		if _, err := tx.Exec(ctx, `INSERT INTO tool_confirms (id, tenant_id, user_id, workflow_id, version, environment, command, care, input_digest, channel, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'prod', $6, 'yes', $7, $8, $9)`, id, c.Business, person, t.Workflow, t.Version, t.Command, confirmDigest(id, req.Input), customerToolChannel, until); err != nil {
			return err
		}
		// Customers always say yes, whatever the tool's care.
		out = toolStart{Care: wdcheck.CareYes, Confirm: &id, Expires: &until, Values: toolValues(d, req.Input)}
		return nil
	})
	if err != nil {
		s.doorToolFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// doorToolConfirm is POST /v1/me/chats/{id}/tools/{command}/confirm
// {confirm, input}: the run a start waited for, once, as the customer,
// under the menu's limits (three starts, then one every 20 minutes).
func (s *Server) doorToolConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Confirm uuid.UUID      `json:"confirm"`
		Input   map[string]any `json:"input"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	command := chi.URLParam(r, "command")
	var t toolRow
	var def []byte
	var business uuid.UUID
	var ran *uuid.UUID
	err = s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.tool", map[string]any{"tool": command, "step": "confirm"}, func(tx pgx.Tx, c personChat) error {
		business = c.Business
		var err error
		if t, err = customerToolTx(ctx, tx, command); err != nil {
			return err
		}
		var wf uuid.UUID
		var version int
		var digest []byte
		var expires time.Time
		err = tx.QueryRow(ctx, `SELECT workflow_id, version, input_digest, expires_at, run_id FROM tool_confirms
			WHERE id = $1 AND command = $2 AND environment = 'prod' AND channel = $3 AND user_id = $4`, req.Confirm, command, customerToolChannel, person).
			Scan(&wf, &version, &digest, &expires, &ran)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: that confirmation is not yours, or has gone; start the tool again", errNotFound)
		case err != nil:
			return err
		case ran != nil:
			return nil
		case time.Now().After(expires):
			return fmt.Errorf("%w: that confirmation has expired; start the tool again", errConflict)
		case wf != t.Workflow || version != t.Version:
			return fmt.Errorf("%w: the tool changed since you started it; start it again", errConflict)
		case !bytes.Equal(digest, confirmDigest(req.Confirm, req.Input)):
			return fmt.Errorf("%w: those are not the values you were shown; start the tool again", errConflict)
		}
		def, _, err = s.customerDefinition(ctx, tx, c.Business, t)
		return err
	})
	if err != nil {
		s.doorToolFail(w, r, err)
		return
	}
	if ran != nil {
		writeJSON(w, http.StatusOK, map[string]any{"run_id": *ran, "created": false})
		return
	}
	if !s.limiter("cust-tool-run:"+business.String()+"/"+person.String(), 20*time.Minute, 3).Allow() || !s.limiter("wa-pub-run-tenant:"+business.String(), 6*time.Second, 30).Allow() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many requests just now. Try again later."})
		return
	}
	run, created, err := s.startForCustomer(ctx, business, customerStart{tool: t, def: def, input: req.Input, actor: customerAppActor(person),
		shown: person.String(), kind: "person", channel: customerToolChannel, dedup: "customer-tool-confirm:" + req.Confirm.String()})
	if err != nil {
		s.doorToolFail(w, r, err)
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{business}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tool_confirms SET run_id = $2 WHERE id = $1 AND run_id IS NULL`, req.Confirm, run)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run_id": run, "created": created})
}

// doorToolAnswer is GET /v1/me/chats/{id}/runs/{run}/answer: the answer
// of a run the person started in this chat, from its public outputs.
func (s *Server) doorToolAnswer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	run, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// Asked again until the run ends: checked by the door (a block, the
	// chat), not written to the business's chain each time.
	if s.doorToolLimited(w, person, chi.URLParam(r, "id"), "answer", time.Second, 20) {
		return
	}
	var business uuid.UUID
	if err := s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.answer", map[string]any{"run": run}, func(_ pgx.Tx, c personChat) error {
		business = c.Business
		return nil
	}); err != nil {
		s.personFail(w, r, err)
		return
	}
	a, err := s.customerAnswer(ctx, business, run, customerAppActor(person), buttonRows)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// doorToolButton is POST /v1/me/chats/{id}/tools/buttons/{token}: a
// button on an answer the person was given, pressed: the customers' tool
// it opens and the values it fills in.
func (s *Server) doorToolButton(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this button is not available any more")
		return
	}
	if s.doorToolLimited(w, person, chi.URLParam(r, "id"), "button", 6*time.Second, 10) {
		return
	}
	var out pressed
	err = s.doorOnce(ctx, person, chi.URLParam(r, "id"), "customer.tool", map[string]any{"button": id, "step": "button"}, func(tx pgx.Tx, _ personChat) error {
		t, in, err := pressCustomerButton(ctx, tx, id, customerAppActor(person))
		out = pressed{Button: id, Command: t.Command, Name: t.Name, Inputs: in}
		return err
	})
	if err != nil {
		s.doorToolFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

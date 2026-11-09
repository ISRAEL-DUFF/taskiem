package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Talk to it here (decision 0062 V7, R10): a member writes to an assistant
// as a customer would, from the conversations page. The message goes
// through the assistant's guard (on a test channel of the member's own, so
// STOP, the notice and a hand-over can be tried) and starts a sandbox run
// of the version in the environment: everything is real (what it knows,
// the model, hearing and speaking) except what it would change, which is
// simulated. What it would have sent is recorded as the test chat's turns.

// testSender is the member's pretend customer: the same for each member,
// never a real number or chat id.
func testSender(actor string) string {
	sum := sha256.Sum256([]byte("taskiem/test-customer\x00" + actor))
	return "test-" + hex.EncodeToString(sum[:6])
}

// testChannelPrefix is the start of a member's test chats' channels
// (test/<their mark>.<workflow>/<env>): one per workflow, theirs only.
func testChannelPrefix(actor string) string {
	return "test/" + strings.TrimPrefix(testSender(actor), "test-") + "."
}

func testChannel(actor string, wf uuid.UUID, env string) string {
	return testChannelPrefix(actor) + wf.String()[:8] + "/" + env
}

func (s *Server) startTestTurn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workflow    uuid.UUID `json:"workflow"`
		Environment string    `json:"environment"`
		Text        string    `json:"text"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || utf8.RuneCountInString(req.Text) > 4000 {
		writeErr(w, http.StatusBadRequest, "write a message of 1 to 4,000 characters")
		return
	}
	p := principalFrom(r.Context())
	sealer, ok := any(s.Vault).(assistant.Sealer)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no vault on this deployment")
		return
	}
	var (
		a       assistantView
		cfg     assistant.Config
		version int
		d       assistant.Decision
		msg     assistant.Message
	)
	sender := testSender(p.Actor())
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		as, err := assistantsOf(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		found := false
		for _, x := range as {
			if x.Workflow == req.Workflow && (req.Environment == "" || x.Environment == req.Environment) {
				// The first published environment, dev before prod.
				if !found || x.Environment == "dev" {
					a, found = x, true
				}
			}
		}
		if !found {
			return errNotAssistant
		}
		var raw, def []byte
		if err := tx.QueryRow(ctx, `SELECT t.version, t.guard, v.definition FROM triggers t JOIN workflow_versions v ON v.workflow_id = t.workflow_id AND v.version = t.version
			WHERE t.tenant_id = $1 AND t.workflow_id = $2 AND t.environment = $3 AND t.connector = $4 AND t.type = 'connector_event' AND t.guard IS NOT NULL LIMIT 1`,
			p.TenantID, a.Workflow, a.Environment, a.Connector).Scan(&version, &raw, &def); err != nil {
			return err
		}
		// A test chat changes nothing and messages no one: a workflow that
		// would ask a person (an approval, a form, a callback) cannot be
		// tried here.
		if d, err := wd.Load(def); err == nil {
			var asks string
			d.Walk(func(st *wd.Step) {
				switch st.Type {
				case "approval", "form", "callback":
					asks = st.ID
				}
			})
			if asks != "" {
				return fmt.Errorf("%w: this assistant asks a person for something (step %s), which a test chat cannot do; try it with a real message instead", errBadRequest, asks)
			}
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if cfg, err = assistant.ParseConfig(m); err != nil {
			cfg = assistant.Defaults()
		}
		msg = assistant.Message{Channel: testChannel(p.Actor(), a.Workflow, a.Environment), Dedup: uuid.NewString(),
			Sender: sender, SenderCategory: "other", Private: true, Kind: "text", Text: req.Text}
		if d, err = (&assistant.Guard{Mode: s.AssistantMode}).Decide(ctx, tx, sealer, p.TenantID, cfg, msg); err != nil {
			return err
		}
		return assistant.RecordInbound(ctx, tx, sealer, p.TenantID, msg, d)
	})
	if errors.Is(err, errNotAssistant) {
		writeErr(w, http.StatusNotFound, "that workflow does not answer customers in any environment (its chat trigger has no guard, or it is not published)")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"channel": msg.Channel, "customer": d.Subject, "outcome": d.Outcome, "reason": d.Reason, "dropped": d.Dropped}
	if !d.Start {
		writeJSON(w, http.StatusOK, out)
		return
	}
	g := d.Map(msg, "text")
	g["test"] = true
	trig := map[string]any{"type": "connector_event", "connector": a.Connector, "event": "message", "test": true, "guard": g}
	switch {
	case strings.HasPrefix(a.Connector, "whatsapp@"):
		trig["trigger"] = "messages"
		trig["item"] = map[string]any{"kind": "message", "message": map[string]any{"from": sender, "id": "test." + msg.Dedup, "type": "text", "text": map[string]any{"body": req.Text}}}
		trig["body"] = map[string]any{}
	case strings.HasPrefix(a.Connector, "telegram@"):
		trig["trigger"] = "update"
		trig["body"] = map[string]any{"message": map[string]any{"chat": map[string]any{"id": sender, "type": "private"}, "text": req.Text}}
	}
	ref, _, err := s.Store.StartRun(r.Context(), runtime.StartRequest{
		TenantID: p.TenantID, WorkflowID: a.Workflow, Version: version, Environment: a.Environment,
		Trigger: trig, StartedBy: p.Actor(), TriggerID: "test-chat/" + a.Workflow.String(), DedupKey: msg.Dedup,
		Customer: &runtime.Customer{Category: "other", Value: sender}, Sandbox: true,
		Conversation: &runtime.ConversationRef{Channel: msg.Channel, Dedup: msg.Dedup},
		TriggerPII: []pii.Path{{Segments: []string{"guard", "sender"}, Category: "other"}, {Segments: []string{"guard", "text"}, Category: "other"},
			{Segments: []string{"item", "message", "from"}, Category: "other"}, {Segments: []string{"item", "message", "text", "body"}, Category: "other"},
			{Segments: []string{"body", "message", "text"}, Category: "other"}, {Segments: []string{"body", "message", "chat", "id"}, Category: "other"}},
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out["run"] = ref.ID
	writeJSON(w, http.StatusCreated, out)
}

var errNotAssistant = errors.New("not an assistant")

// getTestTurn is a test message's run and what it would have sent: the
// page asks until the run has ended.
func (s *Server) getTestTurn(w http.ResponseWriter, r *http.Request) {
	run, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such test message")
		return
	}
	p := principalFrom(r.Context())
	out := map[string]any{}
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status string
		var sandbox bool
		if err := tx.QueryRow(ctx, `SELECT status, sandbox FROM runs WHERE id = $1`, run).Scan(&status, &sandbox); err != nil {
			return err
		}
		if !sandbox {
			return pgx.ErrNoRows
		}
		channel, subject, root, ok, err := assistant.RunConversation(ctx, tx, p.TenantID, run)
		if err != nil {
			return err
		}
		// Only the member's own test chat.
		if !ok || !strings.HasPrefix(channel, testChannelPrefix(p.Actor())) {
			return pgx.ErrNoRows
		}
		turns, err := assistant.Conversation(ctx, tx, p.TenantID, channel, subject, nil, 500)
		if err != nil {
			return err
		}
		var views []turnView
		for _, t := range turns {
			if t.RunID == nil || *t.RunID != root || t.Role == assistant.RoleCustomer {
				continue
			}
			text, _ := s.openTurnText(ctx, tx, p.TenantID, t.Body, true)
			views = append(views, turnView{ID: t.ID, At: t.At, Role: t.Role, Kind: t.Kind, Text: text, Reason: t.Reason, File: t.File, RunID: t.RunID, Step: t.Step})
		}
		out["status"], out["ended"], out["turns"] = status, status == "completed" || status == "failed" || status == "cancelled", nonNil(views)
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no such test message")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// resetTestChat starts a member's test chat again: what was said, and
// STOP or a hand-over tried in it, are forgotten.
func (s *Server) resetTestChat(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	prefix := testChannelPrefix(p.Actor())
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if _, err := tx.Exec(ctx, `DELETE FROM assistant_turns WHERE tenant_id = $1 AND channel LIKE $2 || '%'`, p.TenantID, prefix); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM assistant_contacts WHERE tenant_id = $1 AND channel LIKE $2 || '%'`, p.TenantID, prefix)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

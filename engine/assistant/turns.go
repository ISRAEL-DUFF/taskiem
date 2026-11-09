package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
)

// The conversation record (decision 0062 V7, docs/conversations.md): every
// message from a customer, as it arrived and what the guard did with it,
// and every message sent to them, by the assistant's runs or a member by
// hand, with a voice note's transcript and the replies the check held
// back. Words are sealed under the customer's key: erasing the customer
// erases them.

// Who said it.
const (
	RoleCustomer  = "customer"
	RoleAssistant = "assistant" // a run sent it (an answer, a fixed reply)
	RoleMember    = "member"    // a person sent it by hand
	RoleHeld      = "held"      // a reply the check held back (never sent)
)

// Turn is one message of a conversation.
type Turn struct {
	ID         uuid.UUID  `json:"id"`
	At         time.Time  `json:"at"`
	Role       string     `json:"role"`
	Kind       string     `json:"kind"`
	Body       any        `json:"text,omitempty"`       // sealed until opened
	Transcript any        `json:"transcript,omitempty"` // sealed until opened
	File       any        `json:"file,omitempty"`
	Status     string     `json:"status,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	RunID      *uuid.UUID `json:"run_id,omitempty"`
	Step       string     `json:"step,omitempty"`
	By         string     `json:"by,omitempty"`
}

// sealText seals words under the customer, or nothing for none.
func sealText(ctx context.Context, tx pgx.Tx, sealer Sealer, tenant uuid.UUID, subject, text string) (any, error) {
	if text == "" || sealer == nil {
		return nil, nil
	}
	return sealer.SealUnderTx(ctx, tx, tenant, subject, "other", text)
}

// RecordInbound records a customer's message and what the guard did with
// it, once per delivery.
func RecordInbound(ctx context.Context, tx pgx.Tx, sealer Sealer, tenant uuid.UUID, m Message, d Decision) error {
	if d.Subject == "" {
		return nil // not a customer's message
	}
	body, err := sealText(ctx, tx, sealer, tenant, d.Subject, m.Text)
	if err != nil {
		return err
	}
	status, reason := d.Outcome, d.Reason
	if !d.Start {
		status, reason = "dropped", d.Dropped
	}
	var dedup *string
	if m.Dedup != "" {
		dedup = &m.Dedup
	}
	tag, err := tx.Exec(ctx, `INSERT INTO assistant_turns (id, tenant_id, channel, subject, role, kind, body, status, reason, dedup)
		VALUES ($1, $2, $3, $4, 'customer', $5, $6, $7, NULLIF($8, ''), $9) ON CONFLICT (tenant_id, channel, dedup) WHERE dedup IS NOT NULL DO NOTHING`,
		uuid.Must(uuid.NewV7()), tenant, m.Channel, d.Subject, kindOr(m.Kind), body, status, reason, dedup)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return moved(ctx, tx, tenant, m.Channel, d.Subject, RoleCustomer)
}

// moved records when a customer's conversation last moved, and who spoke,
// for the customer list.
func moved(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject, role string) error {
	_, err := tx.Exec(ctx, `UPDATE assistant_contacts SET last_turn_at = now(), last_role = $4 WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
		tenant, channel, subject, role)
	return err
}

func kindOr(k string) string {
	if k == "" {
		return "text"
	}
	return k
}

// AttachInbound ties a customer's message to the run it started, and to
// the voice note fetched for it.
func AttachInbound(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, dedup string, run *uuid.UUID, file any) error {
	if dedup == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE assistant_turns SET run_id = COALESCE($4, run_id), file = COALESCE($5, file)
		WHERE tenant_id = $1 AND channel = $2 AND dedup = $3`, tenant, channel, dedup, run, fileRef(file))
	return err
}

// Sent is a message sent to a customer, to record.
type Sent struct {
	Role      string // RoleAssistant, RoleMember or RoleHeld
	Kind      string
	Text      string
	File      any
	Reason    string     // a held reply's problems
	Run       *uuid.UUID // the run the customer's message started
	StepRun   *uuid.UUID // the run whose step sent it (that run, or a child of it)
	Step      string
	By        string
	RequestID string // a member's: the same request records (and sends) once
	Status    string // a member's: sending, sent, failed, unknown
}

// ErrDuplicate: the turn was recorded already (a member's request sent
// twice, a step's result written twice).
var ErrDuplicate = errors.New("assistant: already recorded")

// RecordSent records a message sent to a customer (or held back).
func RecordSent(ctx context.Context, tx pgx.Tx, sealer Sealer, tenant uuid.UUID, channel, subject string, s Sent) error {
	body, err := sealText(ctx, tx, sealer, tenant, subject, s.Text)
	if err != nil {
		return err
	}
	var req *string
	if s.RequestID != "" {
		req = &s.RequestID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO assistant_turns (id, tenant_id, channel, subject, role, kind, body, file, reason, run_id, step_run, step, by_actor, request_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10, $11, NULLIF($12, ''), NULLIF($13, ''), $14, NULLIF($15, '')) ON CONFLICT DO NOTHING`,
		uuid.Must(uuid.NewV7()), tenant, channel, subject, s.Role, kindOr(s.Kind), body, fileRef(s.File), s.Reason, s.Run, s.StepRun, s.Step, s.By, req, s.Status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrDuplicate
	}
	if err != nil || s.Role == RoleHeld {
		return err
	}
	return moved(ctx, tx, tenant, channel, subject, s.Role)
}

// fileRef keeps a run file's reference without its name (a customer's
// file name is personal): its id, type and size.
func fileRef(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"file", "type", "size"} {
		if x, ok := m[k]; ok {
			out[k] = x
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SetTranscript records the words heard in the voice note a run's customer
// sent (the run, or the run it is a child of, that the message started).
func SetTranscript(ctx context.Context, tx pgx.Tx, sealer Sealer, tenant, run uuid.UUID, text string) error {
	channel, subject, root, ok, err := RunConversation(ctx, tx, tenant, run)
	if err != nil || !ok {
		return err
	}
	sealed, err := sealText(ctx, tx, sealer, tenant, subject, text)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE assistant_turns SET transcript = $4 WHERE tenant_id = $1 AND run_id = $2 AND channel = $3 AND role = 'customer'`,
		tenant, root, channel, sealed)
	return err
}

// RunConversation finds the conversation a run belongs to: the customer
// message that started it, or started the run it is a child of.
func RunConversation(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID) (channel, subject string, root uuid.UUID, ok bool, err error) {
	err = tx.QueryRow(ctx, `WITH RECURSIVE up AS (
			SELECT id, parent_run_id, 0 AS depth FROM runs WHERE tenant_id = $1 AND id = $2
			UNION ALL
			SELECT r.id, r.parent_run_id, up.depth + 1 FROM runs r JOIN up ON r.id = up.parent_run_id WHERE up.depth < 8)
		SELECT t.channel, t.subject, t.run_id FROM up JOIN assistant_turns t ON t.tenant_id = $1 AND t.run_id = up.id AND t.role = 'customer'
		ORDER BY up.depth LIMIT 1`, tenant, run).Scan(&channel, &subject, &root)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", uuid.Nil, false, nil
	}
	return channel, subject, root, err == nil, err
}

// Conversation is a customer's turns on a channel, oldest first: the last
// limit of them (before, when set, pages back).
// Before is a page's edge: the turns before this one (at, then id, so
// turns of one moment are neither skipped nor shown twice). A zero ID
// means everything before At.
type Before struct {
	At time.Time
	ID uuid.UUID
}

func Conversation(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string, before *Before, limit int) ([]Turn, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var at *time.Time
	var id uuid.UUID
	if before != nil {
		at, id = &before.At, before.ID
	}
	rows, err := tx.Query(ctx, `SELECT id, at, role, kind, body, transcript, file, COALESCE(status, ''), COALESCE(reason, ''), run_id, COALESCE(step, ''), COALESCE(by_actor, '')
		FROM (SELECT * FROM assistant_turns WHERE tenant_id = $1 AND channel = $2 AND subject = $3 AND ($4::timestamptz IS NULL OR (at, id) < ($4, $6))
		      ORDER BY at DESC, id DESC LIMIT $5) t ORDER BY at, id`, tenant, channel, subject, at, limit, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Turn{}
	for rows.Next() {
		var t Turn
		var body, transcript, file []byte
		if err := rows.Scan(&t.ID, &t.At, &t.Role, &t.Kind, &body, &transcript, &file, &t.Status, &t.Reason, &t.RunID, &t.Step, &t.By); err != nil {
			return nil, err
		}
		t.Body, t.Transcript, t.File = decode(body), decode(transcript), decode(file)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ExpireTurns deletes what was said before the business's conversation
// days, for one business; it answers how many went.
func ExpireTurns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `DELETE FROM assistant_turns WHERE tenant_id = $1 AND at < now() - make_interval(days => $2)`, tenant, days)
	return int(tag.RowsAffected()), err
}

// EraseTurns deletes a customer's conversations, on every channel (their
// words are unreadable once their key is gone; the rows go too).
func EraseTurns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string) (int, error) {
	base := pii.BaseSubject(subject)
	tag, err := tx.Exec(ctx, `DELETE FROM assistant_turns WHERE tenant_id = $1 AND subject ~>=~ $2 AND subject ~<~ ($2 || chr(127))`, tenant, base)
	return int(tag.RowsAffected()), err
}

func decode(raw []byte) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// Gathered is what gather answers a run (decision 0062 V10, L5): a
// customer who sends several short messages in a row gets one answer.
type Gathered struct {
	// Latest: no newer message of theirs is waiting for an answer (nor a
	// STOP or a request for a person after it), so this run answers;
	// otherwise the run for the newer one does, or nothing should.
	Latest bool
	// Earlier are their messages before this run's own that no answer has
	// taken yet, oldest first (at most 20, of the last quarter of an hour):
	// what this answer must also answer. A voice note no run heard is said
	// as such.
	Earlier []string
}

// unheardVoice stands for an earlier voice note no run transcribed.
const unheardVoice = "(a voice note the assistant has not listened to)"

// Gather is what the run's customer wrote that no answer has taken yet,
// and whether this run's message is their latest. A run that answers
// (Latest) takes its message and the earlier ones: it marks them
// (step_run), so a later run never answers them again, and a message
// that arrives while this answer is being written is still gathered by
// the run after it. A run that belongs to no conversation is its own
// latest, with nothing earlier.
func (s *Service) Gather(ctx context.Context, tenant, run uuid.UUID) (Gathered, error) {
	out := Gathered{Latest: true}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		channel, subject, root, ok, err := RunConversation(ctx, tx, tenant, run)
		if err != nil || !ok {
			return err
		}
		var mine time.Time
		var id uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id, at FROM assistant_turns WHERE tenant_id = $1 AND run_id = $2 AND role = 'customer' ORDER BY at LIMIT 1`, tenant, root).Scan(&id, &mine)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// One customer's runs gather one at a time.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('assistant-gather:' || $1::text || $2 || $3))`, tenant, channel, subject); err != nil {
			return err
		}
		// A newer message that a live run will answer, or a STOP or a
		// request for a person after this one (nothing more is said then).
		var newer bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assistant_turns t LEFT JOIN runs r ON r.id = t.run_id
			WHERE t.tenant_id = $1 AND t.channel = $2 AND t.subject = $3 AND t.role = 'customer' AND (t.at, t.id) > ($4, $5)
			  AND (t.status = 'reply' AND t.reason IN ('stopped', 'handover')
			    OR t.status = 'answer' AND (r.status IS NULL AND t.run_id IS NULL AND t.at > now() - interval '30 seconds'
			      OR r.status IS NOT NULL AND r.status NOT IN ('failed', 'cancelled'))))`, tenant, channel, subject, mine, id).Scan(&newer); err != nil {
			return err
		}
		out.Latest = !newer
		if !out.Latest {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id, kind, body, transcript FROM (
			SELECT id, at, kind, body, transcript FROM assistant_turns WHERE tenant_id = $1 AND channel = $2 AND subject = $3
			  AND role = 'customer' AND status = 'answer' AND step_run IS NULL AND (at, id) < ($4, $5) AND at > $4 - interval '15 minutes'
			ORDER BY at DESC, id DESC LIMIT 20) t ORDER BY at, id`, tenant, channel, subject, mine, id)
		if err != nil {
			return err
		}
		type turn struct {
			id               uuid.UUID
			kind             string
			body, transcript []byte
		}
		var list []turn
		for rows.Next() {
			var t turn
			if err := rows.Scan(&t.id, &t.kind, &t.body, &t.transcript); err != nil {
				rows.Close()
				return err
			}
			list = append(list, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		taken := []uuid.UUID{id}
		for _, t := range list {
			taken = append(taken, t.id)
			said := ""
			for _, raw := range [][]byte{t.transcript, t.body} {
				env, _ := decode(raw).(map[string]any)
				if env == nil {
					continue
				}
				v, err := pii.Open(ctx, s.Vault, tx, tenant, env, pii.Taint{})
				if err != nil || v == pii.Erased {
					continue
				}
				if text, _ := v.(string); strings.TrimSpace(text) != "" {
					said = text
					break
				}
			}
			if said == "" && (t.kind == "voice" || t.kind == "audio") {
				said = unheardVoice
			}
			if said != "" {
				out.Earlier = append(out.Earlier, said)
			}
		}
		// This run answers them: no later run answers them again.
		_, err = tx.Exec(ctx, `UPDATE assistant_turns SET step_run = $3 WHERE tenant_id = $1 AND id = ANY($2) AND step_run IS NULL`, tenant, taken, run)
		return err
	})
	return out, err
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Safety in people chats, the person's side (docs/spec/people-chatting.md
// §8, "As built: stage 5a gaps, safety and moderation"): what moderators
// decided about the person (their standing), their appeals, the abuse
// signals a sender's limits weigh, the messages the checks hold, kept
// sealed for moderators, and the evidence a report keeps. The operator's
// side is ops_people.go; lawful requests are lawful.go.

// safetyPersonRoutes are the routes of this file a personal session
// reaches (personalAllowed).
var safetyPersonRoutes = regexp.MustCompile(`^(GET /v1/me/standing|POST /v1/me/standing/(appeals|seen))$`)

func (s *Server) peopleSafetyRoutes(r chi.Router) {
	r.Get("/me/standing", s.myStanding)
	r.Post("/me/standing/appeals", s.appealStanding)
	r.Post("/me/standing/seen", s.seenStanding)
}

// errPeopleYouSuspended answers a suspended person who writes to someone.
var errPeopleYouSuspended = customerSendErr{http.StatusForbidden,
	"Taskiem's moderators have stopped your account from messaging people for now. See Your standing in Settings to find out why and to appeal."}

// standing is what is in force of the moderators' actions on a person.
type standing struct {
	Suspended, Closing, Limited bool
}

// standingTx reads the person's actions in force, in their own
// transaction.
func standingTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (standing, error) {
	var st standing
	// A closure is in force until the account is deleted (one whose
	// deletion failed stays so until it is carried out).
	rows, err := tx.Query(ctx, `SELECT action FROM people_moderation WHERE person_id = $1 AND lifted_at IS NULL
		AND action IN ('limit', 'suspend', 'close') AND (action = 'close' OR (closed_at IS NULL AND (until IS NULL OR until > now())))`, person)
	if err != nil {
		return st, err
	}
	acts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return st, err
	}
	for _, a := range acts {
		switch a {
		case peoplesafety.ActionLimit:
			st.Limited = true
		case peoplesafety.ActionSuspend:
			st.Suspended = true
		case peoplesafety.ActionClose:
			st.Suspended, st.Closing = true, true
		}
	}
	return st, nil
}

// notSuspendedTx refuses a person whom Taskiem's moderators stopped from
// messaging people (a suspension or a closure in force), or whose account
// is not active, with the words that send them to Your standing: for
// what reaches the other's copy of a chat (a reaction, marking a share
// paid, accepting a request).
func notSuspendedTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	st, err := standingTx(ctx, tx, person)
	if err != nil {
		return err
	}
	if st.Suspended {
		return errPeopleYouSuspended
	}
	return nil
}

// senderSignalsTx gathers what the sender's request allowance weighs.
func senderSignalsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, st standing) (peoplesafety.Signals, error) {
	sig := peoplesafety.Signals{Limited: st.Limited}
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1`, person).Scan(&created); err != nil {
		return sig, err
	}
	sig.Age = time.Since(created)
	if err := tx.QueryRow(ctx, `SELECT accepted, refused FROM taskiem_people_request_counts()`).Scan(&sig.Accepted, &sig.Refused); err != nil {
		return sig, err
	}
	err := tx.QueryRow(ctx, `SELECT reported_by, device_accounts FROM taskiem_people_sender_signals()`).Scan(&sig.ReportedBy, &sig.DeviceAccounts)
	return sig, err
}

// heldContent is what a held message keeps, sealed: what the sender
// wrote, as peopleSend would have kept it.
type heldContent struct {
	Body     string          `json:"body,omitempty"`
	Card     *peopleCard     `json:"card,omitempty"`
	Location *peopleLocation `json:"location,omitempty"`
	Name     string          `json:"name,omitempty"`
}

// holdTx keeps a message the checks held, sealed under a key of its own,
// with its verdict's reasons, for moderators to release or confirm
// (§8.4). The recipient gets nothing.
func (s *Server) holdTx(ctx context.Context, tx pgx.Tx, me, peer, chat uuid.UUID, d peopleDraft, reasons []string) error {
	return s.keepHeldTx(ctx, tx, me, &peer, &chat, nil, d, nil, reasons)
}

// keepHeldTx keeps a held message: of a one-to-one chat (peer, the
// sender's copy chat) or of a group (group, with the mentions).
func (s *Server) keepHeldTx(ctx context.Context, tx pgx.Tx, me uuid.UUID, peer, chat, group *uuid.UUID, d peopleDraft, mentions []uuid.UUID, reasons []string) error {
	if s.Vault == nil {
		return nil
	}
	id := uuid.Must(uuid.NewV7())
	box, err := peoplesafety.NewBox(ctx, s.Vault, peoplesafety.HeldPurpose(id), id)
	if err != nil {
		return err
	}
	c := heldContent{Body: d.body, Card: d.card, Location: d.location}
	var fileSealed []byte
	var fileType *string
	var fileSize *int64
	if d.file != nil {
		c.Name = d.file.Name
		if fileSealed, err = box.Seal(peoplesafety.FileRow(id), d.file.Data); err != nil {
			return err
		}
		n := int64(len(d.file.Data))
		fileType, fileSize = &d.file.Type, &n
	}
	raw, _ := json.Marshal(c)
	sealed, err := box.Seal(peoplesafety.MessagesRow, raw)
	if err != nil {
		return err
	}
	if reasons == nil {
		reasons = []string{}
	}
	if mentions == nil {
		mentions = []uuid.UUID{}
	}
	_, err = tx.Exec(ctx, `INSERT INTO people_held (id, sender_id, recipient_id, chat_id, kind, reply_to, forwards, reasons, sealed, file_sealed, file_type,
		file_size, box_key, kms_key, group_id, mentions) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		id, me, peer, chat, d.kind, d.replyTo, d.forwards, reasons, sealed, fileSealed, fileType, fileSize, box.Sealed, box.KMSKey, group, mentions)
	return err
}

// reportMessage is one message of a report, as moderators read it.
type reportMessage struct {
	ID           *uuid.UUID `json:"id,omitempty"`
	FromReporter bool       `json:"from_reporter"`
	// SenderID and Sender: who sent it, in a group's report (their own
	// name; "The reporter" for the reporter).
	SenderID   *uuid.UUID      `json:"sender_id,omitempty"`
	Sender     string          `json:"sender,omitempty"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Card       *peopleCard     `json:"card,omitempty"`
	Location   *peopleLocation `json:"location,omitempty"`
	Attachment map[string]any  `json:"attachment,omitempty"`
	Deleted    bool            `json:"deleted,omitempty"`
	// File: the photo or file is kept with the report (FileLeftOut: it
	// was not, the report's files being full).
	File        bool      `json:"file,omitempty"`
	FileLeftOut bool      `json:"file_left_out,omitempty"`
	At          time.Time `json:"at"`
}

// reportTx sends the chat's last 20 messages of the person's copy, opened,
// to Taskiem's moderators (§8.3): sealed under the report's own key, with
// the photos and files in them copied and sealed with it (up to
// ReportFilesMax, the newest first), so the report keeps its evidence
// after the chat or either account goes.
func (s *Server) reportTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, c peopleCopy, in reportInput) (uuid.UUID, error) {
	key, err := s.keys().KeyTx(ctx, tx, person, false)
	if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
		return uuid.Nil, err
	}
	rows, err := tx.Query(ctx, `SELECT m.id, m.mine, m.kind, m.sealed, m.attachment, m.deleted_at IS NOT NULL, m.created_at, f.sealed IS NOT NULL, f.type, f.size
		FROM people_messages m LEFT JOIN people_files f ON f.person_id = m.person_id AND f.message_id = m.id
		WHERE m.person_id = $1 AND m.chat_id = $2 ORDER BY m.created_at DESC, m.id DESC LIMIT 20`, person, c.ID)
	if err != nil {
		return uuid.Nil, err
	}
	type fileRef struct {
		id   uuid.UUID
		typ  string
		size int64
	}
	var files []fileRef
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (reportMessage, error) {
		var m reportMessage
		var id uuid.UUID
		var sealed []byte
		var hasFile bool
		var ftype *string
		var fsize *int64
		if err := r.Scan(&id, &m.FromReporter, &m.Kind, &sealed, &m.Attachment, &m.Deleted, &m.At, &hasFile, &ftype, &fsize); err != nil {
			return m, err
		}
		m.ID = &id
		if key != nil {
			if v, err := openSealed(key, person, id, sealed); err == nil {
				m.Body, m.Card, m.Location = v.Body, v.Card, v.Location
				if m.Attachment != nil && v.Name != "" {
					m.Attachment["name"] = v.Name
				}
			}
		}
		if hasFile && !m.Deleted && ftype != nil && fsize != nil {
			files = append(files, fileRef{id, *ftype, *fsize})
		}
		return m, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.Must(uuid.NewV7())
	box, err := peoplesafety.NewBox(ctx, s.Vault, peoplesafety.ReportPurpose(id), id)
	if err != nil {
		return uuid.Nil, err
	}
	// The files, newest first, up to the report's room.
	kept := map[uuid.UUID]bool{}
	var room int64 = peoplesafety.ReportFilesMax
	type copied struct {
		ref    fileRef
		sealed []byte
	}
	var out []copied
	for _, f := range files {
		if f.size > room || key == nil {
			continue
		}
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT sealed FROM people_files WHERE person_id = $1 AND message_id = $2`, person, f.id).Scan(&sealed); err != nil {
			return uuid.Nil, err
		}
		data, err := peoplechats.Open(key, person, fileRow(f.id), sealed)
		if err != nil {
			continue
		}
		again, err := box.Seal(peoplesafety.FileRow(f.id), data)
		if err != nil {
			return uuid.Nil, err
		}
		room -= f.size
		kept[f.id] = true
		out = append(out, copied{f, again})
	}
	for i := range msgs {
		if msgs[i].Attachment != nil && !msgs[i].Deleted {
			msgs[i].File = kept[*msgs[i].ID]
			msgs[i].FileLeftOut = !msgs[i].File
		}
	}
	if msgs == nil {
		msgs = []reportMessage{}
	}
	raw, _ := json.Marshal(msgs)
	sealed, err := box.Seal(peoplesafety.MessagesRow, raw)
	if err != nil {
		return uuid.Nil, err
	}
	// The reporter's keyed mark (one person counts once, even after they
	// delete their account) and whether the reported person ever wrote to
	// them (a report of someone who never did weighs nothing on their
	// allowance: taskiem_people_sender_signals).
	if _, err = tx.Exec(ctx, `INSERT INTO people_reports (id, person_id, reported_id, reason, note, consent, messages, from_request, sealed, box_key, kms_key, files,
		reporter_mark, peer_wrote) VALUES ($1, $2, $3, $4, $5, true, '[]', $6, $7, $8, $9, $10, $11, $12)`,
		id, person, c.Peer, in.Reason, in.Note, c.RequestAt != nil, sealed, box.Sealed, box.KMSKey, len(out), s.reporterMark(person), c.PeerWroteAt != nil); err != nil {
		return uuid.Nil, err
	}
	for _, f := range out {
		if _, err := tx.Exec(ctx, `INSERT INTO people_report_files (report_id, message_id, reporter_id, type, size, sealed) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, f.ref.id, person, f.ref.typ, f.ref.size, f.sealed); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

// --- the person's standing ---

// standingAction is one of the moderators' actions on the person, as
// they are shown it.
type standingAction struct {
	ID       uuid.UUID  `json:"id"`
	Action   string     `json:"action"`
	Reason   string     `json:"reason"`
	At       time.Time  `json:"at"`
	Until    *time.Time `json:"until"`
	InForce  bool       `json:"in_force"`
	LiftedAt *time.Time `json:"lifted_at"`
	Seen     bool       `json:"seen"`
	// Words say what it means, in plain words.
	Words     string          `json:"words"`
	CanAppeal bool            `json:"can_appeal"`
	Appeal    *standingAppeal `json:"appeal"`
}

type standingAppeal struct {
	ID        uuid.UUID  `json:"id"`
	Text      string     `json:"text"`
	State     string     `json:"state"`
	At        time.Time  `json:"at"`
	DecidedAt *time.Time `json:"decided_at"`
	Note      *string    `json:"note"`
}

// actionWords is what an action means for the person.
func moderationWords(action string, until *time.Time) string {
	day := "until Taskiem lifts it"
	if until != nil {
		day = "until " + until.In(lagos()).Format("Mon 2 Jan 2006")
	}
	switch action {
	case peoplesafety.ActionWarn:
		return "Taskiem's moderators warned you about how you message people. Keep to the rules for chats with people, or your account may be limited or suspended."
	case peoplesafety.ActionLimit:
		return "You can send only a few message requests to people you don't know, " + day + "."
	case peoplesafety.ActionSuspend:
		return "Your account can't message people or get messages from them, " + day + ". You can still read your chats."
	case peoplesafety.ActionClose:
		if until != nil {
			return "Taskiem will close your account on " + until.In(lagos()).Format("Mon 2 Jan 2006") + ". Until then it can't message people. If you think this is a mistake, appeal before then."
		}
		return "Taskiem is closing your account."
	}
	return ""
}

// myStanding is GET /v1/me/standing: what Taskiem's moderators decided
// about the person, in force or not, with their appeals.
func (s *Server) myStanding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var acts []standingAction
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.id, m.action, m.reason, m.created_at, m.until, m.lifted_at, m.lifted_at IS NULL
			AND (m.action = 'close' OR (m.closed_at IS NULL AND (m.action = 'warn' OR m.until IS NULL OR m.until > now()))), m.seen_at IS NOT NULL,
			a.id, a.text, a.state, a.created_at, a.decided_at, a.decision_note
			FROM people_moderation m LEFT JOIN people_appeals a ON a.action_id = m.id
			WHERE m.person_id = $1 ORDER BY m.created_at DESC LIMIT 50`, person)
		if err != nil {
			return err
		}
		acts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (standingAction, error) {
			var a standingAction
			var apID *uuid.UUID
			var apText, apState *string
			var apAt *time.Time
			var ap standingAppeal
			if err := r.Scan(&a.ID, &a.Action, &a.Reason, &a.At, &a.Until, &a.LiftedAt, &a.InForce, &a.Seen, &apID, &apText, &apState, &apAt, &ap.DecidedAt, &ap.Note); err != nil {
				return a, err
			}
			if apID != nil {
				ap.ID, ap.Text, ap.State, ap.At = *apID, *apText, *apState, *apAt
				a.Appeal = &ap
			}
			// A warning stays in force for 30 days, as shown.
			if a.Action == peoplesafety.ActionWarn && a.InForce && time.Since(a.At) > 30*24*time.Hour {
				a.InForce = false
			}
			a.Words = moderationWords(a.Action, a.Until)
			a.CanAppeal = a.Appeal == nil && a.LiftedAt == nil && (a.InForce || a.Action == peoplesafety.ActionWarn) &&
				(a.Action != peoplesafety.ActionClose || a.Until == nil || a.Until.After(time.Now()))
			return a, nil
		})
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, standingOut(acts))
}

// standingOut is the person's standing from their actions: the strongest
// in force names it.
func standingOut(acts []standingAction) map[string]any {
	state, words := "ok", "Your account is in good standing."
	var until *time.Time
	unseen := 0
	rank := map[string]int{"warned": 1, "limited": 2, "suspended": 3, "closing": 4}
	for _, a := range acts {
		if !a.Seen {
			unseen++
		}
		if !a.InForce {
			continue
		}
		st := map[string]string{peoplesafety.ActionWarn: "warned", peoplesafety.ActionLimit: "limited", peoplesafety.ActionSuspend: "suspended",
			peoplesafety.ActionClose: "closing"}[a.Action]
		if rank[st] > rank[state] {
			state, words, until = st, a.Words, a.Until
		}
	}
	if acts == nil {
		acts = []standingAction{}
	}
	return map[string]any{"state": state, "words": words, "until": until, "actions": acts, "unseen": unseen,
		"appeal_words": "If you think a decision is wrong, tell us why. A different moderator reads your appeal, and we tell you what they decide."}
}

// appealStanding is POST /v1/me/standing/appeals {action_id, text}: the
// person appeals one decision, once.
func (s *Server) appealStanding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		ActionID uuid.UUID `json:"action_id"`
		Text     string    `json:"text"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || utf8.RuneCountInString(in.Text) > 2000 {
		writeErr(w, http.StatusBadRequest, "Say why you think the decision is wrong, in up to 2,000 characters.")
		return
	}
	if !s.limiter("people-appeal:"+person.String(), time.Hour, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Try again later.")
		return
	}
	var out standingAppeal
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		// The decision's lock (as the closure's carrying out and the
		// moderators' lift take it), so an appeal made as the closing day
		// comes either holds the closure or is refused.
		var action string
		var lifted, closed, until *time.Time
		if err := tx.QueryRow(ctx, `SELECT action, until, lifted_at, closed_at FROM people_moderation WHERE id = $1 AND person_id = $2 FOR UPDATE`,
			in.ActionID, person).Scan(&action, &until, &lifted, &closed); err != nil {
			return err
		}
		if lifted != nil || closed != nil {
			return customerSendErr{http.StatusConflict, "This decision is no longer in force, so there is nothing to appeal."}
		}
		if until != nil && !until.After(time.Now()) {
			if action == peoplesafety.ActionClose {
				return customerSendErr{http.StatusConflict, "The time to appeal the closing of your account has ended."}
			}
			if action != peoplesafety.ActionWarn {
				return customerSendErr{http.StatusConflict, "This decision is no longer in force, so there is nothing to appeal."}
			}
		}
		out = standingAppeal{ID: uuid.Must(uuid.NewV7()), Text: in.Text, State: "open"}
		if err := tx.QueryRow(ctx, `INSERT INTO people_appeals (id, action_id, person_id, text) VALUES ($1, $2, $3, $4) RETURNING created_at`,
			out.ID, in.ActionID, person, in.Text).Scan(&out.At); err != nil {
			return err
		}
		// In the platform chain, beside the decision appealed.
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(ops.PlatformChain)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'person', $2, 'people.appeal', $3, jsonb_build_object('appeal', $4::text))`,
			ops.PlatformChain, person.String(), in.ActionID.String(), out.ID.String())
		return err
	})
	if isUnique(err) {
		writeErr(w, http.StatusConflict, "You already appealed this decision. We tell you when a moderator decides.")
		return
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"appeal": out, "said": "Thank you. A moderator will read your appeal, and we will tell you what they decide."})
}

// seenStanding is POST /v1/me/standing/seen: the person has seen the
// moderators' notices (You stops showing them as new).
func (s *Server) seenStanding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE people_moderation SET seen_at = now() WHERE person_id = $1 AND seen_at IS NULL`, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tellStanding tells the person of a moderator's decision on their own
// devices (a push on You opening Your standing); the standing itself is
// the notice You shows.
func (s *Server) tellStanding(ctx context.Context, person uuid.UUID, title, body string) {
	s.tellYou(ctx, person, title, body, standingLink, "standing")
}

// standingLink is where the person reads their standing.
const standingLink = "/you/settings/standing"

// --- the safety job ---

// SafetyTick seals reports kept plain before 00480, ends suspensions
// whose time ran out, closes accounts whose closure came with no open
// appeal, and purges reviewed reports and held messages past their
// retention (peoplesafety.ReportsKept, HeldKept).
func (s *Server) SafetyTick(ctx context.Context) error {
	var errs []error
	if err := s.sealOldReports(ctx); err != nil {
		errs = append(errs, err)
	}
	var due [][2]uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action_id, person_id FROM taskiem_people_standing_tick()`)
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]uuid.UUID, error) {
			var a [2]uuid.UUID
			return a, r.Scan(&a[0], &a[1])
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT what, n FROM taskiem_people_safety_purge(make_interval(secs => $1), make_interval(secs => $2))`,
			peoplesafety.ReportsKept.Seconds(), peoplesafety.HeldKept.Seconds())
		if err != nil {
			return err
		}
		purged := map[string]int64{}
		for rows.Next() {
			var what string
			var n int64
			if err := rows.Scan(&what, &n); err != nil {
				return err
			}
			if n > 0 {
				purged[what] = n
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(purged) > 0 {
			return ops.AuditTx(ctx, tx, "taskiem:safety", "people.safety.purge", "retention", map[string]any{"purged": purged})
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	for _, d := range due {
		if err := s.closeAccount(ctx, d[0], d[1]); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.closeGroupsDue(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.sweepPeople(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// closeAccount carries out one closure that came due, all in one
// transaction: the closure is locked and checked still due
// (taskiem_people_closure_due: a lift, an appeal or its decision made
// meanwhile wins), the account deleted as the person would delete it, and
// the closure recorded done, audited people.account.close. If the
// deletion fails nothing of it is kept: the closure stays in force (the
// person stays unable to message anyone), the failure is audited
// people.account.close.failed, and the next tick tries again.
func (s *Server) closeAccount(ctx context.Context, action, person uuid.UUID) error {
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(ops.PlatformChain)); err != nil {
			return err
		}
		// The closure's lock before the account's: an appeal holds the
		// closure's lock while its row points at the account (a key share
		// of it), so the other order would wait on each other.
		var due bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_people_closure_due($1)`, action).Scan(&due); err != nil || !due {
			return err
		}
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM users WHERE id = $1 FOR UPDATE`, person).Scan(&deleted); err != nil {
			return err
		}
		note := "closed"
		if deleted != nil {
			note = "the account was already deleted"
		} else if _, err := s.deletePersonTx(ctx, tx, person, "closed_by_moderators", ""); err != nil {
			return err
		}
		// deletePersonTx keeps the platform chain in scope.
		if _, err := tx.Exec(ctx, `SELECT taskiem_people_closure_done($1, $2)`, action, note); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, "taskiem:safety", "people.account.close", person.String(), map[string]any{"action": action, "outcome": note})
	})
	if err == nil {
		return nil
	}
	if aerr := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		return ops.AuditTx(ctx, tx, "taskiem:safety", "people.account.close.failed", person.String(), map[string]any{"action": action, "error": err.Error()})
	}); aerr != nil {
		return errors.Join(err, aerr)
	}
	return fmt.Errorf("people safety: closing an account: %w", err)
}

// sweepPeople prunes what the people chats keep a short while, for
// everyone: gone records past their hour, typing marks past their time
// and live connections that ended without saying so.
func (s *Server) sweepPeople(ctx context.Context) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT * FROM taskiem_people_sweep(make_interval(secs => $1))`, liveStale.Seconds())
		return err
	})
}

// sealOldReports seals reports kept plain (people's before 00480,
// businesses' before 00481) under keys of their own.
func (s *Server) sealOldReports(ctx context.Context) error {
	if s.Vault == nil {
		return nil
	}
	return errors.Join(s.sealReports(ctx, "taskiem_ops_people_reports_unsealed", "taskiem_ops_people_report_seal", peoplesafety.ReportPurpose),
		s.sealReports(ctx, "taskiem_ops_customer_reports_unsealed", "taskiem_ops_customer_report_seal", peoplesafety.CustomerReportPurpose))
}

// sealReports seals one kind's plain reports, 100 a tick: list names the
// definer answering them, seal the one keeping each sealed.
func (s *Server) sealReports(ctx context.Context, list, seal string, purpose func(uuid.UUID) string) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, messages FROM `+list+`(100)`)
		if err != nil {
			return err
		}
		type plain struct {
			id  uuid.UUID
			raw []byte
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (plain, error) {
			var p plain
			return p, r.Scan(&p.id, &p.raw)
		})
		if err != nil {
			return err
		}
		for _, p := range list {
			box, err := peoplesafety.NewBox(ctx, s.Vault, purpose(p.id), p.id)
			if err != nil {
				return err
			}
			sealed, err := box.Seal(peoplesafety.MessagesRow, p.raw)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT `+seal+`($1, $2, $3, $4)`, p.id, sealed, box.Sealed, box.KMSKey); err != nil {
				return err
			}
		}
		return nil
	})
}

// RunSafety runs SafetyTick every ten minutes until ctx ends.
func (s *Server) RunSafety(ctx context.Context) error {
	for {
		if err := s.SafetyTick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("people safety", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Minute):
		}
	}
}

// --- storage watched ---

func storageWords(rows []telemetry.StorageRow) string {
	var total int64
	for _, r := range rows {
		total += r.Bytes
	}
	return fmt.Sprintf("People chats take %s on disk.", humanBytes(total))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

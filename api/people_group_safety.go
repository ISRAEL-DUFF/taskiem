package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
)

// Safety in groups (people-chatting §8.2 to §8.4, §14;
// docs/spec/people-chatting-5b.md B with the founder's B1; "As built:
// stage 5b, group safety (server)"; migration 00484): the limits on making
// groups, inviting people who aren't contacts and joining by links; a
// report of a group or a member with its evidence; what moderators put in
// force on a group (its link turned off, the group closed) as its members
// see it, and its admins' appeal; and the scam checks on messages in new
// groups. The moderators' side is ops_people_groups.go.

// The refusals, in plain words.
var (
	errGroupClosed = customerSendErr{http.StatusForbidden,
		"Taskiem's moderators closed this group, so no one can send messages in it or join it. Its admins can appeal from the group's info."}
	errGroupLinkStopped = customerSendErr{http.StatusForbidden,
		"Taskiem's moderators turned off this group's invite link, so a new one can't be made for now."}
	errGroupMakeHour = customerSendErr{http.StatusTooManyRequests, "You have made several groups in the last hour. Try again later."}
	errGroupMakeDay  = customerSendErr{http.StatusTooManyRequests, "You have made many groups today. Try again tomorrow."}
	errGroupInvites  = customerSendErr{http.StatusTooManyRequests,
		"You have invited many people you haven't added to your contacts today. Add them to your contacts first, or try again tomorrow."}
	errGroupJoins = customerSendErr{http.StatusTooManyRequests, "You have joined many groups by a link in the last hour. Try again later."}
)

// groupStanding is what Taskiem's moderators put in force on a group
// (group_standing): closed (deleted at CloseUntil unless an appeal holds
// it), and its link stopped.
type groupStanding struct {
	ClosedAt, CloseUntil, LinkStoppedAt *time.Time
	CloseAction                         *uuid.UUID
}

// open refuses what writes to a group its moderators closed: a message, a
// reaction, adding people, its info, its link, letting people in.
func (g groupRow) open() error {
	if g.standing.ClosedAt != nil {
		return errGroupClosed
	}
	return nil
}

// groupClosedView is a closed group as its members see it: when, until
// when it is kept and why, in words; and for its admins the closure to
// appeal (once, by any admin) with the appeal.
type groupClosedView struct {
	At    time.Time  `json:"at"`
	Until *time.Time `json:"until"`
	Words string     `json:"words"`
	// For admins: the closure, whether it may be appealed and the appeal.
	ActionID  *uuid.UUID      `json:"action_id,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	CanAppeal bool            `json:"can_appeal"`
	Appeal    *standingAppeal `json:"appeal,omitempty"`
}

func groupClosedWords(until *time.Time) string {
	if until == nil {
		return "Taskiem's moderators closed this group because it broke the rules for chats with people. No one can send messages in it."
	}
	return "Taskiem's moderators closed this group because it broke the rules for chats with people. No one can send messages in it, and it will be deleted with all its messages on " +
		until.In(lagos()).Format("Mon 2 Jan 2006") + " unless its admins appeal and the decision is overturned."
}

// taskiemEventWords says Taskiem's own events in a group (its
// moderators'), or "" for an event of the members.
func taskiemEventWords(ev groupEvent) string {
	switch ev.Type {
	case "taskiem_closed":
		if ev.Until != nil {
			return "Taskiem's moderators closed this group. No one can send messages in it, and it will be deleted on " +
				ev.Until.In(lagos()).Format("Mon 2 Jan 2006") + " unless its admins appeal."
		}
		return "Taskiem's moderators closed this group. No one can send messages in it."
	case "taskiem_reopened":
		return "Taskiem's moderators reopened this group. You can send messages again."
	case "taskiem_link_off":
		return "Taskiem's moderators turned off this group's invite link."
	case "taskiem_link_back":
		return "Taskiem's moderators let this group's admins make an invite link again."
	}
	return ""
}

// taskiemEvent is whether an event is Taskiem's own (no member sent it).
func taskiemEvent(ev *groupEvent) bool {
	return ev != nil && strings.HasPrefix(ev.Type, "taskiem_")
}

// groupClosedTx is the closed group's view for the person: the appeal's
// parts for an admin.
func groupClosedTx(ctx context.Context, tx pgx.Tx, g groupRow) (*groupClosedView, error) {
	st := g.standing
	if st.ClosedAt == nil {
		return nil, nil
	}
	v := &groupClosedView{At: *st.ClosedAt, Until: st.CloseUntil, Words: groupClosedWords(st.CloseUntil)}
	if !g.admin() || st.CloseAction == nil {
		return v, nil
	}
	v.ActionID = st.CloseAction
	var apID *uuid.UUID
	var apText, apState *string
	var apAt *time.Time
	var ap standingAppeal
	err := tx.QueryRow(ctx, `SELECT m.reason, a.id, a.text, a.state, a.created_at, a.decided_at, a.decision_note
		FROM people_moderation m LEFT JOIN people_appeals a ON a.action_id = m.id WHERE m.id = $1`, *st.CloseAction).
		Scan(&v.Reason, &apID, &apText, &apState, &apAt, &ap.DecidedAt, &ap.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	if apID != nil {
		ap.ID, ap.Text, ap.State, ap.At = *apID, *apText, *apState, *apAt
		v.Appeal = &ap
	}
	v.CanAppeal = v.Appeal == nil && (st.CloseUntil == nil || st.CloseUntil.After(time.Now()))
	return v, nil
}

// --- limits (§8.2) ---

// groupLimitsTx is the person's group limits from their signals (their
// account's age, reports, device, a moderator's limit).
func (s *Server) groupLimitsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (peoplesafety.GroupLimits, error) {
	st, err := standingTx(ctx, tx, person)
	if err != nil {
		return peoplesafety.GroupLimits{}, err
	}
	sig, err := senderSignalsTx(ctx, tx, person, st)
	if err != nil {
		return peoplesafety.GroupLimits{}, err
	}
	return peoplesafety.GroupAllowance(sig), nil
}

// groupLedgerTx counts what the person did with groups of one kind
// (create, invite, link_join) within the last while.
func groupLedgerTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, kind string, within time.Duration) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM group_ledger WHERE person_id = $1 AND kind = $2 AND at > now() - make_interval(secs => $3)`,
		person, kind, within.Seconds()).Scan(&n)
	return n, err
}

// groupLedgerAddTx keeps n of the person's doings of a kind.
func groupLedgerAddTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, kind string, group uuid.UUID, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO group_ledger (person_id, kind, group_id) SELECT $1, $2, $3 FROM generate_series(1, $4)`, person, kind, group, n)
	return err
}

// groupMakeAllowedTx refuses a person past their groups an hour or a day,
// and keeps the new one; in their transaction, with their sender's lock.
func (s *Server) groupMakeAllowedTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID) error {
	if err := lockSenderTx(ctx, tx, person); err != nil {
		return err
	}
	l, err := s.groupLimitsTx(ctx, tx, person)
	if err != nil {
		return err
	}
	hour, err := groupLedgerTx(ctx, tx, person, "create", time.Hour)
	if err != nil {
		return err
	}
	if hour >= l.CreatePerHour {
		return errGroupMakeHour
	}
	day, err := groupLedgerTx(ctx, tx, person, "create", 24*time.Hour)
	if err != nil {
		return err
	}
	if day >= l.CreatePerDay {
		return errGroupMakeDay
	}
	return groupLedgerAddTx(ctx, tx, person, "create", group, 1)
}

// groupInvitesAllowedTx refuses an admin who would invite more people who
// aren't their contacts than their day allows (n more now); the group's
// row is locked first, then their sender's lock.
func (s *Server) groupInvitesAllowedTx(ctx context.Context, tx pgx.Tx, admin uuid.UUID, n int) error {
	if n == 0 {
		return nil
	}
	if err := lockSenderTx(ctx, tx, admin); err != nil {
		return err
	}
	l, err := s.groupLimitsTx(ctx, tx, admin)
	if err != nil {
		return err
	}
	day, err := groupLedgerTx(ctx, tx, admin, "invite", 24*time.Hour)
	if err != nil {
		return err
	}
	if day+n > l.StrangersPerDay {
		return errGroupInvites
	}
	return nil
}

// groupJoinAllowedTx refuses a person past their link joins an hour, once
// the join's definer holds the group (the group's lock, then the sender's,
// as an admin's add takes them); the join is then kept.
func (s *Server) groupJoinAllowedTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID) error {
	if err := lockSenderTx(ctx, tx, person); err != nil {
		return err
	}
	l, err := s.groupLimitsTx(ctx, tx, person)
	if err != nil {
		return err
	}
	hour, err := groupLedgerTx(ctx, tx, person, "link_join", time.Hour)
	if err != nil {
		return err
	}
	if hour >= l.LinkJoinsPerHour {
		return errGroupJoins
	}
	return groupLedgerAddTx(ctx, tx, person, "link_join", group, 1)
}

// groupOpenNowTx refuses a group its moderators closed, read once its row
// is held by this transaction (a join's or an invite's definer took it).
func groupOpenNowTx(ctx context.Context, tx pgx.Tx, group uuid.UUID) error {
	var closed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_standing WHERE group_id = $1 AND closed_at IS NOT NULL)`, group).Scan(&closed); err != nil {
		return err
	}
	if closed {
		return errGroupClosed
	}
	return nil
}

// --- reports (§8.3) ---

// groupReportRow is the row a report keeps the group's own info in.
const groupReportRow = "g"

// groupReportInfo is the group as the reporter saw it when reporting.
type groupReportInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Members     int    `json:"members"`
	Left        bool   `json:"left,omitempty"`
}

// reportGroup is POST /v1/me/groups/{id}/report {reason, note?, consent,
// person_id?}: the group, or one of its members (person_id), to Taskiem's
// moderators with the last 20 messages the reporter could see (only
// within their own time in the group) and the photos and files in them,
// copied and sealed under the report's own key; only with consent. Kept
// after the group, the member or the reporter goes.
func (s *Server) reportGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		reportInput
		PersonID *uuid.UUID `json:"person_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.personFail(w, r, err)
		return
	}
	if in.PersonID != nil && *in.PersonID == person {
		writeErr(w, http.StatusBadRequest, "You can't report yourself.")
		return
	}
	if s.reportLimited(person) {
		writeErr(w, http.StatusTooManyRequests, "You have sent many reports. Try again later.")
		return
	}
	var id uuid.UUID
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if in.PersonID != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id = $1 AND person_id = $2)
				OR EXISTS (SELECT 1 FROM group_messages WHERE group_id = $1 AND sender_id = $2)`, g.ID, *in.PersonID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return customerSendErr{http.StatusNotFound, "This person isn't in this group."}
			}
		}
		id, err = s.groupReportTx(ctx, tx, person, g, in.PersonID, in.reportInput)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	said := "Thank you. Taskiem's moderators will look at this group. You can also leave it."
	if in.PersonID != nil {
		said = "Thank you. Taskiem's moderators will look at what this person sent. You can also block them."
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "messages_sent": true, "said": said})
}

// groupReportTx keeps a report of the group (member nil) or of one of its
// members, in the reporter's own transaction: the last 20 messages of the
// group they see (row-level security keeps them to their own time in it;
// never one deleted for them, nor an event), opened and sealed under the
// report's own key with who sent each, the photos and files in them copied
// (the newest first, up to ReportFilesMax), and the group's name and
// description then.
func (s *Server) groupReportTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, g groupRow, member *uuid.UUID, in reportInput) (uuid.UUID, error) {
	key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
	if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
		return uuid.Nil, err
	}
	rows, err := tx.Query(ctx, `SELECT x.id, x.sender_id, x.kind, x.sealed, x.attachment, x.deleted_at IS NOT NULL, x.created_at, f.type, f.size
		FROM group_messages x LEFT JOIN group_files f ON f.group_id = x.group_id AND f.message_id = x.id
		LEFT JOIN group_marks k ON k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1
		WHERE x.group_id = $2 AND x.kind <> 'event' AND k.hidden_at IS NULL ORDER BY x.created_at DESC, x.id DESC LIMIT 20`, person, g.ID)
	if err != nil {
		return uuid.Nil, err
	}
	type fileRef struct {
		id   uuid.UUID
		typ  string
		size int64
	}
	var files []fileRef
	var senders []uuid.UUID
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (reportMessage, error) {
		var m reportMessage
		var id uuid.UUID
		var sealed []byte
		var ftype *string
		var fsize *int64
		if err := r.Scan(&id, &m.SenderID, &m.Kind, &sealed, &m.Attachment, &m.Deleted, &m.At, &ftype, &fsize); err != nil {
			return m, err
		}
		m.ID = &id
		if m.SenderID != nil {
			m.FromReporter = *m.SenderID == person
			senders = append(senders, *m.SenderID)
		}
		if !m.Deleted {
			if v, err := openGroupSealed(key, g.ID, id, sealed); err == nil {
				m.Body, m.Card, m.Location = v.Body, v.Card, v.Location
				if m.Attachment != nil && v.Name != "" {
					m.Attachment["name"] = v.Name
				}
			}
		}
		if !m.Deleted && ftype != nil && fsize != nil {
			files = append(files, fileRef{id, *ftype, *fsize})
		}
		return m, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	// Who sent each, by their own name (moderators act on accounts, not
	// on the names the reporter gave them).
	_, cards, err := s.groupNamesTx(ctx, tx, person, senders)
	if err != nil {
		return uuid.Nil, err
	}
	peerWrote := false
	for i := range msgs {
		m := &msgs[i]
		switch {
		case m.SenderID == nil:
			m.Sender = peopleGoneName
		case *m.SenderID == person:
			m.Sender = "The reporter"
		default:
			c, ok := cards[*m.SenderID]
			if !ok || (c.Deleted && !c.Suspended) || c.Name == "" {
				m.Sender = peopleGoneName
			} else {
				m.Sender = c.Name
			}
		}
		if member != nil && m.SenderID != nil && *m.SenderID == *member {
			peerWrote = true
		}
	}
	id := uuid.Must(uuid.NewV7())
	box, err := peoplesafety.NewBox(ctx, s.Vault, peoplesafety.ReportPurpose(id), id)
	if err != nil {
		return uuid.Nil, err
	}
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
		if err := tx.QueryRow(ctx, `SELECT sealed FROM group_files WHERE group_id = $1 AND message_id = $2`, g.ID, f.id).Scan(&sealed); err != nil {
			return uuid.Nil, err
		}
		data, err := peoplechats.Open(key, g.ID, groupFileRow(f.id), sealed)
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
	// The group as the reporter saw it: as it is, or as it was when they
	// left (A5).
	infoSealed := g.sealed
	if !g.current() && g.leftSealed != nil {
		infoSealed = g.leftSealed
	}
	info := openGroupInfo(key, g.ID, infoSealed)
	gi := groupReportInfo{Name: info.Name, Description: info.Description, Left: !g.current()}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1 AND left_at IS NULL`, g.ID).Scan(&gi.Members); err != nil {
		return uuid.Nil, err
	}
	rawInfo, _ := json.Marshal(gi)
	groupSealed, err := box.Seal(groupReportRow, rawInfo)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO people_reports (id, person_id, reported_id, reason, note, consent, messages, from_request, sealed, box_key, kms_key, files,
		reporter_mark, peer_wrote, group_id, group_sealed) VALUES ($1, $2, $3, $4, $5, true, '[]', false, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id, person, member, in.Reason, in.Note, sealed, box.Sealed, box.KMSKey, len(out), s.reporterMark(person), peerWrote, g.ID, groupSealed); err != nil {
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

// --- the admins' appeal ---

// appealGroup is POST /v1/me/groups/{id}/appeal {text}: one of the group's
// admins appeals its closing, once for the group, while it may still be
// appealed (before the day it is deleted). 201 {appeal, said}; 403 not an
// admin; 409 nothing to appeal, already appealed, or too late.
func (s *Server) appealGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Text string `json:"text"`
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
	var action uuid.UUID
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		// The group's row is not taken: the closure's lock alone, as its
		// carrying out takes it (taskiem_group_close_carry), so an appeal
		// made as the day comes either holds the closure or is refused.
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if !g.admin() {
			return customerSendErr{http.StatusForbidden, "Only the group's admins can appeal its closing."}
		}
		if g.standing.CloseAction == nil {
			return customerSendErr{http.StatusConflict, "This group isn't closed, so there is nothing to appeal."}
		}
		action = *g.standing.CloseAction
		var until *time.Time
		var lifted, closed *time.Time
		if err := tx.QueryRow(ctx, `SELECT until, lifted_at, closed_at FROM people_moderation WHERE id = $1 AND group_id = $2 FOR UPDATE`,
			action, g.ID).Scan(&until, &lifted, &closed); err != nil {
			return err
		}
		if lifted != nil || closed != nil {
			return customerSendErr{http.StatusConflict, "This decision is no longer in force, so there is nothing to appeal."}
		}
		if until != nil && !until.After(time.Now()) {
			return customerSendErr{http.StatusConflict, "The time to appeal the closing of this group has ended."}
		}
		out = standingAppeal{ID: uuid.Must(uuid.NewV7()), Text: in.Text, State: "open"}
		if err := tx.QueryRow(ctx, `INSERT INTO people_appeals (id, action_id, person_id, text) VALUES ($1, $2, $3, $4) RETURNING created_at`,
			out.ID, action, person, in.Text).Scan(&out.At); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(ops.PlatformChain)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'person', $2, 'people.group.appeal', $3, jsonb_build_object('appeal', $4::text, 'group', $5::text))`,
			ops.PlatformChain, person.String(), action.String(), out.ID.String(), g.ID.String())
		return err
	})
	if isUnique(err) {
		writeErr(w, http.StatusConflict, "This group's closing was already appealed. Its admins are told when a moderator decides.")
		return
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"appeal": out, "said": "Thank you. A moderator will read your appeal, and we will tell you what they decide."})
}

// --- scam checks (B1) ---

// groupScreen is what the checks decided of a group message: warn marks
// it for those who haven't added its sender; held keeps it back from
// everyone (one copy), refused to the sender in the same words as 5a's.
type groupScreen struct {
	warn    string
	held    bool
	reasons []string
}

// groupScreenTx checks a message in a group (B1): only while the group is
// younger than a week or the sender joined it by its link in the last
// day, and only when some member now in it hasn't added the sender; only
// the verdict is kept. Released messages are not checked again.
func (s *Server) groupScreenTx(ctx context.Context, tx pgx.Tx, me uuid.UUID, g groupRow, d peopleDraft) (groupScreen, error) {
	var out groupScreen
	text := d.screenText()
	if text == "" || d.released {
		return out, nil
	}
	young := time.Since(g.CreatedAt) < peoplesafety.GroupCheckYoung
	byLink := g.How == "link" && time.Since(g.JoinedAt) < peoplesafety.GroupCheckJoined
	if !young && !byLink {
		return out, nil
	}
	strangers, err := groupStrangersTx(ctx, s, tx, me, g.ID)
	if err != nil || strangers == 0 {
		return out, err
	}
	// One sender's checks one after another (their day's count).
	if err := lockSenderTx(ctx, tx, me); err != nil {
		return out, err
	}
	v, err := s.people().ScreenGroupTx(ctx, tx, me, g.ID, text)
	if err != nil {
		return out, err
	}
	switch v.Verdict {
	case "hold":
		out.held, out.reasons = true, v.Reasons
	case "warn":
		out.warn, out.reasons = "warn", v.Reasons
	}
	return out, nil
}

// groupStrangersTx is how many members now in the group, other than the
// sender, haven't added the sender to their contacts.
func groupStrangersTx(ctx context.Context, s *Server, tx pgx.Tx, me, group uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND left_at IS NULL AND person_id <> $2`, group, me)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	n := 0
	for start := 0; start < len(ids); start += 500 {
		list, err := s.people().CardsTx(ctx, tx, me, ids[start:min(start+500, len(ids))])
		if err != nil {
			return 0, err
		}
		added := map[uuid.UUID]bool{}
		for _, c := range list {
			if c.TheyAddedYou {
				added[c.ID] = true
			}
		}
		for _, id := range ids[start:min(start+500, len(ids))] {
			if !added[id] {
				n++
			}
		}
	}
	return n, nil
}

// holdGroupTx keeps a group message the checks held, sealed under its own
// key with the verdict's reasons and its mentions, for moderators to
// release (sent to the group then, as the sender meant it) or confirm.
// No member gets it.
func (s *Server) holdGroupTx(ctx context.Context, tx pgx.Tx, me, group uuid.UUID, d peopleDraft, mentions []uuid.UUID, reasons []string) error {
	return s.keepHeldTx(ctx, tx, me, nil, nil, &group, d, mentions, reasons)
}

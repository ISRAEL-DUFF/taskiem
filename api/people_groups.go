package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Groups of people (people-chatting §9, §13, §14, §17 Q7, Q16, Q17;
// docs/spec/people-chatting-5b.md A and D with the founder's answers A2 to
// A8; decision 0069 point 7; migration 00482). A group keeps one copy of
// its messages, sealed under the group's own key, with each member's own
// marks beside it. Kept by Taskiem, opened only by its servers: not end to
// end encrypted. Who sees what is the database's row-level security (a
// member sees messages only within their time in the group); who may do
// what (admins) is decided here.
//
// Every writer of a group takes the group's row first (FOR UPDATE: sends,
// membership and settings), so writes to one group are in one order and
// a message's time (clock_timestamp(), taken after the lock) is the order
// it was kept in; a writer that also reaches a one-to-one chat (blocking
// from a group) does so in its own transaction. A transaction touching
// several groups (deleting an account) takes them in id order.

var (
	// groupMax is how many people a group has at most (§17 Q7).
	groupMax = 256
	// groupLinkJoinsPerHour is how many people may join one group by its
	// link in an hour.
	groupLinkJoinsPerHour = 60
	// groupMessagesPerMinute is how many messages one member sends to a
	// group a minute (halved in their first week).
	groupMessagesPerMinute = 20
	// groupNameMax and groupAboutMax are the longest name and description.
	groupNameMax  = 80
	groupAboutMax = 500
	// groupReadsKept is how long a member's read and delivered times are
	// kept for "Read by".
	groupReadsKept = 31 * 24 * time.Hour
)

// The groups' refusals, in plain words.
var (
	errGroupLeft      = customerSendErr{http.StatusForbidden, "You're no longer in this group."}
	errGroupNotAdmin  = customerSendErr{http.StatusForbidden, "Only the group's admins can do this."}
	errGroupNoEdit    = customerSendErr{http.StatusForbidden, "Only the group's admins can change its name, photo and description."}
	errGroupAdminPost = customerSendErr{http.StatusForbidden, "Only admins can send messages to this group."}
	errGroupFull      = customerSendErr{http.StatusConflict, "This group is full: a group has at most 256 people."}
	errGroupNoLinks   = customerSendErr{http.StatusServiceUnavailable, "Group links are not set up on this Taskiem."}
)

// groupRoutes are the person's group routes (groupPersonRoutes lists
// them).
func (s *Server) groupRoutes(r chi.Router) {
	r = r.With(s.peopleHere)
	r.Get("/me/groups", s.listGroups)
	r.Post("/me/groups", s.createGroup)
	r.Get("/me/groups/invites", s.listGroupInvites)
	r.Post("/me/groups/invites/{id}/accept", s.acceptGroupInvite)
	r.Post("/me/groups/invites/{id}/decline", s.declineGroupInvite)
	r.Get("/me/groups/search", s.searchGroups)
	r.Get("/me/groups/starred", s.starredGroups)
	r.Post("/me/forward", s.forwardMessage)
	r.Post("/me/group-links/{code}/join", s.joinGroupByLink)
	r.Delete("/me/group-links/{code}/join", s.withdrawGroupJoin)
	r.Get("/me/groups/{id}", s.getGroup)
	r.Put("/me/groups/{id}", s.putGroup)
	r.Delete("/me/groups/{id}", s.deleteGroupForMe)
	r.Put("/me/groups/{id}/me", s.putMyGroup)
	r.Post("/me/groups/{id}/leave", s.leaveGroup)
	r.Get("/me/groups/{id}/photo", s.groupPhoto)
	r.Put("/me/groups/{id}/photo", s.putGroupPhoto)
	r.Delete("/me/groups/{id}/photo", s.deleteGroupPhoto)
	r.Get("/me/groups/{id}/members", s.groupMembers)
	r.Post("/me/groups/{id}/members", s.addGroupMembers)
	r.Put("/me/groups/{id}/members/{person}", s.setGroupAdmin)
	r.Delete("/me/groups/{id}/members/{person}", s.removeGroupMember)
	r.Post("/me/groups/{id}/members/{person}/block", s.blockGroupMember)
	r.Delete("/me/groups/{id}/invites/{person}", s.cancelGroupInvite)
	r.Get("/me/groups/{id}/link", s.groupLink)
	r.Post("/me/groups/{id}/link", s.newGroupLink)
	r.Delete("/me/groups/{id}/link", s.groupLinkOff)
	r.Get("/me/groups/{id}/link/qr", s.groupLinkQR)
	r.Get("/me/groups/{id}/joins", s.groupJoinRequests)
	r.Post("/me/groups/{id}/joins/{person}/approve", s.approveGroupJoin)
	r.Post("/me/groups/{id}/joins/{person}/decline", s.declineGroupJoin)
	r.Get("/me/groups/{id}/messages", s.groupMessages)
	r.Get("/me/groups/{id}/media", s.groupMedia)
	r.Post("/me/groups/{id}/messages", s.sendGroupMessage)
	r.Post("/me/groups/{id}/files", s.sendGroupFile)
	r.Get("/me/groups/{id}/files/{message}", s.groupFile)
	r.Put("/me/groups/{id}/messages/{message}", s.markGroupMessage)
	r.Delete("/me/groups/{id}/messages/{message}", s.deleteGroupMessage)
	r.Get("/me/groups/{id}/messages/{message}/info", s.groupMessageInfo)
	r.Get("/me/groups/{id}/messages/{message}/reactions", s.groupMessageReactions)
	r.Get("/me/groups/{id}/messages/{message}/card", s.openGroupCard)
	r.Post("/me/groups/{id}/messages/{message}/paid", s.markGroupSplitPaid)
	r.Post("/me/groups/{id}/read", s.readGroup)
	r.Post("/me/groups/{id}/typing", s.typingGroup)
	r.Post("/me/groups/{id}/report", s.reportGroup)
	r.Post("/me/groups/{id}/appeal", s.appealGroup)
}

// groupPublicRoutes are for anyone holding a group's link: its preview.
func (s *Server) groupPublicRoutes(r chi.Router) {
	r.Get("/group-links/{code}", s.groupLinkPreview)
	r.Get("/group-links/{code}/photo", s.groupLinkPhoto)
}

// groupPersonRoutes are the group routes a personal session reaches
// (personalAllowed).
var groupPersonRoutes = regexp.MustCompile(`^((GET|POST) /v1/me/groups|GET /v1/me/groups/(invites|search|starred)|POST /v1/me/groups/invites/[^/]+/(accept|decline)|` +
	`POST /v1/me/forward|(POST|DELETE) /v1/me/group-links/[^/]+/join|(GET|PUT|DELETE) /v1/me/groups/[^/]+|PUT /v1/me/groups/[^/]+/me|POST /v1/me/groups/[^/]+/(leave|read|typing|messages|files|members|link|report|appeal)|` +
	`(GET|PUT|DELETE) /v1/me/groups/[^/]+/photo|GET /v1/me/groups/[^/]+/(members|link|link/qr|joins|messages|media|files/[^/]+)|(PUT|DELETE) /v1/me/groups/[^/]+/members/[^/]+|` +
	`POST /v1/me/groups/[^/]+/members/[^/]+/block|DELETE /v1/me/groups/[^/]+/(invites/[^/]+|link)|POST /v1/me/groups/[^/]+/joins/[^/]+/(approve|decline)|` +
	`(PUT|DELETE) /v1/me/groups/[^/]+/messages/[^/]+|GET /v1/me/groups/[^/]+/messages/[^/]+/(info|reactions|card)|POST /v1/me/groups/[^/]+/messages/[^/]+/paid)$`)

// --- the group as kept ---

// groupInfo is what a group's sealed value holds.
type groupInfo struct {
	Name        string `json:"n"`
	Description string `json:"d,omitempty"`
}

const groupInfoRow = "gi"
const groupPhotoRow = "gp"

func groupMsgKey(id uuid.UUID) string  { return "gm:" + id.String() }
func groupFileRow(id uuid.UUID) string { return "gf:" + id.String() }

func openGroupInfo(key []byte, group uuid.UUID, sealed []byte) groupInfo {
	var v groupInfo
	if key == nil {
		return v
	}
	raw, err := peoplechats.Open(key, group, groupInfoRow, sealed)
	if err == nil {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

// groupRow is a group with the person's own membership of it.
type groupRow struct {
	ID                                           uuid.UUID
	sealed                                       []byte
	AdminsOnlyPost, AdminsOnlyEdit, ApproveJoins bool
	PhotoAt                                      *time.Time
	CreatedAt                                    time.Time
	Role, How                                    string
	JoinedAt                                     time.Time
	LeftAt                                       *time.Time
	LeftHow                                      *string
	Muted, Archived, Mentions                    bool
	HiddenAt                                     *time.Time
	ReadUpto, DeliveredUpto                      time.Time
	// leftSealed: the name and description when the person left.
	leftSealed []byte
	// standing: what Taskiem's moderators put in force on the group.
	standing groupStanding
	// From the lists: the last message the person sees, how many current
	// members, the unread count and whether one mentions them.
	Members, Unread int
	Mentioned       bool
	last            *groupMsgRow
}

func (g groupRow) current() bool { return g.LeftAt == nil }
func (g groupRow) admin() bool   { return g.current() && g.Role == "admin" }

const groupRowCols = `g.id, g.sealed, g.admins_only_post, g.admins_only_edit, g.approve_joins, g.photo_at, g.created_at,
	m.role, m.how, m.joined_at, m.left_at, m.left_how, m.muted, m.archived, m.mentions, m.hidden_at, m.read_upto, m.delivered_upto, m.left_sealed,
	(SELECT s.closed_at FROM group_standing s WHERE s.group_id = g.id) AS s_closed, (SELECT s.close_until FROM group_standing s WHERE s.group_id = g.id) AS s_until,
	(SELECT s.close_action FROM group_standing s WHERE s.group_id = g.id) AS s_action, (SELECT s.link_stopped_at FROM group_standing s WHERE s.group_id = g.id) AS s_link`

func scanGroupRow(r pgx.Row, extra ...any) (groupRow, error) {
	var g groupRow
	dest := append([]any{&g.ID, &g.sealed, &g.AdminsOnlyPost, &g.AdminsOnlyEdit, &g.ApproveJoins, &g.PhotoAt, &g.CreatedAt,
		&g.Role, &g.How, &g.JoinedAt, &g.LeftAt, &g.LeftHow, &g.Muted, &g.Archived, &g.Mentions, &g.HiddenAt, &g.ReadUpto, &g.DeliveredUpto, &g.leftSealed,
		&g.standing.ClosedAt, &g.standing.CloseUntil, &g.standing.CloseAction, &g.standing.LinkStoppedAt}, extra...)
	err := r.Scan(dest...)
	return g, err
}

// groupTx is one of the person's groups (one they are or were in) by its
// id. lock is for a writer: the group's row is taken first (every
// writer's first lock), and the person must be in the group now
// (errGroupLeft otherwise).
func groupTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, idText string, lock bool) (groupRow, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return groupRow{}, errDoorNone
	}
	if lock {
		// Seen and changed only by a current member (the policies): a
		// person who left finds nothing to lock.
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM people_groups WHERE id = $1 FOR UPDATE`, id).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			g, err := groupTx(ctx, tx, person, idText, false)
			if err != nil {
				return g, err
			}
			return g, errGroupLeft
		}
		if err != nil {
			return groupRow{}, err
		}
	}
	g, err := scanGroupRow(tx.QueryRow(ctx, `SELECT `+groupRowCols+` FROM people_groups g JOIN group_members m ON m.group_id = g.id AND m.person_id = $1
		WHERE g.id = $2`, person, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return g, errDoorNone
	}
	if err == nil && lock && !g.current() {
		return g, errGroupLeft
	}
	return g, err
}

// bumpGroupTx says the group has a new message (its place in lists).
func bumpGroupTx(ctx context.Context, tx pgx.Tx, group uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE people_groups SET last_message_at = clock_timestamp() WHERE id = $1`, group)
	return err
}

// groupEvent is something that happened in a group, kept as a message of
// kind event: created, added, joined, left, removed, admin, unadmin,
// info, photo, settings.
type groupEvent struct {
	Type   string     `json:"type"`
	Actor  uuid.UUID  `json:"actor"`
	Target *uuid.UUID `json:"target,omitempty"`
	// Until: when a group Taskiem's moderators closed is deleted
	// (taskiem_closed).
	Until *time.Time `json:"until,omitempty"`
}

// groupEventTx keeps an event in the group, sent by the person in scope
// (who must be in the group now), and bumps the group.
func groupEventTx(ctx context.Context, tx pgx.Tx, group, actor uuid.UUID, typ string, target *uuid.UUID) error {
	ev := groupEvent{Type: typ, Actor: actor, Target: target}
	raw, _ := json.Marshal(ev)
	if _, err := tx.Exec(ctx, `INSERT INTO group_messages (group_id, id, sender_id, kind, event) VALUES ($1, $2, $3, 'event', $4)`,
		group, uuid.Must(uuid.NewV7()), actor, raw); err != nil {
		return err
	}
	return bumpGroupTx(ctx, tx, group)
}

// --- what the person sees of a group ---

// groupView is one of the person's groups as their side shows it.
type groupView struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Photo       *photoRef `json:"photo"`
	// Members: how many are in it now.
	Members int `json:"members"`
	// Role: admin or member; Left: the person left or was removed
	// (left_how): their copy up to then stays theirs, nothing new comes.
	Role    string `json:"role"`
	Left    bool   `json:"left"`
	LeftHow string `json:"left_how,omitempty"`
	// Settings: admins only post; admins only edit the info (A2); joining
	// by the link waits for an admin (A4).
	AdminsOnlyPost bool `json:"admins_only_post"`
	AdminsOnlyEdit bool `json:"admins_only_edit"`
	ApproveJoins   bool `json:"approve_joins"`
	// What the person may do now.
	CanPost bool `json:"can_post"`
	CanEdit bool `json:"can_edit"`
	// The person's own: muted, archived, and whether a mention pushes while
	// muted (A7).
	Muted    bool `json:"muted"`
	Archived bool `json:"archived"`
	Mentions bool `json:"mentions"`
	// Unread: others' messages after the person's read mark; Mentioned:
	// one of them mentions the person.
	Unread    int    `json:"unread"`
	Mentioned bool   `json:"mentioned"`
	Last      string `json:"last"`
	LastMine  bool   `json:"last_mine"`
	// LastSender: who wrote the last message ("You" for the person).
	LastSender string    `json:"last_sender,omitempty"`
	LastAt     time.Time `json:"last_at"`
	JoinedAt   time.Time `json:"joined_at"`
	// JoinRequests: people waiting for an admin to let them in (admins).
	JoinRequests int `json:"join_requests,omitempty"`
	// Closed: Taskiem's moderators closed the group (no one writes in it;
	// it is deleted at closed.until unless its admins' appeal wins), in
	// words; for its admins with the closure to appeal.
	Closed *groupClosedView `json:"closed,omitempty"`
	// LinkStopped: Taskiem's moderators turned its invite link off; no new
	// one can be made for now.
	LinkStopped bool `json:"link_stopped,omitempty"`
	// Kept says how groups are kept (one group, and on making one).
	Kept string `json:"kept,omitempty"`
}

func groupPhotoRef(id uuid.UUID, at *time.Time) *photoRef {
	if at == nil {
		return nil
	}
	return &photoRef{URL: "/v1/me/groups/" + id.String() + "/photo?v=" + strconv.FormatInt(at.UnixMilli(), 10), UpdatedAt: *at}
}

// groupNamesTx is how the person sees each of these people named: the
// name they gave them in contacts, else their own; "Deleted account" for a
// deleted account; "You" for themself.
func (s *Server) groupNamesTx(ctx context.Context, tx pgx.Tx, viewer uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, map[uuid.UUID]people.Card, error) {
	names := map[uuid.UUID]string{}
	cards := map[uuid.UUID]people.Card{}
	var others []uuid.UUID
	for _, id := range ids {
		if id == viewer {
			names[id] = "You"
		} else if !slices.Contains(others, id) {
			others = append(others, id)
		}
	}
	for start := 0; start < len(others); start += 500 {
		end := min(start+500, len(others))
		list, err := s.people().CardsTx(ctx, tx, viewer, others[start:end])
		if err != nil {
			return nil, nil, err
		}
		for _, c := range list {
			cards[c.ID] = c
			switch {
			case c.Deleted && !c.Suspended:
				names[c.ID] = peopleGoneName
			case c.ContactName != nil:
				names[c.ID] = *c.ContactName
			case c.Name != "":
				names[c.ID] = c.Name
			default:
				names[c.ID] = "Someone"
			}
		}
	}
	for _, id := range others {
		if _, ok := names[id]; !ok {
			names[id] = peopleGoneName
		}
	}
	return names, cards, nil
}

// eventWords says an event as the person reads it.
func eventWords(ev groupEvent, names map[uuid.UUID]string) string {
	if w := taskiemEventWords(ev); w != "" {
		return w
	}
	a := names[ev.Actor]
	if a == "" {
		a = peopleGoneName
	}
	t := ""
	if ev.Target != nil {
		t = names[*ev.Target]
		if t == "" {
			t = peopleGoneName
		}
	}
	switch ev.Type {
	case "created":
		return a + " created the group"
	case "added":
		return a + " added " + t
	case "joined":
		return a + " joined"
	case "left":
		return a + " left"
	case "removed":
		return a + " removed " + t
	case "admin":
		if ev.Target != nil && *ev.Target == ev.Actor {
			return a + " is now an admin"
		}
		return a + " made " + t + " an admin"
	case "unadmin":
		return a + " made " + t + " no longer an admin"
	case "info":
		return a + " changed the group's info"
	case "photo":
		return a + " changed the group's photo"
	case "settings":
		return a + " changed the group's settings"
	}
	return ""
}

// groupViewsTx turns groups into what the person's side shows.
func (s *Server) groupViewsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, rows []groupRow) ([]groupView, error) {
	out := make([]groupView, 0, len(rows))
	var ids []uuid.UUID
	for _, g := range rows {
		if g.last != nil {
			if g.last.sender != nil {
				ids = append(ids, *g.last.sender)
			}
			if g.last.ev != nil && !taskiemEvent(g.last.ev) {
				ids = append(ids, g.last.ev.Actor)
				if g.last.ev.Target != nil {
					ids = append(ids, *g.last.ev.Target)
				}
			}
		}
	}
	names, _, err := s.groupNamesTx(ctx, tx, person, ids)
	if err != nil {
		return nil, err
	}
	for _, g := range rows {
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
			return nil, err
		}
		// Someone who left sees the group as it was then (A5): its name
		// and description then, no photo, no count.
		sealed := g.sealed
		if !g.current() && g.leftSealed != nil {
			sealed = g.leftSealed
		}
		info := openGroupInfo(key, g.ID, sealed)
		v := groupView{ID: g.ID, Kind: "group", Name: info.Name, Description: info.Description, Photo: groupPhotoRef(g.ID, g.PhotoAt),
			Members: g.Members, Role: g.Role, Left: !g.current(), AdminsOnlyPost: g.AdminsOnlyPost, AdminsOnlyEdit: g.AdminsOnlyEdit,
			ApproveJoins: g.ApproveJoins, Muted: g.Muted, Archived: g.Archived, Mentions: g.Mentions, Unread: g.Unread, Mentioned: g.Mentioned,
			LastAt: g.JoinedAt, JoinedAt: g.JoinedAt}
		if g.LeftHow != nil {
			v.LeftHow = *g.LeftHow
		}
		if !g.current() {
			v.Photo, v.Members = nil, 0
		}
		v.CanPost = g.current() && (!g.AdminsOnlyPost || g.Role == "admin") && g.standing.ClosedAt == nil
		v.CanEdit = g.current() && (!g.AdminsOnlyEdit || g.Role == "admin") && g.standing.ClosedAt == nil
		if g.current() && g.standing.ClosedAt != nil {
			v.Closed = &groupClosedView{At: *g.standing.ClosedAt, Until: g.standing.CloseUntil, Words: groupClosedWords(g.standing.CloseUntil)}
		}
		v.LinkStopped = g.current() && g.standing.LinkStoppedAt != nil
		if l := g.last; l != nil {
			v.LastAt = l.createdAt
			v.LastMine = l.sender != nil && *l.sender == person
			switch {
			case l.ev != nil:
				v.Last = eventWords(*l.ev, names)
			case l.deletedAt != nil:
				v.Last = "This message was deleted"
			default:
				var sealed peopleSealed
				if key != nil && l.sealed != nil {
					sealed, _ = openGroupSealed(key, g.ID, l.id, l.sealed)
				}
				v.Last = previewOf(l.kind, attachKind(l.attach), sealed)
			}
			if l.sender != nil && l.ev == nil {
				v.LastSender = names[*l.sender]
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// groupListCols reads, beside groupRowCols, the person's last visible
// message, the members now, unread and mentioned, for the person ($1).
var groupListCols = groupRowCols + `,
	taskiem_group_count(g.id) AS members, -- one membership check, not one per member's row (00490)
	(SELECT count(*)::int FROM group_messages u WHERE u.group_id = g.id AND u.created_at > m.read_upto AND u.sender_id IS DISTINCT FROM $1
	   AND u.kind <> 'event' AND u.deleted_at IS NULL AND m.visible @> u.created_at
	   AND NOT EXISTS (SELECT 1 FROM group_marks k WHERE k.group_id = u.group_id AND k.message_id = u.id AND k.person_id = $1 AND k.hidden_at IS NOT NULL)) AS unread,
	EXISTS (SELECT 1 FROM group_messages u WHERE u.group_id = g.id AND u.created_at > m.read_upto AND $1 = ANY (u.mentions)
	   AND u.deleted_at IS NULL AND m.visible @> u.created_at) AS mentioned,
	` + groupMsgColsAs("l")

const groupListFrom = ` FROM group_members m JOIN people_groups g ON g.id = m.group_id
	LEFT JOIN LATERAL (SELECT x.* FROM group_messages x WHERE x.group_id = m.group_id AND m.visible @> x.created_at
	  AND NOT EXISTS (SELECT 1 FROM group_marks k WHERE k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1 AND k.hidden_at IS NOT NULL)
	  ORDER BY x.created_at DESC, x.id DESC LIMIT 1) l ON true`

// groupListTx is a page of the person's groups (limit, and whether there
// are more), archived or not, the latest first; never one deleted for
// them (until a new message).
func (s *Server) groupListTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, archived bool, before *listCursor, limit int) ([]groupView, bool, error) {
	q := `SELECT * FROM (SELECT ` + groupListCols + `, COALESCE(l.created_at, m.joined_at) AS at` + groupListFrom + `
		WHERE m.person_id = $1 AND m.archived = $2) q WHERE (q.hidden_at IS NULL OR q.l_id IS NOT NULL)`
	args := []any{person, archived}
	if before != nil {
		q += ` AND (q.at, q.id) < ($3, $4)`
		args = append(args, before.at, before.id)
	}
	rows, err := tx.Query(ctx, q+fmt.Sprintf(` ORDER BY q.at DESC, q.id DESC LIMIT %d`, limit+1), args...)
	if err != nil {
		return nil, false, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupRow, error) { return scanGroupListAt(r) })
	if err != nil {
		return nil, false, err
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	out, err := s.groupViewsTx(ctx, tx, person, list)
	return out, more, err
}

// scanGroupListAt scans a groupListTx row (the list's columns and at).
func scanGroupListAt(r pgx.Row) (groupRow, error) {
	var l groupMsgRow
	var lid *uuid.UUID
	var members, unread int
	var mentioned bool
	var at time.Time
	extra := append([]any{&members, &unread, &mentioned, &lid}, l.dest()[1:]...)
	extra = append(extra, &at)
	g, err := scanGroupRow(r, extra...)
	if err != nil {
		return g, err
	}
	g.Members, g.Unread, g.Mentioned = members, unread, mentioned
	if lid != nil {
		l.id = *lid
		l.decode()
		g.last = &l
	}
	return g, nil
}

// groupOneTx is one of the person's groups as their side shows it.
func (s *Server) groupOneTx(ctx context.Context, tx pgx.Tx, person, id uuid.UUID) (groupView, error) {
	g, err := scanGroupListAt(tx.QueryRow(ctx, `SELECT `+groupListCols+`, COALESCE(l.created_at, m.joined_at)`+groupListFrom+` WHERE m.person_id = $1 AND m.group_id = $2`, person, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return groupView{}, errDoorNone
	}
	if err != nil {
		return groupView{}, err
	}
	list, err := s.groupViewsTx(ctx, tx, person, []groupRow{g})
	if err != nil {
		return groupView{}, err
	}
	v := list[0]
	if g.admin() {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_join_requests WHERE group_id = $1`, id).Scan(&v.JoinRequests); err != nil {
			return v, err
		}
		if v.Closed != nil {
			if v.Closed, err = groupClosedTx(ctx, tx, g); err != nil {
				return v, err
			}
		}
	}
	return v, nil
}

// groupUnreadTx is how many unread messages the person has in their
// groups (archived or not), all of them.
func groupUnreadTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, archived bool) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum((SELECT count(*) FROM group_messages u WHERE u.group_id = m.group_id AND u.created_at > m.read_upto
		  AND u.sender_id IS DISTINCT FROM $1 AND u.kind <> 'event' AND u.deleted_at IS NULL AND m.visible @> u.created_at
		  AND NOT EXISTS (SELECT 1 FROM group_marks k WHERE k.group_id = u.group_id AND k.message_id = u.id AND k.person_id = $1 AND k.hidden_at IS NOT NULL))), 0)::int
		FROM group_members m WHERE m.person_id = $1 AND m.archived = $2`, person, archived).Scan(&n)
	return n, err
}

// groupInvitesCountTx is how many invites wait for the person.
func groupInvitesCountTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM group_invites WHERE person_id = $1 AND NOT hidden`, person).Scan(&n)
	return n, err
}

// listGroups is GET /v1/me/groups?archived=1&before=: the person's groups,
// the latest first, 200 a page (more, next), with unread across them all
// and the invites waiting.
func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	archived := r.URL.Query().Get("archived") == "1"
	var out []groupView
	var unread, invites int
	more := false
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if out, more, err = s.groupListTx(ctx, tx, person, archived, before, peopleListPage); err != nil {
			return err
		}
		if err := groupDeliveredTx(ctx, tx, person, out); err != nil {
			return err
		}
		if unread, err = groupUnreadTx(ctx, tx, person, archived); err != nil {
			return err
		}
		invites, err = groupInvitesCountTx(ctx, tx, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"groups": out, "unread": unread, "invites": invites, "more": more}
	if more {
		last := out[len(out)-1]
		body["next"] = listCursor{last.LastAt, last.ID}.String()
	}
	writeJSON(w, http.StatusOK, body)
}

// groupDeliveredTx marks the groups listed delivered to the person up to
// their last message (the list shows it), as the app having it.
func groupDeliveredTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, list []groupView) error {
	for _, g := range list {
		if !g.Left && g.LastAt.After(g.JoinedAt) {
			if err := deliveredUptoTx(ctx, tx, person, g.ID, g.LastAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// deliveredUptoTx moves the person's delivered mark in a group to at.
func deliveredUptoTx(ctx context.Context, tx pgx.Tx, person, group uuid.UUID, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE group_members SET delivered_upto = $3 WHERE group_id = $1 AND person_id = $2 AND left_at IS NULL AND delivered_upto < $3`,
		group, person, at)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO group_reads (group_id, person_id, kind, upto) VALUES ($1, $2, 'delivered', $3) ON CONFLICT DO NOTHING`, group, person, at)
	return err
}

// --- making a group ---

// cleanGroupText trims a name or description and checks its length.
func cleanGroupText(v string, max int, what string, required bool) (string, error) {
	v = strings.TrimSpace(v)
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > max || (required && v == "") || strings.ContainsAny(v, "\x00") {
		if required {
			return "", customerSendErr{http.StatusBadRequest, fmt.Sprintf("%s is required, up to %d characters", what, max)}
		}
		return "", customerSendErr{http.StatusBadRequest, fmt.Sprintf("%s is up to %d characters", what, max)}
	}
	return v, nil
}

// createGroup is POST /v1/me/groups {name, description?, person_ids?}:
// a new group, the person its first member and admin (§9); the people
// named are added as an admin adds them (each added or invited: A3).
func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		PersonIDs   []uuid.UUID `json:"person_ids"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	name, err := cleanGroupText(in.Name, groupNameMax, "name", true)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	about, err := cleanGroupText(in.Description, groupAboutMax, "description", false)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if len(in.PersonIDs) >= groupMax {
		s.personFail(w, r, errGroupFull)
		return
	}
	if !s.limiter("group-create:"+person.String(), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	id := uuid.Must(uuid.NewV7())
	key, keySealed, kmsKey, err := s.keys().NewGroupKey(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, _ := json.Marshal(groupInfo{Name: name, Description: about})
	sealed, err := peoplechats.Seal(key, id, groupInfoRow, raw)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out groupView
	var results []addResult
	var owed []groupInvitePush
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		results, owed = []addResult{}, nil
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		// Groups made an hour and a day (§8.2), by the account's age and
		// signals.
		if err := s.groupMakeAllowedTx(ctx, tx, person, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_group_create($1, $2, $3, $4)`, id, sealed, keySealed, kmsKey); err != nil {
			return err
		}
		if err := groupEventTx(ctx, tx, id, person, "created", nil); err != nil {
			return err
		}
		if len(in.PersonIDs) > 0 {
			g, err := groupTx(ctx, tx, person, id.String(), true)
			if err != nil {
				return err
			}
			if results, owed, err = s.addPeopleTx(ctx, tx, person, g, in.PersonIDs); err != nil {
				return err
			}
		}
		out, err = s.groupOneTx(ctx, tx, person, id)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	s.pushGroupInvites(ctx, owed)
	out.Kept = peopleKeptWords
	writeJSON(w, http.StatusCreated, map[string]any{"group": out, "added": results})
}

// --- one group ---

// getGroup is GET /v1/me/groups/{id}: one group, as the person sees it.
func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out groupView
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		out, err = s.groupOneTx(ctx, tx, person, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out.Kept = peopleKeptWords
	writeJSON(w, http.StatusOK, out)
}

// putGroup is PUT /v1/me/groups/{id} {name?, description?,
// admins_only_post?, admins_only_edit?, approve_joins?}: the group's
// info (admins, or everyone when admins_only_edit is off: A2) and its
// settings (admins only).
func (s *Server) putGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		AdminsOnlyPost *bool   `json:"admins_only_post"`
		AdminsOnlyEdit *bool   `json:"admins_only_edit"`
		ApproveJoins   *bool   `json:"approve_joins"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	info := in.Name != nil || in.Description != nil
	settings := in.AdminsOnlyPost != nil || in.AdminsOnlyEdit != nil || in.ApproveJoins != nil
	if !info && !settings {
		writeErr(w, http.StatusBadRequest, "name, description or a setting is required")
		return
	}
	var out groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		if settings && !g.admin() {
			return errGroupNotAdmin
		}
		if info && g.AdminsOnlyEdit && !g.admin() {
			return errGroupNoEdit
		}
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		if info {
			key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
			if err != nil {
				return err
			}
			v := openGroupInfo(key, g.ID, g.sealed)
			if in.Name != nil {
				if v.Name, err = cleanGroupText(*in.Name, groupNameMax, "name", true); err != nil {
					return err
				}
			}
			if in.Description != nil {
				if v.Description, err = cleanGroupText(*in.Description, groupAboutMax, "description", false); err != nil {
					return err
				}
			}
			raw, _ := json.Marshal(v)
			sealed, err := peoplechats.Seal(key, g.ID, groupInfoRow, raw)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE people_groups SET sealed = $2 WHERE id = $1`, g.ID, sealed); err != nil {
				return err
			}
			if err := groupEventTx(ctx, tx, g.ID, person, "info", nil); err != nil {
				return err
			}
		}
		if settings {
			if _, err := tx.Exec(ctx, `UPDATE people_groups SET admins_only_post = COALESCE($2, admins_only_post), admins_only_edit = COALESCE($3, admins_only_edit),
				approve_joins = COALESCE($4, approve_joins) WHERE id = $1`, g.ID, in.AdminsOnlyPost, in.AdminsOnlyEdit, in.ApproveJoins); err != nil {
				return err
			}
			if err := groupEventTx(ctx, tx, g.ID, person, "settings", nil); err != nil {
				return err
			}
		}
		out, err = s.groupOneTx(ctx, tx, person, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putMyGroup is PUT /v1/me/groups/{id}/me {muted?, archived?, mentions?}:
// the person's own settings for the group; mentions: a mention pushes even
// while the group is muted (A7), on by default.
func (s *Server) putMyGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Muted    *bool `json:"muted"`
		Archived *bool `json:"archived"`
		Mentions *bool `json:"mentions"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Muted == nil && in.Archived == nil && in.Mentions == nil {
		writeErr(w, http.StatusBadRequest, "muted, archived or mentions is required")
		return
	}
	var out groupView
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE group_members SET muted = COALESCE($3, muted), archived = COALESCE($4, archived), mentions = COALESCE($5, mentions)
			WHERE group_id = $1 AND person_id = $2`, g.ID, person, in.Muted, in.Archived, in.Mentions); err != nil {
			return err
		}
		out, err = s.groupOneTx(ctx, tx, person, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// leaveGroupTx takes the person out of a group they are in (g, its row
// locked): the last one in closes it, deleting all of it (§14); the last
// admin first makes the member in it longest an admin (§9). how is left
// (the person's own choice) or deleted (their account); answers whether
// the group was closed.
func leaveGroupTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, g groupRow) (bool, error) {
	var others, admins int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE person_id <> $2), count(*) FILTER (WHERE person_id <> $2 AND role = 'admin')
		FROM group_members WHERE group_id = $1 AND left_at IS NULL`, g.ID, person).Scan(&others, &admins); err != nil {
		return false, err
	}
	if others == 0 {
		_, err := tx.Exec(ctx, `DELETE FROM people_groups WHERE id = $1`, g.ID)
		return true, err
	}
	if g.Role == "admin" && admins == 0 {
		var next uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND left_at IS NULL AND person_id <> $2
			ORDER BY joined_at, person_id LIMIT 1`, g.ID, person).Scan(&next); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE group_members SET role = 'admin' WHERE group_id = $1 AND person_id = $2`, g.ID, next); err != nil {
			return false, err
		}
		if err := groupEventTx(ctx, tx, g.ID, person, "admin", &next); err != nil {
			return false, err
		}
	}
	if err := groupEventTx(ctx, tx, g.ID, person, "left", nil); err != nil {
		return false, err
	}
	// Their typing and any request of theirs to join (an old one would
	// let an admin bring them back past their settings) go.
	for _, q := range []string{
		`DELETE FROM group_typing WHERE group_id = $1 AND person_id = $2`,
		`DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = $2`,
	} {
		if _, err := tx.Exec(ctx, q, g.ID, person); err != nil {
			return false, err
		}
	}
	// One time for both: they see nothing from when they left.
	_, err := tx.Exec(ctx, `WITH t AS (SELECT clock_timestamp() AS at)
		UPDATE group_members SET left_at = t.at, left_how = 'left', left_sealed = (SELECT sealed FROM people_groups WHERE id = $1),
		visible = visible - tstzmultirange(tstzrange(t.at, NULL)) FROM t WHERE group_id = $1 AND person_id = $2`, g.ID, person)
	return false, err
}

// leaveGroup is POST /v1/me/groups/{id}/leave: the person leaves (A5):
// their copy up to now stays theirs until they delete it; they see nothing
// new and can't write. The last one out closes the group.
func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	closed := false
	var out *groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		out = nil
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if closed, err = leaveGroupTx(ctx, tx, person, g); err != nil || closed {
			return err
		}
		v, err := s.groupOneTx(ctx, tx, person, g.ID)
		out = &v
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"left": true, "closed": closed, "group": out})
}

// deleteGroupForMe is DELETE /v1/me/groups/{id}: the person's copy goes.
// Someone who left or was removed: the group leaves their list for good
// (their marks with it). Someone in it: their copy up to now is cleared
// and the group is out of their list until a new message.
func (s *Server) deleteGroupForMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if !g.current() {
			if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND person_id = $2`, g.ID, person); err != nil {
				return err
			}
			return goneTx(ctx, tx, person, g.ID, uuid.Nil)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_marks WHERE group_id = $1 AND person_id = $2`, g.ID, person); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE group_members SET hidden_at = clock_timestamp(), archived = false,
			read_upto = GREATEST(read_upto, clock_timestamp()), visible = visible - tstzmultirange(tstzrange(NULL, clock_timestamp()))
			WHERE group_id = $1 AND person_id = $2`, g.ID, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- the group's photo ---

// groupPhoto is GET /v1/me/groups/{id}/photo: the group's photo, for its
// members (and someone invited).
func (s *Server) groupPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	var ctype string
	var data []byte
	var at time.Time
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT type, sealed, updated_at FROM group_photos WHERE group_id = $1`, id).Scan(&ctype, &sealed, &at); err != nil {
			return err
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, id)
		if err != nil {
			return err
		}
		data, err = peoplechats.Open(key, id, groupPhotoRow, sealed)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writePhoto(w, r, ctype, data, at, "private, max-age=86400")
}

// putGroupPhoto is PUT /v1/me/groups/{id}/photo (a form with a file): the
// group's photo, by whoever may edit its info (A2).
func (s *Server) putGroupPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.limiter("group-photo:"+person.String(), 10*time.Second, 6).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		writeErr(w, http.StatusBadRequest, "Send the photo as a form with a file field, at most 10 MB.")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "The form has no file.")
		return
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "The photo could not be read.")
		return
	}
	ctype, clean, err := media.CheckProfilePhoto(data)
	if err != nil {
		s.personFail(w, r, photoErr(err))
		return
	}
	var out groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		if g.AdminsOnlyEdit && !g.admin() {
			return errGroupNoEdit
		}
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		key, err := s.keys().GroupKeyTx(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		sealed, err := peoplechats.Seal(key, g.ID, groupPhotoRow, clean)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO group_photos (group_id, type, sealed) VALUES ($1, $2, $3)
			ON CONFLICT (group_id) DO UPDATE SET type = EXCLUDED.type, sealed = EXCLUDED.sealed, updated_at = now()`, g.ID, ctype, sealed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE people_groups SET photo_at = now() WHERE id = $1`, g.ID); err != nil {
			return err
		}
		if err := groupEventTx(ctx, tx, g.ID, person, "photo", nil); err != nil {
			return err
		}
		out, err = s.groupOneTx(ctx, tx, person, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteGroupPhoto is DELETE /v1/me/groups/{id}/photo.
func (s *Server) deleteGroupPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var out groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		if g.AdminsOnlyEdit && !g.admin() {
			return errGroupNoEdit
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_photos WHERE group_id = $1`, g.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE people_groups SET photo_at = NULL WHERE id = $1`, g.ID); err != nil {
			return err
		}
		if err := groupEventTx(ctx, tx, g.ID, person, "photo", nil); err != nil {
			return err
		}
		out, err = s.groupOneTx(ctx, tx, person, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- members ---

// groupMemberOut is one member of a group as the person sees them.
type groupMemberOut struct {
	PersonID uuid.UUID `json:"person_id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
	You      bool      `json:"you"`
	// Person: their card as their settings let the person see them
	// (photo, last seen; none across a block either way).
	Person *personCardOut `json:"person,omitempty"`
	// Phone: only when they chose to show their number (§17 Q5).
	Phone *string `json:"phone,omitempty"`
}

// groupInviteOut is an invite waiting, as an admin sees it.
type groupPendingOut struct {
	PersonID uuid.UUID `json:"person_id"`
	Name     string    `json:"name"`
	At       time.Time `json:"at"`
}

// groupMembers is GET /v1/me/groups/{id}/members: the members now, admins
// first; to admins also the invites waiting (each answered as any invite,
// A8) and the people waiting to join.
func (s *Server) groupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []groupMemberOut{}
	var invited, waiting []groupPendingOut
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if !g.current() {
			return errGroupLeft
		}
		rows, err := tx.Query(ctx, `SELECT person_id, role, joined_at FROM group_members WHERE group_id = $1 AND left_at IS NULL
			ORDER BY role = 'admin' DESC, joined_at, person_id`, g.ID)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupMemberOut, error) {
			var m groupMemberOut
			return m, r.Scan(&m.PersonID, &m.Role, &m.JoinedAt)
		}); err != nil {
			return err
		}
		ids := []uuid.UUID{}
		for _, m := range out {
			ids = append(ids, m.PersonID)
		}
		if g.admin() {
			for _, q := range []struct {
				sql  string
				dest *[]groupPendingOut
			}{
				{`SELECT person_id, created_at FROM group_invites WHERE group_id = $1 ORDER BY created_at`, &invited},
				{`SELECT person_id, created_at FROM group_join_requests WHERE group_id = $1 ORDER BY created_at`, &waiting},
			} {
				rows, err := tx.Query(ctx, q.sql, g.ID)
				if err != nil {
					return err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupPendingOut, error) {
					var p groupPendingOut
					return p, r.Scan(&p.PersonID, &p.At)
				})
				if err != nil {
					return err
				}
				*q.dest = list
				for _, p := range list {
					ids = append(ids, p.PersonID)
				}
			}
		}
		names, cards, err := s.groupNamesTx(ctx, tx, person, ids)
		if err != nil {
			return err
		}
		phones := map[uuid.UUID]string{}
		prow, err := tx.Query(ctx, `SELECT person_id, number FROM taskiem_group_phones($1)`, g.ID)
		if err != nil {
			return err
		}
		for prow.Next() {
			var id uuid.UUID
			var n string
			if err := prow.Scan(&id, &n); err != nil {
				prow.Close()
				return err
			}
			phones[id] = n
		}
		prow.Close()
		if err := prow.Err(); err != nil {
			return err
		}
		for i := range out {
			m := &out[i]
			m.Name, m.You = names[m.PersonID], m.PersonID == person
			if c, ok := cards[m.PersonID]; ok {
				o := cardOut(c)
				m.Person = &o
			}
			if n, ok := phones[m.PersonID]; ok {
				m.Phone = &n
			}
		}
		// A deleted account's invite or wait is no one's any more.
		keep := func(list []groupPendingOut) []groupPendingOut {
			out := []groupPendingOut{}
			for _, p := range list {
				if c, ok := cards[p.PersonID]; ok && c.Deleted && !c.Suspended {
					continue
				}
				p.Name = names[p.PersonID]
				out = append(out, p)
			}
			return out
		}
		if g.admin() {
			invited, waiting = keep(invited), keep(waiting)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	body := map[string]any{"members": out}
	if invited != nil {
		body["invited"] = invited
		body["waiting"] = waiting
	}
	writeJSON(w, http.StatusOK, body)
}

// addResult is what became of one person an admin added: added, invited
// (an invite waits in their Requests: their "Who can add me to groups" or
// a contact the admin has not added, A3; an add across a block is said
// the same, A8), member (in it already), full, or unavailable (no such
// active account).
type addResult struct {
	PersonID uuid.UUID `json:"person_id"`
	State    string    `json:"state"`
}

// groupInvitePush is a push owed for an invite.
type groupInvitePush struct {
	to, group, by uuid.UUID
}

// addPeopleTx adds people to a group as admin (g, its row locked).
func (s *Server) addPeopleTx(ctx context.Context, tx pgx.Tx, admin uuid.UUID, g groupRow, ids []uuid.UUID) ([]addResult, []groupInvitePush, error) {
	if !g.admin() {
		return nil, nil, errGroupNotAdmin
	}
	if err := g.changeable(); err != nil {
		return nil, nil, err
	}
	if err := notSuspendedTx(ctx, tx, admin); err != nil {
		return nil, nil, err
	}
	var uniq []uuid.UUID
	for _, id := range ids {
		if id != admin && !slices.Contains(uniq, id) {
			uniq = append(uniq, id)
		}
	}
	if len(uniq) > groupMax {
		return nil, nil, errGroupFull
	}
	type may struct{ active, allowed, blocked bool }
	got := map[uuid.UUID]may{}
	if len(uniq) > 0 {
		rows, err := tx.Query(ctx, `SELECT person_id, active, allowed, blocked FROM taskiem_group_may_add($1)`, uniq)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			var m may
			if err := rows.Scan(&id, &m.active, &m.allowed, &m.blocked); err != nil {
				rows.Close()
				return nil, nil, err
			}
			got[id] = m
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1 AND left_at IS NULL`, g.ID).Scan(&count); err != nil {
		return nil, nil, err
	}
	// Who of them is or was in the group, and whom the admin has as a
	// contact: two queries for up to 256 people.
	type had struct{ left *time.Time }
	was := map[uuid.UUID]had{}
	contacts := map[uuid.UUID]bool{}
	if len(uniq) > 0 {
		rows, err := tx.Query(ctx, `SELECT person_id, left_at FROM group_members WHERE group_id = $1 AND person_id = ANY ($2)`, g.ID, uniq)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			var h had
			if err := rows.Scan(&id, &h.left); err != nil {
				rows.Close()
				return nil, nil, err
			}
			was[id] = h
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		if contacts, err = people.ContactsAmongTx(ctx, tx, admin, uniq); err != nil {
			return nil, nil, err
		}
	}
	// People who aren't the admin's contacts get an invite (§9); how many
	// an admin may invite in a day is limited (§8.2): those not yet in the
	// group or invited to it count.
	var strangers []uuid.UUID
	for _, id := range uniq {
		if m, ok := got[id]; ok && m.active && !contacts[id] {
			if h, found := was[id]; !found || h.left != nil {
				strangers = append(strangers, id)
			}
		}
	}
	if len(strangers) > 0 {
		rows, err := tx.Query(ctx, `SELECT person_id FROM group_invites WHERE group_id = $1 AND person_id = ANY ($2)`, g.ID, strangers)
		if err != nil {
			return nil, nil, err
		}
		invited, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, nil, err
		}
		if err := s.groupInvitesAllowedTx(ctx, tx, admin, len(strangers)-len(invited)); err != nil {
			return nil, nil, err
		}
	}
	newStrangers := 0
	out := []addResult{}
	var owed []groupInvitePush
	var added []uuid.UUID
	for _, id := range uniq {
		m, ok := got[id]
		if !ok || !m.active {
			out = append(out, addResult{id, "unavailable"})
			continue
		}
		h, found := was[id]
		if found && h.left == nil {
			out = append(out, addResult{id, "member"})
			continue
		}
		if count >= groupMax {
			out = append(out, addResult{id, "full"})
			continue
		}
		if m.blocked || !m.allowed || !contacts[id] {
			// An invite (A3); across a block, one they are never shown (A8):
			// the admin is told "invited" either way.
			// An invite kept unseen across a block becomes an ordinary one
			// when asked again once the block is gone.
			tag, err := tx.Exec(ctx, `INSERT INTO group_invites (group_id, person_id, invited_by, hidden) VALUES ($1, $2, $3, $4)
				ON CONFLICT (group_id, person_id) DO UPDATE SET hidden = false, invited_by = EXCLUDED.invited_by, created_at = clock_timestamp()
				WHERE group_invites.hidden AND NOT EXCLUDED.hidden`, g.ID, id, admin, m.blocked)
			if err != nil {
				return nil, nil, err
			}
			if tag.RowsAffected() == 1 && !m.blocked {
				owed = append(owed, groupInvitePush{to: id, group: g.ID, by: admin})
			}
			if tag.RowsAffected() == 1 && !contacts[id] {
				newStrangers++
			}
			out = append(out, addResult{id, "invited"})
			continue
		}
		if found {
			if _, err := tx.Exec(ctx, `UPDATE group_members SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'added', added_by = $3, role = 'member',
				joined_at = clock_timestamp(), hidden_at = NULL, visible = visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
				WHERE group_id = $1 AND person_id = $2`, g.ID, id, admin); err != nil {
				return nil, nil, err
			}
		} else if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, person_id, how, added_by, visible)
			VALUES ($1, $2, 'added', $3, tstzmultirange(tstzrange(clock_timestamp(), NULL)))`, g.ID, id, admin); err != nil {
			return nil, nil, err
		}
		target := id
		if err := groupEventTx(ctx, tx, g.ID, admin, "added", &target); err != nil {
			return nil, nil, err
		}
		count++
		added = append(added, id)
		out = append(out, addResult{id, "added"})
	}
	if err := groupLedgerAddTx(ctx, tx, admin, "invite", g.ID, newStrangers); err != nil {
		return nil, nil, err
	}
	if len(added) > 0 {
		// In the group now: their invites, requests to join and being kept
		// as removed go.
		for _, q := range []string{
			`DELETE FROM group_invites WHERE group_id = $1 AND person_id = ANY ($2)`,
			`DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = ANY ($2)`,
			`DELETE FROM group_removed WHERE group_id = $1 AND person_id = ANY ($2)`,
		} {
			if _, err := tx.Exec(ctx, q, g.ID, added); err != nil {
				return nil, nil, err
			}
		}
	}
	return out, owed, nil
}

// addGroupMembers is POST /v1/me/groups/{id}/members {person_ids}: an
// admin adds people (§9). Each is added at once only when the admin has
// them as a contact and their "Who can add me to groups" lets the admin;
// otherwise an invite waits in their Requests (A3). An add across a block
// is answered "invited" as any invite is, and they never see it (A8).
func (s *Server) addGroupMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		PersonIDs []uuid.UUID `json:"person_ids"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.PersonIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "person_ids names the people to add")
		return
	}
	var out []addResult
	var owed []groupInvitePush
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		out, owed, err = s.addPeopleTx(ctx, tx, person, g, in.PersonIDs)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	s.pushGroupInvites(ctx, owed)
	writeJSON(w, http.StatusOK, map[string]any{"added": out})
}

// memberParam is the person a members route names.
func memberParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "person"))
	if err != nil {
		return uuid.Nil, errDoorNone
	}
	return id, nil
}

// setGroupAdmin is PUT /v1/me/groups/{id}/members/{person} {admin}: an
// admin makes a member an admin, or no longer one; a group keeps at least
// one admin.
func (s *Server) setGroupAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Admin *bool `json:"admin"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Admin == nil {
		writeErr(w, http.StatusBadRequest, "admin is required")
		return
	}
	target, err := memberParam(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if !g.admin() {
			return errGroupNotAdmin
		}
		if err := g.changeable(); err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT role FROM group_members WHERE group_id = $1 AND person_id = $2 AND left_at IS NULL`, g.ID, target).Scan(&role); err != nil {
			return err
		}
		want := map[bool]string{true: "admin", false: "member"}[*in.Admin]
		if role == want {
			return nil
		}
		if !*in.Admin {
			var admins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1 AND left_at IS NULL AND role = 'admin'`, g.ID).Scan(&admins); err != nil {
				return err
			}
			if admins <= 1 {
				return customerSendErr{http.StatusConflict, "A group needs at least one admin."}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE group_members SET role = $3 WHERE group_id = $1 AND person_id = $2`, g.ID, target, want); err != nil {
			return err
		}
		return groupEventTx(ctx, tx, g.ID, person, map[bool]string{true: "admin", false: "unadmin"}[*in.Admin], &target)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"person_id": target, "admin": *in.Admin})
}

// removeGroupMember is DELETE /v1/me/groups/{id}/members/{person}: an
// admin removes a member (A5: their copy up to now stays theirs; they see
// nothing new and can't write).
func (s *Server) removeGroupMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	target, err := memberParam(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if target == person {
		writeErr(w, http.StatusBadRequest, "Leave the group instead (POST /v1/me/groups/{id}/leave).")
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if !g.admin() {
			return errGroupNotAdmin
		}
		if err := g.changeable(); err != nil {
			return err
		}
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM group_members WHERE group_id = $1 AND person_id = $2 AND left_at IS NULL`, g.ID, target).Scan(&one); err != nil {
			return err
		}
		if err := groupEventTx(ctx, tx, g.ID, person, "removed", &target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `WITH t AS (SELECT clock_timestamp() AS at)
			UPDATE group_members SET left_at = t.at, left_how = 'removed', removed_by = $3,
			left_sealed = (SELECT sealed FROM people_groups WHERE id = $1), visible = visible - tstzmultirange(tstzrange(t.at, NULL))
			FROM t WHERE group_id = $1 AND person_id = $2`, g.ID, target, person); err != nil {
			return err
		}
		// Kept as removed after they delete their copy: a link join of
		// theirs waits for an admin until an admin lets them back.
		if _, err := tx.Exec(ctx, `INSERT INTO group_removed (group_id, person_id, removed_by) VALUES ($1, $2, $3)
			ON CONFLICT (group_id, person_id) DO NOTHING`, g.ID, target, person); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM group_typing WHERE group_id = $1 AND person_id = $2`,
			`DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = $2`,
		} {
			if _, err := tx.Exec(ctx, q, g.ID, target); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// blockGroupMember is POST /v1/me/groups/{id}/members/{person}/block: the
// person blocks a member of a group they share (A8): they still see the
// member's messages in the group; the member can no longer message them
// one to one, nor add them to groups (an add becomes an invite they never
// see), nor see their last seen or photo. Listed with the person's
// blocked people (unblock with DELETE /v1/me/people/{chat_id}/block).
func (s *Server) blockGroupMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	target, err := memberParam(r)
	if err != nil || target == person {
		s.personFail(w, r, errDoorNone)
		return
	}
	var chat uuid.UUID
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		// Someone the person was in the group with: their times in it
		// overlap (the latest of each, or what each sees). Anyone else is
		// answered as an unknown id: someone who joined after the person
		// left is not theirs to know.
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM group_members t JOIN group_members c ON c.group_id = t.group_id AND c.person_id = $3
			WHERE t.group_id = $1 AND t.person_id = $2
			  AND (tstzrange(t.joined_at, t.left_at) && tstzrange(c.joined_at, c.left_at) OR t.visible && c.visible)`, g.ID, target, person).Scan(&one); err != nil {
			return err
		}
		// The block is kept as a one-to-one block is, with the person's own
		// copy of a chat with them (made, hidden and empty, when there is
		// none) so it is listed by name.
		err = tx.QueryRow(ctx, `SELECT id FROM people_chats WHERE person_id = $1 AND peer_id = $2`, person, target).Scan(&chat)
		if errors.Is(err, pgx.ErrNoRows) {
			chat = uuid.Must(uuid.NewV7())
			_, err = tx.Exec(ctx, `INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, empty, hidden_at)
				VALUES ($1, $2, $3, taskiem_people_pair($2, $3), false, true, now())`, chat, person, target)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO people_blocks (person_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, person, target)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocked": true, "chat_id": chat})
}

// --- invites ---

// groupInviteOut is an invite waiting in the person's Requests.
type groupInviteOut struct {
	GroupID   uuid.UUID `json:"group_id"`
	Name      string    `json:"name"`
	Photo     *photoRef `json:"photo"`
	InvitedBy uuid.UUID `json:"invited_by"`
	// By: the name of who invited them, as the person sees them.
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// listGroupInvites is GET /v1/me/groups/invites: the invites to groups
// waiting for the person (Accept, Decline), the latest first.
func (s *Server) listGroupInvites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []groupInviteOut{}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		out, err = s.groupInvitesTx(ctx, tx, person, nil)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

// groupInvitesTx is the invites waiting for the person (only, when set,
// that group's).
func (s *Server) groupInvitesTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, only *uuid.UUID) ([]groupInviteOut, error) {
	rows, err := tx.Query(ctx, `SELECT i.group_id, i.invited_by, i.created_at, g.sealed, g.photo_at
		FROM group_invites i JOIN people_groups g ON g.id = i.group_id
		WHERE i.person_id = $1 AND NOT i.hidden AND ($2::uuid IS NULL OR i.group_id = $2) ORDER BY i.created_at DESC LIMIT 200`, person, only)
	if err != nil {
		return nil, err
	}
	type inv struct {
		groupInviteOut
		by     *uuid.UUID
		sealed []byte
		photo  *time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inv, error) {
		var i inv
		return i, r.Scan(&i.GroupID, &i.by, &i.At, &i.sealed, &i.photo)
	})
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, i := range list {
		if i.by != nil {
			ids = append(ids, *i.by)
		}
	}
	names, _, err := s.groupNamesTx(ctx, tx, person, ids)
	if err != nil {
		return nil, err
	}
	out := []groupInviteOut{}
	for _, i := range list {
		key, err := s.keys().GroupKeyTx(ctx, tx, i.GroupID)
		if err != nil && !errors.Is(err, peoplechats.ErrNoKey) {
			return nil, err
		}
		v := i.groupInviteOut
		v.Name = openGroupInfo(key, i.GroupID, i.sealed).Name
		v.Photo = groupPhotoRef(i.GroupID, i.photo)
		if i.by != nil {
			v.InvitedBy, v.By = *i.by, names[*i.by]
		} else {
			v.By = peopleGoneName
		}
		out = append(out, v)
	}
	return out, nil
}

// acceptGroupInvite is POST /v1/me/groups/invites/{id}/accept: the person
// joins the group from now (§17 Q16).
func (s *Server) acceptGroupInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	var out groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT taskiem_group_accept($1, $2)`, id, groupMax).Scan(&state); err != nil {
			return err
		}
		switch state {
		case "none":
			return errDoorNone
		case "full":
			return errGroupFull
		case "joined":
			// The definer holds the group's row: a group its moderators
			// closed takes no one in.
			if err := groupOpenNowTx(ctx, tx, id); err != nil {
				return err
			}
			if err := groupEventTx(ctx, tx, id, person, "joined", nil); err != nil {
				return err
			}
		}
		out, err = s.groupOneTx(ctx, tx, person, id)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out.Kept = peopleKeptWords
	writeJSON(w, http.StatusOK, out)
}

// declineGroupInvite is POST /v1/me/groups/invites/{id}/decline: the
// invite goes; whoever invited is not told.
func (s *Server) declineGroupInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM group_invites WHERE group_id = $1 AND person_id = $2 AND NOT hidden`, id, person)
		if err == nil && tag.RowsAffected() == 0 {
			return errDoorNone
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancelGroupInvite is DELETE /v1/me/groups/{id}/invites/{person}: an
// admin takes an invite back.
func (s *Server) cancelGroupInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	target, err := memberParam(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := groupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if !g.admin() {
			return errGroupNotAdmin
		}
		if err := g.changeable(); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM group_invites WHERE group_id = $1 AND person_id = $2`, g.ID, target)
		if err == nil && tag.RowsAffected() == 0 {
			return errDoorNone
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pushGroupInvites tells each person invited, on their own devices (kind
// group_invite, under their Requests switch).
func (s *Server) pushGroupInvites(ctx context.Context, owed []groupInvitePush) {
	if !s.Push.Enabled() || len(owed) == 0 {
		return
	}
	for _, p := range owed {
		var by, group string
		_ = db.InPersonTx(ctx, s.Store.Pool, p.to, func(tx pgx.Tx) error {
			list, err := s.groupInvitesTx(ctx, tx, p.to, &p.group)
			if err != nil || len(list) == 0 {
				return err
			}
			by, group = list[0].By, list[0].Name
			return nil
		})
		if group == "" {
			continue
		}
		err := db.InPersonTx(ctx, s.Store.Pool, p.to, func(tx pgx.Tx) error {
			n, err := notifyTx(ctx, tx, p.to)
			if err != nil || !n.Requests {
				return err
			}
			live, err := push.LiveYouTx(ctx, tx, p.to, s.Push.WebPushOn(), s.Push.FCMOn())
			if err != nil || !live {
				return err
			}
			return push.EnqueueTx(ctx, tx, uuid.Nil, []uuid.UUID{p.to}, push.Queued{Kind: push.KindGroupInvite, Title: group,
				Body: by + " invited you to the group", Link: "/you/groups/invites", Tag: "group-invite:" + p.group.String()})
		})
		if err != nil {
			s.Logger.Warn("groups: invite push", "err", err)
		}
	}
}

// --- the invite link ---

// groupLinkOut is the group's link as an admin sees it.
type groupLinkOut struct {
	On        bool       `json:"on"`
	Code      string     `json:"code,omitempty"`
	URL       string     `json:"url,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	Words     string     `json:"words,omitempty"`
	QR        string     `json:"qr,omitempty"`
}

func (s *Server) groupLinkView(group uuid.UUID, nonce []byte, at time.Time) groupLinkOut {
	p := s.people()
	code := p.GroupLinkCode(nonce)
	url := p.GroupLinkURL(code)
	return groupLinkOut{On: true, Code: code, URL: url, CreatedAt: &at, QR: "/v1/me/groups/" + group.String() + "/link/qr",
		Words: "Join my group on Taskiem: " + url}
}

// liveGroupLinkTx is the group's live link, if it has one.
func (s *Server) liveGroupLinkTx(ctx context.Context, tx pgx.Tx, group uuid.UUID) (groupLinkOut, error) {
	var nonce []byte
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT nonce, created_at FROM group_links WHERE group_id = $1 AND replaced_at IS NULL`, group).Scan(&nonce, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupLinkOut{On: false}, nil
	}
	if err != nil {
		return groupLinkOut{}, err
	}
	return s.groupLinkView(group, nonce, at), nil
}

// adminGroupTx is a group the person is an admin of now.
func adminGroupTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, id string, lock bool) (groupRow, error) {
	g, err := groupTx(ctx, tx, person, id, lock)
	if err == nil && !g.admin() {
		err = errGroupNotAdmin
	}
	return g, err
}

// groupLink is GET /v1/me/groups/{id}/link: the group's invite link
// (admins), {on: false} while it has none.
func (s *Server) groupLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.people().Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	var out groupLinkOut
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		out, err = s.liveGroupLinkTx(ctx, tx, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// newGroupLink is POST /v1/me/groups/{id}/link: turns the link on, or
// replaces it (the old one answers 410 from now).
func (s *Server) newGroupLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.people().Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	if !s.limiter("group-link-new:"+person.String(), time.Minute, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many new links. Wait a minute.")
		return
	}
	var out groupLinkOut
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		if g.standing.LinkStoppedAt != nil {
			return errGroupLinkStopped
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_links WHERE group_id = $1 AND replaced_at < now() - $2::interval`, g.ID, people.LinkKeptFor.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE group_links SET replaced_at = now() WHERE group_id = $1 AND replaced_at IS NULL`, g.ID); err != nil {
			return err
		}
		for try := 0; try < 5; try++ {
			nonce := make([]byte, 16)
			if _, err := rand.Read(nonce); err != nil {
				return err
			}
			code := s.people().GroupLinkCode(nonce)
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_group_link_find($1))`, s.people().GroupLinkHash(code)).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			var at time.Time
			if err := tx.QueryRow(ctx, `INSERT INTO group_links (id, group_id, code_hash, nonce, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
				uuid.Must(uuid.NewV7()), g.ID, s.people().GroupLinkHash(code), nonce, person).Scan(&at); err != nil {
				return err
			}
			out = s.groupLinkView(g.ID, nonce, at)
			return nil
		}
		return errors.New("groups: no free link code")
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// groupLinkOff is DELETE /v1/me/groups/{id}/link: no link; the last one
// answers 410 from now.
func (s *Server) groupLinkOff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE group_links SET replaced_at = now() WHERE group_id = $1 AND replaced_at IS NULL`, g.ID)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// groupLinkQR is GET /v1/me/groups/{id}/link/qr[?format=png]: the link as
// a QR code; 404 while there is no link.
func (s *Server) groupLinkQR(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.people().Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	var l groupLinkOut
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		l, err = s.liveGroupLinkTx(ctx, tx, g.ID)
		if err == nil && !l.On {
			err = errDoorNone
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "png" {
		png, err := storefront.QRPNG(l.URL)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	svg, err := storefront.QRSVG(l.URL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, svg)
}

// groupLinkFound is what a group's link names.
type groupLinkFound struct {
	group   uuid.UUID
	name    string
	members int
	photoAt *time.Time
}

// findGroupLink reads a group's link (anyone holding it): errDoorNone for
// no such link, errGroupLinkGone for one replaced or turned off.
func (s *Server) findGroupLink(ctx context.Context, code string) (groupLinkFound, error) {
	p := s.people()
	if !p.Keyed() {
		return groupLinkFound{}, errGroupNoLinks
	}
	var out groupLinkFound
	var replaced *time.Time
	var sealed []byte
	var keySealed, kmsKey string
	err := s.Store.Pool.QueryRow(ctx, `SELECT group_id, replaced_at, sealed, key_sealed, kms_key, members, photo_at FROM taskiem_group_link_find($1)`,
		p.GroupLinkHash(code)).Scan(&out.group, &replaced, &sealed, &keySealed, &kmsKey, &out.members, &out.photoAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errDoorNone
	}
	if err != nil {
		return out, err
	}
	if replaced != nil {
		if time.Since(*replaced) > people.LinkKeptFor {
			return out, errDoorNone
		}
		return out, errGroupLinkGone
	}
	key, err := s.keys().OpenGroupKey(ctx, out.group, keySealed, kmsKey)
	if err != nil {
		return out, err
	}
	out.name = openGroupInfo(key, out.group, sealed).Name
	return out, nil
}

// errGroupLinkGone is a replaced or turned off group link.
var errGroupLinkGone = errors.New("groups: this link no longer works")

const groupLinkGoneWords = "This group link no longer works. Ask someone in the group for a new one."

// groupLinkFail answers a link that does not open a group.
func (s *Server) groupLinkFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errGroupLinkGone):
		writeJSON(w, http.StatusGone, map[string]any{"error": groupLinkGoneWords, "gone": true})
	case errors.Is(err, errDoorNone):
		writeErr(w, http.StatusNotFound, "This is not a Taskiem group's link. Check it, or ask for it again.")
	default:
		s.personFail(w, r, err)
	}
}

// groupLinkPreview is GET /v1/group-links/{code}, for anyone holding the
// link (A4): the group's name, photo and how many are in it; nothing of
// its members or messages, nor whether joining waits for an admin (that
// would tell someone an admin blocked, A8: their join waits either way).
func (s *Server) groupLinkPreview(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("group-link-ip:"+clientIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a moment.")
		return
	}
	code := chi.URLParam(r, "code")
	f, err := s.findGroupLink(r.Context(), code)
	if err != nil {
		s.groupLinkFail(w, r, err)
		return
	}
	var photo any
	if f.photoAt != nil {
		photo = map[string]any{"url": "/v1/group-links/" + code + "/photo?v=" + strconv.FormatInt(f.photoAt.UnixMilli(), 10), "updated_at": *f.photoAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "name": f.name, "photo": photo, "members": f.members,
		"full": f.members >= groupMax, "link": s.people().GroupLinkURL(code)})
}

// groupLinkPhoto is GET /v1/group-links/{code}/photo: the group's photo for
// the link's preview.
func (s *Server) groupLinkPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.limiter("group-link-ip:"+clientIP(r), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a moment.")
		return
	}
	p := s.people()
	if !p.Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	var group uuid.UUID
	var ctype, keySealed, kmsKey string
	var sealed []byte
	err := s.Store.Pool.QueryRow(ctx, `SELECT group_id, type, sealed, key_sealed, kms_key FROM taskiem_group_link_photo($1)`, p.GroupLinkHash(chi.URLParam(r, "code"))).
		Scan(&group, &ctype, &sealed, &keySealed, &kmsKey)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	key, err := s.keys().OpenGroupKey(ctx, group, keySealed, kmsKey)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := peoplechats.Open(key, group, groupPhotoRow, sealed)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writePhoto(w, r, ctype, data, time.Time{}, "public, max-age=300")
}

// joinGroupByLink is POST /v1/me/group-links/{code}/join: the person joins
// the group at once (A4), or waits for an admin (the group approves new
// members): {state: joined|waiting|member, group?}. 409 full; 429 too many
// joins; 410 a replaced or turned off link; 404 no such link.
func (s *Server) joinGroupByLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	p := s.people()
	if !p.Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	if !s.limiter("group-join:"+person.String(), 6*time.Minute, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "You have joined many groups. Try again later.")
		return
	}
	code := chi.URLParam(r, "code")
	var state string
	var out *groupView
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		out = nil
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		var group *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT group_id, state FROM taskiem_group_join($1, $2, $3)`, p.GroupLinkHash(code), groupMax, groupLinkJoinsPerHour).
			Scan(&group, &state); err != nil {
			return err
		}
		switch state {
		case "none":
			return errDoorNone
		case "gone":
			return errGroupLinkGone
		case "full":
			return errGroupFull
		case "busy":
			return customerSendErr{http.StatusTooManyRequests, "Many people are joining this group right now. Try again later."}
		}
		if state == "waiting" || state == "joined" {
			// The definer holds the group's row: a closed group takes no
			// one; then the person's link joins an hour (§8.2), by their
			// account's age and signals.
			if err := groupOpenNowTx(ctx, tx, *group); err != nil {
				return err
			}
			if err := s.groupJoinAllowedTx(ctx, tx, person, *group); err != nil {
				return err
			}
		}
		switch state {
		case "waiting":
			return nil
		case "joined":
			if err := groupEventTx(ctx, tx, *group, person, "joined", nil); err != nil {
				return err
			}
		}
		v, err := s.groupOneTx(ctx, tx, person, *group)
		out = &v
		return err
	})
	if err != nil {
		s.groupLinkFail(w, r, err)
		return
	}
	body := map[string]any{"state": state}
	if out != nil {
		out.Kept = peopleKeptWords
		body["group"] = out
	}
	writeJSON(w, http.StatusOK, body)
}

// withdrawGroupJoin is DELETE /v1/me/group-links/{code}/join: the person
// takes back their request to join by this link (waiting for an admin),
// 204 whether or not one was waiting (the link may since be replaced or
// turned off); 404 for no such link.
func (s *Server) withdrawGroupJoin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	p := s.people()
	if !p.Keyed() {
		s.personFail(w, r, errGroupNoLinks)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		var group uuid.UUID
		err := tx.QueryRow(ctx, `SELECT group_id FROM taskiem_group_link_find($1)`, p.GroupLinkHash(chi.URLParam(r, "code"))).Scan(&group)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDoorNone
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = $2`, group, person)
		return err
	})
	if err != nil {
		s.groupLinkFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- people waiting to join (A4) ---

// groupJoinRequests is GET /v1/me/groups/{id}/joins: who waits for an
// admin to let them in, by the link.
func (s *Server) groupJoinRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := []groupPendingOut{}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT person_id, created_at FROM group_join_requests WHERE group_id = $1 ORDER BY created_at LIMIT 500`, g.ID)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (groupPendingOut, error) {
			var p groupPendingOut
			return p, r.Scan(&p.PersonID, &p.At)
		})
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for _, p := range list {
			ids = append(ids, p.PersonID)
		}
		names, cards, err := s.groupNamesTx(ctx, tx, person, ids)
		if err != nil {
			return err
		}
		for _, p := range list {
			if c, ok := cards[p.PersonID]; ok && c.Deleted && !c.Suspended {
				continue
			}
			p.Name = names[p.PersonID]
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// approveGroupJoin is POST /v1/me/groups/{id}/joins/{person}/approve: an
// admin lets someone waiting in; they join from now.
func (s *Server) approveGroupJoin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	target, err := memberParam(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	wasStale := false
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		wasStale = false
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		// The request, if it is newer than the person's latest joining or
		// leaving: an older one (they joined another way since, or left)
		// is stale and goes without letting them in, so an admin can't
		// bring back someone who left past "Who can add me to groups".
		var asked time.Time
		err = tx.QueryRow(ctx, `DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = $2 RETURNING created_at`, g.ID, target).Scan(&asked)
		if err != nil {
			return err
		}
		var stale bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id = $1 AND person_id = $2
			AND (joined_at >= $3 OR left_at >= $3))`, g.ID, target, asked).Scan(&stale); err != nil {
			return err
		}
		if stale {
			// Gone for good (committed), and said so.
			wasStale = true
			return nil
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id = $1 AND left_at IS NULL`, g.ID).Scan(&count); err != nil {
			return err
		}
		if count >= groupMax {
			return errGroupFull
		}
		var left *time.Time
		err = tx.QueryRow(ctx, `SELECT left_at FROM group_members WHERE group_id = $1 AND person_id = $2`, g.ID, target).Scan(&left)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, person_id, how, added_by, visible)
				VALUES ($1, $2, 'link', $3, tstzmultirange(tstzrange(clock_timestamp(), NULL)))`, g.ID, target, person); err != nil {
				return err
			}
		case err != nil:
			return err
		case left == nil:
			return nil
		default:
			if _, err := tx.Exec(ctx, `UPDATE group_members SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'link', added_by = $3, role = 'member',
				joined_at = clock_timestamp(), hidden_at = NULL, visible = visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
				WHERE group_id = $1 AND person_id = $2`, g.ID, target, person); err != nil {
				return err
			}
		}
		// Let in by an admin: no longer kept as removed.
		if _, err := tx.Exec(ctx, `DELETE FROM group_removed WHERE group_id = $1 AND person_id = $2`, g.ID, target); err != nil {
			return err
		}
		return groupEventTx(ctx, tx, g.ID, person, "added", &target)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if wasStale {
		writeErr(w, http.StatusConflict, "This request is no longer waiting: they joined or left the group since asking.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// declineGroupJoin is POST /v1/me/groups/{id}/joins/{person}/decline: an
// admin turns someone waiting away (they are not told).
func (s *Server) declineGroupJoin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	target, err := memberParam(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		g, err := adminGroupTx(ctx, tx, person, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		if err := g.changeable(); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM group_join_requests WHERE group_id = $1 AND person_id = $2`, g.ID, target)
		if err == nil && tag.RowsAffected() == 0 {
			return errDoorNone
		}
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- an account deleted (§14, §17 Q17) ---

// leaveAllGroupsTx takes a person whose account is being deleted out of
// every group, in the deletion's transaction: from each group they are in
// they leave (the last one out closes it; the last admin hands on), and
// every row of theirs goes (their copies, marks, reactions, read times,
// invites and waits). Their messages stay, shown as from "Deleted
// account". Groups are taken in id order.
func leaveAllGroupsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT group_id FROM group_members WHERE person_id = $1 AND left_at IS NULL ORDER BY group_id`, person)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		g, err := groupTx(ctx, tx, person, id.String(), true)
		if errors.Is(err, errGroupLeft) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := leaveGroupTx(ctx, tx, person, g); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE person_id = $1`, person); err != nil {
		return err
	}
	// Every invite to them (the ones they were never shown too), their
	// requests to join and their being kept as removed: a definer, as the
	// person may not see the unseen invites (A8).
	_, err = tx.Exec(ctx, `SELECT taskiem_group_forget()`)
	return err
}

package api

// Tasks for staff (docs/spec/chat-five-more-flows.md, flow 2 and the
// phase 3 plan, step 4). An owner or a manager gives a task to one
// person, or to a role ("any cashier": the first to take it has it), from
// the Tasks tool, from a card ("Give this to…"), or in chat ("@Tunde
// deliver Rivers Foods' order by 3pm", confirmed before it is made). The
// person is told in their chat (on WhatsApp outside the 24 hours, the
// staff task template); DONE closes it, with a photo when one was asked
// for; CAN'T with a word sends it back. The staff job nudges 30 minutes
// before a task is due, tells the giver when it is late, and makes each
// repeating task's task on the day it falls due.

import (
	"context"
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
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	// taskGive is who gives tasks and sees everyone's (an owner, an
	// admin, a shift manager).
	taskGive = PermTaskGive
	// taskPrefix starts a task's buttons: tsk:done:<id>, tsk:cant:<id>,
	// tsk:take:<id>, tsk:remind:<id>.
	taskPrefix = "tsk:"
	// taskNudge is how long before a task is due its person is reminded.
	taskNudge = 30 * time.Minute
	// taskPhotoWait is how long after DONE the photo it needs is waited for.
	taskPhotoWait = 30 * time.Minute
)

var errTaskPhoto = errors.New("this task needs a photo when it is done")

type taskLink struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// staffTask is a task as the Tasks tool and the person's page show it.
type staffTask struct {
	ID        uuid.UUID  `json:"id"`
	What      string     `json:"what"`
	GivenBy   uuid.UUID  `json:"given_by"`
	Giver     string     `json:"giver"`
	ToUser    *uuid.UUID `json:"to_user,omitempty"`
	ToRole    string     `json:"to_role,omitempty"`
	TakenBy   *uuid.UUID `json:"taken_by,omitempty"`
	Person    string     `json:"person,omitempty"` // the person's name, once there is one
	DueAt     *time.Time `json:"due_at,omitempty"`
	Photo     bool       `json:"photo"`
	State     string     `json:"state"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
	PhotoURL  string     `json:"photo_url,omitempty"`
	CantWhy   string     `json:"cant_why,omitempty"`
	Link      *taskLink  `json:"link,omitempty"`
	Repeat    *uuid.UUID `json:"repeat_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Late      bool       `json:"late"`
	photoPath string
	asked     *time.Time
}

// holder is who has the task now: the person, or who took the role's.
func (t staffTask) holder() *uuid.UUID {
	if t.TakenBy != nil {
		return t.TakenBy
	}
	return t.ToUser
}

// who is "Tunde", "any cashier", or "Tunde (any cashier)".
func (t staffTask) who() string {
	if t.ToRole == "" {
		return t.Person
	}
	r := "any " + strings.ReplaceAll(t.ToRole, "_", " ")
	if t.TakenBy != nil && t.Person != "" {
		return t.Person + " (" + r + ")"
	}
	return r
}

// line is "deliver Rivers Foods' order, by 15:00".
func (t staffTask) line(now time.Time) string {
	if t.DueAt == nil {
		return t.What
	}
	return t.What + ", " + dueWords(*t.DueAt, now)
}

// dueWords is "by 15:00", "by tomorrow 15:00" or "by Mon 10 Nov, 15:00".
func dueWords(due, now time.Time) string {
	d, n := due.In(storefront.Lagos), now.In(storefront.Lagos)
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	switch {
	case day(d) == day(n):
		return "by " + d.Format("15:04")
	case day(d) == day(n.AddDate(0, 0, 1)):
		return "by tomorrow " + d.Format("15:04")
	case day(d) == day(n.AddDate(0, 0, -1)):
		return "by yesterday " + d.Format("15:04")
	}
	return "by " + d.Format("Mon 2 Jan, 15:04")
}

const taskCols = `t.id, t.what, t.given_by, CASE WHEN g.name = '' THEN 'Someone' ELSE g.name END,
	t.to_user, COALESCE(t.to_role, ''), t.taken_by, COALESCE(CASE WHEN u.name = '' THEN 'Someone' ELSE u.name END, ''),
	t.due_at, t.photo, t.state, t.done_at, COALESCE(t.photo_path, ''), COALESCE(t.cant_why, ''),
	COALESCE(t.link_kind, ''), COALESCE(t.link_id, ''), COALESCE(t.link_name, ''), t.repeat_id, t.created_at, t.photo_asked
	FROM staff_tasks t JOIN users g ON g.id = t.given_by LEFT JOIN users u ON u.id = COALESCE(t.taken_by, t.to_user)`

func scanTask(now time.Time) func(pgx.CollectableRow) (staffTask, error) {
	return func(r pgx.CollectableRow) (staffTask, error) {
		var t staffTask
		var kind, id, name string
		err := r.Scan(&t.ID, &t.What, &t.GivenBy, &t.Giver, &t.ToUser, &t.ToRole, &t.TakenBy, &t.Person,
			&t.DueAt, &t.Photo, &t.State, &t.DoneAt, &t.photoPath, &t.CantWhy, &kind, &id, &name, &t.Repeat, &t.CreatedAt, &t.asked)
		if kind != "" {
			t.Link = &taskLink{Kind: kind, ID: id, Name: name}
		}
		t.Late = t.State == "open" && t.DueAt != nil && now.After(*t.DueAt)
		return t, err
	}
}

func taskTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) (staffTask, error) {
	rows, err := tx.Query(ctx, `SELECT `+taskCols+` WHERE t.id = $1 FOR UPDATE OF t`, id)
	if err != nil {
		return staffTask{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanTask(now))
}

// tasksForTx is the open tasks a person has or may take: their own, and
// those for a role they hold that no one has taken yet.
func tasksForTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, roles []string, now time.Time) ([]staffTask, error) {
	rows, err := tx.Query(ctx, `SELECT `+taskCols+`
		WHERE t.tenant_id = $1 AND t.state = 'open'
		  AND (COALESCE(t.taken_by, t.to_user) = $2 OR (t.taken_by IS NULL AND t.to_role = ANY ($3)))
		ORDER BY t.due_at NULLS LAST, t.created_at`, tenant, user, roles)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanTask(now))
}

// mayDo reports whether user (holding roles) may say DONE or CAN'T to t.
func (t staffTask) mayDo(user uuid.UUID, roles []string) bool {
	if h := t.holder(); h != nil {
		return *h == user
	}
	return slices.Contains(roles, t.ToRole)
}

// --- making one ---

type taskRepeatIn struct {
	Every string `json:"every"` // day or week
	Day   string `json:"day"`   // mon … sun, for week
	At    string `json:"at"`    // HH:MM
}

type staffTaskIn struct {
	What   string        `json:"what"`
	ToUser *uuid.UUID    `json:"to_user"`
	ToRole string        `json:"to_role"`
	DueAt  *time.Time    `json:"due_at"`
	Photo  bool          `json:"photo"`
	Link   *taskLink     `json:"link"`
	Repeat *taskRepeatIn `json:"repeat"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// check tidies the task and says what is wrong with it, in plain words.
func (in *staffTaskIn) check() error {
	in.What = strings.Join(strings.Fields(in.What), " ")
	if n := utf8.RuneCountInString(in.What); n == 0 || n > 200 {
		return fmt.Errorf("%w: say what is to be done (up to 200 letters)", errBadRequest)
	}
	in.ToRole = staffRole(in.ToRole)
	if (in.ToUser == nil) == (in.ToRole == "") {
		return fmt.Errorf("%w: choose who it is for: a person, or anyone in a role", errBadRequest)
	}
	if l := in.Link; l != nil {
		if !slices.Contains([]string{"order", "invoice", "bill", "purchase", "stock"}, l.Kind) || l.ID == "" || len(l.ID) > 100 || utf8.RuneCountInString(l.Name) > 120 {
			return fmt.Errorf("%w: that is not something a task can be about", errBadRequest)
		}
	}
	if r := in.Repeat; r != nil {
		if r.Every != "day" && r.Every != "week" {
			return fmt.Errorf("%w: a task repeats every day or every week", errBadRequest)
		}
		if r.Every == "week" && !slices.Contains(rotaDays, r.Day) {
			return fmt.Errorf("%w: choose the day of the week it repeats on", errBadRequest)
		}
		if r.Every == "day" {
			r.Day = ""
		}
		if !hhmm.MatchString(r.At) {
			return fmt.Errorf("%w: choose the time it is due each time, like 07:00", errBadRequest)
		}
		in.DueAt = nil
	}
	return nil
}

// checkWhoTx checks the person is in the business, or someone holds the role.
func checkWhoTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in *staffTaskIn) error {
	if in.ToUser != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, tenant, *in.ToUser).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: that person is not in the business", errBadRequest)
		}
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND role = $2)`, tenant, in.ToRole).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no one in the business is %s", errBadRequest, aRole(in.ToRole))
	}
	return nil
}

func aRole(role string) string {
	r := strings.ReplaceAll(role, "_", " ")
	if strings.ContainsRune("aeiou", rune(r[0])) {
		return "an " + r
	}
	return "a " + r
}

// repeatDue is when a repeat falls due on day (Lagos), if it does.
func repeatDue(every, weekday, at string, day time.Time) (time.Time, bool) {
	if every == "week" && rotaDayOf(day) != weekday {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(at[:2])
	m, _ := strconv.Atoi(at[3:])
	d := day.In(storefront.Lagos)
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, storefront.Lagos), true
}

// newTaskTx makes a task (or a repeat, and today's task when today is one
// of its days and its time has not passed); the task made, if any.
func newTaskTx(ctx context.Context, tx pgx.Tx, tenant, giver uuid.UUID, in staffTaskIn, now time.Time) (*staffTask, *uuid.UUID, error) {
	if err := checkWhoTx(ctx, tx, tenant, &in); err != nil {
		return nil, nil, err
	}
	var repeat *uuid.UUID
	due := in.DueAt
	if r := in.Repeat; r != nil {
		id := uuid.Must(uuid.NewV7())
		repeat = &id
		// Today counts as made either way: today's task now when its time
		// is still to come, else the first one on its next day.
		today := now.In(storefront.Lagos).Format("2006-01-02")
		made := &today
		start := false
		if at, ok := repeatDue(r.Every, r.Day, r.At, now); ok && at.After(now) {
			due, start = &at, true
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_task_repeats (id, tenant_id, what, given_by, to_user, to_role, photo, every, weekday, at, made_on)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, NULLIF($9, ''), $10, $11::date)`,
			id, tenant, in.What, giver, in.ToUser, in.ToRole, in.Photo, r.Every, r.Day, r.At, made); err != nil {
			return nil, nil, err
		}
		if !start {
			return nil, repeat, nil
		}
	}
	id := uuid.Must(uuid.NewV7())
	var kind, lid, lname *string
	if in.Link != nil {
		kind, lid = &in.Link.Kind, &in.Link.ID
		if in.Link.Name != "" {
			lname = &in.Link.Name
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staff_tasks (id, tenant_id, what, given_by, to_user, to_role, due_at, photo, link_kind, link_id, link_name, repeat_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12)`,
		id, tenant, in.What, giver, in.ToUser, in.ToRole, due, in.Photo, kind, lid, lname, repeat); err != nil {
		return nil, nil, err
	}
	// Given already due soon, or late: told now, so not nudged or told late as well.
	for _, k := range []struct {
		key  string
		when time.Duration
	}{{"task-nudge:", taskNudge}, {"task-late:", 0}} {
		if due != nil && !due.After(now.Add(k.when)) {
			if _, err := tx.Exec(ctx, `INSERT INTO staff_notices (tenant_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, k.key+id.String()); err != nil {
				return nil, nil, err
			}
		}
	}
	t, err := taskTx(ctx, tx, id, now)
	return &t, repeat, err
}

// --- telling people ---

// taskTold is one chat a task's message goes to.
type taskTold struct {
	channel, addr string
	user          uuid.UUID
}

// tellAbout tells users of a task: a notice in the app (link to the
// tasks), and a message on each chat they use, with buttons. For a
// person's new task outside the 24 hours on WhatsApp, the staff task
// template (Done and Can't); otherwise the business message template.
func (s *Server) tellAbout(ctx context.Context, tenant uuid.UUID, users []uuid.UUID, group, title, body string, buttons []whatsapp.Button, task *staffTask) {
	if len(users) == 0 {
		return
	}
	var to []taskTold
	var business string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		if err := notices.AddTx(ctx, tx, tenant, users, notices.Notice{Kind: notices.Setup, Title: title, Body: body, Link: "/tasks", Group: group}); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, users)
			if err != nil {
				return err
			}
			for _, u := range users {
				if a, ok := addrs[u]; ok && !slices.ContainsFunc(to, func(t taskTold) bool { return t.addr == a }) {
					to = append(to, taskTold{channel: channel, addr: a, user: u})
				}
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("tasks: telling", "tenant", tenant, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		m := linkPhoneMessage(tenant, ch.Prefix(""), title, body, buttons)
		if task != nil {
			tpl := whatsapp.TplStaffTask
			m.Template, m.Vars = &tpl, map[string]string{"tenant": business, "giver": task.Giver, "task": clip(task.line(s.staffNow()), 300)}
			m.Payloads = []string{taskPrefix + "done:" + task.ID.String(), taskPrefix + "cant:" + task.ID.String()}
		}
		_, err = ch.Send(ctx, t.addr, m)
		if task != nil && errors.Is(err, whatsapp.ErrTemplateNotApproved) {
			// The staff task template is not approved yet on this number:
			// the business message template, which is, without the buttons'
			// payloads (DONE and CAN'T still work as words).
			_, err = ch.Send(ctx, t.addr, linkPhoneMessage(tenant, ch.Prefix(""), title, body, buttons))
		}
		if err != nil {
			s.Logger.Warn("tasks: a chat message", "tenant", tenant, "err", err)
		}
	}
}

func doButtons(t staffTask) []whatsapp.Button {
	id := t.ID.String()
	return []whatsapp.Button{{ID: taskPrefix + "done:" + id, Title: "Done"}, {ID: taskPrefix + "cant:" + id, Title: "Can't"}}
}

// tellNewTask tells the person (or everyone in the role) of a task.
func (s *Server) tellNewTask(ctx context.Context, tenant uuid.UUID, t staffTask, again bool) {
	now := s.staffNow()
	how := "Reply DONE when it is done"
	if t.Photo {
		how = "Reply DONE and send a photo when it is done"
	}
	ask := t.Giver + " asked you"
	if again {
		ask = t.Giver + " reminds you"
	}
	if h := t.holder(); h != nil {
		s.tellAbout(ctx, tenant, []uuid.UUID{*h}, "task:"+t.ID.String(), ask+": "+t.line(now), how+", or CAN'T and why.", doButtons(t), &t)
		return
	}
	s.tellAbout(ctx, tenant, s.roleUsers(ctx, tenant, t.ToRole, t.GivenBy), "task:"+t.ID.String(), t.Giver+" asked "+t.who()+": "+t.line(now),
		"The first to take it has it.", takeButton(t), nil)
}

func takeButton(t staffTask) []whatsapp.Button {
	return []whatsapp.Button{{ID: taskPrefix + "take:" + t.ID.String(), Title: "I'll do it"}}
}

// roleUsers is everyone in the business with role, but except.
func (s *Server) roleUsers(ctx context.Context, tenant uuid.UUID, role string, except uuid.UUID) []uuid.UUID {
	var users []uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE tenant_id = $1 AND role = $2 AND user_id <> $3`, tenant, role, except)
		if err != nil {
			return err
		}
		users, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		s.Logger.Warn("tasks: the role's people", "err", err)
	}
	return users
}

// tellGiver tells whoever gave the task what became of it.
func (s *Server) tellGiver(ctx context.Context, tenant uuid.UUID, t staffTask, by uuid.UUID, title, body string, buttons []whatsapp.Button) {
	if t.GivenBy == by {
		return
	}
	s.tellAbout(ctx, tenant, []uuid.UUID{t.GivenBy}, "task:"+t.ID.String(), title, body, buttons, nil)
}

// --- doing one ---

// takeTaskTx gives a role's task to user, the first to take it.
func takeTaskTx(ctx context.Context, tx pgx.Tx, t *staffTask, user uuid.UUID, roles []string) error {
	if t.State != "open" {
		return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
	}
	if h := t.holder(); h != nil {
		if *h == user {
			return nil
		}
		return fmt.Errorf("%w: %s has it already", errConflict, t.Person)
	}
	if !slices.Contains(roles, t.ToRole) {
		return fmt.Errorf("%w: that task is for %s", errForbidden, t.who())
	}
	if _, err := tx.Exec(ctx, `UPDATE staff_tasks SET taken_by = $2, updated_at = now() WHERE id = $1`, t.ID, user); err != nil {
		return err
	}
	t.TakenBy = &user
	return tx.QueryRow(ctx, `SELECT CASE WHEN name = '' THEN 'Someone' ELSE name END FROM users WHERE id = $1`, user).Scan(&t.Person)
}

func taskStateWords(state string) string {
	switch state {
	case "done":
		return "done"
	case "cant":
		return "sent back"
	case "cancelled":
		return "called off"
	}
	return "open"
}

// doneTaskTx closes a task, with the photo's path when it needs one.
func doneTaskTx(ctx context.Context, tx pgx.Tx, t *staffTask, user uuid.UUID, roles []string, photo string) error {
	if !t.mayDo(user, roles) {
		return fmt.Errorf("%w: that task is for %s", errForbidden, t.who())
	}
	if t.State != "open" {
		return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
	}
	if t.Photo && photo == "" {
		return errTaskPhoto
	}
	if t.holder() == nil {
		if err := takeTaskTx(ctx, tx, t, user, roles); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE staff_tasks SET state = 'done', done_at = now(), done_by = $2, photo_path = NULLIF($3, ''), photo_asked = NULL, updated_at = now() WHERE id = $1`, t.ID, user, photo)
	t.State = "done"
	return err
}

// cantTaskTx sends a task back to whoever gave it, with why.
func cantTaskTx(ctx context.Context, tx pgx.Tx, t *staffTask, user uuid.UUID, roles []string, why string) error {
	if !t.mayDo(user, roles) {
		return fmt.Errorf("%w: that task is for %s", errForbidden, t.who())
	}
	if t.State != "open" {
		return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
	}
	if t.holder() == nil {
		if err := takeTaskTx(ctx, tx, t, user, roles); err != nil {
			return err
		}
	}
	why = clip(strings.Join(strings.Fields(why), " "), 300)
	_, err := tx.Exec(ctx, `UPDATE staff_tasks SET state = 'cant', cant_why = NULLIF($2, ''), photo_asked = NULL, updated_at = now() WHERE id = $1`, t.ID, why)
	t.State, t.CantWhy = "cant", why
	return err
}

func (s *Server) afterDone(ctx context.Context, tenant uuid.UUID, t staffTask, by uuid.UUID) {
	body := "Done " + doneWhen(s.staffNow(), t) + "."
	if t.Photo {
		body = "Done, with a photo. Open the tasks to see it."
	}
	s.tellGiver(ctx, tenant, t, by, t.Person+" did: "+t.What, body, nil)
}

func doneWhen(now time.Time, t staffTask) string {
	w := "at " + now.In(storefront.Lagos).Format("15:04")
	if t.DueAt != nil && now.After(*t.DueAt) {
		w += " (" + lateWords(now.Sub(*t.DueAt)) + " late)"
	}
	return w
}

func lateWords(d time.Duration) string {
	if d < time.Hour {
		return plural(int(d.Minutes()), "minute")
	}
	return hoursWords(d)
}

func (s *Server) afterCant(ctx context.Context, tenant uuid.UUID, t staffTask, by uuid.UUID) {
	body := "No reason given."
	if t.CantWhy != "" {
		body = "\"" + t.CantWhy + "\""
	}
	s.tellGiver(ctx, tenant, t, by, t.Person+" can't: "+t.What, body+" Give it to someone else in Tasks.", nil)
}

// --- the web ---

type taskRepeat struct {
	ID     uuid.UUID  `json:"id"`
	What   string     `json:"what"`
	ToUser *uuid.UUID `json:"to_user,omitempty"`
	ToRole string     `json:"to_role,omitempty"`
	Person string     `json:"person,omitempty"`
	Photo  bool       `json:"photo"`
	Every  string     `json:"every"`
	Day    string     `json:"day,omitempty"`
	At     string     `json:"at"`
}

// listTasks is GET /v1/staff/tasks: the person's own (and their roles'),
// or, for those who give tasks with ?all=true, everyone's open ones and
// the last week's closed ones, and the repeats.
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	now := s.staffNow()
	all := r.URL.Query().Get("all") == "true"
	if all && !p.Can(taskGive) {
		s.fail(w, r, fmt.Errorf("%w: only those who run the business see everyone's tasks", errForbidden))
		return
	}
	type person struct {
		ID    uuid.UUID `json:"id"`
		Name  string    `json:"name"`
		Roles []string  `json:"roles"`
	}
	out := struct {
		Tasks   []staffTask  `json:"tasks"`
		Repeats []taskRepeat `json:"repeats"`
		Give    bool         `json:"give"`
		// People are who a task can be given to, for those who give them.
		People []person `json:"people"`
	}{Tasks: []staffTask{}, Repeats: []taskRepeat{}, Give: p.Can(taskGive), People: []person{}}
	if err := s.tx(r, func(tx pgx.Tx) error {
		q := `SELECT ` + taskCols + ` WHERE t.tenant_id = $1 AND (t.state = 'open' OR t.updated_at > now() - interval '7 days')`
		args := []any{p.TenantID}
		if !all {
			q += ` AND (COALESCE(t.taken_by, t.to_user) = $2 OR (t.taken_by IS NULL AND t.to_role = ANY ($3)))`
			args = append(args, p.UserID, p.Roles)
		}
		rows, err := tx.Query(r.Context(), q+` ORDER BY t.state <> 'open', t.due_at NULLS LAST, t.created_at DESC LIMIT 200`, args...)
		if err != nil {
			return err
		}
		if out.Tasks, err = pgx.CollectRows(rows, scanTask(now)); err != nil {
			return err
		}
		if out.Give {
			rows, err := tx.Query(r.Context(), `SELECT u.id, CASE WHEN u.name = '' THEN 'Someone' ELSE u.name END, array_agg(DISTINCT m.role ORDER BY m.role)
				FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.tenant_id = $1 GROUP BY u.id, u.name ORDER BY 2`, p.TenantID)
			if err != nil {
				return err
			}
			if out.People, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (person, error) {
				var x person
				return x, row.Scan(&x.ID, &x.Name, &x.Roles)
			}); err != nil {
				return err
			}
		}
		if !all {
			return nil
		}
		rows, err = tx.Query(r.Context(), `SELECT x.id, x.what, x.to_user, COALESCE(x.to_role, ''), COALESCE(CASE WHEN u.name = '' THEN 'Someone' ELSE u.name END, ''),
			x.photo, x.every, COALESCE(x.weekday, ''), x.at
			FROM staff_task_repeats x LEFT JOIN users u ON u.id = x.to_user
			WHERE x.tenant_id = $1 AND x.stopped_at IS NULL ORDER BY x.created_at`, p.TenantID)
		if err != nil {
			return err
		}
		out.Repeats, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (taskRepeat, error) {
			var x taskRepeat
			return x, row.Scan(&x.ID, &x.What, &x.ToUser, &x.ToRole, &x.Person, &x.Photo, &x.Every, &x.Day, &x.At)
		})
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range out.Tasks {
		s.taskPhotoURL(r.Context(), p.TenantID, &out.Tasks[i])
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) taskPhotoURL(ctx context.Context, tenant uuid.UUID, t *staffTask) {
	if t.photoPath == "" || s.Media == nil {
		return
	}
	if u, _, err := s.Media.SignURL(ctx, tenant, t.photoPath, 10*time.Minute); err == nil {
		t.PhotoURL = u
	}
}

// giveTask is POST /v1/staff/tasks.
func (s *Server) giveTask(w http.ResponseWriter, r *http.Request) {
	var in staffTaskIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var t *staffTask
	var repeat *uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, repeat, err = newTaskTx(r.Context(), tx, p.TenantID, p.UserID, in, s.staffNow()); err != nil {
			return err
		}
		return auditTx(r, tx, "staff.task_give", p.TenantID.String(), map[string]any{"repeats": repeat != nil})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if t != nil {
		task := *t
		s.background(r.Context(), "tasks", func(ctx context.Context) { s.tellNewTask(ctx, p.TenantID, task, false) })
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": t, "repeat_id": repeat})
}

// taskAct runs fn on the task named in the path, then after, outside the
// transaction, and answers with the task.
func (s *Server) taskAct(w http.ResponseWriter, r *http.Request, fn func(tx pgx.Tx, t *staffTask) error, after func(t staffTask)) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var t staffTask
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, err = taskTx(r.Context(), tx, id, s.staffNow()); err != nil {
			return err
		}
		if err := fn(tx, &t); err != nil {
			return err
		}
		return auditTx(r, tx, "staff.task_"+t.State, t.ID.String(), nil)
	}); err != nil {
		if errors.Is(err, errTaskPhoto) {
			err = fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		s.fail(w, r, err)
		return
	}
	if after != nil {
		after(t)
	}
	s.taskPhotoURL(r.Context(), p.TenantID, &t)
	writeJSON(w, http.StatusOK, t)
}

// takeTask is POST /v1/staff/tasks/{id}/take.
func (s *Server) takeTask(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error { return takeTaskTx(r.Context(), tx, t, p.UserID, p.Roles) }, nil)
}

// doneTask is POST /v1/staff/tasks/{id}/done: JSON, or a form with the
// photo as "photo" for a task that needs one.
func (s *Server) doneTask(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var data []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, media.MaxImageBytes+1<<16)
		f, _, err := r.FormFile("photo")
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: add the photo (up to %d MB)", errBadRequest, media.MaxImageBytes>>20))
			return
		}
		data, err = io.ReadAll(io.LimitReader(f, media.MaxImageBytes+1))
		f.Close()
		if err != nil || len(data) > media.MaxImageBytes {
			s.fail(w, r, fmt.Errorf("%w: the photo is over %d MB", errBadRequest, media.MaxImageBytes>>20))
			return
		}
	}
	path := ""
	if len(data) > 0 {
		if ct := http.DetectContentType(data); !slices.Contains([]string{"image/jpeg", "image/png", "image/webp", "image/gif"}, ct) {
			s.fail(w, r, fmt.Errorf("%w: that is not a photo (a JPEG, PNG or WebP)", errBadRequest))
			return
		}
		// The task is checked before the photo is kept, so a refusal keeps nothing.
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
		if err := s.tx(r, func(tx pgx.Tx) error {
			t, err := taskTx(r.Context(), tx, id, s.staffNow())
			if err != nil {
				return err
			}
			if !t.mayDo(p.UserID, p.Roles) {
				return fmt.Errorf("%w: that task is for %s", errForbidden, t.who())
			}
			if t.State != "open" {
				return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
			}
			return nil
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if path, err = s.storeProof(r.Context(), p.TenantID, data); err != nil {
			s.Logger.Warn("tasks: keeping a photo", "err", err)
			s.fail(w, r, fmt.Errorf("%w: the photo could not be kept; try again", errConflict))
			return
		}
	}
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error { return doneTaskTx(r.Context(), tx, t, p.UserID, p.Roles, path) },
		func(t staffTask) {
			s.background(r.Context(), "tasks", func(ctx context.Context) { s.afterDone(ctx, p.TenantID, t, p.UserID) })
		})
}

// cantTask is POST /v1/staff/tasks/{id}/cant {why}.
func (s *Server) cantTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Why string `json:"why"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error { return cantTaskTx(r.Context(), tx, t, p.UserID, p.Roles, in.Why) },
		func(t staffTask) {
			s.background(r.Context(), "tasks", func(ctx context.Context) { s.afterCant(ctx, p.TenantID, t, p.UserID) })
		})
}

// remindTask is POST /v1/staff/tasks/{id}/remind: the person is told again.
func (s *Server) remindTask(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !s.limiter("task-remind:"+chi.URLParam(r, "id"), 10*time.Minute, 1).Allow() {
		s.fail(w, r, fmt.Errorf("%w: they were reminded a moment ago", errConflict))
		return
	}
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error {
		if t.State != "open" {
			return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
		}
		return nil
	}, func(t staffTask) {
		s.background(r.Context(), "tasks", func(ctx context.Context) { s.tellNewTask(ctx, p.TenantID, t, true) })
	})
}

// passTask is POST /v1/staff/tasks/{id}/give {to_user | to_role}: given
// to someone else, open again if it was sent back.
func (s *Server) passTask(w http.ResponseWriter, r *http.Request) {
	var in staffTaskIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.ToRole = staffRole(in.ToRole)
	if (in.ToUser == nil) == (in.ToRole == "") {
		s.fail(w, r, fmt.Errorf("%w: choose who it is for: a person, or anyone in a role", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	var had *uuid.UUID
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error {
		had = t.holder()
		if t.State == "done" || t.State == "cancelled" {
			return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
		}
		if err := checkWhoTx(r.Context(), tx, p.TenantID, &in); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE staff_tasks SET to_user = $2, to_role = NULLIF($3, ''), taken_by = NULL, state = 'open', cant_why = NULL,
			photo_asked = NULL, updated_at = now() WHERE id = $1`, t.ID, in.ToUser, in.ToRole); err != nil {
			return err
		}
		n, err := taskTx(r.Context(), tx, t.ID, s.staffNow())
		*t = n
		return err
	}, func(t staffTask) {
		s.background(r.Context(), "tasks", func(ctx context.Context) {
			if had != nil && *had != p.UserID && (t.holder() == nil || *t.holder() != *had) {
				s.tellAbout(ctx, p.TenantID, []uuid.UUID{*had}, "task:"+t.ID.String(), "Given to someone else: "+t.What, "You need not do it now.", nil, nil)
			}
			s.tellNewTask(ctx, p.TenantID, t, false)
		})
	})
}

// cancelTask is DELETE /v1/staff/tasks/{id}: called off, and the person
// who had it told.
func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	s.taskAct(w, r, func(tx pgx.Tx, t *staffTask) error {
		if t.State != "open" && t.State != "cant" {
			return fmt.Errorf("%w: that task is %s", errConflict, taskStateWords(t.State))
		}
		_, err := tx.Exec(r.Context(), `UPDATE staff_tasks SET state = 'cancelled', photo_asked = NULL, updated_at = now() WHERE id = $1`, t.ID)
		t.State = "cancelled"
		return err
	}, func(t staffTask) {
		h := t.holder()
		if h == nil || *h == p.UserID {
			return
		}
		s.background(r.Context(), "tasks", func(ctx context.Context) {
			s.tellAbout(ctx, p.TenantID, []uuid.UUID{*h}, "task:"+t.ID.String(), "Called off: "+t.What, "Nothing more to do.", nil, nil)
		})
	})
}

// stopRepeat is DELETE /v1/staff/task-repeats/{id}: no more tasks from it.
func (s *Server) stopRepeat(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE staff_task_repeats SET stopped_at = now() WHERE id = $1 AND stopped_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "staff.task_repeat_stop", id.String(), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// returnTasksTx gives a removed person's open tasks back to whoever gave
// them; the givers and the tasks, to tell.
func returnTasksTx(ctx context.Context, tx pgx.Tx, tenant, user, by uuid.UUID) ([]staffTask, error) {
	rows, err := tx.Query(ctx, `UPDATE staff_tasks SET to_user = given_by, to_role = NULL, taken_by = NULL, updated_at = now()
		WHERE tenant_id = $1 AND state IN ('open', 'cant') AND COALESCE(taken_by, to_user) = $2 AND given_by <> $2 RETURNING id`, tenant, user)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE staff_tasks SET state = 'cancelled', updated_at = now()
		WHERE tenant_id = $1 AND state IN ('open', 'cant') AND COALESCE(taken_by, to_user) = $2`, tenant, user); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE staff_task_repeats SET stopped_at = now() WHERE tenant_id = $1 AND to_user = $2 AND stopped_at IS NULL`, tenant, user); err != nil {
		return nil, err
	}
	// Tasks and repeats they gave are now those of whoever removed them,
	// who hears when one is late; the person who left hears nothing more.
	if _, err := tx.Exec(ctx, `UPDATE staff_tasks SET given_by = $3, updated_at = now() WHERE tenant_id = $1 AND state IN ('open', 'cant') AND given_by = $2`, tenant, user, by); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE staff_task_repeats SET given_by = $3 WHERE tenant_id = $1 AND given_by = $2 AND stopped_at IS NULL`, tenant, user, by); err != nil {
		return nil, err
	}
	var out []staffTask
	for _, id := range ids {
		t, err := taskTx(ctx, tx, id, time.Now())
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// --- the staff job ---

// staffTaskTick makes repeats' tasks, nudges, and tells givers of late
// tasks, for one business.
func (s *Server) staffTaskTick(ctx context.Context, tenant uuid.UUID, now time.Time) error {
	var made, nudge, late []staffTask
	day := now.In(storefront.Lagos)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// Repeats falling due today: today's task, from the day's start.
		rows, err := tx.Query(ctx, `SELECT id, what, given_by, to_user, COALESCE(to_role, ''), photo, every, COALESCE(weekday, ''), at
			FROM staff_task_repeats WHERE tenant_id = $1 AND stopped_at IS NULL AND (made_on IS NULL OR made_on < $2::date)`, tenant, day.Format("2006-01-02"))
		if err != nil {
			return err
		}
		type rep struct {
			id, giver       uuid.UUID
			to              *uuid.UUID
			what, role      string
			photo           bool
			every, wday, at string
		}
		reps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rep, error) {
			var x rep
			return x, r.Scan(&x.id, &x.what, &x.giver, &x.to, &x.role, &x.photo, &x.every, &x.wday, &x.at)
		})
		if err != nil {
			return err
		}
		for _, x := range reps {
			due, ok := repeatDue(x.every, x.wday, x.at, day)
			if !ok {
				continue
			}
			id := uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO staff_tasks (id, tenant_id, what, given_by, to_user, to_role, due_at, photo, repeat_id)
				VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9) ON CONFLICT DO NOTHING`,
				id, tenant, x.what, x.giver, x.to, x.role, due, x.photo, x.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE staff_task_repeats SET made_on = $2::date WHERE id = $1`, x.id, day.Format("2006-01-02")); err != nil {
				return err
			}
			t, err := taskTx(ctx, tx, id, now)
			if err == nil {
				made = append(made, t)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		// A task about an order closes when the order is ready or completed;
		// one about an invoice when it is paid; either is called off when
		// the thing is (the order declined or cancelled, the invoice void).
		if _, err := tx.Exec(ctx, `UPDATE staff_tasks t SET
			state = CASE WHEN o.status = 'completed' OR x.ready_at IS NOT NULL THEN 'done' ELSE 'cancelled' END,
			done_at = CASE WHEN o.status = 'completed' OR x.ready_at IS NOT NULL THEN $2::timestamptz END,
			photo_asked = NULL, updated_at = now()
			FROM storefront_orders o LEFT JOIN link_orders x ON x.order_id = o.id
			WHERE t.tenant_id = $1 AND t.state = 'open' AND t.link_kind = 'order' AND t.link_id = o.id::text AND o.tenant_id = $1
			  AND (o.status IN ('completed', 'declined', 'expired', 'cancelled', 'refunded') OR x.ready_at IS NOT NULL)`, tenant, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_tasks t SET
			state = CASE WHEN i.status = 'paid' THEN 'done' ELSE 'cancelled' END,
			done_at = CASE WHEN i.status = 'paid' THEN $2::timestamptz END,
			photo_asked = NULL, updated_at = now()
			FROM customer_invoices i
			WHERE t.tenant_id = $1 AND t.state = 'open' AND t.link_kind = 'invoice' AND t.link_id = i.id::text AND i.tenant_id = $1
			  AND i.status IN ('paid', 'void')`, tenant, now); err != nil {
			return err
		}
		// Due within 30 minutes: a nudge; past due: the giver is told. Each once.
		rows, err = tx.Query(ctx, `SELECT `+taskCols+` WHERE t.tenant_id = $1 AND t.state = 'open' AND t.due_at IS NOT NULL AND t.due_at <= $2`, tenant, now.Add(taskNudge))
		if err != nil {
			return err
		}
		open, err := pgx.CollectRows(rows, scanTask(now))
		if err != nil {
			return err
		}
		for _, t := range open {
			key, list := "task-nudge:"+t.ID.String(), &nudge
			if t.Late {
				key, list = "task-late:"+t.ID.String(), &late
			}
			tag, err := tx.Exec(ctx, `INSERT INTO staff_notices (tenant_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, key)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				*list = append(*list, t)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, t := range made {
		s.tellNewTask(ctx, tenant, t, false)
	}
	for _, t := range nudge {
		if h := t.holder(); h != nil {
			s.tellAbout(ctx, tenant, []uuid.UUID{*h}, "task:"+t.ID.String(), "Soon: "+t.line(now), "Reply DONE when it is done.", doButtons(t), nil)
			continue
		}
		s.tellAbout(ctx, tenant, s.roleUsers(ctx, tenant, t.ToRole, t.GivenBy), "task:"+t.ID.String(), "Soon, for "+t.who()+": "+t.line(now),
			"No one has taken it yet.", takeButton(t), nil)
	}
	for _, t := range late {
		who := t.who()
		if who == "" {
			who = "no one yet"
		}
		s.tellAbout(ctx, tenant, []uuid.UUID{t.GivenBy}, "task:"+t.ID.String(),
			fmt.Sprintf("%s is %s late (%s)", taskTitle(t.What), lateWords(now.Sub(*t.DueAt)), who), "Remind them, or give it to someone else in Tasks.",
			[]whatsapp.Button{{ID: taskPrefix + "remind:" + t.ID.String(), Title: "Remind"}}, nil)
	}
	return nil
}

// taskTitle is the task's words with a capital first letter.
func taskTitle(what string) string {
	if what == "" {
		return what
	}
	r, n := utf8.DecodeRuneInString(what)
	return strings.ToUpper(string(r)) + what[n:]
}

// taskCounts is the briefing's tasks: still due today, and late (each
// counted once).
type taskCounts struct {
	Today int `json:"today"`
	Late  int `json:"late"`
}

func taskCountsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) (taskCounts, error) {
	d := now.In(storefront.Lagos)
	tomorrow := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, storefront.Lagos).AddDate(0, 0, 1)
	var c taskCounts
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE due_at >= $2 AND due_at < $3), count(*) FILTER (WHERE due_at < $2)
		FROM staff_tasks WHERE tenant_id = $1 AND state = 'open' AND due_at IS NOT NULL`, tenant, now, tomorrow).Scan(&c.Today, &c.Late)
	return c, err
}

// --- chat ---

// chatTask is a task said in chat, waiting for the giver's yes.
type chatTask struct {
	In   staffTaskIn `json:"in"`
	Line string      `json:"line"`
}

var (
	taskDoneWords = []string{"done", "it's done", "its done", "it is done", "finished", "task done"}
	taskListWords = []string{"tasks", "my tasks", "task", "my task", "what are my tasks", "to do", "todo"}
	taskCantRe    = regexp.MustCompile(`(?i)^(?:can'?t|cannot|can not|can’t)(?:\s+do(?:\s+it)?)?\b[\s:,.-]*(.*)$`)
	taskNumRe     = regexp.MustCompile(`^(\d{1,2})\b[\s:,.-]*(.*)$`)
)

// waTaskPress takes a task's buttons.
func (s *Server) waTaskPress(ctx context.Context, c *chat, text string) error {
	verb, rest, _ := strings.Cut(strings.TrimPrefix(text, taskPrefix), ":")
	id, err := uuid.Parse(rest)
	if err != nil || c.p == nil || c.tenant.ID == uuid.Nil {
		return c.say(ctx, s, "That task is no longer here.")
	}
	if err := s.taskHome(ctx, c, id); err != nil {
		return err
	}
	switch verb {
	case "done":
		return s.waTaskDone(ctx, c, &id)
	case "cant":
		return s.waTaskCant(ctx, c, &id, "")
	case "take":
		var t staffTask
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if t, err = taskTx(ctx, tx, id, s.staffNow()); err != nil {
				return err
			}
			return takeTaskTx(ctx, tx, &t, c.p.UserID, c.p.Roles)
		})
		if msg, ok := taskRefusal(err); ok {
			return c.say(ctx, s, msg)
		}
		if err != nil {
			return err
		}
		how := "Reply DONE when it is done."
		if t.Photo {
			how = "Reply DONE and send a photo when it is done."
		}
		return c.say(ctx, s, "It is yours: "+t.line(s.staffNow())+". "+how, doButtons(t)...)
	case "remind":
		if !c.p.Can(taskGive) {
			return c.say(ctx, s, "Only those who run the business remind people of tasks.")
		}
		var t staffTask
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			t, err = taskTx(ctx, tx, id, s.staffNow())
			return err
		}); errors.Is(err, pgx.ErrNoRows) {
			return c.say(ctx, s, "That task is no longer here.")
		} else if err != nil {
			return err
		}
		if t.State != "open" {
			return c.say(ctx, s, "That task is "+taskStateWords(t.State)+".")
		}
		if !s.limiter("task-remind:"+t.ID.String(), 10*time.Minute, 1).Allow() {
			return c.say(ctx, s, "They were reminded a moment ago.")
		}
		s.tellNewTask(ctx, c.tenant.ID, t, true)
		return c.say(ctx, s, "Reminded "+t.who()+".")
	}
	return c.say(ctx, s, "That task is no longer here.")
}

// taskHome turns the chat to the business a task is in, for a button on a
// task from another of the person's businesses (the chat stays on its
// business for what comes after).
func (s *Server) taskHome(ctx context.Context, c *chat, id uuid.UUID) error {
	in := func(tenant uuid.UUID) (bool, error) {
		var ok bool
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM staff_tasks WHERE id = $1 AND tenant_id = $2)`, id, tenant).Scan(&ok)
		})
		return ok, err
	}
	if ok, err := in(c.tenant.ID); ok || err != nil {
		return err
	}
	for _, t := range c.tenants {
		if t.ID == c.tenant.ID {
			continue
		}
		ok, err := in(t.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		p, err := s.principalOf(ctx, t.ID, c.p.UserID)
		if err != nil {
			return err
		}
		c.tenant, c.p = t, p
		return nil
	}
	return nil
}

// taskRefusal is a plain answer for a refusal, true when err is one.
func taskRefusal(err error) (string, bool) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "That task is no longer here.", true
	case errors.Is(err, errConflict), errors.Is(err, errForbidden), errors.Is(err, errBadRequest):
		msg := err.Error()
		if _, after, ok := strings.Cut(msg, ": "); ok {
			msg = after
		}
		return taskTitle(msg) + ".", true
	}
	return "", false
}

// waTaskWords answers DONE, CAN'T, "my tasks", and "@Tunde do this by
// 3pm" from those who give tasks; false for other words.
func (s *Server) waTaskWords(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if c.p == nil || c.tenant.ID == uuid.Nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	cmd = strings.TrimRight(strings.TrimSpace(cmd), ".!")
	switch {
	case slices.Contains(taskListWords, cmd):
		return true, s.waMyTasks(ctx, c)
	case slices.Contains(taskDoneWords, cmd):
		return true, s.waTaskDone(ctx, c, nil)
	case strings.HasPrefix(cmd, "done "):
		if m := taskNumRe.FindStringSubmatch(strings.TrimPrefix(cmd, "done ")); m != nil && m[2] == "" {
			n, _ := strconv.Atoi(m[1])
			return true, s.waTaskDoneN(ctx, c, n)
		}
	}
	if m := taskCantRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil && strings.HasPrefix(cmd, "can") {
		return true, s.waTaskCant(ctx, c, nil, m[1])
	}
	if strings.HasPrefix(strings.TrimSpace(text), "@") && c.p.Can(taskGive) {
		return s.waTaskSaid(ctx, c, text)
	}
	return false, nil
}

func (s *Server) myOpenTasks(ctx context.Context, c *chat) ([]staffTask, error) {
	var list []staffTask
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = tasksForTx(ctx, tx, c.tenant.ID, c.p.UserID, c.p.Roles, s.staffNow())
		return err
	})
	return list, err
}

func (s *Server) waMyTasks(ctx context.Context, c *chat) error {
	list, err := s.myOpenTasks(ctx, c)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, "You have no tasks open.")
	}
	now := s.staffNow()
	lines := []string{"Your tasks:"}
	for i, t := range list {
		l := fmt.Sprintf("%d. %s", i+1, t.line(now))
		if t.holder() == nil {
			l += " (" + t.who() + ")"
		}
		if t.Late {
			l += " (late)"
		}
		lines = append(lines, l)
	}
	lines = append(lines, "Reply DONE 1 when the first is done, or CAN'T 1 and why.")
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waTaskDone is DONE: for the task named (a button), or the one task the
// person has; with several, which.
func (s *Server) waTaskDone(ctx context.Context, c *chat, id *uuid.UUID) error {
	if id == nil {
		list, err := s.myOpenTasks(ctx, c)
		if err != nil {
			return err
		}
		switch len(list) {
		case 0:
			return c.say(ctx, s, "You have no tasks open.")
		case 1:
			id = &list[0].ID
		default:
			return s.waMyTasks(ctx, c)
		}
	}
	var t staffTask
	asked := false
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if t, err = taskTx(ctx, tx, *id, s.staffNow()); err != nil {
			return err
		}
		err = doneTaskTx(ctx, tx, &t, c.p.UserID, c.p.Roles, "")
		if !errors.Is(err, errTaskPhoto) {
			return err
		}
		// Waiting for the photo; a role's task is theirs meanwhile.
		asked = true
		if t.holder() == nil {
			if err := takeTaskTx(ctx, tx, &t, c.p.UserID, c.p.Roles); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE staff_tasks SET photo_asked = $2 WHERE id = $1`, t.ID, s.staffNow())
		return err
	})
	if err == nil && asked {
		return c.say(ctx, s, "Send a photo of it to finish: "+t.What+".")
	}
	if msg, ok := taskRefusal(err); ok {
		return c.say(ctx, s, msg)
	}
	if err != nil {
		return err
	}
	s.afterDone(ctx, c.tenant.ID, t, c.p.UserID)
	return c.say(ctx, s, "Done: "+t.What+". Thank you.")
}

func (s *Server) waTaskDoneN(ctx context.Context, c *chat, n int) error {
	list, err := s.myOpenTasks(ctx, c)
	if err != nil {
		return err
	}
	if n < 1 || n > len(list) {
		return s.waMyTasks(ctx, c)
	}
	return s.waTaskDone(ctx, c, &list[n-1].ID)
}

// waTaskCant is CAN'T (and why): for the task named, the numbered one, or
// the one task the person has.
func (s *Server) waTaskCant(ctx context.Context, c *chat, id *uuid.UUID, why string) error {
	if id == nil {
		list, err := s.myOpenTasks(ctx, c)
		if err != nil {
			return err
		}
		if m := taskNumRe.FindStringSubmatch(why); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n >= 1 && n <= len(list) {
				id, why = &list[n-1].ID, m[2]
			}
		}
		if id == nil {
			switch len(list) {
			case 0:
				return c.say(ctx, s, "You have no tasks open.")
			case 1:
				id = &list[0].ID
			default:
				return s.waMyTasks(ctx, c)
			}
		}
	}
	var t staffTask
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if t, err = taskTx(ctx, tx, *id, s.staffNow()); err != nil {
			return err
		}
		return cantTaskTx(ctx, tx, &t, c.p.UserID, c.p.Roles, why)
	})
	if msg, ok := taskRefusal(err); ok {
		return c.say(ctx, s, msg)
	}
	if err != nil {
		return err
	}
	s.afterCant(ctx, c.tenant.ID, t, c.p.UserID)
	if why == "" {
		return c.say(ctx, s, "Sent back to "+t.Giver+": "+t.What+". Say why next time, so they know.")
	}
	return c.say(ctx, s, "Sent back to "+t.Giver+": "+t.What+".")
}

// taskPhoto takes a photo sent soon after DONE on a task that needs one;
// false when no task is waiting for a photo.
func (s *Server) taskPhoto(ctx context.Context, c *chat) (bool, error) {
	if c.in.Image == nil || c.p == nil || c.tenant.ID == uuid.Nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	var id *uuid.UUID
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM staff_tasks WHERE tenant_id = $1 AND state = 'open' AND photo_asked > $3
			AND (COALESCE(taken_by, to_user) = $2 OR (taken_by IS NULL AND to_role = ANY ($4))) ORDER BY photo_asked DESC LIMIT 1`,
			c.tenant.ID, c.p.UserID, s.staffNow().Add(-taskPhotoWait), c.p.Roles).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}); err != nil || id == nil {
		return false, err
	}
	data, _, _, err := s.fetchMedia(ctx, c, c.in.Image, media.MaxImageBytes)
	if err != nil {
		s.Logger.Warn("tasks: fetching a photo", "err", err)
		return true, c.say(ctx, s, "The photo did not come through. Send it again.")
	}
	path, err := s.storeProof(ctx, c.tenant.ID, data)
	clear(data)
	if err != nil {
		s.Logger.Warn("tasks: keeping a photo", "err", err)
		return true, c.say(ctx, s, "The photo could not be kept. Send it again in a moment.")
	}
	var t staffTask
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if t, err = taskTx(ctx, tx, *id, s.staffNow()); err != nil {
			return err
		}
		return doneTaskTx(ctx, tx, &t, c.p.UserID, c.p.Roles, path)
	})
	if msg, ok := taskRefusal(err); ok {
		return true, c.say(ctx, s, msg)
	}
	if err != nil {
		return true, err
	}
	s.afterDone(ctx, c.tenant.ID, t, c.p.UserID)
	return true, c.say(ctx, s, "Done, with the photo: "+t.What+". Thank you.")
}

// --- a task said in chat ---

var (
	taskByRe    = regexp.MustCompile(`(?i)\s+(?:by|at|before)\s+(today\s+|tomorrow\s+)?(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)?\s*(today|tomorrow)?\s*\.?$`)
	taskEveryRe = regexp.MustCompile(`(?i)^every\s+(day|monday|tuesday|wednesday|thursday|friday|saturday|sunday|mon|tue|wed|thu|fri|sat|sun)\s+at\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)?\s*[:,-]?\s*`)
	// "count the float every day at 7am": the repeat said at the end.
	taskEveryEndRe = regexp.MustCompile(`(?i)[,;]?\s+every\s+(day|monday|tuesday|wednesday|thursday|friday|saturday|sunday|mon|tue|wed|thu|fri|sat|sun)\s+at\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)?\s*\.?$`)
	taskPhotoRe    = regexp.MustCompile(`(?i)[,.]?\s*(?:with|send)\s+a\s+photo\.?$`)
)

// clockOf is "3pm", "15:00", "7.30am" as hours and minutes; false if not a time.
func clockOf(h, m, ampm string) (int, int, bool) {
	hour, _ := strconv.Atoi(h)
	minute := 0
	if m != "" {
		minute, _ = strconv.Atoi(m)
	}
	switch strings.ToLower(ampm) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	return hour, minute, hour <= 23 && minute <= 59
}

// parseTaskWords reads "deliver Rivers Foods' order by 3pm", "every day
// at 7am: count the cash float", "clean the freezer by 5pm tomorrow, with
// a photo" into a task; the words left are what is to be done.
func parseTaskWords(words string, now time.Time) (staffTaskIn, bool) {
	var in staffTaskIn
	words = strings.TrimSpace(words)
	if m := taskPhotoRe.FindStringIndex(words); m != nil {
		in.Photo, words = true, strings.TrimSpace(words[:m[0]])
	}
	repeat := func(m []string) (*taskRepeatIn, bool) {
		h, mi, ok := clockOf(m[2], m[3], m[4])
		if !ok {
			return nil, false
		}
		r := &taskRepeatIn{Every: "day", At: fmt.Sprintf("%02d:%02d", h, mi)}
		if d := strings.ToLower(m[1]); d != "day" {
			r.Every, r.Day = "week", d[:3]
		}
		return r, true
	}
	if m := taskEveryRe.FindStringSubmatch(words); m != nil {
		r, ok := repeat(m)
		if !ok {
			return in, false
		}
		in.Repeat, words = r, strings.TrimSpace(words[len(m[0]):])
	} else if i := taskEveryEndRe.FindStringSubmatchIndex(words); i != nil {
		m := taskEveryEndRe.FindStringSubmatch(words)
		r, ok := repeat(m)
		if !ok {
			return in, false
		}
		in.Repeat, words = r, strings.TrimSpace(words[:i[0]])
	} else if m := taskByRe.FindStringSubmatchIndex(words); m != nil {
		sub := func(i int) string {
			if m[2*i] < 0 {
				return ""
			}
			return words[m[2*i]:m[2*i+1]]
		}
		h, mi, ok := clockOf(sub(2), sub(3), sub(4))
		// A bare number with no am, pm or minutes is not a time ("by 2").
		if ok && (sub(3) != "" || sub(4) != "") {
			d := now.In(storefront.Lagos)
			due := time.Date(d.Year(), d.Month(), d.Day(), h, mi, 0, 0, storefront.Lagos)
			switch strings.TrimSpace(strings.ToLower(sub(1) + sub(5))) {
			case "tomorrow":
				due = due.AddDate(0, 0, 1)
			case "":
				// "by 7am" said at 9am is tomorrow's 7am, not one already late.
				if !due.After(now) {
					due = due.AddDate(0, 0, 1)
				}
			}
			in.DueAt = &due
			words = strings.TrimSpace(words[:m[0]])
		}
	}
	in.What = strings.TrimRight(strings.TrimSpace(words), ",.:;")
	return in, in.What != ""
}

// waTaskSaid is "@Tunde deliver Rivers Foods' order by 3pm": a person (or
// "@cashier", anyone in a role) and a thing to do; Taskiem asks before it
// makes the task. False when no one in the business is named.
func (s *Server) waTaskSaid(ctx context.Context, c *chat, text string) (bool, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "@"))
	type person struct {
		id   uuid.UUID
		name string
	}
	var people []person
	var roles []string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT u.id, u.name FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.tenant_id = $1 AND u.name <> ''`, c.tenant.ID)
		if err != nil {
			return err
		}
		if people, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (person, error) {
			var p person
			return p, r.Scan(&p.id, &p.name)
		}); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT DISTINCT role FROM memberships WHERE tenant_id = $1`, c.tenant.ID)
		if err != nil {
			return err
		}
		roles, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil {
		return true, err
	}
	low := strings.ToLower(rest)
	starts := func(name string) bool {
		n := strings.ToLower(name)
		return strings.HasPrefix(low, n) && (len(low) == len(n) || !isWordRune(rune(low[len(n)])))
	}
	var in staffTaskIn
	var who string
	var n int
	var same []string // others whose name fits as well: which one is meant
	// The longest name that starts the words: "Tunde Bakare" before "Tunde".
	for _, p := range people {
		best := 0
		for _, name := range []string{p.name, strings.Fields(p.name)[0]} {
			if starts(name) && len(name) > best {
				best = len(name)
			}
		}
		switch {
		case best == 0:
		case best > n:
			id := p.id
			in.ToUser, in.ToRole, who, n, same = &id, "", p.name, best, nil
		case best == n && in.ToUser != nil && *in.ToUser != p.id:
			same = append(same, p.name)
		}
	}
	for _, r := range roles {
		for _, name := range []string{strings.ReplaceAll(r, "_", " "), "any " + strings.ReplaceAll(r, "_", " ")} {
			if starts(name) && len(name) > n {
				in.ToUser, in.ToRole, who, n = nil, r, "any "+strings.ReplaceAll(r, "_", " "), len(name)
			}
		}
	}
	if n == 0 {
		return false, nil
	}
	// "@Ada Foods …" names a linked business when the business's name is
	// longer than the person's: it is a message to them, not a task.
	if to, _, which, err := s.mentionedLinks(ctx, c, strings.TrimSpace(text)); err != nil {
		return true, err
	} else {
		for _, l := range to {
			if len(l.Name) > n && strings.HasPrefix(low, strings.ToLower(l.Name)) {
				return false, nil
			}
		}
		if which != nil && len(which.Typed) > n {
			return false, nil
		}
	}
	if len(same) > 0 {
		names := append([]string{who}, same...)
		return true, c.say(ctx, s, "Which one: "+strings.Join(names, " or ")+"? Say the whole name, like @"+names[0]+" "+strings.TrimSpace(rest[n:])+".")
	}
	said, ok := parseTaskWords(rest[n:], s.staffNow())
	if !ok {
		return true, c.say(ctx, s, "Say what "+who+" is to do, like: @"+strings.Fields(who)[0]+" count the cash float by 7am.")
	}
	said.ToUser, said.ToRole = in.ToUser, in.ToRole
	if err := said.check(); err != nil {
		msg, _ := taskRefusal(err)
		return true, c.say(ctx, s, msg)
	}
	line := said.What
	switch {
	case said.Repeat != nil && said.Repeat.Every == "day":
		line += ", every day at " + said.Repeat.At
	case said.Repeat != nil:
		line += ", every " + said.Repeat.Day + " at " + said.Repeat.At
	case said.DueAt != nil:
		line += ", " + dueWords(*said.DueAt, s.staffNow())
	}
	if said.Photo {
		line += ", with a photo"
	}
	d := chatData{Task: &chatTask{In: said, Line: line}}
	if err := s.saveSession(ctx, c, stateConfirming, d, 15*time.Minute); err != nil {
		return true, err
	}
	return true, c.say(ctx, s, "Make it a task for "+who+": "+line+"?",
		whatsapp.Button{ID: "yes", Title: c.t("button.yes")}, whatsapp.Button{ID: "no", Title: c.t("button.no")})
}

func isWordRune(r rune) bool {
	return r == '_' || r == '\'' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// waTaskReply is the giver's yes or no to a task said in chat.
func (s *Server) waTaskReply(ctx context.Context, c *chat, cmd string) error {
	ct := c.data.Task
	switch cmd {
	case "yes", "y", "ok", "confirm":
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, "No task made.")
	default:
		// Anything else lets the task go and is read as a new message.
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return s.waCommand(ctx, c)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(taskGive) {
		return c.say(ctx, s, "Only those who run the business give tasks.")
	}
	var t *staffTask
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if t, _, err = newTaskTx(ctx, tx, c.tenant.ID, c.p.UserID, ct.In, s.staffNow()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "staff.task_give", c.tenant.ID.String(), map[string]any{"repeats": ct.In.Repeat != nil})
	})
	if msg, ok := taskRefusal(err); ok {
		return c.say(ctx, s, msg)
	}
	if err != nil {
		return err
	}
	if t == nil {
		return c.say(ctx, s, "Done: "+ct.Line+". Each time it falls due, it is a task of its own.")
	}
	s.tellNewTask(ctx, c.tenant.ID, *t, false)
	return c.say(ctx, s, "Task given to "+t.who()+": "+ct.Line+". You will hear when it is done.")
}

package forms

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Asking a person where they are (docs/spec/device-fields.md §2, D1): a
// member or a role in the app and the web first (the request under To
// fill in, with an alert), then in WhatsApp or Telegram where they are
// linked; the person working a business link in their side of the
// conversation, through the door the rider steps use.

// ChatAsk is a form step to put to people in their chat apps, once the
// request is recorded as sent. The api role sends it (Service.Ask).
type ChatAsk struct {
	// Tenant is the business whose conversation it is said in: the asking
	// business for its members, the linked business for its person.
	Tenant uuid.UUID
	// Users are who is asked; Role, when set, is everyone with it.
	Users []uuid.UUID
	Role  string
	// From is the asking business's name, Request the form request,
	// Title and Schema its question.
	From    string
	Request uuid.UUID
	Title   string
	Schema  map[string]any
	// Through is true for a link's person: the request is the other
	// business's, answered through the door.
	Through bool
	// Starter is true for the person who started the run (to: "starter",
	// made tools M4), asked in Channel, the chat they started it from
	// (whatsapp, telegram, slack, web; else the app).
	Starter bool
	Channel string
}

// AlertFormLink is the alert the person working a business link gets for
// a form step the other business put to them.
const AlertFormLink = "form.link_request"

// linkActor is who the door names for a form step's crossing.
var linkActor = links.Actor{Type: "system", ID: "forms"}

// sendToLink delivers a form step to the person working a business link:
// the link is found by its id or the linked business's (an active one),
// and through the door the other side keeps a note of the request
// (form_request_doors) for that person, with an alert. Returns who to ask
// in their chat, or nil when nobody can be asked (noted on the request).
func (s *Service) sendToLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p pending, ref string) (*ChatAsk, error) {
	note := func(why string) (*ChatAsk, error) {
		s.log().Warn("forms: a form step to a link was not sent", "request", p.id, "why", why)
		_, err := tx.Exec(ctx, `UPDATE form_requests SET sent_at = now(), note = $2 WHERE id = $1`, p.id, why)
		return nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(ref))
	if err != nil {
		return note("No way to ask: the link named is not a link's id")
	}
	list, _, err := links.ListTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var link *links.Link
	for i, l := range list {
		if (l.ID == id || l.Business == id) && l.State == links.Active {
			link = &list[i]
			break
		}
	}
	if link == nil {
		return note("No way to ask: this business is not linked with the business named")
	}
	var schemaRaw map[string]any
	if err := tx.QueryRow(ctx, `SELECT schema FROM form_requests WHERE id = $1`, p.id).Scan(&schemaRaw); err != nil {
		return nil, err
	}
	other, err := links.EnterTx(ctx, tx, link.ID, "link.form.asked", nil, linkActor)
	if err != nil {
		if back := links.ReturnTx(ctx, tx, tenant); back != nil {
			return nil, back
		}
		var off *links.OtherOffError
		if errors.As(err, &off) || errors.Is(err, links.ErrNotFound) || errors.Is(err, links.ErrState) {
			return note("No way to ask: the link has ended, or the other business cannot be reached through it")
		}
		return nil, err
	}
	// The person working the conversation on their side: its owner (a
	// rider's business is one person; a company's owner hands it on).
	var user uuid.UUID
	err = tx.QueryRow(ctx, `SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'owner' ORDER BY user_id LIMIT 1`, other).Scan(&user)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO form_request_doors (id, tenant_id, from_tenant, user_id, business, title, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (id) DO NOTHING`, p.id, other, tenant, user, clip(p.business, 200), clip(p.title, 200), p.expires)
	}
	if err == nil {
		app := strings.TrimRight(s.AppURL, "/") + "/forms/requests/" + p.id.String()
		_, err = alerts.Notify(ctx, tx, other, alerts.Alert{Kind: AlertFormLink, Dedup: p.id.String(), Title: p.business + " asks: " + p.title,
			Body: "Answer it in the app or on the web, or in WhatsApp or Telegram.", Link: app, Detail: map[string]any{"request": p.id.String(), "from": p.business}})
	}
	noOwner := errors.Is(err, pgx.ErrNoRows)
	if back := links.ReturnTx(ctx, tx, tenant); back != nil {
		return nil, back
	}
	if noOwner {
		return note("No way to ask: nobody is working the link on the other side")
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE form_requests SET sent_at = now() WHERE id = $1`, p.id); err != nil {
		return nil, err
	}
	return &ChatAsk{Tenant: other, Users: []uuid.UUID{user}, From: p.business, Request: p.id, Title: p.title, Schema: schemaRaw, Through: true}, nil
}

// ChatKinds are the device fields a question has that a chat answers with
// its own messages (a location, a photo), in the schema's order.
func ChatKinds(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var out []string
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if k := wd.DeviceKind(p); k == wd.DeviceLocation || k == wd.DevicePhoto {
			out = append(out, k)
		}
	}
	return out
}

// ChatAnswerable reports whether a chat can answer a question whole: every
// field it requires is a location or a photo, or a signature or a scan,
// which a chat sends unavailable (§3). A question that also requires
// something typed is answered in the app or the browser.
func ChatAnswerable(schema map[string]any) bool {
	props, _ := schema["properties"].(map[string]any)
	req, _ := schema["required"].([]any)
	for _, r := range req {
		name, _ := r.(string)
		p, _ := props[name].(map[string]any)
		switch wd.DeviceKind(p) {
		case wd.DeviceLocation, wd.DevicePhoto:
		case wd.DeviceSignature, wd.DeviceScan:
			// Sent unavailable: the workflow handles that.
		default:
			return false
		}
	}
	return len(ChatKinds(schema)) > 0
}

// FieldOf is the name of a question's field of a device kind, "" if none.
func FieldOf(schema map[string]any, kind string) string {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if wd.DeviceKind(p) == kind {
			return name
		}
	}
	return ""
}

// ChatWhole reports whether what a chat has sent (fields named in have)
// answers the question: every required location and photo, or, when none
// is required, every location and photo it asks.
func ChatWhole(schema map[string]any, have map[string]bool) bool {
	props, _ := schema["properties"].(map[string]any)
	req, _ := schema["required"].([]any)
	needed := 0
	for _, r := range req {
		name, _ := r.(string)
		p, _ := props[name].(map[string]any)
		if k := wd.DeviceKind(p); k == wd.DeviceLocation || k == wd.DevicePhoto {
			needed++
			if !have[name] {
				return false
			}
		}
	}
	if needed > 0 {
		return true
	}
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if k := wd.DeviceKind(p); (k == wd.DeviceLocation || k == wd.DevicePhoto) && !have[name] {
			return false
		}
	}
	return true
}

// WeakerInChat names the fields whose require_source a chat cannot meet
// (a pin sent is not a reading; a photo sent may be an old one).
func WeakerInChat(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var out []string
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if k := wd.DeviceKind(p); (k == wd.DeviceLocation || k == wd.DevicePhoto) && p["require_source"] != nil {
			out = append(out, name)
		}
	}
	return out
}

// RequestDoor is a form step another business put to the caller's
// business through a link, as its note (form_request_doors) has it.
type RequestDoor struct {
	ID        uuid.UUID
	From      uuid.UUID
	User      uuid.UUID
	Business  string
	Title     string
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// DoorTx is the note of a request put to the business in scope through a
// link; pgx.ErrNoRows when there is none.
func DoorTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (RequestDoor, error) {
	var d RequestDoor
	err := tx.QueryRow(ctx, `SELECT id, from_tenant, user_id, business, title, status, expires_at, created_at FROM form_request_doors WHERE id = $1`, id).
		Scan(&d.ID, &d.From, &d.User, &d.Business, &d.Title, &d.Status, &d.ExpiresAt, &d.CreatedAt)
	return d, err
}

// LinkedTx are the businesses the business in scope is actively linked
// with: a door's note is listed, read and answered only while the link
// it came through is active, as the door itself (links.EnterTx) is
// crossed only then.
func LinkedTx(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]bool, error) {
	list, _, err := links.ListTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]bool{}
	for _, l := range list {
		if l.State == links.Active {
			out[l.Business] = true
		}
	}
	return out, nil
}

// AnsweredDoorTx marks a door's note answered, in the asked side's scope.
func AnsweredDoorTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE form_request_doors SET status = 'answered', answered_at = now() WHERE id = $1 AND status = 'pending'`, id)
	return err
}

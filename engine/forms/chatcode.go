package forms

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A code typed in WhatsApp or Telegram (build-plan-gaps H3, g-scan-wa): a
// form step's scan is asked there as words to type, once the question's
// location and photo are in, and only the person's next message in that
// chat is taken as the code (ChatCodeWaiting): never a command word, and
// never a bare number while a menu waits for one (the api decides that).
// asking names the field on the person's draft and asking_in the chats it
// was asked in; any next message in one of them clears it there.

// CodeField is the scan field a chat asks next, as a code to type: the
// question's typed-allowed scan, once every location and photo it asks is
// held (have). "" when nothing is left but the code, or no code is asked.
func CodeField(schema map[string]any, have map[string]bool) string {
	props, _ := schema["properties"].(map[string]any)
	code := ""
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		switch k := wd.DeviceKind(p); {
		case (k == wd.DeviceLocation || k == wd.DevicePhoto) && !have[name]:
			return ""
		case k == wd.DeviceScan && chatTakes(k, p) && !have[name]:
			code = name
		}
	}
	return code
}

// ChatArm marks field as asked of user in channel: their next message
// there is its code.
func (s *Service) ChatArm(ctx context.Context, tenant, request, user uuid.UUID, field, channel string) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO form_chat_answers (request_id, tenant_id, user_id, asking, asking_in) VALUES ($1, $2, $3, $4, ARRAY[$5])
			ON CONFLICT (request_id, user_id) DO UPDATE SET asking = $4,
			  asking_in = CASE WHEN $5 = ANY (form_chat_answers.asking_in) THEN form_chat_answers.asking_in ELSE form_chat_answers.asking_in || $5 END,
			  updated_at = now()`, request, tenant, user, field, channel)
		return err
	})
}

// ChatCodeWaiting is called with every message the person sends in a
// chat: it clears what was asked of them there (only the next message is
// the code), and returns the question and field the message may answer,
// the newest armed. Nil when nothing was asked there.
func (s *Service) ChatCodeWaiting(ctx context.Context, who ChatAsker, channel string) (*ChatQuestion, string, error) {
	type armed struct {
		q     ChatQuestion
		field string
	}
	var got []armed
	take := func(tx pgx.Tx, tenant uuid.UUID, through bool, from string) error {
		rows, err := tx.Query(ctx, `WITH old AS (SELECT request_id, asking FROM form_chat_answers WHERE user_id = $1 AND $2 = ANY (asking_in) FOR UPDATE)
			UPDATE form_chat_answers c SET asking_in = array_remove(c.asking_in, $2), asking = CASE WHEN cardinality(array_remove(c.asking_in, $2)) = 0 THEN NULL ELSE c.asking END
			FROM old, form_requests r JOIN tenants t ON t.id = r.tenant_id WHERE c.request_id = old.request_id AND c.user_id = $1 AND r.id = c.request_id
			RETURNING r.id, r.title, r.schema, r.created_at, r.status, old.asking, t.name`, who.User, channel)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a armed
			var status string
			var field *string
			var name string
			a.q = ChatQuestion{Tenant: tenant, Through: through, From: from}
			if err := rows.Scan(&a.q.Request, &a.q.Title, &a.q.Schema, &a.q.created, &status, &field, &name); err != nil {
				return err
			}
			if !through {
				a.q.From = name
			}
			if status == "pending" && field != nil {
				a.field = *field
				got = append(got, a)
			}
		}
		return rows.Err()
	}
	var doors []RequestDoor
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{who.Tenant}, func(tx pgx.Tx) error {
		if err := take(tx, who.Tenant, false, ""); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, from_tenant, business, created_at FROM form_request_doors WHERE user_id = $1 AND status = 'pending' AND expires_at > now()`, who.User)
		if err != nil {
			return err
		}
		doors, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestDoor, error) {
			var d RequestDoor
			return d, r.Scan(&d.ID, &d.From, &d.Business, &d.CreatedAt)
		})
		if err != nil || len(doors) == 0 {
			return err
		}
		linked, err := LinkedTx(ctx, tx)
		if err != nil {
			return err
		}
		doors = slices.DeleteFunc(doors, func(d RequestDoor) bool { return !linked[d.From] })
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	seen := map[uuid.UUID]bool{}
	for _, d := range doors {
		if seen[d.From] {
			continue
		}
		seen[d.From] = true
		if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{d.From}, func(tx pgx.Tx) error { return take(tx, d.From, true, d.Business) }); err != nil {
			return nil, "", err
		}
	}
	if len(got) == 0 {
		return nil, "", nil
	}
	slices.SortStableFunc(got, func(a, b armed) int { return b.q.created.Compare(a.q.created) })
	q := got[0].q
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		var err error
		q.Held, q.Waiting, err = s.heldTx(ctx, tx, q.Tenant, q.Request, who.User)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	return &q, got[0].field, err
}

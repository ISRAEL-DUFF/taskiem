package notices_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/notices"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// The money group: listed only for people who may read invoices, on by
// default for owners and off for others, each half chosen on its own.
func TestMoneyGroup(t *testing.T) {
	ctx := context.Background()
	e := rt.New(t)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	person := func(role string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		in(func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'm')`, id, id.String()+"@notices.test"); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`, e.Tenant, id, role)
			return err
		})
		return id
	}
	owner, viewer := person("owner"), person("viewer")
	money := func(user uuid.UUID, may bool) *notices.Setting {
		t.Helper()
		var list []notices.Setting
		in(func(tx pgx.Tx) error {
			var err error
			list, err = notices.SettingsTx(ctx, tx, user, may)
			return err
		})
		if i := slices.IndexFunc(list, func(s notices.Setting) bool { return s.Group == notices.GroupMoney }); i >= 0 {
			return &list[i]
		}
		return nil
	}
	if s := money(owner, true); s == nil || *s != notices.MoneyDefault(true) {
		t.Fatalf("owner's default: %+v", s)
	}
	if s := money(viewer, true); s == nil || *s != notices.MoneyDefault(false) {
		t.Fatalf("viewer's default: %+v", s)
	}
	if s := money(viewer, false); s != nil {
		t.Fatalf("listed without invoice.read: %+v", s)
	}
	wants := func() (daily, monthly []uuid.UUID) {
		t.Helper()
		in(func(tx pgx.Tx) error {
			var err error
			daily, monthly, err = notices.Wants(ctx, tx, []uuid.UUID{owner, viewer}, notices.GroupMoney)
			return err
		})
		return daily, monthly
	}
	if d, m := wants(); !slices.Equal(d, []uuid.UUID{owner}) || !slices.Equal(m, []uuid.UUID{owner}) {
		t.Fatalf("by default: daily %v, monthly %v", d, m)
	}
	set := func(user uuid.UUID, s notices.Setting, may bool) error {
		var err error
		_ = db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			err = notices.SetTx(ctx, tx, e.Tenant, user, []notices.Setting{s}, may)
			return err
		})
		return err
	}
	if err := set(viewer, notices.Setting{Group: notices.GroupMoney, Chat: true}, false); !errors.Is(err, notices.ErrNoMoney) {
		t.Fatalf("without invoice.read: %v", err)
	}
	if err := set(viewer, notices.Setting{Group: notices.GroupMoney, Chat: true}, true); err != nil {
		t.Fatal(err)
	}
	if err := set(owner, notices.Setting{Group: notices.GroupMoney, Email: true}, true); err != nil {
		t.Fatal(err)
	}
	if d, m := wants(); !slices.Equal(d, []uuid.UUID{viewer}) || !slices.Equal(m, []uuid.UUID{owner}) {
		t.Fatalf("after choosing: daily %v, monthly %v", d, m)
	}
	if s := money(owner, true); s == nil || s.Chat || !s.Email {
		t.Fatalf("owner's choice: %+v", s)
	}
	// The other groups keep their defaults, whoever the person.
	in(func(tx pgx.Tx) error {
		chat, _, err := notices.Wants(ctx, tx, []uuid.UUID{owner, viewer}, notices.GroupFinished)
		if err == nil && len(chat) != 0 {
			t.Errorf("finished runs on chat by default: %v", chat)
		}
		return err
	})
}

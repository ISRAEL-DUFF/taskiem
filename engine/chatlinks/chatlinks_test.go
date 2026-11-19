package chatlinks_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func TestCodesHaveNoLookAlikes(t *testing.T) {
	for _, c := range "01iluvoIO" {
		if strings.ContainsRune(chatlinks.Alphabet, c) {
			t.Fatalf("the alphabet has %q", c)
		}
	}
	d := dbtest.New(t)
	l := &chatlinks.Links{Pool: d.App, Key: bytes.Repeat([]byte{7}, 32), Base: "https://taskiem.test"}
	seen := map[string]bool{}
	for range 200 {
		tn := d.SeedTenant(t, nil)
		var k chatlinks.Link
		in(t, d, tn.ID, func(tx pgx.Tx) error {
			var err error
			k, err = l.CurrentTx(context.Background(), tx, tn.ID, "user:o")
			return err
		})
		if len(k.Code) != chatlinks.Length || chatlinks.Clean(k.Code) != k.Code {
			t.Fatalf("code %q", k.Code)
		}
		if strings.ContainsAny(k.Code, "01iluvo") {
			t.Fatalf("code %q has a look-alike", k.Code)
		}
		if seen[k.Code] {
			t.Fatalf("code %q given twice", k.Code)
		}
		seen[k.Code] = true
		if k.URL != "https://taskiem.test/m/"+k.Code {
			t.Fatalf("url %q", k.URL)
		}
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{" ABCDEFGH23 ": "abcdefgh23", "abcdefgh2": "", "abcdefgh20": "", "abcdefgh2l": "", "": ""} {
		if got := chatlinks.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func in(t *testing.T, d *dbtest.DB, tenant uuid.UUID, fn func(pgx.Tx) error) {
	t.Helper()
	if err := db.InTenantTx(context.Background(), d.App, []uuid.UUID{tenant}, fn); err != nil {
		t.Fatal(err)
	}
}

func TestLinkLifecycle(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	l := &chatlinks.Links{Pool: d.App, Key: bytes.Repeat([]byte{7}, 32), Base: "https://taskiem.test"}
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	var first, again, other chatlinks.Link
	in(t, d, a.ID, func(tx pgx.Tx) error {
		var err error
		if first, err = l.CurrentTx(ctx, tx, a.ID, "user:o"); err != nil {
			return err
		}
		again, err = l.CurrentTx(ctx, tx, a.ID, "user:o")
		return err
	})
	if first.Code != again.Code {
		t.Fatalf("the live link changed: %s then %s", first.Code, again.Code)
	}
	in(t, d, b.ID, func(tx pgx.Tx) error {
		var err error
		other, err = l.CurrentTx(ctx, tx, b.ID, "user:o")
		return err
	})
	if other.Code == first.Code {
		t.Fatal("two businesses share a code")
	}
	// Only the hash is kept.
	var plain int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_chat_codes WHERE code_hash = convert_to($1, 'UTF8') OR encode(nonce, 'escape') LIKE '%' || $1 || '%'`, first.Code).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("the code is kept as it is: %d %v", plain, err)
	}
	if got, err := l.Resolve(ctx, strings.ToUpper(first.Code)); err != nil || got != a.ID {
		t.Fatalf("resolve: %v %v", got, err)
	}
	if _, err := l.Resolve(ctx, "abcdefgh23"); !errors.Is(err, chatlinks.ErrNotFound) {
		t.Fatalf("an unknown code: %v", err)
	}
	if _, err := l.Resolve(ctx, "nonsense"); !errors.Is(err, chatlinks.ErrNotFound) {
		t.Fatalf("not a code: %v", err)
	}

	// Make a new link: the old one says it no longer works, for 30 days.
	var fresh chatlinks.Link
	in(t, d, a.ID, func(tx pgx.Tx) error {
		var err error
		fresh, err = l.ReplaceTx(ctx, tx, a.ID, "user:o")
		return err
	})
	if fresh.Code == first.Code {
		t.Fatal("the new link is the old one")
	}
	if got, err := l.Resolve(ctx, fresh.Code); err != nil || got != a.ID {
		t.Fatalf("the new link: %v %v", got, err)
	}
	if _, err := l.Resolve(ctx, first.Code); !errors.Is(err, chatlinks.ErrReplaced) {
		t.Fatalf("the old link: %v", err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE customer_chat_codes SET replaced_at = now() - interval '29 days' WHERE tenant_id = $1 AND replaced_at IS NOT NULL`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve(ctx, first.Code); !errors.Is(err, chatlinks.ErrReplaced) {
		t.Fatalf("the old link at 29 days: %v", err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE customer_chat_codes SET replaced_at = now() - interval '31 days' WHERE tenant_id = $1 AND replaced_at IS NOT NULL`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve(ctx, first.Code); !errors.Is(err, chatlinks.ErrNotFound) {
		t.Fatalf("the old link after 30 days: %v", err)
	}
	// A later new link clears codes past their 30 days.
	in(t, d, a.ID, func(tx pgx.Tx) error {
		_, err := l.ReplaceTx(ctx, tx, a.ID, "user:o")
		return err
	})
	var kept int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_chat_codes WHERE tenant_id = $1`, a.ID).Scan(&kept); err != nil || kept != 2 {
		t.Fatalf("codes kept: %d %v", kept, err)
	}

	// A workflows workspace or a suspended business has no link that works.
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET kind = 'workflows' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve(ctx, other.Code); !errors.Is(err, chatlinks.ErrNotFound) {
		t.Fatalf("a workspace's link: %v", err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET kind = 'business', status = 'suspended' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Resolve(ctx, other.Code); !errors.Is(err, chatlinks.ErrNotFound) {
		t.Fatalf("a suspended business's link: %v", err)
	}

	// One business never reads another's code.
	var n int
	in(t, d, a.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM customer_chat_codes WHERE tenant_id = $1`, b.ID).Scan(&n)
	})
	if n != 0 {
		t.Fatal("a business reads another's codes")
	}
}

func TestLinkMadeOnceAtOnce(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	l := &chatlinks.Links{Pool: d.App, Key: bytes.Repeat([]byte{7}, 32), Base: "https://taskiem.test"}
	a := d.SeedTenant(t, nil)
	var mu sync.Mutex
	codes := map[string]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.InTenantTx(ctx, d.App, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
				k, err := l.CurrentTx(ctx, tx, a.ID, "user:o")
				mu.Lock()
				codes[k.Code] = true
				mu.Unlock()
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(codes) != 1 {
		t.Fatalf("one business, %d links", len(codes))
	}
}

func TestNoticesOncePerPeriod(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	key := []byte("conversation-1")
	take := func() bool {
		var took bool
		in(t, d, a.ID, func(tx pgx.Tx) error {
			var err error
			took, err = chatlinks.TakeNoticeTx(ctx, tx, a.ID, chatlinks.NoticeAnsweredSMS, key, time.Hour)
			return err
		})
		return took
	}
	if !take() {
		t.Fatal("the first notice was refused")
	}
	if take() {
		t.Fatal("a second notice within the hour")
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE customer_chat_notices SET last_at = now() - interval '61 minutes'`); err != nil {
		t.Fatal(err)
	}
	if !take() {
		t.Fatal("a notice after the hour was refused")
	}
	if take() {
		t.Fatal("a second notice within the next hour")
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	in(t, d, a.ID, func(tx pgx.Tx) error {
		st, err := chatlinks.SettingsTx(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if !st.TakesChats || st.StorefrontButton != chatlinks.ButtonBoth || st.LinkInUpdates != nil || !st.UpdatesLink {
			t.Errorf("defaults: %+v", st)
		}
		// A business with its own number: the link is off in updates by default.
		if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_numbers (phone_number_id, tenant_id, waba_id, display_number, created_by) VALUES ('1234567', $1, '7654321', '+2348030000000', 'test')`, a.ID); err != nil {
			return err
		}
		if st, err = chatlinks.SettingsTx(ctx, tx, a.ID); err != nil || st.UpdatesLink {
			t.Errorf("own number: %+v %v", st, err)
		}
		on, btn := true, "taskiem"
		if st, err = chatlinks.SaveSettingsTx(ctx, tx, a.ID, chatlinks.SettingsIn{LinkInUpdates: &on, StorefrontButton: &btn}, "user:o"); err != nil {
			return err
		}
		if !st.UpdatesLink || st.StorefrontButton != "taskiem" || !st.TakesChats {
			t.Errorf("saved: %+v", st)
		}
		bad := "telegram"
		if _, err := chatlinks.SaveSettingsTx(ctx, tx, a.ID, chatlinks.SettingsIn{StorefrontButton: &bad}, "user:o"); !errors.Is(err, chatlinks.ErrInvalid) {
			t.Errorf("a bad button: %v", err)
		}
		if st, err = chatlinks.SaveSettingsTx(ctx, tx, a.ID, chatlinks.SettingsIn{ResetUpdates: true}, "user:o"); err != nil || st.LinkInUpdates != nil || st.UpdatesLink {
			t.Errorf("back to the default: %+v %v", st, err)
		}
		return nil
	})
}

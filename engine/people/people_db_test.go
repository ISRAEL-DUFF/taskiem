package people_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/people"
)

type fixture struct {
	t   *testing.T
	d   *dbtest.DB
	p   *people.People
	now time.Time
	// blocks are pairs blocked (either way).
	blocks map[[2]uuid.UUID]bool
	chats  map[[2]uuid.UUID]bool
}

func pair(a, b uuid.UUID) [2]uuid.UUID {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return [2]uuid.UUID{a, b}
}

func newFixture(t *testing.T) *fixture {
	d := dbtest.New(t)
	f := &fixture{t: t, d: d, now: time.Now().UTC().Truncate(time.Microsecond), blocks: map[[2]uuid.UUID]bool{}, chats: map[[2]uuid.UUID]bool{}}
	f.p = &people.People{Pool: d.App, Key: bytes.Repeat([]byte{7}, 32), Base: "https://taskiem.test",
		Now:      func() time.Time { return f.now },
		Blocked:  func(_ context.Context, a, b uuid.UUID) (bool, error) { return f.blocks[pair(a, b)], nil },
		Chatting: func(_ context.Context, a, b uuid.UUID) (bool, error) { return f.chats[pair(a, b)], nil }}
	return f
}

// person makes an account with a verified phone ("" for none).
func (f *fixture) person(name, phone string) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	if _, err := f.d.Admin.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, id, id.String()+"@phone.taskiem.invalid", name); err != nil {
		f.t.Fatal(err)
	}
	if phone != "" {
		if _, err := f.d.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) VALUES ($1, $2)`, id, phone); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

func (f *fixture) set(person uuid.UUID, in people.SettingsIn) {
	f.t.Helper()
	if _, err := f.p.SaveSettings(context.Background(), person, in); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) add(person, contact uuid.UUID) {
	f.t.Helper()
	if err := f.p.AddContact(context.Background(), person, contact, "", people.FromNumber); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) photo(person uuid.UUID) {
	f.t.Helper()
	if _, err := f.d.Admin.Exec(context.Background(), `INSERT INTO person_photos (person_id, type, data) VALUES ($1, 'image/png', '\x89504e47')`, person); err != nil {
		f.t.Fatal(err)
	}
}

func aud(a people.Audience) *people.Audience { return &a }
func yes(b bool) *bool                       { return &b }

// The settings (§5.4, §17): the defaults until changed, each change kept,
// an audience that is not one refused, and another person's untouched.
func TestSettingsDefaultsAndChanges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada := f.person("Tolu", "+2348031000001"), f.person("Ada", "+2348031000002")
	s, err := f.p.SettingsOf(ctx, tolu)
	if err != nil || s != people.Defaults() {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	want := people.Settings{FindByNumber: people.Contacts, FindFromContacts: true, Message: people.Everyone, LastSeen: people.Contacts, ReadTicks: true, Photo: people.Contacts, ShowNumber: false}
	if s != want {
		t.Fatalf("the founder's defaults: %+v", s)
	}
	s, err = f.p.SaveSettings(ctx, tolu, people.SettingsIn{FindByNumber: aud(people.Everyone), ShowNumber: yes(true), ReadTicks: yes(false)})
	if err != nil || s.FindByNumber != people.Everyone || !s.ShowNumber || s.ReadTicks || s.Message != people.Everyone || s.Photo != people.Contacts {
		t.Fatalf("saved: %+v %v", s, err)
	}
	s, _ = f.p.SaveSettings(ctx, tolu, people.SettingsIn{Message: aud(people.Nobody), LastSeen: aud(people.Nobody), Photo: aud(people.Everyone), FindFromContacts: yes(false)})
	if s.FindByNumber != people.Everyone || s.Message != people.Nobody || s.LastSeen != people.Nobody || s.Photo != people.Everyone || s.FindFromContacts || !s.ShowNumber {
		t.Fatalf("saved again: %+v", s)
	}
	if got, _ := f.p.SettingsOf(ctx, tolu); got != s {
		t.Fatalf("read back: %+v", got)
	}
	if _, err := f.p.SaveSettings(ctx, tolu, people.SettingsIn{Photo: aud("friends")}); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("a bad audience: %v", err)
	}
	if got, _ := f.p.SettingsOf(ctx, ada); got != people.Defaults() {
		t.Fatalf("another person's: %+v", got)
	}
	// A person's transaction reads only their own settings.
	var n int
	if err := db.InPersonTx(ctx, f.d.App, ada, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM person_privacy`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("Ada reads Tolu's settings: %d %v", n, err)
	}
}

// Contacts: added, renamed, listed by name, removed; IsContact is the
// owner's own list (one way); oneself, a bad name and a deleted account
// refused.
func TestContacts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada, bola := f.person("Tolu", "+2348031000011"), f.person("Ada", "+2348031000012"), f.person("Bola", "+2348031000013")
	if err := f.p.AddContact(ctx, tolu, ada, "Ada from church", people.FromNumber); err != nil {
		t.Fatal(err)
	}
	if err := f.p.AddContact(ctx, tolu, bola, "", people.FromLink); err != nil {
		t.Fatal(err)
	}
	if err := f.p.AddContact(ctx, tolu, tolu, "", people.FromLink); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("oneself: %v", err)
	}
	if err := f.p.AddContact(ctx, tolu, ada, string(bytes.Repeat([]byte("a"), 81)), people.FromLink); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("a long name: %v", err)
	}
	if err := f.p.AddContact(ctx, tolu, ada, "", "guess"); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("a bad source: %v", err)
	}
	if err := f.p.AddContact(ctx, tolu, uuid.Must(uuid.NewV7()), "", people.FromLink); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("no one: %v", err)
	}
	list, err := f.p.Contacts(ctx, tolu)
	if err != nil || len(list) != 2 || list[0].ID != ada || *list[0].ContactName != "Ada from church" || list[1].ID != bola || list[1].ContactName != nil || list[1].Source != people.FromLink {
		t.Fatalf("list: %+v %v", list, err)
	}
	if !list[0].InContacts || list[0].TheyAddedYou {
		t.Fatalf("one way: %+v", list[0])
	}
	if ok, _ := f.p.IsContact(ctx, tolu, ada); !ok {
		t.Fatal("Tolu added Ada")
	}
	if ok, _ := f.p.IsContact(ctx, ada, tolu); ok {
		t.Fatal("Ada did not add Tolu")
	}
	if err := f.p.RenameContact(ctx, tolu, bola, "Aunty Bola"); err != nil {
		t.Fatal(err)
	}
	if err := f.p.RenameContact(ctx, ada, bola, "x"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("not Ada's: %v", err)
	}
	// Adding again keeps the name given unless a new one is.
	if err := f.p.AddContact(ctx, tolu, bola, "", people.FromContacts); err != nil {
		t.Fatal(err)
	}
	list, _ = f.p.Contacts(ctx, tolu)
	if *list[1].ContactName != "Aunty Bola" {
		t.Fatalf("renamed: %+v", list)
	}
	if err := f.p.RemoveContact(ctx, tolu, ada); err != nil {
		t.Fatal(err)
	}
	if err := f.p.RemoveContact(ctx, tolu, ada); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("removed twice: %v", err)
	}
	if list, _ = f.p.Contacts(ctx, tolu); len(list) != 1 || list[0].ID != bola {
		t.Fatalf("after removing: %+v", list)
	}
	if list, _ = f.p.Contacts(ctx, ada); len(list) != 0 {
		t.Fatalf("Ada's: %+v", list)
	}
}

// Finding by number (§5.1) follows "find me by my number": contacts (the
// default) finds only people the owner added; everyone; nobody; the same
// answer for a hidden person and no one; a block hides; oneself; deleted
// accounts are no one; and fifty a day.
func TestFindByNumber(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada := f.person("Tolu", "+2348031000021"), f.person("Ada", "+2348031000022")
	if _, err := f.p.FindByNumber(ctx, tolu, "+2348031000022"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("contacts by default: %v", err)
	}
	if _, err := f.p.FindByNumber(ctx, tolu, "+2348031999999"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("no one: %v", err)
	}
	f.add(ada, tolu)
	got, err := f.p.FindByNumber(ctx, tolu, "+2348031000022")
	if err != nil || got.ID != ada || got.Name != "Ada" || got.Self || got.AddToken == "" || got.PhotoAt != nil {
		t.Fatalf("Ada added Tolu: %+v %v", got, err)
	}
	if !f.p.CheckAddToken(tolu, ada, got.AddToken) || f.p.CheckAddToken(ada, tolu, got.AddToken) || f.p.CheckAddToken(tolu, tolu, got.AddToken) {
		t.Fatal("the add token is Tolu's for Ada only")
	}
	f.now = f.now.Add(25 * time.Hour)
	if f.p.CheckAddToken(tolu, ada, got.AddToken) {
		t.Fatal("an add token lasts a day")
	}
	f.set(ada, people.SettingsIn{FindByNumber: aud(people.Nobody)})
	if _, err := f.p.FindByNumber(ctx, tolu, "+2348031000022"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("nobody: %v", err)
	}
	f.set(ada, people.SettingsIn{FindByNumber: aud(people.Everyone)})
	stranger := f.person("Kemi", "+2348031000023")
	if got, err := f.p.FindByNumber(ctx, stranger, "+2348031000022"); err != nil || got.ID != ada {
		t.Fatalf("everyone: %+v %v", got, err)
	}
	f.blocks[pair(stranger, ada)] = true
	if _, err := f.p.FindByNumber(ctx, stranger, "+2348031000022"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("blocked: %v", err)
	}
	if got, err := f.p.FindByNumber(ctx, tolu, "+2348031000021"); err != nil || !got.Self || got.AddToken != "" {
		t.Fatalf("oneself: %+v %v", got, err)
	}
	// A deleted account (emptied, disabled) is no one.
	if _, err := f.d.Admin.Exec(ctx, `UPDATE users SET status = 'disabled', deleted_at = now(), name = '' WHERE id = $1`, ada); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.FindByNumber(ctx, tolu, "+2348031000022"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	// Fifty a day, counted whatever the answer; the next day, again.
	f.now = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for i := 0; i < people.NumberLookupsPerDay; i++ {
		if _, err := f.p.FindByNumber(ctx, stranger, "+2348031777777"); !errors.Is(err, people.ErrNotFound) {
			t.Fatalf("lookup %d: %v", i, err)
		}
	}
	if _, err := f.p.FindByNumber(ctx, stranger, "+2348031000021"); !errors.Is(err, people.ErrLimit) {
		t.Fatalf("the 51st: %v", err)
	}
	if _, err := f.p.FindByNumber(ctx, stranger, "+2348031000021"); !errors.Is(err, people.ErrLimit) {
		t.Fatalf("the 52nd: %v", err)
	}
	f.now = f.now.Add(24 * time.Hour)
	if _, err := f.p.FindByNumber(ctx, stranger, "+2348031777777"); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("the next day: %v", err)
	}
}

// A person's link (§5.2): one live code, read back the same, resolving to
// the person; replaced, the old code says it no longer works for 30 days
// and then names no one; the public card shows the photo only when it is
// shown to everyone; a deleted account's link names no one.
func TestPersonLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu := f.person("Tolu", "+2348031000031")
	l1, err := f.p.LinkOf(ctx, tolu)
	if err != nil || len(l1.Code) != 10 || l1.URL != "https://taskiem.test/p/"+l1.Code {
		t.Fatalf("link: %+v %v", l1, err)
	}
	if again, _ := f.p.LinkOf(ctx, tolu); again != l1 {
		t.Fatalf("read back: %+v", again)
	}
	if id, err := f.p.Resolve(ctx, l1.Code); err != nil || id != tolu {
		t.Fatalf("resolve: %v %v", id, err)
	}
	if id, err := f.p.Resolve(ctx, " "+string(bytes.ToUpper([]byte(l1.Code)))+" "); err != nil || id != tolu {
		t.Fatalf("typed: %v %v", id, err)
	}
	if _, err := f.p.Resolve(ctx, "2222222222"); !errors.Is(err, people.ErrLinkNotFound) {
		t.Fatalf("no one's: %v", err)
	}
	card, err := f.p.PublicCardOf(ctx, l1.Code)
	if err != nil || card.Name != "Tolu" || card.HasPhoto {
		t.Fatalf("card: %+v %v", card, err)
	}
	f.photo(tolu)
	if card, _ = f.p.PublicCardOf(ctx, l1.Code); card.HasPhoto {
		t.Fatal("the photo is for contacts by default")
	}
	if _, _, _, err := f.p.Photo(ctx, uuid.Nil, tolu); !errors.Is(err, people.ErrNotFound) {
		t.Fatalf("the public photo: %v", err)
	}
	f.set(tolu, people.SettingsIn{Photo: aud(people.Everyone)})
	if card, _ = f.p.PublicCardOf(ctx, l1.Code); !card.HasPhoto {
		t.Fatal("shown to everyone")
	}
	if ct, data, _, err := f.p.Photo(ctx, uuid.Nil, tolu); err != nil || ct != "image/png" || len(data) != 4 {
		t.Fatalf("the public photo: %v %d %v", ct, len(data), err)
	}
	l2, err := f.p.ReplaceLink(ctx, tolu)
	if err != nil || l2.Code == l1.Code {
		t.Fatalf("replaced: %+v %v", l2, err)
	}
	if _, err := f.p.Resolve(ctx, l1.Code); !errors.Is(err, people.ErrLinkReplaced) {
		t.Fatalf("the old link: %v", err)
	}
	if id, _ := f.p.Resolve(ctx, l2.Code); id != tolu {
		t.Fatal("the new link")
	}
	f.now = f.now.Add(31 * 24 * time.Hour)
	if _, err := f.p.Resolve(ctx, l1.Code); !errors.Is(err, people.ErrLinkNotFound) {
		t.Fatalf("after 30 days: %v", err)
	}
	// Replacing again deletes codes replaced more than 30 days ago.
	if _, err := f.p.ReplaceLink(ctx, tolu); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = f.d.Admin.QueryRow(ctx, `SELECT count(*) FROM person_links WHERE person_id = $1`, tolu).Scan(&n)
	if n != 2 {
		t.Fatalf("kept codes: %d", n)
	}
	if _, err := f.d.Admin.Exec(ctx, `UPDATE users SET status = 'disabled', deleted_at = now() WHERE id = $1`, tolu); err != nil {
		t.Fatal(err)
	}
	cur, _ := f.p.LinkOf(ctx, tolu)
	if _, err := f.p.Resolve(ctx, cur.Code); !errors.Is(err, people.ErrLinkNotFound) {
		t.Fatalf("a deleted account's link: %v", err)
	}
	off := &people.People{Pool: f.d.App}
	if _, err := off.LinkOf(ctx, tolu); !errors.Is(err, people.ErrOff) {
		t.Fatalf("no key: %v", err)
	}
}

// rowsEverywhere counts every row of every table in the database.
func rowsEverywhere(t *testing.T, f *fixture) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	rows, err := f.d.Admin.Query(ctx, `SELECT quote_ident(tablename) FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, n := range names {
		var c int64
		if err := f.d.Admin.QueryRow(ctx, `SELECT count(*) FROM `+n).Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[n] = c
	}
	return out
}

// Contact matching (§5.3): only hashes of people who let themselves be
// found from contacts are answered (never the asker), with this week's
// key or last week's in its grace; nothing of the request is stored (only
// the day's counts change); batches and the day are capped.
func TestMatchContacts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu := f.person("Tolu", "+2348031000041")
	ada := f.person("Ada", "+2348031000042")
	bola := f.person("Bola", "+2348031000043")
	kemi := f.person("Kemi", "+2348031000044")
	f.set(bola, people.SettingsIn{FindFromContacts: yes(false)})
	f.add(kemi, tolu)
	f.photo(kemi)
	key, err := f.p.CurrentContactsKey()
	if err != nil || len(key.Key) != 32 || key.ExpiresAt.Before(f.now) {
		t.Fatalf("key: %+v %v", key, err)
	}
	h := func(k []byte, n string) string { return hex.EncodeToString(people.ContactHash(k, n)) }
	numbers := []string{"+2348031000041", "+2348031000042", "+2348031000043", "+2348031000044", "+2348031999999"}
	var hashes []string
	for _, n := range numbers {
		hashes = append(hashes, h(key.Key, n))
	}
	before := rowsEverywhere(t, f)
	got, err := f.p.MatchContacts(ctx, tolu, key.ID, hashes)
	if err != nil || len(got) != 2 {
		t.Fatalf("matches: %+v %v", got, err)
	}
	byID := map[uuid.UUID]people.Match{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if m := byID[ada]; m.Hash != hashes[1] || m.Name != "Ada" || m.AddToken == "" || m.PhotoAt != nil {
		t.Fatalf("Ada: %+v", m)
	}
	if m := byID[kemi]; m.Hash != hashes[3] || m.PhotoAt == nil || !m.TheyAddedYou {
		t.Fatalf("Kemi (her photo, since she added Tolu): %+v", m)
	}
	after := rowsEverywhere(t, f)
	for table, n := range after {
		if table == "people_find_counts" {
			if n-before[table] != 2 {
				t.Fatalf("counts: %d", n-before[table])
			}
			continue
		}
		if n != before[table] {
			t.Errorf("matching wrote to %s: %d to %d rows", table, before[table], n)
		}
	}
	var stored int
	if err := f.d.Admin.QueryRow(ctx, `SELECT count(*) FROM people_find_counts WHERE person_id = $1 AND kind = 'contacts' AND n = 5`, tolu).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("only how many were sent: %d %v", stored, err)
	}
	// A hash made with another key matches nothing.
	if got, _ := f.p.MatchContacts(ctx, tolu, key.ID, []string{h(bytes.Repeat([]byte{1}, 32), "+2348031000042")}); len(got) != 0 {
		t.Fatalf("another key: %+v", got)
	}
	// Next week: last week's key in its grace, then not.
	f.now = key.ExpiresAt.Add(time.Hour)
	if got, err := f.p.MatchContacts(ctx, tolu, key.ID, hashes[1:2]); err != nil || len(got) != 1 {
		t.Fatalf("grace: %+v %v", got, err)
	}
	f.now = key.ExpiresAt.Add(people.KeyGrace + time.Hour)
	if _, err := f.p.MatchContacts(ctx, tolu, key.ID, hashes[1:2]); !errors.Is(err, people.ErrOldKey) {
		t.Fatalf("after the grace: %v", err)
	}
	if _, err := f.p.MatchContacts(ctx, tolu, "nonsense", hashes[1:2]); !errors.Is(err, people.ErrOldKey) {
		t.Fatalf("a made-up key: %v", err)
	}
	key2, _ := f.p.CurrentContactsKey()
	if key2.ID == key.ID || bytes.Equal(key2.Key, key.Key) {
		t.Fatal("the key rotates")
	}
	if _, err := f.p.MatchContacts(ctx, tolu, key2.ID, []string{"zz"}); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("not a hash: %v", err)
	}
	big := make([]string, people.MatchBatch+1)
	for i := range big {
		big[i] = hashes[0]
	}
	if _, err := f.p.MatchContacts(ctx, tolu, key2.ID, big); !errors.Is(err, people.ErrInvalid) {
		t.Fatalf("too many: %v", err)
	}
	// The day's hashes: 5000.
	many := make([]string, people.MatchBatch)
	for i := range many {
		many[i] = h(key2.Key, "+23480310"+string(rune('0'+i/100))+string(rune('0'+i/10%10))+string(rune('0'+i%10))+"99")
	}
	var lastErr error
	for i := 0; i < 12 && lastErr == nil; i++ {
		_, lastErr = f.p.MatchContacts(ctx, tolu, key2.ID, many)
	}
	if !errors.Is(lastErr, people.ErrLimit) {
		t.Fatalf("the day's limit: %v", lastErr)
	}
}

// Last seen and online (§5.4): contacts by default (the owner's list),
// everyone, nobody; hidden both ways when the viewer hides their own;
// online until the heartbeat stops or the app goes away; a block hides.
func TestPresence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada := f.person("Tolu", "+2348031000051"), f.person("Ada", "+2348031000052")
	if err := f.p.Touch(ctx, ada); err != nil {
		t.Fatal(err)
	}
	if pr, _ := f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil || pr.Online != nil {
		t.Fatalf("a stranger: %+v", pr)
	}
	f.add(ada, tolu)
	// Tolu's own is for his contacts (the default), and he has not added
	// Ada: hidden both ways.
	if pr, _ := f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil {
		t.Fatalf("Tolu hides his from Ada: %+v", pr)
	}
	f.add(tolu, ada)
	pr, err := f.p.PresenceOf(ctx, tolu, ada)
	if err != nil || pr.LastSeenAt == nil || !pr.LastSeenAt.Equal(f.now) || pr.Online == nil || !*pr.Online {
		t.Fatalf("a contact of Ada's: %+v %v", pr, err)
	}
	// Online only while the heartbeat goes on (the database's own clock),
	// or until the app goes away.
	if err := f.p.Away(ctx, ada); err != nil {
		t.Fatal(err)
	}
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.Online == nil || *pr.Online || pr.LastSeenAt == nil {
		t.Fatalf("away: %+v", pr)
	}
	// Tolu hides his own: he sees Ada's no more (both ways).
	f.set(tolu, people.SettingsIn{LastSeen: aud(people.Nobody)})
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil || pr.Online != nil {
		t.Fatalf("hidden both ways: %+v", pr)
	}
	if pr, _ = f.p.PresenceOf(ctx, ada, tolu); pr.LastSeenAt != nil {
		t.Fatalf("Tolu's own, hidden: %+v", pr)
	}
	// Contacts (his own list): he must have added Ada for her to see his.
	f.set(tolu, people.SettingsIn{LastSeen: aud(people.Contacts)})
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt == nil {
		t.Fatalf("contacts both ways: %+v", pr)
	}
	if err := f.p.RemoveContact(ctx, tolu, ada); err != nil {
		t.Fatal(err)
	}
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil {
		t.Fatalf("Tolu removed Ada, so he does not see hers either: %+v", pr)
	}
	f.set(tolu, people.SettingsIn{LastSeen: aud(people.Everyone)})
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt == nil {
		t.Fatalf("everyone: %+v", pr)
	}
	f.set(ada, people.SettingsIn{LastSeen: aud(people.Nobody)})
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil || pr.Online != nil {
		t.Fatalf("nobody: %+v", pr)
	}
	f.set(ada, people.SettingsIn{LastSeen: aud(people.Everyone)})
	f.blocks[pair(tolu, ada)] = true
	if pr, _ = f.p.PresenceOf(ctx, tolu, ada); pr.LastSeenAt != nil {
		t.Fatalf("blocked: %+v", pr)
	}
	// One's own, always.
	if pr, _ = f.p.PresenceOf(ctx, ada, ada); pr.LastSeenAt == nil {
		t.Fatalf("one's own: %+v", pr)
	}
}

// MayMessage, NumberShownTo and ReadTicks follow the settings, contacts
// and blocks.
func TestPolicy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada := f.person("Tolu", "+2348031000061"), f.person("Ada", "+2348031000062")
	d, err := f.p.MayMessage(ctx, tolu, ada)
	if err != nil || !d.Allowed || !d.AsRequest {
		t.Fatalf("a stranger, everyone by default: %+v %v", d, err)
	}
	f.add(ada, tolu)
	if d, _ = f.p.MayMessage(ctx, tolu, ada); !d.Allowed || d.AsRequest {
		t.Fatalf("Ada added Tolu: %+v", d)
	}
	if d, _ = f.p.MayMessage(ctx, ada, tolu); !d.Allowed || !d.AsRequest {
		t.Fatalf("Tolu did not add Ada (one way): %+v", d)
	}
	f.set(tolu, people.SettingsIn{Message: aud(people.Contacts)})
	if d, _ = f.p.MayMessage(ctx, ada, tolu); d.Allowed || d.Refused != people.RefusedContacts {
		t.Fatalf("contacts only: %+v", d)
	}
	f.set(ada, people.SettingsIn{Message: aud(people.Nobody)})
	if d, _ = f.p.MayMessage(ctx, tolu, ada); d.Allowed || d.Refused != people.RefusedNobody {
		t.Fatalf("nobody: %+v", d)
	}
	if d, _ = f.p.MayMessage(ctx, tolu, tolu); d.Allowed || d.Refused != people.RefusedSelf {
		t.Fatalf("oneself: %+v", d)
	}
	if d, _ = f.p.MayMessage(ctx, tolu, uuid.Must(uuid.NewV7())); d.Allowed || d.Refused != people.RefusedGone {
		t.Fatalf("no one: %+v", d)
	}
	f.set(ada, people.SettingsIn{Message: aud(people.Everyone)})
	f.blocks[pair(tolu, ada)] = true
	if d, _ = f.p.MayMessage(ctx, tolu, ada); d.Allowed || d.Refused != people.RefusedBlocked {
		t.Fatalf("blocked: %+v", d)
	}
	delete(f.blocks, pair(tolu, ada))

	// The number: hidden by default; shown, once chosen, to contacts and
	// people chatted with; never across a block.
	if n, _ := f.p.NumberShownTo(ctx, ada, tolu); n != "" {
		t.Fatalf("hidden by default: %q", n)
	}
	f.set(ada, people.SettingsIn{ShowNumber: yes(true)})
	if n, _ := f.p.NumberShownTo(ctx, ada, tolu); n != "+2348031000062" {
		t.Fatalf("shown to a contact: %q", n)
	}
	kemi := f.person("Kemi", "+2348031000063")
	if n, _ := f.p.NumberShownTo(ctx, ada, kemi); n != "" {
		t.Fatalf("a stranger: %q", n)
	}
	f.chats[pair(ada, kemi)] = true
	if n, _ := f.p.NumberShownTo(ctx, ada, kemi); n != "+2348031000062" {
		t.Fatalf("someone she chats with: %q", n)
	}
	f.blocks[pair(ada, kemi)] = true
	if n, _ := f.p.NumberShownTo(ctx, ada, kemi); n != "" {
		t.Fatalf("blocked: %q", n)
	}

	// Read ticks: both on.
	if on, _ := f.p.ReadTicks(ctx, tolu, ada); !on {
		t.Fatal("on by default")
	}
	f.set(tolu, people.SettingsIn{ReadTicks: yes(false)})
	if on, _ := f.p.ReadTicks(ctx, tolu, ada); on {
		t.Fatal("Tolu turned them off")
	}
	if on, _ := f.p.ReadTicks(ctx, ada, tolu); on {
		t.Fatal("so he sees no one's")
	}
}

// The checks on strangers' messages: ok, warn and hold, by the transfer
// warning's rule and the scam patterns; only warn and hold are kept, as a
// verdict with its reasons and never the words; a sender warned three
// times in a day is held.
func TestScreenStranger(t *testing.T) {
	for _, c := range []struct {
		text, verdict string
	}{
		{"Hello, are you coming to the party on Saturday?", people.VerdictOK},
		{"Call me on 08031234567 when you are free", people.VerdictOK},
		{"I paid ₦3,000 for the cake, here is the receipt", people.VerdictOK},
		{"Pay into my account 0123456789 Access bank", people.VerdictWarn},
		{"Please send money to this account so I can buy fuel", people.VerdictWarn},
		{"Congratulations! You have won a brand new car. Claim your prize now", people.VerdictWarn},
		{"You have won ₦500,000! Pay the processing fee to 0123456789 GTBank to claim", people.VerdictHold},
		{"Please send me the code you received, it was sent by mistake", people.VerdictHold},
		{"What is your BVN?", people.VerdictHold},
		{"Double your money in 7 days, guaranteed returns. Visit bit.ly/xx", people.VerdictHold},
		{"You won the lucky draw, open www.claim-now.xyz", people.VerdictHold},
	} {
		if v := people.Screen(c.text); v.Verdict != c.verdict {
			t.Errorf("%q: %+v, want %s", c.text, v, c.verdict)
		}
	}
	if people.AsksForTransfer("my number is 08031234567, bank later") {
		t.Error("a phone number is not an account number")
	}

	f := newFixture(t)
	ctx := context.Background()
	tolu, ada := f.person("Tolu", "+2348031000071"), f.person("Ada", "+2348031000072")
	const secret = "Please send money to this account, 0123456789 Kuda"
	if v, err := f.p.ScreenStranger(ctx, tolu, ada, "Hi Ada, it's Tolu from school"); err != nil || v.Verdict != people.VerdictOK {
		t.Fatalf("ok: %+v %v", v, err)
	}
	for i := 0; i < people.RepeatHold; i++ {
		if v, _ := f.p.ScreenStranger(ctx, tolu, ada, secret); v.Verdict != people.VerdictWarn {
			t.Fatalf("warn %d: %+v", i, v)
		}
	}
	v, _ := f.p.ScreenStranger(ctx, tolu, ada, secret)
	if v.Verdict != people.VerdictHold || v.Reasons[len(v.Reasons)-1] != people.ReasonRepeat {
		t.Fatalf("held after three: %+v", v)
	}
	warn, hold, err := f.p.VerdictsSince(ctx, tolu, f.now.Add(-time.Hour))
	if err != nil || warn != 3 || hold != 1 {
		t.Fatalf("kept: %d %d %v", warn, hold, err)
	}
	// Nothing of the words anywhere.
	var n int
	if err := f.d.Admin.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts v WHERE v::text ILIKE '%0123456789%' OR v::text ILIKE '%send money%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the words kept: %d %v", n, err)
	}
	// Ada sees none of Tolu's verdicts.
	if err := db.InPersonTx(ctx, f.d.App, ada, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("Ada reads them: %d %v", n, err)
	}
}

// Deleting an account (§14): the person leaves others' contacts, and
// their settings, contacts, link, presence, counts and verdicts go.
func TestForget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tolu, ada, bola := f.person("Tolu", "+2348031000081"), f.person("Ada", "+2348031000082"), f.person("Bola", "+2348031000083")
	f.add(ada, tolu)
	f.add(bola, tolu)
	f.add(tolu, ada)
	f.set(tolu, people.SettingsIn{ShowNumber: yes(true)})
	link, _ := f.p.LinkOf(ctx, tolu)
	_ = f.p.Touch(ctx, tolu)
	_, _ = f.p.FindByNumber(ctx, tolu, "+2348031000082")
	_, _ = f.p.ScreenStranger(ctx, tolu, bola, "What is your BVN?")
	_, _ = f.p.ScreenStranger(ctx, bola, tolu, "What is your BVN?")
	var left int64
	if err := db.InPersonTx(ctx, f.d.App, tolu, func(tx pgx.Tx) error {
		var err error
		left, err = people.ForgetTx(ctx, tx, tolu)
		return err
	}); err != nil || left != 2 {
		t.Fatalf("forget: %d %v", left, err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM person_contacts WHERE person_id = $1 OR contact_id = $1`,
		`SELECT count(*) FROM person_privacy WHERE person_id = $1`,
		`SELECT count(*) FROM person_links WHERE person_id = $1`,
		`SELECT count(*) FROM person_presence WHERE person_id = $1`,
		`SELECT count(*) FROM people_find_counts WHERE person_id = $1`,
		`SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1 OR to_person = $1`,
	} {
		var n int
		if err := f.d.Admin.QueryRow(ctx, q, tolu).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d %v", q, n, err)
		}
	}
	var n int
	_ = f.d.Admin.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1`, bola).Scan(&n)
	if n != 1 {
		t.Fatalf("Bola's verdict stays, naming no one: %d", n)
	}
	if _, err := f.p.Resolve(ctx, link.Code); !errors.Is(err, people.ErrLinkNotFound) {
		t.Fatalf("the link: %v", err)
	}
	// Another person cannot forget Tolu.
	if err := db.InPersonTx(ctx, f.d.App, ada, func(tx pgx.Tx) error {
		_, err := people.ForgetTx(ctx, tx, tolu)
		return err
	}); err == nil {
		t.Fatal("Ada forgot Tolu")
	}
}

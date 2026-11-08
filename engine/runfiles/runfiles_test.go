package runfiles_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
)

// ogg is an Ogg Opus stream whose last page ends at the given length.
func ogg(d time.Duration) []byte {
	head := []byte("OpusHead\x01\x01\x38\x01\x80\xbb\x00\x00\x00\x00\x00")
	page := func(granule uint64, payload []byte) []byte {
		p := make([]byte, 27)
		copy(p, "OggS")
		binary.LittleEndian.PutUint64(p[6:], granule)
		p[26] = 1
		p = append(p, byte(len(payload)))
		return append(p, payload...)
	}
	out := page(0, head)
	return append(out, page(uint64(d.Seconds()*48000)+312, []byte("audio"))...)
}

func setup(t *testing.T) (*runfiles.Service, *runfilestest.Store, runfiles.Owner, *dbtest.DB) {
	t.Helper()
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	st := runfilestest.New()
	return &runfiles.Service{Pool: d.App, Store: st}, st, runfiles.Owner{Tenant: tn.ID, Environment: "prod"}, d
}

func TestPutReadLinkDelete(t *testing.T) {
	s, st, o, _ := setup(t)
	ctx := context.Background()
	data := ogg(20 * time.Second)
	ref, err := s.Put(ctx, o, "../voice/note.ogg", data, true)
	if err != nil {
		t.Fatal(err)
	}
	// A person's file is kept under a neutral name, never what they called it.
	if ref.Name != "voice-note.ogg" || ref.Type != "audio/ogg" || ref.Size != int64(len(data)) || len(ref.SHA256) != 64 {
		t.Fatalf("ref %+v", ref)
	}
	if st.Scanned() != 1 {
		t.Fatalf("a person's file was scanned %d times", st.Scanned())
	}
	m := ref.Map()
	if m["file"] != ref.ID.String() || m["name"] != "voice-note.ogg" {
		t.Fatalf("map %v", m)
	}
	got, back, err := s.For(o).Read(ctx, m)
	if err != nil || !bytes.Equal(got, data) || back["sha256"] != ref.SHA256 {
		t.Fatalf("read: %v %v", err, back)
	}
	url, exp, _, err := s.Link(ctx, o.Tenant, ref.ID.String(), 2*time.Hour)
	if err != nil || !strings.HasPrefix(url, "https://storage.test/") || time.Until(exp) > 16*time.Minute {
		t.Fatalf("link %q %v %v: a link longer than an hour is cut to 15 minutes", url, exp, err)
	}
	if err := s.Delete(ctx, o.Tenant, ref.ID.String()); err != nil {
		t.Fatal(err)
	}
	if st.Count() != 0 {
		t.Fatal("the stored object outlived its file")
	}
	if _, _, err := s.Read(ctx, o.Tenant, ref.ID.String()); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("read after delete: %v", err)
	}
	if err := s.Delete(ctx, o.Tenant, ref.ID.String()); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	s, st, o, _ := setup(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		data     []byte
		personal bool
		prep     func()
	}{
		"empty":        {data: nil},
		"too long":     {data: ogg(6 * time.Minute)},
		"not accepted": {data: []byte("MZ\x90\x00an executable")},
		"malware":      {data: ogg(5 * time.Second), personal: true, prep: func() { st.Threat = true }},
		"no scanner":   {data: ogg(5 * time.Second), personal: true, prep: func() { st.Threat = false; st.NoScanner = true }},
		"over 25 MB":   {data: append([]byte("%PDF"), make([]byte, media.MaxFileBytes)...)},
	} {
		if c.prep != nil {
			c.prep()
		}
		if _, err := s.Put(ctx, o, "f", c.data, c.personal); !errors.Is(err, runfiles.ErrRefused) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	if st.Count() != 0 {
		t.Fatal("a refused file was stored")
	}
	// A file made by a step (not a person's) is not scanned.
	st.NoScanner = true
	if _, err := s.Put(ctx, o, "reply.ogg", ogg(3*time.Second), false); err != nil {
		t.Fatalf("a step's own file: %v", err)
	}
}

func TestOtherTenantsFilesAreNotFound(t *testing.T) {
	s, _, o, d := setup(t)
	ctx := context.Background()
	ref, err := s.Put(ctx, o, "a.pdf", []byte("%PDF-1.4 x"), false)
	if err != nil {
		t.Fatal(err)
	}
	other := d.SeedTenant(t, nil)
	if _, _, err := s.Read(ctx, other.ID, ref.ID.String()); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("another tenant read the file: %v", err)
	}
	if _, _, _, err := s.Link(ctx, other.ID, ref.ID.String(), time.Minute); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("another tenant got a link: %v", err)
	}
	if _, _, err := s.Read(ctx, o.Tenant, "not-an-id"); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("a bad id: %v", err)
	}
}

func TestRetentionAndSweep(t *testing.T) {
	s, st, o, d := setup(t)
	ctx := context.Background()
	s.Retention = func(context.Context, uuid.UUID) (time.Duration, error) { return time.Hour, nil }
	ref, err := s.Put(ctx, o, "a.ogg", ogg(time.Second), false)
	if err != nil {
		t.Fatal(err)
	}
	var ends time.Time
	if err := d.Admin.QueryRow(ctx, `SELECT expires_at FROM run_files WHERE id = $1`, ref.ID).Scan(&ends); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(ends); left > time.Hour || left < 59*time.Minute {
		t.Fatalf("kept for %s, want the tenant's hour", left)
	}
	keep, err := s.Put(ctx, o, "b.ogg", ogg(time.Second), false)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("nothing has expired yet: %d %v", n, err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE run_files SET expires_at = now() - interval '1 second' WHERE id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	// An expired file is gone at once, before the sweep runs.
	if _, _, err := s.Read(ctx, o.Tenant, ref.ID.String()); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("an expired file was read: %v", err)
	}
	// Storage down: the file waits for the next sweep.
	st.FailDelete = true
	if n, _ := s.Sweep(ctx); n != 0 {
		t.Fatalf("deleted %d with storage down", n)
	}
	st.FailDelete = false
	if n, err := s.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	if st.Count() != 1 {
		t.Fatalf("%d objects left, want the unexpired one", st.Count())
	}
	if _, _, err := s.Read(ctx, o.Tenant, keep.ID.String()); err != nil {
		t.Fatalf("the unexpired file: %v", err)
	}
}

func TestName(t *testing.T) {
	for in, want := range map[string]string{
		"note.ogg": "note.ogg", "a/b/c.pdf": "c.pdf", `C:\x\y.pdf`: "y.pdf", "": "file", "..": "file",
		"bad\x00\nname\".pdf": "badname.pdf", strings.Repeat("é", 300): strings.Repeat("é", 200),
	} {
		if got := runfiles.Name(in); got != want {
			t.Errorf("runfiles.Name(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDailyAllowance(t *testing.T) {
	s, st, o, _ := setup(t)
	ctx := context.Background()
	one := ogg(time.Second)
	s.DailyBytes = int64(len(one)*2 + 1)
	for i := 0; i < 2; i++ {
		if _, err := s.Put(ctx, o, "a.ogg", one, false); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Put(ctx, o, "a.ogg", one, false)
	if !errors.Is(err, runfiles.ErrOverAllowance) || !errors.Is(err, runfiles.ErrRefused) {
		t.Fatalf("the third file: %v", err)
	}
	if st.Count() != 2 {
		t.Fatalf("%d kept", st.Count())
	}
}

func TestErasingAPersonDeletesTheirFiles(t *testing.T) {
	s, st, o, _ := setup(t)
	ctx := context.Background()
	subjects := map[string]string{}
	s.Subject = func(_ context.Context, _ uuid.UUID, category, value string) (string, error) {
		subjects[category+":"+value] = "subj-" + value
		return "subj-" + value, nil
	}
	ada, err := s.Put(ctx, o, "a.ogg", ogg(time.Second), true, runfiles.About{Category: "phone", Value: "2348031234567"})
	if err != nil {
		t.Fatal(err)
	}
	bola, err := s.Put(ctx, o, "b.ogg", ogg(time.Second), true, runfiles.About{Category: "phone", Value: "2348030000004"})
	if err != nil {
		t.Fatal(err)
	}
	if subjects["phone:2348031234567"] == "" {
		t.Fatal("the subject was not asked for")
	}
	n, err := s.EraseSubject(ctx, o.Tenant, "subj-2348031234567")
	if err != nil || n != 1 {
		t.Fatalf("erased %d, %v", n, err)
	}
	if _, _, err := s.Read(ctx, o.Tenant, ada.ID.String()); !errors.Is(err, runfiles.ErrNotFound) {
		t.Fatalf("Ada's file outlived her erasure: %v", err)
	}
	if _, _, err := s.Read(ctx, o.Tenant, bola.ID.String()); err != nil {
		t.Fatalf("Bola's file went too: %v", err)
	}
	if st.Count() != 1 {
		t.Fatalf("%d objects left", st.Count())
	}
}

// Package runfiles keeps the files a workflow run fetched or made: a
// customer's voice note a connector downloaded, a reply a speech step
// spoke, a document a step needs to pass on (decision 0062,
// docs/files.md). The content lives in the organisation's own Xenibase
// storage (engine/media, the private bucket); a run carries only a
// reference, {"file": id, "name", "type", "size", "sha256"}, so a run's
// history never holds a file's bytes and stays within its size limits.
//
// Every file has an end. A file is kept for the business's file retention
// (30 days by default), capped by the plan's run retention, and the sweep
// deletes the stored object and then the row. What a person sent is
// scanned for malware before it is kept and is marked personal.
package runfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/voice"
)

// DefaultRetention is how long a file is kept when nothing says otherwise.
const DefaultRetention = 30 * 24 * time.Hour

// DefaultMaxAudio caps an Ogg recording's length (the ceiling Taskiem's
// chat allows for a voice note): longer speech is refused before it is kept.
const DefaultMaxAudio = 5 * time.Minute

// MaxLinkTTL is the longest a download link lasts.
const MaxLinkTTL = time.Hour

var (
	// ErrNotFound is a file the organisation does not have (or no longer).
	ErrNotFound = errors.New("runfiles: no such file")
	// ErrRefused wraps every file that cannot be kept: its type, size or
	// length, or a failed malware check. Retrying cannot help.
	ErrRefused = errors.New("runfiles: the file cannot be kept")
	// ErrUnavailable says the deployment keeps no run files.
	ErrUnavailable = errors.New("runfiles: files are not available on this deployment")
)

// Store is the organisation's storage: *media.Service.
type Store interface {
	PutPrivate(ctx context.Context, tenant uuid.UUID, prefix string, data []byte) (media.File, error)
	ScanChatFile(ctx context.Context, data []byte) error
	GetFile(ctx context.Context, tenant uuid.UUID, path string) ([]byte, string, error)
	SignURL(ctx context.Context, tenant uuid.UUID, path string, ttl time.Duration) (string, time.Time, error)
	Delete(ctx context.Context, tenant uuid.UUID, bucket string, paths []string) error
}

// Service keeps run files.
type Service struct {
	Pool  *pgxpool.Pool
	Store Store
	// Retention is how long a new file of the tenant is kept (the plan's
	// cap already applied); nil keeps DefaultRetention.
	Retention func(ctx context.Context, tenant uuid.UUID) (time.Duration, error)
	// MaxAudio caps an Ogg recording's length; 0 is DefaultMaxAudio.
	MaxAudio time.Duration
	// DailyBytes caps what one organisation's runs may keep in 24 hours,
	// so one busy or abusive sender cannot fill its storage; 0 is
	// DefaultDailyBytes.
	DailyBytes int64
	// Subject finds the vault's subject id for a person (category and
	// value), as erasure names them (secrets.Vault.SubjectFor); nil keeps
	// files without one, so they are not erased with the person.
	Subject func(ctx context.Context, tenant uuid.UUID, category, value string) (string, error)
	Now     func() time.Time
	Logger  *slog.Logger
}

// DefaultDailyBytes is what one organisation's runs may keep in 24 hours.
const DefaultDailyBytes = 1 << 30

// About is whom a file is about: a category (phone, email, other) and the
// value, as erasure names a person.
type About struct {
	Category, Value string
}

// Owner is the run a file is kept for.
type Owner struct {
	Tenant      uuid.UUID
	Environment string
	Workflow    *uuid.UUID
	Run         *uuid.UUID
}

// Ref is a file's reference, as a run carries it.
type Ref struct {
	ID     uuid.UUID
	Name   string
	Type   string
	Size   int64
	SHA256 string
}

// Map is the reference as JSON: {"file", "name", "type", "size", "sha256"}.
func (r Ref) Map() map[string]any {
	return map[string]any{"file": r.ID.String(), "name": r.Name, "type": r.Type, "size": r.Size, "sha256": r.SHA256}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Name makes a file name safe to show and to send on: the last path part,
// no control characters, at most 200 characters; "file" when nothing is
// left.
func Name(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	var b strings.Builder
	n := 0
	for _, r := range raw {
		if r == utf8.RuneError || unicode.IsControl(r) || r == '"' {
			continue
		}
		if n == 200 {
			break
		}
		b.WriteRune(r)
		n++
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "file"
	}
	return out
}

// neutralName is the name a person's file is kept under: its kind and
// extension, never what they called it (a document's name can be personal
// data, and the name is kept in the clear in run_files).
func neutralName(name, ctype string) string {
	ext := ""
	if i := strings.LastIndex(name, "."); i > 0 && i >= len(name)-6 {
		ext = strings.ToLower(name[i:])
	}
	switch {
	case strings.HasPrefix(ctype, "audio/"):
		return "voice-note" + ext
	case strings.HasPrefix(ctype, "image/"):
		return "photo" + ext
	}
	return "document" + ext
}

// Put checks data and keeps it for the run. A person's file is scanned
// first, kept under a neutral name, and tied to whom it is about, so
// erasing them deletes it; an Ogg recording longer than MaxAudio, or a file
// past the organisation's daily allowance, is refused.
func (s *Service) Put(ctx context.Context, o Owner, name string, data []byte, personal bool, about ...About) (Ref, error) {
	origin := "made"
	if personal {
		origin = "person"
	}
	ref, err := s.put(ctx, o, name, data, personal, about)
	switch {
	case err == nil:
		telemetry.RunFilesKept.WithLabelValues(origin, "kept").Inc()
	case errors.Is(err, ErrOverAllowance):
		telemetry.RunFilesKept.WithLabelValues(origin, "over_allowance").Inc()
	case errors.Is(err, ErrRefused):
		telemetry.RunFilesKept.WithLabelValues(origin, "refused").Inc()
	}
	return ref, err
}

// ErrOverAllowance is a file past the organisation's daily allowance; it
// wraps ErrRefused.
var ErrOverAllowance = fmt.Errorf("%w: today's allowance for files is used up", ErrRefused)

func (s *Service) put(ctx context.Context, o Owner, name string, data []byte, personal bool, about []About) (Ref, error) {
	if s == nil || s.Store == nil || s.Pool == nil {
		return Ref{}, ErrUnavailable
	}
	if len(data) == 0 {
		return Ref{}, fmt.Errorf("%w: it is empty", ErrRefused)
	}
	if len(data) > media.MaxFileBytes {
		return Ref{}, fmt.Errorf("%w: larger than %d MB", ErrRefused, media.MaxFileBytes>>20)
	}
	if d, ok := voice.OggDuration(data); ok {
		max := s.MaxAudio
		if max <= 0 {
			max = DefaultMaxAudio
		}
		if d > max {
			return Ref{}, fmt.Errorf("%w: a recording of %s, longer than %s", ErrRefused, d.Round(time.Second), max)
		}
	}
	daily := s.DailyBytes
	if daily <= 0 {
		daily = DefaultDailyBytes
	}
	var used int64
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{o.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(size), 0) FROM run_files WHERE tenant_id = $1 AND created_at > now() - interval '24 hours'`, o.Tenant).Scan(&used)
	}); err != nil {
		return Ref{}, err
	}
	if used+int64(len(data)) > daily {
		return Ref{}, fmt.Errorf("%w (%d MB a day)", ErrOverAllowance, daily>>20)
	}
	if personal {
		start := time.Now()
		err := s.Store.ScanChatFile(ctx, data)
		telemetry.FileScanSeconds.Observe(time.Since(start).Seconds())
		if err != nil {
			return Ref{}, fmt.Errorf("%w: %w", ErrRefused, err)
		}
	}
	var subject *string
	if personal && s.Subject != nil {
		for _, a := range about {
			if strings.TrimSpace(a.Value) == "" {
				continue
			}
			id, err := s.Subject(ctx, o.Tenant, a.Category, strings.TrimSpace(a.Value))
			if err != nil {
				return Ref{}, err
			}
			subject = &id
			break
		}
	}
	keep := DefaultRetention
	if s.Retention != nil {
		d, err := s.Retention(ctx, o.Tenant)
		if err != nil {
			return Ref{}, err
		}
		if d > 0 {
			keep = d
		}
	}
	f, err := s.Store.PutPrivate(ctx, o.Tenant, "runs/", data)
	if err != nil {
		if errors.Is(err, media.ErrFile) {
			return Ref{}, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		return Ref{}, err
	}
	// What was stored may differ from what was given (tags stripped, a
	// photo re-encoded): the reference describes what is kept.
	stored := data
	if f.Size != int64(len(data)) {
		if b, _, err := s.Store.GetFile(ctx, o.Tenant, f.Path); err == nil {
			stored = b
		}
	}
	sum := sha256.Sum256(stored)
	kept := Name(name)
	if personal {
		kept = neutralName(kept, f.Type)
	}
	ref := Ref{ID: uuid.Must(uuid.NewV7()), Name: kept, Type: f.Type, Size: int64(len(stored)), SHA256: hex.EncodeToString(sum[:])}
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{o.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO run_files (id, tenant_id, environment, workflow_id, run_id, name, type, size, sha256, storage_key, personal, expires_at, subject)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			ref.ID, o.Tenant, o.Environment, o.Workflow, o.Run, ref.Name, ref.Type, ref.Size, sum[:], f.Path, personal, s.now().Add(keep), subject)
		return err
	})
	if err != nil {
		// The object would be kept with no row to end it: remove it.
		if derr := s.Store.Delete(context.WithoutCancel(ctx), o.Tenant, media.PrivateBucket, []string{f.Path}); derr != nil {
			s.logger().Warn("runfiles: an object kept without its row", "tenant", o.Tenant, "error", derr)
		}
		return Ref{}, err
	}
	return ref, nil
}

type row struct {
	ref  Ref
	key  string
	sum  []byte
	ends time.Time
}

func (s *Service) find(ctx context.Context, tenant uuid.UUID, id string) (row, error) {
	if s == nil || s.Store == nil || s.Pool == nil {
		return row{}, ErrUnavailable
	}
	fid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return row{}, ErrNotFound
	}
	var r row
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, type, size, sha256, storage_key, expires_at FROM run_files WHERE id = $1`, fid).
			Scan(&r.ref.ID, &r.ref.Name, &r.ref.Type, &r.ref.Size, &r.sum, &r.key, &r.ends)
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !r.ends.After(s.now())) {
		return row{}, ErrNotFound
	}
	if err != nil {
		return row{}, err
	}
	r.ref.SHA256 = hex.EncodeToString(r.sum)
	return r, nil
}

// Read answers a file's bytes, checked against the hash it was kept with.
func (s *Service) Read(ctx context.Context, tenant uuid.UUID, id string) ([]byte, Ref, error) {
	r, err := s.find(ctx, tenant, id)
	if err != nil {
		return nil, Ref{}, err
	}
	b, _, err := s.Store.GetFile(ctx, tenant, r.key)
	if err != nil {
		return nil, Ref{}, err
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != r.ref.SHA256 {
		return nil, Ref{}, fmt.Errorf("runfiles: file %s does not match what was kept", r.ref.ID)
	}
	return b, r.ref, nil
}

// Link is a download link for a file, at most MaxLinkTTL long.
func (s *Service) Link(ctx context.Context, tenant uuid.UUID, id string, ttl time.Duration) (string, time.Time, Ref, error) {
	r, err := s.find(ctx, tenant, id)
	if err != nil {
		return "", time.Time{}, Ref{}, err
	}
	if ttl <= 0 || ttl > MaxLinkTTL {
		ttl = 15 * time.Minute
	}
	url, exp, err := s.Store.SignURL(ctx, tenant, r.key, ttl)
	if err != nil {
		return "", time.Time{}, Ref{}, err
	}
	return url, exp, r.ref, nil
}

// Delete removes a file now: the stored object, then its row. Deleting a
// file that is already gone is not an error.
func (s *Service) Delete(ctx context.Context, tenant uuid.UUID, id string) error {
	r, err := s.find(ctx, tenant, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.remove(ctx, tenant, []uuid.UUID{r.ref.ID}, []string{r.key})
}

func (s *Service) remove(ctx context.Context, tenant uuid.UUID, ids []uuid.UUID, keys []string) error {
	if err := s.Store.Delete(ctx, tenant, media.PrivateBucket, keys); err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM run_files WHERE id = ANY($1)`, ids)
		return err
	})
}

// EraseSubject deletes every file about a person, now (the subject the
// vault erased: erasure, spec 9.4). It answers how many it deleted.
func (s *Service) EraseSubject(ctx context.Context, tenant uuid.UUID, subject string) (int, error) {
	if s == nil || s.Store == nil || s.Pool == nil || subject == "" {
		return 0, nil
	}
	var ids []uuid.UUID
	var keys []string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, storage_key FROM run_files WHERE subject = $1`, subject)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				return err
			}
			ids, keys = append(ids, id), append(keys, key)
		}
		return rows.Err()
	})
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if err := s.remove(ctx, tenant, ids, keys); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// Sweep deletes expired files, a batch at a time, in each tenant's own
// storage; it answers how many it deleted. A tenant whose storage cannot
// be reached keeps its files until the next sweep.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	if s == nil || s.Store == nil || s.Pool == nil {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, storage_key FROM taskiem_run_files_expired(500)`)
	if err != nil {
		return 0, err
	}
	type batch struct {
		ids  []uuid.UUID
		keys []string
	}
	by := map[uuid.UUID]*batch{}
	var order []uuid.UUID
	for rows.Next() {
		var id, tenant uuid.UUID
		var key string
		if err := rows.Scan(&id, &tenant, &key); err != nil {
			rows.Close()
			return 0, err
		}
		b := by[tenant]
		if b == nil {
			b = &batch{}
			by[tenant] = b
			order = append(order, tenant)
		}
		b.ids = append(b.ids, id)
		b.keys = append(b.keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, t := range order {
		b := by[t]
		if err := s.remove(ctx, t, b.ids, b.keys); err != nil {
			s.logger().Warn("runfiles: expired files not deleted yet", "tenant", t, "files", len(b.ids), "error", err)
			continue
		}
		n += len(b.ids)
	}
	return n, nil
}

// For is the run's file store as a connector sees it (connector.Files).
func (s *Service) For(o Owner) *RunFiles { return &RunFiles{s: s, o: o} }

// RunFiles is one run's view: it keeps files for the run and reads only
// its organisation's.
type RunFiles struct {
	s *Service
	o Owner
}

// Put keeps data for the run.
func (f *RunFiles) Put(ctx context.Context, name string, data []byte, personal bool, about ...connector.FileSubject) (map[string]any, error) {
	ab := make([]About, len(about))
	for i, a := range about {
		ab[i] = About{Category: a.Category, Value: a.Value}
	}
	ref, err := f.s.Put(ctx, f.o, name, data, personal, ab...)
	if err != nil {
		return nil, err
	}
	return ref.Map(), nil
}

// Read answers a file's bytes and reference, given the reference or its id.
func (f *RunFiles) Read(ctx context.Context, ref any) ([]byte, map[string]any, error) {
	id := ""
	switch x := ref.(type) {
	case string:
		id = x
	case map[string]any:
		id, _ = x["file"].(string)
	}
	b, r, err := f.s.Read(ctx, f.o.Tenant, id)
	if err != nil {
		return nil, nil, err
	}
	return b, r.Map(), nil
}

// ActionError says what a connector should report when keeping or reading
// a file failed: a file that cannot be kept or does not exist, or a
// deployment without files, is fatal (retrying cannot help); anything
// else (storage unreachable) is retryable.
func ActionError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound), errors.Is(err, ErrUnavailable):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
}

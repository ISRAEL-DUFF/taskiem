package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Per-use secret.read auditing (spec 14.1, 9.2). The vault records one row
// in secret_reads (migration 00033) for every decryption it performs for
// use, in the transaction that decrypts: no value leaves the vault without
// its record. Rows hold which secret, why, and for which run, step and
// attempt; never the value or a hash of it.
//
// Callers say why they decrypt by attaching a Use to the context. A read
// without one is still recorded, with purpose "unspecified".

// Kinds of secret a read is of.
const (
	KindSecret       = "secret"        // a tenant secret a workflow names
	KindConnection   = "connection"    // a connection's credentials
	KindWebhook      = "webhook"       // a webhook trigger's verification key
	KindGit          = "git"           // Git sync's credentials or webhook secret
	KindAlertChannel = "alert_channel" // an alert channel's webhook URL or signing key
	KindIdentity     = "identity"      // SSO client secrets and TOTP seeds
)

// Purposes recorded by the platform's own read paths.
const (
	PurposeIngestVerify = "ingest.verify"
	PurposeGitSync      = "git.sync"
	PurposeAlertDeliver = "alert.deliver"
	// PurposeRemoteRegister: creating, updating, checking or deleting a
	// trigger's subscription at its provider (decision 0021).
	PurposeRemoteRegister = "remote.register"
	// PurposeIngestEnrich: completing a verified delivery at the provider
	// before its runs start (a truncated event's row).
	PurposeIngestEnrich = "ingest.enrich"
	PurposeUnspecified  = "unspecified"
)

// Use says why a secret is decrypted; the vault records it with the read.
type Use struct {
	Kind    string // default: "secret" for Get, "connection" for Credentials
	Purpose string
	RunID   uuid.UUID
	StepID  string
	Attempt int
	Actor   string // default "system"
}

type useKey struct{}

// WithUse attaches u to ctx: reads under ctx are recorded with it.
func WithUse(ctx context.Context, u Use) context.Context { return context.WithValue(ctx, useKey{}, u) }

func useFrom(ctx context.Context, kind string) Use {
	u, _ := ctx.Value(useKey{}).(Use)
	if u.Kind == "" {
		u.Kind = kind
	}
	if u.Purpose == "" {
		u.Purpose = PurposeUnspecified
	}
	if u.Actor == "" {
		u.Actor = "system"
	}
	return u
}

// record writes one read in the decrypting transaction.
func record(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, u Use, env, name string, connID *uuid.UUID, connector string) error {
	var run *uuid.UUID
	var step *string
	var attempt *int
	if u.RunID != uuid.Nil {
		run = &u.RunID
		step = &u.StepID
		attempt = &u.Attempt
	}
	var conn *string
	if connID != nil {
		conn = &connector
	}
	_, err := tx.Exec(ctx, `INSERT INTO secret_reads (id, tenant_id, kind, environment, name, connection_id, connector, purpose, run_id, step_id, attempt, actor)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uuid.Must(uuid.NewV7()), tenant, u.Kind, env, name, connID, conn, u.Purpose, run, step, attempt, u.Actor)
	if err != nil {
		return fmt.Errorf("record secret read: %w", err)
	}
	return nil
}

// Exists reports whether a named secret is set, without decrypting it (so
// without a read).
func (v *Vault) Exists(ctx context.Context, tenant uuid.UUID, env, name string) (bool, error) {
	var ok bool
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3)`, tenant, env, name).Scan(&ok)
	})
	return ok, err
}

// Read is one recorded decryption.
type Read struct {
	ID           uuid.UUID  `json:"id"`
	At           time.Time  `json:"at"`
	Kind         string     `json:"kind"`
	Environment  string     `json:"environment"`
	Name         string     `json:"name"`
	ConnectionID *uuid.UUID `json:"connection_id,omitempty"`
	Connector    *string    `json:"connector,omitempty"`
	Purpose      string     `json:"purpose"`
	RunID        *uuid.UUID `json:"run_id,omitempty"`
	StepID       *string    `json:"step_id,omitempty"`
	Attempt      *int       `json:"attempt,omitempty"`
	Actor        string     `json:"actor"`
}

// ReadFilter narrows ListReads; zero fields match everything.
type ReadFilter struct {
	Name        string
	Connection  uuid.UUID
	Run         uuid.UUID
	Environment string
	Kind        string
	Since       time.Time
	Until       time.Time
	Limit       int // default 100, at most 1000
}

const readColumns = `id, at, kind, environment, name, connection_id, connector, purpose, run_id, step_id, attempt, actor`

// ListReads returns reads visible in tx's tenant scope, newest first.
func ListReads(ctx context.Context, tx pgx.Tx, f ReadFilter) ([]Read, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	f.Limit = min(f.Limit, 1000)
	q := `SELECT ` + readColumns + ` FROM secret_reads WHERE true`
	var args []any
	arg := func(cond string, v any) {
		args = append(args, v)
		q += " AND " + cond + " $" + strconv.Itoa(len(args))
	}
	if f.Name != "" {
		arg("connection_id IS NULL AND name =", f.Name)
	}
	if f.Connection != uuid.Nil {
		arg("connection_id =", f.Connection)
	}
	if f.Run != uuid.Nil {
		arg("run_id =", f.Run)
	}
	if f.Environment != "" {
		arg("environment =", f.Environment)
	}
	if f.Kind != "" {
		arg("kind =", f.Kind)
	}
	if !f.Since.IsZero() {
		arg("at >=", f.Since)
	}
	if !f.Until.IsZero() {
		arg("at <", f.Until)
	}
	args = append(args, f.Limit)
	q += " ORDER BY at DESC, id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Read])
}

// --- hourly digests into the audit chain ---

// DigestAction is the audit action a tenant-hour's digest is recorded under.
const DigestAction = "secret.read.digest"

// SecretCount is how often one secret was read in a digested hour.
type SecretCount struct {
	Kind         string `json:"kind"`
	Environment  string `json:"environment"`
	Name         string `json:"name"`
	ConnectionID string `json:"connection_id,omitempty"`
	Reads        int64  `json:"reads"`
}

// Digest summarises one tenant-hour of reads.
type Digest struct {
	Hour    time.Time     `json:"hour"`
	Reads   int64         `json:"reads"`
	SHA256  string        `json:"sha256"`
	Secrets []SecretCount `json:"secrets"`
	// Truncated is set when more secrets were read than the entry lists;
	// Reads and SHA256 still cover every row.
	Truncated bool `json:"secrets_truncated,omitempty"`
}

// maxDigestSecrets bounds the per-secret counts one audit entry carries.
const maxDigestSecrets = 1000

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// digestHour computes the digest of a tenant's reads in the hour starting
// at hour, from the rows as they are now. The hash covers every column of
// every row, in (at, id) order, one JSON array per row.
func digestHour(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hour time.Time) (Digest, error) {
	hour = hour.UTC()
	d := Digest{Hour: hour}
	rows, err := tx.Query(ctx, `SELECT `+readColumns+` FROM secret_reads WHERE tenant_id = $1 AND at >= $2 AND at < $3 ORDER BY at, id`,
		tenant, hour, hour.Add(time.Hour))
	if err != nil {
		return d, err
	}
	defer rows.Close()
	h := sha256.New()
	h.Write([]byte("taskiem-secret-reads/v1\n" + tenant.String() + "\n" + fmtTime(hour) + "\n"))
	counts := map[SecretCount]int64{}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	for rows.Next() {
		r, err := pgx.RowToStructByPos[Read](rows)
		if err != nil {
			return d, err
		}
		conn, run, attempt := "", "", ""
		if r.ConnectionID != nil {
			conn = r.ConnectionID.String()
		}
		if r.RunID != nil {
			run = r.RunID.String()
		}
		if r.Attempt != nil {
			attempt = strconv.Itoa(*r.Attempt)
		}
		line, _ := json.Marshal([]string{r.ID.String(), fmtTime(r.At), r.Kind, r.Environment, r.Name, conn, str(r.Connector),
			r.Purpose, run, str(r.StepID), attempt, r.Actor})
		h.Write(append(line, '\n'))
		d.Reads++
		counts[SecretCount{Kind: r.Kind, Environment: r.Environment, Name: r.Name, ConnectionID: conn}]++
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	d.SHA256 = hex.EncodeToString(h.Sum(nil))
	for k, n := range counts {
		k.Reads = n
		d.Secrets = append(d.Secrets, k)
	}
	sort.Slice(d.Secrets, func(i, j int) bool {
		a, b := d.Secrets[i], d.Secrets[j]
		if a.Reads != b.Reads {
			return a.Reads > b.Reads
		}
		return a.Kind+"\x00"+a.Environment+"\x00"+a.Name+"\x00"+a.ConnectionID < b.Kind+"\x00"+b.Environment+"\x00"+b.Name+"\x00"+b.ConnectionID
	})
	if len(d.Secrets) > maxDigestSecrets {
		d.Secrets, d.Truncated = d.Secrets[:maxDigestSecrets], true
	}
	return d, nil
}

// ReadDigester is the scheduler's job that digests each closed tenant-hour
// of reads into the tenant's audit chain, and purges reads past retention.
type ReadDigester struct {
	Pool *pgxpool.Pool
	// Grace is how long after an hour ends it is digested, so reads whose
	// transactions were still open at the boundary are in; default 15m.
	Grace time.Duration
	// Window is how far back each tick looks for undigested hours; default
	// 48h. The first tick after start looks back 7 days.
	Window time.Duration
	// Retention keeps rows at least this long (and never less than 400
	// days, which the database enforces); default 400 days.
	Retention time.Duration
	Interval  time.Duration // default 15m
	Logger    *slog.Logger
	Now       func() time.Time

	started bool
}

// DefaultReadRetention is how long secret_reads rows are kept by default.
const DefaultReadRetention = 400 * 24 * time.Hour

// Run digests on a loop until ctx ends.
func (x *ReadDigester) Run(ctx context.Context) error {
	every := x.Interval
	if every <= 0 {
		every = 15 * time.Minute
	}
	for {
		if _, err := x.Tick(ctx); err != nil && ctx.Err() == nil && x.Logger != nil {
			x.Logger.Error("secret read digests", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick digests every due tenant-hour and purges expired rows. It returns
// how many digests it appended.
func (x *ReadDigester) Tick(ctx context.Context) (int, error) {
	now := time.Now
	if x.Now != nil {
		now = x.Now
	}
	grace, window := x.Grace, x.Window
	if grace <= 0 {
		grace = 15 * time.Minute
	}
	if window <= 0 {
		window = 48 * time.Hour
	}
	if !x.started {
		window = max(window, 7*24*time.Hour)
	}
	until := now().UTC().Add(-grace).Truncate(time.Hour)
	rows, err := x.Pool.Query(ctx, `SELECT tenant_id, hour FROM taskiem_secret_read_hours_due($1, $2)`, until.Add(-window), until)
	if err != nil {
		return 0, err
	}
	type due struct {
		Tenant uuid.UUID
		Hour   time.Time
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[due])
	if err != nil {
		return 0, err
	}
	n := 0
	var bad error
	for _, d := range list {
		err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{d.Tenant}, func(tx pgx.Tx) error {
			dg, err := digestHour(ctx, tx, d.Tenant, d.Hour)
			if err != nil {
				return err
			}
			detail, _ := json.Marshal(dg)
			var seq int64
			if err := tx.QueryRow(ctx, `SELECT taskiem_audit_append($1, 'system', 'secret-reads', $2, $3, $4)`,
				d.Tenant, DigestAction, "secret_reads/"+dg.Hour.Format(time.RFC3339), detail).Scan(&seq); err != nil {
				return err
			}
			sum, _ := hex.DecodeString(dg.SHA256)
			_, err = tx.Exec(ctx, `INSERT INTO secret_read_digests (tenant_id, hour, reads, sha256, chain_seq) VALUES ($1, $2, $3, $4, $5)`,
				d.Tenant, dg.Hour, dg.Reads, sum, seq)
			return err
		})
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			n++
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			// Another scheduler digested it first; its transaction, audit
			// entry included, is the one that stands.
		default:
			bad = errors.Join(bad, fmt.Errorf("digest tenant %s hour %s: %w", d.Tenant, d.Hour.UTC().Format(time.RFC3339), err))
		}
	}
	x.started = true
	keep := x.Retention
	if keep <= 0 {
		keep = DefaultReadRetention
	}
	if _, err := x.Pool.Exec(ctx, `SELECT taskiem_secret_reads_purge(make_interval(secs => $1), 10000)`, keep.Seconds()); err != nil {
		bad = errors.Join(bad, fmt.Errorf("purge secret reads: %w", err))
	}
	return n, bad
}

// DigestCheck is one tenant-hour compared with the digest in the chain.
type DigestCheck struct {
	Hour     time.Time `json:"hour"`
	ChainSeq int64     `json:"chain_seq,omitempty"`
	Recorded int64     `json:"recorded_reads"` // in the chain's digest
	Found    int64     `json:"found_reads"`    // in secret_reads now
	// Status: "ok"; "mismatch" (rows changed, removed or added after the
	// digest); "purged" (rows removed by retention, as expected);
	// "undigested" (closed hours the job has not reached).
	Status string `json:"status"`
}

// VerifyDigests recomputes every digest the tenant's chain holds for hours
// in [from, to) and compares it with the rows as they are now. It reads the
// digests from audit_log, not from secret_read_digests, so the chain is the
// reference. Rows that retention has purged report "purged".
func VerifyDigests(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time, retention time.Duration) ([]DigestCheck, error) {
	if retention <= 0 {
		retention = DefaultReadRetention
	}
	rows, err := tx.Query(ctx, `SELECT chain_seq, detail FROM audit_log WHERE tenant_id = $1 AND action = $2
		AND (detail->>'hour')::timestamptz >= $3 AND (detail->>'hour')::timestamptz < $4 ORDER BY chain_seq`, tenant, DigestAction, from, to)
	if err != nil {
		return nil, err
	}
	type entry struct {
		seq int64
		d   Digest
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var raw []byte
		if err := rows.Scan(&e.seq, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &e.d); err != nil {
			rows.Close()
			return nil, fmt.Errorf("digest entry %d: %w", e.seq, err)
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []DigestCheck
	seen := map[time.Time]bool{}
	purgedBefore := time.Now().Add(-retention)
	for _, e := range entries {
		now, err := digestHour(ctx, tx, tenant, e.d.Hour)
		if err != nil {
			return nil, err
		}
		c := DigestCheck{Hour: e.d.Hour.UTC(), ChainSeq: e.seq, Recorded: e.d.Reads, Found: now.Reads, Status: "ok"}
		switch {
		case now.SHA256 == e.d.SHA256 && now.Reads == e.d.Reads:
		case now.Reads == 0 && e.d.Hour.Add(time.Hour).Before(purgedBefore):
			c.Status = "purged"
		default:
			c.Status = "mismatch"
		}
		seen[c.Hour] = true
		out = append(out, c)
	}
	// Closed hours with reads but no digest in the chain.
	hours, err := tx.Query(ctx, `SELECT date_trunc('hour', at, 'UTC') AS h, count(*) FROM secret_reads
		WHERE tenant_id = $1 AND at >= $2 AND at < least($3, date_trunc('hour', now(), 'UTC')) GROUP BY h ORDER BY h`, tenant, from, to)
	if err != nil {
		return nil, err
	}
	defer hours.Close()
	for hours.Next() {
		var h time.Time
		var n int64
		if err := hours.Scan(&h, &n); err != nil {
			return nil, err
		}
		if !seen[h.UTC()] {
			out = append(out, DigestCheck{Hour: h.UTC(), Found: n, Status: "undigested"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hour.Before(out[j].Hour) })
	return out, hours.Err()
}

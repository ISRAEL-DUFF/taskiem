package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Anchoring (spec 9.2): every day each tenant's chain head is signed and
// delivered outside the database. The chain alone shows an edit to any
// row, but someone who can write to the database could recompute every
// hash after the edit; an anchor held elsewhere still disagrees.

// Anchor is a signed chain head.
type Anchor struct {
	TenantID  string `json:"tenant_id"`
	Seq       int64  `json:"seq"`
	Hash      string `json:"hash"`        // hex
	At        string `json:"anchored_at"` // RFC 3339, UTC
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"` // base64 Ed25519 over Message()
}

// Message is what an anchor's signature covers.
func (a Anchor) Message() []byte {
	return []byte("taskiem-audit-anchor/v1\n" + a.TenantID + "\n" + strconv.FormatInt(a.Seq, 10) + "\n" + a.Hash + "\n" + a.At + "\n")
}

// Signer signs anchors with the platform's Ed25519 key.
type Signer struct {
	key   ed25519.PrivateKey
	KeyID string
}

// NewSigner makes a signer from a 32-byte seed.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("audit: anchor key must be a %d-byte seed", ed25519.SeedSize)
	}
	key := ed25519.NewKeyFromSeed(seed)
	return &Signer{key: key, KeyID: KeyID(key.Public().(ed25519.PublicKey))}, nil
}

// KeyID names a public key: the first 8 bytes of its SHA-256, in hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// PublicKey is the key anchors verify with, published to tenants.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.key.Public().(ed25519.PublicKey) }

// Sign fills in the anchor's key and signature.
func (s *Signer) Sign(a *Anchor) {
	a.KeyID = s.KeyID
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(s.key, a.Message()))
}

// VerifySignature checks an anchor's signature with pub.
func (a Anchor) VerifySignature(pub ed25519.PublicKey) bool {
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	return err == nil && a.KeyID == KeyID(pub) && ed25519.Verify(pub, a.Message(), sig)
}

// Anchorer is the daily job that anchors every tenant whose chain moved.
type Anchorer struct {
	Pool   *pgxpool.Pool
	Signer *Signer
	// Dir receives anchors/<tenant>.jsonl, appended to and never rewritten.
	// Point it at write-once storage (an object-lock bucket mounted, or an
	// append-only volume).
	Dir      string
	Interval time.Duration // default 24h
	Logger   *slog.Logger
}

// Run anchors on a loop until ctx ends.
func (x *Anchorer) Run(ctx context.Context) error {
	for {
		if _, err := x.Tick(ctx); err != nil && ctx.Err() == nil && x.Logger != nil {
			x.Logger.Error("audit anchoring", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Hour):
		}
	}
}

// Tick anchors the tenants that are due and returns how many it anchored.
func (x *Anchorer) Tick(ctx context.Context) (int, error) {
	every := x.Interval
	if every <= 0 {
		every = 24 * time.Hour
	}
	rows, err := x.Pool.Query(ctx, `SELECT tenant_id, chain_seq, head_hash FROM taskiem_audit_heads_due($1::interval)`,
		strconv.FormatInt(int64(every/time.Second), 10)+" seconds")
	if err != nil {
		return 0, err
	}
	type due struct {
		tenant uuid.UUID
		seq    int64
		hash   []byte
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.tenant, &d.seq, &d.hash); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		a := Anchor{TenantID: d.tenant.String(), Seq: d.seq, Hash: hex.EncodeToString(d.hash), At: time.Now().UTC().Format(time.RFC3339)}
		x.Signer.Sign(&a)
		// Outside first: an anchor recorded only in the database would prove
		// nothing. A failure after this point anchors the same head again.
		if err := x.deliver(a); err != nil {
			return n, fmt.Errorf("anchor tenant %s: %w", d.tenant, err)
		}
		sig, _ := base64.StdEncoding.DecodeString(a.Signature)
		at, _ := time.Parse(time.RFC3339, a.At)
		err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{d.tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO audit_anchors (tenant_id, chain_seq, head_hash, anchored_at, key_id, signature) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT DO NOTHING`, d.tenant, d.seq, d.hash, at, a.KeyID, sig)
			return err
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (x *Anchorer) deliver(a Anchor) error {
	if x.Dir == "" {
		return errors.New("no anchor directory configured")
	}
	if err := os.MkdirAll(x.Dir, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(x.Dir, a.TenantID+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640) //nolint:gosec // our own anchor file
	if err != nil {
		return err
	}
	line, _ := json.Marshal(a)
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ListAnchors returns a tenant's anchors, oldest first, inside its
// transaction.
func ListAnchors(ctx context.Context, tx pgx.Tx) ([]Anchor, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id::text, chain_seq, encode(head_hash, 'hex'), to_char(anchored_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		key_id, translate(encode(signature, 'base64'), E'\n', '') FROM audit_anchors ORDER BY chain_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Anchor
	for rows.Next() {
		var a Anchor
		if err := rows.Scan(&a.TenantID, &a.Seq, &a.Hash, &a.At, &a.KeyID, &a.Signature); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReadAnchors reads anchors as JSON lines.
func ReadAnchors(r io.Reader) ([]Anchor, error) {
	dec := json.NewDecoder(r)
	var out []Anchor
	for {
		var a Anchor
		err := dec.Decode(&a)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("audit: bad anchor: %w", err)
		}
		out = append(out, a)
	}
}

// VerifyWithAnchors verifies an export as Verify does, then checks each
// anchor: its signature (when pub is given), and that the export's entry at
// the anchored sequence number has the anchored hash.
func VerifyWithAnchors(r io.Reader, anchors []Anchor, pub ed25519.PublicKey) (Result, error) {
	want := map[int64]string{}
	for _, a := range anchors {
		want[a.Seq] = a.Hash
	}
	got := map[int64]string{}
	res, err := verify(r, func(seq int64, hash string) {
		if _, ok := want[seq]; ok {
			got[seq] = hash
		}
	})
	if err != nil || res.FirstBroken != 0 {
		return res, err
	}
	for _, a := range anchors {
		switch {
		case pub != nil && !a.VerifySignature(pub):
			res.FirstBroken, res.Reason = a.Seq, fmt.Sprintf("the anchor at entry %d (%s) is not signed by this key", a.Seq, a.At)
		case got[a.Seq] == "":
			res.FirstBroken, res.Reason = a.Seq, fmt.Sprintf("entry %d, anchored %s, is missing from the export", a.Seq, a.At)
		case got[a.Seq] != a.Hash:
			res.FirstBroken, res.Reason = a.Seq, fmt.Sprintf("entry %d differs from the anchor made %s: the chain was rewritten after it", a.Seq, a.At)
		default:
			res.Anchors++
			continue
		}
		return res, nil
	}
	return res, nil
}

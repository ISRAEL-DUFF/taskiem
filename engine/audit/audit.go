// Package audit verifies a tenant's hash-chained audit log independently of
// the database (spec 9.2, gate G1): it recomputes every hash from an export,
// reproducing taskiem_audit_canonical (migration 00005) byte for byte.
package audit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// Entry is one exported audit row. Detail is the jsonb value's text form
// exactly as Postgres prints it; At is already in the canonical format.
type Entry struct {
	TenantID  string `json:"tenant_id"`
	Seq       int64  `json:"seq"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	At        string `json:"at"`
	PrevHash  string `json:"prev_hash"`
	Hash      string `json:"hash"`
}

// Head is the chain head an export ends with.
type Head struct {
	TenantID string `json:"tenant_id"`
	Seq      int64  `json:"seq"`
	Hash     string `json:"hash"`
}

// Line is one line of an export: an entry, or the head at the end.
type Line struct {
	Entry *Entry `json:"entry,omitempty"`
	Head  *Head  `json:"head,omitempty"`
}

// quote writes s as Postgres's jsonb text output does (escape_json).
func quote(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// Canonical is the byte string hashed into the chain for e. jsonb prints
// object keys shortest first, then bytewise; the order below is that order.
func Canonical(e Entry) []byte {
	var b bytes.Buffer
	field := func(first bool, k string) {
		if !first {
			b.WriteString(", ")
		}
		quote(&b, k)
		b.WriteString(": ")
	}
	b.WriteByte('{')
	field(true, "v")
	b.WriteString("1")
	field(false, "at")
	quote(&b, e.At)
	field(false, "action")
	quote(&b, e.Action)
	field(false, "detail")
	b.WriteString(e.Detail)
	field(false, "target")
	quote(&b, e.Target)
	field(false, "actor_id")
	quote(&b, e.ActorID)
	field(false, "chain_seq")
	b.WriteString(strconv.FormatInt(e.Seq, 10))
	field(false, "tenant_id")
	quote(&b, e.TenantID)
	field(false, "actor_type")
	quote(&b, e.ActorType)
	b.WriteByte('}')
	return b.Bytes()
}

// Result of a verification.
type Result struct {
	Entries     int64
	FirstBroken int64 // 0 when intact
	Reason      string
	Anchors     int // anchors matched, with VerifyWithAnchors
}

// Verify reads an export (JSON lines) and checks every link and hash, and
// that the chain ends at the exported head.
func Verify(r io.Reader) (Result, error) { return verify(r, nil) }

// verify is Verify, calling seen with each verified entry's hash.
func verify(r io.Reader, seen func(seq int64, hash string)) (Result, error) {
	var res Result
	prev := make([]byte, 32)
	expect := int64(1)
	var head *Head
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	fail := func(seq int64, why string) (Result, error) {
		res.FirstBroken, res.Reason = seq, why
		return res, nil
	}
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l Line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return res, fmt.Errorf("audit: bad export line: %w", err)
		}
		if l.Head != nil {
			head = l.Head
			continue
		}
		if l.Entry == nil {
			continue
		}
		e := *l.Entry
		if e.Seq != expect {
			return fail(expect, fmt.Sprintf("entry %d missing (found %d)", expect, e.Seq))
		}
		if e.PrevHash != hex.EncodeToString(prev) {
			return fail(e.Seq, "prev_hash does not link to the previous entry")
		}
		sum := sha256.Sum256(append(append([]byte{}, prev...), Canonical(e)...))
		if hex.EncodeToString(sum[:]) != e.Hash {
			return fail(e.Seq, "hash does not match the entry's contents")
		}
		prev = sum[:]
		if seen != nil {
			seen(e.Seq, e.Hash)
		}
		expect++
		res.Entries++
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	if head == nil {
		return fail(expect, "export has no chain head; it may be truncated")
	}
	if head.Seq != expect-1 || head.Hash != hex.EncodeToString(prev) {
		return fail(expect, fmt.Sprintf("chain head is %d but the export ends at %d; entries were removed or added", head.Seq, expect-1))
	}
	return res, nil
}

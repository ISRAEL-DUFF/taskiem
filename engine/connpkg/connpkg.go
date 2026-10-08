// Package connpkg is the connector package format,
// taskiem-connector-package/v1: one JSON document carrying a connector
// version for the public catalogue (docs/connector-submissions.md): its
// manifest, WebAssembly module, conformance suite, licence and the
// publisher's attestation, with a SHA-256 digest over all of it and the
// publisher's Ed25519 signature over the digest, as audit anchors are
// signed (spec 9.2).
//
// The digest is what reviewers approve and installing tenants pin: a
// package whose bytes change has another digest, and a signature made for
// one digest verifies for no other.
package connpkg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/conntest"
)

// Format names this format.
const Format = "taskiem-connector-package/v1"

// MaxSize bounds a package document (a 32 MiB module, base64, and its
// suite).
const MaxSize = 48 << 20

// Package is one connector version, packaged.
type Package struct {
	Format  string `json:"format"`
	ID      string `json:"id"`
	Version string `json:"version"`
	// Manifest is the connector/v1 manifest as the author wrote it.
	Manifest string `json:"manifest"`
	// Module is the WebAssembly module (base64 in JSON).
	Module []byte `json:"module"`
	// Licence is an SPDX identifier, or LicenseRef-Proprietary.
	Licence     string          `json:"licence"`
	SourceURL   string          `json:"source_url,omitempty"`
	Conformance *conntest.Suite `json:"conformance"`
	Attestation Attestation     `json:"attestation"`
	CreatedAt   string          `json:"created_at"` // RFC 3339, UTC

	Digest    string `json:"digest"`    // hex SHA-256 of Content()
	KeyID     string `json:"key_id"`    // the publisher key's id
	Signature string `json:"signature"` // base64 Ed25519 over Message()
}

// Attestation is what the publisher states about the work.
type Attestation struct {
	// OriginalWork: the publisher wrote it, or has the right to publish it,
	// and copied no other product's connector definitions, code or
	// fixtures (docs/clean-room-policy.md).
	OriginalWork bool `json:"original_work"`
	// Contact is where reviewers and Taskiem reach the publisher about
	// this version (an email address).
	Contact string `json:"contact"`
}

// content is what the digest covers: everything but the digest and
// signature, the module by its own SHA-256.
type content struct {
	Format       string          `json:"format"`
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	Manifest     string          `json:"manifest"`
	ModuleSHA256 string          `json:"module_sha256"`
	Licence      string          `json:"licence"`
	SourceURL    string          `json:"source_url"`
	Conformance  *conntest.Suite `json:"conformance"`
	Attestation  Attestation     `json:"attestation"`
	CreatedAt    string          `json:"created_at"`
}

// ModuleDigest is the hex SHA-256 of the module.
func (p *Package) ModuleDigest() string {
	sum := sha256.Sum256(p.Module)
	return hex.EncodeToString(sum[:])
}

// Content is the canonical form the digest covers.
func (p *Package) Content() []byte {
	b, _ := json.Marshal(content{Format: p.Format, ID: p.ID, Version: p.Version, Manifest: p.Manifest, ModuleSHA256: p.ModuleDigest(),
		Licence: p.Licence, SourceURL: p.SourceURL, Conformance: p.Conformance, Attestation: p.Attestation, CreatedAt: p.CreatedAt})
	return b
}

// ComputeDigest is the hex SHA-256 of Content().
func (p *Package) ComputeDigest() string {
	sum := sha256.Sum256(p.Content())
	return hex.EncodeToString(sum[:])
}

// Message is what a package's signature covers.
func Message(id, version, digest string) []byte {
	return []byte(Format + "\n" + id + "\n" + version + "\n" + digest + "\n")
}

// Sign sets the digest and signs it.
func (p *Package) Sign(key ed25519.PrivateKey) {
	p.Format = Format
	p.Digest = p.ComputeDigest()
	p.KeyID = KeyID(key.Public().(ed25519.PublicKey))
	p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, Message(p.ID, p.Version, p.Digest)))
}

// Errors from Verify.
var (
	ErrDigest    = errors.New("the package digest does not match its content")
	ErrSignature = errors.New("the package signature does not verify with the publisher's key")
)

// Verify checks the format, the digest against the content, and the
// signature with pub.
func (p *Package) Verify(pub ed25519.PublicKey) error {
	if p.Format != Format {
		return fmt.Errorf("format %q, want %q", p.Format, Format)
	}
	if p.Digest != p.ComputeDigest() {
		return ErrDigest
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil || len(pub) != ed25519.PublicKeySize || p.KeyID != KeyID(pub) || !ed25519.Verify(pub, Message(p.ID, p.Version, p.Digest), sig) {
		return ErrSignature
	}
	return nil
}

// Read decodes a package of at most MaxSize bytes.
func Read(r io.Reader) (*Package, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxSize {
		return nil, fmt.Errorf("the package is larger than %d bytes", MaxSize)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Package
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("not a %s document: %w", Format, err)
	}
	if p.Format != Format {
		return nil, fmt.Errorf("format %q, want %q", p.Format, Format)
	}
	if p.Conformance != nil {
		if err := p.Conformance.Check(); err != nil {
			return nil, fmt.Errorf("conformance: %w", err)
		}
	}
	return &p, nil
}

// Write encodes a package.
func Write(w io.Writer, p *Package) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(p)
}

// KeyID names a public key as audit anchors do: the first 8 bytes of its
// SHA-256, in hex.
func KeyID(pub ed25519.PublicKey) string { return audit.KeyID(pub) }

// GenerateKey makes a publisher key: the private key as a base64 seed (to
// keep secret) and the public key in base64 (to register).
func GenerateKey() (seed, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey reads a base64 seed, as GenerateKey writes it.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, fmt.Errorf("a publisher key is a base64 %d-byte Ed25519 seed", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(b), nil
}

// ParsePublicKey reads a base64 Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("a publisher public key is a base64 %d-byte Ed25519 key", ed25519.PublicKeySize)
	}
	return b, nil
}

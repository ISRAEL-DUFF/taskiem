package formguard

import (
	"crypto/sha256"
	"encoding/hex"
	"math/bits"
	"strconv"

	_ "embed"
)

// PoWField is the hidden input pow.js writes the solved counter into.
const PoWField = "pow"

// Proof-of-work. The challenge is the token itself (its nonce, form id and
// issue time, under its tag): the browser finds a uint32 counter such that
// SHA-256(token || ":" || counter in decimal) starts with at least the
// token's difficulty in zero bits. The server checks it with one hash and
// no state; replaying a solution only replays the token, which the
// idempotency key already makes harmless.
//
// Each bit doubles the work. About 16 to 18 bits takes well under a second
// on a low-end Android phone, which is barely noticed by a person and
// costly for a bot posting thousands of times. The default is 0 (off);
// callers raise it on a page under attack. Without JavaScript a form that
// needs proof-of-work cannot be submitted, so such a page must also offer
// the fallback: a code sent by SMS to the visitor's phone (forms.md 7.3,
// storefronts.md 7.5).

//go:embed pow.js
var script []byte

var scriptHash = func() string {
	s := sha256.Sum256(script)
	return hex.EncodeToString(s[:])
}()

// Script is pow.js, to be served as a static file (the CSP allows no
// inline script). It finds each form[data-pow] with a difficulty in
// data-pow and the token in data-pow-token, disables its submit buttons,
// solves, writes the counter into the PoWField input and enables them
// again. With difficulty 0 it does nothing. Callers must not modify the
// returned slice.
func Script() []byte { return script }

// ScriptHash is the SHA-256 of Script in hex, for a content-hashed file
// name (cached forever) and for a CSP hash if one is ever needed.
func ScriptHash() string { return scriptHash }

// VerifyPoW checks counter against the token Verify returned claims for.
// With difficulty 0 any counter passes. Otherwise counter must be a uint32
// in canonical decimal, else ErrPoW.
func VerifyPoW(token string, claims Claims, counter string) error {
	if claims.PoW == 0 {
		return nil
	}
	if token != claims.token {
		return ErrPoW
	}
	n, err := strconv.ParseUint(counter, 10, 32)
	if err != nil || strconv.FormatUint(n, 10) != counter {
		return ErrPoW
	}
	if leadingZeros(sha256.Sum256([]byte(token+":"+counter))) < claims.PoW {
		return ErrPoW
	}
	return nil
}

// leadingZeros counts the zero bits at the start of h.
func leadingZeros(h [sha256.Size]byte) int {
	n := 0
	for _, b := range h {
		n += bits.LeadingZeros8(b)
		if b != 0 {
			break
		}
	}
	return n
}

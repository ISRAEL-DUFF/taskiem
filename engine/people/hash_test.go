package people_test

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/israel-duff/taskiem/engine/people"
)

// TestContactHashVector is the fixed vector the app's own hash is checked
// against (mobile/test/people_test.dart, "the contacts hash"): the same
// key, sent as base64 by GET /v1/me/contacts/key, and the same E.164 number
// give the same 64 hex digits on both sides.
func TestContactHashVector(t *testing.T) {
	key, err := base64.StdEncoding.DecodeString("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(people.ContactHash(key, "+2348031234567"))
	if want := "b4913c73c5ed5c7ea72d96186255b89cddbb6931ce8aea9da6a39d26af286458"; got != want {
		t.Fatalf("ContactHash = %s, want %s", got, want)
	}
}

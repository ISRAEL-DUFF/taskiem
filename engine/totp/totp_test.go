package totp_test

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/totp"
)

// RFC 6238 appendix B test vectors (SHA1, 8-digit codes truncated to 6).
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want8 := range map[int64]string{59: "94287082", 1111111109: "07081804", 1111111111: "14050471", 1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130"} {
		got, err := totp.Code(secret, unix/30)
		if err != nil {
			t.Fatal(err)
		}
		if got != want8[2:] {
			t.Errorf("t=%d: got %s, want %s", unix, got, want8[2:])
		}
	}
}

func TestVerifyOnceWithinSkew(t *testing.T) {
	secret, _ := totp.NewSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := totp.Code(secret, totp.Step(now)-1) // the previous step: clock drift
	step, ok := totp.Verify(secret, code, now, 0)
	if !ok {
		t.Fatal("a code from the previous step should pass")
	}
	if _, ok := totp.Verify(secret, code, now, step); ok {
		t.Error("a used code passed again")
	}
	old, _ := totp.Code(secret, totp.Step(now)-3)
	if _, ok := totp.Verify(secret, old, now, 0); ok {
		t.Error("a code three steps old passed")
	}
	if _, ok := totp.Verify(secret, "12345", now, 0); ok {
		t.Error("a short code passed")
	}
	if u := totp.URI(secret, "Taskiem", "ada@acme.test"); !strings.HasPrefix(u, "otpauth://totp/Taskiem:ada@acme.test?") || !strings.Contains(u, "secret="+secret) {
		t.Errorf("uri: %s", u)
	}
}

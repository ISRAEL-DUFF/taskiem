package webauthn

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

var cfg = Config{RPID: "taskiem.test", RPName: "Taskiem", Origins: []string{"https://app.taskiem.test"}}

func register(t *testing.T, a *webauthntest.Authenticator) *Credential {
	t.Helper()
	ch := NewChallenge()
	r := a.Register(ch)
	c, err := cfg.VerifyRegistration(ch, r.ClientDataJSON, r.AttestationObject)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRegisterAndSignIn(t *testing.T) {
	a := webauthntest.New("taskiem.test", "https://app.taskiem.test")
	c := register(t, a)
	if c.Alg != AlgES256 || string(c.ID) != string(a.CredID) {
		t.Fatalf("credential %+v", c)
	}
	for i := range 3 {
		ch := NewChallenge()
		s := a.Assert(ch)
		n, err := cfg.VerifyAssertion(ch, *c, s.ClientDataJSON, s.AuthenticatorData, s.Signature)
		if err != nil {
			t.Fatalf("sign-in %d: %v", i, err)
		}
		c.SignCount = n
	}
}

func TestRefusals(t *testing.T) {
	a := webauthntest.New("taskiem.test", "https://app.taskiem.test")
	c := register(t, a)
	ch := NewChallenge()
	s := a.Assert(ch)
	check := func(name string, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}
	_, err := cfg.VerifyAssertion(NewChallenge(), *c, s.ClientDataJSON, s.AuthenticatorData, s.Signature)
	check("other challenge", err, ErrInvalid)
	bad := append([]byte(nil), s.Signature...)
	bad[len(bad)-1] ^= 1
	_, err = cfg.VerifyAssertion(ch, *c, s.ClientDataJSON, s.AuthenticatorData, bad)
	check("tampered signature", err, ErrInvalid)
	other := Config{RPID: "evil.test", Origins: cfg.Origins}
	_, err = other.VerifyAssertion(ch, *c, s.ClientDataJSON, s.AuthenticatorData, s.Signature)
	check("another site's rp id", err, ErrInvalid)
	phish := webauthntest.New("taskiem.test", "https://taskiem-login.test")
	ch2 := NewChallenge()
	r := phish.Register(ch2)
	_, err = cfg.VerifyRegistration(ch2, r.ClientDataJSON, r.AttestationObject)
	check("another origin", err, ErrInvalid)
	noUV := webauthntest.New("taskiem.test", "https://app.taskiem.test")
	noUV.SkipUV = true
	ch3 := NewChallenge()
	r = noUV.Register(ch3)
	_, err = cfg.VerifyRegistration(ch3, r.ClientDataJSON, r.AttestationObject)
	check("no user verification", err, ErrInvalid)
	// A replayed assertion: the counter does not advance.
	c.SignCount = 100
	_, err = cfg.VerifyAssertion(ch, *c, s.ClientDataJSON, s.AuthenticatorData, s.Signature)
	check("counter went back", err, ErrCloned)
}

func TestSyncedPasskeysKeepCountZero(t *testing.T) {
	a := webauthntest.New("taskiem.test", "https://app.taskiem.test")
	a.Synced, a.Count = true, 0
	c := register(t, a)
	if !c.BackupEligible {
		t.Error("backup eligibility lost")
	}
	for range 2 {
		ch := NewChallenge()
		s := a.Assert(ch)
		if _, err := cfg.VerifyAssertion(ch, *c, s.ClientDataJSON, s.AuthenticatorData, s.Signature); err != nil {
			t.Fatal(err)
		}
	}
}

func FuzzCBOR(f *testing.F) {
	f.Add(webauthntest.Encode(map[string]any{"fmt": "none", "authData": []byte{1, 2, 3}}))
	f.Add([]byte{0x9f}) // indefinite array
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _, _ = decodeCBOR(b)
		_, _ = parseCOSE(b)
	})
}

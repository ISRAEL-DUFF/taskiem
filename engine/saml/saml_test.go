package saml

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/saml/samltest"
)

const acs = "https://app.taskiem.test/v1/auth/sso/saml/acs"

func setup(t *testing.T) (*samltest.IdP, SP) {
	t.Helper()
	idp := samltest.New("https://idp.bank.test", "https://idp.bank.test/sso")
	md, err := ParseMetadata(idp.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	return idp, SP{EntityID: "https://app.taskiem.test/v1/auth/sso/saml/c1", ACSURL: acs, IdPEntityID: md.EntityID, IdPSSOURL: md.SSOURL, IdPCerts: md.Certs}
}

func TestSignIn(t *testing.T) {
	idp, sp := setup(t)
	u, reqID, err := sp.AuthURL("state-1")
	if err != nil || reqID == "" {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	if !strings.HasPrefix(u, "https://idp.bank.test/sso?") || pu.Query().Get("SAMLRequest") == "" || pu.Query().Get("RelayState") != "state-1" {
		t.Fatalf("auth url %s", u)
	}
	resp := idp.Response(samltest.Assertion{InResponseTo: reqID, Audience: sp.EntityID, Recipient: acs, NameID: "ada@bank.test",
		Attributes: map[string][]string{"name": {"Ada Obi"}, "groups": {"treasury", "ops"}}})
	id, err := sp.Verify(resp, reqID, Attributes{Name: "name", Groups: "groups"})
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "ada@bank.test" || id.Name != "Ada Obi" || len(id.Groups) != 2 {
		t.Errorf("identity %+v", id)
	}
	if md, err := sp.SPMetadata(); err != nil || !strings.Contains(string(md), acs) {
		t.Errorf("sp metadata: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	idp, sp := setup(t)
	_, reqID, _ := sp.AuthURL("s")
	good := samltest.Assertion{InResponseTo: reqID, Audience: sp.EntityID, Recipient: acs, NameID: "ada@bank.test"}
	for name, tc := range map[string]struct {
		mut    func(*samltest.Assertion)
		signer *samltest.IdP
		reqID  string
	}{
		"another request":    {mut: func(a *samltest.Assertion) { a.InResponseTo = "_other" }},
		"unsolicited":        {reqID: "-"},
		"other audience":     {mut: func(a *samltest.Assertion) { a.Audience = "https://evil.test" }},
		"other recipient":    {mut: func(a *samltest.Assertion) { a.Recipient = "https://evil.test/acs" }},
		"expired":            {mut: func(a *samltest.Assertion) { a.IssuedAt = time.Now().Add(-time.Hour) }},
		"other issuer":       {mut: func(a *samltest.Assertion) { a.Issuer = "https://evil.test" }},
		"someone else's key": {signer: samltest.New("https://idp.bank.test", "https://idp.bank.test/sso")},
	} {
		a := good
		if tc.mut != nil {
			tc.mut(&a)
		}
		signer := idp
		if tc.signer != nil {
			signer = tc.signer
		}
		want := reqID
		if tc.reqID == "-" {
			want = ""
		}
		if _, err := sp.Verify(signer.Response(a), want, Attributes{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A signed assertion whose NameID was edited afterwards.
	raw, _ := base64.StdEncoding.DecodeString(idp.Response(good))
	edited := strings.Replace(string(raw), "ada@bank.test", "owner@bank.test", 1)
	if _, err := sp.Verify(base64.StdEncoding.EncodeToString([]byte(edited)), reqID, Attributes{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("edited assertion: %v", err)
	}
}

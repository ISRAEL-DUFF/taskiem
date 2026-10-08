// Package samltest is a fake SAML identity provider for tests: it signs
// assertions with its own certificate, as a real IdP would.
package samltest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

// IdP holds the signing key and certificate.
type IdP struct {
	EntityID string
	SSOURL   string
	Key      *rsa.PrivateKey
	CertDER  []byte
	Cert     *x509.Certificate
}

// New makes an IdP with a fresh self-signed certificate.
func New(entityID, ssoURL string) *IdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: entityID},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	cert, _ := x509.ParseCertificate(der)
	return &IdP{EntityID: entityID, SSOURL: ssoURL, Key: k, CertDER: der, Cert: cert}
}

// GetKeyPair implements dsig.X509KeyStore.
func (i *IdP) GetKeyPair() (*rsa.PrivateKey, []byte, error) { return i.Key, i.CertDER, nil }

// Assertion describes what to assert.
type Assertion struct {
	InResponseTo string
	Audience     string
	Recipient    string
	NameID       string
	Attributes   map[string][]string
	IssuedAt     time.Time // default now
	Issuer       string    // default the IdP's entity id
}

// Response returns a base64 SAMLResponse with a signed assertion.
func (i *IdP) Response(a Assertion) string {
	now := a.IssuedAt
	if now.IsZero() {
		now = time.Now()
	}
	issuer := a.Issuer
	if issuer == "" {
		issuer = i.EntityID
	}
	ts := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	doc := etree.NewDocument()
	resp := doc.CreateElement("samlp:Response")
	resp.CreateAttr("xmlns:samlp", "urn:oasis:names:tc:SAML:2.0:protocol")
	resp.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	resp.CreateAttr("ID", "_resp1")
	resp.CreateAttr("Version", "2.0")
	resp.CreateAttr("IssueInstant", ts(now))
	resp.CreateAttr("Destination", a.Recipient)
	resp.CreateAttr("InResponseTo", a.InResponseTo)
	resp.CreateElement("saml:Issuer").SetText(issuer)
	resp.CreateElement("samlp:Status").CreateElement("samlp:StatusCode").CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:Success")

	as := etree.NewElement("saml:Assertion")
	as.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	as.CreateAttr("ID", "_assert1")
	as.CreateAttr("Version", "2.0")
	as.CreateAttr("IssueInstant", ts(now))
	as.CreateElement("saml:Issuer").SetText(issuer)
	subj := as.CreateElement("saml:Subject")
	nid := subj.CreateElement("saml:NameID")
	nid.CreateAttr("Format", "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress")
	nid.SetText(a.NameID)
	sc := subj.CreateElement("saml:SubjectConfirmation")
	sc.CreateAttr("Method", "urn:oasis:names:tc:SAML:2.0:cm:bearer")
	scd := sc.CreateElement("saml:SubjectConfirmationData")
	scd.CreateAttr("InResponseTo", a.InResponseTo)
	scd.CreateAttr("Recipient", a.Recipient)
	scd.CreateAttr("NotOnOrAfter", ts(now.Add(5*time.Minute)))
	cond := as.CreateElement("saml:Conditions")
	cond.CreateAttr("NotBefore", ts(now.Add(-time.Minute)))
	cond.CreateAttr("NotOnOrAfter", ts(now.Add(5*time.Minute)))
	cond.CreateElement("saml:AudienceRestriction").CreateElement("saml:Audience").SetText(a.Audience)
	authn := as.CreateElement("saml:AuthnStatement")
	authn.CreateAttr("AuthnInstant", ts(now))
	authn.CreateAttr("SessionIndex", "_s1")
	authn.CreateElement("saml:AuthnContext").CreateElement("saml:AuthnContextClassRef").SetText("urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport")
	if len(a.Attributes) > 0 {
		st := as.CreateElement("saml:AttributeStatement")
		for name, values := range a.Attributes {
			at := st.CreateElement("saml:Attribute")
			at.CreateAttr("Name", name)
			for _, v := range values {
				av := at.CreateElement("saml:AttributeValue")
				av.SetText(v)
			}
		}
	}
	ctx := dsig.NewDefaultSigningContext(i)
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	signed, err := ctx.SignEnveloped(as)
	if err != nil {
		panic(err)
	}
	resp.AddChild(signed)
	raw, _ := doc.WriteToBytes()
	return base64.StdEncoding.EncodeToString(raw)
}

// Metadata is the IdP's metadata XML.
func (i *IdP) Metadata() []byte {
	return []byte(`<?xml version="1.0"?>
<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + i.EntityID + `">
  <md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data><ds:X509Certificate>` +
		base64.StdEncoding.EncodeToString(i.CertDER) + `</ds:X509Certificate></ds:X509Data></ds:KeyInfo></md:KeyDescriptor>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="` + i.SSOURL + `"/>
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`)
}

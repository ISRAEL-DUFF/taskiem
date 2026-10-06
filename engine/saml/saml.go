// Package saml signs people in with a SAML 2.0 identity provider (SP
// initiated, HTTP-Redirect request, HTTP-POST response). XML signature
// checking is done by gosaml2/goxmldsig; this package adds what they leave
// to the caller: the response must answer our own request (InResponseTo),
// be for our audience and recipient, and be inside its validity window.
// IdP-initiated sign-in is refused: nothing would tie it to a request.
package saml

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"
)

// ErrInvalid marks a response that must not be trusted.
var ErrInvalid = errors.New("saml: invalid response")

// SP is Taskiem as a service provider for one identity provider.
type SP struct {
	EntityID    string // our metadata URL
	ACSURL      string // where the IdP posts the response
	IdPEntityID string
	IdPSSOURL   string
	IdPCerts    []*x509.Certificate
	Now         func() time.Time
}

func (sp SP) provider() *saml2.SAMLServiceProvider {
	p := &saml2.SAMLServiceProvider{
		IdentityProviderSSOURL:      sp.IdPSSOURL,
		IdentityProviderSSOBinding:  saml2.BindingHttpRedirect,
		IdentityProviderIssuer:      sp.IdPEntityID,
		AssertionConsumerServiceURL: sp.ACSURL,
		ServiceProviderIssuer:       sp.EntityID,
		AudienceURI:                 sp.EntityID,
		IDPCertificateStore:         &dsig.MemoryX509CertificateStore{Roots: sp.IdPCerts},
		NameIdFormat:                saml2.NameIdFormatEmailAddress,
		AllowMissingAttributes:      true,
	}
	if sp.Now != nil {
		p.Clock = dsig.NewFakeClockAt(sp.Now())
	}
	return p
}

// AuthURL is where to send the person, and the request id their response
// must answer.
func (sp SP) AuthURL(relayState string) (string, string, error) {
	p := sp.provider()
	doc, err := p.BuildAuthRequestDocument()
	if err != nil {
		return "", "", err
	}
	id := doc.Root().SelectAttrValue("ID", "")
	u, err := p.BuildAuthURLFromDocument(relayState, doc)
	return u, id, err
}

// Identity is who the IdP says signed in.
type Identity struct {
	NameID string
	Email  string
	Name   string
	Groups []string
}

// Attributes name where the IdP puts email, name and groups ("" to skip).
type Attributes struct {
	Email, Name, Groups string
}

// Verify checks a posted SAMLResponse against the request it must answer.
func (sp SP) Verify(encoded, requestID string, attrs Attributes) (Identity, error) {
	if requestID == "" {
		return Identity{}, fmt.Errorf("%w: unsolicited response", ErrInvalid)
	}
	p := sp.provider()
	info, err := p.RetrieveAssertionInfo(encoded)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if w := info.WarningInfo; w == nil || w.InvalidTime || w.NotInAudience {
		return Identity{}, fmt.Errorf("%w: outside its validity window or for another audience", ErrInvalid)
	}
	a := info.Assertions[0]
	if a.Issuer == nil || a.Issuer.Value != sp.IdPEntityID {
		return Identity{}, fmt.Errorf("%w: issued by someone else", ErrInvalid)
	}
	sc := a.Subject.SubjectConfirmation
	if sc == nil || sc.SubjectConfirmationData == nil {
		return Identity{}, fmt.Errorf("%w: no subject confirmation", ErrInvalid)
	}
	scd := sc.SubjectConfirmationData
	if scd.InResponseTo != requestID {
		return Identity{}, fmt.Errorf("%w: not an answer to our request", ErrInvalid)
	}
	if scd.Recipient != "" && scd.Recipient != sp.ACSURL {
		return Identity{}, fmt.Errorf("%w: for another recipient", ErrInvalid)
	}
	now := time.Now()
	if sp.Now != nil {
		now = sp.Now()
	}
	if scd.NotOnOrAfter != "" {
		t, err := time.Parse(time.RFC3339, scd.NotOnOrAfter)
		if err != nil || !now.Before(t) {
			return Identity{}, fmt.Errorf("%w: expired", ErrInvalid)
		}
	}
	id := Identity{NameID: info.NameID}
	if attrs.Email != "" {
		id.Email = info.Values.Get(attrs.Email)
	}
	if id.Email == "" && strings.Contains(info.NameID, "@") {
		id.Email = info.NameID
	}
	if attrs.Name != "" {
		id.Name = info.Values.Get(attrs.Name)
	}
	if attrs.Groups != "" {
		if v, ok := info.Values[attrs.Groups]; ok {
			for _, x := range v.Values {
				id.Groups = append(id.Groups, x.Value)
			}
		}
	}
	return id, nil
}

// Metadata is what an IdP's metadata says about it.
type Metadata struct {
	EntityID string
	SSOURL   string
	Certs    []*x509.Certificate
}

// ParseMetadata reads an IdP's metadata XML.
func ParseMetadata(raw []byte) (Metadata, error) {
	var ed types.EntityDescriptor
	if err := xml.Unmarshal(raw, &ed); err != nil {
		return Metadata{}, fmt.Errorf("saml: metadata: %w", err)
	}
	if ed.IDPSSODescriptor == nil {
		return Metadata{}, errors.New("saml: metadata has no IDPSSODescriptor")
	}
	m := Metadata{EntityID: ed.EntityID}
	for _, s := range ed.IDPSSODescriptor.SingleSignOnServices {
		if s.Binding == saml2.BindingHttpRedirect {
			m.SSOURL = s.Location
		}
	}
	for _, kd := range ed.IDPSSODescriptor.KeyDescriptors {
		if kd.Use != "" && kd.Use != "signing" {
			continue
		}
		for _, c := range kd.KeyInfo.X509Data.X509Certificates {
			der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(c.Data), ""))
			if err != nil {
				return Metadata{}, fmt.Errorf("saml: metadata certificate: %w", err)
			}
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return Metadata{}, fmt.Errorf("saml: metadata certificate: %w", err)
			}
			m.Certs = append(m.Certs, cert)
		}
	}
	if m.EntityID == "" || m.SSOURL == "" || len(m.Certs) == 0 {
		return Metadata{}, errors.New("saml: metadata needs an entity id, an HTTP-Redirect sign-on URL and a signing certificate")
	}
	return m, nil
}

// SPMetadata is our metadata, for the IdP's administrator.
func (sp SP) SPMetadata() ([]byte, error) {
	ed := types.EntityDescriptor{EntityID: sp.EntityID, SPSSODescriptor: &types.SPSSODescriptor{
		WantAssertionsSigned:       true,
		ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
		NameIDFormats:              []string{saml2.NameIdFormatEmailAddress},
		AssertionConsumerServices:  []types.IndexedEndpoint{{Binding: saml2.BindingHttpPost, Location: sp.ACSURL, Index: 1}},
	}}
	out, err := xml.MarshalIndent(ed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

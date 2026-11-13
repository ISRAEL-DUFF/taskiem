package inmail

import (
	"bytes"
	"context"
	"net"
	"strings"
	"time"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
)

// Resolver is the DNS the checks use; *net.Resolver is one.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// Authenticate checks where a message came from: SPF for the envelope
// sender and the connecting address, the DKIM signatures, and whether
// either proves the From address's domain. DNS failures count as no
// proof, never as a reason to refuse: the message is still taken, and a
// workflow's senders list then does not trust it. from is the message's
// one From address (signedFrom), the one a DKIM signature covers.
func Authenticate(ctx context.Context, res Resolver, raw []byte, from, mailFrom, helo string, ip net.IP) Auth {
	a, _ := authenticate(ctx, res, raw, from, mailFrom, helo, ip)
	return a
}

// authenticate is Authenticate, and what the first DKIM signature that
// verified and belongs to the From's domain fixes of the message
// (signedIdentity): nil when none does.
func authenticate(ctx context.Context, res Resolver, raw []byte, from, mailFrom, helo string, ip net.IP) (Auth, []byte) {
	if res == nil {
		res = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	a := Auth{SPF: string(spf.None), DKIM: []string{}, From: strings.ToLower(from)}
	if ip != nil {
		sender := strings.Trim(mailFrom, "<>")
		if sender == "" {
			sender = "postmaster@" + helo
		}
		r, _ := spf.CheckHostWithSender(ip, helo, sender, spf.WithContext(ctx), spf.WithResolver(res))
		a.SPF = string(r)
	}
	fromDomain := Domain(from)
	var signed []byte
	lookup := func(name string) ([]string, error) { return res.LookupTXT(ctx, name) }
	// One verification for each DKIM-Signature header, in their order.
	if vs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: lookup, MaxVerifications: 5}); err == nil || len(vs) > 0 {
		for i, v := range vs {
			if v == nil || v.Err != nil {
				continue
			}
			d := strings.ToLower(v.Domain)
			a.DKIM = append(a.DKIM, d)
			if signed == nil && Aligned(d, fromDomain) {
				signed = signedIdentity(raw, i, v.HeaderKeys)
			}
		}
	}
	for _, d := range a.DKIM {
		if Aligned(d, fromDomain) {
			a.Aligned = true
		}
	}
	if a.SPF == string(spf.Pass) && Aligned(Domain(strings.Trim(mailFrom, "<>")), fromDomain) {
		a.Aligned = true
	}
	if fromDomain != "" {
		if rec, err := dmarc.LookupWithOptions(fromDomain, &dmarc.LookupOptions{LookupTXT: lookup}); err == nil {
			a.DMARC = string(rec.Policy)
		}
	}
	return a, signed
}

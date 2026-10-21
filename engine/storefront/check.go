package storefront

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"time"
)

// Check now (no-technical-steps N9): each record the owner was asked to
// create is looked up, and what is wrong with it said in plain words, so
// an owner at their registrar's screen knows which one to fix.

// Resolver looks DNS up; a nil field uses the system resolver.
type Resolver struct {
	TXT   func(ctx context.Context, name string) ([]string, error)
	CNAME func(ctx context.Context, name string) (string, error)
	Host  func(ctx context.Context, name string) ([]string, error)
}

func (r Resolver) txt(ctx context.Context, n string) ([]string, error) {
	if r.TXT != nil {
		return r.TXT(ctx, n)
	}
	return net.DefaultResolver.LookupTXT(ctx, n)
}

func (r Resolver) cname(ctx context.Context, n string) (string, error) {
	if r.CNAME != nil {
		return r.CNAME(ctx, n)
	}
	return net.DefaultResolver.LookupCNAME(ctx, n)
}

func (r Resolver) host(ctx context.Context, n string) ([]string, error) {
	if r.Host != nil {
		return r.Host(ctx, n)
	}
	return net.DefaultResolver.LookupHost(ctx, n)
}

// RecordCheck is one record and what DNS has for it.
type RecordCheck struct {
	Record
	OK bool `json:"ok"`
	// Found is what DNS answers for the name now.
	Found []string `json:"found"`
	// Says is the record's state in plain words.
	Says string `json:"says"`
}

func notThere(err error) bool {
	var de *net.DNSError
	return err == nil || (errors.As(err, &de) && de.IsNotFound)
}

func dotless(s string) string { return strings.ToLower(strings.TrimSuffix(s, ".")) }

// CheckRecords looks up each of the domain's records.
func CheckRecords(ctx context.Context, d Domain, t DomainTarget, r Resolver) []RecordCheck {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out []RecordCheck
	for _, rec := range d.Records(t) {
		c := RecordCheck{Record: rec, Found: []string{}}
		switch rec.Type {
		case "TXT":
			found, err := r.txt(ctx, rec.Name)
			c.Found = append(c.Found, found...)
			c.OK = slices.Contains(found, rec.Value)
			switch {
			case c.OK:
				c.Says = "The TXT record is there."
			case !notThere(err):
				c.Says = "Taskiem could not look the TXT record up just now. Try again in a few minutes."
			case len(found) == 0:
				c.Says = "The TXT record named " + rec.Short + " is not there yet. Add it at the company you bought the domain from (it can take a few hours to show)."
			default:
				c.Says = "There is a TXT record named " + rec.Short + ", but its value is not the one Taskiem gave you. Change its value to " + rec.Value + "."
			}
		case "CNAME":
			got, err := r.cname(ctx, rec.Name)
			if got != "" && dotless(got) != dotless(rec.Name) {
				c.Found = append(c.Found, dotless(got))
			}
			c.OK = dotless(got) == dotless(rec.Value)
			switch {
			case c.OK:
				c.Says = rec.Name + " points at your page."
			case !notThere(err):
				c.Says = "Taskiem could not look " + rec.Name + " up just now. Try again in a few minutes."
			case len(c.Found) == 0:
				c.Says = "The CNAME record for " + rec.Short + " is not there yet. Add it with the value " + rec.Value + "."
			default:
				c.Says = rec.Name + " points at " + c.Found[0] + ", not at " + rec.Value + ". Change the CNAME record's value (and remove any A record for " + rec.Short + ")."
			}
		case "A", "AAAA", "ALIAS":
			got, err := r.host(ctx, rec.Name)
			c.Found = append(c.Found, got...)
			want := []string{rec.Value}
			if rec.Type == "ALIAS" {
				want, _ = r.host(ctx, rec.Value)
			}
			for _, w := range want {
				if slices.Contains(got, w) {
					c.OK = true
				}
			}
			switch {
			case c.OK:
				c.Says = rec.Name + " points at your page."
			case !notThere(err):
				c.Says = "Taskiem could not look " + rec.Name + " up just now. Try again in a few minutes."
			case len(got) == 0:
				c.Says = "The " + rec.Type + " record for " + rec.Short + " is not there yet. Add it with the value " + rec.Value + "."
			default:
				c.Says = rec.Name + " points somewhere else (" + strings.Join(got, ", ") + "). Change its " + rec.Type + " record to " + rec.Value + " and remove the others."
			}
		}
		out = append(out, c)
	}
	return out
}

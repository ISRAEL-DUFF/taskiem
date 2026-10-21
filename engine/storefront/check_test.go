package storefront_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Check now says which record is missing or wrong (no-technical-steps N9).
func TestCheckRecords(t *testing.T) {
	www := "www.adakitchen.ng"
	d := storefront.Domain{Host: "adakitchen.ng", Twin: &www, Token: "taskiem-verify=abc"}
	target := storefront.DomainTarget{CNAME: "pages.taskiem.test", Addresses: []string{"203.0.113.7"}}
	nx := &net.DNSError{Err: "no such host", IsNotFound: true}
	txt := map[string][]string{}
	hosts := map[string][]string{"adakitchen.ng": {"198.51.100.1"}}
	cnames := map[string]string{"www.adakitchen.ng": "parked.registrar.test."}
	r := storefront.Resolver{
		TXT: func(_ context.Context, n string) ([]string, error) {
			if v, ok := txt[n]; ok {
				return v, nil
			}
			return nil, nx
		},
		CNAME: func(_ context.Context, n string) (string, error) {
			if v, ok := cnames[n]; ok {
				return v, nil
			}
			return "", nx
		},
		Host: func(_ context.Context, n string) ([]string, error) {
			if v, ok := hosts[n]; ok {
				return v, nil
			}
			return nil, nx
		},
	}
	says := func() map[string]storefront.RecordCheck {
		out := map[string]storefront.RecordCheck{}
		for _, c := range storefront.CheckRecords(context.Background(), d, target, r) {
			out[c.Type+" "+c.Short] = c
		}
		return out
	}
	got := says()
	if len(got) != 3 {
		t.Fatalf("records: %v", got)
	}
	if c := got["TXT _taskiem-verify"]; c.OK || !strings.Contains(c.Says, "is not there yet") {
		t.Errorf("no TXT: %+v", c)
	}
	if c := got["A @"]; c.OK || !strings.Contains(c.Says, "points somewhere else (198.51.100.1)") {
		t.Errorf("the A record: %+v", c)
	}
	if c := got["CNAME www"]; c.OK || !strings.Contains(c.Says, "points at parked.registrar.test, not at pages.taskiem.test") {
		t.Errorf("the CNAME: %+v", c)
	}
	txt["_taskiem-verify.adakitchen.ng"] = []string{"something-else"}
	if c := says()["TXT _taskiem-verify"]; c.OK || !strings.Contains(c.Says, "its value is not the one Taskiem gave you") {
		t.Errorf("a wrong TXT: %+v", c)
	}
	txt["_taskiem-verify.adakitchen.ng"] = []string{"taskiem-verify=abc"}
	hosts["adakitchen.ng"] = []string{"203.0.113.7"}
	cnames["www.adakitchen.ng"] = "pages.taskiem.test."
	for k, c := range says() {
		if !c.OK {
			t.Errorf("%s: %+v", k, c)
		}
	}
}

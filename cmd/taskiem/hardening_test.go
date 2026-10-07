package main

import "testing"

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestHSTSFromEnv(t *testing.T) {
	cases := []struct {
		env       map[string]string
		publicURL string
		want      string
		bad       bool
	}{
		{nil, "https://taskiem.example.com", defaultHSTS, false},
		{nil, "http://localhost:8080", "", false},
		{nil, "", "", false},
		{map[string]string{"TASKIEM_HSTS": "off"}, "https://taskiem.example.com", "", false},
		{map[string]string{"TASKIEM_HSTS": "max-age=63072000; includeSubDomains"}, "https://taskiem.example.com", "max-age=63072000; includeSubDomains", false},
		{map[string]string{"TASKIEM_HSTS": "max-age=300"}, "", "max-age=300", false},
		{map[string]string{"TASKIEM_HSTS": "yes please"}, "", "", true},
	}
	for _, c := range cases {
		got, err := hstsFromEnv(lookupFrom(c.env), c.publicURL)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%v %q: %q %v", c.env, c.publicURL, got, err)
		}
	}
}

func TestCodeLimitsFromEnv(t *testing.T) {
	l, err := codeLimitsFromEnv(lookupFrom(map[string]string{"TASKIEM_TENANT_CODE_CONCURRENCY": "8", "TASKIEM_TENANT_CODE_PER_TENANT": "3"}))
	if err != nil || l.Concurrency != 8 || l.PerTenant != 3 {
		t.Errorf("limits: %+v %v", l, err)
	}
	if l, err := codeLimitsFromEnv(lookupFrom(nil)); err != nil || l.Concurrency != 0 || l.PerTenant != 0 {
		t.Errorf("defaults: %+v %v", l, err)
	}
	for _, v := range []string{"0", "-1", "many"} {
		if _, err := codeLimitsFromEnv(lookupFrom(map[string]string{"TASKIEM_TENANT_CODE_CONCURRENCY": v})); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

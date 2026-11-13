package main

import "testing"

func TestHooksShareConsole(t *testing.T) {
	for _, c := range []struct {
		public, hooks string
		want          bool
	}{
		{"https://app.example.com", "https://app.example.com/hooks", true},
		{"https://app.example.com", "https://APP.example.com/hooks", true},
		{"https://app.example.com", "https://hooks.example.com/hooks", false},
		{"https://app.example.com", "https://app.example.com:8443/hooks", false},
		{"http://localhost:8080", "http://localhost:8080/hooks", false},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/hooks", false},
		{"", "", false},
	} {
		if got := hooksShareConsole(c.public, c.hooks); got != c.want {
			t.Errorf("hooksShareConsole(%q, %q) = %v, want %v", c.public, c.hooks, got, c.want)
		}
	}
}

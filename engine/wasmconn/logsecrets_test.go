package wasmconn

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
)

// A module that logs its own credential: the line reaches the platform's
// log without it, through taskiem.log and through standard output, and
// also where the line is cut in the middle of the secret.
func TestConnectorLogLinesCarryNoSecrets(t *testing.T) {
	const key, token = "fixture-key-9f8e7d6c5b4a39281706f5e4d3c2b1a0", "tok_refresh_0123456789abcdef0123456789abcdef"
	lim := Limits{}.withDefaults()
	secrets := []string{key, token, "abc"} // too short to look for: ordinary text
	run := func(write func(c *call)) string {
		var got []string
		c := &call{lim: lim, req: connector.Request{Logger: slog.New(recordHandler{&got}), Secrets: secrets}}
		write(c)
		return strings.Join(got, "\n")
	}
	for name, write := range map[string]func(c *call){
		"taskiem.log": func(c *call) { c.log("calling with Authorization: Bearer " + key + " and refresh " + token) },
		"standard output": func(c *call) {
			_, _ = (&logWriter{c: c}).Write([]byte("debug creds=" + key + "\nrefresh=" + token + "\n"))
		},
		"cut at the line cap": func(c *call) {
			// The key starts ten bytes before the line cap.
			c.log(strings.Repeat("x", lim.MaxLogLine-10) + key + " tail")
		},
		"cut at the call's cap": func(c *call) {
			c.logBytes = lim.MaxLogs - 20
			c.log("0123456789" + token + strings.Repeat("y", 4000))
		},
		"a secret longer than the margin read past the cut": func(c *call) {
			long := "LONG" + strings.Repeat("s3cr3t", 200)
			c.req.Secrets = []string{long}
			c.log(strings.Repeat("x", lim.MaxLogLine-12) + long)
			c.req.Secrets = secrets
		},
	} {
		out := run(write)
		if out == "" {
			t.Errorf("%s: nothing was logged", name)
		}
		for _, part := range []string{key, token, key[:8], token[:8], "s3cr3t", "LONG"} {
			if strings.Contains(out, part) {
				t.Errorf("%s: the log carries %q: %.200q", name, part, out)
			}
		}
	}
	if out := run(func(c *call) { c.log("abc is not a secret worth masking; key is " + key) }); !strings.Contains(out, "abc is not") || !strings.Contains(out, "[secret]") {
		t.Errorf("short values are left, secrets replaced: %q", out)
	}
}

// One line is cut at MaxLogLine and a call's lines stop at MaxLogLines,
// so a module cannot flood the platform's log line by line.
func TestConnectorLogLineLengthAndRate(t *testing.T) {
	lim := Limits{MaxLogs: 1 << 20}.withDefaults()
	var got []string
	c := &call{lim: lim, req: connector.Request{Logger: slog.New(recordHandler{&got})}}
	c.log(strings.Repeat("z", 10*lim.MaxLogLine))
	if len(got) != 1 || len(got[0]) > lim.MaxLogLine+32 || !strings.HasSuffix(got[0], "[truncated]") {
		t.Fatalf("a long line: %d lines, %d bytes", len(got), len(got[0]))
	}
	for i := 0; i < 10*lim.MaxLogLines; i++ {
		c.log("line")
	}
	if len(got) > lim.MaxLogLines+1 || !strings.Contains(got[len(got)-1], "dropped") {
		t.Errorf("%d lines kept of a flood (cap %d); last %q", len(got), lim.MaxLogLines, got[len(got)-1])
	}
}

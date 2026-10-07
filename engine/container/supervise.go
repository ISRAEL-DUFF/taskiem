package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Supervision is how one program is run: the shim inside a sandbox and the
// local runner both use it.
type Supervision struct {
	Command []string // program and arguments
	Env     []string
	Dir     string
	// Stdin is fed to the program (input mode "stdin"); nil: empty.
	Stdin io.Reader
	// OutputPath is read for the output in output mode "file"; "" means
	// the output is the program's stdout.
	OutputPath  string
	OutputBytes int
	LogBytes    int
	Timeout     time.Duration
	// Prepare runs on the started process (the local runner's rlimits).
	Prepare func(*os.Process) error
	// SysProc adjusts the command before it starts (process group, namespaces).
	SysProc func(*exec.Cmd)
}

// capped keeps at most max bytes and notes whether more came.
type capped struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.over = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.over = true
	}
	return len(p), nil // keep draining: a blocked pipe would stall the program
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Supervise runs the program to completion (or its timeout) and reports
// the outcome. It never returns an error: everything is in the Outcome.
func Supervise(ctx context.Context, s Supervision) Outcome {
	if len(s.Command) == 0 {
		return Outcome{Error: "start: no command"}
	}
	if s.LogBytes <= 0 {
		s.LogBytes = LogBytes
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.Command(s.Command[0], s.Command[1:]...) //nolint:gosec // running the step's own program is the point
	cmd.Env = s.Env
	cmd.Dir = s.Dir
	// A descendant that escaped the process group may hold the pipes open.
	cmd.WaitDelay = 2 * time.Second
	if s.Stdin != nil {
		cmd.Stdin = s.Stdin
	}
	logs := &capped{max: s.LogBytes}
	out := &capped{max: s.OutputBytes}
	cmd.Stderr = logs
	if s.OutputPath == "" {
		cmd.Stdout = out
	} else {
		cmd.Stdout = logs
	}
	if s.SysProc != nil {
		s.SysProc(cmd)
	}
	setProcessGroup(cmd)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Outcome{Error: "start: " + err.Error()}
	}
	if s.Prepare != nil {
		if err := s.Prepare(cmd.Process); err != nil {
			killGroup(cmd)
			_ = cmd.Wait()
			return Outcome{Error: "start: " + err.Error()}
		}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var werr error
	timedOut := false
	select {
	case werr = <-done:
	case <-ctx.Done():
		timedOut = true
		killGroup(cmd)
		werr = <-done
	}
	o := Outcome{Elapsed: time.Since(start), Logs: logs.String()}
	if logs.over {
		o.Logs += "\n[logs truncated]"
	}
	if timedOut {
		o.Error = "timeout"
		return o
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		o.Exit = ee.ExitCode()
		if sig := signalOf(ee); sig != "" {
			o.Error = "killed: " + sig
			return o
		}
	} else if werr != nil {
		o.Error = werr.Error()
		return o
	}
	var raw []byte
	if s.OutputPath == "" {
		if out.over {
			o.Error = "output_too_large"
			return o
		}
		raw = out.buf.Bytes()
	} else {
		f, err := os.Open(s.OutputPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			o.Error = "bad_output"
			return o
		default:
			raw, err = io.ReadAll(io.LimitReader(f, int64(s.OutputBytes)+1))
			_ = f.Close()
			if err != nil {
				o.Error = "bad_output"
				return o
			}
			if len(raw) > s.OutputBytes {
				o.Error = "output_too_large"
				return o
			}
		}
	}
	if o.Exit != 0 {
		return o
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	if !json.Valid(raw) {
		o.Error = "bad_output"
		return o
	}
	o.Output = append(json.RawMessage(nil), raw...)
	return o
}

package container

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// Paths inside the sandbox (Kubernetes runner): the Pod mounts the input
// (and secrets, in file mode) read-only, and a small writable directory for
// the output.
const (
	ShimDir     = "/taskiem/bin"
	ShimPath    = ShimDir + "/taskiem-shim"
	InputPath   = "/taskiem/in/input.json"
	OutputPath  = "/taskiem/out/output.json"
	SecretsPath = "/taskiem/secrets"
)

// Shim is taskiem-shim's command line (cmd/taskiem-shim):
//
//	taskiem-shim install DEST
//	    copy this binary to DEST (the init container, into a shared volume)
//	taskiem-shim run [--input-mode stdin|file] [--output-mode stdout|file]
//	    [--output-bytes N] [--timeout D] -- COMMAND [ARG...]
//	    run COMMAND with the step's input, print one result line
//
// It runs inside the sandbox, as the step's own user: it is a convenience
// for collecting the output, not a security control. Whatever the program
// does to it can only change its own step's result.
func Shim(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: taskiem-shim install DEST | run [flags] -- COMMAND [ARG...]")
	}
	switch args[0] {
	case "install":
		if len(args) != 2 {
			return errors.New("usage: taskiem-shim install DEST")
		}
		return install(args[1])
	case "run":
		return shimRun(args[1:], stdout)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func install(dest string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(self) //nolint:gosec // this binary itself
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o555) //nolint:gosec // an executable the step's user runs
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func shimRun(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	inputMode := fs.String("input-mode", "stdin", "stdin or file")
	outputMode := fs.String("output-mode", "stdout", "stdout or file")
	outputBytes := fs.Int("output-bytes", 256<<10, "largest output accepted")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long the program may run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd := fs.Args()
	inPath := envOr("TASKIEM_INPUT", InputPath)
	s := Supervision{Command: cmd, Env: os.Environ(), OutputBytes: *outputBytes, Timeout: *timeout}
	if *inputMode == "stdin" {
		f, err := os.Open(inPath) //nolint:gosec // the input file the runner mounted
		if err != nil {
			_, _ = fmt.Fprintln(stdout, Outcome{Error: "start: input: " + err.Error()}.Encode())
			return nil
		}
		defer func() { _ = f.Close() }()
		s.Stdin = f
	}
	if *outputMode == "file" {
		s.OutputPath = envOr("TASKIEM_OUTPUT", OutputPath)
	}
	o := Supervise(context.Background(), s)
	_, err := fmt.Fprintln(stdout, o.Encode())
	return err
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

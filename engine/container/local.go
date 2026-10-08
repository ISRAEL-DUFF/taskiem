package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Local runs container steps as processes on the worker's own host, for
// development and tests.
//
// IT IS NOT A SECURITY BOUNDARY. The image is ignored: the step's command
// runs from the host's PATH, as the worker's user, with the worker's file
// system visible. What it does provide: a fresh temporary directory as the
// working and home directory, an environment holding only what the step is
// given, the step's timeout (the whole process group is killed), rlimits on
// memory, CPU time and file size where the platform has prlimit, and on
// Linux, where unprivileged user namespaces are allowed, a network
// namespace with no interfaces for network "none". Never use it where
// tenants you do not trust can publish workflows.
type Local struct {
	// Dir is where temporary directories are made; default os.TempDir().
	Dir    string
	Logger *slog.Logger

	warnOnce sync.Once
}

// Environment that enables the local runner: both must be set.
const (
	EnvRunner   = "TASKIEM_CONTAINER_RUNNER"
	EnvLocalDev = "TASKIEM_CONTAINER_LOCAL_DEV"
)

// NewLocal returns the local runner only when the environment asks for it
// twice: TASKIEM_CONTAINER_RUNNER=local and TASKIEM_CONTAINER_LOCAL_DEV=1.
func NewLocal(getenv func(string) string, logger *slog.Logger) (*Local, error) {
	if getenv(EnvRunner) != "local" {
		return nil, fmt.Errorf("the local container runner needs %s=local", EnvRunner)
	}
	if v := getenv(EnvLocalDev); v != "1" && v != "true" {
		return nil, fmt.Errorf("the local container runner is not a sandbox: it runs step commands on this host; set %s=1 to use it for development", EnvLocalDev)
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("container steps run on the LOCAL runner: commands run on this host, unsandboxed; for development only")
	return &Local{Logger: logger}, nil
}

func (l *Local) logger() *slog.Logger {
	if l.Logger == nil {
		return slog.Default()
	}
	return l.Logger
}

// Run runs the step's command on this host.
func (l *Local) Run(ctx context.Context, s Spec) (Result, error) {
	dir, err := os.MkdirTemp(l.Dir, "taskiem-container-")
	if err != nil {
		return Result{}, notStarted("temporary directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	in := filepath.Join(dir, "input.json")
	if err := os.WriteFile(in, s.Input, 0o600); err != nil {
		return Result{}, notStarted("input: %v", err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, "TASKIEM_INPUT=" + in, "TASKIEM_OUTPUT=" + filepath.Join(dir, "output.json")}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	if s.SecretsMode == "file" {
		sd := filepath.Join(dir, "secrets")
		if err := os.Mkdir(sd, 0o700); err != nil {
			return Result{}, notStarted("secrets: %v", err)
		}
		for k, v := range s.Secrets {
			if err := os.WriteFile(filepath.Join(sd, k), []byte(v), 0o400); err != nil {
				return Result{}, notStarted("secrets: %v", err)
			}
		}
		env = append(env, "TASKIEM_SECRETS="+sd)
	} else {
		for k, v := range s.Secrets {
			env = append(env, k+"="+v)
		}
	}
	if s.Proxy != "" {
		env = append(env, "HTTP_PROXY="+s.Proxy, "HTTPS_PROXY="+s.Proxy, "http_proxy="+s.Proxy, "https_proxy="+s.Proxy)
	}
	sup := Supervision{
		Command: append(append([]string(nil), s.Command...), s.Args...), Env: env, Dir: dir,
		OutputBytes: s.OutputBytes, Timeout: s.Timeout,
		Prepare: func(p *os.Process) error {
			if err := limitProcess(p.Pid, s); err != nil {
				l.warnOnce.Do(func() { l.logger().Warn("local container runner: rlimits not applied", "err", err) })
			}
			return nil
		},
	}
	if s.InputMode != "file" {
		sup.Stdin = bytes.NewReader(s.Input)
	}
	if s.OutputMode == "file" {
		sup.OutputPath = filepath.Join(dir, "output.json")
	}
	isolated := s.Proxy == "" && canIsolateNetwork()
	if isolated {
		sup.SysProc = isolateNetwork
	}
	o := Supervise(ctx, sup)
	if isolated && strings.HasPrefix(o.Error, "start: ") && isNamespaceError(o.Error) {
		// User namespaces are off on this host: run without one.
		l.warnOnce.Do(func() {
			l.logger().Warn("local container runner: no network namespace on this host; steps have the host's network")
		})
		disableIsolation()
		sup.SysProc = nil
		if s.InputMode != "file" {
			sup.Stdin = bytes.NewReader(s.Input)
		}
		o = Supervise(ctx, sup)
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Result{Logs: o.Logs, Elapsed: o.Elapsed}, unknown("container step cancelled")
	}
	return Settle(o, o.Elapsed)
}

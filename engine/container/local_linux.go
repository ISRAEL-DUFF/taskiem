//go:build linux

package container

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// noIsolation is set once creating a network namespace has failed.
var noIsolation atomic.Bool

func canIsolateNetwork() bool { return !noIsolation.Load() }

func disableIsolation() { noIsolation.Store(true) }

// isolateNetwork starts the program in new user and network namespaces:
// no interfaces but a down loopback, so no network at all.
func isolateNetwork(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}}
	cmd.SysProcAttr.GidMappingsEnableSetgroups = false
}

func isNamespaceError(msg string) bool {
	return strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "invalid argument") || strings.Contains(msg, "no space left")
}

// limitProcess sets rlimits on a started process: address space (the
// memory limit), CPU seconds (the timeout) and file size (64 MiB).
func limitProcess(pid int, s Spec) error {
	set := func(res int, v uint64) error {
		lim := syscall.Rlimit{Cur: v, Max: v}
		if _, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(pid), uintptr(res), uintptr(unsafe.Pointer(&lim)), 0, 0, 0); e != 0 { //nolint:gosec // prlimit(2) takes a pointer to the limit
			return fmt.Errorf("prlimit %d: %w", res, e)
		}
		return nil
	}
	if s.MemoryMB > 0 {
		if err := set(syscall.RLIMIT_AS, uint64(s.MemoryMB)<<20); err != nil {
			return err
		}
	}
	if s.Timeout > 0 {
		if err := set(syscall.RLIMIT_CPU, uint64(s.Timeout.Seconds())+1); err != nil {
			return err
		}
	}
	return set(syscall.RLIMIT_FSIZE, 64<<20)
}

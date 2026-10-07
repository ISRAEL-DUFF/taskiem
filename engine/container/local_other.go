//go:build !linux

package container

import (
	"errors"
	"os/exec"
)

func canIsolateNetwork() bool { return false }

func disableIsolation() {}

func isolateNetwork(*exec.Cmd) {}

func isNamespaceError(string) bool { return false }

func limitProcess(int, Spec) error { return errors.New("rlimits are only applied on Linux") }

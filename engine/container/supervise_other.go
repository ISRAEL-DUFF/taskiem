//go:build !unix

package container

import "os/exec"

func setProcessGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func signalOf(*exec.ExitError) string { return "" }

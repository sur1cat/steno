//go:build windows

package core

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

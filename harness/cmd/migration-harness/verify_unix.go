//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the verification command in its own process group.
// Without it, a timeout kills only the `sh -c` wrapper and leaves the build
// it started running in the pod — the per-command timeout would bound the
// harness's wait, not the work.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals the whole group, falling back to the single
// process when the group is not there to signal (the command exited between
// the deadline firing and this call).
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

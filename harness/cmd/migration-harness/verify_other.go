//go:build !unix

package main

import "os/exec"

// The harness ships in a Linux container; these keep the package building
// everywhere else, where only the command itself can be signalled.
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

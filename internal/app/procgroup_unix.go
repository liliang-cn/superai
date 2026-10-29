//go:build unix

package app

import (
	"os/exec"
	"syscall"
	"time"
)

// shellKillGrace is how long a command's process group gets, after SIGTERM, to
// exit before it is SIGKILLed.
var shellKillGrace = 2 * time.Second

// ownGroup puts the command in a process group of its own, so everything it
// starts can be signalled together.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	// When the context ends: ask the whole group to stop. WaitDelay bounds the
	// wait on output pipes a child still holds.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = shellKillGrace
}

// reapGroup is called once the command has returned because its context ended:
// whatever of its group is still there after the grace is SIGKILLed.
func reapGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	deadline := time.Now().Add(shellKillGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

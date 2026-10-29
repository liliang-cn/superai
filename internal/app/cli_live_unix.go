//go:build unix

package app

import (
	"os/exec"
	"syscall"
)

// detachedCommand runs argv in a session of its own, under a shell that
// writes the CLI's exit code to $SUPERLEO_CLI_EXIT when it ends: SuperLeo,
// which may not be its parent by then, reads how it ended from there.
func detachedCommand(argv []string, _ string) *exec.Cmd {
	script := `"$@"; c=$?; printf '%s\n' "$c" > "$SUPERLEO_CLI_EXIT.tmp" && mv -f "$SUPERLEO_CLI_EXIT.tmp" "$SUPERLEO_CLI_EXIT"; exit $c`
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "superleo-cli"}, argv...)...)
	// A session of its own: no terminal to hang up on it, and no signal to
	// SuperLeo's process group reaches it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// procAlive says whether pid is still a process leading its own group, as
// the shell detachedCommand starts does: a pid reused by something else
// after a reboot usually is not.
func procAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	pgid, err := syscall.Getpgid(pid)
	return err == nil && pgid == pid
}

// signalGroup asks the run's whole process group to stop, or kills it.
func signalGroup(pid int, kill bool) {
	sig := syscall.SIGINT
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}

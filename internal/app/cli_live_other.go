//go:build !unix

package app

import (
	"os"
	"os/exec"
)

// Without sessions and process groups the CLI is a plain child: SuperLeo
// stopping may take it along, and a run found without its exit code is
// resumed.
func detachedCommand(argv []string, _ string) *exec.Cmd {
	return exec.Command(argv[0], argv[1:]...)
}

func procAlive(pid int) bool { return false }

func signalGroup(pid int, kill bool) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

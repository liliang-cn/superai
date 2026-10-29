//go:build !unix

package app

import "os/exec"

func ownGroup(cmd *exec.Cmd)  {}
func reapGroup(cmd *exec.Cmd) {}

package app

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/liliang-cn/superai/internal/backend"
)

// Agents installed on this machine that can be added as workers: pi, hermes
// and openclaw, each with the command that asks it one thing and prints the
// answer. Added, one is a remote agent whose host is "local" (run here, no
// ssh), so it is asked, counted and drawn like any other.

// LocalWorkerCandidate is one agent found on this machine.
type LocalWorkerCandidate struct {
	// Name is the one it would be added under, the name after the @.
	Name    string   `json:"name"`
	About   string   `json:"about"`
	Command []string `json:"command"`
	// Added is whether a worker already runs this program here.
	Added bool `json:"added"`
}

var localWorkerKinds = []struct {
	bin, about string
	args       []string
}{
	{"pi", "pi — a coding agent on this machine.", []string{"-p", "{prompt}"}},
	{"hermes", "hermes — a general agent on this machine, with its own memory.", []string{"--oneshot", "{prompt}"}},
	{"openclaw", "openclaw — its embedded agent on this machine.", []string{"agent", "--local", "-m", "{prompt}"}},
}

// LocalWorkerCandidates lists the agents installed here that can be added
// as workers.
func (a *App) LocalWorkerCandidates() []LocalWorkerCandidate {
	a.mu.Lock()
	var have map[string]backend.RemoteAgent
	if a.settings != nil {
		have = a.settings.RemoteAgents.Agents
	}
	a.mu.Unlock()
	out := []LocalWorkerCandidate{}
	for _, k := range localWorkerKinds {
		path, err := exec.LookPath(k.bin)
		if err != nil {
			continue
		}
		if p, err := filepath.EvalSymlinks(path); err == nil && filepath.Base(p) == k.bin {
			path = p
		}
		cmd := append(interpreterOf(path), path)
		cmd = append(cmd, k.args...)
		c := LocalWorkerCandidate{Name: k.bin + "-" + machineLabel(), About: k.about, Command: cmd}
		for _, ra := range have {
			if onlyLocal(ra.Hosts) && runsProgram(ra.Command, path, k.bin) {
				c.Added = true
			}
		}
		out = append(out, c)
	}
	return out
}

// interpreterOf is what runs a script whose first line is
// "#!/usr/bin/env node", found on this process's PATH: the agent is started
// by sh with launchd's PATH, where node (from fnm or Homebrew) is not.
func interpreterOf(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) != 2 || filepath.Base(fields[0]) != "env" {
		return nil
	}
	if p, err := exec.LookPath(fields[1]); err == nil {
		return []string{p}
	}
	return nil
}

func runsProgram(cmd []string, path, bin string) bool {
	for _, part := range cmd {
		if part == path || part == bin || filepath.Base(part) == bin {
			return true
		}
		if part == "{prompt}" {
			break
		}
	}
	return false
}

// machineLabel is the short word a worker's name carries for this machine:
// pi-mac, hermes-win, openclaw-<host>.
func machineLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "win"
	}
	h, _ := os.Hostname()
	h = strings.ToLower(strings.SplitN(h, ".", 2)[0])
	if h == "" {
		return "local"
	}
	return h
}

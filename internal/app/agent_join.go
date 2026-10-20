package app

// `superai agent join`: this machine joins a core in one step. A pairing
// code from the core is traded for a token of the machine's own, the token
// and where to reach the core are kept in ~/.superai-agent, and `superai
// agent` is installed as a service of this login. From then on the core sees
// the machine: its signed-in coding CLIs and the agents it runs are workers,
// and so is the machine's own SuperAI once it has a model; without one it is
// only the core's hands there.
//
//	superai agent join <core address> <code> [-name n] [-link host:port] [-tls]
//	superai agent leave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superai/internal/backend"
)

var agentService = loginService{label: "cn.superleo.superai-agent", unit: "superai-agent.service", about: "SuperAI agent", log: "agent.log"}

// agentAdvertiseEnv is where a core says the address agents reach its link
// at, which it cannot see for itself behind a NodePort or a proxy; a join
// learns it from the claim.
const agentAdvertiseEnv = "SUPERAI_AGENT_ADVERTISE"

func agentHome() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".superai-agent")
}

// agentJoinConf is what a join chose, so `superai agent` run with no flags
// (as the service runs it) links the same way.
type agentJoinConf struct {
	Core string `json:"core"`
	Name string `json:"name"`
	TLS  bool   `json:"tls,omitempty"`
}

const agentTokenFile = "core-token"

// joinedOptions is how to link from what a join kept, if one was made here.
func joinedOptions() (AgentLinkOptions, bool) {
	home := agentHome()
	raw, err := os.ReadFile(filepath.Join(home, "agent.json"))
	if err != nil {
		return AgentLinkOptions{}, false
	}
	var c agentJoinConf
	if json.Unmarshal(raw, &c) != nil || c.Core == "" {
		return AgentLinkOptions{}, false
	}
	tok, _ := os.ReadFile(filepath.Join(home, agentTokenFile))
	if os.Getenv("SUPERAI_HOME") == "" {
		_ = os.Setenv("SUPERAI_HOME", home)
	}
	return AgentLinkOptions{Core: c.Core, Token: strings.TrimSpace(string(tok)), Name: c.Name, TLS: c.TLS, Version: buildVersion()}, true
}

func agentJoin(argv []string) error {
	fs := flag.NewFlagSet("agent join", flag.ExitOnError)
	name := fs.String("name", "", "what the core calls this machine; the host name when empty")
	link := fs.String("link", "", "the core's agent link, host:port, when the core does not say it")
	useTLS := fs.Bool("tls", false, "dial the agent link with TLS")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: superai agent join <core address> <pairing code> [-name n] [-link host:port] [-tls]")
		fs.PrintDefaults()
	}
	// The address and the code first, the flags after, as people type it.
	var pos []string
	rest := argv
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos, rest = append(pos, rest[0]), rest[1:]
	}
	_ = fs.Parse(rest)
	pos = append(pos, fs.Args()...)
	if len(pos) != 2 {
		fs.Usage()
		os.Exit(2)
	}
	base, err := normaliseSuperAIURL(pos[0])
	if err != nil {
		return err
	}
	code := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, pos[1])
	if len(code) != 6 {
		return errors.New("the pairing code is six digits")
	}
	n := strings.TrimSpace(*name)
	if n == "" {
		h, _ := os.Hostname()
		n = strings.Split(h, ".")[0]
	}
	if !validLinkName(n) {
		return fmt.Errorf("%q cannot be a machine's name: use letters, digits, - _ or .", n)
	}

	tok, advertised, err := claimAgentToken(base, code, "agent "+n)
	if err != nil {
		return err
	}
	core := strings.TrimSpace(*link)
	if core == "" {
		core = advertised
	}
	if core == "" {
		return fmt.Errorf("paired, but %s does not say where agents reach it: run again with a new code and -link host:port", base)
	}

	home := agentHome()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(home, agentTokenFile), []byte(tok+"\n"), 0o600); err != nil {
		return err
	}
	raw, _ := json.Marshal(agentJoinConf{Core: core, Name: n, TLS: *useTLS})
	if err := os.WriteFile(filepath.Join(home, "agent.json"), raw, 0o600); err != nil {
		return err
	}

	// The service runs a copy at a fixed path, as a node's does.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(home, "bin", filepath.Base(self))
	if err := copyExecutable(self, bin); err != nil {
		return fmt.Errorf("copying the binary: %w", err)
	}

	// The coding CLIs, by the full path the login shell finds them at: a
	// service's PATH is bare.
	env := loginEnv()
	_ = os.Setenv("SUPERAI_HOME", home)
	_ = os.Setenv("PATH", env["PATH"])
	s, err := backend.LoadSettings()
	if err != nil {
		return err
	}
	bins := map[string]string{}
	for _, a := range agentexec.Discover(nil) {
		if p, err := exec.LookPath(a.Binary); err == nil {
			bins[a.Name] = p
		}
	}
	s.ExternalAgents.Enabled = true
	s.ExternalAgents.Binaries = bins
	if err := s.Save(); err != nil {
		return err
	}

	args := []string{bin, "agent"}
	switch runtime.GOOS {
	case "darwin":
		err = agentService.installLaunchd(home, args, env)
	case "linux":
		err = agentService.installSystemd(home, args, env)
	case "windows":
		err = installWindowsTask(agentService.label, args)
	default:
		err = fmt.Errorf("%s is not supported; run %s by hand", runtime.GOOS, strings.Join(args, " "))
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s joined %s as %q and starts with this login.\n", n, base, n)
	var names []string
	for c := range bins {
		names = append(names, c)
	}
	if len(names) > 0 {
		fmt.Printf("Coding agents here: %s. The signed-in ones are workers now.\n", strings.Join(names, ", "))
	}
	fmt.Println("With a model of its own (set under Settings in", home+"), this machine's SuperAI is a worker too.")
	return nil
}

// claimAgentToken trades a pairing code for a token of this machine's own,
// and returns where the core says agents reach its link, if it says.
func claimAgentToken(base, code, device string) (token, link string, err error) {
	body, _ := json.Marshal(map[string]string{"code": code, "device": device})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+pairClaimPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cliRemoteHTTP.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var got struct {
		Token     string `json:"token"`
		AgentLink string `json:"agent_link"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(raw, &got)
	if resp.StatusCode != http.StatusOK || got.Token == "" {
		if got.Error != "" {
			return "", "", fmt.Errorf("%s: %s", base, got.Error)
		}
		return "", "", fmt.Errorf("%s refused the code: %s", base, resp.Status)
	}
	return got.Token, strings.TrimSpace(got.AgentLink), nil
}

// installWindowsTask runs args at every logon of this user, and now. The
// binary is a windowed one, so nothing flashes on the screen.
func installWindowsTask(name string, args []string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = a
		if strings.ContainsAny(a, " \t") {
			quoted[i] = `"` + a + `"`
		}
	}
	if out, err := exec.Command("schtasks", "/Create", "/TN", name, "/TR", strings.Join(quoted, " "), "/SC", "ONLOGON", "/RL", "LIMITED", "/F").CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %v: %s (an administrator's terminal may be needed)", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("schtasks", "/End", "/TN", name).Run()
	if out, err := exec.Command("schtasks", "/Run", "/TN", name).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// agentLeave stops the service a join installed and forgets the core.
func agentLeave() error {
	home := agentHome()
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentService.label)).Run()
		_ = os.Remove(agentService.launchdPlist())
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", agentService.unit).Run()
		_ = os.Remove(agentService.systemdUnit())
	case "windows":
		_ = exec.Command("schtasks", "/End", "/TN", agentService.label).Run()
		_ = exec.Command("schtasks", "/Delete", "/TN", agentService.label, "/F").Run()
	}
	_ = os.Remove(filepath.Join(home, agentTokenFile))
	_ = os.Remove(filepath.Join(home, "agent.json"))
	fmt.Println("Left. The core still lists this machine's device until it is unpaired there.")
	return nil
}

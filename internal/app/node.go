package app

// `superai-desktop node`: this machine as an agent node — the way an ops agent
// is installed on a host. It runs SuperAI headless as a login service (launchd
// on macOS, a systemd user unit on Linux) with External agents on, so another
// SuperAI — a hive queen, a desktop — can link it with a pairing code and drive
// the Claude Code and Codex installed here as "claude.<name>".
//
//	superai-desktop node install [-port 43779] [-roots ~/code,~/work] [-unattended]
//	superai-desktop node pair        a fresh six-digit code
//	superai-desktop node status
//	superai-desktop node uninstall [-purge]

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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

const (
	nodeLabel       = "cn.superleo.superai-node"
	nodeDefaultPort = 43779
)

// NodeMain is `superai-desktop node …`.
func NodeMain(argv []string) {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: superai-desktop node install|pair|status|uninstall")
		os.Exit(2)
	}
	var err error
	switch argv[0] {
	case "install":
		err = nodeInstall(argv[1:])
	case "pair":
		err = nodePair()
	case "status":
		err = nodeStatus()
	case "uninstall":
		err = nodeUninstall(argv[1:])
	default:
		err = fmt.Errorf("unknown command %q", argv[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func nodeHome() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".superai-node")
}

// nodeConf is what install chose, so pair and status find the node again.
type nodeConf struct {
	Port int    `json:"port"`
	Home string `json:"home"`
}

func readNodeConf() (nodeConf, error) {
	raw, err := os.ReadFile(filepath.Join(nodeHome(), "node.json"))
	if err != nil {
		return nodeConf{}, errors.New("no node installed here; run: superai-desktop node install")
	}
	var c nodeConf
	err = json.Unmarshal(raw, &c)
	return c, err
}

func nodeInstall(argv []string) error {
	fs := flag.NewFlagSet("node install", flag.ExitOnError)
	port := fs.Int("port", nodeDefaultPort, "port to listen on, on every interface")
	roots := fs.String("roots", "", "comma-separated directories the agents may work in, besides the node's workspace")
	unattended := fs.Bool("unattended", false, "let the agents run tools without asking anyone (not recommended)")
	_ = fs.Parse(argv)

	home := nodeHome()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o700); err != nil {
		return err
	}
	// The service runs a copy at a fixed path: what the plist names must not
	// move when this binary is upgraded or deleted, and on macOS permissions
	// are granted to an executable by its path.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(home, "bin", "superai")
	if err := copyExecutable(self, bin); err != nil {
		return fmt.Errorf("copying the binary: %w", err)
	}

	// A login service gets a bare environment. The PATH and proxy settings
	// of the login shell go into the unit, so `claude` and `codex` are found
	// and reach their APIs the way they do from a terminal.
	env := loginEnv()

	// Settings: External agents on, each CLI by its full path.
	_ = os.Setenv("SUPERAI_DESKTOP_HOME", home)
	s, err := backend.LoadSettings()
	if err != nil {
		return err
	}
	_ = os.Setenv("PATH", env["PATH"])
	bins := map[string]string{}
	for _, a := range agentexec.Discover(nil) {
		if p, err := exec.LookPath(a.Binary); err == nil {
			bins[a.Name] = p
		}
	}
	var rs []string
	for _, r := range strings.Split(*roots, ",") {
		if r = strings.TrimSpace(r); r != "" {
			if abs, err := filepath.Abs(cliExpandHome(r)); err == nil {
				rs = append(rs, abs)
			}
		}
	}
	s.ExternalAgents = backend.ExternalAgents{Enabled: true, Unattended: *unattended, Roots: rs, Binaries: bins}
	if err := s.Save(); err != nil {
		return err
	}
	raw, _ := json.Marshal(nodeConf{Port: *port, Home: home})
	if err := os.WriteFile(filepath.Join(home, "node.json"), raw, 0o600); err != nil {
		return err
	}

	args := []string{bin, "serve", "-port", fmt.Sprint(*port), "-bind", "0.0.0.0"}
	switch runtime.GOOS {
	case "darwin":
		err = installLaunchd(home, args, env)
	case "linux":
		err = installSystemd(home, args, env)
	default:
		err = fmt.Errorf("%s is not supported; run %s by hand", runtime.GOOS, strings.Join(args, " "))
	}
	if err != nil {
		return err
	}
	if err := waitNode(*port, 60*time.Second); err != nil {
		return fmt.Errorf("installed, but it did not come up: %w (log: %s)", err, filepath.Join(home, "serve.log"))
	}

	fmt.Println("SuperAI node is running and starts with this login.")
	names := []string{}
	for n := range bins {
		names = append(names, n)
	}
	if len(names) == 0 {
		fmt.Println("No agent CLI was found. Install Claude Code or Codex, log in, then run `node install` again.")
	} else {
		fmt.Printf("Agents: %s\n", strings.Join(names, ", "))
	}
	return nodePair()
}

func copyExecutable(src, dst string) error {
	if same, _ := filepath.EvalSymlinks(src); same == dst {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// loginEnv is PATH and the proxy variables as the user's login shell has them.
func loginEnv() map[string]string {
	keys := []string{"PATH", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "all_proxy", "no_proxy"}
	out := map[string]string{}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	var script strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&script, `printf '%%s=%%s\0' %s "$%s";`, k, k)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if raw, err := exec.CommandContext(ctx, shell, "-lc", script.String()).Output(); err == nil {
		for _, kv := range strings.Split(string(raw), "\x00") {
			if k, v, ok := strings.Cut(kv, "="); ok && v != "" {
				out[k] = v
			}
		}
	}
	for _, k := range keys {
		if out[k] == "" && os.Getenv(k) != "" {
			out[k] = os.Getenv(k)
		}
	}
	if out["PATH"] == "" {
		out["PATH"] = "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"
	}
	return out
}

func launchdPlist() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", nodeLabel+".plist")
}

func installLaunchd(home string, args []string, env map[string]string) error {
	env["SUPERAI_DESKTOP_HOME"] = home
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + nodeLabel + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("  </array>\n  <key>EnvironmentVariables</key>\n  <dict>\n")
	for k, v := range env {
		fmt.Fprintf(&b, "    <key>%s</key><string>%s</string>\n", xmlEscape(k), xmlEscape(v))
	}
	log := filepath.Join(home, "serve.log")
	fmt.Fprintf(&b, `  </dict>
  <key>WorkingDirectory</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, xmlEscape(home), xmlEscape(log), xmlEscape(log))

	p := launchdPlist()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+nodeLabel).Run() // a previous install
	time.Sleep(time.Second)
	if out, err := exec.Command("launchctl", "bootstrap", domain, p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func systemdUnit() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "systemd", "user", "superai-node.service")
}

func installSystemd(home string, args []string, env map[string]string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("no systemd here (a container?): start it with your own supervisor:\n  SUPERAI_DESKTOP_HOME=%s %s", home, strings.Join(args, " "))
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=SuperAI node\nAfter=network-online.target\n\n[Service]\n")
	fmt.Fprintf(&b, "Environment=SUPERAI_DESKTOP_HOME=%s\n", home)
	for k, v := range env {
		fmt.Fprintf(&b, "Environment=%q\n", k+"="+v)
	}
	fmt.Fprintf(&b, "WorkingDirectory=%s\nExecStart=%s\nRestart=always\nRestartSec=3\n\n[Install]\nWantedBy=default.target\n",
		home, strings.Join(args, " "))
	p := systemdUnit()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		return err
	}
	for _, c := range [][]string{{"daemon-reload"}, {"enable", "--now", "superai-node.service"}, {"restart", "superai-node.service"}} {
		if out, err := exec.Command("systemctl", append([]string{"--user"}, c...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(c, " "), err, strings.TrimSpace(string(out)))
		}
	}
	// Without lingering, a user unit stops at logout: wrong for a node.
	if u := os.Getenv("USER"); u != "" {
		_ = exec.Command("loginctl", "enable-linger", u).Run()
	}
	return nil
}

func waitNode(port int, d time.Duration) error {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("no answer on port " + fmt.Sprint(port))
}

// nodeRPC calls the node's own API with its own key.
func nodeRPC(c nodeConf, method string, out any) error {
	raw, err := os.ReadFile(filepath.Join(c.Home, "auth.json"))
	if err != nil {
		return fmt.Errorf("the node has not started yet: %w", err)
	}
	var cred struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(raw, &cred)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/rpc/%s", c.Port, method), strings.NewReader("[]"))
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("the node is not running: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", method, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func nodePair() error {
	c, err := readNodeConf()
	if err != nil {
		return err
	}
	var code PairCode
	if err := nodeRPC(c, "PairPhone", &code); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("To link this node, on the other SuperAI open Settings › Runtime › Other SuperAIs and enter:")
	for _, a := range lanAddresses(c.Port) {
		fmt.Printf("  address  %s\n", a)
	}
	fmt.Printf("  code     %s %s   (valid until %s)\n", code.Code[:3], code.Code[3:], code.ExpiresAt.Local().Format("15:04"))
	fmt.Printf("Its agents are then @claude.<name>, @codex.<name> there. A new code: %s node pair\n",
		filepath.Join(c.Home, "bin", "superai"))
	return nil
}

func lanAddresses(port int) []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		// 198.18.0.0/15 is a proxy's TUN device (Clash, Surge), not an
		// address anyone else can reach.
		if ip4 := ipn.IP.To4(); ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d", ipn.IP, port))
	}
	if len(out) == 0 {
		out = []string{fmt.Sprintf("<this machine's address>:%d", port)}
	}
	return out
}

func nodeStatus() error {
	c, err := readNodeConf()
	if err != nil {
		return err
	}
	var st []backend.ExternalAgentStatus
	if err := nodeRPC(c, "ExternalAgentsStatus", &st); err != nil {
		return err
	}
	fmt.Printf("running on port %d, data in %s\n", c.Port, c.Home)
	for _, a := range st {
		if a.Installed {
			fmt.Printf("  agent  %-14s %s\n", a.Name, a.Version)
		}
	}
	var devs []PairedDevice
	if err := nodeRPC(c, "PairedDevices", &devs); err == nil {
		for _, d := range devs {
			fmt.Printf("  linked %s (last seen %s)\n", d.Name, d.LastSeen.Local().Format("2006-01-02 15:04"))
		}
	}
	return nil
}

func nodeUninstall(argv []string) error {
	fs := flag.NewFlagSet("node uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the node's data, keys and workspace")
	_ = fs.Parse(argv)
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), nodeLabel)).Run()
		_ = os.Remove(launchdPlist())
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "superai-node.service").Run()
		_ = os.Remove(systemdUnit())
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	if *purge {
		if err := os.RemoveAll(nodeHome()); err != nil {
			return err
		}
		fmt.Println("node removed, with its data")
		return nil
	}
	fmt.Println("node stopped and removed from login; data kept in", nodeHome())
	return nil
}

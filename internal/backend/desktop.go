package backend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Desktop control: the person's own apps, operated for them.
//
// agent-desktop (lahfir, Apache-2.0) is a Rust CLI that reads an app's
// accessibility tree and acts on it: every element a snapshot lists has a ref,
// and a click or a typed text goes to that element through the accessibility
// API. It does that without moving the pointer, taking the focus or sending
// keys through the system — the person keeps using the machine while it
// works. It has no MCP server, so its commands are tools here, each one run
// of the binary.
//
// Like the browser it is opt-in, fetched from the project's GitHub release
// for this platform and checked against the digest pinned here, and it never
// gets --headed: no physical input, ever. Some apps are not its to read at
// all (desktopBlocked), and quitting an app is not offered.

// AgentDesktopVersion is the agent-desktop release SuperLeo runs.
const AgentDesktopVersion = "1.0.1"

// agentDesktopAssets are the release archives by GOOS/GOARCH and their
// SHA-256 from the release's checksums.txt. An archive that does not match is
// discarded before anything in it is unpacked.
var agentDesktopAssets = map[string][2]string{
	"darwin/arm64":  {"agent-desktop-v1.0.1-aarch64-apple-darwin.tar.gz", "c80155f7dc4e1bd99e46492407d245a57f8f1016481bbb4d6ee468f4f827d64d"},
	"darwin/amd64":  {"agent-desktop-v1.0.1-x86_64-apple-darwin.tar.gz", "4b7c453ba2dc69bf847c1aa5fae2e5389c0752178dd9f49ff3c48b44775c9b5f"},
	"linux/arm64":   {"agent-desktop-v1.0.1-aarch64-unknown-linux-gnu.tar.gz", "da08ae5a365004813133455b260c293b79712dfe1699f792edd5a668fc87a5ba"},
	"linux/amd64":   {"agent-desktop-v1.0.1-x86_64-unknown-linux-gnu.tar.gz", "56b5c66d797c4f8bceff01bbb74c4e288383e0f749225a1c65398fc3afae7513"},
	"windows/arm64": {"agent-desktop-v1.0.1-aarch64-pc-windows-msvc.tar.gz", "2bff829d25ab9eccd936aee2782fd6c9361d647adb25928dfbba4c497d43cdfd"},
	"windows/amd64": {"agent-desktop-v1.0.1-x86_64-pc-windows-msvc.tar.gz", "b5a382622470828250bd6938f9d5164115a9049d9181eff6d86944eebe631054"},
}

var agentDesktopURL = func(asset string) string {
	return fmt.Sprintf("https://github.com/lahfir/agent-desktop/releases/download/v%s/%s", AgentDesktopVersion, asset)
}

// desktopControl is whether the person turned desktop control on.
// SUPERLEO_DESKTOP wins over the file, as SUPERLEO_BROWSER_MODE does.
func (s *Settings) desktopControl() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUPERLEO_DESKTOP"))) {
	case "on", "1", "true":
		return true
	case "off", "0", "false":
		return false
	}
	return s.DesktopControl
}

// desktopBlockedDefault are the apps desktop control may not read or touch
// unless the person lists their own: messages and mail are other people's
// words, password stores are secrets, a terminal runs whatever is typed into
// it, and System Settings changes the machine itself.
var desktopBlockedDefault = []string{
	"WeChat", "微信", "Mail", "邮件", "Messages", "信息", "Telegram", "Slack",
	"1Password", "Bitwarden", "Keychain Access", "钥匙串访问", "Passwords", "密码",
	"Terminal", "终端", "iTerm2", "Ghostty", "Warp", "Otty", "kitty", "Alacritty",
	"System Settings", "系统设置",
}

func (s *Settings) desktopBlocked() []string {
	if s.DesktopBlockedApps != nil {
		return s.DesktopBlockedApps
	}
	return desktopBlockedDefault
}

// DesktopStatus is what the settings page shows of desktop control.
type DesktopStatus struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
	// Accessibility is the OS permission as agent-desktop reads it:
	// "granted", "denied", "unknown", or "" before it has been asked.
	Accessibility string   `json:"accessibility,omitempty"`
	Blocked       []string `json:"blocked"`
	Error         string   `json:"error,omitempty"`
}

type desktopRuntime struct {
	bin     string
	home    string
	blocked []string
	ready   atomic.Bool
	cancel  context.CancelFunc

	mu     sync.Mutex
	err    string
	access string
	// snaps maps a snapshot id to the app it was taken of. A ref names its
	// snapshot, so an action on a ref is only taken when this service took
	// that snapshot itself, of an app it may touch.
	snaps map[string]string
}

func (d *desktopRuntime) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.err = ""
		return
	}
	d.err = err.Error()
}

// DesktopStatus reports desktop control on this service.
func (s *Service) DesktopStatus() DesktopStatus {
	st := DesktopStatus{Version: AgentDesktopVersion, Blocked: desktopBlockedDefault}
	if s == nil {
		return st
	}
	if s.settings != nil {
		st.Blocked = s.settings.desktopBlocked()
	}
	d := s.desktop
	if d == nil {
		return st
	}
	st.Enabled = true
	st.Ready = d.ready.Load()
	d.mu.Lock()
	st.Error, st.Accessibility = d.err, d.access
	d.mu.Unlock()
	return st
}

func (s *Service) startDesktop(blocked []string) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &desktopRuntime{
		home:    filepath.Join(DataDir(), "agent-desktop"),
		blocked: blocked,
		cancel:  cancel,
		snaps:   map[string]string{},
	}
	s.desktop = d
	go func() {
		bin, err := EnsureAgentDesktop(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("superleo: desktop control: %v", err)
				d.setErr(err)
			}
			return
		}
		d.bin = bin
		if s.svc == nil {
			d.setErr(errors.New("no agent to give desktop control to"))
			return
		}
		// The permission is the OS's and the person grants it; without it
		// every call fails, so the page says so instead of each tool call.
		if out, err := d.run(ctx, "permissions"); err == nil {
			var env struct {
				Data struct {
					Accessibility struct {
						State string `json:"state"`
					} `json:"accessibility"`
				} `json:"data"`
			}
			if json.Unmarshal(out, &env) == nil {
				d.mu.Lock()
				d.access = env.Data.Accessibility.State
				d.mu.Unlock()
			}
		}
		s.registerDesktopTools(d)
		d.setErr(nil)
		d.ready.Store(true)
		log.Printf("superleo: desktop control %s ready", AgentDesktopVersion)
	}()
}

func (s *Service) stopDesktop() {
	if s != nil && s.desktop != nil && s.desktop.cancel != nil {
		s.desktop.cancel()
	}
}

// AgentDesktopDir is where this version is unpacked.
func AgentDesktopDir() string {
	return filepath.Join(DataDir(), "bin", "agent-desktop-"+AgentDesktopVersion)
}

var agentDesktopFetch sync.Mutex

// EnsureAgentDesktop returns the agent-desktop binary for this platform,
// downloading and unpacking the release archive the first time. The archive's
// digest is recorded beside what was unpacked from it, so a later start that
// finds the same digest there trusts the directory without fetching again.
func EnsureAgentDesktop(ctx context.Context) (string, error) {
	a, ok := agentDesktopAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("agent-desktop publishes no binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	asset, want := a[0], a[1]
	agentDesktopFetch.Lock()
	defer agentDesktopFetch.Unlock()
	dir := AgentDesktopDir()
	bin := filepath.Join(dir, "agent-desktop")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ".sha256")); strings.TrimSpace(string(got)) == want {
		if _, err := os.Stat(bin); err == nil {
			return bin, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentDesktopURL(asset), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download agent-desktop: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download agent-desktop: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", fmt.Errorf("download agent-desktop: %w", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("agent-desktop %s: digest %s, want %s; not installed", asset, got, want)
	}
	tmp := dir + ".unpack"
	_ = os.RemoveAll(tmp)
	if err := unpackTarGz(raw, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("unpack agent-desktop: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".sha256"), []byte(want+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("agent-desktop archive has no %s", filepath.Base(bin))
	}
	log.Printf("superleo: agent-desktop %s installed at %s", AgentDesktopVersion, dir)
	return bin, nil
}

// unpackTarGz writes the regular files of a flat archive into dir. Anything
// with a directory in its name, a link or a device is refused: the release
// archives are a binary or two side by side, and nothing else is expected.
func unpackTarGz(raw []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir && (name == "" || name == ".") {
			continue
		}
		if h.Typeflag != tar.TypeReg || name == "" || strings.ContainsAny(name, `/\`) || name == ".." {
			return fmt.Errorf("unexpected entry %q", h.Name)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, 64<<20))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// run is one agent-desktop command. Its answer is a JSON envelope on stdout
// whether the command worked or not; a failed one is still returned, with
// the error only when there was no envelope at all.
func (d *desktopRuntime) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Env = append(os.Environ(), "AGENT_DESKTOP_HOME="+d.home)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if b := bytes.TrimSpace(out.Bytes()); len(b) > 0 && b[0] == '{' {
		return b, nil
	}
	if err == nil {
		err = errors.New("no answer")
	}
	return nil, fmt.Errorf("agent-desktop %s: %v %s", args[0], err, strings.TrimSpace(errb.String()))
}

// blockedName reports whether an app name is one desktop control leaves
// alone, matched without case either way round, so "wechat", "微信" and
// "WeChat Helper" all count.
func (d *desktopRuntime) blockedName(app string) bool {
	a := strings.ToLower(strings.TrimSpace(app))
	if a == "" {
		return false
	}
	for _, b := range d.blocked {
		b = strings.ToLower(strings.TrimSpace(b))
		if b != "" && (strings.Contains(a, b) || strings.Contains(b, a)) {
			return true
		}
	}
	return false
}

var desktopRefRE = regexp.MustCompile(`@([A-Za-z0-9]+):e[0-9]+`)

// remember files the snapshot ids an answer hands out under the app it was
// about, so the refs in it can be acted on.
func (d *desktopRuntime) remember(app string, out []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.snaps) > 512 {
		d.snaps = map[string]string{}
	}
	for _, m := range desktopRefRE.FindAllSubmatch(out, -1) {
		d.snaps[string(m[1])] = app
	}
}

// refApp is the app of a ref this service handed out, or an error that
// tells the model what to do instead.
func (d *desktopRuntime) refApp(ref string) (string, error) {
	m := desktopRefRE.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil || m[0] != strings.TrimSpace(ref) {
		return "", fmt.Errorf("%q is not a ref: pass one as desktop_snapshot printed it, like @s8f3k2p9:e5", ref)
	}
	d.mu.Lock()
	app, ok := d.snaps[m[1]]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("ref %s is from no snapshot taken here: take one with desktop_snapshot and use its refs", ref)
	}
	return app, nil
}

// envelopeApp is the app an answer says it was about (data.app).
func envelopeApp(out []byte) string {
	var env struct {
		Data struct {
			App string `json:"app"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	return env.Data.App
}

// quitCombos are the key combos that close an app or a window outright.
// press reaches an app without focusing it, so cmd+q there would quit
// whatever the person had open in it.
var quitCombos = map[string]bool{
	"cmd+q": true, "cmd+shift+q": true, "cmd+alt+q": true, "cmd+option+q": true,
	"cmd+w": true, "cmd+shift+w": true, "cmd+alt+w": true, "cmd+option+w": true,
	"alt+f4": true, "ctrl+q": true, "ctrl+w": true, "ctrl+shift+q": true, "ctrl+shift+w": true,
	"cmd+alt+escape": true, "cmd+option+escape": true, "ctrl+alt+delete": true, "cmd+ctrl+q": true,
}

func normalCombo(c string) string {
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(c, " ", "")), "+")
	for i, p := range parts {
		switch p {
		case "command", "meta", "super", "win":
			parts[i] = "cmd"
		case "option", "opt":
			parts[i] = "alt"
		case "control":
			parts[i] = "ctrl"
		case "esc":
			parts[i] = "escape"
		}
	}
	return strings.Join(parts, "+")
}

func isQuitCombo(c string) bool {
	n := normalCombo(c)
	if quitCombos[n] {
		return true
	}
	// The same keys in another order.
	parts := strings.Split(n, "+")
	for k := range quitCombos {
		kp := strings.Split(k, "+")
		if len(kp) != len(parts) {
			continue
		}
		same := true
		for _, p := range parts {
			found := false
			for _, q := range kp {
				if p == q {
					found = true
				}
			}
			same = same && found
		}
		if same {
			return true
		}
	}
	return false
}

// desktopOutputLimit keeps one answer inside what a turn can carry; a full
// snapshot of a busy app runs to hundreds of kilobytes.
const desktopOutputLimit = 60 << 10

func desktopAnswer(out []byte) any {
	if len(out) > desktopOutputLimit {
		return string(out[:desktopOutputLimit]) + "\n… cut here: snapshot a smaller part with root, or use desktop_find"
	}
	return string(out)
}

func (s *Service) registerDesktopTools(d *desktopRuntime) {
	svc := s.svc
	read := agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
	act := agent.ToolMetadata{InterruptBehavior: agent.InterruptBehaviorBlock}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	refused := func(app string) map[string]any {
		return errResult(fmt.Sprintf("%s is not desktop control's to read or operate: the person keeps it to themselves. Do it another way, or ask them.", app))
	}
	// onRef runs a command on a ref this service handed out.
	onRef := func(ctx context.Context, ref string, args ...string) (any, error) {
		app, err := d.refApp(ref)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if d.blockedName(app) {
			return refused(app), nil
		}
		out, err := d.run(ctx, args...)
		if err != nil {
			return errResult(err.Error()), nil
		}
		d.remember(app, out)
		return desktopAnswer(out), nil
	}

	svc.AddToolWithMetadata("desktop_windows",
		"List the open windows of the desktop apps on this machine (app, title, window id). Start here to find the app to work in.",
		obj(map[string]any{"app": str("Only this app's windows (optional)")}),
		func(ctx context.Context, a map[string]any) (any, error) {
			args := []string{"list-windows"}
			if app := argStr(a, "app"); app != "" {
				if d.blockedName(app) {
					return refused(app), nil
				}
				args = append(args, "--app", app)
			}
			out, err := d.run(ctx, args...)
			if err != nil {
				return errResult(err.Error()), nil
			}
			var env struct {
				OK    bool             `json:"ok"`
				Data  []map[string]any `json:"data"`
				Error json.RawMessage  `json:"error"`
			}
			if json.Unmarshal(out, &env) != nil || !env.OK {
				return desktopAnswer(out), nil
			}
			var list []map[string]any
			for _, w := range env.Data {
				name, _ := w["app_name"].(string)
				if d.blockedName(name) {
					continue
				}
				list = append(list, map[string]any{"app": name, "title": w["title"], "window_id": w["id"], "focused": w["is_focused"], "minimized": w["minimized"]})
			}
			return okData(map[string]any{"windows": list}), nil
		}, read)

	svc.AddToolWithMetadata("desktop_snapshot",
		"Read a desktop app's window as its accessibility tree: every element you can act on has a ref like @s8f3k2p9:e5. Use the app name as desktop_windows shows it (a Chinese system names apps in Chinese). For a big app take skeleton first, then root on one region. Refs go stale when the window changes: snapshot again after acting to check what happened.",
		obj(map[string]any{
			"app":       str("App name, as desktop_windows shows it"),
			"window_id": str("One window of the app, from desktop_windows (optional)"),
			"root":      str("Start from this ref instead of the window (optional)"),
			"skeleton":  map[string]any{"type": "boolean", "description": "A shallow map with child counts on cut branches"},
		}, "app"),
		func(ctx context.Context, a map[string]any) (any, error) {
			app := argStr(a, "app")
			if app == "" {
				return errResult("app is required"), nil
			}
			if d.blockedName(app) {
				return refused(app), nil
			}
			args := []string{"snapshot", "--app", app, "-i", "--compact"}
			if w := argStr(a, "window_id"); w != "" {
				args = append(args, "--window-id", w)
			}
			if r := argStr(a, "root"); r != "" {
				if _, err := d.refApp(r); err != nil {
					return errResult(err.Error()), nil
				}
				args = append(args, "--root", r)
			}
			if argBool(a, "skeleton") {
				args = append(args, "--skeleton")
			}
			out, err := d.run(ctx, args...)
			if err != nil {
				return errResult(err.Error()), nil
			}
			// The app it resolved to, which a bundle id or a partial name
			// can hide from the check above.
			if got := envelopeApp(out); got != "" && d.blockedName(got) {
				return refused(got), nil
			}
			d.remember(app, out)
			return desktopAnswer(out), nil
		}, read)

	svc.AddToolWithMetadata("desktop_find",
		"Find elements in a desktop app by role, name or text, when you know what you are looking for; returns refs like desktop_snapshot.",
		obj(map[string]any{
			"app":   str("App name"),
			"role":  str("button, textfield, checkbox, menuitem… (optional)"),
			"name":  str("Accessible name or label (optional)"),
			"text":  str("Text in its name, value, title or description (optional)"),
			"exact": map[string]any{"type": "boolean", "description": "Match the name exactly"},
		}, "app"),
		func(ctx context.Context, a map[string]any) (any, error) {
			app := argStr(a, "app")
			if app == "" {
				return errResult("app is required"), nil
			}
			if d.blockedName(app) {
				return refused(app), nil
			}
			args := []string{"find", "--app", app}
			for _, k := range []string{"role", "name", "text"} {
				if v := argStr(a, k); v != "" {
					args = append(args, "--"+k, v)
				}
			}
			if argBool(a, "exact") {
				args = append(args, "--exact")
			}
			out, err := d.run(ctx, args...)
			if err != nil {
				return errResult(err.Error()), nil
			}
			d.remember(app, out)
			return desktopAnswer(out), nil
		}, read)

	svc.AddToolWithMetadata("desktop_get",
		"Read one element: text (what a person reads), value, title, role, states or bounds.",
		obj(map[string]any{"ref": str("Ref from desktop_snapshot"), "property": str("text (default), value, title, role, states or bounds")}, "ref"),
		func(ctx context.Context, a map[string]any) (any, error) {
			args := []string{"get", argStr(a, "ref")}
			if p := argStr(a, "property"); p != "" {
				args = append(args, "--property", p)
			}
			return onRef(ctx, argStr(a, "ref"), args...)
		}, read)

	svc.AddToolWithMetadata("desktop_click",
		"Press an element (a button, a link, a menu item) through the accessibility API. The pointer does not move and the app does not come to the front.",
		obj(map[string]any{"ref": str("Ref from desktop_snapshot")}, "ref"),
		func(ctx context.Context, a map[string]any) (any, error) {
			return onRef(ctx, argStr(a, "ref"), "click", argStr(a, "ref"))
		}, act)

	svc.AddToolWithMetadata("desktop_type",
		"Insert text into a text field or text area at its ref.",
		obj(map[string]any{"ref": str("Ref of the text field"), "text": str("Text to insert")}, "ref", "text"),
		func(ctx context.Context, a map[string]any) (any, error) {
			return onRef(ctx, argStr(a, "ref"), "type", argStr(a, "ref"), argStr(a, "text"))
		}, act)

	svc.AddToolWithMetadata("desktop_set_value",
		"Replace an element's value outright (a text field's whole text, a slider's position).",
		obj(map[string]any{"ref": str("Ref of the element"), "value": str("The new value")}, "ref", "value"),
		func(ctx context.Context, a map[string]any) (any, error) {
			return onRef(ctx, argStr(a, "ref"), "set-value", argStr(a, "ref"), argStr(a, "value"))
		}, act)

	svc.AddToolWithMetadata("desktop_select",
		"Choose an option in a list or a pop-up menu.",
		obj(map[string]any{"ref": str("Ref of the list or pop-up"), "value": str("The option")}, "ref", "value"),
		func(ctx context.Context, a map[string]any) (any, error) {
			return onRef(ctx, argStr(a, "ref"), "select", argStr(a, "ref"), argStr(a, "value"))
		}, act)

	svc.AddToolWithMetadata("desktop_toggle",
		"Flip a checkbox or a switch. Read its state with desktop_get first: toggling twice undoes it.",
		obj(map[string]any{"ref": str("Ref of the checkbox or switch")}, "ref"),
		func(ctx context.Context, a map[string]any) (any, error) {
			return onRef(ctx, argStr(a, "ref"), "toggle", argStr(a, "ref"))
		}, act)

	svc.AddToolWithMetadata("desktop_scroll",
		"Scroll an element (a list, a scroll area).",
		obj(map[string]any{"ref": str("Ref of the element"), "direction": str("up, down (default), left or right"), "amount": map[string]any{"type": "integer", "description": "Scroll units, default 3"}}, "ref"),
		func(ctx context.Context, a map[string]any) (any, error) {
			args := []string{"scroll", argStr(a, "ref")}
			if v := argStr(a, "direction"); v != "" {
				args = append(args, "--direction", v)
			}
			if n := argInt(a, "amount"); n > 0 {
				args = append(args, "--amount", fmt.Sprint(n))
			}
			return onRef(ctx, argStr(a, "ref"), args...)
		}, act)

	svc.AddToolWithMetadata("desktop_press",
		"Send a key combo to an app without bringing it forward: return, escape, tab, cmd+f, shift+tab… Combos that quit an app or close a window are refused.",
		obj(map[string]any{"app": str("App name"), "combo": str("Key combo")}, "app", "combo"),
		func(ctx context.Context, a map[string]any) (any, error) {
			app, combo := argStr(a, "app"), argStr(a, "combo")
			if app == "" || combo == "" {
				return errResult("app and combo are required"), nil
			}
			if d.blockedName(app) {
				return refused(app), nil
			}
			if isQuitCombo(combo) {
				return errResult(combo + " quits or closes what the person has open; closing apps and windows is theirs to do."), nil
			}
			out, err := d.run(ctx, "press", combo, "--app", app)
			if err != nil {
				return errResult(err.Error()), nil
			}
			return desktopAnswer(out), nil
		}, act)

	svc.AddToolWithMetadata("desktop_launch",
		"Start a desktop app, or attach to it if it is running, without bringing it to the front.",
		obj(map[string]any{"app": str("App name or bundle id, e.g. Calculator")}, "app"),
		func(ctx context.Context, a map[string]any) (any, error) {
			app := argStr(a, "app")
			if app == "" {
				return errResult("app is required"), nil
			}
			if d.blockedName(app) {
				return refused(app), nil
			}
			out, err := d.run(ctx, "launch", app)
			if err != nil {
				return errResult(err.Error()), nil
			}
			return desktopAnswer(out), nil
		}, act)
}

func desktopSection(on bool) string {
	if !on {
		return ""
	}
	return `
- You can operate the desktop apps on this machine (desktop_*): desktop_windows to see what is open, desktop_snapshot for an app's elements and their refs, then desktop_click / desktop_type / desktop_select / desktop_toggle / desktop_scroll on a ref, and desktop_snapshot again to check. It works through the accessibility API: the person's pointer and focus stay theirs. Prefer an app's own tool or MCP when there is one. Before anything that sends, buys, deletes, or changes an account, stop and ask.`
}

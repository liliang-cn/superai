package backend

// MCP servers started with npx run from a package installed once.
//
// `npx -y <package>` resolves the package against the registry on every start.
// Through the proxy that took 12-27s for mcp-remote, every launch, and the
// window waited on it. The entry in mcpServers.json stays as it is — it is
// what a queen hands her workers, and npx is the portable way to say "this
// package" — but the copy agent-go reads names the installed script and the
// node that sits beside npx. The first start of a package still goes through
// npx while the install runs in the background; every start after it is local.

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// mcpPackagesDir holds one npm prefix per package spec.
func mcpPackagesDir() string { return filepath.Join(DataDir(), "mcp-packages") }

var unsafeSpec = regexp.MustCompile(`[^A-Za-z0-9._@-]+`)

// npxSpec is the package an npx command line runs, and the arguments that
// follow it. ok is false for anything that is not `npx [flags] <package> …`.
func npxSpec(command string, args []string) (spec string, rest []string, ok bool) {
	if filepath.Base(command) != "npx" {
		return "", nil, false
	}
	for i, a := range args {
		switch {
		case a == "-y" || a == "--yes" || a == "-q" || a == "--quiet":
			continue
		case strings.HasPrefix(a, "-"):
			// --package=…, -p, --call: forms this does not take apart.
			return "", nil, false
		default:
			return a, args[i+1:], true
		}
	}
	return "", nil, false
}

// packageName drops a version from a spec: "@scope/pkg@1.2" -> "@scope/pkg".
func packageName(spec string) string {
	at := strings.LastIndex(spec, "@")
	if at > 0 {
		return spec[:at]
	}
	return spec
}

// installedScript is the file the package's bin points at, once installed
// under dir, or "" when it is not there yet.
func installedScript(dir, spec string) string {
	name := packageName(spec)
	pkgDir := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
	raw, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(raw, &pkg) != nil || len(pkg.Bin) == 0 {
		return ""
	}
	var rel string
	var one string
	var many map[string]string
	switch {
	case json.Unmarshal(pkg.Bin, &one) == nil:
		rel = one
	case json.Unmarshal(pkg.Bin, &many) == nil:
		// npx runs the bin named after the package, or the only one there is.
		base := name[strings.LastIndex(name, "/")+1:]
		if s, ok := many[base]; ok {
			rel = s
		} else if len(many) == 1 {
			for _, s := range many {
				rel = s
			}
		}
	}
	if rel == "" {
		return ""
	}
	script := filepath.Join(pkgDir, filepath.FromSlash(rel))
	if _, err := os.Stat(script); err != nil {
		return ""
	}
	return script
}

var npxInstalls sync.Map // dir -> struct{}: one background install per destination

// installNpxPackage installs spec under dir with the npm beside npx. Errors
// are logged: the server keeps starting through npx, which works, only slower.
func installNpxPackage(npx, spec, dir string) {
	if _, busy := npxInstalls.LoadOrStore(dir, struct{}{}); busy {
		return
	}
	go func() {
		npm := filepath.Join(filepath.Dir(npx), "npm")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			slog.Warn("mcp package", "package", spec, "error", err)
			return
		}
		cmd := exec.Command(npm, "install", "--prefix", dir, "--no-audit", "--no-fund", "--loglevel=error", spec)
		if out, err := cmd.CombinedOutput(); err != nil {
			npxInstalls.Delete(dir) // let a later start try again
			slog.Warn("mcp package not installed", "package", spec, "error", err, "output", strings.TrimSpace(string(out)))
			return
		}
		slog.Info("mcp package installed; it starts without npx from now on", "package", spec)
	}()
}

// localizeNpx rewrites an `npx <package>` entry to run the installed package
// with node directly. changed is false when the entry is not an npx one, or
// its package is not installed yet (an install is then started).
func localizeNpx(entry map[string]any) (out map[string]any, changed bool) {
	command, _ := entry["command"].(string)
	var args []string
	if raw, ok := entry["args"].([]any); ok {
		for _, a := range raw {
			s, ok := a.(string)
			if !ok {
				return entry, false
			}
			args = append(args, s)
		}
	}
	spec, rest, ok := npxSpec(command, args)
	if !ok {
		return entry, false
	}
	npx := command
	if !filepath.IsAbs(npx) {
		p, err := exec.LookPath(npx)
		if err != nil {
			return entry, false
		}
		npx = p
	}
	node := filepath.Join(filepath.Dir(npx), "node")
	if _, err := os.Stat(node); err != nil {
		return entry, false
	}
	dir := filepath.Join(mcpPackagesDir(), unsafeSpec.ReplaceAllString(spec, "_"))
	script := installedScript(dir, spec)
	if script == "" {
		installNpxPackage(npx, spec, dir)
		return entry, false
	}
	out = make(map[string]any, len(entry))
	for k, v := range entry {
		out[k] = v
	}
	newArgs := make([]any, 0, len(rest)+1)
	newArgs = append(newArgs, script)
	for _, a := range rest {
		newArgs = append(newArgs, a)
	}
	out["command"] = node
	out["args"] = newArgs
	return out, true
}

package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A fake npm that "installs" a package with one bin, as npm would lay it out.
const fakeNpm = `#!/bin/sh
# npm install --prefix DIR ... SPEC
while [ $# -gt 0 ]; do
  case "$1" in --prefix) shift; dir="$1";; -*) ;; *) spec="$1";; esac
  shift
done
mkdir -p "$dir/node_modules/$spec/dist"
printf '{"name":"%s","bin":{"%s":"dist/proxy.js"}}' "$spec" "$spec" > "$dir/node_modules/$spec/package.json"
echo 'console.log(1)' > "$dir/node_modules/$spec/dist/proxy.js"
`

func TestNpxServerRunsFromItsInstalledPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_HOME", home)
	bin := t.TempDir()
	for name, body := range map[string]string{"npx": "#!/bin/sh\n", "node": "#!/bin/sh\n", "npm": fakeNpm} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(home, "mcpServers.json")
	cfg := map[string]any{"mcpServers": map[string]any{
		"noted": map[string]any{"command": filepath.Join(bin, "npx"), "args": []any{"-y", "mcp-remote", "http://x/mcp", "--allow-http"}},
		"other": map[string]any{"command": "/usr/bin/true"},
	}}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return "", false }

	// First start: not installed yet, so the file is used as it is and the
	// install starts behind it.
	if got, err := resolveMCPConfig(src, lookup); err != nil || got != src {
		t.Fatalf("first resolve = %q, %v; want the source unchanged", got, err)
	}
	dir := filepath.Join(mcpPackagesDir(), "mcp-remote")
	deadline := time.Now().Add(10 * time.Second)
	for installedScript(dir, "mcp-remote") == "" {
		if time.Now().After(deadline) {
			t.Fatal("the package was never installed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Every start after: node runs the installed script, npx is gone.
	got, err := resolveMCPConfig(src, lookup)
	if err != nil || got == src {
		t.Fatalf("second resolve = %q, %v; want a resolved copy", got, err)
	}
	var f struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	b, _ := os.ReadFile(got)
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	n := f.MCPServers["noted"]
	want := filepath.Join(dir, "node_modules", "mcp-remote", "dist", "proxy.js")
	if n.Command != filepath.Join(bin, "node") || len(n.Args) != 3 || n.Args[0] != want || n.Args[1] != "http://x/mcp" {
		t.Fatalf("noted = %+v; want node %s http://x/mcp --allow-http", n, want)
	}
	if f.MCPServers["other"].Command != "/usr/bin/true" {
		t.Fatalf("a server that is not npx changed: %+v", f.MCPServers["other"])
	}
}

func TestNpxSpec(t *testing.T) {
	for _, c := range []struct {
		args []string
		spec string
		ok   bool
	}{
		{[]string{"-y", "mcp-remote", "u"}, "mcp-remote", true},
		{[]string{"@scope/pkg@1.2.0"}, "@scope/pkg@1.2.0", true},
		{[]string{"--package=x", "y"}, "", false},
	} {
		spec, _, ok := npxSpec("/opt/bin/npx", c.args)
		if spec != c.spec || ok != c.ok {
			t.Errorf("npxSpec(%v) = %q %v", c.args, spec, ok)
		}
	}
	if packageName("@scope/pkg@1.2.0") != "@scope/pkg" || packageName("mcp-remote") != "mcp-remote" {
		t.Error("packageName")
	}
}

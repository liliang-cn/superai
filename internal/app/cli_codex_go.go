package app

// Go's caches for a sandboxed codex run in a Go module.
//
// go build and go test write their build cache (GOCACHE, under
// ~/Library/Caches) and, when go.mod names a module it has not got yet, the
// module cache (GOMODCACHE, under ~/go). Neither is inside the workspace, so
// Codex's workspace-write sandbox refused both: in superai, go vet stopped at
// "open …/go-build/…: operation not permitted", and a run sent to change and
// test the code worked around it with a cold cache in /tmp, rebuilt every
// dependency, and was cut off before its tests finished.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// goCaches is GOCACHE and GOMODCACHE, as real paths, when dir is inside a Go
// module or workspace; nothing otherwise, so a run elsewhere gets no more than
// it did.
func goCaches(dir string) []string {
	if dir == "" || !inGoModule(dir) {
		return nil
	}
	cache, mod := goEnvCaches(dir)
	out := []string{}
	for _, p := range []string{cache, mod} {
		// GOCACHE=off, or a value that is not a path, is nothing to write.
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		// go makes them on first use; codex wants a root that exists.
		if err := os.MkdirAll(p, 0o755); err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		out = append(out, p)
	}
	return out
}

func inGoModule(dir string) bool {
	for d := dir; ; {
		for _, f := range []string{"go.mod", "go.work"} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return true
			}
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}

// goEnvCaches asks go itself, which also knows what `go env -w` set; without a
// go on PATH it is the environment, then go's defaults.
func goEnvCaches(dir string) (cache, mod string) {
	if bin, err := exec.LookPath("go"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "env", "GOCACHE", "GOMODCACHE")
		cmd.Dir = dir
		// A go.mod asking for a newer toolchain must not start a download.
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		if b, err := cmd.Output(); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) == 2 {
				return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
			}
		}
	}
	cache = os.Getenv("GOCACHE")
	if cache == "" {
		if d, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(d, "go-build")
		}
	}
	mod = os.Getenv("GOMODCACHE")
	if mod == "" {
		gopath, _, _ := strings.Cut(os.Getenv("GOPATH"), string(os.PathListSeparator))
		if gopath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				gopath = filepath.Join(home, "go")
			}
		}
		if gopath != "" {
			mod = filepath.Join(gopath, "pkg", "mod")
		}
	}
	return cache, mod
}

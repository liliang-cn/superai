// Package brand carries what is left of the name SuperAI.
//
// The project was renamed SuperLeo. Machines that ran it before have their
// data in ~/.superai, ~/.superai-node and ~/.superai-agent, and their
// environment — launchd jobs, k3s manifests, shell profiles — sets
// SUPERAI_* variables. Neither should stop working on the day the binary
// changes its name, so before anything else reads them this package
//
//   - copies every SUPERAI_X into SUPERLEO_X that is not set, and
//   - moves each old data directory to its new name and leaves a symlink at
//     the old path, so hooks, plugins and scripts that still name it keep
//     reaching the same files. Where the old directory cannot be moved (a
//     volume mounted on it), the new path becomes a symlink to it instead.
//
// It is imported by internal/backend, so it runs ahead of every package that
// reads the data directory or the environment.
package brand

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// legacyDirs are the data directories that were renamed, old to new, in the
// home directory.
var legacyDirs = [][2]string{
	{".superai", ".superleo"},
	{".superai-node", ".superleo-node"},
	{".superai-agent", ".superleo-agent"},
}

func init() {
	legacyEnv(os.Environ(), os.LookupEnv, os.Setenv)
	// A test binary never moves the real home directory.
	if testing.Testing() {
		return
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		MigrateDirs(home)
	}
}

func legacyEnv(environ []string, lookup func(string) (string, bool), set func(string, string) error) {
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, "SUPERAI_") {
			continue
		}
		next := "SUPERLEO_" + strings.TrimPrefix(name, "SUPERAI_")
		if _, set_ := lookup(next); !set_ {
			_ = set(next, value)
		}
	}
}

// MigrateDirs moves the old data directories under home to their new names.
// It does nothing for a directory already moved, or never created.
func MigrateDirs(home string) {
	for _, d := range legacyDirs {
		migrateDir(filepath.Join(home, d[0]), filepath.Join(home, d[1]))
	}
}

func migrateDir(old, next string) {
	if _, err := os.Lstat(next); err == nil {
		if !foreign(old, next) {
			return // already there (moved, or a fresh install)
		}
		// The new name was someone else's first: the SuperLeo project
		// before this one kept its data in ~/.superleo. Set it aside
		// rather than run on it or mix the two.
		if err := os.Rename(next, nextFree(next+"-legacy")); err != nil {
			return
		}
	}
	st, err := os.Lstat(old)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return
	}
	if err := os.Rename(old, next); err != nil {
		// A mount point cannot be renamed: reach it under the new name.
		_ = os.Symlink(old, next)
		return
	}
	_ = os.Symlink(filepath.Base(next), old)
}

// foreign reports whether next is a directory that is not this program's
// data while old is: old has settings.json and next, a real directory, has
// none.
func foreign(old, next string) bool {
	st, err := os.Lstat(next)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if _, err := os.Stat(filepath.Join(next, "settings.json")); err == nil {
		return false
	}
	ost, err := os.Lstat(old)
	if err != nil || !ost.IsDir() || ost.Mode()&os.ModeSymlink != 0 {
		return false
	}
	_, err = os.Stat(filepath.Join(old, "settings.json"))
	return err == nil
}

// nextFree is p, or p-2, p-3 … whichever does not exist yet.
func nextFree(p string) string {
	for i := 1; ; i++ {
		c := p
		if i > 1 {
			c = p + "-" + strconv.Itoa(i)
		}
		if _, err := os.Lstat(c); err != nil {
			return c
		}
	}
}

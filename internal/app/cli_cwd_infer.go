package app

// Where a handed-out coding run works when nobody said: the order names the
// repository — "~/Things/dev/apps/superai", "fix the build in superai-android"
// — and the run starts there instead of in the empty workspace, where the
// agent would first have to go and find it. It is worked out on the machine
// the CLI runs on, so "codex.mac" looks at the Mac's disk, and only ever
// inside the workspace and the allowed roots.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// cwdPathRe finds paths in an order: ~/…, or absolute ones under a home or a
// usual code place. Bare "/x" is left alone; "/" turns up in too much prose.
var cwdPathRe = regexp.MustCompile(`(?:~|/Users|/home|/srv|/opt|/root|/var|/tmp|/private)(?:/[^\s"'` + "`" + `<>|;,，。；：、（）()\[\]{}]+)+`)

// cwdScanDepth is how deep under a root repositories are looked for by name;
// ~/Things/dev/apps/<repo> is one level, a root of ~ needs four.
const (
	cwdScanDepth = 4
	cwdScanMax   = 20000
)

// inferCLICwd is the directory an order points at, inside allowed, or "".
// A path written out wins over a repository named.
func inferCLICwd(prompt string, allowed []string) string {
	if strings.TrimSpace(prompt) == "" || len(allowed) == 0 {
		return ""
	}
	for _, m := range cwdPathRe.FindAllString(prompt, -1) {
		if d := cwdFromPath(strings.TrimRight(m, ".:!?)。！？"), allowed); d != "" {
			return d
		}
	}
	// A name counts only as a word of its own, so "superai" is not found in
	// "superai-android"; of the repositories named, the one said first is the
	// subject. Names under four letters are too likely to be just words.
	repos := cwdRepos(allowed)
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	best, bestAt := "", -1
	for _, name := range names {
		if len(name) < 4 {
			continue
		}
		if at := wordIndex(prompt, name); at >= 0 && (bestAt < 0 || at < bestAt) {
			best, bestAt = name, at
		}
	}
	return repos[best]
}

// cwdFromPath is the repository a path is in (or the directory itself, or the
// directory a file is in), when that is inside allowed.
func cwdFromPath(p string, allowed []string) string {
	p = filepath.Clean(cliExpandHome(p))
	if !filepath.IsAbs(p) {
		return ""
	}
	// Walk up to the first thing that is there: an order may name a file it
	// wants made.
	for {
		if st, err := os.Stat(p); err == nil {
			if !st.IsDir() {
				p = filepath.Dir(p)
			}
			break
		}
		up := filepath.Dir(p)
		if up == p {
			return ""
		}
		p = up
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	root := withinRoots(p, allowed)
	if root == "" {
		return ""
	}
	// The repository it is in, not a folder deep inside it, as far up as the root.
	for d := p; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == root || filepath.Dir(d) == d {
			break
		}
	}
	return p
}

func withinRoots(dir string, allowed []string) string {
	for _, root := range allowed {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return root
		}
	}
	return ""
}

// cwdRepos maps a repository's folder name to where it is, for every git
// repository under the roots. A name found twice keeps the shallower one.
func cwdRepos(allowed []string) map[string]string {
	out := map[string]string{}
	depth := map[string]int{}
	seen := 0
	var walk func(dir string, d int)
	walk = func(dir string, d int) {
		if d > cwdScanDepth || seen > cwdScanMax {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.Name() == ".git" {
				name := filepath.Base(dir)
				if old, ok := depth[name]; !ok || d < old {
					out[name], depth[name] = dir, d
				}
				return // a repository's own folders are not more repositories
			}
		}
		for _, e := range ents {
			seen++
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || n == "node_modules" || n == "Library" || n == "vendor" {
				continue
			}
			walk(filepath.Join(dir, n), d+1)
		}
	}
	for _, root := range allowed {
		walk(root, 0)
	}
	return out
}

// wordIndex is where name stands in s as a word of its own: not part of a
// longer name ("superai" in "superai-android" is not a hit), or -1.
func wordIndex(s, name string) int {
	ls, ln := strings.ToLower(s), strings.ToLower(name)
	for from := 0; ; {
		i := strings.Index(ls[from:], ln)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(ln)
		if (i == 0 || !nameByte(ls[i-1])) && (end == len(ls) || !nameByte(ls[end]) || (ls[end] == '.' && (end+1 == len(ls) || !nameByte(ls[end+1])))) {
			return i
		}
		from = i + 1
	}
}

func nameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c == '/' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

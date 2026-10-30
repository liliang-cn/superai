package backend

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every prompt a queen or worker is given says what the queen is called in
// Chinese, and names the word she is not called only in that rule.
func TestQueenPromptsSayFenghou(t *testing.T) {
	queen := &Settings{}
	queen.Hive.Role = HiveRoleQueen
	worker := &Settings{}
	worker.Hive.Role = HiveRoleWorker
	prompts := map[string]string{
		"engine, alone": ExternalQueenPrompt(&Settings{}),
		"engine, queen": ExternalQueenPrompt(queen),
		"own, queen":    hiveSection(queen.Hive),
		"own, worker":   hiveSection(worker.Hive),
	}
	for name, p := range prompts {
		if strings.Count(p, queenChineseName) != 1 {
			t.Errorf("%s: the rule is there %d times, want once:\n%s", name, strings.Count(p, queenChineseName), p)
		}
		if rest := strings.ReplaceAll(p, queenChineseName, ""); strings.Contains(rest, queenNotWord) {
			t.Errorf("%s: says %s outside the rule:\n%s", name, queenNotWord, p)
		}
	}
	if !strings.Contains(queenChineseName, "蜂后") || !strings.Contains(queenChineseName, queenNotWord) {
		t.Fatalf("the rule names neither word: %q", queenChineseName)
	}
}

// No copy, prompt or doc in the repository calls the queen by the word for a
// monarch: the Chinese for the hive's queen is 蜂后.
func TestNoMonarchWordForTheQueen(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("not the repository root: %s", root)
	}
	skipDirs := map[string]bool{".git": true, "node_modules": true, "dist": true, "build": true, "wailsjs": true}
	text := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true, ".json": true,
		".md": true, ".html": true, ".css": true, ".yaml": true, ".yml": true, ".toml": true, ".txt": true, ".sh": true, ".py": true, ".proto": true}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !text[filepath.Ext(path)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, queenNotWord) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d says %s; the queen is 蜂后: %s", rel, i+1, queenNotWord, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Chinese copy is among what was read.
	if scanned < 100 {
		t.Fatalf("only %d files read", scanned)
	}
	zh, err := os.ReadFile(filepath.Join(root, "frontend", "src", "lib", "i18n.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(zh), `"The queen":"蜂后"`) {
		t.Fatal(`the zh copy no longer has "The queen":"蜂后"`)
	}
}

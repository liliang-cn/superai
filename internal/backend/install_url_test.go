package backend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsGitRepoURL(t *testing.T) {
	yes := []string{
		"https://github.com/owner/repo",
		"https://github.com/owner/repo.git",
		"http://gitlab.com/owner/repo",
		"https://bitbucket.org/owner/repo",
		"git@github.com:owner/repo.git",
		"https://github.com/owner/repo/", // trailing slash
	}
	for _, u := range yes {
		if !isGitRepoURL(u) {
			t.Errorf("%q should be clonable", u)
		}
	}
	no := []string{
		// Deeper than owner/repo is a page, not a repo root.
		"https://github.com/owner/repo/blob/main/SKILL.md",
		"https://github.com/owner/repo/tree/main/skills",
		"https://github.com/owner",
		"https://example.com/owner/repo",
		"https://www.npmjs.com/package/foo",
		"",
	}
	for _, u := range no {
		if isGitRepoURL(u) {
			t.Errorf("%q should not be treated as a clonable repo", u)
		}
	}
}

func TestGithubReadmeURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo":     "https://raw.githubusercontent.com/owner/repo/HEAD/README.md",
		"https://github.com/owner/repo.git": "https://raw.githubusercontent.com/owner/repo/HEAD/README.md",
		// Anything that is not a bare repo root has no obvious README.
		"https://github.com/owner/repo/tree/main/x": "",
		"https://github.com/owner":                  "",
		"https://gitlab.com/owner/repo":             "",
		"https://example.com":                       "",
	}
	for in, want := range cases {
		if got := githubReadmeURL(in); got != want {
			t.Errorf("githubReadmeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeSkillMDAndName(t *testing.T) {
	skill := "---\nname: my-skill\ndescription: Use when testing\n---\n\n# Body\n"
	if !looksLikeSkillMD(skill) {
		t.Error("a SKILL.md with frontmatter must be recognised")
	}
	if got := skillNameFromMD(skill); got != "my-skill" {
		t.Errorf("skillNameFromMD = %q, want my-skill", got)
	}

	notSkills := []string{
		"# Just a readme\n\nInstall with npx.",
		"---\ntitle: no name field\n---\n",
		"",
	}
	for _, text := range notSkills {
		if looksLikeSkillMD(text) {
			t.Errorf("%q must not be taken for a SKILL.md", text)
		}
	}

	// A quoted name is still a name.
	if got := skillNameFromMD(`---` + "\n" + `name: "quoted-name"` + "\n" + `---`); got != "quoted-name" {
		t.Errorf("quoted name not unwrapped, got %q", got)
	}
}

func TestSkillNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/my-skill":                   "my-skill",
		"https://github.com/owner/my-skill.git":               "my-skill",
		"https://example.com/skills/investing/SKILL.md":       "investing",
		"https://raw.githubusercontent.com/o/r/HEAD/SKILL.md": "HEAD",
		"https://example.com/a/b/named-thing.md":              "named-thing",
		"https://github.com/owner/my-skill/":                  "my-skill",
	}
	for in, want := range cases {
		if got := skillNameFromURL(in); got != want {
			t.Errorf("skillNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchReadableConvertsHTMLAndRespectsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><body><h1>Install</h1><p>Run <code>npx -y foo</code></p></body></html>"))
		case "/plain.md":
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("# Install\n\nnpx -y foo\n"))
		case "/huge":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("x", fetchLimit*2)))
		case "/empty":
			w.Header().Set("Content-Type", "text/plain")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	html, err := fetchReadable(ctx, srv.URL+"/page.html")
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	if strings.Contains(html, "<h1>") {
		t.Errorf("HTML should be converted to markdown, got %q", html)
	}
	if !strings.Contains(html, "Install") || !strings.Contains(html, "npx -y foo") {
		t.Errorf("conversion lost the content: %q", html)
	}

	md, err := fetchReadable(ctx, srv.URL+"/plain.md")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if !strings.HasPrefix(md, "# Install") {
		t.Errorf("non-HTML should pass through unchanged, got %q", md)
	}

	big, err := fetchReadable(ctx, srv.URL+"/huge")
	if err != nil {
		t.Fatalf("huge: %v", err)
	}
	if len(big) > fetchLimit {
		t.Errorf("read %d bytes, must be capped at %d", len(big), fetchLimit)
	}

	if _, err := fetchReadable(ctx, srv.URL+"/empty"); err == nil {
		t.Error("an empty page should be an error, not empty content for the model")
	}
	if _, err := fetchReadable(ctx, srv.URL+"/missing"); err == nil {
		t.Error("a 404 should be an error")
	}
	if _, err := fetchReadable(ctx, ""); err == nil {
		t.Error("an empty url should be an error")
	}
}

func TestFindSkillDir(t *testing.T) {
	root := t.TempDir()
	// A repo that keeps the skill one level down.
	nested := filepath.Join(root, "the-skill")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Noise that must not be picked: a dotdir and a dir without SKILL.md.
	_ = os.MkdirAll(filepath.Join(root, ".github"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "docs"), 0o755)

	if got := findSkillDir(root); got != nested {
		t.Errorf("findSkillDir = %q, want %q", got, nested)
	}
	if got := findSkillDir(filepath.Join(root, "docs")); got != "" {
		t.Errorf("a directory with no skill inside should return empty, got %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "picked", "later"); got != "picked" {
		t.Errorf("firstNonEmpty = %q, want picked", got)
	}
	if got := firstNonEmpty("", "   "); got != "" {
		t.Errorf("all-blank should give empty, got %q", got)
	}
	if got := firstNonEmpty("  spaced  "); got != "spaced" {
		t.Errorf("result should be trimmed, got %q", got)
	}
}

func TestInstallLoopFetchGuards(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("page " + r.URL.Path))
	}))
	defer srv.Close()
	ctx := context.Background()

	loop := newInstallLoop()
	if err := loop.fetch(ctx, srv.URL+"/a"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	// Re-reading the same URL must be refused, or a confused model spins on it.
	if err := loop.fetch(ctx, srv.URL+"/a"); err == nil {
		t.Error("fetching the same url twice should be refused")
	}
	if err := loop.fetch(ctx, ""); err == nil {
		t.Error("an empty url should be refused")
	}

	// The page budget is a hard stop.
	for i := 2; i <= maxPages; i++ {
		if err := loop.fetch(ctx, fmt.Sprintf("%s/p%d", srv.URL, i)); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if err := loop.fetch(ctx, srv.URL+"/one-too-many"); err == nil {
		t.Errorf("reading more than %d pages should be refused", maxPages)
	}

	// A failed fetch still marks the URL visited, so the loop cannot retry it
	// forever.
	loop2 := newInstallLoop()
	_ = loop2.fetch(ctx, srv.URL+"/nope-not-a-server-xyz")
	if !loop2.visited[srv.URL+"/nope-not-a-server-xyz"] {
		t.Error("a failed fetch must still be recorded as visited")
	}
}

func TestInstallLoopContextCarriesPagesAndAttempts(t *testing.T) {
	loop := newInstallLoop()
	loop.pages = append(loop.pages, readPage{url: "https://x/readme", text: "run npx -y thing"})
	loop.note("installing thing failed: %v", "exec: npx not found")

	ctx := loop.context()
	for _, want := range []string{"https://x/readme", "npx -y thing", "npx not found", "do not repeat"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("loop context missing %q:\n%s", want, ctx)
		}
	}

	trace := loop.trace()
	if len(trace) != 2 || !strings.HasPrefix(trace[0], "read ") {
		t.Errorf("trace should list the page read then the attempt, got %v", trace)
	}
}

func TestInstallLoopTruncatesHugePages(t *testing.T) {
	body := strings.Repeat("y", pageBudget*2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	loop := newInstallLoop()
	if err := loop.fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if got := len(loop.pages[0].text); got > pageBudget+64 {
		t.Errorf("page kept at %d bytes, should be trimmed to about %d", got, pageBudget)
	}
	if !strings.Contains(loop.pages[0].text, "truncated") {
		t.Error("a trimmed page should say so, or the model will think it saw everything")
	}
}

func TestInstallSucceededReadsThePayloadNotTheEnvelope(t *testing.T) {
	// The trap this guards: okData reports ok=true even when the payload says
	// the install failed, so judging by the envelope calls failure success.
	writtenButBroken := okData(map[string]interface{}{
		"installed": false,
		"note":      "the SKILL.md is probably malformed",
	})
	if installSucceeded(writtenButBroken) {
		t.Error("installed=false must not count as success just because ok=true")
	}
	if got := resultProblem(writtenButBroken); !strings.Contains(got, "malformed") {
		t.Errorf("resultProblem = %q, want the note", got)
	}

	good := okData(map[string]interface{}{"installed": true, "skill": "x"})
	if !installSucceeded(good) {
		t.Error("installed=true should count as success")
	}

	failed := errResult("git clone failed")
	if installSucceeded(failed) {
		t.Error("an error result is not success")
	}
	if got := resultProblem(failed); got != "git clone failed" {
		t.Errorf("resultProblem = %q, want the error", got)
	}

	if installSucceeded(nil) {
		t.Error("nil is not success")
	}
	if got := resultProblem(okData(map[string]interface{}{"installed": false})); got == "" {
		t.Error("resultProblem should always say something")
	}
}

func TestMissingEnv(t *testing.T) {
	env := map[string]string{"GITHUB_TOKEN": "x"}
	got := missingEnv([]string{"GITHUB_TOKEN", "DATABASE_URL", "  ", ""}, env)
	if len(got) != 1 || got[0] != "DATABASE_URL" {
		t.Errorf("missingEnv = %v, want [DATABASE_URL]", got)
	}
	if got := missingEnv(nil, env); len(got) != 0 {
		t.Errorf("no keys requested should mean nothing missing, got %v", got)
	}
	if got := missingEnv([]string{"A"}, nil); len(got) != 1 {
		t.Errorf("with no env supplied everything is missing, got %v", got)
	}
}

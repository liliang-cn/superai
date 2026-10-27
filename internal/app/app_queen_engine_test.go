package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// A stand-in for the claude CLI: it answers --version, keeps the arguments
// and the queen token it was started with, and prints what a short Claude
// Code turn prints.
const fakeClaudeTurn = `{"type":"system","subtype":"init","session_id":"sess-1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hive 里有"}},"session_id":"sess-1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"两个成员。"}},"session_id":"sess-1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hive 里有两个成员。"}]},"session_id":"sess-1"}
{"type":"result","subtype":"success","is_error":false,"result":"hive 里有两个成员。","total_cost_usd":0.0123,"session_id":"sess-1"}`

const fakeClaudeLoggedOut = `{"type":"system","subtype":"init","session_id":"sess-x"}
{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login","session_id":"sess-x"}`

func fakeClaude(t *testing.T, out string, exit int) (bin, dir string) {
	t.Helper()
	// Only the system's own tools on the PATH, so no real agent CLI is found
	// (and asked its version) alongside the fake.
	t.Setenv("PATH", "/usr/bin:/bin")
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.jsonl"), []byte(out+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "2.1.0 (Claude Code)"; exit 0; fi
d=%q
n=$(ls "$d" | grep -c '^args\.')
n=$((n+1))
for a in "$@"; do printf '%%s\n\036\n' "$a"; done > "$d/args.$n"
printf '%%s' "$SUPERAI_QUEEN_TOKEN" > "$d/token.$n"
printf '%%s' "$CLAUDE_CODE_DISABLE_AUTO_MEMORY" > "$d/lean.$n"
cat "$d/out.jsonl"
exit %d
`, dir, exit)
	bin = filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// fakeArgs is what the fake CLI was started with on its nth start.
func fakeArgs(t *testing.T, dir string, n int) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("args.%d", n)))
	if err != nil {
		t.Fatal(err)
	}
	// Every argument is kept, an empty one too: --setting-sources "" is one.
	return strings.Split(strings.TrimSuffix(string(raw), "\n\036\n"), "\n\036\n")
}

// fakePrompt is the prompt the fake CLI was given on its nth start.
func fakePrompt(t *testing.T, dir string, n int) string {
	args := fakeArgs(t, dir, n)
	return args[len(args)-1]
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

type engineEvents struct {
	mu  sync.Mutex
	got []struct {
		name string
		p    map[string]any
	}
}

func (e *engineEvents) add(name string, p map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, struct {
		name string
		p    map[string]any
	}{name, p})
}

func (e *engineEvents) find(name, requestID string) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, g := range e.got {
		if g.name == name && g.p["requestId"] == requestID {
			return g.p
		}
	}
	return nil
}

func (e *engineEvents) all(name, requestID string) []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []map[string]any
	for _, g := range e.got {
		if g.name == name && g.p["requestId"] == requestID {
			out = append(out, g.p)
		}
	}
	return out
}

func (e *engineEvents) wait(t *testing.T, requestID string) (string, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range []string{"chat:done", "chat:error", "chat:cancelled"} {
			if p := e.find(n, requestID); p != nil {
				return n, p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s never ended", requestID)
	return "", nil
}

type ownCall struct{ session, message, standIn string }

type engineHarness struct {
	a      *App
	ev     *engineEvents
	dir    string
	mu     sync.Mutex
	own    []ownCall
	stored [][2]string
	// base is the conversation as history had it before the test's turns.
	base []backend.ChatTurn
}

func newEngineHarness(t *testing.T, out string, exit int) *engineHarness {
	t.Helper()
	t.Setenv("SUPERAI_HOME", t.TempDir())
	bin, dir := fakeClaude(t, out, exit)
	h := &engineHarness{ev: &engineEvents{}, dir: dir}
	h.a = &App{settings: &backend.Settings{
		WorkspaceDir:   t.TempDir(),
		ExternalAgents: backend.ExternalAgents{Enabled: true, Unattended: true, Binaries: map[string]string{"claude": bin}},
		Hive:           backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q", QueenEngine: "claude"},
	}}
	h.a.serveURL = "http://127.0.0.1:43999"
	h.a.emitFn = h.ev.add
	h.a.ownTurnFn = func(requestID, session, message, standIn string) {
		h.mu.Lock()
		h.own = append(h.own, ownCall{session, message, standIn})
		h.stored = append(h.stored, [2]string{message, "own: " + message})
		h.mu.Unlock()
		recordTurnTimeBy(session, message, time.Now(), time.Second, turnBy{StoodInFor: standIn})
		h.a.emit("chat:done", map[string]any{"requestId": requestID, "final": "own"})
	}
	h.a.appendFn = func(session, q, ans string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.stored = append(h.stored, [2]string{q, ans})
		return nil
	}
	h.a.turnsFn = func(string) []backend.ChatTurn {
		h.mu.Lock()
		defer h.mu.Unlock()
		out := append([]backend.ChatTurn(nil), h.base...)
		for _, x := range h.stored {
			out = append(out, backend.ChatTurn{Role: "user", Content: x[0]}, backend.ChatTurn{Role: "assistant", Content: x[1]})
		}
		return out
	}
	return h
}

func (h *engineHarness) owned() []ownCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ownCall(nil), h.own...)
}

func TestOnlyAPersonsOwnTurnGoesToTheQueenEngine(t *testing.T) {
	s := &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, QueenEngine: "claude.mac"}}
	for session, want := range map[string]string{
		"0b6f2c1e-7d1a-4c55-9f0e-1f2a3b4c5d6e": "claude.mac",
		"hive-console-web":                     "claude.mac",
		"hive:order-1":                         "",
		"hive-mail:queen":                      "",
		"dashboard:abc":                        "",
		"tile-k2-x1":                           "",
		"scheduled":                            "",
		"mcp-1234":                             "",
		"cc:terminal":                          "",
		"":                                     "",
	} {
		if got := queenEngineRoute(s, session, false); got != want {
			t.Errorf("%q went to %q, want %q", session, got, want)
		}
	}
	if got := queenEngineRoute(s, "c1", true); got != "" {
		t.Errorf("a turn with pictures went to %q", got)
	}
	worker := &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker, QueenEngine: "claude"}}
	if got := queenEngineRoute(worker, "c1", false); got != "" {
		t.Errorf("a worker's own chat went to %q", got)
	}
	if got := queenEngineRoute(&backend.Settings{}, "c1", false); got != "" {
		t.Errorf("no engine set, yet it went to %q", got)
	}
}

func TestTheQueenEngineAnswersStreamsTaggedAndIsKept(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	id := h.a.SendChat("c1", "现在 hive 里有哪些成员？", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" {
		t.Fatalf("%s %v", name, done)
	}
	if done["final"] != "hive 里有两个成员。" || done["engine"] != "claude" || done["costUsd"] != 0.0123 {
		t.Fatalf("done %v", done)
	}
	if strings.Contains(done["final"].(string), "**@claude**") {
		t.Fatal("a queen reply carries the @mention header")
	}
	var streamed string
	for _, p := range h.ev.all("chat:event", id) {
		if p["engine"] != "claude" {
			t.Fatalf("an event without its engine: %v", p)
		}
		if p["type"] == "partial" {
			streamed += p["content"].(string)
		}
	}
	if streamed != "hive 里有两个成员。" {
		t.Fatalf("streamed %q: the whole message came again after its deltas, or not at all", streamed)
	}
	if len(h.stored) != 1 || h.stored[0] != [2]string{"现在 hive 里有哪些成员？", "hive 里有两个成员。"} {
		t.Fatalf("stored %v", h.stored)
	}
	turns := withTurnTimes("c1", []backend.ChatTurn{{Role: "user", Content: "现在 hive 里有哪些成员？"}, {Role: "assistant", Content: "hive 里有两个成员。"}})
	if turns[1].Engine != "claude" || turns[1].CostUSD != 0.0123 {
		t.Fatalf("history %+v", turns[1])
	}
	if len(h.owned()) != 0 {
		t.Fatalf("SuperAI's own model ran too: %v", h.owned())
	}

	args := fakeArgs(t, h.dir, 1)
	persona := argAfter(args, "--append-system-prompt")
	if !strings.Contains(persona, "In the hive you are q, the queen.") || !strings.Contains(persona, "superai_hive_x") {
		t.Fatalf("persona %q", persona)
	}
	for _, want := range []string{"--strict-mcp-config", "--include-partial-messages"} {
		if !slices.Contains(args, want) {
			t.Fatalf("%s missing from %q", want, args)
		}
	}
	// Lean: none of the person's own settings, skills or auto-memory.
	if i := slices.Index(args, "--setting-sources"); i < 0 || args[i+1] != "" || !slices.Contains(args, "--disable-slash-commands") {
		t.Fatalf("not a lean run: %q", args)
	}
	if lean, _ := os.ReadFile(filepath.Join(h.dir, "lean.1")); string(lean) != "1" {
		t.Fatalf("auto-memory left on: %q", lean)
	}
	if fakePrompt(t, h.dir, 1) != "现在 hive 里有哪些成员？" {
		t.Fatalf("a fresh conversation was handed earlier turns: %q", fakePrompt(t, h.dir, 1))
	}
	if argAfter(args, "--allowedTools") != "mcp__hive" {
		t.Fatalf("allowed %q", argAfter(args, "--allowedTools"))
	}
	if slices.Contains(args, "--permission-prompt-tool") {
		t.Fatal("an unattended run asked for permission")
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(argAfter(args, "--mcp-config")), &cfg); err != nil {
		t.Fatal(err)
	}
	hive := cfg.MCPServers["hive"]
	if hive.URL != "http://127.0.0.1:43999/mcp" || hive.Headers["Authorization"] != "Bearer ${SUPERAI_QUEEN_TOKEN}" {
		t.Fatalf("hive server %+v", hive)
	}

	tok, _ := os.ReadFile(filepath.Join(h.dir, "token.1"))
	if !strings.HasPrefix(string(tok), queenTokenPrefix) {
		t.Fatalf("the CLI was not handed a queen token: %q", tok)
	}
	store, _ := openIntegrations(integrationsPath())
	if !store.holds(string(tok), scopeQueen) {
		t.Fatal("the token the CLI got is not a live queen token")
	}
	if strings.Contains(strings.Join(args, " "), string(tok)) {
		t.Fatal("the token is on the command line")
	}
	runs, _ := os.ReadFile(cliRunsPath())
	if strings.Contains(string(runs), string(tok)) || strings.Contains(logs.String(), string(tok)) {
		t.Fatal("the token was written down")
	}
}

func TestTheNextTurnInAConversationResumesItsEngineSession(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.ev.wait(t, h.a.SendChat("c1", "一", nil))
	_, second := h.ev.wait(t, h.a.SendChat("c1", "二", nil))
	// The fake reports the session's running total, as Claude does: the
	// same total again is a turn that added nothing.
	if second["costUsd"] != nil {
		t.Fatalf("the second turn was charged the first one's cost: %v", second)
	}
	h.ev.wait(t, h.a.SendChat("c2", "三", nil))
	if got := argAfter(fakeArgs(t, h.dir, 1), "--resume"); got != "" {
		t.Fatalf("the first turn resumed %q", got)
	}
	if got := argAfter(fakeArgs(t, h.dir, 2), "--resume"); got != "sess-1" {
		t.Fatalf("the second turn resumed %q", got)
	}
	if got := argAfter(fakeArgs(t, h.dir, 3), "--resume"); got != "" {
		t.Fatalf("another conversation resumed %q", got)
	}
	// The queen's persona goes with every turn, the resumed one too.
	if argAfter(fakeArgs(t, h.dir, 2), "--append-system-prompt") == "" {
		t.Fatal("a resumed turn was not told it is the queen")
	}
}

func TestAnAtMentionOfTheSameCLIIsStillAnAtMention(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	_, done := h.ev.wait(t, h.a.SendChat("c1", "@claude 看一下测试", nil))
	if !strings.HasPrefix(fmt.Sprint(done["final"]), "**@claude**") || done["engine"] != nil {
		t.Fatalf("done %v", done)
	}
	if argAfter(fakeArgs(t, h.dir, 1), "--append-system-prompt") != "" {
		t.Fatal("an @claude run was told it is the queen")
	}
	// And the queen's next turn does not continue that session.
	h.ev.wait(t, h.a.SendChat("c1", "hive 怎么样", nil))
	if got := argAfter(fakeArgs(t, h.dir, 2), "--resume"); got != "" {
		t.Fatalf("the queen resumed the @claude session %q", got)
	}
}

func TestACallAndSuperAIAskStayWithTheOwnModel(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.ev.wait(t, h.a.SendCallChat("c1", "你好", nil))
	if own := h.owned(); len(own) != 1 || own[0].standIn != "" {
		t.Fatalf("own %v", own)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "args.1")); err == nil {
		t.Fatal("a call started the engine")
	}
}

func TestSuperAIStandsInForAnEngineThatCannotStart(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.ExternalAgents.Enabled = false
	id := h.a.SendChat("c1", "你好", nil)
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatal(name)
	}
	if own := h.owned(); len(own) != 1 || own[0].standIn != "claude" || own[0].message != "你好" {
		t.Fatalf("own %v", own)
	}
	if !strings.HasPrefix(standInNote("claude.mac"), "> Claude Code · mac ") {
		t.Fatal(standInNote("claude.mac"))
	}
	recordTurnTimeBy("c9", "你好", time.Now(), time.Second, turnBy{StoodInFor: "claude"})
	turns := withTurnTimes("c9", []backend.ChatTurn{{Role: "user", Content: "你好"}, {Role: "assistant", Content: "在呢"}})
	if turns[1].Content != standInNote("claude")+"在呢" || turns[1].Engine != "" {
		t.Fatalf("history %+v", turns[1])
	}
}

func TestSuperAIStandsInForAnEngineThatFailsBeforeSayingAnything(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeLoggedOut, 1)
	h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if own := h.owned(); len(own) != 1 || own[0].standIn != "claude" {
		t.Fatalf("own %v", own)
	}
}

func TestAnEngineThatCannotStartHandsTheTurnToItsBackup(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	// No linked machine called nowhere: it cannot start, and claude here can.
	h.a.settings.Hive.QueenEngine = "claude.nowhere"
	h.a.settings.Hive.QueenBackups = []string{"claude"}
	id := h.a.SendChat("c1", "你好", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["engine"] != "claude" || done["final"] != "hive 里有两个成员。" {
		t.Fatalf("%s %v", name, done)
	}
	if own := h.owned(); len(own) != 0 {
		t.Fatalf("SuperAI answered while a backup could: %v", own)
	}
	tried := false
	for _, p := range h.ev.all("chat:event", id) {
		if c, _ := p["content"].(string); strings.Contains(c, "trying Claude Code") {
			tried = true
		}
	}
	if !tried {
		t.Fatal("the turn moved to the backup without a word")
	}
	if done["backupFor"] != "claude.nowhere" {
		t.Fatalf("the answer does not say it came from a backup: %v", done)
	}
	if now := h.a.queenEngineNow(); now != "claude" {
		t.Fatalf("now %q", now)
	}
	var said []any
	h.ev.mu.Lock()
	for _, g := range h.ev.got {
		if g.name == "queen:engine" {
			said = append(said, g.p["engine"])
		}
	}
	h.ev.mu.Unlock()
	if fmt.Sprint(said) != "[claude.nowhere claude]" {
		t.Fatalf("queen:engine said %v", said)
	}
	turns := withTurnTimes("c1", []backend.ChatTurn{{Role: "user", Content: "你好"}, {Role: "assistant", Content: "hive 里有两个成员。"}})
	if turns[1].Engine != "claude" || turns[1].BackupFor != "claude.nowhere" {
		t.Fatalf("history %+v", turns[1])
	}
	tr, err := h.a.TurnTrace(id[:8])
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != "done" || tr.By != "claude, backup for claude.nowhere" || fmt.Sprint(tr.Engines) != "[claude.nowhere claude]" || len(tr.Events) == 0 {
		t.Fatalf("trace %+v", tr)
	}
	if list := h.a.TurnTraces(5); len(list) != 1 || list[0].ID != id || list[0].Events != nil {
		t.Fatalf("traces %+v", list)
	}
}

func TestSuperAIStandsInOnlyWhenEveryBackupFailedToo(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeLoggedOut, 1)
	h.a.settings.Hive.QueenEngine = "claude.nowhere"
	h.a.settings.Hive.QueenBackups = []string{"claude", "claude.nowhere", ""}
	h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if own := h.owned(); len(own) != 1 || own[0].standIn != "claude" {
		t.Fatalf("own %v", own)
	}
	if now := h.a.queenEngineNow(); now != queenNowSuperAI {
		t.Fatalf("now %q", now)
	}
	if tr := h.a.TurnTraces(1); len(tr) != 1 || fmt.Sprint(tr[0].Engines) != "[claude.nowhere claude superai]" {
		t.Fatalf("traces %+v", tr)
	}
	for _, it := range h.a.Attention() {
		if it.Kind == "run" {
			t.Fatalf("a queen run that was handed on still asks for the person: %+v", it)
		}
	}
	if runs := len(h.a.cliStore().runs); runs != 1 {
		t.Fatalf("%d runs: a backup was tried twice", runs)
	}
	if chain := h.a.settings.Hive.QueenChain(); !slices.Equal(chain, []string{"claude.nowhere", "claude"}) {
		t.Fatalf("chain %v", chain)
	}
}

func TestWithTheFallbackOffAnEngineThatCannotAnswerFailsTheTurn(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeLoggedOut, 1)
	off := false
	h.a.settings.Hive.QueenFallback = &off
	name, p := h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if name != "chat:error" || !strings.Contains(fmt.Sprint(p["error"]), "Invalid API key") || p["engine"] != "claude" {
		t.Fatalf("%s %v", name, p)
	}
	if len(h.owned()) != 0 {
		t.Fatal("SuperAI answered with the fallback off")
	}
}

func TestAnAttendedEngineAsksThroughTheApprovalChannel(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.ExternalAgents.Unattended = false
	h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	args := fakeArgs(t, h.dir, 1)
	if argAfter(args, "--permission-prompt-tool") != "mcp__superai__approve" {
		t.Fatalf("args %q", args)
	}
	var cfg struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal([]byte(argAfter(args, "--mcp-config")), &cfg)
	if cfg.MCPServers["superai"] == nil || cfg.MCPServers["hive"] == nil {
		t.Fatalf("servers %v", cfg.MCPServers)
	}
	// The --allowedTools list is never the last flag before the prompt.
	if i := slices.Index(args, "--allowedTools"); i < 0 || i+2 >= len(args)-1 {
		t.Fatalf("args %q", args)
	}
}

func TestAQueenRunAsksAboutAToolWhenTheQueenWould(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{gateForMCP: backend.NewToolGate(true, filepath.Join(t.TempDir(), "audit.jsonl"))}
	if !a.queenMayCall("mcp__noted__list_tasks", map[string]any{}) {
		t.Fatal("a read was asked about")
	}
	if a.queenMayCall("mcp__noted__delete_task", map[string]any{"id": "1"}) {
		t.Fatal("a delete went through unasked")
	}
	if a.queenMayCall("Bash", map[string]any{"command": "ls"}) {
		t.Fatal("Claude's own shell went through unasked")
	}
}

func TestTheQueenTokenIsMintedOnceAndAgainWhenRevoked(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{}
	first, err := a.queenEngineToken()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := a.queenEngineToken()
	if first != again || !strings.HasPrefix(first, queenTokenPrefix) {
		t.Fatalf("%q then %q", first, again)
	}
	listed, _ := a.ListIntegrationTokens()
	if len(listed) != 1 || listed[0].Scope != "queen" || listed[0].Name != queenEngineTokenName {
		t.Fatalf("listed %+v", listed)
	}
	if err := a.RevokeIntegrationToken(listed[0].ID); err != nil {
		t.Fatal(err)
	}
	rotated, _ := a.queenEngineToken()
	if rotated == first {
		t.Fatal("a revoked token was handed out again")
	}
	// A restart has lost the secret: the old token goes, a new one comes.
	b := &App{}
	fresh, _ := b.queenEngineToken()
	store, _ := openIntegrations(integrationsPath())
	if fresh == rotated || store.holds(rotated, scopeQueen) || !store.holds(fresh, scopeQueen) {
		t.Fatal("the token of a previous run was kept")
	}
	if listed, _ := a.ListIntegrationTokens(); len(listed) != 1 {
		t.Fatalf("listed %+v", listed)
	}
}

func TestAnEngineThatIsThereCanBeTheEngine(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	bin, _ := fakeClaude(t, "", 0)
	a := &App{settings: &backend.Settings{}}
	check := func(engine string, ext backend.ExternalAgents, prev string) error {
		return a.validQueenEngine(&backend.Settings{ExternalAgents: ext, Hive: backend.HiveSettings{QueenEngine: engine}}, prev)
	}
	on := backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"claude": bin}}
	if err := check("", backend.ExternalAgents{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := check("claude", on, ""); err != nil {
		t.Fatal(err)
	}
	if err := check("claude", backend.ExternalAgents{Binaries: on.Binaries}, ""); err == nil {
		t.Fatal("claude passed with External agents off")
	}
	if err := check("claude", backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"claude": filepath.Join(t.TempDir(), "none")}}, ""); err == nil {
		t.Fatal("a claude that is not installed passed")
	}
	if err := check("codex", on, ""); err == nil {
		t.Fatal("a codex that is not installed passed")
	}
	if err := check("claude.nowhere", on, ""); err == nil {
		t.Fatal("a machine that is not linked passed")
	}
	if err := check("claude.mac", on, "claude.mac"); err != nil {
		t.Fatalf("the saved engine was refused while its machine is away: %v", err)
	}
	if err := a.validQueenEngine(&backend.Settings{Hive: backend.HiveSettings{QueenBackups: []string{"claude.asleep", "codex"}}}, ""); err != nil {
		t.Fatalf("a backup on a machine that is away was refused: %v", err)
	}
	if err := a.validQueenEngine(&backend.Settings{Hive: backend.HiveSettings{QueenBackups: []string{"vim"}}}, ""); err == nil {
		t.Fatal("a backup that cannot think for the queen passed")
	}
	if !(backend.HiveSettings{}).QueenFallsBack() {
		t.Fatal("the fallback is off by default")
	}
}

func TestAnotherMachineRunsOnlyServersItReachesByURL(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{}
	_, err := a.StartQueenCLIRun(QueenRunRequest{Agent: "claude", Prompt: "hi", Kit: QueenKit{
		Servers: map[string]any{"x": map[string]any{"command": "/bin/sh", "args": []any{"-c", "id"}}}}})
	if err == nil || !strings.Contains(err.Error(), "not one reached by URL") {
		t.Fatalf("%v", err)
	}
}

func TestAnotherMachineTakesOnlyTheLeanFlagsAndTheServersVariables(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{settings: &backend.Settings{}}
	hive := map[string]any{"hive": map[string]any{"type": "http", "url": "https://core.example/mcp",
		"headers": map[string]any{"Authorization": "Bearer ${SUPERAI_QUEEN_TOKEN}"}}}
	for _, kit := range []QueenKit{
		{Servers: hive, Args: []string{"--dangerously-skip-permissions"}},
		{Servers: hive, Env: map[string]string{"NODE_OPTIONS": "--require /tmp/x.js"}},
	} {
		if _, err := a.StartQueenCLIRun(QueenRunRequest{Agent: "claude", Prompt: "hi", Kit: kit}); err == nil ||
			!strings.Contains(err.Error(), "not") {
			t.Fatalf("%+v was taken: %v", kit, err)
		}
	}
	// The lean kit passes those checks, and then stops at External agents
	// being off here.
	_, err := a.StartQueenCLIRun(QueenRunRequest{Agent: "claude", Prompt: "hi", Kit: QueenKit{Servers: hive,
		Args: queenLeanArgs, Env: map[string]string{"SUPERAI_QUEEN_TOKEN": "t", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"}}})
	if err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("%v", err)
	}
}

func TestTheEngineIsToldWhatWasSaidWithoutIt(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.base = []backend.ChatTurn{
		{Role: "user", Content: "早上好"}, {Role: "assistant", Content: "早，今天有雨"},
		{Role: "user", Kind: backend.TurnKindContext, Content: "recalled memory"},
		{Role: "user", Content: "今天几号"}, {Role: "assistant", Content: "17 号"},
	}

	// Taking over a conversation SuperAI's agent had: the earlier turns go
	// first, oldest first, marked as such.
	h.ev.wait(t, h.a.SendChat("c1", "hive 怎么样", nil))
	p := fakePrompt(t, h.dir, 1)
	if !strings.HasPrefix(p, "[Earlier in this conversation") || !strings.HasSuffix(p, "hive 怎么样") ||
		strings.Index(p, "Person: 早上好") > strings.Index(p, "Queen: 17 号") || strings.Contains(p, "recalled memory") {
		t.Fatalf("prompt %q", p)
	}

	// Its own session continued: nothing to hand over.
	h.ev.wait(t, h.a.SendChat("c1", "再说一遍", nil))
	if p := fakePrompt(t, h.dir, 2); p != "再说一遍" {
		t.Fatalf("a continued session was handed history: %q", p)
	}

	// A turn SuperAI's agent stood in for, and a call: both are news to it.
	h.a.mu.Lock()
	h.a.settings.ExternalAgents.Enabled = false
	h.a.mu.Unlock()
	h.ev.wait(t, h.a.SendChat("c1", "断网时问的", nil))
	h.a.mu.Lock()
	h.a.settings.ExternalAgents.Enabled = true
	h.a.mu.Unlock()
	h.ev.wait(t, h.a.SendCallChat("c1", "通话里说的", nil))
	h.ev.wait(t, h.a.SendChat("c1", "接着来", nil))
	p = fakePrompt(t, h.dir, 3)
	for _, want := range []string{"Person: 断网时问的", "没能接这一条", "Person: 通话里说的", "Queen: own: 通话里说的"} {
		if !strings.Contains(p, want) {
			t.Fatalf("%q missing from %q", want, p)
		}
	}
	if strings.Contains(p, "早上好") || strings.Contains(p, "再说一遍") || !strings.HasSuffix(p, "接着来") {
		t.Fatalf("handed what it had already heard: %q", p)
	}
}

func TestHandedHistoryStaysSmall(t *testing.T) {
	var turns []backend.ChatTurn
	for i := range 20 {
		turns = append(turns, backend.ChatTurn{Role: "user", Content: fmt.Sprintf("q%d %s", i, strings.Repeat("长", 3000))})
	}
	got := handoffText(turns)
	if len(got) > handoffTotalMax+300 || !strings.Contains(got, "q19") || strings.Contains(got, "q13") {
		t.Fatalf("%d bytes: %q…", len(got), got[:80])
	}
	if handoffText(nil) != "" {
		t.Fatal("nothing to hand over still made a preamble")
	}
}

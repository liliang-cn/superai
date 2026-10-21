package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superai/internal/backend"
)

// queenTestKit is a kit as queenKitFor makes one: the hive by URL with its
// token by name, noted as a command with its own.
func queenTestKit() *QueenKit {
	return &QueenKit{
		SystemPrompt: "In the hive you are q, the queen. Say \"hi\".\nSecond line.",
		Servers: map[string]any{
			"hive": map[string]any{"type": "http", "url": "http://127.0.0.1:43999/mcp",
				"headers": map[string]any{"Authorization": "Bearer ${SUPERAI_QUEEN_TOKEN}"}},
			"noted": map[string]any{"command": "/opt/noted", "args": []any{"mcp"},
				"env": map[string]any{"NOTED_TOKEN": "${NOTED_TOKEN}", "NOTED_MODE": "stdio"}},
		},
		Allowed: []string{"mcp__hive"},
		Env:     map[string]string{queenTokenEnv: "sai_secret_q", "NOTED_TOKEN": "noted_secret"},
	}
}

// buildQueen applies a CLI's adapter and builds its command as a run would.
func buildQueen(t *testing.T, agent string, provider agentexec.Provider, ask bool, approve string) (agentexec.CommandSpec, agentexec.Request) {
	t.Helper()
	req := agentexec.Request{Prompt: "现在 hive 里有哪些成员？", WorkspacePath: t.TempDir(),
		PermissionMode: agentexec.PermissionBypass, Env: map[string]string{}}
	if err := queenCLIs[agent].apply(&req, queenTestKit(), ask, approve); err != nil {
		t.Fatal(err)
	}
	spec, err := provider.NewSession().BuildCommand(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spec.Argv {
		if strings.Contains(a, "sai_secret_q") || strings.Contains(a, "noted_secret") {
			t.Fatalf("a token is on the command line: %q", spec.Argv)
		}
	}
	return spec, req
}

func overrides(argv []string) map[string]string {
	out := map[string]string{}
	for i, a := range argv {
		if a == "-c" && i+1 < len(argv) {
			k, v, _ := strings.Cut(argv[i+1], "=")
			out[k] = v
		}
	}
	return out
}

func TestCodexIsHandedTheQueensServersAndPromptAsConfig(t *testing.T) {
	spec, req := buildQueen(t, "codex", agentexec.NewCodex(agentexec.WithBinary("codex")), false, "")
	for _, want := range []string{"--ignore-user-config", "hooks", "--json"} {
		if !slices.Contains(spec.Argv, want) {
			t.Fatalf("%s missing from %q", want, spec.Argv)
		}
	}
	c := overrides(spec.Argv)
	// Every override is TOML codex can read, the prompt with its quotes and
	// lines intact.
	var got struct {
		DeveloperInstructions string `toml:"developer_instructions"`
		Hive                  struct {
			URL      string `toml:"url"`
			Bearer   string `toml:"bearer_token_env_var"`
			Approval string `toml:"default_tools_approval_mode"`
		} `toml:"hive"`
		Noted struct {
			Command string            `toml:"command"`
			Args    []string          `toml:"args"`
			Env     map[string]string `toml:"env"`
			EnvVars []string          `toml:"env_vars"`
			Mode    string            `toml:"default_tools_approval_mode"`
		} `toml:"noted"`
	}
	doc := "developer_instructions = " + c["developer_instructions"] + "\nhive = " + c["mcp_servers.hive"] + "\nnoted = " + c["mcp_servers.noted"]
	if _, err := toml.Decode(doc, &got); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	if got.DeveloperInstructions != queenTestKit().SystemPrompt {
		t.Fatalf("prompt %q", got.DeveloperInstructions)
	}
	if got.Hive.URL != "http://127.0.0.1:43999/mcp" || got.Hive.Bearer != queenTokenEnv || got.Hive.Approval != "approve" {
		t.Fatalf("hive %+v", got.Hive)
	}
	// Only the hive's tools are let through unasked: its changes wait on
	// this app's gate.
	if got.Noted.Command != "/opt/noted" || !slices.Equal(got.Noted.Args, []string{"mcp"}) ||
		got.Noted.Env["NOTED_MODE"] != "stdio" || !slices.Equal(got.Noted.EnvVars, []string{"NOTED_TOKEN"}) || got.Noted.Mode != "" {
		t.Fatalf("noted %+v", got.Noted)
	}
	if req.Env[queenTokenEnv] != "sai_secret_q" || req.Env["NOTED_TOKEN"] != "noted_secret" {
		t.Fatal("the tokens are not in the CLI's environment")
	}
	// The prompt is codex's, not in front of the question.
	if spec.Argv[len(spec.Argv)-1] != "现在 hive 里有哪些成员？" {
		t.Fatalf("prompt %q", spec.Argv[len(spec.Argv)-1])
	}

	// A resumed turn is handed the same again.
	req.ResumeSessionID = "th-1"
	resumed, _ := agentexec.NewCodex(agentexec.WithBinary("codex")).NewSession().BuildCommand(context.Background(), req)
	if !slices.Equal(resumed.Argv[:4], []string{"codex", "exec", "resume", "th-1"}) || overrides(resumed.Argv)["mcp_servers.hive"] == "" {
		t.Fatalf("resumed %q", resumed.Argv)
	}
}

func TestCodexTakesAServerOnlyAsItCanBeWritten(t *testing.T) {
	for _, c := range []struct {
		name  string
		entry map[string]any
		want  map[string]any
	}{
		{"headers", map[string]any{"url": "https://x/mcp", "headers": map[string]any{"X-Key": "${K}", "X-Plain": "v"}},
			map[string]any{"url": "https://x/mcp", "env_http_headers": map[string]any{"X-Key": "K"}, "http_headers": map[string]any{"X-Plain": "v"}}},
		{"a token in the URL", map[string]any{"url": "https://x/mcp?k=${K}"}, nil},
		{"a header half a variable", map[string]any{"url": "https://x/mcp", "headers": map[string]any{"X": "a ${K}"}}, nil},
		{"a variable under another name", map[string]any{"command": "x", "env": map[string]any{"A": "${B}"}}, nil},
		{"neither", map[string]any{"type": "http"}, nil},
	} {
		got, err := codexServer(c.entry)
		if c.want == nil {
			if err == nil {
				t.Errorf("%s: taken as %v", c.name, got)
			}
			continue
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(c.want)
		if err != nil || string(a) != string(b) {
			t.Errorf("%s: %s, %v; want %s", c.name, a, err, b)
		}
	}
}

func TestPiIsHandedTheQueenThroughSuperAIsExtension(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	const approve = "http://127.0.0.1:1/approve/r/s"
	spec, req := buildQueen(t, "pi", agentexec.NewPi(agentexec.WithBinary("pi")), true, approve)
	var exts []string
	for i, a := range spec.Argv {
		if a == "-e" && i+1 < len(spec.Argv) {
			exts = append(exts, spec.Argv[i+1])
		}
	}
	// pi's own MCP support, named again after --no-extensions, and SuperAI's.
	if len(exts) != 4 || !slices.Contains(exts, "builtin:mcp") || !slices.Contains(exts, "builtin:tool-search") {
		t.Fatalf("extensions %q", exts)
	}
	ext := exts[3]
	src, err := os.ReadFile(ext)
	if err != nil || !strings.Contains(string(src), "registerMcpServer") || !strings.HasPrefix(ext, backend.DataDir()) {
		t.Fatalf("extension %q: %v", ext, err)
	}
	// Only SuperAI's extension: none of the person's, nor their skills,
	// templates, AGENTS.md or project files.
	for _, want := range []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve", "--print"} {
		if !slices.Contains(spec.Argv, want) {
			t.Fatalf("%s missing from %q", want, spec.Argv)
		}
	}
	if argAfter(spec.Argv, "--append-system-prompt") != queenTestKit().SystemPrompt {
		t.Fatalf("prompt %q", spec.Argv)
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal([]byte(req.Env[piServersEnv]), &servers); err != nil {
		t.Fatal(err)
	}
	hive := servers["hive"]
	if hive["url"] != "http://127.0.0.1:43999/mcp" || hive["exposure"] != "direct" || servers["noted"]["exposure"] != "deferred" {
		t.Fatalf("servers %v", servers)
	}
	if h, _ := hive["headers"].(map[string]any); h["Authorization"] != "Bearer ${SUPERAI_QUEEN_TOKEN}" {
		t.Fatalf("the hive's token is not left to the extension: %v", hive)
	}
	if req.Env[piApproveEnv] != approve || req.Env[piAllowedEnv] != "mcp__hive" || req.Env[queenTokenEnv] != "sai_secret_q" {
		t.Fatalf("env %v", req.Env)
	}
	// Unattended: no approval channel to ask.
	_, req = buildQueen(t, "pi", agentexec.NewPi(agentexec.WithBinary("pi")), false, "")
	if _, ok := req.Env[piApproveEnv]; ok {
		t.Fatal("an unattended run asks")
	}
}

func TestHermesIsHandedTheQueensServersInAHomeOfItsOwn(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	own := t.TempDir()
	t.Setenv("HERMES_HOME", own)
	spec, req := buildQueen(t, "hermes", agentexec.NewHermes(agentexec.WithBinary("hermes")), true, "")
	defer os.Remove(argAfter(spec.Argv, "--usage-file"))
	// The person's own hermes home is the layer over it: their model and keys.
	if req.Env["HERMES_MANAGED_DIR"] != own {
		t.Fatalf("managed %q", req.Env["HERMES_MANAGED_DIR"])
	}
	home := req.Env["HERMES_HOME"]
	raw, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil || !strings.HasPrefix(home, backend.DataDir()) {
		t.Fatalf("%s: %v", home, err)
	}
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcp_servers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers["hive"]["url"] != "http://127.0.0.1:43999/mcp" || cfg.MCPServers["hive"]["type"] != nil || cfg.MCPServers["noted"]["command"] != "/opt/noted" {
		t.Fatalf("config %s", raw)
	}
	if strings.Contains(string(raw), "sai_secret_q") || strings.Contains(string(raw), "noted_secret") {
		t.Fatal("a token was written into the config")
	}
	// One-shot mode takes no prompt of its own: it goes in front.
	if p := argAfter(spec.Argv, "--oneshot"); !strings.HasPrefix(p, queenTestKit().SystemPrompt) || !strings.HasSuffix(p, "现在 hive 里有哪些成员？") {
		t.Fatalf("prompt %q", p)
	}
	// Attended, it has no shell: hermes would run one unasked.
	if got := argAfter(spec.Argv, "--toolsets"); got != "safe,hive,noted" {
		t.Fatalf("toolsets %q", got)
	}
	spec, _ = buildQueen(t, "hermes", agentexec.NewHermes(agentexec.WithBinary("hermes")), false, "")
	defer os.Remove(argAfter(spec.Argv, "--usage-file"))
	if slices.Contains(spec.Argv, "--toolsets") {
		t.Fatalf("an unattended run was limited: %q", spec.Argv)
	}
}

func TestEveryCLIsEventsReadTheSame(t *testing.T) {
	calls := map[string]string{}
	norm := func(e agentexec.Event) []CLIRunEvent { return normaliseCLIEvent(e, calls) }
	// pi's deltas are deltas, and the message after them text.
	if got := norm(agentexec.Event{Type: agentexec.EventAgentMessage, Payload: map[string]any{"role": "assistant", "text": "两个", "delta": true}}); len(got) != 1 || got[0].Kind != "delta" {
		t.Fatalf("pi delta %+v", got)
	}
	// pi's call and its end.
	norm(agentexec.Event{Type: agentexec.EventToolCall, Payload: map[string]any{"id": "c1", "name": "mcp__hive__superai_hive_members", "input": map[string]any{}}})
	got := norm(agentexec.Event{Type: agentexec.EventToolResult, Payload: map[string]any{"id": "c1", "name": "mcp__hive__superai_hive_members",
		"output": map[string]any{"content": []any{map[string]any{"type": "text", "text": "q, w1"}}}, "is_error": false}})
	if len(got) != 1 || got[0].Kind != "result" || got[0].Text != "q, w1" || got[0].Tool != "mcp__hive__superai_hive_members" || got[0].Failed {
		t.Fatalf("pi result %+v", got)
	}
	// Codex's MCP call, reported once with what came back.
	got = norm(agentexec.Event{Type: agentexec.EventAgentMessage, Payload: map[string]any{"role": "mcp_tool_call", "raw": map[string]any{
		"id": "i1", "type": "mcp_tool_call", "server": "hive", "tool": "superai_hive_members", "arguments": map[string]any{"x": 1.0},
		"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "q, w1"}}}, "status": "completed"}}})
	if len(got) != 2 || got[0].Kind != "tool" || shortTool(got[0].Tool) != "superai_hive_members" || got[0].Detail != `{"x":1}` ||
		got[1].Kind != "result" || got[1].Text != "q, w1" || got[1].Failed {
		t.Fatalf("codex mcp call %+v", got)
	}
	got = norm(agentexec.Event{Type: agentexec.EventAgentMessage, Payload: map[string]any{"role": "mcp_tool_call", "raw": map[string]any{
		"id": "i2", "type": "mcp_tool_call", "server": "hive", "tool": "x", "error": map[string]any{"message": "denied"}, "status": "failed"}}})
	if len(got) != 2 || !got[1].Failed || got[1].Text != "denied" {
		t.Fatalf("codex failed call %+v", got)
	}
}

// fakeAgent is a stand-in for an agent CLI by name: it answers --version,
// keeps its arguments, the queen token and HERMES_MANAGED_DIR, and prints
// out.
func fakeAgent(t *testing.T, name, out string) (bin, dir string) {
	t.Helper()
	t.Setenv("PATH", "/usr/bin:/bin")
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out"), []byte(out+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "%s 1.0.0"; exit 0; fi
d=%q
n=$(ls "$d" | grep -c '^args\.')
n=$((n+1))
for a in "$@"; do printf '%%s\n\036\n' "$a"; done > "$d/args.$n"
printf '%%s' "$SUPERAI_QUEEN_TOKEN" > "$d/token.$n"
printf '%%s' "$HERMES_MANAGED_DIR" > "$d/managed.$n"
cat "$d/out"
`, name, dir)
	bin = filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func newCLIEngineHarness(t *testing.T, name, out string) *engineHarness {
	t.Helper()
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	bin, dir := fakeAgent(t, name, out)
	h.dir = dir
	h.a.settings.ExternalAgents.Binaries = map[string]string{name: bin}
	h.a.settings.Hive.QueenEngine = name
	return h
}

const fakeCodexTurn = `{"type":"thread.started","thread_id":"th-1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i1","type":"mcp_tool_call","server":"hive","tool":"superai_hive_members","arguments":{},"result":{"content":[{"type":"text","text":"q, w1"}]},"status":"completed"}}
{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"hive 里有两个成员。"}}
{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`

const fakePiTurn = `{"type":"session","id":"pi-1","cwd":"/w"}
{"type":"tool_execution_start","toolCallId":"c1","toolName":"mcp__hive__superai_hive_members","args":{}}
{"type":"tool_execution_end","toolCallId":"c1","toolName":"mcp__hive__superai_hive_members","result":{"content":[{"type":"text","text":"q, w1"}]},"isError":false}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hive 里有"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"两个成员。"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hive 里有两个成员。"}],"usage":{"input":10,"output":5,"cost":{"total":0.001}},"stopReason":"stop"}}
{"type":"agent_end"}`

// Codex, pi and Hermes each answer a turn, the answer streamed and tagged
// with them, the call they made shown, and the next turn continuing where
// the CLI can.
func TestCodexPiAndHermesThinkForTheQueen(t *testing.T) {
	for _, c := range []struct {
		name, out, resume, session string
		tool                       bool
	}{
		{"codex", fakeCodexTurn, "resume", "th-1", true},
		{"pi", fakePiTurn, "--session", "pi-1", true},
		{"hermes", "hive 里有两个成员。", "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newCLIEngineHarness(t, c.name, c.out)
			id := h.a.SendChat("c1", "现在 hive 里有哪些成员？", nil)
			name, done := h.ev.wait(t, id)
			if name != "chat:done" || done["final"] != "hive 里有两个成员。" || done["engine"] != c.name {
				t.Fatalf("%s %v", name, done)
			}
			var streamed string
			sawTool := false
			for _, p := range h.ev.all("chat:event", id) {
				switch p["type"] {
				case "partial":
					streamed += p["content"].(string)
				case "tool_call":
					sawTool = p["tool"] == "superai_hive_members"
				}
			}
			if streamed != "hive 里有两个成员。" || sawTool != c.tool {
				t.Fatalf("streamed %q, tool %v", streamed, sawTool)
			}
			if len(h.owned()) != 0 {
				t.Fatalf("SuperAI's own model ran: %v", h.owned())
			}
			if tok, _ := os.ReadFile(filepath.Join(h.dir, "token.1")); !strings.HasPrefix(string(tok), queenTokenPrefix) {
				t.Fatalf("no queen token: %q", tok)
			}

			h.ev.wait(t, h.a.SendChat("c1", "再说一遍", nil))
			args := fakeArgs(t, h.dir, 2)
			prompt := args[len(args)-1]
			if c.resume == "" {
				// No session to continue: the conversation comes again.
				if slices.Contains(args, "--resume") || !strings.Contains(prompt, "现在 hive 里有哪些成员？") || !strings.HasSuffix(prompt, "再说一遍") {
					t.Fatalf("args %q", args)
				}
				return
			}
			if got := argAfter(args, c.resume); got != c.session {
				t.Fatalf("resumed %q: %q", got, args)
			}
			if strings.Contains(prompt, "现在 hive 里有哪些成员？") {
				t.Fatalf("a resumed session was handed what it heard: %q", prompt)
			}
		})
	}
}

func TestCodexPiAndHermesCanBeTheEngineWhenThere(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{settings: &backend.Settings{}}
	for _, name := range []string{"codex", "pi", "hermes"} {
		bin, _ := fakeAgent(t, name, "")
		on := backend.ExternalAgents{Enabled: true, Binaries: map[string]string{name: bin}}
		if err := a.validQueenEngine(&backend.Settings{ExternalAgents: on, Hive: backend.HiveSettings{QueenEngine: name}}, ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	bin, _ := fakeAgent(t, "gemini", "")
	on := backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"gemini": bin}}
	if err := a.validQueenEngine(&backend.Settings{ExternalAgents: on, Hive: backend.HiveSettings{QueenEngine: "gemini"}}, ""); err == nil {
		t.Fatal("gemini passed")
	}
	if _, err := a.StartQueenCLIRun(QueenRunRequest{Agent: "gemini", Prompt: "hi"}); err == nil {
		t.Fatal("another machine had gemini think for its queen")
	}
}

// The first start offers every CLI that can think, and choosing one makes
// it the queen's engine.
func TestAFirstStartOffersAndTakesEveryEngine(t *testing.T) {
	for _, name := range []string{"codex", "pi", "hermes"} {
		bin, _ := fakeAgent(t, name, "")
		a := onboardingApp(t, backend.Settings{WorkspaceDir: t.TempDir(),
			ExternalAgents: backend.ExternalAgents{Binaries: map[string]string{name: bin}}}, true)
		got := a.DetectAgents()
		if len(got) == 0 || got[0].Name != name || !got[0].CanBeDefault {
			t.Fatalf("%s: found %+v", name, got)
		}
		if err := a.FinishOnboarding(name, true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.settings.Hive.QueenEngine != name || a.settings.ExternalAgents.Binaries[name] != bin {
			t.Fatalf("%s: engine %q", name, a.settings.Hive.QueenEngine)
		}
	}
}

// With no history of its own (no model set up), Hermes still hears what was
// said: from its own earlier runs, each question without what was handed in
// front of it.
func TestHermesHearsTheConversationWithoutAHistoryToKeepIt(t *testing.T) {
	h := newCLIEngineHarness(t, "hermes", "答案")
	h.a.turnsFn = func(string) []backend.ChatTurn { return nil }
	h.a.appendFn = func(string, string, string) error { return nil }
	for _, q := range []string{"一", "二", "三"} {
		h.ev.wait(t, h.a.SendChat("c1", q, nil))
	}
	args := fakeArgs(t, h.dir, 3)
	prompt := args[len(args)-1]
	if strings.Count(prompt, "Person: 一") != 1 || strings.Count(prompt, "Person: 二") != 1 || strings.Count(prompt, "Queen: 答案") != 2 ||
		strings.Count(prompt, "[Earlier in this conversation") != 1 || !strings.HasSuffix(prompt, "三") {
		t.Fatalf("prompt %q", prompt)
	}
}

// Hermes prints its failure where its answer would be: a turn it could not
// take is still SuperAI's to answer, and the failure is not shown as one.
func TestSuperAIStandsInForAHermesThatFailedAtItsProvider(t *testing.T) {
	h := newCLIEngineHarness(t, "hermes", "API call failed after 3 retries: HTTP 429")
	bin := h.a.settings.ExternalAgents.Binaries["hermes"]
	raw, _ := os.ReadFile(bin)
	if err := os.WriteFile(bin, []byte(strings.Replace(string(raw), `cat "$d/out"`, `cat "$d/out"; exit 1`, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	id := h.a.SendChat("c1", "你好", nil)
	if name, done := h.ev.wait(t, id); name != "chat:done" || done["final"] != "own" {
		t.Fatalf("%s %v", name, done)
	}
	for _, p := range h.ev.all("chat:event", id) {
		if p["type"] == "partial" && strings.Contains(fmt.Sprint(p["content"]), "429") {
			t.Fatalf("the failure was shown as the answer: %v", p)
		}
	}
	if own := h.owned(); len(own) != 1 || own[0].standIn != "hermes" {
		t.Fatalf("own %v", own)
	}
}

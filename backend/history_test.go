package backend

import (
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

func TestVisibleTurnsStripsInjectedContext(t *testing.T) {
	messages := []domain.Message{
		{Role: "system", Content: "你是 SuperAI"},
		{Role: "user", Content: "[文档附件，可用 read_document 工具读取]\n- uploads/简历.pdf\n\n他有几年工作经验？"},
		{Role: "user", Content: "<system-reminder>\nToday's date is 2026-07-29.\n</system-reminder>"},
		{Role: "user", Content: "<skill-discovery>\nSkills relevant…\n</skill-discovery>"},
		{Role: "assistant", Content: "", ToolCalls: []domain.ToolCall{{ID: "1"}}},
		{Role: "tool", Content: `{"ok":true}`},
		{Role: "assistant", Content: "他有五年经验。\n情绪: 开心"},
		{Role: "assistant", Content: "   "},
	}

	turns := visibleTurns(messages)
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2 (system/tool/injected/empty must be dropped): %+v", len(turns), turns)
	}
	if turns[0].Role != "user" || turns[0].Content == "" {
		t.Errorf("first turn should be the user's question: %+v", turns[0])
	}
	if turns[1].Role != "assistant" {
		t.Fatalf("second turn should be the assistant: %+v", turns[1])
	}
	if turns[1].Content != "他有五年经验。" {
		t.Errorf("assistant content = %q, emotion tag should be split off", turns[1].Content)
	}
	if turns[1].Emotion != "开心" {
		t.Errorf("emotion = %q, want 开心", turns[1].Emotion)
	}
}

func TestSessionTitle(t *testing.T) {
	cases := []struct {
		name  string
		turns []ChatTurn
		want  string
	}{
		{
			name:  "plain question",
			turns: []ChatTurn{{Role: "user", Content: "查一下黄金的价格"}},
			want:  "查一下黄金的价格",
		},
		{
			name: "attachment preamble uses the real question",
			turns: []ChatTurn{{
				Role:    "user",
				Content: "[文档附件，可用 read_document 工具读取]\n- uploads/简历.pdf\n\n他有几年工作经验？",
			}},
			want: "他有几年工作经验？",
		},
		{
			name:  "assistant-first session still finds the user turn",
			turns: []ChatTurn{{Role: "assistant", Content: "在的"}, {Role: "user", Content: "hi"}},
			want:  "hi",
		},
		{
			name:  "no user turn",
			turns: []ChatTurn{{Role: "assistant", Content: "orphan"}},
			want:  "Untitled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionTitle(tc.turns); got != tc.want {
				t.Errorf("sessionTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionTitleTruncates(t *testing.T) {
	long := ""
	for i := 0; i < 80; i++ {
		long += "字"
	}
	got := sessionTitle([]ChatTurn{{Role: "user", Content: long}})
	if runes := []rune(got); len(runes) != maxTitle+1 || runes[len(runes)-1] != '…' {
		t.Errorf("long title not truncated to %d runes + ellipsis: %d runes", maxTitle, len([]rune(got)))
	}
}

// TestVisibleTurnsHidesToolScripts is the regression for reopening a PTC
// conversation and finding `return "…"` presented as something the assistant
// said. The scripts are stored on purpose — the model needs them next turn — so
// the transcript is where they have to be hidden.
func TestVisibleTurnsHidesToolScripts(t *testing.T) {
	messages := []domain.Message{
		{Role: "user", Content: "搜一下上证指数收盘"},
		// A pure script turn: nothing was said, so nothing should show.
		{Role: "assistant", Content: "<code>\nreturn callTool('mcp_websearch_websearch', {query: '上证指数'});\n</code>"},
		// A whole PTC transcript: script, the tool's results pasted in, script,
		// more results, and a closing sentence. Dropped whole — the answer below
		// is stored separately, and keeping the prose here would keep the results
		// with it.
		{Role: "assistant", Content: "我查一下。\n<code>\nreturn callTool('mcp_websearch_websearch', {query: '上证'});\n</code>Object containing 1 item: \n1. `results`: Array containing 10 items: \n  - `0`: \"3865.23\"<code>\nreturn \"3865.23\";\n</code>上证指数收盘 3865.23 点。"},
		// A turn stored before the agent summarised its own runs.
		{Role: "assistant", Content: `{"results":[{"snippet":"3865.23"}]}`},
		{Role: "assistant", Content: "上证指数收盘 3865.23 点。\n情绪: 开心"},
	}

	turns := visibleTurns(messages)
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2 — the question and the answer: %+v", len(turns), turns)
	}
	for _, turn := range turns {
		for _, leak := range []string{"<code>", "callTool(", "Object containing"} {
			if strings.Contains(turn.Content, leak) {
				t.Errorf("machine traffic leaked into the transcript (%s): %q", leak, turn.Content)
			}
		}
	}
	if turns[1].Content != "上证指数收盘 3865.23 点。" || turns[1].Emotion != "开心" {
		t.Errorf("the real answer should be untouched: %+v", turns[1])
	}
}

// TestVisibleTurnsSalvagesALoneTranscript covers the turns recorded before the
// agent learned to summarise its own runs, where the script and the only copy of
// the answer are the same message. Dropping it would erase the reply, so the
// scripts come out and the sentence stays.
func TestVisibleTurnsSalvagesALoneTranscript(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "搜一下上证指数收盘"},
		{Role: "assistant", Content: "<code>\nreturn \"截至收盘，上证指数报 3865.23 点。\\n\\n情绪: 开心\";\n</code>截至收盘，上证指数报 3865.23 点。\n\n情绪: 开心"},
	})
	if len(turns) != 2 {
		t.Fatalf("the answer must survive when nothing follows it: %+v", turns)
	}
	if turns[1].Content != "截至收盘，上证指数报 3865.23 点。" || turns[1].Emotion != "开心" {
		t.Errorf("salvaged answer is wrong: %+v", turns[1])
	}

	// The same message followed by a real answer: now it is safe to drop.
	turns = visibleTurns([]domain.Message{
		{Role: "user", Content: "搜一下上证指数收盘"},
		{Role: "assistant", Content: "<code>\nreturn callTool('websearch', {});\n</code>Object containing 1 item:"},
		{Role: "assistant", Content: "截至收盘，上证指数报 3865.23 点。"},
	})
	if len(turns) != 2 || turns[1].Content != "截至收盘，上证指数报 3865.23 点。" {
		t.Fatalf("the transcript should give way to the answer that follows: %+v", turns)
	}
}

// A fenced code block is how an assistant legitimately shows code, and it must
// not be mistaken for the tool-calling protocol.
func TestVisibleTurnsKeepsFencedCode(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "assistant", Content: "用这段就行：\n```js\nconst a = 1;\n```\n情绪: 中性"},
	})
	if len(turns) != 1 || !strings.Contains(turns[0].Content, "const a = 1;") {
		t.Fatalf("a fenced code reply must survive: %+v", turns)
	}
}

// TestVisibleTurnsKeepsTranscriptsApartInASharedSession covers the conversation
// every schedule appends to: many runs in one session, and not reliably ordered.
// A later run's answer must not be mistaken for this transcript's answer, or the
// earlier reply is dropped as redundant when nothing replaced it.
func TestVisibleTurnsKeepsTranscriptsApartInASharedSession(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "搜一下上证指数收盘"},
		// Run 1: transcript and answer in one message, nothing following it.
		{Role: "assistant", Content: "<code>\nreturn \"3865.23 点\";\n</code>截至收盘，上证指数报 3865.23 点。"},
		// Run 2: its own transcript, then its own answer.
		{Role: "assistant", Content: "<code>\nreturn callTool('memory_recall', {});\n</code>Object containing 1 item:"},
		{Role: "assistant", Content: "没有找到你的持仓数据。"},
		{Role: "user", Content: "统计我的股票收益"},
	})

	want := []string{"搜一下上证指数收盘", "截至收盘，上证指数报 3865.23 点。", "没有找到你的持仓数据。", "统计我的股票收益"}
	if len(turns) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(turns), len(want), turns)
	}
	for i := range want {
		if turns[i].Content != want[i] {
			t.Errorf("turn %d = %q, want %q", i, turns[i].Content, want[i])
		}
	}
}

// TestVisibleTurnsTellsSalvageFromToolOutput is the pair the position check
// cannot separate: two consecutive script-bearing messages, one whose remainder
// is the reply and one whose remainder is the search results it was built from.
func TestVisibleTurnsTellsSalvageFromToolOutput(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "搜索上证指数收盘"},
		{Role: "assistant", Content: "<code>\nreturn callTool('websearch', {});\n</code>Object containing 2 items: \n1. `tools`: Array containing 1 item: \n  - `0`: \"mcp_websearch_websearch\""},
		{Role: "assistant", Content: "<code>\nreturn \"截至收盘，上证指数报 3865.23 点，上涨 1.15%。\";\n</code>截至收盘，上证指数报 3865.23 点，上涨 1.15%。"},
	})

	want := []string{"搜索上证指数收盘", "截至收盘，上证指数报 3865.23 点，上涨 1.15%。"}
	if len(turns) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(turns), len(want), turns)
	}
	for i := range want {
		if turns[i].Content != want[i] {
			t.Errorf("turn %d = %q, want %q", i, turns[i].Content, want[i])
		}
	}
}

// The runtime asks the model to carry on after each tool round by appending a
// user message. It is not a question anyone asked, and a tool-heavy turn
// collects one per round.
func TestVisibleTurnsHidesTheContinuePrompt(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "把这个简历转换为 markdown"},
		{Role: "user", Content: "Analyze the tool results above. If you have fulfilled the user's request, provide your final answer and call task_complete. If a concrete blocker prevents completion, call task_blocked. Otherwise continue executing directly with the available tools."},
		{Role: "assistant", Content: "已转换好，写到 简历.md 了。"},
	})
	if len(turns) != 2 {
		t.Fatalf("the continue prompt must not read as a question: %+v", turns)
	}
	if turns[0].Content != "把这个简历转换为 markdown" {
		t.Errorf("wrong first turn: %q", turns[0].Content)
	}
}

// TestVisibleTurnsHidesAHalfScript covers a transcript that lost its opening tag
// on the way through the stream: a closing </code> and no opening one, so it
// reads as `const res = callTool(…)` with nothing marking it as a script.
func TestVisibleTurnsHidesAHalfScript(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "搜一下 websearch 有什么工具"},
		{Role: "assistant", Content: "code\nconst res = callTool('search_available_tools', { query: 'websearch' });\nreturn res;\n</code>Constraint text: keep text between tool calls to ≤25 words."},
		{Role: "assistant", Content: "找到一个 websearch 工具。"},
	})
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2: %+v", len(turns), turns)
	}
	if strings.Contains(turns[1].Content, "callTool(") || turns[1].Content != "找到一个 websearch 工具。" {
		t.Errorf("wrong answer turn: %+v", turns[1])
	}

	// Alone, it has nothing worth salvaging: what survives the tags is the
	// runtime's own word limit.
	turns = visibleTurns([]domain.Message{
		{Role: "user", Content: "搜一下"},
		{Role: "assistant", Content: "code\nreturn callTool('x', {});\n</code>Constraint text: keep text between tool calls to ≤25 words."},
	})
	if len(turns) != 1 {
		t.Fatalf("only the question should remain: %+v", turns)
	}
}

// TestVisibleTurnsKeepsTheWorkingAsSteps is the other half of hiding transcripts:
// the scripts are not shown, but what they did is. Without this, reopening a
// conversation loses every trace that the agent worked at all — the live progress
// block is built from stream events that are long gone.
func TestVisibleTurnsKeepsTheWorkingAsSteps(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "把简历转成 markdown"},
		{Role: "assistant", Content: "<code>\nreturn callTool('read_document', {path: 'uploads/cv.pdf'});\n</code>Object containing 1 item:"},
		{Role: "assistant", Content: "<code>\nreturn callTool('write_file', {path: 'cv.md'});\n</code>Object containing 1 item:"},
		{Role: "assistant", Content: "转好了，写到 cv.md。"},
	})

	if len(turns) != 2 {
		t.Fatalf("got %d turns, want the question and the answer: %+v", len(turns), turns)
	}
	answer := turns[1]
	if answer.Content != "转好了，写到 cv.md。" {
		t.Errorf("answer = %q", answer.Content)
	}
	want := []string{"Called read_document", "Called write_file"}
	if len(answer.Steps) != len(want) {
		t.Fatalf("steps = %v, want %v", answer.Steps, want)
	}
	for i := range want {
		if answer.Steps[i] != want[i] {
			t.Errorf("step %d = %q, want %q", i, answer.Steps[i], want[i])
		}
	}
}

func TestScriptSteps(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "a repeated call in a loop is one step",
			in:   "<code>\nfor (const p of ps) callTool('bash', {command: p});\ncallTool('bash', {});\n</code>",
			want: []string{"Called bash"},
		},
		{
			name: "two different tools in one script",
			in:   "<code>\ncallTool('read_document', {});\ncallTool('write_file', {});\n</code>",
			want: []string{"Called read_document", "Called write_file"},
		},
		{
			name: "a script that only computes still happened",
			in:   "<code>\nreturn 1 + 1;\n</code>",
			want: []string{"Ran code"},
		},
		{
			name: "a transcript missing its opening tag still yields steps",
			in:   "code\nreturn callTool('search_available_tools', {});\n</code>",
			want: []string{"Called search_available_tools"},
		},
		{
			name: "plain prose contributes nothing",
			in:   "已经写好了。",
			want: nil,
		},
	}
	for _, tc := range cases {
		got := scriptSteps(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: step %d = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// TestToolCallSteps covers where the working actually lives on the streaming
// path: the assistant message is empty and the calls hang off it. A programmatic
// call is one execute_javascript whose code calls the real tools, so the names
// worth showing are the ones inside the script — otherwise every step of every
// run reads "Called execute_javascript".
func TestToolCallSteps(t *testing.T) {
	js := func(code string) domain.ToolCall {
		return domain.ToolCall{Function: domain.FunctionCall{
			Name:      "execute_javascript",
			Arguments: map[string]interface{}{"code": code},
		}}
	}
	cases := []struct {
		name string
		in   []domain.ToolCall
		want []string
	}{
		{
			name: "the tools inside the script, not the script runner",
			in:   []domain.ToolCall{js("const doc = callTool('read_document', {path: 'uploads/cv.pdf'});\nreturn doc;\n")},
			want: []string{"Called read_document"},
		},
		{
			name: "several scripts in order, a repeat collapsed",
			in: []domain.ToolCall{
				js("callTool('bash', {});"),
				js("callTool('bash', {});"),
				js("callTool('write_file', {});"),
			},
			want: []string{"Called bash", "Called write_file"},
		},
		{
			name: "a script that only computes",
			in:   []domain.ToolCall{js("return 6 * 7;")},
			want: []string{"Ran code"},
		},
		{
			name: "a plain tool call keeps its own name",
			in:   []domain.ToolCall{{Function: domain.FunctionCall{Name: "read_document"}}},
			want: []string{"Called read_document"},
		},
		{
			name: "nothing to say",
			in:   nil,
			want: []string{},
		},
	}
	for _, tc := range cases {
		got := toolCallSteps(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: step %d = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// The whole point, end to end: a stored streaming turn keeps its working.
func TestVisibleTurnsRecoversStepsFromToolCalls(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "把简历提取成 markdown"},
		{Role: "user", Content: "<system-reminder>\nToday's date is 2026-07-30.\n</system-reminder>"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{Function: domain.FunctionCall{
			Name:      "execute_javascript",
			Arguments: map[string]interface{}{"code": "return callTool('read_document', {path: 'uploads/cv.pdf'});"},
		}}}},
		{Role: "tool", Content: "…4896 chars of document…"},
		{Role: "assistant", Content: "简历已提取并保存到 uploads/cv.md。"},
	})

	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2: %+v", len(turns), turns)
	}
	if got := turns[1].Steps; len(got) != 1 || got[0] != "Called read_document" {
		t.Errorf("steps = %v, want [Called read_document]", got)
	}
	// An injected reminder between the question and the work must not be mistaken
	// for a new question and throw the steps away.
	if turns[0].Role != "user" || len(turns[0].Steps) != 0 {
		t.Errorf("the question should carry no steps: %+v", turns[0])
	}
}

// TestVisibleTurnsKeepsRecalledMemoryAsAFoldedBlock covers the block the agent
// injects as a user message when memory turns something up. It was rendered as
// something the user had typed — a wall of "## Memory Index" in a chat bubble.
// It is worth being able to look at, since it is why the answer says what it
// says, so it is kept and marked rather than dropped or disguised.
func TestVisibleTurnsKeepsRecalledMemoryAsAFoldedBlock(t *testing.T) {
	injected := memoryContextHeader + "\n## Memory Index\n\n# MEMORY\n\n[1] 用户是李亮。\n\n" +
		"Use the context above when responding to the next user message."
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "我是谁？"},
		{Role: "user", Content: injected},
		{Role: "assistant", Content: "你是李亮。"},
	})

	if len(turns) != 3 {
		t.Fatalf("got %d turns, want question + context + answer: %+v", len(turns), turns)
	}
	ctx := turns[1]
	if ctx.Kind != TurnKindContext {
		t.Errorf("the memory block must be marked as context, got kind %q", ctx.Kind)
	}
	if strings.Contains(ctx.Content, memoryContextHeader) {
		t.Error("the framing header is the block's label, not its body")
	}
	if strings.Contains(ctx.Content, "Use the context above") {
		t.Error("the model's instruction is not something to read")
	}
	if !strings.Contains(ctx.Content, "## Memory Index") {
		t.Errorf("the markdown body must survive: %q", ctx.Content)
	}
	if turns[2].Content != "你是李亮。" {
		t.Errorf("the answer must be unaffected: %+v", turns[2])
	}

	// A conversation is named after what was asked, never after recalled memory.
	if got := sessionTitle(turns); got != "我是谁？" {
		t.Errorf("title = %q, want the question", got)
	}
}

// An interim message sent via notify_user was a real bubble in the live
// conversation; a reopened one must restore it as such — not demote it to a
// "Called notify_user" progress line.
func TestVisibleTurnsRestoresInterimMessages(t *testing.T) {
	turns := visibleTurns([]domain.Message{
		{Role: "user", Content: "调研三个方案并给结论"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{
			{Function: domain.FunctionCall{
				Name:      "notify_user",
				Arguments: map[string]interface{}{"message": "方案 A 已排除：许可证不兼容。"},
			}},
			{Function: domain.FunctionCall{
				Name:      "web_search",
				Arguments: map[string]interface{}{"query": "方案 B"},
			}},
		}},
		{Role: "tool", Content: "…search results…"},
		{Role: "assistant", Content: "结论：选方案 B。"},
	})

	if len(turns) != 3 {
		t.Fatalf("got %d turns, want question + interim + answer: %+v", len(turns), turns)
	}
	interim := turns[1]
	if interim.Kind != TurnKindInterim || interim.Role != "assistant" {
		t.Errorf("interim turn mismarked: %+v", interim)
	}
	if interim.Content != "方案 A 已排除：许可证不兼容。" {
		t.Errorf("interim content = %q", interim.Content)
	}
	// The sibling tool call still becomes a step on the final answer, and the
	// notify_user call must not double as one.
	final := turns[2]
	if len(final.Steps) != 1 || final.Steps[0] != "Called web_search" {
		t.Errorf("final steps = %v, want [Called web_search]", final.Steps)
	}
}

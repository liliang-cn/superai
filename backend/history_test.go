package backend

import (
	"testing"

	"github.com/liliang-cn/agent-go/v2/pkg/domain"
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

package backend

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// TestSmokeLive drives the real backend.Service (the exact code the desktop app
// uses) against a live LLM, for a chat turn and an autonomous task. It is gated
// behind SUPERAI_SMOKE=1 so normal `go test` skips it. Configure the LLM via
// LLM_BASE / LLM_KEY / LLM_MODEL (or DASHSCOPE_API_KEY).
//
//	SUPERAI_SMOKE=1 LLM_BASE=... LLM_KEY=... LLM_MODEL=gpt-5.5 \
//	  SUPERAI_DESKTOP_HOME=/tmp/superai-smoke go test ./backend/ -run TestSmokeLive -v -count=1 -timeout 15m
func TestSmokeLive(t *testing.T) {
	if os.Getenv("SUPERAI_SMOKE") != "1" {
		t.Skip("set SUPERAI_SMOKE=1 to run the live backend smoke test")
	}
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	svc, err := NewService(s)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()
	t.Logf("memory=%s browser=%v skills=%v", svc.MemoryMode, svc.HasBrowser(), svc.InstalledSkills())

	trace := func(label string) func(ev *agent.Event) {
		return func(ev *agent.Event) {
			switch ev.Type {
			case agent.EventTypeToolCall:
				mark := "▶"
				if ev.DebugType == "ptc_inner" {
					mark = "  ↳"
				}
				t.Logf("[%s] %s %s", label, mark, ev.ToolName)
			case agent.EventTypeBlocked, agent.EventTypeError:
				t.Logf("[%s] %s: %s", label, ev.Type, strings.TrimSpace(ev.Content))
			}
		}
	}

	// 1) plain chat turn
	ctx1, cancel1 := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel1()
	reply, err := svc.Stream(ctx1, "smoke-chat", "用一句话介绍你自己。", nil, trace("chat"))
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	t.Logf("CHAT REPLY: %s", strings.TrimSpace(reply))
	if strings.TrimSpace(reply) == "" {
		t.Error("empty chat reply")
	}

	// 2) autonomous task: write + verify a file in the sandbox workspace
	ctx2, cancel2 := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel2()
	task := "在你的工作区写一个文件 note.md，内容是一句话：SuperAI Desktop 可用。然后读回确认。"
	final, err := svc.Stream(ctx2, "smoke-agent", task, nil, trace("agent"))
	if err != nil {
		t.Fatalf("agent stream: %v", err)
	}
	t.Logf("AGENT FINAL: %s", strings.TrimSpace(final))

	ds, err := svc.Deliverables(ctx2, "smoke-agent")
	if err != nil {
		t.Fatalf("deliverables: %v", err)
	}
	t.Logf("DELIVERABLES (%d):", len(ds))
	found := false
	for _, d := range ds {
		t.Logf("  %s (%d bytes)", d.Path, d.Size)
		if strings.HasSuffix(d.Path, "note.md") {
			found = true
			if body, err := svc.ReadWorkspaceFile(d.Path); err == nil {
				t.Logf("  note.md => %q", strings.TrimSpace(body))
			}
		}
	}
	if !found {
		t.Error("autonomous task did not produce note.md")
	}
	fmt.Println("smoke ok")
}

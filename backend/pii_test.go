package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// resumeExcerpt has the shape of a CV contact block — an e-mail plus a mobile
// written both grouped and contiguous. Placeholder values only: a test fixture
// is committed, printed in CI logs, and read by anyone browsing the repo.
const resumeExcerpt = `# 张三 - 资深全栈工程师

**基本信息**
- **联系电话**：138 0013 8000
- **备用电话**：13800138000
- **电子邮箱**：alice@example.com
`

// piiSecrets are the values that must never leave the process when the PII
// toggle is on. The space-grouped number is tracked separately because
// agent-go's cn_mobile pattern only matches contiguous digits.
var (
	piiSecrets      = []string{"alice@example.com", "13800138000"}
	piiSpacedMobile = "138 0013 8000"
)

// capturedCall is one request the fake provider received.
type capturedCall struct {
	path string
	body string
}

// captureLLM is a minimal OpenAI-compatible endpoint that records every request
// body handed to it, so a test can assert on what actually left the process.
type captureLLM struct {
	mu    sync.Mutex
	calls []capturedCall
}

func (c *captureLLM) snapshot() []capturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedCall, len(c.calls))
	copy(out, c.calls)
	return out
}

func (c *captureLLM) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.calls = append(c.calls, capturedCall{path: r.URL.Path, body: string(raw)})
		c.mu.Unlock()

		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": "收到。\n情绪: 中性"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// runOneTurn drives the real backend.Service — the exact code the desktop app
// runs — against the capture endpoint.
func runOneTurn(t *testing.T, piiOn bool, message string) []capturedCall {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	// Chrome costs ~1s per NewService and no test here browses.
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	cap := &captureLLM{}
	svc, err := NewService(&Settings{
		LLMBaseURL:   cap.start(t),
		LLMKey:       "test-key",
		LLMModel:     "test-model",
		WorkspaceDir: home + "/workspace",
		MaxRounds:    2,
		Headless:     true,
		DisablePTC:   true,
		PIIRedaction: piiOn,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := svc.Stream(ctx, "pii-test", message, nil, func(*agent.Event) {}); err != nil {
		t.Logf("stream returned %v (the fake provider answers a single canned turn)", err)
	}
	return cap.snapshot()
}

// leaks reports which secrets survived into a request body.
func leaks(body string, secrets []string) []string {
	found := []string{}
	for _, s := range secrets {
		if strings.Contains(body, s) {
			found = append(found, s)
		}
	}
	return found
}

// mainTurnCall picks the request that carries the actual conversation — the one
// with the full system prompt and tool definitions, i.e. by far the largest.
func mainTurnCall(t *testing.T, calls []capturedCall) capturedCall {
	t.Helper()
	var biggest capturedCall
	for _, c := range calls {
		if len(c.body) > len(biggest.body) {
			biggest = c
		}
	}
	if biggest.body == "" {
		t.Fatal("the fake provider was never called")
	}
	return biggest
}

// TestPIIRedactionScrubsTheMainTurn covers the path that carries the
// conversation: with the toggle on, the guardrail seam in agent-go's run loop
// must scrub PII out of the messages before the provider sees them.
func TestPIIRedactionScrubsTheMainTurn(t *testing.T) {
	calls := runOneTurn(t, true, "帮我看看这份简历：\n"+resumeExcerpt)
	main := mainTurnCall(t, calls)

	if got := leaks(main.body, piiSecrets); len(got) > 0 {
		t.Errorf("main turn (%d bytes) still carried %v to the provider", len(main.body), got)
	}
}

// TestPIIRedactionOffSendsEverything pins the contrast, so the test above is
// known to be testing the toggle rather than some unrelated rewriting.
func TestPIIRedactionOffSendsEverything(t *testing.T) {
	calls := runOneTurn(t, false, "帮我看看这份简历：\n"+resumeExcerpt)
	main := mainTurnCall(t, calls)

	for _, secret := range piiSecrets {
		if !strings.Contains(main.body, secret) {
			t.Errorf("with redaction OFF, %q should have reached the provider verbatim", secret)
		}
	}
}

// TestPIIRedactionKnownGaps documents the two holes found on 2026-07-29 by
// capturing every provider request during a turn. Both live in agent-go, not
// here, so this test reports instead of failing — it will name fewer gaps as
// they get fixed upstream.
//
//  1. The intent classifier (a ~2.4 KB side call that asks the model to label
//     the user's goal) embeds the raw goal and never passes through the
//     guardrail seam, so it leaks everything the main turn scrubs.
//  2. cn_mobile only matches contiguous digits, so "138 0013 8000" survives
//     even on the main turn.
func TestPIIRedactionKnownGaps(t *testing.T) {
	calls := runOneTurn(t, true, "帮我看看这份简历：\n"+resumeExcerpt)
	main := mainTurnCall(t, calls)
	all := append([]string{}, piiSecrets...)
	all = append(all, piiSpacedMobile)

	sideLeaks := map[string][]string{}
	for _, c := range calls {
		if c.body == main.body {
			continue
		}
		if got := leaks(c.body, all); len(got) > 0 {
			sideLeaks[c.path] = got
		}
	}
	if len(sideLeaks) > 0 {
		t.Logf("GAP 1 — side calls bypass the guardrail seam and leaked: %v", sideLeaks)
	} else {
		t.Log("GAP 1 appears fixed: no side call leaked PII")
	}

	if strings.Contains(main.body, piiSpacedMobile) {
		t.Logf("GAP 2 — space-grouped mobile %q survived redaction (cn_mobile wants contiguous digits)", piiSpacedMobile)
	} else {
		t.Log("GAP 2 appears fixed: the space-grouped mobile was redacted")
	}
}

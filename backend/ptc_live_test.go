package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Does PTC actually work?
//
// PTC (programmatic tool calling) has the model write JavaScript that calls
// tools in a sandbox, so a multi-step job costs one model round instead of one
// per tool. Whether it works is not something unit tests can answer — it
// depends on the model emitting code the runtime accepts — so this drives the
// real service against the real provider and looks at what came back.
//
// Gated behind SUPERAI_PTC_TEST=1 because it spends tokens:
//
//	SUPERAI_PTC_TEST=1 go test ./backend/ -run TestPTCLive -v -count=1 -timeout 10m

// ptcTask needs three tool calls in sequence, which is exactly where PTC is
// supposed to pay off.
const ptcTask = "用 resolve_datetime 工具分别算出「明天」和「下周一」的日期，" +
	"然后把这两个日期写到工作区的 dates.md 里，格式为两行：tomorrow=<日期> 和 next_monday=<日期>。"

// prepareLiveHome copies the real provider credentials into an isolated home so
// the test can reach a model without writing to the user's own data.
func prepareLiveHome(t *testing.T) string {
	t.Helper()
	realAuths := filepath.Join(DataDir(), "cliproxy", "auths")
	entries, err := os.ReadDir(realAuths)
	if err != nil || len(entries) == 0 {
		t.Skipf("no provider credentials in %s — sign in first", realAuths)
	}

	home := t.TempDir()
	dstAuths := filepath.Join(home, "cliproxy", "auths")
	if err := os.MkdirAll(dstAuths, 0o700); err != nil {
		t.Fatalf("mkdir auths: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(realAuths, e.Name()))
		if rerr != nil {
			continue
		}
		if werr := os.WriteFile(filepath.Join(dstAuths, e.Name()), raw, 0o600); werr != nil {
			t.Fatalf("copy credential: %v", werr)
		}
	}
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	// Chrome costs ~1s per NewService and no test here browses.
	t.Setenv("SUPERAI_NO_BROWSER", "1")
	return home
}

// ptcOutcome is what one run tells us.
type ptcOutcome struct {
	innerCalls  int // tool calls made from inside PTC-generated code
	directCalls int // ordinary one-tool-per-round calls
	toolNames   []string
	errors      []string
	final       string
	wroteFile   bool
	fileBody    string
	elapsed     time.Duration
}

func runPTCTask(t *testing.T, ptcOn bool) ptcOutcome {
	t.Helper()
	home := prepareLiveHome(t)

	proxy, err := StartCLIProxy(43541)
	if err != nil {
		t.Fatalf("StartCLIProxy: %v", err)
	}
	defer proxy.Close()

	// The proxy loads credentials through a file watcher, so the catalog is
	// briefly empty right after startup.
	var models []string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m, merr := proxy.Models(context.Background())
		if merr == nil && len(m) > 0 {
			models = m
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(models) == 0 {
		t.Skip("the proxy serves no models — credentials may be expired")
	}
	model := pickModel(models)
	t.Logf("model=%s ptc=%v", model, ptcOn)

	workspace := filepath.Join(home, "workspace")
	svc, err := NewService(&Settings{
		LLMBaseURL:   proxy.BaseURL(),
		LLMKey:       proxy.Key(),
		LLMModel:     model,
		WorkspaceDir: workspace,
		MaxRounds:    12,
		Headless:     true,
		DisablePTC:   !ptcOn,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	out := ptcOutcome{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	final, err := svc.Stream(ctx, "ptc-test", ptcTask, nil, func(ev *agent.Event) {
		if os.Getenv("SUPERAI_PTC_TRACE") == "1" {
			c := strings.ReplaceAll(truncate(strings.TrimSpace(ev.Content), 160), "\n", " ⏎ ")
			t.Logf("  EV %-14s tool=%-20s debug=%-10s %s", ev.Type, ev.ToolName, ev.DebugType, c)
		}
		switch ev.Type {
		case agent.EventTypeToolCall:
			if ev.DebugType == "ptc_inner" {
				out.innerCalls++
			} else {
				out.directCalls++
			}
			out.toolNames = append(out.toolNames, ev.ToolName)
		case agent.EventTypeError, agent.EventTypeBlocked:
			if msg := strings.TrimSpace(ev.Content); msg != "" {
				out.errors = append(out.errors, msg)
			}
		}
	})
	out.elapsed = time.Since(start)
	if err != nil {
		out.errors = append(out.errors, "stream: "+err.Error())
	}
	out.final = strings.TrimSpace(final)

	if body, rerr := os.ReadFile(filepath.Join(workspace, "dates.md")); rerr == nil {
		out.wroteFile = true
		out.fileBody = strings.TrimSpace(string(body))
	}
	return out
}

// pickModel prefers a capable general model, which is what PTC needs.
func pickModel(models []string) string {
	for _, want := range []string{"gpt-5.5", "gpt-5.4", "claude-sonnet-4-6", "gpt-5.2"} {
		for _, m := range models {
			if m == want {
				return m
			}
		}
	}
	return models[0]
}

func report(t *testing.T, label string, o ptcOutcome) {
	t.Helper()
	t.Logf("%s: %.1fs  inner=%d direct=%d tools=%v wrote_file=%v",
		label, o.elapsed.Seconds(), o.innerCalls, o.directCalls, o.toolNames, o.wroteFile)
	if o.fileBody != "" {
		t.Logf("%s: dates.md => %q", label, o.fileBody)
	}
	if len(o.errors) > 0 {
		t.Logf("%s: errors => %v", label, o.errors)
	}
	if o.final != "" {
		t.Logf("%s: final => %s", label, truncate(o.final, 300))
	}
}

// TestPTCLive answers "does PTC work" by running the same task with it on, and
// checking the model's code actually drove the tools.
func TestPTCLive(t *testing.T) {
	if os.Getenv("SUPERAI_PTC_TEST") != "1" {
		t.Skip("set SUPERAI_PTC_TEST=1 to run the live PTC test")
	}
	o := runPTCTask(t, true)
	report(t, "PTC ON", o)

	if o.innerCalls == 0 && o.directCalls == 0 {
		t.Error("no tools were called at all — the task needed three")
	}
	if o.innerCalls == 0 {
		t.Error("PTC was enabled but no tool ran from inside generated code (no ptc_inner events): the model fell back to plain tool calls, or PTC is not engaging")
	}
	if !o.wroteFile {
		t.Error("PTC run did not produce dates.md — the job did not complete")
	}
	for _, e := range o.errors {
		if strings.Contains(strings.ToLower(e), "ptc") {
			t.Errorf("PTC-related error during the run: %s", e)
		}
	}

	// The reply the user sees must be a reply — not the execution report, and
	// not missing what the persona requires of every answer.
	if strings.Contains(o.final, "Code execution completed") ||
		strings.Contains(o.final, "**Status:**") ||
		(strings.HasPrefix(strings.TrimSpace(o.final), "{") && strings.HasSuffix(strings.TrimSpace(o.final), "}")) {
		t.Errorf("final reply is the execution report rather than an answer: %q", truncate(o.final, 400))
	}
	if !strings.Contains(o.final, "情绪:") {
		t.Errorf("final reply lost the 情绪 tag the Avatar needs: %q", truncate(o.final, 400))
	}
}

// TestPTCLiveComparison runs the same task both ways. It asserts only that the
// job gets done, and reports the round counts so the trade-off is visible
// instead of assumed.
func TestPTCLiveComparison(t *testing.T) {
	if os.Getenv("SUPERAI_PTC_TEST") != "1" {
		t.Skip("set SUPERAI_PTC_TEST=1 to run the live PTC comparison")
	}
	on := runPTCTask(t, true)
	report(t, "PTC ON ", on)

	off := runPTCTask(t, false)
	report(t, "PTC OFF", off)

	if !on.wroteFile {
		t.Error("with PTC on, the task did not complete")
	}
	if !off.wroteFile {
		t.Error("with PTC off, the task did not complete — the baseline is broken too")
	}
	t.Logf("SUMMARY: on=%.1fs (inner=%d direct=%d) vs off=%.1fs (direct=%d)",
		on.elapsed.Seconds(), on.innerCalls, on.directCalls, off.elapsed.Seconds(), off.directCalls)
}

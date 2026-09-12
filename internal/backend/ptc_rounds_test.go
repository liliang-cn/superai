package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// What PTC actually trades.
//
// The claim is that PTC costs fewer model round-trips: instead of one request
// per tool, the model writes a script that calls several. The earlier test
// counted tool-call *events*, which does not show that at all — the tool count
// is the same either way. What matters is how many times the provider was
// asked, and how many tokens that took.
//
// So this sits a counting reverse proxy between the agent and the real provider
// and measures both.
//
//	SUPERAI_PTC_TEST=1 go test ./backend/ -run TestPTCRounds -v -count=1 -timeout 15m

// countingProxy forwards to the real endpoint while recording each call.
type countingProxy struct {
	target string

	mu          sync.Mutex
	calls       int
	promptTok   int
	completeTok int
	sizes       []int
}

var usageRe = regexp.MustCompile(`"(prompt_tokens|completion_tokens)"\s*:\s*(\d+)`)

func (c *countingProxy) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		if strings.Contains(r.URL.Path, "/chat/completions") || strings.Contains(r.URL.Path, "/responses") {
			c.mu.Lock()
			c.calls++
			c.sizes = append(c.sizes, len(body))
			c.mu.Unlock()
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, c.target+r.URL.Path, bytes.NewReader(body))
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		for k, vs := range r.Header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		// Usage rides in the JSON body (or the final SSE chunk); a regex handles
		// both without needing to know which shape came back.
		for _, m := range usageRe.FindAllStringSubmatch(string(respBody), -1) {
			n, _ := strconv.Atoi(m[2])
			c.mu.Lock()
			if m[1] == "prompt_tokens" {
				c.promptTok += n
			} else {
				c.completeTok += n
			}
			c.mu.Unlock()
		}

		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func (c *countingProxy) snapshot() (calls, promptTok, completeTok int, sizes []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.promptTok, c.completeTok, append([]int{}, c.sizes...)
}

type roundStats struct {
	providerCalls int
	promptTok     int
	completeTok   int
	toolCalls     int
	innerCalls    int
	elapsed       time.Duration
	wroteFile     bool
	final         string
}

func measureRounds(t *testing.T, ptcOn bool) roundStats {
	t.Helper()
	home := prepareLiveHome(t)

	upstream, err := StartCLIProxy(43547)
	if err != nil {
		t.Fatalf("StartCLIProxy: %v", err)
	}
	defer upstream.Close()

	var models []string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if m, merr := upstream.Models(context.Background()); merr == nil && len(m) > 0 {
			models = m
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(models) == 0 {
		t.Skip("the proxy serves no models — credentials may be expired")
	}

	counter := &countingProxy{target: strings.TrimSuffix(upstream.BaseURL(), "/v1")}
	base := counter.start(t)

	workspace := filepath.Join(home, "workspace")
	svc, err := NewService(&Settings{
		LLMBaseURL:   base,
		LLMKey:       upstream.Key(),
		LLMModel:     pickModel(models),
		WorkspaceDir: workspace,
		MaxRounds:    12,
		Headless:     true,
		DisablePTC:   !ptcOn,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	st := roundStats{}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	start := time.Now()
	final, err := svc.Stream(ctx, "ptc-rounds", ptcTask, nil, func(ev *agent.Event) {
		if ev.Type == agent.EventTypeToolCall {
			st.toolCalls++
			if ev.DebugType == "ptc_inner" {
				st.innerCalls++
			}
		}
	})
	st.elapsed = time.Since(start)
	if err != nil {
		t.Logf("stream error (ptc=%v): %v", ptcOn, err)
	}
	st.final = strings.TrimSpace(final)

	if _, rerr := os.ReadFile(filepath.Join(workspace, "dates.md")); rerr == nil {
		st.wroteFile = true
	}
	st.providerCalls, st.promptTok, st.completeTok, _ = counter.snapshot()
	return st
}

// TestPTCRoundsTradeoff measures the thing PTC is actually supposed to improve.
func TestPTCRoundsTradeoff(t *testing.T) {
	if os.Getenv("SUPERAI_PTC_TEST") != "1" {
		t.Skip("set SUPERAI_PTC_TEST=1 to measure PTC round-trips")
	}

	on := measureRounds(t, true)
	off := measureRounds(t, false)

	show := func(label string, s roundStats) {
		t.Logf("%s: provider_calls=%d  prompt_tok=%d  completion_tok=%d  tool_calls=%d (inner=%d)  %.1fs  file=%v",
			label, s.providerCalls, s.promptTok, s.completeTok, s.toolCalls, s.innerCalls, s.elapsed.Seconds(), s.wroteFile)
	}
	show("PTC ON ", on)
	show("PTC OFF", off)

	if on.providerCalls == 0 || off.providerCalls == 0 {
		t.Fatal("the counting proxy saw no provider calls — measurement is broken")
	}

	// Report the comparison rather than asserting a winner: which way it goes is
	// the finding, and it depends on the model.
	t.Logf("ROUND-TRIPS: PTC %d vs direct %d", on.providerCalls, off.providerCalls)
	t.Logf("COMPLETION TOKENS: PTC %d vs direct %d", on.completeTok, off.completeTok)
	if on.promptTok > 0 && off.promptTok > 0 {
		t.Logf("PROMPT TOKENS: PTC %d vs direct %d", on.promptTok, off.promptTok)
	}
	if on.providerCalls >= off.providerCalls {
		t.Logf("NOTE: PTC did not reduce round-trips here — its whole premise is that it should")
	}

	if !on.wroteFile || !off.wroteFile {
		t.Errorf("both runs must finish the task (on=%v off=%v)", on.wroteFile, off.wroteFile)
	}

	// Keep the raw numbers in one machine-readable line for the record.
	blob, _ := json.Marshal(map[string]any{
		"ptc_on":  map[string]any{"calls": on.providerCalls, "prompt": on.promptTok, "completion": on.completeTok, "seconds": on.elapsed.Seconds()},
		"ptc_off": map[string]any{"calls": off.providerCalls, "prompt": off.promptTok, "completion": off.completeTok, "seconds": off.elapsed.Seconds()},
	})
	t.Logf("DATA %s", blob)
}

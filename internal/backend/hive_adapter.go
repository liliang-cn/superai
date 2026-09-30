package backend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// Workers that are not SuperAI.
//
// The hive asks two things of a worker: that it announce itself (hive.go) and
// that it take an order and report back the way a SuperAI does — one POST to
// start a turn, an event stream for its progress and its end. Nothing in that
// needs to be SuperAI. This is a small server that says those things on behalf
// of any agent, so a Claude Code, a Codex, a script or a model behind an API
// can be a worker in the hive and be commanded, scheduled and drawn like any
// other.
//
// The agent behind it is an Engine. It gets the order as text and gives back
// text, and may report what it is doing on the way (a tool called, some words
// written) so the panel has something to draw; an engine that can say nothing
// but the answer still works, it is just a quieter beam.

// AdapterEvent is one thing an engine reports while it works. Types are the
// ones the queen already understands: "thinking", "tool_call", "tool_result",
// "partial".
type AdapterEvent struct {
	Type    string
	Tool    string
	Content string
	Result  any
}

// Engine is an agent behind an adapter.
type Engine interface {
	// Describe says what it is, in a few words, for the roster.
	Describe() string
	// Run does one order and returns the answer. It must stop when ctx ends.
	Run(ctx context.Context, prompt string, emit func(AdapterEvent)) (string, error)
}

// Adapter serves the worker side of the hive for an Engine.
type Adapter struct {
	// Token is the bearer the queen must present; it is also what the worker
	// hands the queen when it joins, which is how the queen knows to use it.
	Token  string
	Engine Engine
	// Concurrency is how many orders run at once; the rest wait their turn.
	// Zero means one, because an agent that edits files or drives a browser does
	// not usually take being run twice at the same moment in the same place.
	Concurrency int

	once sync.Once
	sem  chan struct{}

	mu   sync.Mutex
	subs map[chan []byte]struct{}
	runs map[string]context.CancelFunc
}

func (a *Adapter) init() {
	a.once.Do(func() {
		n := a.Concurrency
		if n <= 0 {
			n = 1
		}
		a.sem = make(chan struct{}, n)
		a.subs = map[chan []byte]struct{}{}
		a.runs = map[string]context.CancelFunc{}
	})
}

// Handler is the HTTP surface: exactly what askWorker calls.
func (a *Adapter) Handler() http.Handler {
	a.init()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", a.auth(a.serveEvents))
	mux.HandleFunc("/api/rpc/SendChat", a.auth(a.sendChat))
	mux.HandleFunc("/api/rpc/CancelChat", a.auth(a.cancelChat))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

func (a *Adapter) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.Token == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(a.Token)) != 1 {
			http.Error(w, `{"error":"sign in"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (a *Adapter) emit(name string, payload map[string]any) {
	b, err := json.Marshal(map[string]any{"name": name, "payload": payload})
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for ch := range a.subs {
		select {
		case ch <- b:
		default: // a reader that cannot keep up loses progress, never the run
		}
	}
}

func (a *Adapter) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ch := make(chan []byte, 512)
	a.mu.Lock()
	a.subs[ch] = struct{}{}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.subs, ch); a.mu.Unlock() }()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

func (a *Adapter) sendChat(w http.ResponseWriter, r *http.Request) {
	var args []any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&args); err != nil || len(args) < 2 {
		http.Error(w, `{"error":"want [session, message, images]"}`, http.StatusBadRequest)
		return
	}
	prompt, _ := args[1].(string)
	if strings.TrimSpace(prompt) == "" {
		http.Error(w, `{"error":"nothing was asked"}`, http.StatusBadRequest)
		return
	}
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.runs[id] = cancel
	a.mu.Unlock()
	go a.run(ctx, cancel, id, prompt)
	json.NewEncoder(w).Encode(id)
}

func (a *Adapter) cancelChat(w http.ResponseWriter, r *http.Request) {
	var args []any
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&args)
	id := ""
	if len(args) > 0 {
		id, _ = args[0].(string)
	}
	a.mu.Lock()
	cancel := a.runs[id]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	json.NewEncoder(w).Encode("ok")
}

func (a *Adapter) run(ctx context.Context, cancel context.CancelFunc, id, prompt string) {
	defer cancel()
	defer func() { a.mu.Lock(); delete(a.runs, id); a.mu.Unlock() }()

	ev := func(e AdapterEvent) {
		a.emit("chat:event", map[string]any{
			"requestId": id, "type": e.Type, "tool": e.Tool, "content": e.Content, "result": e.Result,
		})
	}

	// Waiting for a turn is thinking, as far as the queen can tell: the order
	// is accepted and nothing has come back.
	ev(AdapterEvent{Type: "thinking"})
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		a.emit("chat:cancelled", map[string]any{"requestId": id, "final": ""})
		return
	}

	final, err := a.Engine.Run(ctx, prompt, ev)
	switch {
	case ctx.Err() != nil:
		a.emit("chat:cancelled", map[string]any{"requestId": id, "final": final})
	case err != nil:
		a.emit("chat:error", map[string]any{"requestId": id, "error": err.Error()})
	case strings.TrimSpace(final) == "":
		a.emit("chat:error", map[string]any{"requestId": id, "error": "the agent finished without saying anything"})
	default:
		a.emit("chat:done", map[string]any{"requestId": id, "final": final, "emotion": ""})
	}
}

// ---- engines ----

// ExecEngine runs a command per order — any agent with a one-shot mode. The
// order goes in as one argument, never spliced into a shell string.
//
//	claude -p {prompt}
//	codex exec {prompt}
//	hermes --oneshot {prompt}
type ExecEngine struct {
	// Argv is the command, with exactly one element that is exactly "{prompt}".
	Argv []string
	Dir  string
	Env  []string
	// Stdin sends the order on the command's standard input instead of as an
	// argument, and then no {prompt} is wanted. It is for commands that run the
	// agent somewhere else — over ssh, say — where an argument would be joined
	// into a string and parsed again by a remote shell, and an order containing a
	// quote would become code. Standard input is never parsed by anything.
	Stdin bool
	// Mode is "text" (what it prints is the answer) or "claude" (it prints
	// Claude Code's stream-json, from which tool calls and the answer are read,
	// so the panel can show them).
	Mode string
}

func (e *ExecEngine) Describe() string {
	if len(e.Argv) == 0 {
		return "exec"
	}
	name := e.Argv[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return "cli · " + name
}

// Validate says why an engine cannot run, before it is asked to.
func (e *ExecEngine) Validate() error {
	if len(e.Argv) == 0 {
		return errors.New("no command given")
	}
	n := 0
	for _, a := range e.Argv {
		if a == "{prompt}" {
			n++
		}
	}
	if e.Stdin {
		if n != 0 {
			return errors.New("with stdin the order is not an argument: remove {prompt}")
		}
	} else if n != 1 {
		return fmt.Errorf("the command needs exactly one {prompt} argument, has %d", n)
	}
	switch e.Mode {
	case "", "text", "claude":
	default:
		return fmt.Errorf("unknown output mode %q: text or claude", e.Mode)
	}
	return nil
}

func (e *ExecEngine) Run(ctx context.Context, prompt string, emit func(AdapterEvent)) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	args := make([]string, len(e.Argv)-1)
	for i, a := range e.Argv[1:] {
		if a == "{prompt}" {
			a = prompt
		}
		args[i] = a
	}
	// The command itself can never be the placeholder, so a first element of
	// {prompt} would have been caught above only if it counted; guard anyway.
	if e.Argv[0] == "{prompt}" {
		return "", errors.New("the command cannot be the prompt")
	}
	cmd := exec.CommandContext(ctx, e.Argv[0], args...)
	if e.Stdin {
		cmd.Stdin = strings.NewReader(prompt)
	}
	cmd.Dir = e.Dir
	cmd.Env = append(os.Environ(), e.Env...)
	// Its own process group, so cancelling takes the agent's children with it: an
	// agent that shelled out and was then stopped must not leave the shell
	// running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start %s: %w", e.Argv[0], err)
	}

	var all strings.Builder
	final, sawResult, isErr := "", false, false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if e.Mode == "claude" {
			evs, res, fin, bad := parseClaudeLine(line)
			for _, ev := range evs {
				emit(ev)
			}
			if fin {
				final, sawResult, isErr = res, true, bad
			}
			continue
		}
		all.WriteString(line + "\n")
		emit(AdapterEvent{Type: "partial", Content: line + "\n"})
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return strings.TrimSpace(final + all.String()), ctx.Err()
	}
	if e.Mode == "claude" {
		if sawResult {
			if isErr {
				return "", fmt.Errorf("the agent reported an error: %s", strings.TrimSpace(final))
			}
			return strings.TrimSpace(final), nil
		}
	} else if waitErr == nil {
		return strings.TrimSpace(all.String()), nil
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" && waitErr != nil {
		msg = waitErr.Error()
	}
	if msg == "" {
		msg = "the command ended without an answer"
	}
	return "", fmt.Errorf("%s: %s", e.Argv[0], CondenseAgentFailure(msg))
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		q := p
		if len(q) > l.n {
			q = q[:l.n]
		}
		l.n -= len(q)
		l.w.Write(q)
	}
	return len(p), nil
}

// parseClaudeLine reads one line of Claude Code's stream-json. It returns the
// events the line carries, and — for the closing "result" line — the final
// answer and whether it was an error. Lines it does not understand (hooks,
// rate-limit notices, whatever a newer version adds) are ignored, because the
// answer is what matters and it comes in one well-known place.
func parseClaudeLine(line string) (events []AdapterEvent, result string, final, isErr bool) {
	var d struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &d) != nil {
		return nil, "", false, false
	}
	switch d.Type {
	case "result":
		return nil, d.Result, true, d.IsError
	case "assistant", "user":
		var blocks []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Text    string          `json:"text"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(d.Message.Content, &blocks) != nil {
			return nil, "", false, false
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				events = append(events, AdapterEvent{Type: "tool_call", Tool: b.Name})
			case "tool_result":
				events = append(events, AdapterEvent{Type: "tool_result", Result: string(b.Content)})
			case "text":
				if d.Type == "assistant" && b.Text != "" {
					events = append(events, AdapterEvent{Type: "partial", Content: b.Text})
				}
			}
		}
	}
	return events, "", false, false
}

// OpenAIEngine asks a model behind any OpenAI-compatible endpoint, streaming.
// It is a model and not an agent — there are no tool calls to report — but it
// is what most gateways, and most other people's agents, speak.
type OpenAIEngine struct {
	BaseURL string
	Key     string
	Model   string
	System  string
	Client  *http.Client
}

func (e *OpenAIEngine) Describe() string { return "openai · " + e.Model }

func (e *OpenAIEngine) Run(ctx context.Context, prompt string, emit func(AdapterEvent)) (string, error) {
	msgs := []map[string]string{}
	if strings.TrimSpace(e.System) != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": e.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": prompt})
	body, _ := json.Marshal(map[string]any{"model": e.Model, "messages": msgs, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	cl := e.Client
	if cl == nil {
		cl = &http.Client{}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("model endpoint: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var all strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		if c := chunk.Choices[0].Delta.Content; c != "" {
			all.WriteString(c)
			emit(AdapterEvent{Type: "partial", Content: c})
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return all.String(), err
	}
	return strings.TrimSpace(all.String()), nil
}

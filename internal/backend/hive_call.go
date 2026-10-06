package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"github.com/google/uuid"
)

// Workers: SuperAI instances commanding one another.
//
// A worker is a SuperAI serving over HTTP, and the one that holds a list of them
// is the queen. The command runs one way only, and that is enforced by
// the data rather than by a rule: a RemoteAgent with a URL is a worker, and only
// an instance whose settings list workers can command any. A worker's settings
// list none, so it has nothing to call and no tool to call it with.
//
// A worker is reached over the same HTTP surface a browser uses — SendChat to
// start a turn, the SSE stream for its end — rather than a second protocol
// built for the purpose. That means a worker needs no code to be commanded, and
// that whatever the login gate lets a bearer token do is exactly what the
// queen can do to it.
//
// Each command is its own session on the worker. They share the brain (the
// CortexDB namespace), so a worker knows what the others learned; what they do
// not share is a transcript, because two commands to one worker at once
// interleaved in one conversation would be one confused turn.

// workerTarget is what askWorker needs from a worker entry.
type workerTarget struct {
	name  string
	url   string
	token string
	// taskID is the order's UUID; it becomes the worker's session name, so the
	// worker's own record of the order carries the same id.
	taskID string
	// pulse, when set, is told about every event the worker streams while it
	// works, thinned into pulses worth drawing (see Pulser).
	pulse func(kind, tool string, bytes int)
	// timeout of zero means none. A worker's turn ends when its model stops, not
	// when a clock does; only an explicit setting cuts one off.
	timeout time.Duration
}

var workerHTTP = &http.Client{
	// No overall Timeout: the SSE body legitimately stays open as long as the
	// turn runs. Connect and header waits are bounded instead.
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

type workerEvent struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// askWorker sends one command to a worker and waits for its answer.
func askWorker(ctx context.Context, t workerTarget, prompt string, progress func(phase, tool string)) RemoteResult {
	res := RemoteResult{Agent: t.name, Host: t.url}
	fail := func(format string, a ...any) RemoteResult {
		res.Failed, res.Reason = true, fmt.Sprintf(format, a...)
		return res
	}

	base, err := url.Parse(strings.TrimRight(t.url, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fail("worker %q has no usable url: %q", t.name, t.url)
	}
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	auth := func(r *http.Request) {
		if t.token != "" {
			r.Header.Set("Authorization", "Bearer "+t.token)
		}
	}

	// The stream is opened before the command is sent. The other order loses
	// the race a short turn wins: the worker answers before this side is
	// listening, and the terminal event it already sent is gone.
	sseCtx, stopSSE := context.WithCancel(ctx)
	defer stopSSE()
	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, base.String()+"/api/events", nil)
	auth(req)
	resp, err := workerHTTP.Do(req)
	if err != nil {
		return fail("cannot reach the worker: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("the worker refused the event stream: %s", resp.Status)
	}

	events := make(chan workerEvent, 64)
	lastPhase := map[string]string{}
	// The id of the turn this call started, known only once the worker has
	// answered the command. Pulses are for that turn and no other: the stream
	// carries every conversation the worker has.
	var turn atomic.Value
	pulser := NewPulser(t.pulse)
	streamErr := make(chan error, 1)
	go func() {
		defer close(events)
		sc := newEventLines(resp.Body, 8<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev workerEvent
			if json.Unmarshal([]byte(line[6:]), &ev) != nil {
				continue
			}
			switch ev.Name {
			case "chat:done", "chat:error", "chat:cancelled":
				// The end of a turn must arrive, so this waits for room.
				select {
				case events <- ev:
				case <-sseCtx.Done():
					return
				}
			case "chat:event":
				// Progress is only worth showing, never worth blocking for: a
				// token stream would otherwise fill the channel and delay the
				// end of the turn behind it. A tool call always passes; the
				// other phases pass only when they change, so a stream of
				// partial text is one "writing" and not a thousand.
				rid, _ := ev.Payload["requestId"].(string)
				typ, _ := ev.Payload["type"].(string)
				if mine, _ := turn.Load().(string); mine != "" && rid == mine {
					tool, _ := ev.Payload["tool"].(string)
					content, _ := ev.Payload["content"].(string)
					resultLen := 0
					if r := ev.Payload["result"]; r != nil {
						if b, err := json.Marshal(r); err == nil {
							resultLen = len(b)
						}
					}
					pulser.Event(typ, tool, len(content), resultLen)
				}
				phase := ""
				switch typ {
				case "tool_call":
					phase = PhaseTool
				case "thinking":
					phase = PhaseThinking
				case "partial":
					phase = PhaseWriting
				}
				if phase == "" || rid == "" {
					continue
				}
				if phase != PhaseTool && lastPhase[rid] == phase {
					continue
				}
				lastPhase[rid] = phase
				select {
				case events <- ev:
				default:
				}
			}
		}
		streamErr <- sc.Err()
	}()

	if t.taskID == "" {
		t.taskID = uuid.NewString()
	}
	session := HiveSessionPrefix + t.taskID
	body, _ := json.Marshal([]any{session, prompt, []string{}})
	post, _ := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+"/api/rpc/SendChat", bytes.NewReader(body))
	post.Header.Set("Content-Type", "application/json")
	auth(post)
	pr, err := workerHTTP.Do(post)
	if err != nil {
		return fail("cannot send the command: %v", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(pr.Body, 1<<20))
	pr.Body.Close()
	if pr.StatusCode != http.StatusOK {
		return fail("the worker refused the command: %s %s", pr.Status, strings.TrimSpace(string(raw)))
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || id == "" {
		return fail("the worker did not start a turn: %s", strings.TrimSpace(string(raw)))
	}
	turn.Store(id)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				err := errors.New("stream closed")
				select {
				case err = <-streamErr:
				default:
				}
				return fail("lost the worker before it answered: %v", err)
			}
			if rid, _ := ev.Payload["requestId"].(string); rid != id {
				continue
			}
			if ev.Name == "chat:event" {
				if progress != nil {
					tool, _ := ev.Payload["tool"].(string)
					typ, _ := ev.Payload["type"].(string)
					switch typ {
					case "tool_call":
						progress(PhaseTool, tool)
					case "thinking":
						progress(PhaseThinking, "")
					case "partial":
						progress(PhaseWriting, "")
					}
				}
				continue
			}
			text, _ := ev.Payload["final"].(string)
			res.Text = strings.TrimSpace(text)
			readTurnUsage(ev.Payload, &res)
			switch ev.Name {
			case "chat:error":
				msg, _ := ev.Payload["error"].(string)
				return fail("%s", msg)
			case "chat:cancelled":
				return fail("the worker stopped before finishing")
			}
			if res.Text == "" {
				return fail("the worker finished without saying anything")
			}
			return res
		case <-ctx.Done():
			// Stopping the commander must stop the work, or a cancelled
			// command keeps spending the worker's budget with nobody reading.
			cancelWorker(base.String(), t.token, id)
			return fail("stopped: %v", ctx.Err())
		}
	}
}

// cancelWorker is best effort, on its own short context: the caller's is already
// cancelled, which is why it is being called.
func cancelWorker(base, token, requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, _ := json.Marshal([]any{requestID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/rpc/CancelChat", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if resp, err := workerHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

// readTurnUsage takes what a worker reported about its turn's tokens out of a
// terminal event. A worker built before it reported any leaves Usage nil,
// which is exactly how "it did not say" is spelled.
func readTurnUsage(payload map[string]any, res *RemoteResult) {
	if u, ok := payload["usage"].(map[string]any); ok {
		n := func(k string) int {
			f, _ := u[k].(float64)
			return int(f)
		}
		res.Usage = &domain.TokenUsage{
			PromptTokens:       n("prompt_tokens"),
			CompletionTokens:   n("completion_tokens"),
			CachedPromptTokens: n("cached_prompt_tokens"),
			CacheWriteTokens:   n("cache_write_tokens"),
		}
	}
}

// eventLines reads an event stream a line at a time, and passes over a line
// longer than max instead of stopping at it. The stream carries every
// conversation on the worker, so the line too long is usually someone else's
// tool result; a bufio.Scanner ends the stream there, and the queen was told
// the worker was lost while it was still working on her order.
type eventLines struct {
	r    *bufio.Reader
	max  int
	line string
	err  error
}

func newEventLines(r io.Reader, max int) *eventLines {
	return &eventLines{r: bufio.NewReaderSize(r, 64<<10), max: max}
}

func (e *eventLines) Scan() bool {
	for {
		var buf []byte
		over := false
		for {
			frag, isPrefix, err := e.r.ReadLine()
			if err != nil {
				if err != io.EOF {
					e.err = err
				}
				return false
			}
			if !over {
				if len(buf)+len(frag) > e.max {
					over, buf = true, nil
				} else {
					buf = append(buf, frag...)
				}
			}
			if !isPrefix {
				break
			}
		}
		if over {
			continue
		}
		e.line = string(buf)
		return true
	}
}

func (e *eventLines) Text() string { return e.line }
func (e *eventLines) Err() error   { return e.err }

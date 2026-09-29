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
	"time"

	"github.com/google/uuid"
)

// Rings: SuperAI instances commanding one another.
//
// A ring is a SuperAI serving over HTTP, and the one that holds a list of them
// is the supreme ring. The command runs one way only, and that is enforced by
// the data rather than by a rule: a RemoteAgent with a URL is a ring, and only
// an instance whose settings list rings can command any. A worker's settings
// list none, so it has nothing to call and no tool to call it with.
//
// A ring is reached over the same HTTP surface a browser uses — SendChat to
// start a turn, the SSE stream for its end — rather than a second protocol
// built for the purpose. That means a ring needs no code to be commanded, and
// that whatever the login gate lets a bearer token do is exactly what the
// supreme ring can do to it.
//
// Each command is its own session on the ring. They share the brain (the
// CortexDB namespace), so a ring knows what the others learned; what they do
// not share is a transcript, because two commands to one ring at once
// interleaved in one conversation would be one confused turn.

// ringTarget is what askRing needs from a ring entry.
type ringTarget struct {
	name  string
	url   string
	token string
	// timeout of zero means none. A ring's turn ends when its model stops, not
	// when a clock does; only an explicit setting cuts one off.
	timeout time.Duration
}

var ringHTTP = &http.Client{
	// No overall Timeout: the SSE body legitimately stays open as long as the
	// turn runs. Connect and header waits are bounded instead.
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

type ringEvent struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// askRing sends one command to a ring and waits for its answer.
func askRing(ctx context.Context, t ringTarget, prompt string) RemoteResult {
	res := RemoteResult{Agent: t.name, Host: t.url}
	fail := func(format string, a ...any) RemoteResult {
		res.Failed, res.Reason = true, fmt.Sprintf(format, a...)
		return res
	}

	base, err := url.Parse(strings.TrimRight(t.url, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fail("ring %q has no usable url: %q", t.name, t.url)
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
	// the race a short turn wins: the ring answers before this side is
	// listening, and the terminal event it already sent is gone.
	sseCtx, stopSSE := context.WithCancel(ctx)
	defer stopSSE()
	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, base.String()+"/api/events", nil)
	auth(req)
	resp, err := ringHTTP.Do(req)
	if err != nil {
		return fail("cannot reach the ring: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("the ring refused the event stream: %s", resp.Status)
	}

	events := make(chan ringEvent, 64)
	streamErr := make(chan error, 1)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev ringEvent
			if json.Unmarshal([]byte(line[6:]), &ev) != nil {
				continue
			}
			// Only ends of turns matter here; the ring's other traffic is
			// somebody else's conversation.
			switch ev.Name {
			case "chat:done", "chat:error", "chat:cancelled":
				select {
				case events <- ev:
				case <-sseCtx.Done():
					return
				}
			}
		}
		streamErr <- sc.Err()
	}()

	session := "ring:" + uuid.NewString()
	body, _ := json.Marshal([]any{session, prompt, []string{}})
	post, _ := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+"/api/rpc/SendChat", bytes.NewReader(body))
	post.Header.Set("Content-Type", "application/json")
	auth(post)
	pr, err := ringHTTP.Do(post)
	if err != nil {
		return fail("cannot send the command: %v", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(pr.Body, 1<<20))
	pr.Body.Close()
	if pr.StatusCode != http.StatusOK {
		return fail("the ring refused the command: %s %s", pr.Status, strings.TrimSpace(string(raw)))
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || id == "" {
		return fail("the ring did not start a turn: %s", strings.TrimSpace(string(raw)))
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				err := errors.New("stream closed")
				select {
				case err = <-streamErr:
				default:
				}
				return fail("lost the ring before it answered: %v", err)
			}
			if rid, _ := ev.Payload["requestId"].(string); rid != id {
				continue
			}
			text, _ := ev.Payload["final"].(string)
			res.Text = strings.TrimSpace(text)
			switch ev.Name {
			case "chat:error":
				msg, _ := ev.Payload["error"].(string)
				return fail("%s", msg)
			case "chat:cancelled":
				return fail("the ring stopped before finishing")
			}
			if res.Text == "" {
				return fail("the ring finished without saying anything")
			}
			return res
		case <-ctx.Done():
			// Stopping the commander must stop the work, or a cancelled
			// command keeps spending the ring's budget with nobody reading.
			cancelRing(base.String(), t.token, id)
			return fail("stopped: %v", ctx.Err())
		}
	}
}

// cancelRing is best effort, on its own short context: the caller's is already
// cancelled, which is why it is being called.
func cancelRing(base, token, requestID string) {
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
	if resp, err := ringHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

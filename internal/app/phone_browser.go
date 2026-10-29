package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The phone's own browser.
//
// The iPhone app keeps a WKWebView it can drive, and lends it to this core as
// tools while the app is open. Nothing new is needed on the wire: the phone
// already listens on the event stream and already calls RPC, so a call goes
// out as a phone:browser_call event and its answer comes back as
// PhoneBrowserResult. That works the same over the LAN and through the relay.
//
// The phone is not always there: iOS suspends the app in the background, so
// it says it is online every 30 seconds while it is open, and a call made
// with no phone heard from lately fails at once with a reason the model can
// pass on, rather than waiting out a timeout.

const (
	phoneBrowserEvent = "phone:browser_call"
	phoneBrowserFresh = 75 * time.Second // a phone not heard from in this long is gone
)

// phoneBrowserTimeout is how long one call may take, end to end; a variable
// so a test need not wait it out.
var phoneBrowserTimeout = 45 * time.Second

// phoneBrowserOps are the tools the phone's browser offers, as the model sees
// them, with the parameters each takes.
var phoneBrowserOps = []struct {
	op, about string
	params    map[string]any
}{
	{"open", "Open a URL in the browser on the person's iPhone (their phone's logins). Returns the page title and URL.",
		obj(map[string]any{"url": strParam("The full URL to open.")}, "url")},
	{"snapshot", "Outline of the phone browser's current page: text, links and controls, each control with a ref for click and fill.",
		obj(map[string]any{})},
	{"click", "Click the element with this ref (from snapshot) in the phone's browser.",
		obj(map[string]any{"ref": strParam("The element's ref from snapshot, like e12.")}, "ref")},
	{"fill", "Type text into the field with this ref (from snapshot) in the phone's browser, replacing what is there.",
		obj(map[string]any{"ref": strParam("The field's ref from snapshot."), "text": strParam("The text to put in it.")}, "ref", "text")},
	{"scroll", "Scroll the phone browser's page.",
		obj(map[string]any{"direction": map[string]any{"type": "string", "enum": []string{"up", "down"}}, "amount": map[string]any{"type": "integer", "description": "Pixels, default one screen."}}, "direction")},
	{"get_text", "The visible text of the phone browser's current page.", obj(map[string]any{})},
	{"get_url", "The URL of the phone browser's current page.", obj(map[string]any{})},
	{"back", "Go back one page in the phone's browser.", obj(map[string]any{})},
}

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func strParam(about string) map[string]any {
	return map[string]any{"type": "string", "description": about}
}

// phoneBrowserTool is the name an op reaches the model as.
func phoneBrowserTool(op string) string { return "phone_browser_" + op }

type phoneBrowserAnswer struct {
	ok     bool
	result string
	err    string
}

// phoneBrowser routes the tools to whichever phone was heard from last.
type phoneBrowser struct {
	mu      sync.Mutex
	seen    map[string]time.Time // device → last PhoneBrowserOnline
	pending map[string]chan phoneBrowserAnswer
	on      *backend.Service // the service the tools are mounted on
	now     func() time.Time
}

func newPhoneBrowser() *phoneBrowser {
	return &phoneBrowser{seen: map[string]time.Time{}, pending: map[string]chan phoneBrowserAnswer{}, now: time.Now}
}

// device is the phone heard from most recently, or "" when none is fresh.
func (p *phoneBrowser) device() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	type seen struct {
		id string
		at time.Time
	}
	var all []seen
	for id, at := range p.seen {
		if p.now().Sub(at) <= phoneBrowserFresh {
			all = append(all, seen{id, at})
		}
	}
	if len(all) == 0 {
		return ""
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	return all[0].id
}

// call sends one op to the phone and waits for its answer.
func (p *phoneBrowser) call(ctx context.Context, emit func(string, map[string]any), op string, args map[string]any) (any, error) {
	dev := p.device()
	if dev == "" {
		return nil, fmt.Errorf("the phone's browser is not open: SuperLeo has to be open on the iPhone")
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	ch := make(chan phoneBrowserAnswer, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if args == nil {
		args = map[string]any{}
	}
	emit(phoneBrowserEvent, map[string]any{"id": id, "device": dev, "op": op, "args": args})
	t := time.NewTimer(phoneBrowserTimeout)
	defer t.Stop()
	select {
	case a := <-ch:
		if !a.ok {
			return nil, fmt.Errorf("phone browser: %s", strings.TrimSpace(a.err))
		}
		return a.result, nil
	case <-t.C:
		return nil, fmt.Errorf("the phone did not answer within %s; the app may have gone to the background", phoneBrowserTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answer hands a result to the call waiting on it; false when none is.
func (p *phoneBrowser) answer(id string, a phoneBrowserAnswer) bool {
	p.mu.Lock()
	ch := p.pending[id]
	p.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- a:
	default:
	}
	return true
}

// mount puts the tools on svc unless they are on it already. A settings save
// rebuilds the service without them; the phone's next check-in mounts them on
// the new one.
func (p *phoneBrowser) mount(svc *backend.Service, emit func(string, map[string]any)) {
	if svc == nil || svc.Agent() == nil {
		return
	}
	p.mu.Lock()
	if p.on == svc {
		p.mu.Unlock()
		return
	}
	p.on = svc
	p.mu.Unlock()
	for _, o := range phoneBrowserOps {
		op := o.op
		svc.Agent().AddToolWithMetadata(phoneBrowserTool(op), o.about, o.params,
			func(ctx context.Context, args map[string]any) (any, error) { return p.call(ctx, emit, op, args) },
			agent.ToolMetadata{ReadOnly: op == "snapshot" || op == "get_text" || op == "get_url"})
	}
}

func (a *App) phoneBrowserHub() *phoneBrowser {
	a.phoneBrowserOnce.Do(func() { a.phoneBrowserState = newPhoneBrowser() })
	return a.phoneBrowserState
}

// PhoneBrowserOnline is the iPhone saying its browser is open and can take
// calls. It repeats it every 30 seconds while the app is in the foreground.
func (a *App) PhoneBrowserOnline(device string) error {
	device = strings.TrimSpace(device)
	if device == "" {
		return fmt.Errorf("device is required")
	}
	p := a.phoneBrowserHub()
	p.mu.Lock()
	p.seen[device] = p.now()
	p.mu.Unlock()
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	p.mount(svc, a.emit)
	return nil
}

// PhoneBrowserResult is the iPhone's answer to one phone:browser_call.
func (a *App) PhoneBrowserResult(id string, ok bool, result, errText string) error {
	if !a.phoneBrowserHub().answer(id, phoneBrowserAnswer{ok: ok, result: result, err: errText}) {
		return fmt.Errorf("no call %s is waiting (it may have timed out)", id)
	}
	return nil
}

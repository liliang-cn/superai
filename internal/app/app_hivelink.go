package app

// The desktop window as one end of the hive. The phone and the browser talk
// to the queen; until now the Mac app talked only to the engine inside itself,
// which is not in the hive, so the screen meant to watch the hive had nothing
// to show. Linked, the window sends its calls to the queen and the queen's
// events are relayed into it; the local engine keeps running underneath for
// what only this machine can do (file pickers, the window's own theme).
//
// The link is a paired device like a phone, now made by joining the hive
// (hive_member.go). hive-link.json is what an older version kept; it is only
// read, once, to turn into a membership (hive_migrate.go).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

const hiveLinkFile = "hive-link.json"

// errOnlyInTheWindow refuses the link's methods when they arrive over HTTP:
// they are the desktop window's, and a served SuperLeo has no window.
var errOnlyInTheWindow = errors.New("only the desktop window can do this")

type hiveLink struct {
	// ID is the address it was paired at: one link per queen.
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	URL      string    `json:"url"`
	Token    string    `json:"token"`
	DeviceID string    `json:"device_id"`
	LinkedAt time.Time `json:"linked_at"`
}

// hiveBook is every hive this window has been paired with, and which one it
// is looking at now ("" is this Mac on its own).
type hiveBook struct {
	Active string     `json:"active"`
	Links  []hiveLink `json:"links"`
}

func (b *hiveBook) find(id string) int {
	for i := range b.Links {
		if b.Links[i].ID == id {
			return i
		}
	}
	return -1
}

// HiveLinkInfo is the link as the window sees it, without the token.
type HiveLinkInfo struct {
	Linked bool   `json:"linked"`
	URL    string `json:"url,omitempty"`
	Name   string `json:"name,omitempty"`
	// Live is true while the queen's event stream is connected.
	Live  bool   `json:"live"`
	Error string `json:"error,omitempty"`
}

// hiveLinkState is the App's hold on the link and its relay.
type hiveLinkState struct {
	mu     sync.Mutex
	link   *hiveLink
	cancel context.CancelFunc
	// done closes when the relay for the current link has stopped.
	done chan struct{}
	live atomic.Bool
	err  atomic.Value // string
	// on is read by emit on every event, so it is an atomic of its own.
	on atomic.Bool
}

func hiveLinkPath() string { return filepath.Join(backend.DataDir(), hiveLinkFile) }

var hiveLinkHTTP = &http.Client{Timeout: 5 * time.Minute}

// loadHiveBook reads the saved hives. A file from before there could be more
// than one — a single link — becomes a book holding it, active.
func loadHiveBook() hiveBook {
	raw, err := os.ReadFile(hiveLinkPath())
	if err != nil {
		return hiveBook{}
	}
	var b hiveBook
	if json.Unmarshal(raw, &b) == nil && len(b.Links) > 0 {
		return b
	}
	var one hiveLink
	if json.Unmarshal(raw, &one) == nil && one.URL != "" && one.Token != "" {
		one.ID, one.Name = one.URL, hostOf(one.URL)
		return hiveBook{Active: one.ID, Links: []hiveLink{one}}
	}
	return hiveBook{}
}

func saveHiveBook(b hiveBook) error {
	if len(b.Links) == 0 {
		if err := os.Remove(hiveLinkPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	raw, _ := json.MarshalIndent(b, "", "  ")
	return os.WriteFile(hiveLinkPath(), raw, 0o600)
}

// loadHiveLink is the active link, if the window is looking at a hive.
func loadHiveLink() *hiveLink {
	b := loadHiveBook()
	if i := b.find(b.Active); i >= 0 {
		l := b.Links[i]
		return &l
	}
	return nil
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

// startHiveLink picks up a saved link at startup. Desktop only: a served
// SuperLeo is reached by the browser directly and relays nothing.
func (a *App) startHiveLink() {
	if a.ctx == nil {
		return
	}
	// A member's window follows its membership: one hive, no switch.
	if m := a.member.cur.Load(); m != nil {
		a.useHiveLink(m.asLink())
		return
	}
	if l := loadHiveLink(); l != nil {
		a.useHiveLink(l)
	}
}

func (a *App) useHiveLink(l *hiveLink) {
	hl := &a.hiveLink
	hl.mu.Lock()
	defer hl.mu.Unlock()
	if hl.cancel != nil {
		hl.cancel()
		hl.cancel = nil
		// Nothing of the old link may reach the window after this returns.
		<-hl.done
	}
	hl.link = l
	hl.on.Store(l != nil)
	hl.live.Store(false)
	hl.err.Store("")
	if l == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	hl.cancel = cancel
	hl.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
		a.relayHiveEvents(ctx, *l)
	}(hl.done)
}

// HiveLinkStatus reports whether this window is linked, and to where.
func (a *App) HiveLinkStatus() HiveLinkInfo {
	hl := &a.hiveLink
	hl.mu.Lock()
	l := hl.link
	hl.mu.Unlock()
	if l == nil {
		return HiveLinkInfo{}
	}
	e, _ := hl.err.Load().(string)
	return HiveLinkInfo{Linked: true, URL: l.URL, Name: l.Name, Live: hl.live.Load(), Error: e}
}

// Remote makes one call on the queen: the same method the window would call
// here, with the same arguments, answered by her.
func (a *App) Remote(method string, args []any) (any, error) {
	if a.ctx == nil {
		return nil, errOnlyInTheWindow
	}
	a.hiveLink.mu.Lock()
	l := a.hiveLink.link
	a.hiveLink.mu.Unlock()
	if l == nil {
		return nil, errors.New("not linked to a hive")
	}
	out, err := callQueen(*l, method, args)
	if err != nil && a.isHiveMember() {
		// The hive is out of reach: say so. The engine in this process is
		// not the queen and does not answer in her place.
		return nil, a.hiveUnreachable(err)
	}
	return out, err
}

func callQueen(l hiveLink, method string, args []any) (any, error) {
	if args == nil {
		args = []any{}
	}
	body, _ := json.Marshal(args)
	req, err := http.NewRequest(http.MethodPost, l.URL+"/api/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.Token)
	resp, err := hiveLinkHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "the hive no longer accepts this computer: link it again"
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s: %s", method, msg)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return out, nil
}

// relayHiveEvents follows the queen's event stream and repeats every event in
// this window under its own name, reconnecting until the link is dropped.
func (a *App) relayHiveEvents(ctx context.Context, l hiveLink) {
	hl := &a.hiveLink
	wait := time.Second
	for ctx.Err() == nil {
		err := a.followHiveEvents(ctx, l)
		hl.live.Store(false)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			hl.err.Store(err.Error())
		}
		windowEmit(a.ctx, "hivelink:state", map[string]any{"live": false})
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < 30*time.Second {
			wait *= 2
		}
	}
}

func (a *App) followHiveEvents(ctx context.Context, l hiveLink) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL+"/api/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.Token)
	req.Header.Set("Accept", "text/event-stream")
	// No timeout: the stream is meant to stay open.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: %s", resp.Status)
	}
	hl := &a.hiveLink
	hl.live.Store(true)
	hl.err.Store("")
	windowEmit(a.ctx, "hivelink:state", map[string]any{"live": true})
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var env struct {
			Name    string         `json:"name"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &env) != nil || env.Name == "" {
			continue
		}
		windowEmit(a.ctx, env.Name, env.Payload)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("event stream closed")
}

// localWhileLinked are the events this machine's own engine still sends to a
// linked window: the ones about the machine, not the hive.
var localWhileLinked = map[string]bool{"open:conversation": true, "hivelink:state": true, "hive:membership": true}

// quietWhileLinked reports whether a local event should stay out of a linked
// window, which shows the hive's events instead.
func (a *App) quietWhileLinked(name string) bool {
	return a.hiveLink.on.Load() && !localWhileLinked[name]
}

// AssetMiddleware is the desktop window's asset server: no caching for the
// shell, and, while linked, the queen's knowledge-graph view under /graph/ —
// the same path the browser reaches her graph by, carried with this Mac's
// token instead of a session cookie. A function rather than a method, so it
// is neither bound into the window nor callable over /api/rpc.
func AssetMiddleware(a *App) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return a.assetMiddleware(next) }
}

func (a *App) assetMiddleware(next http.Handler) http.Handler {
	next = NoCache(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/graph/") {
			next.ServeHTTP(w, r)
			return
		}
		a.hiveLink.mu.Lock()
		l := a.hiveLink.link
		a.hiveLink.mu.Unlock()
		if l == nil {
			next.ServeHTTP(w, r)
			return
		}
		target, err := url.Parse(l.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = target.Host
				pr.Out.Header.Set("Authorization", "Bearer "+l.Token)
				pr.Out.Header.Del("Cookie")
			},
			FlushInterval: -1,
		}
		proxy.ServeHTTP(w, r)
	})
}

package app

// The desktop window as one end of the hive. The phone and the browser talk
// to the queen; until now the Mac app talked only to the engine inside itself,
// which is not in the hive, so the screen meant to watch the hive had nothing
// to show. Linked, the window sends its calls to the queen and the queen's
// events are relayed into it; the local engine keeps running underneath for
// what only this machine can do (file pickers, the window's own theme).
//
// The link is a paired device like a phone: a code shown by the queen,
// exchanged once for a token, kept in hive-link.json beside the settings.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

const hiveLinkFile = "hive-link.json"

// errOnlyInTheWindow refuses the link's methods when they arrive over HTTP:
// they are the desktop window's, and a served SuperAI has no window.
var errOnlyInTheWindow = errors.New("only the desktop window can do this")

type hiveLink struct {
	URL      string    `json:"url"`
	Token    string    `json:"token"`
	DeviceID string    `json:"device_id"`
	LinkedAt time.Time `json:"linked_at"`
}

// HiveLinkInfo is the link as the window sees it, without the token.
type HiveLinkInfo struct {
	Linked bool   `json:"linked"`
	URL    string `json:"url,omitempty"`
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

func loadHiveLink() *hiveLink {
	b, err := os.ReadFile(hiveLinkPath())
	if err != nil {
		return nil
	}
	var l hiveLink
	if json.Unmarshal(b, &l) != nil || l.URL == "" || l.Token == "" {
		return nil
	}
	return &l
}

// startHiveLink picks up a saved link at startup. Desktop only: a served
// SuperAI is reached by the browser directly and relays nothing.
func (a *App) startHiveLink() {
	if a.ctx == nil {
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

// LinkHive pairs this window with the queen at address, using a code the queen
// is showing (Settings → Pair a device on any of her screens).
func (a *App) LinkHive(address, code string) (HiveLinkInfo, error) {
	if a.ctx == nil {
		return HiveLinkInfo{}, errOnlyInTheWindow
	}
	base := strings.TrimRight(strings.TrimSpace(address), "/")
	if base == "" || strings.TrimSpace(code) == "" {
		return HiveLinkInfo{}, errors.New("the address and the code are both needed")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(code), "device": "SuperAI on " + strings.TrimSuffix(host, ".local")})
	resp, err := hiveLinkHTTP.Post(base+pairClaimPath, "application/json", bytes.NewReader(body))
	if err != nil {
		return HiveLinkInfo{}, fmt.Errorf("could not reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	var out struct {
		Token    string `json:"token"`
		DeviceID string `json:"device_id"`
		Error    string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.Token == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return HiveLinkInfo{}, errors.New(out.Error)
	}
	l := &hiveLink{URL: base, Token: out.Token, DeviceID: out.DeviceID, LinkedAt: time.Now().UTC()}
	raw, _ := json.MarshalIndent(l, "", "  ")
	if err := os.WriteFile(hiveLinkPath(), raw, 0o600); err != nil {
		return HiveLinkInfo{}, err
	}
	a.useHiveLink(l)
	return a.HiveLinkStatus(), nil
}

// UnlinkHive forgets the link; the window goes back to this Mac alone.
func (a *App) UnlinkHive() error {
	a.useHiveLink(nil)
	if err := os.Remove(hiveLinkPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
	return HiveLinkInfo{Linked: true, URL: l.URL, Live: hl.live.Load(), Error: e}
}

// Remote makes one call on the queen: the same method the window would call
// here, with the same arguments, answered by her.
func (a *App) Remote(method string, args []any) (any, error) {
	if a.ctx == nil {
		return nil, errOnlyInTheWindow
	}
	hl := &a.hiveLink
	hl.mu.Lock()
	l := hl.link
	hl.mu.Unlock()
	if l == nil {
		return nil, errors.New("not linked to a hive")
	}
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
			msg = "the hive no longer accepts this Mac: link it again"
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
var localWhileLinked = map[string]bool{"open:conversation": true, "hivelink:state": true}

// quietWhileLinked reports whether a local event should stay out of a linked
// window, which shows the hive's events instead.
func (a *App) quietWhileLinked(name string) bool {
	return a.hiveLink.on.Load() && !localWhileLinked[name]
}

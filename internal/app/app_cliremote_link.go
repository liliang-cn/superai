package app

// Linking another SuperAI, the way a phone is paired with one: the other side
// shows a six-digit code (Settings › Runtime › Pair a device), this side is
// given its address and the code, and claims a device key of its own. The
// key goes under Remote agents with the address, and the other machine's CLIs
// become "claude.<name>" here. The other side can unpair this one like any
// phone.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/liliang-cn/superai/internal/backend"
)

// LinkedSuperAI is one other SuperAI this one can drive.
type LinkedSuperAI struct {
	Name string   `json:"name"`
	URL  string   `json:"url"`
	CLIs []string `json:"clis"`
	// Reachable is false when it did not answer just now.
	Reachable bool `json:"reachable"`
}

// LinkSuperAI pairs with the SuperAI at address using a code it is showing,
// and returns the name its CLIs are addressed by ("claude.<name>").
func (a *App) LinkSuperAI(address, code, name string) (string, error) {
	base, err := normaliseSuperAIURL(address)
	if err != nil {
		return "", err
	}
	digits := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, code)
	if len(digits) != 6 {
		return "", errors.New("the pairing code is six digits")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		u, _ := url.Parse(base)
		name = u.Hostname()
	}
	if !validLinkName(name) {
		return "", fmt.Errorf("%q cannot follow an @: use letters, digits, - _ or .", name)
	}
	cur := a.GetSettings()
	if r, ok := cur.RemoteAgents.Agents[name]; ok && strings.TrimRight(r.URL, "/") != base {
		return "", fmt.Errorf("%s is already the name of another remote agent", name)
	}

	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"code": digits, "device": strings.TrimSuffix(host, ".local") + " · SuperAI"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+pairClaimPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cliRemoteHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var got struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &got)
	if resp.StatusCode != http.StatusOK || got.Token == "" {
		if got.Error != "" {
			return "", errors.New(got.Error)
		}
		return "", fmt.Errorf("%s did not pair: %s", base, resp.Status)
	}

	link := backend.RemoteAgent{
		About: "SuperAI at " + strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://"),
		URL:   base, Token: got.Token,
	}
	links := backend.LoadLinkedSuperAIs()
	links[name] = link
	if err := backend.SaveLinkedSuperAIs(links); err != nil {
		return "", err
	}
	s := a.GetSettings()
	if s.RemoteAgents.Agents == nil {
		s.RemoteAgents.Agents = map[string]backend.RemoteAgent{}
	}
	s.RemoteAgents.Enabled = true
	s.RemoteAgents.Agents[name] = link
	if err := a.SaveSettings(s); err != nil {
		return "", err
	}
	a.forgetRemoteCLIs()
	return name, nil
}

// LinkedSuperAIs lists the other SuperAIs and the CLIs each has.
func (a *App) LinkedSuperAIs() []LinkedSuperAI {
	a.forgetRemoteCLIs()
	clis := a.remoteCLIs()
	out := []LinkedSuperAI{}
	for name, r := range a.cliRemotes() {
		_, ok := clis[name]
		if !ok {
			// Reachable with no CLIs installed is still reachable.
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			ok = r.rpc(ctx, "ExternalAgentsStatus", []any{}, nil) == nil
			cancel()
		}
		out = append(out, LinkedSuperAI{Name: name, URL: r.base, CLIs: clis[name], Reachable: ok})
	}
	return out
}

// UnlinkSuperAI forgets another SuperAI here. Its key stays valid there until
// this machine is unpaired on that side.
func (a *App) UnlinkSuperAI(name string) error {
	s := a.GetSettings()
	if _, ok := s.RemoteAgents.Agents[name]; !ok {
		return fmt.Errorf("no remote agent called %s", name)
	}
	delete(s.RemoteAgents.Agents, name)
	links := backend.LoadLinkedSuperAIs()
	delete(links, name)
	if err := backend.SaveLinkedSuperAIs(links); err != nil {
		return err
	}
	if err := a.SaveSettings(s); err != nil {
		return err
	}
	a.forgetRemoteCLIs()
	return nil
}

func (a *App) forgetRemoteCLIs() {
	a.remoteCLI.mu.Lock()
	a.remoteCLI.names = nil
	a.remoteCLI.mu.Unlock()
}

func normaliseSuperAIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("the address is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%q is not an address", raw)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func validLinkName(n string) bool {
	if n == "" || len(n) > 40 {
		return false
	}
	for _, r := range n {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

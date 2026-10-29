package app

// One hive: this process is either a hive of one machine (its own queen), or
// a member of a hive whose queen is elsewhere. Never both, and never a queen
// that quietly takes over because the real one is out of reach, since two
// queens are two sets of conversations and memories.
//
// Membership is what is on disk, in the app's own home:
//
//	hive-member.json   the hive's address, where its agent link is, the name
//	                   the queen knows this machine by
//	hive-member.token  the token the pairing code was traded for (0600)
//
// isHiveMember is the one question every duty asks. A member runs its agent
// link in the background (the same RunAgentLink a `superleo agent` runs), is a
// worker of the hive, and its window talks to the hive's queen. The duties
// that belong to a queen — schedules, the Telegram bridge, the hive roster,
// the alarm watch, the phone link, the door other machines join by — stay off
// until the Mac leaves.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	hiveMemberFile      = "hive-member.json"
	hiveMemberTokenFile = "hive-member.token"
)

// errInAHive refuses what only a Mac on its own can do.
var errInAHive = errors.New("this Mac belongs to a hive: leave it first")

// hiveMember is the persisted membership.
type hiveMember struct {
	// URL is the hive's web address: where the window's calls go.
	URL string `json:"url"`
	// Link is the hive's agent link, host:port, or several joined by commas.
	Link string `json:"link"`
	// Name is what the queen calls this machine.
	Name     string    `json:"name"`
	TLS      bool      `json:"tls,omitempty"`
	DeviceID string    `json:"device_id,omitempty"`
	JoinedAt time.Time `json:"joined_at"`

	token string
}

func (m *hiveMember) asLink() *hiveLink {
	return &hiveLink{ID: m.URL, Name: hostOf(m.URL), URL: m.URL, Token: m.token, DeviceID: m.DeviceID, LinkedAt: m.JoinedAt}
}

func hiveMemberPath() string      { return filepath.Join(backend.DataDir(), hiveMemberFile) }
func hiveMemberTokenPath() string { return filepath.Join(backend.DataDir(), hiveMemberTokenFile) }

// loadHiveMember reads the membership, or nil when this process is in no hive
// (nothing saved, or what is saved cannot be used).
func loadHiveMember() *hiveMember {
	raw, err := os.ReadFile(hiveMemberPath())
	if err != nil {
		return nil
	}
	var m hiveMember
	if json.Unmarshal(raw, &m) != nil || m.URL == "" || m.Link == "" || m.Name == "" {
		return nil
	}
	tok, err := os.ReadFile(hiveMemberTokenPath())
	if err != nil || strings.TrimSpace(string(tok)) == "" {
		return nil
	}
	m.token = strings.TrimSpace(string(tok))
	return &m
}

func (m *hiveMember) save() error {
	if err := os.MkdirAll(backend.DataDir(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(hiveMemberTokenPath(), []byte(m.token+"\n"), 0o600); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(hiveMemberPath(), raw, 0o600)
}

func clearHiveMember() {
	_ = os.Remove(hiveMemberTokenPath())
	_ = os.Remove(hiveMemberPath())
}

// hiveMemberState is the App's hold on its membership.
type hiveMemberState struct {
	// cur mirrors the file, so hot paths do not read the disk.
	cur atomic.Pointer[hiveMember]
	// mu serialises Join, Leave and the start of the link.
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	// up is whether the agent link is connected now, err why not.
	up  atomic.Bool
	err atomic.Value // string
	// told is the last state emitted, so only a change is.
	told atomic.Value // string
}

// isHiveMember is whether this process is a member of a hive whose queen is
// another SuperLeo. It is true from the moment a join is saved until a leave.
func (a *App) isHiveMember() bool { return a.member.cur.Load() != nil }

// refreshMember reads the membership from disk.
func (a *App) refreshMember() { a.member.cur.Store(loadHiveMember()) }

// HiveMembership is where this Mac stands in the hive, for the screens.
type HiveMembership struct {
	Joined bool `json:"joined"`
	// Hive is the hive's web address, Link its agent link, Name what the
	// queen calls this Mac.
	Hive string `json:"hive,omitempty"`
	Link string `json:"link,omitempty"`
	Name string `json:"name,omitempty"`
	// Connected is whether the agent link is up now. Unreachable is a link
	// that is down because the hive did not answer; until the first try has
	// failed it is neither.
	Connected   bool   `json:"connected"`
	Unreachable bool   `json:"unreachable"`
	Error       string `json:"error,omitempty"`
	// WindowLive is whether the window's event stream from the queen is up.
	WindowLive bool `json:"windowLive"`
	// NeedsCode is a hive this Mac was paired with before, which cannot become
	// a membership without a new pairing code.
	NeedsCode bool `json:"needsCode,omitempty"`
	// LocalDuties are the queen's duties running in this process: empty for a
	// member.
	LocalDuties []string `json:"localDuties"`
}

// HiveMembership reports the membership.
func (a *App) HiveMembership() HiveMembership {
	out := HiveMembership{LocalDuties: a.localDuties()}
	m := a.member.cur.Load()
	if m == nil {
		// A hive paired the old way and not converted.
		out.NeedsCode = loadHiveLink() != nil
		return out
	}
	e, _ := a.member.err.Load().(string)
	up := a.member.up.Load()
	out.Joined, out.Hive, out.Link, out.Name = true, m.URL, m.Link, m.Name
	out.Connected, out.Unreachable, out.Error = up, !up && e != "", e
	out.WindowLive = a.hiveLink.live.Load()
	return out
}

// localDuties names the queen's duties this process is running.
func (a *App) localDuties() []string {
	out := []string{}
	a.mu.Lock()
	if a.scheduler != nil {
		out = append(out, "scheduler")
	}
	if a.telegram != nil {
		out = append(out, "telegram")
	}
	if a.hive != nil || a.hiveStop != nil {
		out = append(out, "hive")
	}
	a.mu.Unlock()
	a.alarmsMu.Lock()
	if a.alarmsStop != nil {
		out = append(out, "alarms")
	}
	a.alarmsMu.Unlock()
	a.mu.Lock()
	if a.phones != nil {
		out = append(out, "phones")
	}
	a.mu.Unlock()
	a.doorMu.Lock()
	if a.doorStop != nil {
		out = append(out, "door")
	}
	a.doorMu.Unlock()
	return out
}

func (a *App) emitMembership() {
	st := a.HiveMembership()
	key := fmt.Sprintf("%v/%v/%s", st.Joined, st.Connected, st.Error)
	if prev, _ := a.member.told.Load().(string); prev == key {
		return
	}
	a.member.told.Store(key)
	a.emit("hive:membership", map[string]any{
		"joined": st.Joined, "connected": st.Connected, "unreachable": st.Unreachable,
		"error": st.Error, "hive": st.Hive, "name": st.Name,
	})
}

// noteAgentLink is the agent link saying it is up, or why it is not.
func (a *App) noteAgentLink(up bool, err error) {
	if !a.isHiveMember() {
		return
	}
	msg := ""
	if err != nil {
		msg = plainLinkError(err, a.member.cur.Load())
	}
	a.member.up.Store(up)
	a.member.err.Store(msg)
	a.emitMembership()
}

// plainLinkError is why the link failed, in words that do not need the gRPC
// status format.
func plainLinkError(err error, m *hiveMember) string {
	where := "the hive"
	if m != nil {
		where = "the hive at " + hostOf(m.URL)
	}
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied:
		return where + " no longer accepts this Mac: join again with a new code"
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return where + " is not reachable"
	}
	return where + ": " + err.Error()
}

// hiveAddress makes the address a person typed into the hive's web address:
// https unless a port was given, which is a hive on the local network.
func hiveAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		if h, _, err := splitHostPortLoose(raw); err == nil && strings.Contains(raw, ":") && h != "" {
			raw = "http://" + raw
		} else {
			raw = "https://" + raw
		}
	}
	return normaliseSuperLeoURL(raw)
}

func splitHostPortLoose(s string) (string, string, error) {
	u, err := url.Parse("http://" + s)
	if err != nil {
		return "", "", err
	}
	return u.Hostname(), u.Port(), nil
}

// machineHiveName is what the queen should call this Mac: the name the old
// node or agent here already used, so the queen sees the same machine, else
// the short host name.
func machineHiveName() string {
	if n := oldNodeName(); validLinkName(n) {
		return n
	}
	h, _ := os.Hostname()
	n := strings.Split(strings.TrimSuffix(h, ".local"), ".")[0]
	if !validLinkName(n) {
		return "mac"
	}
	return n
}

// JoinHive makes this Mac a member of the hive at address, with a pairing code
// the hive's queen is showing. The code is traded for a token of this Mac's
// own, kept in the app's home; from then on the app is a worker of that hive,
// its window is the queen's, and the queen's duties here stop. A Mac is in one
// hive at a time.
func (a *App) JoinHive(address, code string) (HiveMembership, error) {
	ms := &a.member
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if a.isHiveMember() {
		return a.HiveMembership(), errors.New("this Mac is already in a hive: leave it first")
	}
	base, err := hiveAddress(address)
	if err != nil {
		return HiveMembership{}, err
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	if len(digits) != 6 {
		return HiveMembership{}, errors.New("the pairing code is six digits")
	}
	name := machineHiveName()
	c, err := claimAgent(base, digits, "agent "+name)
	if err != nil {
		return HiveMembership{}, err
	}
	if c.Link == "" {
		return HiveMembership{}, fmt.Errorf("paired, but %s does not say where agents reach it", base)
	}
	m := &hiveMember{URL: base, Link: c.Link, Name: name, DeviceID: c.DeviceID, JoinedAt: time.Now().UTC(), token: c.Token}
	m.TLS = strings.HasSuffix(strings.Split(c.Link, ",")[0], ":443")
	if err := m.save(); err != nil {
		return HiveMembership{}, err
	}
	a.enterHive(m)
	return a.HiveMembership(), nil
}

// enterHive makes m this process's hive: the queen's duties stop, the link
// starts, the window follows. ms.mu is held.
func (a *App) enterHive(m *hiveMember) {
	a.member.cur.Store(m)
	a.member.up.Store(false)
	a.member.err.Store("")
	a.stopQueenDuties()
	a.startMemberLink()
	if a.ctx != nil {
		a.useHiveLink(m.asLink())
	}
	a.emitMembership()
}

// LeaveHive takes this Mac out of its hive: the hive is asked to forget it,
// the link stops, and the queen's duties start again here — this Mac is its
// own hive, as before it joined. The conversations it held stay with the
// hive.
func (a *App) LeaveHive() (HiveMembership, error) {
	ms := &a.member
	ms.mu.Lock()
	defer ms.mu.Unlock()
	m := ms.cur.Load()
	if m == nil {
		return a.HiveMembership(), nil
	}
	// Best effort: a hive that cannot be reached keeps a dead device in its
	// list, which it can remove itself.
	if m.DeviceID != "" {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = callQueen(*m.asLink(), "UnpairDevice", []any{m.DeviceID})
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	a.stopMemberLink()
	clearHiveMember()
	ms.cur.Store(nil)
	ms.up.Store(false)
	ms.err.Store("")
	if a.ctx != nil {
		a.useHiveLink(loadHiveLink())
	}
	a.startQueenDuties()
	a.emitMembership()
	return a.HiveMembership(), nil
}

// startMemberLink runs the agent link of the membership in the background.
// ms.mu is held, or nothing else is running yet.
func (a *App) startMemberLink() {
	ms := &a.member
	m := ms.cur.Load()
	if m == nil {
		return
	}
	a.stopMemberLink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ms.cancel, ms.done = cancel, done
	o := AgentLinkOptions{Core: m.Link, Token: m.token, Name: m.Name, TLS: m.TLS, Version: buildVersion()}
	go func() {
		defer close(done)
		a.RunAgentLink(ctx, o)
	}()
}

func (a *App) stopMemberLink() {
	ms := &a.member
	if ms.cancel == nil {
		return
	}
	ms.cancel()
	select {
	case <-ms.done:
	case <-time.After(5 * time.Second):
	}
	ms.cancel, ms.done = nil, nil
}

// stopQueenDuties stops what only a queen does, as Shutdown would have, but
// leaves the process running. The model and tools stay: the queen hands this
// Mac whole tasks and it does them with its own.
func (a *App) stopQueenDuties() {
	a.stopTelegram()
	a.stopHive()
	a.stopLocalHive()
	a.stopAlarmWatch()
	a.stopScheduler()
	a.stopPhoneLink()
	a.stopCompanion()
	a.stopAgentDoor()
}

// startQueenDuties starts them, for a Mac that is not in a hive: at boot, and
// after leaving.
func (a *App) startQueenDuties() {
	if a.isHiveMember() {
		return
	}
	a.startScheduler()
	a.startTelegram()
	a.startHive()
	a.startAlarmWatch()
	a.lookForRemoteAgents()
	if a.ctx != nil {
		go a.startPhoneLinkIfPaired()
		a.startAgentDoor()
	}
}

// startAgentDoor opens the door other machines join this one by, unless this
// Mac is in a hive, whose queen has the door.
func (a *App) startAgentDoor() {
	if a.isHiveMember() || a.ctx == nil {
		return
	}
	a.doorMu.Lock()
	defer a.doorMu.Unlock()
	if a.doorStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.doorStop = cancel
	go a.openAgentDoor(ctx)
}

func (a *App) stopAgentDoor() {
	a.doorMu.Lock()
	cancel := a.doorStop
	a.doorStop = nil
	a.doorMu.Unlock()
	if cancel != nil {
		cancel()
		a.agentDoor.reset()
	}
}

// startMemberAtLaunch is the start of a process that was left in a hive:
// the link, and the window on the queen.
func (a *App) startMemberAtLaunch() {
	a.member.mu.Lock()
	defer a.member.mu.Unlock()
	if m := a.member.cur.Load(); m != nil {
		a.startMemberLink()
	}
}

// errHiveUnreachable is what a call meant for the queen says when she cannot
// be reached. It never falls back to this Mac's own engine.
func (a *App) hiveUnreachable(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	where := "the hive"
	if m := a.member.cur.Load(); m != nil {
		where = "the hive at " + hostOf(m.URL)
	}
	return fmt.Errorf("%s is not reachable, so nothing can be done until it is", where)
}

package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The hive: how workers find each other.
//
// hive_call.go is how one SuperAI commands another. This is how the commander comes
// to know who there is to command — and it replaces a list someone typed into
// a Secret, which was wrong the moment a pod was rescheduled, a worker was added
// or one died and nothing said so.
//
// The protocol is one message, repeated. A worker says who it is to the queen
// worker: protocol, name, role, where it can be reached, and the credential the
// queen should use to command it. The queen answers with how often to say
// it again. That is joining and it is also the heartbeat, which is why there is
// one endpoint and no separate "leave": a worker that stops saying it is gone,
// and the roster reflects that without anyone having to notice.
//
// Roles are what the settings say, and a role decides what an instance may do
// rather than what it is called:
//
//	queen  accepts joins, keeps the roster, commands
//	worker     announces itself, obeys
//	(none)   a standalone install; neither joins nor is joined
//
// Handing over the worker's own bearer token in the hello is the join itself —
// "you may command me" is exactly what that sentence means — so it travels
// only to the address the worker was told to join.

// HiveProtocol names the wire format. A queen that meets a different string
// refuses rather than guessing, so a v2 worker cannot half-join a v1 queen.
const HiveProtocol = "superai-hive/1"

const (
	HiveRoleQueen  = "queen"
	HiveRoleWorker = "worker"
)

// DefaultHiveInterval is how often a worker re-announces itself.
const DefaultHiveInterval = 10 * time.Second

// hiveMisses is how many intervals of silence make a member lost. Three,
// because one missed beat is a slow request and two is a busy pod; the third
// is when it is worth telling the commander not to try.
const hiveMisses = 3

// hiveForget is how long a lost member stays on the roster before it is
// dropped, so a worker that is restarting shows up as lost rather than
// vanishing and reappearing as new.
const hiveForget = 15 * time.Minute

// HiveSettings configures this instance's part in a hive. The zero value is a
// standalone install.
type HiveSettings struct {
	// Role is "queen", "worker", or empty.
	Role string `json:"role,omitempty"`
	// Name of this worker in the roster and after the @. Empty takes the
	// hostname, which in a StatefulSet is already stable and unique.
	Name string `json:"name,omitempty"`
	// JoinURL is the queen's address. Only a worker uses it.
	JoinURL string `json:"join_url,omitempty"`
	// JoinToken is the bearer the queen's API wants. Empty reuses this
	// instance's own token, which is right when a hive shares one.
	JoinToken string `json:"join_token,omitempty"`
	// AdvertiseURL is where the queen should reach this worker. Empty takes
	// $SUPERAI_ADVERTISE_URL.
	AdvertiseURL string `json:"advertise_url,omitempty"`
	// PeerToken is the bearer other workers accept, when it is not this
	// instance's own. Empty is right for a hive that shares one token.
	PeerToken string `json:"peer_token,omitempty"`
	// Spawner lets a queen make and retire workers. See hive_spawn.go.
	Spawner *SpawnerSettings `json:"spawner,omitempty"`
	// IntervalSeconds between announcements. Zero takes the default.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// MaxConcurrentOrders is how many turns a worker runs at once, orders and
	// its own person's together. Zero takes DefaultMaxConcurrentOrders. Past
	// it a turn is refused rather than queued, so the queen's scheduler moves
	// the order to a worker that is free instead of waiting on one that is not.
	MaxConcurrentOrders int `json:"max_concurrent_orders,omitempty"`
}

// DefaultMaxConcurrentOrders is the worker's concurrency when the settings
// do not say: one order from the queen, one question from a peer, and one
// person at the keyboard.
const DefaultMaxConcurrentOrders = 3

func (h HiveSettings) Concurrency() int {
	if h.MaxConcurrentOrders <= 0 {
		return DefaultMaxConcurrentOrders
	}
	return h.MaxConcurrentOrders
}

func (h HiveSettings) Interval() time.Duration {
	if h.IntervalSeconds <= 0 {
		return DefaultHiveInterval
	}
	return time.Duration(h.IntervalSeconds) * time.Second
}

// HiveHello is what a worker sends: who it is and how to reach it.
type HiveHello struct {
	Protocol  string    `json:"protocol"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	URL       string    `json:"url"`
	Token     string    `json:"token,omitempty"`
	Version   string    `json:"version,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Engine says what is behind this worker when it is not a SuperAI ("cli ·
	// claude"), and About is a line for the roster. Both are only labels.
	Engine string `json:"engine,omitempty"`
	About  string `json:"about,omitempty"`
	// Node is the machine the worker runs on ($SUPERAI_NODE; in k3s the pod's
	// node), so a client can draw workers where they really are.
	Node string `json:"node,omitempty"`
}

// HiveWelcome is the queen's answer.
type HiveWelcome struct {
	Protocol   string `json:"protocol"`
	Queen      string `json:"queen"`
	IntervalMS int    `json:"interval_ms"`
	Members    int    `json:"members"`
}

// HiveMember is one worker on the roster.
type HiveMember struct {
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	URL      string    `json:"url"`
	Version  string    `json:"version,omitempty"`
	JoinedAt time.Time `json:"joined_at"`
	LastSeen time.Time `json:"last_seen"`
	// StartedAt is when the worker's process started, as it says. Two pods that
	// share a name (a StatefulSet's replacement for a retired ordinal) have
	// different ones, which is what tells a new worker from the last
	// heartbeat of the one it replaced.
	StartedAt time.Time `json:"started_at"`
	Engine    string    `json:"engine,omitempty"`
	Node      string    `json:"node,omitempty"`
	about     string
	// State is "live" or "lost", computed when asked rather than stored, so it
	// cannot go stale between sweeps.
	State string `json:"state"`
	token string
}

var hiveName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// Hive is the queen's roster.
type Hive struct {
	name     string
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	members map[string]*HiveMember
	// retired is who was let go, by the start time of the process let go, so
	// its last heartbeats while the pod stops do not put it back.
	retired map[string]time.Time
}

func NewHive(name string, interval time.Duration) *Hive {
	if interval <= 0 {
		interval = DefaultHiveInterval
	}
	return &Hive{name: name, interval: interval, now: time.Now, members: map[string]*HiveMember{}, retired: map[string]time.Time{}}
}

// Join records one announcement. Idempotent: the tenth hello from a worker is the
// same call as the first, and only refreshes when it was last heard.
func (h *Hive) Join(hello HiveHello) (HiveWelcome, error) {
	if hello.Protocol != HiveProtocol {
		return HiveWelcome{}, fmt.Errorf("unsupported protocol %q; this queen speaks %s", hello.Protocol, HiveProtocol)
	}
	if hello.Role != HiveRoleWorker {
		// Two queens in one hive is two things giving orders. Refused
		// outright, because there is no arrangement of it that is safe.
		return HiveWelcome{}, fmt.Errorf("role %q cannot join: only workers join a queen", hello.Role)
	}
	if !hiveName.MatchString(hello.Name) {
		return HiveWelcome{}, fmt.Errorf("name %q is not usable: letters, digits, . _ - and at most 40 characters", hello.Name)
	}
	u, err := url.Parse(hello.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return HiveWelcome{}, fmt.Errorf("url %q is not an http(s) address", hello.URL)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if at, ok := h.retired[hello.Name]; ok {
		if hello.StartedAt.Equal(at) {
			return HiveWelcome{Protocol: HiveProtocol, Queen: h.name, IntervalMS: int(h.interval / time.Millisecond), Members: len(h.members)}, nil
		}
		delete(h.retired, hello.Name)
	}
	now := h.now()
	m, ok := h.members[hello.Name]
	if !ok {
		m = &HiveMember{Name: hello.Name, JoinedAt: now}
		h.members[hello.Name] = m
		log.Printf("hive: %s joined at %s", hello.Name, hello.URL)
	} else if m.URL != hello.URL || h.stateLocked(m, now) == "lost" {
		log.Printf("hive: %s is back at %s", hello.Name, hello.URL)
	}
	m.Role, m.URL, m.Version, m.token, m.LastSeen = hello.Role, strings.TrimRight(hello.URL, "/"), hello.Version, hello.Token, now
	m.StartedAt, m.Engine, m.about, m.Node = hello.StartedAt, hello.Engine, hello.About, hello.Node
	return HiveWelcome{
		Protocol: HiveProtocol, Queen: h.name,
		IntervalMS: int(h.interval / time.Millisecond), Members: len(h.members),
	}, nil
}

func (h *Hive) stateLocked(m *HiveMember, now time.Time) string {
	if now.Sub(m.LastSeen) > hiveMisses*h.interval {
		return "lost"
	}
	return "live"
}

// Leave removes a worker that said goodbye. It only counts when the goodbye
// carries the start time of the process on the roster: a StatefulSet's
// replacement pod has the same name, and the old one's last words arriving after
// the new one has joined must not remove it.
func (h *Hive) Leave(name string, startedAt time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.members[name]
	if !ok || !m.StartedAt.Equal(startedAt) {
		return false
	}
	delete(h.members, name)
	log.Printf("hive: %s left", name)
	return true
}

// Retire takes workers being let go off the roster at once. A retired worker
// is not lost — nobody should be told it went missing.
func (h *Hive) Retire(names []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range names {
		if m, ok := h.members[n]; ok {
			h.retired[n] = m.StartedAt
			delete(h.members, n)
			log.Printf("hive: %s retired", n)
		}
	}
}

// Members is the roster, sorted by name, dropping anyone lost for long enough.
func (h *Hive) Members() []HiveMember {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	out := make([]HiveMember, 0, len(h.members))
	for name, m := range h.members {
		if now.Sub(m.LastSeen) > hiveForget {
			delete(h.members, name)
			continue
		}
		c := *m
		c.State = h.stateLocked(m, now)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Agents is the live members as remote agents, ready for the runner. A lost
// worker is not offered: commanding one is a connection timeout with a name.
func (h *Hive) Agents() map[string]RemoteAgent {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	out := map[string]RemoteAgent{}
	for name, m := range h.members {
		if h.stateLocked(m, now) != "live" {
			continue
		}
		about := fmt.Sprintf("%s — a worker in the hive, at %s. It has its own tools and the shared memory.", name, m.URL)
		if m.about != "" {
			about = fmt.Sprintf("%s — %s", name, m.about)
		} else if m.Engine != "" {
			about = fmt.Sprintf("%s — a %s agent in the hive, at %s.", name, m.Engine, m.URL)
		}
		out[name] = RemoteAgent{
			About: about,
			URL:   m.URL, Token: m.token,
		}
	}
	return out
}

// Announcer is a worker's half: say who it is, and keep saying it.
type Announcer struct {
	Settings HiveSettings
	// Token is this instance's own bearer, sent so the queen can command it,
	// and used to authenticate the join when JoinToken is empty.
	Token   string
	Version string
	// Engine and About label a worker that is not a SuperAI. See HiveHello.
	Engine string
	About  string

	started time.Time
	client  *http.Client

	mu      sync.Mutex
	joined  bool
	lastOK  time.Time
	lastErr string
	peers   map[string]RemoteAgent
	peersAt time.Time
	queen   string
}

// AnnouncerState is what a worker knows about its own membership.
type AnnouncerState struct {
	Joined  bool      `json:"joined"`
	LastOK  time.Time `json:"last_ok"`
	LastErr string    `json:"error"`
}

// State reports whether the last announcement was heard, and if not why.
func (a *Announcer) State() AnnouncerState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AnnouncerState{Joined: a.joined, LastOK: a.lastOK, LastErr: a.lastErr}
}

func (a *Announcer) record(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.joined, a.lastOK, a.lastErr = true, time.Now(), ""
		return
	}
	a.joined, a.lastErr = false, err.Error()
}

// Name is the configured name or, failing that, the hostname.
func (a *Announcer) Name() string { return a.Settings.Self() }

// Self is what this instance is called in the hive: the configured name, else
// the hostname.
func (h HiveSettings) Self() string {
	if n := strings.TrimSpace(h.Name); n != "" {
		return n
	}
	n, _ := os.Hostname()
	return n
}

func (a *Announcer) advertise() string {
	if u := strings.TrimSpace(a.Settings.AdvertiseURL); u != "" {
		return u
	}
	return strings.TrimSpace(os.Getenv("SUPERAI_ADVERTISE_URL"))
}

// Validate says why this worker cannot announce itself, before the loop starts
// and cannot say anything.
func (a *Announcer) Validate() error {
	if a.Settings.Role != HiveRoleWorker {
		return errors.New("only a worker announces itself")
	}
	if strings.TrimSpace(a.Settings.JoinURL) == "" {
		return errors.New("hive.join_url is empty: a worker has to be told where the queen is")
	}
	if a.advertise() == "" {
		return errors.New("no advertise url: set hive.advertise_url or $SUPERAI_ADVERTISE_URL to where the queen can reach this worker")
	}
	if !hiveName.MatchString(a.Name()) {
		return fmt.Errorf("worker name %q is not usable", a.Name())
	}
	return nil
}

// Once sends one hello and returns the queen's answer.
func (a *Announcer) Once(ctx context.Context) (HiveWelcome, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 10 * time.Second}
	}
	if a.started.IsZero() {
		a.started = time.Now()
	}
	body, _ := json.Marshal(HiveHello{
		Protocol: HiveProtocol, Name: a.Name(), Role: HiveRoleWorker, URL: a.advertise(),
		Token: a.Token, Version: a.Version, StartedAt: a.started, Engine: a.Engine, About: a.About,
		Node: os.Getenv("SUPERAI_NODE"),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.Settings.JoinURL, "/")+"/api/hive/join", bytes.NewReader(body))
	if err != nil {
		return HiveWelcome{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		return HiveWelcome{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return HiveWelcome{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var w HiveWelcome
	if err := json.Unmarshal(raw, &w); err != nil || w.Protocol != HiveProtocol {
		return HiveWelcome{}, fmt.Errorf("that is not a %s queen: %s", HiveProtocol, strings.TrimSpace(string(raw)))
	}
	if w.Queen != "" {
		a.mu.Lock()
		a.queen = w.Queen
		a.mu.Unlock()
	}
	return w, nil
}

// Leave tells the queen this worker is going away on purpose, so it comes off
// the roster at once instead of sitting there as "lost" until it is forgotten.
// A worker that vanishes without saying so is still lost, which is the point of
// having both.
func (a *Announcer) Leave(ctx context.Context) error {
	if a.client == nil {
		a.client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(HiveHello{Protocol: HiveProtocol, Name: a.Name(), Role: HiveRoleWorker, StartedAt: a.started})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.Settings.JoinURL, "/")+"/api/hive/leave", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Run announces until ctx ends. It never gives up: a queen that is not up yet
// or is restarting is the ordinary case, and the loop is what makes the order
// pods start in irrelevant.
func (a *Announcer) Run(ctx context.Context) {
	wait := a.Settings.Interval()
	backoff := time.Second
	joined := false
	for {
		w, err := a.Once(ctx)
		a.record(err)
		switch {
		case err == nil:
			if !joined {
				log.Printf("hive: joined %q as %s", w.Queen, a.Name())
				joined = true
			}
			backoff = time.Second
			wait = a.Settings.Interval()
			if w.IntervalMS > 0 {
				wait = time.Duration(w.IntervalMS) * time.Millisecond
			}
		default:
			if joined {
				log.Printf("hive: lost the queen: %v", err)
			} else if backoff == time.Second {
				log.Printf("hive: cannot join yet: %v", err)
			}
			joined = false
			wait = backoff
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
		select {
		case <-ctx.Done():
			// Its own context: the one that ended is why we are here.
			if joined {
				bye, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = a.Leave(bye)
				cancel()
			}
			return
		case <-time.After(wait):
		}
	}
}

// RosterEntry is one live worker as a peer needs to know it: where it is. No
// credential travels here; how a worker authenticates to a peer is the
// worker's own business (see Announcer.Peers).
type RosterEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Roster lists the live workers, sorted.
func (h *Hive) Roster() []RosterEntry {
	out := []RosterEntry{}
	for _, m := range h.Members() {
		if m.State == "live" {
			out = append(out, RosterEntry{Name: m.Name, URL: m.URL})
		}
	}
	return out
}

// peerTTL is how long a worker believes the roster it fetched. Short: a peer
// that just joined should be askable within a moment, and one that left should
// stop being offered.
const peerTTL = 5 * time.Second

// Peers is the other workers, from the queen's roster, ready to be asked.
//
// A peer is authenticated with PeerToken when set and otherwise with this
// worker's own bearer — which is right for a hive that shares one token, the
// way the k8s deployment builds it, and is why a hive that does not must set
// it. The queen never hands out anyone's credential.
func (a *Announcer) Peers(ctx context.Context) map[string]RemoteAgent {
	a.mu.Lock()
	if a.peers != nil && time.Since(a.peersAt) < peerTTL {
		p := a.peers
		a.mu.Unlock()
		return p
	}
	a.mu.Unlock()

	if a.client == nil {
		a.client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.Settings.JoinURL, "/")+"/api/hive/roster", nil)
	if err != nil {
		return nil
	}
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.peers // stale beats none when the queen blinks
	}
	defer resp.Body.Close()
	var roster []RosterEntry
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&roster) != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.peers
	}
	pt := a.Settings.PeerToken
	if pt == "" {
		pt = a.Token
	}
	peers := map[string]RemoteAgent{}
	for _, e := range roster {
		if e.Name == a.Name() {
			continue
		}
		peers[e.Name] = RemoteAgent{
			About: fmt.Sprintf("%s — a fellow worker in the hive. It has its own tools and the shared memory.", e.Name),
			URL:   e.URL, Token: pt,
		}
	}
	a.mu.Lock()
	a.peers, a.peersAt = peers, time.Now()
	a.mu.Unlock()
	return peers
}

// Queen is the queen's name as her last welcome gave it; empty before the
// first one.
func (a *Announcer) Queen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.queen
}

// SendToQueen delivers a message to the queen.
func (a *Announcer) SendToQueen(ctx context.Context, m HiveMessage) error {
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	return PostMessage(ctx, a.Settings.JoinURL, tok, m)
}

// Report tells the queen about a task this worker gave a peer, so the queen's
// board shows dealings the queen was not part of.
func (a *Announcer) Report(ctx context.Context, t HiveTask) error {
	return a.reportTo(ctx, "/api/hive/task", t)
}

// ReportPulse is Report for the flicker: one pulse of a peer order.
func (a *Announcer) ReportPulse(ctx context.Context, p HivePulse) error {
	return a.reportTo(ctx, "/api/hive/pulse", p)
}

func (a *Announcer) reportTo(ctx context.Context, path string, v any) error {
	if a.client == nil {
		a.client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(v)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.Settings.JoinURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

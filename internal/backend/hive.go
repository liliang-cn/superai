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
// hive_call.go is how one SuperLeo commands another. This is how the commander comes
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
	// $SUPERLEO_ADVERTISE_URL.
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
	// Delegation is how much of the work a queen hands to her workers:
	// "max" (the default for a queen), "auto" or "off". See hive_delegate.go.
	// $SUPERLEO_HIVE_DELEGATION, when set, wins: in a cluster the settings
	// come from a Secret, and turning delegation down should not need one.
	Delegation string `json:"delegation,omitempty"`
	// DelegateTimeoutSeconds bounds one delegated piece of work on one worker
	// before it is given to another (or done by the queen). Zero takes
	// DefaultDelegateTimeout.
	DelegateTimeoutSeconds int `json:"delegate_timeout_seconds,omitempty"`
	// QueenEngine is who thinks for the queen when a person talks to her: ""
	// is SuperLeo's own agent, a driven agent CLI's name ("claude", or
	// "claude.mac" on a linked machine) hands those turns to it. Schedules,
	// calls, dashboards and the hive's own traffic stay with SuperLeo's agent
	// whatever this says. See app_queen_engine.go.
	QueenEngine string `json:"queen_engine,omitempty"`
	// QueenFallback is whether SuperLeo's own agent answers a turn the engine
	// cannot take (not installed, machine offline, failed before saying
	// anything), with a note saying so. Nil is on.
	QueenFallback *bool `json:"queen_fallback,omitempty"`
	// Standalone is the person saying this SuperLeo is not to be a queen:
	// a machine that can think joining it (app_agentdoor.go) does not make
	// it one.
	Standalone bool `json:"standalone,omitempty"`
	// PublicURL is where machines other than this one reach its API, e.g.
	// https://ai.superleo.cn. An engine on a linked machine calls /mcp there;
	// empty takes $SUPERLEO_PUBLIC_URL.
	PublicURL string `json:"public_url,omitempty"`
}

// QueenFallsBack reports whether SuperLeo's own agent stands in for an engine
// that cannot take a turn.
func (h HiveSettings) QueenFallsBack() bool { return h.QueenFallback == nil || *h.QueenFallback }

// Public is PublicURL or $SUPERLEO_PUBLIC_URL, without a trailing slash.
func (h HiveSettings) Public() string {
	u := strings.TrimSpace(h.PublicURL)
	if u == "" {
		u = strings.TrimSpace(os.Getenv("SUPERLEO_PUBLIC_URL"))
	}
	return strings.TrimRight(u, "/")
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
	// Engine says what is behind this worker when it is not a SuperLeo ("cli ·
	// claude"), and About is a line for the roster. Both are only labels.
	Engine string `json:"engine,omitempty"`
	About  string `json:"about,omitempty"`
	// Node is the machine the worker runs on ($SUPERLEO_NODE; in k3s the pod's
	// node), so a client can draw workers where they really are.
	Node string `json:"node,omitempty"`
	// Kind and Host say what the worker runs in (a pod, a VM...) and that
	// box's name; empty from a worker too old to say.
	Kind string `json:"kind,omitempty"`
	Host string `json:"host,omitempty"`
	// MCP names the MCP servers whose tools this worker can call right now, so
	// the queen sends noted work to a worker that has noted. Nil is a worker
	// too old to say, which the queen treats as "may have anything".
	MCP []string `json:"mcp"`
	// CLIs names the coding CLIs installed where the worker runs (claude,
	// codex, ...), so work that needs one goes to a worker that has it.
	CLIs []string `json:"clis,omitempty"`
	// Term is the latest queen's term this worker has followed
	// (hive_election.go): a queen on an earlier one is stale.
	Term int64 `json:"term,omitempty"`
}

// HiveWelcome is the queen's answer.
type HiveWelcome struct {
	Protocol   string `json:"protocol"`
	Queen      string `json:"queen"`
	IntervalMS int    `json:"interval_ms"`
	Members    int    `json:"members"`
	// Term is the queen's, and Roster the live workers: what every worker
	// needs to pick the same next queen should this one go quiet.
	Term   int64         `json:"term,omitempty"`
	Roster []RosterEntry `json:"roster,omitempty"`
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
	Kind      string    `json:"kind,omitempty"`
	Host      string    `json:"host,omitempty"`
	// MCP is what the worker last said it can reach (HiveHello.MCP), and
	// CLIs the coding CLIs installed where it runs.
	MCP   []string `json:"mcp"`
	CLIs  []string `json:"clis,omitempty"`
	about string
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
	// term is this queen's (hive_election.go).
	term int64
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
	if hello.Term > h.term {
		// The worker has followed a later queen: this one is left over.
		return HiveWelcome{}, ErrSuperseded{Term: hello.Term}
	}
	if at, ok := h.retired[hello.Name]; ok {
		if hello.StartedAt.Equal(at) {
			return HiveWelcome{Protocol: HiveProtocol, Queen: h.name, IntervalMS: int(h.interval / time.Millisecond), Members: len(h.members), Term: h.term}, nil
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
	m.Kind, m.Host = hello.Kind, hello.Host
	m.CLIs = append([]string(nil), hello.CLIs...)
	m.MCP = nil
	if hello.MCP != nil {
		m.MCP = append([]string{}, hello.MCP...)
	}
	return HiveWelcome{
		Protocol: HiveProtocol, Queen: h.name,
		IntervalMS: int(h.interval / time.Millisecond), Members: len(h.members),
		Term: h.term, Roster: h.rosterLocked(now),
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
	// Engine and About label a worker that is not a SuperLeo. See HiveHello.
	Engine string
	About  string
	// Capabilities, when set, names the MCP servers this worker can call,
	// asked again for every hello so a server that came up later is offered.
	Capabilities func() []string
	// CLIs, when set, names the coding CLIs installed here.
	CLIs func() []string
	// OnPromote, when set, is called when this worker is elected queen
	// (hive_election.go) with the term it now holds; Run returns after it.
	// Unset, the worker only waits for its queen to come back.
	OnPromote func(term int64)

	started time.Time
	client  *http.Client

	mu      sync.Mutex
	joined  bool
	lastOK  time.Time
	lastErr string
	peers   map[string]RemoteAgent
	peersAt time.Time
	queen   string
	// What an election needs (hive_election.go): the term followed, the
	// roster the queen last gave, and the queen being followed when she is
	// not the one Settings.JoinURL names.
	term   int64
	roster []RosterEntry
	target string
	// retry is the first wait after a missed beat; zero is a second. A test
	// sets it shorter.
	retry time.Duration
	// holdUntil is when a queen who said she was restarting (Hold) is given
	// up on; until then her silence elects nobody.
	holdUntil time.Time
}

// Hold is the queen saying she is restarting: wait d for her before electing
// another.
func (a *Announcer) Hold(d time.Duration) {
	a.mu.Lock()
	a.holdUntil = time.Now().Add(d)
	a.mu.Unlock()
	log.Printf("hive: the queen is restarting; waiting up to %s for her", d.Round(time.Second))
}

func (a *Announcer) holding() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.holdUntil)
}

// joinURL is where the queen being followed is reached.
func (a *Announcer) joinURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.target != "" {
		return a.target
	}
	return a.Settings.JoinURL
}

// Term is the latest queen's term this worker has followed.
func (a *Announcer) Term() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.term
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
	return strings.TrimSpace(os.Getenv("SUPERLEO_ADVERTISE_URL"))
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
		return errors.New("no advertise url: set hive.advertise_url or $SUPERLEO_ADVERTISE_URL to where the queen can reach this worker")
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
	host := DetectHost()
	hello := HiveHello{
		Protocol: HiveProtocol, Name: a.Name(), Role: HiveRoleWorker, URL: a.advertise(),
		Token: a.Token, Version: a.Version, StartedAt: a.started, Engine: a.Engine, About: a.About,
		Node: os.Getenv("SUPERLEO_NODE"), Kind: host.Kind, Host: host.Name, Term: a.Term(),
	}
	if a.Capabilities != nil {
		// Never nil once asked: an empty list is "none", which is not the
		// same as a worker that does not say.
		hello.MCP = append([]string{}, a.Capabilities()...)
	}
	if a.CLIs != nil {
		hello.CLIs = a.CLIs()
	}
	body, _ := json.Marshal(hello)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.joinURL(), "/")+"/api/hive/join", bytes.NewReader(body))
	if err != nil {
		return HiveWelcome{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	// A beat is small and a queen answers it at once: one that has not in a
	// few seconds is not answering, and waiting longer only delays noticing.
	beat, cancel := context.WithTimeout(ctx, beatTimeout)
	defer cancel()
	resp, err := a.client.Do(req.WithContext(beat))
	if err != nil {
		return HiveWelcome{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		// Up, but not a queen (yet): a member just elected that has not
		// taken over.
		return HiveWelcome{}, fmt.Errorf("%w: %s", errNotQueen, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusOK {
		return HiveWelcome{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var w HiveWelcome
	if err := json.Unmarshal(raw, &w); err != nil || w.Protocol != HiveProtocol {
		return HiveWelcome{}, fmt.Errorf("that is not a %s queen: %s", HiveProtocol, strings.TrimSpace(string(raw)))
	}
	a.mu.Lock()
	if w.Queen != "" {
		a.queen = w.Queen
	}
	if w.Term > a.term {
		a.term = w.Term
	}
	if w.Roster != nil {
		a.roster = w.Roster
	}
	a.mu.Unlock()
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.joinURL(), "/")+"/api/hive/leave", bytes.NewReader(body))
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
	first := a.retry
	if first <= 0 {
		first = time.Second
	}
	wait := a.Settings.Interval()
	backoff := first
	joined := false
	// misses counts the beats in a row the queen has not answered, and skip
	// who has been passed over since the last queen that did.
	misses, skip := 0, map[string]bool{}
	for {
		w, err := a.Once(ctx)
		a.record(err)
		switch {
		case err == nil:
			if !joined {
				log.Printf("hive: joined %q as %s", w.Queen, a.Name())
				joined = true
			}
			misses, skip = 0, map[string]bool{}
			backoff = first
			wait = a.Settings.Interval()
			if w.IntervalMS > 0 {
				wait = time.Duration(w.IntervalMS) * time.Millisecond
			}
		default:
			if joined {
				log.Printf("hive: lost the queen: %v", err)
			} else if backoff == first {
				log.Printf("hive: cannot join yet: %v", err)
			}
			joined = false
			wait = backoff
			if backoff < 30*time.Second {
				backoff *= 2
			}
			misses++
			// A member that is up but not queen yet is given longer: it is
			// most likely the one just picked, taking over.
			limit := hiveMisses
			if errors.Is(err, errNotQueen) {
				limit = 3 * hiveMisses
				// The queen followed by her own address has stepped down
				// since: whoever is queen now is at the address that always
				// leads to her, not a reason to elect again.
				if a.unpin() {
					misses, backoff, wait = 0, first, first
				}
			}
			if a.OnPromote != nil && misses >= limit && !a.holding() {
				misses = 0
				if a.elect(skip) {
					return
				}
				backoff, wait = first, first
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

func (h *Hive) rosterLocked(now time.Time) []RosterEntry {
	out := []RosterEntry{}
	for _, m := range h.members {
		if h.stateLocked(m, now) == "live" {
			out = append(out, RosterEntry{Name: m.Name, URL: m.URL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.joinURL(), "/")+"/api/hive/roster", nil)
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
	return PostMessage(ctx, a.joinURL(), tok, m)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.joinURL(), "/")+path, bytes.NewReader(body))
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

// errNotQueen is a member answering a join that it is not a queen.
var errNotQueen = errors.New("not a queen")

// beatTimeout bounds one beat.
const beatTimeout = 3 * time.Second

// unpin goes back from following a queen by her own address (set by an
// election) to Settings.JoinURL. False when not pinned, or when JoinURL is
// that same address.
func (a *Announcer) unpin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.target == "" || a.target == a.Settings.JoinURL {
		return false
	}
	log.Printf("hive: %s is not queen any more; following %s", a.queen, a.Settings.JoinURL)
	a.target = ""
	return true
}

// elect passes over the queen that went quiet and follows the next one:
// term's pick from the last roster. True when the pick is this worker, which
// has then become queen (OnPromote).
func (a *Announcer) elect(skip map[string]bool) bool {
	a.mu.Lock()
	roster, term, gone := a.roster, a.term, a.queen
	a.mu.Unlock()
	if len(roster) == 0 {
		return false // nobody to pick from yet: wait for the queen
	}
	if gone != "" {
		skip[gone] = true
	}
	term++
	pick, ok := ElectQueen(roster, term, skip)
	if !ok {
		return false
	}
	self := pick.Name == a.Name()
	a.mu.Lock()
	a.term, a.queen = term, pick.Name
	if !self {
		a.target = pick.URL
	}
	a.mu.Unlock()
	if self {
		log.Printf("hive: %q is not answering; this member is queen now (term %d)", gone, term)
		a.OnPromote(term)
		return true
	}
	log.Printf("hive: %q is not answering; %s is queen now (term %d)", gone, pick.Name, term)
	return false
}

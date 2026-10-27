package app

// A hive on a local network: machines joined to a SuperAI over the agent
// link (app_agentdoor.go), and a queen among them. When she goes, the ones
// that can think pick another among themselves, the way a hive's pods do
// (backend/hive_election.go), and follow her.
//
// What they need for it reaches them from the queen every few seconds, as a
// call over the link (HiveRoster): who of them can think and where each
// would take agents in if it were queen, her term, and the hive's key — a
// credential every queen of this hive accepts, so the members can join the
// next one without pairing again.
//
// A member that cannot reach its queen for lanMisses tries in a row draws
// the next one from the last roster: itself, and it opens its door and takes
// the queen's part; another, and it dials that one with the key. A queen
// that comes back asks the members' doors who is queen now and, if the hive
// has moved on to a later term, steps down and joins the one in charge.
//
// The pods' hive on k3s elects through its own announcements and a label
// (app_election.go); none of this runs there.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

const (
	// lanRosterEvery is how often a queen tells her members the roster.
	lanRosterEvery = 10 * time.Second
	// lanMisses is how many tries in a row a member gives its queen.
	lanMisses = 3
	// lanProbeEvery is how often a queen asks whether the hive moved on.
	lanProbeEvery = 30 * time.Second
	// hiveTermPath answers who is queen, on a door's claim port.
	hiveTermPath = "/api/hive/term"
)

// LanMember is one member that could be queen, and where it would take
// agents in if it were.
type LanMember struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

// LanRoster is what a queen tells her members.
type LanRoster struct {
	Queen   string      `json:"queen"`
	Term    int64       `json:"term"`
	Key     string      `json:"key"`
	Members []LanMember `json:"members"`
}

// lanHive is a member's last word from its queen.
type lanHive struct {
	mu     sync.Mutex
	roster LanRoster
}

func lanRosterPath() string { return filepath.Join(backend.DataDir(), "lan-hive.json") }
func hiveKeyPath() string   { return filepath.Join(backend.DataDir(), "hive-key") }

// hiveKey is this hive's key, "" when this SuperAI is in none.
func hiveKey() string {
	raw, err := os.ReadFile(hiveKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ensureHiveKey is the key, minted the first time a queen needs one.
func ensureHiveKey() string {
	if k := hiveKey(); k != "" {
		return k
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	k := hex.EncodeToString(b)
	if err := os.WriteFile(hiveKeyPath(), []byte(k+"\n"), 0o600); err != nil {
		log.Printf("hive: could not keep the hive key: %v", err)
	}
	return k
}

// hiveKeyMatches is whether tok is this hive's key.
func hiveKeyMatches(tok string) bool {
	k := hiveKey()
	return k != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(k)) == 1
}

// doorPorts are the claim and link ports of this app's door:
// $SUPERAI_AGENT_DOOR_PORT for the link (the claim one below it), else the
// desktop app's.
func doorPorts() (claim, link int) {
	if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("SUPERAI_AGENT_DOOR_PORT"))); err == nil && p > 1 {
		return p - 1, p
	}
	return desktopClaimPort, desktopAgentPort
}

// --- the queen's side ---

// tellLanRoster is a queen's round: the roster to every member that can
// think, which answers with the port it would open as queen.
func (a *App) tellLanRoster(ctx context.Context) {
	a.mu.Lock()
	h, s := a.hive, a.settings
	a.mu.Unlock()
	if h == nil || s == nil {
		return
	}
	var thinkers []*agentConn
	for _, c := range a.agents().list() {
		if c.hello.GetThinks() {
			thinkers = append(thinkers, c)
		}
	}
	if len(thinkers) == 0 {
		return
	}
	r := LanRoster{Queen: s.Hive.Self(), Term: h.Term(), Key: ensureHiveKey()}
	for _, c := range thinkers {
		if p := c.doorPort.Load(); p > 0 && c.addr != "" {
			r.Members = append(r.Members, LanMember{Name: c.name(), Link: net.JoinHostPort(c.addr, strconv.Itoa(int(p)))})
		}
	}
	// Kept here too: a queen back from away asks these doors whether the
	// hive moved on without her.
	if len(r.Members) > 0 {
		a.lanHive.mu.Lock()
		a.lanHive.roster = r
		a.lanHive.mu.Unlock()
		if raw, err := json.Marshal(r); err == nil {
			_ = os.WriteFile(lanRosterPath(), raw, 0o600)
		}
	}
	for _, c := range thinkers {
		var got struct {
			DoorPort int `json:"door_port"`
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.call(cctx, "HiveRoster", []any{r}, &got)
		cancel()
		if err == nil && got.DoorPort > 0 {
			c.doorPort.Store(int32(got.DoorPort))
		}
	}
}

// keepLanHive is a queen's two watches while she is queen of a hive on a
// network: the roster to her members, and whether the hive moved on.
func (a *App) keepLanHive(ctx context.Context) {
	roster := time.NewTicker(lanRosterEvery)
	probe := time.NewTicker(lanProbeEvery)
	defer roster.Stop()
	defer probe.Stop()
	a.tellLanRoster(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-roster.C:
			a.tellLanRoster(ctx)
		case <-probe.C:
			a.askWhoIsQueen(ctx)
		}
	}
}

// askWhoIsQueen is a queen back from away asking the members of her last
// roster who is queen now, and stepping down to one of a later term.
func (a *App) askWhoIsQueen(ctx context.Context) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	key := hiveKey()
	if h == nil || key == "" {
		return
	}
	last := a.lanRoster()
	for _, m := range last.Members {
		host, port, err := net.SplitHostPort(m.Link)
		if err != nil {
			continue
		}
		p, _ := strconv.Atoi(port)
		u := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(p-1)), hiveTermPath)
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := cliRemoteHTTP.Do(req)
		cancel()
		if err != nil {
			continue
		}
		var got struct {
			Queen string `json:"queen"`
			Term  int64  `json:"term"`
			Link  string `json:"link"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if got.Queen != "" && got.Term > h.Term() {
			a.stepDownToLanQueen(got.Queen, got.Term, m.Link)
			return
		}
	}
}

// handleHiveTerm answers who is queen and on what term, for a queen who
// was away; only to the hive's key.
func (a *App) handleHiveTerm(w http.ResponseWriter, r *http.Request) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !hiveKeyMatches(tok) {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	a.mu.Lock()
	h, s := a.hive, a.settings
	a.mu.Unlock()
	if h == nil || s == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "not a queen"})
		return
	}
	writeJSON(w, map[string]any{"queen": s.Hive.Self(), "term": h.Term()})
}

// stepDownToLanQueen makes a queen the hive has moved on from a member of
// the queen in charge, reached at link with the hive's key.
func (a *App) stepDownToLanQueen(queen string, term int64, link string) {
	a.mu.Lock()
	var next backend.Settings
	ok := a.settings != nil
	if ok {
		next = *a.settings
	}
	a.mu.Unlock()
	if !ok || next.Hive.Role != backend.HiveRoleQueen {
		return
	}
	log.Printf("hive: %s is queen on term %d; this one steps down and joins her", queen, term)
	next.Hive.Role = ""
	if err := a.SaveSettings(next); err != nil {
		log.Printf("hive: could not step down: %v", err)
		return
	}
	go a.RunAgentLink(context.Background(), AgentLinkOptions{Core: link, Token: hiveKey(), Name: next.Hive.Self()})
}

// --- a member's side ---

// HiveRoster is a member hearing its queen's roster. It keeps it, and the
// key with it, and answers with the port it would take agents in on.
func (a *App) HiveRoster(r LanRoster) (map[string]any, error) {
	a.lanHive.mu.Lock()
	a.lanHive.roster = r
	a.lanHive.mu.Unlock()
	if raw, err := json.Marshal(r); err == nil {
		_ = os.WriteFile(lanRosterPath(), raw, 0o600)
	}
	if r.Key != "" && hiveKey() != r.Key {
		_ = os.WriteFile(hiveKeyPath(), []byte(r.Key+"\n"), 0o600)
	}
	_, link := doorPorts()
	return map[string]any{"door_port": link}, nil
}

// lanRoster is the last roster heard, from memory or, after a restart, disk.
func (a *App) lanRoster() LanRoster {
	a.lanHive.mu.Lock()
	r := a.lanHive.roster
	a.lanHive.mu.Unlock()
	if r.Queen != "" || len(r.Members) > 0 {
		return r
	}
	if raw, err := os.ReadFile(lanRosterPath()); err == nil {
		_ = json.Unmarshal(raw, &r)
	}
	return r
}

// lanElect is a member that cannot reach its queen drawing the next one
// from the last roster, passing over who is in skip. self means it is this
// member, which has taken over; otherwise next is the queen to dial.
func (a *App) lanElect(name string, skip map[string]bool) (next LanMember, self, ok bool) {
	r := a.lanRoster()
	if len(r.Members) == 0 || r.Key == "" || !a.hasModel() {
		return LanMember{}, false, false
	}
	skip[r.Queen] = true
	term := r.Term + 1
	var entries []backend.RosterEntry
	for _, m := range r.Members {
		entries = append(entries, backend.RosterEntry{Name: m.Name, URL: m.Link})
	}
	pick, found := backend.ElectQueen(entries, term, skip)
	if !found {
		return LanMember{}, false, false
	}
	next = LanMember{Name: pick.Name, Link: pick.URL}
	r.Queen, r.Term = pick.Name, term
	a.lanHive.mu.Lock()
	a.lanHive.roster = r
	a.lanHive.mu.Unlock()
	if pick.Name == name {
		log.Printf("hive: the queen is not answering; this member is queen now (term %d)", term)
		a.becomeLanQueen(term)
		return next, true, true
	}
	log.Printf("hive: the queen is not answering; %s is queen now (term %d)", pick.Name, term)
	return next, false, true
}

// becomeLanQueen makes this member the hive's queen, under term: its door
// open, and the queen's part in the settings.
func (a *App) becomeLanQueen(term int64) {
	if claim, link, _ := a.agentDoor.get(); claim == 0 || link == 0 {
		a.openAgentDoor(context.Background())
	}
	a.mu.Lock()
	var next backend.Settings
	ok := a.settings != nil
	if ok {
		next = *a.settings
	}
	a.electedTerm = term
	a.mu.Unlock()
	saveTerm(term)
	if !ok {
		return
	}
	next.Hive.Role = backend.HiveRoleQueen
	next.Hive.Standalone = false
	if err := a.SaveSettings(next); err != nil {
		log.Printf("hive: could not take over as queen: %v", err)
		return
	}
	a.emit("hosts:changed", nil)
}

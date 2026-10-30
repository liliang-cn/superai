package app

// What a machine can do, beyond running agents on it.
//
// Every machine runs one SuperLeo agent, and what that agent can reach differs
// by machine: a Mac has desktop apps and the person's Chrome, a pod has
// neither. The agent says which in its Hello (abilities), core keeps it with
// the agent (the topology, the roster, linked_agents), and the queen reads it
// to decide where a piece of work goes: whoever has the ability is handed the
// whole task and does it with its own local tools.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// The abilities a machine can name. A new one is just a new name here.
const (
	abilityDesktop = "desktop"
	abilityBrowser = "browser"
	abilityFiles   = "files"
	abilityExec    = "exec"
)

// HostAbility is one thing a machine's SuperLeo can do, with how it stands.
type HostAbility struct {
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
}

// localAbilities is what this process can do right now: only what is on and
// ready, since a queen who is told "desktop" will hand a task to it.
func (a *App) localAbilities() []HostAbility {
	a.mu.Lock()
	svc, s := a.svc, a.settings
	a.mu.Unlock()
	if svc == nil || s == nil {
		return nil
	}
	var out []HostAbility

	if d := svc.DesktopStatus(); d.Enabled && d.Ready && (runtime.GOOS != "darwin" || d.Accessibility == "granted") {
		about := "operate this machine's desktop apps"
		if blocked, _ := backend.DesktopBlockedApps(s); len(blocked) > 0 {
			if len(blocked) > 10 {
				blocked = blocked[:10]
			}
			about += "; leaves alone " + strings.Join(blocked, ", ")
		}
		out = append(out, HostAbility{abilityDesktop, about})
	}
	if b := svc.BrowserStatus(); b.Enabled && b.Ready {
		about := "a browser it drives"
		if b.Mode == backend.BrowserModeChrome {
			about = "the person's own Chrome, signed-in sites included"
		}
		out = append(out, HostAbility{abilityBrowser, about})
	}
	if dir := strings.TrimSpace(s.WorkspaceDir); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			out = append(out, HostAbility{abilityFiles, "read and write files on this machine, workspace " + filepath.ToSlash(dir)})
		}
	}
	if hostControl() {
		out = append(out, HostAbility{abilityExec, "run shell commands on this machine (" + runtime.GOOS + ")"})
	}
	return out
}

// helloAbilities is localAbilities as the Hello carries it.
func (a *App) helloAbilities() []*agentlinkpb.Ability {
	var out []*agentlinkpb.Ability
	for _, ab := range a.localAbilities() {
		out = append(out, &agentlinkpb.Ability{Name: ab.Name, About: ab.About})
	}
	return out
}

// abilitiesOfMember reads the abilities a roster worker (a pod) said in its hello.
func abilitiesOfMember(m backend.HiveMember) []HostAbility {
	var out []HostAbility
	for _, ab := range m.Abilities {
		out = append(out, HostAbility{ab.Name, ab.About})
	}
	return out
}

// abilitiesFrom reads a Hello's abilities.
func abilitiesFrom(h *agentlinkpb.Hello) []HostAbility {
	var out []HostAbility
	for _, ab := range h.GetAbilities() {
		out = append(out, HostAbility{ab.GetName(), ab.GetAbout()})
	}
	return out
}

// helloAbilitiesKey is what a changed ability looks like to the minute check.
func helloAbilitiesKey(h *agentlinkpb.Hello) string {
	var b strings.Builder
	for _, ab := range h.GetAbilities() {
		fmt.Fprintf(&b, "%s=%s;", ab.GetName(), ab.GetAbout())
	}
	return b.String()
}

// abilityRoster says which machine's SuperLeo can do what, for the queen's
// tool descriptions: "mac-liang (host liang-mac): desktop, browser, files".
// Abilities are of a machine, so "read a file on my Mac" goes to the Mac's
// worker even though the queen has files of her own. Empty when no live
// worker has said anything.
func (a *App) abilityRoster() string {
	var parts []string
	for _, w := range a.HiveWorkers() {
		if w.Kind != hostAgentSuperLeo || !w.Up || len(w.Abilities) == 0 {
			continue
		}
		names := make([]string, 0, len(w.Abilities))
		for _, ab := range w.Abilities {
			names = append(names, ab.Name)
		}
		sort.Strings(names)
		parts = append(parts, fmt.Sprintf("%s (machine %s): %s", w.Name, w.Host, strings.Join(names, ", ")))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// kitRoster is, by live worker, the MCP servers and skills it has that not
// every live worker has — "w1: mcp sqlite; skills pdf" — for the queen to send
// a task that needs one where it is. Empty when they all have the same.
func (a *App) kitRoster() string {
	ws := a.liveWorkers()
	if len(ws) == 0 {
		return ""
	}
	count := map[string]int{}
	for _, w := range ws {
		for _, n := range slices.Compact(slices.Sorted(slices.Values(append(append([]string{}, w.MCP...), w.Skills...)))) {
			count[n]++
		}
	}
	var parts []string
	for _, w := range ws {
		var mcp, skills []string
		for _, n := range w.MCP {
			if count[n] < len(ws) {
				mcp = append(mcp, n+toolList(w.MCPTools[n]))
			}
		}
		for _, n := range w.Skills {
			if count[n] < len(ws) {
				skills = append(skills, n)
			}
		}
		if len(mcp)+len(skills) == 0 {
			continue
		}
		var have []string
		if len(mcp) > 0 {
			sort.Strings(mcp)
			have = append(have, "mcp "+strings.Join(mcp, " "))
		}
		if len(skills) > 0 {
			sort.Strings(skills)
			have = append(have, "skills "+strings.Join(skills, " "))
		}
		parts = append(parts, w.Name+": "+strings.Join(have, ", "))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// toolList is " (tools a, b, …)" for a server's tools, the first dozen.
func toolList(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	t := slices.Sorted(slices.Values(tools))
	more := ""
	if len(t) > 12 {
		t, more = t[:12], ", …"
	}
	return " (tools " + strings.Join(t, ", ") + more + ")"
}

// workerKitNames is every MCP server and skill a live worker has.
func (a *App) workerKitNames() []string {
	var out []string
	for _, w := range a.liveWorkers() {
		out = append(append(out, w.MCP...), w.Skills...)
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}

// abilityLine is the names of abs, sorted, for a line of text.
func abilityLine(abs []HostAbility) []string {
	out := make([]string, 0, len(abs))
	for _, ab := range abs {
		out = append(out, ab.Name)
	}
	sort.Strings(out)
	return out
}

// abilityCache keeps localAbilities for a few seconds: asking the desktop for
// its permission is a process call, and a run asks on every turn.
type abilityCache struct {
	mu  sync.Mutex
	at  time.Time
	val []HostAbility
}

func (a *App) cachedAbilities() []HostAbility {
	c := &a.abilitiesCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > 15*time.Second {
		c.val, c.at = a.localAbilities(), time.Now()
	}
	return c.val
}

// unavailableTools are the registry's tools that need an ability this machine
// does not have right now: desktop control whose permission is not granted.
// A queen in a pod or on a Mac without it never sees them; the task goes to
// the worker that has the ability (hive_delegate says which).
func (a *App) unavailableTools(registry []string) []string {
	var out []string
	var have []HostAbility
	loaded := false
	for _, n := range registry {
		if !strings.HasPrefix(n, "desktop_") {
			continue
		}
		if !loaded {
			have, loaded = a.cachedAbilities(), true
		}
		if !hasAbility(have, abilityDesktop) {
			out = append(out, n)
		}
	}
	return out
}

func hasAbility(abs []HostAbility, name string) bool {
	for _, ab := range abs {
		if ab.Name == name {
			return true
		}
	}
	return false
}

// abilityNamesOf are the ability names a Hello carries.
func abilityNamesOf(h *agentlinkpb.Hello) []string {
	var out []string
	for _, ab := range h.GetAbilities() {
		out = append(out, ab.GetName())
	}
	return out
}

// machineAbilities are the names a need may be: what a worker's machine can do.
var machineAbilities = []string{abilityDesktop, abilityBrowser, abilityFiles, abilityExec}

// unmetAbilities are the machine abilities among needs that this queen does not
// have herself, so a piece with them can only be done by a worker that has them.
func (a *App) unmetAbilities(needs []string) []string {
	have := a.cachedAbilities()
	var out []string
	for _, n := range needs {
		if slices.Contains(machineAbilities, n) && !hasAbility(have, n) {
			out = append(out, n)
		}
	}
	return out
}

func noneIfEmpty(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

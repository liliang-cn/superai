package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/internal/backend"
)

// The hive, from this instance's side.
//
// A supreme ring keeps the roster and commands what is on it. A ring runs a
// loop that keeps announcing itself to the supreme. Which of the two an
// instance is comes from its settings; with neither it is standalone and none
// of this runs. The wire format and the reasoning are in backend/hive.go.

// hiveAgents is the roster as the runner sees it.
func (a *App) hiveAgents() map[string]backend.RemoteAgent {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.Agents()
}

// HiveMembers is the roster, for the UI and for anything that wants to know
// who is in the hive. Empty on an instance that is not a supreme.
func (a *App) HiveMembers() []backend.HiveMember {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		return []backend.HiveMember{}
	}
	return h.Members()
}

// handleHiveJoin is POST /api/hive/join: a ring saying who it is. It sits
// behind the ordinary credential gate — a stranger cannot put itself on the
// roster — and answers 404 on anything but a supreme, so a ring does not
// pretend to be one.
func (a *App) handleHiveJoin(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a supreme ring"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var hello backend.HiveHello
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&hello); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unreadable hello: " + err.Error()})
		return
	}
	welcome, err := h.Join(hello)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, welcome)
}

// startHive brings up this instance's part: a roster for a supreme, an
// announcing loop for a ring. Restarted on every settings save, because
// changing the role or the join address is a settings change.
func (a *App) startHive() {
	a.stopHive()

	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil || s.Hive.Role != backend.HiveRoleRing {
		return
	}

	creds, err := loadOrCreateCredentials()
	if err != nil {
		log.Printf("hive: not joining: %v", err)
		return
	}
	ann := &backend.Announcer{Settings: s.Hive, Token: creds.Token}
	if err := ann.Validate(); err != nil {
		log.Printf("hive: not joining: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.hiveStop = cancel
	a.mu.Unlock()
	go ann.Run(ctx)
}

func (a *App) stopHive() {
	a.mu.Lock()
	stop := a.hiveStop
	a.hiveStop = nil
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// registerHiveTools gives a supreme ring its command. Called from the build,
// which already holds a.mu, so it takes nothing — the roster is created here,
// once, and the tools read it through the runner at call time.
//
// Present for a supreme, and for anyone whose settings name a ring by URL.
// Absent otherwise, which is what stops a worker commanding anyone: its
// settings list none and it never gets a roster.
func (a *App) registerHiveTools(svc *backend.Service, cfg *backend.Settings) {
	h := cfg.Hive
	if svc == nil {
		return
	}
	if h.Role == backend.HiveRoleSupreme && a.hive == nil {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name, _ = os.Hostname()
		}
		a.hive = backend.NewHive(name, h.Interval())
	}
	if h.Role != backend.HiveRoleSupreme {
		// Not a supreme any more (or never): drop a stale roster so a demoted
		// instance does not go on holding other rings' credentials.
		a.hive = nil
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}
	static := false
	for _, ag := range cfg.RemoteAgents.Agents {
		if ag.URL != "" {
			static = true
		}
	}
	if a.hive == nil && !static {
		return
	}

	inner.AddToolWithMetadata("hive_members",
		"Who is in the hive right now: every ring that has joined, whether it is live or lost, and where it is."+
			" Look here before commanding, because a ring that is lost will not answer.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			b, err := json.Marshal(a.rosterView())
			return string(b), err
		},
		agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true})

	inner.AddToolWithMetadata("ring_command",
		"Command the other rings — SuperAI instances in this hive — to work in parallel and collect what each reports back."+
			"\n\nYou are the supreme ring: they act on your word, they share your memory, and they cannot command you."+
			" The rings are whoever has joined; call hive_members to see them. Give every live ring the same order with"+
			" `prompt`, or split the work with `commands`, one entry per ring. Each ring gets a fresh conversation and"+
			" has no idea what the others were told, so every order must stand alone. What comes back is one report per"+
			" ring, and a ring that failed or was lost says so — read them all before you conclude anything.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "One order sent to every live ring (or to `rings`, if given)."},
				"rings": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "Limit `prompt` to these rings. Omit for all live rings.",
				},
				"commands": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"ring":   map[string]any{"type": "string"},
							"prompt": map[string]any{"type": "string"},
						},
						"required": []string{"ring", "prompt"},
					},
					"description": "A different order per ring. Use instead of `prompt`.",
				},
			},
		},
		a.ringCommand,
		agent.ToolMetadata{Destructive: true})
}

// rosterView is hive members plus statically configured rings, for the model.
func (a *App) rosterView() []map[string]any {
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, m := range a.HiveMembers() {
		seen[m.Name] = true
		out = append(out, map[string]any{
			"name": m.Name, "state": m.State, "url": m.URL,
			"last_seen_seconds_ago": int(time.Since(m.LastSeen).Seconds()),
		})
	}
	for n, ag := range a.remoteRunner().Rings() {
		if !seen[n] {
			out = append(out, map[string]any{"name": n, "state": "configured", "url": ag.URL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

// ringCommand fans an order out to rings in parallel.
func (a *App) ringCommand(ctx context.Context, args map[string]any) (any, error) {
	runner := a.remoteRunner()
	live := runner.Rings()
	names := make([]string, 0, len(live))
	for n := range live {
		names = append(names, n)
	}
	sort.Strings(names)

	type order struct{ ring, prompt string }
	var orders []order
	if cs, ok := args["commands"].([]any); ok && len(cs) > 0 {
		for _, c := range cs {
			m, _ := c.(map[string]any)
			orders = append(orders, order{strings.TrimSpace(str(m["ring"])), str(m["prompt"])})
		}
	} else if p := strings.TrimSpace(str(args["prompt"])); p != "" {
		targets := names
		if rs, ok := args["rings"].([]any); ok && len(rs) > 0 {
			targets = nil
			for _, r := range rs {
				targets = append(targets, strings.TrimSpace(str(r)))
			}
		}
		for _, r := range targets {
			orders = append(orders, order{r, p})
		}
	}
	if len(orders) == 0 {
		if len(names) == 0 {
			return "There are no live rings in the hive right now. Call hive_members to see who has joined and who is lost.", nil
		}
		return nil, fmt.Errorf("give either prompt or commands")
	}

	// Explains a name that is not commandable in the terms the roster uses: a
	// ring that was lost is not the same problem as one that never existed.
	why := func(name string) string {
		for _, m := range a.HiveMembers() {
			if m.Name == name {
				return fmt.Sprintf("%s is %s (last heard %ds ago) and cannot be commanded until it is back",
					name, m.State, int(time.Since(m.LastSeen).Seconds()))
			}
		}
		return fmt.Sprintf("there is no ring called %q; live rings: %s", name, strings.Join(names, ", "))
	}

	out := make([]string, len(orders))
	var wg sync.WaitGroup
	for i, o := range orders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := live[o.ring]; !ok {
				out[i] = fmt.Sprintf("## %s\nFAILED: %s", o.ring, why(o.ring))
				return
			}
			res, err := runner.Run(ctx, o.ring, o.prompt)
			switch {
			case err != nil:
				out[i] = fmt.Sprintf("## %s\nFAILED: %v", o.ring, err)
			case res.Failed:
				out[i] = fmt.Sprintf("## %s\nFAILED (%s) after %.1fs. What came back, if anything:\n%s",
					o.ring, res.Reason, float64(res.MS)/1000, res.Text)
			default:
				out[i] = fmt.Sprintf("## %s (%.1fs)\n%s", o.ring, float64(res.MS)/1000, res.Text)
			}
		}()
	}
	wg.Wait()
	return strings.Join(out, "\n\n"), nil
}

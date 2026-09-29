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
// A queen keeps the roster and commands what is on it. A worker runs a
// loop that keeps announcing itself to the queen. Which of the two an
// instance is comes from its settings; with neither it is standalone and none
// of this runs. The wire format and the reasoning are in backend/hive.go.

// tasks is the board, built on first use. Each change goes out as a hive:task
// event on the same stream chat uses, so a browser that is already listening
// gets it with no extra connection.
func (a *App) tasks() *backend.TaskBoard {
	a.hiveBoardOnce.Do(func() {
		a.hiveBoard = backend.NewTaskBoard(func(t backend.HiveTask) {
			raw, err := json.Marshal(t)
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				a.emit("hive:task", m)
			}
			if t.Dir == backend.TaskPeer {
				a.reportToQueen(t)
			}
		})
		a.hiveBoard.SetPulse(func(p backend.HivePulse) {
			raw, err := json.Marshal(p)
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				a.emit("hive:pulse", m)
			}
			if p.Dir == backend.TaskPeer {
				a.reportToQueen(p)
			}
		})
	})
	return a.hiveBoard
}

// reportToQueen queues a peer task for the queen. Best effort by design: the
// queen's picture of the hive is a view, and a worker doing its job must never
// wait on it.
func (a *App) reportToQueen(v any) {
	a.hiveReportOnce.Do(func() {
		a.hiveReports = make(chan any, 256)
		go func() {
			for v := range a.hiveReports {
				a.mu.Lock()
				ann := a.hiveAnn
				a.mu.Unlock()
				if ann == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				var err error
				switch x := v.(type) {
				case backend.HiveTask:
					err = ann.Report(ctx, x)
				case backend.HivePulse:
					err = ann.ReportPulse(ctx, x)
				}
				if err != nil {
					log.Printf("hive: could not report to the queen: %v", err)
				}
				cancel()
			}
		}()
	})
	select {
	case a.hiveReports <- v:
	default:
		// A pulse is a flicker; losing one is nothing. A task update lost here is
		// repaired by the next, which carries the whole task.
	}
}

// hiveName is what this instance is called in the hive.
func (a *App) hiveName() string {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return a.hiveNameOf(s)
}

func (a *App) hiveNameOf(s *backend.Settings) string {
	if s != nil {
		if n := strings.TrimSpace(s.Hive.Name); n != "" {
			return n
		}
	}
	n, _ := os.Hostname()
	return n
}

// hiveAgents is the roster as the runner sees it.
func (a *App) hiveAgents() map[string]backend.RemoteAgent {
	a.mu.Lock()
	h, ann := a.hive, a.hiveAnn
	a.mu.Unlock()
	if h != nil {
		return h.Agents()
	}
	// A worker's roster is its peers, as the queen lists them.
	if ann != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		return ann.Peers(ctx)
	}
	return nil
}

// HiveTaskDetail is one order by its UUID, whole: the full prompt, the full
// answer, and every step it went through. Only the last few orders are kept, in
// memory, so an old id — or one from before a restart — comes back not found.
func (a *App) HiveTaskDetail(id string) map[string]any {
	t, ok := a.tasks().Get(strings.TrimSpace(id))
	if !ok {
		return map[string]any{"ok": false, "error": "no such task here: only recent orders are kept, and not across a restart"}
	}
	return map[string]any{"ok": true, "task": t}
}

// HiveMembers is the roster, for the UI and for anything that wants to know
// who is in the hive. Empty on an instance that is not a queen.
func (a *App) HiveMembers() []backend.HiveMember {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		return []backend.HiveMember{}
	}
	return h.Members()
}

// HiveStatus is what the Hive panel draws: this instance's part in the hive
// and, on a queen, who is in it. One call rather than two so the panel never
// shows a role from one moment and a roster from another.
func (a *App) HiveStatus() map[string]any {
	a.mu.Lock()
	s, h, ann := a.settings, a.hive, a.hiveAnn
	a.mu.Unlock()

	out := map[string]any{
		"protocol": backend.HiveProtocol, "role": "", "name": "",
		"members": []backend.HiveMember{}, "tasks": a.tasks().Recent(),
	}
	if s == nil {
		return out
	}
	out["role"] = s.Hive.Role
	if sp, _ := backend.NewSpawner(s.Hive.Spawner); sp != nil && s.Hive.Role == backend.HiveRoleQueen {
		out["spawner"] = map[string]any{"enabled": true, "max": sp.Max()}
	}
	name := strings.TrimSpace(s.Hive.Name)
	if name == "" {
		name, _ = os.Hostname()
	}
	out["name"] = name
	if h != nil {
		out["members"] = h.Members()
		out["interval_ms"] = int(s.Hive.Interval() / time.Millisecond)
	}
	if ann != nil {
		st := ann.State()
		out["queen"] = map[string]any{
			"url": s.Hive.JoinURL, "joined": st.Joined, "last_ok": st.LastOK, "error": st.LastErr,
		}
	}
	return out
}

// handleHiveJoin is POST /api/hive/join: a worker saying who it is. It sits
// behind the ordinary credential gate — a stranger cannot put itself on the
// roster — and answers 404 on anything but a queen, so a worker does not
// pretend to be one.
func (a *App) handleHiveJoin(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
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

// handleHiveRoster is GET /api/hive/roster: the live workers, for a worker that
// wants to ask a peer something. Queen only.
func (a *App) handleHiveRoster(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
		return
	}
	writeJSONStatus(w, http.StatusOK, h.Roster())
}

// handleHiveLeave is POST /api/hive/leave: a worker going away on purpose.
func (a *App) handleHiveLeave(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
		return
	}
	var hello backend.HiveHello
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&hello); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unreadable goodbye: " + err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"left": h.Leave(hello.Name, hello.StartedAt)})
}

// handleHivePulse is POST /api/hive/pulse: one flicker of a peer order, from
// the worker that gave it. Republished on the queen's own stream so the panel
// can draw light between two workers the queen was not part of. Not stored.
func (a *App) handleHivePulse(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
		return
	}
	var p backend.HivePulse
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unreadable pulse: " + err.Error()})
		return
	}
	if p.Dir != backend.TaskPeer || p.From == "" || p.Worker == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "only peer pulses are reported here"})
		return
	}
	known := false
	for _, m := range h.Members() {
		if m.Name == p.From {
			known = true
		}
	}
	if !known {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "not on the roster: " + p.From})
		return
	}
	raw, _ := json.Marshal(p)
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		a.emit("hive:pulse", m)
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
}

// handleHiveTask is POST /api/hive/task: a worker reporting an order it gave a
// peer. Only from someone on the roster — the credential gate says who may
// speak to the queen at all, and this says who may speak for a worker.
func (a *App) handleHiveTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
		return
	}
	var t backend.HiveTask
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&t); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unreadable task: " + err.Error()})
		return
	}
	if t.Dir != backend.TaskPeer || t.From == "" || t.Worker == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "only peer tasks are reported here"})
		return
	}
	known := false
	for _, m := range h.Members() {
		if m.Name == t.From {
			known = true
		}
	}
	if !known {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "not on the roster: " + t.From})
		return
	}
	a.tasks().Ingest(t)
	writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
}

// startHive brings up this instance's part: a roster for a queen, an
// announcing loop for a worker. Restarted on every settings save, because
// changing the role or the join address is a settings change.
func (a *App) startHive() {
	a.stopHive()

	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil || s.Hive.Role != backend.HiveRoleWorker {
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
	done := make(chan struct{})
	a.mu.Lock()
	a.hiveAnn = ann
	a.hiveStop = cancel
	a.hiveDone = done
	a.mu.Unlock()
	go func() {
		defer close(done)
		ann.Run(ctx)
	}()
}

func (a *App) stopHive() {
	a.mu.Lock()
	stop, done := a.hiveStop, a.hiveDone
	a.hiveStop, a.hiveAnn, a.hiveDone = nil, nil, nil
	a.mu.Unlock()
	if stop != nil {
		stop()
		// Wait for the goodbye to be said: the process is about to exit, and a
		// goodbye still in flight when it does is a worker shown as lost.
		if done != nil {
			select {
			case <-done:
			case <-time.After(4 * time.Second):
			}
		}
	}
}

// registerHiveTools gives a queen its command. Called from the build,
// which already holds a.mu, so it takes nothing — the roster is created here,
// once, and the tools read it through the runner at call time.
//
// Present for a queen, and for anyone whose settings name a worker by URL.
// Absent otherwise, which is what stops a worker commanding anyone: its
// settings list none and it never gets a roster.
func (a *App) registerHiveTools(svc *backend.Service, cfg *backend.Settings) {
	h := cfg.Hive
	if svc == nil {
		return
	}
	if h.Role == backend.HiveRoleQueen && a.hive == nil {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name, _ = os.Hostname()
		}
		a.hive = backend.NewHive(name, h.Interval())
	}
	if h.Role != backend.HiveRoleQueen {
		// Not a queen any more (or never): drop a stale roster so a demoted
		// instance does not go on holding other workers' credentials.
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
	if h.Role == backend.HiveRoleWorker {
		a.registerPeerTools(inner)
	}
	if a.hive == nil && !static {
		return
	}

	if a.hive != nil {
		inner.AddToolWithMetadata("hive_spawn",
			"Make more workers for this hive. Returns once they have joined and are live, and says who they are."+
				" Use it when there is more to do than the workers you have can do at once, and check hive_members first:"+
				" idle workers are cheaper than new ones. There is a cap, and asking for more than it allows is refused.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer", "minimum": 1, "description": "How many more workers. Default 1."},
				},
			},
			func(ctx context.Context, args map[string]any) (any, error) {
				n := 1
				if v, ok := args["count"].(float64); ok {
					n = int(v)
				}
				return a.spawnReport(a.hiveSpawn(ctx, n)), nil
			},
			agent.ToolMetadata{Destructive: true})
		inner.AddToolWithMetadata("hive_map",
			"Get a list of independent jobs done by the hive's workers, as fast as they can be done."+
				"\n\nThis is the tool when there are more jobs than workers, or when you do not know how many workers"+
				" there are: the jobs go in a queue and each worker takes the next one as soon as it is free, so nobody"+
				" holds two and nobody sits idle while jobs wait. If a worker fails, its job goes to another."+
				" hive_command is for the other case, where you already know which worker should do which thing."+
				"\n\nIf there are more jobs than live workers, set spawn_up_to to let the hive grow — it makes the"+
				" workers it needs, up to that number and up to its cap, before it starts. Set retire_after to let the"+
				" ones it made go again when the jobs are done."+
				"\n\nEvery job must stand alone: a worker starts a fresh conversation and knows nothing of the others."+
				" What comes back is one report per job, in the order you gave them; read all of them, failures included.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"jobs": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
						"description": "One self-contained instruction per job.",
					},
					"spawn_up_to": map[string]any{
						"type": "integer", "minimum": 0,
						"description": "Grow the hive to at most this many workers if the jobs outnumber the live ones. 0 or omitted: use only who is there.",
					},
					"retire_after": map[string]any{"type": "boolean", "description": "Let go the workers this call made, when done."},
				},
				"required": []string{"jobs"},
			},
			func(ctx context.Context, args map[string]any) (any, error) {
				var jobs []string
				if js, ok := args["jobs"].([]any); ok {
					for _, j := range js {
						jobs = append(jobs, strings.TrimSpace(str(j)))
					}
				}
				up := 0
				if v, ok := args["spawn_up_to"].(float64); ok {
					up = int(v)
				}
				ra, _ := args["retire_after"].(bool)
				return a.hiveMap(ctx, jobs, up, ra)
			},
			agent.ToolMetadata{Destructive: true})
		inner.AddToolWithMetadata("hive_retire",
			"Let workers go when the work is done. It removes the highest-numbered ones and refuses while any of them"+
				" is in the middle of an order, so nothing is lost. Do not retire workers you may need again soon.",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer", "minimum": 1, "description": "How many to retire. Default 1."},
				},
			},
			func(ctx context.Context, args map[string]any) (any, error) {
				n := 1
				if v, ok := args["count"].(float64); ok {
					n = int(v)
				}
				return a.spawnReport(a.HiveRetire(n, false)), nil
			},
			agent.ToolMetadata{Destructive: true})
	}

	inner.AddToolWithMetadata("hive_members",
		"Who is in the hive right now: every worker that has joined, whether it is live or lost, and where it is."+
			" Look here before commanding, because a worker that is lost will not answer.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			b, err := json.Marshal(a.rosterView())
			return string(b), err
		},
		agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true})

	inner.AddToolWithMetadata("hive_command",
		"Command the other workers — SuperAI instances in this hive — to work in parallel and collect what each reports back."+
			"\n\nYou are the queen: they act on your word, they share your memory, and they cannot command you."+
			" The workers are whoever has joined; call hive_members to see them. Give every live worker the same order with"+
			" `prompt`, or split the work with `commands`, one entry per worker. Each worker gets a fresh conversation and"+
			" has no idea what the others were told, so every order must stand alone. What comes back is one report per"+
			" worker, and a worker that failed or was lost says so — read them all before you conclude anything.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "One order sent to every live worker (or to `workers`, if given)."},
				"workers": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "Limit `prompt` to these workers. Omit for all live workers.",
				},
				"commands": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"worker": map[string]any{"type": "string"},
							"prompt": map[string]any{"type": "string"},
						},
						"required": []string{"worker", "prompt"},
					},
					"description": "A different order per worker. Use instead of `prompt`.",
				},
			},
		},
		a.hiveCommand,
		agent.ToolMetadata{Destructive: true})
}

// rosterView is hive members plus statically configured workers, for the model.
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
	for n, ag := range a.remoteRunner().Workers() {
		if !seen[n] {
			out = append(out, map[string]any{"name": n, "state": "configured", "url": ag.URL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

// hiveCommand fans an order out to workers in parallel.
func (a *App) hiveCommand(ctx context.Context, args map[string]any) (any, error) {
	runner := a.remoteRunner()
	live := runner.Workers()
	names := make([]string, 0, len(live))
	for n := range live {
		names = append(names, n)
	}
	sort.Strings(names)

	type order struct{ worker, prompt string }
	var orders []order
	if cs, ok := args["commands"].([]any); ok && len(cs) > 0 {
		for _, c := range cs {
			m, _ := c.(map[string]any)
			orders = append(orders, order{strings.TrimSpace(str(m["worker"])), str(m["prompt"])})
		}
	} else if p := strings.TrimSpace(str(args["prompt"])); p != "" {
		targets := names
		if rs, ok := args["workers"].([]any); ok && len(rs) > 0 {
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
			return "There are no live workers in the hive right now. Call hive_members to see who has joined and who is lost.", nil
		}
		return nil, fmt.Errorf("give either prompt or commands")
	}

	// Explains a name that is not commandable in the terms the roster uses: a
	// worker that was lost is not the same problem as one that never existed.
	why := func(name string) string {
		for _, m := range a.HiveMembers() {
			if m.Name == name {
				return fmt.Sprintf("%s is %s (last heard %ds ago) and cannot be commanded until it is back",
					name, m.State, int(time.Since(m.LastSeen).Seconds()))
			}
		}
		return fmt.Sprintf("there is no worker called %q; live workers: %s", name, strings.Join(names, ", "))
	}

	out := make([]string, len(orders))
	var wg sync.WaitGroup
	for i, o := range orders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := live[o.worker]; !ok {
				out[i] = fmt.Sprintf("## %s\nFAILED: %s", o.worker, why(o.worker))
				return
			}
			res, err := runner.Run(ctx, o.worker, o.prompt)
			switch {
			case err != nil:
				out[i] = fmt.Sprintf("## %s\nFAILED: %v", o.worker, err)
			case res.Failed:
				out[i] = fmt.Sprintf("## %s\nFAILED (%s) after %.1fs. What came back, if anything:\n%s",
					o.worker, res.Reason, float64(res.MS)/1000, res.Text)
			default:
				out[i] = fmt.Sprintf("## %s (%.1fs)\n%s", o.worker, float64(res.MS)/1000, res.Text)
			}
		}()
	}
	wg.Wait()
	return strings.Join(out, "\n\n"), nil
}

// registerPeerTools lets a worker ask another worker something directly.
//
// This is the sideways half of the hive. The queen still gives the orders, but
// a worker that needs what a peer knows or can reach — its host, its files, its
// view of the cluster — no longer has to go back up and down through the queen
// to get it. It asks, and the answer comes straight back.
func (a *App) registerPeerTools(inner interface {
	AddToolWithMetadata(name, description string, params map[string]any, fn func(context.Context, map[string]any) (any, error), meta agent.ToolMetadata)
}) {
	a.peerAsks = make(chan struct{}, 3)

	inner.AddToolWithMetadata("hive_peers",
		"The other workers in this hive that you can ask something directly.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			peers := a.remoteRunner().Workers()
			names := make([]string, 0, len(peers))
			for n := range peers {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "No peers are live right now.", nil
			}
			return strings.Join(names, "\n"), nil
		},
		agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true})

	inner.AddToolWithMetadata("hive_ask",
		"Ask another worker in this hive a question and get its answer back."+
			"\n\nRight when a peer can see something you cannot: its own host, its files, its tools."+
			" The peer starts a fresh conversation with no memory of yours, so put everything it needs in the question."+
			" It shares your long-term memory, so anything you have saved it can find."+
			"\n\nAsk for facts and results, one hop. Do not ask a peer to ask another peer: that is how"+
			" two workers end up waiting on each other. At most three questions are out at once.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"worker": map[string]any{"type": "string", "description": "Which peer; see hive_peers."},
				"prompt": map[string]any{"type": "string", "description": "The whole question, standing on its own."},
			},
			"required": []string{"worker", "prompt"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			select {
			case a.peerAsks <- struct{}{}:
				defer func() { <-a.peerAsks }()
			default:
				return "Too many questions to peers are already out; wait for one to come back.", nil
			}
			res, err := a.remoteRunner().Run(ctx, strings.TrimSpace(str(args["worker"])), str(args["prompt"]))
			if err != nil {
				return nil, err
			}
			if res.Failed {
				return fmt.Sprintf("%s did not answer (%s). What came back, if anything:\n%s", res.Agent, res.Reason, res.Text), nil
			}
			return fmt.Sprintf("%s answered in %.1fs:\n\n%s", res.Agent, float64(res.MS)/1000, res.Text), nil
		},
		agent.ToolMetadata{})
}

// queenSpawner is the queen's roster and the way it makes workers, or the
// reason it has neither.
func (a *App) queenSpawner() (*backend.Hive, backend.Spawner, error) {
	a.mu.Lock()
	h, s := a.hive, a.settings
	a.mu.Unlock()
	if h == nil {
		return nil, nil, fmt.Errorf("this instance is not a queen; only a queen makes workers")
	}
	if s == nil {
		return nil, nil, fmt.Errorf("settings are not loaded yet")
	}
	sp, err := backend.NewSpawner(s.Hive.Spawner)
	if err != nil {
		return nil, nil, err
	}
	if sp == nil {
		return nil, nil, fmt.Errorf("this queen has no spawner: set hive.spawner to say where its workers run")
	}
	return h, sp, nil
}

func liveNames(h *backend.Hive) map[string]bool {
	out := map[string]bool{}
	for n := range h.Agents() {
		out[n] = true
	}
	return out
}

// spawnWait is how long HiveSpawn waits for the new workers to announce
// themselves. A pod takes a few seconds to start and one more heartbeat to be
// heard, so two minutes covers a slow image pull and still ends.
const spawnWait = 120 * time.Second

// HiveSpawn makes `count` more workers and returns once they have joined, or
// once it is clear they are not going to.
//
// It waits because a tool that answers "requested" leaves the caller to guess
// when it is safe to command what it asked for, and the honest answer — who is
// actually in the hive now — is one roster read away.
func (a *App) HiveSpawn(count int) map[string]any {
	return a.hiveSpawn(context.Background(), count)
}

func (a *App) hiveSpawn(ctx context.Context, count int) map[string]any {
	fail := func(err error) map[string]any { return map[string]any{"ok": false, "error": err.Error()} }
	h, sp, err := a.queenSpawner()
	if err != nil {
		return fail(err)
	}
	if count < 1 {
		count = 1
	}
	cur, err := sp.Replicas(ctx)
	if err != nil {
		return fail(err)
	}
	target := cur + count
	if target > sp.Max() {
		return fail(fmt.Errorf("%d workers would pass the cap of %d (there are %d now)", target, sp.Max(), cur))
	}
	// The workers this should produce are known by name — a StatefulSet's next
	// ordinals — and one counts as new when it is live under a start time it
	// did not have before. Counting live workers instead was wrong in a way that
	// showed: retire the top ordinal and spawn one straight after, and the
	// retired worker's last heartbeat is still "live", so the count was already
	// satisfied and the call returned before the new pod existed.
	prev := map[string]time.Time{}
	for _, m := range h.Members() {
		prev[m.Name] = m.StartedAt
	}
	expected := []string{}
	for i := cur; i < target; i++ {
		expected = append(expected, fmt.Sprintf("%s-%d", sp.Prefix(), i))
	}
	if err := sp.SetReplicas(ctx, target); err != nil {
		return fail(err)
	}

	arrived := func() []string {
		out := []string{}
		byName := map[string]backend.HiveMember{}
		for _, m := range h.Members() {
			byName[m.Name] = m
		}
		for _, n := range expected {
			m, ok := byName[n]
			if !ok || m.State != "live" {
				continue
			}
			if was, had := prev[n]; !had || !m.StartedAt.Equal(was) {
				out = append(out, n)
			}
		}
		return out
	}

	started := time.Now()
	deadline := time.After(spawnWait)
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	for len(arrived()) < len(expected) {
		select {
		case <-tick.C:
			continue
		case <-deadline:
		case <-ctx.Done():
		}
		break
	}
	joined := arrived()
	sort.Strings(joined)
	return map[string]any{
		"ok": true, "replicas": target, "live": len(liveNames(h)), "joined": joined,
		"complete": len(joined) == len(expected), "waited_seconds": int(time.Since(started).Seconds()),
	}
}

// HiveRetire lets `count` workers go — the highest-numbered ones, because that
// is how a StatefulSet shrinks. It refuses while any of them is in the middle
// of an order, unless force says the order may be lost.
func (a *App) HiveRetire(count int, force bool) map[string]any {
	ctx := context.Background()
	fail := func(err error) map[string]any { return map[string]any{"ok": false, "error": err.Error()} }
	_, sp, err := a.queenSpawner()
	if err != nil {
		return fail(err)
	}
	if count < 1 {
		count = 1
	}
	cur, err := sp.Replicas(ctx)
	if err != nil {
		return fail(err)
	}
	if cur == 0 {
		return fail(fmt.Errorf("there are no workers to retire"))
	}
	target := cur - count
	if target < 0 {
		target = 0
	}
	going := []string{}
	for i := cur - 1; i >= target; i-- {
		going = append(going, fmt.Sprintf("%s-%d", sp.Prefix(), i))
	}
	if !force {
		busy := map[string]bool{}
		for _, t := range a.tasks().Recent() {
			if t.State == backend.TaskRunning && (t.Dir == backend.TaskOut || t.Dir == backend.TaskPeer) {
				busy[t.Worker] = true
				if t.From != "" {
					busy[t.From] = true
				}
			}
		}
		for _, n := range going {
			if busy[n] {
				return fail(fmt.Errorf("%s is in the middle of an order; retiring it now would lose that work. "+
					"Wait for it to finish, or retire with force", n))
			}
		}
	}
	if err := sp.SetReplicas(ctx, target); err != nil {
		return fail(err)
	}
	sort.Strings(going)
	return map[string]any{"ok": true, "replicas": target, "retiring": going}
}

// spawnReport says a spawn or retire result the way the model should read it:
// the verdict first, in words.
func (a *App) spawnReport(r map[string]any) string {
	if ok, _ := r["ok"].(bool); !ok {
		return fmt.Sprintf("Not done: %v", r["error"])
	}
	if going, ok := r["retiring"].([]string); ok {
		return fmt.Sprintf("Retiring %s. The hive is going to %v workers.", strings.Join(going, ", "), r["replicas"])
	}
	joined, _ := r["joined"].([]string)
	if done, _ := r["complete"].(bool); !done {
		return fmt.Sprintf("Asked for %v workers in total, but only %v are live after %vs. Joined so far: %s. "+
			"The rest may still be starting; check hive_members.", r["replicas"], r["live"], r["waited_seconds"], strings.Join(joined, ", "))
	}
	return fmt.Sprintf("The hive now has %v live workers. New: %s (took %vs).", r["live"], strings.Join(joined, ", "), r["waited_seconds"])
}

// HiveMap is the scheduler: many jobs, however many workers there are, growing
// the hive first if it was allowed to. See backend/hive_map.go for how the jobs
// are shared out; what is here is the part that decides how many workers to
// have.
func (a *App) hiveMap(ctx context.Context, jobs []string, spawnUpTo int, retireAfter bool) (string, error) {
	clean := jobs[:0:0]
	for _, j := range jobs {
		if strings.TrimSpace(j) != "" {
			clean = append(clean, j)
		}
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("no jobs given")
	}
	if len(clean) > 200 {
		return "", fmt.Errorf("%d jobs is more than one call takes (200); split them", len(clean))
	}
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		return "", fmt.Errorf("this instance is not a queen")
	}
	runner := a.remoteRunner()

	var notes []string
	made := 0
	if live := len(runner.Workers()); spawnUpTo > live {
		want := spawnUpTo
		if len(clean) < want {
			want = len(clean)
		}
		if add := want - live; add > 0 {
			r := a.hiveSpawn(ctx, add)
			if ok, _ := r["ok"].(bool); !ok {
				// Not fatal: the jobs can still run on who is there, slower.
				notes = append(notes, fmt.Sprintf("Could not grow the hive (%v); running on the %d workers there are.", r["error"], live))
			} else if joined, _ := r["joined"].([]string); len(joined) > 0 {
				made = len(joined)
				notes = append(notes, fmt.Sprintf("Grew the hive by %d: %s.", made, strings.Join(joined, ", ")))
			}
		}
	}

	started := time.Now()
	res := backend.MapOrders(ctx, runner, clean, backend.MapOptions{})

	if retireAfter && made > 0 {
		r := a.HiveRetire(made, false)
		if ok, _ := r["ok"].(bool); ok {
			notes = append(notes, fmt.Sprintf("Retired %d worker(s) after the jobs.", made))
		} else {
			notes = append(notes, fmt.Sprintf("Could not retire the new workers yet: %v", r["error"]))
		}
	}

	ok := 0
	var b strings.Builder
	for _, r := range res {
		if r.OK {
			ok++
		}
	}
	fmt.Fprintf(&b, "%d of %d jobs done in %.0fs.", ok, len(res), time.Since(started).Seconds())
	for _, n := range notes {
		b.WriteString(" " + n)
	}
	b.WriteString("\n")
	for _, r := range res {
		if r.OK {
			fmt.Fprintf(&b, "\n## Job %d — %s (%.1fs)\n%s\n", r.Index+1, r.Worker, float64(r.MS)/1000, r.Text)
		} else {
			fmt.Fprintf(&b, "\n## Job %d — FAILED after %d attempt(s)\n%s\n", r.Index+1, r.Attempts, r.Reason)
		}
	}
	return b.String(), nil
}

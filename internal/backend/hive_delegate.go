package backend

// Delegation: the queen plans, the workers work.
//
// A queen that answers "下周有哪些例会？" by calling mcp_noted_list_events
// herself is a queen doing a worker's job: her model is the one every person
// and every phone is waiting on, and a turn spent on tool calls is a turn the
// next question queues behind. So, by default, a queen with a live worker
// does not hold the work tools at all. They are taken out of her run (the
// run's deny list, enforced by agent-go both in the schema and at dispatch)
// and the one way to get a tool-using piece done is hive_delegate, which
// hands it to a worker — idle ones first, one per piece when there are
// several — streams what the worker does back into her run, and returns its
// report. What she keeps is what a coordinator needs: the hive tools, her own
// state, the conversation, approvals, and composing the answer.
//
// A worker that fails or times out is replaced by another once; when no
// worker can do a piece, the queen does it herself in a run of its own (with
// her tools, without hive_delegate) and the report says so. With no live
// worker at all, the run simply keeps every tool — there is nobody to give
// the work to.
//
// The setting is hive.delegation:
//
//	max   (a queen's default) the above
//	auto  hive_delegate is offered and preferred, nothing is withheld
//	off   the queen as she was: answers small things herself

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DelegationMax  = "max"
	DelegationAuto = "auto"
	DelegationOff  = "off"
)

// DelegateToolName is the queen's one route to tool work in max mode.
const DelegateToolName = "hive_delegate"


// MaxDelegateTasks is how many pieces one call takes.
const MaxDelegateTasks = 20

// DelegationMode is the mode in force: only a queen delegates, and an
// unknown value is read as the default rather than as off, so a typo does not
// quietly put the queen back to work.
func (h HiveSettings) DelegationMode() string {
	if h.Role != HiveRoleQueen {
		return DelegationOff
	}
	v := strings.ToLower(strings.TrimSpace(h.Delegation))
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("SUPERLEO_HIVE_DELEGATION"))); env != "" {
		v = env
	}
	switch v {
	case DelegationAuto, DelegationOff, DelegationMax:
		return v
	}
	return DelegationMax
}

// DelegateTimeout is DelegateTimeoutSeconds as a duration, or the default.
func (h HiveSettings) DelegateTimeout() time.Duration {
	if h.DelegateTimeoutSeconds <= 0 {
		return 0 // no limit unless one is set: a piece ends when it is done
	}
	return time.Duration(h.DelegateTimeoutSeconds) * time.Second
}

// queenKeeps are the tools a queen keeps in max mode, by exact name: the
// conversation, her own state and settings, approvals-adjacent and
// self-extension tools (what she installs, her workers get), and agent-go's
// own loop tools. Everything else in her registry is work.
var queenKeeps = map[string]bool{
	// the loop itself
	"task_complete": true, "task_blocked": true, "search_available_tools": true,
	// talking to the person, and the shape of the answer
	"notify_user": true, "ui_block_rules": true, "resolve_datetime": true,
	// her own records, reminders and schedules: they live, and ring, on her
	"add_record": true, "add_schedule": true, "upsert_person": true, "search_records": true,
	"list_schedules": true, "list_reminders": true, "set_reminder": true,
	"memory_save": true, "memory_update": true, "memory_delete": true,
	"list_deliverables": true, "status": true, "doctor": true, "attention": true,
	"devices": true, "linked_agents": true, "coding_runs": true, "standing_agents": true,
	"skills": true,
	// the person's phone home screen, which lives on her
	"home_layout": true,
	// self-extension: installed on the queen, synced to every worker
	"add_mcp_server": true, "remove_mcp_server": true, "mcp_servers": true,
	"install_skill": true, "install_skill_from_url": true, "install_mcp_from_url": true,
	"remove_skill": true, "search_mcp_servers": true, "search_skills": true,
}

// queenKeepsPrefix are kept by prefix: the hive's own tools, agents outside
// the hive (delegation too), her plan, skills' know-how, dashboards,
// schedules, settings, the pet, long tasks, the machines.
var queenKeepsPrefix = []string{
	"hive_", "coding_agent_", "remote_agent_", "scratchpad_", "skill_", "dashboard_",
	"schedule_", "settings_", "pet_", "standing_agent_", "long_task_", "machine_",
}

// QueenKeepsTool says whether a queen in max mode keeps a tool in her own run.
func QueenKeepsTool(name string) bool {
	if queenKeeps[name] {
		return true
	}
	for _, p := range queenKeepsPrefix {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// WorkToolDenylist is the part of a registry a queen in max mode hands to her
// workers: MCP tools (noted, search, CortexDB…), the web, files and shell,
// knowledge and graph research, imports, sub-agents of her own.
func WorkToolDenylist(registry []string) []string {
	var out []string
	for _, n := range registry {
		if !QueenKeepsTool(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// DelegateTask is one piece of work, as the queen hands it over.
type DelegateTask struct {
	Task string `json:"task"`
	// Needs names the MCP servers the piece's tools come from ("noted"), so
	// it goes to a worker that has them.
	Needs []string `json:"needs,omitempty"`
	// Worker pins the piece to one worker, when the queen has a reason.
	Worker string `json:"worker,omitempty"`
}

// WorkerToolEvent is one tool call (or its result) made on the queen's behalf,
// by a worker or, in a fallback, by the queen's own run.
type WorkerToolEvent struct {
	Worker string `json:"worker"`
	// Kind is "tool_call", "tool_result" or "text": a piece of what the
	// worker is writing, in Text, as it writes it.
	Kind string         `json:"kind"`
	Text string         `json:"text,omitempty"`
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
	OK   bool           `json:"ok"`
	// Error is what a failed result said.
	Error string `json:"error,omitempty"`
}

// Label is how the event reads on a screen: "orange2 · mcp_noted_list_events".
func (e WorkerToolEvent) Label() string { return StepLabel(e.Worker, e.Tool) }

// StepLabel attributes one tool to whoever ran it.
func StepLabel(worker, tool string) string { return worker + " · " + tool }

type workerTraceKey struct{}

// WithWorkerTrace has every tool event of work done under ctx reported to fn.
func WithWorkerTrace(ctx context.Context, fn func(WorkerToolEvent)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, workerTraceKey{}, fn)
}

// WorkerTraceFrom is the trace on ctx, or nil.
func WorkerTraceFrom(ctx context.Context) func(WorkerToolEvent) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(workerTraceKey{}).(func(WorkerToolEvent))
	return fn
}

type queenSelfKey struct{}

// WithQueenSelf marks a run the queen makes to do a piece herself, so it keeps
// her work tools and does not delegate again.
func WithQueenSelf(ctx context.Context) context.Context {
	return context.WithValue(ctx, queenSelfKey{}, true)
}

// IsQueenSelf says whether ctx is such a run.
func IsQueenSelf(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(queenSelfKey{}).(bool)
	return v
}

// DelegateEnv is what RunDelegated needs from the hive.
type DelegateEnv struct {
	// Candidates are the workers able to take a piece needing these servers,
	// best first (idle before busy).
	Candidates func(needs []string) []string
	// Run gives one piece to one worker.
	Run func(ctx context.Context, worker, prompt string) (RemoteResult, error)
	// Self does a piece on the queen. Nil: no fallback.
	Self func(ctx context.Context, prompt string) (RemoteResult, error)
	// SelfName is the queen's name, for the attribution of what she did.
	SelfName string
	// Timeout bounds one attempt on one worker. Zero: none.
	Timeout time.Duration
	// OnAssign, when set, is told each time a piece goes to someone.
	OnAssign func(index int, worker string, fallback bool)
}

// DelegateResult is one piece's outcome.
type DelegateResult struct {
	Index int    `json:"index"`
	Task  string `json:"task"`
	// Worker is who did it (the queen's name for a fallback).
	Worker   string `json:"worker"`
	Fallback bool   `json:"fallback,omitempty"`
	// Why is why it fell back: who failed, or that nobody could take it.
	Why    string   `json:"why,omitempty"`
	Tried  []string `json:"tried,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Text   string   `json:"text"`
	Failed bool     `json:"failed,omitempty"`
	Reason string   `json:"reason,omitempty"`
	MS     int64    `json:"ms"`
	TaskID string   `json:"task_id,omitempty"`
}

// delegatePreamble goes before every piece a worker gets: who asked and
// what to send back. The worker's reply is read by the queen, not the person.
const delegatePreamble = "The queen hands you this piece of a request. Do it with your own tools and report back what you found or did — the facts, ids, times and results, plainly; she writes the reply to the person.\n\n"

const selfPreamble = "No worker could take this piece, so do it yourself with your own tools and report back what you found or did — the facts, ids, times and results, plainly.\n\n"

// RunDelegated runs every piece at once, each on its own worker where there
// are enough, and returns the results in the order given.
func RunDelegated(ctx context.Context, env DelegateEnv, tasks []DelegateTask) []DelegateResult {
	results := make([]DelegateResult, len(tasks))
	var mu sync.Mutex
	load := map[string]int{} // pieces of this call given to each worker

	// pick chooses the best candidate not yet tried, spreading this call's
	// pieces over different workers before doubling up.
	pick := func(t DelegateTask, tried []string) string {
		cands := env.Candidates(t.Needs)
		mu.Lock()
		defer mu.Unlock()
		if w := strings.TrimSpace(t.Worker); w != "" && len(tried) == 0 && slices.Contains(cands, w) {
			load[w]++
			return w
		}
		best, bestLoad := "", 0
		for _, c := range cands {
			if slices.Contains(tried, c) {
				continue
			}
			if best == "" || load[c] < bestLoad {
				best, bestLoad = c, load[c]
			}
		}
		if best != "" {
			load[best]++
		}
		return best
	}

	var wg sync.WaitGroup
	for i, t := range tasks {
		results[i] = DelegateResult{Index: i, Task: t.Task}
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			res := &results[i]
			defer func() { res.MS = time.Since(started).Milliseconds() }()

			trace := func(worker string) context.Context {
				outer := WorkerTraceFrom(ctx)
				return WithWorkerTrace(ctx, func(ev WorkerToolEvent) {
					if ev.Worker == "" {
						ev.Worker = worker
					}
					if ev.Kind == "tool_call" {
						mu.Lock()
						res.Tools = append(res.Tools, ev.Tool)
						mu.Unlock()
					}
					if outer != nil {
						outer(ev)
					}
				})
			}

			var failures []string
			for attempt := 0; attempt < 2; attempt++ {
				w := pick(t, res.Tried)
				if w == "" {
					break
				}
				res.Tried = append(res.Tried, w)
				if env.OnAssign != nil {
					env.OnAssign(i, w, false)
				}
				actx := trace(w)
				var cancel context.CancelFunc = func() {}
				if env.Timeout > 0 {
					actx, cancel = context.WithTimeout(actx, env.Timeout)
				}
				out, err := env.Run(actx, w, delegatePreamble+t.Task)
				timedOut := actx.Err() == context.DeadlineExceeded
				cancel()
				if out.Cancelled {
					// Stopped on request: that is the answer for this piece,
					// not a failure for someone else to retry.
					res.Worker, res.Text, res.TaskID = w, out.Text, out.TaskID
					res.Failed, res.Reason = true, out.Reason
					return
				}
				if err == nil && !out.Failed {
					res.Worker, res.Text, res.TaskID = w, out.Text, out.TaskID
					return
				}
				reason := out.Reason
				if err != nil {
					reason = err.Error()
				}
				if timedOut {
					reason = fmt.Sprintf("no answer within %s", env.Timeout)
				}
				failures = append(failures, fmt.Sprintf("%s: %s", w, clip(reason, 300)))
				mu.Lock()
				res.Tools = nil // what the failed attempt called is not this piece's record
				mu.Unlock()
				if ctx.Err() != nil {
					break
				}
			}

			if ctx.Err() != nil {
				res.Failed, res.Reason = true, fmt.Sprintf("stopped: %v", ctx.Err())
				return
			}
			why := "no live worker"
			if len(t.Needs) > 0 {
				why = "no live worker has " + strings.Join(t.Needs, ", ")
			}
			if len(failures) > 0 {
				why = strings.Join(failures, "; ")
			}
			if env.Self == nil {
				res.Failed, res.Reason = true, why
				return
			}
			res.Fallback, res.Why, res.Worker = true, why, env.SelfName
			if env.OnAssign != nil {
				env.OnAssign(i, env.SelfName, true)
			}
			out, err := env.Self(trace(env.SelfName), selfPreamble+t.Task)
			res.Text = out.Text
			switch {
			case err != nil:
				res.Failed, res.Reason = true, err.Error()
			case out.Failed:
				res.Failed, res.Reason = true, out.Reason
			}
		}()
	}
	wg.Wait()
	return results
}

// delegateKeep is how much of one piece's report goes back into the queen's
// context; the whole of it is on the task board (hive_task).
const delegateKeep = 16000

// stepsPrefix opens the line of a report that says who called what. The
// transcript reads it back (DelegatedSteps), so a reopened conversation shows
// "orange2 · mcp_noted_list_events" where the live one did.
const stepsPrefix = "steps: "

// FormatDelegated is the report the queen's model reads.
func FormatDelegated(results []DelegateResult) string {
	var b strings.Builder
	ok := 0
	for _, r := range results {
		if !r.Failed {
			ok++
		}
	}
	fmt.Fprintf(&b, "%d of %d piece(s) done.\n", ok, len(results))
	for _, r := range results {
		who := r.Worker
		if who == "" {
			who = "nobody"
		}
		fmt.Fprintf(&b, "\n## Piece %d — %s · %.1fs", r.Index+1, who, float64(r.MS)/1000)
		if r.TaskID != "" {
			fmt.Fprintf(&b, " · task %s", r.TaskID)
		}
		b.WriteString("\n")
		if r.Fallback {
			fmt.Fprintf(&b, "Done by the queen herself, because %s.\n", r.Why)
		}
		b.WriteString(stepsPrefix + strings.Join(stepLines(r), "; ") + "\n")
		text := r.Text
		if rs := []rune(text); len(rs) > delegateKeep {
			text = string(rs[:delegateKeep]) + fmt.Sprintf("\n[… %d more characters; the whole report: hive_task %s]", len(rs)-delegateKeep, r.TaskID)
		}
		if r.Failed {
			fmt.Fprintf(&b, "FAILED: %s\n", r.Reason)
			if strings.TrimSpace(text) != "" {
				b.WriteString("What came back, if anything:\n" + text + "\n")
			}
			continue
		}
		b.WriteString(text + "\n")
	}
	return b.String()
}

func stepLines(r DelegateResult) []string {
	who := r.Worker
	if who == "" {
		who = "nobody"
	}
	var out []string
	for _, t := range r.Tools {
		line := StepLabel(who, t)
		if len(out) > 0 && out[len(out)-1] == line {
			continue
		}
		out = append(out, line)
	}
	switch {
	case r.Failed:
		out = append(out, StepLabel(who, "failed"))
	case len(out) == 0:
		out = append(out, StepLabel(who, "answered"))
	}
	return out
}

// DelegatedSteps reads the attributed steps back out of a hive_delegate
// report, for the transcript.
func DelegatedSteps(report string) []string {
	var out []string
	for _, line := range strings.Split(report, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), stepsPrefix)
		if !ok {
			continue
		}
		for _, s := range strings.Split(rest, "; ") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// InferNeeds names the servers a piece mentions, from those the queen knows,
// for a piece whose needs were left out: "list next week's events in noted"
// needs noted.
func InferNeeds(task string, servers []string) []string {
	low := strings.ToLower(task)
	var out []string
	for _, s := range servers {
		if s == "" {
			continue
		}
		re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(strings.ToLower(s)) + `([^a-z0-9]|$)`)
		if re.MatchString(low) || strings.Contains(low, "mcp_"+strings.ToLower(s)+"_") {
			out = append(out, s)
		}
	}
	return out
}

// HasAll says whether have holds every one of need. A nil have is a worker
// that did not say, and may have anything.
func HasAll(have, need []string) bool {
	if have == nil {
		return true
	}
	for _, n := range need {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}

// ToolResultFailed reads a tool result the way the chat panel does: an object
// with ok false, an error, success false or is_error true.
func ToolResultFailed(r any) bool {
	m, ok := r.(map[string]any)
	if !ok {
		return false
	}
	if v, ok := m["ok"].(bool); ok && !v {
		return true
	}
	if v, ok := m["success"].(bool); ok && !v {
		return true
	}
	if v, ok := m["is_error"].(bool); ok && v {
		return true
	}
	switch e := m["error"].(type) {
	case string:
		return e != ""
	case nil:
		return false
	default:
		return true
	}
}

// toolResultError is the error a failed result carried, short.
func toolResultError(r any) string {
	m, ok := r.(map[string]any)
	if !ok {
		return ""
	}
	if e, ok := m["error"].(string); ok {
		return clip(e, 300)
	}
	return ""
}

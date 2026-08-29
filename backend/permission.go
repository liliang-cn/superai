package backend

// Approval gate for tool calls.
//
// agent-go has had an authorization seam since v3 (pkg/agent/permission.go):
// every tool dispatch runs through Service.authorizeTool before the tool
// handler is reached. The seam is inert until somebody installs a handler —
// authorizeTool opens with `if handler == nil { return nil }` — and SuperAI
// never installed one. The practical consequence was that `bash` ran whatever
// the model wrote, with no prompt and no record.
//
// The sandbox does not make up for that. LocalSandbox.Exec sets cmd.Dir and
// nothing else: no chroot, no namespace, no seccomp, no separate user. The
// workspace is where the command starts, not where it is confined, so
// `bash("rm -rf ~/Documents")` leaves the workspace on its first character.
// fs_* really is jailed; bash is not, and bash is what the model reaches for.
//
// So: a handler that asks a human, blocks until they answer, denies on a
// timeout, and writes down what happened either way.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// DefaultApprovalWait is how long a gated call waits for a human.
//
// Two minutes is a compromise between two failure modes. Too short and a user
// who stepped away to read the command in another window comes back to a run
// that already gave up. Too long and an unattended run — a 3am schedule, a
// `superai` invocation from a shell script — parks a turn for as long as the
// LLM timeout allows while nobody is ever going to answer. Two minutes is long
// enough to read a shell command and think about it, short enough that an
// unattended run fails fast and says why.
const DefaultApprovalWait = 2 * time.Minute

// Who made a decision. These land verbatim in the audit log, so a reader can
// tell "the user said no" from "nobody was there".
const (
	DecidedByUser       = "user"
	DecidedByTimeout    = "timeout"
	DecidedByNoApprover = "no-approver"
	DecidedByCancelled  = "run-cancelled"
	DecidedByGateOff    = "gate-disabled"
	DecidedByError      = "error"
)

// ApprovalRequest is one gated tool call, as the UI is asked about it.
//
// Args and Command are NOT redacted here. A person cannot approve what they
// cannot see, and a half-shown command is worse than none — it invites a yes
// on a false picture. Redaction happens on the way to the audit log instead,
// which is the copy that outlives the decision.
type ApprovalRequest struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	// Command is the shell command for the shell-family tools, lifted out of
	// Args so the UI can put the one thing that matters in front of the user
	// instead of a JSON blob they will skim.
	Command   string         `json:"command,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	AgentID   string         `json:"agentId,omitempty"`
	AskedAt   time.Time      `json:"askedAt"`
	// ExpiresAt lets the UI show a countdown, so the user knows the prompt is
	// on a clock rather than wondering why the card vanished.
	ExpiresAt time.Time `json:"expiresAt"`
}

// ApprovalDecision is an answer to an ApprovalRequest.
type ApprovalDecision struct {
	Allowed bool `json:"allowed"`
	// By is one of the DecidedBy* constants. An approver may leave it empty;
	// the gate fills in DecidedByUser, since an approver answering at all
	// means a human answered.
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Approver is the seam a UI plugs into. It must block until the user answers
// or ctx is done. It must not answer on the user's behalf.
type Approver func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)

// ToolGate turns agent-go's permission seam into an approval prompt plus an
// audit trail. The zero value is not usable; build one with NewToolGate.
type ToolGate struct {
	mu       sync.RWMutex
	enabled  bool
	approver Approver
	wait     time.Duration
	audit    *approvalAudit

	// Injected so tests can pin ids and timestamps.
	newID func() string
	now   func() time.Time
}

// NewToolGate builds a gate. auditPath may be empty, which disables the log
// (tests do this); everywhere else it is AuditLogPath().
func NewToolGate(enabled bool, auditPath string) *ToolGate {
	g := &ToolGate{
		enabled: enabled,
		wait:    DefaultApprovalWait,
		newID:   func() string { return uuid.NewString() },
		now:     time.Now,
	}
	if strings.TrimSpace(auditPath) != "" {
		g.audit = &approvalAudit{path: auditPath}
	}
	return g
}

// SetApprover installs (or with nil, removes) the surface that asks the user.
// Called by whoever owns a UI; a process without one leaves it nil, and gated
// tools are then denied rather than left hanging.
func (g *ToolGate) SetApprover(a Approver) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.approver = a
}

// SetWait overrides the approval deadline. Only tests should need it.
func (g *ToolGate) SetWait(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d > 0 {
		g.wait = d
	}
}

// Enabled reports whether the gate is asking at all.
func (g *ToolGate) Enabled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// Wait is the current approval deadline.
func (g *ToolGate) Wait() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.wait
}

// Log returns the most recent audit entries, oldest first, for the UI.
func (g *ToolGate) Log(limit int) []AuditEntry {
	if g == nil || g.audit == nil {
		return nil
	}
	return g.audit.tail(limit)
}

// AuditPath is where decisions are written, for the UI to point the user at.
func (g *ToolGate) AuditPath() string {
	if g == nil || g.audit == nil {
		return ""
	}
	return g.audit.path
}

// Policy is the agent-go PermissionPolicy: which calls reach the handler.
//
// Deliberately not agent.DefaultPermissionPolicy. That one gates every tool
// whose name contains create / update / ingest / write / edit, which in this
// app is add_record, upsert_person, memory_update, ingest_document, fs_write
// and a dozen more — a normal turn would raise five or six prompts. A person
// asked to approve everything approves everything, and a gate that trains the
// user to click yes is worse than no gate, because it also carries the claim
// that someone looked. So the list below is the calls that actually escape the
// containment the app already has:
//
//   - the shell family, which runs outside the workspace jail entirely;
//   - the self-install tools, which fetch and then run other people's code;
//   - deletions, which are the ones that cannot be walked back.
//
// Everything else is either read-only or confined to the workspace, and shows
// up in the tool trace and the deliverables bar where the user can see it.
func (g *ToolGate) Policy() agent.PermissionPolicy {
	return toolNeedsApproval
}

// Handler is the agent-go PermissionHandler.
func (g *ToolGate) Handler() agent.PermissionHandler {
	return g.decide
}

// shellFragments name the tools that hand a string to an OS shell. Matched as
// substrings because MCP servers bring their own spellings (run_command,
// execute_shell, container_exec) and a missed spelling here is a hole.
var shellFragments = []string{"bash", "shell", "exec", "terminal", "run_command", "subprocess"}

// codeFetchFragments name the self-extension tools. install_skill and
// add_mcp_server both end with someone else's code running in this process's
// shoes; that is the same decision as running a command, made one step earlier.
var codeFetchFragments = []string{"install_", "add_mcp_server"}

// deleteFragments name the calls whose effect survives saying "undo it".
var deleteFragments = []string{"delete", "remove", "destroy"}

func toolNeedsApproval(req agent.PermissionRequest) bool {
	// The tool registry's own read-only flag wins: a call that cannot change
	// anything has nothing to approve, and asking about it is pure noise.
	if req.ReadOnly {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(req.ToolName))
	if name == "" {
		return false
	}
	for _, group := range [][]string{shellFragments, codeFetchFragments, deleteFragments} {
		for _, frag := range group {
			if strings.Contains(name, frag) {
				return true
			}
		}
	}
	return false
}

func (g *ToolGate) decide(ctx context.Context, req agent.PermissionRequest) (*agent.PermissionResponse, error) {
	g.mu.RLock()
	enabled, approver, wait := g.enabled, g.approver, g.wait
	g.mu.RUnlock()

	ask := ApprovalRequest{
		ID:        g.newID(),
		Tool:      req.ToolName,
		Command:   commandOf(req.ToolName, req.ToolArgs),
		Args:      req.ToolArgs,
		SessionID: req.SessionID,
		AgentID:   req.AgentID,
		AskedAt:   g.now(),
	}
	ask.ExpiresAt = ask.AskedAt.Add(wait)

	if !enabled {
		// Still audited. Turning the gate off is a decision about prompts, not
		// a decision to stop keeping records — and the log is the only way to
		// answer "what did it run while I had this switched off?".
		return g.answer(ask, ApprovalDecision{
			Allowed: true,
			By:      DecidedByGateOff,
			Reason:  "approval gate is switched off in Settings",
		})
	}

	if approver == nil {
		// No UI is attached to this process at all: the one-shot CLI, the
		// schedule daemon, a test. Deny immediately rather than burn the whole
		// deadline waiting for a surface that does not exist. Failing in a
		// second with a reason the model can relay beats two minutes of dead
		// air, and it is the same answer either way.
		return g.answer(ask, ApprovalDecision{
			By: DecidedByNoApprover,
			Reason: fmt.Sprintf("%s needs approval and this process has no way to ask "+
				"(no window, no browser session). Run it from the SuperAI app, or turn the "+
				"approval gate off in Settings if you meant this to be unattended.", req.ToolName),
		})
	}

	// The deadline lives in the select below and nowhere else. The obvious
	// alternative — hand the approver a context.WithTimeout and let it notice —
	// races: at the deadline both the approver's ctx and the timer fire, and
	// whichever wins decides what the audit log says happened. One clock, one
	// answer. The approver gets a cancellable context instead, and the deferred
	// cancel() is what tells it to take its card off the screen once this call
	// has stopped caring.
	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type answer struct {
		dec ApprovalDecision
		err error
	}
	// The approver runs in its own goroutine so that a UI which forgets its
	// context cannot wedge the turn: this is the one place where blocking
	// forever would read to the user as "the agent is thinking" rather than
	// "the agent is stuck waiting on itself". The goroutine may outlive this
	// call; the buffered channel is what keeps it from leaking.
	done := make(chan answer, 1)
	go func() {
		dec, err := approver(askCtx, ask)
		done <- answer{dec, err}
	}()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	var dec ApprovalDecision
	select {
	case a := <-done:
		switch {
		case a.err != nil:
			// An approver that errors has not got an answer from anyone, so
			// this is a denial, not a pass-through.
			dec = ApprovalDecision{By: DecidedByError, Reason: "could not ask for approval: " + a.err.Error()}
		default:
			dec = a.dec
			if dec.By == "" {
				dec.By = DecidedByUser
			}
		}
	case <-timer.C:
		dec = ApprovalDecision{
			By:     DecidedByTimeout,
			Reason: fmt.Sprintf("nobody approved %s within %s, so it was denied", req.ToolName, wait),
		}
	case <-ctx.Done():
		// The user pressed stop, or the turn's deadline expired. Not an
		// approval by any reading.
		dec = ApprovalDecision{By: DecidedByCancelled, Reason: "the run ended before anyone approved " + req.ToolName}
	}
	return g.answer(ask, dec)
}

// answer records the decision and shapes it for agent-go.
//
// A denial comes back as a PermissionResponse with Allowed false rather than
// as a Go error. agent-go turns the former into a PermissionDeniedError on
// that one tool call, which the model sees and can work around or explain; a
// returned error aborts the run. "No, not that command" should not end the
// conversation.
func (g *ToolGate) answer(ask ApprovalRequest, dec ApprovalDecision) (*agent.PermissionResponse, error) {
	if !dec.Allowed && strings.TrimSpace(dec.Reason) == "" {
		dec.Reason = "denied: " + ask.Tool + " was not approved"
	}
	if dec.By == "" {
		dec.By = DecidedByUser
	}
	g.record(ask, dec)
	return &agent.PermissionResponse{Allowed: dec.Allowed, Reason: dec.Reason}, nil
}

func (g *ToolGate) record(ask ApprovalRequest, dec ApprovalDecision) {
	if g.audit == nil {
		return
	}
	g.audit.append(AuditEntry{
		At:        g.now().UTC(),
		Tool:      ask.Tool,
		Allowed:   dec.Allowed,
		DecidedBy: dec.By,
		Reason:    dec.Reason,
		Summary:   summarizeArgs(ask.Tool, ask.Args),
		SessionID: ask.SessionID,
		AgentID:   ask.AgentID,
	})
}

// commandOf pulls the shell command out of a tool's arguments. agent-go's bash
// tool takes {"command": "..."}; MCP shells vary, so a couple of spellings are
// tried before giving up and letting the UI fall back to the raw args.
func commandOf(tool string, args map[string]any) string {
	lower := strings.ToLower(tool)
	shellish := false
	for _, frag := range shellFragments {
		if strings.Contains(lower, frag) {
			shellish = true
			break
		}
	}
	if !shellish || args == nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "script", "input"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

package app

// The desktop SuperLeo as one of core's queen engines.
//
// A Mac linked to core over the agent link offers its CLIs as engines
// ("claude.mac"); the desktop app there offers itself too, as
// "superleo.mac": its own agent, with its own model, memory and tools,
// thinks and answers the queen's turn. It says it can in its Hello
// (queen_engine), so core lists it only while the link is up and only from
// a SuperLeo new enough to do it.
//
// On the wire it is a queen run like any other: core asks StartQueenCLIRun
// with agent "superleo", and the desktop runs the turn as a CLIRun of its
// own whose events are its agent's — deltas as it writes, tool calls and
// results — so core's mirror (app_cliremote.go) and routeToEngine stream it,
// relay its approvals, stop it and hand the turn on exactly as they do for
// claude.mac. The run's session is a conversation of the desktop's own,
// kept apart from the person's and resumed on the queen's next turn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

const (
	// desktopEngineAgent is the agent a queen run names for the desktop
	// SuperLeo's own: "superleo.<name>" on core.
	desktopEngineAgent = "superleo"
	// desktopEngineSessionPrefix marks the desktop's conversations that
	// think for core's queen. Only those can be resumed from core: any other
	// session is the person's own.
	desktopEngineSessionPrefix = "queen-engine-"
)

// answersForQueen is whether this process can think for core's queen with
// its own agent: the desktop app, with a model set up.
func (a *App) answersForQueen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.desktop && (a.svc != nil || a.desktopTurnFn != nil)
}

// startDesktopQueenRun is a queen run of this app's own agent: the turn
// core's queen handed here, as a CLIRun core mirrors.
func (a *App) startDesktopQueenRun(r QueenRunRequest) (CLIRun, error) {
	if !a.answersForQueen() {
		return CLIRun{}, errors.New("this SuperLeo has no model of its own to think for the queen with")
	}
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		return CLIRun{}, errors.New("the prompt is empty")
	}
	session := strings.TrimSpace(r.Session)
	if session != "" && !strings.HasPrefix(session, desktopEngineSessionPrefix) {
		return CLIRun{}, fmt.Errorf("%s is not a conversation this SuperLeo keeps for the queen", session)
	}
	id := uuid.NewString()
	if session == "" {
		session = desktopEngineSessionPrefix + id
	}
	// The thread is the session: an approval card the turn raises names
	// it, and that is what core relays to its own cards by.
	run := &CLIRun{ID: id, Thread: session, Agent: desktopEngineAgent, Prompt: prompt, Session: session, Ask: r.Ask,
		Queen: true, Origin: "core's queen, thinking in this SuperLeo", State: "running", Started: time.Now()}
	ctx, cancel := context.WithCancel(backend.WithChatSession(context.Background(), session))
	s := a.cliStore()
	s.mu.Lock()
	s.runs[id] = run
	s.cancels[id] = cancel
	out := summaryOf(run)
	payload := runPayload(run)
	s.mu.Unlock()
	a.emit("cli:run", payload)
	go a.driveDesktopQueenRun(ctx, cancel, run, session, prompt)
	return out, nil
}

// driveDesktopQueenRun runs the turn and reports it as the run's events.
func (a *App) driveDesktopQueenRun(ctx context.Context, cancel context.CancelFunc, run *CLIRun, session, prompt string) {
	defer cancel()
	turn, err := a.desktopTurn(ctx, session, prompt, func(ev *agent.Event) {
		switch ev.Type {
		case agent.EventTypePartial:
			if ev.Content != "" {
				a.addCLIEvent(run, CLIRunEvent{Kind: "delta", Text: ev.Content})
			}
		case agent.EventTypeToolCall:
			detail := ""
			if len(ev.ToolArgs) > 0 {
				if b, err := json.Marshal(ev.ToolArgs); err == nil {
					detail = cliClip(string(b), 300)
				}
			}
			a.addCLIEvent(run, CLIRunEvent{Kind: "tool", Tool: ev.ToolName, Detail: detail})
		case agent.EventTypeToolResult:
			out := ev.Content
			if ev.ToolResult != nil {
				if b, err := json.Marshal(ev.ToolResult); err == nil {
					out = string(b)
				}
			}
			a.addCLIEvent(run, CLIRunEvent{Kind: "result", Tool: ev.ToolName, Text: cliClip(out, cliRunTextMax), Failed: toolEventFailed(ev)})
		}
	})
	reply, _ := backend.SplitEmotion(turn.Text)
	reply = strings.TrimSpace(reply)
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		a.finishCLIRun(run, func(run *CLIRun) { run.State, run.Error, run.Summary = "cancelled", "stopped", reply })
	case err != nil:
		a.finishCLIRun(run, func(run *CLIRun) { run.State, run.Error = "failed", err.Error() })
	default:
		if reply != "" {
			a.addCLIEvent(run, CLIRunEvent{Kind: "text", Text: reply})
		}
		a.finishCLIRun(run, func(run *CLIRun) {
			run.State, run.Summary = "done", reply
			if u := turn.Usage; u != nil {
				run.In, run.Out, run.Cache = u.PromptTokens, u.CompletionTokens, u.CachedPromptTokens
			}
		})
	}
}

// desktopTurn is one turn of this app's own agent in session.
func (a *App) desktopTurn(ctx context.Context, session, message string, emit func(*agent.Event)) (backend.TurnResult, error) {
	a.mu.Lock()
	svc, fn := a.svc, a.desktopTurnFn
	a.mu.Unlock()
	if fn != nil {
		return fn(ctx, session, message, emit)
	}
	if svc == nil {
		return backend.TurnResult{}, errors.New("this SuperLeo has no model set up")
	}
	return svc.StreamTurn(ctx, session, message, nil, emit)
}

// Core's side.

// isDesktopEngineID is whether an engine names a linked desktop SuperLeo's
// own agent, "superleo.<name>", whether or not it is there now.
func isDesktopEngineID(engine string) bool {
	agent, _, ok := splitRemoteCLI(engine)
	return ok && agent == desktopEngineAgent
}

// desktopEngine is the linked desktop SuperLeo an engine names, while its
// link is up and it says it thinks for the queen.
func (a *App) desktopEngine(engine string) (*agentlinkpb.Hello, bool) {
	agent, host, ok := splitRemoteCLI(engine)
	if !ok || agent != desktopEngineAgent {
		return nil, false
	}
	c := a.agents().get(host)
	if c == nil || !c.alive() {
		return nil, false
	}
	h := c.helloNow()
	return h, h.GetQueenEngine()
}

// desktopEngines is every linked desktop SuperLeo that thinks for the queen
// now, as the settings list it.
func (a *App) desktopEngines() []QueenEngineInfo {
	var out []QueenEngineInfo
	for _, c := range a.agents().list() {
		h := c.helloNow()
		if !c.alive() || !h.GetQueenEngine() {
			continue
		}
		out = append(out, QueenEngineInfo{ID: desktopEngineAgent + "." + h.GetName(), Label: desktopEngineLabel(h),
			Where: h.GetName(), Version: h.GetVersion(), State: "online"})
	}
	return out
}

// desktopEngineLabel is how the settings name a desktop SuperLeo.
func desktopEngineLabel(h *agentlinkpb.Hello) string {
	if h.GetKind() == backend.HostKindMac || h.GetOs() == "darwin" {
		return "SuperLeo（Mac 桌面版）"
	}
	return "SuperLeo（桌面版）"
}

// desktopEngineAway is a desktop SuperLeo in the list that cannot think for
// the queen now: its link is down, or its SuperLeo is too old to.
func (a *App) desktopEngineAway(id string) QueenEngineInfo {
	_, host, _ := splitRemoteCLI(id)
	info := QueenEngineInfo{ID: id, Label: "SuperLeo（桌面版）", Where: host, State: "offline", Reason: "not reachable now"}
	if c := a.agents().get(host); c != nil && c.alive() {
		h := c.helloNow()
		info.Label, info.Version = desktopEngineLabel(h), h.GetVersion()
		info.Reason = "its SuperLeo is too old to think for the queen; update it"
	}
	return info
}

// queenEngineTraits is how an engine thinks for the queen: a CLI's own
// (queenCLIs), or a desktop SuperLeo's, which writes as it goes and keeps
// its conversation.
func queenEngineTraits(engine string) queenCLI {
	if isDesktopEngineID(engine) {
		return queenCLI{Resumes: true, Streams: true}
	}
	return queenCLIs[engineCLI(engine)]
}

// canBeQueenEngine is whether an engine's name is one that can think for
// the queen at all.
func canBeQueenEngine(engine string) bool {
	return isDesktopEngineID(engine) || canThinkForQueen(engineCLI(engine))
}

// startDesktopEngineRun starts the queen's turn on a linked desktop
// SuperLeo.
func (a *App) startDesktopEngineRun(o cliStart, host string) (CLIRun, error) {
	engine := desktopEngineAgent + "." + host
	if o.Queen == nil {
		return CLIRun{}, fmt.Errorf("%s thinks only for the queen", engine)
	}
	if _, ok := a.desktopEngine(engine); !ok {
		return CLIRun{}, fmt.Errorf("the SuperLeo on %s is not linked now, or too old to think for the queen", host)
	}
	o.Agent = desktopEngineAgent
	return a.startRemoteCLIRun(o, host)
}

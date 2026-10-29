package app

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// fakeEngines stands in for starting a queen run: each engine by name fails
// before saying anything, or answers.
type fakeEngines struct {
	mu      sync.Mutex
	fail    map[string]string // engine → the error it fails with
	started []string
}

func (f *fakeEngines) start(_ *App, o cliStart) (CLIRun, error) {
	f.mu.Lock()
	f.started = append(f.started, o.Agent)
	reason, fails := f.fail[o.Agent]
	f.mu.Unlock()
	if reason == "cannot start" {
		return CLIRun{}, fmt.Errorf("%s cannot start", o.Agent)
	}
	go func() {
		if fails {
			o.Done(CLIRun{ID: "r-" + o.Agent, Agent: o.Agent, State: "failed", Error: reason})
			return
		}
		o.Watch(CLIRunEvent{Kind: "delta", Text: "answer from " + o.Agent})
		o.Done(CLIRun{ID: "r-" + o.Agent, Agent: o.Agent, State: "done", Summary: "answer from " + o.Agent})
	}()
	return CLIRun{ID: "r-" + o.Agent, Agent: o.Agent, State: "running"}, nil
}

func (f *fakeEngines) tried() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func chainHarness(t *testing.T, list []backend.QueenEngineEntry, fail map[string]string) (*engineHarness, *fakeEngines) {
	t.Helper()
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.SetQueenChain(list)
	h.a.settings.Hive.PublicURL = "https://ai.example.test"
	f := &fakeEngines{fail: fail}
	prev := queenStartRun
	queenStartRun = f.start
	t.Cleanup(func() { queenStartRun = prev })
	return h, f
}

func on(names ...string) []backend.QueenEngineEntry {
	var out []backend.QueenEngineEntry
	for _, n := range names {
		e := backend.QueenEngineEntry{Engine: strings.TrimPrefix(n, "-"), Enabled: !strings.HasPrefix(n, "-")}
		out = append(out, e)
	}
	return out
}

// The first engine cannot answer, so the second one does, and its answer
// says it answered in the first one's place.
func TestTheNextEngineAnswersWhenTheFirstCannot(t *testing.T) {
	h, f := chainHarness(t, on("claude", "codex.dell", "superleo"), map[string]string{"claude": "Failed to authenticate. API Error: 401"})
	id := h.a.SendChat("c1", "你好", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["engine"] != "codex.dell" || done["inPlaceOf"] != "claude" {
		t.Fatalf("%s %v", name, done)
	}
	if !strings.HasPrefix(fmt.Sprint(done["final"]), inPlaceNote("claude", "codex.dell")) || !strings.HasSuffix(fmt.Sprint(done["final"]), "answer from codex.dell") {
		t.Fatalf("final %q", done["final"])
	}
	if got := fmt.Sprint(f.tried()); got != "[claude codex.dell]" {
		t.Fatalf("tried %s", got)
	}
	if len(h.owned()) != 0 {
		t.Fatal("SuperLeo answered as well")
	}
	if now := h.a.queenEngineNow(); now != "codex.dell" {
		t.Fatalf("now %q", now)
	}
	if tr := h.a.TurnTraces(1); len(tr) != 1 || fmt.Sprint(tr[0].Engines) != "[claude codex.dell]" {
		t.Fatalf("traces %+v", tr)
	}
	handed := false
	for _, e := range h.ev.all("chat:event", id) {
		if e["failed"] == "claude" && e["engine"] == "codex.dell" {
			handed = true
		}
	}
	if !handed {
		t.Fatal("no event says the turn was handed on")
	}
	// History says who answered, and for whom.
	turns := withTurnTimes("c1", []backend.ChatTurn{{Role: "user", Content: "你好"}, {Role: "assistant", Content: "answer from codex.dell"}})
	if turns[1].Engine != "codex.dell" || !strings.HasPrefix(turns[1].Content, inPlaceNote("claude", "codex.dell")) {
		t.Fatalf("history %+v", turns[1])
	}
}

// One that cannot even start hands on the same way, and SuperLeo after it
// answers in the first one's place.
func TestSuperLeoAnswersWhenEveryEngineBeforeItCannot(t *testing.T) {
	h, f := chainHarness(t, on("claude", "codex", "superleo"), map[string]string{"claude": "cannot start", "codex": "turn failed"})
	name, _ := h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if name != "chat:done" {
		t.Fatal(name)
	}
	if got := fmt.Sprint(f.tried()); got != "[claude codex]" {
		t.Fatalf("tried %s", got)
	}
	if own := h.owned(); len(own) != 1 || own[0].standIn != "claude" {
		t.Fatalf("own %v", own)
	}
}

// With none left the turn fails, with every engine's reason.
func TestWhenNoEngineCanAnswerTheTurnFails(t *testing.T) {
	h, f := chainHarness(t, on("claude", "codex", "-superleo"), map[string]string{"claude": "signed out", "codex": "quota gone"})
	name, p := h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	msg := fmt.Sprint(p["error"])
	if name != "chat:error" || !strings.Contains(msg, "signed out") || !strings.Contains(msg, "quota gone") {
		t.Fatalf("%s %v", name, p)
	}
	if got := fmt.Sprint(f.tried()); got != "[claude codex]" {
		t.Fatalf("tried %s", got)
	}
	if len(h.owned()) != 0 {
		t.Fatal("SuperLeo answered while switched off")
	}
}

// Engines switched off are not tried, in front or behind.
func TestEnginesSwitchedOffAreSkipped(t *testing.T) {
	h, f := chainHarness(t, on("-claude", "pi", "-codex", "hermes", "-superleo"), map[string]string{"pi": "down"})
	name, done := h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if name != "chat:done" || done["engine"] != "hermes" || done["inPlaceOf"] != "pi" {
		t.Fatalf("%s %v", name, done)
	}
	if got := fmt.Sprint(f.tried()); got != "[pi hermes]" {
		t.Fatalf("tried %s", got)
	}
}

// SuperLeo first answers with its own model; without one the turn goes to
// the first engine after it.
func TestSuperLeoFirstWithoutAModelHandsTheTurnOn(t *testing.T) {
	h, f := chainHarness(t, on("superleo", "claude"), nil)
	h.ev.wait(t, h.a.SendChat("c1", "你好", nil))
	if len(h.owned()) != 1 || len(f.tried()) != 0 {
		t.Fatalf("own %v tried %v", h.owned(), f.tried())
	}
	h.a.ownTurnFn = nil
	name, done := h.ev.wait(t, h.a.SendChat("c2", "你好", nil))
	if name != "chat:done" || done["engine"] != "claude" {
		t.Fatalf("%s %v", name, done)
	}
}

// A signed-out Claude writes its refusal as a message, fails the turn and
// exits 0. The refusal is not shown as the answer, and the next engine
// takes the turn.
const fakeClaudeRefusesAndExitsZero = `{"type":"system","subtype":"init","session_id":"sess-x"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Failed to authenticate. API Error: 401"}]},"session_id":"sess-x"}
{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate. API Error: 401","session_id":"sess-x"}`

func TestAnAuthRefusalThatExitsZeroHandsTheTurnOn(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeRefusesAndExitsZero, 0)
	id := h.a.SendChat("c1", "你好", nil)
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatal(name)
	}
	if own := h.owned(); len(own) != 1 || own[0].standIn != "claude" {
		t.Fatalf("own %v", own)
	}
	for _, e := range h.ev.all("chat:event", id) {
		if e["type"] == "partial" && strings.Contains(fmt.Sprint(e["content"]), "Failed to authenticate") {
			t.Fatalf("the refusal was written as the answer: %v", e)
		}
	}
}

// A save that changes only the single engine (an older phone app) puts it
// first; otherwise the list is the word. One engine at least stays on.
func TestASaveKeepsTheListAndTheSingleEngineInStep(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	bin, _ := fakeClaude(t, "", 0)
	prev := backend.HiveSettings{}
	prev.SetQueenChain(on("claude", "-codex.dell", "superleo"))
	a := &App{settings: &backend.Settings{Hive: prev}}
	ext := backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"claude": bin}}

	old := backend.Settings{ExternalAgents: ext, Hive: prev}
	old.Hive.QueenEngine = ""
	if err := a.validQueenEngine(&old, prev.QueenEngine); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(old.Hive.QueenEnginesOn()); got != "[superleo]" {
		t.Fatalf("an older client choosing SuperLeo: %s", got)
	}

	list := backend.Settings{ExternalAgents: ext, Hive: prev}
	list.Hive.QueenEngines = on("codex.dell", "claude", "-superleo")
	if err := a.validQueenEngine(&list, prev.QueenEngine); err != nil {
		t.Fatal(err)
	}
	if list.Hive.QueenEngine != "codex.dell" || list.Hive.QueenFallsBack() {
		t.Fatalf("the single engine did not follow the list: %q %v", list.Hive.QueenEngine, list.Hive.QueenFallsBack())
	}

	none := backend.Settings{ExternalAgents: ext, Hive: prev}
	none.Hive.QueenEngines = on("-claude", "-superleo")
	if err := a.validQueenEngine(&none, prev.QueenEngine); err == nil {
		t.Fatal("a list with nothing on was saved")
	}

	away := backend.Settings{ExternalAgents: ext, Hive: prev}
	away.Hive.QueenEngines = on("codex.nowhere", "superleo")
	if err := a.validQueenEngine(&away, prev.QueenEngine); err == nil {
		t.Fatal("an engine this system does not have was saved")
	}
}

// The list shows the saved engines in order, one away as offline, then
// every other engine this system has, switched off.
func TestTheEngineListShowsWhatThereIs(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	bin, _ := fakeClaude(t, "", 0)
	clis.mu.Lock()
	clis.known = false
	clis.mu.Unlock()
	s := &backend.Settings{ExternalAgents: backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"claude": bin}}}
	s.Hive.SetQueenChain(on("claude.dell", "superleo"))
	a := &App{settings: s}
	got := a.QueenEngineList().Engines
	var rows []string
	for _, e := range got {
		rows = append(rows, fmt.Sprintf("%s:%s:%v", e.ID, e.State, e.Enabled))
	}
	// SuperLeo is offline here: the test app has no model.
	if fmt.Sprint(rows) != "[claude.dell:offline:true superleo:offline:true claude:installed:false]" {
		t.Fatalf("%v", rows)
	}
	if got[0].Where != "dell" || got[2].Version == "" || !got[2].Local {
		t.Fatalf("%+v", got)
	}
}

func TestAVersionIsItsNumber(t *testing.T) {
	for in, want := range map[string]string{"2.1.312 (Claude Code)": "2.1.312", "codex-cli 0.165.0": "0.165.0", "1.4.0": "1.4.0", "": ""} {
		if got := versionNumber(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

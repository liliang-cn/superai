package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/trace"
	"google.golang.org/grpc/metadata"
)

func decodeLines(t *testing.T, lines []string) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestAnEntryPointsFlowIsARootRunPairInTheFlowFile(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ctx, f := StartFlow(context.Background(), "chat", "what is on tomorrow")
	if len(f.TraceID()) != 32 {
		t.Fatalf("trace id %q", f.TraceID())
	}
	if sc, ok := trace.SpanFromContext(ctx); !ok || sc.TraceID != f.TraceID() || sc.ParentSpanID != "" {
		t.Fatalf("ctx span %+v, want the root of %s", sc, f.TraceID())
	}
	// An entry point inside it does not split the request.
	inner, g := StartFlow(ctx, "dashboard", "refresh")
	if g.TraceID() != f.TraceID() || FlowFrom(inner) != f {
		t.Fatalf("a nested entry point opened a flow of its own")
	}
	g.End("", errors.New("ignored"))
	// A span of SuperLeo's own is a child.
	_, c := StartSpan(ctx, "coding.claude", "fix it")
	c.End("completed", nil)
	f.End("", nil)
	f.End("failed", errors.New("only the first end counts"))

	got := decodeLines(t, FlowLines(f.TraceID()))
	if len(got) != 4 {
		t.Fatalf("%d lines, want run_start/run_end for the flow and the coding span: %v", len(got), got)
	}
	var root, child []map[string]any
	for _, m := range got {
		switch m["agent"] {
		case "superleo.chat":
			root = append(root, m)
		case "superleo.coding.claude":
			child = append(child, m)
		default:
			t.Fatalf("unexpected line %v", m)
		}
	}
	if len(root) != 2 || root[0]["event"] != "run_start" || root[1]["event"] != "run_end" || root[1]["outcome"] != "completed" {
		t.Fatalf("root lines %v", root)
	}
	if root[0]["goal"] != "what is on tomorrow" || root[0]["parent_span_id"] != nil {
		t.Fatalf("root start %v", root[0])
	}
	if len(child) != 2 || child[0]["parent_span_id"] != root[0]["span_id"] {
		t.Fatalf("the coding span is not the root's child: %v", child)
	}
	if lines := FlowLines("not-a-trace-id"); len(lines) != 0 {
		t.Fatalf("a malformed id read %d lines", len(lines))
	}
}

func TestATurnAskedForOverTheWireContinuesTheCallersFlow(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	caller := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	answered := AcceptFlow("hive-1234", "hive.order", caller, "")
	sc, err := trace.ParseTraceparent(answered)
	if err != nil || sc.TraceID != "0af7651916cd43dd8448eb211c80319c" || sc.SpanID == "b7ad6b7169203331" {
		t.Fatalf("answered %q (%v), want a new span of the caller's trace", answered, err)
	}
	ctx, f := beginTurnFlow(context.Background(), "hive-1234", "check the disks")
	got, _ := trace.SpanFromContext(ctx)
	if got.SpanID != sc.SpanID || got.ParentSpanID != "b7ad6b7169203331" || f.kind != "hive.order" {
		t.Fatalf("turn span %+v kind %q, want the span answered (%s) under the caller", got, f.kind, sc.SpanID)
	}
	f.End("", nil)
	// Taken once: the next turn in that conversation is a flow of its own.
	_, g := beginTurnFlow(context.Background(), "hive-1234", "again")
	if g.TraceID() == sc.TraceID {
		t.Fatalf("the accepted flow was used twice")
	}
	g.End("", nil)
	// A phone's turn with no caller span is still a flow of its kind.
	if tp := AcceptFlow("s-1", "device", "", ""); tp == "" {
		t.Fatalf("no span for a device turn")
	}
	_, d := beginTurnFlow(context.Background(), "s-1", "hi")
	if d.kind != "device" {
		t.Fatalf("kind %q", d.kind)
	}
	d.End("", nil)
	if AcceptFlow("s-2", "", "", "") != "" || AcceptFlow("", "device", caller, "") != "" {
		t.Fatalf("accepted a flow with nothing to continue")
	}
}

func TestACortexCallCarriesItsSpanAndIsTracedUnderTheRun(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	if _, _, ok := cortexOutgoing(context.Background()); ok {
		t.Fatalf("a call outside any flow was given a span")
	}
	ctx, f := StartFlow(context.Background(), "chat", "recall")
	run, _ := trace.SpanFromContext(ctx)
	cctx, sc, ok := cortexOutgoing(ctx)
	if !ok || sc.ParentSpanID != run.SpanID || sc.TraceID != run.TraceID {
		t.Fatalf("call span %+v under %+v", sc, run)
	}
	md, _ := metadata.FromOutgoingContext(cctx)
	if got := md.Get("traceparent"); len(got) != 1 || got[0] != sc.Traceparent() {
		t.Fatalf("metadata traceparent %v, want %s", got, sc.Traceparent())
	}
	cortexCallStart(sc, "/cortexdb.v1.MemoryService/SearchMemory", f.started)
	cortexCallEnd(sc, "/cortexdb.v1.MemoryService/SearchMemory", 0, nil, []string{"00-" + run.TraceID + "-1111111111111111-01"})
	f.End("", nil)
	var start, end map[string]any
	for _, m := range decodeLines(t, FlowLines(run.TraceID)) {
		switch m["event"] {
		case "tool_start":
			start = m
		case "tool_end":
			end = m
		}
	}
	if start == nil || start["tool"] != "cortexdb.SearchMemory" || start["parent_span_id"] != run.SpanID {
		t.Fatalf("tool_start %v", start)
	}
	if end == nil || end["span_id"] != sc.SpanID || !strings.Contains(end["remote"].([]any)[0].(string), "1111111111111111") {
		t.Fatalf("tool_end %v", end)
	}
}

func TestAFlowTakesInThePeersHalf(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ctx, f := StartFlow(context.Background(), "chat", "ask the worker")
	tool, _ := trace.StartSpan(ctx)
	notePeer(tool, "w1", "")
	f.End("", nil)
	peerLine := `{"ts":"2099-01-01T00:00:00Z","event":"run_start","trace_id":"` + f.TraceID() + `","span_id":"2222222222222222","agent":"SuperLeo"}`
	asked := ""
	lines := FlowTrace(context.Background(), f.TraceID(), func(_ context.Context, peer, id string) ([]string, error) {
		asked = peer
		return []string{peerLine, `{"event":"run_start","trace_id":"ffffffffffffffffffffffffffffffff"}`}, nil
	})
	if asked != "w1" {
		t.Fatalf("asked %q, want the peer the flow handed work to", asked)
	}
	if len(lines) != 4 || lines[3] != peerLine {
		t.Fatalf("lines %v, want ours then the peer's (and not another flow's)", lines)
	}
}

func TestRotatingFileKeepsWhatItWasToldTo(t *testing.T) {
	dir := t.TempDir()
	r, err := openRotating(dir+"/flows.jsonl", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Close()
	for _, want := range []string{"flows.jsonl", "flows.1.jsonl", "flows.2.jsonl"} {
		if info, err := os.Stat(dir + "/" + want); err != nil || info.Size() != 11 {
			t.Fatalf("%s: %v", want, err)
		}
	}
	if _, err := os.Stat(dir + "/flows.3.jsonl"); err == nil {
		t.Fatalf("kept more than it was told to")
	}
	if r.rotated(3) != dir+"/flows.3.jsonl" {
		t.Fatalf("rotated name %s", r.rotated(3))
	}
}

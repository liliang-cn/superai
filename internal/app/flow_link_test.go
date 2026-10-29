package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/trace"
	"github.com/liliang-cn/superleo/internal/backend"
)

// An order core hands an agent over the link carries core's flow, and the
// agent answers with the span its turn will be: a child of core's call.
func TestTheFlowCrossesTheAgentLinkBothWays(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	core := NewApp()
	addr := startCore(t, core)

	agent := NewApp()
	turns := make(chan string, 1)
	agent.ownTurnFn = func(requestID, session, message, standIn string) { turns <- session }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "mac3", Version: "test"})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the agent to connect", func() bool { return core.agents().get("mac3") != nil })
	c := core.agents().get("mac3")

	fctx, flow := backend.StartFlow(context.Background(), "chat", "ask mac3")
	callCtx, call := trace.StartSpan(fctx)
	callCtx, links := trace.WithLinks(callCtx)
	var id string
	if err := c.call(callCtx, "SendChat", []any{backend.HiveSessionPrefix + "t1", "check the disk", []string{}}, &id); err != nil || id == "" {
		t.Fatalf("SendChat over the link: %q %v", id, err)
	}
	if s := <-turns; s != backend.HiveSessionPrefix+"t1" {
		t.Fatalf("turn in %q", s)
	}
	got := links.List()
	if len(got) != 1 {
		t.Fatalf("children noted %v, want the agent's turn span", got)
	}
	sc, err := trace.ParseTraceparent(got[0])
	if err != nil || sc.TraceID != flow.TraceID() || sc.SpanID == call.SpanID {
		t.Fatalf("answered %q, want a new span of flow %s", got[0], flow.TraceID())
	}
	flow.End("", nil)

	// The agent's half of the flow, for core's timeline.
	var lines []string
	if err := c.call(context.Background(), "FlowLines", []any{flow.TraceID()}, &lines); err != nil {
		t.Fatalf("FlowLines over the link: %v", err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], flow.TraceID()) {
		t.Fatalf("flow lines %v", lines)
	}
}

// /api/rpc takes the caller's traceparent and answers with the span the turn
// will be; a paired device's turn is a flow of its own kind.
func TestRPCAcceptsTheCallersFlow(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := NewApp()
	a.ownTurnFn = func(requestID, session, message, standIn string) {}
	body, _ := json.Marshal([]any{"s-rpc", "hello", []string{}})
	r := httptest.NewRequest(http.MethodPost, "/api/rpc/SendChat", bytes.NewReader(body))
	caller := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	r.Header.Set("traceparent", caller)
	w := httptest.NewRecorder()
	rpcCall(a, w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	sc, err := trace.ParseTraceparent(w.Header().Get("traceparent"))
	if err != nil || sc.TraceID != "0af7651916cd43dd8448eb211c80319c" || sc.SpanID == "b7ad6b7169203331" {
		t.Fatalf("answered %q (%v)", w.Header().Get("traceparent"), err)
	}

	// No caller span and no device: nothing to answer.
	r = httptest.NewRequest(http.MethodPost, "/api/rpc/SendChat", bytes.NewReader(body))
	w = httptest.NewRecorder()
	rpcCall(a, w, r)
	if w.Header().Get("traceparent") != "" {
		t.Fatalf("answered a span for a call in no flow")
	}
	// A device: a flow of its own.
	r = httptest.NewRequest(http.MethodPost, "/api/rpc/SendChat", bytes.NewReader(body))
	r = r.WithContext(withClientKind(r.Context(), "device"))
	w = httptest.NewRecorder()
	rpcCall(a, w, r)
	if _, err := trace.ParseTraceparent(w.Header().Get("traceparent")); err != nil {
		t.Fatalf("no span for a device's turn: %v", err)
	}
}

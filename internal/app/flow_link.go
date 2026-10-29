package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/liliang-cn/agent-go/v3/pkg/trace"
	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A flow arriving through a method call.
//
// /api/rpc and the agent link run App methods by name, with JSON arguments and
// no context, and the methods that start work (SendChat) return before the
// work begins. So the caller's trace context is accepted here, against the
// conversation the call names, before the method runs: backend.AcceptFlow
// mints the span the turn will be, and that span goes back to the caller —
// a response header, the reply's traceparent — so the caller records it as
// its call's child (backend/flow.go).

// flowMethods are the methods that start a turn, by which argument names
// its conversation.
var flowMethods = map[string]int{"SendChat": 0, "SendCallChat": 0}

// acceptCallFlow notes the flow (and the entry kind, "" for the default) a
// call to method arrives in, and returns the traceparent of the span its
// work will run as, or "" when there is nothing to continue.
func acceptCallFlow(method string, raw []json.RawMessage, kind, traceparent, tracestate string) string {
	i, ok := flowMethods[method]
	if !ok || i >= len(raw) || (traceparent == "" && kind == "") {
		return ""
	}
	var session string
	if json.Unmarshal(raw[i], &session) != nil || session == "" {
		return ""
	}
	return backend.AcceptFlow(session, kind, traceparent, tracestate)
}

type clientKindKey struct{}

// withClientKind marks a request as coming from kind of client: "device" for
// a paired phone or watch.
func withClientKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, clientKindKey{}, kind)
}

func clientKindFrom(ctx context.Context) string {
	k, _ := ctx.Value(clientKindKey{}).(string)
	return k
}

// acceptHTTPFlow is acceptCallFlow for /api/rpc: the traceparent header and
// the client the request was signed in as. The span answered goes back as
// the response's traceparent header.
func acceptHTTPFlow(w http.ResponseWriter, r *http.Request, method string, raw []json.RawMessage) {
	tp := r.Header.Get(trace.TraceparentHeader)
	if _, err := trace.ParseTraceparent(tp); err != nil {
		tp = ""
	}
	if out := acceptCallFlow(method, raw, clientKindFrom(r.Context()), tp, r.Header.Get(trace.TracestateHeader)); out != "" {
		w.Header().Set(trace.TraceparentHeader, out)
	}
}

// acceptMCPFlow is acceptCallFlow for superleo_ask over MCP: the caller's
// traceparent rides in params._meta, as agent-go sends it. An ask is a flow
// of kind "mcp" whether or not the caller is in one.
func acceptMCPFlow(req *mcp.CallToolRequest, conversation string) string {
	tp, ts := "", ""
	if req != nil && req.Params != nil {
		if m := req.Params.GetMeta(); m != nil {
			tp, _ = m[trace.TraceparentHeader].(string)
			ts, _ = m[trace.TracestateHeader].(string)
		}
	}
	if _, err := trace.ParseTraceparent(tp); err != nil {
		tp, ts = "", ""
	}
	return backend.AcceptFlow(conversation, "mcp", tp, ts)
}

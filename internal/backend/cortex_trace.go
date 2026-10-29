package backend

import (
	"context"
	"os"
	"path"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// The shared brain, inside the flow.
//
// A recall from the shared CortexDB is a gRPC call SuperLeo's memory store
// makes on the run's behalf. Dialled here rather than by agent-go's store, so
// every call:
//
//   - carries the traceparent of the span it was made under in the request
//     metadata, which a CortexDB that reads it (cortexdb#113) continues as a
//     child — the server's own spans join the flow the day it is upgraded;
//   - notes a traceparent the server answers with (response header) as a
//     child of that span (trace.NoteChild);
//   - is written to the flow as a call of its own, cortexdb.<Method>, under
//     the run that made it, so the timeline shows each recall and how long it
//     took even before the server traces anything.

// cortexServiceConfig is agent-go's: the read RPCs retried once on
// UNAVAILABLE, for a brain that fails over behind a floating address.
const cortexServiceConfig = `{"methodConfig":[{
  "name":[{"service":"cortexdb.v1.MemoryService","method":"SearchMemory"},
          {"service":"cortexdb.v1.MemoryService","method":"GetMemory"}],
  "retryPolicy":{"maxAttempts":2,"initialBackoff":"0.05s","maxBackoff":"0.2s",
                 "backoffMultiplier":2,"retryableStatusCodes":["UNAVAILABLE"]}}]}`

// dialCortex dials a remote CortexDB with the flow carried on every call.
func dialCortex(endpoint string, useTLS bool) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if useTLS {
		creds = credentials.NewClientTLSFromCert(nil, "")
	}
	return grpc.NewClient(strings.TrimSpace(endpoint),
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultServiceConfig(cortexServiceConfig),
		grpc.WithChainUnaryInterceptor(cortexUnaryTrace),
		grpc.WithChainStreamInterceptor(cortexStreamTrace))
}

// cortexOutgoing puts the span of a call made under ctx on its metadata: a
// child span of whatever ctx is in, so the call is a span of its own. ok is
// false when ctx is in no flow, and the call goes as it would have.
func cortexOutgoing(ctx context.Context) (context.Context, trace.SpanContext, bool) {
	if _, ok := trace.SpanFromContext(ctx); !ok {
		return ctx, trace.SpanContext{}, false
	}
	cctx, sc := trace.StartSpan(ctx)
	pairs := []string{trace.TraceparentHeader, sc.Traceparent()}
	if sc.TraceState != "" {
		pairs = append(pairs, trace.TracestateHeader, sc.TraceState)
	}
	return metadata.AppendToOutgoingContext(cctx, pairs...), sc, true
}

func cortexUnaryTrace(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	cctx, sc, ok := cortexOutgoing(ctx)
	if !ok {
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	var hdr metadata.MD
	opts = append(opts, grpc.Header(&hdr))
	started := time.Now()
	cortexCallStart(sc, method, started)
	err := invoker(cctx, method, req, reply, cc, opts...)
	var remote []string
	for _, tp := range hdr.Get(trace.TraceparentHeader) {
		trace.NoteChild(ctx, tp)
		remote = append(remote, tp)
	}
	cortexCallEnd(sc, method, time.Since(started), err, remote)
	return err
}

func cortexStreamTrace(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	cctx, sc, ok := cortexOutgoing(ctx)
	if !ok {
		return streamer(ctx, desc, cc, method, opts...)
	}
	started := time.Now()
	cortexCallStart(sc, method, started)
	cs, err := streamer(cctx, desc, cc, method, opts...)
	// A stream's span ends when it is opened: what follows is reading it,
	// which is the caller's time, not the server's.
	cortexCallEnd(sc, method, time.Since(started), err, nil)
	return cs, err
}

// cortexName is "cortexdb.SearchMemory" for "/cortexdb.v1.MemoryService/SearchMemory".
func cortexName(method string) string {
	return "cortexdb." + path.Base(method)
}

func cortexCallStart(sc trace.SpanContext, method string, at time.Time) {
	writeFlowLine(flowLine{TS: at.Format(time.RFC3339Nano), Event: "tool_start", TraceID: sc.TraceID,
		SpanID: sc.SpanID, ParentSpanID: sc.ParentSpanID, CallID: sc.SpanID, Tool: cortexName(method),
		Host: flowHost, PID: os.Getpid()})
}

func cortexCallEnd(sc trace.SpanContext, method string, took time.Duration, err error, remote []string) {
	l := flowLine{Event: "tool_end", TraceID: sc.TraceID, SpanID: sc.SpanID, ParentSpanID: sc.ParentSpanID,
		CallID: sc.SpanID, Tool: cortexName(method), DurationMs: took.Milliseconds(), Remote: remote}
	if err != nil {
		l.Error = clipFlow(err.Error(), 300)
	}
	writeFlowLine(l)
	ctx := trace.ContextWithSpan(context.Background(), sc)
	if err != nil {
		Logger("cortexdb").WarnContext(ctx, "cortexdb call failed", "method", cortexName(method), "ms", took.Milliseconds(), "error", l.Error)
		return
	}
	Logger("cortexdb").DebugContext(ctx, "cortexdb call", "method", cortexName(method), "ms", took.Milliseconds())
}

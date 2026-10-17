package app

// Who is on the other end of /mcp, and what they may do there.
//
// Two kinds of caller reach /mcp. Whoever holds the full token, a paired
// device or a signed-in browser gets every tool, as before. A queen token
// (integrations.go) is an agent running the queen's thinking from outside, and
// it gets the cluster-command tools only: the hive, the machines, the
// schedules, the task board, the pending approvals and the conversations. Not
// superai_ask (that is asking herself), not settings, not bees or devices.
//
// The two are two MCP servers behind two streamable handlers, picked per
// request by the scope requireAuth put on the request. Separate handlers mean
// separate session tables, so a session opened with the full token is not
// found when the next request carries a queen token. The queen's server also
// filters tools/list and refuses tools/call for anything off its list, so a
// tool added to the full server later does not quietly become hers.
//
// Destructive calls pass SuperAI's approval gate here, whoever makes them,
// exactly as the queen's own run passes it (mcpGate below).

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpScopeQueen = "queen"

type mcpScopeKey struct{}

func withMCPScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, mcpScopeKey{}, scope)
}

func mcpScopeFrom(ctx context.Context) string {
	s, _ := ctx.Value(mcpScopeKey{}).(string)
	return s
}

// queenMCPTools is everything a queen token may call.
var queenMCPTools = []string{
	// the hive
	"superai_hive_members", "superai_hive_spawn", "superai_hive_retire", "superai_hive_map",
	"superai_hive_command", "superai_hive_send", "superai_hive_inbox", "superai_hive_delegate",
	"superai_hive_tasks", "superai_hive_task_cancel",
	// the machines, and how the cluster is doing
	"superai_machine_inspect", "superai_machine_act", "superai_linked_agents", "superai_status", "superai_attention",
	// schedules
	"superai_schedule_create", "superai_schedule_list", "superai_schedule_enable",
	"superai_schedule_delete", "superai_schedule_run_now",
	// approvals, read only, and the conversations
	"superai_approvals_pending", "superai_conversations", "superai_chat_history",
}

// onlyTools is the queen server's enforcement: tools/list shows the allowed
// tools only, and tools/call for any other is an error. A request it cannot
// read is refused rather than passed.
func onlyTools(allowed []string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				r, ok := req.(*mcp.CallToolRequest)
				if !ok || r.Params == nil {
					return nil, fmt.Errorf("unreadable tool call refused")
				}
				if !slices.Contains(allowed, r.Params.Name) {
					return nil, fmt.Errorf("tool %q is not available to a queen token", r.Params.Name)
				}
			}
			res, err := next(ctx, method, req)
			if method == "tools/list" && err == nil {
				if lr, ok := res.(*mcp.ListToolsResult); ok {
					kept := lr.Tools[:0:0]
					for _, t := range lr.Tools {
						if slices.Contains(allowed, t.Name) {
							kept = append(kept, t)
						}
					}
					lr.Tools = kept
				}
			}
			return res, err
		}
	}
}

// scopedMCPHandler serves full or queen by the request's scope.
type scopedMCPHandler struct{ full, queen http.Handler }

func (h scopedMCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mcpScopeFrom(r.Context()) == mcpScopeQueen {
		h.queen.ServeHTTP(w, r)
		return
	}
	h.full.ServeHTTP(w, r)
}

// mcpGated maps each /mcp tool that passes the approval gate to the name the
// gate knows it by in the queen's own run, so a remembered rule and the audit
// log read the same whichever way the call came.
var mcpGated = map[string]string{
	"superai_machine_act":      "machine_act",
	"superai_hive_retire":      "hive_retire",
	"superai_hive_task_cancel": "hive_task_cancel",
	"superai_schedule_delete":  "schedule_remove",
}

type approvalByKey struct{}

// withApprovalBy names who is asking, for the card, when the gate's request
// does not (an MCP caller has no conversation to be recognised by).
func withApprovalBy(ctx context.Context, by string) context.Context {
	return context.WithValue(ctx, approvalByKey{}, by)
}

func approvalByFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(approvalByKey{}).(string)
	return s
}

// mcpToolGate is the gate the queen's run uses; a test may put its own.
func (a *App) mcpToolGate() *backend.ToolGate {
	if a.gateForMCP != nil {
		return a.gateForMCP
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.ToolGate()
}

// mcpGate puts one /mcp call through the approval gate: the policy decides
// whether it is asked about (machine_act always, hive_retire when forced,
// task cancel always, schedule delete by its name), and the handler asks the
// person on the phone and waits, as for the queen's own call. It returns nil
// when the call may go ahead, or the refusal to hand back. Nothing on /mcp
// can answer the card.
func (a *App) mcpGate(ctx context.Context, caller, tool string, args map[string]any) *mcp.CallToolResult {
	name, gated := mcpGated[tool]
	if !gated {
		return nil
	}
	gate := a.mcpToolGate()
	if gate == nil {
		return selfErr(tool + " needs approval and the approval gate is not running (the backend is not ready); nothing was done")
	}
	req := agent.PermissionRequest{ToolName: name, ToolArgs: args, Destructive: true}
	if !gate.Policy()(req) {
		return nil
	}
	resp, err := gate.Handler()(withApprovalBy(ctx, caller), req)
	if err != nil {
		return selfErr("could not ask for approval: " + err.Error())
	}
	if resp == nil || !resp.Allowed {
		reason := "not approved"
		if resp != nil && resp.Reason != "" {
			reason = resp.Reason
		}
		return selfErr(fmt.Sprintf("%s was not done: %s", tool, reason))
	}
	return nil
}

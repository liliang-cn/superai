package backend

import (
	"context"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// registerNotifyTool gives the model a way to send standalone interim messages
// to the user in the middle of a long multi-step turn. Delivery rides the
// tool_call event that already streams to the frontend (chat:event with
// type=tool_call, tool=notify_user, args.message), so the handler itself has
// nothing to transport — it only acknowledges.
func (s *Service) registerNotifyTool() {
	s.svc.AddToolWithMetadata("notify_user",
		"Send the user a standalone interim message partway through a long task: an intermediate conclusion, a confirmed partial result, an important finding, or a change of plan. They see it immediately, without waiting for the final answer. Still give the final answer as usual — do not use this tool in place of it, and do not repeat what you have already sent. Short tasks do not need it.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send the user; Markdown is supported",
				},
			},
			"required": []string{"message"},
		},
		func(context.Context, map[string]any) (any, error) {
			return map[string]any{"delivered": true}, nil
		},
		agent.ToolMetadata{
			ConcurrencySafe:   true,
			InterruptBehavior: agent.InterruptBehaviorCancel,
		},
	)
}

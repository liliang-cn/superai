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
		"在长任务中途给用户发一条独立的即时消息：阶段性结论、已确认的部分结果、重要发现或计划变更。用户会立刻看到它，不用等最终答案。最终答案仍需照常给出——不要用本工具代替最终答案，也不要重复已发过的内容。简短任务不需要它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "要发给用户的消息，支持 Markdown",
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

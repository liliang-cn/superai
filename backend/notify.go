package backend

import (
	"context"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Notifier exposes the outbound webhook to whoever owns the process — the app
// and the daemon both post scheduled runs through it. Nil-safe by way of
// Notifier's own methods, so callers never branch on it.
func (s *Service) Notifier() *Notifier {
	if s == nil {
		return nil
	}
	return s.notifier
}

// registerNotifyTool gives the model a way to send standalone interim messages
// to the user in the middle of a long multi-step turn.
//
// Delivery has two halves. In-app it rides the tool_call event that already
// streams to the frontend (chat:event with type=tool_call, tool=notify_user,
// args.message), so a page that is open shows it with nothing more to do here.
// That was the whole implementation for a while, and it meant the message
// reached exactly the people who were already watching. The webhook is the
// other half: it goes out to whoever is not.
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
		func(ctx context.Context, args map[string]any) (any, error) {
			message, _ := args["message"].(string)
			message = strings.TrimSpace(message)

			// Reported as delivered either way: the SSE half has already
			// happened by the time this handler runs, and a webhook that is
			// down is not a reason to tell the model its message was lost —
			// it would only send it again.
			s.Notifier().Send(ctx, WebhookPayload{
				Event:   WebhookEventNotify,
				Message: message,
			})
			return map[string]any{"delivered": true}, nil
		},
		agent.ToolMetadata{
			ConcurrencySafe:   true,
			InterruptBehavior: agent.InterruptBehaviorCancel,
		},
	)
}

// NotifyScheduledRun posts a finished scheduled run to the webhook.
//
// Shared by the app and the daemon because they observe the same scheduler in
// turn — whichever process holds the timer lock is the one that has to send —
// and a reminder that reads differently depending on which one fired it is a
// bug the user would have to reproduce twice to see.
func (s *Service) NotifyScheduledRun(ctx context.Context, run agent.PromptRun) {
	n := s.Notifier()
	if !n.Enabled() {
		return
	}

	// The persona ends every answer with a trailing "情绪: X" tag that drives the
	// avatar. The chat transcript peels it off before display; a webhook that
	// did not would put an internal marker at the bottom of every message the
	// user reads in Telegram.
	message, _ := SplitEmotion(run.Answer)
	message = strings.TrimSpace(message)
	payload := WebhookPayload{
		Event:     WebhookEventSchedule,
		Source:    strings.TrimSpace(run.Prompt),
		Cancelled: run.Cancelled,
	}
	switch {
	case run.Cancelled:
		// A stop is the user's own doing, so it is an outcome and not a fault —
		// the same rule onScheduledRun follows for the transcript.
		payload.Message = "Cancelled"
	case run.Err != nil:
		payload.Error = run.Err.Error()
		payload.Message = "Run failed: " + run.Err.Error()
	case message == "":
		payload.Message = "Scheduled task finished"
	default:
		payload.Message = message
	}
	n.Send(ctx, payload)
}

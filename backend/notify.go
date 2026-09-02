package backend

import (
	"context"
	"strings"
	"time"

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

// Notices is the fan-out every message goes through. Nil-safe by way of its own
// methods, so callers never branch on it.
func (s *Service) Notices() *Notices {
	if s == nil {
		return nil
	}
	return s.notices
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

			// Pushed: the whole point of an interim message is that the task is
			// long, which is exactly when the person who asked for it has gone
			// to do something else.
			s.Notices().Raise(ctx, Notice{
				Level:   LevelInfo,
				Message: message,
				Push:    true,
			})
			// Reported as delivered either way. The in-page half has already
			// happened by the time this returns, and a webhook that is down is
			// not a reason to tell the model its message was lost — it would
			// only send it again.
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
	// The persona ends every answer with a trailing "情绪: X" tag that drives the
	// avatar. The chat transcript peels it off before display; anything that did
	// not would put an internal marker at the bottom of every message the user
	// reads in Telegram.
	message, _ := SplitEmotion(run.Answer)
	message = strings.TrimSpace(message)

	notice := Notice{
		Source:  strings.TrimSpace(run.Prompt),
		Session: run.SessionID,
		// One toast per run. A run that reports twice — the observer and a
		// retry, say — should replace its own toast rather than stack a second.
		Key:  "run:" + run.SessionID + ":" + run.StartedAt.Format(time.RFC3339Nano),
		Push: true,
	}
	switch {
	case run.Cancelled:
		// A stop is the user's own doing, so it is an outcome and not a fault —
		// the same rule the transcript follows. It is also the one case not
		// pushed: the user was at the machine, they pressed the button, and a
		// message telling them what they just did is noise.
		notice.Level = LevelInfo
		notice.Message = "Cancelled"
		notice.Push = false
	case run.Err != nil:
		notice.Level = LevelError
		notice.Message = "Run failed: " + run.Err.Error()
	case message == "":
		notice.Level = LevelSuccess
		notice.Message = "Scheduled task finished"
	default:
		notice.Level = LevelSuccess
		notice.Message = message
	}
	s.Notices().Raise(ctx, notice)
}

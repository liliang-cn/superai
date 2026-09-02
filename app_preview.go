package main

import (
	"context"
	"strings"

	"github.com/liliang-cn/superai-desktop/backend"
)

// PreviewPrompt returns the turn SendChat would send for this message, without
// sending it: no model call, no session write, no reminder set on the way past.
//
// It takes the composer's session id and text and nothing else, because that
// is what the send button has in hand at the moment somebody would want to
// look first.
func (a *App) PreviewPrompt(sessionID, goal string) backend.PromptPreview {
	a.mu.Lock()
	svc := a.svc
	buildErr := a.buildErr
	a.mu.Unlock()

	if svc == nil {
		return backend.PromptPreview{
			SessionID:    sessionID,
			Messages:     []backend.PreviewMessage{},
			Tools:        []string{},
			Deliverables: []string{},
			Error:        "backend not ready: " + buildErr,
		}
	}
	return svc.PreviewPrompt(context.Background(), sessionID, strings.TrimSpace(goal))
}

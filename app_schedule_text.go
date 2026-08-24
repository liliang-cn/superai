package main

// Arranging a schedule by saying it.
//
// The Schedules page used to be a form: presets, a cadence row, a time picker
// and a cron preview — six controls to express what one sentence already says.
// The agent has tools for this and a model is good at turning "每天早八点" into a
// cron, so the page is a box you talk to and this is what it talks to.
//
// Synchronous on purpose. The caller is a form, not a chat: it wants the answer
// and the list refresh, not a stream of tokens.

import (
	"context"
	"strings"
	"time"
)

// scheduleTextSession keeps this out of the user's own conversations: it is
// housekeeping, and it should not be what someone finds when they scroll back
// through a chat looking for something they said.
const scheduleTextSession = "schedules"

// scheduleTextTimeout bounds one arrangement. Creating a schedule is a couple of
// tool calls; anything longer has gone wrong somewhere the user cannot see.
const scheduleTextTimeout = 3 * time.Minute

// scheduleTextFraming tells the model what job it is doing. Without it a bare
// "每天早八点看看部署" reads as a request to look at the deployment now.
const scheduleTextFraming = `The user is arranging something to run on a clock, not asking for it now.

Use schedule_prompt, once. It takes a cron and can express any cadence —
weekdays, every few hours, a day of the month — which set_reminder cannot, and
calling both leaves two schedules where the user asked for one. Do not call
set_reminder from here at all.

Then reply in one or two sentences: what will run, when it next runs, nothing
else. If the timing is genuinely ambiguous — "later", "sometimes" — ask for the
missing piece instead of guessing a time.

What they said:
`

// ScheduleFromText hands one sentence to the agent and lets it arrange the
// schedule with its own tools. It returns the agent's own words.
func (a *App) ScheduleFromText(text string) map[string]any {
	text = strings.TrimSpace(text)
	if text == "" {
		return map[string]any{"ok": false, "error": "say what should happen, and when"}
	}

	a.mu.Lock()
	svc := a.svc
	buildErr := a.buildErr
	a.mu.Unlock()
	if svc == nil {
		return map[string]any{"ok": false, "error": "backend not ready: " + buildErr}
	}

	ctx, cancel := context.WithTimeout(context.Background(), scheduleTextTimeout)
	defer cancel()

	answer, err := svc.Stream(ctx, scheduleTextSession, scheduleTextFraming+text, nil, nil)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "answer": answer}
	}
	return map[string]any{"ok": true, "answer": strings.TrimSpace(answer)}
}

package app

import (
	"strings"
	"sync"
	"time"
)

// The person's one conversation with the queen.
//
// Every client used to keep a console of its own — "hive-console-web" for the
// browser and the Mac, "hive-console-<device id>" for each phone and pad — and
// hid the others', so what was said to the queen on the iPad was nowhere on
// the iPhone. There is one person and one queen, so there is one
// conversation, and the core names it: a client asks QueenConsole and opens
// what it answers, falling back to the same constant against a core too old
// to have the call.
//
// Two devices may talk in it at once. The second turn waits behind the first
// (turn_queue.go), and every client watching the conversation sees both:
// chat:turn announces each turn as it is taken, with the words that started
// it, and every chat:* event of the turn carries the conversation's id, so a
// client can follow a turn it did not start.
//
// The old per-device consoles are left as they are. Clients list them as
// ordinary conversations; nothing is merged or rewritten.

// QueenConsoleSession is the shared conversation's id.
const QueenConsoleSession = "hive-console"

// QueenConsole names the conversation every client opens as the queen's.
func (a *App) QueenConsole() map[string]string {
	return map[string]string{"session": QueenConsoleSession}
}

// turnSessions maps a running turn's request id to its conversation, for
// stamping the turn's events. Kept apart from runs: a turn routed to another
// agent or to a CLI is never tracked there, and its events need the stamp too.
type turnSessions struct{ m sync.Map }

// announceTurn records the turn's conversation and tells every client a turn
// was taken in it.
func (a *App) announceTurn(requestID, sessionID, message string, images int) {
	if sessionID == "" {
		return
	}
	a.turnSess.m.Store(requestID, sessionID)
	p := map[string]any{
		"requestId": requestID,
		"sessionId": sessionID,
		"message":   message,
		"at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if images > 0 {
		p["images"] = images
	}
	a.emit("chat:turn", p)
}

// withTurnSession puts the conversation's id on a turn's chat:* event that
// does not carry it, and forgets the turn once the event is its last. The
// payload is copied, not changed: the caller may still hold it.
func (a *App) withTurnSession(name string, payload map[string]any) map[string]any {
	if !strings.HasPrefix(name, "chat:") || name == "chat:turn" {
		return payload
	}
	id, _ := payload["requestId"].(string)
	if id == "" {
		return payload
	}
	v, ok := a.turnSess.m.Load(id)
	if !ok {
		return payload
	}
	switch name {
	case "chat:done", "chat:error", "chat:cancelled":
		a.turnSess.m.Delete(id)
	}
	if _, has := payload["sessionId"]; has {
		return payload
	}
	out := make(map[string]any, len(payload)+1)
	for k, val := range payload {
		out[k] = val
	}
	out["sessionId"] = v
	return out
}

package app

import (
	"context"
	"time"
)

// A second ask while the queen is still answering the first.
//
// It waits its turn in that conversation and then goes where it would have
// gone with the conversation free: the queen engine for a person's turn when
// one is set, this app's model otherwise. It used to go to an idle worker, and
// a worker has no agent link and none of the queen's machine tools: asked to
// update something on the Mac while the queen was doing just that, one
// answered that the Mac was not connected. Workers still do the pieces the
// queen hands them with hive_delegate.
//
// Waiting also means the second ask sees how the first one ended, which a
// question typed in the middle of an answer is usually about.

// queuedNote is what the client shows on a turn while it waits.
const queuedNote = "排在上一条后面…"

// queueWaitPoll bounds how long a waiting turn sleeps before it looks again,
// should a turn end without the wake-up (it is sent from untrackRun).
const queueWaitPoll = time.Second

// queueTurn holds a turn behind the one running in its conversation and runs
// start once the conversation is free and the turns queued before it have
// started. It reports false, and does nothing, when the conversation is free
// with nobody waiting: the caller then starts the turn itself.
//
// While it waits, the turn is tracked without a conversation (so it does not
// make the conversation busy itself) and a stop ends the wait.
func (a *App) queueTurn(requestID, sessionID string, start func()) bool {
	if sessionID == "" {
		// No conversation to wait in; and "" is also what a tracked turn
		// that belongs to none has as its session.
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	held := &chatRun{cancel: cancel}

	a.runMu.Lock()
	if !a.sessionBusyLocked(sessionID) && len(a.turnQueue[sessionID]) == 0 {
		a.runMu.Unlock()
		cancel()
		return false
	}
	if a.runs == nil {
		a.runs = make(map[string]*chatRun)
	}
	if a.turnQueue == nil {
		a.turnQueue = make(map[string][]string)
	}
	a.runs[requestID] = held
	a.turnQueue[sessionID] = append(a.turnQueue[sessionID], requestID)
	a.runMu.Unlock()

	a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update", "content": queuedNote})

	go func() {
		defer cancel()
		for {
			a.runMu.Lock()
			if ctx.Err() != nil {
				a.leaveQueueLocked(requestID, sessionID, held)
				a.runMu.Unlock()
				a.emit("chat:cancelled", map[string]any{"requestId": requestID, "final": ""})
				return
			}
			if q := a.turnQueue[sessionID]; len(q) > 0 && q[0] == requestID && !a.sessionBusyLocked(sessionID) {
				// Claimed: the conversation is busy with this turn from here, so
				// the next one in line keeps waiting while it starts.
				held.session = sessionID
				a.runMu.Unlock()
				break
			}
			freed := a.runFreedLocked()
			a.runMu.Unlock()
			select {
			case <-freed:
			case <-ctx.Done():
			case <-time.After(queueWaitPoll):
			}
		}

		// start tracks the turn under the same id, which replaces held.
		start()

		a.runMu.Lock()
		a.leaveQueueLocked(requestID, sessionID, held)
		a.runMu.Unlock()
	}()
	return true
}

// leaveQueueLocked takes the turn out of its conversation's line and forgets
// its waiting entry, unless the turn has been tracked for real since. Either
// way the line has moved, so the turns waiting in it look again.
func (a *App) leaveQueueLocked(requestID, sessionID string, held *chatRun) {
	q := a.turnQueue[sessionID]
	for i, id := range q {
		if id == requestID {
			q = append(q[:i:i], q[i+1:]...)
			break
		}
	}
	if len(q) == 0 {
		delete(a.turnQueue, sessionID)
	} else {
		a.turnQueue[sessionID] = q
	}
	if a.runs[requestID] == held {
		delete(a.runs, requestID)
	}
	a.wakeQueueLocked()
}

// queuedTurns is how many turns are waiting in a conversation.
func (a *App) queuedTurns(sessionID string) int {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return len(a.turnQueue[sessionID])
}

// runFreedLocked is closed the next time a tracked turn ends or the line moves.
func (a *App) runFreedLocked() <-chan struct{} {
	if a.runFreed == nil {
		a.runFreed = make(chan struct{})
	}
	return a.runFreed
}

// wakeQueueLocked wakes every turn waiting for a conversation to come free.
func (a *App) wakeQueueLocked() {
	if a.runFreed != nil {
		close(a.runFreed)
		a.runFreed = nil
	}
}

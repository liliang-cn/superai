package main

import (
	"github.com/liliang-cn/superai-desktop/backend"
)

// The notification centre, from the window's side.
//
// Everything it shows was already published through Notices; what is new is
// that it is still there afterwards. A toast is for the person watching, and
// the messages worth a centre are precisely the ones raised when nobody was —
// a schedule that ran overnight, a reminder that came due, a task that failed
// on the second of its eight segments.

// notificationsShown caps one read. The store keeps a few hundred; a panel
// nobody scrolls to the bottom of does not need all of them at once.
const notificationsShown = 100

// inbox is the store, whether or not the backend came up.
//
// The service's own inbox when there is one, so both halves of the process are
// looking at the same object; the file itself otherwise. Either way it is the
// same path.
func (a *App) inbox() *backend.Inbox {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if b := svc.Inbox(); b != nil {
		return b
	}
	return backend.OpenInbox()
}

// Notifications lists what has been said, newest first.
func (a *App) Notifications() []backend.Notification {
	return a.inbox().List(notificationsShown)
}

// UnreadNotifications is the number for the badge.
//
// Its own call rather than a field on the status: the badge is polled by
// whatever is on screen, and making it part of GetStatus would tie the count
// to a call that rebuilds far more than a number.
func (a *App) UnreadNotifications() int {
	return a.inbox().Unread()
}

// MarkNotificationsRead marks the given ids read, or all of them when the list
// is empty — which is what opening the centre does.
func (a *App) MarkNotificationsRead(ids []string) map[string]any {
	if err := a.inbox().MarkRead(ids...); err != nil {
		return failWith(err.Error())
	}
	return okData(nil)
}

// ClearNotifications empties the centre.
func (a *App) ClearNotifications() map[string]any {
	if err := a.inbox().Clear(); err != nil {
		return failWith(err.Error())
	}
	return okData(nil)
}

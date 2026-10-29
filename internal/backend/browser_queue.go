package backend

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// The browser queue.
//
// One browser is one set of hands: two runs driving it at once open pages
// under each other, click on the other's snapshot refs and type into the
// other's form. In the person's own Chrome every SuperLeo on the machine
// reaches the same browser, and the hive hands a machine as many orders as
// it likes. So a run takes the browser for itself at its first browser call
// and keeps it until the run ends; a second run's first call waits its turn,
// first come first served, and says that it is waiting while it does.
//
// The claim is per browser, not per process: in-process the runs line up
// here, and between processes (the desktop and a node on the same machine,
// the app and the daemon) the one at the front also takes a file lock named
// after the browser, which the kernel lets go of if the process dies.
//
// A claim nobody is using any more is not kept for ever. A run that has not
// touched the browser for browserQueueIdle, or has held it for longer than
// browserQueueMaxHold, loses it as soon as someone else wants it; should it
// reach for the browser again, it queues like anyone. And no one waits
// longer than browserQueueMaxWait: the call is refused with a reason the
// model can pass on.

// BrowserQueued starts what a run waiting for the browser is told, and
// PhaseQueued is the task board's phase for it: the order is in line, not
// working.
// BrowserFree starts what it is told when its turn comes.
const (
	BrowserQueued = "排队中"
	BrowserFree   = "browser free"
	PhaseQueued   = "queued"
)

// browserWantFresh is how recently another process must have said it wants
// the browser (the lock's .want file) for a holder here that went idle to
// let go of it.
const browserWantFresh = 3 * time.Second

// Defaults for the browser queue; variables so a test can shorten them.
var (
	browserQueueIdle    = 3 * time.Minute
	browserQueueMaxHold = 30 * time.Minute
	browserQueueMaxWait = 30 * time.Minute
)

// browserQueue lines up the runs that want one browser.
type browserQueue struct {
	key string
	// lockPath is the file the holder locks so other processes wait too; ""
	// for none.
	lockPath string

	idle, maxHold, maxWait time.Duration

	mu      sync.Mutex
	holder  *browserLease
	waiters []*browserWaiter
}

type browserLease struct {
	owner, label   string
	since, touched time.Time
	file           *os.File      // the cross-process lock, once taken
	done           chan struct{} // closed when the claim ends; stops its watch

	lockOnce sync.Once
	lockErr  error
}

type browserWaiter struct {
	owner, label string
	since        time.Time
	granted      chan struct{} // closed when the browser is this waiter's
}

// BrowserQueueStatus is who has the browser and who waits for it, for the
// page and for diagnosis.
type BrowserQueueStatus struct {
	Holder      string    `json:"holder,omitempty"`
	HolderSince time.Time `json:"holderSince,omitempty"`
	Waiting     []string  `json:"waiting,omitempty"`
}

var (
	browserQueuesMu sync.Mutex
	browserQueues   = map[string]*browserQueue{}
)

// browserQueueFor is the queue of the browser named key. One per process and
// browser, so a service rebuilt by a settings save keeps the line as it was.
func browserQueueFor(key string) *browserQueue {
	browserQueuesMu.Lock()
	defer browserQueuesMu.Unlock()
	q := browserQueues[key]
	if q == nil {
		q = newBrowserQueue(key, browserQueueLockPath(key))
		browserQueues[key] = q
	}
	return q
}

func newBrowserQueue(key, lockPath string) *browserQueue {
	return &browserQueue{
		key:      key,
		lockPath: lockPath,
		idle:     browserQueueIdle,
		maxHold:  browserQueueMaxHold,
		maxWait:  browserQueueMaxWait,
	}
}

// browserQueueLockPath is the machine-wide lock file for a browser: in the
// user's cache, not the data directory, since two SuperLeos with their own
// data directories still share the person's Chrome.
func browserQueueLockPath(key string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "superleo", "browser-"+key+".lock")
}

// browserKey names the browser a runtime drives: the person's Chrome is one
// per machine, a launched one is one per agent-browser session.
func browserKey(mode string, env map[string]string) string {
	if mode == BrowserModeChrome {
		return "chrome"
	}
	return env["AGENT_BROWSER_SESSION"]
}

// acquire returns once owner has the browser, or with an error when ctx ends
// or the wait runs past maxWait. Calling it again while holding is a touch.
// notify, if set, hears that the call is waiting and when it stops waiting.
func (q *browserQueue) acquire(ctx context.Context, owner, label string, notify func(string)) error {
	now := time.Now()
	q.mu.Lock()
	q.reapLocked(now)
	if h := q.holder; h != nil && h.owner == owner {
		h.touched = now
		q.mu.Unlock()
		return q.lockAcrossProcesses(ctx, h, notify)
	}
	if q.holder == nil && len(q.waiters) == 0 {
		h := q.grantLocked(&browserWaiter{owner: owner, label: label, since: now, granted: make(chan struct{})}, now)
		q.mu.Unlock()
		return q.lockAcrossProcesses(ctx, h, notify)
	}
	// One place in line per owner: two of its calls made at once wait as one.
	var w *browserWaiter
	for _, other := range q.waiters {
		if other.owner == owner {
			w = other
			break
		}
	}
	if w == nil {
		w = &browserWaiter{owner: owner, label: label, since: now, granted: make(chan struct{})}
		q.waiters = append(q.waiters, w)
	}
	ahead, busy := len(q.waiters)-1, "another run"
	if q.holder != nil {
		busy = q.holder.label
	}
	q.mu.Unlock()

	msg := fmt.Sprintf("%s: browser %s is in use by %s; waiting for it", BrowserQueued, q.key, busy)
	if ahead > 0 {
		msg += fmt.Sprintf(" (%d ahead)", ahead)
	}
	log.Printf("superleo: browser queue: %s %s", label, msg)
	if notify != nil {
		notify(msg)
	}

	limit := time.NewTimer(q.maxWait)
	defer limit.Stop()
	tick := time.NewTicker(q.reapEvery())
	defer tick.Stop()
	for {
		select {
		case <-w.granted:
			waited := time.Since(w.since).Round(time.Millisecond)
			log.Printf("superleo: browser queue: %s got browser %s after waiting %s", label, q.key, waited)
			if notify != nil {
				notify(fmt.Sprintf("%s: %s is this run's after %s in the queue", BrowserFree, q.key, waited))
			}
			q.mu.Lock()
			h := q.holder
			q.mu.Unlock()
			if h == nil || h.owner != owner {
				// Granted and reaped again before this goroutine woke.
				return fmt.Errorf("lost the browser before using it; try again")
			}
			return q.lockAcrossProcesses(ctx, h, notify)
		case <-tick.C:
			q.mu.Lock()
			q.reapLocked(time.Now())
			q.mu.Unlock()
		case <-ctx.Done():
			q.leave(w)
			log.Printf("superleo: browser queue: %s left the queue for %s: %v", label, q.key, ctx.Err())
			return ctx.Err()
		case <-limit.C:
			q.leave(w)
			q.mu.Lock()
			if q.holder != nil {
				busy = q.holder.label
			}
			q.mu.Unlock()
			log.Printf("superleo: browser queue: %s gave up on %s after %s", label, q.key, q.maxWait)
			return fmt.Errorf("the browser is busy with %s and stayed busy for %s; try again later", busy, q.maxWait)
		}
	}
}

// lockAcrossProcesses takes the browser's file lock for the holder h, waiting
// while another process has it. It happens once per claim; a second call of
// the same run waits for the first and gets the same answer.
func (q *browserQueue) lockAcrossProcesses(ctx context.Context, h *browserLease, notify func(string)) error {
	h.lockOnce.Do(func() { h.lockErr = q.takeFileLock(ctx, h, notify) })
	return h.lockErr
}

func (q *browserQueue) takeFileLock(ctx context.Context, h *browserLease, notify func(string)) error {
	if q.lockPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(q.lockPath), 0o755); err != nil {
		log.Printf("superleo: browser queue: %v; only this process is kept in line", err)
		return nil
	}
	f, err := os.OpenFile(q.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Printf("superleo: browser queue: %v; only this process is kept in line", err)
		return nil
	}
	started, told := time.Now(), false
	for {
		if lockFile(f) == nil {
			_ = f.Truncate(0)
			_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+" "+h.label+"\n"), 0)
			q.mu.Lock()
			if q.holder != h {
				// Taken away while it waited (reaped): not this run's to keep.
				q.mu.Unlock()
				_ = unlockFile(f)
				_ = f.Close()
				return fmt.Errorf("lost the browser while waiting for another process; try again")
			}
			h.file, h.touched = f, time.Now()
			q.mu.Unlock()
			if told {
				log.Printf("superleo: browser queue: %s got browser %s from another process after %s", h.label, q.key, time.Since(started).Round(time.Millisecond))
			}
			return nil
		}
		if !told {
			told = true
			who, _ := os.ReadFile(q.lockPath)
			msg := fmt.Sprintf("%s: browser %s is in use by another SuperLeo process (pid %s); waiting for it", BrowserQueued, q.key, strings.TrimSpace(string(who)))
			log.Printf("superleo: browser queue: %s %s", h.label, msg)
			if notify != nil {
				notify(msg)
			}
		}
		// Said where the other process looks: a holder there that went idle
		// lets go for it (reapLocked).
		_ = os.WriteFile(q.lockPath+".want", []byte(strconv.Itoa(os.Getpid())+" "+h.label+"\n"), 0o600)
		// Still at the front here; waiting on another process is not idling.
		q.mu.Lock()
		if q.holder == h {
			h.touched = time.Now()
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			_ = f.Close()
			q.releaseLease(h)
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if time.Since(started) > q.maxWait {
			_ = f.Close()
			q.releaseLease(h)
			return fmt.Errorf("the browser is busy in another SuperLeo process and stayed busy for %s; try again later", q.maxWait)
		}
	}
}

// release lets go of owner's claim or place in line; anything else is a no-op,
// so a run whose claim was reaped can release without harming the next one.
func (q *browserQueue) release(owner string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.holder != nil && q.holder.owner == owner {
		log.Printf("superleo: browser queue: %s released browser %s after %s", q.holder.label, q.key, time.Since(q.holder.since).Round(time.Millisecond))
		q.dropHolderLocked()
		q.nextLocked(time.Now())
		return
	}
	for i, w := range q.waiters {
		if w.owner == owner {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return
		}
	}
}

// releaseLease lets go of the claim h, if it is still the one held.
func (q *browserQueue) releaseLease(h *browserLease) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.holder == h {
		q.dropHolderLocked()
		q.nextLocked(time.Now())
	}
}

// leave takes w out of the line; when the browser was handed to it in the
// same moment, it is handed on.
func (q *browserQueue) leave(w *browserWaiter) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, other := range q.waiters {
		if other == w {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return
		}
	}
	if q.holder != nil && q.holder.owner == w.owner {
		q.dropHolderLocked()
		q.nextLocked(time.Now())
	}
}

// reapLocked takes the browser from a holder that stopped using it, when
// someone else is waiting for it: here, or in another process.
func (q *browserQueue) reapLocked(now time.Time) {
	h := q.holder
	if h == nil || (len(q.waiters) == 0 && !q.wantedElsewhere(now)) {
		return
	}
	idle, held := now.Sub(h.touched), now.Sub(h.since)
	if idle < q.idle && held < q.maxHold {
		return
	}
	log.Printf("superleo: browser queue: %s lost browser %s (idle %s, held %s); passing it on",
		h.label, q.key, idle.Round(time.Millisecond), held.Round(time.Millisecond))
	q.dropHolderLocked()
	q.nextLocked(now)
}

// wantedElsewhere says whether another process is waiting for the browser's
// lock right now.
func (q *browserQueue) wantedElsewhere(now time.Time) bool {
	if q.lockPath == "" || q.holder == nil || q.holder.file == nil {
		return false
	}
	st, err := os.Stat(q.lockPath + ".want")
	return err == nil && now.Sub(st.ModTime()) < browserWantFresh
}

func (q *browserQueue) dropHolderLocked() {
	if f := q.holder.file; f != nil {
		_ = unlockFile(f)
		_ = f.Close()
	}
	close(q.holder.done)
	q.holder = nil
}

// watch looks after one claim while it lasts: nobody here may be waiting to
// reap it, and a process elsewhere cannot.
func (q *browserQueue) watch(h *browserLease) {
	t := time.NewTicker(q.reapEvery())
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-t.C:
			q.mu.Lock()
			if q.holder == h {
				q.reapLocked(time.Now())
			}
			q.mu.Unlock()
		}
	}
}

func (q *browserQueue) nextLocked(now time.Time) {
	if len(q.waiters) == 0 {
		return
	}
	w := q.waiters[0]
	q.waiters = q.waiters[1:]
	q.grantLocked(w, now)
}

func (q *browserQueue) grantLocked(w *browserWaiter, now time.Time) *browserLease {
	q.holder = &browserLease{owner: w.owner, label: w.label, since: now, touched: now, done: make(chan struct{})}
	close(w.granted)
	go q.watch(q.holder)
	return q.holder
}

func (q *browserQueue) reapEvery() time.Duration {
	d := q.idle / 4
	if q.maxHold/4 < d {
		d = q.maxHold / 4
	}
	return min(max(d, 10*time.Millisecond), 5*time.Second)
}

// status is the line as it stands.
func (q *browserQueue) status() BrowserQueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	var st BrowserQueueStatus
	if q.holder != nil {
		st.Holder, st.HolderSince = q.holder.label, q.holder.since
	}
	for _, w := range q.waiters {
		st.Waiting = append(st.Waiting, w.label)
	}
	return st
}

// browserTurn is the run a browser call belongs to, carried on the context
// from where the run starts (StreamTurn, RunLong) to the gate's guard.
type browserTurn struct {
	owner, label string
	notify       func(string)
}

type browserTurnKey struct{}

var browserTurnSeq atomic.Uint64

// withBrowserTurn marks ctx as one run of session; the browser it takes is
// released by the returned func, which the run defers.
func (s *Service) withBrowserTurn(ctx context.Context, session string, emit func(*agent.Event)) (context.Context, func()) {
	t := &browserTurn{
		owner: fmt.Sprintf("%s#%d", session, browserTurnSeq.Add(1)),
		label: session,
	}
	if t.label == "" {
		t.label = "a run"
	}
	if emit != nil {
		t.notify = func(msg string) { emit(&agent.Event{Type: agent.EventTypeStateUpdate, Content: msg}) }
	}
	ctx = context.WithValue(ctx, browserTurnKey{}, t)
	return ctx, func() {
		if s != nil && s.browser != nil && s.browser.queue != nil {
			s.browser.queue.release(t.owner)
		}
	}
}

// serialEmit makes emit safe to call from more than one goroutine: the
// browser's queue says a call is waiting from the call's own goroutine, beside
// the run's event loop.
func serialEmit(emit func(*agent.Event)) func(*agent.Event) {
	if emit == nil {
		return nil
	}
	var mu sync.Mutex
	return func(ev *agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		emit(ev)
	}
}

// takeBrowser is the gate's guard: the call waits until its run has the
// browser. A call from outside any marked run is lined up under its session,
// and loses the browser by going idle.
func (b *browserRuntime) takeBrowser(ctx context.Context) error {
	if b.queue == nil {
		return nil
	}
	t, _ := ctx.Value(browserTurnKey{}).(*browserTurn)
	if t == nil {
		id := sessionIDFrom(ctx)
		if id == "" {
			id = "an unnamed run"
		}
		t = &browserTurn{owner: "session:" + id, label: id}
	}
	return b.queue.acquire(ctx, t.owner, t.label, t.notify)
}

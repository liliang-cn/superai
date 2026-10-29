package app

// Coding runs that outlive SuperLeo.
//
// A coding run used to be a child on a pty this process held: SuperLeo
// restarting (an update, a crash, the Mac's own restart of the app) took the
// CLI with it, and whoever handed the work out — a chat turn, the queen's hive
// task on another machine — heard "failed" for work that was half done.
//
// Now the CLI is started in a session of its own (setsid) with its output going
// to a file, and everything needed to find it again is written beside it under
// <data>/cli-live: the run, the request it was started with, its process, which
// segment of output is current, and every event read so far with how far into
// the output it had been read. SuperLeo stopping leaves the CLI working. When it
// starts again, recoverCLIRuns reads the journals back:
//
//   - the process is still there: the output file is read on from where it was
//     left, and the run goes on as if nothing happened;
//   - it finished while SuperLeo was away: its exit code is in a file the
//     wrapping shell wrote, its output is all there, and the run is finished
//     from them;
//   - it is gone without finishing (the machine restarted): the CLI is started
//     again on its own session with its own resume (claude --resume, codex exec
//     resume, opencode and pi --session), told to carry on, into a new segment.
//
// The run keeps its id throughout, so whoever is following it — the core
// mirroring it over the agent link (app_cliremote.go) — sees it pause and go
// on, and gets its end like any other run's.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

// cliLive is the journal of one run going on this machine.
type cliLive struct {
	// Run is the run as it stands, without its events (those are in the
	// events file).
	Run CLIRun `json:"run"`
	// Req is what the CLI was started with; a resume is built from it.
	Req agentexec.Request `json:"req"`
	// Seg is the current segment: 0 for the first start, one more for each
	// resume. Each has its own output and exit files.
	Seg int `json:"seg"`
	// PID is the wrapping shell's, the leader of the CLI's session and
	// process group.
	PID int `json:"pid"`
	// Deadline is when the run's time limit is up; zero for none.
	Deadline time.Time `json:"deadline,omitzero"`
	// Approve is the approval URL in Req, if it has one: a resume gets a
	// fresh one in its place.
	Approve string `json:"approve,omitempty"`
}

// cliLiveEvent is one line of a run's events file: the event, and how far
// into which segment's output had been read when it came.
type cliLiveEvent struct {
	Seg   int         `json:"seg"`
	Off   int64       `json:"off"`
	Event CLIRunEvent `json:"e"`
}

// cliResumes is how many times a run is resumed before it is let fail: a CLI
// that dies again every time it is started is not going to finish.
const cliResumes = 3

// cliResumePrompt is what a resumed CLI is told.
const cliResumePrompt = "SuperLeo, which runs you, was restarted while you were working on this task, and your process was stopped part way. " +
	"Carry on with the same task from where you left off — check what is already done rather than redoing it — finish it, " +
	"and end with the final answer you would have given."

// cliPoll is how often a followed run's output file is looked at for more.
var cliPoll = 150 * time.Millisecond

func cliLiveDir() string { return filepath.Join(backend.DataDir(), "cli-live") }

func cliLivePath(id string) string { return filepath.Join(cliLiveDir(), id+".json") }

// cliLiveExists says whether a run has a journal to be picked up from.
func cliLiveExists(id string) bool {
	_, err := os.Stat(cliLivePath(id))
	return err == nil
}

func (l *cliLive) file(ext string) string {
	return filepath.Join(cliLiveDir(), l.Run.ID+ext)
}
func (l *cliLive) outPath() string  { return l.file(fmt.Sprintf(".%d.out", l.Seg)) }
func (l *cliLive) exitPath() string { return l.file(fmt.Sprintf(".%d.exit", l.Seg)) }
func (l *cliLive) eventsPath() string {
	return l.file(".events")
}

// cliDriver follows one run on this machine: starts its CLI, reads its
// output, keeps its journal, and finishes it.
type cliDriver struct {
	a        *App
	run      *CLIRun
	provider agentexec.Provider

	mu    sync.Mutex // the journal, the events file and off
	live  *cliLive
	sess  agentexec.Session
	calls map[string]string // call id → tool name, to label results
	off   int64             // how far into the current segment's output has been read
	gone  bool              // finished: nothing more is written
}

func newCLIDriver(a *App, run *CLIRun, live *cliLive, provider agentexec.Provider, sess agentexec.Session) *cliDriver {
	live.Run = summaryOf(run)
	return &cliDriver{a: a, run: run, provider: provider, live: live, sess: sess, calls: map[string]string{}}
}

// save writes the journal: the run as it stands now, read under the store's
// lock, with the request and process beside it.
func (d *cliDriver) save() {
	s := d.a.cliStore()
	s.mu.Lock()
	run := summaryOf(d.run)
	s.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gone {
		return
	}
	d.live.Run = run
	d.saveLocked()
}

func (d *cliDriver) saveLocked() {
	raw, err := json.Marshal(d.live)
	if err != nil {
		return
	}
	if err := os.MkdirAll(cliLiveDir(), 0o700); err != nil {
		return
	}
	path := cliLivePath(d.live.Run.ID)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// record appends an event to the run's events file.
func (d *cliDriver) record(e CLIRunEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gone {
		return
	}
	raw, err := json.Marshal(cliLiveEvent{Seg: d.live.Seg, Off: d.off, Event: e})
	if err != nil {
		return
	}
	f, err := os.OpenFile(d.live.eventsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
}

// forget removes the journal of a finished run: it is in cli-runs.json now.
func (d *cliDriver) forget() {
	d.mu.Lock()
	d.gone = true
	id := d.live.Run.ID
	d.mu.Unlock()
	files, _ := filepath.Glob(filepath.Join(cliLiveDir(), id+".*"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}

// launch starts the current segment's command detached, and hands back a
// channel that gets its exit code when it ends while this process is its
// parent.
func (d *cliDriver) launch(spec agentexec.CommandSpec) (<-chan int, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("empty command")
	}
	if err := os.MkdirAll(cliLiveDir(), 0o700); err != nil {
		return nil, err
	}
	d.mu.Lock()
	outPath, exitPath := d.live.outPath(), d.live.exitPath()
	d.mu.Unlock()
	_ = os.Remove(exitPath)
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	cmd := detachedCommand(spec.Argv, exitPath)
	cmd.Env = append(append(os.Environ(), spec.Env...), "SUPERLEO_CLI_EXIT="+exitPath)
	cmd.Dir = spec.WorkDir
	// Stdin is /dev/null: these CLIs read stdin when it is not a terminal.
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	s := d.a.cliStore()
	s.mu.Lock()
	d.run.PID = pid
	s.mu.Unlock()
	d.mu.Lock()
	d.live.PID = pid
	d.off = 0
	d.mu.Unlock()
	d.save()
	wait := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		code := 0
		var ee *exec.ExitError
		switch {
		case errors.As(err, &ee):
			code = ee.ExitCode()
		case err != nil:
			code = -1
		}
		wait <- code
	}()
	return wait, nil
}

// feed parses a piece of output read up to off.
func (d *cliDriver) feed(chunk []byte, off int64, emit bool) {
	d.mu.Lock()
	d.off = off
	sess, calls := d.sess, d.calls
	d.mu.Unlock()
	evs, _ := sess.ParseChunk(chunk)
	if emit {
		d.push(evs, calls)
	} else {
		// Already recorded: only the call names are wanted, to label results.
		for _, e := range evs {
			normaliseCLIEvent(e, calls)
		}
	}
	if id := sess.SessionID(); id != "" {
		d.a.setCLISession(d.run, id)
	}
}

func (d *cliDriver) push(evs []agentexec.Event, calls map[string]string) {
	for _, e := range evs {
		for _, ce := range normaliseCLIEvent(e, calls) {
			d.a.addCLIEvent(d.run, ce)
		}
	}
}

// segEnd is how following a segment ended.
type segEnd int

const (
	segExited    segEnd = iota // the CLI exited; code says how
	segStopped                 // the run's context ended: stopped or out of time
	segVanished                // the process is gone and left no exit code
	segAbandoned               // SuperLeo is stopping: the CLI carries on without it
)

// follow reads the current segment's output from off until the CLI ends.
// wait is the exit code channel when this process started it, nil when it
// was picked up after a restart.
func (d *cliDriver) follow(ctx context.Context, wait <-chan int, off int64) (segEnd, int) {
	d.mu.Lock()
	outPath, exitPath, pid := d.live.outPath(), d.live.exitPath(), d.live.PID
	d.mu.Unlock()
	f, err := os.Open(outPath)
	if err != nil {
		slog.Warn("cli run: output file unreadable", "run", d.run.ID, "error", err)
		return segVanished, -1
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return segVanished, -1
	}
	buf := make([]byte, 64<<10)
	ended, end, code := false, segExited, 0
	tick := time.NewTicker(cliPoll)
	defer tick.Stop()
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			off += int64(n)
			d.feed(buf[:n], off, true)
			continue
		}
		if rerr != nil && rerr != io.EOF {
			return segVanished, -1
		}
		if ended {
			return end, code
		}
		select {
		case <-ctx.Done():
			return segStopped, -1
		case c := <-wait:
			// Read to the end first: the CLI has written all it will.
			ended = true
			if got, ok := readExitCode(exitPath); ok {
				code = got
			} else if d.a.cliDetached.Load() {
				end = segAbandoned
			} else {
				// The wrapping shell was killed before it could write the
				// code: the CLI did not finish.
				end, code = segVanished, c
			}
		case <-tick.C:
			if d.a.cliDetached.Load() {
				return segAbandoned, 0
			}
			if wait != nil {
				continue
			}
			if got, ok := readExitCode(exitPath); ok {
				ended, code = true, got
			} else if !procAlive(pid) {
				// The code may have landed between the two looks.
				if got, ok := readExitCode(exitPath); ok {
					ended, code = true, got
				} else {
					ended, end = true, segVanished
				}
			}
		}
	}
}

// drive follows the run until it is finished, resuming the CLI when it is
// found gone without finishing.
func (d *cliDriver) drive(ctx context.Context, cancel context.CancelFunc, wait <-chan int, off int64) {
	defer cancel()
	for {
		end, code := d.follow(ctx, wait, off)
		switch end {
		case segAbandoned:
			// SuperLeo is stopping; the journal says where to pick it up.
			return
		case segStopped:
			d.stop(wait)
			d.finish(ctx, -1, nil)
			return
		case segExited:
			d.finish(ctx, code, nil)
			return
		case segVanished:
			if ctx.Err() != nil {
				d.finish(ctx, -1, nil)
				return
			}
			d.mu.Lock()
			resumed := d.live.Run.Resumed
			d.mu.Unlock()
			if resumed >= cliResumes {
				d.finish(ctx, -1, fmt.Errorf("%s stopped part way %d times and was not resumed again", d.run.Agent, resumed+1))
				return
			}
			var err error
			if wait, err = d.resume(ctx); err != nil {
				d.finish(ctx, -1, fmt.Errorf("%s stopped part way and could not be resumed: %w", d.run.Agent, err))
				return
			}
			off = 0
		}
	}
}

// stop ends the CLI's whole process group: asked first, then made to.
func (d *cliDriver) stop(wait <-chan int) {
	d.mu.Lock()
	pid := d.live.PID
	d.mu.Unlock()
	if pid <= 0 {
		return
	}
	signalGroup(pid, false)
	deadline := time.After(2 * time.Second)
	for {
		if wait != nil {
			select {
			case <-wait:
				return
			case <-deadline:
				signalGroup(pid, true)
				return
			}
		}
		if !procAlive(pid) {
			return
		}
		select {
		case <-deadline:
			signalGroup(pid, true)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// resume starts the CLI again on its session, into a new segment.
func (d *cliDriver) resume(ctx context.Context) (<-chan int, error) {
	s := d.a.cliStore()
	s.mu.Lock()
	session := d.run.Session
	d.run.Resumed++
	s.mu.Unlock()

	d.mu.Lock()
	d.live.Seg++
	d.live.Run.Resumed++
	d.off = 0
	req := d.live.Req
	req.ExtraArgs = append([]string(nil), req.ExtraArgs...)
	oldApprove := d.live.Approve
	d.mu.Unlock()

	note := ""
	if session != "" {
		req.ResumeSessionID, req.Prompt = session, cliResumePrompt
		note = fmt.Sprintf("SuperLeo restarted and %s had stopped; resuming its session %s", d.run.Agent, session)
	} else {
		// Nothing to resume: the task again, from the start.
		req.ResumeSessionID = ""
		note = fmt.Sprintf("SuperLeo restarted and %s had stopped before it named a session; starting the task again", d.run.Agent)
	}
	// The approval channel is this process's: a URL from before the
	// restart points at nobody.
	if oldApprove != "" {
		fresh, err := d.a.cliApproveURL(d.run.ID)
		if err != nil {
			return nil, fmt.Errorf("could not open the approval channel: %w", err)
		}
		for i, arg := range req.ExtraArgs {
			req.ExtraArgs[i] = strings.ReplaceAll(arg, oldApprove, fresh)
		}
		d.mu.Lock()
		d.live.Approve = fresh
		d.mu.Unlock()
	}
	slog.Info("cli run: resuming", "run", d.run.ID, "agent", d.run.Agent, "session", session, "segment", d.live.Seg, "origin", d.run.Origin)
	d.a.addCLIEvent(d.run, CLIRunEvent{Kind: "note", Text: note})

	sess := d.provider.NewSession()
	spec, err := sess.BuildCommand(ctx, req)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.sess, d.calls = sess, map[string]string{}
	d.mu.Unlock()
	return d.launch(spec)
}

// finish reads the segment's output whole, lets the provider make its answer
// out of it, and finishes the run the way it ended.
func (d *cliDriver) finish(ctx context.Context, code int, failure error) {
	d.mu.Lock()
	sess, calls, outPath := d.sess, d.calls, d.live.outPath()
	d.mu.Unlock()
	output, _ := os.ReadFile(outPath)
	result, tail, finErr := sess.Finalize(context.Background(), output, code)
	d.push(tail, calls)
	d.a.finishCLIRun(d.run, func(run *CLIRun) {
		if id := sess.SessionID(); id != "" {
			run.Session = id
		}
		run.Summary = strings.TrimSpace(result.Summary)
		run.In, run.Out, run.Cache = int(result.Usage.InputTokens), int(result.Usage.OutputTokens), int(result.Usage.CacheTokens)
		run.CostUSD = result.Usage.EstimatedCostUSD
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			run.State, run.Error = "cancelled", "stopped"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			run.State, run.Error = "failed", "did not finish in time and was stopped"
		case failure != nil:
			run.State, run.Error = "failed", failure.Error()
		case finErr != nil:
			run.State, run.Error = "failed", finErr.Error()
		case result.Failed || code != 0:
			run.State = "failed"
			run.Error = run.Summary
			if run.Error == "" {
				run.Error = cliLastLine(string(output))
			}
		default:
			run.State = "done"
		}
	})
}

func readExitCode(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// detachCLIRuns is SuperLeo stopping: its runs are left to carry on, each
// with its journal saying where it is, and nothing stops them on the way out.
func (a *App) detachCLIRuns() {
	a.cliDetached.Store(true)
	s := a.cliStore()
	s.mu.Lock()
	var going []*cliDriver
	for _, d := range s.live {
		going = append(going, d)
	}
	s.mu.Unlock()
	for _, d := range going {
		d.save()
		d.mu.Lock()
		pid, seg := d.live.PID, d.live.Seg
		d.mu.Unlock()
		slog.Info("cli run: SuperLeo stopping; the CLI carries on and is picked up when it is back",
			"run", d.run.ID, "agent", d.run.Agent, "pid", pid, "segment", seg, "origin", d.run.Origin)
	}
}

// recoverCLIRuns picks up the runs whose journals are still there: the ones
// that were going when SuperLeo last stopped.
func (a *App) recoverCLIRuns() {
	paths, _ := filepath.Glob(filepath.Join(cliLiveDir(), "*.json"))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var live cliLive
		if json.Unmarshal(raw, &live) != nil || live.Run.ID == "" {
			_ = os.Remove(p)
			continue
		}
		a.recoverCLIRun(&live)
	}
}

func (a *App) recoverCLIRun(live *cliLive) {
	s := a.cliStore()
	s.mu.Lock()
	if _, going := s.live[live.Run.ID]; going {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	events, offs := readCLILiveEvents(live.eventsPath(), live.Seg)
	run := live.Run
	run.State, run.Ended, run.Error = "running", nil, ""
	run.Tools = 0
	for _, e := range events {
		if e.Kind == "tool" {
			run.Tools++
		}
	}
	if len(events) > cliRunEventsKept {
		events = events[len(events)-cliRunEventsKept:]
	}
	run.Events = events
	r := &run

	provider, _, perr := a.cliProvider(run.Agent)
	var sess agentexec.Session
	if perr == nil {
		sess = provider.NewSession()
		// Built again so the session knows what it was started with; the
		// command itself is not run.
		_, _ = sess.BuildCommand(context.Background(), live.Req)
	}
	d := newCLIDriver(a, r, live, provider, sess)
	ctx, cancel := context.WithCancel(context.Background())
	if !live.Deadline.IsZero() {
		cancel()
		ctx, cancel = context.WithDeadline(context.Background(), live.Deadline)
	}
	s.mu.Lock()
	s.runs[r.ID] = r
	s.cancels[r.ID] = cancel
	s.live[r.ID] = d
	s.mu.Unlock()

	if perr != nil {
		slog.Warn("cli run: cannot pick back up", "run", r.ID, "agent", r.Agent, "error", perr)
		d.finishWithout(fmt.Errorf("SuperLeo restarted and could not pick the run back up: %w", perr))
		cancel()
		return
	}
	if live.Approve != "" {
		// The CLI still holds its approval URL: served again at the same
		// place when it can be (cliApproveURL).
		a.cliApproveRestore(r.ID, live.Approve)
	}

	// The output read so far, again, without events: they are recorded
	// already. The provider's parser comes back to where it was.
	if offs > 0 {
		if f, err := os.Open(live.outPath()); err == nil {
			chunk := make([]byte, offs)
			n, _ := io.ReadFull(f, chunk)
			_ = f.Close()
			d.feed(chunk[:n], int64(n), false)
			offs = int64(n)
		} else {
			offs = 0
		}
	}

	_, finished := readExitCode(live.exitPath())
	alive := live.PID > 0 && procAlive(live.PID)
	var note string
	switch {
	case finished:
		note = fmt.Sprintf("SuperLeo restarted; %s finished while it was away", r.Agent)
	case alive:
		note = fmt.Sprintf("SuperLeo restarted; back on %s (pid %d), still working", r.Agent, live.PID)
	default:
		note = fmt.Sprintf("SuperLeo restarted; %s had stopped", r.Agent)
	}
	slog.Info("cli run: picked back up after a restart", "run", r.ID, "agent", r.Agent, "pid", live.PID,
		"alive", alive, "finished", finished, "session", r.Session, "segment", live.Seg, "read", offs, "origin", r.Origin)
	a.addCLIEvent(r, CLIRunEvent{Kind: "note", Text: note})
	a.emit("cli:run", runPayload(r))
	go d.drive(ctx, cancel, nil, offs)
}

// finishWithout fails a run that could not be picked up.
func (d *cliDriver) finishWithout(err error) {
	d.a.finishCLIRun(d.run, func(run *CLIRun) { run.State, run.Error = "failed", err.Error() })
}

// tagCLIOrigin says who a run was handed out by, in the run and its journal.
func (a *App) tagCLIOrigin(id, origin string) {
	s := a.cliStore()
	s.mu.Lock()
	r, d := s.runs[id], s.live[id]
	if r != nil {
		r.Origin = origin
	}
	s.mu.Unlock()
	if d != nil {
		d.save()
	}
}

// readCLILiveEvents reads a run's events back, with how far into segment
// seg's output they had been read.
func readCLILiveEvents(path string, seg int) ([]CLIRunEvent, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	var out []CLIRunEvent
	var off int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var l cliLiveEvent
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue // a line cut short by the process ending
		}
		out = append(out, l.Event)
		if l.Seg == seg && l.Off > off {
			off = l.Off
		}
	}
	return out, off
}

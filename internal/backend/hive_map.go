package backend

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Scheduling many orders over few workers.
//
// hive_command gives each worker one order and waits for all of them. That is
// right for "each of you do this", and wrong for "here are forty things, get
// them done": forty orders do not fit ten workers, and sending them all at once
// would put four on each and make the slow ones wait behind the fast ones for
// no reason.
//
// This is the other shape. The orders go in a queue, every live worker takes the
// next one when it is free, and so the hive stays as busy as it can be and no
// worker holds two at a time. A worker that fails or drops out gives its order
// back, once more than one attempt is allowed, so one dead pod costs a retry and
// not a hole in the results.

// MapResult is one order's outcome.
type MapResult struct {
	Index    int    `json:"index"`
	Prompt   string `json:"prompt"`
	Worker   string `json:"worker"`
	TaskID   string `json:"task_id,omitempty"`
	Text     string `json:"text"`
	OK       bool   `json:"ok"`
	Reason   string `json:"reason,omitempty"`
	MS       int64  `json:"ms"`
	Attempts int    `json:"attempts"`
}

// MapOptions tunes a run.
type MapOptions struct {
	// Attempts is how many times one order may be tried, on different workers
	// where it can. Zero means two.
	Attempts int
}

// Runner is what MapOrders needs from the hive: who is live now, and a way to
// ask one of them.
type Runner interface {
	Workers() map[string]RemoteAgent
	Run(ctx context.Context, name, prompt string) (RemoteResult, error)
}

// MapOrders runs every prompt on the hive's workers, one at a time per worker,
// and returns the results in the order the prompts were given.
//
// The set of workers is read again whenever one is free to take work, so a
// worker that joined after the run began (a spawned one, most likely) picks up
// orders that are still waiting.
func MapOrders(ctx context.Context, r Runner, prompts []string, opt MapOptions) []MapResult {
	if opt.Attempts <= 0 {
		opt.Attempts = 2
	}
	n := len(prompts)
	results := make([]MapResult, n)
	for i, p := range prompts {
		results[i] = MapResult{Index: i, Prompt: p}
	}
	if n == 0 {
		return results
	}

	type item struct{ idx, attempt int }
	var (
		mu       sync.Mutex
		queue    = make([]item, 0, n)
		left     = n
		failedOn = map[int]map[string]bool{}
		started  = map[string]bool{} // workers that already have a loop
		wg       sync.WaitGroup
		wake     = make(chan struct{}, 1)
	)
	for i := 0; i < n; i++ {
		queue = append(queue, item{i, 1})
	}
	poke := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	// take hands worker w the first waiting order it has not already failed.
	take := func(w string) (item, bool) {
		mu.Lock()
		defer mu.Unlock()
		for k, it := range queue {
			if failedOn[it.idx][w] {
				continue
			}
			queue = append(queue[:k], queue[k+1:]...)
			return it, true
		}
		return item{}, false
	}

	var loop func(w string)
	loop = func(w string) {
		defer wg.Done()
		// A worker that fails twice in a row is presumed dead and stops taking
		// orders. Without this a lost pod would take every waiting order in turn,
		// fail each one, and spend an attempt of each.
		streak := 0
		for {
			if streak >= 2 {
				return
			}
			if ctx.Err() != nil {
				return
			}
			it, ok := take(w)
			if !ok {
				mu.Lock()
				done := left == 0
				mu.Unlock()
				if done {
					return
				}
				// Nothing for this worker right now, but orders are still in
				// flight elsewhere and one may come back.
				select {
				case <-ctx.Done():
					return
				case <-time.After(300 * time.Millisecond):
					continue
				}
			}
			res, err := r.Run(ctx, w, prompts[it.idx])
			ok = err == nil && !res.Failed
			if ok {
				streak = 0
			} else {
				streak++
			}

			mu.Lock()
			results[it.idx].Worker, results[it.idx].Attempts = w, it.attempt
			results[it.idx].TaskID = res.TaskID
			results[it.idx].MS += res.MS
			if ok {
				results[it.idx].OK, results[it.idx].Text, results[it.idx].Reason = true, res.Text, ""
				left--
			} else {
				reason := res.Reason
				if err != nil {
					reason = err.Error()
				}
				results[it.idx].Reason, results[it.idx].Text = reason, res.Text
				if failedOn[it.idx] == nil {
					failedOn[it.idx] = map[string]bool{}
				}
				failedOn[it.idx][w] = true
				if it.attempt < opt.Attempts && ctx.Err() == nil {
					queue = append(queue, item{it.idx, it.attempt + 1})
				} else {
					left--
				}
			}
			mu.Unlock()
			poke()
		}
	}

	// Keep adding a loop for every live worker until the work is done, so
	// workers that arrive mid-run are used.
	for {
		mu.Lock()
		done := left == 0
		mu.Unlock()
		if done || ctx.Err() != nil {
			break
		}
		for name := range r.Workers() {
			mu.Lock()
			seen := started[name]
			started[name] = true
			mu.Unlock()
			if !seen {
				wg.Add(1)
				go loop(name)
			}
		}
		mu.Lock()
		none := len(started) == 0
		mu.Unlock()
		if none {
			// No workers at all: nothing will ever take these.
			for i := range results {
				results[i].Reason = "no live workers"
			}
			return results
		}
		select {
		case <-ctx.Done():
		case <-wake:
		case <-time.After(500 * time.Millisecond):
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		for i := range results {
			if !results[i].OK && results[i].Reason == "" {
				results[i].Reason = fmt.Sprintf("stopped: %v", ctx.Err())
			}
		}
	}
	return results
}

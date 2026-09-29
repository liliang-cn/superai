package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	mu      sync.Mutex
	workers map[string]bool
	inHand  map[string]int
	maxHand map[string]int
	ran     map[string]int
	fail    map[string]bool
	delay   time.Duration
}

func newFakeRunner(names ...string) *fakeRunner {
	f := &fakeRunner{workers: map[string]bool{}, inHand: map[string]int{}, maxHand: map[string]int{}, ran: map[string]int{}, fail: map[string]bool{}, delay: 20 * time.Millisecond}
	for _, n := range names {
		f.workers[n] = true
	}
	return f
}

func (f *fakeRunner) Workers() map[string]RemoteAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]RemoteAgent{}
	for n := range f.workers {
		out[n] = RemoteAgent{URL: "http://" + n}
	}
	return out
}

func (f *fakeRunner) add(n string) { f.mu.Lock(); f.workers[n] = true; f.mu.Unlock() }

func (f *fakeRunner) Run(ctx context.Context, name, prompt string) (RemoteResult, error) {
	f.mu.Lock()
	f.inHand[name]++
	if f.inHand[name] > f.maxHand[name] {
		f.maxHand[name] = f.inHand[name]
	}
	f.ran[name]++
	fail := f.fail[name]
	f.mu.Unlock()
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
	}
	f.mu.Lock()
	f.inHand[name]--
	f.mu.Unlock()
	if fail {
		return RemoteResult{Agent: name, Failed: true, Reason: "connection refused", MS: 5}, nil
	}
	return RemoteResult{Agent: name, Text: name + " did " + prompt, MS: 10}, nil
}

func prompts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("job-%d", i)
	}
	return out
}

func TestMoreOrdersThanWorkersAllGetDoneOneAtATimePerWorker(t *testing.T) {
	f := newFakeRunner("a", "b", "c")
	res := MapOrders(context.Background(), f, prompts(12), MapOptions{})
	for i, r := range res {
		if !r.OK || r.Index != i || r.Prompt != fmt.Sprintf("job-%d", i) {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	for w, m := range f.maxHand {
		if m != 1 {
			t.Errorf("%s held %d orders at once", w, m)
		}
	}
	if len(f.ran) != 3 {
		t.Fatalf("only %d of 3 workers were used: %v", len(f.ran), f.ran)
	}
}

func TestADeadWorkersOrdersMoveToOthersAndItStopsTakingMore(t *testing.T) {
	f := newFakeRunner("good1", "good2", "dead")
	f.fail["dead"] = true
	res := MapOrders(context.Background(), f, prompts(9), MapOptions{})
	for _, r := range res {
		if !r.OK {
			t.Fatalf("an order was lost to a dead worker: %+v", r)
		}
		if r.Worker == "dead" {
			t.Fatalf("a result is credited to the dead worker: %+v", r)
		}
	}
	if f.ran["dead"] > 2 {
		t.Fatalf("the dead worker was given %d orders; it should stop after two failures", f.ran["dead"])
	}
}

func TestAnOrderThatFailsEverywhereSaysSoAfterItsAttempts(t *testing.T) {
	f := newFakeRunner("a", "b")
	f.fail["a"], f.fail["b"] = true, true
	res := MapOrders(context.Background(), f, prompts(1), MapOptions{Attempts: 2})
	if res[0].OK || res[0].Reason == "" || res[0].Attempts != 2 {
		t.Fatalf("%+v", res[0])
	}
}

func TestNoWorkersIsAnAnswerNotAHang(t *testing.T) {
	done := make(chan []MapResult, 1)
	go func() { done <- MapOrders(context.Background(), newFakeRunner(), prompts(3), MapOptions{}) }()
	select {
	case res := <-done:
		if res[0].OK || res[0].Reason != "no live workers" {
			t.Fatalf("%+v", res[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hung with no workers")
	}
}

func TestAWorkerThatJoinsMidRunTakesWaitingOrders(t *testing.T) {
	f := newFakeRunner("a")
	f.delay = 80 * time.Millisecond
	var joined atomic.Bool
	go func() { time.Sleep(150 * time.Millisecond); f.add("late"); joined.Store(true) }()
	res := MapOrders(context.Background(), f, prompts(10), MapOptions{})
	for _, r := range res {
		if !r.OK {
			t.Fatalf("%+v", r)
		}
	}
	if f.ran["late"] == 0 {
		t.Fatalf("the worker that arrived mid-run was never used: %v", f.ran)
	}
}

func TestStoppingTheRunStopsTheWork(t *testing.T) {
	f := newFakeRunner("a")
	f.delay = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	res := MapOrders(ctx, f, prompts(5), MapOptions{})
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not stop")
	}
	if res[4].OK || res[4].Reason == "" {
		t.Fatalf("%+v", res[4])
	}
}

var _ = errors.New

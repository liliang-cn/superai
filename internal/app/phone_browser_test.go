package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type emitted struct {
	mu   sync.Mutex
	name string
	body map[string]any
	got  chan struct{}
}

func (e *emitted) emit(name string, body map[string]any) {
	e.mu.Lock()
	e.name, e.body = name, body
	e.mu.Unlock()
	close(e.got)
}

// A call goes to the phone heard from last and comes back with its answer.
func TestAPhoneBrowserCallIsAnsweredByTheLatestPhone(t *testing.T) {
	p := newPhoneBrowser()
	now := time.Now()
	p.now = func() time.Time { return now }
	p.seen["old-phone"] = now.Add(-30 * time.Second)
	p.seen["new-phone"] = now.Add(-5 * time.Second)
	e := &emitted{got: make(chan struct{})}
	done := make(chan struct{})
	var res any
	var err error
	go func() {
		res, err = p.call(context.Background(), e.emit, "open", map[string]any{"url": "https://www.wikipedia.org"})
		close(done)
	}()
	<-e.got
	e.mu.Lock()
	if e.name != phoneBrowserEvent || e.body["device"] != "new-phone" || e.body["op"] != "open" {
		t.Fatalf("emitted %s %v", e.name, e.body)
	}
	id, _ := e.body["id"].(string)
	e.mu.Unlock()
	if !p.answer(id, phoneBrowserAnswer{ok: true, result: "Wikipedia"}) {
		t.Fatal("the answer found no call waiting")
	}
	<-done
	if err != nil || res != "Wikipedia" {
		t.Fatalf("call = %v, %v", res, err)
	}
	if p.answer(id, phoneBrowserAnswer{ok: true}) {
		t.Fatal("a second answer to a finished call was taken")
	}
}

// With no phone heard from lately the call fails at once with a reason the
// model can pass on, instead of waiting out the timeout.
func TestAPhoneBrowserCallWithNoPhoneFailsAtOnce(t *testing.T) {
	p := newPhoneBrowser()
	now := time.Now()
	p.now = func() time.Time { return now }
	p.seen["gone"] = now.Add(-2 * phoneBrowserFresh)
	start := time.Now()
	_, err := p.call(context.Background(), func(string, map[string]any) { t.Fatal("sent to a phone that is gone") }, "open", nil)
	if err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited for a phone that is not there")
	}
}

// A phone that never answers ends the call at the timeout, and an answer the
// phone reports as failed comes back as the model's error.
func TestAPhoneBrowserCallTimesOutAndReportsFailures(t *testing.T) {
	old := phoneBrowserTimeout
	phoneBrowserTimeout = 50 * time.Millisecond
	t.Cleanup(func() { phoneBrowserTimeout = old })
	p := newPhoneBrowser()
	p.seen["phone"] = p.now()
	if _, err := p.call(context.Background(), func(string, map[string]any) {}, "snapshot", nil); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}

	phoneBrowserTimeout = time.Second
	e := &emitted{got: make(chan struct{})}
	errc := make(chan error, 1)
	go func() {
		_, err := p.call(context.Background(), e.emit, "click", map[string]any{"ref": "e9"})
		errc <- err
	}()
	<-e.got
	e.mu.Lock()
	id, _ := e.body["id"].(string)
	e.mu.Unlock()
	p.answer(id, phoneBrowserAnswer{ok: false, err: "no element e9"})
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "no element e9") {
		t.Fatalf("err = %v", err)
	}
}

// Every parameter of every phone-browser tool is a schema object. A property
// written as a bare string (str() where strParam() was meant) made the
// Messages API refuse the whole request, and with it every turn the queen
// took while a phone was online.
func TestEveryPhoneBrowserParameterIsASchema(t *testing.T) {
	for _, op := range phoneBrowserOps {
		props, _ := op.params["properties"].(map[string]any)
		for name, p := range props {
			s, ok := p.(map[string]any)
			if !ok || s["type"] == nil {
				t.Errorf("%s.%s is %T %v, not a schema", op.op, name, p, p)
			}
		}
	}
}

package app

import "testing"

func TestOpenToolsClosesWhatTheTurnLeftRunning(t *testing.T) {
	var o openTools
	o.call("bash")
	o.call("bash")
	o.call("fs_read")
	o.result("fs_read")
	o.result("bash")

	evs := o.closeEvents("req-1", "the turn ended")
	if len(evs) != 1 {
		t.Fatalf("want one open call closed, got %d: %v", len(evs), evs)
	}
	e := evs[0]
	if e["tool"] != "bash" || e["type"] != "tool_result" || e["requestId"] != "req-1" {
		t.Fatalf("%v", e)
	}
	r := e["result"].(map[string]any)
	if r["ok"] != false || r["closed"] != true {
		t.Fatalf("%v", r)
	}
	if again := o.closeEvents("req-1", "x"); len(again) != 0 {
		t.Fatalf("closed twice: %v", again)
	}
	var none openTools
	if len(none.closeEvents("r", "x")) != 0 {
		t.Fatal("events from nothing")
	}
}

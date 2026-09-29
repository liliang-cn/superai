package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hello(name string) HiveHello {
	return HiveHello{Protocol: HiveProtocol, Name: name, Role: HiveRoleWorker, URL: "http://" + name + ":43117", Token: "t-" + name}
}

func TestAWorkerJoinsAndAppearsOnTheRoster(t *testing.T) {
	h := NewHive("sup", 10*time.Second)
	w, err := h.Join(hello("worker-0"))
	if err != nil || w.Queen != "sup" || w.IntervalMS != 10000 || w.Members != 1 {
		t.Fatalf("welcome %+v, err %v", w, err)
	}
	ms := h.Members()
	if len(ms) != 1 || ms[0].Name != "worker-0" || ms[0].State != "live" {
		t.Fatalf("roster %+v", ms)
	}
}

func TestJoiningTwiceIsOneMemberNotTwo(t *testing.T) {
	// The heartbeat is the same message as the join.
	h := NewHive("sup", time.Second)
	h.Join(hello("a"))
	h.Join(hello("a"))
	if n := len(h.Members()); n != 1 {
		t.Fatalf("%d members", n)
	}
}

func TestTheQueenRefusesWhatIsNotAWorker(t *testing.T) {
	h := NewHive("sup", time.Second)
	cases := map[string]func(*HiveHello){
		"wrong protocol": func(x *HiveHello) { x.Protocol = "superai-hive/2" },
		"a second queen": func(x *HiveHello) { x.Role = HiveRoleQueen },
		"bad name":       func(x *HiveHello) { x.Name = "has space" },
		"empty name":     func(x *HiveHello) { x.Name = "" },
		"non-http url":   func(x *HiveHello) { x.URL = "ssh://x" },
		"no url":         func(x *HiveHello) { x.URL = "" },
	}
	for name, mut := range cases {
		x := hello("worker-0")
		mut(&x)
		if _, err := h.Join(x); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(h.Members()) != 0 {
		t.Fatal("a refused join left a member behind")
	}
}

func TestASilentWorkerIsLostAfterThreeMissedBeats(t *testing.T) {
	now := time.Unix(1000, 0)
	h := NewHive("sup", 10*time.Second)
	h.now = func() time.Time { return now }
	h.Join(hello("a"))

	now = now.Add(29 * time.Second)
	if h.Members()[0].State != "live" || len(h.Agents()) != 1 {
		t.Fatal("dropped a worker that had only missed two beats")
	}
	now = now.Add(2 * time.Second)
	if h.Members()[0].State != "lost" {
		t.Fatal("still live after three missed beats")
	}
	if len(h.Agents()) != 0 {
		t.Fatal("a lost worker is still offered for commanding")
	}
	// It comes back by saying so; nothing else is needed.
	h.Join(hello("a"))
	if h.Members()[0].State != "live" || len(h.Agents()) != 1 {
		t.Fatal("a worker that spoke again did not come back")
	}
}

func TestALongLostWorkerIsForgotten(t *testing.T) {
	now := time.Unix(1000, 0)
	h := NewHive("sup", 10*time.Second)
	h.now = func() time.Time { return now }
	h.Join(hello("a"))
	now = now.Add(hiveForget + time.Second)
	if n := len(h.Members()); n != 0 {
		t.Fatalf("%d members after the forget window", n)
	}
}

func TestARescheduledWorkerReplacesItsOldAddress(t *testing.T) {
	h := NewHive("sup", time.Second)
	h.Join(hello("a"))
	moved := hello("a")
	moved.URL = "http://10.0.0.9:43117"
	h.Join(moved)
	ms := h.Members()
	if len(ms) != 1 || ms[0].URL != "http://10.0.0.9:43117" {
		t.Fatalf("roster %+v", ms)
	}
}

func TestTheRosterFeedsTheRunnerWithoutTheRemoteSwitch(t *testing.T) {
	// Being a queen is the operator's decision to command; the SSH switch
	// governs something else and stays off.
	f := newFakeWorker(t, "t-x")
	f.onSend = func(_, _ string, r *fakeWorker) string {
		go r.emit("chat:done", map[string]any{"requestId": "req-1", "final": "done"})
		return "req-1"
	}
	h := NewHive("sup", time.Minute)
	x := hello("x")
	x.URL = f.srv.URL
	h.Join(x)

	run := NewRemoteRunner(RemoteAgents{Enabled: false})
	run.SetRoster(h.Agents)
	res, _ := run.Run(context.Background(), "x", "go")
	if res.Failed || res.Text != "done" {
		t.Fatalf("%+v", res)
	}
	if _, ok := run.Workers()["x"]; !ok {
		t.Fatal("Workers() does not include a joined worker")
	}
	res, _ = run.Run(context.Background(), "nobody", "go")
	if !res.Failed {
		t.Fatal("an unknown name was accepted")
	}
}

// queenServer is the join endpoint as the app serves it, over a real Hive.
func queenServer(t *testing.T, h *Hive, token string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hive/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "sign in", http.StatusUnauthorized)
			return
		}
		var x HiveHello
		json.NewDecoder(r.Body).Decode(&x)
		wl, err := h.Join(x)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(wl)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestAWorkerJoinsThroughTheLoop(t *testing.T) {
	h := NewHive("sup", 50*time.Millisecond)
	srv := queenServer(t, h, "secret")
	a := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "worker-7", JoinURL: srv.URL, AdvertiseURL: "http://worker-7:43117"}, Token: "secret"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for len(h.Members()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ms := h.Members()
	if len(ms) != 1 || ms[0].Name != "worker-7" || ms[0].URL != "http://worker-7:43117" {
		t.Fatalf("roster %+v", ms)
	}
	// The credential to command it travelled with the join.
	if got := h.Agents()["worker-7"].Token; got != "secret" {
		t.Fatalf("token %q", got)
	}
	// And the loop keeps talking: LastSeen moves.
	first := ms[0].LastSeen
	time.Sleep(200 * time.Millisecond)
	if !h.Members()[0].LastSeen.After(first) {
		t.Fatal("no heartbeat after joining")
	}
}

func TestAWorkerStartedBeforeTheQueenJoinsWhenItAppears(t *testing.T) {
	// Pods start in no particular order, and the loop is what makes that fine.
	var up atomic.Bool
	h := NewHive("sup", 50*time.Millisecond)
	inner := queenServer(t, h, "secret")
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		req, _ := http.NewRequest(r.Method, inner.URL+r.URL.Path, r.Body)
		req.Header = r.Header
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		w.Write(buf[:n])
	}))
	defer gate.Close()

	a := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "late", JoinURL: gate.URL, AdvertiseURL: "http://late:1"}, Token: "secret"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	if len(h.Members()) != 0 {
		t.Fatal("joined a queen that was not up")
	}
	up.Store(true)
	deadline := time.Now().Add(6 * time.Second)
	for len(h.Members()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(h.Members()) != 1 {
		t.Fatal("never joined after the queen came up")
	}
}

func TestAWorkerSaysWhyItCannotAnnounce(t *testing.T) {
	cases := map[string]*Announcer{
		"not a worker":      {Settings: HiveSettings{Role: HiveRoleQueen, JoinURL: "http://x", AdvertiseURL: "http://y"}},
		"no join url":       {Settings: HiveSettings{Role: HiveRoleWorker, Name: "a", AdvertiseURL: "http://y"}},
		"nowhere to reach":  {Settings: HiveSettings{Role: HiveRoleWorker, Name: "a", JoinURL: "http://x"}},
		"a name that fails": {Settings: HiveSettings{Role: HiveRoleWorker, Name: "a b", JoinURL: "http://x", AdvertiseURL: "http://y"}},
	}
	t.Setenv("SUPERAI_ADVERTISE_URL", "")
	for name, a := range cases {
		if err := a.Validate(); err == nil {
			t.Errorf("%s: no error", name)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%s: empty reason", name)
		}
	}
}

func TestTheAdvertiseAddressFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("SUPERAI_ADVERTISE_URL", "http://10.42.0.5:43117")
	a := Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "a", JoinURL: "http://x"}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAWorkerThatSaysGoodbyeComesOffTheRosterAtOnce(t *testing.T) {
	h := NewHive("q", time.Minute)
	x := hello("w0")
	x.StartedAt = time.Now()
	h.Join(x)
	if !h.Leave("w0", x.StartedAt) {
		t.Fatal("a goodbye was not honoured")
	}
	if len(h.Members()) != 0 {
		t.Fatalf("%+v", h.Members())
	}
}

func TestAnOldPodsGoodbyeDoesNotRemoveItsReplacement(t *testing.T) {
	// A StatefulSet reuses the name. The old pod's last words can arrive after
	// the new one has joined.
	h := NewHive("q", time.Minute)
	old := hello("w0")
	old.StartedAt = time.Now().Add(-time.Hour)
	h.Join(old)
	fresh := hello("w0")
	fresh.StartedAt = time.Now()
	h.Join(fresh)
	if h.Leave("w0", old.StartedAt) {
		t.Fatal("the old pod's goodbye removed the new pod")
	}
	if len(h.Members()) != 1 {
		t.Fatal("the replacement is gone")
	}
}

func TestAWorkerShuttingDownTellsTheQueen(t *testing.T) {
	h := NewHive("q", 30*time.Millisecond)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hive/join", func(w http.ResponseWriter, r *http.Request) {
		var x HiveHello
		json.NewDecoder(r.Body).Decode(&x)
		wl, _ := h.Join(x)
		json.NewEncoder(w).Encode(wl)
	})
	mux.HandleFunc("/api/hive/leave", func(w http.ResponseWriter, r *http.Request) {
		var x HiveHello
		json.NewDecoder(r.Body).Decode(&x)
		json.NewEncoder(w).Encode(map[string]any{"left": h.Leave(x.Name, x.StartedAt)})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "w9", JoinURL: srv.URL, AdvertiseURL: "http://w9:1"}, Token: "t"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(h.Members()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.Members()) != 1 {
		t.Fatal("never joined")
	}
	cancel()
	<-done
	if n := len(h.Members()); n != 0 {
		t.Fatalf("still on the roster after a clean shutdown: %d", n)
	}
}

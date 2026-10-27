package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every member draws the same queen from the same roster and term, nobody
// passed over is drawn, and a new term is a new draw.
func TestEveryMemberDrawsTheSameQueen(t *testing.T) {
	roster := []RosterEntry{{"superai-worker-0", "http://w0"}, {"superai-worker-1", "http://w1"}, {"superai-worker-2", "http://w2"}, {"mac", "http://mac"}}
	first, ok := ElectQueen(roster, 1, nil)
	if !ok {
		t.Fatal("nobody drawn")
	}
	shuffled := []RosterEntry{roster[3], roster[1], roster[0], roster[2]}
	if again, _ := ElectQueen(shuffled, 1, nil); again != first {
		t.Fatalf("the order of the roster changed the draw: %v, %v", first, again)
	}
	next, _ := ElectQueen(roster, 1, map[string]bool{first.Name: true})
	if next == first {
		t.Fatal("a member passed over was drawn")
	}
	if _, ok := ElectQueen(roster[:1], 1, map[string]bool{roster[0].Name: true}); ok {
		t.Fatal("drew from nobody")
	}
	seen := map[string]bool{}
	for term := int64(1); term <= 40; term++ {
		p, _ := ElectQueen(roster, term, nil)
		seen[p.Name] = true
	}
	if len(seen) < 3 {
		t.Fatalf("40 terms drew only %v: the draw is not spread", seen)
	}
}

// member is one SuperAI in a test hive: a worker announcing, which becomes a
// queen with its own roster when it is elected.
type member struct {
	name string
	srv  *httptest.Server
	ann  *Announcer

	mu       sync.Mutex
	hive     *Hive
	promoted chan int64
}

func (m *member) serveHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	h := m.hive
	m.mu.Unlock()
	if h == nil {
		http.Error(w, "this instance is not a queen", http.StatusNotFound)
		return
	}
	var hello HiveHello
	_ = json.NewDecoder(r.Body).Decode(&hello)
	welcome, err := h.Join(hello)
	if _, ok := IsSuperseded(err); ok {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(welcome)
}

func newMember(t *testing.T, name, join string) *member {
	m := &member{name: name, promoted: make(chan int64, 1)}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	t.Cleanup(m.srv.Close)
	m.ann = &Announcer{
		Settings: HiveSettings{Role: HiveRoleWorker, Name: name, JoinURL: join, AdvertiseURL: m.srv.URL},
		Token:    "t", retry: 20 * time.Millisecond,
	}
	m.ann.OnPromote = func(term int64) {
		h := NewHive(name, 50*time.Millisecond)
		h.SetTerm(term)
		m.mu.Lock()
		m.hive = h
		m.mu.Unlock()
		m.promoted <- term
	}
	return m
}

// The queen goes quiet; the workers, each on its own, agree on one of them,
// which takes over, and the others follow it under the new term.
func TestWhenTheQueenIsGoneTheWorkersAgreeOnANewOne(t *testing.T) {
	queen := &member{name: "queen", hive: NewHive("queen", 50*time.Millisecond)}
	queen.srv = httptest.NewServer(http.HandlerFunc(queen.serveHTTP))
	members := []*member{}
	for _, n := range []string{"superai-worker-0", "superai-worker-1", "superai-worker-2"} {
		members = append(members, newMember(t, n, queen.srv.URL))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, m := range members {
		go m.ann.Run(ctx)
	}
	// Everyone has joined, and has heard a roster with everyone on it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		all := true
		for _, m := range members {
			m.ann.mu.Lock()
			n := len(m.ann.roster)
			m.ann.mu.Unlock()
			all = all && n == len(members)
		}
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hive never formed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	queen.srv.Close()

	want, _ := ElectQueen(queen.hive.Roster(), 1, map[string]bool{"queen": true})
	var elected *member
	select {
	case term := <-func() chan int64 {
		c := make(chan int64, 1)
		for _, m := range members {
			go func(m *member) {
				select {
				case tm := <-m.promoted:
					m.mu.Lock()
					if elected == nil {
						elected = m
					}
					m.mu.Unlock()
					c <- tm
				case <-ctx.Done():
				}
			}(m)
		}
		return c
	}():
		if term != 1 {
			t.Fatalf("elected on term %d", term)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nobody took over")
	}
	if elected.name != want.Name {
		t.Fatalf("%s took over; the draw was %s", elected.name, want.Name)
	}
	// The others follow it.
	deadline = time.Now().Add(10 * time.Second)
	for {
		followers := 0
		for _, m := range elected.hive.Members() {
			if m.State == "live" {
				followers++
			}
		}
		if followers == len(members)-1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d followed the new queen", followers, len(members)-1)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, m := range members {
		if m != elected && (m.ann.Term() != 1 || m.ann.Queen() != elected.name) {
			t.Fatalf("%s follows %q on term %d", m.name, m.ann.Queen(), m.ann.Term())
		}
	}
}

// A queen that is behind hears it from the first worker that has moved on.
func TestAStaleQueenHearsItWasSuperseded(t *testing.T) {
	h := NewHive("old", time.Second)
	_, err := h.Join(HiveHello{Protocol: HiveProtocol, Name: "w", Role: HiveRoleWorker, URL: "http://w", Term: 3})
	if s, ok := IsSuperseded(err); !ok || s.Term != 3 {
		t.Fatalf("%v", err)
	}
	h.SetTerm(3)
	if w, err := h.Join(HiveHello{Protocol: HiveProtocol, Name: "w", Role: HiveRoleWorker, URL: "http://w", Term: 3}); err != nil || w.Term != 3 || len(w.Roster) != 1 {
		t.Fatalf("%+v %v", w, err)
	}
}

// A queen elected from among the workers is not scaled away.
func TestAnElectedWorkerQueenIsNotScaledAway(t *testing.T) {
	t.Setenv("POD_NAME", "superai-worker-2")
	k := &kubectlSpawner{s: SpawnerSettings{Kind: "kubectl", StatefulSet: "superai-worker", MaxWorkers: 20, Kubectl: "/bin/false"}}
	if err := k.SetReplicas(context.Background(), 2); err == nil || !strings.Contains(err.Error(), "at least 3") {
		t.Fatalf("%v", err)
	}
	if _, ok := statefulOrdinal("superai-queen-0", "superai-worker"); ok {
		t.Fatal("the queen's own pod counted as a worker")
	}
}

// A queen who said she is restarting is waited for; one who is still gone
// when the wait is over is replaced.
func TestAQueenWhoSaidSheIsRestartingIsWaitedFor(t *testing.T) {
	queen := &member{name: "queen", hive: NewHive("queen", 50*time.Millisecond)}
	queen.srv = httptest.NewServer(http.HandlerFunc(queen.serveHTTP))
	w := newMember(t, "superai-worker-0", queen.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.ann.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.ann.mu.Lock()
		n := len(w.ann.roster)
		w.ann.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never joined")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.ann.Hold(700 * time.Millisecond)
	queen.srv.Close()
	select {
	case <-w.promoted:
		t.Fatal("elected while the queen said she was restarting")
	case <-time.After(500 * time.Millisecond):
	}
	select {
	case <-w.promoted:
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting after the hold ran out")
	}
}

// A member following an elected queen by her own address, who has stepped
// down since, goes back to the address that always leads to the queen
// instead of electing again — the flapping seen on the cluster.
func TestAFollowerOfAQueenWhoSteppedDownGoesHomeInsteadOfElecting(t *testing.T) {
	home := &member{name: "superai-queen-0", hive: NewHive("superai-queen-0", 50*time.Millisecond)}
	home.hive.SetTerm(5)
	home.srv = httptest.NewServer(http.HandlerFunc(home.serveHTTP))
	t.Cleanup(home.srv.Close)
	stepped := &member{name: "superai-worker-0"} // up, but no longer a queen: 404
	stepped.srv = httptest.NewServer(http.HandlerFunc(stepped.serveHTTP))
	t.Cleanup(stepped.srv.Close)

	w := newMember(t, "superai-worker-1", home.srv.URL)
	w.ann.term, w.ann.queen, w.ann.target = 4, stepped.name, stepped.srv.URL
	w.ann.roster = []RosterEntry{{stepped.name, stepped.srv.URL}, {w.name, w.srv.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.ann.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for w.ann.Queen() != home.name {
		if time.Now().After(deadline) {
			t.Fatalf("still following %q", w.ann.Queen())
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case term := <-w.promoted:
		t.Fatalf("elected itself on term %d instead of going home", term)
	default:
	}
	if w.ann.Term() != 5 {
		t.Fatalf("term %d", w.ann.Term())
	}
}

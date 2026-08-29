package backend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
)

// fakeSource is a graph that is whatever the test says it is. It stands in for
// a database or a shared brain so the lifecycle can be exercised without
// either — every one of these tests would otherwise need a server on the
// network or a sqlite file on disk to say something about a mutex.
func fakeSource(name string, nodes int) func(*Settings) (*liveview.Source, error) {
	return func(*Settings) (*liveview.Source, error) {
		return &liveview.Source{
			Describe: name,
			Read: func(context.Context) ([]liveview.Node, []liveview.Edge, error) {
				out := make([]liveview.Node, nodes)
				for i := range out {
					out[i] = liveview.Node{ID: string(rune('a' + i)), Label: "n", Type: "entity"}
				}
				return out, nil, nil
			},
			Close: func() error { return nil },
		}, nil
	}
}

func TestGraphKeyFollowsTheConfiguredBackend(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")

	local := graphKey(&Settings{MemoryBackend: MemoryBackendLocal})
	if want := "local:" + LocalBrainPath(); local != want {
		t.Fatalf("local key = %q, want %q", local, want)
	}
	if !strings.HasSuffix(local, filepath.Join("data", "cortex.db")) {
		t.Fatalf("local key does not point at the agent's data dir: %q", local)
	}

	shared := graphKey(&Settings{MemoryBackend: MemoryBackendShared, SharedMemoryEndpoint: "brain:9000"})
	if shared != "shared:brain:9000" {
		t.Fatalf("shared key = %q", shared)
	}
	if shared == local {
		t.Fatal("local and shared brains share a key; switching backends would not restart the view")
	}

	// nil settings must not panic — GraphView is reachable before boot finishes.
	if k := graphKey(nil); k != local {
		t.Fatalf("nil settings key = %q, want the local default %q", k, local)
	}
}

func TestGraphKeyReadsTheEnvironmentFallback(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "env-brain:9000")

	// Same fallback the memory backend itself uses, so the view and the agent
	// cannot end up on different servers.
	if k := graphKey(&Settings{MemoryBackend: MemoryBackendShared}); k != "shared:env-brain:9000" {
		t.Fatalf("key = %q, want the CORTEXDB_REMOTE fallback", k)
	}
}

func TestGraphSourceRefusesSharedWithoutAnEndpoint(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")

	src, err := GraphSource(&Settings{MemoryBackend: MemoryBackendShared})
	if err == nil {
		_ = src.Close()
		t.Fatal("shared with no endpoint opened a source; it would have shown the local brain instead")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("error does not name the missing endpoint: %v", err)
	}
}

func TestGraphSourceDescribesTheSharedBrain(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	src, err := GraphSource(&Settings{
		MemoryBackend:        MemoryBackendShared,
		SharedMemoryEndpoint: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("GraphSource: %v", err)
	}
	defer func() { _ = src.Close() }()

	if !strings.Contains(src.Describe, "127.0.0.1:1") {
		t.Fatalf("Describe = %q, want the endpoint in it", src.Describe)
	}
	// Nothing is listening there, so the read must fail rather than quietly
	// report an empty graph — an empty brain and an unreachable one look the
	// same on screen otherwise.
	if _, _, rerr := src.Read(context.Background()); rerr == nil {
		t.Fatal("read of an unreachable brain succeeded")
	}
}

// The local backend is the default, so this one opens a real database rather
// than a stand-in: the thing worth checking is that the path the view derives
// is a path CortexDB will actually open, which no fake can tell us.
func TestGraphSourceOpensTheLocalBrain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	src, err := GraphSource(&Settings{MemoryBackend: MemoryBackendLocal})
	if err != nil {
		t.Fatalf("GraphSource: %v", err)
	}
	defer func() { _ = src.Close() }()

	if src.Describe != LocalBrainPath() {
		t.Fatalf("Describe = %q, want %q", src.Describe, LocalBrainPath())
	}
	nodes, edges, err := src.Read(context.Background())
	if err != nil {
		t.Fatalf("read a fresh local brain: %v", err)
	}
	// A brain nobody has written to yet reads as empty, not as an error. The
	// page has to be able to tell those apart.
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("a fresh brain has %d nodes and %d edges", len(nodes), len(edges))
	}
}

func TestGraphSourceRejectsNilSettings(t *testing.T) {
	if _, err := GraphSource(nil); err == nil {
		t.Fatal("nil settings opened a source")
	}
}

func TestEnsureStartsOneViewAndReusesIt(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	g := &GraphViews{open: fakeSource("brain-one", 3)}
	defer func() { _ = g.Close() }()

	s := &Settings{MemoryBackend: MemoryBackendLocal}
	first, err := g.Ensure(context.Background(), s)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	second, err := g.Ensure(context.Background(), s)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if first != second {
		t.Fatal("opening the page twice started two servers")
	}
	if first.URL() == "" || !strings.HasPrefix(first.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want a loopback address", first.URL())
	}
	// Start reads once before serving, so the counts are there for the first
	// page load rather than a poll later.
	if got := len(first.Snapshot().Nodes); got != 3 {
		t.Fatalf("snapshot has %d nodes, want 3", got)
	}
}

func TestEnsureServesTheLivePage(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	g := &GraphViews{open: fakeSource("brain-one", 1)}
	defer func() { _ = g.Close() }()

	sv, err := g.Ensure(context.Background(), &Settings{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	resp, err := http.Get(sv.URL())
	if err != nil {
		t.Fatalf("GET %s: %v", sv.URL(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatal("the URL does not serve a page")
	}
}

func TestEnsureRestartsWhenTheBrainChanges(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")
	g := &GraphViews{open: fakeSource("brain-one", 1)}
	defer func() { _ = g.Close() }()

	first, err := g.Ensure(context.Background(), &Settings{MemoryBackend: MemoryBackendLocal})
	if err != nil {
		t.Fatalf("Ensure local: %v", err)
	}
	oldURL := first.URL()

	second, err := g.Ensure(context.Background(), &Settings{
		MemoryBackend:        MemoryBackendShared,
		SharedMemoryEndpoint: "brain:9000",
	})
	if err != nil {
		t.Fatalf("Ensure shared: %v", err)
	}
	if first == second {
		t.Fatal("switching the memory backend kept the old brain's view")
	}
	// The old listener has to be gone, not merely forgotten: a stale view of
	// the previous brain still answering on its port is the failure this key
	// exists to prevent.
	if _, err := http.Get(oldURL); err == nil {
		t.Fatalf("the previous view is still serving %s", oldURL)
	}
}

func TestEnsureReportsAFailedOpen(t *testing.T) {
	g := &GraphViews{open: func(*Settings) (*liveview.Source, error) {
		return nil, errors.New("brain is not answering")
	}}
	defer func() { _ = g.Close() }()

	if _, err := g.Ensure(context.Background(), &Settings{}); err == nil {
		t.Fatal("Ensure succeeded with a source that cannot be opened")
	}
	if g.Running() != nil {
		t.Fatal("a failed start left a view behind")
	}
	// And it must be retryable: the shared brain coming back should not need an
	// app restart.
	g.open = fakeSource("brain-two", 1)
	if _, err := g.Ensure(context.Background(), &Settings{}); err != nil {
		t.Fatalf("retry after a failure: %v", err)
	}
	if g.Running() == nil {
		t.Fatal("retry did not record the view")
	}
}

func TestCloseStopsServingAndIsIdempotent(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	closed := false
	g := &GraphViews{open: func(*Settings) (*liveview.Source, error) {
		src, _ := fakeSource("brain-one", 1)(nil)
		src.Close = func() error { closed = true; return nil }
		return src, nil
	}}

	sv, err := g.Ensure(context.Background(), &Settings{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	url := sv.URL()

	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !closed {
		t.Fatal("Close did not release the brain")
	}
	if _, err := http.Get(url); err == nil {
		t.Fatalf("still serving %s after Close", url)
	}
	// Shutdown paths get run more than once; the second one must not panic.
	if err := g.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if g.Running() != nil {
		t.Fatal("Running reports a view after Close")
	}
}

func TestCloseOnAZeroValueDoesNothing(t *testing.T) {
	var g GraphViews
	if err := g.Close(); err != nil {
		t.Fatalf("Close on a zero value: %v", err)
	}
}

func TestGraphStatusDescribesTheView(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	g := &GraphViews{open: fakeSource("shared brain brain:9000", 2)}
	defer func() { _ = g.Close() }()

	st := g.GraphStatus(context.Background(), &Settings{
		MemoryBackend:        MemoryBackendShared,
		SharedMemoryEndpoint: "brain:9000",
	})
	if st.Error != "" {
		t.Fatalf("Error = %q", st.Error)
	}
	if st.Backend != MemoryBackendShared {
		t.Fatalf("Backend = %q, want %q", st.Backend, MemoryBackendShared)
	}
	if st.Source != "shared brain brain:9000" {
		t.Fatalf("Source = %q", st.Source)
	}
	if st.Nodes != 2 {
		t.Fatalf("Nodes = %d, want 2", st.Nodes)
	}
	if st.Activity {
		t.Fatal("Activity is true; SuperAI does not feed tool calls to the view")
	}

	m := st.Map()
	for _, k := range []string{"url", "source", "backend", "nodes", "edges", "activity", "error"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("Map is missing %q; the page reads it", k)
		}
	}
}

func TestGraphStatusCarriesTheReasonThereIsNoView(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")

	var g GraphViews
	defer func() { _ = g.Close() }()

	// Shared selected, no endpoint: the real path a user lands on by flipping
	// the backend in Settings and not filling the rest in.
	st := g.GraphStatus(context.Background(), &Settings{MemoryBackend: MemoryBackendShared})
	if st.Error == "" {
		t.Fatal("no error reported for a shared backend with no endpoint")
	}
	if st.URL != "" {
		t.Fatalf("URL = %q, want none", st.URL)
	}
	if st.Backend != MemoryBackendShared {
		t.Fatalf("Backend = %q; the page has to say which brain failed", st.Backend)
	}
}

package backend

// The live 3D view of the brain.
//
// CortexDB can serve its own knowledge graph as a rotatable WebGL page on
// loopback, and SuperAI reads that brain on every turn. This wires the two
// together so the app can show the graph it is actually thinking with, rather
// than a picture of one exported at some point in the past.
//
// Two decisions are worth stating outright.
//
// Which brain: it comes from Settings, never from the environment. The user
// picks local or shared in the Settings page, and asking them to also export
// CORTEXDB_REMOTE before the graph page works would be a second, invisible
// place to configure the same thing — and a way for the page to quietly show a
// different brain than the agent uses.
//
// Which handle: the view opens its own connection to the local database rather
// than borrowing the one Service holds. Service is closed and rebuilt whenever
// settings are saved, and a view polling a handle someone else closed goes
// permanently stale with nothing on screen to say so. The read is read-only and
// SQLite is happy with a second reader, so an independent handle costs a file
// descriptor and buys a lifetime this package controls.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/liliang-cn/agent-go/v3/pkg/config"
	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
)

// LocalBrainPath is the CortexDB file the local memory backend writes to.
//
// Derived through agent-go's config rather than by joining the path here, so it
// cannot drift from the one NewService opens a few hundred lines away in
// service.go. Two functions that each know where the database "is" is how a
// view ends up rendering an empty file next to a full one.
func LocalBrainPath() string {
	cfg := &config.Config{Home: DataDir()}
	return cfg.CortexDBPath()
}

// GraphSource builds the liveview source for the brain these settings select.
//
// The shared case keys off MemoryBackend alone, not UseSharedMemory: a user who
// picked the shared brain and has not filled in an endpoint gets an error
// naming that, rather than a view of the local store labelled as if it were the
// shared one. Being wrong about which brain is on screen is worse than having
// no brain on screen.
func GraphSource(s *Settings) (*liveview.Source, error) {
	if s == nil {
		return nil, errors.New("settings are not loaded yet")
	}

	if s.MemoryBackend == MemoryBackendShared {
		addr := s.SharedMemoryEndpointResolved()
		if addr == "" {
			return nil, errors.New("shared memory is selected but no endpoint is set (Settings → Memory)")
		}
		token := s.SharedMemoryTokenResolved()
		return &liveview.Source{
			Describe: "shared brain " + addr,
			Read: func(ctx context.Context) ([]liveview.Node, []liveview.Edge, error) {
				// limit 0 asks for the server's own cap; quiet because this
				// runs on a two-second timer and the truncation note would
				// become the only thing in the log.
				return liveview.LoadRemote(ctx, addr, token, 0, true)
			},
			Close: func() error { return nil },
		}, nil
	}

	path := LocalBrainPath()
	// No embedder and no reranker: the view reads the graph tables and never
	// embeds anything, so wiring up a model it will not call would only add a
	// way for a read-only picture to fail.
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &liveview.Source{
		Describe: path,
		Read: func(ctx context.Context) ([]liveview.Node, []liveview.Edge, error) {
			return liveview.LoadLocal(ctx, db.SQL())
		},
		Close: db.Close,
	}, nil
}

// graphKey names the brain a view was started against.
//
// Kept as a pure function of the settings — deliberately not Source.Describe —
// so the running view can be compared against the current settings without
// opening a database to find out whether it already matches.
func graphKey(s *Settings) string {
	if s != nil && s.MemoryBackend == MemoryBackendShared {
		return "shared:" + s.SharedMemoryEndpointResolved()
	}
	return "local:" + LocalBrainPath()
}

// GraphViews holds the one live view this process serves.
//
// One, not one per visit: a view is a listener plus a poller reading the whole
// graph every couple of seconds, and starting a fresh pair each time someone
// opened the page would leave a trail of them running for the life of the app.
// Opening the page again means "show me", not "start another".
//
// The key records which brain it was started against. Switching the memory
// backend in Settings therefore replaces the view instead of leaving the old
// brain on screen under the new label — the failure that would otherwise be
// invisible, because both views look equally alive.
type GraphViews struct {
	mu   sync.Mutex
	key  string
	sv   *liveview.Server
	stop context.CancelFunc

	// open builds the source. Nil means GraphSource; tests replace it to
	// exercise the lifecycle without a database or a network.
	open func(*Settings) (*liveview.Source, error)
}

// Ensure returns the live view of the brain these settings select, starting it
// on first use. It is safe to call from any goroutine and cheap once running.
//
// Callers must not hold a lock that a settings rebuild also takes: starting a
// view does a full read of the brain first, and against a shared one that is a
// network round trip with a timeout measured in seconds.
func (g *GraphViews) Ensure(ctx context.Context, s *Settings) (*liveview.Server, error) {
	key := graphKey(s)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sv != nil && g.key == key {
		return g.sv, nil
	}
	g.closeLocked()

	openFn := g.open
	if openFn == nil {
		openFn = GraphSource
	}
	src, err := openFn(s)
	if err != nil {
		return nil, err
	}

	// The view outlives the call that asked for it, so its poller must not be
	// tied to that call's context — but it must still be stoppable, or the
	// goroutine polls the brain forever after the window closes. Hence a
	// cancellation of our own, ended by Close.
	viewCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	// activity=false: tool-call events reach a live view through CortexDB's own
	// MCP middleware, and SuperAI's tools do not run through it. The page reads
	// the flag and says "structure only" rather than leaving a still ticker to
	// be mistaken for a fault.
	sv, err := liveview.Start(viewCtx, src, liveview.DefaultPort, liveview.DefaultInterval, false)
	if err != nil {
		cancel()
		if src.Close != nil {
			_ = src.Close()
		}
		return nil, err
	}

	g.sv, g.key, g.stop = sv, key, cancel
	return sv, nil
}

// Running returns the view without starting one, or nil. The app uses it on
// shutdown and in tests; nothing should start a server just to ask whether one
// is running.
func (g *GraphViews) Running() *liveview.Server {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sv
}

// Close stops the view and releases the brain. Safe on a zero value and safe to
// call twice, because shutdown paths get run more than once.
func (g *GraphViews) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closeLocked()
}

func (g *GraphViews) closeLocked() error {
	if g.stop != nil {
		g.stop()
		g.stop = nil
	}
	sv := g.sv
	g.sv, g.key = nil, ""
	if sv == nil {
		return nil
	}
	// Server.Close also closes the source, which is what releases the database
	// handle GraphSource opened.
	return sv.Close()
}

// GraphViewStatus is everything the page needs to explain itself: where the
// view is, which brain it reads, and how much is in it. A page that can only
// say "it did not work" leaves the user guessing between an unreachable shared
// brain and an empty local one, which are opposite problems.
type GraphViewStatus struct {
	URL     string `json:"url"`
	Source  string `json:"source"`
	Backend string `json:"backend"`
	Nodes   int    `json:"nodes"`
	Edges   int    `json:"edges"`
	// Activity reports whether tool calls light the graph up, so the page can
	// say the ticker is quiet on purpose.
	Activity bool `json:"activity"`
	// Error is the reason there is no view, empty when there is one. Carried in
	// the payload rather than returned as a failure because the reason is the
	// content of the page in that case.
	Error string `json:"error"`
}

// Map renders the status the way the frontend reads it. The bound methods on
// App hand plain maps to the generated bindings (see GetStatus, CLIProxyStatus)
// rather than growing the generated models file for one panel.
func (st GraphViewStatus) Map() map[string]any {
	return map[string]any{
		"url":      st.URL,
		"source":   st.Source,
		"backend":  st.Backend,
		"nodes":    st.Nodes,
		"edges":    st.Edges,
		"activity": st.Activity,
		"error":    st.Error,
	}
}

// GraphStatus starts the view if needed and describes it.
func (g *GraphViews) GraphStatus(ctx context.Context, s *Settings) GraphViewStatus {
	st := GraphViewStatus{Backend: MemoryBackendLocal}
	if s != nil && s.MemoryBackend == MemoryBackendShared {
		st.Backend = MemoryBackendShared
	}

	sv, err := g.Ensure(ctx, s)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	snap := sv.Snapshot()
	st.URL = sv.URL()
	st.Source = sv.SourceName()
	st.Nodes = len(snap.Nodes)
	st.Edges = len(snap.Edges)
	st.Activity = sv.WatchesCalls()
	return st
}

package main

import (
	"net/http"
	"testing"

	"github.com/liliang-cn/superai-desktop/backend"
)

// The bound method is the whole contract with the page: it must start the view
// on demand, hand back a usable URL and the counts that let the page explain
// itself, and return the same view the second time rather than a second port.
func TestGraphViewStartsOnDemandAndStaysPut(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")

	a := NewApp()
	a.settings = &backend.Settings{MemoryBackend: backend.MemoryBackendLocal}
	defer func() { _ = a.graphs.Close() }()

	// Nothing runs until someone asks. Boot must not have paid for a page most
	// sessions never open.
	if a.graphs.Running() != nil {
		t.Fatal("a view was running before GraphView was called")
	}

	st := a.GraphView()
	if msg, _ := st["error"].(string); msg != "" {
		t.Fatalf("GraphView: %s", msg)
	}
	url, _ := st["url"].(string)
	if url == "" {
		t.Fatal("no URL returned")
	}
	if got, _ := st["backend"].(string); got != backend.MemoryBackendLocal {
		t.Fatalf("backend = %q, want local", got)
	}
	if got, _ := st["source"].(string); got != backend.LocalBrainPath() {
		t.Fatalf("source = %q, want the local brain path", got)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("the returned URL is not serving: %v", err)
	}
	_ = resp.Body.Close()

	if again, _ := a.GraphView()["url"].(string); again != url {
		t.Fatalf("second call moved the view from %s to %s", url, again)
	}
}

// Opening the page before the app has settings must produce a reason, not a
// crash and not a blank frame.
func TestGraphViewWithoutSettingsExplainsItself(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	a := NewApp()
	defer func() { _ = a.graphs.Close() }()

	st := a.GraphView()
	if msg, _ := st["error"].(string); msg == "" {
		t.Fatal("no settings and no error; the page would show an empty frame")
	}
	if url, _ := st["url"].(string); url != "" {
		t.Fatalf("url = %q, want none", url)
	}
}

// shutdown has to take the listener down with it. A view left serving the brain
// after the window closed is an unauthenticated read of everything in it.
func TestShutdownStopsTheGraphView(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	t.Setenv("CORTEXDB_REMOTE", "")

	a := NewApp()
	a.settings = &backend.Settings{MemoryBackend: backend.MemoryBackendLocal}
	url, _ := a.GraphView()["url"].(string)
	if url == "" {
		t.Fatal("no view to shut down")
	}

	a.shutdown(nil)

	if _, err := http.Get(url); err == nil {
		t.Fatalf("still serving %s after shutdown", url)
	}
	if a.graphs.Running() != nil {
		t.Fatal("shutdown left a view behind")
	}
}

package backend

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The view SuperAI is proxying to, standing in for liveview.
func fakeView(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body>graph at `+r.URL.Path+`</body></html>`)
	})
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"nodes":[],"edges":[]}`)
	})
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(w, "event: tick\ndata: {}\n\n")
			if f != nil {
				f.Flush()
			}
			time.Sleep(30 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The whole point: a browser that cannot reach the loopback view reaches it
// through SuperAI, which runs on the same host and does have a front door.
func TestProxyServesThePage(t *testing.T) {
	view := fakeView(t)
	h := NewGraphProxy(func() string { return view.URL })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, GraphProxyPrefix+"/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("body is not the page: %q", rec.Body.String())
	}
}

// The page asks for its stream relative to itself, so it arrives prefixed. The
// prefix has to come off or the view answers 404 for its own endpoints.
func TestProxyStripsItsPrefix(t *testing.T) {
	view := fakeView(t)
	h := NewGraphProxy(func() string { return view.URL })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, GraphProxyPrefix+"/api/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d — the prefix reached the view", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"nodes"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// A path without a trailing slash makes every relative URL on the page resolve
// one level too high, which is a broken page rather than an error anyone sees.
func TestProxyRedirectsToATrailingSlash(t *testing.T) {
	view := fakeView(t)
	h := NewGraphProxy(func() string { return view.URL })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, GraphProxyPrefix, nil))
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusFound {
		t.Fatalf("status %d, want a redirect to the trailing slash", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != GraphProxyPrefix+"/" {
		t.Errorf("Location = %q", loc)
	}
}

// The view is a live stream. A proxy that buffers turns it into a page that
// never updates — the one thing this view is for.
func TestProxyStreamsWithoutBuffering(t *testing.T) {
	view := fakeView(t)
	h := NewGraphProxy(func() string { return view.URL })
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + GraphProxyPrefix + "/api/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	// The first frame must arrive well before the handler finishes writing all
	// of them; if it does not, something in the middle is holding the response.
	buf := make([]byte, 64)
	done := make(chan int, 1)
	go func() {
		n, _ := resp.Body.Read(buf)
		done <- n
	}()
	select {
	case n := <-done:
		if n == 0 {
			t.Error("read nothing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame arrived — the proxy is buffering the stream")
	}
}

// No view running is a plain answer, not a stack trace or a hang.
func TestProxyWithNoViewSaysSo(t *testing.T) {
	h := NewGraphProxy(func() string { return "" })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, GraphProxyPrefix+"/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "not running") {
		t.Errorf("body = %q, want it to say why", rec.Body.String())
	}
}

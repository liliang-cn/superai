package backend

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Reaching the live graph view from a browser that is not on the SuperAI host.
//
// The view binds 127.0.0.1 and has no authentication: the page it serves is the
// whole brain, so a listener on any other interface would be an unauthenticated
// read of everything in it. That is right, and it means a browser anywhere else
// — which is how SuperAI is actually used, over the network at a domain —
// cannot reach it at all. The page used to explain that and offer an `ssh -L`
// command, which is honest and is not a feature working.
//
// SuperAI runs on that host and already has a front door. So it proxies: the
// tab asks SuperAI, SuperAI asks loopback, and the view inherits the
// authentication it does not have. This is exactly what liveview's own package
// documentation tells an embedder to do.
//
// The proxy is mounted behind requireAuth like every other non-public path. It
// must be: without that it would be the wider listener the view deliberately
// refuses to open.

// GraphProxyPrefix is where the view is mounted.
const GraphProxyPrefix = "/graph"

// NewGraphProxy returns a handler that forwards to whatever view is running.
//
// The target is looked up per request rather than captured once: the view
// starts on demand, and it restarts on a different port whenever the memory
// backend changes. A proxy holding the first address it ever saw would answer
// for a brain nobody is reading any more.
func NewGraphProxy(target func() string) http.Handler {
	proxy := &httputil.ReverseProxy{
		// Flushed immediately because this is a live stream. The default
		// buffers, which would turn a view whose whole point is keeping up
		// into a page that draws once and then sits there.
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			raw := target()
			if raw == "" {
				return
			}
			u, err := url.Parse(raw)
			if err != nil {
				return
			}
			r.Out.URL.Scheme = u.Scheme
			r.Out.URL.Host = u.Host
			r.Out.Host = u.Host
			// The page asks for its stream relative to itself, so requests
			// arrive under the mount point. The view knows nothing about that
			// prefix and would 404 its own endpoints.
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, GraphProxyPrefix)
			if r.Out.URL.Path == "" {
				r.Out.URL.Path = "/"
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// The view is loopback on this host, so a failure here is it being
			// gone rather than a network anyone can do something about.
			http.Error(w, "the graph view is not running: "+err.Error(), http.StatusServiceUnavailable)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target() == "" {
			http.Error(w, "the graph view is not running", http.StatusServiceUnavailable)
			return
		}
		// Without the trailing slash every relative URL on the page resolves
		// one level too high — a page that loads and then quietly fails to
		// find its own stream, which is worse than an error.
		if r.URL.Path == GraphProxyPrefix {
			http.Redirect(w, r, GraphProxyPrefix+"/", http.StatusMovedPermanently)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// graphProxyIdleTimeout is unused by the proxy itself and kept as the reason
// the server's write timeout must not apply here: an SSE connection is meant to
// stay open, and a deadline would cut the stream on a schedule.
const graphProxyIdleTimeout = 0 * time.Second

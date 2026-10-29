package app

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
)

// startPprof serves Go's profiler when SUPERLEO_PPROF names an address, and
// only a loopback one: it is for `kubectl port-forward` into a pod or a shell
// on the machine, never for the network, and it is off unless asked for.
func startPprof() {
	addr := strings.TrimSpace(os.Getenv("SUPERLEO_PPROF"))
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		slog.Warn("SUPERLEO_PPROF ignored: it must be a loopback address such as 127.0.0.1:46061", "addr", addr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	go func() { slog.Warn("pprof stopped", "error", http.ListenAndServe(addr, mux)) }()
}

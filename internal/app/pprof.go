package app

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
)

// startPprof serves Go's profiler when SUPERAI_PPROF names an address, and
// only a loopback one: it is for `kubectl port-forward` into a pod or a shell
// on the machine, never for the network, and it is off unless asked for.
func startPprof() {
	addr := strings.TrimSpace(os.Getenv("SUPERAI_PPROF"))
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		log.Printf("SUPERAI_PPROF=%q ignored: it must be a loopback address such as 127.0.0.1:46061", addr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	go func() { log.Printf("pprof: %v", http.ListenAndServe(addr, mux)) }()
}

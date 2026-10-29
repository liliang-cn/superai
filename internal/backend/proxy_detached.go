package backend

// The way out for a CLI that outlives SuperLeo (a detached coding run).
//
// macOS grants "Local Network" to the app, and a program it started is let
// onto the LAN on the app's account. Once SuperLeo quits, the orphaned CLI
// is not: every connection to 192.168.x fails with EHOSTUNREACH, so a proxy
// set to a LAN address (the home mihomo) cuts the run off from its model the
// moment SuperLeo goes, and it dies a few minutes later on "No internet
// route". Loopback and the internet itself need no such grant.
//
// So a detached CLI is handed a proxy on 127.0.0.1: the one set, if it is on
// loopback already; else a proxy this machine runs itself (the system proxy
// when it points at loopback, else Clash on its usual ports). With no proxy
// set it gets none, as before: going out directly needs no grant either.
// Only when nothing local answers does it keep the LAN proxy, which works
// for as long as SuperLeo is up.
//
// A forwarder of SuperLeo's own on 127.0.0.1 would have the same trouble:
// left running after SuperLeo quits, it would be one more orphan barred from
// the LAN proxy it forwards to.

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// localProxyPorts are where a proxy app on this machine usually listens:
// Clash Verge's mixed port, then Clash / ClashX / mihomo's.
var localProxyPorts = []string{"7897", "7890"}

// localProxyListening reports whether something accepts connections at
// host:port; a variable so tests need no proxy running.
var localProxyListening = func(hostport string) bool {
	c, err := net.DialTimeout("tcp", hostport, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// DetachedProxyEnv is ProxyEnv for a program that may outlive SuperLeo: the
// same variables, but with a proxy on a LAN address swapped for one on
// loopback when this machine has one. The environment mode, which ProxyEnv
// leaves alone, is looked at too: an inherited HTTPS_PROXY is just as much
// on the LAN.
func DetachedProxyEnv() map[string]string {
	env := ProxyEnv()
	p := currentProxy()
	cur, err := proxyForURL(p, &url.URL{Scheme: "https", Host: "api.anthropic.com"})
	if err != nil || cur == nil || isLoopbackProxy(cur) {
		return env
	}
	lo := loopbackProxy()
	if lo == nil {
		return env
	}
	if env == nil {
		env = map[string]string{}
		noProxy := "localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,.local,.ts.net"
		env["NO_PROXY"], env["no_proxy"] = noProxy, noProxy
	}
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"} {
		env[k] = lo.String()
	}
	return env
}

// loopbackProxy is a proxy on this machine that answers, or nil.
func loopbackProxy() *url.URL {
	if sys := systemProxy().forScheme("https"); sys != nil && isLoopbackProxy(sys) && localProxyListening(sys.Host) {
		return sys
	}
	for _, port := range localProxyPorts {
		hp := net.JoinHostPort("127.0.0.1", port)
		if localProxyListening(hp) {
			return &url.URL{Scheme: "http", Host: hp}
		}
	}
	return nil
}

func isLoopbackProxy(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

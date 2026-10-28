package backend

// The proxy SuperLeo's own requests go through: model calls, web search and
// fetch, MCP servers reached by URL, quotes, and the agent CLIs it starts.
//
// Four modes (Settings → Runtime → Proxy):
//
//	""        the environment (HTTPS_PROXY, ALL_PROXY …), as before there was a setting
//	"off"     direct
//	"system"  the system proxy: on macOS what `scutil --proxy` reports, so
//	          Clash Verge's "system proxy" switch is followed as it is flipped
//	"manual"  Proxy.URL: http://, https:// or socks5://
//
// Whatever the mode, the hive's own traffic stays direct: loopback, private
// and Tailscale addresses, .local and .ts.net names, and single-label host
// names (orange1, infra). Sent through a proxy abroad, an agent link to
// 192.168.123.x would simply fail.
//
// InstallProxy puts ProxyFor on Go's default transport and agent-go's LLM
// pool once, at start; ApplyProxy changes what it answers from the next
// request on, so a change in Settings needs no restart.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/pool"
)

// Proxy modes.
const (
	ProxyModeEnv    = ""
	ProxyModeOff    = "off"
	ProxyModeSystem = "system"
	ProxyModeManual = "manual"
)

// ProxySettings is Settings.Proxy.
type ProxySettings struct {
	Mode string `json:"mode,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Validate reports a mode or URL the proxy cannot be set to.
func (p ProxySettings) Validate() error {
	switch p.Mode {
	case ProxyModeEnv, ProxyModeOff, ProxyModeSystem:
		return nil
	case ProxyModeManual:
		_, err := parseProxyURL(p.URL)
		return err
	}
	return fmt.Errorf("unknown proxy mode %q", p.Mode)
}

func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("the proxy address is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy address %q: %w", raw, err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("proxy address %q: use http://, https:// or socks5://", raw)
	}
	if u.Hostname() == "" || u.Port() == "" {
		return nil, fmt.Errorf("proxy address %q needs a host and a port, e.g. http://127.0.0.1:7897", raw)
	}
	return u, nil
}

var (
	proxyNow     atomic.Pointer[ProxySettings]
	proxyInstall sync.Once
)

// InstallProxy routes the process's outbound HTTP through ProxyFor. Call it
// once, before anything makes a request; later calls do nothing.
func InstallProxy() {
	proxyInstall.Do(func() {
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			t.Proxy = ProxyFor
		}
		pool.SetProxy(ProxyFor)
	})
}

// ApplyProxy makes p the proxy from the next request on.
func ApplyProxy(p ProxySettings) {
	p.Mode = strings.TrimSpace(p.Mode)
	p.URL = strings.TrimSpace(p.URL)
	proxyNow.Store(&p)
}

func currentProxy() ProxySettings {
	if p := proxyNow.Load(); p != nil {
		return *p
	}
	return ProxySettings{}
}

// ProxyFor is the proxy a request goes through, nil for direct.
func ProxyFor(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	return proxyForURL(currentProxy(), req.URL)
}

func proxyForURL(p ProxySettings, target *url.URL) (*url.URL, error) {
	host := target.Hostname()
	if StaysDirect(host) {
		return nil, nil
	}
	switch p.Mode {
	case ProxyModeOff:
		return nil, nil
	case ProxyModeManual:
		return parseProxyURL(p.URL)
	case ProxyModeSystem:
		sys := systemProxy()
		if sys.excepted(host) {
			return nil, nil
		}
		return sys.forScheme(target.Scheme), nil
	}
	return http.ProxyFromEnvironment(&http.Request{URL: target})
}

var (
	cgnat = mustCIDR("100.64.0.0/10")
	ula   = mustCIDR("fd00::/8")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// StaysDirect reports whether a host is the hive's own, reached without a
// proxy whatever the setting.
func StaysDirect(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
			cgnat.Contains(ip) || ula.Contains(ip)
	}
	if !strings.Contains(host, ".") {
		return true // orange1, infra: a name only the LAN resolves
	}
	for _, suffix := range []string{".local", ".localhost", ".lan", ".home.arpa", ".ts.net", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// sysProxy is the system proxy as the OS reports it.
type sysProxy struct {
	http, https, socks *url.URL
	pac                bool
	except             []string
}

func (s sysProxy) forScheme(scheme string) *url.URL {
	if scheme == "https" && s.https != nil {
		return s.https
	}
	if scheme == "http" && s.http != nil {
		return s.http
	}
	if s.socks != nil {
		return s.socks
	}
	if s.https != nil {
		return s.https
	}
	return s.http
}

// excepted is the system's own bypass list: exact names, *.suffix and
// domain suffixes, and CIDRs.
func (s sysProxy) excepted(host string) bool {
	host = strings.ToLower(host)
	ip := net.ParseIP(host)
	for _, e := range s.except {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case e == "":
		case strings.Contains(e, "/") && ip != nil:
			if _, n, err := net.ParseCIDR(e); err == nil && n.Contains(ip) {
				return true
			}
		case strings.HasPrefix(e, "*."):
			if strings.HasSuffix(host, e[1:]) {
				return true
			}
		case host == e || strings.HasSuffix(host, "."+strings.TrimPrefix(e, ".")):
			return true
		}
	}
	return false
}

func (s sysProxy) String() string {
	var parts []string
	if s.http != nil {
		parts = append(parts, "HTTP "+s.http.Host)
	}
	if s.https != nil {
		parts = append(parts, "HTTPS "+s.https.Host)
	}
	if s.socks != nil {
		parts = append(parts, "SOCKS "+s.socks.Host)
	}
	if len(parts) == 0 {
		if s.pac {
			return "PAC (not supported, direct)"
		}
		return "none"
	}
	return strings.Join(parts, ", ")
}

var sysCache struct {
	mu  sync.Mutex
	at  time.Time
	val sysProxy
}

// systemProxy is the OS's proxy, read at most every few seconds so a switch
// flipped in Clash Verge is followed within them.
func systemProxy() sysProxy {
	sysCache.mu.Lock()
	defer sysCache.mu.Unlock()
	if time.Since(sysCache.at) < 5*time.Second {
		return sysCache.val
	}
	sysCache.val, sysCache.at = readSystemProxy(), time.Now()
	return sysCache.val
}

// readSystemProxy asks the OS. Only macOS has a system proxy SuperLeo reads;
// elsewhere "system" is the environment, which is how a Linux desktop or a
// Windows service usually hands one out.
func readSystemProxy() sysProxy {
	if runtime.GOOS != "darwin" {
		return envSysProxy()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return sysProxy{}
	}
	return parseScutil(string(out))
}

func envSysProxy() sysProxy {
	var s sysProxy
	pick := func(keys ...string) *url.URL {
		for _, k := range keys {
			if v := os.Getenv(k); v != "" {
				if u, err := parseProxyURL(v); err == nil {
					return u
				}
			}
		}
		return nil
	}
	s.http = pick("HTTP_PROXY", "http_proxy")
	s.https = pick("HTTPS_PROXY", "https_proxy")
	s.socks = pick("ALL_PROXY", "all_proxy")
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if v := os.Getenv(k); v != "" {
			s.except = append(s.except, strings.Split(v, ",")...)
		}
	}
	return s
}

// parseScutil reads `scutil --proxy`.
func parseScutil(out string) sysProxy {
	kv := map[string]string{}
	var s sysProxy
	inExceptions := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "ExceptionsList"):
			inExceptions = true
			continue
		case inExceptions && line == "}":
			inExceptions = false
			continue
		}
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if inExceptions {
			s.except = append(s.except, strings.TrimSpace(v))
			continue
		}
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	at := func(prefix, scheme string) *url.URL {
		if kv[prefix+"Enable"] != "1" || kv[prefix+"Proxy"] == "" {
			return nil
		}
		port, err := strconv.Atoi(kv[prefix+"Port"])
		if err != nil || port <= 0 {
			return nil
		}
		return &url.URL{Scheme: scheme, Host: net.JoinHostPort(kv[prefix+"Proxy"], strconv.Itoa(port))}
	}
	s.http, s.https, s.socks = at("HTTP", "http"), at("HTTPS", "http"), at("SOCKS", "socks5")
	s.pac = kv["ProxyAutoConfigEnable"] == "1"
	return s
}

// ProxyState is what the proxy setting comes to now, for Settings to show.
type ProxyState struct {
	Mode string `json:"mode"`
	// Using is the proxy an outside request goes through now, empty for
	// direct.
	Using string `json:"using"`
	// System is what the system proxy is set to, in system mode.
	System string `json:"system,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CurrentProxyState is the proxy an outside request would take now.
func CurrentProxyState() ProxyState { return ProxyStateOf(currentProxy()) }

func systemCacheReset() {
	sysCache.mu.Lock()
	sysCache.at = time.Time{}
	sysCache.mu.Unlock()
}

// ProxyEnv is the proxy variables a program SuperLeo starts (Claude Code,
// Codex, an MCP server) is given, so it takes the same way out. Nil leaves
// the program the environment it would get anyway (mode ""); "off" clears
// them.
func ProxyEnv() map[string]string {
	p := currentProxy()
	if p.Mode == ProxyModeEnv {
		return nil
	}
	env := map[string]string{"HTTPS_PROXY": "", "HTTP_PROXY": "", "ALL_PROXY": "", "https_proxy": "", "http_proxy": "", "all_proxy": ""}
	u, err := proxyForURL(p, &url.URL{Scheme: "https", Host: "api.anthropic.com"})
	if err == nil && u != nil {
		for k := range env {
			env[k] = u.String()
		}
	}
	noProxy := "localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,.local,.ts.net"
	env["NO_PROXY"], env["no_proxy"] = noProxy, noProxy
	return env
}

// TestProxy fetches a small page through p, on a transport of its own so
// the setting in use is not touched, and says how it went.
func TestProxy(ctx context.Context, p ProxySettings, target string) (string, error) {
	if target == "" {
		target = "https://www.gstatic.com/generate_204"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	tr := &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) { return proxyForURL(p, r.URL) },
		TLSHandshakeTimeout: 10 * time.Second}
	defer tr.CloseIdleConnections()
	start := time.Now()
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	return fmt.Sprintf("HTTP %d in %s", resp.StatusCode, time.Since(start).Round(time.Millisecond)), nil
}

// ProxyStateOf is what p would come to now, as CurrentProxyState is for the
// setting in use.
func ProxyStateOf(p ProxySettings) ProxyState {
	st := ProxyState{Mode: p.Mode}
	if p.Mode == ProxyModeSystem {
		systemCacheReset()
		st.System = systemProxy().String()
	}
	u, err := proxyForURL(p, &url.URL{Scheme: "https", Host: "api.openai.com"})
	if err != nil {
		st.Error = err.Error()
	} else if u != nil {
		st.Using = u.Redacted()
	}
	return st
}

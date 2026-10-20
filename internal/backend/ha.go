package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// HAService is something kept highly available: one copy running on one host
// at a time, ready to move to a standby, and reached at a service IP that
// moves with it. openclaw on the SDS nodes is one: it belongs to the service,
// not to whichever node it is on today.
type HAService struct {
	Name string `json:"name"`
	// Provider is what keeps it up: "haify" (drbd-reactor promoters).
	Provider string `json:"provider"`
	// VIP is the service IP, without the prefix length.
	VIP string `json:"vip,omitempty"`
	// Active is the hostname it runs on now; empty when it runs nowhere.
	Active string `json:"active,omitempty"`
	// Standbys are the hosts it can move to, by hostname.
	Standbys []HAStandby `json:"standbys"`
	// Units are the systemd units it starts, which is how an agent run by a
	// unit of that name (openclaw.service) is known to be this service's.
	Units []string `json:"units"`
	// State is active, degraded (running, no standby ready) or down.
	State string `json:"state"`
	// Addresses is each of its hosts' address as haify knows the node, by
	// hostname: what an ssh destination like sds@192.168.123.212 is matched
	// against to find the host it means.
	Addresses map[string]string `json:"addresses,omitempty"`
}

// HAStandby is a host an HA service can move to.
type HAStandby struct {
	Host  string `json:"host"`
	Ready bool   `json:"ready"`
}

// HASettings is where core learns about HA services. Empty is none.
type HASettings struct {
	// Haify is a haify controller's REST API ("http://192.168.123.251:9431")
	// and its bearer token; TokenFile is read instead when set.
	Haify HaifySource `json:"haify,omitempty"`
}

// HaifySource is one haify controller.
type HaifySource struct {
	URL       string `json:"url,omitempty"`
	Token     string `json:"token,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
}

// Enabled says whether a controller is configured.
func (h HaifySource) Enabled() bool { return strings.TrimSpace(h.URL) != "" }

func (h HaifySource) token() string {
	if h.TokenFile != "" {
		if b, err := os.ReadFile(h.TokenFile); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return strings.TrimSpace(h.Token)
}

// HaifyServices asks a haify controller which HA services it keeps and where
// each runs. Node names are turned into hostnames, the names hosts go by.
func HaifyServices(ctx context.Context, src HaifySource, client *http.Client) ([]HAService, error) {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	get := func(path string) (map[string]any, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(src.URL, "/")+path, nil)
		if err != nil {
			return nil, err
		}
		if t := src.token(); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("haify %s: %s", path, resp.Status)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("haify %s: %w", path, err)
		}
		return m, nil
	}

	nodes, err := get("/v1/nodes")
	if err != nil {
		return nil, err
	}
	hostOf := map[string]string{}
	addrOf := map[string]string{}
	for _, n := range objects(nodes["nodes"]) {
		name, host := field(n, "name"), field(n, "hostname")
		if host == "" {
			host = name
		}
		hostOf[name] = host
		hostOf[host] = host
		if a := nodeAddress(field(n, "address", "addr", "ip", "nodeAddress", "node_address")); a != "" {
			addrOf[host] = a
		}
	}
	named := func(n string) string {
		if h := hostOf[n]; h != "" {
			return h
		}
		return n
	}

	list, err := get("/v1/ha")
	if err != nil {
		return nil, err
	}
	var out []HAService
	for _, c := range objects(list["configs"]) {
		res := field(c, "resource")
		if res == "" {
			continue
		}
		s := HAService{Name: res, Provider: "haify", VIP: strings.SplitN(field(c, "vip"), "/", 2)[0], Standbys: []HAStandby{}, Units: stringList(c["services"])}
		if s.Units == nil {
			s.Units = []string{}
		}
		st, err := get("/v1/ha/" + res + "/status")
		if err == nil {
			for _, p := range objects(st["promoters"]) {
				if a := field(p, "activeNode", "active_node"); a != "" {
					s.Active = named(a)
				} else if a := field(p, "primaryOn", "primary_on"); a != "" {
					s.Active = named(a)
				}
				for _, sb := range objects(p["standbys"]) {
					ready, _ := sb["ready"].(bool)
					s.Standbys = append(s.Standbys, HAStandby{Host: named(field(sb, "node")), Ready: ready})
				}
			}
		}
		s.State = haState(s)
		for _, h := range append([]string{s.Active}, standbyHosts(s)...) {
			if a := addrOf[h]; h != "" && a != "" {
				if s.Addresses == nil {
					s.Addresses = map[string]string{}
				}
				s.Addresses[h] = a
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func standbyHosts(s HAService) []string {
	out := make([]string, 0, len(s.Standbys))
	for _, sb := range s.Standbys {
		out = append(out, sb.Host)
	}
	return out
}

// nodeAddress is a node's address without a port or prefix length:
// "192.168.123.212:3366" and "192.168.123.212/24" are 192.168.123.212.
func nodeAddress(a string) string {
	a = strings.TrimSpace(strings.SplitN(a, "/", 2)[0])
	if h, _, err := net.SplitHostPort(a); err == nil {
		return h
	}
	return strings.Trim(a, "[]")
}

func haState(s HAService) string {
	if s.Active == "" {
		return "down"
	}
	for _, sb := range s.Standbys {
		if sb.Ready {
			return "active"
		}
	}
	return "degraded"
}

// Serves says whether this service runs the agent called name: its units
// include name.service.
func (s HAService) Serves(name string) bool {
	for _, u := range s.Units {
		if strings.TrimSuffix(u, ".service") == name {
			return true
		}
	}
	return false
}

// objects reads a JSON array of objects, skipping anything else.
func objects(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// field is the first of keys that m has as a string: the REST gateway writes
// camelCase, other haify front ends snake_case.
func field(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// HaifyEvict moves an HA service off the node it runs on: haify stops it
// there and starts it on a standby, and answers once another node has taken
// over (or that none did). Which standby is haify's choice.
func HaifyEvict(ctx context.Context, src HaifySource, resource string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	body, _ := json.Marshal(map[string]string{"resource": resource})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(src.URL, "/")+"/v1/resources/"+resource+"/ha/evict", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := src.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || !out.Success {
		why := firstNonEmpty(out.Message, out.Error, strings.TrimSpace(string(raw)), resp.Status)
		return "", fmt.Errorf("haify would not move %s: %s", resource, why)
	}
	return out.Message, nil
}

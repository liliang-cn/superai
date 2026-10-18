package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	for _, n := range objects(nodes["nodes"]) {
		name, host := field(n, "name"), field(n, "hostname")
		if host == "" {
			host = name
		}
		hostOf[name] = host
		hostOf[host] = host
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
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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

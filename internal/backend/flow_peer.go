package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// PeerFlowLines asks the worker called name for its lines of traceID — the
// half of a flow it ran — over the same wire an order goes out on.
func (r *RemoteRunner) PeerFlowLines(ctx context.Context, name, traceID string) ([]string, error) {
	if r == nil {
		return nil, fmt.Errorf("no remote runner")
	}
	ag, ok, _ := r.lookup(strings.TrimSpace(name))
	if !ok {
		return nil, fmt.Errorf("no worker called %q", name)
	}
	wire := ag.Wire
	if wire == nil {
		base, err := url.Parse(strings.TrimRight(ag.URL, "/"))
		if err != nil || base.Scheme == "" || base.Host == "" {
			return nil, fmt.Errorf("worker %q is not reachable over HTTP", name)
		}
		wire = httpWire{base: base.String(), token: ag.Token}
	}
	raw, err := wire.Call(ctx, "FlowLines", []any{traceID})
	if err != nil {
		return nil, err
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

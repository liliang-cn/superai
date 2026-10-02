package app

// The webhook a standing agent can be woken by: any service POSTs to
// /api/agents/hook/<id>/<secret> and the body reaches the agent as an event —
// a CI result, an alert, a form submission. The secret in the path is the
// credential, like a phone's pairing code; the route is otherwise ungated.

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

const agentHookPrefix = "/api/agents/hook/"

// agentHookMax bounds one event: an agent reads it whole.
const agentHookMax = 64 << 10

func agentHookRoute(mux *http.ServeMux, app *App) {
	mux.HandleFunc(agentHookPrefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, agentHookPrefix), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.NotFound(w, r)
			return
		}
		id, secret := parts[0], parts[1]
		h := app.standing()
		h.mu.Lock()
		sp, ok := h.specs[id]
		h.mu.Unlock()
		if !ok || sp.HookSecret == "" || subtle.ConstantTimeCompare([]byte(sp.HookSecret), []byte(secret)) != 1 {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentHookMax))
		if err != nil {
			http.Error(w, "the event is larger than 64 KB", http.StatusRequestEntityTooLarge)
			return
		}
		st, err := h.engine()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		source := strings.TrimSpace(r.Header.Get("X-Event-Source"))
		if source == "" {
			source = "webhook"
		}
		steered, err := st.Deliver(context.Background(), id, agent.StandingEvent{
			Source: source, Kind: r.Header.Get("X-Event-Kind"), Payload: string(body),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		app.emit("agent:update", map[string]any{"id": id})
		writeJSON(w, map[string]any{"delivered": true, "steered": steered})
	})
}

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

// Asked to move openclaw, the queen has haify evict it and says where it went;
// a service with no ready standby, or haify's own controller, is refused.
func TestMovingAnHAServiceEvictsItAndSaysWhereItWent(t *testing.T) {
	var on atomic.Value
	on.Store("node-e")
	var evicted atomic.Int32
	haify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/nodes":
			w.Write([]byte(`{"nodes":[{"name":"node-b","hostname":"sds-b"},{"name":"node-e","hostname":"sds-e"}]}`))
		case "/v1/ha":
			w.Write([]byte(`{"configs":[{"resource":"openclaw","services":["openclaw.service"]},{"resource":"lonely","services":[]},{"resource":"haify-meta"}]}`))
		case "/v1/ha/openclaw/status":
			standby := "node-b"
			if on.Load() == "node-b" {
				standby = "node-e"
			}
			w.Write([]byte(`{"promoters":[{"activeNode":"` + on.Load().(string) + `","standbys":[{"node":"` + standby + `","ready":true}]}]}`))
		case "/v1/ha/lonely/status":
			w.Write([]byte(`{"promoters":[{"activeNode":"node-b","standbys":[{"node":"node-e","ready":false}]}]}`))
		case "/v1/ha/haify-meta/status":
			w.Write([]byte(`{"promoters":[{"activeNode":"node-b","standbys":[{"node":"node-e","ready":true}]}]}`))
		case "/v1/resources/openclaw/ha/evict":
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || !strings.Contains(string(body), `"openclaw"`) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			evicted.Add(1)
			on.Store("node-b")
			w.Write([]byte(`{"success":true,"message":"openclaw now on node-b"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer haify.Close()

	core := NewApp()
	core.settings = &backend.Settings{HA: backend.HASettings{Haify: backend.HaifySource{URL: haify.URL, Token: "tok"}}}
	core.haServices()
	waitFor(t, "the HA services", func() bool { return len(core.haServices()) == 3 })

	got, err := core.moveHA(context.Background(), "openclaw")
	if err != nil || got != "openclaw moved from sds-e to sds-b." || evicted.Load() != 1 {
		t.Fatalf("move: %q %v (evicted %d)", got, err, evicted.Load())
	}
	if _, err := core.moveHA(context.Background(), "lonely"); err == nil || !strings.Contains(err.Error(), "no standby ready") {
		t.Fatalf("a service with no ready standby was moved: %v", err)
	}
	if _, err := core.moveHA(context.Background(), "haify-meta"); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("haify's controller was moved: %v", err)
	}
	if _, err := core.moveHA(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "openclaw") {
		t.Fatalf("an unknown name: %v", err)
	}
	if evicted.Load() != 1 {
		t.Fatalf("refused moves reached haify: %d", evicted.Load())
	}
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A fake queen: pairs one code, answers one method for the token it handed
// out, and streams one event.
func fakeQueen(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(pairClaimPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Code string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Code != "123456" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"配对码不对或已过期"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"tok-1","device_id":"d1"}`))
	})
	mux.HandleFunc("/api/rpc/HiveStatus", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		_, _ = fmt.Fprintf(w, `{"role":"queen","args":%d}`, len(args))
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": connected\n\n"))
		_, _ = w.Write([]byte(`data: {"name":"hive:task","payload":{"id":"t1","state":"running"}}` + "\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTheDesktopWindowSeesTheHiveOnceLinked(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	queen := fakeQueen(t)

	events := make(chan string, 16)
	prev := windowEmit
	windowEmit = func(_ context.Context, name string, payload map[string]any) {
		if name == "hive:task" {
			events <- fmt.Sprint(payload["id"])
		}
	}
	t.Cleanup(func() { windowEmit = prev })

	a := &App{ctx: context.Background()}
	t.Cleanup(func() { a.useHiveLink(nil) })

	if _, err := a.LinkHive(queen.URL, "000000"); err == nil {
		t.Fatal("a wrong code linked")
	}
	if a.HiveLinkStatus().Linked {
		t.Fatal("linked after a refused code")
	}
	if _, err := a.LinkHive(queen.URL, "123456"); err != nil {
		t.Fatalf("link: %v", err)
	}

	got, err := a.Remote("HiveStatus", []any{"x", 1})
	if err != nil {
		t.Fatalf("remote call: %v", err)
	}
	if m, _ := got.(map[string]any); m["role"] != "queen" || m["args"] != float64(2) {
		t.Fatalf("remote call answered %v", got)
	}

	select {
	case id := <-events:
		if id != "t1" {
			t.Fatalf("relayed %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queen's event never reached the window")
	}
	if !a.quietWhileLinked("chat:event") || a.quietWhileLinked("open:conversation") {
		t.Fatal("a linked window should hear the hive, not this machine's engine")
	}

	// A fresh start finds the link on disk.
	b := &App{ctx: context.Background()}
	t.Cleanup(func() { b.useHiveLink(nil) })
	b.startHiveLink()
	if !b.HiveLinkStatus().Linked {
		t.Fatal("the link was not kept")
	}

	if err := a.UnlinkHive(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Remote("HiveStatus", nil); err == nil {
		t.Fatal("a call went out after unlinking")
	}
	if loadHiveLink() != nil {
		t.Fatal("the link file survived unlinking")
	}
}

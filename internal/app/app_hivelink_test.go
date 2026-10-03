package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	mux.HandleFunc("/graph/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" || r.Header.Get("Cookie") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("queen graph " + r.URL.Path))
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

	// The window's /graph/ is the queen's, carried with the token.
	local := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("local " + r.URL.Path)) })
	mw := AssetMiddleware(a)(local)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graph/index.js", nil)
	req.Header.Set("Cookie", "superai_session=x")
	mw.ServeHTTP(rec, req)
	if got := rec.Body.String(); got != "queen graph /graph/index.js" {
		t.Fatalf("/graph/ answered %q", got)
	}
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if got := rec.Body.String(); got != "local /index.html" {
		t.Fatalf("the shell answered %q", got)
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

func TestTheDesktopWindowSwitchesBetweenHives(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	prev := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prev })

	// A file from before there could be several: one link, in use.
	q0 := fakeQueen(t)
	old, _ := json.Marshal(map[string]any{"url": q0.URL, "token": "tok-1", "device_id": "d0"})
	if err := os.WriteFile(hiveLinkPath(), old, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: context.Background()}
	t.Cleanup(func() { a.useHiveLink(nil) })
	a.startHiveLink()
	if st := a.HiveLinkStatus(); !st.Linked || st.URL != q0.URL {
		t.Fatalf("the old link was not picked up: %+v", st)
	}

	q1, q2 := fakeQueen(t), fakeQueen(t)
	for _, q := range []string{q1.URL, q2.URL} {
		if _, err := a.LinkHive(q, "123456"); err != nil {
			t.Fatal(err)
		}
	}
	links := a.HiveLinks()
	if len(links) != 3 || !links[2].Active || links[0].Active {
		t.Fatalf("saved hives: %+v", links)
	}
	if st := a.HiveLinkStatus(); st.URL != q2.URL {
		t.Fatalf("pairing did not switch to the new hive: %+v", st)
	}

	if err := a.UseHive(q1.URL); err != nil {
		t.Fatal(err)
	}
	if st := a.HiveLinkStatus(); st.URL != q1.URL {
		t.Fatalf("switched to %+v", st)
	}
	if _, err := a.Remote("HiveStatus", nil); err != nil {
		t.Fatalf("calls after switching: %v", err)
	}

	// This Mac on its own, the hives still saved.
	if err := a.UseHive(""); err != nil {
		t.Fatal(err)
	}
	if a.HiveLinkStatus().Linked || len(a.HiveLinks()) != 3 {
		t.Fatal("this Mac alone should keep the saved hives and use none")
	}
	if err := a.UseHive("http://nowhere"); err == nil {
		t.Fatal("switched to a hive never saved")
	}

	// Forgetting the one in use goes back to this Mac.
	_ = a.UseHive(q2.URL)
	if err := a.ForgetHive(q2.URL); err != nil {
		t.Fatal(err)
	}
	if a.HiveLinkStatus().Linked || len(a.HiveLinks()) != 2 {
		t.Fatalf("after forgetting: %+v", a.HiveLinks())
	}
	// A fresh start keeps what is left, and that none is in use.
	b := &App{ctx: context.Background()}
	t.Cleanup(func() { b.useHiveLink(nil) })
	b.startHiveLink()
	if b.HiveLinkStatus().Linked || len(b.HiveLinks()) != 2 {
		t.Fatal("the book did not survive a restart")
	}
}

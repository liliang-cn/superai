package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

type homeEvents struct {
	mu  sync.Mutex
	got []map[string]any
}

func (e *homeEvents) last(t *testing.T) map[string]any {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.got) == 0 {
		t.Fatal("no home:layout event")
	}
	return e.got[len(e.got)-1]
}

func (e *homeEvents) count() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.got) }

// newHomeApp is an App with its layout in a temp dir and market data from a
// stub that knows VOO and nothing else.
func newHomeApp(t *testing.T) (*App, *homeEvents) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/VOO") {
			fmt.Fprint(w, `{"chart":{"result":[{"meta":{"currency":"USD","symbol":"VOO","regularMarketPrice":707.43,"regularMarketTime":1792008000,"exchangeTimezoneName":"America/New_York"},"timestamp":[1791898200,1791984600],"indicators":{"quote":[{"close":[715.2,707.43]}]}}]}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found"}}}`)
	}))
	t.Cleanup(srv.Close)
	ev := &homeEvents{}
	a := &App{
		homeStore: backend.NewHomeLayoutStore(filepath.Join(t.TempDir(), backend.HomeLayoutFile)),
		quotesV:   backend.NewQuoteService(srv.URL),
	}
	a.emitFn = func(name string, p map[string]any) {
		if name == homeLayoutEvent {
			ev.mu.Lock()
			ev.got = append(ev.got, p)
			ev.mu.Unlock()
		}
	}
	return a, ev
}

func homeRPC(t *testing.T, a *App, method string, args ...string) map[string]any {
	t.Helper()
	raw := make([]json.RawMessage, len(args))
	for i, s := range args {
		raw[i] = json.RawMessage(s)
	}
	out, err := callMethod(a, method, raw)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestHomeLayoutRPCsVersionAndAnnounce(t *testing.T) {
	a, ev := newHomeApp(t)
	got := homeRPC(t, a, "HomeLayout")
	if got["layout"] != nil || got["version"].(float64) != 0 {
		t.Fatalf("empty: %v", got)
	}
	layout := `{"version":1,"widgets":[{"id":"6F9619FF-8B86-D011-B42D-00C04FC964FF","kind":"quote","size":"small","config":{"symbol":"VOO"}},{"id":"7C9E6679-7425-40DE-944B-E07FC1F90AE7","kind":"later-kind","size":"small","config":{"x":1}}]}`
	got = homeRPC(t, a, "SetHomeLayout", layout, "0")
	if got["ok"] != true || got["version"].(float64) != 1 || got["source"] != "phone" {
		t.Fatalf("set: %v", got)
	}
	if e := ev.last(t); e["version"].(float64) != 1 || e["layout"] == nil {
		t.Fatalf("event: %v", e)
	}

	// The queen adds a widget: version 2, announced, the unknown kind kept.
	res := a.runHomeLayoutTool(context.Background(), map[string]any{"action": "add", "kind": "hive", "size": "wide", "index": float64(0)})
	if res["ok"] != true || res["version"].(int64) != 2 {
		t.Fatalf("tool add: %v", res)
	}
	if e := ev.last(t); e["source"] != "queen" || e["version"].(float64) != 2 {
		t.Fatalf("event after tool: %v", e)
	}
	ws := homeRPC(t, a, "HomeLayout")["layout"].(map[string]any)["widgets"].([]any)
	if len(ws) != 3 || ws[0].(map[string]any)["kind"] != "hive" || ws[2].(map[string]any)["kind"] != "later-kind" {
		t.Fatalf("after tool: %v", ws)
	}

	// The phone, still on version 1, gets the conflict and the current copy,
	// and nothing is announced for it.
	n := ev.count()
	got = homeRPC(t, a, "SetHomeLayout", layout, "1")
	if got["ok"] != false || got["conflict"] != true || got["version"].(float64) != 2 {
		t.Fatalf("stale: %v", got)
	}
	if ev.count() != n {
		t.Fatal("a rejected write was announced")
	}
	// Reset from the phone.
	got = homeRPC(t, a, "SetHomeLayout", "null", "2")
	if got["ok"] != true || got["layout"] != nil || got["version"].(float64) != 3 {
		t.Fatalf("reset: %v", got)
	}
	// A broken layout is refused outright.
	if _, err := callMethod(a, "SetHomeLayout", []json.RawMessage{json.RawMessage(`{"widgets":[{"id":"x","kind":"hive"}]}`), json.RawMessage("3")}); err == nil {
		t.Fatal("accepted a widget whose id is not a UUID")
	}
}

func TestHomeLayoutToolValidatesAndChecksSymbols(t *testing.T) {
	a, ev := newHomeApp(t)
	ctx := context.Background()
	res := a.runHomeLayoutTool(ctx, map[string]any{"action": "get"})
	if res["ok"] != true || res["arranged"] != false || !strings.Contains(fmt.Sprint(res["note"]), "suggestion") {
		t.Fatalf("get on nothing: %v", res)
	}
	res = a.runHomeLayoutTool(ctx, map[string]any{"action": "add", "kind": "quote", "symbol": "NOPEX"})
	if res["ok"] != false || !strings.Contains(fmt.Sprint(res["error"]), "no market data") {
		t.Fatalf("unknown symbol: %v", res)
	}
	if ev.count() != 0 {
		t.Fatal("a refused edit was announced")
	}
	res = a.runHomeLayoutTool(ctx, map[string]any{"action": "add", "kind": "quote", "symbol": "voo"})
	ws := res["widgets"].([]map[string]any)
	if res["ok"] != true || len(ws) != 5 || ws[4]["title"] != "quote VOO" {
		t.Fatalf("add VOO: %v", res)
	}
	res = a.runHomeLayoutTool(ctx, map[string]any{"action": "remove", "id": ws[4]["id"]})
	if res["ok"] != true || len(res["widgets"].([]map[string]any)) != 4 {
		t.Fatalf("remove: %v", res)
	}
	res = a.runHomeLayoutTool(ctx, map[string]any{"action": "reset"})
	if res["ok"] != true || res["arranged"] != false {
		t.Fatalf("reset: %v", res)
	}
	if e := ev.last(t); e["layout"] != nil {
		t.Fatalf("reset event: %v", e)
	}
}

func TestQuotesRPC(t *testing.T) {
	a, _ := newHomeApp(t)
	got := homeRPC(t, a, "Quotes", `["VOO","ZZZZ"]`)
	qs := got["quotes"].([]any)
	if len(qs) != 1 || qs[0].(map[string]any)["symbol"] != "VOO" {
		t.Fatalf("quotes: %v", got)
	}
	if got["errors"].(map[string]any)["ZZZZ"] != "unknown symbol" {
		t.Fatalf("errors: %v", got)
	}
	many := make([]string, backend.QuoteMaxSymbols+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprint("S", i))
	}
	if _, err := callMethod(a, "Quotes", []json.RawMessage{json.RawMessage("[" + strings.Join(many, ",") + "]")}); err == nil {
		t.Fatal("too many symbols accepted")
	}
}

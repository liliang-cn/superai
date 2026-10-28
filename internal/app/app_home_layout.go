package app

// The phone's home screen, kept by core so the phone and the queen arrange one
// layout (backend/homelayout.go), and the market data its widgets show
// (backend/quotes.go).
//
//	POST /api/rpc/HomeLayout     []                 → {"layout":{…}|null,"version":7,"updatedAt":"…","source":"queen"}
//	POST /api/rpc/SetHomeLayout  [layout|null, 7]   → {"ok":true,"layout":…,"version":8,…}
//	                                                  {"ok":false,"conflict":true,"layout":<current>,"version":9,…}
//	POST /api/rpc/Quotes         [["VOO","BTC"]]    → {"quotes":[{"symbol":"VOO","price":…,"change":…,"closes":[…]}],"errors":{}}
//
// Every change, whoever made it, goes out as home:layout with the same body as
// HomeLayout, so an open phone moves its widgets while the queen is still
// talking about it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
)

const (
	homeLayoutEvent = "home:layout"
	// homeToolName is the queen's tool; QueenKeepsTool keeps it on her.
	homeToolName = "home_layout"
)

func (a *App) homeLayouts() *backend.HomeLayoutStore {
	a.homeOnce.Do(func() {
		if a.homeStore == nil {
			a.homeStore = backend.DefaultHomeLayoutStore()
		}
	})
	return a.homeStore
}

func (a *App) quotes() *backend.QuoteService {
	a.quotesOnce.Do(func() {
		if a.quotesV == nil {
			a.quotesV = backend.NewQuoteService("")
		}
	})
	return a.quotesV
}

// HomeLayout is the home screen's layout and its revision.
func (a *App) HomeLayout() (map[string]any, error) {
	st, err := a.homeLayouts().Get()
	if err != nil {
		return nil, err
	}
	return st.Map(), nil
}

// SetHomeLayout replaces the layout a client edited, when baseVersion is the
// revision its edit started from. null is back to the phone's suggestion. A
// stale base changes nothing and answers ok false with the current layout, so
// the client puts its edit on top of that and sends again.
func (a *App) SetHomeLayout(layout any, baseVersion int64) (map[string]any, error) {
	var l *backend.HomeLayoutDoc
	if layout != nil {
		b, err := json.Marshal(layout)
		if err != nil {
			return nil, err
		}
		l = &backend.HomeLayoutDoc{}
		if err := json.Unmarshal(b, l); err != nil {
			return nil, &callError{400, "layout: " + err.Error()}
		}
	}
	st, conflict, err := a.homeLayouts().Set(l, baseVersion, "phone")
	if err != nil {
		return nil, &callError{400, err.Error()}
	}
	out := st.Map()
	out["ok"] = !conflict
	if conflict {
		out["conflict"] = true
		return out, nil
	}
	a.emit(homeLayoutEvent, st.Map())
	return out, nil
}

// Quotes is the price, the day's change and a month of daily closes for each
// symbol, in the order asked; the ones with no data are in errors.
func (a *App) Quotes(symbols []string) (map[string]any, error) {
	if len(symbols) == 0 {
		return map[string]any{"quotes": []backend.Quote{}, "errors": map[string]string{}}, nil
	}
	if len(symbols) > backend.QuoteMaxSymbols {
		return nil, &callError{400, fmt.Sprintf("at most %d symbols at once", backend.QuoteMaxSymbols)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	qs, bad := a.quotes().Quotes(ctx, symbols)
	return map[string]any{"quotes": qs, "errors": bad}, nil
}

// registerHomeLayoutTool gives the queen home_layout. Not on a worker: the
// phone pairs with core, and a worker's layout would be nobody's. Runs inside
// the build, under a.mu; the tool itself runs later.
func (a *App) registerHomeLayoutTool(svc *backend.Service, cfg *backend.Settings) {
	if svc == nil || (cfg != nil && cfg.Hive.Role == backend.HiveRoleWorker) {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}
	// It changes what the person sees, so it is not read-only; but it is the
	// person's own screen, every change is visible at once and undone in one
	// more sentence, so it does not stop at the approval gate.
	inner.AddToolWithMetadata(homeToolName, backend.HomeLayoutToolDescription(), backend.HomeLayoutToolSchema(),
		func(ctx context.Context, args map[string]any) (any, error) {
			return a.runHomeLayoutTool(ctx, args), nil
		},
		agent.ToolMetadata{})
}

// runHomeLayoutTool is one home_layout call: the result the model reads, an
// error in it rather than a Go error so the model can correct the call.
func (a *App) runHomeLayoutTool(ctx context.Context, args map[string]any) map[string]any {
	act := backend.HomeActionFromArgs(args)
	store := a.homeLayouts()
	if act.Action == "get" || act.Action == "" {
		if act.Action == "" {
			_, _, err := backend.ApplyHomeAction(nil, act, nil)
			return map[string]any{"ok": false, "error": err.Error()}
		}
		st, err := store.Get()
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		return homeToolResult(st, homeGetNote(st))
	}

	// A symbol is looked up before the store is locked: the lookup is a
	// network call, and the phone's own writes should not wait on it.
	var symbolErr error
	if act.Has["symbol"] && strings.TrimSpace(act.Symbol) != "" {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		symbolErr = a.quotes().Check(cctx, backend.NormalizeQuoteSymbol(act.Symbol))
		cancel()
	}
	check := func(string) error { return symbolErr }

	var note string
	st, err := store.Update("queen", func(cur *backend.HomeLayoutDoc) (*backend.HomeLayoutDoc, error) {
		next, n, err := backend.ApplyHomeAction(cur, act, check)
		note = n
		return next, err
	})
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	a.emit(homeLayoutEvent, st.Map())
	return homeToolResult(st, note)
}

func homeGetNote(st backend.HomeLayoutState) string {
	if st.Layout == nil {
		return "Nothing arranged yet: the phone shows its own suggestion (this week's time, US stocks, the hive, the next alarm, as far as there is data for them). An edit starts from those four."
	}
	return ""
}

func homeToolResult(st backend.HomeLayoutState, note string) map[string]any {
	out := map[string]any{"ok": true, "version": st.Version, "widgets": backend.HomeLayoutSummary(st.Layout)}
	if st.Layout == nil {
		out["arranged"] = false
	}
	if note != "" {
		out["note"] = note
	}
	return out
}

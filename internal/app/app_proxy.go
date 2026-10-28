package app

import (
	"context"

	"github.com/liliang-cn/superleo/internal/backend"
)

// ProxyStatus is what the proxy setting comes to now: the mode, the proxy an
// outside request goes through (empty: direct) and, in system mode, what the
// system proxy is set to.
//
//	POST /api/rpc/ProxyStatus  []
//	→ {"mode":"system","using":"http://127.0.0.1:7897","system":"HTTP 127.0.0.1:7897, HTTPS 127.0.0.1:7897"}
func (a *App) ProxyStatus() backend.ProxyState {
	return backend.CurrentProxyState()
}

// TestProxy tries p, saved or not, without changing the setting in use.
//
//	POST /api/rpc/TestProxy  [{"mode":"manual","url":"http://127.0.0.1:7897"}]
//	→ {"ok":true,"result":"HTTP 204 in 312ms","state":{"mode":"manual","using":"http://127.0.0.1:7897"}}
func (a *App) TestProxy(p backend.ProxySettings) map[string]any {
	if err := p.Validate(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	st := backend.ProxyStateOf(p)
	res, err := backend.TestProxy(context.Background(), p, "")
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "state": st}
	}
	return map[string]any{"ok": true, "result": res, "state": st}
}

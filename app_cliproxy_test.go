package main

import (
	"testing"

	"github.com/liliang-cn/superai-desktop/backend"
)

// TestEffectiveRoutesThroughProxy checks the wiring that actually matters: when
// the embedded proxy is running, the Service is built against it rather than the
// user's cloud endpoint — and the user's own settings are left untouched.
func TestEffectiveRoutesThroughProxy(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	a := NewApp()
	a.settings = &backend.Settings{
		LLMBaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		LLMKey:          "cloud-key",
		LLMModel:        "qwen-plus",
		CLIProxyEnabled: true,
		CLIProxyPort:    43527,
	}

	if got := a.effective(); got.LLMBaseURL != a.settings.LLMBaseURL || got.LLMKey != "cloud-key" {
		t.Fatalf("without a proxy the cloud endpoint must be used, got %q", got.LLMBaseURL)
	}

	a.syncProxy()
	if a.proxy == nil {
		t.Fatalf("syncProxy did not start the proxy: %s", a.proxyErr)
	}
	defer a.proxy.Close()

	eff := a.effective()
	if eff.LLMBaseURL != "http://127.0.0.1:43527/v1" {
		t.Errorf("effective base URL = %q, want the proxy", eff.LLMBaseURL)
	}
	if eff.LLMKey != a.proxy.Key() {
		t.Errorf("effective key = %q, want the proxy's local key", eff.LLMKey)
	}
	if eff.LLMModel != "qwen-plus" {
		t.Errorf("model must stay user-selected, got %q", eff.LLMModel)
	}
	if a.settings.LLMBaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" || a.settings.LLMKey != "cloud-key" {
		t.Error("effective() must not mutate the persisted settings")
	}

	// Turning it off must stop the proxy and restore the cloud endpoint.
	a.settings.CLIProxyEnabled = false
	a.syncProxy()
	if a.proxy != nil {
		t.Fatal("syncProxy left the proxy running after it was disabled")
	}
	if got := a.effective(); got.LLMBaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Errorf("after disabling, base URL = %q, want the cloud endpoint", got.LLMBaseURL)
	}
}

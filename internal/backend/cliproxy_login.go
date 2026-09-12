package backend

import (
	"context"
	"fmt"
	"strings"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	cpaconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

// CLIProxyProvider describes a provider the user can log into from the UI.
type CLIProxyProvider struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// NeedsProject marks providers whose credentials are only usable once a
	// project is attached — the SDK login flow does not discover one on its own.
	NeedsProject bool `json:"needs_project"`
	// Note is shown under the button in the UI.
	Note string `json:"note"`
}

// CLIProxyProviders lists the providers SuperAI can log into. Callback ports are
// fixed by each provider's registered redirect URI, so they are never overridden.
func CLIProxyProviders() []CLIProxyProvider {
	return []CLIProxyProvider{
		{ID: "claude", Label: "Claude", Note: "Your Claude subscription."},
		{ID: "codex", Label: "ChatGPT", Note: "Your ChatGPT subscription."},
		{ID: "gemini", Label: "Gemini", NeedsProject: true, Note: "Needs a Google Cloud project ID."},
		{ID: "antigravity", Label: "Antigravity", Note: "Your Google Antigravity account."},
		{ID: "kimi", Label: "Kimi", Note: "Your Moonshot Kimi account."},
	}
}

// Login runs a provider OAuth flow and writes the resulting credential into the
// proxy's auth dir. The proxy's own file watcher picks the new file up, so the
// models become available without a restart.
//
// The flow opens the system browser and waits for the callback. If the callback
// never arrives, prompt is called (~15s in) so the UI can ask the user to paste
// the callback URL by hand; pass nil to skip that fallback.
func (p *CLIProxy) Login(ctx context.Context, provider, projectID string, prompt func(string) (string, error)) (string, error) {
	if p == nil {
		return "", fmt.Errorf("cliproxy not running")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "", fmt.Errorf("no provider given")
	}
	known := false
	for _, prov := range CLIProxyProviders() {
		if prov.ID == provider {
			known = true
			break
		}
	}
	if !known {
		return "", fmt.Errorf("unknown provider %q", provider)
	}

	// Reload from disk so a hand-edited config is respected, then pin the auth
	// dir the running proxy actually watches.
	cfg, err := cpaconfig.LoadConfig(p.cfgPath)
	if err != nil {
		return "", fmt.Errorf("load cliproxy config: %w", err)
	}
	cfg.AuthDir = p.AuthDir()

	mgr := sdkauth.NewManager(
		sdkauth.NewFileTokenStore(),
		sdkauth.NewGeminiAuthenticator(),
		sdkauth.NewCodexAuthenticator(),
		sdkauth.NewClaudeAuthenticator(),
		sdkauth.NewAntigravityAuthenticator(),
		sdkauth.NewKimiAuthenticator(),
	)

	_, savedPath, err := mgr.Login(ctx, provider, cfg, &sdkauth.LoginOptions{
		ProjectID: strings.TrimSpace(projectID),
		Metadata:  map[string]string{},
		Prompt:    prompt,
	})
	if err != nil {
		return "", err
	}
	return savedPath, nil
}

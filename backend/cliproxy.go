package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy"
	cpaconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"golang.org/x/crypto/bcrypt"

	// Registers every built-in request/response translator. Without this the
	// proxy forwards payloads to the provider untranslated — an OpenAI
	// chat-completions body reaches the Codex backend as-is and comes back
	// "Store must be set to false" / "Unsupported parameter: messages".
	_ "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator/builtin"
	"github.com/sirupsen/logrus"
)

// CLIProxy is an embedded CLIProxyAPI instance. It runs in-process and exposes
// an OpenAI-compatible endpoint on 127.0.0.1, which lets SuperAI drive Claude
// Code / Codex / Gemini CLI subscriptions (whose OAuth credentials live in
// <data>/cliproxy/auths) instead of a paid API key.
//
// The agent side needs no changes: the proxy is just another OpenAI-compatible
// base URL, so LLMBaseURL/LLMKey are pointed at it while it is running.
type CLIProxy struct {
	dir     string
	cfgPath string
	port    int
	key     string
	// mgmtKey is the plaintext management secret. The config holds only its
	// bcrypt hash, so this is the only copy that can be presented as a
	// credential — see mgmtRequest.
	mgmtKey string

	svc    *cliproxy.Service
	cancel context.CancelFunc
	done   chan struct{}
}

// cliProxyConfigTemplate is written on first run. Only local-facing defaults are
// set; everything else is left to CLIProxyAPI's own defaults so the file stays
// editable by hand (and by the proxy's own config watcher).
const cliProxyConfigTemplate = `# SuperAI-managed CLIProxyAPI config.
# Add provider credentials by logging in with the CLIProxyAPI CLI against this
# same auth-dir, or by dropping existing auth JSON files into it.
host: "127.0.0.1"
port: %d
auth-dir: "%s"
api-keys:
  - "%s"
`

// StartCLIProxy boots an embedded CLIProxyAPI on 127.0.0.1:port and waits until
// it answers requests. The config lives at <data>/cliproxy/config.yaml and is
// created (with a generated local API key) when missing.
func StartCLIProxy(port int) (*CLIProxy, error) {
	if port <= 0 {
		port = DefaultCLIProxyPort
	}

	dir := filepath.Join(DataDir(), "cliproxy")
	authDir := filepath.Join(dir, "auths")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir cliproxy dir: %w", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		key, kerr := randomKey()
		if kerr != nil {
			return nil, kerr
		}
		body := fmt.Sprintf(cliProxyConfigTemplate, port, authDir, key)
		if werr := os.WriteFile(cfgPath, []byte(body), 0o600); werr != nil {
			return nil, fmt.Errorf("write cliproxy config: %w", werr)
		}
	}

	cfg, err := cpaconfig.LoadConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load cliproxy config: %w", err)
	}

	// A desktop app must never expose the proxy beyond the machine: CLIProxyAPI
	// binds every interface when host is empty, so pin it regardless of the file.
	cfg.Host = "127.0.0.1"
	cfg.Port = port
	if strings.TrimSpace(cfg.AuthDir) == "" {
		cfg.AuthDir = authDir
	}
	if len(cfg.APIKeys) == 0 || strings.TrimSpace(cfg.APIKeys[0]) == "" {
		key, kerr := randomKey()
		if kerr != nil {
			return nil, kerr
		}
		cfg.APIKeys = []string{key}
		if serr := cpaconfig.SaveConfigPreserveComments(cfgPath, cfg); serr != nil {
			log.Printf("superai: cliproxy: persist generated api key: %v", serr)
		}
	}

	// CLIProxyAPI logs every request through the global logrus logger; keep the
	// desktop log readable unless explicitly debugging.
	if strings.TrimSpace(os.Getenv("SUPERAI_CLIPROXY_DEBUG")) == "" {
		logrus.SetLevel(logrus.WarnLevel)
	}

	// Turn on the proxy's own management API, on this loopback instance only.
	//
	// It is how account changes stop fighting the proxy. Editing the auth JSON
	// on disk looks like it works and is then quietly undone: the proxy holds
	// those credentials in memory, and its own persistence writes the record it
	// already had back over the edit. Disabling an account reverted about one
	// time in five, and the same write is what a reader catches mid-truncate.
	// Routed through the management API instead, the proxy is the single writer
	// and applies the change to the record and the file together.
	//
	// The secret is generated per process and never written to the config: the
	// file gets the bcrypt hash the middleware compares against, and the
	// plaintext stays in memory. Nothing outside this process can present it,
	// and nothing is left behind for the next one to find.
	mgmtKey, kerr := randomKey()
	if kerr != nil {
		return nil, kerr
	}
	hashed, herr := bcrypt.GenerateFromPassword([]byte(mgmtKey), bcrypt.DefaultCost)
	if herr != nil {
		return nil, fmt.Errorf("hash management key: %w", herr)
	}
	cfg.RemoteManagement.SecretKey = string(hashed)
	// Loopback only. AllowRemote stays off, so the middleware refuses any
	// caller that is not 127.0.0.1 whatever it presents.
	cfg.RemoteManagement.AllowRemote = false

	svc, err := cliproxy.NewBuilder().
		WithConfig(cfg).
		WithConfigPath(cfgPath).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build cliproxy: %w", err)
	}

	p := &CLIProxy{
		dir:     dir,
		cfgPath: cfgPath,
		port:    port,
		key:     cfg.APIKeys[0],
		mgmtKey: mgmtKey,
		svc:     svc,
		done:    make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	runErr := make(chan error, 1)
	go func() {
		defer close(p.done)
		err := svc.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr <- err
		}
		close(runErr)
	}()

	if err := p.waitReady(runErr); err != nil {
		cancel()
		<-p.done
		return nil, err
	}
	return p, nil
}

// waitReady blocks until the proxy accepts a request, Run fails, or it times out.
func (p *CLIProxy) waitReady(runErr <-chan error) error {
	deadline := time.Now().Add(15 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", p.port)
	for time.Now().Before(deadline) {
		select {
		case err := <-runErr:
			if err != nil {
				return fmt.Errorf("cliproxy: %w", err)
			}
			return errors.New("cliproxy: server stopped during startup")
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			if _, merr := p.Models(context.Background()); merr == nil {
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("cliproxy: not ready on %s after 15s", addr)
}

// BaseURL is the OpenAI-compatible endpoint to point the agent's brain at.
func (p *CLIProxy) BaseURL() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1", p.port)
}

// Key is the local API key clients must present.
func (p *CLIProxy) Key() string {
	if p == nil {
		return ""
	}
	return p.key
}

// Port is the bound local port.
func (p *CLIProxy) Port() int {
	if p == nil {
		return 0
	}
	return p.port
}

// AuthDir is where provider OAuth credential files are read from.
func (p *CLIProxy) AuthDir() string {
	if p == nil {
		return ""
	}
	return filepath.Join(p.dir, "auths")
}

// ConfigPath is the managed config.yaml.
func (p *CLIProxy) ConfigPath() string {
	if p == nil {
		return ""
	}
	return p.cfgPath
}

// Models lists the model IDs the proxy currently serves. An empty list means no
// provider credentials have been added to the auth dir yet.
func (p *CLIProxy) Models(ctx context.Context) ([]string, error) {
	if p == nil {
		return nil, errors.New("cliproxy not running")
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, p.BaseURL()+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("cliproxy /v1/models: status %d", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if strings.TrimSpace(m.ID) != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// Close stops the embedded proxy and waits for its goroutine to finish.
func (p *CLIProxy) Close() error {
	if p == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.svc.Shutdown(ctx)
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
	return err
}

func randomKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate cliproxy key: %w", err)
	}
	return "superai-" + hex.EncodeToString(buf), nil
}

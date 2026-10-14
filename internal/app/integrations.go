// Integration tokens.
//
// Something outside SuperAI that only has news to deliver — openclaw's cron
// jobs, the morning memo, a price alert — needs a credential to post it with.
// The token in auth.json would do and would be far too much: it is the whole
// API, the agent, the shell tools, MCP. A paired device's token is the same
// key with a name on it. So an integration gets a token of its own kind that
// opens exactly one door, PostNotification (postnotify.go), and is answered
// 403 everywhere else.
//
// Kept like the devices are: only the sha256 is on disk, the secret is shown
// once when minted, and each can be revoked on its own. The file is re-read
// when it changes, because the ops path is `superai integration-token create`
// run beside a server that is already up — a token minted that way must work
// without a restart, and one revoked that way must stop working without one.
package app

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

const (
	integrationsFile = "integrations.json"
	// integrationTokenPrefix tells an integration token apart in a config
	// file or a log at a glance; nothing checks it.
	integrationTokenPrefix = "sai_int_"
	maxIntegrationName     = 60
)

// integrationToken is one integration's credential, as kept on disk.
type integrationToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitzero"`
}

// integrationStore is the integration tokens in one file.
type integrationStore struct {
	path string
	now  func() time.Time

	mu     sync.Mutex
	tokens []integrationToken
	// stamp is the file's size and mtime when last read; a different one
	// means another process wrote it.
	stamp string
}

var (
	integrationStoresMu sync.Mutex
	integrationStores   = map[string]*integrationStore{}
)

// openIntegrations returns the one store for path, as openDevices does.
func openIntegrations(path string) (*integrationStore, error) {
	integrationStoresMu.Lock()
	defer integrationStoresMu.Unlock()
	if s, ok := integrationStores[path]; ok {
		return s, nil
	}
	s := &integrationStore{path: path, now: time.Now}
	s.mu.Lock()
	err := s.reloadLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	integrationStores[path] = s
	return s, nil
}

func integrationsPath() string { return filepath.Join(backend.DataDir(), integrationsFile) }

// reloadLocked reads the file again if it changed since the last read.
func (s *integrationStore) reloadLocked() error {
	st, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.tokens, s.stamp = nil, ""
		return nil
	}
	if err != nil {
		return err
	}
	stamp := fmt.Sprintf("%d|%d", st.Size(), st.ModTime().UnixNano())
	if stamp == s.stamp {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var tokens []integrationToken
	if err := json.Unmarshal(b, &tokens); err != nil {
		return fmt.Errorf("%s is not readable as integration tokens: %w", s.path, err)
	}
	s.tokens, s.stamp = tokens, stamp
	return nil
}

func (s *integrationStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tokens := s.tokens
	if tokens == nil {
		tokens = []integrationToken{}
	}
	b, err := json.MarshalIndent(tokens, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if st, err := os.Stat(s.path); err == nil {
		s.stamp = fmt.Sprintf("%d|%d", st.Size(), st.ModTime().UnixNano())
	}
	return nil
}

// create mints a token for name and returns it with its secret, which exists
// nowhere else afterwards.
func (s *integrationStore) create(name string) (integrationToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return integrationToken{}, "", errors.New("an integration token needs a name, e.g. openclaw")
	}
	if len([]rune(name)) > maxIntegrationName {
		return integrationToken{}, "", fmt.Errorf("name is longer than %d characters", maxIntegrationName)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return integrationToken{}, "", err
	}
	secret := integrationTokenPrefix + randomSecret(32)
	t := integrationToken{ID: randomSecret(6), Name: name, TokenHash: hashToken(secret), CreatedAt: s.now().UTC()}
	s.tokens = append(s.tokens, t)
	if err := s.saveLocked(); err != nil {
		s.tokens = s.tokens[:len(s.tokens)-1]
		return integrationToken{}, "", err
	}
	return t, secret, nil
}

// authenticate says whether tok is a live integration token, noting when it
// was last used.
func (s *integrationStore) authenticate(tok string) bool {
	if tok == "" {
		return false
	}
	h := hashToken(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		// A file that cannot be read authenticates nobody, and keeps nobody
		// it already had: a revocation must not be undone by a bad write.
		s.tokens = nil
		return false
	}
	for i := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(s.tokens[i].TokenHash), []byte(h)) == 1 {
			now := s.now().UTC()
			if now.Sub(s.tokens[i].LastUsed) > lastSeenEvery {
				s.tokens[i].LastUsed = now
				_ = s.saveLocked() // a missed last-used is not worth refusing a post over
			}
			return true
		}
	}
	return false
}

func (s *integrationStore) list() ([]integrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	out := append([]integrationToken(nil), s.tokens...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *integrationStore) revoke(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return false, err
	}
	for i := range s.tokens {
		if s.tokens[i].ID == id {
			s.tokens = append(s.tokens[:i], s.tokens[i+1:]...)
			return true, s.saveLocked()
		}
	}
	return false, nil
}

// IntegrationToken is an integration token as listed: never the secret.
type IntegrationToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitzero"`
}

// NewIntegrationToken is a token just minted, with the secret shown this once.
type NewIntegrationToken struct {
	IntegrationToken
	Token string `json:"token"`
}

func integrationTokenView(t integrationToken) IntegrationToken {
	return IntegrationToken{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, LastUsed: t.LastUsed}
}

// CreateIntegrationToken mints a token that may only call PostNotification:
//
//	POST /api/rpc/CreateIntegrationToken  ["openclaw"]
//	→ {"id":"…","name":"openclaw","created_at":"…","token":"sai_int_…"}
//
// The token is in this answer and nowhere else.
func (a *App) CreateIntegrationToken(name string) (NewIntegrationToken, error) {
	s, err := openIntegrations(integrationsPath())
	if err != nil {
		return NewIntegrationToken{}, err
	}
	t, secret, err := s.create(name)
	if err != nil {
		return NewIntegrationToken{}, &callError{http.StatusBadRequest, err.Error()}
	}
	return NewIntegrationToken{IntegrationToken: integrationTokenView(t), Token: secret}, nil
}

// ListIntegrationTokens lists the integration tokens, oldest first:
//
//	POST /api/rpc/ListIntegrationTokens  []
//	→ [{"id":"…","name":"openclaw","created_at":"…","last_used":"…"}]
func (a *App) ListIntegrationTokens() ([]IntegrationToken, error) {
	s, err := openIntegrations(integrationsPath())
	if err != nil {
		return nil, err
	}
	tokens, err := s.list()
	if err != nil {
		return nil, err
	}
	out := []IntegrationToken{}
	for _, t := range tokens {
		out = append(out, integrationTokenView(t))
	}
	return out, nil
}

// RevokeIntegrationToken shuts one integration out; its next call is a 401.
//
//	POST /api/rpc/RevokeIntegrationToken  ["<id>"]  → null (404 for an unknown id)
func (a *App) RevokeIntegrationToken(id string) error {
	s, err := openIntegrations(integrationsPath())
	if err != nil {
		return err
	}
	ok, err := s.revoke(strings.TrimSpace(id))
	if err == nil && !ok {
		err = &callError{http.StatusNotFound, "no integration token with that id"}
	}
	return err
}

// IntegrationTokenMain is `superai integration-token`, for whoever operates
// the server and has its data directory (SUPERAI_HOME) at hand:
//
//	superai integration-token create <name>   prints the token, once
//	superai integration-token list
//	superai integration-token revoke <id>
//
// It writes the same file the server reads, and the server picks the change
// up on the next request.
func IntegrationTokenMain(argv []string) {
	usage := func() {
		fmt.Fprintln(os.Stderr, "usage: superai integration-token create <name> | list | revoke <id>")
		fmt.Fprintln(os.Stderr, "  tokens are kept in", integrationsPath(), "(set SUPERAI_HOME to change)")
		os.Exit(2)
	}
	fl := flag.NewFlagSet("integration-token", flag.ExitOnError)
	fl.Usage = usage
	_ = fl.Parse(argv)
	args := fl.Args()
	if len(args) == 0 {
		usage()
	}
	a := &App{}
	switch args[0] {
	case "create":
		if len(args) != 2 {
			usage()
		}
		t, err := a.CreateIntegrationToken(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "superai:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "created integration token %s (%s); it can only call PostNotification.\n", t.ID, t.Name)
		fmt.Fprintln(os.Stderr, "This is the only time it is shown:")
		// The secret alone on stdout, so it can be piped straight into a
		// secret store without anything else around it.
		fmt.Println(t.Token)
	case "list":
		tokens, err := a.ListIntegrationTokens()
		if err != nil {
			fmt.Fprintln(os.Stderr, "superai:", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tCREATED\tLAST USED")
		for _, t := range tokens {
			last := "never"
			if !t.LastUsed.IsZero() {
				last = t.LastUsed.Local().Format(time.DateTime)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Local().Format(time.DateTime), last)
		}
		_ = w.Flush()
	case "revoke":
		if len(args) != 2 {
			usage()
		}
		if err := a.RevokeIntegrationToken(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "superai:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "revoked", args[1])
	default:
		usage()
	}
}

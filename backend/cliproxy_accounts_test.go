package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIProxyAccounts(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	p, err := StartCLIProxy(43537)
	if err != nil {
		t.Fatalf("StartCLIProxy: %v", err)
	}
	defer p.Close()

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(p.AuthDir(), name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("codex-me@example.com.json",
		`{"type":"codex","email":"me@example.com","disabled":false,"access_token":"secret-token","expired":"2026-08-08T21:01:48+08:00"}`)
	write("gemini-me@example.com-proj.json",
		`{"type":"gemini","email":"me@example.com","project_id":"proj-1","disabled":true}`)
	write("notes.txt", "ignored")

	accounts, err := p.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2 (non-JSON must be skipped): %+v", len(accounts), accounts)
	}
	// Sorted by provider: codex before gemini.
	if accounts[0].Provider != "codex" || accounts[0].Account != "me@example.com" || accounts[0].Disabled {
		t.Errorf("codex account parsed wrong: %+v", accounts[0])
	}
	if accounts[1].Project != "proj-1" || !accounts[1].Disabled {
		t.Errorf("gemini account parsed wrong: %+v", accounts[1])
	}
	// Tokens must never reach the UI layer.
	blob, _ := json.Marshal(accounts)
	if string(blob) == "" || containsToken(string(blob)) {
		t.Errorf("account list leaked a token: %s", blob)
	}

	// Disabling must flip the flag and preserve every other field.
	if err := p.SetAccountDisabled("codex-me@example.com.json", true); err != nil {
		t.Fatalf("SetAccountDisabled: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(p.AuthDir(), "codex-me@example.com.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reread: %v", err)
	}
	if doc["disabled"] != true {
		t.Errorf("disabled not set: %v", doc["disabled"])
	}
	if doc["access_token"] != "secret-token" || doc["expired"] == nil {
		t.Errorf("other fields were dropped: %v", doc)
	}

	// Path traversal must be refused.
	for _, bad := range []string{"../settings.json", "sub/x.json", "", "x.txt"} {
		if err := p.RemoveAccount(bad); err == nil {
			t.Errorf("RemoveAccount(%q) should have been refused", bad)
		}
	}

	if err := p.RemoveAccount("codex-me@example.com.json"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	after, _ := p.Accounts()
	if len(after) != 1 {
		t.Errorf("after sign-out got %d accounts, want 1", len(after))
	}
}

func containsToken(s string) bool {
	for _, needle := range []string{"secret-token", "access_token", "refresh_token"} {
		if len(s) >= len(needle) && (func() bool {
			for i := 0; i+len(needle) <= len(s); i++ {
				if s[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})() {
			return true
		}
	}
	return false
}

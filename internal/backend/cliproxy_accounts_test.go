package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		t.Fatalf("got %d accounts, want 2 (non-JSON must be skipped): %+v (dir=%v)", len(accounts), accounts, dirList(p.AuthDir()))
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
		t.Fatalf("reread: %v (authDir=%s len=%d content=%q dir=%v)",
			err, p.AuthDir(), len(raw), string(raw), dirList(p.AuthDir()))
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
		t.Errorf("after sign-out got %d accounts, want 1 (dir=%v accounts=%+v)", len(after), dirList(p.AuthDir()), after)
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

func dirList(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return []string{"readdir: " + err.Error()}
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		info, _ := e.Info()
		size := int64(-1)
		if info != nil {
			size = info.Size()
		}
		out = append(out, fmt.Sprintf("%s(%d)", e.Name(), size))
	}
	return out
}

// A credential change has to still be there a moment later.
//
// It did not used to be. These files are edited while the proxy that owns them
// is running: it holds the same credentials in memory, and its own persistence
// writes the record it already had back over the edit. Disabling an account
// looked like it worked and was reverted about one time in five — and the
// window where the file is being rewritten is what a reader catches empty.
//
// Asserting immediately after the call would not have caught it, because the
// revert arrives afterwards. This waits.
func TestAccountChangesAreNotRevertedByTheProxy(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	p, err := StartCLIProxy(43539)
	if err != nil {
		t.Fatalf("StartCLIProxy: %v", err)
	}
	defer p.Close()

	const name = "codex-revert@example.com.json"
	if err := os.WriteFile(filepath.Join(p.AuthDir(), name), []byte(
		`{"type":"codex","email":"revert@example.com","disabled":false,`+
			`"access_token":"secret-token","expired":"2026-08-08T21:01:48+08:00"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Deliberately NOT waiting for the proxy to settle. The revert this guards
	// against happens when the change lands while the proxy is still taking
	// the file in — waiting for it first is exactly what makes the race
	// disappear, and a test that waits would pass against the broken code.
	//
	// But the proxy has to have noticed the file at all before it can be
	// asked to change it, and until it has, the management API answers "auth
	// file not found". Retrying until the call is accepted lands the change
	// at the earliest moment it can land, which is the window the revert
	// happens in — so this removes a setup flake (about two runs in five,
	// both before and after this was noticed) without weakening what the
	// test asserts.
	var disableErr error
	for start := time.Now(); time.Since(start) < 5*time.Second; {
		if disableErr = p.SetAccountDisabled(name, true); disableErr == nil {
			break
		}
		if !strings.Contains(disableErr.Error(), "auth file not found") {
			break // a real failure, not the proxy still catching up
		}
		time.Sleep(20 * time.Millisecond)
	}
	if disableErr != nil {
		t.Fatalf("SetAccountDisabled: %v", disableErr)
	}

	// Long enough for a write-back to land, checked throughout rather than
	// only at the end: a value that flips and flips back would otherwise pass.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(p.AuthDir(), name))
		if err != nil {
			t.Fatalf("read back: %v (dir=%v)", err, dirList(p.AuthDir()))
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("the file was not valid JSON %v after the change: %v (len=%d)",
				time.Until(deadline), err, len(raw))
		}
		if doc["disabled"] != true {
			t.Fatalf("the disable was reverted: disabled=%v", doc["disabled"])
		}
		if doc["access_token"] != "secret-token" {
			t.Fatalf("the token was dropped: %v", doc["access_token"])
		}
		time.Sleep(20 * time.Millisecond)
	}

	// And the same through the reader the UI uses.
	accounts, err := p.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(accounts) != 1 || !accounts[0].Disabled {
		t.Fatalf("Accounts disagrees with the file: %+v", accounts)
	}
}

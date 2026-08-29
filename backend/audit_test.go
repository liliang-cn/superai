package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The audit log is read after something went wrong, usually by someone who was
// not at the keyboard. These tests pin the two ways it stops being useful: it
// leaks the secret it was recording around, or it silently stops recording.

func TestSummaryRedactsCredentialArgumentsByName(t *testing.T) {
	got := summarizeArgs("add_mcp_server", map[string]any{
		"name":     "notion",
		"api_key":  "sk-live-0123456789",
		"token":    "ghp_abcdef",
		"password": "hunter2",
		"url":      "https://example.com",
	})
	for _, leaked := range []string{"sk-live-0123456789", "ghp_abcdef", "hunter2"} {
		if strings.Contains(got, leaked) {
			t.Errorf("summary leaks a credential (%q): %s", leaked, got)
		}
	}
	// Redacting everything would make the log useless, which is its own way of
	// failing: the point is to be able to see what was asked for.
	if !strings.Contains(got, "notion") || !strings.Contains(got, "https://example.com") {
		t.Errorf("summary dropped the non-secret arguments too: %s", got)
	}
}

// A shell command is opaque, so redaction there is best-effort by design. The
// common shape — an inline environment assignment — is the one worth catching,
// because it is how a model passes a key it was told about.
func TestSummaryRedactsInlineSecretAssignmentsInCommands(t *testing.T) {
	got := summarizeArgs("bash", map[string]any{
		"command": `AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI ./deploy.sh --region cn-north-1`,
	})
	if strings.Contains(got, "wJalrXUtnFEMI") {
		t.Errorf("summary leaks an assigned secret: %s", got)
	}
	if !strings.Contains(got, "./deploy.sh --region cn-north-1") {
		t.Errorf("summary lost the command it was supposed to describe: %s", got)
	}
}

// A file that a model can make grow without limit is a disk-full bug on a
// timer. One rollover is enough history and bounds the damage.
func TestAuditRotatesInsteadOfGrowingForever(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-approvals.jsonl")
	a := &approvalAudit{path: path}

	if err := os.WriteFile(path, make([]byte, maxAuditBytes+1), 0o600); err != nil {
		t.Fatalf("seed oversized log: %v", err)
	}
	a.append(AuditEntry{At: time.Now(), Tool: "bash", DecidedBy: DecidedByUser})

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if st.Size() >= maxAuditBytes {
		t.Errorf("log is %d bytes after rotation; it was never rolled", st.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("the rolled-away history is gone: %v", err)
	}
}

// tail is what the Settings page shows. It has to survive a log whose last
// line was cut off by a crash — otherwise one bad byte hides the whole record.
func TestTailSkipsATruncatedLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-approvals.jsonl")
	a := &approvalAudit{path: path}
	a.append(AuditEntry{At: time.Now(), Tool: "bash", Allowed: true, DecidedBy: DecidedByUser})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	_, _ = f.WriteString(`{"tool":"bash","all`)
	f.Close()

	got := a.tail(10)
	if len(got) != 1 || got[0].Tool != "bash" {
		t.Fatalf("tail = %+v, want the one complete entry", got)
	}
}

func TestTailReturnsTheMostRecentEntriesInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-approvals.jsonl")
	a := &approvalAudit{path: path}
	for _, tool := range []string{"one", "two", "three", "four"} {
		a.append(AuditEntry{At: time.Now(), Tool: tool, DecidedBy: DecidedByUser})
	}
	got := a.tail(2)
	if len(got) != 2 || got[0].Tool != "three" || got[1].Tool != "four" {
		t.Fatalf("tail(2) = %+v, want the last two in order", got)
	}
}

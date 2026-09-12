package backend

// The record of what the agent was allowed to do.
//
// A gate with no log answers "may it?" but never "did it, and who said yes?".
// The second question is the one asked after something went wrong, usually by
// someone who was not at the keyboard at the time, so the log is a plain file
// they can open — one JSON object per line under the app's own data directory,
// not a table inside a database that needs this app running to read.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// AuditEntry is one decision, as written to the log and shown in Settings.
type AuditEntry struct {
	At        time.Time `json:"at"`
	Tool      string    `json:"tool"`
	Allowed   bool      `json:"allowed"`
	DecidedBy string    `json:"decided_by"`
	Reason    string    `json:"reason,omitempty"`
	// Summary is the redacted one-liner describing what was asked for. The
	// full arguments are not stored: the log outlives the conversation and
	// gets copied into bug reports, and an agent's arguments routinely carry
	// file contents and, occasionally, credentials.
	Summary   string `json:"summary,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// AuditLogPath is where decisions are appended.
func AuditLogPath() string {
	return filepath.Join(DataDir(), "audit", "tool-approvals.jsonl")
}

// maxAuditBytes caps one file before it rolls to .1. A denied-then-retried
// loop can write a line per round, and an audit log that quietly grows without
// bound is a disk-full bug waiting for the least convenient moment. One
// rollover keeps roughly the last two files' worth, which is far more history
// than anyone reads and small enough to grep.
const maxAuditBytes = 4 << 20

// summaryLimit truncates a summary line. Long enough for a real shell command,
// short enough that one entry stays one line in a terminal.
const summaryLimit = 400

type approvalAudit struct {
	mu   sync.Mutex
	path string
}

// append writes one entry. Errors are swallowed on purpose: the audit log
// failing must not stop the decision it is describing from being carried out —
// a full disk should not turn into "every tool call now errors". The gate has
// already made its call by the time this runs.
func (a *approvalAudit) append(e AuditEntry) {
	if a == nil || strings.TrimSpace(a.path) == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// 0o700 / 0o600: the log names the commands that ran on this machine and
	// the sessions they belonged to. That is the owner's business.
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return
	}
	a.rotateLocked()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (a *approvalAudit) rotateLocked() {
	st, err := os.Stat(a.path)
	if err != nil || st.Size() < maxAuditBytes {
		return
	}
	_ = os.Rename(a.path, a.path+".1")
}

// tail returns the last `limit` entries, oldest first. It reads the whole file
// rather than seeking from the end: the file is capped at a few megabytes, and
// a backwards line scanner is a lot of fiddly code to save a few milliseconds
// on a screen the user opens by hand.
func (a *approvalAudit) tail(limit int) []AuditEntry {
	if a == nil || strings.TrimSpace(a.path) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	f, err := os.Open(a.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := make([]AuditEntry, 0, limit)
	sc := bufio.NewScanner(f)
	// A summary is capped at summaryLimit, so a line cannot legitimately be
	// long — but a truncated write from a crash could be, and the default 64K
	// scanner buffer would turn that into "the log is unreadable".
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // a half-written last line, most likely; skip it
		}
		out = append(out, e)
		if len(out) > limit {
			out = out[1:]
		}
	}
	return out
}

// secretKeyRe matches argument names that carry credentials. Names, not
// values: there is no reliable way to look at a string and tell a token from
// an id, but "api_key" says what it holds.
var secretKeyRe = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|credential|authorization|api[-_ ]?key|^key$|_key$|^auth$)`)

// assignedSecretRe catches FOO_TOKEN=... inside a shell command. A command
// line is opaque — `curl -H "Authorization: Bearer …"` and `mysql -pHunter2`
// are not going to be parsed here — so this covers the common shape and no
// more. The honest summary of this function is that it lowers the odds of a
// credential in the log, and is not a reason to put one in a command.
var assignedSecretRe = regexp.MustCompile(`(?i)\b([A-Za-z_][A-Za-z0-9_]*(?:password|passwd|secret|token|credential|apikey|api_key|_key)[A-Za-z0-9_]*)=("[^"]*"|'[^']*'|\S+)`)

const redacted = "[redacted]"

// summarizeArgs renders a tool call as one redacted, truncated line.
func summarizeArgs(tool string, args map[string]any) string {
	if cmd := commandOf(tool, args); cmd != "" {
		return clipRunes(assignedSecretRe.ReplaceAllString(cmd, "$1="+redacted), summaryLimit)
	}
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	// Sorted so the same call summarises identically every time, which is what
	// makes the log greppable.
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if secretKeyRe.MatchString(k) {
			parts = append(parts, k+"="+redacted)
			continue
		}
		parts = append(parts, k+"="+clipRunes(valueString(args[k]), 120))
	}
	return clipRunes(strings.Join(parts, " "), summaryLimit)
}

func valueString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.Join(strings.Fields(t), " ")
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "?"
		}
		return string(b)
	}
}

func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

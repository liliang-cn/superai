package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What a coding agent running in a terminal said and did, read from its own
// session log on the machine it runs on: Claude Code's project transcript,
// codex's rollout, pi's session file. Read when somebody opens the session,
// never sent anywhere on its own; the activity snapshots stay what they were
// (ids, states, tool names).

// sessionLogEvents is how many events one read returns at most, newest kept.
const sessionLogEvents = 300

// sessionLogTail is how much of the end of a log is read: these files grow to
// tens of megabytes, and the screen shows the recent part.
const sessionLogTail = 4 << 20

// SessionLog is a session's recent events.
type SessionLog struct {
	CLI     string        `json:"cli"`
	Session string        `json:"session"`
	Cwd     string        `json:"cwd,omitempty"`
	Events  []CLIRunEvent `json:"events"`
	// Truncated says older events were left out.
	Truncated bool `json:"truncated,omitempty"`
}

var errNoSessionLog = errors.New("no log for that session on this machine")

// sessionLogFile finds a session's log by the id its hooks reported.
func sessionLogFile(cli, session string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if session == "" || strings.ContainsAny(session, `/\*?[`) {
		return "", errors.New("not a session id")
	}
	var pats []string
	switch cli {
	case "claude":
		pats = []string{filepath.Join(home, ".claude", "projects", "*", session+".jsonl")}
	case "codex":
		pats = []string{filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*"+session+".jsonl")}
	case "pi":
		pats = []string{filepath.Join(home, ".pi", "agent", "sessions", "*", "*_"+session+".jsonl")}
	default:
		return "", fmt.Errorf("%s keeps no log SuperLeo can read", cli)
	}
	var found []string
	for _, p := range pats {
		m, _ := filepath.Glob(p)
		found = append(found, m...)
	}
	if len(found) == 0 {
		return "", errNoSessionLog
	}
	sort.Slice(found, func(i, j int) bool { return modTime(found[i]).After(modTime(found[j])) })
	return found[0], nil
}

func modTime(p string) time.Time {
	if st, err := os.Stat(p); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// readSessionLog reads a session's recent events on this machine.
func readSessionLog(cli, session string) (SessionLog, error) {
	out := SessionLog{CLI: cli, Session: session, Events: []CLIRunEvent{}}
	path, err := sessionLogFile(cli, session)
	if err != nil {
		return out, err
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > sessionLogTail {
		if _, err := f.Seek(st.Size()-sessionLogTail, io.SeekStart); err == nil {
			out.Truncated = true
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	first := out.Truncated // the first line after a seek is a partial one
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		evs, cwd := sessionLine(cli, m)
		if cwd != "" {
			out.Cwd = cwd
		}
		out.Events = append(out.Events, evs...)
	}
	if len(out.Events) > sessionLogEvents {
		out.Events = out.Events[len(out.Events)-sessionLogEvents:]
		out.Truncated = true
	}
	for i := range out.Events {
		out.Events[i].Seq = i + 1
	}
	return out, nil
}

// sessionLine turns one log line into events, and the working directory
// when the line says it.
func sessionLine(cli string, m map[string]any) ([]CLIRunEvent, string) {
	at := lineTime(m["timestamp"])
	switch cli {
	case "claude":
		return claudeLine(m, at), str(m["cwd"])
	case "codex":
		return codexLine(m, at)
	case "pi":
		return piLine(m, at)
	}
	return nil, ""
}

func claudeLine(m map[string]any, at time.Time) []CLIRunEvent {
	t := str(m["type"])
	if (t != "user" && t != "assistant") || m["isMeta"] == true || m["isSidechain"] == true {
		return nil
	}
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	if s, ok := msg["content"].(string); ok {
		if t == "user" {
			return youSaid(s, at)
		}
		return said(s, at)
	}
	var out []CLIRunEvent
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		switch str(bm["type"]) {
		case "text":
			if t == "user" {
				out = append(out, youSaid(str(bm["text"]), at)...)
			} else {
				out = append(out, said(str(bm["text"]), at)...)
			}
		case "tool_use":
			out = append(out, CLIRunEvent{At: at, Kind: "tool", Tool: str(bm["name"]), Detail: briefInput(bm["input"]), CallID: str(bm["id"])})
		case "tool_result":
			out = append(out, CLIRunEvent{At: at, Kind: "result", Text: cliClip(resultText(bm["content"]), 600),
				CallID: str(bm["tool_use_id"]), Failed: bm["is_error"] == true})
		}
	}
	return out
}

func codexLine(m map[string]any, at time.Time) ([]CLIRunEvent, string) {
	p, _ := m["payload"].(map[string]any)
	if p == nil {
		return nil, ""
	}
	switch str(m["type"]) {
	case "session_meta", "turn_context":
		return nil, str(p["cwd"])
	case "response_item":
	default:
		return nil, ""
	}
	switch str(p["type"]) {
	case "message":
		var b strings.Builder
		blocks, _ := p["content"].([]any)
		for _, x := range blocks {
			xm, _ := x.(map[string]any)
			b.WriteString(str(xm["text"]))
		}
		if str(p["role"]) == "user" {
			return youSaid(b.String(), at), ""
		}
		if str(p["role"]) == "assistant" {
			return said(b.String(), at), ""
		}
	case "function_call", "custom_tool_call":
		in := p["arguments"]
		if in == nil {
			in = p["input"]
		}
		if s, ok := in.(string); ok {
			var v any
			if json.Unmarshal([]byte(s), &v) == nil {
				in = v
			}
		}
		return []CLIRunEvent{{At: at, Kind: "tool", Tool: str(p["name"]), Detail: briefInput(in), CallID: str(p["call_id"])}}, ""
	case "function_call_output", "custom_tool_call_output":
		return []CLIRunEvent{{At: at, Kind: "result", Text: cliClip(resultText(p["output"]), 600), CallID: str(p["call_id"])}}, ""
	}
	return nil, ""
}

func piLine(m map[string]any, at time.Time) ([]CLIRunEvent, string) {
	switch str(m["type"]) {
	case "session":
		return nil, str(m["cwd"])
	case "message":
	default:
		return nil, ""
	}
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return nil, ""
	}
	role := str(msg["role"])
	blocks, _ := msg["content"].([]any)
	if role == "toolResult" {
		var b strings.Builder
		for _, x := range blocks {
			xm, _ := x.(map[string]any)
			b.WriteString(str(xm["text"]))
		}
		return []CLIRunEvent{{At: at, Kind: "result", Text: cliClip(b.String(), 600), CallID: str(msg["toolCallId"]), Failed: msg["isError"] == true}}, ""
	}
	var out []CLIRunEvent
	for _, x := range blocks {
		xm, _ := x.(map[string]any)
		switch str(xm["type"]) {
		case "text":
			if role == "user" {
				out = append(out, youSaid(str(xm["text"]), at)...)
			} else if role == "assistant" {
				out = append(out, said(str(xm["text"]), at)...)
			}
		case "toolCall":
			out = append(out, CLIRunEvent{At: at, Kind: "tool", Tool: str(xm["name"]), Detail: briefInput(xm["arguments"]), CallID: str(xm["id"])})
		}
	}
	return out, ""
}

// youSaid is what the person typed; the wrappers the CLIs put around
// injected context (<environment_context>, <command-name>, …) are not.
func youSaid(s string, at time.Time) []CLIRunEvent {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "<") {
		return nil
	}
	return []CLIRunEvent{{At: at, Kind: "you", Text: cliClip(s, 4000)}}
}

func said(s string, at time.Time) []CLIRunEvent {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return []CLIRunEvent{{At: at, Kind: "text", Text: cliClip(s, 8000)}}
}

// briefInput is a tool call's arguments in one line: the part that says what
// it does (a command, a path, a pattern), else the arguments clipped.
func briefInput(v any) string {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"command", "cmd", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
			switch x := m[k].(type) {
			case string:
				if x != "" {
					return cliClip(x, 200)
				}
			case []any:
				parts := make([]string, 0, len(x))
				for _, p := range x {
					parts = append(parts, fmt.Sprint(p))
				}
				return cliClip(strings.Join(parts, " "), 200)
			}
		}
	}
	if s, ok := v.(string); ok {
		return cliClip(s, 200)
	}
	raw, _ := json.Marshal(v)
	return cliClip(string(raw), 200)
}

func lineTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t
		}
	case float64:
		return time.UnixMilli(int64(x))
	}
	return time.Time{}
}

package backend

// Calling an MCP tool straight from a button.
//
// A card in the transcript can carry "完成" for a to-do. Sending that back
// through the model would cost a whole turn — seconds and tokens — to do one
// thing whose name and arguments are already on the button. So the click goes
// here instead: one tool on one server, no model in between.
//
// That makes this a way to run tools that skips the model's judgement, so it
// is fenced: only servers the settings name (just noted unless changed), only
// tools the server lists, destructive ones only with a confirmation the client
// asked the person for, a JSON object for arguments, a deadline on every call,
// and a line in the approvals log for each — tool and outcome, never the
// arguments.
//
// It holds its own MCP session per server rather than borrowing agent-go's:
// agent-go's client hands back only the first text content of a result and
// drops structuredContent, which is the part a client draws from. The session
// is built from the same mcpServers.json entry the agent uses, ${NAME}
// references expanded the same way.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultActionTimeout bounds one direct call, connecting included.
const DefaultActionTimeout = 20 * time.Second

// Codes a refused or failed call answers with, so a client can tell "set
// noted up" from "noted said no".
const (
	ActionNotConfigured = "not_configured"
	ActionNotAllowed    = "not_allowed"
	ActionNeedsConfirm  = "needs_confirm"
	ActionBadArgs       = "bad_args"
	ActionUnknownTool   = "unknown_tool"
	ActionUnavailable   = "unavailable"
	ActionToolError     = "tool_error"
)

// MCPCallResult is the answer to one direct call.
type MCPCallResult struct {
	OK bool `json:"ok"`
	// Text is the tool's text content, joined. On a tool error it is the
	// tool's own message.
	Text string `json:"text"`
	// Structured is the tool's structuredContent; when the tool sent none and
	// its text is JSON, that JSON. Otherwise null.
	Structured any    `json:"structured"`
	Error      string `json:"error,omitempty"`
	Code       string `json:"code,omitempty"`
}

// MCPCallOptions are the caller's side of a direct call.
type MCPCallOptions struct {
	// Confirmed says the person was asked and said yes. A destructive tool is
	// refused without it.
	Confirmed bool
	// SessionID is the conversation the button was in, for the log.
	SessionID string
	// Unlisted skips the allowlist: for the app's own fixed read-only calls
	// (the home tiles), never for anything a client names.
	Unlisted bool
	// Quiet leaves the call out of the approvals log, for the same reads.
	Quiet   bool
	Timeout time.Duration
}

// MCPActions calls tools on configured MCP servers directly.
type MCPActions struct {
	// Sources are the mcpServers.json files, in precedence order.
	Sources func() []string
	// Allowed is the server allowlist (Settings.ActionServers).
	Allowed func() []string
	// Lookup reads the environment for ${NAME}; os.LookupEnv by default.
	Lookup func(string) (string, bool)

	audit *approvalAudit

	mu       sync.Mutex
	sessions map[string]*actionSession
}

type actionSession struct {
	key     string // the expanded entry, so a changed config reconnects
	session *mcp.ClientSession
	tools   map[string]*mcp.Tool
}

// NewMCPActions reads servers from the instance's MCP config files and
// audits into the approvals log.
func NewMCPActions(allowed func() []string) *MCPActions {
	return &MCPActions{
		Sources: MCPConfigSources,
		Allowed: allowed,
		Lookup:  os.LookupEnv,
		audit:   &approvalAudit{path: AuditLogPath()},
	}
}

// Configured reports whether server has a usable entry, and if not, why.
func (m *MCPActions) Configured(server string) (bool, string) {
	_, missing, _, ok := findMCPServer(m.Sources(), server, m.lookup())
	switch {
	case !ok:
		return false, fmt.Sprintf("%s is not configured: add a %q server to mcpServers.json", server, server)
	case len(missing) > 0:
		return false, fmt.Sprintf("%s is not configured: %s not set", server, strings.Join(missing, ", "))
	}
	return true, ""
}

func (m *MCPActions) lookup() func(string) (string, bool) {
	if m.Lookup != nil {
		return m.Lookup
	}
	return os.LookupEnv
}

// Call runs tool on server with args. It never returns a Go error: every
// outcome, refusals included, is a result the client can show.
func (m *MCPActions) Call(ctx context.Context, server, tool string, args any, opts MCPCallOptions) MCPCallResult {
	server, tool = strings.TrimSpace(server), strings.TrimSpace(tool)
	res := m.call(ctx, server, tool, args, opts)
	if !opts.Quiet {
		reason := "ok"
		if !res.OK {
			reason = res.Code
		}
		m.audit.append(AuditEntry{
			At:        time.Now().UTC(),
			Tool:      "mcp_" + server + "_" + tool,
			Allowed:   res.Code != ActionNotAllowed && res.Code != ActionNeedsConfirm,
			DecidedBy: "ui-action",
			Reason:    reason,
			SessionID: opts.SessionID,
		})
	}
	return res
}

func fail(code, msg string) MCPCallResult {
	return MCPCallResult{OK: false, Error: msg, Code: code}
}

func (m *MCPActions) call(ctx context.Context, server, tool string, args any, opts MCPCallOptions) MCPCallResult {
	if server == "" || tool == "" {
		return fail(ActionBadArgs, "server and tool are required")
	}
	if !opts.Unlisted {
		var allowed []string
		if m.Allowed != nil {
			allowed = m.Allowed()
		}
		if !slices.Contains(allowed, server) {
			return fail(ActionNotAllowed, fmt.Sprintf("%s is not a server buttons may call directly", server))
		}
	}
	argMap, ok := args.(map[string]any)
	switch {
	case args == nil:
		argMap = map[string]any{}
	case !ok:
		return fail(ActionBadArgs, "args must be a JSON object")
	}
	if ok, why := m.Configured(server); !ok {
		return fail(ActionNotConfigured, why)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultActionTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sess, err := m.session(ctx, server)
	if err != nil {
		return fail(ActionUnavailable, fmt.Sprintf("%s: %v", server, err))
	}
	t, known := sess.tools[tool]
	if !known {
		return fail(ActionUnknownTool, fmt.Sprintf("%s has no tool %s", server, tool))
	}
	if destructive(t) && !opts.Confirmed {
		return fail(ActionNeedsConfirm, fmt.Sprintf("%s deletes or overwrites something; ask the person and call again with confirmed", tool))
	}

	out, err := sess.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: argMap})
	if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		// A session the server forgot (it restarted) fails before the tool
		// runs; one fresh session is worth trying. A deadline is not retried:
		// the tool may have run.
		m.drop(server, sess)
		if sess, err = m.session(ctx, server); err == nil {
			out, err = sess.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: argMap})
		}
	}
	if err != nil {
		m.drop(server, sess)
		return fail(ActionUnavailable, fmt.Sprintf("%s/%s: %v", server, tool, err))
	}
	text := toolText(out)
	r := MCPCallResult{OK: !out.IsError, Text: text, Structured: structuredOf(out, text)}
	if out.IsError {
		r.Code, r.Error = ActionToolError, text
		if r.Error == "" {
			r.Error = tool + " failed"
		}
	}
	return r
}

// destructive is a tool that removes or overwrites: by name (delete_*) or by
// its own annotation. A tool with no annotation is not treated as one — the
// spec's default would put a confirmation in front of every write.
func destructive(t *mcp.Tool) bool {
	if strings.HasPrefix(t.Name, "delete_") {
		return true
	}
	return t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
}

func toolText(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// structuredOf is the result's structuredContent, or its text read as JSON
// for a server that does not send structured output yet.
func structuredOf(r *mcp.CallToolResult, text string) any {
	if r.StructuredContent != nil {
		if raw, ok := r.StructuredContent.(json.RawMessage); ok {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				return v
			}
			return nil
		}
		return r.StructuredContent
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var v any
		if json.Unmarshal([]byte(trimmed), &v) == nil {
			return v
		}
	}
	return nil
}

func (m *MCPActions) drop(server string, s *actionSession) {
	m.mu.Lock()
	if cur := m.sessions[server]; cur == s {
		delete(m.sessions, server)
	}
	m.mu.Unlock()
	if s != nil && s.session != nil {
		_ = s.session.Close()
	}
}

// session returns a live session to server, connecting (and listing its
// tools) when there is none or the configuration changed.
func (m *MCPActions) session(ctx context.Context, server string) (*actionSession, error) {
	entry, missing, _, ok := findMCPServer(m.Sources(), server, m.lookup())
	if !ok || len(missing) > 0 {
		return nil, errors.New("not configured")
	}
	keyRaw, _ := json.Marshal(entry)
	key := string(keyRaw)

	m.mu.Lock()
	cur := m.sessions[server]
	m.mu.Unlock()
	if cur != nil && cur.key == key {
		return cur, nil
	}
	if cur != nil {
		m.drop(server, cur)
	}

	transport, err := actionTransport(entry)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "superleo-actions", Version: "1"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	tools := map[string]*mcp.Tool{}
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			_ = cs.Close()
			return nil, fmt.Errorf("list tools: %w", err)
		}
		tools[t.Name] = t
	}
	s := &actionSession{key: key, session: cs, tools: tools}

	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]*actionSession{}
	}
	if other := m.sessions[server]; other != nil && other != s {
		// Another call connected meanwhile; keep theirs.
		m.mu.Unlock()
		_ = cs.Close()
		return other, nil
	}
	m.sessions[server] = s
	m.mu.Unlock()
	return s, nil
}

// Close ends every session.
func (m *MCPActions) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	all := m.sessions
	m.sessions = nil
	m.mu.Unlock()
	for _, s := range all {
		_ = s.session.Close()
	}
}

// actionTransport builds the transport an mcpServers entry describes: http
// (streamable, the default when there is a url), sse, or stdio.
func actionTransport(entry map[string]any) (mcp.Transport, error) {
	typ, _ := entry["type"].(string)
	url, _ := entry["url"].(string)
	headers := map[string]string{}
	if h, ok := entry["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = fmt.Sprint(v)
		}
	}
	client := &http.Client{}
	if len(headers) > 0 {
		client.Transport = &headerRoundTripper{headers: headers, base: http.DefaultTransport}
	}
	switch {
	case typ == "sse":
		if url == "" {
			return nil, errors.New("sse server has no url")
		}
		return &mcp.SSEClientTransport{Endpoint: url, HTTPClient: client}, nil
	case typ == "http" || typ == "streamable-http" || (typ == "" && url != ""):
		if url == "" {
			return nil, errors.New("http server has no url")
		}
		return &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: client, MaxRetries: 1}, nil
	case typ == "" || typ == "stdio":
		cmd := mcpCommandOf(entry)
		if cmd == "" {
			return nil, errors.New("stdio server has no command")
		}
		var args []string
		if list, ok := entry["args"].([]any); ok {
			for _, a := range list {
				args = append(args, fmt.Sprint(a))
			}
		}
		c := exec.Command(cmd, args...)
		c.Env = os.Environ()
		if env, ok := entry["env"].(map[string]any); ok {
			for k, v := range env {
				c.Env = append(c.Env, k+"="+fmt.Sprint(v))
			}
		}
		return &mcp.CommandTransport{Command: c}, nil
	}
	return nil, fmt.Errorf("unsupported server type %q", typ)
}

type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

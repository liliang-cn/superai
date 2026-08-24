// SuperAI as an MCP server: its schedules, reachable from any agent.
//
// The Schedules page is the only way to say "every morning at eight, work out
// my stock returns and message me" — which means a person has to open a browser
// to arrange something a sentence describes. These tools let any MCP client say
// it instead: Claude Code, OpenClaw, another SuperAI, a script.
//
// It is mounted inside the running `serve` process rather than spawned as its
// own. A second process would mean a second App over the same store, a second
// scheduler, and two holders of an advisory lock designed for one — the same
// reason `superai-daemon` waits for the lock instead of taking it.
//
// Transport is streamable HTTP at /mcp. Authentication is the edge's job: this
// endpoint inherits whatever sits in front of the server, which today is Caddy's
// basic_auth on superai.superleo.app and loopback everywhere else. It does not
// add a second, weaker gate of its own.

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robfig/cron/v3"
)

// mcpPath is where the MCP endpoint is mounted on the serve mux.
const mcpPath = "/mcp"

// mcpServerVersion is what a client sees in the initialize handshake. The app
// itself carries no version string, so this tracks the tool surface: bump it
// when a tool is added, removed, or changes shape.
const mcpServerVersion = "0.1.0"

// nextRunsShown is how many upcoming times a create/validate answer names.
//
// A wrong cron is a silent failure: the schedule exists, looks right in a
// listing, and fires at the wrong hour or never. Naming the next few times is
// the cheapest thing that turns that into something a human notices while they
// are still reading the reply.
const nextRunsShown = 3

// cronParser matches what the scheduler itself accepts: five fields, no
// seconds, plus the @every / @daily descriptors.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type scheduleCreateIn struct {
	Prompt string `json:"prompt" jsonschema:"what SuperAI should do, written exactly as you would type it into chat"`
	Cron   string `json:"cron" jsonschema:"five-field cron, e.g. '0 8 * * *' for every morning at 08:00, '0 9 * * 1-5' weekdays at 09:00, '0 */4 * * *' every four hours"`
	Name   string `json:"name,omitempty" jsonschema:"optional label shown in the schedule list; defaults to the prompt"`
	Conversation string `json:"conversation,omitempty" jsonschema:"optional conversation the runs append to; blank shares one called 'scheduled'"`
}

type scheduleIDIn struct {
	ID string `json:"id" jsonschema:"schedule id from superai_schedule_list"`
}

type scheduleEnableIn struct {
	ID      string `json:"id" jsonschema:"schedule id from superai_schedule_list"`
	Enabled bool   `json:"enabled" jsonschema:"true resumes the schedule, false pauses it without deleting it"`
}

// newMCPHandler builds the MCP endpoint over app.
func newMCPHandler(app *App, version string) http.Handler {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	s := mcp.NewServer(
		&mcp.Implementation{Name: "superai", Title: "SuperAI", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions},
	)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "superai_schedule_create",
		Title: "Schedule a prompt",
		Description: "Have SuperAI run a prompt on a clock. The cron is validated before anything " +
			"is saved, and the answer names the next few run times so a wrong hour is visible " +
			"immediately rather than at the wrong hour.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleCreateIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Prompt) == "" {
			return toolErr("prompt is empty: a schedule with nothing to do is not a schedule")
		}
		next, err := nextRuns(in.Cron, nextRunsShown)
		if err != nil {
			return toolErr(fmt.Sprintf("cron %q is not valid: %v", in.Cron, err))
		}
		if msg := app.SchedulePrompt(in.Prompt, in.Cron, in.Name, in.Conversation); msg != "ok" {
			return toolErr(msg)
		}
		return toolOK(map[string]any{
			"created":   true,
			"cron":      in.Cron,
			"next_runs": next,
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "superai_schedule_list",
		Title:       "List schedules",
		Description: "Every schedule with its cron, whether it is enabled, and when it next runs.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return toolOK(map[string]any{"schedules": app.ScheduledPrompts()})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "superai_schedule_enable",
		Title:       "Pause or resume a schedule",
		Description: "Pausing keeps the schedule and its history; deleting does not.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleEnableIn) (*mcp.CallToolResult, any, error) {
		if msg := app.SetScheduledPromptEnabled(in.ID, in.Enabled); msg != "ok" {
			return toolErr(msg)
		}
		return toolOK(map[string]any{"id": in.ID, "enabled": in.Enabled})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "superai_schedule_delete",
		Title:       "Delete a schedule",
		Description: "Removes it for good. To stop it temporarily use superai_schedule_enable with enabled=false.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleIDIn) (*mcp.CallToolResult, any, error) {
		if msg := app.DeleteScheduledPrompt(in.ID); msg != "ok" {
			return toolErr(msg)
		}
		return toolOK(map[string]any{"deleted": in.ID})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "superai_schedule_run_now",
		Title: "Run a schedule immediately",
		Description: "Runs it once, right now, without touching its timetable — the way to check " +
			"that a schedule does what its author meant before waiting until morning.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleIDIn) (*mcp.CallToolResult, any, error) {
		if msg := app.RunScheduledPromptNow(in.ID); msg != "ok" {
			return toolErr(msg)
		}
		return toolOK(map[string]any{"started": in.ID})
	})

	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		// The SDK refuses a non-loopback Host header when the listener is on
		// loopback — DNS-rebinding protection for a local server a browser
		// could be tricked into calling. That threat needs a browser that
		// reaches the listener directly, and nothing does: serve binds
		// 127.0.0.1 and the only way in is a reverse proxy that authenticates
		// first. Leaving it on would mean rejecting every request that arrives
		// with the real hostname, which is all of them.
		&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true},
	)
}

const mcpInstructions = `SuperAI runs prompts on a clock and keeps the answers in a conversation.

Use superai_schedule_create for anything of the shape "every <when>, <do something>".
Translate the human's words into a five-field cron yourself, then read back the
next run times the tool returns — if they are not what the person asked for, fix
the cron rather than explaining it.

A schedule's prompt is an instruction to SuperAI, written the way it would be
typed into chat. It is not a description of a schedule.`

// nextRuns validates a cron expression and returns its next n occurrences.
func nextRuns(expr string, n int) ([]string, error) {
	sched, err := cronParser.Parse(strings.TrimSpace(expr))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	t := time.Now()
	for range n {
		t = sched.Next(t)
		out = append(out, t.Format("Mon 2006-01-02 15:04 MST"))
	}
	return out, nil
}

func toolOK(v any) (*mcp.CallToolResult, any, error) {
	return nil, v, nil
}

// toolErr reports a failure to the model rather than to the transport: a bad
// cron is something the caller can fix on the next turn, not a broken server.
func toolErr(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

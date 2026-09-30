package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Messages, for an agent behind an adapter.
//
// A SuperAI worker takes hive messages into its own loop and has hive_send
// and hive_inbox as tools of its own. An agent behind an adapter has neither,
// so the adapter supplies both halves:
//
//   - It takes messages at /api/hive/message like any member. An order that
//     is running and can take one in (Claude Code with stream input) gets it
//     at its next step; otherwise a message that is not a reply starts a turn
//     of its own, after whatever is running now. Replies wait in the inbox.
//   - It serves hive_peers, hive_send and hive_inbox over MCP at /mcp, behind
//     the same bearer, for the agent to connect to — Claude Code with
//     --mcp-config, openclaw in its mcp.servers.

// told puts the line about the hive in front of an order, when asked to.
func (a *Adapter) told(prompt string) string {
	if !a.Tell || a.Name == "" {
		return prompt
	}
	return fmt.Sprintf("[hive] You are %s, a worker in a hive of agents led by a queen. If the hive tools are "+
		"connected (hive_peers, hive_send, hive_inbox) you can see who else is in it and send them messages; "+
		"a message that arrives while you work will appear in this conversation.\n\n%s", a.Name, prompt)
}

func (a *Adapter) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdapterJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	if a.Name == "" {
		writeAdapterJSON(w, http.StatusNotFound, map[string]any{"error": "this worker takes orders, not messages"})
		return
	}
	var m HiveMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&m); err != nil {
		writeAdapterJSON(w, http.StatusBadRequest, map[string]any{"error": "unreadable message: " + err.Error()})
		return
	}
	if err := m.Valid(); err != nil {
		writeAdapterJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if m.To != a.Name {
		writeAdapterJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("this is %s, not %s", a.Name, m.To)})
		return
	}
	if a.mail.Put(m) {
		a.deliver(m)
	}
	writeAdapterJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeAdapterJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// deliver puts a message in front of the agent: into the order it is in the
// middle of if that order can take one, otherwise into a turn of its own.
func (a *Adapter) deliver(m HiveMessage) {
	if a.mail.Waiting(m.From) {
		return // the agent is in hive_wait for it, and takes it from there
	}
	a.mu.Lock()
	injectors := make([]func(string) bool, 0, len(a.injectors))
	for _, inj := range a.injectors {
		injectors = append(injectors, inj)
	}
	a.mu.Unlock()
	text := MailPrompt(m.From, []HiveMessage{m}, true)
	for _, inj := range injectors {
		if inj(text) {
			a.mail.Mark(m.ID, true)
			return
		}
	}
	if m.ReplyTo != "" {
		return // a reply waits to be read; it does not start work
	}
	a.mu.Lock()
	if a.waking[m.From] {
		a.mu.Unlock()
		return // a turn for this sender's mail is already queued, and takes this one too
	}
	a.waking[m.From] = true
	a.mu.Unlock()
	from := m.From
	a.start(func() string {
		a.mu.Lock()
		delete(a.waking, from)
		a.mu.Unlock()
		msgs := a.mail.TakeFrom(from)
		if len(msgs) == 0 {
			return ""
		}
		return a.told(MailPrompt(from, msgs, false))
	})
}

// MailPrompt is how messages are put to an agent: who they are from, what
// they say, and how to answer. during is a message put into a turn already
// going, which is told so and left to judge whether it changes anything.
// InboxEmpty is what hive_inbox says when nothing is waiting. It says more than
// "nothing" because of what agents did with that: a worker waiting for the
// previous leg of a relay slept in the shell and called hive_inbox again, a
// dozen times, when a message arriving later wakes it anyway.
const InboxEmpty = "No unread messages. To wait for one, call hive_wait: it returns the moment a message" +
	" arrives. Do not sleep in the shell, call this again in a loop, or ask the sender with hive_ask whether" +
	" they have sent it."

// InboxStillEmpty is the answer to looking again with nothing new.
const InboxStillEmpty = "Still nothing, and nothing has arrived since you last looked. Stop checking: call" +
	" hive_wait, which returns the moment the message arrives, or end your turn saying what you are waiting for" +
	" — the message then starts a new turn for you with it and your last order in front of you."

// HiveWaitDescription is hive_wait's, the same for a SuperAI member and for an
// agent behind the adapter.
const HiveWaitDescription = "Wait for a message from another member of the hive and get it the moment it arrives." +
	"\n\nThe way to wait on someone — the previous step of a relay, an answer you asked for. It takes no" +
	" effort while it waits and returns as soon as a message is filed, so never sleep in the shell, loop on" +
	" hive_inbox, or ask the sender with hive_ask whether they have sent it. Returns the messages (marked read)," +
	" or says that nothing came in time."

// WaitResult runs one hive_wait on box and says what came of it.
func WaitResult(box *Mailbox, ctx context.Context, from string, seconds any) string {
	// JSON numbers arrive as float64, and a fraction is a fraction:
	// time.Duration(0.05)*time.Second is 0, which Wait reads as "the longest".
	d := 600 * time.Second
	switch v := seconds.(type) {
	case float64:
		if v > 0 {
			d = time.Duration(v * float64(time.Second))
		}
	case int:
		if v > 0 {
			d = time.Duration(v) * time.Second
		}
	}
	start := time.Now()
	msgs := box.Wait(ctx, strings.TrimSpace(from), d)
	if len(msgs) == 0 {
		who := "anyone"
		if strings.TrimSpace(from) != "" {
			who = from
		}
		return fmt.Sprintf("Nothing from %s in %s. If it still matters, say in your answer what you were waiting for.",
			who, time.Since(start).Round(time.Second))
	}
	raw, _ := json.Marshal(msgs)
	return string(raw)
}

func MailPrompt(from string, msgs []HiveMessage, during bool) string {
	var b strings.Builder
	if during {
		fmt.Fprintf(&b, "[hive] While you work, a message from %s:\n", from)
	} else {
		fmt.Fprintf(&b, "[hive] %d message(s) from %s:\n", len(msgs), from)
	}
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n— id %s, %s\n%s\n", m.ID, m.At.Format(time.RFC3339), m.Text)
	}
	if during {
		fmt.Fprintf(&b, "\nTake it into account in what you are doing: it may change it, or it may not. If %s needs"+
			" an answer, send it with hive_send (to %q, reply_to the id). Then carry on.", from, from)
		return b.String()
	}
	fmt.Fprintf(&b, "\nDeal with this as a member of the hive. If %s needs an answer, send it with hive_send"+
		" (to %q, reply_to the id) — a reply is read by them, it does not start work for them. If nothing is"+
		" needed, say so in a line.", from, from)
	return b.String()
}

type adapterSendIn struct {
	To      string `json:"to" jsonschema:"a member's name, queen for the queen, or * for every other member"`
	Text    string `json:"text" jsonschema:"the message, standing on its own"`
	ReplyTo string `json:"reply_to,omitempty" jsonschema:"the id of the message this answers, if any"`
}

type adapterWaitIn struct {
	From    string `json:"from,omitempty" jsonschema:"wait only for this member's messages; empty for anyone's"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"the longest to wait, up to 900; default 600"`
}

type adapterInboxIn struct {
	All bool `json:"all,omitempty" jsonschema:"the last 20 messages, read or not"`
}

type adapterNoIn struct{}

// mcpHandler serves the hive tools to the agent behind this adapter.
func (a *Adapter) mcpHandler() http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "superai-hive", Title: "SuperAI hive", Version: "1"},
		&mcp.ServerOptions{Instructions: "You are a worker in a hive of agents led by a queen. hive_peers shows who is in it; " +
			"hive_send puts a short message in another member's inbox without starting work there, and nothing waits for " +
			"the answer; hive_inbox reads what was sent to you."})
	text := func(t string) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: t}}}, nil, nil
	}
	fail := func(t string) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: t}}}, nil, nil
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "hive_peers",
		Description: "Who is in the hive besides you: the queen, and the other workers you can write to with hive_send.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ adapterNoIn) (*mcp.CallToolResult, any, error) {
		if a.Announcer == nil {
			return fail("this worker is not in a hive")
		}
		c := a.Announcer.Contacts(ctx)
		names := []string{}
		queen := HiveQueenAlias
		for n, x := range c {
			if x.Queen {
				queen = n
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		out := fmt.Sprintf("You are %s, a worker. The queen is %s — write to her with hive_send to \"queen\".\n", a.Name, queen)
		if len(names) == 0 {
			return text(out + "No other workers are live right now.")
		}
		return text(out + "Other workers:\n" + strings.Join(names, "\n"))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "hive_send",
		Description: "Send a short message to another member of the hive — the queen (\"queen\"), a worker by name, or " +
			"everyone (\"*\") — and carry on. It lands in their inbox and starts no work that you wait on. To answer a message, " +
			"set reply_to to its id. Put anything long in a file or the shared memory and send a pointer to it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in adapterSendIn) (*mcp.CallToolResult, any, error) {
		if a.Announcer == nil {
			return fail("this worker is not in a hive")
		}
		ann := a.Announcer
		out, err := SendHive(ctx, a.Name, ann.Contacts(ctx), in.To, in.Text, in.ReplyTo, ann.SendToQueen,
			func(c HiveContact, m HiveMessage) {
				// The queen sees what passes between two workers — unless
				// she carried it, and has seen it already.
				if !c.Queen && m.Via == "" {
					m.Observed = true
					go func() {
						sctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
						defer cancel()
						_ = ann.SendToQueen(sctx, m)
					}()
				}
			})
		if err != nil {
			return fail(err.Error())
		}
		return text(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "hive_inbox",
		Description: "Read the messages other members of the hive sent you: the unread ones, which are then marked read; with all, the last 20.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in adapterInboxIn) (*mcp.CallToolResult, any, error) {
		limit := 50
		if in.All {
			limit = 20
		}
		msgs := a.mail.Take(in.All, limit)
		if len(msgs) == 0 {
			if in.All {
				return text("No messages.")
			}
			return text(a.mail.Empty())
		}
		b, _ := json.Marshal(msgs)
		return text(string(b))
	})

	mcp.AddTool(s, &mcp.Tool{Name: "hive_wait", Description: HiveWaitDescription},
		func(ctx context.Context, _ *mcp.CallToolRequest, in adapterWaitIn) (*mcp.CallToolResult, any, error) {
			return text(WaitResult(a.mail, ctx, in.From, in.Seconds))
		})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		// The agent reaching this is on this machine or told the address; the
		// bearer in front is the gate, as for everything else here.
		&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
}

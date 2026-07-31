// Command superai answers one prompt and exits.
//
// The app is a window, and a window cannot be measured. Everything the desktop
// build can do — the same agent, tools, skills, MCP servers and settings — is
// reachable here without one, so the agent can be scripted, diffed between
// versions, and put in a benchmark next to other agents.
//
//	superai "what is 2 + 2?"
//	superai --json "read uploads/cv.pdf and summarise it"
//	echo "summarise this" | superai -
//
// Plain mode writes the answer to stdout and nothing else, so it composes with
// pipes. --json writes one object with the answer, the tools that were called
// and how long it took, which is what a harness needs to score a run.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v2/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
)

// result is the --json shape. Deliberately small and stable: a harness reads
// answer to score the task and tools to score the tool use.
type result struct {
	OK     bool   `json:"ok"`
	Answer string `json:"answer"`
	// Emotion is the tag the agent's system prompt makes it end on. Split out of
	// the answer rather than left in it: the desktop app shows it as a chip, and
	// anything scoring the answer would otherwise be scoring "情绪: 开心" too.
	Emotion    string   `json:"emotion,omitempty"`
	Tools      []string `json:"tools"`
	DurationMS int64    `json:"duration_ms"`
	SessionID  string   `json:"session_id"`
	Error      string   `json:"error,omitempty"`
}

func main() {
	var (
		asJSON  = flag.Bool("json", false, "print one JSON object (answer, tools, timing) instead of the answer")
		session = flag.String("session", "", "conversation to continue; default is a fresh one per run")
		timeout = flag.Duration("timeout", 10*time.Minute, "give up after this long")
		browser = flag.Bool("browser", false, "allow the agent to launch a browser (off by default: it costs a second per start)")
		verbose = flag.Bool("v", false, "log tool calls to stderr as they happen")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: superai [flags] <prompt>\n\n"+
			"Answers one prompt with the same agent the desktop app uses.\n"+
			"Pass - as the prompt to read it from stdin.\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	prompt, err := readPrompt(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}

	// Nothing about answering a prompt needs a window, and a browser costs about
	// a second of every start.
	if !*browser {
		_ = os.Setenv("SUPERAI_NO_BROWSER", "1")
	}
	log.SetOutput(os.Stderr)
	log.SetPrefix("superai ")

	settings, err := backend.LoadSettings()
	if err != nil {
		log.Printf("settings: %v (continuing with defaults)", err)
	}

	// The agent prints setup notices to stdout. In plain mode stdout is the
	// answer and nothing else, so the build happens with stdout pointed at
	// stderr and is put back afterwards.
	svc, err := withStdoutOnStderr(func() (*backend.Service, error) {
		return backend.NewService(settings)
	})
	if err != nil {
		fail(*asJSON, *session, fmt.Errorf("build agent: %w", err))
	}
	defer svc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	// A fresh conversation per run by default: a benchmark's runs must not see
	// each other, and neither should two unrelated shell invocations.
	sessionID := strings.TrimSpace(*session)
	if sessionID == "" {
		sessionID = "cli-" + uuid.NewString()
	}

	var (
		mu    sync.Mutex
		tools []string
	)
	started := time.Now()
	answer, runErr := svc.Stream(ctx, sessionID, prompt, nil, func(ev *agent.Event) {
		if ev == nil || ev.Type != agent.EventTypeToolCall || ev.ToolName == "" {
			return
		}
		mu.Lock()
		tools = append(tools, ev.ToolName)
		mu.Unlock()
		if *verbose {
			log.Printf("tool: %s", ev.ToolName)
		}
	})

	body, emotion := backend.SplitEmotion(strings.TrimSpace(answer))
	res := result{
		OK:         runErr == nil,
		Answer:     strings.TrimSpace(body),
		Emotion:    emotion,
		Tools:      tools,
		DurationMS: time.Since(started).Milliseconds(),
		SessionID:  sessionID,
	}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	if res.Tools == nil {
		res.Tools = []string{}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(res)
	} else if res.Answer != "" {
		fmt.Println(res.Answer)
	}
	if runErr != nil {
		if !*asJSON {
			fmt.Fprintln(os.Stderr, "error:", runErr)
		}
		os.Exit(1)
	}
	// An empty answer is a failed run as far as a caller is concerned, even
	// though nothing reported an error.
	if res.Answer == "" {
		if !*asJSON {
			fmt.Fprintln(os.Stderr, "error: the agent produced no answer")
		}
		os.Exit(1)
	}
}

// readPrompt takes the prompt from the arguments, or from stdin when the only
// argument is "-". Several arguments are joined, so quoting is optional.
func readPrompt(args []string) (string, error) {
	if len(args) == 1 && args[0] == "-" {
		raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		if p := strings.TrimSpace(string(raw)); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("no prompt on stdin")
	}
	if p := strings.TrimSpace(strings.Join(args, " ")); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no prompt given")
}

// withStdoutOnStderr runs fn with os.Stdout redirected to stderr, so anything
// it prints cannot be mistaken for the answer.
func withStdoutOnStderr[T any](fn func() (T, error)) (T, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return fn() // not worth failing the run over
	}
	real := os.Stdout
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stderr, r)
		close(done)
	}()

	out, fnErr := fn()

	os.Stdout = real
	_ = w.Close()
	<-done
	_ = r.Close()
	return out, fnErr
}

// fail reports a setup error in whichever shape the caller asked for.
func fail(asJSON bool, session string, err error) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(result{Tools: []string{}, SessionID: session, Error: err.Error()})
	} else {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(1)
}

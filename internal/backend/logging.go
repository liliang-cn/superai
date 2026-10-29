package backend

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	agentgolog "github.com/liliang-cn/agent-go/v3/pkg/log"
)

// SuperLeo's logger.
//
// One slog logger for the whole process: SuperLeo's own lines and agent-go's,
// redacted (agent-go's pkg/log handler masks bearer tokens, provider key
// shapes and any attribute named like a secret) and joined to the trace — a
// line logged with a context that is in a flow carries its trace_id and
// span_id, so `grep <trace id>` finds every line a request wrote.
//
// Two levels, because the two speak at very different rates. SuperLeo's own
// lines default to info; agent-go narrates every model and tool call at info,
// so its lines default to warn and are turned up on their own:
//
//	SUPERLEO_LOG_LEVEL         error | warn | info (default) | debug
//	SUPERLEO_LOG_FORMAT        text (default) | json
//	AGENTGO_LOG_LEVEL          error | warn (default) | info | debug | payload
//
// Lines go to stderr and to <home>/logs/<component>.log, rotated by size, so a
// desktop app with nowhere to print still has them.

const (
	logFileMax  = 16 << 20
	logFileKeep = 4
)

var (
	logMu      sync.Mutex
	rootLogger = slog.New(agentgolog.NewHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	agentGoLog *slog.Logger
	logFile    *rotatingFile
)

// SetupLogging installs the process logger for component ("desktop",
// "serve", "agent", …): slog.Default, the standard log package (which then
// writes through it at info), and agent-go's framework logger; also to each
// of also. It names the process's flow file after component too (flow.go).
// Safe to call again; the last call wins.
func SetupLogging(component string, also ...io.Writer) *slog.Logger {
	level := parseLogLevel(os.Getenv("SUPERLEO_LOG_LEVEL"), slog.LevelInfo)
	agentLevel, _ := agentgolog.ParseLevel(os.Getenv("AGENTGO_LOG_LEVEL"))

	var out io.Writer = os.Stderr
	component = strings.TrimSpace(component)
	if component == "" {
		component = "superleo"
	}
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
	sinks := []io.Writer{os.Stderr}
	if f, err := openRotating(LogFilePath(component), logFileMax, logFileKeep); err == nil {
		logFile = f
		sinks = append(sinks, f)
	}
	for _, w := range also {
		if w != nil {
			sinks = append(sinks, w)
		}
	}
	if len(sinks) > 1 {
		out = io.MultiWriter(sinks...)
	}
	SetFlowComponent(component)

	// The handler's own level is the lower of the two, so agent-go's lines
	// reach the filter below instead of being dropped by the sink.
	min := level
	if agentLevel < min {
		min = agentLevel
	}
	opts := &slog.HandlerOptions{Level: min}
	var base slog.Handler
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SUPERLEO_LOG_FORMAT")), "json") {
		base = slog.NewJSONHandler(out, opts)
	} else {
		base = slog.NewTextHandler(out, opts)
	}
	base = base.WithAttrs([]slog.Attr{slog.String("component", component)})
	shared := agentgolog.NewHandler(base)

	rootLogger = slog.New(levelFilter{inner: shared, min: level})
	agentGoLog = slog.New(levelFilter{inner: shared, min: agentLevel})
	slog.SetDefault(rootLogger)
	// slog.SetDefault already routes the log package through the handler;
	// the prefix and flags would only repeat what the record says.
	log.SetFlags(0)
	log.SetPrefix("")
	agentgolog.SetLogger(agentGoLog)
	return rootLogger
}

// Logger is SuperLeo's logger, with attrs for module.
func Logger(module string) *slog.Logger {
	logMu.Lock()
	l := rootLogger
	logMu.Unlock()
	if module == "" {
		return l
	}
	return l.With(slog.String("module", module))
}

// AgentGoLogger is the logger agent-go's services are given: the same sink,
// at agent-go's level. Nil before SetupLogging, which leaves agent-go on its
// own default.
func AgentGoLogger() *slog.Logger {
	logMu.Lock()
	defer logMu.Unlock()
	return agentGoLog
}

// LogFilePath is where component's lines are kept.
func LogFilePath(component string) string {
	return filepath.Join(DataDir(), "logs", component+".log")
}

func parseLogLevel(s string, def slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	}
	return def
}

// levelFilter drops records below min before they reach inner.
type levelFilter struct {
	inner slog.Handler
	min   slog.Level
}

func (f levelFilter) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= f.min && f.inner.Enabled(ctx, l)
}

func (f levelFilter) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < f.min {
		return nil
	}
	return f.inner.Handle(ctx, r)
}

func (f levelFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return levelFilter{inner: f.inner.WithAttrs(attrs), min: f.min}
}

func (f levelFilter) WithGroup(name string) slog.Handler {
	return levelFilter{inner: f.inner.WithGroup(name), min: f.min}
}

// rotatingFile appends to path and, past max bytes, moves it aside:
// path → name.1.ext → name.2.ext …, keeping keep of them. Each Write is one
// record and lands whole in one file.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	n    int64
}

func openRotating(path string, max int64, keep int) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &rotatingFile{path: path, max: max, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	r.f, r.n = f, 0
	if info, err := f.Stat(); err == nil {
		r.n = info.Size()
	}
	return nil
}

// rotated is the i-th older file: flows.jsonl → flows.1.jsonl, so whatever
// reads a directory by extension still reads them.
func (r *rotatingFile) rotated(i int) string {
	ext := filepath.Ext(r.path)
	return strings.TrimSuffix(r.path, ext) + "." + strconv.Itoa(i) + ext
}

func (r *rotatingFile) rotate() {
	_ = r.f.Close()
	_ = os.Remove(r.rotated(r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		_ = os.Rename(r.rotated(i), r.rotated(i+1))
	}
	_ = os.Rename(r.path, r.rotated(1))
	if err := r.open(); err != nil {
		r.f = nil
	}
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return len(p), nil
		}
	}
	if r.max > 0 && r.n > 0 && r.n+int64(len(p)) > r.max {
		r.rotate()
		if r.f == nil {
			return len(p), nil
		}
	}
	n, err := r.f.Write(p)
	r.n += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

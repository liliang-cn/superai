package backend

import (
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// The desktop process has no terminal; its log file is the only place its
// lines survive.
func TestSetupLoggingDesktopWritesFile(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	prevDefault, prevRoot := slog.Default(), rootLogger
	t.Cleanup(func() {
		slog.SetDefault(prevDefault)
		log.SetFlags(log.LstdFlags)
		logMu.Lock()
		rootLogger = prevRoot
		if logFile != nil {
			_ = logFile.Close()
			logFile = nil
		}
		logMu.Unlock()
	})
	SetupLogging("desktop")
	slog.Info("desktop line", "k", "v")
	log.Printf("std log line")
	Logger("mod").Warn("module line")

	b, err := os.ReadFile(LogFilePath("desktop"))
	if err != nil {
		t.Fatalf("desktop.log: %v", err)
	}
	s := string(b)
	for _, want := range []string{"desktop line", "std log line", "module line", "component=desktop"} {
		if !strings.Contains(s, want) {
			t.Errorf("desktop.log lacks %q:\n%s", want, s)
		}
	}
	if !strings.HasSuffix(LogFilePath("desktop"), "/logs/desktop.log") {
		t.Errorf("path %s", LogFilePath("desktop"))
	}
}

func TestRotatingFileRotates(t *testing.T) {
	p := t.TempDir() + "/x.log"
	r, err := openRotating(p, 20, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 5; i++ {
		_, _ = r.Write([]byte("0123456789abcdef\n"))
	}
	if _, err := os.Stat(strings.TrimSuffix(p, ".log") + ".1.log"); err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
}

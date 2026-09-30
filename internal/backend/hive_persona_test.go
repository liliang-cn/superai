package backend

import (
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/pool"
)

// Ten workers once each wrote "their" audit record to hive-audit/SuperAI,
// because nothing in the prompt told them their own name.
func TestAWorkerIsToldItsNameInTheHive(t *testing.T) {
	got := hiveSection(HiveSettings{Role: HiveRoleWorker, Name: "superai-worker-3"})
	if !strings.Contains(got, "you are superai-worker-3, a worker") {
		t.Fatalf("worker section does not name it: %q", got)
	}
}

func TestTheQueenIsToldToQuoteDisagreementNotExplainIt(t *testing.T) {
	got := hiveSection(HiveSettings{Role: HiveRoleQueen, Name: "q"})
	if !strings.Contains(got, "you are q, the queen") || !strings.Contains(got, "unexplained") {
		t.Fatalf("queen section: %q", got)
	}
}

func TestOutsideAHiveThereIsNoHiveSection(t *testing.T) {
	if got := hiveSection(HiveSettings{}); got != "" {
		t.Fatalf("standalone got %q", got)
	}
}

func TestAnUnnamedMemberIsItsHostname(t *testing.T) {
	if (HiveSettings{}).Self() == "" {
		t.Fatal("no name and no hostname")
	}
	if got := (HiveSettings{Name: "  w1 "}).Self(); got != "w1" {
		t.Fatalf("got %q", got)
	}
}

// agent-go knows no model's window; without the setting a 1M-token model
// compacts at 60k.
func TestTheSettingsStateTheModelsWindow(t *testing.T) {
	s := &Settings{LLMModel: "window-test-model-x", LLMContextTokens: 1048576, LLMMaxOutputTokens: 65536}
	s.registerWindow()
	defer pool.UnregisterModelWindow("window-test-model-x")
	w, ok := pool.LookupModelWindow("window-test-model-x")
	if !ok || w.ContextTokens != 1048576 || w.MaxOutputTokens != 65536 {
		t.Fatalf("window = %+v %v", w, ok)
	}
	if _, ok := pool.LookupModelWindow("some-other-model"); ok {
		t.Fatal("registered more than the one model")
	}
}

package backend

import (
	"strings"
	"testing"
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

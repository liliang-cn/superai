package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A fresh home has no provider in it, which is a failure with a fix — the
// exact shape the Health card is there to show. This asserts the report is
// about *this app's* home rather than AGENTGO_HOME, that the counts agree
// with the checks, and that every failure carries something to do about it.
func TestDoctorReportsThisAppsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	t.Setenv("AGENTGO_HOME", filepath.Join(home, "not-this-one"))

	app := NewApp()
	rep := app.Doctor()

	if rep.Error != "" {
		t.Fatalf("inspection itself failed: %s", rep.Error)
	}
	if rep.Home != home {
		t.Errorf("report is about %q, want the app's own home %q", rep.Home, home)
	}
	if len(rep.Checks) == 0 {
		t.Fatal("no checks in the report")
	}
	if rep.At == "" {
		t.Error("report carries no timestamp")
	}

	ok, warn, fail := 0, 0, 0
	for _, c := range rep.Checks {
		switch c.Status {
		case "ok":
			ok++
		case "warn":
			warn++
		case "fail":
			fail++
			if strings.TrimSpace(c.Fix) == "" {
				t.Errorf("failing check %q has no fix text", c.Name)
			}
		default:
			t.Errorf("check %q has unknown status %q", c.Name, c.Status)
		}
	}
	if ok != rep.OK || warn != rep.Warn || fail != rep.Fail {
		t.Errorf("counts %d/%d/%d disagree with the checks %d/%d/%d",
			rep.OK, rep.Warn, rep.Fail, ok, warn, fail)
	}
	if rep.Healthy != (fail == 0) {
		t.Errorf("Healthy=%v with %d failures", rep.Healthy, fail)
	}

	// A home with nothing configured in it cannot run an agent, and the check
	// that says so is the one the card exists to surface.
	var sawProviders bool
	for _, c := range rep.Checks {
		if c.Name == "llm.providers" {
			sawProviders = true
			if c.Status != "fail" {
				t.Errorf("empty install reports llm.providers as %q, want fail", c.Status)
			}
		}
	}
	if !sawProviders {
		t.Error("no llm.providers check in the report")
	}
	if rep.Healthy {
		t.Error("an install with no provider reported healthy")
	}
}

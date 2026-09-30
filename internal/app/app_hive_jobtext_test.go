package app

import (
	"strings"
	"testing"
)

func TestJobTextKeepsShortAnswersWhole(t *testing.T) {
	if got := jobText("short", "t1"); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("好", jobTextKeep+10)
	got := jobText(long, "t2")
	if !strings.HasPrefix(got, strings.Repeat("好", jobTextKeep)) || !strings.Contains(got, "10 more characters") || !strings.Contains(got, "hive_task t2") {
		t.Fatalf("long answer not cut with a pointer to the rest: %q", got[len(got)-80:])
	}
}

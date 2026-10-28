package app

import (
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

func TestTurnTimesLandOnTheirAsksAndAnswers(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	const s = "hive-console-test"
	at := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	recordTurnTime(s, "我的持仓是？", at, 11400*time.Millisecond)
	recordTurnTime(s, "我的持仓是？", at.Add(time.Hour), 5*time.Second)

	turns := withTurnTimes(s, []backend.ChatTurn{
		{Role: "user", Content: "更早的一句"},
		{Role: "assistant", Content: "没有记录的回答"},
		{Role: "user", Content: "我的持仓是？"},
		{Role: "user", Kind: backend.TurnKindContext, Content: "memory"},
		{Role: "assistant", Content: "第一次"},
		{Role: "user", Content: "我的持仓是？"},
		{Role: "assistant", Content: "第二次"},
	})

	if turns[0].At != "" || turns[1].At != "" {
		t.Fatalf("a turn from before times were kept got one: %+v", turns[:2])
	}
	if turns[2].At != "2026-10-12T06:00:00Z" {
		t.Fatalf("first ask at %q", turns[2].At)
	}
	if turns[3].At != "" {
		t.Fatalf("the injected memory turn got a time: %q", turns[3].At)
	}
	if turns[4].ElapsedMs != 11400 || turns[4].At != "2026-10-12T06:00:11Z" {
		t.Fatalf("first answer: at %q, %d ms", turns[4].At, turns[4].ElapsedMs)
	}
	if turns[5].At != "2026-10-12T07:00:00Z" || turns[6].ElapsedMs != 5000 {
		t.Fatalf("the same question asked again took the first one's time: %+v %+v", turns[5], turns[6])
	}
}

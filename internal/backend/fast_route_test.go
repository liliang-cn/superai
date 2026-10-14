package backend

import (
	"context"
	"strings"
	"testing"
)

func TestFastRouterTakesOnlyShortEverydayAsks(t *testing.T) {
	r := fastRouter{model: "lite"}
	for _, ask := range []string{"你好", "我的持仓是？", "明天有什么安排？", "提醒我八点吃药", "现在几点"} {
		if route, ok := r.Route(context.Background(), ask); !ok || route.Model != "lite" {
			t.Errorf("%q should go to the fast model", ask)
		}
	}
	for _, ask := range []string{
		"帮我分析一下这只股票", "写一封邮件给房东", "为什么 VOO 跌了", "@pi 看看这个",
		strings.Repeat("今天天气不错", 10), "第一行\n第二行",
	} {
		if _, ok := r.Route(context.Background(), ask); ok {
			t.Errorf("%q should stay with the brain", ask)
		}
	}
	if _, ok := (fastRouter{}).Route(context.Background(), "你好"); ok {
		t.Error("no fast model configured must route nothing")
	}
}

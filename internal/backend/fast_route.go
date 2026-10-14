package backend

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// fastRouter sends short everyday asks to the fast model and leaves the rest
// to the brain.
//
// A rule rather than a model: agent-go's decision router asks a model which
// model to use, and that question costs as much as the seconds it would save.
// The rule is deliberately narrow — short, one line, nothing asking to
// analyse, write, plan or build — so what it gets wrong is a simple question
// answered by the stronger model, never a hard one answered by the weaker.
type fastRouter struct{ model string }

// fastAskMaxRunes is how long an ask can be and still count as everyday.
const fastAskMaxRunes = 40

// heavyAskWords mark an ask as work for the brain, however short.
var heavyAskWords = []string{
	"分析", "调研", "研究", "写", "代码", "程序", "编程", "设计", "规划", "计划", "方案",
	"报告", "总结", "比较", "对比", "评估", "为什么", "怎么实现", "部署", "修复", "排查", "debug",
	"review", "翻译", "优化", "重构", "策略", "建议", "解释", "推理", "证明", "worker", "hive", "@",
}

func (r fastRouter) Route(_ context.Context, goal string) (agent.ModelRoute, bool) {
	if r.model == "" || !isEverydayAsk(goal) {
		return agent.ModelRoute{}, false
	}
	return agent.ModelRoute{Name: "fast", Model: r.model}, true
}

func isEverydayAsk(goal string) bool {
	g := strings.TrimSpace(goal)
	if g == "" || strings.Contains(g, "\n") || utf8.RuneCountInString(g) > fastAskMaxRunes {
		return false
	}
	lower := strings.ToLower(g)
	for _, w := range heavyAskWords {
		if strings.Contains(lower, w) {
			return false
		}
	}
	return true
}

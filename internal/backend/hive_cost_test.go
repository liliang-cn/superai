package backend

import "testing"

func TestReadTurnCost(t *testing.T) {
	var res RemoteResult
	readTurnCost(map[string]any{
		"final":              "ok",
		"estimated_cost_usd": 0.0011,
		"usage":              map[string]any{"prompt_tokens": 1200.0, "completion_tokens": 80.0, "cached_prompt_tokens": 1000.0},
	}, &res)
	if res.Usage == nil || res.Usage.PromptTokens != 1200 || res.Usage.CompletionTokens != 80 || res.Usage.CachedPromptTokens != 1000 {
		t.Fatalf("usage not read: %+v", res.Usage)
	}
	if res.CostUSD != 0.0011 || res.CostUnpriced {
		t.Fatalf("cost not read: %v %v", res.CostUSD, res.CostUnpriced)
	}

	// A worker from before cost was reported says nothing, and nothing is
	// what must come out: nil usage, not zero tokens.
	var old RemoteResult
	readTurnCost(map[string]any{"final": "ok"}, &old)
	if old.Usage != nil || old.CostUSD != 0 {
		t.Fatalf("an old worker read as reporting cost: %+v", old)
	}
}

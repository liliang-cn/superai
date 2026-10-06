package backend

import "testing"

func TestReadTurnUsage(t *testing.T) {
	var res RemoteResult
	readTurnUsage(map[string]any{
		"final": "ok",
		"usage": map[string]any{"prompt_tokens": 1200.0, "completion_tokens": 80.0, "cached_prompt_tokens": 1000.0},
	}, &res)
	if res.Usage == nil || res.Usage.PromptTokens != 1200 || res.Usage.CompletionTokens != 80 || res.Usage.CachedPromptTokens != 1000 {
		t.Fatalf("usage not read: %+v", res.Usage)
	}

	// A worker from before usage was reported says nothing, and nothing is
	// what must come out: nil usage, not zero tokens.
	var old RemoteResult
	readTurnUsage(map[string]any{"final": "ok"}, &old)
	if old.Usage != nil {
		t.Fatalf("an old worker read as reporting usage: %+v", old)
	}
}

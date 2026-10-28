package backend

import "testing"

func TestBrainSearchesItself(t *testing.T) {
	for _, c := range []struct {
		name              string
		base, search, nat string
		want              bool
	}{
		{"anthropic api, nothing declared", "https://api.anthropic.com/v1", "", "", true},
		{"anthropic api, declared none", "https://api.anthropic.com/v1", "", "none", false},
		{"anthropic api, search elsewhere", "https://api.anthropic.com/v1", "https://gw.example/v1", "", false},
		{"gateway, declared anthropic", "https://gw.example/v1", "", "anthropic", true},
		{"gateway, nothing declared", "https://gw.example/v1", "", "", false},
		{"gemini gateway", "https://gw.example/v1", "", "google_search", false},
	} {
		s := &Settings{LLMBaseURL: c.base, SearchBaseURL: c.search, SearchNative: c.nat}
		if got := s.BrainSearchesItself(); got != c.want {
			t.Errorf("%s: BrainSearchesItself() = %v, want %v", c.name, got, c.want)
		}
		if got, want := brainNativeSearch(s), map[bool]string{true: "anthropic", false: ""}[c.want]; got != want {
			t.Errorf("%s: brainNativeSearch() = %q, want %q", c.name, got, want)
		}
	}
}

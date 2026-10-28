package backend

import "testing"

// mcp-remote's form: the header names a variable the entry's own env sets.
func TestAVariableTheEntrySetsItselfIsNotMissing(t *testing.T) {
	entry := map[string]any{
		"command": "npx",
		"args":    []any{"-y", "mcp-remote", "http://h/mcp", "--header", "Authorization:${NOTED_AUTH}"},
		"env":     map[string]any{"NOTED_AUTH": "Bearer t"},
	}
	out, missing := expandMCPEntry(entry, func(string) (string, bool) { return "", false })
	if len(missing) != 0 {
		t.Fatalf("missing %v", missing)
	}
	if got := out["args"].([]any)[4]; got != "Authorization:Bearer t" {
		t.Fatalf("%v", got)
	}
}

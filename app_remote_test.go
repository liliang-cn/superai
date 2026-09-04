package main

import "testing"

// @ at the front is an address; @ anywhere else is a mention. This split is
// the whole feature's contract, and the risk is entirely on one side of it: a
// false positive silently sends somebody's message to another machine instead
// of answering it.
func TestOnlyALeadingNameIsAnAddress(t *testing.T) {
	known := func(n string) bool { return n == "pi" || n == "hermes" || n == "claude" }

	addressed := []struct{ in, agent, rest string }{
		{"@pi 帮我看看这段代码", "pi", "帮我看看这段代码"},
		{"  @hermes what is on node-e?", "hermes", "what is on node-e?"},
		{"@pi: summarise this", "pi", "summarise this"},
		{"@pi，看一下", "pi", "看一下"},
		{"@claude\nrefactor the auth package", "claude", "refactor the auth package"},
		{"@pi", "pi", ""}, // an address with no question; the caller says so
	}
	for _, c := range addressed {
		agent, rest, ok := addressedTo(c.in, known)
		if !ok {
			t.Errorf("%q was not read as an address", c.in)
			continue
		}
		if agent != c.agent || rest != c.rest {
			t.Errorf("%q -> (%q, %q), want (%q, %q)", c.in, agent, rest, c.agent, c.rest)
		}
	}

	// Everything here has to reach SuperAI's own model. The last three are the
	// ones that would hurt: an address in the middle of a sentence, a name
	// that is not an agent, and text that merely starts with punctuation.
	for _, in := range []string{
		"让 @pi 和 @hermes 各看一遍再对比",
		"email me at @pi.example.com",             // not followed by a separator
		"@gpt what do you think",                  // not a configured agent
		"@pi/scripts/run.sh 这个文件是干嘛的",             // a path, not an address
		"what does @pi do?",                       // a question about it
		"`@pi` is the one on the cluster",         // quoted
		"@",                                       // nothing at all
		"@ pi hello",                              // a space where the name goes
		"the decorator is @pi and it wraps calls", // mid-sentence
	} {
		if agent, _, ok := addressedTo(in, known); ok {
			t.Errorf("%q was routed to %q instead of being answered", in, agent)
		}
	}
}

// A message that is not addressed must come back byte-for-byte, or routing
// would quietly rewrite ordinary messages on their way to the model.
func TestAnUnaddressedMessageIsUntouched(t *testing.T) {
	never := func(string) bool { return false }
	for _, in := range []string{"", "  ", "hello", "@pi hello", "a@b.com"} {
		if _, rest, ok := addressedTo(in, never); ok || rest != in {
			t.Errorf("%q came back as (%q, %v)", in, rest, ok)
		}
	}
}

package backend

import (
	"strings"
	"testing"
	"time"
)

func TestSplitEmotion(t *testing.T) {
	for _, tc := range []struct {
		name, in, wantReply, wantMood string
	}{
		{"english tag", "all green\n\nMOOD: happy", "all green", "happy"},
		// Only the English tag is recognised. The Chinese one is gone, not
		// deprecated: a parser that still peels it would keep the old shape
		// alive in the one place anyone would look to find out what the format
		// is.
		{"chinese tag is not a tag", "全绿\n\n情绪: 中性", "全绿\n\n情绪: 中性", ""},

		// The tag is optional now, which is the whole reason the rest of these
		// cases exist: a reply without one must come back untouched.
		{"no tag", "just an answer", "just an answer", ""},
		{"no tag, multiline", "line one\nline two", "line one\nline two", ""},

		// Before the tag was optional, every reply ended with one, so finding
		// the marker anywhere was good enough. Now a reply that merely talks
		// about it would be cut in half and the remainder reported as a mood.
		{
			"marker mid-sentence",
			"The MOOD: prefix is how the avatar is driven.",
			"The MOOD: prefix is how the avatar is driven.",
			"",
		},
		{
			"marker mid-sentence on an earlier line",
			"Write MOOD: happy at the end.\nThat is the format.",
			"Write MOOD: happy at the end.\nThat is the format.",
			"",
		},

		// A tag with nothing after it says nothing; removing the line would be
		// its only effect.
		{"empty tag", "an answer\nMOOD:", "an answer\nMOOD:", ""},

		{"tag only", "MOOD: sleepy", "", "sleepy"},
		{"trailing whitespace", "done\n\nMOOD: excited   \n\n", "done", "excited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply, mood := SplitEmotion(tc.in)
			if reply != tc.wantReply {
				t.Errorf("reply = %q, want %q", reply, tc.wantReply)
			}
			if mood != tc.wantMood {
				t.Errorf("mood = %q, want %q", mood, tc.wantMood)
			}
		})
	}
}

// The persona offers the tag rather than requiring it: a reply that is only a
// fact should not be made worse by hunting for a feeling to attach.
func TestPersonaDoesNotDemandAMood(t *testing.T) {
	p := buildPersona(time.Now(), false)
	if !strings.Contains(p, "MOOD:") {
		t.Error("the persona should still name the tag")
	}
	for _, want := range []string{"optional", "never worth a worse answer"} {
		if !strings.Contains(p, want) {
			t.Errorf("the persona should say the tag is %q", want)
		}
	}
	if strings.Contains(p, "End every reply") {
		t.Error("the tag must not be demanded on every reply")
	}
}

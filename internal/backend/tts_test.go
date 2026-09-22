package backend

import (
	"context"
	"strings"
	"testing"
)

// The splitter is what decides where the voice pauses, so a mistake here is
// audible rather than theoretical: a cut mid-clause sounds like the speaker
// lost their place.

func TestASentenceIsCutAtItsTerminator(t *testing.T) {
	got := SplitSentences("成都今天天气不错。明天可能下雨。", 60)
	want := []string{"成都今天天气不错。", "明天可能下雨。"}
	if len(got) != len(want) {
		t.Fatalf("got %d pieces %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("piece %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTheClosingQuoteStaysWithTheSentenceItEnds(t *testing.T) {
	// Otherwise the next piece opens with a stray ” and the pause lands
	// before the punctuation rather than after it.
	got := SplitSentences(`他说：“今天不错。”然后就走了。`, 60)
	if len(got) != 2 {
		t.Fatalf("got %d pieces: %q", len(got), got)
	}
	if !strings.HasSuffix(got[0], `”`) {
		t.Errorf("the quote was left behind: %q", got[0])
	}
	if strings.HasPrefix(got[1], `”`) {
		t.Errorf("the next piece starts with the previous one's quote: %q", got[1])
	}
}

func TestEnglishTerminatorsCountToo(t *testing.T) {
	got := SplitSentences("First one. Second one! Third?", 60)
	if len(got) != 3 {
		t.Fatalf("got %d pieces: %q", len(got), got)
	}
}

func TestARunawayClauseIsCutAtAComma(t *testing.T) {
	// No terminator anywhere: without a fallback the whole thing goes to the
	// synthesiser as one request and the wait is the thing this avoids.
	long := "成都是四川省的省会，位于四川盆地西部，自古有天府之国的美称，是国家历史文化名城，也是西部地区重要的中心城市"
	got := SplitSentences(long, 20)
	if len(got) < 3 {
		t.Fatalf("a %d-character clause became %d pieces: %q", len([]rune(long)), len(got), got)
	}
	for _, p := range got {
		if n := len([]rune(p)); n > 20+8 {
			t.Errorf("piece is %d characters, well past the %d asked for: %q", n, 20, p)
		}
	}
	// Nothing may be dropped: the reader would hear a sentence with a hole in
	// it and have no way to know.
	if joined := strings.Join(got, ""); joined != long {
		t.Errorf("splitting lost or changed text:\n got %q\nwant %q", joined, long)
	}
}

func TestNothingIsLostAcrossATypicalAnswer(t *testing.T) {
	text := "第一句话。第二句话！第三句话？\n第四句话，还有一点补充。"
	got := SplitSentences(text, 60)
	joined := strings.Join(got, "")
	// Whitespace between sentences is the one thing trimmed, so compare with
	// it removed on both sides.
	strip := func(s string) string { return strings.Join(strings.Fields(s), "") }
	if strip(joined) != strip(text) {
		t.Errorf("text changed:\n got %q\nwant %q", joined, text)
	}
}

func TestEmptyInputSaysNothing(t *testing.T) {
	if got := SplitSentences("   \n  ", 60); len(got) != 0 {
		t.Fatalf("whitespace produced %d pieces: %q", len(got), got)
	}
}

func TestSpeakingWithNoModelConfiguredIsAStateNotAFailure(t *testing.T) {
	svc := &Service{settings: &Settings{EmbedBaseURL: "https://example.invalid/v1", EmbedKey: "k"}}
	_, err := svc.Speak(context.Background(), "hello")
	if err == nil {
		t.Fatal("an unconfigured install synthesised something")
	}
	if !strings.Contains(err.Error(), "no speech model") {
		t.Fatalf("the caller cannot tell this apart from a real failure: %v", err)
	}
}

func TestTheSpeechEndpointInheritsTheEmbeddingOne(t *testing.T) {
	// They are the same gateway on this install, and asking for the same two
	// secrets twice is how one of them goes stale.
	s := &Settings{
		EmbedBaseURL: "https://gw.example/v1",
		EmbedKey:     "abc",
		TTSModel:     "some-tts",
	}
	base, key, model, _, ok := s.Speech()
	if !ok {
		t.Fatal("a configured model was reported as no speech")
	}
	if base != "https://gw.example/v1" || key != "abc" || model != "some-tts" {
		t.Fatalf("inherited the wrong values: %q %q %q", base, key, model)
	}
}

func TestTheWordNoneIsNotSentAsACredential(t *testing.T) {
	// "none" is how EmbedKey says there are no embeddings. Inheriting it
	// literally would put the word none in an Authorization header.
	s := &Settings{EmbedBaseURL: "https://gw.example/v1", EmbedKey: "none", TTSModel: "m"}
	_, key, _, _, ok := s.Speech()
	if !ok {
		t.Fatal("a configured model was reported as no speech")
	}
	if key != "" {
		t.Fatalf("key is %q", key)
	}
}

func TestAModelIsRequiredBeforeAnythingElse(t *testing.T) {
	s := &Settings{EmbedBaseURL: "https://gw.example/v1", EmbedKey: "abc"}
	if _, _, _, _, ok := s.Speech(); ok {
		t.Fatal("no model, yet speech reported itself configured")
	}
}

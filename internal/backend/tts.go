package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Reading an answer out loud.
//
// One POST to an OpenAI-compatible /v1/audio/speech, returning mp3. It lives
// here rather than in the browser for the ordinary reason: the key would
// otherwise have to be in the page, and a page is not a place to keep one.
//
// It synthesises one sentence at a time, and that is the whole design rather
// than a convenience. Measured against the gateway this install uses, latency
// tracks length at roughly 0.15s per character — a ninety-character paragraph
// takes about fifteen seconds before a single sound comes out, which after a
// model turn that already took seven is a very long silence. A twenty
// character sentence takes about three.
//
// The reason the rest does not then stutter: the same measurement produced
// 22.5 seconds of audio in 14.6 seconds of synthesis, so synthesis runs about
// 1.5x faster than speech. Once the first sentence is playing, the next is
// always ready before it is needed.

// speechTimeout bounds one sentence. Generous, because a long one legitimately
// takes ten seconds, and a wedged request must still end.
const speechTimeout = 90 * time.Second

// ttsMaxInput caps one request. A caller that sends a whole document would
// wait minutes for a single response, which is exactly the failure this is
// shaped to avoid — better to refuse and say why.
const ttsMaxInput = 600

// ErrNoSpeech is returned when no speech model is configured. Callers turn it
// into "this install has no voice" rather than an error, because not
// configuring one is the default.
var ErrNoSpeech = errors.New("no speech model configured")

// Speak synthesises text and returns mp3 bytes.
func (s *Service) Speak(ctx context.Context, text string) ([]byte, error) {
	body := strings.TrimSpace(text)
	if body == "" {
		return nil, errors.New("nothing to say")
	}
	if len([]rune(body)) > ttsMaxInput {
		return nil, fmt.Errorf("too long to speak in one piece: %d characters, limit %d — split it into sentences",
			len([]rune(body)), ttsMaxInput)
	}
	if s == nil || s.settings == nil {
		return nil, ErrNoSpeech
	}
	baseURL, key, model, voice, ok := s.settings.Speech()
	if !ok {
		return nil, ErrNoSpeech
	}

	payload := map[string]any{
		"model":           model,
		"input":           body,
		"response_format": "mp3",
	}
	if voice != "" {
		payload["voice"] = voice
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, speechTimeout)
	defer cancel()

	endpoint := strings.TrimRight(baseURL, "/") + "/audio/speech"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Bounded: a provider that answers a speech request with a stream of
	// something else must not be able to fill memory. Twenty megabytes is
	// minutes of audio at this bitrate.
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// The body is the provider's own explanation — a wrong model id, a
		// dead key — and it is the whole diagnosis most of the time.
		msg := strings.TrimSpace(string(audio))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("speech: %s: %s", resp.Status, msg)
	}
	if len(audio) == 0 {
		return nil, errors.New("speech: the provider returned no audio")
	}
	return audio, nil
}

// SplitSentences cuts text into pieces short enough to synthesise one at a
// time, breaking where a speaker would.
//
// It exists here rather than in the page so both the console and anything else
// that grows a voice later split the same way — a sentence boundary the
// synthesiser disagrees with is audible, and two implementations of it would
// eventually disagree with each other too.
func SplitSentences(text string, max int) []string {
	if max <= 0 {
		max = 60
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}

	// Chinese and Latin terminators both end a sentence, and the closing
	// quote or bracket that may follow one belongs to it rather than to the
	// next.
	isEnd := func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', '\n', '.', '!', '?', ';':
			return true
		}
		return false
	}
	isTrailer := func(r rune) bool {
		switch r {
		case '”', '’', '』', '」', '）', ')', '"', '\'', '】', ']':
			return true
		}
		return false
	}

	var out []string
	start := 0
	flush := func(end int) {
		piece := strings.TrimSpace(string(runes[start:end]))
		if piece != "" {
			out = append(out, piece)
		}
		start = end
	}
	for i := 0; i < len(runes); i++ {
		if isEnd(runes[i]) {
			j := i + 1
			for j < len(runes) && isTrailer(runes[j]) {
				j++
			}
			flush(j)
			i = j - 1
			continue
		}
		// A sentence with no terminator in sight still has to be cut, or one
		// runaway paragraph becomes one very long wait. Prefer a comma.
		if i-start >= max {
			cut := i + 1
			for k := i; k > start+max/2; k-- {
				if runes[k] == '，' || runes[k] == ',' || runes[k] == '、' || runes[k] == ' ' {
					cut = k + 1
					break
				}
			}
			flush(cut)
			i = cut - 1
		}
	}
	flush(len(runes))
	return out
}

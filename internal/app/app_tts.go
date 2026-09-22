package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/liliang-cn/superai-desktop/internal/backend"
)

// The speech route.
//
// Gated like everything else under /api — see auth.go. That matters more here
// than it looks: this endpoint spends someone's money per call, and an open
// one is a way to bill them for reading arbitrary text aloud.

// handleSpeak synthesises one sentence and returns audio/mpeg.
func (a *App) handleSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	// 16KB: the cap on what may be spoken is in characters, enforced below
	// with a message that says so. This is only here to stop a body big
	// enough to matter from being read at all.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "expected {\"text\": \"…\"}"})
		return
	}

	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "backend not ready"})
		return
	}

	audio, err := svc.Speak(r.Context(), body.Text)
	if err != nil {
		// Not configured is a state, not a fault: the caller should fall back
		// to whatever voice it has rather than show an error to someone who
		// never asked for this.
		if errors.Is(err, backend.ErrNoSpeech) {
			writeJSONStatus(w, http.StatusNotImplemented, map[string]any{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
	// Each sentence is asked for once, by one page, and holding it would only
	// mean a stale voice after the model is changed.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(audio)
}

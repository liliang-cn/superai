package app

// The generic webhook channel, inbound: anything that can make an HTTP POST
// can talk to SuperLeo the way a Telegram chat does.
//
//	POST /in/hook/<channel id>
//	Authorization: Bearer <the channel's inbound token>   (or X-Hook-Token)
//	{"text": "…", "conversation": "optional session id"}
//
// The message runs a turn exactly as one from Telegram does (backend
// telegram.go): the same Service.Stream, in the conversation named, or the
// channel's own ("hook:<id>") when none is, so it shows in History and can be
// carried on from the app. How the answer goes back is the channel's reply
// mode:
//
//   - sync: the answer is the response body. A turn can take minutes; past
//     channelSyncWait the response is 202 with a request id, and the answer
//     is fetched from GET /in/hook/<id>/result/<request id> with the same
//     token once it is there.
//   - callback: 202 at once, and the answer is POSTed to the channel's send
//     URL as a WebhookPayload with event "reply", the request id and the
//     conversation, signed as every other post to it is.
//
// The route is outside /api and not behind the login gate: it carries its own
// credential, which opens this door and nothing else. Wrong tokens count
// against the caller's address (the login throttle's rules), each channel is
// rate limited, the body is capped, and a turn the caller started is the only
// thing a token can do.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

const (
	// channelInboundMax caps one message. A message is something a person or
	// a script says to the assistant; 64 KB is pages of it.
	channelInboundMax = 64 << 10
	// channelRatePerMinute and channelConcurrent bound one channel: a leaked
	// token, or a loop in somebody's automation, must not run the model flat
	// out.
	channelRatePerMinute = 30
	channelConcurrent    = 4
	// channelResultTTL is how long an answer waits to be fetched.
	channelResultTTL = time.Hour
	channelResultMax = 256
)

// channelSyncWait is how long a sync request waits for its answer before it
// is answered 202. Under the minute most proxies and serverless callers give
// up at. A var so tests need not wait it out.
var channelSyncWait = 50 * time.Second

// channelTurn runs one inbound message; tests swap it.
var channelTurn = func(a *App, ctx context.Context, session, text string) (string, error) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "", errors.New("the agent is not ready")
	}
	return svc.Stream(ctx, session, text, nil, nil)
}

// channelReply posts a callback answer; tests swap it.
var channelReply = backend.Reply

type inboundResult struct {
	done         chan struct{}
	at           time.Time
	channel      string
	conversation string
	answer       string
	err          string
}

type inboundState struct {
	mu       sync.Mutex
	recent   map[string][]time.Time // channel → request times in the last minute
	running  map[string]int         // channel → turns in flight
	results  map[string]*inboundResult
	throttle *loginThrottle
}

var channelInbound = &inboundState{
	recent:   map[string][]time.Time{},
	running:  map[string]int{},
	results:  map[string]*inboundResult{},
	throttle: newLoginThrottle(),
}

// admit takes a slot for one turn on the channel, or says why not.
func (s *inboundState) admit(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := now.Add(-time.Minute)
	kept := s.recent[id][:0]
	for _, t := range s.recent[id] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.recent[id] = kept
	if len(kept) >= channelRatePerMinute || s.running[id] >= channelConcurrent {
		return false
	}
	s.recent[id] = append(kept, now)
	s.running[id]++
	return true
}

func (s *inboundState) release(id string) {
	s.mu.Lock()
	s.running[id]--
	s.mu.Unlock()
}

func (s *inboundState) put(rid string, r *inboundResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.results {
		if now.Sub(v.at) > channelResultTTL {
			delete(s.results, k)
		}
	}
	// Still too many: the oldest go. A result nobody fetched in the time it
	// took 256 more to arrive is not going to be fetched.
	for len(s.results) >= channelResultMax {
		oldest := ""
		for k, v := range s.results {
			if oldest == "" || v.at.Before(s.results[oldest].at) {
				oldest = k
			}
		}
		delete(s.results, oldest)
	}
	s.results[rid] = r
}

func (s *inboundState) get(rid string) *inboundResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.results[rid]
}

func channelInboundRoute(mux *http.ServeMux, app *App) {
	mux.HandleFunc(backend.ChannelInboundPrefix, app.handleChannelInbound)
}

// inboundMessage is the request body. text is the message; message is
// accepted for it, and conversation_id and session for conversation, because
// those are the names other tools already send.
type inboundMessage struct {
	Text           string `json:"text"`
	Message        string `json:"message"`
	Conversation   string `json:"conversation"`
	ConversationID string `json:"conversation_id"`
	Session        string `json:"session"`
}

func (a *App) handleChannelInbound(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, backend.ChannelInboundPrefix)
	parts := strings.Split(rest, "/")
	var id, rid string
	switch {
	case len(parts) == 1 && r.Method == http.MethodPost:
		id = parts[0]
	case len(parts) == 3 && parts[1] == "result" && r.Method == http.MethodGet:
		id, rid = parts[0], parts[2]
	case len(parts) == 1 || (len(parts) == 3 && parts[1] == "result"):
		w.Header().Set("Allow", "POST, GET")
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST a message, GET a result"})
		return
	default:
		http.NotFound(w, r)
		return
	}

	ip := clientIP(r)
	now := time.Now()
	if !channelInbound.throttle.allow(ip, now) {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "too many failed attempts; try again later"})
		return
	}
	ch, ok := a.inboundChannel(id, inboundToken(r))
	if !ok {
		channelInbound.throttle.record(ip, now)
		// One answer for an unknown channel, a disabled one and a wrong
		// token: which of them it was is nothing a stranger needs to learn.
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}

	if rid != "" {
		res := channelInbound.get(rid)
		if res == nil || res.channel != ch.ID {
			writeJSONStatus(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no such request (results are kept for an hour)"})
			return
		}
		writeResult(w, rid, ch.ID, res, false)
		return
	}

	msg, err := readInbound(w, r)
	if err != nil {
		code := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code = http.StatusRequestEntityTooLarge
		}
		writeJSONStatus(w, code, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	text := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Message))
	if text == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": `send {"text": "…"}`})
		return
	}
	conversation := strings.TrimSpace(firstNonEmpty(msg.Conversation, msg.ConversationID, msg.Session))
	if len(conversation) > 128 || strings.ContainsAny(conversation, "\r\n\t") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "conversation is an id of at most 128 characters"})
		return
	}
	if conversation == "" {
		conversation = "hook:" + ch.ID
	}
	if !channelInbound.admit(ch.ID, now) {
		w.Header().Set("Retry-After", "30")
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "this channel is busy; try again shortly"})
		return
	}
	backend.RecordChannelInbound(ch.ID)

	rid = "req_" + randomSecret(12)
	res := &inboundResult{done: make(chan struct{}), at: now, channel: ch.ID, conversation: conversation}
	channelInbound.put(rid, res)

	// The turn belongs to the app, not to the request: a caller that hangs
	// up, or a sync wait that runs out, must not throw the work away. Like a
	// Telegram turn it has no deadline of its own.
	go func() {
		defer channelInbound.release(ch.ID)
		final, err := channelTurn(a, context.Background(), conversation, text)
		if err != nil {
			res.err = err.Error()
		} else {
			reply, _ := backend.SplitEmotion(final)
			res.answer = strings.TrimSpace(reply)
		}
		close(res.done)
		if ch.ReplyMode == backend.ReplyCallback {
			// The channel as it is now: its URL or secret may have been
			// changed while the turn ran.
			cur, ok := a.channelByID(ch.ID)
			if !ok {
				return
			}
			_ = channelReply(context.Background(), cur, backend.WebhookPayload{
				Title: "SuperLeo", Message: firstNonEmpty(res.answer, res.err), Error: res.err,
				RequestID: rid, Conversation: conversation,
			})
		}
	}()

	if ch.ReplyMode == backend.ReplyCallback {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{
			"ok": true, "request_id": rid, "conversation": conversation, "status": "accepted",
		})
		return
	}
	select {
	case <-res.done:
	case <-time.After(channelSyncWait):
	case <-r.Context().Done():
		return
	}
	writeResult(w, rid, ch.ID, res, true)
}

// writeResult answers with a request's state: the answer, the failure, or
// that it is still running and where to look.
func writeResult(w http.ResponseWriter, rid, channel string, res *inboundResult, first bool) {
	body := map[string]any{"request_id": rid, "conversation": res.conversation}
	select {
	case <-res.done:
	default:
		body["ok"], body["status"] = true, "running"
		body["result"] = backend.ChannelInboundPrefix + channel + "/result/" + rid
		code := http.StatusOK
		if first {
			code = http.StatusAccepted
		}
		writeJSONStatus(w, code, body)
		return
	}
	if res.err != "" {
		body["ok"], body["status"], body["error"] = false, "failed", res.err
		writeJSONStatus(w, http.StatusBadGateway, body)
		return
	}
	body["ok"], body["status"], body["answer"] = true, "done", res.answer
	writeJSONStatus(w, http.StatusOK, body)
}

// inboundToken reads the credential: a Bearer, or X-Hook-Token for senders
// that can set a header but not Authorization.
func inboundToken(r *http.Request) string {
	if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(b)
	}
	return strings.TrimSpace(r.Header.Get("X-Hook-Token"))
}

// inboundChannel is the enabled webhook channel with the id, if token is its
// inbound token. Compared in constant time.
func (a *App) inboundChannel(id, token string) (backend.Channel, bool) {
	ch, ok := a.channelByID(id)
	if !ok || ch.Type != backend.ChannelWebhook || !ch.Enabled || ch.InboundToken == "" || token == "" {
		return backend.Channel{}, false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(ch.InboundToken)) != 1 {
		return backend.Channel{}, false
	}
	return ch, true
}

func (a *App) channelByID(id string) (backend.Channel, bool) {
	if id == "" {
		return backend.Channel{}, false
	}
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return backend.FindChannel(backend.EffectiveChannels(s), id)
}

// readInbound reads the body: JSON, or the message as plain text.
func readInbound(w http.ResponseWriter, r *http.Request) (inboundMessage, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, channelInboundMax))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return inboundMessage{}, tooBig
		}
		return inboundMessage{}, errors.New("could not read the body")
	}
	body := strings.TrimSpace(string(raw))
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "json") || strings.HasPrefix(body, "{") {
		var m inboundMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return inboundMessage{}, errors.New("the body is not JSON")
		}
		return m, nil
	}
	return inboundMessage{Text: body}, nil
}

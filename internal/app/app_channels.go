package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The Channels list in Settings: what each channel is, whether it works, and
// the changes made to it one channel at a time.
//
// A channel save writes the settings and points the dispatcher at the new
// list; it does not rebuild the Service the way SaveSettings does, because
// flicking a channel's switch should not restart the agent. The Telegram
// bridge is restarted when its channel changed, since the poll holds the
// token.

// channelsMu serialises the read-change-write of the channel list.
var channelsMu sync.Mutex

// ChannelList is what ListChannels answers.
type ChannelList struct {
	Channels []backend.ChannelView `json:"channels"`
	// PublicURL is where this SuperLeo is reached from outside, when known:
	// the receive address is PublicURL + a channel's inbound_path.
	PublicURL string   `json:"public_url,omitempty"`
	Types     []string `json:"types"`
	Events    []string `json:"events"`
}

// ChannelTestResult is what TestChannel answers.
type ChannelTestResult struct {
	OK      bool                `json:"ok"`
	Error   string              `json:"error,omitempty"`
	Channel backend.ChannelView `json:"channel"`
}

// ListChannels lists the channels, their state and what is needed to wire
// them up. No secret is returned whole.
func (a *App) ListChannels() ChannelList {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	out := ChannelList{Channels: []backend.ChannelView{}, Types: backend.ChannelTypes, Events: backend.ChannelEvents}
	if s == nil {
		return out
	}
	out.PublicURL = s.Hive.Public()
	for _, c := range backend.EffectiveChannels(s) {
		out.Channels = append(out.Channels, a.channelView(c))
	}
	return out
}

func (a *App) channelView(c backend.Channel) backend.ChannelView {
	v := backend.ViewOf(c)
	if c.Type != backend.ChannelTelegram || !c.Enabled {
		return v
	}
	a.mu.Lock()
	s, terr, bridge := a.settings, a.telegramErr, a.telegram
	a.mu.Unlock()
	// Only the first enabled bot is polled; the others only send.
	if polled, ok := s.TelegramChannel(); !ok || polled.ID != c.ID {
		return v
	}
	switch {
	case terr != "":
		v.State, v.StateError = "error", terr
	case bridge != nil && telegramPolling.Load():
		v.State = "polling"
	case bridge != nil:
		v.State = "idle"
	}
	return v
}

// SaveChannel creates a channel (no id) or changes one. Fields left out keep
// their values; an empty string clears one.
func (a *App) SaveChannel(in backend.ChannelInput) (backend.ChannelView, error) {
	var id string
	err := a.changeChannels(func(chs []backend.Channel) ([]backend.Channel, error) {
		next, cid, err := backend.ApplyChannelInput(chs, in)
		id = cid
		return next, err
	})
	if err != nil {
		return backend.ChannelView{}, err
	}
	c, _ := a.channelByID(id)
	return a.channelView(c), nil
}

// DeleteChannel removes a channel.
func (a *App) DeleteChannel(id string) error {
	err := a.changeChannels(func(chs []backend.Channel) ([]backend.Channel, error) {
		i := slices.IndexFunc(chs, func(c backend.Channel) bool { return c.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("no channel %q", id)
		}
		return slices.Delete(chs, i, i+1), nil
	})
	if err == nil {
		backend.ForgetChannelStatus(id)
	}
	return err
}

// RegenerateChannelToken gives a webhook channel a new inbound token; the old
// one stops working at once.
func (a *App) RegenerateChannelToken(id string) (backend.ChannelView, error) {
	err := a.changeChannels(func(chs []backend.Channel) ([]backend.Channel, error) {
		for i := range chs {
			if chs[i].ID == id {
				if chs[i].Type != backend.ChannelWebhook {
					return nil, errors.New("only a webhook channel receives with a token")
				}
				chs[i].InboundToken = backend.NewChannelToken()
				return chs, nil
			}
		}
		return nil, fmt.Errorf("no channel %q", id)
	})
	if err != nil {
		return backend.ChannelView{}, err
	}
	c, _ := a.channelByID(id)
	return a.channelView(c), nil
}

// RevealChannelToken returns a webhook channel's inbound token whole, for the
// copy and show buttons. The one call that does: the list never carries it.
func (a *App) RevealChannelToken(id string) (string, error) {
	c, ok := a.channelByID(id)
	if !ok || c.Type != backend.ChannelWebhook {
		return "", fmt.Errorf("no webhook channel %q", id)
	}
	return c.InboundToken, nil
}

// TestChannel sends a test message through one channel, on or off.
func (a *App) TestChannel(id string) ChannelTestResult {
	c, ok := a.channelByID(id)
	if !ok {
		return ChannelTestResult{Error: fmt.Sprintf("no channel %q", id)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := backend.TestChannel(ctx, c)
	res := ChannelTestResult{OK: err == nil, Channel: a.channelView(c)}
	if err != nil {
		res.Error = err.Error()
	}
	a.emit("channels:changed", nil)
	return res
}

// changeChannels applies one change to the list, saves, and brings the
// dispatcher and the Telegram bridge up to date.
func (a *App) changeChannels(change func([]backend.Channel) ([]backend.Channel, error)) error {
	channelsMu.Lock()
	defer channelsMu.Unlock()
	a.mu.Lock()
	if a.settings == nil {
		a.mu.Unlock()
		return errors.New("settings are not loaded yet")
	}
	next := *a.settings
	a.mu.Unlock()

	before := backend.EffectiveChannels(&next)
	chs, err := change(backend.EffectiveChannels(&next))
	if err != nil {
		return err
	}
	next.Channels = chs
	if err := next.Save(); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = &next
	svc := a.svc
	a.mu.Unlock()
	if svc != nil {
		svc.Notifier().Update(&next)
	}
	prevBot, _ := (&backend.Settings{Channels: before}).TelegramChannel()
	nextBot, _ := next.TelegramChannel()
	if prevBot.ID != nextBot.ID || prevBot.BotToken != nextBot.BotToken || !slices.Equal(prevBot.AllowedChats, nextBot.AllowedChats) {
		a.startTelegram()
	}
	a.emit("channels:changed", nil)
	// The old Webhook and Telegram fields mirror the channels; a page that
	// shows them should read them again.
	a.emit("settings:changed", nil)
	return nil
}

// pushNeedsYou tells the channels that want it that a tool call is waiting
// for an answer. Only the channels: the window already shows the card.
func (a *App) pushNeedsYou(req backend.ApprovalRequest) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil || !svc.Notifier().Enabled() {
		return
	}
	who := strings.TrimSpace(req.By)
	if who == "" {
		who = "SuperLeo"
	}
	what := strings.TrimSpace(req.Command)
	if what == "" {
		what = req.Tool
	}
	if req.Machine != "" {
		what += "\n(on " + req.Machine + ")"
	}
	go svc.Notifier().Send(context.Background(), backend.WebhookPayload{
		Event: backend.WebhookEventNeedsYou, Kind: backend.ChannelEventNeedsYou,
		Title: who + " needs your OK", Message: what, Conversation: req.SessionID,
	})
}

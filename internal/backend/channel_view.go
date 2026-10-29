package backend

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// What the settings page sees of a channel, and what it sends back.
//
// The page never gets a secret back whole: URLs that carry a bot's key show
// its first characters, tokens their last four. What it sends is a patch —
// a field left out keeps its value — so saving a channel without retyping its
// secret does not blank it.

// ChannelView is one channel as listed.
type ChannelView struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
	TwoWay  bool     `json:"two_way"`

	// URL is masked where it carries a key.
	URL        string `json:"url,omitempty"`
	SecretSet  bool   `json:"secret_set"`
	SecretMask string `json:"secret_mask,omitempty"`

	// Webhook inbound.
	InboundPath string `json:"inbound_path,omitempty"`
	TokenMask   string `json:"token_mask,omitempty"`
	ReplyMode   string `json:"reply_mode,omitempty"`

	// Telegram.
	BotTokenMask string  `json:"bot_token_mask,omitempty"`
	AllowedChats []int64 `json:"allowed_chats,omitempty"`

	// Bark.
	Server        string `json:"server,omitempty"`
	DeviceKeyMask string `json:"device_key_mask,omitempty"`

	CanSend bool          `json:"can_send"`
	Status  ChannelStatus `json:"status"`
	// State is what the app knows live, beyond the record: for Telegram,
	// "polling", "idle" (another process holds the poll) or "error".
	State      string `json:"state,omitempty"`
	StateError string `json:"state_error,omitempty"`
}

// ChannelInput creates or changes a channel. Every field but Type is a
// patch: absent keeps what is there. Type is only read on create.
type ChannelInput struct {
	ID           string   `json:"id,omitempty"`
	Type         string   `json:"type,omitempty"`
	Name         *string  `json:"name,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
	Events       []string `json:"events,omitempty"`
	URL          *string  `json:"url,omitempty"`
	Secret       *string  `json:"secret,omitempty"`
	ReplyMode    *string  `json:"reply_mode,omitempty"`
	BotToken     *string  `json:"bot_token,omitempty"`
	AllowedChats []int64  `json:"allowed_chats,omitempty"`
	Server       *string  `json:"server,omitempty"`
	DeviceKey    *string  `json:"device_key,omitempty"`
}

// ViewOf is the listed form of a channel.
func ViewOf(c Channel) ChannelView {
	v := ChannelView{
		ID: c.ID, Type: c.Type, Name: c.Name, Enabled: c.Enabled,
		Events: slices.Clone(c.Events), TwoWay: c.TwoWay(),
		URL:       maskURL(c.URL),
		SecretSet: c.Secret != "", SecretMask: maskSecret(c.Secret),
		InboundPath: c.InboundPath(), TokenMask: maskSecret(c.InboundToken), ReplyMode: c.ReplyMode,
		BotTokenMask: maskSecret(c.BotToken), AllowedChats: slices.Clone(c.AllowedChats),
		Server: c.Server, DeviceKeyMask: maskSecret(c.DeviceKey),
		CanSend: c.CanSend(),
		Status:  ChannelStatusOf(c.ID),
	}
	if v.Events == nil {
		v.Events = []string{}
	}
	return v
}

// ApplyChannelInput creates a channel (in.ID empty) or patches the one with
// in.ID, and returns the new list and the channel's id.
func ApplyChannelInput(chs []Channel, in ChannelInput) ([]Channel, string, error) {
	chs = cloneChannels(chs)
	var c Channel
	idx := -1
	if id := strings.TrimSpace(in.ID); id != "" {
		for i := range chs {
			if chs[i].ID == id {
				idx, c = i, chs[i]
				break
			}
		}
		if idx < 0 {
			return nil, "", fmt.Errorf("no channel %q", id)
		}
	} else {
		t := strings.ToLower(strings.TrimSpace(in.Type))
		if !slices.Contains(ChannelTypes, t) {
			return nil, "", fmt.Errorf("unknown channel type %q", in.Type)
		}
		c = Channel{
			ID: newChannelID(), Type: t, Enabled: true,
			Events: slices.Clone(ChannelEvents),
		}
		if t == ChannelWebhook {
			c.InboundToken, c.ReplyMode = NewChannelToken(), ReplySync
		}
	}
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Events != nil {
		c.Events = slices.Clone(in.Events)
	}
	if in.URL != nil {
		c.URL = strings.TrimSpace(*in.URL)
	}
	if in.Secret != nil {
		c.Secret = strings.TrimSpace(*in.Secret)
	}
	if in.ReplyMode != nil {
		c.ReplyMode = strings.TrimSpace(*in.ReplyMode)
	}
	if in.BotToken != nil {
		c.BotToken = strings.TrimSpace(*in.BotToken)
	}
	if in.AllowedChats != nil {
		c.AllowedChats = slices.Clone(in.AllowedChats)
	}
	if in.Server != nil {
		c.Server = strings.TrimSpace(*in.Server)
	}
	if in.DeviceKey != nil {
		c.DeviceKey = strings.TrimSpace(*in.DeviceKey)
	}
	if err := validateChannel(c); err != nil {
		return nil, "", err
	}
	if idx < 0 {
		chs = append(chs, c)
	} else {
		chs[idx] = c
	}
	return normalizeChannels(chs), c.ID, nil
}

func validateChannel(c Channel) error {
	if c.URL != "" {
		bare := (c.Type == ChannelWeCom || c.Type == ChannelDingTalk) && !strings.Contains(c.URL, "://")
		if !bare {
			if err := httpURL(c.URL); err != nil {
				return err
			}
		}
	}
	if c.Server != "" {
		if err := httpURL(c.Server); err != nil {
			return err
		}
	}
	if c.ReplyMode != "" && c.ReplyMode != ReplySync && c.ReplyMode != ReplyCallback {
		return fmt.Errorf("reply mode is %q or %q", ReplySync, ReplyCallback)
	}
	if c.Type == ChannelTelegram && c.Enabled && c.BotToken != "" && len(c.AllowedChats) == 0 {
		// The bridge refuses to start open (telegram.go); saying so here is
		// better than a channel that is on and silently does nothing.
		return errors.New("a Telegram bot needs at least one allowed chat id: anyone can find a bot, and this one runs commands")
	}
	return nil
}

func httpURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", truncateRunes(v, 60))
	}
	return nil
}

// maskSecret shows the last four characters of anything long enough to
// spare them.
func maskSecret(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) <= 8:
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}

// maskURL keeps a URL recognisable and hides the parts that are its key: a
// long path segment (Feishu's hook id, Slack's three) and every query value
// (WeCom's key, DingTalk's access_token).
func maskURL(v string) string {
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		// A bare WeCom key or DingTalk token.
		return maskPrefix(v)
	}
	segs := strings.Split(u.Path, "/")
	for i, s := range segs {
		if len(s) >= 12 {
			segs[i] = maskPrefix(s)
		}
	}
	out := u.Scheme + "://" + u.Host + strings.Join(segs, "/")
	if u.RawQuery != "" {
		q := u.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+maskPrefix(q.Get(k)))
		}
		out += "?" + strings.Join(parts, "&")
	}
	return out
}

func maskPrefix(s string) string {
	if len(s) <= 4 {
		return "…"
	}
	return s[:4] + "…"
}

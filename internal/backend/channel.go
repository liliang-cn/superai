package backend

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Channels: where SuperLeo reaches the person, and where the person reaches
// SuperLeo.
//
// There used to be two fixed slots: one webhook URL for everything outbound,
// and one Telegram bot for the way in. The webhook was a single JSON POST on
// purpose (see webhook.go), with a relay expected to turn it into whatever a
// phone shows. People did not want to run a relay; they wanted to paste a
// Feishu or WeCom bot URL and be done. So the slots became a list, each entry
// a channel of one type, each with its own choice of what it is sent.
//
// The old fields stay. A client that predates channels reads and writes
// webhook_url and telegram_bot_token, and the agent's own settings tool can
// set webhook_url; both keep working because those fields mirror the first
// webhook and the first Telegram channel (reconcileChannels below).

// Channel types. Part of settings.json, so never renamed.
const (
	ChannelTelegram = "telegram"
	ChannelWebhook  = "webhook"
	ChannelFeishu   = "feishu"
	ChannelWeCom    = "wecom"
	ChannelDingTalk = "dingtalk"
	ChannelSlack    = "slack"
	ChannelBark     = "bark"
)

// ChannelTypes lists every type, in the order the Add menu shows them.
var ChannelTypes = []string{ChannelTelegram, ChannelFeishu, ChannelWeCom, ChannelDingTalk, ChannelSlack, ChannelBark, ChannelWebhook}

// What a channel can be sent. A notice belongs to exactly one of these
// (eventOf in webhook.go); a channel lists the ones it wants.
const (
	ChannelEventReminder = "reminder"  // a reminder came due; notify_user mid-task
	ChannelEventSchedule = "schedule"  // a scheduled prompt finished
	ChannelEventNeedsYou = "needs_you" // a tool call waits for approval
	ChannelEventFailed   = "failed"    // a run or an agent failed
)

// ChannelEvents is every event, in the order the UI shows them.
var ChannelEvents = []string{ChannelEventReminder, ChannelEventSchedule, ChannelEventNeedsYou, ChannelEventFailed}

// Reply modes for a webhook channel's inbound side.
const (
	ReplySync     = "sync"     // the answer is the HTTP response
	ReplyCallback = "callback" // the answer is POSTed to the channel's URL
)

// Channel is one entry of settings.channels. Flat, with each type using the
// fields it needs, so settings.json stays readable by a person.
type Channel struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Name    string   `json:"name,omitempty"`
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`

	// URL is where messages go: the generic webhook's receiver, or the
	// Feishu / WeCom / DingTalk / Slack bot's webhook address. WeCom also
	// takes the bare key and DingTalk the bare access_token.
	URL string `json:"url,omitempty"`
	// Secret signs or authenticates what is sent: the webhook's Bearer token,
	// Feishu's and DingTalk's signing secret.
	Secret string `json:"secret,omitempty"`

	// InboundToken is the Bearer a sender must present to the webhook
	// channel's receive path, /in/hook/<id>.
	InboundToken string `json:"inbound_token,omitempty"`
	// ReplyMode is how an inbound turn's answer goes back: ReplySync or
	// ReplyCallback. Empty is sync.
	ReplyMode string `json:"reply_mode,omitempty"`

	// Telegram.
	BotToken     string  `json:"bot_token,omitempty"`
	AllowedChats []int64 `json:"allowed_chats,omitempty"`

	// Bark. Server is empty for the public https://api.day.app.
	Server    string `json:"server,omitempty"`
	DeviceKey string `json:"device_key,omitempty"`
}

// Wants reports whether the channel is sent this event.
func (c Channel) Wants(event string) bool { return slices.Contains(c.Events, event) }

// CanSend reports whether the channel has somewhere to send to.
func (c Channel) CanSend() bool {
	switch c.Type {
	case ChannelTelegram:
		return c.BotToken != "" && len(c.AllowedChats) > 0
	case ChannelBark:
		return c.DeviceKey != ""
	case ChannelWebhook, ChannelFeishu, ChannelWeCom, ChannelDingTalk, ChannelSlack:
		return c.URL != ""
	}
	return false
}

// TwoWay reports whether messages can also arrive through the channel.
func (c Channel) TwoWay() bool {
	return c.Type == ChannelWebhook || c.Type == ChannelTelegram
}

// InboundPath is the webhook channel's receive path, relative to the server.
func (c Channel) InboundPath() string {
	if c.Type != ChannelWebhook || c.ID == "" {
		return ""
	}
	return ChannelInboundPrefix + c.ID
}

// ChannelInboundPrefix is the receive route. Outside /api on purpose: it is
// not the app's API and carries its own credential, the channel's token.
const ChannelInboundPrefix = "/in/hook/"

// FindChannel returns the channel with the id.
func FindChannel(chs []Channel, id string) (Channel, bool) {
	for _, c := range chs {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

// EffectiveChannels is the channel list a settings value means: its channels
// when it has them, and otherwise what its legacy fields amount to. Pure: a
// settings value built in code (tests, a worker) is read the same way as one
// loaded from disk, with nothing written.
func EffectiveChannels(s *Settings) []Channel {
	if s == nil {
		return nil
	}
	if s.Channels != nil {
		return cloneChannels(s.Channels)
	}
	return reconcileChannels(nil, legacyOf(s), channelLegacy{})
}

// TelegramChannel is the channel the bridge polls: the first enabled
// Telegram channel with a token. One bot per process, because the poll is
// one lock (telegram.go); further Telegram channels only send.
func (s *Settings) TelegramChannel() (Channel, bool) {
	for _, c := range EffectiveChannels(s) {
		if c.Type == ChannelTelegram && c.Enabled && c.BotToken != "" {
			return c, true
		}
	}
	return Channel{}, false
}

// channelLegacy is what the old fields said.
type channelLegacy struct {
	WebhookURL           string  `json:"webhook_url,omitempty"`
	WebhookSecret        string  `json:"webhook_secret,omitempty"`
	TelegramBotToken     string  `json:"telegram_bot_token,omitempty"`
	TelegramAllowedChats []int64 `json:"telegram_allowed_chats,omitempty"`
}

func legacyOf(s *Settings) channelLegacy {
	return channelLegacy{
		WebhookURL:           strings.TrimSpace(s.WebhookURL),
		WebhookSecret:        strings.TrimSpace(s.WebhookSecret),
		TelegramBotToken:     strings.TrimSpace(s.TelegramBotToken),
		TelegramAllowedChats: s.TelegramAllowedChats,
	}
}

func (l channelLegacy) sameWebhook(o channelLegacy) bool {
	return l.WebhookURL == o.WebhookURL && l.WebhookSecret == o.WebhookSecret
}

func (l channelLegacy) sameTelegram(o channelLegacy) bool {
	return l.TelegramBotToken == o.TelegramBotToken && slices.Equal(l.TelegramAllowedChats, o.TelegramAllowedChats)
}

// primaryIndex is the channel the legacy fields mirror: the first of the type
// that has somewhere to send, else the first of the type.
func primaryIndex(chs []Channel, typ string) int {
	first := -1
	for i, c := range chs {
		if c.Type != typ {
			continue
		}
		if first < 0 {
			first = i
		}
		if (typ == ChannelWebhook && c.URL != "") || (typ == ChannelTelegram && c.BotToken != "") {
			return i
		}
	}
	return first
}

// mirrorLegacy is what the old fields should say for a channel list.
func mirrorLegacy(chs []Channel) channelLegacy {
	var l channelLegacy
	if i := primaryIndex(chs, ChannelWebhook); i >= 0 && chs[i].Enabled {
		l.WebhookURL, l.WebhookSecret = chs[i].URL, chs[i].Secret
	}
	if i := primaryIndex(chs, ChannelTelegram); i >= 0 && chs[i].Enabled {
		l.TelegramBotToken = chs[i].BotToken
		l.TelegramAllowedChats = slices.Clone(chs[i].AllowedChats)
	}
	return l
}

// reconcileChannels folds the legacy fields into the channel list.
//
// recorded is what the legacy fields said the last time they were mirrored
// from the channels. When they still say that, the channels are the truth.
// When they say something else, somebody wrote the old fields since — an
// older client, the agent's settings tool, a Secret in the cluster — and that
// is the later word, so it lands on the channel they mirror (created if there
// is none). With nothing recorded, this is the first migration.
func reconcileChannels(chs []Channel, legacy, recorded channelLegacy) []Channel {
	chs = cloneChannels(chs)
	if !legacy.sameWebhook(recorded) {
		if i := primaryIndex(chs, ChannelWebhook); i >= 0 {
			chs[i].URL, chs[i].Secret = legacy.WebhookURL, legacy.WebhookSecret
			if legacy.WebhookURL != "" {
				chs[i].Enabled = true
			}
		} else if legacy.WebhookURL != "" {
			chs = append(chs, Channel{
				ID: newChannelID(), Type: ChannelWebhook, Name: "Webhook", Enabled: true,
				// What the webhook was sent before channels: notify_user,
				// scheduled runs and their failures. Approvals are new and
				// are not switched on by an upgrade.
				Events: []string{ChannelEventReminder, ChannelEventSchedule, ChannelEventFailed},
				URL:    legacy.WebhookURL, Secret: legacy.WebhookSecret,
			})
		}
	}
	if !legacy.sameTelegram(recorded) {
		if i := primaryIndex(chs, ChannelTelegram); i >= 0 {
			chs[i].BotToken = legacy.TelegramBotToken
			chs[i].AllowedChats = slices.Clone(legacy.TelegramAllowedChats)
			chs[i].Enabled = legacy.TelegramBotToken != ""
		} else if legacy.TelegramBotToken != "" {
			chs = append(chs, Channel{
				ID: newChannelID(), Type: ChannelTelegram, Name: "Telegram", Enabled: true,
				// The bridge only ever answered; it was never sent notices,
				// and an upgrade does not start sending them.
				Events:   []string{},
				BotToken: legacy.TelegramBotToken, AllowedChats: slices.Clone(legacy.TelegramAllowedChats),
			})
		}
	}
	return normalizeChannels(chs)
}

// normalizeChannels trims, fills ids and inbound tokens, and drops events and
// types it does not know. Never nil, so "no channels" survives a round trip
// as [] and is not taken for "never migrated".
func normalizeChannels(chs []Channel) []Channel {
	out := make([]Channel, 0, len(chs))
	seen := map[string]bool{}
	for _, c := range chs {
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		if !slices.Contains(ChannelTypes, c.Type) {
			continue
		}
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" || seen[c.ID] {
			c.ID = newChannelID()
		}
		seen[c.ID] = true
		c.Name = strings.TrimSpace(c.Name)
		c.URL = strings.TrimSpace(c.URL)
		c.Secret = strings.TrimSpace(c.Secret)
		c.BotToken = strings.TrimSpace(c.BotToken)
		c.Server = strings.TrimRight(strings.TrimSpace(c.Server), "/")
		c.DeviceKey = strings.TrimSpace(c.DeviceKey)
		events := []string{}
		for _, e := range ChannelEvents {
			if slices.Contains(c.Events, e) {
				events = append(events, e)
			}
		}
		c.Events = events
		if c.Type == ChannelWebhook {
			if c.InboundToken == "" {
				c.InboundToken = NewChannelToken()
			}
			if c.ReplyMode != ReplyCallback {
				c.ReplyMode = ReplySync
			}
		} else {
			c.InboundToken, c.ReplyMode = "", ""
		}
		out = append(out, c)
	}
	return out
}

func cloneChannels(chs []Channel) []Channel {
	if chs == nil {
		return nil
	}
	out := make([]Channel, len(chs))
	for i, c := range chs {
		c.Events = slices.Clone(c.Events)
		c.AllowedChats = slices.Clone(c.AllowedChats)
		out[i] = c
	}
	return out
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newChannelID is short, lowercase and unguessable enough not to list: it is
// half of the inbound path, the token is the other half of the credential.
func newChannelID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return idEncoding.EncodeToString(b)
}

// NewChannelToken is a fresh inbound token: 32 random bytes.
func NewChannelToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewChannelID is newChannelID for the app's create path.
func NewChannelID() string { return newChannelID() }

// channels.json keeps the list beside settings.json, for the reason
// queenengine.json does: in the hive's containers settings.json is rewritten
// from a Secret on every start, and a channel added from the page would be
// gone after the next restart. Readable by this user only: it holds tokens.
type channelsFile struct {
	Channels []Channel     `json:"channels"`
	Legacy   channelLegacy `json:"legacy"`
}

func channelsPath() string { return filepath.Join(DataDir(), "channels.json") }

func readChannelsFile() (channelsFile, bool) {
	raw, err := os.ReadFile(channelsPath())
	if err != nil {
		return channelsFile{}, false
	}
	var f channelsFile
	if json.Unmarshal(raw, &f) != nil {
		return channelsFile{}, false
	}
	return f, true
}

// mergeChannels settles the channel list on load: channels.json if there is
// one, else what settings.json holds, with the legacy fields folded in.
func (s *Settings) mergeChannels() {
	var recorded channelLegacy
	if f, ok := readChannelsFile(); ok {
		s.Channels = f.Channels
		recorded = f.Legacy
	} else if s.Channels != nil {
		recorded = mirrorLegacy(s.Channels)
	}
	s.settleChannels(recorded)
}

// prepareChannels settles the list before a save. Channels nil means a client
// that does not know about them: the list on disk stands, and only the legacy
// fields it did send are folded in.
func (s *Settings) prepareChannels() {
	f, haveFile := readChannelsFile()
	if s.Channels == nil && haveFile {
		s.Channels = f.Channels
	}
	var recorded channelLegacy
	switch {
	case s.channelLegacy != nil:
		recorded = *s.channelLegacy
	case haveFile:
		recorded = f.Legacy
	case s.Channels != nil:
		recorded = mirrorLegacy(s.Channels)
	}
	s.settleChannels(recorded)
}

func (s *Settings) settleChannels(recorded channelLegacy) {
	s.Channels = reconcileChannels(s.Channels, legacyOf(s), recorded)
	l := mirrorLegacy(s.Channels)
	s.WebhookURL, s.WebhookSecret = l.WebhookURL, l.WebhookSecret
	s.TelegramBotToken, s.TelegramAllowedChats = l.TelegramBotToken, l.TelegramAllowedChats
	s.channelLegacy = &l
}

// saveChannels writes channels.json.
func (s *Settings) saveChannels() error {
	l := mirrorLegacy(s.Channels)
	raw, err := json.MarshalIndent(channelsFile{Channels: normalizeChannels(s.Channels), Legacy: l}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(channelsPath(), raw, 0o600)
}

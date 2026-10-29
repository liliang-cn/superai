package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func channelsOfType(chs []Channel, typ string) []Channel {
	var out []Channel
	for _, c := range chs {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

func writeSettingsFile(t *testing.T, home string, v map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChannelsMigrateFromLegacyFieldsOnLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	writeSettingsFile(t, home, map[string]any{
		"webhook_url": "https://hook.example.test/in", "webhook_secret": "s3cret",
		"telegram_bot_token": "123:abc", "telegram_allowed_chats": []int64{42},
	})
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	hooks := channelsOfType(s.Channels, ChannelWebhook)
	if len(hooks) != 1 || hooks[0].URL != "https://hook.example.test/in" || hooks[0].Secret != "s3cret" || !hooks[0].Enabled {
		t.Fatalf("webhook channel = %+v", hooks)
	}
	if !slices.Equal(hooks[0].Events, []string{ChannelEventReminder, ChannelEventSchedule, ChannelEventFailed}) {
		t.Errorf("migrated webhook events = %v; approvals must not be switched on by an upgrade", hooks[0].Events)
	}
	if hooks[0].InboundToken == "" || hooks[0].ReplyMode != ReplySync {
		t.Errorf("webhook channel has no inbound token or reply mode: %+v", hooks[0])
	}
	tg := channelsOfType(s.Channels, ChannelTelegram)
	if len(tg) != 1 || tg[0].BotToken != "123:abc" || !slices.Equal(tg[0].AllowedChats, []int64{42}) || !tg[0].Enabled {
		t.Fatalf("telegram channel = %+v", tg)
	}
	if len(tg[0].Events) != 0 {
		t.Errorf("migrated telegram events = %v; the bridge was never sent notices", tg[0].Events)
	}
	// The legacy fields still say what they said.
	if s.WebhookURL != "https://hook.example.test/in" || s.TelegramBotToken != "123:abc" {
		t.Errorf("legacy fields changed: %q %q", s.WebhookURL, s.TelegramBotToken)
	}

	// Saved and loaded again: the same channels, same ids, not migrated twice.
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Channels) != 2 || again.Channels[0].ID != s.Channels[0].ID || again.Channels[0].InboundToken != s.Channels[0].InboundToken {
		t.Fatalf("reloaded channels = %+v, want %+v", again.Channels, s.Channels)
	}
	if fi, err := os.Stat(filepath.Join(home, "channels.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("channels.json: %v %v", fi, err)
	}
}

func TestChannelsWinOverUnchangedLegacyFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	writeSettingsFile(t, home, map[string]any{"webhook_url": "https://a.test/hook"})
	s, _ := LoadSettings()
	s.Channels[0].URL = "https://b.test/hook" // edited on the channels page
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.WebhookURL != "https://b.test/hook" {
		t.Errorf("legacy field = %q, want the channel's URL mirrored", s.WebhookURL)
	}
	again, _ := LoadSettings()
	if again.Channels[0].URL != "https://b.test/hook" || again.WebhookURL != "https://b.test/hook" {
		t.Errorf("after reload: channel %q legacy %q", again.Channels[0].URL, again.WebhookURL)
	}
}

func TestLegacyWriteLandsOnThePrimaryChannel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	writeSettingsFile(t, home, map[string]any{"webhook_url": "https://a.test/hook"})
	s, _ := LoadSettings()
	s.Channels = append(s.Channels, Channel{Type: ChannelBark, DeviceKey: "k", Enabled: true})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	id := s.Channels[0].ID

	// The agent's settings tool: load, set webhook_url, save.
	cfg, _ := LoadSettings()
	cfg.WebhookURL = "https://c.test/hook"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadSettings()
	hooks := channelsOfType(got.Channels, ChannelWebhook)
	if len(hooks) != 1 || hooks[0].ID != id || hooks[0].URL != "https://c.test/hook" {
		t.Fatalf("webhook channels = %+v", hooks)
	}
	if len(channelsOfType(got.Channels, ChannelBark)) != 1 {
		t.Errorf("the bark channel was lost: %+v", got.Channels)
	}

	// An older client that sends no channels at all: the list on disk stands.
	old := *got
	old.Channels = nil
	old.channelLegacy = nil
	old.WebhookURL = "https://d.test/hook"
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadSettings()
	if len(got.Channels) != 2 || channelsOfType(got.Channels, ChannelWebhook)[0].URL != "https://d.test/hook" {
		t.Fatalf("after an old client's save: %+v", got.Channels)
	}
}

func TestLegacyEditInSettingsJSONWins(t *testing.T) {
	// The cluster: settings.json is rewritten from a Secret on every start,
	// channels.json survives. A changed webhook_url in the Secret is the
	// operator's later word.
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	writeSettingsFile(t, home, map[string]any{"webhook_url": "https://a.test/hook"})
	s, _ := LoadSettings()
	s.Channels = append(s.Channels, Channel{Type: ChannelSlack, URL: "https://hooks.slack.test/x", Enabled: true})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	writeSettingsFile(t, home, map[string]any{"webhook_url": "https://e.test/hook"})
	got, _ := LoadSettings()
	if len(got.Channels) != 2 || channelsOfType(got.Channels, ChannelWebhook)[0].URL != "https://e.test/hook" {
		t.Fatalf("channels = %+v", got.Channels)
	}
}

func TestDeletingTheLastWebhookClearsTheLegacyField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	writeSettingsFile(t, home, map[string]any{"webhook_url": "https://a.test/hook"})
	s, _ := LoadSettings()
	s.Channels = []Channel{}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadSettings()
	if len(got.Channels) != 0 || got.WebhookURL != "" {
		t.Fatalf("deleted channel came back: %+v %q", got.Channels, got.WebhookURL)
	}
}

func TestEffectiveChannelsOfBareSettings(t *testing.T) {
	chs := EffectiveChannels(&Settings{WebhookURL: "https://x.test"})
	if len(chs) != 1 || chs[0].Type != ChannelWebhook || !chs[0].CanSend() {
		t.Fatalf("channels = %+v", chs)
	}
	if got := EffectiveChannels(&Settings{}); len(got) != 0 {
		t.Fatalf("empty settings have channels: %+v", got)
	}
}

func TestApplyChannelInputPatchesAndValidates(t *testing.T) {
	str := func(s string) *string { return &s }
	chs, id, err := ApplyChannelInput(nil, ChannelInput{Type: "feishu", Name: str("家庭群"), URL: str("https://open.feishu.cn/open-apis/bot/v2/hook/abcdef1234567890"), Secret: str("sig")})
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 1 || chs[0].ID != id || !chs[0].Enabled || len(chs[0].Events) != 4 {
		t.Fatalf("created = %+v", chs)
	}
	// A save without the secret keeps it.
	chs, _, err = ApplyChannelInput(chs, ChannelInput{ID: id, Name: str("Family")})
	if err != nil || chs[0].Secret != "sig" || chs[0].Name != "Family" {
		t.Fatalf("patched = %+v, %v", chs, err)
	}
	// An empty secret clears it.
	chs, _, _ = ApplyChannelInput(chs, ChannelInput{ID: id, Secret: str("")})
	if chs[0].Secret != "" {
		t.Errorf("secret not cleared")
	}
	if _, _, err := ApplyChannelInput(chs, ChannelInput{ID: id, URL: str("ftp://x")}); err == nil {
		t.Error("ftp URL accepted")
	}
	if _, _, err := ApplyChannelInput(nil, ChannelInput{Type: "telegram", BotToken: str("1:a")}); err == nil {
		t.Error("telegram bot with no allowed chat accepted")
	}
	if _, _, err := ApplyChannelInput(nil, ChannelInput{Type: "carrier-pigeon"}); err == nil {
		t.Error("unknown type accepted")
	}
	// A bare WeCom key is accepted.
	if _, _, err := ApplyChannelInput(nil, ChannelInput{Type: "wecom", URL: str("a91f-key")}); err != nil {
		t.Error(err)
	}
	// A webhook gets a token and sync replies.
	chs, id, _ = ApplyChannelInput(nil, ChannelInput{Type: "webhook"})
	if c, _ := FindChannel(chs, id); c.InboundToken == "" || c.ReplyMode != ReplySync || c.InboundPath() != "/in/hook/"+id {
		t.Errorf("webhook = %+v", c)
	}
}

func TestViewMasksSecrets(t *testing.T) {
	c := Channel{
		ID: "abc", Type: ChannelWeCom, Enabled: true,
		URL:    "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=a91f0000-1111-2222-3333-444455556666",
		Secret: "supersecretvalue", InboundToken: "tok-0123456789", BotToken: "123456:ABCDEFGH", DeviceKey: "devkey123456",
	}
	v := ViewOf(c)
	raw, _ := json.Marshal(v)
	for _, secret := range []string{"a91f0000-1111", "supersecretvalue", "tok-0123456789", "123456:ABCDEFGH", "devkey123456"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("view leaks %q: %s", secret, raw)
		}
	}
	if v.URL != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=a91f…" {
		t.Errorf("masked url = %q", v.URL)
	}
	if got := maskURL("https://open.feishu.cn/open-apis/bot/v2/hook/f8k2abcdefabcdef"); got != "https://open.feishu.cn/open-apis/bot/v2/hook/f8k2…" {
		t.Errorf("feishu mask = %q", got)
	}
	if v.SecretMask != "••••alue" || !v.SecretSet {
		t.Errorf("secret mask = %q", v.SecretMask)
	}
}

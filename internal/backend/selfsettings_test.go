package backend

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fillSecrets sets every string the settings can hold, at any depth, to a
// value that must never be shown: a marker in a secret field, and in every
// other string an address carrying a password, a query key and a path key.
// It returns the markers that must not appear.
func fillSecrets(v reflect.Value, name string, n *int, forbid *[]string) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fillSecrets(v.Elem(), name, n, forbid)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			fillSecrets(v.Field(i), tag, n, forbid)
		}
	case reflect.Slice:
		el := reflect.New(v.Type().Elem()).Elem()
		fillSecrets(el, name, n, forbid)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), el))
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		el := reflect.New(v.Type().Elem()).Elem()
		elName := name
		if secretMapNames[name] {
			elName = "token"
		}
		fillSecrets(el, elName, n, forbid)
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), el)
		v.Set(m)
	case reflect.String:
		*n++
		if IsSecretName(name) {
			mark := fmt.Sprintf("SEKRIT%03dX", *n)
			*forbid = append(*forbid, mark)
			v.SetString(mark)
			return
		}
		pass, q, p := fmt.Sprintf("PASSWD%03dX", *n), fmt.Sprintf("QUERYK%03dX", *n), fmt.Sprintf("PATHK%03dX", *n)
		*forbid = append(*forbid, pass, q)
		if hostOnlyNames[name] {
			*forbid = append(*forbid, p)
		}
		v.SetString("https://user:" + pass + "@example.org/hook/" + p + "?access_token=" + q)
	}
}

// Every secret any field can hold is absent from what a model reads:
// secret fields, passwords in addresses, query strings, bot webhook paths.
func TestRedactedSettingsShowNoSecretAnywhere(t *testing.T) {
	var cfg Settings
	n := 0
	var forbid []string
	fillSecrets(reflect.ValueOf(&cfg).Elem(), "", &n, &forbid)
	if len(forbid) < 40 {
		t.Fatalf("filled only %d values; the walk missed the settings", len(forbid))
	}
	b, _ := json.Marshal(RedactedSettings(&cfg))
	out := string(b)
	for _, f := range forbid {
		if strings.Contains(out, f) {
			t.Errorf("%s is shown: %s", f, selfSnippet(out, f))
		}
	}
	for _, sec := range SelfKeySections() {
		b, _ := json.Marshal(SettingsSection(&cfg, sec))
		for _, f := range forbid {
			if strings.Contains(string(b), f) {
				t.Errorf("section %s shows %s", sec, f)
			}
		}
	}
	// And the named secrets read as set.
	red := RedactedSettings(&cfg)
	for _, k := range []string{"llm_key", "embed_key", "tts_key", "search_key", "shared_memory_token", "webhook_secret", "telegram_bot_token"} {
		if red[k] != SecretSet {
			t.Errorf("%s reads %v, want %q", k, red[k], SecretSet)
		}
	}
	if (Settings{}).LLMKey == "" && RedactedSettings(&Settings{})["llm_key"] != SecretNotSet {
		t.Error("an empty secret should read not set")
	}
}

func selfSnippet(s, at string) string {
	i := strings.Index(s, at)
	lo, hi := max(0, i-60), min(len(s), i+len(at)+20)
	return s[lo:hi]
}

// The secrets settings_get must never show, by name, so a rule that stops
// recognising one fails here and not in a reply.
func TestTheKnownSecretsAreSecrets(t *testing.T) {
	for _, name := range []string{"llm_key", "embed_key", "tts_key", "search_key", "shared_memory_token", "webhook_secret",
		"telegram_bot_token", "join_token", "peer_token", "token", "secret", "inbound_token", "bot_token", "device_key", "joinToken"} {
		if !IsSecretName(name) {
			t.Errorf("%s is not treated as a secret", name)
		}
	}
	for _, name := range []string{"llm_context_tokens", "turn_max_tokens", "long_task_max_tokens", "llm_max_output_tokens", "key_path", "key_id", "llm_model", "proxy"} {
		if IsSecretName(name) {
			t.Errorf("%s is treated as a secret", name)
		}
	}
	for in, want := range map[string]string{
		"http://127.0.0.1:7890":                       "http://127.0.0.1:7890",
		"127.0.0.1:7890":                              "127.0.0.1:7890",
		"http://u:p4ss@proxy.lan:3128":                "http://proxy.lan:3128",
		"u:p4ss@proxy.lan:3128":                       "…@proxy.lan:3128",
		"https://hooks.slack.com/services/T0/B0/XXXX": "https://hooks.slack.com/…",
		"693a91f6-7xxx-4bc4-97a0-0ec2sifa5aaa":        SecretSet,
	} {
		if got := RedactValue("url", in); got != want {
			t.Errorf("RedactValue(url, %q) = %q, want %q", in, got, want)
		}
	}
}

// A writable key holds no secret anywhere beneath it: making a container
// writable must not make a secret inside it writable.
func TestNoWritableKeyReachesASecret(t *testing.T) {
	for _, k := range selfKeys() {
		if k.Tier != TierWritable || k.Virtual {
			continue
		}
		typ, err := settingsPathType(strings.ReplaceAll(k.Key, "*", "x"))
		if err != nil {
			t.Errorf("%s: %v", k.Key, err)
			continue
		}
		if IsSecretName(lastSeg(k.Key)) {
			t.Errorf("%s is writable and is a secret", k.Key)
		}
		if p := secretBeneath(typ, ""); p != "" {
			t.Errorf("%s is writable and holds the secret %s", k.Key, p)
		}
	}
}

func secretBeneath(t reflect.Type, at string) string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if !f.IsExported() || name == "-" {
			continue
		}
		if IsSecretName(name) || secretMapNames[name] {
			return at + name
		}
		if p := secretBeneath(f.Type, at+name+"."); p != "" {
			return p
		}
	}
	return ""
}

// The boundary keys are where the user put them: credentials, addresses,
// and anything that widens what the agent may do.
func TestBoundaryKeysNeedApproval(t *testing.T) {
	for _, key := range []string{
		"disable_tool_approval", "desktop_control", "desktop_blocked_apps", "external_agents.unattended",
		"llm_key", "embed_key", "shared_memory_token", "webhook_secret", "telegram_bot_token", "llm_base_url",
		"shared_memory_endpoint", "hive.role", "hive.join_url", "hive.join_token", "local_hive", "hive_link.active",
		"channels", "channels.c1.bot_token", "channels.c1.url", "remote_agents.agents", "external_agents.roots",
		"external_agents.binaries", "disable_self_install", "telegram_allowed_chats", "hive", "external_agents",
		"ha.haify.token", "apns.key_path",
	} {
		if tier := SelfKeyTier(key, true); tier != TierNeedsApproval {
			if key == "disable_self_install" { // true narrows
				continue
			}
			t.Errorf("%s is %s", key, tier)
		}
	}
	// Narrowing needs nobody's yes.
	for key, v := range map[string]any{"disable_tool_approval": false, "desktop_control": false, "external_agents.unattended": "false", "disable_self_install": true} {
		if SelfKeyTier(key, v) != TierWritable {
			t.Errorf("%s=%v narrows and should apply at once", key, v)
		}
	}
	for _, key := range []string{"proxy.url", "proxy", "browser_mode", "llm_model", "hive.queen_engines", "max_rounds", "workspace_dir",
		"memory_backend", "embed_model", "external_agents.enabled", "remote_agents.enabled", "channels.c1.enabled", "webhook_url"} {
		if SelfKeyTier(key, "x") != TierWritable {
			t.Errorf("%s should be writable", key)
		}
	}
}

// Every key in the table that is not virtual is a settings path. The coding
// tools' custom list is allowed to be missing until it lands.
func TestEveryTableKeyIsASettingsPath(t *testing.T) {
	for _, k := range selfKeys() {
		if k.Virtual {
			continue
		}
		if !IsSettingsPath(strings.ReplaceAll(k.Key, "*", "x")) && k.Key != "external_agents.custom" {
			t.Errorf("%s is not a settings path", k.Key)
		}
	}
}

func TestSetSettingKey(t *testing.T) {
	cfg := Settings{
		LLMKey:    "sk-keep",
		MaxRounds: 30,
		Proxy:     ProxySettings{Mode: ProxyModeManual, URL: "http://u:pw@127.0.0.1:7890"},
		Channels: []Channel{{ID: "c1", Type: ChannelTelegram, BotToken: "tg-keep", AllowedChats: []int64{1}, Events: []string{"reminder"}},
			{ID: "c2", Type: ChannelFeishu, URL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdef", Secret: "fs-keep"}},
	}
	must := func(key string, v any) {
		t.Helper()
		if _, err := SetSettingKey(&cfg, key, v); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	must("max_rounds", "40")
	must("proxy.mode", "system")
	must("channels.c1.enabled", true)
	must("external_agents.models.claude", "opus")
	must("headless", "on")
	if cfg.MaxRounds != 40 || cfg.Proxy.Mode != "system" || cfg.Proxy.URL != "http://u:pw@127.0.0.1:7890" || !cfg.Channels[0].Enabled ||
		cfg.ExternalAgents.Models["claude"] != "opus" || !cfg.Headless || cfg.LLMKey != "sk-keep" {
		t.Fatalf("settings after the changes: %+v", cfg)
	}
	// The proxy URL sent back as it was shown keeps the password.
	must("proxy.url", RedactValue("url", cfg.Proxy.URL))
	if cfg.Proxy.URL != "http://u:pw@127.0.0.1:7890" {
		t.Fatalf("the shown proxy replaced the real one: %q", cfg.Proxy.URL)
	}
	// The channel list sent back as settings_get showed it keeps every secret.
	var shown []any
	b, _ := json.Marshal(RedactedSettings(&cfg)["channels"])
	_ = json.Unmarshal(b, &shown)
	shown[0].(map[string]any)["name"] = "phone"
	must("channels", shown)
	if c := cfg.Channels; c[0].BotToken != "tg-keep" || c[0].Name != "phone" || c[1].Secret != "fs-keep" ||
		c[1].URL != "https://open.feishu.cn/open-apis/bot/v2/hook/abcdef" {
		t.Fatalf("the channels lost a secret: %+v", c)
	}
	// An exact secret key with "" clears it.
	must("llm_key", "")
	if cfg.LLMKey != "" {
		t.Fatal("llm_key was not cleared")
	}
	must("hive.queen_engines", []any{map[string]any{"engine": "claude", "enabled": true}, map[string]any{"engine": "superleo", "enabled": true}})
	if cfg.Hive.QueenEngine != "claude" || cfg.Hive.QueenFallback == nil || !*cfg.Hive.QueenFallback {
		t.Fatalf("queen engines out of step: %+v", cfg.Hive)
	}

	for key, v := range map[string]any{
		"no_such_key": 1, "max_rounds": "lots", "proxy.nope": "x", "channels.zz.enabled": true,
		"headless": "perhaps", "channels": []any{map[string]any{"id": "c1", "typo_field": 1}}, "llm_model": []any{"a"},
	} {
		c := cfg
		if _, err := SetSettingKey(&c, key, v); err == nil {
			t.Errorf("%s=%v was accepted", key, v)
		}
	}
	c := cfg
	_, _ = SetSettingKey(&c, "max_rounds", 0)
	if CheckSettingKeys(&c, []string{"max_rounds"}) == nil {
		t.Error("max_rounds 0 passed its check")
	}
	_, _ = SetSettingKey(&c, "browser_mode", "sometimes")
	if CheckSettingKeys(&c, []string{"browser_mode"}) == nil {
		t.Error("browser_mode sometimes passed its check")
	}
}

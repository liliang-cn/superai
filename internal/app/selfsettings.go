package app

// settings_get and settings_set: every setting and every piece of state the
// Settings page shows, readable, and every setting changeable, in the two
// tiers backend/selfsettings.go defines.
//
// A writable key is validated and applied at once (or, under a running turn,
// as soon as no turn is running, since applying rebuilds the agent). A key
// that needs approval is put to the person on an approval card, asked
// whatever the gate, YOLO or a remembered rule would say, and changes nothing
// until they say yes. The call returns at once with the request's id; the
// outcome is in settings_get {"section":"requests"}.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/superleo/internal/backend"
)

// selfApprovalWait is how long a boundary change waits for the person. Longer
// than a tool call's: nothing is blocked on it, and the person may be away.
var selfApprovalWait = 15 * time.Minute

// selfIdleWait is how long a change waits for running turns to finish.
const selfIdleWait = 10 * time.Minute

// selfChange is one key and the value asked for.
type selfChange struct {
	Key   string
	Value any
}

// --- reading ---

// selfSettingsSections are settings_get's sections, in order.
func selfSettingsSections() []string {
	out := append([]string{}, backend.SelfKeySections()...)
	return append(out, "devices", "settings", "keys", "requests")
}

// selfSettingsGet is settings_get: the whole picture, one section, one key
// or one request.
func (a *App) selfSettingsGet(args map[string]any) (any, error) {
	if id := strings.TrimSpace(str(args["request"])); id != "" {
		r, ok := a.selfRequest(id)
		if !ok {
			return nil, fmt.Errorf("no settings request %q (requests are kept until the app restarts)", id)
		}
		return r, nil
	}
	if key := strings.TrimSpace(str(args["key"])); key != "" {
		return a.selfSettingsKey(key)
	}
	if sec := strings.TrimSpace(str(args["section"])); sec != "" {
		for _, s := range selfSettingsSections() {
			if s == sec {
				return a.selfSettingsSection(sec), nil
			}
		}
		return nil, fmt.Errorf("no section %q; sections are %s", sec, strings.Join(selfSettingsSections(), ", "))
	}
	return a.selfSettings(), nil
}

// selfSettings is the whole picture: the flat fields settings_get always
// had, every section's settings and state, the changes waiting and the
// recent requests.
func (a *App) selfSettings() map[string]any {
	cfg := a.GetSettings()
	out := backend.SettingsSnapshot(&cfg)
	writable, approval := map[string]string{}, map[string]string{}
	for _, k := range backend.SelfKeys() {
		if k.Tier == backend.TierWritable {
			writable[k.Key] = k.Desc
		} else {
			approval[k.Key] = k.Desc
		}
	}
	out["writable_from_this_tool"] = writable
	out["needs_approval"] = approval
	state := map[string]any{}
	for _, sec := range selfSettingsSections() {
		if sec == "settings" || sec == "keys" || sec == "requests" {
			continue
		}
		state[sec] = a.selfSettingsSection(sec)
	}
	out["sections"] = state
	if p := a.pendingSettings(); len(p) > 0 {
		out["pending"] = p
	}
	if rs := a.selfRequests(); len(rs) > 0 {
		out["requests"] = rs
	}
	if os.Getenv("SUPERLEO_SETTINGS_JSON") != "" {
		out["note"] = "This instance takes its settings from its deployment: a change here lasts until it restarts."
	}
	return scrubOutput(out).(map[string]any)
}

// selfSettingsKey is one key: its value as it may be shown, tier and
// description.
func (a *App) selfSettingsKey(key string) (any, error) {
	k, known := backend.LookupSelfKey(key)
	if !k.Virtual && !backend.IsSettingsPath(key) {
		return nil, fmt.Errorf("unknown setting %q; settings_get {\"section\":\"keys\"} lists them", key)
	}
	out := map[string]any{"key": key, "tier": backend.SelfKeyTier(key, nil)}
	if known {
		out["description"], out["section"] = k.Desc, k.Section
	}
	if k.Virtual {
		out["value"] = a.selfSettingsSection(k.Section)
		return scrubOutput(out), nil
	}
	cfg := a.GetSettings()
	var cur any = backend.RedactedSettings(&cfg)
	for _, seg := range strings.Split(key, ".") {
		switch t := cur.(type) {
		case map[string]any:
			cur = t[seg]
		case []any:
			var found any
			for _, it := range t {
				if m, ok := it.(map[string]any); ok && (m["id"] == seg || m["engine"] == seg) {
					found = m
				}
			}
			cur = found
		default:
			cur = nil
		}
	}
	out["value"] = cur
	return scrubOutput(out), nil
}

// selfSettingsSection is one section: its settings, redacted, and what the
// Settings page shows about it now.
func (a *App) selfSettingsSection(sec string) any {
	cfg := a.GetSettings()
	switch sec {
	case "settings":
		return backend.RedactedSettings(&cfg)
	case "keys":
		rows := []map[string]any{}
		for _, k := range backend.SelfKeys() {
			rows = append(rows, map[string]any{"key": k.Key, "section": k.Section, "tier": k.Tier, "description": k.Desc})
		}
		return map[string]any{"keys": rows, "other": "Any other settings path (see section settings) needs approval."}
	case "requests":
		return map[string]any{"requests": a.selfRequests()}
	case "devices":
		devs, err := a.PairedDevices()
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return scrubOutput(map[string]any{"devices": devs})
	}
	out := backend.SettingsSection(&cfg, sec)
	live := a.selfSectionState(sec, &cfg)
	if len(live) > 0 {
		out["state"] = live
	}
	return scrubOutput(out)
}

// selfSectionState is the live half of a section: what the Settings page
// shows beside the fields.
func (a *App) selfSectionState(sec string, cfg *backend.Settings) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	switch sec {
	case "model":
		source := "custom_api"
		if cfg.CLIProxyEnabled {
			source = "accounts"
		}
		accounts := []map[string]any{}
		for _, ac := range a.CLIProxyAccounts() {
			accounts = append(accounts, map[string]any{
				"provider": ac.Provider, "account": ac.Account, "project": ac.Project,
				"expires": ac.Expires, "enabled": !ac.Disabled, "file": ac.File,
			})
		}
		out := map[string]any{"source": source, "accounts": accounts, "ready": svc != nil}
		a.mu.Lock()
		p, perr := a.proxy, a.proxyErr
		a.mu.Unlock()
		if p != nil || perr != "" {
			out["accounts_proxy"] = map[string]any{"running": p != nil, "error": perr}
		}
		return out
	case "queen":
		return map[string]any{"engines": a.QueenEngineList()}
	case "proxy":
		st := backend.CurrentProxyState()
		return map[string]any{"mode": st.Mode, "using": st.Using, "system": st.System, "error": st.Error,
			"note": "using is the proxy an outside request goes through now; empty is direct."}
	case "browser":
		if svc == nil {
			return map[string]any{"ready": false}
		}
		st := a.BrowserStatus()
		return map[string]any{"enabled": st.Enabled, "ready": st.Ready, "mode": st.Mode, "connected": st.Connected, "version": st.Version, "error": st.Error}
	case "desktop":
		st := a.DesktopStatus()
		return map[string]any{"enabled": st.Enabled, "ready": st.Ready, "accessibility": st.Accessibility,
			"tools": st.Tools, "left_alone": st.Blocked, "left_alone_is_default": st.BlockedIsDefault, "error": st.Error}
	case "memory":
		return map[string]any{
			"using_shared":           cfg.UseSharedMemory(),
			"shared_endpoint_in_use": cfg.SharedMemoryEndpointResolved(),
			"shared_token":           secretState(cfg.SharedMemoryTokenResolved()),
			"embeddings_on":          cfg.UseEmbeddings(),
		}
	case "coding_tools":
		return map[string]any{"tools": a.ListCodingTools()}
	case "remote_agents":
		return map[string]any{"linked_agents": len(a.LinkedAgents())}
	case "hive":
		hs := a.HiveStatus()
		out := map[string]any{"role": hs["role"], "name": hs["name"], "node": hs["node"], "spawner": hs["spawner"], "queen": hs["queen"]}
		if m, ok := hs["members"].([]backend.HiveMember); ok {
			out["members"] = len(m)
		}
		out["local_hive"] = a.GetLocalHive()
		out["link"] = a.HiveLinkStatus()
		return out
	case "notifications":
		return map[string]any{"channels": a.ListChannels()}
	case "approval":
		info := a.ToolApprovalInfo(0)
		out := map[string]any{"enabled": info["enabled"], "yolo": info["yolo"], "wait_seconds": info["waitSeconds"]}
		out["waiting"] = len(a.PendingToolApprovals())
		return out
	}
	return nil
}

func secretState(v string) string {
	if strings.TrimSpace(v) == "" {
		return backend.SecretNotSet
	}
	return backend.SecretSet
}

func toJSONAny(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// userinfoRe is the user:password of an address inside free text.
var userinfoRe = regexp.MustCompile(`(://)[^/@\s"]+@`)

// scrubOutput is the last pass over anything settings_get returns: values
// through the same redaction as the settings (secret names, addresses), and
// no user:password left in any text.
func scrubOutput(v any) any {
	return scrubStrings(backend.RedactTree("", toJSONAny(v)))
}

func scrubStrings(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = scrubStrings(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = scrubStrings(val)
		}
		return t
	case string:
		return userinfoRe.ReplaceAllString(t, "$1")
	}
	return v
}

// --- writing ---

// selfSetArgs reads settings_set's arguments: {key, value} as it always
// was, or {changes: {key: value, ...}}, or both.
func selfSetArgs(args map[string]any) ([]selfChange, error) {
	var out []selfChange
	if ch, ok := args["changes"]; ok && ch != nil {
		m, ok := ch.(map[string]any)
		if !ok {
			return nil, errors.New("changes must be an object of key: value")
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, selfChange{Key: strings.TrimSpace(k), Value: m[k]})
		}
	}
	if key := strings.TrimSpace(str(args["key"])); key != "" {
		if _, ok := args["value"]; !ok {
			return nil, fmt.Errorf("no value given for %s (null is a value: it clears the key)", key)
		}
		out = append(out, selfChange{Key: key, Value: args["value"]})
	}
	if len(out) == 0 {
		return nil, errors.New("nothing to change: give key and value, or changes")
	}
	seen := map[string]bool{}
	for _, c := range out {
		if c.Key == "" {
			return nil, errors.New("a change has no key")
		}
		if seen[c.Key] {
			return nil, fmt.Errorf("%s is given twice", c.Key)
		}
		seen[c.Key] = true
	}
	return out, nil
}

// selfSetSettings is settings_set. by is who the approval card says is
// asking.
func (a *App) selfSetSettings(by string, args map[string]any) (any, error) {
	changes, err := selfSetArgs(args)
	if err != nil {
		return nil, err
	}
	var now, ask []selfChange
	for _, c := range changes {
		if !backend.IsVirtualSelfKey(c.Key) && !backend.IsSettingsPath(c.Key) {
			return nil, fmt.Errorf("unknown setting %q; settings_get {\"section\":\"keys\"} lists them", c.Key)
		}
		if backend.SelfKeyTier(c.Key, c.Value) == backend.TierWritable {
			now = append(now, c)
		} else {
			ask = append(ask, c)
		}
	}
	// Every value is checked now, against the settings as they are, so a bad
	// one is this call's answer and not a failure after the person said yes.
	if err := a.checkSelfChanges(changes); err != nil {
		return nil, scrubErr(err, changes)
	}

	out := map[string]any{}
	if len(now) > 0 {
		applied, deferred, err := a.applySelfNow(now)
		if err != nil {
			return nil, scrubErr(err, changes)
		}
		if deferred {
			out["accepted"] = shownChanges(now)
			out["note"] = "Accepted; saved and applied as soon as no conversation is mid-turn, usually right after this reply."
		} else {
			out["applied"] = applied
		}
	}
	if len(ask) > 0 {
		r := a.askSelfChanges(by, ask)
		out["needs_approval"] = map[string]any{
			"id": r.ID, "status": r.Status, "keys": r.Keys, "values": r.Values,
			"note": "Waiting for the person to approve on the approval card. Nothing has changed yet. settings_get {\"request\":\"" + r.ID + "\"} shows the outcome.",
		}
	}
	// The single-key form answered with key and value; it still does.
	if len(changes) == 1 && len(now) == 1 {
		out["key"] = changes[0].Key
		out["value"] = shownValue(changes[0].Key, changes[0].Value)
	}
	if os.Getenv("SUPERLEO_SETTINGS_JSON") != "" {
		out["deployment"] = "This instance takes its settings from its deployment, so a change lasts until it restarts."
	}
	return out, nil
}

// checkSelfChanges applies every change to a copy of the settings and runs
// the checks the keys have, and the ones a save would.
func (a *App) checkSelfChanges(changes []selfChange) error {
	cfg := a.GetSettings()
	prevEngine := cfg.Hive.QueenEngine
	var keys []string
	queen := false
	for _, c := range changes {
		if backend.IsVirtualSelfKey(c.Key) {
			if err := checkVirtual(c); err != nil {
				return err
			}
			continue
		}
		if _, err := backend.SetSettingKey(&cfg, c.Key, c.Value); err != nil {
			return err
		}
		keys = append(keys, c.Key)
		queen = queen || strings.HasPrefix(c.Key, "hive.queen") || c.Key == "hive"
	}
	if err := backend.CheckSettingKeys(&cfg, keys); err != nil {
		return err
	}
	if err := cfg.Proxy.Validate(); err != nil {
		return err
	}
	if err := backend.ValidateQueenName(cfg.QueenName, cfg.QueenAliases); err != nil {
		return err
	}
	if queen {
		return a.validQueenEngine(&cfg, prevEngine)
	}
	return nil
}

// applySelfNow applies writable changes: at once when no turn is running,
// else as soon as none is (deferred true).
func (a *App) applySelfNow(changes []selfChange) (map[string]any, bool, error) {
	if a.turnsRunning() == 0 {
		if err := a.applySelfChanges(changes); err != nil {
			return nil, false, err
		}
		return shownChanges(changes), false, nil
	}
	shown := shownChanges(changes)
	a.selfPendingMu.Lock()
	if a.selfPending == nil {
		a.selfPending = map[string]any{}
	}
	for k, v := range shown {
		a.selfPending[k] = v
	}
	a.selfPendingMu.Unlock()
	go func() {
		defer a.clearPending(shown)
		if !a.waitIdle() {
			return
		}
		_ = a.applySelfChanges(changes)
	}()
	return nil, true, nil
}

func (a *App) clearPending(shown map[string]any) {
	a.selfPendingMu.Lock()
	defer a.selfPendingMu.Unlock()
	for k, v := range shown {
		if cur, ok := a.selfPending[k]; ok && fmt.Sprint(cur) == fmt.Sprint(v) {
			delete(a.selfPending, k)
		}
	}
}

// waitIdle waits until no turn is running, for up to selfIdleWait.
func (a *App) waitIdle() bool {
	deadline := time.Now().Add(selfIdleWait)
	for a.turnsRunning() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
	return true
}

// applySelfChanges applies changes on top of the settings as they are now
// and saves them the way the Settings page does (SaveSettings rebuilds what
// they affect); nothing not named is touched. Virtual keys are acted on
// after.
func (a *App) applySelfChanges(changes []selfChange) error {
	selfSettingsMu.Lock()
	defer selfSettingsMu.Unlock()
	// The settings page's patches take the same lock: one read-change-save
	// at a time, so neither writes back the other's stale copy.
	settingsPatchMu.Lock()
	defer settingsPatchMu.Unlock()
	cfg := a.GetSettings()
	var keys []string
	var virtual []selfChange
	for _, c := range changes {
		if backend.IsVirtualSelfKey(c.Key) {
			virtual = append(virtual, c)
			continue
		}
		if _, err := backend.SetSettingKey(&cfg, c.Key, c.Value); err != nil {
			return err
		}
		keys = append(keys, c.Key)
	}
	if len(keys) > 0 {
		if err := backend.CheckSettingKeys(&cfg, keys); err != nil {
			return err
		}
		if err := a.SaveSettings(cfg); err != nil {
			return err
		}
	}
	for _, c := range virtual {
		if err := a.applyVirtual(c); err != nil {
			return err
		}
	}
	return nil
}

// --- virtual keys ---

type localHiveValue struct {
	Enabled    bool `json:"enabled"`
	MinWorkers int  `json:"min_workers"`
	MaxWorkers int  `json:"max_workers"`
}

func parseLocalHive(raw any) (localHiveValue, error) {
	var v localHiveValue
	if b, ok := raw.(bool); ok {
		v.Enabled = b
		return v, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return v, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, errors.New(`local_hive: want {"enabled": true|false, "min_workers": n, "max_workers": n}`)
	}
	if v.MinWorkers < 0 || v.MaxWorkers < 0 {
		return v, errors.New("local_hive: worker counts cannot be negative")
	}
	return v, nil
}

func checkVirtual(c selfChange) error {
	switch c.Key {
	case "local_hive":
		_, err := parseLocalHive(c.Value)
		return err
	}
	return fmt.Errorf("unknown setting %q", c.Key)
}

func (a *App) applyVirtual(c selfChange) error {
	switch c.Key {
	case "local_hive":
		v, err := parseLocalHive(c.Value)
		if err != nil {
			return err
		}
		_, err = a.SetLocalHive(v.Enabled, v.MinWorkers, v.MaxWorkers)
		return err
	}
	return fmt.Errorf("unknown setting %q", c.Key)
}

// --- what a change is shown as ---

// shownValue is a value as a reply or a card may show it: a secret as
// "(a new secret, not shown)", addresses without what they carry.
func shownValue(key string, v any) any {
	if keyIsSecret(key) {
		if s, ok := v.(string); v == nil || (ok && strings.TrimSpace(s) == "") {
			return "(cleared)"
		}
		return "(a new secret, not shown)"
	}
	return scrubStrings(backend.RedactTree(lastKeySeg(key), toJSONAny(v)))
}

func shownChanges(changes []selfChange) map[string]any {
	out := map[string]any{}
	for _, c := range changes {
		out[c.Key] = shownValue(c.Key, c.Value)
	}
	return out
}

// keyIsSecret: any part of the key names a secret, or a map of them (env).
func keyIsSecret(key string) bool {
	for _, seg := range strings.Split(key, ".") {
		if backend.IsSecretName(seg) || seg == "env" || seg == "headers" {
			return true
		}
	}
	return false
}

func lastKeySeg(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return key[i+1:]
	}
	return key
}

// scrubErr takes out of an error every value given that could be a secret
// or carry one, so a refusal never repeats it.
func scrubErr(err error, changes []selfChange) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	var hide []string
	for _, c := range changes {
		sensitive := keyIsSecret(c.Key) || backend.SelfKeyTier(c.Key, c.Value) == backend.TierNeedsApproval
		collectSensitive(lastKeySeg(c.Key), c.Value, sensitive, &hide)
	}
	sort.Slice(hide, func(i, j int) bool { return len(hide[i]) > len(hide[j]) })
	for _, h := range hide {
		msg = strings.ReplaceAll(msg, h, "(hidden)")
	}
	return errors.New(userinfoRe.ReplaceAllString(msg, "$1"))
}

func collectSensitive(name string, v any, sensitive bool, out *[]string) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if len(s) < 4 {
			return
		}
		if sensitive || backend.IsSecretName(name) || strings.Contains(s, "://") || strings.Contains(s, "@") {
			*out = append(*out, s)
			if i := strings.Index(s, "://"); i >= 0 {
				*out = append(*out, s[i+3:])
			}
		}
	case map[string]any:
		for k, val := range t {
			collectSensitive(k, val, sensitive || backend.IsSecretName(k) || k == "env", out)
		}
	case []any:
		for _, val := range t {
			collectSensitive(name, val, sensitive, out)
		}
	}
}

// --- approval requests ---

// SelfSettingsRequest is a boundary change put to the person.
type SelfSettingsRequest struct {
	ID string `json:"id"`
	// Keys and Values are what was asked for, values as they may be shown.
	Keys   []string       `json:"keys"`
	Values map[string]any `json:"values"`
	By     string         `json:"by,omitempty"`
	// Status: "waiting for approval", "approved, applying", "applied",
	// "denied", "expired" or "failed".
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	AskedAt   time.Time `json:"asked_at"`
	DecidedAt time.Time `json:"decided_at,omitzero"`

	changes []selfChange // never marshalled: may hold a secret
}

const (
	reqWaiting  = "waiting for approval"
	reqApplying = "approved, applying"
	reqApplied  = "applied"
	reqDenied   = "denied"
	reqExpired  = "expired"
	reqFailed   = "failed"
)

// selfReqStore keeps each app's recent requests. Package-level, keyed by
// app, so the App struct (another file's) needs no field.
var selfReqStore = struct {
	mu   sync.Mutex
	byID map[*App][]*SelfSettingsRequest
}{byID: map[*App][]*SelfSettingsRequest{}}

const selfReqKeep = 50

func (a *App) selfRequests() []SelfSettingsRequest {
	selfReqStore.mu.Lock()
	defer selfReqStore.mu.Unlock()
	rs := selfReqStore.byID[a]
	out := make([]SelfSettingsRequest, 0, len(rs))
	for i := len(rs) - 1; i >= 0; i-- {
		r := *rs[i]
		r.changes = nil
		out = append(out, r)
	}
	return out
}

func (a *App) selfRequest(id string) (SelfSettingsRequest, bool) {
	for _, r := range a.selfRequests() {
		if r.ID == id {
			return r, true
		}
	}
	return SelfSettingsRequest{}, false
}

func (a *App) updateSelfRequest(id string, f func(r *SelfSettingsRequest)) {
	selfReqStore.mu.Lock()
	defer selfReqStore.mu.Unlock()
	for _, r := range selfReqStore.byID[a] {
		if r.ID == id {
			f(r)
			if r.Status != reqWaiting && r.Status != reqApplying {
				r.changes = nil // done with the values, secrets included
			}
			return
		}
	}
}

// askSelfChanges puts changes to the person on one approval card and returns
// at once; a goroutine waits for the answer and applies them on a yes.
func (a *App) askSelfChanges(by string, changes []selfChange) SelfSettingsRequest {
	now := time.Now()
	r := &SelfSettingsRequest{
		ID: uuid.NewString(), Values: shownChanges(changes), By: by,
		Status: reqWaiting, AskedAt: now, changes: changes,
	}
	for _, c := range changes {
		r.Keys = append(r.Keys, c.Key)
	}
	selfReqStore.mu.Lock()
	list := append(selfReqStore.byID[a], r)
	if len(list) > selfReqKeep {
		list = list[len(list)-selfReqKeep:]
	}
	selfReqStore.byID[a] = list
	shown := *r
	shown.changes = nil
	selfReqStore.mu.Unlock()

	go a.awaitSelfApproval(r.ID, by, changes, r.Values, now)
	return shown
}

func (a *App) awaitSelfApproval(id, by string, changes []selfChange, shown map[string]any, asked time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), selfApprovalWait)
	defer cancel()
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		b, _ := json.Marshal(shown[c.Key])
		parts = append(parts, c.Key+" → "+string(b))
	}
	sort.Strings(parts)
	args := map[string]any{}
	for k, v := range shown {
		args[k] = v
	}
	dec, _ := a.askToolApproval(ctx, backend.ApprovalRequest{
		ID: id, Tool: "settings_set", Command: "Change SuperLeo's settings: " + strings.Join(parts, "; "),
		Args: args, By: by, AskedAt: asked, ExpiresAt: asked.Add(selfApprovalWait),
		// Asked every time: neither YOLO nor a remembered rule may say yes to
		// a change of what the agent may do.
		MustAsk: true,
	})
	if !dec.Allowed {
		status, why := reqDenied, "the person said no"
		if dec.By != backend.DecidedByUser {
			status, why = reqExpired, "nobody answered in time"
		}
		a.updateSelfRequest(id, func(r *SelfSettingsRequest) { r.Status, r.Error, r.DecidedAt = status, why, time.Now() })
		return
	}
	a.updateSelfRequest(id, func(r *SelfSettingsRequest) { r.Status, r.DecidedAt = reqApplying, time.Now() })
	if !a.waitIdle() {
		a.updateSelfRequest(id, func(r *SelfSettingsRequest) {
			r.Status, r.Error = reqFailed, "approved, but a conversation stayed mid-turn too long to apply it; ask again"
		})
		return
	}
	if err := a.applySelfChanges(changes); err != nil {
		msg := scrubErr(err, changes).Error()
		a.updateSelfRequest(id, func(r *SelfSettingsRequest) { r.Status, r.Error = reqFailed, msg })
		return
	}
	a.updateSelfRequest(id, func(r *SelfSettingsRequest) { r.Status = reqApplied })
}

package app

// Notifications from outside, and the phone as a surface.
//
// The reminders that matter most are not raised by SuperLeo at all: the 08:30
// memo, "该出发接三宝了", 起立喝水 and the price alerts are openclaw's cron
// jobs. PostNotification is how they arrive. Each one becomes an ordinary
// notice (backend/notice.go), so it reaches the open page as the `notice`
// event and is kept in the notification centre — and, the reason this exists,
// reaches the paired phones as a real notification through the apns
// subscriber below. It does not go to the webhook: the sender already posts
// to the chat the webhook relays to (Notice.External).
//
// The apns subscriber is not only for posted notices. It reads Notice.Push
// like the webhook does, so anything raised for a person who is away —
// notify_user from the middle of a long task, a scheduled run that finished —
// lands on the phone the same way.
//
// An integration retries; a cron job may fire twice around a restart. So a
// post that names a key is delivered once per source and key in 24 hours, and
// the keys are kept on disk so a restart does not forget them.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liliang-cn/superleo/internal/apns"
	"github.com/liliang-cn/superleo/internal/backend"
)

// postNotificationPath is the one route an integration token opens.
const postNotificationPath = "/api/rpc/PostNotification"

const (
	maxPostTitle    = 120
	maxPostBody     = 4000
	maxPostSource   = 60
	maxPostKey      = 200
	maxPostDeeplink = 2048
	maxPostActions  = 3
	maxActionLabel  = 40
	maxActionSay    = 1000
	// postDedupeWindow is how long a source and key stay delivered.
	postDedupeWindow = 24 * time.Hour
	postedKeysFile   = "notification-keys.json"
	// alertBodyChars is how much of the body goes on the lock screen; the
	// whole of it is in the notification centre.
	alertBodyChars = 180
)

// PostNotificationRequest is one notification from an integration.
type PostNotificationRequest struct {
	// Title is required, at most 120 characters.
	Title string `json:"title"`
	// Body is Markdown, at most 4000 characters.
	Body string `json:"body"`
	// Source names the sender, e.g. "openclaw".
	Source string `json:"source"`
	// Kind is "brief", "reminder", "alert" or "info" (the default).
	Kind string `json:"kind"`
	// Key, when given, makes the post deliver once per Source and Key in 24
	// hours, and later ones with the same key replace it on the phone.
	Key string `json:"key,omitempty"`
	// Deeplink is where tapping it goes.
	Deeplink string `json:"deeplink,omitempty"`
	// Time is when the thing happened, RFC 3339; now when empty.
	Time string `json:"time,omitempty"`
	// Actions are up to 3 buttons; each says Say to the agent when tapped.
	Actions []backend.NoticeAction `json:"actions,omitempty"`
}

// PostedNotification is PostNotification's answer. ID is the notification's
// id in the centre (Notifications, MarkNotificationsRead) and in the push's
// data; for a duplicate it is the id the first post was given.
type PostedNotification struct {
	OK        bool   `json:"ok"`
	ID        string `json:"id"`
	Duplicate bool   `json:"duplicate"`
}

// PostNotification delivers a notification from outside to every surface:
//
//	POST /api/rpc/PostNotification  [{"title":"该出发接三宝了","body":"…","source":"openclaw","kind":"reminder","key":"pickup-2026-10-14"}]
//	→ {"ok":true,"id":"…","duplicate":false}
//
// The master token, a paired device or an integration token may call it
// (integrations.go). A request that does not validate is a 400 saying why.
func (a *App) PostNotification(req PostNotificationRequest) (PostedNotification, error) {
	n, err := req.notice()
	if err != nil {
		return PostedNotification{}, &callError{http.StatusBadRequest, "PostNotification: " + err.Error()}
	}
	n.ID = "ntf_" + randomSecret(8)
	if n.Key != "" {
		prev, dup, err := postedKeysAt(postedKeysPath()).claim(n.Source, n.Key, n.ID)
		if err != nil {
			return PostedNotification{}, err
		}
		if dup {
			return PostedNotification{OK: true, ID: prev, Duplicate: true}, nil
		}
	}
	a.notices().Raise(context.Background(), n)
	return PostedNotification{OK: true, ID: n.ID}, nil
}

// notice validates the request and turns it into a notice.
func (r PostNotificationRequest) notice() (backend.Notice, error) {
	title := strings.TrimSpace(r.Title)
	body := strings.TrimSpace(r.Body)
	source := strings.TrimSpace(r.Source)
	key := strings.TrimSpace(r.Key)
	link := strings.TrimSpace(r.Deeplink)
	kind := strings.ToLower(strings.TrimSpace(r.Kind))
	switch {
	case title == "":
		return backend.Notice{}, errors.New("title is required")
	case utf8.RuneCountInString(title) > maxPostTitle:
		return backend.Notice{}, errors.New("title is longer than 120 characters")
	case utf8.RuneCountInString(body) > maxPostBody:
		return backend.Notice{}, errors.New("body is longer than 4000 characters")
	case utf8.RuneCountInString(source) > maxPostSource:
		return backend.Notice{}, errors.New("source is longer than 60 characters")
	case utf8.RuneCountInString(key) > maxPostKey:
		return backend.Notice{}, errors.New("key is longer than 200 characters")
	}
	switch kind {
	case "":
		kind = backend.KindInfo
	case backend.KindInfo, backend.KindBrief, backend.KindReminder, backend.KindAlert:
	default:
		return backend.Notice{}, errors.New(`kind must be "brief", "reminder", "alert" or "info"`)
	}
	if link != "" {
		if err := checkDeeplink(link); err != nil {
			return backend.Notice{}, err
		}
	}
	at := time.Now()
	if t := strings.TrimSpace(r.Time); t != "" {
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return backend.Notice{}, errors.New("time is not RFC 3339, e.g. 2026-10-14T08:30:00+08:00")
		}
		at = parsed
	}
	if len(r.Actions) > maxPostActions {
		return backend.Notice{}, errors.New("at most 3 actions")
	}
	var actions []backend.NoticeAction
	for _, act := range r.Actions {
		act.Label, act.Say = strings.TrimSpace(act.Label), strings.TrimSpace(act.Say)
		switch {
		case act.Label == "" || act.Say == "":
			return backend.Notice{}, errors.New("every action needs a label and a say")
		case utf8.RuneCountInString(act.Label) > maxActionLabel:
			return backend.Notice{}, errors.New("an action label is longer than 40 characters")
		case utf8.RuneCountInString(act.Say) > maxActionSay:
			return backend.Notice{}, errors.New("an action's say is longer than 1000 characters")
		}
		actions = append(actions, act)
	}
	level := backend.LevelInfo
	if kind == backend.KindAlert {
		level = backend.LevelWarn
	}
	return backend.Notice{
		Level:   level,
		Title:   title,
		Message: body,
		Source:  source,
		Key:     key,
		Kind:    kind,
		// Local, as every other notice is stamped: the centre sorts by the
		// text, and two zones would sort wrong.
		At:       at.Local().Format(time.RFC3339),
		Deeplink: link,
		Actions:  actions,
		Push:     true,
		// Not to the webhook: the sender has its own way to Telegram.
		External: true,
	}, nil
}

var schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*$`)

// checkDeeplink wants a URL with a scheme the app might open, and none of the
// ones that run or read something where they are opened.
func checkDeeplink(link string) error {
	if len(link) > maxPostDeeplink {
		return errors.New("deeplink is too long")
	}
	u, err := url.Parse(link)
	if err != nil || !schemeRE.MatchString(u.Scheme) || strings.ContainsAny(link, " \t\r\n") {
		return errors.New("deeplink is not a URL, e.g. superleo://chat?prompt=… or noted://alarm/<id>")
	}
	switch strings.ToLower(u.Scheme) {
	case "javascript", "data", "file", "vbscript":
		return errors.New("deeplink scheme " + u.Scheme + " is not allowed")
	}
	return nil
}

// notices is the fan-out a notice goes through: the service's, or — when the
// agent is not up, which is no reason to lose a reminder — one built for the
// occasion with the surfaces that do not need it.
func (a *App) notices() *backend.Notices {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if p := svc.Notices(); p != nil {
		return p
	}
	p := backend.NewNotices(nil)
	p.SetEmitter(a.emit)
	p.Subscribe("inbox", backend.OpenInbox().Sink())
	p.Subscribe("apns", a.apnsNoticeSink())
	return p
}

// postedKeys remembers which source and key were delivered, and as what.
type postedKeys struct {
	path string
	now  func() time.Time
}

type postedKey struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// postedKeysMu serialises every claim: two posts with one key arriving
// together must not both be first.
var postedKeysMu sync.Mutex

func postedKeysPath() string {
	return filepath.Join(backend.DataDir(), "data", postedKeysFile)
}

func postedKeysAt(path string) *postedKeys { return &postedKeys{path: path, now: time.Now} }

// claim records source and key as delivered under id, unless they already
// were within the window: then it answers with that delivery's id.
func (p *postedKeys) claim(source, key, id string) (string, bool, error) {
	postedKeysMu.Lock()
	defer postedKeysMu.Unlock()
	keys := map[string]postedKey{}
	if b, err := os.ReadFile(p.path); err == nil {
		if err := json.Unmarshal(b, &keys); err != nil {
			// A broken file loses the window, not the notification.
			slog.Warn("notifications: posted keys unreadable, starting over", "path", p.path, "error", err)
			keys = map[string]postedKey{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	now := p.now()
	for k, v := range keys {
		if now.Sub(v.At) >= postDedupeWindow {
			delete(keys, k)
		}
	}
	k := source + "\x00" + key
	if prev, ok := keys[k]; ok {
		return prev.ID, true, nil
	}
	keys[k] = postedKey{ID: id, At: now.UTC()}
	b, err := json.MarshalIndent(keys, "", " ")
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return "", false, err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return "", false, err
	}
	return id, false, os.Rename(tmp, p.path)
}

// noticePusher sends one device a visible notification. *apns.Client is one.
type noticePusher interface {
	Alert(ctx context.Context, deviceToken, env string, a apns.Alert) error
}

// noticePush is the apns subscriber's parts, functions so a test can stand in
// for Apple and the phones.
type noticePush struct {
	// pusher is nil when pushes are not configured.
	pusher  func() noticePusher
	devices func() (*deviceStore, error)
	// unread, when set, is the notification centre's unread count with the
	// notice named id in it: the number for the app icon's badge.
	unread func(id string) int
	// done, when set, hears when a notice has been sent to every device.
	done func()
	// vendors picks the sender for an Android phone's provider
	// (vendorSenderFor when nil). See push_vendor.go.
	vendors func(provider string) vendorSender
}

// apnsNoticeSink is the subscriber that puts pushed notices on the phones.
func (a *App) apnsNoticeSink() backend.Subscriber { return a.phonePush().sink() }

// phonePush is the parts that put an alert on the paired phones.
func (a *App) phonePush() noticePush {
	return noticePush{
		pusher: func() noticePusher {
			// The same client the alarm pushes use; nil (with its one log
			// line) when APNs is not configured.
			p, _ := a.alarmPusher().(noticePusher)
			return p
		},
		devices: func() (*deviceStore, error) { return openDevices(devicesPath()) },
		unread:  func(id string) int { return a.inbox().UnreadIncluding(id) },
	}
}

func (np noticePush) sink() backend.Subscriber {
	return func(ctx context.Context, n backend.Notice) {
		if !n.Push {
			return
		}
		// Detached and on its own goroutine: a subscriber must not block, and
		// the turn that raised this ends — cancelling its context — the moment
		// the notice is out.
		go np.send(context.WithoutCancel(ctx), n)
	}
}

func (np noticePush) send(ctx context.Context, n backend.Notice) {
	if np.done != nil {
		defer np.done()
	}
	alert, ok := alertFor(n)
	if !ok {
		return
	}
	np.deliver(ctx, alert, true)
}

// deliver sends one alert to every phone with a push token, with the unread
// badge when badge is set.
func (np noticePush) deliver(ctx context.Context, alert apns.Alert, badge bool) {
	store, err := np.devices()
	if err != nil {
		slog.WarnContext(ctx, "notifications: no devices to push to", "error", err)
		return
	}
	// Android phones first: their channels need no APNs client.
	pushToVendors(ctx, store, "notifications", np.vendors, vendorAlert(alert))
	// No phone, no client: a machine nobody paired a phone to never builds
	// one, nor logs that it cannot.
	if len(store.pushTargets()) == 0 {
		return
	}
	p := np.pusher()
	if p == nil {
		return
	}
	// The icon's number, so it is right while the app is closed. Counted
	// with this notice in, whether or not the centre has filed it yet.
	if badge && np.unread != nil {
		id, _ := alert.Data.(map[string]any)["id"].(string)
		n := np.unread(id)
		alert.Badge = &n
	}
	pushToDevices(ctx, store, "notifications", func(ctx context.Context, t pushTarget) error {
		return p.Alert(ctx, t.Token, t.Env, alert)
	})
}

// vendorAlert is an APNs alert as a vendor channel carries it, on the
// Android notification channel its kind belongs on.
func vendorAlert(a apns.Alert) vendorPush {
	ch := "general"
	switch a.ThreadID {
	case "approval":
		ch = "approval"
	case backend.KindReminder, backend.KindAlert:
		ch = "reminder"
	}
	data, _ := a.Data.(map[string]any)
	return vendorPush{Title: a.Title, Body: a.Body, Channel: ch, Data: data}
}

// alertFor is how a notice looks on the lock screen, by kind:
//
//	kind      priority  sound    interruption
//	reminder  10        default  time-sensitive
//	alert     10        default  time-sensitive
//	brief     10        —        active
//	info      5         —        active   (also a notice with no kind)
//
// false when there is nothing to show.
func alertFor(n backend.Notice) (apns.Alert, bool) {
	kind := n.Kind
	if kind == "" {
		kind = backend.KindInfo
	}
	title := strings.TrimSpace(n.Title)
	body := plainText(n.Message, alertBodyChars)
	if title == "" && body == "" {
		return apns.Alert{}, false
	}
	// A notice that was not posted has no id of its own; the centre derives
	// one from the key, or the time and the text, and so does this.
	id := n.ID
	if id == "" {
		id = strings.TrimSpace(n.Key)
	}
	if id == "" {
		id = n.At + "|" + n.Message
	}
	data := map[string]any{"kind": "notice", "id": id, "source": n.Source}
	if n.Deeplink != "" {
		data["deeplink"] = n.Deeplink
	}
	category := ""
	if n.Reply != nil && strings.TrimSpace(n.Reply.Session) != "" {
		// A terminal turn: answered from the lock screen, to that session.
		category = ccReplyCategory
		data["reply"] = map[string]any{"agent": n.Reply.Agent, "session": n.Reply.Session}
	}
	a := apns.Alert{
		Title:             title,
		Body:              body,
		ThreadID:          kind,
		InterruptionLevel: "active",
		Priority:          10,
		CollapseID:        n.Key,
		Data:              data,
		Category:          category,
	}
	switch kind {
	case backend.KindReminder, backend.KindAlert:
		a.Sound = true
		a.InterruptionLevel = "time-sensitive"
	case backend.KindInfo:
		a.Priority = 5
	}
	return a, true
}

// pushToDevices sends to every registered phone and forgets the tokens Apple
// says are dead. what names the sender in the log.
func pushToDevices(ctx context.Context, store *deviceStore, what string, send func(context.Context, pushTarget) error) {
	for _, t := range store.pushTargets() {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := send(cctx, t)
		cancel()
		switch {
		case errors.Is(err, apns.ErrDeviceGone):
			slog.InfoContext(ctx, "push token is dead, dropped", "module", what, "device", t.DeviceID, "error", err)
			if derr := store.dropPushToken(t.DeviceID, t.Token); derr != nil {
				slog.WarnContext(ctx, "dropping a push token", "module", what, "device", t.DeviceID, "error", derr)
			}
		case err != nil:
			slog.WarnContext(ctx, "push to device", "module", what, "device", t.DeviceID, "error", err)
		}
	}
}

var (
	mdImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdFence    = regexp.MustCompile("(?m)^\\s*(```|~~~).*$")
	mdLineHead = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+\[[ xX]\]\s+|[-*+]\s+|\d+[.)]\s+)`)
	mdRule     = regexp.MustCompile(`(?m)^\s*([-*_]\s*){3,}$`)
	mdEmph     = regexp.MustCompile(`(\*\*|__|~~|\*|` + "`" + `)`)
	mdTableSep = regexp.MustCompile(`(?m)^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-*:?\s*$`)
	spaces     = regexp.MustCompile(`\s+`)
)

// plainText is Markdown read aloud: the words without the marks, on one line,
// cut to max characters.
func plainText(md string, max int) string {
	s := md
	s = mdFence.ReplaceAllString(s, "")
	s = mdTableSep.ReplaceAllString(s, "")
	s = mdRule.ReplaceAllString(s, "")
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdLineHead.ReplaceAllString(s, "")
	s = mdEmph.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "|", " ")
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

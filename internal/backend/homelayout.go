package backend

// The phone's home screen: an ordered stack of widgets the person arranges on
// the phone and the queen arranges in a sentence. Core keeps the one copy both
// edit (home-layout.json in the data dir), with a revision that only goes up,
// so a phone that edited an older copy is told so and can put its edit on top
// of the current one instead of overwriting the queen's.
//
// The JSON is the iOS app's Codable HomeLayout exactly (HomeLayout.swift):
//
//	{"version":1,"widgets":[{"id":"<UUID>","kind":"quote","size":"small","config":{"symbol":"VOO"}}]}
//
// Swift's synthesized decoder needs id, kind, size and config on every widget
// and drops a widget whose UUID or enum fields do not parse, so everything
// written here carries all four and only values the phone can read. A widget
// of a kind this build does not know, and fields it does not know, are kept as
// they came: a newer phone's widget survives an older core.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// HomeWidgetKinds are the kinds the phone draws, in the order its gallery
// shows them.
var HomeWidgetKinds = []string{"time", "portfolio", "quote", "hive", "alarm", "agenda", "generated"}

// homeKindHelp is what each kind is, for the tool description and errors.
var homeKindHelp = map[string]string{
	"time":      "time logged this week (config space: work | life, omit for both)",
	"portfolio": "holdings added up (config asset_class: stocks | gold | crypto | cash, omit for all; currency e.g. USD)",
	"quote":     "one symbol's price, daily change and 30-day line (config symbol, e.g. VOO, QQQM, BTC, GC=F, 黄金)",
	"hive":      "workers online and what they are doing",
	"alarm":     "the next alarm",
	"agenda":    "how many things today still holds",
	"generated": "a widget described in words, made by the hive (config prompt)",
}

var (
	homeSizes       = map[string]bool{"small": true, "wide": true}
	homeSpaces      = map[string]bool{"work": true, "life": true}
	homeAssetClass  = map[string]bool{"stocks": true, "gold": true, "crypto": true, "cash": true}
	homeCurrency    = regexp.MustCompile(`^[A-Z]{3}$`)
	homeSymbol      = regexp.MustCompile(`^[\p{L}\p{N}.=^_/-]{1,24}$`)
	homeUUID        = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	errHomeNotFound = errors.New("no widget with that id — call home_layout with action get for the ids")
)

const (
	// homeMaxWidgets bounds the stack; a home screen of more is a list.
	homeMaxWidgets = 24
	// homeMaxBytes bounds what a client may store.
	homeMaxBytes = 128 << 10
	// homeMaxPrompt bounds a generated widget's description.
	homeMaxPrompt = 200
)

// IsHomeWidgetKind says whether the phone knows how to draw kind.
func IsHomeWidgetKind(kind string) bool { _, ok := homeKindHelp[kind]; return ok }

// HomeWidget is one widget. Fields this build does not know are kept in extra
// and written back unchanged.
type HomeWidget struct {
	ID     string
	Kind   string
	Size   string
	Config map[string]any
	extra  map[string]json.RawMessage
}

func (w HomeWidget) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(w.extra)+4)
	for k, v := range w.extra {
		m[k] = v
	}
	size := w.Size
	if size == "" {
		size = "small"
	}
	cfg := w.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	m["id"], m["kind"], m["size"], m["config"] = w.ID, w.Kind, size, cfg
	return json.Marshal(m)
}

func (w *HomeWidget) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*w = HomeWidget{}
	take := func(k string, into any) error {
		raw, ok := m[k]
		if !ok {
			return nil
		}
		delete(m, k)
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return json.Unmarshal(raw, into)
	}
	if err := take("id", &w.ID); err != nil {
		return fmt.Errorf("widget id: %w", err)
	}
	if err := take("kind", &w.Kind); err != nil {
		return fmt.Errorf("widget kind: %w", err)
	}
	if err := take("size", &w.Size); err != nil {
		return fmt.Errorf("widget size: %w", err)
	}
	if err := take("config", &w.Config); err != nil {
		return fmt.Errorf("widget config: %w", err)
	}
	if len(m) > 0 {
		w.extra = m
	}
	return nil
}

// HomeLayoutDoc is the stack in order. Version is the layout's schema version
// as the phone writes it, not the store's revision.
type HomeLayoutDoc struct {
	Version int
	Widgets []HomeWidget
	extra   map[string]json.RawMessage
}

func (l HomeLayoutDoc) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(l.extra)+2)
	for k, v := range l.extra {
		m[k] = v
	}
	v := l.Version
	if v == 0 {
		v = 1
	}
	ws := l.Widgets
	if ws == nil {
		ws = []HomeWidget{}
	}
	m["version"], m["widgets"] = v, ws
	return json.Marshal(m)
}

func (l *HomeLayoutDoc) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*l = HomeLayoutDoc{}
	if raw, ok := m["version"]; ok {
		_ = json.Unmarshal(raw, &l.Version)
		delete(m, "version")
	}
	if raw, ok := m["widgets"]; ok {
		if err := json.Unmarshal(raw, &l.Widgets); err != nil {
			return fmt.Errorf("widgets: %w", err)
		}
		delete(m, "widgets")
	}
	if l.Version == 0 {
		l.Version = 1
	}
	if len(m) > 0 {
		l.extra = m
	}
	return nil
}

// Clone is a deep copy, through JSON so the kept-unknown parts copy too.
func (l *HomeLayoutDoc) Clone() *HomeLayoutDoc {
	if l == nil {
		return nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil
	}
	var out HomeLayoutDoc
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return &out
}

func (l *HomeLayoutDoc) index(ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1, errors.New("id is required — call home_layout with action get for the ids")
	}
	for i, w := range l.Widgets {
		if strings.EqualFold(w.ID, ref) {
			return i, nil
		}
	}
	// A prefix, as long as it names one widget.
	found := -1
	if len(ref) >= 4 {
		for i, w := range l.Widgets {
			if len(w.ID) >= len(ref) && strings.EqualFold(w.ID[:len(ref)], ref) {
				if found >= 0 {
					return -1, fmt.Errorf("id %q matches more than one widget; give the whole id", ref)
				}
				found = i
			}
		}
	}
	if found < 0 {
		return -1, errHomeNotFound
	}
	return found, nil
}

// DefaultHomeLayout is the approved starting four — this week's time, the US
// stocks, the hive, the next alarm — with the ids the phone gives its own
// suggestion, so a phone still showing the suggestion sees the same widgets.
func DefaultHomeLayout() *HomeLayoutDoc {
	id := func(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }
	return &HomeLayoutDoc{Version: 1, Widgets: []HomeWidget{
		{ID: id(1), Kind: "time", Size: "small", Config: map[string]any{}},
		{ID: id(2), Kind: "portfolio", Size: "small", Config: map[string]any{"assetClass": "stocks", "currency": "USD"}},
		{ID: id(3), Kind: "hive", Size: "small", Config: map[string]any{}},
		{ID: id(4), Kind: "alarm", Size: "small", Config: map[string]any{}},
	}}
}

// NewHomeWidgetID is a fresh UUID in the form Swift writes one.
func NewHomeWidgetID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// ValidateClientHomeLayout checks a layout a client sends whole. It is the
// person's own arrangement from a phone that already only offers what it can
// draw, so it is checked for shape, not taste: ids that are UUIDs and unique,
// a kind on every widget, a bounded size. Kinds this build does not know pass.
func ValidateClientHomeLayout(l *HomeLayoutDoc) error {
	if l == nil {
		return nil
	}
	if len(l.Widgets) > 64 {
		return fmt.Errorf("%d widgets; at most 64", len(l.Widgets))
	}
	seen := map[string]bool{}
	for i, w := range l.Widgets {
		if !homeUUID.MatchString(w.ID) {
			return fmt.Errorf("widget %d: id %q is not a UUID", i, w.ID)
		}
		if seen[strings.ToUpper(w.ID)] {
			return fmt.Errorf("widget %d: id %s appears twice", i, w.ID)
		}
		seen[strings.ToUpper(w.ID)] = true
		if strings.TrimSpace(w.Kind) == "" || len(w.Kind) > 32 {
			return fmt.Errorf("widget %d: kind %q", i, w.Kind)
		}
	}
	if b, err := json.Marshal(l); err != nil {
		return err
	} else if len(b) > homeMaxBytes {
		return fmt.Errorf("layout is %d bytes; at most %d", len(b), homeMaxBytes)
	}
	return nil
}

// HomeLayoutState is the stored layout and its revision, as the RPCs and the
// home:layout event carry it. Layout nil means nothing arranged: the phone
// shows its own suggestion. Version 0 means never written.
type HomeLayoutState struct {
	Layout    *HomeLayoutDoc `json:"layout"`
	Version   int64          `json:"version"`
	UpdatedAt *time.Time     `json:"updatedAt"`
	Source    string         `json:"source"`
}

// Map is the state as an event payload.
func (s HomeLayoutState) Map() map[string]any {
	var m map[string]any
	b, _ := json.Marshal(s)
	_ = json.Unmarshal(b, &m)
	return m
}

// HomeLayoutStore keeps the layout in one JSON file, written whole and
// atomically, under a lock so a phone's write and the queen's cannot
// interleave.
type HomeLayoutStore struct {
	path string
	now  func() time.Time

	mu     sync.Mutex
	state  HomeLayoutState
	loaded bool
}

// HomeLayoutFile is where the layout is kept in the data dir.
const HomeLayoutFile = "home-layout.json"

// NewHomeLayoutStore keeps the layout at path.
func NewHomeLayoutStore(path string) *HomeLayoutStore {
	return &HomeLayoutStore{path: path, now: time.Now}
}

// DefaultHomeLayoutStore keeps it in the data dir.
func DefaultHomeLayoutStore() *HomeLayoutStore {
	return NewHomeLayoutStore(filepath.Join(DataDir(), HomeLayoutFile))
}

func (s *HomeLayoutStore) load() error {
	if s.loaded {
		return nil
	}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		var st HomeLayoutState
		if err := json.Unmarshal(b, &st); err != nil {
			return fmt.Errorf("%s: %w", s.path, err)
		}
		s.state = st
	}
	s.loaded = true
	return nil
}

func (s *HomeLayoutStore) snapshot() HomeLayoutState {
	st := s.state
	st.Layout = s.state.Layout.Clone()
	return st
}

// Get is the current layout and revision.
func (s *HomeLayoutStore) Get() (HomeLayoutState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return HomeLayoutState{}, err
	}
	return s.snapshot(), nil
}

// Set replaces the layout when base is the current revision. A stale base
// changes nothing and reports the current state with conflict true, so the
// writer can put its edit on top of it and try again.
func (s *HomeLayoutStore) Set(l *HomeLayoutDoc, base int64, source string) (HomeLayoutState, bool, error) {
	if err := ValidateClientHomeLayout(l); err != nil {
		return HomeLayoutState{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return HomeLayoutState{}, false, err
	}
	if base != s.state.Version {
		return s.snapshot(), true, nil
	}
	if err := s.commit(l.Clone(), source); err != nil {
		return HomeLayoutState{}, false, err
	}
	return s.snapshot(), false, nil
}

// Update edits the current layout in place: fn gets a copy (nil when nothing
// is arranged) and returns the next one. An error from fn changes nothing.
func (s *HomeLayoutStore) Update(source string, fn func(cur *HomeLayoutDoc) (*HomeLayoutDoc, error)) (HomeLayoutState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return HomeLayoutState{}, err
	}
	next, err := fn(s.state.Layout.Clone())
	if err != nil {
		return HomeLayoutState{}, err
	}
	if err := ValidateClientHomeLayout(next); err != nil {
		return HomeLayoutState{}, err
	}
	if err := s.commit(next, source); err != nil {
		return HomeLayoutState{}, err
	}
	return s.snapshot(), nil
}

// commit writes the next state with the revision moved on. Callers hold mu.
func (s *HomeLayoutStore) commit(l *HomeLayoutDoc, source string) error {
	t := s.now().UTC().Truncate(time.Millisecond)
	next := HomeLayoutState{Layout: l, Version: s.state.Version + 1, UpdatedAt: &t, Source: source}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.state = next
	return nil
}

// --- the queen's edits ---

// HomeAction is one home_layout call, already read from the tool's arguments.
type HomeAction struct {
	Action string
	ID     string
	Kind   string
	Size   string
	// Index is the 0-based position for add and move; nil is the end (add).
	Index *int
	// Config fields; Has says which were given, so configure can clear one
	// with an empty value and leave the rest.
	Space, AssetClass, Currency, Symbol, Prompt string
	Has                                         map[string]bool
}

// HomeActionFromArgs reads a tool call's arguments.
func HomeActionFromArgs(a map[string]any) HomeAction {
	act := HomeAction{
		Action: strings.ToLower(strings.TrimSpace(argStr(a, "action"))),
		ID:     strings.TrimSpace(argStr(a, "id")),
		Kind:   strings.ToLower(strings.TrimSpace(argStr(a, "kind"))),
		Size:   strings.ToLower(strings.TrimSpace(argStr(a, "size"))),
		Has:    map[string]bool{},
	}
	if v, ok := a["index"]; ok && v != nil {
		n := argInt(a, "index")
		act.Index = &n
	}
	for _, k := range []string{"space", "asset_class", "currency", "symbol", "prompt"} {
		if v, ok := a[k]; ok && v != nil {
			act.Has[k] = true
		}
	}
	act.Space = strings.ToLower(strings.TrimSpace(argStr(a, "space")))
	act.AssetClass = strings.ToLower(strings.TrimSpace(argStr(a, "asset_class")))
	act.Currency = strings.ToUpper(strings.TrimSpace(argStr(a, "currency")))
	act.Symbol = strings.TrimSpace(argStr(a, "symbol"))
	act.Prompt = strings.TrimSpace(argStr(a, "prompt"))
	return act
}

// HomeSymbolCheck says whether a quote symbol exists. It returns
// ErrUnknownSymbol for one that does not, and any other error when it could
// not tell (offline); that one is let through.
type HomeSymbolCheck func(symbol string) error

// ApplyHomeAction runs one edit on cur (nil: nothing arranged yet, which edits
// start from DefaultHomeLayout) and returns the next layout and a line about
// what happened. get returns cur unchanged; reset returns nil.
func ApplyHomeAction(cur *HomeLayoutDoc, act HomeAction, check HomeSymbolCheck) (*HomeLayoutDoc, string, error) {
	switch act.Action {
	case "get":
		return cur, "", nil
	case "reset":
		return nil, "Back to the phone's own suggestion.", nil
	case "add", "remove", "move", "resize", "configure":
	case "":
		return nil, "", errors.New("action is required: get, add, remove, move, resize, configure or reset")
	default:
		return nil, "", fmt.Errorf("unknown action %q: use get, add, remove, move, resize, configure or reset", act.Action)
	}
	l := cur.Clone()
	note := ""
	if l == nil {
		l = DefaultHomeLayout()
		note = "Started from the default four (time, US stocks, hive, alarm). "
	}
	if act.Size != "" && !homeSizes[act.Size] {
		return nil, "", fmt.Errorf("size %q: small (half a row) or wide (a whole row)", act.Size)
	}

	switch act.Action {
	case "add":
		if !IsHomeWidgetKind(act.Kind) {
			return nil, "", fmt.Errorf("kind %q is not one the phone draws; kinds: %s", act.Kind, strings.Join(HomeWidgetKinds, ", "))
		}
		if len(l.Widgets) >= homeMaxWidgets {
			return nil, "", fmt.Errorf("home already has %d widgets; remove one first", len(l.Widgets))
		}
		w := HomeWidget{ID: NewHomeWidgetID(), Kind: act.Kind, Size: act.Size, Config: map[string]any{}}
		if w.Size == "" {
			w.Size = "small"
		}
		extra, err := configureHomeWidget(&w, act, true, check)
		if err != nil {
			return nil, "", err
		}
		at := len(l.Widgets)
		if act.Index != nil {
			at = clampInt(*act.Index, 0, len(l.Widgets))
		}
		l.Widgets = append(l.Widgets, HomeWidget{})
		copy(l.Widgets[at+1:], l.Widgets[at:])
		l.Widgets[at] = w
		note += fmt.Sprintf("Added %s at position %d.%s", homeWidgetTitle(w), at, extra)
	case "remove":
		i, err := l.index(act.ID)
		if err != nil {
			return nil, "", err
		}
		w := l.Widgets[i]
		l.Widgets = append(l.Widgets[:i], l.Widgets[i+1:]...)
		note += "Removed " + homeWidgetTitle(w) + "."
	case "move":
		i, err := l.index(act.ID)
		if err != nil {
			return nil, "", err
		}
		if act.Index == nil {
			return nil, "", errors.New("index is required for move: the 0-based position to move it to")
		}
		w := l.Widgets[i]
		l.Widgets = append(l.Widgets[:i], l.Widgets[i+1:]...)
		to := clampInt(*act.Index, 0, len(l.Widgets))
		l.Widgets = append(l.Widgets, HomeWidget{})
		copy(l.Widgets[to+1:], l.Widgets[to:])
		l.Widgets[to] = w
		note += fmt.Sprintf("Moved %s to position %d.", homeWidgetTitle(w), to)
	case "resize":
		i, err := l.index(act.ID)
		if err != nil {
			return nil, "", err
		}
		if act.Size == "" {
			return nil, "", errors.New("size is required for resize: small or wide")
		}
		l.Widgets[i].Size = act.Size
		note += fmt.Sprintf("%s is now %s.", homeWidgetTitle(l.Widgets[i]), act.Size)
	case "configure":
		i, err := l.index(act.ID)
		if err != nil {
			return nil, "", err
		}
		w := l.Widgets[i]
		if !IsHomeWidgetKind(w.Kind) {
			return nil, "", fmt.Errorf("widget %s is of kind %q, which this core does not know how to set", w.ID, w.Kind)
		}
		if act.Size != "" {
			w.Size = act.Size
		}
		extra, err := configureHomeWidget(&w, act, false, check)
		if err != nil {
			return nil, "", err
		}
		l.Widgets[i] = w
		note += "Set " + homeWidgetTitle(w) + "." + extra
	}
	return l, strings.TrimSpace(note), nil
}

// configureHomeWidget applies the config fields act gives to w, checking each
// against w's kind. fresh is an add: a quote needs its symbol and a generated
// widget its prompt. Fields that are not the kind's own are an error, so a
// wrong guess is said rather than silently dropped.
func configureHomeWidget(w *HomeWidget, act HomeAction, fresh bool, check HomeSymbolCheck) (string, error) {
	if w.Config == nil {
		w.Config = map[string]any{}
	}
	own := map[string][]string{
		"time":      {"space"},
		"portfolio": {"asset_class", "currency"},
		"quote":     {"symbol"},
		"generated": {"prompt"},
	}[w.Kind]
	given := map[string]string{"space": act.Space, "asset_class": act.AssetClass, "currency": act.Currency,
		"symbol": act.Symbol, "prompt": act.Prompt}
	for k := range act.Has {
		ok := false
		for _, o := range own {
			ok = ok || o == k
		}
		// An empty field that is not the kind's own is a model filling in
		// every parameter, not a request.
		if !ok && given[k] != "" {
			return "", fmt.Errorf("%s does not apply to a %s widget (%s)", k, w.Kind, homeKindHelp[w.Kind])
		}
	}
	set := func(key, v string) {
		if v == "" {
			delete(w.Config, key)
		} else {
			w.Config[key] = v
		}
	}
	extra := ""
	switch w.Kind {
	case "time":
		if act.Has["space"] {
			if act.Space != "" && !homeSpaces[act.Space] {
				return "", fmt.Errorf("space %q: work or life (omit for both)", act.Space)
			}
			set("space", act.Space)
		}
	case "portfolio":
		if act.Has["asset_class"] {
			if act.AssetClass != "" && !homeAssetClass[act.AssetClass] {
				return "", fmt.Errorf("asset_class %q: stocks, gold, crypto or cash (omit for all)", act.AssetClass)
			}
			set("assetClass", act.AssetClass)
		}
		if act.Has["currency"] {
			if act.Currency != "" && !homeCurrency.MatchString(act.Currency) {
				return "", fmt.Errorf("currency %q: a three-letter code such as USD, CNY, HKD", act.Currency)
			}
			set("currency", act.Currency)
		}
	case "quote":
		if fresh && act.Symbol == "" {
			return "", errors.New("a quote widget needs a symbol, e.g. VOO, QQQM, BTC, GC=F or 黄金")
		}
		if act.Has["symbol"] {
			sym := NormalizeQuoteSymbol(act.Symbol)
			if !homeSymbol.MatchString(sym) {
				return "", fmt.Errorf("symbol %q does not look like a ticker", act.Symbol)
			}
			if check != nil {
				switch err := check(sym); {
				case errors.Is(err, ErrUnknownSymbol):
					return "", fmt.Errorf("no market data for symbol %q; check the ticker (Yahoo Finance style: VOO, 0700.HK, 600519.SS, BTC-USD, GC=F)", sym)
				case err != nil:
					extra = " Could not check the symbol just now; it is saved as given."
				}
			}
			set("symbol", sym)
		}
	case "generated":
		if fresh && act.Prompt == "" {
			return "", errors.New("a generated widget needs a prompt: what it should show, in the person's words")
		}
		if act.Has["prompt"] {
			if act.Prompt == "" {
				return "", errors.New("a generated widget's prompt cannot be empty")
			}
			if utf8.RuneCountInString(act.Prompt) > homeMaxPrompt {
				return "", fmt.Errorf("prompt is longer than %d characters", homeMaxPrompt)
			}
			if act.Prompt != w.Config["prompt"] {
				// A new spec: the phone sees an id it has never made and asks
				// the hive to make it from the prompt.
				w.Config["prompt"] = act.Prompt
				w.Config["spec"] = NewHomeWidgetID()
				extra = " The phone makes it from the prompt the next time it shows home."
			}
		}
	}
	return extra, nil
}

// HomeLayoutSummary is the layout as the tool shows it to the model: each
// widget's position, id, kind, size, settings and what the phone titles it.
func HomeLayoutSummary(l *HomeLayoutDoc) []map[string]any {
	out := []map[string]any{}
	if l == nil {
		return out
	}
	for i, w := range l.Widgets {
		row := map[string]any{"index": i, "id": w.ID, "kind": w.Kind, "size": w.Size, "title": homeWidgetTitle(w)}
		if len(w.Config) > 0 {
			cfg := map[string]any{}
			for k, v := range w.Config {
				if k != "spec" {
					cfg[k] = v
				}
			}
			if len(cfg) > 0 {
				row["config"] = cfg
			}
		}
		if !IsHomeWidgetKind(w.Kind) {
			row["note"] = "a kind this core does not know; it is kept as is"
		}
		out = append(out, row)
	}
	return out
}

// homeWidgetTitle is roughly what the phone writes on the widget.
func homeWidgetTitle(w HomeWidget) string {
	str := func(k string) string { s, _ := w.Config[k].(string); return s }
	switch w.Kind {
	case "time":
		if s := str("space"); s != "" {
			return "time (" + s + ")"
		}
		return "time"
	case "portfolio":
		parts := []string{}
		if s := str("assetClass"); s != "" {
			parts = append(parts, s)
		}
		if s := str("currency"); s != "" {
			parts = append(parts, s)
		}
		if len(parts) == 0 {
			return "portfolio (all)"
		}
		return "portfolio (" + strings.Join(parts, " ") + ")"
	case "quote":
		return "quote " + str("symbol")
	case "generated":
		return "generated «" + str("prompt") + "»"
	}
	return w.Kind
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// homeLayoutSection is the persona's one line about home_layout, for the
// members that have it: everyone but a worker.
func homeLayoutSection(h HiveSettings) string {
	if h.Role == HiveRoleWorker {
		return ""
	}
	return "\n\nThe person's phone home screen is yours to arrange too: home_layout adds, removes, moves, resizes and sets its widgets."
}

// HomeLayoutToolDescription is home_layout's description. The model picks the
// tool from it; nothing routes by keywords.
func HomeLayoutToolDescription() string {
	kinds := make([]string, 0, len(HomeWidgetKinds))
	for _, k := range HomeWidgetKinds {
		kinds = append(kinds, k+": "+homeKindHelp[k])
	}
	return "Arrange the person's phone home screen: an ordered stack of widgets, small ones two to a row, wide ones a row each. " +
		"The phone and this tool edit the same layout and the phone updates at once. " +
		"Actions: get (the widgets with their ids), add (kind, optional size, index and config), remove (id), move (id, index), " +
		"resize (id, size), configure (id and the config fields to change; an empty value clears one), reset (back to the phone's own suggestion). " +
		"Call get before editing a widget whose id you do not have. Kinds — " + strings.Join(kinds, "; ") + ". " +
		"Each call returns the new layout."
}

// HomeLayoutToolSchema is home_layout's parameters.
func HomeLayoutToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"get", "add", "remove", "move", "resize", "configure", "reset"}},
			"id":          map[string]any{"type": "string", "description": "The widget's id from get (a unique prefix is enough)."},
			"kind":        map[string]any{"type": "string", "enum": HomeWidgetKinds, "description": "add: what the widget shows."},
			"size":        map[string]any{"type": "string", "enum": []string{"small", "wide"}},
			"index":       map[string]any{"type": "integer", "description": "add, move: 0-based position in the stack; add without it goes to the end."},
			"space":       map[string]any{"type": "string", "enum": []string{"work", "life", ""}, "description": "time: which space; empty for both."},
			"asset_class": map[string]any{"type": "string", "enum": []string{"stocks", "gold", "crypto", "cash", ""}, "description": "portfolio: which holdings; empty for all."},
			"currency":    map[string]any{"type": "string", "description": "portfolio: the currency added up, e.g. USD; empty for the main one."},
			"symbol":      map[string]any{"type": "string", "description": "quote: a ticker, Yahoo Finance style (VOO, QQQM, 0700.HK, 600519.SS, BTC-USD, GC=F) or BTC, ETH, 黄金 (gold in CNY per gram)."},
			"prompt":      map[string]any{"type": "string", "description": "generated: what the widget should show, in the person's words."},
		},
		"required": []string{"action"},
	}
}

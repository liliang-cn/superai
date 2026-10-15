package backend

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What the iOS app's JSONEncoder writes for a HomeLayout, unknown parts and all.
const phoneLayoutJSON = `{"version":1,"widgets":[
 {"id":"6F9619FF-8B86-D011-B42D-00C04FC964FF","kind":"quote","size":"wide","config":{"symbol":"VOO"}},
 {"id":"00000000-0000-0000-0000-000000000003","kind":"hive","size":"small","config":{}},
 {"id":"7C9E6679-7425-40DE-944B-E07FC1F90AE7","kind":"weather","size":"small","config":{"city":"杭州","units":{"t":"C"}},"tint":"blue"}
],"theme":"x"}`

func TestHomeLayoutRoundTripKeepsUnknownKindsAndFields(t *testing.T) {
	var l HomeLayoutDoc
	if err := json.Unmarshal([]byte(phoneLayoutJSON), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Widgets) != 3 || l.Widgets[2].Kind != "weather" {
		t.Fatalf("widgets: %+v", l.Widgets)
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	_ = json.Unmarshal(b, &got)
	_ = json.Unmarshal([]byte(phoneLayoutJSON), &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("round trip changed the layout:\n got %s\nwant %s", gb, wb)
	}
}

func TestHomeWidgetAlwaysCarriesSizeAndConfig(t *testing.T) {
	// Swift's synthesized decoder drops a widget missing either.
	b, _ := json.Marshal(HomeWidget{ID: NewHomeWidgetID(), Kind: "hive"})
	s := string(b)
	if !strings.Contains(s, `"size":"small"`) || !strings.Contains(s, `"config":{}`) {
		t.Fatalf("widget json %s", s)
	}
	id := NewHomeWidgetID()
	if !homeUUID.MatchString(id) || id != strings.ToUpper(id) || id[14] != '4' {
		t.Fatalf("id %s is not an upper-case v4 UUID", id)
	}
}

func newTestStore(t *testing.T) *HomeLayoutStore {
	t.Helper()
	s := NewHomeLayoutStore(filepath.Join(t.TempDir(), HomeLayoutFile))
	clock := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return s
}

func phoneLayout(t *testing.T) *HomeLayoutDoc {
	t.Helper()
	var l HomeLayoutDoc
	if err := json.Unmarshal([]byte(phoneLayoutJSON), &l); err != nil {
		t.Fatal(err)
	}
	return &l
}

func TestHomeLayoutStoreVersionsAndRejectsStaleBase(t *testing.T) {
	s := newTestStore(t)
	st, err := s.Get()
	if err != nil || st.Version != 0 || st.Layout != nil {
		t.Fatalf("empty store: %+v %v", st, err)
	}
	st, conflict, err := s.Set(phoneLayout(t), 0, "phone")
	if err != nil || conflict || st.Version != 1 || st.UpdatedAt == nil || st.Source != "phone" {
		t.Fatalf("first set: %+v conflict=%v err=%v", st, conflict, err)
	}
	first := *st.UpdatedAt

	// The queen edits on top.
	st, err = s.Update("queen", func(cur *HomeLayoutDoc) (*HomeLayoutDoc, error) {
		next, _, err := ApplyHomeAction(cur, HomeAction{Action: "remove", ID: "00000000-0000-0000-0000-000000000003"}, nil)
		return next, err
	})
	if err != nil || st.Version != 2 || len(st.Layout.Widgets) != 2 || !st.UpdatedAt.After(first) {
		t.Fatalf("queen update: %+v %v", st, err)
	}

	// The phone, still on version 1, is told so and given version 2.
	st, conflict, err = s.Set(phoneLayout(t), 1, "phone")
	if err != nil || !conflict || st.Version != 2 || len(st.Layout.Widgets) != 2 {
		t.Fatalf("stale set: %+v conflict=%v err=%v", st, conflict, err)
	}

	// Back to the suggestion is a revision too.
	st, conflict, err = s.Set(nil, 2, "phone")
	if err != nil || conflict || st.Version != 3 || st.Layout != nil {
		t.Fatalf("reset: %+v conflict=%v err=%v", st, conflict, err)
	}

	// And it all survives a restart.
	again := NewHomeLayoutStore(s.path)
	st2, err := again.Get()
	if err != nil || st2.Version != 3 || st2.Layout != nil {
		t.Fatalf("reloaded: %+v %v", st2, err)
	}
}

func TestHomeLayoutStoreGetReturnsACopy(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.Set(phoneLayout(t), 0, "phone"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Get()
	st.Layout.Widgets[0].Config["symbol"] = "XXX"
	st2, _ := s.Get()
	if st2.Layout.Widgets[0].Config["symbol"] != "VOO" {
		t.Fatal("Get handed out the store's own layout")
	}
}

func TestValidateClientHomeLayout(t *testing.T) {
	bad := []string{
		`{"widgets":[{"id":"nope","kind":"hive","size":"small","config":{}}]}`,
		`{"widgets":[{"id":"00000000-0000-0000-0000-000000000001","kind":"","size":"small","config":{}}]}`,
		`{"widgets":[{"id":"00000000-0000-0000-0000-000000000001","kind":"hive","size":"small","config":{}},{"id":"00000000-0000-0000-0000-000000000001","kind":"time","size":"small","config":{}}]}`,
	}
	for _, b := range bad {
		var l HomeLayoutDoc
		if err := json.Unmarshal([]byte(b), &l); err != nil {
			t.Fatal(err)
		}
		if ValidateClientHomeLayout(&l) == nil {
			t.Errorf("accepted %s", b)
		}
	}
	if err := ValidateClientHomeLayout(phoneLayout(t)); err != nil {
		t.Fatalf("rejected the phone's own layout: %v", err)
	}
}

func idx(n int) *int { return &n }

func TestApplyHomeActionEditsFromTheDefault(t *testing.T) {
	check := func(s string) error {
		if s == "NOPE" {
			return ErrUnknownSymbol
		}
		return nil
	}
	l, note, err := ApplyHomeAction(nil, HomeAction{Action: "add", Kind: "quote", Size: "wide", Index: idx(0),
		Symbol: "btc", Has: map[string]bool{"symbol": true}}, check)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Widgets) != 5 || l.Widgets[0].Kind != "quote" || l.Widgets[0].Size != "wide" || l.Widgets[0].Config["symbol"] != "BTC" {
		t.Fatalf("add: %+v", l.Widgets)
	}
	if !strings.Contains(note, "default four") {
		t.Fatalf("note %q does not say it started from the default", note)
	}
	id := l.Widgets[0].ID

	l, _, err = ApplyHomeAction(l, HomeAction{Action: "move", ID: id[:8], Index: idx(99)}, nil)
	if err != nil || l.Widgets[4].ID != id {
		t.Fatalf("move to the end: %v %+v", err, l.Widgets)
	}
	l, _, err = ApplyHomeAction(l, HomeAction{Action: "resize", ID: strings.ToLower(id), Size: "small"}, nil)
	if err != nil || l.Widgets[4].Size != "small" {
		t.Fatalf("resize: %v", err)
	}
	l, _, err = ApplyHomeAction(l, HomeAction{Action: "configure", ID: "00000000-0000-0000-0000-000000000002",
		AssetClass: "crypto", Currency: "", Has: map[string]bool{"asset_class": true, "currency": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := l.Widgets[1].Config; c["assetClass"] != "crypto" || c["currency"] != nil {
		t.Fatalf("configure portfolio: %+v", c)
	}
	l, _, err = ApplyHomeAction(l, HomeAction{Action: "remove", ID: "00000000-0000-0000-0000-000000000004"}, nil)
	if err != nil || len(l.Widgets) != 4 {
		t.Fatalf("remove: %v", err)
	}
	l, _, err = ApplyHomeAction(l, HomeAction{Action: "reset"}, nil)
	if err != nil || l != nil {
		t.Fatalf("reset: %v %+v", err, l)
	}
}

func TestApplyHomeActionGeneratedGetsASpec(t *testing.T) {
	l, _, err := ApplyHomeAction(&HomeLayoutDoc{Version: 1}, HomeAction{Action: "add", Kind: "generated",
		Prompt: "这周跑了几公里", Has: map[string]bool{"prompt": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := l.Widgets[0]
	spec, _ := w.Config["spec"].(string)
	if w.Config["prompt"] != "这周跑了几公里" || !homeUUID.MatchString(spec) {
		t.Fatalf("generated config %+v", w.Config)
	}
	// A new prompt is a new spec; the same prompt keeps it.
	same, _, _ := ApplyHomeAction(l, HomeAction{Action: "configure", ID: w.ID, Prompt: "这周跑了几公里", Has: map[string]bool{"prompt": true}}, nil)
	if same.Widgets[0].Config["spec"] != spec {
		t.Fatal("the same prompt made a new spec")
	}
	other, _, _ := ApplyHomeAction(l, HomeAction{Action: "configure", ID: w.ID, Prompt: "这个月", Has: map[string]bool{"prompt": true}}, nil)
	if other.Widgets[0].Config["spec"] == spec {
		t.Fatal("a new prompt kept the old spec")
	}
}

func TestApplyHomeActionRejectsBadInput(t *testing.T) {
	base := DefaultHomeLayout()
	unknown := func(string) error { return ErrUnknownSymbol }
	offline := func(string) error { return errors.New("dial tcp: timeout") }
	cases := []struct {
		name  string
		act   HomeAction
		check HomeSymbolCheck
		want  string
	}{
		{"no action", HomeAction{}, nil, "action is required"},
		{"bad action", HomeAction{Action: "explode"}, nil, "unknown action"},
		{"bad kind", HomeAction{Action: "add", Kind: "weather"}, nil, "not one the phone draws"},
		{"bad size", HomeAction{Action: "add", Kind: "hive", Size: "huge"}, nil, "small"},
		{"quote without symbol", HomeAction{Action: "add", Kind: "quote"}, nil, "needs a symbol"},
		{"unknown symbol", HomeAction{Action: "add", Kind: "quote", Symbol: "ZZZZ", Has: map[string]bool{"symbol": true}}, unknown, "no market data"},
		{"bad symbol", HomeAction{Action: "add", Kind: "quote", Symbol: "a b;c", Has: map[string]bool{"symbol": true}}, nil, "ticker"},
		{"generated without prompt", HomeAction{Action: "add", Kind: "generated"}, nil, "needs a prompt"},
		{"foreign field", HomeAction{Action: "add", Kind: "hive", Symbol: "VOO", Has: map[string]bool{"symbol": true}}, nil, "does not apply"},
		{"bad space", HomeAction{Action: "configure", ID: "00000000-0000-0000-0000-000000000001", Space: "play", Has: map[string]bool{"space": true}}, nil, "work or life"},
		{"bad class", HomeAction{Action: "configure", ID: "00000000-0000-0000-0000-000000000002", AssetClass: "bonds", Has: map[string]bool{"asset_class": true}}, nil, "stocks, gold"},
		{"bad currency", HomeAction{Action: "configure", ID: "00000000-0000-0000-0000-000000000002", Currency: "dollars", Has: map[string]bool{"currency": true}}, nil, "three-letter"},
		{"no such id", HomeAction{Action: "remove", ID: "ABCDEF12"}, nil, "no widget"},
		{"ambiguous prefix", HomeAction{Action: "remove", ID: "00000000"}, nil, "more than one"},
		{"move without index", HomeAction{Action: "move", ID: "00000000-0000-0000-0000-000000000001"}, nil, "index is required"},
		{"resize without size", HomeAction{Action: "resize", ID: "00000000-0000-0000-0000-000000000001"}, nil, "size is required"},
	}
	for _, c := range cases {
		_, _, err := ApplyHomeAction(base, c.act, c.check)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
	// Offline: the symbol is kept as given, and the note says it was not checked.
	l, note, err := ApplyHomeAction(base, HomeAction{Action: "add", Kind: "quote", Symbol: "QQQM", Has: map[string]bool{"symbol": true}}, offline)
	if err != nil || l.Widgets[len(l.Widgets)-1].Config["symbol"] != "QQQM" || !strings.Contains(note, "Could not check") {
		t.Fatalf("offline add: %v %q", err, note)
	}
	// An empty field that is not the kind's own is ignored, not an error.
	if _, _, err := ApplyHomeAction(base, HomeAction{Action: "add", Kind: "hive", Has: map[string]bool{"symbol": true, "prompt": true}}, nil); err != nil {
		t.Fatalf("empty foreign fields: %v", err)
	}
	// The input layout is never changed in place.
	if len(base.Widgets) != 4 {
		t.Fatal("ApplyHomeAction changed its input")
	}
}

func TestHomeActionFromArgs(t *testing.T) {
	a := HomeActionFromArgs(map[string]any{"action": " Add ", "kind": "Quote", "index": float64(2), "symbol": " voo ", "prompt": nil, "size": "WIDE"})
	if a.Action != "add" || a.Kind != "quote" || a.Index == nil || *a.Index != 2 || a.Symbol != "voo" || !a.Has["symbol"] || a.Has["prompt"] || a.Size != "wide" {
		t.Fatalf("%+v", a)
	}
}

func TestHomeLayoutSectionOnlyWhereTheToolIs(t *testing.T) {
	if homeLayoutSection(HiveSettings{Role: HiveRoleWorker}) != "" {
		t.Fatal("a worker is told about home_layout")
	}
	if !strings.Contains(homeLayoutSection(HiveSettings{Role: HiveRoleQueen}), "home_layout") {
		t.Fatal("the queen is not told about home_layout")
	}
	if !QueenKeepsTool("home_layout") {
		t.Fatal("max delegation would take home_layout off the queen")
	}
}

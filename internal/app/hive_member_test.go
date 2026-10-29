package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// freePort is a port nothing listens on a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Membership is what is on disk: the address, the link, the name and a token
// file. Any of them missing is no membership.
func TestMembershipIsWhatIsOnDisk(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{}
	a.refreshMember()
	if a.isHiveMember() || loadHiveMember() != nil {
		t.Fatal("a member with nothing saved")
	}
	m := &hiveMember{URL: "https://hive.example", Link: "10.0.0.1:31744", Name: "mac", token: "secret"}
	if err := m.save(); err != nil {
		t.Fatal(err)
	}
	a.refreshMember()
	got := a.member.cur.Load()
	if !a.isHiveMember() || got.Name != "mac" || got.Link != "10.0.0.1:31744" || got.token != "secret" {
		t.Fatalf("loaded %+v", got)
	}
	if fi, err := os.Stat(hiveMemberTokenPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", fi, err)
	}
	raw, _ := os.ReadFile(hiveMemberPath())
	if strings.Contains(string(raw), "secret") {
		t.Fatal("the token is in the membership file")
	}
	if err := os.Remove(hiveMemberTokenPath()); err != nil {
		t.Fatal(err)
	}
	a.refreshMember()
	if a.isHiveMember() {
		t.Fatal("a membership with no token")
	}
}

// fakeHive is a queen's two doors: the claim over HTTP and the agent link,
// and what she was asked to forget.
type fakeHive struct {
	web      *httptest.Server
	core     *App
	unpaired atomic.Value
	stop     func()
}

func newFakeHive(t *testing.T) *fakeHive {
	t.Helper()
	f := &fakeHive{core: NewApp()}
	f.core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newAgentLinkServer(f.core, func(tok string) bool { return tok == "tok" })
	go func() { _ = s.Serve(lis) }()
	f.stop = s.Stop
	t.Cleanup(s.Stop)
	link := lis.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc(pairClaimPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Code string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Code != "123456" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"wrong code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"tok","device_id":"dev1","agent_link":"` + link + `"}`))
	})
	mux.HandleFunc("/api/rpc/UnpairDevice", func(w http.ResponseWriter, r *http.Request) {
		var args []string
		_ = json.NewDecoder(r.Body).Decode(&args)
		f.unpaired.Store(strings.Join(args, ","))
		_, _ = w.Write([]byte("null"))
	})
	mux.HandleFunc("/api/rpc/HiveStatus", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"role":"queen"}`)) })
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": hi\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f.web = httptest.NewServer(mux)
	t.Cleanup(f.web.Close)
	return f
}

// A Mac joins a hive: it is one of the queen's workers, the queen's duties
// here stop and stay stopped through a settings save, the window follows, and
// with the hive gone it says so and answers nothing itself. Leaving gives the
// duties back.
func TestJoiningAHiveMakesTheMacItsWorker(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("SUPERLEO_AGENT_DOOR_PORT", strconv.Itoa(freePort(t)))
	prev := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prev })

	hive := newFakeHive(t)
	mac := NewApp()
	mac.ctx = context.Background()
	mac.settings = &backend.Settings{}
	mac.ownTurnFn = func(string, string, string, string) {}
	t.Cleanup(func() { _, _ = mac.LeaveHive() })

	// Not in the hive yet it is still no queen: the window is a client.
	mac.startAlarmWatch()
	mac.startAgentDoor()
	if d := mac.localDuties(); len(d) != 0 {
		t.Fatalf("duties alone: %v", d)
	}

	if _, err := mac.JoinHive(hive.web.URL, "000000"); err == nil {
		t.Fatal("a wrong code joined")
	}
	if mac.isHiveMember() {
		t.Fatal("a member after a refused code")
	}
	st, err := mac.JoinHive(hive.web.URL, "123456")
	if err != nil || !st.Joined || st.Name == "" {
		t.Fatalf("join: %+v %v", st, err)
	}
	if _, err := mac.JoinHive(hive.web.URL, "123456"); err == nil {
		t.Fatal("joined twice")
	}
	waitFor(t, "the queen to see this Mac", func() bool { return hive.core.agents().get(st.Name) != nil })
	if r := hive.core.agentRole(hive.core.agents().get(st.Name)); r != agentRoleWorker {
		t.Fatalf("the Mac is a %s to the queen", r)
	}
	waitFor(t, "connected", func() bool { return mac.HiveMembership().Connected })

	// The queen's duties are off, and a settings save does not bring them back.
	if d := mac.localDuties(); len(d) != 0 {
		t.Fatalf("duties as a member: %v", d)
	}
	mac.startScheduler()
	mac.startTelegram()
	mac.startHive()
	mac.startAlarmWatch()
	mac.startAgentDoor()
	if _, err := mac.ensurePhoneLink(); err == nil {
		t.Fatal("a member paired phones")
	}
	if d := mac.localDuties(); len(d) != 0 {
		t.Fatalf("duties after the starts: %v", d)
	}
	// It never makes itself a queen, nor takes over when hers is away.
	mac.queenForWorkers("someone")
	if mac.GetSettings().Hive.Role != "" {
		t.Fatal("a member became a queen")
	}
	if _, self, ok := mac.lanElect(st.Name, map[string]bool{}); ok || self {
		t.Fatal("a member elected itself")
	}
	if _, err := mac.JoinHive("http://x", "123456"); err == nil {
		t.Fatal("a member joined a second hive")
	}

	// The window is on the queen.
	if l := mac.HiveLinkStatus(); !l.Linked || l.URL != hive.web.URL {
		t.Fatalf("window: %+v", l)
	}
	if got, err := mac.Remote("HiveStatus", nil); err != nil || got.(map[string]any)["role"] != "queen" {
		t.Fatalf("remote: %v %v", got, err)
	}
	if u, _ := mac.OpenInBrowser(); u != hive.web.URL {
		t.Fatalf("open in browser went to %q", u)
	}

	// It survives a restart: the file is the membership.
	again := NewApp()
	again.refreshMember()
	if !again.isHiveMember() {
		t.Fatal("the membership was not kept")
	}

	// The hive goes away.
	hive.stop()
	hive.web.CloseClientConnections()
	hive.web.Close()
	waitFor(t, "unreachable", func() bool { return mac.HiveMembership().Unreachable })
	if s := mac.HiveMembership(); !s.Joined || s.Connected || !strings.Contains(s.Error, "not reachable") {
		t.Fatalf("membership: %+v", s)
	}
	if _, err := mac.Remote("HiveStatus", nil); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("a call while the hive is away: %v", err)
	}
	if !mac.isHiveMember() || len(mac.localDuties()) != 0 {
		t.Fatal("the Mac fell back to being its own queen")
	}
}

// Leaving asks the hive to forget the Mac and gives the duties back.
func TestLeavingAHiveLeavesTheMacNoQueen(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("SUPERLEO_AGENT_DOOR_PORT", strconv.Itoa(freePort(t)))
	prev := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prev })
	hive := newFakeHive(t)
	mac := NewApp()
	mac.ctx = context.Background()
	mac.settings = &backend.Settings{}
	t.Cleanup(func() { mac.stopQueenDuties() })

	if _, err := mac.JoinHive(hive.web.URL, "123456"); err != nil {
		t.Fatal(err)
	}
	st, err := mac.LeaveHive()
	if err != nil || st.Joined || mac.isHiveMember() {
		t.Fatalf("leave: %+v %v", st, err)
	}
	if got, _ := hive.unpaired.Load().(string); got != "dev1" {
		t.Fatalf("the hive was told to forget %q", got)
	}
	if _, err := os.Stat(hiveMemberPath()); !os.IsNotExist(err) {
		t.Fatal("the membership file survived leaving")
	}
	if mac.HiveLinkStatus().Linked {
		t.Fatal("the window is still on the hive")
	}
	if d := mac.localDuties(); len(d) != 0 {
		t.Fatalf("duties after leaving: %v", d)
	}
	waitFor(t, "the queen to lose the Mac", func() bool { return hive.core.agents().get(st.Name) == nil && !mac.HiveMembership().Connected })
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// The old node's job is read, retired without touching its files, and what it
// joined with becomes the membership when nothing is left to ask the person.
func TestTheOldNodeIsRetiredAndBecomesTheMembership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUPERLEO_HOME", filepath.Join(home, ".superleo"))
	nodeDir := filepath.Join(home, ".superleo-node")
	if err := os.MkdirAll(nodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(nodeDir, "core-token"), []byte("tok\n"), 0o600)
	_ = os.WriteFile(filepath.Join(nodeDir, "settings.json"), []byte(`{"hive":{"role":"worker","name":"mac","join_url":"https://hive.example"}}`), 0o600)
	plist := nodeService.launchdPlist()
	_ = os.MkdirAll(filepath.Dir(plist), 0o755)
	_ = os.WriteFile(plist, []byte(`<plist><dict><key>EnvironmentVariables</key><dict>
    <key>SUPERLEO_AGENT_NAME</key><string>mac</string>
    <key>SUPERLEO_CORE</key><string>127.0.0.1:1</string>
    <key>SUPERLEO_CORE_TOKEN_FILE</key><string>`+filepath.Join(nodeDir, "core-token")+`</string>
    <key>SUPERLEO_HOME</key><string>`+nodeDir+`</string></dict></dict></plist>`), 0o600)

	var calls []string
	running := true
	prevL := launchctlOK
	launchctlOK = func(args ...string) bool {
		calls = append(calls, strings.Join(args, " "))
		return args[0] == "print" && running
	}
	t.Cleanup(func() { launchctlOK = prevL })
	prevW := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prevW })

	if machineHiveName() != "mac" {
		t.Fatalf("the Mac's name is %q", machineHiveName())
	}
	a := NewApp()
	a.settings = &backend.Settings{}
	t.Cleanup(func() { _, _ = a.LeaveHive() })
	st := a.OldNodeStatus()
	if !st.Present || !st.Running || st.Name != "mac" || st.Hive != "https://hive.example" {
		t.Fatalf("status: %+v", st)
	}
	// Still running: converting now would put two Macs called "mac" in the hive.
	if a.MigrateHiveLink().Joined {
		t.Fatal("converted while the old node was running")
	}

	running = false
	st, err := a.RetireOldNode()
	if err != nil || st.Present || !st.Retired {
		t.Fatalf("retire: %+v %v", st, err)
	}
	if !contains(calls, "bootout gui/"+strconv.Itoa(os.Getuid())+"/"+nodeLabel) {
		t.Fatalf("launchctl calls: %v", calls)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatal("the plist is still there")
	}
	if _, err := os.Stat(filepath.Join(nodeDir, "core-token")); err != nil {
		t.Fatal("the old home was touched")
	}
	if machineHiveName() != "mac" {
		t.Fatal("the name was lost with the plist")
	}
	got := a.MigrateHiveLink()
	if !got.Joined || got.Name != "mac" || got.Hive != "https://hive.example" || got.Link != "127.0.0.1:1" {
		t.Fatalf("migrate: %+v", got)
	}
	if m := loadHiveMember(); m == nil || m.token != "tok" {
		t.Fatal("the membership was not kept")
	}
}

// A hive the window was paired with, and nothing to join it by, asks for a code.
func TestAnOldLinkWithNoTokenToJoinByNeedsACode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUPERLEO_HOME", filepath.Join(home, ".superleo"))
	_ = os.MkdirAll(filepath.Join(home, ".superleo"), 0o700)
	if err := saveHiveBook(hiveBook{Active: "https://hive.example", Links: []hiveLink{{ID: "https://hive.example", URL: "https://hive.example", Token: "t"}}}); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if st := a.MigrateHiveLink(); st.Joined || !st.NeedsCode {
		t.Fatalf("migrate: %+v", st)
	}
	if loadHiveLink() == nil {
		t.Fatal("the saved link was lost")
	}
}

// The login item is a plist that opens the app hidden, and goes away again.
func TestTheLoginItemIsAPlistThatOpensTheAppHidden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := writeLoginItem(true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(loginItemPath())
	if err != nil || !strings.Contains(string(raw), HiddenArg) || !strings.Contains(string(raw), "<key>RunAtLoad</key><true/>") {
		t.Fatalf("plist: %s %v", raw, err)
	}
	if !strings.HasPrefix(loginItemPath(), home) {
		t.Fatalf("wrote outside the home: %s", loginItemPath())
	}
	if err := writeLoginItem(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(loginItemPath()); !os.IsNotExist(err) {
		t.Fatal("the login item stayed")
	}
	if StartHiddenWanted([]string{HiddenArg}) {
		t.Fatal("hidden start without keep-running")
	}
}

// Retry redials at once: the agent link and the window's stream start over, and
// the Mac stays a member. A Mac in no hive has nothing to redial.
func TestReconnectingRedialsTheHive(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("SUPERLEO_AGENT_DOOR_PORT", strconv.Itoa(freePort(t)))
	prev := windowEmit
	windowEmit = func(context.Context, string, map[string]any) {}
	t.Cleanup(func() { windowEmit = prev })

	hive := newFakeHive(t)
	mac := NewApp()
	mac.ctx = context.Background()
	mac.settings = &backend.Settings{}
	mac.ownTurnFn = func(string, string, string, string) {}
	t.Cleanup(func() { _, _ = mac.LeaveHive() })

	if st, err := mac.ReconnectHive(); err != nil || st.Joined {
		t.Fatalf("alone: %+v %v", st, err)
	}
	st, err := mac.JoinHive(hive.web.URL, "123456")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return mac.HiveMembership().Connected })
	first := hive.core.agents().get(st.Name)

	if _, err := mac.ReconnectHive(); err != nil {
		t.Fatal(err)
	}
	if !mac.isHiveMember() {
		t.Fatal("reconnecting left the hive")
	}
	waitFor(t, "the link to come back as a new connection", func() bool {
		c := hive.core.agents().get(st.Name)
		return c != nil && c != first && mac.HiveMembership().Connected
	})
}

// The conversations a Mac kept before joining stay readable from the process
// itself, minus the pieces it did for a queen; nothing is changed by reading.
func TestEarlierChatsAreLocalAndReadOnly(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := NewApp()
	if got := a.EarlierChats(); got == nil || len(got) != 0 {
		t.Fatalf("no service: %v", got)
	}
	if got := a.EarlierChat("x"); got == nil || len(got) != 0 {
		t.Fatalf("no service: %v", got)
	}
	if got := a.ThisMacAbilities(); got.Abilities == nil {
		t.Fatalf("abilities should be a list, not null: %+v", got)
	}
}

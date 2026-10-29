package app

// The Mac used to be in the cluster as a second SuperLeo: a `superleo serve`
// kept by launchd (~/.superleo-node), beside the desktop app. Now the app is
// the Mac's one agent, so that job is redundant, and running both would put
// two machines called "mac" in front of the queen.
//
// Nothing here runs by itself except the conversion in migrateHiveLink, and
// that only when it needs nothing from the person: OldNodeStatus looks,
// RetireOldNode stops the launchd jobs and removes their plists, and neither
// touches the files in the old homes.

import (
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

const oldNodeRecordFile = "old-node.json"

// launchctlOK runs launchctl and says whether it succeeded. A variable so a
// test never talks to the real launchd.
var launchctlOK = func(args ...string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	return exec.Command("launchctl", args...).Run() == nil
}

// OldNodeInfo is what is left of the old node and agent on this Mac. Never a
// token.
type OldNodeInfo struct {
	// Present is a launchd plist for the old node or agent that is still there.
	Present bool `json:"present"`
	// Running is one of the jobs loaded now.
	Running bool `json:"running"`
	// Retired is that they were stopped and removed by RetireOldNode.
	Retired bool `json:"retired"`
	// Name is what the cluster called this Mac; Hive and Core where it joined.
	Name string `json:"name,omitempty"`
	Hive string `json:"hive,omitempty"`
	Core string `json:"core,omitempty"`
	// Home is the old node's directory, left in place.
	Home string `json:"home,omitempty"`
}

// oldNodeConf is what the old node was started with.
type oldNodeConf struct {
	Name      string `json:"name"`
	Core      string `json:"core"`
	Hive      string `json:"hive"`
	TokenFile string `json:"token_file"`
	Home      string `json:"home"`
}

var plistEnv = regexp.MustCompile(`<key>([A-Z_]+)</key>\s*<string>([^<]*)</string>`)

// readOldNodeConf is the old node's settings, from its launchd plist if that
// is still there, else from the record RetireOldNode left.
func readOldNodeConf() (oldNodeConf, bool) {
	var c oldNodeConf
	if raw, err := os.ReadFile(nodeService.launchdPlist()); err == nil {
		env := map[string]string{}
		for _, m := range plistEnv.FindAllStringSubmatch(string(raw), -1) {
			env[m[1]] = html.UnescapeString(m[2])
		}
		c = oldNodeConf{Name: env["SUPERLEO_AGENT_NAME"], Core: env["SUPERLEO_CORE"],
			TokenFile: env["SUPERLEO_CORE_TOKEN_FILE"], Home: env["SUPERLEO_HOME"]}
	} else if raw, err := os.ReadFile(filepath.Join(backend.DataDir(), oldNodeRecordFile)); err == nil {
		if json.Unmarshal(raw, &c) != nil {
			return oldNodeConf{}, false
		}
	} else {
		return oldNodeConf{}, false
	}
	if c.Home == "" {
		c.Home = nodeHome()
	}
	// The node's own settings say its name and where it joined.
	if raw, err := os.ReadFile(filepath.Join(c.Home, "settings.json")); err == nil {
		var s struct {
			Hive struct {
				Name    string `json:"name"`
				JoinURL string `json:"join_url"`
			} `json:"hive"`
		}
		if json.Unmarshal(raw, &s) == nil {
			if s.Hive.Name != "" {
				c.Name = s.Hive.Name
			}
			if c.Hive == "" {
				c.Hive = s.Hive.JoinURL
			}
		}
	}
	return c, c.Name != "" || c.Core != "" || c.Hive != ""
}

// oldNodeName is the name the cluster knows this Mac by, if it did.
func oldNodeName() string {
	c, _ := readOldNodeConf()
	return c.Name
}

func serviceLoaded(sv loginService) bool {
	return launchctlOK("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), sv.label))
}

func servicePresent(sv loginService) bool {
	_, err := os.Stat(sv.launchdPlist())
	return err == nil
}

// OldNodeStatus looks at what is left of the old node and agent here.
func (a *App) OldNodeStatus() OldNodeInfo {
	var out OldNodeInfo
	for _, sv := range []loginService{nodeService, agentService} {
		if servicePresent(sv) {
			out.Present = true
		}
		if serviceLoaded(sv) {
			out.Running = true
		}
	}
	c, ok := readOldNodeConf()
	if ok {
		out.Name, out.Hive, out.Core, out.Home = c.Name, c.Hive, c.Core, c.Home
	}
	_, err := os.Stat(filepath.Join(backend.DataDir(), oldNodeRecordFile))
	out.Retired = err == nil && !out.Present
	return out
}

// RetireOldNode stops the old node and agent's launchd jobs and removes their
// plists, so they do not start again. Their directories stay. What the node
// was joined with is kept in the app's own home first, so the Mac can still
// become a member of the same hive without a new pairing code.
func (a *App) RetireOldNode() (OldNodeInfo, error) {
	if c, ok := readOldNodeConf(); ok && servicePresent(nodeService) {
		raw, _ := json.MarshalIndent(c, "", "  ")
		if err := os.MkdirAll(backend.DataDir(), 0o700); err != nil {
			return OldNodeInfo{}, err
		}
		if err := os.WriteFile(filepath.Join(backend.DataDir(), oldNodeRecordFile), raw, 0o600); err != nil {
			return OldNodeInfo{}, err
		}
	}
	for _, sv := range []loginService{nodeService, agentService} {
		if !servicePresent(sv) {
			continue
		}
		launchctlOK("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), sv.label))
		if err := os.Remove(sv.launchdPlist()); err != nil && !os.IsNotExist(err) {
			return a.OldNodeStatus(), err
		}
	}
	return a.OldNodeStatus(), nil
}

// MigrateHiveLink turns what this Mac was linked to before into a membership,
// when that takes nothing from the person: the old node's own address, name
// and token are on disk, the old node is no longer running, and the hive it
// joined is the one this window was linked to (or the window was linked to
// none). Otherwise nothing changes and the answer says why: NeedsCode.
func (a *App) MigrateHiveLink() HiveMembership {
	ms := &a.member
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if a.isHiveMember() {
		return a.HiveMembership()
	}
	st := a.HiveMembership()
	c, ok := readOldNodeConf()
	if !ok || c.Core == "" || c.Name == "" || c.TokenFile == "" || !validLinkName(c.Name) {
		return st
	}
	if serviceLoaded(nodeService) || serviceLoaded(agentService) {
		// Two Macs called the same name would fight over the queen.
		return st
	}
	tok, err := os.ReadFile(c.TokenFile)
	if err != nil || strings.TrimSpace(string(tok)) == "" {
		return st
	}
	hiveURL := c.Hive
	old := loadHiveLink()
	if hiveURL == "" && old != nil {
		hiveURL = old.URL
	}
	if hiveURL == "" {
		return st
	}
	base, err := hiveAddress(hiveURL)
	if err != nil {
		return st
	}
	// A window linked to some other hive is that hive's business: not
	// replaced behind its back.
	if old != nil && old.URL != base && hostOf(old.URL) != hostOf(base) {
		return st
	}
	m := &hiveMember{URL: base, Link: c.Core, Name: c.Name, JoinedAt: time.Now().UTC(), token: strings.TrimSpace(string(tok))}
	m.TLS = strings.HasSuffix(strings.Split(c.Core, ",")[0], ":443")
	if old != nil && old.URL == base {
		m.DeviceID = old.DeviceID
	}
	if err := m.save(); err != nil {
		return st
	}
	a.enterHive(m)
	// One hive is saved: the earlier link is that hive.
	if old != nil {
		b := loadHiveBook()
		if i := b.find(old.ID); i >= 0 {
			b.Links = append(b.Links[:i], b.Links[i+1:]...)
		}
		b.Active = ""
		_ = saveHiveBook(b)
	}
	return a.HiveMembership()
}

// migrateHiveLink puts this Mac in the hive at the app's start, from what it
// already has, with nothing asked of the person. The window is a client of
// the one hive, so the old node beside it is stopped and its place taken
// (MigrateHiveLink). A Mac with no old node but a window linked to the hive
// asks the hive for a pairing code with that link and joins by it.
func (a *App) migrateHiveLink() {
	// Stopped even for a member: under the same name the two would take the
	// queen's link from each other, and neither would stay up.
	if _, ok := readOldNodeConf(); ok && (serviceLoaded(nodeService) || serviceLoaded(agentService)) {
		if _, err := a.RetireOldNode(); err != nil {
			slog.Warn("hive: stopping the old node", "error", err)
		}
	}
	if a.isHiveMember() {
		return
	}
	if a.MigrateHiveLink().Joined {
		return
	}
	if err := a.joinByLink(); err != nil {
		slog.Warn("hive: joining by the window's link", "error", err)
	}
}

// joinByLink joins the hive the window is linked to, with a pairing code the
// hive makes for that link.
func (a *App) joinByLink() error {
	l := loadHiveLink()
	if l == nil {
		return nil
	}
	out, err := callQueen(*l, "AgentJoinCode", nil)
	if err != nil {
		return err
	}
	j, _ := out.(map[string]any)
	code, _ := j["code"].(string)
	if code == "" {
		msg, _ := j["error"].(string)
		return fmt.Errorf("the hive made no pairing code: %s", msg)
	}
	_, err = a.JoinHive(l.URL, code)
	return err
}

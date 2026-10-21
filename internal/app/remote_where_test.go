package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
)

// The openclaw cluster as the settings reach it from a Mac on its own:
// openclaw, hermes and pi each on the same three hosts with the same probe,
// running on whichever node holds the resource. One host is given by an
// address and is a machine already linked under its own name.
func clusterCore(t *testing.T) *App {
	t.Helper()
	hosts := []string{"sds@192.168.123.212", "sds@192.168.123.227", "-F ~/.lima/sds-a/ssh.config lima-sds-a"}
	agents := map[string]backend.RemoteAgent{}
	for _, n := range []string{"openclaw", "hermes", "pi"} {
		agents[n] = backend.RemoteAgent{About: n, Hosts: hosts, Probe: "systemctl is-active --quiet openclaw", Command: []string{n, "{prompt}"}}
	}
	core := NewApp()
	core.settings = &backend.Settings{RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: agents}}
	addr := startCore(t, core)
	// sds-e's own SuperAI, with a CLI that works on its own account.
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "node-e", Host: "sds-e", Os: "linux", Kind: "vm", Clis: []string{"codex"}})
	return core
}

func stubHostnames(core *App) {
	core.remoteWhere.hostname = func(_ context.Context, dest string) (string, error) {
		switch sshHost(dest) {
		case "192.168.123.212":
			return "sds-e", nil
		case "192.168.123.227":
			return "sds-b", nil
		case "lima-sds-a":
			return "lima-sds-a", nil
		}
		return "", errors.New("unreachable")
	}
}

// where lists each named agent the apps draw with the host it stands on.
func where(hs []Host) map[string][]string {
	out := map[string][]string{}
	for _, h := range hs {
		for _, g := range h.Agents {
			if g.Kind == hostAgentNamed {
				out[g.Name] = append(out[g.Name], h.ID+":"+g.State)
			}
		}
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out
}

func noAddressHosts(t *testing.T, hs []Host) {
	t.Helper()
	for _, h := range hs {
		if isIP(h.ID) {
			t.Fatalf("a plot for the ssh address %s: %+v", h.ID, h)
		}
	}
}

// Running on sds-b: each agent is drawn once, there and running; the standby
// nodes get no copy; the destination given by address is the machine that
// says its name, one plot with the linked sds-e. The queen's full list still
// has every destination, the standbys marked so, and pi.sds-a still reaches
// the Lima VM.
func TestAgentThatFollowsOneHostIsDrawnWhereItRuns(t *testing.T) {
	core := clusterCore(t)
	stubHostnames(core)
	probes := 0
	core.remoteWhere.locate = func(_ context.Context, name string) (string, error) {
		probes++
		return "sds@192.168.123.227", nil
	}
	core.fullTopology() // starts the first look
	waitFor(t, "the agents located", func() bool {
		return len(where(core.HiveTopology().Hosts)["pi"]) == 1 && where(core.HiveTopology().Hosts)["pi"][0] == "sds-b:live"
	})
	if probes != 1 {
		t.Fatalf("one resource, %d lookups", probes)
	}

	apps := core.HiveTopology().Hosts
	want := map[string][]string{"hermes": {"sds-b:live"}, "openclaw": {"sds-b:live"}, "pi": {"sds-b:live"}}
	if got := where(apps); len(got) != 3 || strings.Join(got["openclaw"], ",") != "sds-b:live" ||
		strings.Join(got["hermes"], ",") != "sds-b:live" || strings.Join(got["pi"], ",") != "sds-b:live" {
		t.Fatalf("drawn %v, want %v", got, want)
	}
	noAddressHosts(t, apps)
	b := hostByName(apps, "sds-b")
	if b == nil || b.State != "live" || len(b.Addresses) != 1 || b.Addresses[0] != "192.168.123.227" {
		t.Fatalf("sds-b: %+v", b)
	}
	n := 0
	for _, h := range apps {
		if hostIs(&h, "sds-e", false) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("sds-e drawn %d times: %+v", n, apps)
	}
	if hostByName(apps, "lima-sds-a") != nil {
		t.Fatal("a standby with nothing on it is drawn")
	}

	full := core.fullTopology().Hosts
	noAddressHosts(t, full)
	if got := where(full)["pi"]; strings.Join(got, ",") != "lima-sds-a:standby,sds-b:live,sds-e:standby" {
		t.Fatalf("pi on the queen's list: %v", got)
	}
	if e := hostByName(full, "node-e"); e == nil || len(e.Addresses) != 1 || e.Addresses[0] != "192.168.123.212" {
		t.Fatalf("sds-e, linked and reached over ssh: %+v", e)
	}
	for machine, dest := range map[string]string{
		"sds-a": "-F ~/.lima/sds-a/ssh.config lima-sds-a",
		"sds-b": "sds@192.168.123.227",
		"sds-e": "sds@192.168.123.212",
	} {
		if got := core.sshDestOn("pi", machine); got != dest {
			t.Fatalf("pi on %s goes to %q, want %q", machine, got, dest)
		}
	}
}

// On none of its hosts, or before anyone has looked: drawn once, on its
// first host, not running; never on all three.
func TestAgentThatFollowsOneHostIsDrawnOnceWhenNotRunning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		locate func(context.Context, string) (string, error)
	}{
		{"not looked for yet", nil},
		{"on no host", func(context.Context, string) (string, error) { return "", errors.New("not running") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := clusterCore(t)
			if tc.locate != nil {
				stubHostnames(core)
				core.remoteWhere.locate = tc.locate
				core.fullTopology()
				waitFor(t, "the hostnames", func() bool { return hostByName(core.fullTopology().Hosts, "sds-b") != nil })
			}
			apps := core.HiveTopology().Hosts
			got := where(apps)
			for _, n := range []string{"openclaw", "hermes", "pi"} {
				if len(got[n]) != 1 || !strings.HasSuffix(got[n][0], ":configured") {
					t.Fatalf("%s drawn as %v", n, got[n])
				}
			}
			// The first host is sds-e, by its address, once its name is known.
			if tc.locate != nil && got["pi"][0] != "sds-e:configured" {
				t.Fatalf("pi drawn on %v, want its first host", got["pi"])
			}
			if tc.locate != nil {
				noAddressHosts(t, apps)
			}
		})
	}
}

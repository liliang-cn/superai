package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
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
	// sds-e's own SuperLeo, with a CLI that works on its own account.
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
		return strings.Join(where(core.HiveTopology().Hosts)["pi"], ",") == "ha:openclaw:live"
	})
	if probes != 1 {
		t.Fatalf("one resource, %d lookups", probes)
	}

	// The apps draw the cluster: the three together, once each, running,
	// on sds-b now; sds-b, holding nothing else, is not a plot of its own.
	apps := core.HiveTopology().Hosts
	want := "ha:openclaw:live"
	if got := where(apps); len(got) != 3 || strings.Join(got["openclaw"], ",") != want ||
		strings.Join(got["hermes"], ",") != want || strings.Join(got["pi"], ",") != want {
		t.Fatalf("drawn %v, want each on %s", got, want)
	}
	noAddressHosts(t, apps)
	c := hostByName(apps, "openclaw")
	if c == nil || c.Kind != hostKindHA || c.State != "live" || c.On != "sds-b" {
		t.Fatalf("the cluster: %+v", c)
	}
	if hostByName(apps, "sds-b") != nil {
		t.Fatal("an HA node with nothing else on it is drawn")
	}
	if b := hostByName(core.fullTopology().Hosts, "sds-b"); b == nil || b.State != "live" || len(b.Addresses) != 1 || b.Addresses[0] != "192.168.123.227" {
		t.Fatalf("sds-b on the queen's list: %+v", b)
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

// On none of its hosts, or before anyone has looked: drawn once, on the
// cluster, not running; never on all three.
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
				if strings.Join(got[n], ",") != "ha:openclaw:configured" {
					t.Fatalf("%s drawn as %v", n, got[n])
				}
			}
			if c := hostByName(apps, "openclaw"); c == nil || c.State != "configured" || c.On != "" {
				t.Fatalf("the cluster: %+v", c)
			}
			if tc.locate != nil {
				noAddressHosts(t, apps)
			}
		})
	}
}

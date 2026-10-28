package app

import (
	"context"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// Where the agents the settings reach over ssh are, for the hosts list.
//
// An agent that follows one host at a time (openclaw, hermes and pi on the
// openclaw cluster: several hosts, a probe) runs on the host its probe
// succeeds on, and is drawn there alone. A destination given by address
// (sds@192.168.123.227) is a machine that may already be drawn under its own
// name (sds-b), so each destination's hostname is asked once. Both take ssh
// round trips, so they are looked up in the background, like the HA services,
// and listing hosts reads what was last found.

// remoteWhereRefresh is how old the answers may get before the next listing
// looks again. The runner believes a located host for longer (remoteHostTTL),
// so most refreshes cost no ssh at all.
const remoteWhereRefresh = 30 * time.Second

// remoteNameRetry is how long a destination whose hostname could not be read
// is left before it is asked again: a standby that is down stays down a while.
const remoteNameRetry = 10 * time.Minute

type remoteWhere struct {
	mu   sync.Mutex
	at   time.Time
	busy bool
	// key is the settings the answers are for; a change starts over.
	key string
	// active is, for each agent that follows one host, the destination it
	// runs on now, "" when it runs on none. Absent until it is looked for.
	active map[string]string
	// names is each destination's machine's own hostname, where it said.
	names  map[string]string
	failed map[string]time.Time
	// locate and hostname do the looking; nil (in tests, and until boot)
	// means nothing is looked up.
	locate   func(ctx context.Context, name string) (string, error)
	hostname func(ctx context.Context, dest string) (string, error)
}

// remoteSpots is what the hosts list reads: where each agent that follows one
// host runs now (absent: not known yet; "": on none), and each destination's
// hostname.
type remoteSpots struct {
	at     time.Time
	active map[string]string
	names  map[string]string
}

// lookForRemoteAgents switches the lookups on, the way a running app has
// them: through the remote runner, which remembers what it found for the
// next delegation too.
func (a *App) lookForRemoteAgents() {
	w := &a.remoteWhere
	w.mu.Lock()
	defer w.mu.Unlock()
	w.locate = func(ctx context.Context, name string) (string, error) { return a.remoteRunner().Locate(ctx, name) }
	w.hostname = backend.RemoteHostname
}

// sshDests is the destinations of the agents the settings reach over ssh that
// say which machine they are: not this one, not a forwarded port on it.
func sshDests(ra backend.RemoteAgent) []string {
	var out []string
	for _, d := range ra.Hosts {
		hn := sshHost(d)
		if hn == "" || backend.IsLocalHost(hn) || loopback(hn) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// remoteSpotsNow is what was last found, starting a fresh look in the
// background when it is stale. Agents an HA service runs are left to it.
func (a *App) remoteSpotsNow(s *backend.Settings, services []backend.HAService) remoteSpots {
	follow := map[string]backend.RemoteAgent{}
	var dests []string
	if s != nil && s.RemoteAgents.Enabled {
		for _, n := range s.RemoteAgents.Names() {
			ra := s.RemoteAgents.Agents[n]
			if ra.URL != "" {
				continue
			}
			dests = append(dests, sshDests(ra)...)
			if ra.FollowsOneHost() && !haServes(services, n) {
				follow[n] = ra
			}
		}
	}
	key := remoteWhereKey(follow, dests)

	w := &a.remoteWhere
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.key != key {
		w.key, w.active, w.at = key, nil, time.Time{}
	}
	out := remoteSpots{at: w.at, active: map[string]string{}, names: map[string]string{}}
	for n, d := range w.active {
		out.active[n] = d
	}
	for d, n := range w.names {
		out.names[d] = n
	}
	if w.locate != nil && w.hostname != nil && !w.busy && len(dests) > 0 && time.Since(w.at) > remoteWhereRefresh {
		w.busy = true
		go a.refreshRemoteWhere(key, follow, dests)
	}
	return out
}

func remoteWhereKey(follow map[string]backend.RemoteAgent, dests []string) string {
	names := make([]string, 0, len(follow))
	for n := range follow {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\x00" + strings.Join(follow[n].Hosts, "\x01") + "\x00" + follow[n].Probe + "\n")
	}
	ds := append([]string(nil), dests...)
	sort.Strings(ds)
	b.WriteString(strings.Join(ds, "\x01"))
	return b.String()
}

// refreshRemoteWhere finds where each agent runs and asks each destination
// its hostname, then says so when anything moved. Agents with the same hosts
// and probe are one lookup: on the cluster they are one resource.
func (a *App) refreshRemoteWhere(key string, follow map[string]backend.RemoteAgent, dests []string) {
	w := &a.remoteWhere
	w.mu.Lock()
	locate, hostname := w.locate, w.hostname
	known := map[string]bool{}
	for d := range w.names {
		known[d] = true
	}
	retry := map[string]bool{}
	for d, at := range w.failed {
		if time.Since(at) < remoteNameRetry {
			retry[d] = true
		}
	}
	w.mu.Unlock()

	names := make([]string, 0, len(follow))
	for n := range follow {
		names = append(names, n)
	}
	sort.Strings(names)
	active := map[string]string{}
	group := map[string]string{}
	for _, n := range names {
		ra := follow[n]
		g := strings.Join(ra.Hosts, "\x01") + "\x00" + ra.Probe
		if d, ok := group[g]; ok {
			active[n] = d
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		d, err := locate(ctx, n)
		cancel()
		if err != nil {
			d = ""
		}
		group[g], active[n] = d, d
	}

	got, failed := map[string]string{}, map[string]time.Time{}
	for _, d := range dests {
		if known[d] || retry[d] || got[d] != "" {
			continue
		}
		if _, ok := failed[d]; ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := hostname(ctx, d)
		cancel()
		if err != nil {
			failed[d] = time.Now()
			continue
		}
		got[d] = n
	}

	w.mu.Lock()
	changed := false
	w.busy, w.at = false, time.Now()
	if w.key == key {
		changed = !reflect.DeepEqual(active, w.active)
		w.active = active
	}
	if w.names == nil {
		w.names = map[string]string{}
	}
	if w.failed == nil {
		w.failed = map[string]time.Time{}
	}
	for d, n := range got {
		if w.names[d] != n {
			changed = true
		}
		w.names[d] = n
		delete(w.failed, d)
	}
	for d, at := range failed {
		w.failed[d] = at
	}
	w.mu.Unlock()
	if changed {
		a.emit("hosts:changed", nil)
	}
}

// sshHostOf is the host already drawn that an ssh destination means: by the
// machine part of it, else by the hostname the machine gave.
func (b *hostBook) sshHostOf(machine, hostname string) *Host {
	if x := b.find(machine); x != nil {
		return x
	}
	if hostname != "" {
		return b.find(hostname)
	}
	return nil
}

// isIP says whether a destination's machine part is an address.
func isIP(h string) bool { return net.ParseIP(strings.Trim(h, "[]")) != nil }

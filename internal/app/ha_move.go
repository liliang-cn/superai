package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// moveHA moves an HA service (named as the service or as an agent it keeps
// up, like openclaw) to another of its hosts and says where it went. The
// world then sees the agent on its new host, and draws it walking there.
func (a *App) moveHA(ctx context.Context, name string) (string, error) {
	src := a.haSource()
	if !src.Enabled() {
		return "", errors.New("no haify controller is configured, so there is no HA service to move")
	}
	name = strings.TrimSpace(name)
	svc, ok := a.findHA(name)
	if !ok {
		var names []string
		for _, s := range a.haServices() {
			names = append(names, s.Name)
		}
		return "", fmt.Errorf("%q is not an HA service; the HA services are: %s", name, strings.Join(names, ", "))
	}
	if svc.Name == "haify-meta" {
		return "", errors.New("haify-meta is haify's own controller: moving it cuts this call off, so move it by hand (haify ha evict haify-meta)")
	}
	ready := 0
	for _, b := range svc.Standbys {
		if b.Ready {
			ready++
		}
	}
	if ready == 0 {
		return "", fmt.Errorf("%s has no standby ready to take over, so moving it would only stop it", svc.Name)
	}
	from := svc.Active
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	msg, err := backend.HaifyEvict(ctx, src, svc.Name, nil)
	if err != nil {
		return "", err
	}
	// Read where it runs now, so the answer and the world agree.
	a.refreshHA(src)
	to := ""
	if now, ok := a.findHA(svc.Name); ok {
		to = now.Active
	}
	switch {
	case to != "" && to != from:
		return fmt.Sprintf("%s moved from %s to %s.", svc.Name, from, to), nil
	case msg != "":
		return fmt.Sprintf("%s: %s", svc.Name, msg), nil
	default:
		return fmt.Sprintf("%s was moved off %s.", svc.Name, from), nil
	}
}

// findHA is the HA service by its name, by a unit it starts (openclaw for
// openclaw.service), or by an agent drawn as belonging to it.
func (a *App) findHA(name string) (backend.HAService, bool) {
	svcs := a.haServices()
	for _, s := range svcs {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	for _, s := range svcs {
		for _, u := range s.Units {
			if strings.EqualFold(strings.TrimSuffix(u, ".service"), name) {
				return s, true
			}
		}
	}
	for _, h := range a.HiveTopology().Hosts {
		for _, ag := range h.Agents {
			if strings.EqualFold(ag.Name, name) && ag.Service != "" {
				for _, s := range svcs {
					if s.Name == ag.Service {
						return s, true
					}
				}
			}
		}
	}
	return backend.HAService{}, false
}

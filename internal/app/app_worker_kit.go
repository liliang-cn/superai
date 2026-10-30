package app

// Each worker's own MCP servers and skills (backend/hive_kit.go): the worker
// says what it has, and the queen's methods here let a worker's card in the
// app look at them, add to them and take from them — on that worker, so each
// pod is set up on its own.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/superleo/internal/backend"
)

// workerKitTimeout bounds one call on a worker for its kit. Starting an MCP
// server is part of adding it, and one fetched on first start takes a while.
const workerKitTimeout = 2 * time.Minute

// HiveKit is this instance's MCP servers and skills, its own told apart from
// the cluster's and the queen's.
func (a *App) HiveKit() backend.WorkerKit {
	own := backend.OwnMCPServers()
	kit := backend.WorkerKit{Worker: a.hiveName(), MCP: []backend.KitItem{}, Skills: backend.SkillFolders()}
	if kit.Skills == nil {
		kit.Skills = []backend.KitItem{}
	}
	for _, st := range a.MCP() {
		item := backend.KitItem{Name: st.Name, About: st.Description, OK: st.Running, Own: own[st.Name]}
		for _, t := range st.Tools {
			item.Tools = append(item.Tools, t.Name)
		}
		kit.MCP = append(kit.MCP, item)
	}
	sort.Slice(kit.MCP, func(i, j int) bool { return kit.MCP[i].Name < kit.MCP[j].Name })
	return kit
}

// InstallSkillFiles takes a skill sent from elsewhere (AddWorkerSkill) as this
// instance's own.
func (a *App) InstallSkillFiles(name string, files map[string]string) string {
	if err := backend.InstallSkillFiles(name, files); err != nil {
		return err.Error()
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc != nil {
		svc.ReloadSkills(context.Background())
	}
	a.announceNow()
	return "ok"
}

// announceNow says hello to the queen at once, on a worker, so what it can do
// now is what she routes by, not what it could do at the last beat.
func (a *App) announceNow() {
	a.mu.Lock()
	ann := a.hiveAnn
	a.mu.Unlock()
	if ann == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = ann.Once(ctx)
	}()
}

// WorkerKit is one worker's MCP servers and skills, asked of the worker.
func (a *App) WorkerKit(worker string) (backend.WorkerKit, error) {
	var kit backend.WorkerKit
	ctx, cancel := context.WithTimeout(context.Background(), workerKitTimeout)
	defer cancel()
	if err := a.remoteRunner().CallWorker(ctx, strings.TrimSpace(worker), "HiveKit", nil, &kit); err != nil {
		return backend.WorkerKit{}, err
	}
	kit.Worker = strings.TrimSpace(worker)
	return kit, nil
}

// AddWorkerMCP adds an MCP server to one worker only, started there now and
// kept in its own settings, once the person allows it on an approval card.
func (a *App) AddWorkerMCP(worker, name, command string, args []string, env map[string]string) (backend.WorkerKit, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(command) == "" {
		return backend.WorkerKit{}, errors.New("a name and a command are needed")
	}
	line := strings.TrimSpace(strings.Join(append([]string{strings.TrimSpace(command)}, args...), " "))
	if err := a.approveWorkerMCP(worker, name, line, map[string]any{"name": name, "command": command, "args": args, "env": maskedValues(env)}); err != nil {
		return backend.WorkerKit{}, err
	}
	if err := a.workerSaysOK(worker, "InstallMCPServer", []any{strings.TrimSpace(name), strings.TrimSpace(command), args, env}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
}

// AddWorkerMCPURL adds an MCP server reached by URL — transport "http"
// (streamable HTTP) or "sse" — to one worker only, with the headers it wants,
// a token among them; asked on an approval card first, like AddWorkerMCP.
func (a *App) AddWorkerMCPURL(worker, name, transport, url string, headers map[string]string) (backend.WorkerKit, error) {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	if name == "" || url == "" {
		return backend.WorkerKit{}, errors.New("a name and a URL are needed")
	}
	kind, err := backend.MCPTransport(transport)
	if err != nil {
		return backend.WorkerKit{}, err
	}
	if err := a.approveWorkerMCP(worker, name, url+" ("+kind+")", map[string]any{"name": name, "url": url, "transport": kind, "headers": maskedValues(headers)}); err != nil {
		return backend.WorkerKit{}, err
	}
	if err := a.workerSaysOK(worker, "InstallMCPURLServer", []any{name, kind, url, headers}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
}

// approveWorkerMCP puts adding a server to a worker to the person on an
// approval card, as the agent's own add_mcp_server is: YOLO and a remembered
// rule for that worker answer it as they answer any card.
func (a *App) approveWorkerMCP(worker, name, what string, args map[string]any) error {
	worker = strings.TrimSpace(worker)
	if _, ok := a.remoteRunner().Workers()[worker]; !ok {
		return fmt.Errorf("%s is not a worker of this hive", worker)
	}
	now := time.Now()
	wait := backend.DefaultApprovalWait
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	dec, _ := a.askToolApproval(ctx, backend.ApprovalRequest{
		ID: uuid.NewString(), Tool: "add_mcp_server", Machine: worker,
		Command: fmt.Sprintf("Add MCP server %s to %s: %s", strings.TrimSpace(name), worker, what),
		Args:    args, AskedAt: now, ExpiresAt: now.Add(wait),
	})
	if !dec.Allowed {
		if dec.By == backend.DecidedByUser {
			return fmt.Errorf("adding %s to %s was not allowed", strings.TrimSpace(name), worker)
		}
		return fmt.Errorf("adding %s to %s was not approved in time", strings.TrimSpace(name), worker)
	}
	return nil
}

var maskedRefOnly = regexp.MustCompile(`^(Bearer )?\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// maskedValues is a header or environment map as a card shows it: each value
// cut to its last four characters, a ${NAME} it names left as it is. The card
// goes to the phones and the audit log; the token does not.
func maskedValues(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch {
		case maskedRefOnly.MatchString(v):
			out[k] = v
		case len(v) <= 4:
			out[k] = "••••"
		default:
			out[k] = "••••" + v[len(v)-4:]
		}
	}
	return out
}

// RemoveWorkerMCP takes one of a worker's own MCP servers off it.
func (a *App) RemoveWorkerMCP(worker, name string) (backend.WorkerKit, error) {
	if err := a.workerSaysOK(worker, "RemoveMCPServer", []any{strings.TrimSpace(name)}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
}

// AddWorkerSkill gives one worker a skill of its own: the folder at
// sourcePath where this app runs (SearchSkills' path), or the queen's own
// skill of that name when sourcePath is empty.
func (a *App) AddWorkerSkill(worker, name, sourcePath string) (backend.WorkerKit, error) {
	name = strings.TrimSpace(name)
	dir := strings.TrimSpace(sourcePath)
	if dir == "" {
		dir = filepath.Join(backend.DataDir(), "skills", name)
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir) // the SKILL.md itself
	}
	files := backend.ReadSkillFiles(dir)
	if _, ok := files["SKILL.md"]; !ok {
		return backend.WorkerKit{}, fmt.Errorf("no SKILL.md in %s", dir)
	}
	if err := a.workerSaysOK(worker, "InstallSkillFiles", []any{name, files}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
}

// RemoveWorkerSkill takes one of a worker's own skills off it.
func (a *App) RemoveWorkerSkill(worker, name string) (backend.WorkerKit, error) {
	if err := a.workerSaysOK(worker, "RemoveSkill", []any{strings.TrimSpace(name)}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
}

// workerSaysOK calls one of a worker's methods that answer "ok" or what went
// wrong.
func (a *App) workerSaysOK(worker, method string, args []any) error {
	ctx, cancel := context.WithTimeout(context.Background(), workerKitTimeout)
	defer cancel()
	var said string
	if err := a.remoteRunner().CallWorker(ctx, strings.TrimSpace(worker), method, args, &said); err != nil {
		return err
	}
	if said != "ok" {
		return fmt.Errorf("%s: %s", strings.TrimSpace(worker), said)
	}
	return nil
}

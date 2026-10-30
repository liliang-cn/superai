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
	"sort"
	"strings"
	"time"

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
// kept in its own settings.
func (a *App) AddWorkerMCP(worker, name, command string, args []string, env map[string]string) (backend.WorkerKit, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(command) == "" {
		return backend.WorkerKit{}, errors.New("a name and a command are needed")
	}
	if err := a.workerSaysOK(worker, "InstallMCPServer", []any{strings.TrimSpace(name), strings.TrimSpace(command), args, env}); err != nil {
		return backend.WorkerKit{}, err
	}
	return a.WorkerKit(worker)
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

package app

// The coding tools on the settings page: the CLIs SuperLeo knows (claude,
// codex, cursor-agent, and whatever else agentexec drives that is installed
// here), and the ones a person added by command line (backend/codingtool.go).
//
// A user-added tool is an agentexec text provider registered beside the
// discovered ones, so every way a coding agent is reached — coding_agent_run,
// an @mention, "name.machine" from core — reaches it the same way, through
// startCLIRun: in the workspace or a configured root, under the same timeout,
// behind the same approval gate, its output streamed into the run.
//
// Tools live on the machine that runs them. "Which machine" on the page picks
// whose settings are written: here, or another SuperLeo linked by URL, whose
// own RPCs are called with its token. An agent connected over the agent link
// is not offered: that link gives core the agent's hands, not its settings
// (agentCallable).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agentexec"
	agentexecpty "github.com/liliang-cn/agentexec/pty"
	"github.com/liliang-cn/superleo/internal/backend"
)

// codingToolProvider runs one user-added tool.
type codingToolProvider struct {
	tool   backend.CodingTool
	binary string
}

func (p codingToolProvider) Name() string { return p.tool.ID }

func (p codingToolProvider) Capabilities() agentexec.Capabilities {
	return agentexec.Capabilities{Streaming: true, SupportsPTY: true, RequiresWorkspace: true}
}

func (p codingToolProvider) NewSession() agentexec.Session {
	return &codingToolSession{p: p, text: agentexec.NewText().NewSession()}
}

// codingToolSession builds the argv itself — the template split, the task put
// into its one argument — and leaves reading the output to agentexec's text
// session, adding the one verdict a text tool cannot give: that an exit 0
// was a refusal.
type codingToolSession struct {
	p    codingToolProvider
	text agentexec.Session
}

func (s *codingToolSession) BuildCommand(_ context.Context, req agentexec.Request) (agentexec.CommandSpec, error) {
	prompt := req.Prompt
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + prompt
	}
	argv, err := s.p.tool.Argv(prompt, req.Model)
	if err != nil {
		return agentexec.CommandSpec{}, err
	}
	argv[0] = s.p.binary
	env := make([]string, 0, len(req.Env))
	for _, k := range slices.Sorted(maps.Keys(req.Env)) {
		env = append(env, k+"="+req.Env[k])
	}
	return agentexec.CommandSpec{Argv: argv, Env: env, WorkDir: req.WorkspacePath}, nil
}

func (s *codingToolSession) ParseChunk(chunk []byte) ([]agentexec.Event, error) {
	return s.text.ParseChunk(chunk)
}

func (s *codingToolSession) SessionID() string { return "" }

func (s *codingToolSession) Finalize(ctx context.Context, out []byte, exitCode int) (agentexec.Result, []agentexec.Event, error) {
	res, tail, err := s.text.Finalize(ctx, out, exitCode)
	if err == nil && exitCode == 0 {
		if _, refused := backend.CodingToolRefusal(res.Summary); refused {
			res.Failed = true
		}
	}
	return res, tail, err
}

// customCLIs are the user-added tools that can run here: their program is
// found and they are not switched off.
func customCLIs(ext backend.ExternalAgents) []agentexec.Installed {
	off := map[string]bool{}
	for _, n := range ext.Disabled {
		off[strings.ToLower(strings.TrimSpace(n))] = true
	}
	var out []agentexec.Installed
	for _, t := range ext.Custom {
		if off[t.ID] {
			continue
		}
		if bin, ok := backend.FindProgram(t.Program()); ok {
			out = append(out, agentexec.Installed{Name: t.ID, Binary: bin, Streaming: true})
		}
	}
	return out
}

// withCustomCLIs adds the user-added tools to a discovered list. A tool wins
// over a discovered CLI of the same name, which only an alias in binaries can
// be: the ids of known CLIs are refused when a tool is saved.
func withCustomCLIs(list []agentexec.Installed, ext backend.ExternalAgents) []agentexec.Installed {
	custom := customCLIs(ext)
	if len(custom) == 0 {
		return list
	}
	mine := map[string]bool{}
	for _, c := range custom {
		mine[c.Name] = true
	}
	out := make([]agentexec.Installed, 0, len(list)+len(custom))
	for _, c := range list {
		if !mine[c.Name] {
			out = append(out, c)
		}
	}
	return append(out, custom...)
}

// registerCodingTools puts the user-added tools in a run's registry.
func registerCodingTools(reg *agentexec.Registry, ext backend.ExternalAgents) {
	for _, c := range customCLIs(ext) {
		if t, ok := ext.CustomTool(c.Name); ok {
			reg.Register(codingToolProvider{tool: t, binary: c.Binary})
		}
	}
}

// CodingToolRow is one tool as the settings page lists it.
type CodingToolRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is builtin (claude, codex, cursor-agent: always listed), found (a
	// CLI agentexec drives, installed here) or custom (added by command).
	Kind string `json:"kind"`
	// Command is a custom tool's template.
	Command string `json:"command,omitempty"`
	Model   string `json:"model,omitempty"`
	// Path is where its program is; Override is the path set in settings
	// for a builtin or found CLI.
	Path      string `json:"path,omitempty"`
	Override  string `json:"override,omitempty"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed"`
	// Off is a builtin switched off in external_agents.disabled.
	Off    bool   `json:"off,omitempty"`
	Preset string `json:"preset,omitempty"`
	// Machine is the linked SuperLeo it is on; empty for this one.
	Machine string `json:"machine,omitempty"`
	// Tried is what it said when it was last tried, if ever.
	Tried *CodingToolTry `json:"tried,omitempty"`
}

// CodingToolOffer is a catalogue CLI not yet in the list, for the add menu.
type CodingToolOffer struct {
	backend.CodingToolPreset
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
}

// CodingTools is the whole group.
type CodingTools struct {
	Enabled bool              `json:"enabled"`
	Tools   []CodingToolRow   `json:"tools"`
	Offers  []CodingToolOffer `json:"offers"`
	// Machines are the linked SuperLeos a tool can be put on.
	Machines []string `json:"machines"`
}

// CodingToolEdit is what the page's form saves or tries.
type CodingToolEdit struct {
	// Kind is builtin, found or custom, as in CodingToolRow.
	Kind string `json:"kind"`
	// ID names the tool. For a new custom tool it may be empty, and is then
	// made from the name.
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Command string `json:"command,omitempty"`
	Model   string `json:"model,omitempty"`
	// Path overrides where a builtin or found CLI's binary is.
	Path   string `json:"path,omitempty"`
	Preset string `json:"preset,omitempty"`
}

// CodingToolTry is one trivial question's answer.
type CodingToolTry struct {
	OK     bool      `json:"ok"`
	MS     int64     `json:"ms"`
	Text   string    `json:"text,omitempty"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// codingTryPrompt is what "try" asks: the cheapest question with an answer
// that can be checked by eye.
const codingTryPrompt = "Reply with exactly: ok"

// codingTryTimeout bounds a try. A person is watching it.
const codingTryTimeout = 90 * time.Second

var (
	codingToolsMu sync.Mutex // one read-modify-write of external_agents at a time
	codingTries   = struct {
		sync.Mutex
		m map[string]CodingToolTry
	}{m: map[string]CodingToolTry{}}
)

func lastTry(id string) *CodingToolTry {
	codingTries.Lock()
	defer codingTries.Unlock()
	if t, ok := codingTries.m[id]; ok {
		return &t
	}
	return nil
}

// LocalCodingTools is this machine's coding tools, without asking any other.
func (a *App) LocalCodingTools() CodingTools {
	ext := a.GetSettings().ExternalAgents
	out := CodingTools{Enabled: ext.Enabled, Tools: []CodingToolRow{}, Offers: []CodingToolOffer{}, Machines: []string{}}
	off := map[string]bool{}
	for _, n := range ext.Disabled {
		off[strings.ToLower(strings.TrimSpace(n))] = true
	}
	found := map[string]agentexec.Installed{}
	for _, c := range installedCLIs(ext.Binaries) {
		found[c.Name] = c
	}
	row := func(name, kind string) CodingToolRow {
		c, ok := found[name]
		label := agentLabel(name)
		if p, isPreset := backend.PresetByID(name); isPreset && label == name {
			label = p.Label
		}
		return CodingToolRow{ID: name, Name: label, Kind: kind, Model: ext.Model(name),
			Path: c.Binary, Version: c.Version, Installed: ok, Override: strings.TrimSpace(ext.Binaries[name]),
			Off: off[name], Tried: lastTry(name)}
	}
	listed := map[string]bool{}
	for _, name := range backend.BuiltinCodingTools {
		out.Tools = append(out.Tools, row(name, "builtin"))
		listed[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(found)) {
		if listed[name] || off[name] {
			continue
		}
		out.Tools = append(out.Tools, row(name, "found"))
		listed[name] = true
	}
	presetsUsed := map[string]bool{}
	for _, t := range ext.Custom {
		r := CodingToolRow{ID: t.ID, Name: t.Name, Kind: "custom", Command: t.Command, Model: t.Model,
			Preset: t.Preset, Off: off[t.ID], Tried: lastTry(t.ID)}
		r.Path, r.Installed = backend.FindProgram(t.Program())
		out.Tools = append(out.Tools, r)
		listed[t.ID] = true
		presetsUsed[t.Preset] = true
	}
	for _, p := range backend.CodingToolPresets {
		if listed[p.ID] || presetsUsed[p.ID] {
			continue
		}
		o := CodingToolOffer{CodingToolPreset: p}
		o.Path, o.Found = backend.FindProgram(firstNonEmpty(ext.Binaries[p.ID], p.Binary))
		out.Offers = append(out.Offers, o)
	}
	for name, r := range a.cliRemotes() {
		if r.link == nil {
			out.Machines = append(out.Machines, name)
		}
	}
	sort.Strings(out.Machines)
	return out
}

// ListCodingTools is this machine's coding tools, and the user-added ones on
// each linked SuperLeo, asked at once and briefly: one that does not answer
// in time is left out, not waited for.
func (a *App) ListCodingTools() CodingTools {
	out := a.LocalCodingTools()
	remotes := a.cliRemotes()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range out.Machines {
		r := remotes[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var there CodingTools
			if r.rpc(ctx, "LocalCodingTools", []any{}, &there) != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, t := range there.Tools {
				if t.Kind == "custom" {
					t.Machine = name
					out.Tools = append(out.Tools, t)
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// SetCodingToolsEnabled is the group's switch: whether SuperLeo may hand work
// to coding tools at all (external_agents.enabled).
func (a *App) SetCodingToolsEnabled(on bool) (CodingTools, error) {
	err := a.changeExternalAgents(func(e *backend.ExternalAgents) error {
		e.Enabled = on
		return nil
	})
	return a.ListCodingTools(), err
}

// SaveCodingTool adds or changes a tool on machine ("" for this one).
func (a *App) SaveCodingTool(machine string, edit CodingToolEdit) (CodingTools, error) {
	if machine = strings.TrimSpace(machine); machine != "" {
		var out CodingTools
		err := a.codingToolOn(machine, "SaveCodingTool", []any{"", edit}, &out)
		return a.ListCodingTools(), err
	}
	if edit.Kind != "custom" && strings.TrimSpace(edit.Path) == "" {
		// Found outside PATH (~/.opencode/bin): discovery looks on PATH only,
		// so the path it was found at is what offers it.
		if _, err := exec.LookPath(edit.ID); err != nil {
			if p, ok := backend.FindProgram(edit.ID); ok {
				edit.Path = p
			}
		}
	}
	err := a.changeExternalAgents(func(e *backend.ExternalAgents) error { return applyCodingToolEdit(e, edit) })
	return a.ListCodingTools(), err
}

// DeleteCodingTool removes a user-added tool, or stops offering a found CLI
// (external_agents.disabled). The three built-ins stay.
func (a *App) DeleteCodingTool(machine, id string) (CodingTools, error) {
	if machine = strings.TrimSpace(machine); machine != "" {
		var out CodingTools
		err := a.codingToolOn(machine, "DeleteCodingTool", []any{"", id}, &out)
		return a.ListCodingTools(), err
	}
	err := a.changeExternalAgents(func(e *backend.ExternalAgents) error { return deleteCodingTool(e, id) })
	codingTries.Lock()
	delete(codingTries.m, id)
	codingTries.Unlock()
	return a.ListCodingTools(), err
}

// TryCodingTool asks a tool one trivial question with the form's values,
// saved or not, and reports what came back and how long it took. It costs a
// turn of whatever the tool talks to, so it runs only when asked.
func (a *App) TryCodingTool(machine string, edit CodingToolEdit) CodingToolTry {
	if machine = strings.TrimSpace(machine); machine != "" {
		var out CodingToolTry
		if err := a.codingToolOn(machine, "TryCodingTool", []any{"", edit}, &out); err != nil {
			return CodingToolTry{Reason: err.Error(), At: time.Now()}
		}
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), codingTryTimeout)
	defer cancel()
	res := a.tryCodingTool(ctx, edit)
	if id := strings.TrimSpace(edit.ID); id != "" {
		codingTries.Lock()
		codingTries.m[id] = res
		codingTries.Unlock()
	}
	return res
}

func (a *App) codingToolOn(machine, method string, args []any, out any) error {
	r, ok := a.cliRemotes()[machine]
	if !ok || r.link != nil {
		return fmt.Errorf("%s is not a SuperLeo linked here by URL", machine)
	}
	ctx, cancel := context.WithTimeout(context.Background(), codingTryTimeout+10*time.Second)
	defer cancel()
	return r.rpc(ctx, method, args, out)
}

// changeExternalAgents reads the settings, changes external_agents on a copy
// and saves the whole: nothing else in the file is touched.
func (a *App) changeExternalAgents(f func(*backend.ExternalAgents) error) error {
	codingToolsMu.Lock()
	defer codingToolsMu.Unlock()
	s := a.GetSettings()
	e := s.ExternalAgents
	// GetSettings copies the struct, not its maps and slices.
	e.Binaries = maps.Clone(e.Binaries)
	e.Models = maps.Clone(e.Models)
	e.Disabled = slices.Clone(e.Disabled)
	e.Custom = slices.Clone(e.Custom)
	if err := f(&e); err != nil {
		return err
	}
	s.ExternalAgents = e
	return a.SaveSettings(s)
}

func setPair(m *map[string]string, k, v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		delete(*m, k)
		return
	}
	if *m == nil {
		*m = map[string]string{}
	}
	(*m)[k] = v
}

func dropName(list []string, name string) []string {
	return slices.DeleteFunc(list, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), name) })
}

// applyCodingToolEdit is SaveCodingTool's change to the settings.
func applyCodingToolEdit(e *backend.ExternalAgents, edit CodingToolEdit) error {
	id := strings.TrimSpace(edit.ID)
	switch edit.Kind {
	case "builtin", "found":
		if id == "" || strings.ContainsAny(id, " ./") {
			return errors.New("which tool?")
		}
		setPair(&e.Binaries, id, edit.Path)
		setPair(&e.Models, id, edit.Model)
		e.Disabled = dropName(e.Disabled, id)
		return nil
	case "custom":
	default:
		return fmt.Errorf("unknown kind of tool %q", edit.Kind)
	}
	t := backend.CodingTool{ID: id, Name: strings.TrimSpace(edit.Name), Command: strings.TrimSpace(edit.Command),
		Model: strings.TrimSpace(edit.Model), Preset: strings.TrimSpace(edit.Preset)}
	if _, err := t.Template(); err != nil {
		return err
	}
	if i := slices.IndexFunc(e.Custom, func(c backend.CodingTool) bool { return c.ID == id }); id != "" && i >= 0 {
		if t.Name == "" {
			t.Name = e.Custom[i].Name
		}
		if t.Preset == "" {
			t.Preset = e.Custom[i].Preset
		}
		e.Custom[i] = t
		return nil
	}
	if t.Name == "" {
		return errors.New("give it a name")
	}
	if t.ID == "" {
		base := backend.CodingToolSlug(t.Name)
		t.ID = base
		for n := 2; slices.ContainsFunc(e.Custom, func(c backend.CodingTool) bool { return c.ID == t.ID }); n++ {
			t.ID = fmt.Sprintf("%s-%d", base, n)
		}
	}
	if err := backend.ValidCodingToolID(t.ID); err != nil {
		return err
	}
	if slices.ContainsFunc(e.Custom, func(c backend.CodingTool) bool { return c.ID == t.ID }) {
		return fmt.Errorf("%s is taken", t.ID)
	}
	e.Custom = append(e.Custom, t)
	e.Disabled = dropName(e.Disabled, t.ID)
	return nil
}

// deleteCodingTool is DeleteCodingTool's change to the settings.
func deleteCodingTool(e *backend.ExternalAgents, id string) error {
	id = strings.TrimSpace(id)
	if slices.Contains(backend.BuiltinCodingTools, id) {
		return fmt.Errorf("%s is built in", id)
	}
	if i := slices.IndexFunc(e.Custom, func(c backend.CodingTool) bool { return c.ID == id }); i >= 0 {
		e.Custom = slices.Delete(e.Custom, i, i+1)
		e.Disabled = dropName(e.Disabled, id)
		return nil
	}
	if id == "" {
		return errors.New("which tool?")
	}
	delete(e.Binaries, id)
	delete(e.Models, id)
	if !slices.ContainsFunc(e.Disabled, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), id) }) {
		e.Disabled = append(e.Disabled, id)
	}
	return nil
}

// codingToolProviderFor builds the provider an edit describes, without
// saving it: a user-added tool from its command, a known CLI from its name
// and binary.
func codingToolProviderFor(edit CodingToolEdit, ext backend.ExternalAgents) (agentexec.Provider, error) {
	id := strings.TrimSpace(edit.ID)
	if edit.Kind == "custom" {
		t := backend.CodingTool{ID: firstNonEmpty(id, "tool"), Command: edit.Command, Model: edit.Model}
		if _, err := t.Template(); err != nil {
			return nil, err
		}
		bin, ok := backend.FindProgram(t.Program())
		if !ok {
			return nil, fmt.Errorf("%s is not installed here", t.Program())
		}
		return codingToolProvider{tool: t, binary: bin}, nil
	}
	bin, ok := backend.FindProgram(firstNonEmpty(edit.Path, ext.Binaries[id], id))
	if !ok {
		return nil, fmt.Errorf("%s is not installed here", id)
	}
	p, err := agentexec.RegistryFrom([]agentexec.Installed{{Name: id, Binary: bin}}).Get(id)
	if err != nil {
		return nil, fmt.Errorf("%s is not a tool SuperLeo can drive", id)
	}
	return p, nil
}

// tryCodingTool runs the one question, in the workspace, the way a run would.
func (a *App) tryCodingTool(ctx context.Context, edit CodingToolEdit) CodingToolTry {
	started := time.Now()
	res := CodingToolTry{At: started}
	fail := func(reason string) CodingToolTry {
		res.Reason, res.MS = reason, time.Since(started).Milliseconds()
		return res
	}
	ext := a.GetSettings().ExternalAgents
	provider, err := codingToolProviderFor(edit, ext)
	if err != nil {
		return fail(err.Error())
	}
	dir, err := a.cliCwd("", nil)
	if err != nil {
		return fail(err.Error())
	}
	model := strings.TrimSpace(edit.Model)
	req := agentexec.Request{Prompt: codingTryPrompt, WorkspacePath: dir, Model: model,
		PermissionMode: agentexec.PermissionBypass, Env: map[string]string{}}
	maps.Copy(req.Env, backend.ProxyEnv())
	if req.Env["HTTPS_PROXY"] != "" {
		req.Env["NODE_USE_ENV_PROXY"] = "1"
	}
	sess := provider.NewSession()
	spec, err := sess.BuildCommand(ctx, req)
	if err != nil {
		return fail(err.Error())
	}
	out, runErr := agentexecpty.Run(ctx, agentexecpty.Command{Argv: spec.Argv, Env: spec.Env, WorkDir: spec.WorkDir},
		func(chunk []byte) { _, _ = sess.ParseChunk(chunk) })
	result, _, finErr := sess.Finalize(context.Background(), out.Output, out.ExitCode)
	res.MS = time.Since(started).Milliseconds()
	text := strings.TrimSpace(result.Summary)
	switch {
	case ctx.Err() != nil:
		return fail(fmt.Sprintf("no answer in %s", codingTryTimeout))
	case runErr != nil:
		return fail(runErr.Error())
	case finErr != nil:
		return fail(finErr.Error())
	case result.Failed || out.ExitCode != 0:
		reason := backend.CondenseAgentFailure(firstNonEmpty(text, cliLastLine(string(out.Output))))
		if reason == "" {
			reason = fmt.Sprintf("exit %d", out.ExitCode)
		}
		return fail(reason)
	case text == "":
		return fail("it exited without saying anything")
	}
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200]) + " …"
	}
	res.OK, res.Text = true, text
	return res
}

// customAgentStatuses are the user-added tools as ExternalAgentsStatus
// reports them, so a SuperLeo linked by URL can offer "tool.machine".
func customAgentStatuses(ext backend.ExternalAgents) []backend.ExternalAgentStatus {
	var out []backend.ExternalAgentStatus
	for _, c := range customCLIs(ext) {
		out = append(out, backend.ExternalAgentStatus{Name: c.Name, Installed: true, Path: c.Binary})
	}
	return out
}

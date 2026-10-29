package app

// Core's side of looking after machines (host.go is the machine's side). A
// machine is a linked agent by its name, or core itself as "core" or blank;
// each Machine* method asks that machine's Host* method, over the agent link
// or in this process. The screens call these, and so do the queen's
// machine_inspect and machine_act tools.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// machineErr says what an agent from before host.go means by its refusal.
func machineErr(name string, err error) error {
	if err != nil && strings.Contains(err.Error(), "is not something core can ask") {
		return fmt.Errorf("the agent on %s is too old to be looked after; update it", name)
	}
	return err
}

func isCoreMachine(name string) bool {
	n := strings.TrimSpace(name)
	return n == "" || n == "core"
}

// onMachine calls method on the named machine and decodes its answer into out.
func (a *App) onMachine(name string, d time.Duration, method string, args []any, out any) error {
	c := a.agents().get(strings.TrimSpace(name))
	role := agentRoleMachine
	if c != nil {
		role = a.agentRole(c)
	}
	if c == nil || !machineToo(role, c.hello) {
		names := []string{"core"}
		for _, l := range a.LinkedAgents() {
			names = append(names, l.Name)
		}
		there := "; the machines are " + strings.Join(names, ", ")
		switch role {
		case agentRoleWorker:
			return fmt.Errorf("%s is a hive worker, not a machine (hive_members and hive_command reach it)%s", name, there)
		case agentRoleRelay:
			return fmt.Errorf("%s is an agent relay, not a machine: it is there for the agents it serves, not to be looked after%s", name, there)
		}
		return fmt.Errorf("no machine called %q is linked%s", name, there)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return machineErr(name, c.call(ctx, method, args, out))
}

// MachineStatus is how a machine is doing: load, memory, disks, uptime.
func (a *App) MachineStatus(name string) (HostInfo, error) {
	if isCoreMachine(name) {
		h, err := a.HostStatus()
		h.Name = "core"
		h.Claude = a.machineActivity("core")
		h.CLIActivity = a.machineCLIs("core")
		return h, err
	}
	var h HostInfo
	err := a.onMachine(name, 30*time.Second, "HostStatus", []any{}, &h)
	h.Claude = a.machineActivity(name)
	h.CLIActivity = a.machineCLIs(name)
	return h, err
}

// MachineServices lists a machine's services, failed ones first.
func (a *App) MachineServices(name string) ([]HostService, error) {
	if isCoreMachine(name) {
		return a.HostServices()
	}
	var out []HostService
	err := a.onMachine(name, 30*time.Second, "HostServices", []any{}, &out)
	return out, err
}

// MachineLogs is the end of a service's log, or of a log file, on a machine.
func (a *App) MachineLogs(name, target string, lines int) (string, error) {
	if isCoreMachine(name) {
		return a.HostLogs(target, lines)
	}
	var out string
	err := a.onMachine(name, 40*time.Second, "HostLogs", []any{target, lines}, &out)
	return out, err
}

// MachineService starts, stops or restarts a service on a machine.
func (a *App) MachineService(name, service, action string) (string, error) {
	if isCoreMachine(name) {
		return a.HostService(service, action)
	}
	var out string
	err := a.onMachine(name, 90*time.Second, "HostService", []any{service, action}, &out)
	return out, err
}

// MachineExec runs a shell command on a machine.
func (a *App) MachineExec(name, command string, timeoutSeconds int) (HostExecResult, error) {
	if isCoreMachine(name) {
		return a.HostExec(command, timeoutSeconds)
	}
	wait := time.Duration(timeoutSeconds)*time.Second + 30*time.Second
	if timeoutSeconds <= 0 {
		wait = 90 * time.Second
	}
	if wait > 11*time.Minute {
		wait = 11 * time.Minute
	}
	var out HostExecResult
	err := a.onMachine(name, wait, "HostExec", []any{command, timeoutSeconds}, &out)
	return out, err
}

// machineInspect is the machine_inspect tool.
func (a *App) machineInspect(args map[string]any) (any, error) {
	name := str(args["machine"])
	switch what := str(args["what"]); what {
	case "", "status":
		return a.MachineStatus(name)
	case "services":
		return a.MachineServices(name)
	case "logs":
		target := str(args["target"])
		if target == "" {
			return nil, errors.New("logs needs target: a service name or a log file's absolute path")
		}
		return a.MachineLogs(name, target, intArg(args["lines"]))
	default:
		return nil, fmt.Errorf("what is status, services or logs, not %q", what)
	}
}

// machineAct is the machine_act tool.
func (a *App) machineAct(args map[string]any) (any, error) {
	name := str(args["machine"])
	switch action := str(args["action"]); action {
	case "exec":
		cmd := str(args["command"])
		if cmd == "" {
			return nil, errors.New("exec needs command")
		}
		return a.MachineExec(name, cmd, intArg(args["timeout_seconds"]))
	case "start", "stop", "restart":
		svc := str(args["service"])
		if svc == "" {
			return nil, fmt.Errorf("%s needs service", action)
		}
		return a.MachineService(name, svc, action)
	default:
		return nil, fmt.Errorf("action is exec, start, stop or restart, not %q", action)
	}
}

func intArg(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var i int
		fmt.Sscan(n, &i)
		return i
	}
	return 0
}

// machineTools are the two tools of the self table (selfmcp.go) that look
// after machines: one only reads, the other changes things and so is asked.
func machineTools() []selfTool {
	machine := map[string]any{"type": "string", "description": "A linked machine's name (the linked_agents entries with machine true), or \"core\" for the machine this SuperLeo runs on. Hive workers and relays are not machines."}
	return []selfTool{
		{
			Name: "machine_inspect", Title: "Inspect a machine", ReadOnly: true,
			Description: "How a machine is doing and what runs on it. what=status: load, memory, disks, uptime, and whether it takes commands." +
				" what=services: its services (systemd on Linux, launchd jobs on a Mac), failed ones first." +
				" what=logs: the last lines of a service's log (target = the service name) or of a log file (target = its absolute path).",
			Params: map[string]any{
				"machine": machine,
				"what":    map[string]any{"type": "string", "enum": []string{"status", "services", "logs"}},
				"target":  map[string]any{"type": "string", "description": "For logs: a service name or an absolute log file path."},
				"lines":   map[string]any{"type": "integer", "description": "For logs: how many lines, 100 by default."},
			},
			Required: []string{"machine", "what"},
			Run:      func(a *App, args map[string]any) (any, error) { return a.machineInspect(args) },
		},
		{
			Name: "machine_act", Title: "Act on a machine",
			Description: "Change something on a machine: start, stop or restart one of its services, or run a shell command there (action=exec)." +
				" Look first with machine_inspect; prefer a service action to a command when one does the job." +
				" This looks after the machine itself. When the person wants a task done on a machine whose SuperLeo is a worker (open an app," +
				" use its browser or files, work that takes several steps), hand the whole task to that worker with hive_delegate instead;" +
				" it does it with its own tools." +
				" A command runs in /bin/sh from the home directory of the user the agent runs as (root on the Linux hosts), a minute by default." +
				" Every call asks the person first (the approval gate) and waits for the answer; a denial or no answer in time means nothing was done.",
			Params: map[string]any{
				"machine":         machine,
				"action":          map[string]any{"type": "string", "enum": []string{"exec", "start", "stop", "restart"}},
				"service":         map[string]any{"type": "string", "description": "For start/stop/restart: the service name."},
				"command":         map[string]any{"type": "string", "description": "For exec: the shell command."},
				"timeout_seconds": map[string]any{"type": "integer", "description": "For exec: up to 600."},
			},
			Required: []string{"machine", "action"},
			Run:      func(a *App, args map[string]any) (any, error) { return a.machineAct(args) },
		},
	}
}

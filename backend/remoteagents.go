package backend

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agents on other machines.
//
// ExternalAgents (settings.go) hands a task to a CLI installed on *this*
// machine. This is the other half: the agents that live somewhere else on the
// network — pi and hermes on the openclaw cluster, and openclaw itself.
//
// They are reached over SSH rather than over an HTTP API, and that is a
// property of what they are, not a shortcut. hermes and pi are launcher shells
// on a DRBD volume with no listener of their own; only the openclaw gateway
// binds a port. Standing an HTTP shim in front of them would mean a long-lived
// process on that volume, which pins it and stops the node from being taken
// over — the exact failure the cluster is built to avoid. SSH costs a
// connection per call and keeps the volume free.
//
// The same volume is why a host is resolved rather than configured. The
// resource follows drbd-reactor's promoter between nodes, so the machine that
// can run hermes today is not necessarily the one that could yesterday. Each
// agent names the hosts it may live on and a probe that says "it is here"; the
// answer is cached briefly, because a failover is rare and a probe per call
// would double the latency of every delegated question.

// DefaultRemoteAgentTimeout bounds one remote run when the settings do not.
//
// Shorter than a local delegation's twenty minutes: these are asked questions
// rather than handed tasks, and an SSH command that has produced nothing in
// five minutes is usually a login prompt or a wedged model, not slow thinking.
const DefaultRemoteAgentTimeout = 5 * time.Minute

// remoteProbeTimeout bounds one host probe. Long enough for an SSH handshake
// on the LAN, short enough that walking three dead hosts still leaves time to
// answer.
const remoteProbeTimeout = 6 * time.Second

// remoteHostTTL is how long a resolved host is believed.
//
// A failover takes longer than this to complete, so a stale answer costs one
// failed call and then re-resolves — where probing every time would cost every
// call an extra round trip for an event that happens a few times a year.
const remoteHostTTL = 2 * time.Minute

// RemoteAgents configures the agents SuperAI can reach over the network.
//
// Off by zero value, like ExternalAgents and for the same reason: it runs
// commands on other machines under someone's credentials.
type RemoteAgents struct {
	Enabled bool `json:"enabled"`
	// Agents, by the name they are called by — the name after the @.
	Agents map[string]RemoteAgent `json:"agents,omitempty"`
	// TimeoutSeconds bounds one remote run. Zero takes the default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// RemoteAgent is one agent on another machine, and how to ask it something.
type RemoteAgent struct {
	// What it is, in one line. Shown in the picker and given to the model.
	About string `json:"about,omitempty"`
	// Hosts it may be running on, tried in order. More than one because a
	// clustered agent moves between nodes.
	//
	// Each entry is the ssh argument list with the target last, so anything
	// ssh can be told is expressible without a schema for it:
	//
	//	"sds@192.168.123.212"
	//	"-p 52848 liliang@127.0.0.1"
	//	"-F ~/.lima/sds-a/ssh.config lima-sds-a"
	//
	// The third form is what reaches a Lima VM, whose forwarded port is
	// assigned at boot and therefore cannot be written down — but whose
	// generated config file can.
	Hosts []string `json:"hosts"`
	// Probe is a shell command that must succeed on the host that currently
	// holds the agent. Empty means every host qualifies, and the first that
	// accepts a connection wins.
	Probe string `json:"probe,omitempty"`
	// User to run as on the far side, through sudo. Empty runs as whoever the
	// SSH target logs in as.
	User string `json:"user,omitempty"`
	// Dir to run in. It matters more than it looks: a launcher started in a
	// directory the run-as user cannot read fails with an error about that
	// directory, which reads like a broken install rather than a wrong cwd.
	Dir string `json:"dir,omitempty"`
	// Env set for the run.
	Env map[string]string `json:"env,omitempty"`
	// Command is the argv. Exactly one element must be the placeholder
	// "{prompt}", which is replaced by the question — as one argument, never
	// spliced into a string.
	Command []string `json:"command"`
}

// Timeout is how long one remote run may take.
func (r RemoteAgents) Timeout() time.Duration {
	if r.TimeoutSeconds <= 0 {
		return DefaultRemoteAgentTimeout
	}
	return time.Duration(r.TimeoutSeconds) * time.Second
}

// Names lists the configured agents, sorted, so a menu and a tool description
// always agree on the order.
func (r RemoteAgents) Names() []string {
	out := make([]string, 0, len(r.Agents))
	for name := range r.Agents {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a name is one of the configured agents.
func (r RemoteAgents) Has(name string) bool {
	if !r.Enabled {
		return false
	}
	_, ok := r.Agents[strings.TrimSpace(name)]
	return ok
}

// normalize fills in what the defaults provide and drops what cannot run.
func (r *RemoteAgents) normalize() {
	if r.Agents == nil {
		return
	}
	for name, a := range r.Agents {
		// An agent with no argv or no host cannot be called, and leaving it in
		// the list means offering the model a name that always fails.
		if len(a.Command) == 0 || len(a.Hosts) == 0 {
			delete(r.Agents, name)
			continue
		}
		hosts := a.Hosts[:0]
		for _, h := range a.Hosts {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		a.Hosts = hosts
		if len(a.Hosts) == 0 {
			delete(r.Agents, name)
			continue
		}
		r.Agents[name] = a
	}
}

// backfill supplies the agents this network is known to have, for any name
// the settings do not already define.
//
// A catalogue rather than a switch: an entry here makes @pi resolvable and
// nothing more, because Enabled stays whatever the file said. Written down
// because the shape of these calls is not guessable — each one encodes
// something that was only learned by getting it wrong:
//
//   - The launchers live on the DRBD volume and run as the openclaw user, so
//     every call goes through sudo. The SSH login is the node's own admin
//     account, which is the one with a key on it.
//   - Dir is /tmp and not a default. The login user's home is unreadable to
//     openclaw, and hermes started there fails with
//     "Permission denied: '/home/<user>/.git'" — which reads like a broken
//     install rather than a wrong working directory.
//   - hermes needs HERMES_HOME's base handed to it explicitly: its launcher
//     derives the path from HOME, and sudo does not carry the login user's.
//   - The hosts are every node the promoter can put the volume on, not the
//     one holding it today; the probe is what picks.
func (r *RemoteAgents) backfill() {
	if r.Agents == nil {
		r.Agents = map[string]RemoteAgent{}
	}
	for name, a := range defaultRemoteAgents() {
		if _, taken := r.Agents[name]; !taken {
			r.Agents[name] = a
		}
	}
}

// clusterHosts are the nodes the openclaw resource can be promoted on.
var clusterHosts = []string{
	"sds@192.168.123.212",                    // node-e, on the hp box
	"-F ~/.lima/sds-a/ssh.config lima-sds-a", // node-a, the Lima VM on this Mac
}

// clusterProbe succeeds only on the node currently holding the resource.
const clusterProbe = "systemctl is-active --quiet openclaw"

func defaultRemoteAgents() map[string]RemoteAgent {
	return map[string]RemoteAgent{
		"pi": {
			About:   "pi — a coding agent on the openclaw cluster. Reads, edits and runs things in its own workspace there.",
			Hosts:   clusterHosts,
			Probe:   clusterProbe,
			User:    "openclaw",
			Dir:     "/tmp",
			Command: []string{"/var/lib/openclaw/pi/pi", "-p", "{prompt}"},
		},
		"hermes": {
			About:   "hermes — a general agent on the openclaw cluster, with its own memory and kanban.",
			Hosts:   clusterHosts,
			Probe:   clusterProbe,
			User:    "openclaw",
			Dir:     "/tmp",
			Env:     map[string]string{"HOME": "/var/lib/openclaw-home"},
			Command: []string{"/var/lib/openclaw/hermes/hermes", "--oneshot", "{prompt}"},
		},
		"openclaw": {
			About: "openclaw — the gateway agent that runs the home cluster: SDS alerts, browser, WeChat, cron.",
			Hosts: clusterHosts,
			Probe: clusterProbe,
			User:  "openclaw",
			Dir:   "/tmp",
			// Both, copied from the systemd unit. Without the state dir the
			// CLI looks for its config under HOME — which is node-local — finds
			// nothing, and reports that the gateway "requires credentials",
			// which sounds like an auth problem rather than a lookup in the
			// wrong directory.
			Env: map[string]string{
				"HOME":               "/var/lib/openclaw-home",
				"OPENCLAW_STATE_DIR": "/var/lib/openclaw",
			},
			Command: []string{"openclaw", "agent", "--message", "{prompt}"},
		},
	}
}

// RemoteRunner asks remote agents things, and remembers where they live.
type RemoteRunner struct {
	cfg RemoteAgents

	mu    sync.Mutex
	where map[string]resolvedHost
}

type resolvedHost struct {
	host string
	at   time.Time
}

func NewRemoteRunner(cfg RemoteAgents) *RemoteRunner {
	return &RemoteRunner{cfg: cfg, where: map[string]resolvedHost{}}
}

// Config is what this runner was built from.
func (r *RemoteRunner) Config() RemoteAgents { return r.cfg }

// RemoteResult is one answer, and enough about the call to explain a bad one.
type RemoteResult struct {
	Agent  string `json:"agent"`
	Host   string `json:"host"`
	Text   string `json:"text"`
	Failed bool   `json:"failed"`
	// Reason says why, when Failed. Kept apart from Text so a caller never has
	// to guess whether it is holding an answer or an explanation — the trap
	// that makes a failed delegation read like a real reply.
	Reason string `json:"reason,omitempty"`
	MS     int64  `json:"ms"`
}

// Run asks one agent one question.
//
// It never returns an error for a refusal the person should read — a missing
// agent, a host that cannot be found, a CLI that answered with a login prompt
// all come back as a RemoteResult with Failed set. An error is reserved for
// the call being impossible to make at all.
func (r *RemoteRunner) Run(ctx context.Context, name, prompt string) (res RemoteResult, err error) {
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)
	res = RemoteResult{Agent: name}
	// Named return, so the deferred stamp lands on what the caller receives
	// rather than on a copy that has already been made.
	started := time.Now()
	defer func() { res.MS = time.Since(started).Milliseconds() }()

	if !r.cfg.Enabled {
		res.Failed, res.Reason = true, "remote agents are switched off in Settings"
		return res, nil
	}
	agent, ok := r.cfg.Agents[name]
	if !ok {
		res.Failed = true
		res.Reason = fmt.Sprintf("there is no agent called %q; configured: %s",
			name, strings.Join(r.cfg.Names(), ", "))
		return res, nil
	}
	if prompt == "" {
		res.Failed, res.Reason = true, "nothing was asked"
		return res, nil
	}

	host, herr := r.host(ctx, name, agent)
	if herr != nil {
		res.Failed, res.Reason = true, herr.Error()
		return res, nil
	}
	res.Host = hostLabel(host)

	script, serr := remoteScript(agent, prompt)
	if serr != nil {
		res.Failed, res.Reason = true, serr.Error()
		return res, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout())
	defer cancel()
	out, runErr := sshRun(runCtx, host, script, r.cfg.Timeout())
	// A rate limit that names its own delay is worth waiting out once. These
	// agents sit behind shared upstream quotas, and a question that failed
	// because somebody else asked one fifteen seconds ago should not come back
	// as "did not answer" when the API said, in the same breath, when to try
	// again. Once only, and only for a delay short enough to hold a turn for.
	if runErr != nil {
		if wait := RetryAfter(runErr.Error()); wait > 0 {
			select {
			case <-time.After(wait):
				out, runErr = sshRun(runCtx, host, script, r.cfg.Timeout())
			case <-runCtx.Done():
			}
		}
	}
	res.Text = strings.TrimSpace(out)
	if runErr != nil {
		res.Failed = true
		// The output is the useful half of a failure — a CLI that wants a
		// login says so on stderr and exits non-zero — so it is kept, condensed
		// to the sentence worth reading; the exit status is only the label.
		res.Reason = CondenseAgentFailure(runErr.Error())
		// A host that has stopped holding the agent must not stay cached, or
		// every call until the TTL expires goes to the wrong machine.
		r.forget(name)
		return res, nil
	}
	if res.Text == "" {
		res.Failed, res.Reason = true, "the agent exited cleanly without saying anything"
	}
	return res, nil
}

// host finds the machine currently holding an agent.
func (r *RemoteRunner) host(ctx context.Context, name string, a RemoteAgent) (string, error) {
	r.mu.Lock()
	if got, ok := r.where[name]; ok && time.Since(got.at) < remoteHostTTL {
		r.mu.Unlock()
		return got.host, nil
	}
	r.mu.Unlock()

	// One host and no probe is not a cluster; do not spend a round trip
	// confirming the only answer there is.
	if len(a.Hosts) == 1 && strings.TrimSpace(a.Probe) == "" {
		r.remember(name, a.Hosts[0])
		return a.Hosts[0], nil
	}

	probe := strings.TrimSpace(a.Probe)
	if probe == "" {
		probe = "true"
	}
	var tried []string
	for _, h := range a.Hosts {
		pctx, cancel := context.WithTimeout(ctx, remoteProbeTimeout)
		_, err := sshRun(pctx, h, probe, remoteProbeTimeout)
		cancel()
		if err == nil {
			r.remember(name, h)
			return h, nil
		}
		tried = append(tried, h)
	}
	return "", fmt.Errorf("%s is not running on any of its hosts (tried %s)", name, strings.Join(tried, ", "))
}

func (r *RemoteRunner) remember(name, host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.where[name] = resolvedHost{host: host, at: time.Now()}
}

func (r *RemoteRunner) forget(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.where, name)
}

// remoteScript renders the shell the far side runs.
//
// Everything variable — the prompt above all — goes through shellQuote, so the
// remote shell sees one literal argument however much punctuation the question
// contains. This is the only place user text meets a shell in this file, and
// getting it wrong would turn a question containing a backtick into a command.
func remoteScript(a RemoteAgent, prompt string) (string, error) {
	argv := make([]string, 0, len(a.Command))
	filled := false
	for _, part := range a.Command {
		if part == "{prompt}" {
			argv = append(argv, shellQuote(prompt))
			filled = true
			continue
		}
		argv = append(argv, shellQuote(part))
	}
	if !filled {
		return "", fmt.Errorf("this agent's command has no {prompt} placeholder, so there is nowhere to put the question")
	}

	var b strings.Builder
	if d := strings.TrimSpace(a.Dir); d != "" {
		fmt.Fprintf(&b, "cd %s && ", shellQuote(d))
	}
	if u := strings.TrimSpace(a.User); u != "" {
		// -n so a sudo that would ask for a password fails immediately rather
		// than holding the connection open waiting for a terminal that is not
		// there.
		b.WriteString("sudo -n -u " + shellQuote(u) + " ")
	}
	if len(a.Env) > 0 {
		b.WriteString("env ")
		keys := make([]string, 0, len(a.Env))
		for k := range a.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(shellQuote(k+"="+a.Env[k]) + " ")
		}
	}
	b.WriteString(strings.Join(argv, " "))
	return b.String(), nil
}

// shellQuote wraps s so a POSIX shell reads it as one literal word.
//
// Single quotes protect everything except a single quote, which is closed,
// escaped and reopened. There is no character this does not survive, which is
// the property that matters: the argument is a person's question.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshRun runs one shell command on a host and returns what it said.
//
// stdin is closed rather than inherited. A CLI that decides to read from it —
// several do, when they think they are in a pipeline — would otherwise wait
// for input that is never coming, and the run would burn its whole deadline in
// silence.
func sshRun(ctx context.Context, host, script string, deadline time.Duration) (string, error) {
	args := []string{
		"-o", "BatchMode=yes", // never prompt for a password or a passphrase
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(remoteProbeTimeout.Seconds())),
	}
	args = append(args, hostArgs(host)...)
	args = append(args, script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = nil
	// WaitDelay because ssh can leave the far side holding the pipes open: the
	// context kills ssh, but a read on its stdout would go on waiting for a
	// writer that is gone.
	cmd.WaitDelay = 3 * time.Second

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	text := out.String()
	if err != nil {
		stderr := strings.TrimSpace(errb.String())
		if ctx.Err() != nil {
			return text, fmt.Errorf("gave up after %s", deadline)
		}
		if stderr != "" {
			return text, fmt.Errorf("%v: %s", err, firstLines(stderr, 40))
		}
		return text, err
	}
	return text, nil
}

// hostArgs splits a host entry into ssh arguments, expanding ~ in any of them
// so a path to a generated config file works from a GUI app, which has no
// shell to do it.
func hostArgs(host string) []string {
	fields := strings.Fields(host)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, expandHome(f))
	}
	return out
}

// hostLabel is the part of a host entry worth showing: the target, which is
// the last argument. The options in front of it are plumbing.
func hostLabel(host string) string {
	fields := strings.Fields(host)
	if len(fields) == 0 {
		return host
	}
	return fields[len(fields)-1]
}

// firstLines carries a failed run's output out of the process.
//
// Both dimensions are bounded, because either alone lets something through: a
// stack trace is many short lines, an API error is one enormous one. But the
// bound here is generous, and deliberately so — this is not what anybody
// reads. CondenseAgentFailure is, and it needs the whole body to work with:
// the sentence worth quoting and the retry delay worth obeying are both near
// the end of a Gemini 429, and a first pass that cut to display length threw
// away both. Trim once, at the point of display.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	trimmed := false
	if len(lines) > n {
		lines, trimmed = lines[:n], true
	}
	out := strings.Join(lines, " / ")
	if r := []rune(out); len(r) > rawFailureRunes {
		out, trimmed = strings.TrimSpace(string(r[:rawFailureRunes])), true
	}
	if trimmed {
		out += " …"
	}
	return out
}

// rawFailureRunes bounds what is carried out of a failed run: enough for a
// whole API error body, short of anything that would be a memory problem.
const rawFailureRunes = 4000

// failureDetailRunes bounds the quoted part of a failure once it is shown.
//
// Long enough for the sentence an API actually wrote, short enough that it
// stays a line in a transcript rather than a wall. The rest is not lost — it is
// in the agent's own output, which the caller still has.
const failureDetailRunes = 200

// jsonMessage pulls the human sentence out of an API error body.
//
// Every one of these services buries the readable part in a "message" field
// surrounded by codes, quota metrics and documentation links. Taking that field
// turns a paragraph of JSON into the one line worth reading. A body without it
// is left alone rather than guessed at.
var jsonMessage = regexp.MustCompile(`"message"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// retryHint finds the delay an API asked to be waited, in its own words.
var retryHint = regexp.MustCompile(`(?i)retry(?:Delay|\s+in)"?[:\s]+"?(\d+(?:\.\d+)?)\s*s`)

// CondenseAgentFailure turns whatever a delegated agent printed on its way out
// into one line a person can act on.
//
// The classification is deliberately coarse: what a reader needs first is
// whether to wait, sign in, or give up, and those three cover almost every
// failure these CLIs produce. The agent's own words follow, because they are
// the part that says which account, which model, which quota — and a message
// that replaced them with a category would be less useful, not more.
func CondenseAgentFailure(raw string) string {
	detail := strings.TrimSpace(raw)
	if m := jsonMessage.FindStringSubmatch(detail); len(m) == 2 {
		// Unescape the pieces that matter for reading; a stray \uXXXX is left
		// as written rather than risking a decode of something that is not JSON.
		unescaped := strings.NewReplacer(`\n`, " ", `\t`, " ", `\"`, `"`, `\\`, `\`).Replace(m[1])
		if strings.TrimSpace(unescaped) != "" {
			detail = unescaped
		}
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if r := []rune(detail); len(r) > failureDetailRunes {
		detail = strings.TrimSpace(string(r[:failureDetailRunes])) + " …"
	}

	lead := ""
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(low, "resource_exhausted") || strings.Contains(low, "rate limit") ||
		strings.Contains(low, "quota") || strings.Contains(low, "429"):
		lead = "rate-limited or out of quota"
		if d := RetryAfter(raw); d > 0 {
			lead += fmt.Sprintf(" (it asks for %s)", d.Round(time.Second))
		}
	case strings.Contains(low, "ineligibletier"):
		lead = "this account is not eligible for the model it asked for"
	case strings.Contains(low, "authenticat") || strings.Contains(low, "unauthorized") ||
		strings.Contains(low, "api key") || strings.Contains(low, "401") ||
		strings.Contains(low, "please run") && strings.Contains(low, "login"):
		lead = "not signed in"
	}
	if lead == "" {
		return detail
	}
	if detail == "" {
		return lead
	}
	return lead + " — " + detail
}

// RetryAfter is how long a failure asked to be waited, or zero when it did not
// say. Only a delay short enough to be worth holding a person's turn for is
// reported: a quota that resets tomorrow is not something to sleep through.
func RetryAfter(raw string) time.Duration {
	m := retryHint.FindStringSubmatch(raw)
	if len(m) != 2 {
		return 0
	}
	secs, err := strconv.ParseFloat(m[1], 64)
	if err != nil || secs <= 0 || secs > maxRetryWait.Seconds() {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// maxRetryWait bounds an automatic retry. Beyond this the honest thing is to
// report the failure and let the person decide, rather than hold a spinner.
const maxRetryWait = 30 * time.Second

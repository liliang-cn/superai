package backend

import (
	"strings"
	"testing"
)

// The prompt is a person's question and it ends up inside a shell command on
// another machine. This is the one place in the feature where getting it wrong
// is not a bug but a remote code execution, so it is tested against the
// characters that would do it rather than against a happy path.
func TestAQuestionCannotBecomeACommand(t *testing.T) {
	agent := RemoteAgent{Command: []string{"/bin/ask", "-p", "{prompt}"}}
	for _, nasty := range []string{
		"; rm -rf /",
		"$(touch /tmp/pwned)",
		"`id`",
		"'; id; '",
		"a' && curl evil.example '",
		"$HOME",
		"line one\nline two",
		"quote\" and 'quote'",
	} {
		script, err := remoteScript(agent, nasty)
		if err != nil {
			t.Fatalf("%q: %v", nasty, err)
		}
		// Everything after the argv's fixed head must be inside one quoted
		// word. The only way that is false is if a quote closed early.
		body := strings.TrimPrefix(script, "'/bin/ask' '-p' ")
		if body == script {
			t.Fatalf("%q produced an unexpected shape: %s", nasty, script)
		}
		if !strings.HasPrefix(body, "'") || !strings.HasSuffix(body, "'") {
			t.Errorf("%q escaped its quoting: %s", nasty, script)
		}
		// A single quote may appear only as the '\'' idiom that closes,
		// escapes and reopens. Anything else is an early close.
		inner := body[1 : len(body)-1]
		if strings.Contains(strings.ReplaceAll(inner, `'\''`, ""), "'") {
			t.Errorf("%q left a bare quote inside the word: %s", nasty, script)
		}
	}
}

// A quoted word has to survive round-tripping, or the other agent is answering
// a different question from the one that was asked.
func TestQuotingKeepsTheQuestionIntact(t *testing.T) {
	for _, s := range []string{"", "plain", "it's", `"double"`, "$(x)", "a\nb", "中文 · 标点，符号"} {
		q := shellQuote(s)
		// Undo the transformation the way sh would.
		got := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(q, "'"), "'"), `'\''`, "'")
		if got != s {
			t.Errorf("%q round-tripped to %q", s, got)
		}
	}
}

// An agent whose command has nowhere to put the question would silently ask
// nothing at all.
func TestACommandWithNoPlaceholderIsRefused(t *testing.T) {
	_, err := remoteScript(RemoteAgent{Command: []string{"/bin/ask", "--interactive"}}, "hello")
	if err == nil {
		t.Fatal("a command with no {prompt} was accepted")
	}
	if !strings.Contains(err.Error(), "{prompt}") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// The two things the cluster taught us the hard way have to be in the rendered
// script, because neither failure names itself: a wrong working directory
// reports a permission error about a file nobody mentioned, and a missing HOME
// silently sends the agent to a config that does not follow the volume.
func TestTheScriptCarriesTheDirAndTheEnvironment(t *testing.T) {
	script, err := remoteScript(RemoteAgent{
		User:    "openclaw",
		Dir:     "/tmp",
		Env:     map[string]string{"HOME": "/var/lib/openclaw-home"},
		Command: []string{"/var/lib/openclaw/hermes/hermes", "--oneshot", "{prompt}"},
	}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cd '/tmp'", "sudo -n -u 'openclaw'", "'HOME=/var/lib/openclaw-home'", "--oneshot"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script is missing %q: %s", want, script)
		}
	}
	// cd first, then sudo: the other order runs cd as the login user and hands
	// sudo a directory it may not be able to enter.
	if strings.Index(script, "cd '/tmp'") > strings.Index(script, "sudo") {
		t.Errorf("sudo comes before the cd: %s", script)
	}
}

// A host entry is an ssh argument list, which is what lets a Lima VM — whose
// port is assigned at boot — be named by its generated config file.
func TestAHostEntryCanCarrySSHOptions(t *testing.T) {
	if got := hostArgs("sds@192.168.123.212"); len(got) != 1 || got[0] != "sds@192.168.123.212" {
		t.Errorf("a plain target was mangled: %v", got)
	}
	got := hostArgs("-F ~/.lima/sds-a/ssh.config lima-sds-a")
	if len(got) != 3 || got[0] != "-F" || got[2] != "lima-sds-a" {
		t.Fatalf("options were not split out: %v", got)
	}
	if strings.HasPrefix(got[1], "~") {
		t.Errorf("~ was left for a shell that is not there: %q", got[1])
	}
	// What the person sees is the machine, not the plumbing in front of it.
	if hostLabel("-F ~/.lima/sds-a/ssh.config lima-sds-a") != "lima-sds-a" {
		t.Error("the label is not the target")
	}
}

// Off is off. The zero value is what a settings file written before this
// existed unmarshals to, and an upgrade must not start running commands on
// other machines.
func TestRemoteAgentsAreOffUntilTurnedOn(t *testing.T) {
	var s Settings
	s.RemoteAgents.backfill()
	if s.RemoteAgents.Enabled {
		t.Error("backfilling the catalogue switched the feature on")
	}
	if len(s.RemoteAgents.Agents) == 0 {
		t.Fatal("the catalogue is empty, so @pi would never resolve")
	}
	if s.RemoteAgents.Has("pi") {
		t.Error("a name resolves while the feature is off")
	}
	s.RemoteAgents.Enabled = true
	if !s.RemoteAgents.Has("pi") || !s.RemoteAgents.Has("hermes") {
		t.Errorf("the cluster agents are missing: %v", s.RemoteAgents.Names())
	}

	// And a run refuses rather than reaching for ssh.
	res, err := NewRemoteRunner(RemoteAgents{}).Run(t.Context(), "pi", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed || !strings.Contains(res.Reason, "switched off") {
		t.Errorf("a run with the feature off got %+v", res)
	}
}

// Something a person configured by hand outranks what ships, or an upgrade
// would quietly undo their fix.
func TestAConfiguredAgentIsNotOverwritten(t *testing.T) {
	s := Settings{RemoteAgents: RemoteAgents{
		Agents: map[string]RemoteAgent{
			"pi": {Hosts: []string{"me@elsewhere"}, Command: []string{"pi", "{prompt}"}},
		},
	}}
	s.RemoteAgents.backfill()
	if got := s.RemoteAgents.Agents["pi"].Hosts[0]; got != "me@elsewhere" {
		t.Errorf("the built-in catalogue overwrote a configured agent: %q", got)
	}
	if _, ok := s.RemoteAgents.Agents["hermes"]; !ok {
		t.Error("backfilling stopped at the first name that was taken")
	}
}

// An entry that cannot be called must not be offered: a name in the menu that
// always fails is worse than a name that is not there.
func TestAnUncallableAgentIsDropped(t *testing.T) {
	r := RemoteAgents{Agents: map[string]RemoteAgent{
		"nohost": {Command: []string{"x", "{prompt}"}},
		"noargv": {Hosts: []string{"a@b"}},
		"blank":  {Hosts: []string{"  ", ""}, Command: []string{"x", "{prompt}"}},
		"fine":   {Hosts: []string{"a@b"}, Command: []string{"x", "{prompt}"}},
	}}
	r.normalize()
	if got := r.Names(); len(got) != 1 || got[0] != "fine" {
		t.Errorf("normalize kept agents that cannot run: %v", got)
	}
}

// The runner reports a missing name rather than erroring, because the caller
// is often a model and the sentence is what it needs to correct itself.
func TestAnUnknownAgentComesBackAsAReadableRefusal(t *testing.T) {
	r := RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{
		"pi": {Hosts: []string{"a@b"}, Command: []string{"x", "{prompt}"}},
	}}
	res, err := NewRemoteRunner(r).Run(t.Context(), "gpt", "hi")
	if err != nil {
		t.Fatalf("a bad name should not be an error: %v", err)
	}
	if !res.Failed || !strings.Contains(res.Reason, "pi") {
		t.Errorf("the refusal does not say what is available: %+v", res)
	}
}

// The model must not be able to switch this on for itself. It is the same rule
// external_agents follows, and here it is about another machine.
func TestTheModelCannotTurnRemoteAgentsOn(t *testing.T) {
	for _, key := range []string{"remote_agents", "remote_agents_enabled", "remote_agents.enabled"} {
		if _, ok := settingsWritable[key]; ok {
			t.Errorf("%q is writable by the agent", key)
		}
	}
}

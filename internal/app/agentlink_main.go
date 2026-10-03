package app

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
)

// buildVersion is this binary's module version, or its VCS revision.
func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// agentLinkFlags are the flags that link a process to core as an agent, the
// same for `superai agent` and `superai serve`. Each has an environment
// variable too, for a service unit or a container.
type agentLinkFlags struct {
	core, token, name *string
	tls               *bool
}

func addAgentLinkFlags(fl *flag.FlagSet) agentLinkFlags {
	return agentLinkFlags{
		core:  fl.String("core", os.Getenv("SUPERAI_CORE"), "core's agent link, host:port, to connect to as an agent (env SUPERAI_CORE)"),
		token: fl.String("core-token", os.Getenv("SUPERAI_CORE_TOKEN"), "a token core accepts: its own, or a paired device's (env SUPERAI_CORE_TOKEN)"),
		name:  fl.String("name", os.Getenv("SUPERAI_AGENT_NAME"), "the name core knows this agent by; the host name when empty (env SUPERAI_AGENT_NAME)"),
		tls:   fl.Bool("core-tls", os.Getenv("SUPERAI_CORE_TLS") == "1", "dial core with TLS (env SUPERAI_CORE_TLS=1)"),
	}
}

func (f agentLinkFlags) options() (AgentLinkOptions, bool) {
	if strings.TrimSpace(*f.core) == "" {
		return AgentLinkOptions{}, false
	}
	token := strings.TrimSpace(*f.token)
	// Or from a file only its owner can read, so the token is not in a unit
	// file or a launchd plist for everyone on the machine to see.
	if p := strings.TrimSpace(os.Getenv("SUPERAI_CORE_TOKEN_FILE")); token == "" && p != "" {
		if b, err := os.ReadFile(p); err == nil {
			token = strings.TrimSpace(string(b))
		} else {
			log.Printf("agent link: reading %s: %v", p, err)
		}
	}
	return AgentLinkOptions{Core: strings.TrimSpace(*f.core), Token: token, Name: strings.TrimSpace(*f.name),
		TLS: *f.tls, Version: buildVersion()}, true
}

// AgentMain is `superai agent`: this machine as an agent of a core, with no
// web surface of its own — it dials core and does what core asks of it.
func AgentMain(argv []string) {
	fl := flag.NewFlagSet("agent", flag.ExitOnError)
	lf := addAgentLinkFlags(fl)
	_ = fl.Parse(argv)
	o, ok := lf.options()
	if !ok {
		fmt.Fprintln(os.Stderr, "superai agent: -core host:port is required (or SUPERAI_CORE)")
		os.Exit(2)
	}
	if o.Token == "" {
		fmt.Fprintln(os.Stderr, "superai agent: -core-token is required (or SUPERAI_CORE_TOKEN, or SUPERAI_CORE_TOKEN_FILE)")
		os.Exit(2)
	}
	log.SetPrefix("superai-agent ")

	app := NewApp()
	app.startupHeadless()
	defer app.Shutdown(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	app.RunAgentLink(ctx, o)
	fmt.Fprintln(os.Stderr, "superai-agent: stopped")
}

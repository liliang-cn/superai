package app

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/liliang-cn/superleo/internal/backend"
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
// same for `superleo agent` and `superleo serve`. Each has an environment
// variable too, for a service unit or a container.
type agentLinkFlags struct {
	core, token, name *string
	tls               *bool
}

func addAgentLinkFlags(fl *flag.FlagSet) agentLinkFlags {
	return agentLinkFlags{
		core:  fl.String("core", os.Getenv("SUPERLEO_CORE"), "core's agent link, host:port, to connect to as an agent (env SUPERLEO_CORE)"),
		token: fl.String("core-token", os.Getenv("SUPERLEO_CORE_TOKEN"), "a token core accepts: its own, or a paired device's (env SUPERLEO_CORE_TOKEN)"),
		name:  fl.String("name", os.Getenv("SUPERLEO_AGENT_NAME"), "the name core knows this agent by; the host name when empty (env SUPERLEO_AGENT_NAME)"),
		tls:   fl.Bool("core-tls", os.Getenv("SUPERLEO_CORE_TLS") == "1", "dial core with TLS (env SUPERLEO_CORE_TLS=1)"),
	}
}

func (f agentLinkFlags) options() (AgentLinkOptions, bool) {
	if strings.TrimSpace(*f.core) == "" {
		return AgentLinkOptions{}, false
	}
	token := strings.TrimSpace(*f.token)
	// Or from a file only its owner can read, so the token is not in a unit
	// file or a launchd plist for everyone on the machine to see.
	if p := strings.TrimSpace(os.Getenv("SUPERLEO_CORE_TOKEN_FILE")); token == "" && p != "" {
		if b, err := os.ReadFile(p); err == nil {
			token = strings.TrimSpace(string(b))
		} else {
			slog.Warn("agent link: reading", "path", p, "error", err)
		}
	}
	return AgentLinkOptions{Core: strings.TrimSpace(*f.core), Token: token, Name: strings.TrimSpace(*f.name),
		TLS: *f.tls, Version: buildVersion()}, true
}

// AgentMain is `superleo agent`: this machine as an agent of a core, with no
// web surface of its own — it dials core and does what core asks of it.
func AgentMain(argv []string) {
	if len(argv) > 0 && (argv[0] == "join" || argv[0] == "leave") {
		var err error
		if argv[0] == "join" {
			err = agentJoin(argv[1:])
		} else {
			err = agentLeave()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	fl := flag.NewFlagSet("agent", flag.ExitOnError)
	lf := addAgentLinkFlags(fl)
	_ = fl.Parse(argv)
	o, ok := lf.options()
	if !ok {
		// As the service a join installed runs it: from what the join kept.
		o, ok = joinedOptions()
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "superleo agent: -core host:port is required (or SUPERLEO_CORE), or join first: superleo agent join <core address> <code>")
		os.Exit(2)
	}
	if o.Token == "" {
		fmt.Fprintln(os.Stderr, "superleo agent: -core-token is required (or SUPERLEO_CORE_TOKEN, or SUPERLEO_CORE_TOKEN_FILE)")
		os.Exit(2)
	}
	backend.SetupLogging("agent")

	app := NewApp()
	app.startupHeadless()
	defer app.Shutdown(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go app.serveCCHookSocket(ctx)
	go autoUpgradeCCHooks()
	go autoInstallCLIHooks()
	app.RunAgentLink(ctx, o)
	fmt.Fprintln(os.Stderr, "superleo-agent: stopped")
}

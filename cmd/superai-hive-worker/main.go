// superai-hive-worker lets an agent that is not a SuperAI be a worker in a hive.
//
// It does the two things a worker has to: it announces itself to the queen and
// keeps doing so, and it takes the orders the queen sends and reports back the
// way a SuperAI worker does. What actually does the work is whatever is behind
// it — a command-line agent, or a model behind an OpenAI-compatible endpoint.
//
// A command-line agent, its answer taken from what it prints:
//
//	superai-hive-worker -queen http://queen:43117 -name codex-box -- \
//	    codex exec {prompt}
//
// Claude Code, with its tool calls shown as they happen:
//
//	superai-hive-worker -queen http://queen:43117 -name claude-mac -mode claude -- \
//	    claude -p {prompt} --output-format stream-json --verbose --dangerously-skip-permissions
//
// An agent on another machine, over ssh. The order goes in on standard input,
// so the remote shell never parses it:
//
//	superai-hive-worker -queen http://queen:43117 -name openclaw -stdin -- \
//	    ssh -o BatchMode=yes host 'openclaw agent --message "$(cat)"'
//
// A model behind an API:
//
//	superai-hive-worker -queen http://queen:43117 -name gpt \
//	    -openai-base https://api.example.com/v1 -openai-model some-model
//
// {prompt} is the order, passed as one argument and never through a shell. The
// queen's token comes from -queen-token or $SUPERAI_HIVE_TOKEN.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

func main() {
	var (
		queen       = flag.String("queen", "", "the queen's address, e.g. http://192.168.123.214")
		queenToken  = flag.String("queen-token", os.Getenv("SUPERAI_HIVE_TOKEN"), "the bearer the queen accepts (or $SUPERAI_HIVE_TOKEN)")
		name        = flag.String("name", "", "this worker's name in the hive (letters, digits, . _ -)")
		about       = flag.String("about", "", "one line about what this worker is, for the roster")
		listen      = flag.String("listen", "0.0.0.0:0", "where to listen for orders; port 0 picks a free one")
		advertise   = flag.String("advertise", "", "the address the queen should use to reach this worker; default is this machine's address toward the queen")
		token       = flag.String("token", os.Getenv("SUPERAI_WORKER_TOKEN"), "the bearer the queen must present; default is a fresh random one, which the queen is told when this joins")
		concurrency = flag.Int("concurrency", 1, "orders to run at once; the rest wait")
		dir         = flag.String("dir", "", "working directory for the command")
		stdin       = flag.Bool("stdin", false, "send the order on the command's standard input instead of as {prompt} (use for ssh, so the order is never re-parsed by a remote shell)")
		mode        = flag.String("mode", "text", "how to read the command's output: text, or claude (Claude Code's stream-json)")
		oaBase      = flag.String("openai-base", "", "use a model behind this OpenAI-compatible base URL instead of a command")
		oaKey       = flag.String("openai-key", os.Getenv("OPENAI_API_KEY"), "its key (or $OPENAI_API_KEY)")
		oaModel     = flag.String("openai-model", "", "its model")
		oaSystem    = flag.String("openai-system", "", "a system prompt for it")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: superai-hive-worker -queen URL -name NAME [flags] [-- command {prompt} ...]")
		flag.PrintDefaults()
	}
	flag.Parse()
	log.SetPrefix("hive-worker ")
	log.SetFlags(log.LstdFlags)

	if *queen == "" || *name == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *queenToken == "" {
		log.Fatal("no queen token: pass -queen-token or set $SUPERAI_HIVE_TOKEN")
	}

	var engine backend.Engine
	switch {
	case *oaBase != "":
		if *oaModel == "" {
			log.Fatal("-openai-base needs -openai-model")
		}
		engine = &backend.OpenAIEngine{BaseURL: *oaBase, Key: *oaKey, Model: *oaModel, System: *oaSystem}
	case flag.NArg() > 0:
		e := &backend.ExecEngine{Argv: flag.Args(), Dir: *dir, Mode: *mode, Stdin: *stdin}
		if err := e.Validate(); err != nil {
			log.Fatalf("the command is not usable: %v", err)
		}
		engine = e
	default:
		log.Fatal("nothing to run: give a command after -- (with {prompt} in it), or -openai-base")
	}

	// A token nobody chose is one nobody has to keep: the queen is handed it in
	// the join, which is the only place it is ever needed.
	if *token == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			log.Fatal(err)
		}
		*token = hex.EncodeToString(b)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *listen, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	adv := strings.TrimRight(*advertise, "/")
	if adv == "" {
		host, err := outboundHost(*queen)
		if err != nil {
			log.Fatalf("cannot tell which of this machine's addresses the queen can reach (%v): pass -advertise", err)
		}
		adv = fmt.Sprintf("http://%s:%d", host, port)
	}

	ad := &backend.Adapter{Token: *token, Engine: engine, Concurrency: *concurrency}
	srv := &http.Server{Handler: ad.Handler()}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	ann := &backend.Announcer{
		Settings: backend.HiveSettings{Role: backend.HiveRoleWorker, Name: *name, JoinURL: *queen, JoinToken: *queenToken, AdvertiseURL: adv},
		Token:    *token,
		Engine:   engine.Describe(),
		About:    *about,
	}
	if err := ann.Validate(); err != nil {
		log.Fatalf("cannot join: %v", err)
	}
	log.Printf("%s (%s) listening on %s, joining %s", *name, engine.Describe(), adv, *queen)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); ann.Run(ctx) }()
	<-ctx.Done()
	// Run says goodbye to the queen on its way out; wait for it.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
}

// outboundHost is the local address the queen would see this machine as: the
// one the operating system would use to reach it. Nothing is sent.
func outboundHost(queen string) (string, error) {
	u, err := url.Parse(queen)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a URL", queen)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	c, err := net.Dial("udp", net.JoinHostPort(host, port))
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

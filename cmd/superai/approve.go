package main

// Approving a tool call from a terminal.
//
// The approval gate defaults on, and it denies anything it cannot get an
// answer to. That is the right outcome for a cron job and the wrong one for
// somebody sitting at a prompt watching this run, so the CLI attaches an
// approver when — and only when — there is a terminal on the other end of
// stdin. Piped or redirected input means a script, and a script is exactly the
// unattended case the gate exists for; it gets no approver and the deny that
// goes with it, which the tool result explains.
//
// Turning the gate off in Settings remains the way to run the CLI unattended
// with tools. That is deliberately a setting and not a flag: a flag on the
// command line is one copy-paste away from being in everyone's script.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/liliang-cn/superai-desktop/backend"
)

// stdinIsTerminal reports whether stdin is a tty. Character-device mode rather
// than a term package: this is the same check the shell tools in this repo
// make, and pulling in a dependency to ask one question about a file mode is
// not worth it.
func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// terminalApprover asks on stderr and reads the answer from stdin.
//
// stderr, not stdout: in plain mode stdout is the answer and nothing else, and
// a prompt mixed into it would break every pipe this command was built to sit
// in.
func terminalApprover(in *bufio.Reader) backend.Approver {
	return func(ctx context.Context, req backend.ApprovalRequest) (backend.ApprovalDecision, error) {
		fmt.Fprintf(os.Stderr, "\nSuperAI wants to run %s\n", req.Tool)
		if req.Command != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", req.Command)
		} else if len(req.Args) > 0 {
			fmt.Fprintf(os.Stderr, "  %v\n", req.Args)
		}
		fmt.Fprintf(os.Stderr, "allow? [y/N] ")

		// The read happens on its own goroutine because a bufio read from a
		// terminal is not interruptible: without this, a gate deadline or a
		// Ctrl-C would leave the run waiting on a keystroke that is never
		// coming.
		type reply struct {
			line string
			err  error
		}
		lines := make(chan reply, 1)
		go func() {
			line, err := in.ReadString('\n')
			lines <- reply{line, err}
		}()

		select {
		case r := <-lines:
			if r.err != nil {
				return backend.ApprovalDecision{Reason: "could not read an answer from the terminal"}, nil
			}
			answer := strings.ToLower(strings.TrimSpace(r.line))
			if answer == "y" || answer == "yes" {
				return backend.ApprovalDecision{Allowed: true, Reason: "approved at the terminal"}, nil
			}
			return backend.ApprovalDecision{Reason: "denied at the terminal"}, nil
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "(no answer — denied)")
			return backend.ApprovalDecision{}, nil
		}
	}
}

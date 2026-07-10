package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	ionhour "github.com/ionhour/ionhour-go"
)

// runExec implements `ionhour exec <token> -- <command> [args...]`. It runs the
// command with the CLI's own stdin/stdout/stderr inherited (so cron mail and
// logging are unchanged), times it, and pings success on exit 0 or fail on a
// non-zero exit — always carrying the duration and an {"exit_code": N} payload.
//
// The CLI's own exit code is ALWAYS the wrapped command's, so monitoring can
// never break the job. A ping that fails to send is a stderr warning only.
func runExec(args []string, stderr io.Writer) int {
	sep := separatorIndex(args)
	if sep < 0 {
		fmt.Fprintln(stderr, "ionhour: exec requires a '--' separator before the command, e.g. ionhour exec <token> -- <command>")
		return 2
	}
	flagArgs, command := args[:sep], args[sep+1:]

	fs := flag.NewFlagSet("ionhour exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	g := registerGlobalFlags(fs)
	noStart := fs.Bool("no-start", false, "do not send a start ping before the command")
	if err := fs.Parse(flagArgs); err != nil {
		return 2 // flag already printed the error and usage
	}

	rest := fs.Args()
	switch {
	case len(rest) == 0:
		fmt.Fprintln(stderr, "ionhour: exec requires a job token before '--'")
		return 2
	case len(rest) > 1:
		fmt.Fprintf(stderr, "ionhour: unexpected arguments before '--': %v (flags must precede the token)\n", rest[1:])
		return 2
	}
	token := rest[0]
	if len(command) == 0 {
		fmt.Fprintln(stderr, "ionhour: exec requires a command after '--'")
		return 2
	}

	job, err := g.newClient().RegisterJob(token)
	if err != nil {
		// InvalidTokenError / ErrEmptyToken deliberately omit the token itself.
		fmt.Fprintf(stderr, "ionhour: %v\n", err)
		return 2
	}

	// Resolve one run-correlation id up front so the start and terminal pings
	// correlate: honor --rid when given, otherwise generate a single id that both
	// pings reuse (execPingOptions and the start ping both read g.rid).
	if g.rid == "" {
		g.rid = ionhour.NewRID()
	}

	ctx := context.Background()

	// Best-effort start ping before the command launches. A failed start ping or
	// --no-start never affects the command or its exit code, and it is sent before
	// the timing clock so it does not inflate the measured duration.
	if !*noStart {
		emitStartPing(ctx, job, g, g.pingOptions(), stderr)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Put the child in its own process group so terminal signals reach the CLI
	// (which forwards them) rather than the child directly, and so a single
	// kill targets the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		code := startFailureExitCode(err)
		fmt.Fprintf(stderr, "ionhour: cannot execute command: %v\n", err)
		emitPing(ctx, job, true, g, execPingOptions(g, time.Since(start), code), stderr)
		return code
	}

	stopForwarding := forwardSignals(cmd)
	waitErr := cmd.Wait()
	stopForwarding()
	elapsed := time.Since(start)
	code := commandExitCode(waitErr)

	emitPing(ctx, job, code != 0, g, execPingOptions(g, elapsed, code), stderr)
	return code
}

// execPingOptions assembles the ping options for an exec ping: the shared
// run-correlation id (if any) plus the measured duration and exit-code payload.
func execPingOptions(g *globalFlags, d time.Duration, exitCode int) []ionhour.PingOption {
	return g.pingOptions(
		ionhour.WithDuration(d),
		ionhour.WithPayload(map[string]any{"exit_code": exitCode}),
	)
}

// separatorIndex returns the index of the first "--" argument, or -1 if absent.
func separatorIndex(args []string) int {
	for i, a := range args {
		if a == "--" {
			return i
		}
	}
	return -1
}

// forwardSignals relays SIGINT/SIGTERM to the child's process group until the
// returned stop function is called. Signals are sent to the negative pid so the
// whole group (the child and anything it spawned) receives them.
func forwardSignals(cmd *exec.Cmd) (stop func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				if cmd.Process != nil {
					if s, ok := sig.(syscall.Signal); ok {
						_ = syscall.Kill(-cmd.Process.Pid, s)
					}
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

// commandExitCode maps the error from exec.Cmd.Wait to a process exit code,
// following shell conventions: a clean exit is its own status, and a command
// killed by a signal reports 128+signal (e.g. SIGTERM -> 143).
func commandExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	// Wait failed for a reason other than a non-zero exit (rare): treat as a
	// generic failure so the job is still reported down.
	return 1
}

// startFailureExitCode maps a Cmd.Start failure to the shell convention: 126 for
// a command that exists but cannot be executed (permission), 127 otherwise
// (not found on PATH, missing file).
func startFailureExitCode(err error) int {
	if errors.Is(err, os.ErrPermission) {
		return 126
	}
	return 127
}

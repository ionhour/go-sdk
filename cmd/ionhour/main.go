// Command ionhour is the Ionhour heartbeat CLI: it monitors an existing crontab
// with zero code changes. Wrap any command with `ionhour exec <token> -- ...`
// and Ionhour learns when the job ran, how long it took, and whether it failed —
// or send a bare `ping`/`fail` from a shell script.
//
// It is a thin wrapper over the Go SDK ([github.com/ionhour/go-sdk]); every
// ping goes through the same client, retries, and credential handling.
//
// Usage:
//
//	ionhour exec <job-token> -- <command> [args...]   run a command, report the outcome
//	ionhour ping <job-token>                           send a success heartbeat
//	ionhour fail <job-token>                           send a failure heartbeat
//	ionhour start <job-token>                          send a run-started telemetry ping
//	ionhour version                                    print the version
//
// The wrapped command's exit code is always passed through unchanged: a ping
// that cannot be delivered is reported on stderr but never breaks the cron job.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	ionhour "github.com/ionhour/go-sdk"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it dispatches the subcommand and returns the
// process exit code. os.Stdin/os.Stdout/os.Stderr are still inherited by the
// child of `exec`; stdout and stderr here carry only the CLI's own output.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "exec":
		return runExec(args[1:], stderr)
	case "ping":
		return runPing(args[1:], false, stderr)
	case "fail":
		return runPing(args[1:], true, stderr)
	case "start":
		return runStart(args[1:], stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ionhour %s\n", ionhour.Version)
		return 0
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "ionhour: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

// globalFlags holds the options shared by exec, ping, fail, and start. Each is
// filled from a --flag whose default is the matching environment variable, so
// an explicit flag beats the environment, which beats the built-in default.
type globalFlags struct {
	baseURL string
	timeout time.Duration
	rid     string
	quiet   bool
}

// registerGlobalFlags binds the shared flags onto fs and returns the destination
// struct. Reading os.Getenv here (rather than in the client) makes the flag's
// help text show the resolved default and keeps precedence in one place.
func registerGlobalFlags(fs *flag.FlagSet) *globalFlags {
	g := &globalFlags{}
	fs.StringVar(&g.baseURL, "base-url", os.Getenv("IONHOUR_BASE_URL"), "Ionhour ingestion base URL (env IONHOUR_BASE_URL)")
	fs.DurationVar(&g.timeout, "timeout", ionhour.DefaultTimeout, "per-attempt ping timeout")
	fs.StringVar(&g.rid, "rid", "", "run correlation id (rid query parameter)")
	fs.BoolVar(&g.quiet, "quiet", false, "suppress ping delivery warnings on stderr")
	return g
}

// newClient builds an SDK client from the resolved flags. Options are only added
// when set so an empty value falls through to the SDK's own env/default logic
// instead of clobbering it.
func (g *globalFlags) newClient() *ionhour.Client {
	opts := []ionhour.ClientOption{ionhour.WithTimeout(g.timeout)}
	if g.baseURL != "" {
		opts = append(opts, ionhour.WithBaseURL(g.baseURL))
	}
	return ionhour.NewClient(opts...)
}

// pingOptions returns the shared ping options (the run-correlation id, when set)
// followed by any per-call extras. It allocates a fresh slice each call so
// callers can't alias one another's options.
func (g *globalFlags) pingOptions(extra ...ionhour.PingOption) []ionhour.PingOption {
	var opts []ionhour.PingOption
	if g.rid != "" {
		opts = append(opts, ionhour.WithRID(g.rid))
	}
	return append(opts, extra...)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `ionhour — heartbeat monitoring for your cron jobs

Usage:
  ionhour exec <job-token> -- <command> [args...]   Run a command and report success/failure
  ionhour ping <job-token>                          Send a success heartbeat
  ionhour fail <job-token>                          Send a failure heartbeat
  ionhour start <job-token>                         Send a run-started telemetry ping
  ionhour version                                   Print the version

exec sends a start ping before the command and the terminal ping after it,
sharing one run-correlation id (--rid if given, otherwise generated).

Flags (given before the token, on exec/ping/fail/start):
  --base-url   Ingestion base URL          (env IONHOUR_BASE_URL)
  --timeout    Per-attempt ping timeout    (default 10s)
  --rid        Run correlation id
  --quiet      Suppress ping warnings on stderr
  --no-start   (exec only) Do not send the start ping

The token is a credential and is never printed. A ping that cannot be delivered
is reported on stderr and never changes the wrapped command's exit code.

Docs: https://docs.ionhour.com
`)
}

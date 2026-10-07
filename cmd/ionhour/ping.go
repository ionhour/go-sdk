package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"

	ionhour "github.com/ionhour/go-sdk"
)

// runPing implements the bare `ionhour ping <token>` and `ionhour fail <token>`
// commands. Unlike exec there is no wrapped command, so the exit code reflects
// delivery: 0 when the ping reached Ionhour, 1 on a transport failure, and 2 on
// a usage error (bad token or flags).
func runPing(args []string, fail bool, stderr io.Writer) int {
	name := "ping"
	if fail {
		name = "fail"
	}

	job, g, code := parseSingleTokenCmd(name, args, stderr)
	if job == nil {
		return code
	}
	if emitPing(context.Background(), job, fail, g, g.pingOptions(), stderr) {
		return 0
	}
	return 1
}

// runStart implements `ionhour start <token>`: a bare persist-only start ping
// for scripts that report run boundaries themselves. Exit codes match ping/fail.
func runStart(args []string, stderr io.Writer) int {
	job, g, code := parseSingleTokenCmd("start", args, stderr)
	if job == nil {
		return code
	}
	if emitStartPing(context.Background(), job, g, g.pingOptions(), stderr) {
		return 0
	}
	return 1
}

// parseSingleTokenCmd parses the shared flags and the single job token common to
// ping, fail, and start. On success it returns the registered job and resolved
// flags with code 0; on a usage error it returns a nil job and the exit code to
// propagate (the message has already been written to stderr).
func parseSingleTokenCmd(name string, args []string, stderr io.Writer) (*ionhour.Job, *globalFlags, int) {
	fs := flag.NewFlagSet("ionhour "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	g := registerGlobalFlags(fs)
	if err := fs.Parse(args); err != nil {
		return nil, nil, 2 // flag already printed the error and usage
	}

	rest := fs.Args()
	switch {
	case len(rest) == 0:
		fmt.Fprintf(stderr, "ionhour: %s requires a job token\n", name)
		return nil, nil, 2
	case len(rest) > 1:
		fmt.Fprintf(stderr, "ionhour: unexpected arguments: %v\n", rest[1:])
		return nil, nil, 2
	}

	job, err := g.newClient().RegisterJob(rest[0])
	if err != nil {
		// InvalidTokenError / ErrEmptyToken deliberately omit the token itself.
		fmt.Fprintf(stderr, "ionhour: %v\n", err)
		return nil, nil, 2
	}
	return job, g, 0
}

// emitPing sends one ping (success or fail) and, unless --quiet, warns on a
// delivery problem. It returns whether the ping was delivered so bare ping/fail
// can map that to an exit code; exec ignores the return so a ping never alters
// the wrapped command's exit code.
func emitPing(ctx context.Context, job *ionhour.Job, fail bool, g *globalFlags, opts []ionhour.PingOption, stderr io.Writer) bool {
	var (
		res ionhour.PingResult
		err error
	)
	if fail {
		res, err = job.Fail(ctx, opts...)
	} else {
		res, err = job.Success(ctx, opts...)
	}

	delivered := err == nil && res.OK
	if delivered || g.quiet {
		return delivered
	}
	kind := "heartbeat"
	if fail {
		kind = "failure"
	}
	warnUndelivered(stderr, g, job, kind, pingReason(res, err))
	return false
}

// emitStartPing sends a best-effort persist-only start ping and, unless --quiet,
// warns on a delivery problem. It returns whether the ping was delivered (used
// by the bare `start` command); exec ignores the return so a failed start ping
// never affects the wrapped command's exit code.
func emitStartPing(ctx context.Context, job *ionhour.Job, g *globalFlags, opts []ionhour.PingOption, stderr io.Writer) bool {
	res, err := job.Start(ctx, opts...)
	delivered := err == nil && res.OK
	if delivered || g.quiet {
		return delivered
	}
	warnUndelivered(stderr, g, job, "start", pingReason(res, err))
	return false
}

// pingReason picks the error to report for an undelivered ping: the usage error
// (second return) when present, otherwise the transport error in the result.
func pingReason(res ionhour.PingResult, err error) error {
	if err != nil { // a usage error (e.g. an oversized payload) — rare on the CLI path
		return err
	}
	return res.Err
}

// warnUndelivered writes the single stderr warning for an undelivered ping. The
// message is scrubbed of the token and API key: transport errors from net/http
// embed the request URL, which carries the token in its path.
func warnUndelivered(stderr io.Writer, g *globalFlags, job *ionhour.Job, kind string, reason error) {
	fmt.Fprintf(stderr, "ionhour: %s ping was not delivered: %s\n", kind, scrubSecrets(reason.Error(), job.Token(), g.apiKey))
}

// scrubSecrets removes the job token (raw and URL-path-escaped) and the API key
// from a message before it is printed. A transport error's message includes the
// ping URL — token and all — so printing it verbatim would leak the credential.
func scrubSecrets(msg, token, apiKey string) string {
	for _, secret := range []string{token, url.PathEscape(token), apiKey} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "<redacted>")
		}
	}
	return msg
}

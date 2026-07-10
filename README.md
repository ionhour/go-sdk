# ionhour-go

The official Go SDK for [Ionhour](https://ionhour.com) heartbeat monitoring.

Register **Jobs** (inbound heartbeat monitors) with two parameters — a job token
and an optional API key — and report success or failure with a single call.
Zero third-party dependencies; standard library only.

## Install

```bash
go get github.com/ionhour/ionhour-go
```

Requires Go 1.21 or newer.

## Quick start

A heartbeat is just an HTTP GET. The raw ping — a 10s timeout and up to 3
attempts — is one line of `curl` (`--retry` counts *additional* attempts, so 2
matches the SDK's 3 total):

```bash
curl -m 10 --retry 2 https://signal.ionhour.com/api/signals/ping/your-job-token
```

The SDK sends that **same ping**, with the retries, timeouts, and failure
signaling handled for you. The shortest path is the package-level,
two-parameter registration backed by a default client that reads
`IONHOUR_API_KEY` / `IONHOUR_BASE_URL` from the environment — three meaningful
lines, no config files:

```go
package main

import (
	"context"

	ionhour "github.com/ionhour/ionhour-go"
)

func main() {
	// Second parameter is the API key. Pass "" for an unprotected job — the
	// token alone is the credential.
	job, _ := ionhour.RegisterJob("your-job-token", "")
	job.Success(context.Background())
}
```

`RegisterJob` makes **no network call** — it only validates the token — so the
error it returns is non-nil solely for a malformed token; you can ignore it for
a hard-coded token. `Success` **never panics**: its returned error is non-nil
only for a usage error (e.g. an oversized payload), and the transport outcome
lives in the returned `PingResult.OK`. For fire-and-forget monitoring, ignore
both:

```go
job, err := ionhour.RegisterJob("your-job-token", "")
if err != nil {
	panic(err) // registration only fails on a malformed token
}
res, err := job.Success(context.Background())
_ = res
_ = err
```

For a **protected** job, pass a workspace API key (`ionh_...`) as the second
argument; the SDK sends it as `Authorization: Bearer <key>` on every ping:

```go
job, _ := ionhour.RegisterJob("your-job-token", "ionh_live_...")
```

> **Env freeze.** The package-level default client is created once, on first use,
> and captures `IONHOUR_API_KEY` / `IONHOUR_BASE_URL` at that moment for the
> lifetime of the process. Changing those variables later has no effect on it —
> build an explicit client with [`NewClient`](#configuring-a-client) if you need
> the environment re-read.

## Wrapping work with `Run` and `RunValue`

`Run` times your function with a monotonic clock, pings **success** with the
measured duration when it returns nil, or pings **fail** (with the error message
in the payload) and re-returns your error when it fails. Ping-transport problems
are never allowed to mask your function's own result.

```go
err := job.Run(context.Background(), func(ctx context.Context) error {
	return backupDatabase(ctx)
})
// err is backupDatabase's error, unchanged. The fail ping has already been sent.
```

When the wrapped work **returns a value**, use the package-level generic
`RunValue` so you don't need an outer closure variable to carry the result
(Go methods can't take type parameters, so it takes the job explicitly):

```go
rows, err := ionhour.RunValue(context.Background(), job, func(ctx context.Context) (int, error) {
	return importRows(ctx)
})
// rows is importRows's value on success, or the zero value if it returned an error.
```

`RunValue` has the same semantics as `Run` — timed success ping on nil error,
fail ping plus your error on failure, its own ping errors swallowed — and
returns `(T, error)`. On error it returns the zero value of `T` alongside your
error.

Before invoking your function, both `Run` and `RunValue` also send a best-effort
**start** ping and share a run-correlation id across the start and terminal pings
(your `WithRID` when supplied, otherwise one from `NewRID`) so the run's duration
correlates end-to-end. Disable the start ping with `WithoutStartPing()`.

## Run-started telemetry

Send a start ping yourself with `job.Start` when you drive the run boundary
directly. It is **persist-only**: it never changes monitor status, opens or
resolves incidents, or resets the schedule — a job that starts and then hangs
still goes LATE. Pair it with the terminal ping via a shared `rid`:

```go
rid := ionhour.NewRID()
job.Start(context.Background(), ionhour.WithRID(rid))
elapsed := doWork()
job.Success(context.Background(), ionhour.WithRID(rid), ionhour.WithDuration(elapsed))
```

Attach richer telemetry to any ping with `ionhour.WithHost`, `ionhour.WithEnv`,
and `ionhour.WithExitCode` (0–255).

## Explicit failure

Report a failure immediately (the `/fail` endpoint) so the monitor goes DOWN
without waiting for the schedule and grace period to lapse:

```go
res, err := job.Fail(context.Background(), ionhour.WithPayload(map[string]any{
	"reason": "upstream timeout",
}))
// err != nil only if the payload is a usage error; res.OK is the transport outcome.
_ = res
_ = err
```

## Configuring a client

Build a client with functional options when you need more than the defaults:

```go
client := ionhour.NewClient(
	ionhour.WithAPIKey("ionh_live_..."),
	ionhour.WithTimeout(5*time.Second),
	ionhour.WithRetries(4),
	ionhour.WithLogger(slog.Default()),
)

job, _ := client.RegisterJob("your-job-token")
if _, err := job.Success(context.Background(), ionhour.WithRID("run-42")); err != nil {
	// Only a usage error lands here (e.g. an oversized payload).
	log.Printf("ionhour usage error: %v", err)
}
```

A per-job key overrides the client key for a single job:

```go
job, _ := client.RegisterJob("protected-token", ionhour.WithJobAPIKey("ionh_live_..."))
```

### Client options

| Option              | Default                          | Notes                                                                 |
|---------------------|----------------------------------|-----------------------------------------------------------------------|
| `WithBaseURL`       | `https://signal.ionhour.com/api` | Env fallback: `IONHOUR_BASE_URL`.                                     |
| `WithAPIKey`        | none                             | Env fallback: `IONHOUR_API_KEY`. Global default; a per-job key wins.  |
| `WithTimeout`       | `10s`                            | Per attempt, applied via context.                                    |
| `WithRetries`       | `3`                              | **Total** attempts. Backoff 1s then 2s, clamped (every later wait stays 2s — never linear or exponential). Retries only on network errors, 429, and 5xx — never other 4xx. |
| `WithUserAgent`     | `ionhour-go/0.1.0`               | Empty values are ignored.                                            |
| `WithHTTPClient`    | `&http.Client{}`                 | Supply a custom transport/proxy. Nil is ignored.                     |
| `WithLogger`        | none                             | `*slog.Logger`; ping failures logged at error level when set.        |

### Ping options

| Option             | Effect                                                                    |
|--------------------|---------------------------------------------------------------------------|
| `WithRID`          | Sets the `rid` run-correlation query parameter. Pairs a start ping with its terminal ping — mint one with `NewRID`. |
| `WithDuration`     | Sets the `duration` query parameter in whole milliseconds.                |
| `WithPayload`      | Attaches a JSON body (switches to POST). Max 10 KiB after encoding.       |
| `WithHost`         | Sets the `host` query parameter. Silently truncated past 253 characters.  |
| `WithEnv`          | Sets the `env` query parameter. Silently truncated past 32 characters.    |
| `WithExitCode`     | Sets the `exit_code` query parameter. Must be 0–255, else a usage error (no request sent). |
| `WithoutStartPing` | `Run`/`RunValue` only: skip the automatic start ping. Ignored on a direct ping. |

`WithHost`/`WithEnv` are never auto-detected — the SDK sends them only when you
set them explicitly.

## Error philosophy

Monitoring must never take down the host application:

- `Success`, `Fail`, `Start`, and `Ping` return `(PingResult, error)` and
  **never panic**. The `error` is the loud channel: it is non-nil **only** for a
  usage (programmer) error — an oversized (> 10 KiB) or non-serializable payload,
  or an out-of-range `WithExitCode` — in which case no request is sent. Every
  transport outcome (network error, `429`
  or `5xx` after retries, cancellation) is reported inside the `PingResult`
  (`{ OK bool; Status int; Err error }`) with a **nil** `error` return, so a
  failure to reach Ionhour can never force you to handle an error. For
  fire-and-forget monitoring, ignore both return values.
- `Run` (and the value-returning `RunValue`) **re-returns your function's error**
  (so you always see your own failure) but swallows its own ping errors — both
  transport and usage — logging them when a logger is configured.
- Programmer errors are loud: an empty or whitespace token fails at
  `RegisterJob`, and an oversized or non-serializable payload is returned as the
  `error` (and also mirrored in `PingResult.Err`) before any request is sent.

You only need an API key for **protected** jobs. For an unprotected job the
token is the credential — pass `""` (package-level) or omit `WithAPIKey`.

## Credential handling

- **Whitespace trimming.** API keys (from `WithAPIKey`, `WithJobAPIKey`,
  `IONHOUR_API_KEY`, or the package-level `RegisterJob` argument) are trimmed of
  surrounding whitespace, and a whitespace-only value is treated as absent. A
  stray newline from a `.env` file can never turn into a malformed `Bearer`
  header that fails as a silent `401`.
- **Redaction in logs.** `Client` and `Job` implement `fmt.Stringer` and redact
  the token and API key, so logging one with `%v` or `%+v` will not leak the
  credential. Note the Go limitation: `%#v` bypasses `Stringer` and still prints
  the unexported fields — avoid `%#v` on a `Client` or `Job`.

## CLI

The same module ships an `ionhour` command — the zero-code-change way to monitor
an **existing** crontab. Wrap any command and Ionhour learns when it ran, how
long it took, and whether it failed. The CLI is a thin wrapper over this SDK, so
it inherits the same retries, timeouts, and credential handling.

```bash
go install github.com/ionhour/ionhour-go/cmd/ionhour@latest
```

The flagship is `exec`: it sends a **start** ping, runs your command with
stdin/stdout/stderr inherited (so cron mail and logs are unchanged), times it,
and pings **success** on exit 0 or **fail** on any non-zero exit — each with the
duration and an `{"exit_code": N}` payload. The start and terminal pings share
one run-correlation id (`--rid` if given, otherwise generated), so the run
correlates end-to-end; pass `--no-start` to skip the start ping. Turning a
monitored cron line on is a one-token diff:

```cron
# before
0 2 * * * /usr/local/bin/nightly-backup.sh

# after — same command, now monitored
0 2 * * * ionhour exec nightly-backup-token -- /usr/local/bin/nightly-backup.sh
```

The `--` separator is required and everything after it is the command, run
verbatim. **The CLI's exit code is always the wrapped command's exit code** — a
ping that can't be delivered is a one-line `ionhour:`-prefixed warning on stderr
and never breaks the cron job. SIGINT/SIGTERM are forwarded to the command's
process group, and a command that can't start reports `127` (not found) or `126`
(not executable), matching shell conventions.

Bare pings are there for shell scripts that already know their own outcome:

```bash
ionhour start <job-token>   # run-started telemetry ping (persist-only)
ionhour ping <job-token>    # success heartbeat
ionhour fail <job-token>    # explicit failure — takes the monitor DOWN now
ionhour version
```

Unlike `exec` (which never lets a ping change your command's exit code), a bare
`start`/`ping`/`fail` exits non-zero if the ping could not be delivered, so a
health-check script can react to it. A `start` ping is persist-only telemetry —
it records that a run began but never changes monitor status on its own.

### CLI flags

Flags go **before** the token (e.g. `ionhour exec --rid run-42 <token> -- ...`).

| Flag         | Default                  | Notes                                                        |
|--------------|--------------------------|--------------------------------------------------------------|
| `--api-key`  | env `IONHOUR_API_KEY`    | Workspace API key for protected jobs (`Authorization: Bearer`). |
| `--base-url` | env `IONHOUR_BASE_URL`   | Ingestion base URL. Falls back to the default endpoint.      |
| `--timeout`  | `10s`                    | Per-ping-attempt timeout.                                    |
| `--rid`      | none                     | Run-correlation id, sent as the `rid` query parameter. Shared by exec's start + terminal pings. |
| `--quiet`    | `false`                  | Suppress ping-delivery warnings on stderr.                   |
| `--no-start` | `false`                  | `exec` only: do not send the start ping before the command.  |

An explicit flag beats its environment variable, which beats the built-in
default. The job token is a credential and is **never** printed — even transport
errors (which embed the ping URL) are scrubbed before they reach stderr.

## Development

The package has zero third-party dependencies, so the full check (format, vet,
and race-enabled tests) runs in a throwaway Go container without installing a
local toolchain — run it from the repository root:

```bash
docker run --rm -v "$PWD/sdks/go":/src -w /src golang:1.22 sh -c "gofmt -l . && go vet ./... && go test -race ./..."
```

`gofmt -l .` printing nothing means every file is formatted.

## Documentation

Full docs: [docs.ionhour.com](https://docs.ionhour.com).

## License

[MIT](./LICENSE)

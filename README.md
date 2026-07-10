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

The simplest install: the package-level, two-parameter registration backed by a
default client that reads `IONHOUR_API_KEY` / `IONHOUR_BASE_URL` from the
environment.

```go
package main

import (
	"context"

	ionhour "github.com/ionhour/ionhour-go"
)

func main() {
	// Second parameter is the API key. Pass "" for an unprotected job — the
	// token alone is the credential.
	job, err := ionhour.RegisterJob("your-job-token", "")
	if err != nil {
		panic(err) // registration only fails on a malformed token
	}

	// Report a successful run. This never panics. The returned error is non-nil
	// only for a usage error (e.g. an oversized payload); the transport outcome
	// is in res.OK. For fire-and-forget monitoring you can ignore both.
	res, err := job.Success(context.Background())
	_ = res
	_ = err
}
```

For a **protected** job, pass a workspace API key (`ionh_...`) as the second
argument; the SDK sends it as `Authorization: Bearer <key>` on every ping:

```go
job, _ := ionhour.RegisterJob("your-job-token", "ionh_live_...")
```

## Wrapping work with `Run`

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
| `WithRetries`       | `3`                              | **Total** attempts. Backoff 1s then 2s. Retries only on network errors, 429, and 5xx — never other 4xx. |
| `WithUserAgent`     | `ionhour-go/0.1.0`               | Empty values are ignored.                                            |
| `WithHTTPClient`    | `&http.Client{}`                 | Supply a custom transport/proxy. Nil is ignored.                     |
| `WithLogger`        | none                             | `*slog.Logger`; ping failures logged at error level when set.        |

### Ping options

| Option           | Effect                                                                    |
|------------------|---------------------------------------------------------------------------|
| `WithRID`        | Sets the `rid` run-correlation query parameter.                           |
| `WithDuration`   | Sets the `duration` query parameter in whole milliseconds.                |
| `WithPayload`    | Attaches a JSON body (switches to POST). Max 10 KiB after encoding.       |

## Error philosophy

Monitoring must never take down the host application:

- `Success`, `Fail`, and `Ping` return `(PingResult, error)` and **never panic**.
  The `error` is the loud channel: it is non-nil **only** for a usage
  (programmer) error — an oversized (> 10 KiB) or non-serializable payload — in
  which case no request is sent. Every transport outcome (network error, `429`
  or `5xx` after retries, cancellation) is reported inside the `PingResult`
  (`{ OK bool; Status int; Err error }`) with a **nil** `error` return, so a
  failure to reach Ionhour can never force you to handle an error. For
  fire-and-forget monitoring, ignore both return values.
- `Run` **re-returns your function's error** (so you always see your own
  failure) but swallows its own ping errors — both transport and usage — logging
  them when a logger is configured.
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

## Documentation

Full docs: [docs.ionhour.com](https://docs.ionhour.com).

## License

[MIT](./LICENSE)

# Changelog

All notable changes to the Ionhour Go SDK (`github.com/ionhour/go-sdk`) are documented here. This project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-10

Initial release.

### Added

- `ionhour exec` reports the wrapped command's exit status as the `exit_code=`
  query parameter defined by the SDK contract (not as a JSON body), so exec pings
  are plain GETs and never touch the 10 KiB payload cap.

- `NewClient` with functional options: `WithAPIKey`, `WithBaseURL`,
  `WithHTTPClient`, `WithTimeout`, `WithRetries`, `WithUserAgent`, `WithLogger`.
  Environment fallbacks (`IONHOUR_API_KEY`, `IONHOUR_BASE_URL`) applied when the
  corresponding option is absent.
- `Client.RegisterJob(jobToken, ...JobOption)` and the package-level
  two-parameter convenience `RegisterJob(jobToken, apiKey)`. `WithJobAPIKey`
  supplies a per-job key that overrides the client key. Registration validates
  the token locally (non-empty, no whitespace) and makes no network call.
- `Job.Success` / `Job.Ping`, `Job.Fail`, `Job.Start`, and the `Job.Run` wrapper.
  Pings never panic and report their outcome via `PingResult`; `Run` re-returns
  the wrapped function's error while swallowing ping-transport errors. `Job.Start`
  sends the persist-only run-started ping (`/start`), which never changes monitor
  status, opens/resolves incidents, or resets the schedule.
- `RunValue[T](ctx, job, fn, opts...)`, a package-level generic with the same
  semantics as `Job.Run` for value-returning work: it returns `(T, error)`,
  handing back fn's value on success and the zero value of `T` on error. (Go
  methods cannot take type parameters, so it takes the job explicitly.) A nil job
  or nil fn is a usage error returned loudly — `Job.Run` and `RunValue` never
  panic on a nil job (including `Job.Run` on a nil receiver).
- `Job.Run` and `RunValue` send a best-effort start ping before the wrapped
  function and share a run-correlation id across the start and terminal pings —
  the caller's `WithRID` when supplied, otherwise one from the exported `NewRID`
  (16 hex characters). The start ping carries only the rid plus any
  `WithHost`/`WithEnv`; its failures are swallowed like every internal run ping.
  `WithoutStartPing()` opts out.
- Ping options `WithRID`, `WithDuration`, `WithPayload` (JSON body, 10 KiB cap
  enforced client-side), `WithHost` (silently truncated past 253 characters),
  `WithEnv` (silently truncated past 32 characters), `WithExitCode` (0–255, else
  an `ExitCodeRangeError` returned on the loud usage-error channel with no request
  sent), and `WithoutStartPing` (Run/RunValue only; ignored on a direct ping).
- Wire protocol per the Ionhour SDK contract: GET without a payload, POST JSON
  with a payload, `/fail` and `/start` suffixes, `rid`, `duration`, `host`, `env`,
  and `exit_code` query parameters, `Authorization: Bearer <key>` when a key is
  present, and the User-Agent `ionhour-go/0.1.0`. Retries (3 attempts) on network
  errors, 429, and 5xx only; other 4xx are never retried; context cancellation
  aborts without a retry sleep. Backoff is clamped — 1s before the second attempt,
  then 2s before every subsequent one (1s, 2s, 2s, …) — so it never grows
  linearly or exponentially.
- Defensive credential handling: the job token is path-escaped when building the
  ping URL; API keys are whitespace-trimmed on entry (option, env, and
  package-level argument) with a whitespace-only value treated as absent; and
  `Client`/`Job` redact the token and key in their `fmt.Stringer` output.
- Transport-error text is scrubbed: net/http's `*url.Error` embeds the request
  URL — and therefore the job token — in its message, so any transport error
  reaching the automatic logger or `PingResult.Err` has the token (raw and
  URL-encoded) and API key replaced with a short redacted prefix. The underlying
  error stays wrapped, so `errors.Is` / `errors.As` chains are unaffected.
- `ionhour` CLI (`cmd/ionhour`, `go install github.com/ionhour/go-sdk/cmd/ionhour@latest`)
  for monitoring an existing crontab with zero code changes. `ionhour exec
  <token> -- <command>` sends a start ping, runs the command with
  stdin/stdout/stderr inherited, times it, and pings success (exit 0) or fail
  (non-zero) with the duration and an `{"exit_code": N}` payload. The start and
  terminal pings share one run-correlation id (`--rid` if given, otherwise
  generated); `--no-start` skips the start ping, and a failed start ping never
  affects the command or its exit code. The CLI's exit code always passes through
  the wrapped command's — a ping that fails to send is a stderr warning only and
  never breaks the cron job; SIGINT/SIGTERM are forwarded to the child's process
  group; a command that can't start reports 127 (not found) / 126 (not
  executable). Bare `ionhour start` / `ionhour ping` / `ionhour fail` cover shell
  scripts (each exits non-zero on an undelivered ping), and `ionhour version`
  prints the version. Flags `--api-key`, `--base-url`, `--timeout`, `--rid`,
  `--quiet`, and `--no-start` (exec only), each defaulting to its env var where
  applicable. The token is never printed, and transport errors (which embed the
  ping URL) are scrubbed before reaching stderr. Built entirely on the SDK client
  — no reimplemented ping path, zero third-party dependencies.

[0.1.0]: https://github.com/ionhour/go-sdk/releases/tag/v0.1.0

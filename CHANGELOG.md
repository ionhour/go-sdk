# Changelog

All notable changes to `ionhour-go` are documented here. This project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-10

Initial release.

### Added

- `NewClient` with functional options: `WithAPIKey`, `WithBaseURL`,
  `WithHTTPClient`, `WithTimeout`, `WithRetries`, `WithUserAgent`, `WithLogger`.
  Environment fallbacks (`IONHOUR_API_KEY`, `IONHOUR_BASE_URL`) applied when the
  corresponding option is absent.
- `Client.RegisterJob(jobToken, ...JobOption)` and the package-level
  two-parameter convenience `RegisterJob(jobToken, apiKey)`. `WithJobAPIKey`
  supplies a per-job key that overrides the client key. Registration validates
  the token locally (non-empty, no whitespace) and makes no network call.
- `Job.Success` / `Job.Ping`, `Job.Fail`, and the `Job.Run` wrapper. Pings never
  panic and report their outcome via `PingResult`; `Run` re-returns the wrapped
  function's error while swallowing ping-transport errors.
- Ping options `WithRID`, `WithDuration`, and `WithPayload` (JSON body, 10 KiB
  cap enforced client-side).
- Wire protocol per the Ionhour SDK contract: GET without a payload, POST JSON
  with a payload, `/fail` suffix, `rid` and `duration` query parameters,
  `Authorization: Bearer <key>` when a key is present, and the User-Agent
  `ionhour-go/0.1.0`. Retries (3 attempts, 1s/2s backoff) on network errors,
  429, and 5xx only; other 4xx are never retried; context cancellation aborts
  without a retry sleep.
- Defensive credential handling: the job token is path-escaped when building the
  ping URL; API keys are whitespace-trimmed on entry (option, env, and
  package-level argument) with a whitespace-only value treated as absent; and
  `Client`/`Job` redact the token and key in their `fmt.Stringer` output.

[0.1.0]: https://github.com/ionhour/ionhour-go/releases/tag/v0.1.0

# Changelog

All notable changes to the Ionhour Integration API Go client
(`github.com/ionhour/go-sdk/api`) are documented here. This project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release tags for this
nested module are prefixed `api/` (for example `api/v0.1.0`).

## [Unreleased]

## [0.1.0] - 2026-10-13

### Added

- Generated client for the Integration API v1 (oapi-codegen v2.4.1, from
  `apps/api-gateway/src/app/openapi/ionhour-v1.json`): one method per
  operationId (`ChecksCreateWithResponse`, `JobsGetOneWithResponse`, ...) and
  typed request/response models. Nullable fields use
  `github.com/oapi-codegen/nullable`, so PATCH bodies can send `null` to clear.
- `NewClient` facade with `WithAPIKey`, `WithBaseURL`, `WithHTTPClient`,
  `WithUserAgent`, `WithTimeout`, `WithMaxAttempts`, `WithMaxRetryWait`, and
  `IONHOUR_API_KEY` / `IONHOUR_API_BASE_URL` environment fallbacks.
- Contract retry policy as a RoundTripper: never 4xx; 429 honours Retry-After
  up to 60 s (longer is returned at once); 5xx and network errors back off 1s,
  2s, 2s for at most 3 attempts. Among POSTs only checks_create, jobs_create
  and projects_create (the Idempotency-Key allowlist) are replayed; requests
  without a re-readable body are never replayed; a cancelled context returns
  `ctx.Err()` at once.
- Automatic Idempotency-Key (UUIDv4) on checks_create, jobs_create and
  projects_create when the caller supplies none, fixed before the first
  attempt so retries reuse it.
- `WithAPIKey` trims whitespace (blank falls back to `IONHOUR_API_KEY`);
  `ErrInvalidAPIKey` for keys with spaces/control bytes; `ErrInvalidBaseURL`
  for base URLs with userinfo, query or fragment.
- Default User-Agent `ionhour-api-go/<version>`.
- `*APIError` parsed from the v1 error envelope (`Status`, `Code`, `Message`,
  `Messages`, `Detail`, `RetryAfter`) with `IsRateLimited`, `IsNotFound` and
  `IsConflict`. A non-envelope body (for example an HTML 502)
  yields Code `HTTP_<status>` and Message `HTTP <status>`, matching `@ionhour/api`.
- `Whoami` returning the generated `WhoamiV1Response` (workspace, permission
  level, projected role, principal kind).
- `IsUnauthorized` and `IsForbidden` predicates.
- `ListAll` generic pagination walker plus `ListAllChecks`, `ListAllJobs` and
  `ListAllProjects` (100 per page by default, bounded at 10,000 pages).
- `CheckResponse`, `WithIdempotencyKey`, `IdempotencyKeyEditor`
  (`ErrIdempotencyKeyNotSupported` outside the allowlist), `NewIdempotencyKey`.
- The API key never appears in fmt output of a `Client` or in error text.

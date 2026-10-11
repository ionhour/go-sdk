# Ionhour Integration API client for Go

`github.com/ionhour/go-sdk/api` is the Go client for the Ionhour Integration
API v1: projects, HTTP checks, heartbeat jobs, regions, deployments, incidents,
maintenance windows and status-page announcements. It is the client the
Terraform provider is built on.

It is a **separate module** from the heartbeat SDK (`github.com/ionhour/go-sdk`),
which stays dependency-free. This module depends on the small oapi-codegen
runtime (`github.com/oapi-codegen/runtime`, `github.com/oapi-codegen/nullable`).

```bash
go get github.com/ionhour/go-sdk/api
```

Requires Go 1.21+.

## Quick start

```go
import (
	"context"
	"log"

	"github.com/oapi-codegen/nullable"

	"github.com/ionhour/go-sdk/api"
)

func main() {
	ctx := context.Background()

	// Falls back to IONHOUR_API_KEY and IONHOUR_API_BASE_URL.
	c, err := api.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	me, err := c.Whoami(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("workspace %d (%s), %s key", me.WorkspaceId, me.WorkspaceName, me.PrincipalKind)

	// Create a check. The client attaches an Idempotency-Key automatically,
	// so its own retries on 5xx/network errors never create a duplicate.
	rsp, err := c.ChecksCreateWithResponse(ctx, nil,
		api.CreateCheckV1Dto{
			Name:      "Public API",
			ProjectId: 7,
			Slug:      nullable.NewNullableWithValue("public-api"),
			Config:    api.V1HttpCheckConfigDto{Url: "https://api.example.com/health"},
		})
	if err != nil {
		log.Fatal(err)
	}
	if err := c.CheckResponse(rsp.HTTPResponse, rsp.Body); err != nil {
		log.Fatal(err) // *api.APIError
	}
	log.Printf("created check %d", rsp.JSON201.Id)

	// Any {id} path parameter also accepts "slug:<slug>".
	got, err := c.ChecksGetOneWithResponse(ctx, "slug:public-api")
	if err != nil {
		log.Fatal(err)
	}
	if err := c.CheckResponse(got.HTTPResponse, got.Body); api.IsNotFound(err) {
		log.Print("gone")
	}

	// Every page of a list, 100 items per request.
	projects, err := c.ListAllProjects(ctx, api.ProjectsListParams{})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d projects", len(projects))
}
```

Every operation in the OpenAPI document is a method named after its
operationId: `checks_create` becomes `ChecksCreateWithResponse`, which returns
the decoded body in `JSON201`. The plain `ChecksCreate` variant returns the raw
`*http.Response`.

`ListAllChecks`, `ListAllJobs` and `ListAllProjects` walk every page and
return all items in order, stopping at the first error. The generic
`api.ListAll` does the same for any list endpoint, given a function that
fetches one page.

Always construct the client with `api.NewClient`. The zero value is not
usable. One client is safe for concurrent use.

To clear a nullable field in a PATCH, send an explicit null:
`api.UpdateProjectV1Dto{Description: nullable.NewNullNullable[string]()}`.
Leaving the field unset omits it.

## Configuration

| Option | Default | Notes |
|---|---|---|
| `WithAPIKey` | `IONHOUR_API_KEY` | API key (`ionh_...`) or OAuth token, sent as `Authorization: Bearer`. Whitespace is trimmed and a blank value counts as unset. `NewClient` returns `ErrMissingAPIKey` when neither is set and `ErrInvalidAPIKey` when the key contains a space, a control character or DEL. |
| `WithBaseURL` | `IONHOUR_API_BASE_URL`, then `https://api.ionhour.com/api` | Must be an absolute http(s) URL without userinfo, query or fragment, else `ErrInvalidBaseURL`. This is the API host, not the heartbeat host behind `IONHOUR_BASE_URL`. |
| `WithHTTPClient` | a new `*http.Client` | Copied, never mutated. Its Transport is wrapped with the retry policy. |
| `WithUserAgent` | `ionhour-api-go/0.1.0` | Empty values are ignored. |
| `WithTimeout` | 30s | `time.Duration`, applied per attempt. Non-positive disables it. |
| `WithMaxAttempts` | 3 | Total attempts. Values below 1 mean 1. |
| `WithMaxRetryWait` | 60s | Longest 429 `Retry-After` the client will sleep. A longer one is returned as an `*APIError` with `RetryAfter` set. |

An explicit option beats its environment variable, which beats the default.

## Retries

- Other 4xx responses are never retried.
- A 429 is retried for every method after its `Retry-After`, or after 1s when the header is absent. A `Retry-After` above 60 seconds is returned at once.
- 5xx responses and network errors are retried after 1s, then 2s. GET, PATCH and DELETE always qualify.
- Among POSTs, only `checks_create`, `jobs_create` and `projects_create` are retried on 5xx or network errors. They are the only routes that honour `Idempotency-Key`. Other POSTs, such as pause, resume and `incidents_create`, could act twice and are never replayed.
- A request whose body cannot be re-read, such as a plain `io.Reader` passed to a `...WithBody` method, is never retried.
- Cancelling the context stops the call at once and returns `ctx.Err()`.

## Idempotency keys

The three creates always carry an `Idempotency-Key`. The client mints a UUIDv4
per call before the first attempt, so its own retries reuse it. To reuse one
key across your own retries of a logical create, mint it with
`api.NewIdempotencyKey` and pass it with `api.WithIdempotencyKey(ctx, key)`,
`Params.IdempotencyKey` or `api.IdempotencyKeyEditor(key)`. A context key is
ignored by every other operation. `IdempotencyKeyEditor` on any other
operation fails with `ErrIdempotencyKeyNotSupported` before sending.

## Errors

A non-2xx response becomes an `*api.APIError` via `Client.CheckResponse`.
It carries `Status`, `Code`, `Message`, `Messages` and `Detail`. `Code` holds
values such as `NOT_FOUND`, `SLUG_TAKEN` and `PLAN_LIMIT_EXCEEDED`, and
`Messages` holds the list of validation failures. `RetryAfter` is set on a
429. `Detail` is the envelope's `detail` when it is a JSON object, otherwise
its extra top-level fields. A 429 carries `scope`, `limit` and `plan` at the
top level, so they land in `Detail`. The generated `JSON429` field holds the
same body as a typed `V1RateLimitResponse`.

A body counts as the v1 envelope only when `code` is a non-empty string and
`message` is a string or a string list. Anything else gets `Code` `HTTP_502`
and `Message` `HTTP 502`, using the actual status. That covers an HTML page
from a proxy, an empty body or an envelope without `code`. The Node client
uses the same rules.
`api.IsUnauthorized`, `api.IsForbidden`, `api.IsNotFound`, `api.IsConflict`
and `api.IsRateLimited` see through wrapping.

The API key never appears in `fmt` output of a `Client`, at any verb, or in
error text. It is replaced with a short prefix such as `ionh_liv…`.

## Regenerating

`client.gen.go` and `types.gen.go` are generated. Never edit them by hand. From the repository root:

```bash
yarn openapi:go-client          # regenerate from apps/api-gateway/src/app/openapi/ionhour-v1.json
yarn openapi:go-client:check    # exit 1 if the committed output drifted
```

The script uses a local Go toolchain when one is installed and the
`golang:1.22` Docker image otherwise. The two files under `templates/`
override upstream oapi-codegen templates. Their only change is to rename the
generated constructor and options to `NewRawClient`, `WithRawHTTPClient` and
related `Raw*` names, so the facade can own `NewClient`. After an oapi-codegen
bump, re-derive them with `scripts/openapi/regen-go-client.sh --refresh-templates`.

## Development

Run format, vet and race tests from the repository root with Docker. The
repository is mounted whole so the tests can cross-check requests against the
OpenAPI document. That check skips itself outside the monorepo.

```bash
docker run --rm -v "$PWD":/src -w /src/sdks/go/api golang:1.22 sh -c "gofmt -l . && go vet ./... && go test -race ./..."
```

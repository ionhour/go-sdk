package ionhour

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"
)

// Job is a registered heartbeat monitor. Register one with [Client.RegisterJob]
// (or the package-level [RegisterJob]) for a token job, or with
// [Client.RegisterJobBySlug] for a slug-mode job, and report on it with
// [Job.Success], [Job.Fail], or [Job.Run]. A Job is safe for concurrent use.
type Job struct {
	token  string // token jobs only
	slug   string // slug jobs only
	create bool   // slug jobs only: send ?create=1
	client *Client
}

// RegisterJob registers a job token against this client.
//
// The token is validated locally (it must be non-empty and contain no
// whitespace) and a malformed token fails fast with an error — no network call
// is made.
func (c *Client) RegisterJob(jobToken string) (*Job, error) {
	if err := validateToken(jobToken); err != nil {
		return nil, err
	}
	return &Job{token: jobToken, client: c}, nil
}

var (
	defaultClientOnce sync.Once
	defaultClient     *Client
)

// RegisterJob is the package-level convenience for the simplest install. It
// registers jobToken against a lazily-created default client (which reads
// IONHOUR_BASE_URL from the environment). For anything beyond the defaults,
// build a client with [NewClient] and use [Client.RegisterJob].
//
// Environment note: the default client is created once, on first use, and the
// IONHOUR_BASE_URL value is captured at that moment and frozen for the lifetime
// of the process. Changing it later has no effect on the default client. Build
// an explicit [NewClient] if you need the environment re-read.
func RegisterJob(jobToken string) (*Job, error) {
	return sharedDefaultClient().RegisterJob(jobToken)
}

// sharedDefaultClient lazily builds the process-wide default client, capturing
// the environment on first use (see the note on [RegisterJob]).
func sharedDefaultClient() *Client {
	defaultClientOnce.Do(func() {
		defaultClient = NewClient()
	})
	return defaultClient
}

// resetDefaultClientForTest clears the process-wide default client so the next
// package-level RegisterJob rebuilds it and re-reads the environment. It exists
// only so package-level tests can control the singleton's frozen environment and
// not poison one another; it is not part of the public API and must be called
// serially (no concurrent RegisterJob).
func resetDefaultClientForTest() {
	defaultClientOnce = sync.Once{}
	defaultClient = nil
}

// Token returns the job's ping token, or "" for a slug-mode job.
func (j *Job) Token() string { return j.token }

// Slug returns the job's slug, or "" for a token job.
func (j *Job) Slug() string { return j.slug }

// isSlug reports whether the job pings the slug routes on the API host.
func (j *Job) isSlug() bool { return j.slug != "" }

// secret is the credential to scrub from error text: the token for token jobs,
// the client's API key for slug jobs.
func (j *Job) secret() string {
	if j.isSlug() {
		return j.client.apiKey
	}
	return j.token
}

// String implements fmt.Stringer, redacting the token so that logging a Job
// with %v or %+v cannot leak it. A slug is not a credential and is shown; the
// API key never is.
func (j *Job) String() string {
	if j.isSlug() {
		return fmt.Sprintf("ionhour.Job(slug=%s, create=%t)", j.slug, j.create)
	}
	return "ionhour.Job(token=<redacted>)"
}

// GoString implements fmt.GoStringer so %#v prints the same redacted view
// instead of the unexported fields.
func (j *Job) GoString() string { return j.String() }

// Success reports a successful heartbeat. It never panics.
//
// The returned error is non-nil only for a usage (programmer) error — an
// oversized payload (> 10 KiB encoded), a non-serializable payload, or a
// misconfigured base URL — in which case no request is sent. Transport outcomes
// (network errors, 429/5xx after retries, cancellation) are reported in the
// [PingResult] with a nil error, so a failure to reach Ionhour can never break
// the host application. [Job.Ping] is an alias.
func (j *Job) Success(ctx context.Context, opts ...PingOption) (PingResult, error) {
	return j.client.doPing(ctx, j, pingSuccess, buildPingRequest(opts))
}

// Ping is an alias for [Job.Success].
func (j *Job) Ping(ctx context.Context, opts ...PingOption) (PingResult, error) {
	return j.Success(ctx, opts...)
}

// Fail reports an explicit failure (the /fail variant), taking the monitor DOWN
// immediately rather than waiting for the schedule and grace period to lapse. It
// never panics. Its return contract matches [Job.Success]: the error is non-nil
// only for a usage error; transport outcomes live in the [PingResult].
func (j *Job) Fail(ctx context.Context, opts ...PingOption) (PingResult, error) {
	return j.client.doPing(ctx, j, pingFail, buildPingRequest(opts))
}

// Start reports that a run has begun (the /start variant). It is persist-only
// telemetry: it never changes monitor status, opens or resolves incidents, or
// resets the schedule — a job that starts and then hangs still goes LATE. Pair
// it with the terminal [Job.Success]/[Job.Fail] via a shared [WithRID] so the
// run's duration correlates. It never panics; its return contract matches
// [Job.Success] (the error is non-nil only for a usage error).
//
// [Job.Run] and [RunValue] send this automatically before the wrapped function.
func (j *Job) Start(ctx context.Context, opts ...PingOption) (PingResult, error) {
	return j.client.doPing(ctx, j, pingStart, buildPingRequest(opts))
}

// validateToken enforces the local usage rules: non-empty, no whitespace.
func validateToken(token string) error {
	if token == "" {
		return ErrEmptyToken
	}
	if strings.ContainsFunc(token, unicode.IsSpace) {
		return &InvalidTokenError{Reason: "must not contain whitespace"}
	}
	return nil
}

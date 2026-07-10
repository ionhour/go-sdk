package ionhour

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Job is a registered heartbeat monitor. Register one with [Client.RegisterJob]
// (or the package-level [RegisterJob]) and report on it with [Job.Success],
// [Job.Fail], or [Job.Run]. A Job is safe for concurrent use.
type Job struct {
	token  string
	apiKey string // per-job override; empty means fall back to the client key
	client *Client
}

// RegisterJob registers a job token against this client.
//
// The token is validated locally (it must be non-empty and contain no
// whitespace) and a malformed token fails fast with an error — no network call
// is made. Supply [WithJobAPIKey] to override the client's global API key for
// this job's pings.
func (c *Client) RegisterJob(jobToken string, opts ...JobOption) (*Job, error) {
	if err := validateToken(jobToken); err != nil {
		return nil, err
	}
	j := &Job{token: jobToken, client: c}
	for _, opt := range opts {
		if opt != nil {
			opt(j)
		}
	}
	return j, nil
}

var (
	defaultClientOnce sync.Once
	defaultClient     *Client
)

// RegisterJob is the package-level, two-parameter convenience for the simplest
// install. It registers jobToken against a lazily-created default client (which
// reads IONHOUR_API_KEY / IONHOUR_BASE_URL from the environment).
//
// An empty apiKey means the job is unprotected and adds no per-job key: the
// default client's key (from IONHOUR_API_KEY, if any) still applies. A non-empty
// apiKey is used as a per-job override. For anything beyond the defaults, build
// a client with [NewClient] and use [Client.RegisterJob].
//
// Environment note: the default client is created once, on first use, and the
// IONHOUR_API_KEY / IONHOUR_BASE_URL values are captured at that moment and
// frozen for the lifetime of the process. Changing those variables later has no
// effect on the default client. Build an explicit [NewClient] if you need the
// environment re-read.
func RegisterJob(jobToken, apiKey string) (*Job, error) {
	var opts []JobOption
	// A whitespace-only key is treated as absent (no per-job override); a padded
	// key is trimmed by WithJobAPIKey so it can't corrupt the Bearer header.
	if strings.TrimSpace(apiKey) != "" {
		opts = append(opts, WithJobAPIKey(apiKey))
	}
	return sharedDefaultClient().RegisterJob(jobToken, opts...)
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

// Token returns the job's ping token.
func (j *Job) Token() string { return j.token }

// String implements fmt.Stringer, redacting both the token and the API key so
// that logging a Job with %v or %+v cannot leak the credential. The token is a
// credential in its own right (it is what authorizes a ping), so it is redacted
// too, not just the API key.
//
// Note: %#v (Go's verbose GoString syntax) bypasses fmt.Stringer and will print
// the unexported fields — avoid %#v on a Job.
func (j *Job) String() string {
	return "ionhour.Job(token=<redacted>, apiKey=<redacted>)"
}

// Success reports a successful heartbeat. It never panics.
//
// The returned error is non-nil only for a usage (programmer) error — an
// oversized payload (> 10 KiB encoded), a non-serializable payload, or a
// misconfigured base URL — in which case no request is sent. Transport outcomes
// (network errors, 429/5xx after retries, cancellation) are reported in the
// [PingResult] with a nil error, so a failure to reach Ionhour can never break
// the host application. [Job.Ping] is an alias.
func (j *Job) Success(ctx context.Context, opts ...PingOption) (PingResult, error) {
	return j.client.doPing(ctx, j, false, buildPingRequest(opts))
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
	return j.client.doPing(ctx, j, true, buildPingRequest(opts))
}

// Run wraps a unit of work with heartbeat reporting. It times fn with a
// monotonic clock and:
//
//   - on success, pings Success with the measured duration and returns nil;
//   - on error, pings Fail with the measured duration and a payload that merges
//     {"error": err.Error()} into any caller-supplied payload, then returns fn's
//     error unchanged.
//
// fn's error is always propagated so the caller sees their own failure. Errors
// from Run's own heartbeat pings — both transport failures and usage errors such
// as an oversized merged payload — are logged (when a logger is configured) but
// never returned or allowed to mask fn's result. A nil fn is a usage error and
// is returned as such.
func (j *Job) Run(ctx context.Context, fn func(ctx context.Context) error, opts ...PingOption) error {
	if fn == nil {
		return errors.New("ionhour: Run requires a non-nil function")
	}

	start := time.Now()
	runErr := fn(ctx)
	elapsed := time.Since(start)

	if runErr == nil {
		successOpts := append([]PingOption{WithDuration(elapsed)}, opts...)
		// Ping errors (transport or usage) are logged by doPing and swallowed
		// here — they must never mask fn's successful result.
		_, _ = j.Success(ctx, successOpts...)
		return nil
	}

	failOpts := append([]PingOption{WithDuration(elapsed)}, opts...)
	failOpts = append(failOpts, withMergedErrorPayload(runErr, opts))
	_, _ = j.Fail(ctx, failOpts...)
	return runErr
}

// effectiveAPIKey returns the per-job key when set, else the client's key.
func (j *Job) effectiveAPIKey() string {
	if j.apiKey != "" {
		return j.apiKey
	}
	return j.client.apiKey
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

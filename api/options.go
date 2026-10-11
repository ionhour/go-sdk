package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const (
	// Version is this module's release version, sent in the default User-Agent.
	Version = "0.1.0"

	// DefaultBaseURL is the Integration API base URL. The generated operation
	// paths (`/v1/...`) are appended to it.
	DefaultBaseURL = "https://api.ionhour.com/api"

	// DefaultTimeout bounds every HTTP attempt (not the whole retried call).
	DefaultTimeout = 30 * time.Second

	// DefaultMaxAttempts is the total number of attempts per request.
	DefaultMaxAttempts = 3

	// DefaultMaxRetryWait caps how long the client will sleep on a 429
	// Retry-After before giving up and returning the 429 to the caller.
	DefaultMaxRetryWait = 60 * time.Second

	// EnvAPIKey is read when no [WithAPIKey] option is given.
	EnvAPIKey = "IONHOUR_API_KEY"

	// EnvBaseURL is read when no [WithBaseURL] option is given. It is distinct
	// from the heartbeat SDK's IONHOUR_BASE_URL, which points at the signal
	// host rather than the API host.
	EnvBaseURL = "IONHOUR_API_BASE_URL"
)

// defaultUserAgent is "ionhour-api-go/<version>", the contract form (Node sends
// "ionhour-api-node/<version>").
const defaultUserAgent = "ionhour-api-go/" + Version

// Option configures a [Client] in [NewClient].
type Option func(*config)

// config is the resolved option set. It is never exposed, so the API key it
// carries cannot be printed by callers.
type config struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	userAgent    string
	timeout      time.Duration
	maxAttempts  int
	maxRetryWait time.Duration
	sleep        sleepFunc
	backoff      backoffFunc
}

// sleepFunc waits d or returns ctx.Err() if ctx ends first. Overridable in
// tests so retry specs run instantly.
type sleepFunc func(ctx context.Context, d time.Duration) error

// backoffFunc returns the wait before retry number n (1-based) after a 5xx or
// network failure.
type backoffFunc func(n int) time.Duration

// WithAPIKey sets the API key (ionh_...) or OAuth access token sent as
// `Authorization: Bearer <key>`. Surrounding whitespace is trimmed and a blank
// value is treated as absent, so the IONHOUR_API_KEY fallback still applies. A
// key containing a space, a control character or DEL makes [NewClient] fail
// with [ErrInvalidAPIKey].
func WithAPIKey(key string) Option {
	return func(c *config) {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			c.apiKey = trimmed
		}
	}
}

// WithBaseURL overrides the API base URL (scheme + host + `/api` prefix, e.g.
// https://api.ionhour.com/api). An empty value is ignored. Precedence: this
// option, then IONHOUR_API_BASE_URL, then [DefaultBaseURL].
func WithBaseURL(baseURL string) Option {
	return func(c *config) {
		if baseURL != "" {
			c.baseURL = baseURL
		}
	}
}

// WithHTTPClient supplies a custom *http.Client (proxies, TLS, pooling). A nil
// client is ignored. The client is copied, never mutated: the copy's Transport
// is wrapped with the retry policy. Any Timeout on the supplied client bounds
// the whole call including retries; [WithTimeout] bounds each attempt.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *config) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithUserAgent overrides the User-Agent header. An empty value is ignored and
// the default ("ionhour-api-go/<version>") is kept.
func WithUserAgent(userAgent string) Option {
	return func(c *config) {
		if userAgent != "" {
			c.userAgent = userAgent
		}
	}
}

// WithTimeout sets the per-attempt timeout. A non-positive value disables the
// SDK-managed deadline and defers to the caller's context.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) { c.timeout = timeout }
}

// WithMaxAttempts sets the total number of attempts per request (not the
// number of extra retries). Values below 1 are clamped to 1 (no retries).
func WithMaxAttempts(attempts int) Option {
	return func(c *config) { c.maxAttempts = attempts }
}

// WithMaxRetryWait caps how long the client sleeps on a 429 Retry-After. A
// 429 whose Retry-After exceeds the cap is returned to the caller at once (as
// an [*APIError] with RetryAfter set) instead of blocking. Non-positive values
// are ignored.
func WithMaxRetryWait(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxRetryWait = d
		}
	}
}

// withSleep is a test seam (unexported on purpose).
func withSleep(fn sleepFunc) Option { return func(c *config) { c.sleep = fn } }

func defaultConfig() config {
	return config{
		userAgent:    defaultUserAgent,
		timeout:      DefaultTimeout,
		maxAttempts:  DefaultMaxAttempts,
		maxRetryWait: DefaultMaxRetryWait,
		sleep:        sleepContext,
		backoff:      contractBackoff,
	}
}

// contractBackoff implements the SDK contract: 1s before the first retry, then
// 2s for every later retry (clamped, never growing).
func contractBackoff(n int) time.Duration {
	if n <= 1 {
		return time.Second
	}
	return 2 * time.Second
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

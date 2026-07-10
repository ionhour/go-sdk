package ionhour

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ClientOption configures a [Client] in [NewClient].
type ClientOption func(*Client)

// WithAPIKey sets the global workspace API key (ionh_...). It is sent as
// "Authorization: Bearer <key>" on every ping unless a per-job key overrides it
// (see [WithJobAPIKey]). When omitted, NewClient falls back to IONHOUR_API_KEY.
//
// The value is trimmed of surrounding whitespace (a stray newline from a .env
// file must not produce a malformed header), and a whitespace-only value is
// treated as absent so the environment fallback can still apply.
func WithAPIKey(apiKey string) ClientOption {
	return func(c *Client) { c.apiKey = strings.TrimSpace(apiKey) }
}

// WithBaseURL overrides the ingestion base URL. When omitted, NewClient falls
// back to IONHOUR_BASE_URL and then to [DefaultBaseURL].
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithHTTPClient supplies a custom *http.Client (for custom transports,
// proxies, or connection pooling). A nil client is ignored. Note that the
// per-attempt timeout from [WithTimeout] is applied via context regardless of
// any Timeout set on the supplied client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithTimeout sets the per-attempt timeout, applied via a context deadline on
// each HTTP attempt. A non-positive duration disables the SDK-managed deadline
// and defers entirely to the caller's context. The default is [DefaultTimeout].
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) { c.timeout = timeout }
}

// WithRetries sets the total number of attempts per ping (not the number of
// additional retries). Values below 1 are clamped to 1. The default is
// [DefaultRetries] (3). Retries happen only on network errors, HTTP 429, and
// HTTP 5xx responses; other 4xx responses are never retried.
func WithRetries(attempts int) ClientOption {
	return func(c *Client) { c.retries = attempts }
}

// WithUserAgent overrides the User-Agent header sent on every ping. An empty
// value is ignored and the default ("ionhour-go/<version>") is kept.
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		if userAgent != "" {
			c.userAgent = userAgent
		}
	}
}

// WithLogger attaches an *slog.Logger. When set, ping failures (transport
// errors, non-2xx responses, and usage errors such as an oversized payload) are
// logged at error level. When nil (the default), failures are silent and
// surface only through [PingResult].
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = logger }
}

// JobOption configures a [Job] in [Client.RegisterJob].
type JobOption func(*Job)

// WithJobAPIKey sets a per-job workspace API key that overrides the client's
// global key for this job's pings. Use it when a single client registers a mix
// of protected and unprotected jobs.
//
// Like [WithAPIKey], the value is whitespace-trimmed and a whitespace-only value
// is treated as absent (the job falls back to the client's key).
func WithJobAPIKey(apiKey string) JobOption {
	return func(j *Job) { j.apiKey = strings.TrimSpace(apiKey) }
}

// PingOption configures a single ping ([Job.Success], [Job.Fail], [Job.Ping]) or
// the ping emitted by [Job.Run].
type PingOption func(*pingRequest)

// WithRID attaches a run-correlation id, sent as the "rid" query parameter.
func WithRID(rid string) PingOption {
	return func(pr *pingRequest) { pr.rid = rid }
}

// WithDuration attaches an execution time, sent as the "duration" query
// parameter in whole milliseconds. Negative durations are clamped to 0.
func WithDuration(d time.Duration) PingOption {
	return func(pr *pingRequest) { pr.duration = &d }
}

// WithPayload attaches a JSON-serializable payload, which switches the ping from
// a GET to a POST with a JSON body. The encoded body must not exceed 10 KiB.
func WithPayload(payload any) PingOption {
	return func(pr *pingRequest) {
		pr.payload = payload
		pr.hasPayload = true
	}
}

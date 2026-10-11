package ionhour

import (
	"log/slog"
	"net/http"
	"time"
)

// ClientOption configures a [Client] in [NewClient].
type ClientOption func(*Client)

// WithBaseURL overrides the ingestion base URL. When omitted, NewClient falls
// back to IONHOUR_BASE_URL and then to [DefaultBaseURL].
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithAPIBaseURL overrides the API host used by slug-mode jobs
// ([Client.RegisterJobBySlug]). When omitted, NewClient falls back to
// IONHOUR_API_BASE_URL and then to [DefaultAPIBaseURL]. Token pings keep using
// the base URL from [WithBaseURL].
func WithAPIBaseURL(apiBaseURL string) ClientOption {
	return func(c *Client) { c.apiBaseURL = apiBaseURL }
}

// WithAPIKey sets the ionh_ API key used ONLY by slug-mode jobs, sent as
// "Authorization: Bearer <key>". Token pings never send it. The key is trimmed;
// a blank value counts as absent and NewClient falls back to IONHOUR_API_KEY.
func WithAPIKey(apiKey string) ClientOption {
	return func(c *Client) { c.apiKey = apiKey }
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

// PingOption configures a single ping ([Job.Success], [Job.Fail], [Job.Ping],
// [Job.Start]) or the pings emitted by [Job.Run] / [RunValue].
type PingOption func(*pingRequest)

// WithRID attaches a run-correlation id, sent as the "rid" query parameter. It
// pairs a [Job.Start] ping with its terminal [Job.Success]/[Job.Fail]; see
// [NewRID] to mint one.
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

// WithHost attaches a host label, sent as the "host" query parameter. A value
// longer than 253 characters is silently truncated (telemetry must never fail a
// heartbeat). The host is never auto-detected — it is sent only when set here.
func WithHost(host string) PingOption {
	return func(pr *pingRequest) {
		truncated := truncateRunes(host, maxHostLen)
		pr.host = &truncated
	}
}

// WithEnv attaches an environment label (for example "production"), sent as the
// "env" query parameter. A value longer than 32 characters is silently
// truncated. The environment is never auto-detected — it is sent only when set.
func WithEnv(env string) PingOption {
	return func(pr *pingRequest) {
		truncated := truncateRunes(env, maxEnvLen)
		pr.env = &truncated
	}
}

// WithExitCode attaches a process exit code, sent as the "exit_code" query
// parameter. Valid codes are 0–255 (a single byte, per POSIX); a value outside
// that range is a usage error, returned as the ping's second return value with
// no request sent (see [ExitCodeRangeError]).
func WithExitCode(code int) PingOption {
	return func(pr *pingRequest) {
		if code < 0 || code > 255 {
			// Record the first usage error; execute surfaces it before any request.
			if pr.optErr == nil {
				pr.optErr = &ExitCodeRangeError{Code: code}
			}
			return
		}
		pr.exitCode = &code
	}
}

// WithoutStartPing disables the automatic start ping that [Job.Run] and
// [RunValue] send before invoking the wrapped function. It has no effect on a
// direct [Job.Success], [Job.Fail], [Job.Start], or [Job.Ping] call.
func WithoutStartPing() PingOption {
	return func(pr *pingRequest) { pr.noStartPing = true }
}

// truncateRunes returns s limited to at most max runes.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

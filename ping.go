package ionhour

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pingRequest is the accumulated, per-call state built from PingOptions.
type pingRequest struct {
	rid         string
	duration    *time.Duration
	payload     any
	hasPayload  bool
	host        *string
	env         *string
	exitCode    *int
	noStartPing bool  // Run/RunValue only; ignored on a direct ping.
	optErr      error // first usage error from an option (e.g. exit code out of range).
}

func buildPingRequest(opts []PingOption) *pingRequest {
	pr := &pingRequest{}
	for _, opt := range opts {
		if opt != nil {
			opt(pr)
		}
	}
	return pr
}

// pingKind selects the ping endpoint variant and labels the failure log line.
type pingKind int

const (
	pingSuccess pingKind = iota // {base}/signals/ping/{token}
	pingFail                    // {base}/signals/ping/{token}/fail
	pingStart                   // {base}/signals/ping/{token}/start
)

// pathSuffix is the URL segment appended after the token for this kind.
func (k pingKind) pathSuffix() string {
	switch k {
	case pingFail:
		return "/fail"
	case pingStart:
		return "/start"
	default:
		return ""
	}
}

// action labels the ping in the failure log line.
func (k pingKind) action() string {
	switch k {
	case pingFail:
		return "fail"
	case pingStart:
		return "start"
	default:
		return "success"
	}
}

// doPing runs a ping and logs the failure (if any) exactly once. It is the sole
// entry point shared by Success, Fail, and Run.
//
// The returned error is the usage-error channel: it is non-nil only for
// programmer errors (an oversized or non-serializable payload, or a
// misconfigured base URL) and is always nil for transport outcomes. Transport
// failures (network errors, 429/5xx exhaustion, cancellation) stay inside
// PingResult.Err with a nil error return, preserving the never-crash philosophy.
func (c *Client) doPing(ctx context.Context, job *Job, kind pingKind, pr *pingRequest) (PingResult, error) {
	result, usageErr := c.execute(ctx, job, kind, pr)
	if !result.OK {
		c.logError(job, kind, result.Err)
	}
	return result, usageErr
}

// execute encodes the payload (validating size), builds the URL, and runs the
// retry loop. It never logs — doPing owns logging so every failure path,
// including client-side usage errors, is logged uniformly. The second return
// value is the usage error (nil for transport outcomes).
func (c *Client) execute(ctx context.Context, job *Job, kind pingKind, pr *pingRequest) (PingResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// An option-level usage error (e.g. an out-of-range exit code) is loud and
	// pre-empts any request, mirroring the oversized-payload path below.
	if pr.optErr != nil {
		return PingResult{Err: pr.optErr}, pr.optErr
	}

	method := http.MethodGet
	var body []byte
	if pr.hasPayload {
		encoded, err := json.Marshal(pr.payload)
		if err != nil {
			usageErr := fmt.Errorf("ionhour: payload is not JSON-serializable: %w", err)
			return PingResult{Err: usageErr}, usageErr
		}
		if len(encoded) > maxPayloadBytes {
			usageErr := &PayloadTooLargeError{Size: len(encoded), Max: maxPayloadBytes}
			return PingResult{Err: usageErr}, usageErr
		}
		body = encoded
		method = http.MethodPost
	}

	endpoint, err := c.buildURL(job, kind, pr)
	if err != nil {
		// A missing/unparsable base URL is a client misconfiguration — loud.
		return PingResult{Err: err}, err
	}

	attempts := c.retries
	if attempts < 1 {
		attempts = 1
	}

	var result PingResult
	for attempt := 1; attempt <= attempts; attempt++ {
		// Honor cancellation before spending an attempt.
		if err := ctx.Err(); err != nil {
			return PingResult{Err: fmt.Errorf("ionhour: ping aborted: %w", err)}, nil
		}

		result = c.attempt(ctx, method, endpoint, body, job)
		if result.OK || !isRetryable(result) {
			return result, nil
		}

		// Back off before the next attempt, but never sleep past cancellation.
		if attempt < attempts {
			if !sleepCtx(ctx, c.backoffFn(attempt)) {
				return PingResult{Err: fmt.Errorf("ionhour: ping aborted during backoff: %w", ctx.Err())}, nil
			}
		}
	}
	return result, nil
}

// attempt performs a single HTTP request bounded by the per-attempt timeout.
func (c *Client) attempt(ctx context.Context, method, endpoint string, body []byte, job *Job) PingResult {
	attemptCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(attemptCtx, method, endpoint, reader)
	if err != nil {
		// A parse failure here can echo the endpoint (and thus the token) back in
		// its message, so scrub before it can reach a logger or PingResult.Err.
		return scrubbedTransportError("ionhour: failed to build ping request", err, job)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.userAgent)
	// Only slug pings carry a credential; token pings never send Authorization.
	if job.isSlug() {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network error, per-attempt timeout, or cancellation: Status stays 0,
		// which isRetryable treats as a transport failure. net/http's *url.Error
		// embeds the request URL — and thus the job token — in its message, so it
		// is scrubbed before it can reach a logger or PingResult.Err. The original
		// error stays wrapped, keeping errors.Is/As chains (e.g. against
		// context.Canceled) intact.
		return scrubbedTransportError("ionhour: ping request failed", err, job)
	}
	defer resp.Body.Close()
	var parsed slugResponseBody
	if job.isSlug() {
		parsed = readSlugBody(resp.Body)
	}
	// Drain the body so the connection can be reused by keep-alive.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		created := job.isSlug() && (resp.StatusCode == http.StatusCreated || parsed.Created)
		return PingResult{OK: true, Status: resp.StatusCode, Created: created}
	}
	return PingResult{Status: resp.StatusCode, Err: &HTTPStatusError{Status: resp.StatusCode, Code: parsed.code()}}
}

// maxSlugBodyBytes bounds how much of a slug-route response is parsed.
const maxSlugBodyBytes = 4 * 1024

// errorCodePattern accepts only well-formed server error codes, so a hostile
// or unexpected body can never inject arbitrary text into an error message.
var errorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// slugResponseBody is the subset of a slug-route response the SDK reads: the
// success body's created flag and the error envelope's code.
type slugResponseBody struct {
	Created bool   `json:"created"`
	Code    string `json:"code"`
}

// code returns the envelope code when it is well-formed, else "".
func (b slugResponseBody) code() string {
	if errorCodePattern.MatchString(b.Code) {
		return b.Code
	}
	return ""
}

// readSlugBody best-effort decodes a bounded slug-route body. It never fails.
func readSlugBody(r io.Reader) slugResponseBody {
	var body slugResponseBody
	_ = json.NewDecoder(io.LimitReader(r, maxSlugBodyBytes)).Decode(&body)
	return body
}

// isRetryable reports whether a failed result should be retried: transport
// errors (Status 0), HTTP 429, and HTTP 5xx. All other 4xx are permanent.
func isRetryable(r PingResult) bool {
	if r.OK {
		return false
	}
	if r.Status == 0 {
		return true
	}
	return r.Status == http.StatusTooManyRequests || r.Status >= 500
}

// sleepCtx waits for d or until ctx is done. It reports true if the full delay
// elapsed and false if ctx was cancelled first (or was already cancelled).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// buildURL assembles the ping endpoint and query string: token jobs ping
// {baseURL}/signals/ping/{token}, slug jobs ping {apiBaseURL}/signals/s/{slug}.
func (c *Client) buildURL(job *Job, kind pingKind, pr *pingRequest) (string, error) {
	baseURL, prefix, id := c.baseURL, "/signals/ping/", job.token
	if job.isSlug() {
		baseURL, prefix, id = c.apiBaseURL, "/signals/s/", job.slug
	}
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return "", &ConfigError{Reason: "base URL is empty"}
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString(prefix)
	b.WriteString(url.PathEscape(id))
	b.WriteString(kind.pathSuffix())

	query := url.Values{}
	if job.create {
		query.Set("create", "1")
	}
	if pr.rid != "" {
		query.Set("rid", pr.rid)
	}
	if pr.duration != nil {
		query.Set("duration", strconv.FormatInt(durationMillis(*pr.duration), 10))
	}
	if pr.host != nil {
		query.Set("host", *pr.host)
	}
	if pr.env != nil {
		query.Set("env", *pr.env)
	}
	if pr.exitCode != nil {
		query.Set("exit_code", strconv.Itoa(*pr.exitCode))
	}
	if encoded := query.Encode(); encoded != "" {
		b.WriteString("?")
		b.WriteString(encoded)
	}

	full := b.String()
	if _, err := url.ParseRequestURI(full); err != nil {
		// The parse error echoes the full URL, which carries the token — scrub it.
		return "", &ConfigError{Reason: "invalid base URL: " + scrubSecrets(err.Error(), job.secret())}
	}
	return full, nil
}

// durationMillis renders a duration in whole milliseconds, clamping negatives.
func durationMillis(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

// logError emits a single error-level log line for a failed ping when a logger
// is configured. The job token and API key are never logged (they are
// credentials); a slug job is attributed by its slug, which is not secret.
func (c *Client) logError(job *Job, kind pingKind, err error) {
	if c.logger == nil || err == nil {
		return
	}
	attrs := []any{
		slog.String("action", kind.action()),
		slog.String("error", err.Error()),
	}
	if job.isSlug() {
		attrs = append(attrs, slog.String("slug", job.slug))
	}
	c.logger.Error("ionhour: heartbeat ping failed", attrs...)
}

// withMergedErrorPayload returns a PingOption whose payload merges the run
// error into any caller-supplied payload. A map[string]any payload is copied and
// given an "error" key; any other payload is nested under "payload" alongside
// the "error" key so no caller data is lost.
func withMergedErrorPayload(runErr error, userOpts []PingOption) PingOption {
	base := buildPingRequest(userOpts)
	merged := map[string]any{}
	if base.hasPayload && base.payload != nil {
		if m, ok := base.payload.(map[string]any); ok {
			for k, v := range m {
				merged[k] = v
			}
		} else {
			merged["payload"] = base.payload
		}
	}
	merged["error"] = runErr.Error()
	return WithPayload(merged)
}

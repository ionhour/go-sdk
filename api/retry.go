package api

import (
	"context"
	"io"
	"net/http"
	"time"
)

// maxDrainBytes bounds how much of a discarded response body is read so the
// connection can be reused.
const maxDrainBytes = 64 << 10

// newHTTPClient returns a copy of the configured *http.Client (or a fresh one)
// whose Transport is wrapped with the retry policy. The caller's client is
// never mutated.
func newHTTPClient(cfg config, key string) *http.Client {
	hc := &http.Client{}
	if cfg.httpClient != nil {
		copied := *cfg.httpClient
		hc = &copied
	}
	next := hc.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	hc.Transport = &retryTransport{
		next:         next,
		key:          key,
		timeout:      cfg.timeout,
		maxAttempts:  max(cfg.maxAttempts, 1),
		maxRetryWait: cfg.maxRetryWait,
		sleep:        cfg.sleep,
		backoff:      cfg.backoff,
	}
	return hc
}

// retryTransport implements the SDK contract retry policy:
//
//   - 429: retried for every method (the request was rejected before any
//     handler ran), waiting Retry-After when present and within maxRetryWait,
//     otherwise the regular backoff. A Retry-After above the cap returns the
//     429 to the caller immediately.
//   - 5xx and network errors: retried with backoff 1s, 2s, 2s... but only for
//     requests that are safe to replay: every method except POST, and POST
//     only for the idempotency allowlist (checks_create, jobs_create,
//     projects_create), which always carry a key. Other POSTs (pause, resume,
//     incidents_create, ...) could act twice, so they are never replayed.
//   - A request whose body cannot be re-created (no GetBody) is never retried.
//   - Other 4xx: never retried.
//
// Each attempt gets its own timeout; the attempt's context is released when
// the returned response body is closed.
type retryTransport struct {
	next         http.RoundTripper
	key          string
	timeout      time.Duration
	maxAttempts  int
	maxRetryWait time.Duration
	sleep        sleepFunc
	backoff      backoffFunc
}

// RoundTrip implements http.RoundTripper.
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	for attempt := 1; ; attempt++ {
		resp, err := t.attempt(req, attempt)
		if err != nil && ctx.Err() != nil {
			// The caller gave up: surface ctx.Err() itself, never a retry.
			return nil, ctx.Err()
		}
		last := attempt >= t.maxAttempts || !replayable || ctx.Err() != nil
		wait, retry := t.decide(req, resp, err)
		if !retry || last {
			if err != nil {
				return nil, newScrubbedError(err, t.key)
			}
			return resp, nil
		}
		if wait < 0 {
			wait = t.backoff(attempt)
		}
		if resp != nil {
			discard(resp)
		}
		if sleepErr := t.sleep(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// attempt sends one try with a fresh body and its own deadline.
func (t *retryTransport) attempt(req *http.Request, n int) (*http.Response, error) {
	ctx, cancel := req.Context(), context.CancelFunc(func() {})
	if t.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
	}
	try := req.Clone(ctx)
	if n > 1 && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			cancel()
			return nil, err
		}
		try.Body = body
	}
	resp, err := t.next.RoundTrip(try)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// decide reports whether to retry and how long to wait. A negative wait means
// "use the backoff schedule".
func (t *retryTransport) decide(req *http.Request, resp *http.Response, err error) (time.Duration, bool) {
	if err != nil {
		return -1, req.Context().Err() == nil && safeToReplay(req)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		after := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		if after > t.maxRetryWait {
			return 0, false
		}
		if after > 0 {
			return after, true
		}
		return -1, true
	case resp.StatusCode >= 500:
		return -1, safeToReplay(req)
	default:
		return 0, false
	}
}

// safeToReplay consults the idempotency allowlist, never header presence.
func safeToReplay(req *http.Request) bool {
	return req.Method != http.MethodPost || isIdempotentCreate(req)
}

func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	_ = resp.Body.Close()
}

// cancelOnClose releases the per-attempt context once the caller is done with
// the body (cancelling earlier would abort the body read).
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

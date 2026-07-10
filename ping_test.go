package ionhour

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSuccessSendsGET(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok-123")

	res, err := job.Success(context.Background())

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if res.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", res.Status)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.method)
	}
	if got.path != "/signals/ping/tok-123" {
		t.Errorf("path = %s, want /signals/ping/tok-123", got.path)
	}
	if len(got.body) != 0 {
		t.Errorf("expected empty body, got %q", got.body)
	}
	if ua := got.header.Get("User-Agent"); ua != "ionhour-go/0.1.0" {
		t.Errorf("User-Agent = %q, want ionhour-go/0.1.0", ua)
	}
}

func TestSuccessWithPayloadSendsPOST(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok-123")

	res, err := job.Success(context.Background(), WithPayload(map[string]any{"cpu": 42, "region": "eu"}))

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.path != "/signals/ping/tok-123" {
		t.Errorf("path = %s, want /signals/ping/tok-123", got.path)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("body is not valid JSON: %v (%q)", err, got.body)
	}
	if body["region"] != "eu" {
		t.Errorf("body[region] = %v, want eu", body["region"])
	}
	if body["cpu"].(float64) != 42 {
		t.Errorf("body[cpu] = %v, want 42", body["cpu"])
	}
}

func TestFailSendsFailPath(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok-123")

	res, err := job.Fail(context.Background())

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.method)
	}
	if got.path != "/signals/ping/tok-123/fail" {
		t.Errorf("path = %s, want /signals/ping/tok-123/fail", got.path)
	}
}

func TestFailWithPayloadPostsToFailPath(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok-123")

	res, err := job.Fail(context.Background(), WithPayload(map[string]any{"reason": "timeout"}))

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.path != "/signals/ping/tok-123/fail" {
		t.Errorf("path = %s, want /signals/ping/tok-123/fail", got.path)
	}
}

func TestRIDAndDurationQueryParams(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background(), WithRID("run-9"), WithDuration(1500*time.Millisecond))

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if rid := got.query.Get("rid"); rid != "run-9" {
		t.Errorf("rid = %q, want run-9", rid)
	}
	if dur := got.query.Get("duration"); dur != "1500" {
		t.Errorf("duration = %q, want 1500", dur)
	}
}

func TestAuthorizationHeaderClientKey(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	c := testClient(t, srv.URL, WithAPIKey("client-key"))
	job := mustRegister(t, c, "tok")

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "Bearer client-key" {
		t.Errorf("Authorization = %q, want Bearer client-key", got)
	}
}

func TestAuthorizationHeaderPerJobBeatsClient(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	c := testClient(t, srv.URL, WithAPIKey("client-key"))
	job := mustRegister(t, c, "tok", WithJobAPIKey("job-key"))

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "Bearer job-key" {
		t.Errorf("Authorization = %q, want Bearer job-key", got)
	}
}

func TestAuthorizationHeaderAbsentWhenNoKey(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	var calls int32
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		okHandler(w, r)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background())

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK after retry, got %+v", res)
	}
	if rec.count() != 2 {
		t.Errorf("attempts = %d, want 2", rec.count())
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls int32
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		okHandler(w, r)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background())

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK after retry, got %+v", res)
	}
	if rec.count() != 2 {
		t.Errorf("attempts = %d, want 2", rec.count())
	}
}

func TestNoRetryOn400(t *testing.T) {
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background())

	// A 4xx is a transport outcome, not a usage error: err stays nil.
	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected failure, got %+v", res)
	}
	if res.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", res.Status)
	}
	if rec.count() != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", rec.count())
	}
	var statusErr *HTTPStatusError
	if !errors.As(res.Err, &statusErr) {
		t.Errorf("res.Err = %v, want *HTTPStatusError", res.Err)
	}
}

// Fix 1(b): a 500-after-retries outcome returns a nil usage error while
// PingResult.Err carries the transport failure.
func TestExhaustedRetriesReturnNilUsageErrWithPingErr(t *testing.T) {
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background())

	if err != nil {
		t.Fatalf("usage error = %v, want nil (transport failure is not a usage error)", err)
	}
	if res.OK {
		t.Fatalf("expected failure, got %+v", res)
	}
	if res.Err == nil {
		t.Error("expected PingResult.Err to be populated for the exhausted-retry failure")
	}
	if rec.count() != DefaultRetries {
		t.Errorf("attempts = %d, want %d", rec.count(), DefaultRetries)
	}
}

func TestConnectionRefusedReturnsFailWithoutPanic(t *testing.T) {
	// Env isolation for suite uniformity (matches the testClient helper).
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")

	// Start then immediately close a server so its address refuses connections.
	srv := httptest.NewServer(http.HandlerFunc(okHandler))
	closedURL := srv.URL
	srv.Close()

	c := NewClient(WithBaseURL(closedURL))
	c.backoffFn = func(int) time.Duration { return 0 }
	job := mustRegister(t, c, "tok")

	res, err := job.Success(context.Background())

	if err != nil {
		t.Fatalf("usage error = %v, want nil (network failure is not a usage error)", err)
	}
	if res.OK {
		t.Fatalf("expected failure against closed server, got %+v", res)
	}
	if res.Err == nil {
		t.Error("expected a transport error in PingResult.Err")
	}
	if res.Status != 0 {
		t.Errorf("Status = %d, want 0 for transport error", res.Status)
	}
}

// Fix 1(a): an oversized payload returns a non-nil usage error and sends no
// request.
func TestOversizedPayloadRejected(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	big := strings.Repeat("a", maxPayloadBytes+1)
	res, err := job.Success(context.Background(), WithPayload(big))

	if err == nil {
		t.Fatal("expected a non-nil usage error for the oversized payload")
	}
	var tooLarge *PayloadTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want *PayloadTooLargeError", err)
	}
	if tooLarge.Max != maxPayloadBytes {
		t.Errorf("Max = %d, want %d", tooLarge.Max, maxPayloadBytes)
	}
	if res.OK {
		t.Errorf("expected res.OK false, got %+v", res)
	}
	// The usage error is also mirrored in PingResult.Err for self-description.
	if !errors.As(res.Err, &tooLarge) {
		t.Errorf("res.Err = %v, want *PayloadTooLargeError", res.Err)
	}
	if rec.count() != 0 {
		t.Errorf("no request should be sent for oversized payload, got %d", rec.count())
	}
}

func TestContextAlreadyCancelledAborts(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := job.Success(ctx)

	if err != nil {
		t.Fatalf("usage error = %v, want nil (cancellation is a transport outcome)", err)
	}
	if res.OK {
		t.Fatalf("expected failure for cancelled context, got %+v", res)
	}
	if !errors.Is(res.Err, context.Canceled) {
		t.Errorf("res.Err = %v, want to wrap context.Canceled", res.Err)
	}
	if rec.count() != 0 {
		t.Errorf("no request should be sent when context is already cancelled, got %d", rec.count())
	}
}

// Fix 3(d): deterministic — the backoff hook signals when it is reached and
// returns a delay long enough that only cancellation can end the wait, removing
// any wall-clock dependence.
func TestContextCancelledDuringBackoffStopsRetries(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	c := NewClient(WithBaseURL(srv.URL))
	c.backoffFn = func(int) time.Duration {
		close(entered) // exactly one attempt has completed when the first backoff begins
		return time.Hour
	}
	go func() {
		<-entered
		cancel()
	}()

	job := mustRegister(t, c, "tok")
	res, err := job.Success(ctx)

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected failure, got %+v", res)
	}
	if rec.count() != 1 {
		t.Errorf("attempts = %d, want 1 (cancelled during backoff)", rec.count())
	}
	if !errors.Is(res.Err, context.Canceled) {
		t.Errorf("res.Err = %v, want to wrap context.Canceled", res.Err)
	}
}

func TestDurationParamOmittedWhenUnset(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	_, _ = job.Success(context.Background())

	got := rec.last(t)
	if got.query.Has("duration") {
		t.Error("duration param should be absent when WithDuration is not used")
	}
	if got.query.Has("rid") {
		t.Error("rid param should be absent when WithRID is not used")
	}
}

func TestDurationZeroIsSent(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	_, _ = job.Success(context.Background(), WithDuration(0))

	got := rec.last(t)
	if dur := got.query.Get("duration"); dur != "0" {
		t.Errorf("duration = %q, want 0", dur)
	}
}

func TestPingAliasMatchesSuccess(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Ping(context.Background())

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	if got := rec.last(t); got.path != "/signals/ping/tok" || got.method != http.MethodGet {
		t.Errorf("Ping sent %s %s, want GET /signals/ping/tok", got.method, got.path)
	}
}

func TestNonSerializablePayloadReturnsError(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	// A channel cannot be marshaled to JSON — a usage error.
	res, err := job.Success(context.Background(), WithPayload(make(chan int)))

	if err == nil {
		t.Fatal("expected a non-nil usage error for the non-serializable payload")
	}
	if res.OK {
		t.Fatalf("expected failure, got %+v", res)
	}
	if res.Err == nil {
		t.Error("expected res.Err to mirror the serialization error")
	}
	if rec.count() != 0 {
		t.Errorf("no request should be sent for a non-serializable payload, got %d", rec.count())
	}
}

// Fix 3(b): a failed ping logs at error level and the log line does not contain
// the job token (the credential).
func TestLoggerRecordsFailureWithoutToken(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	c := testClient(t, srv.URL, WithLogger(logger))
	job := mustRegister(t, c, "super-secret-token")

	if _, err := job.Success(context.Background()); err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected a log line for the failed ping")
	}
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("expected an error-level log line, got: %q", out)
	}
	if strings.Contains(out, "super-secret-token") {
		t.Errorf("log line leaked the job token: %q", out)
	}
}

// Laravel-review parity: a token with a URL-unsafe character must be
// path-escaped so it cannot corrupt the URL (an unescaped '#' would start a
// fragment and truncate the path/query into a quiet transport failure). The
// server sees the decoded path, so it must match the raw token exactly and the
// query string must be intact.
func TestTokenIsPathEscaped(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok#seg")

	res, err := job.Success(context.Background(), WithRID("run-1"), WithDuration(5*time.Millisecond))

	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.path != "/signals/ping/tok#seg" {
		t.Errorf("decoded path = %q, want /signals/ping/tok#seg", got.path)
	}
	if rid := got.query.Get("rid"); rid != "run-1" {
		t.Errorf("rid = %q, want run-1 (query must not be corrupted by the token)", rid)
	}
	if dur := got.query.Get("duration"); dur != "5" {
		t.Errorf("duration = %q, want 5", dur)
	}
}

// Rust-review parity: a whitespace-padded key must be trimmed so the Bearer
// header is exact — a padded " ionh_x\n" must not become a malformed header
// that surfaces only as a silent 401.
func TestAPIKeyTrimmedInAuthorizationHeader(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	c := testClient(t, srv.URL, WithAPIKey(" ionh_secret\n"))
	job := mustRegister(t, c, "tok")

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "Bearer ionh_secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ionh_secret")
	}
}

func TestPerJobKeyTrimmedInAuthorizationHeader(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	c := testClient(t, srv.URL, WithAPIKey("ionh_client"))
	job := mustRegister(t, c, "tok", WithJobAPIKey("\tionh_job "))

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "Bearer ionh_job" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ionh_job")
	}
}

func TestWhitespaceOnlyKeyOmitsAuthorization(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	c := testClient(t, srv.URL, WithAPIKey("   \t\n"))
	job := mustRegister(t, c, "tok")

	_, _ = job.Success(context.Background())

	if got := rec.last(t).header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty (whitespace-only key is absent)", got)
	}
}

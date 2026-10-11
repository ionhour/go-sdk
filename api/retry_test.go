package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// statusSequence answers request n with statuses[n-1] (the last one repeats);
// 2xx statuses get okBody.
func statusSequence(okBody string, statuses ...int) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, n int) {
		status := statuses[min(n, len(statuses))-1]
		if status >= 200 && status < 300 {
			writeJSON(w, status, okBody)
			return
		}
		writeJSON(w, status, errorBody(status, "ERR", http.StatusText(status)))
	}
}

func TestRetryPolicy(t *testing.T) {
	cases := []struct {
		name         string
		statuses     []int
		retryAfter   string
		wantRequests int
		wantWaits    []time.Duration
		wantStatus   int // 0 = success
	}{
		{"429 honours Retry-After then succeeds", []int{429, 200}, "2", 2, []time.Duration{2 * time.Second}, 0},
		{"429 without Retry-After uses backoff", []int{429, 200}, "", 2, []time.Duration{time.Second}, 0},
		{"503 twice then success uses 1s then 2s", []int{503, 503, 200}, "", 3, []time.Duration{time.Second, 2 * time.Second}, 0},
		{"5xx exhausts 3 attempts and surfaces the last response", []int{500}, "", 3, []time.Duration{time.Second, 2 * time.Second}, 500},
		{"400 is never retried", []int{400, 200}, "", 1, nil, 400},
		{"404 is never retried", []int{404, 200}, "", 1, nil, 404},
		{"401 is never retried", []int{401, 200}, "", 1, nil, 401},
		{"409 is never retried", []int{409, 200}, "", 1, nil, 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := statusSequence(whoamiBody, tc.statuses...)
			f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				seq(w, r, n)
			})
			c, rec := newTestClient(t, f)
			_, err := c.Whoami(context.Background())
			if tc.wantStatus == 0 && err != nil {
				t.Fatalf("Whoami: %v", err)
			}
			if tc.wantStatus != 0 {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != tc.wantStatus {
					t.Fatalf("err = %v, want status %d", err, tc.wantStatus)
				}
			}
			if n := len(f.requests()); n != tc.wantRequests {
				t.Fatalf("requests = %d, want %d", n, tc.wantRequests)
			}
			if got := rec.waits(); !reflect.DeepEqual(got, tc.wantWaits) {
				t.Fatalf("waits = %v, want %v", got, tc.wantWaits)
			}
		})
	}
}

func TestPostWithIdempotencyKeyIsRetriedWithIdenticalBody(t *testing.T) {
	f := newFakeAPI(t, statusSequence(projectBody, 502, 201))
	c, _ := newTestClient(t, f)
	ctx := WithIdempotencyKey(context.Background(), "create-payments-1")
	rsp, err := c.ProjectsCreateWithResponse(ctx, &ProjectsCreateParams{}, CreateProjectV1Dto{Name: "Payments"})
	if err != nil {
		t.Fatalf("ProjectsCreate: %v", err)
	}
	if rsp.JSON201 == nil || rsp.JSON201.Id != 7 {
		t.Fatalf("JSON201 = %+v", rsp.JSON201)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if string(reqs[0].Body) != string(reqs[1].Body) || len(reqs[1].Body) == 0 {
		t.Fatalf("replayed body differs: %q vs %q", reqs[0].Body, reqs[1].Body)
	}
	for i, r := range reqs {
		if r.Header.Get("Idempotency-Key") != "create-payments-1" {
			t.Fatalf("request %d Idempotency-Key = %q", i, r.Header.Get("Idempotency-Key"))
		}
	}
}

// 429 means the request was rejected before any handler ran, so even a POST
// outside the idempotency allowlist is retried.
func TestPost429IsRetriedOutsideTheAllowlist(t *testing.T) {
	f := newFakeAPI(t, statusSequence(`{}`, 429, 201))
	c, _ := newTestClient(t, f)
	rsp, err := c.IncidentsCreateWithResponse(context.Background(), CreateIncidentV1Dto{Title: "Payments down"})
	if err != nil {
		t.Fatalf("IncidentsCreate: %v", err)
	}
	if rsp.StatusCode() != 201 || len(f.requests()) != 2 {
		t.Fatalf("status %d after %d requests, want 201 after 2", rsp.StatusCode(), len(f.requests()))
	}
}

func TestNetworkErrorIsRetried(t *testing.T) {
	f := newFakeAPI(t, statusSequence(whoamiBody, 200))
	var calls atomic.Int32
	flaky := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("connection reset by peer")
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	c, rec := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: flaky}))
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if calls.Load() != 2 || !reflect.DeepEqual(rec.waits(), []time.Duration{time.Second}) {
		t.Fatalf("calls = %d waits = %v", calls.Load(), rec.waits())
	}
}

func TestPerAttemptTimeoutRetriesASlowAttempt(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	c, _ := newTestClient(t, f, WithTimeout(100*time.Millisecond))
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if n := len(f.requests()); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
}

func TestMaxAttemptsOneDisablesRetries(t *testing.T) {
	f := newFakeAPI(t, statusSequence(whoamiBody, 503, 200))
	c, _ := newTestClient(t, f, WithMaxAttempts(0))
	if _, err := c.Whoami(context.Background()); err == nil {
		t.Fatal("want 503 error")
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	f := newFakeAPI(t, statusSequence(whoamiBody, 503))
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, f, withSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}))
	_, err := c.Whoami(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestContractBackoffIsClamped(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second}
	for i, w := range want {
		if got := contractBackoff(i + 1); got != w {
			t.Fatalf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestSleepContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext on cancelled ctx = %v", err)
	}
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleepContext = %v", err)
	}
}

func TestWithMaxRetryWaitRaisesTheCap(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Retry-After", "90")
		statusSequence(whoamiBody, 429, 200)(w, r, n)
	})
	c, rec := newTestClient(t, f, WithMaxRetryWait(2*time.Minute), WithMaxRetryWait(0))
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if !reflect.DeepEqual(rec.waits(), []time.Duration{90 * time.Second}) {
		t.Fatalf("waits = %v, want [1m30s]", rec.waits())
	}
}

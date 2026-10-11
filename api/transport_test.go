package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// opaqueReader hides the concrete reader type, so http.NewRequest cannot set
// GetBody and the request is not replayable.
type opaqueReader struct{ io.Reader }

func TestNonReplayableBodyIsNeverRetried(t *testing.T) {
	f := newFakeAPI(t, statusSequence(checkBody, 503, 200))
	c, _ := newTestClient(t, f)
	body := opaqueReader{strings.NewReader(`{"name":"Renamed"}`)}
	rsp, err := c.ChecksUpdateWithBodyWithResponse(context.Background(), "101", "application/json", body)
	if err != nil {
		t.Fatalf("ChecksUpdateWithBody: %v", err)
	}
	if rsp.StatusCode() != 503 || len(f.requests()) != 1 {
		t.Fatalf("status %d after %d requests, want 503 after 1", rsp.StatusCode(), len(f.requests()))
	}
}

func TestIdempotentMethodsAreRetriedOn5xx(t *testing.T) {
	cases := []struct {
		name string
		call func(ctx context.Context, c *Client) (int, error)
	}{
		{"PATCH checks_update", func(ctx context.Context, c *Client) (int, error) {
			r, err := c.ChecksUpdateWithResponse(ctx, "101", UpdateCheckV1Dto{Name: ptr("Renamed")})
			return status(r, err), err
		}},
		{"DELETE checks_remove", func(ctx context.Context, c *Client) (int, error) {
			r, err := c.ChecksRemoveWithResponse(ctx, "101")
			return status(r, err), err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					writeJSON(w, http.StatusServiceUnavailable, errorBody(503, "UNAVAILABLE", "try later"))
					return
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				writeJSON(w, http.StatusOK, checkBody)
			})
			c, _ := newTestClient(t, f)
			got, err := tc.call(context.Background(), c)
			if err != nil || got >= 300 {
				t.Fatalf("status %d err %v, want 2xx", got, err)
			}
			reqs := f.requests()
			if len(reqs) != 2 || string(reqs[0].Body) != string(reqs[1].Body) {
				t.Fatalf("requests = %d bodies %q / %q", len(reqs), firstBody(reqs, 0), firstBody(reqs, 1))
			}
		})
	}
}

func firstBody(reqs []recordedRequest, i int) string {
	if i < len(reqs) {
		return string(reqs[i].Body)
	}
	return ""
}

// The inner transport drains every request body, as a real transport does, so
// a retry only carries the body if GetBody was used to re-create it.
func TestRetryRereadsTheBodyViaGetBody(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	consuming := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			return syntheticResponse(r, http.StatusBadGateway, `{}`), nil
		}
		return syntheticResponse(r, http.StatusCreated, projectBody), nil
	})
	f := newFakeAPI(t, func(http.ResponseWriter, *http.Request, int) {})
	c, _ := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: consuming}))
	rsp, err := c.ProjectsCreateWithResponse(context.Background(), nil, CreateProjectV1Dto{Name: "Payments"})
	if err != nil || rsp.StatusCode() != 201 {
		t.Fatalf("status %d err %v", status(rsp, err), err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("bodies = %q, want the same non-empty body twice", bodies)
	}
}

// trackingBody records whether a discarded response was drained and closed,
// which is what lets net/http reuse the connection.
type trackingBody struct {
	r      *bytes.Reader
	closed atomic.Bool
	eof    atomic.Bool
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		b.eof.Store(true)
	}
	return n, err
}

func (b *trackingBody) Close() error { b.closed.Store(true); return nil }

func TestDiscardedResponseIsDrainedAndClosed(t *testing.T) {
	first := &trackingBody{r: bytes.NewReader([]byte(errorBody(503, "UNAVAILABLE", strings.Repeat("x", 4096))))}
	var calls atomic.Int32
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			resp := syntheticResponse(r, http.StatusServiceUnavailable, "")
			resp.Body = first
			return resp, nil
		}
		return syntheticResponse(r, http.StatusOK, whoamiBody), nil
	})
	f := newFakeAPI(t, func(http.ResponseWriter, *http.Request, int) {})
	c, _ := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: tr}))
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if !first.closed.Load() || !first.eof.Load() {
		t.Fatalf("discarded body closed=%v drained=%v, want both", first.closed.Load(), first.eof.Load())
	}
}

func syntheticResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

// Cancelling the caller's context while an attempt is in flight (not via the
// sleep seam) stops at once and surfaces ctx.Err().
func TestCallerCancelMidAttemptSurfacesContextError(t *testing.T) {
	arrived := make(chan struct{})
	var once sync.Once
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		once.Do(func() { close(arrived) })
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	c, rec := newTestClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()
	_, err := c.Whoami(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(f.requests()); n != 1 || len(rec.waits()) != 0 {
		t.Fatalf("requests = %d waits = %v, want 1 request and no backoff", n, rec.waits())
	}
}

func TestRetryAfterCapBoundary(t *testing.T) {
	cases := []struct {
		retryAfter   string
		wantRequests int
		wantWaits    []time.Duration
	}{
		{"60", 2, []time.Duration{60 * time.Second}},
		{"61", 1, nil},
	}
	for _, tc := range cases {
		t.Run("Retry-After "+tc.retryAfter, func(t *testing.T) {
			f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
				w.Header().Set("Retry-After", tc.retryAfter)
				statusSequence(whoamiBody, 429, 200)(w, r, n)
			})
			c, rec := newTestClient(t, f)
			_, _ = c.Whoami(context.Background())
			if n := len(f.requests()); n != tc.wantRequests {
				t.Fatalf("requests = %d, want %d", n, tc.wantRequests)
			}
			if got := rec.waits(); fmt.Sprint(got) != fmt.Sprint(tc.wantWaits) {
				t.Fatalf("waits = %v, want %v", got, tc.wantWaits)
			}
		})
	}
}

func TestRetryAfterHTTPDateThroughTheTransport(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Retry-After", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
		statusSequence(whoamiBody, 429, 200)(w, r, n)
	})
	c, rec := newTestClient(t, f)
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	waits := rec.waits()
	if len(waits) != 1 || waits[0] < 8*time.Second || waits[0] > 10*time.Second || waits[0]%time.Second != 0 {
		t.Fatalf("waits = %v, want one whole-second wait of about 10s", waits)
	}
}

// One Client shared by many goroutines (the Terraform provider pattern).
func TestConcurrentUse(t *testing.T) {
	// Each create fails once (first sight of its auto key), so every create
	// goes through the retry path concurrently, deterministically.
	var seen sync.Map
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		_, again := seen.LoadOrStore(r.Header.Get("Idempotency-Key"), true)
		switch {
		case r.Method == http.MethodPost && !again:
			writeJSON(w, http.StatusServiceUnavailable, errorBody(503, "UNAVAILABLE", "x"))
		case r.Method == http.MethodPost:
			writeJSON(w, http.StatusCreated, projectBody)
		default:
			writeJSON(w, http.StatusOK, whoamiBody)
		}
	})
	c, _ := newTestClient(t, f)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := c.Whoami(context.Background()); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			r, err := c.ProjectsCreateWithResponse(context.Background(), nil, CreateProjectV1Dto{Name: "P"})
			if err == nil {
				err = c.CheckResponse(r.HTTPResponse, r.Body)
			}
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call failed: %v", err)
	}
}

// Some transports report a cancelled request with an error that does not wrap
// the context error; the client must still surface ctx.Err().
func TestCallerCancelSurfacesContextErrorFromAnyTransport(t *testing.T) {
	arrived := make(chan struct{})
	var calls atomic.Int32
	opaque := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(arrived)
		<-r.Context().Done()
		return nil, errors.New("read tcp: connection reset by peer")
	})
	f := newFakeAPI(t, func(http.ResponseWriter, *http.Request, int) {})
	c, _ := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: opaque}))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()
	_, err := c.Whoami(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", calls.Load())
	}
}

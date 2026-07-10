package ionhour

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// capturedRequest is a snapshot of a request received by the recording server.
type capturedRequest struct {
	method string
	path   string
	query  url.Values
	body   []byte
	header http.Header
}

// recorder collects every request a test server receives, for count and
// content assertions. It is safe for concurrent use.
type recorder struct {
	mu       sync.Mutex
	requests []capturedRequest
}

func (r *recorder) add(c capturedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, c)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recorder) last(t *testing.T) capturedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		t.Fatal("no requests were recorded")
	}
	return r.requests[len(r.requests)-1]
}

// all returns a snapshot copy of every recorded request in order, so a test can
// assert on the start/terminal ping pair a run emits.
func (r *recorder) all() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedRequest(nil), r.requests...)
}

// newRecordingServer starts an httptest server that records each request and
// delegates the response to handler. It is torn down automatically.
func newRecordingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.add(capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
			body:   body,
			header: r.Header.Clone(),
		})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// okHandler writes the contract's success response.
func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"message":"OK"}`))
}

// testClient builds a client pointed at baseURL with sleeps eliminated so retry
// tests run instantly. Env vars are cleared so header-absence assertions hold.
func testClient(t *testing.T, baseURL string, opts ...ClientOption) *Client {
	t.Helper()
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")
	all := append([]ClientOption{WithBaseURL(baseURL)}, opts...)
	c := NewClient(all...)
	c.backoffFn = func(int) time.Duration { return 0 }
	return c
}

// mustRegister registers a job or fails the test.
func mustRegister(t *testing.T, c *Client, token string, opts ...JobOption) *Job {
	t.Helper()
	job, err := c.RegisterJob(token, opts...)
	if err != nil {
		t.Fatalf("RegisterJob(%q) returned error: %v", token, err)
	}
	return job
}

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// capturedRequest is a snapshot of a request the recording server received. The
// cmd tests live in package main and cannot import the SDK's internal test
// helpers, so this mirrors sdks/go/helpers_test.go on purpose.
type capturedRequest struct {
	method string
	path   string
	query  url.Values
	body   []byte
	header http.Header
}

// recorder collects every request a test server receives. Safe for concurrent
// use — the exec child and the ping run in the same process but the SDK client
// may retry from a goroutine-free path, so locking keeps -race clean.
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
// assert on the start+terminal ping pair that exec emits.
func (r *recorder) all() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedRequest(nil), r.requests...)
}

// newRecordingServer starts a server that records each request and delegates the
// response to handler. It is torn down automatically. IONHOUR_BASE_URL is
// cleared so base-url assertions are not polluted by the developer's shell.
func newRecordingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()
	t.Setenv("IONHOUR_BASE_URL", "")
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

// statusHandler always responds with the given status code.
func statusHandler(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

// runCLI invokes run with captured stdout/stderr and returns the exit code and
// both streams as strings.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

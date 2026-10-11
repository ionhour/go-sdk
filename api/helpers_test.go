package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

const testKey = "ionh_live_9f8e7d6c5b4a39281706"

// recordedRequest is a snapshot of one request the fake API received.
type recordedRequest struct {
	Method  string
	Path    string // decoded path
	RawPath string // escaped path as sent on the wire
	Query   string
	Header  http.Header
	Body    []byte
}

// fakeAPI is an httptest server that records every request and answers with
// the handler for the n-th request (1-based).
type fakeAPI struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []recordedRequest
	respond func(w http.ResponseWriter, r *http.Request, n int)
}

func newFakeAPI(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{respond: respond}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recordedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			RawPath: r.URL.EscapedPath(),
			Query:   r.URL.RawQuery,
			Header:  r.Header.Clone(),
			Body:    body,
		})
		n := len(f.reqs)
		f.mu.Unlock()
		f.respond(w, r, n)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.reqs...)
}

// baseURL mirrors production: the API lives under the /api prefix.
func (f *fakeAPI) baseURL() string { return f.URL + "/api" }

// sleepRecorder replaces real sleeps so retry tests run instantly while still
// asserting the waits the policy chose.
type sleepRecorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.sleeps = append(s.sleeps, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleepRecorder) waits() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.sleeps...)
}

// clearEnv isolates a test from the developer's real IONHOUR_* variables.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
}

// newTestClient builds a client against f with instant, recorded sleeps.
func newTestClient(t *testing.T, f *fakeAPI, opts ...Option) (*Client, *sleepRecorder) {
	t.Helper()
	clearEnv(t)
	rec := &sleepRecorder{}
	base := []Option{WithAPIKey(testKey), WithBaseURL(f.baseURL()), withSleep(rec.sleep)}
	c, err := NewClient(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, rec
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func ptr[T any](v T) *T { return &v }

const whoamiBody = `{"workspaceId":42,"workspaceName":"Acme","permissionLevel":"read_write","projectedRole":"admin","apiKeyPrefix":"ionh_live_9f","principalKind":"personal"}`

// rateLimitBody is the server's flat 429 shape (V1RateLimitResponse): scope,
// limit and plan at the top level, not under detail.
const rateLimitBody = `{"statusCode":429,"error":"Too Many Requests","code":"RATE_LIMIT_EXCEEDED_KEY",` +
	`"message":"Rate limit of 60 requests per minute exceeded.","scope":"principal","limit":60,"plan":"pro"}`

// errorBody renders a v1 error envelope.
func errorBody(status int, code, message string) string {
	return `{"statusCode":` + strconv.Itoa(status) + `,"error":"` + http.StatusText(status) + `","code":"` + code + `","message":"` + message + `"}`
}

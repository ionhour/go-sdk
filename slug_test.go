package ionhour

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testAPIKey     = "ionh_go_SECRETSECRETSECRET"
	testSecretTail = "SECRETSECRETSECRET"
)

// slugClient builds a client whose signal base refuses connections (token
// pings must never reach the API host) and whose API base is apiURL.
func slugClient(t *testing.T, apiURL string, opts ...ClientOption) *Client {
	t.Helper()
	t.Setenv(envAPIBaseURL, "")
	t.Setenv(envAPIKey, "")
	all := append([]ClientOption{WithAPIBaseURL(apiURL), WithAPIKey(testAPIKey)}, opts...)
	return testClient(t, "http://127.0.0.1:1/api", all...)
}

func mustRegisterSlug(t *testing.T, c *Client, slug string, opts ...SlugJobOption) *Job {
	t.Helper()
	job, err := c.RegisterJobBySlug(slug, opts...)
	if err != nil {
		t.Fatalf("RegisterJobBySlug(%q) returned error: %v", slug, err)
	}
	return job
}

func TestSlugSuccessUsesAPIHostAndBearer(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "nightly-report")

	res, err := job.Success(context.Background())
	if err != nil || !res.OK || res.Status != 200 || res.Created {
		t.Fatalf("Success = %+v, %v; want OK 200 not created", res, err)
	}
	got := rec.last(t)
	if got.method != http.MethodGet || got.path != "/api/signals/s/nightly-report" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", auth)
	}
	if got.query.Has("create") {
		t.Errorf("create param present without WithCreate: %v", got.query)
	}
}

func TestSlugFailStartAndPayload(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl_job")
	ctx := context.Background()

	_, _ = job.Fail(ctx)
	_, _ = job.Start(ctx)
	_, _ = job.Success(ctx, WithPayload(map[string]any{"rows": 3}))

	reqs := rec.all()
	want := []string{"/api/signals/s/etl_job/fail", "/api/signals/s/etl_job/start", "/api/signals/s/etl_job"}
	for i, path := range want {
		if reqs[i].path != path {
			t.Errorf("request %d path = %q, want %q", i, reqs[i].path, path)
		}
		if reqs[i].header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("request %d missing Bearer", i)
		}
	}
	if reqs[2].method != http.MethodPost || string(reqs[2].body) != `{"rows":3}` {
		t.Errorf("payload request = %s %s", reqs[2].method, reqs[2].body)
	}
}

func TestSlugCreateAddsQueryParamWithTelemetry(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl", WithCreate())

	_, _ = job.Fail(context.Background(), WithRID("r1"), WithDuration(42*time.Millisecond), WithHost("web-1"), WithEnv("prod"), WithExitCode(2))

	q := rec.last(t).query
	checks := map[string]string{"create": "1", "rid": "r1", "duration": "42", "host": "web-1", "env": "prod", "exit_code": "2"}
	for k, v := range checks {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestSlugCreatedFrom201AndBody(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"201", http.StatusCreated, `{"message":"OK","created":true}`},
		{"200 with created body", http.StatusOK, `{"message":"OK","created":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			job := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl", WithCreate())

			res, _ := job.Success(context.Background())
			if !res.OK || !res.Created || res.Status != tc.status {
				t.Fatalf("result = %+v, want OK + Created", res)
			}
		})
	}
}

func TestTokenPingsNeverSendAuthorizationWithAPIKeyConfigured(t *testing.T) {
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"created":true}`))
	})
	t.Setenv(envAPIKey, "ionh_from_env")
	c := testClient(t, srv.URL+"/api", WithAPIKey(testAPIKey))
	job := mustRegister(t, c, "tok")
	ctx := context.Background()

	res, _ := job.Success(ctx)
	_, _ = job.Fail(ctx)
	_, _ = job.Start(ctx)
	_, _ = job.Success(ctx, WithPayload(map[string]any{"a": 1}))

	if res.Created {
		t.Error("token ping reported Created")
	}
	for i, r := range rec.all() {
		if r.header.Get("Authorization") != "" {
			t.Errorf("token request %d sent Authorization", i)
		}
		if !strings.HasPrefix(r.path, "/api/signals/ping/tok") {
			t.Errorf("token request %d path = %q", i, r.path)
		}
	}
}

func TestSlugConfigFromEnv(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	t.Setenv(envBaseURL, "")
	t.Setenv(envAPIBaseURL, srv.URL+"/api/")
	t.Setenv(envAPIKey, "  "+testAPIKey+"\n")

	job := mustRegisterSlug(t, NewClient(), "etl")
	_, _ = job.Success(context.Background())

	got := rec.last(t)
	if got.path != "/api/signals/s/etl" {
		t.Errorf("path = %q", got.path)
	}
	if got.header.Get("Authorization") != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", got.header.Get("Authorization"))
	}
}

func TestSlugAPIKeyPrecedenceAndTrim(t *testing.T) {
	t.Setenv(envAPIKey, "ionh_env")
	if c := NewClient(WithAPIKey("  " + testAPIKey + " ")); c.apiKey != testAPIKey {
		t.Errorf("explicit key not preferred/trimmed")
	}
	if c := NewClient(WithAPIKey("   ")); c.apiKey != "ionh_env" {
		t.Errorf("blank explicit key should fall back to env, got %q", c.apiKey)
	}
}

func TestSlugDefaultAPIBaseURL(t *testing.T) {
	t.Setenv(envAPIBaseURL, "")
	if c := NewClient(); c.apiBaseURL != DefaultAPIBaseURL || DefaultAPIBaseURL != "https://api.ionhour.com/api" {
		t.Errorf("apiBaseURL = %q", c.apiBaseURL)
	}
}

func TestRegisterJobBySlugValidation(t *testing.T) {
	t.Setenv(envAPIKey, "")
	c := NewClient(WithAPIKey(testAPIKey))
	for _, slug := range []string{"", "Nightly", strings.Repeat("a", 65), "a/b", "a b", "a.b", "ü"} {
		_, err := c.RegisterJobBySlug(slug)
		var invalid *InvalidSlugError
		if !errors.As(err, &invalid) {
			t.Errorf("RegisterJobBySlug(%q) err = %v, want *InvalidSlugError", slug, err)
		}
	}
	for _, slug := range []string{"a", strings.Repeat("a", 64), "nightly_job-2"} {
		if _, err := c.RegisterJobBySlug(slug); err != nil {
			t.Errorf("RegisterJobBySlug(%q) err = %v, want nil", slug, err)
		}
	}
}

func TestRegisterJobBySlugMissingAPIKey(t *testing.T) {
	t.Setenv(envAPIKey, "  ")
	_, err := NewClient(WithAPIKey("")).RegisterJobBySlug("etl")
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("err = %v, want ErrMissingAPIKey", err)
	}
	if !strings.Contains(err.Error(), "IONHOUR_API_KEY") {
		t.Errorf("error should name IONHOUR_API_KEY: %v", err)
	}
}

func TestRegisterJobBySlugRejectsMalformedKeyWithoutEchoing(t *testing.T) {
	t.Setenv(envAPIKey, "")
	_, err := NewClient(WithAPIKey("ionh_abc\r\nX-Injected: 1")).RegisterJobBySlug("etl")
	if !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("err = %v, want ErrInvalidAPIKey", err)
	}
	if strings.Contains(err.Error(), "ionh_abc") {
		t.Errorf("error echoes the key: %v", err)
	}
}

func TestTokenJobsNeedNoAPIKey(t *testing.T) {
	t.Setenv(envAPIKey, "")
	if _, err := NewClient().RegisterJob("tok"); err != nil {
		t.Fatalf("RegisterJob err = %v", err)
	}
}

func TestSlugRetries429And5xx(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int32
			srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if atomic.AddInt32(&calls, 1) == 1 {
					w.WriteHeader(status)
					return
				}
				okHandler(w, nil)
			})
			res, _ := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl").Success(context.Background())
			if !res.OK || rec.count() != 2 {
				t.Fatalf("result = %+v after %d requests, want OK after 2", res, rec.count())
			}
		})
	}
}

func TestSlugNeverRetriesEnvelope4xxAndSurfacesCode(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{400, "VALIDATION_FAILED"},
		{401, "MISSING_BEARER"},
		{401, "SERVICE_KEY_INACTIVE"},
		{403, "KEY_SCOPE_DENIED"},
		{403, "INSUFFICIENT_ROLE"},
		{403, "READ_ONLY_KEY"},
		{403, "FEATURE_DISABLED"},
		{404, "NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			srv, rec := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"statusCode":%d,"error":"x","code":%q,"message":"nope"}`, tc.status, tc.code)
			})
			res, err := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl", WithCreate()).Success(context.Background())
			if err != nil {
				t.Fatalf("usage err = %v", err)
			}
			if res.OK || res.Status != tc.status || rec.count() != 1 {
				t.Fatalf("result = %+v after %d requests", res, rec.count())
			}
			var statusErr *HTTPStatusError
			if !errors.As(res.Err, &statusErr) || statusErr.Code != tc.code {
				t.Fatalf("Err = %v, want HTTPStatusError code %s", res.Err, tc.code)
			}
			if !strings.Contains(res.Err.Error(), tc.code) {
				t.Errorf("Err text %q lacks code", res.Err.Error())
			}
		})
	}
}

func TestSlugIgnoresMalformedErrorCode(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"code":"bad code %s"}`, testAPIKey)
	})
	res, _ := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl").Success(context.Background())
	var statusErr *HTTPStatusError
	if !errors.As(res.Err, &statusErr) || statusErr.Code != "" {
		t.Fatalf("Err = %v, want empty code", res.Err)
	}
	if strings.Contains(res.Err.Error(), testSecretTail) {
		t.Errorf("Err leaks key: %v", res.Err)
	}
}

func TestSlugTransportErrorNeverPanics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(okHandler))
	closed := srv.URL
	srv.Close()
	res, err := mustRegisterSlug(t, slugClient(t, closed+"/api"), "etl").Success(context.Background())
	if err != nil || res.OK || res.Err == nil {
		t.Fatalf("result = %+v, %v; want transport failure", res, err)
	}
}

// leakyTransport fails every request with an error echoing the Authorization
// header, standing in for a third-party RoundTripper that is careless with it.
type leakyTransport struct{}

func (leakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("proxy refused request with %s", r.Header.Get("Authorization"))
}

func TestSlugAPIKeyScrubbedFromErrorsAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	c := slugClient(t, "http://127.0.0.1:1/api",
		WithHTTPClient(&http.Client{Transport: leakyTransport{}}),
		WithLogger(logger), WithRetries(1))

	res, _ := mustRegisterSlug(t, c, "etl").Fail(context.Background())

	if res.OK || res.Err == nil {
		t.Fatalf("expected failure, got %+v", res)
	}
	if strings.Contains(res.Err.Error(), testSecretTail) {
		t.Errorf("PingResult.Err leaks key: %v", res.Err)
	}
	if strings.Contains(buf.String(), testSecretTail) {
		t.Errorf("log leaks key: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "slug=etl") {
		t.Errorf("log should attribute the slug: %s", buf.String())
	}
}

func TestSlugAPIKeyNeverPrinted(t *testing.T) {
	c := slugClient(t, "http://127.0.0.1:1/api")
	job := mustRegisterSlug(t, c, "etl", WithCreate())
	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		for _, v := range []any{c, job} {
			if out := fmt.Sprintf(verb, v); strings.Contains(out, testSecretTail) {
				t.Errorf("%s leaks key: %s", verb, out)
			}
		}
	}
	if out := fmt.Sprintf("%v", job); !strings.Contains(out, "etl") {
		t.Errorf("job string should name the slug: %s", out)
	}
	if job.Slug() != "etl" || job.Token() != "" {
		t.Errorf("Slug()/Token() = %q/%q", job.Slug(), job.Token())
	}
}

func TestTokenJobGoStringRedacted(t *testing.T) {
	job := mustRegister(t, testClient(t, "http://127.0.0.1:1/api"), "tok-secret-value")
	if out := fmt.Sprintf("%#v", job); strings.Contains(out, "tok-secret-value") {
		t.Errorf("%%#v leaks token: %s", out)
	}
}

func TestSlugRunSendsBearerOnStartAndTerminal(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegisterSlug(t, slugClient(t, srv.URL+"/api"), "etl")

	if err := job.Run(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 2 || reqs[0].path != "/api/signals/s/etl/start" || reqs[1].path != "/api/signals/s/etl" {
		t.Fatalf("requests = %+v", reqs)
	}
	for _, r := range reqs {
		if r.header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("missing Bearer on %s", r.path)
		}
	}
}

func TestPackageLevelRegisterJobBySlug(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	t.Setenv(envBaseURL, "")
	t.Setenv(envAPIBaseURL, srv.URL+"/api")
	t.Setenv(envAPIKey, testAPIKey)
	resetDefaultClientForTest()
	t.Cleanup(resetDefaultClientForTest)

	job, err := RegisterJobBySlug("etl", WithCreate())
	if err != nil {
		t.Fatalf("RegisterJobBySlug err = %v", err)
	}
	_, _ = job.Success(context.Background())
	if got := rec.last(t); got.query.Get("create") != "1" || got.header.Get("Authorization") != "Bearer "+testAPIKey {
		t.Errorf("request = %+v", got)
	}
}

func TestInvalidSlugErrorNeverEchoesFullSlug(t *testing.T) {
	t.Setenv(envAPIKey, "")
	// A caller that swaps the arguments passes the key as the slug.
	_, err := NewClient(WithAPIKey("ionh_ok")).RegisterJobBySlug(testAPIKey)
	var invalid *InvalidSlugError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *InvalidSlugError", err)
	}
	if strings.Contains(err.Error(), testSecretTail) {
		t.Errorf("error text echoes the slug: %v", err)
	}
	if !strings.Contains(err.Error(), "ionh_go_…") {
		t.Errorf("error text should carry the redacted prefix: %v", err)
	}
	if invalid.Slug != testAPIKey {
		t.Errorf("Slug field = %q, want the full rejected value for errors.As callers", invalid.Slug)
	}
}

func TestTokenPing403EnvelopeYieldsPlainStatus(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"statusCode":403,"error":"x","code":"KEY_SCOPE_DENIED","message":"no"}`))
	})
	res, _ := mustRegister(t, testClient(t, srv.URL+"/api"), "tok").Success(context.Background())
	var statusErr *HTTPStatusError
	if !errors.As(res.Err, &statusErr) || statusErr.Code != "" {
		t.Fatalf("Err = %v, want HTTPStatusError without code", res.Err)
	}
	if got := res.Err.Error(); got != "ionhour: server responded with HTTP status 403" {
		t.Errorf("Err text = %q", got)
	}
}

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCheckResponseParsesEnvelopeWithDetail(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusForbidden, `{"statusCode":403,"error":"Forbidden","code":"PLAN_LIMIT_EXCEEDED",`+
			`"message":"This workspace's current plan allows a maximum of 5 for \"checks\".",`+
			`"detail":{"limit":5,"resource":"checks"}}`)
	})
	c, _ := newTestClient(t, f)
	rsp, err := c.ChecksCreateWithResponse(context.Background(), &ChecksCreateParams{}, CreateCheckV1Dto{
		Name: "x", ProjectId: 7, Config: V1HttpCheckConfigDto{Url: "https://example.com"},
	})
	if err != nil {
		t.Fatalf("ChecksCreate: %v", err)
	}
	if rsp.JSON403 == nil || rsp.JSON403.Code != "PLAN_LIMIT_EXCEEDED" {
		t.Fatalf("generated JSON403 = %+v", rsp.JSON403)
	}
	err = c.CheckResponse(rsp.HTTPResponse, rsp.Body)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != 403 || apiErr.Code != "PLAN_LIMIT_EXCEEDED" || !strings.Contains(apiErr.Message, "maximum of 5") {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if apiErr.Detail["resource"] != "checks" || apiErr.Detail["limit"] != float64(5) {
		t.Fatalf("detail = %#v", apiErr.Detail)
	}
	if got := apiErr.Error(); got != `ionhour api: 403 PLAN_LIMIT_EXCEEDED: This workspace's current plan allows a maximum of 5 for "checks".` {
		t.Fatalf("Error() = %q", got)
	}
}

func TestCheckResponseValidationMessageList(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusBadRequest, `{"statusCode":400,"error":"Bad Request","code":"VALIDATION_FAILED",`+
			`"message":["name must be shorter than or equal to 255 characters","projectId must be an integer number"]}`)
	})
	c, _ := newTestClient(t, f)
	rsp, err := c.ProjectsCreateWithResponse(context.Background(), &ProjectsCreateParams{}, CreateProjectV1Dto{Name: "x"})
	if err != nil {
		t.Fatalf("ProjectsCreate: %v", err)
	}
	var apiErr *APIError
	if !errors.As(c.CheckResponse(rsp.HTTPResponse, rsp.Body), &apiErr) {
		t.Fatal("want *APIError")
	}
	if len(apiErr.Messages) != 2 || apiErr.Message != apiErr.Messages[0]+"; "+apiErr.Messages[1] {
		t.Fatalf("messages = %q / %q", apiErr.Message, apiErr.Messages)
	}
	if apiErr.Detail != nil {
		t.Fatalf("detail = %#v, want nil", apiErr.Detail)
	}
}

func TestCheckResponseNonEnvelopeBody(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	})
	c, _ := newTestClient(t, f)
	rsp, err := c.RegionsListWithResponse(context.Background())
	if err != nil {
		t.Fatalf("RegionsList: %v", err)
	}
	var apiErr *APIError
	if !errors.As(c.CheckResponse(rsp.HTTPResponse, rsp.Body), &apiErr) {
		t.Fatal("want *APIError")
	}
	if apiErr.Status != 502 || apiErr.Code != "HTTP_502" || apiErr.Message != "HTTP 502" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestCheckResponseNilOn2xx(t *testing.T) {
	c := &Client{key: testKey}
	if err := c.CheckResponse(&http.Response{StatusCode: 204, Header: http.Header{}}, nil); err != nil {
		t.Fatalf("204 -> %v, want nil", err)
	}
	if err := c.CheckResponse(nil, nil); err != nil {
		t.Fatalf("nil response -> %v, want nil", err)
	}
}

func TestRateLimitBeyondCapReturnsAPIErrorWithRetryAfter(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, http.StatusTooManyRequests, rateLimitBody)
	})
	c, rec := newTestClient(t, f)
	_, err := c.Whoami(context.Background())
	if !IsRateLimited(err) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	var apiErr *APIError
	errors.As(err, &apiErr)
	if apiErr.RetryAfter != 120*time.Second || apiErr.Code != "RATE_LIMIT_EXCEEDED_KEY" ||
		apiErr.Detail["scope"] != "principal" || apiErr.Detail["limit"] != float64(60) || apiErr.Detail["plan"] != "pro" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1 (Retry-After above the 60s cap is not slept)", n)
	}
	if len(rec.waits()) != 0 {
		t.Fatalf("slept %v, want no sleep", rec.waits())
	}
}

func TestErrorHelpersSeeThroughWrapping(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusTooManyRequests, IsRateLimited},
		{http.StatusNotFound, IsNotFound},
		{http.StatusConflict, IsConflict},
		{http.StatusUnauthorized, IsUnauthorized},
		{http.StatusForbidden, IsForbidden},
	}
	for _, tc := range cases {
		err := fmt.Errorf("provider: %w", &APIError{Status: tc.status})
		if !tc.check(err) {
			t.Fatalf("helper for %d returned false", tc.status)
		}
		if tc.check(&APIError{Status: 400}) || tc.check(errors.New("plain")) || tc.check(nil) {
			t.Fatalf("helper for %d matched a different error", tc.status)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		" 0 ":                           0,
		"-3":                            0,
		"soon":                          0,
		"Sun, 11 Oct 2026 12:00:30 GMT": 30 * time.Second,
		"Sun, 11 Oct 2026 11:59:00 GMT": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Fatalf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestKeyNeverAppearsInErrorStrings(t *testing.T) {
	t.Run("server echoes the key", func(t *testing.T) {
		f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
			writeJSON(w, http.StatusUnauthorized, errorBody(http.StatusUnauthorized, "INVALID_KEY", "Key "+testKey+" is not valid."))
		})
		c, _ := newTestClient(t, f)
		_, err := c.Whoami(context.Background())
		if err == nil || strings.Contains(err.Error(), testKey) {
			t.Fatalf("err = %v, must exist and not contain the key", err)
		}
		if !strings.Contains(err.Error(), redactToken(testKey)) {
			t.Fatalf("err = %v, want the redacted prefix", err)
		}
	})

	t.Run("transport error mentions the key", func(t *testing.T) {
		sentinel := errors.New("dial failed")
		leaky := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("proxy rejected Authorization Bearer %s: %w", testKey, sentinel)
		})
		f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {})
		c, _ := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: leaky}))
		_, err := c.Whoami(context.Background())
		if err == nil || strings.Contains(err.Error(), testKey) {
			t.Fatalf("err = %v, must exist and not contain the key", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("errors.Is lost the cause: %v", err)
		}
		if !strings.Contains(fmt.Sprintf("%+v", err), redactToken(testKey)) {
			t.Fatalf("%%+v = %+v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Generated methods bypass the facade's own scrubbing, so the transport must
// scrub transport errors itself.
func TestGeneratedMethodTransportErrorIsScrubbed(t *testing.T) {
	leaky := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("tls handshake for key %s failed", testKey)
	})
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {})
	c, _ := newTestClient(t, f, WithHTTPClient(&http.Client{Transport: leaky}))
	_, err := c.RegionsListWithResponse(context.Background())
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v, must exist and not contain the key", err)
	}
}

func TestParseAPIErrorEnvelopeShapes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantCode   string
		wantMsg    string
		wantDetail map[string]any
	}{
		{"non-object detail keeps the envelope", 403, `{"statusCode":403,"code":"PLAN_LIMIT_EXCEEDED","message":"limit","detail":"checks"}`,
			"PLAN_LIMIT_EXCEEDED", "limit", nil},
		{"array detail keeps the envelope", 403, `{"statusCode":403,"code":"FORBIDDEN","message":"no","detail":[1,2]}`,
			"FORBIDDEN", "no", nil},
		{"null detail", 404, `{"statusCode":404,"code":"NOT_FOUND","message":"gone","detail":null}`,
			"NOT_FOUND", "gone", nil},
		{"null detail falls back to extras (Node parity)", 429, `{"statusCode":429,"code":"RATE_LIMIT_EXCEEDED_KEY","message":"m","detail":null,"scope":"ip","limit":1200}`,
			"RATE_LIMIT_EXCEEDED_KEY", "m", map[string]any{"scope": "ip", "limit": float64(1200)}},
		{"object detail", 403, `{"statusCode":403,"code":"PLAN_LIMIT_EXCEEDED","message":"m","detail":{"limit":5}}`,
			"PLAN_LIMIT_EXCEEDED", "m", map[string]any{"limit": float64(5)}},
		{"flat extras become detail (Node parity)", 429, `{"statusCode":429,"error":"Too Many Requests","code":"RATE_LIMIT_EXCEEDED_KEY","message":"slow down","scope":"principal","limit":60,"plan":"pro"}`,
			"RATE_LIMIT_EXCEEDED_KEY", "slow down", map[string]any{"scope": "principal", "limit": float64(60), "plan": "pro"}},
		{"envelope without code", 500, `{"statusCode":500,"error":"Internal Server Error","message":"boom"}`,
			"HTTP_500", "HTTP 500", nil},
		{"empty code", 500, `{"statusCode":500,"code":"","message":"boom"}`,
			"HTTP_500", "HTTP 500", nil},
		{"null message", 400, `{"statusCode":400,"code":"VALIDATION_FAILED","message":null}`, "HTTP_400", "HTTP 400", nil},
		{"non-string message", 400, `{"statusCode":400,"code":"VALIDATION_FAILED","message":{"x":1}}`,
			"HTTP_400", "HTTP 400", nil},
		{"empty body", 503, ``, "HTTP_503", "HTTP 503", nil},
		{"JSON array body", 502, `[1]`, "HTTP_502", "HTTP 502", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := parseAPIError(&http.Response{StatusCode: tc.status, Header: http.Header{}}, []byte(tc.body))
			if apiErr.Code != tc.wantCode || apiErr.Message != tc.wantMsg || apiErr.Status != tc.status {
				t.Fatalf("got %d %q %q, want %d %q %q", apiErr.Status, apiErr.Code, apiErr.Message, tc.status, tc.wantCode, tc.wantMsg)
			}
			if fmt.Sprint(apiErr.Detail) != fmt.Sprint(tc.wantDetail) || (tc.wantDetail == nil) != (apiErr.Detail == nil) {
				t.Fatalf("detail = %#v, want %#v", apiErr.Detail, tc.wantDetail)
			}
		})
	}
}

func TestParseRetryAfterIsClamped(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"99999999999999999999999":       maxRetryAfter,
		"9223372036854775807":           maxRetryAfter,
		"86401":                         maxRetryAfter,
		"86400":                         maxRetryAfter,
		"+5":                            0,
		"5.5":                           0,
		"Fri, 11 Oct 2120 12:00:00 GMT": maxRetryAfter,
		"Sun, 11 Oct 2026 12:00:09 GMT": 9 * time.Second,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Fatalf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
	if got := parseRetryAfter("Sun, 11 Oct 2026 12:00:09 GMT", now.Add(500*time.Millisecond)); got != 9*time.Second {
		t.Fatalf("HTTP-date is rounded up to whole seconds: got %v, want 9s", got)
	}
}

// A huge Retry-After must never be read as 0 (which would retry at once).
func TestHugeRetryAfterIsNotRetried(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Retry-After", "99999999999999999999999")
		statusSequence(whoamiBody, 429, 200)(w, r, n)
	})
	c, _ := newTestClient(t, f)
	_, err := c.Whoami(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != maxRetryAfter || len(f.requests()) != 1 {
		t.Fatalf("err = %v requests = %d", err, len(f.requests()))
	}
}

// The generated client decodes the same 429 into the typed V1RateLimitResponse.
func TestGeneratedRateLimitResponseIsTyped(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, http.StatusTooManyRequests, rateLimitBody)
	})
	c, _ := newTestClient(t, f)
	rsp, err := c.RegionsListWithResponse(context.Background())
	if err != nil {
		t.Fatalf("RegionsList: %v", err)
	}
	rl := rsp.JSON429
	if rl == nil || rl.Scope != V1RateLimitScopePrincipal || rl.Limit != 60 || rl.Plan == nil || *rl.Plan != WorkspacePlanPro {
		t.Fatalf("JSON429 = %+v", rl)
	}
	if !IsRateLimited(c.CheckResponse(rsp.HTTPResponse, rsp.Body)) {
		t.Fatal("CheckResponse should report the 429")
	}
}

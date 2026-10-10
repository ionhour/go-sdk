package ionhour

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewClientDefaults(t *testing.T) {
	t.Setenv(envBaseURL, "")

	c := NewClient()

	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if c.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.timeout, DefaultTimeout)
	}
	if c.retries != DefaultRetries {
		t.Errorf("retries = %d, want %d", c.retries, DefaultRetries)
	}
	if c.userAgent != "ionhour-go/0.1.0" {
		t.Errorf("userAgent = %q, want %q", c.userAgent, "ionhour-go/0.1.0")
	}
	if c.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNewClientEnvFallback(t *testing.T) {
	t.Setenv(envBaseURL, "https://env.example/api")

	c := NewClient()
	if c.baseURL != "https://env.example/api" {
		t.Errorf("baseURL = %q, want env value", c.baseURL)
	}
}

func TestNewClientOptionsOverrideEnv(t *testing.T) {
	t.Setenv(envBaseURL, "https://env.example/api")

	c := NewClient(
		WithBaseURL("https://opt.example"),
		WithTimeout(3*time.Second),
		WithUserAgent("custom/9.9"),
	)
	if c.baseURL != "https://opt.example" {
		t.Errorf("baseURL = %q, want opt value", c.baseURL)
	}
	if c.timeout != 3*time.Second {
		t.Errorf("timeout = %v, want 3s", c.timeout)
	}
	if c.userAgent != "custom/9.9" {
		t.Errorf("userAgent = %q, want custom/9.9", c.userAgent)
	}
}

func TestNewClientRetriesClamped(t *testing.T) {
	for _, n := range []int{0, -5} {
		c := NewClient(WithRetries(n))
		if c.retries != 1 {
			t.Errorf("WithRetries(%d): retries = %d, want 1", n, c.retries)
		}
	}
}

func TestDefaultBackoffClamp(t *testing.T) {
	// The schedule is 1s before the second attempt, then 2s clamped for every
	// subsequent wait — never linear (attempt*1s) or exponential growth. The
	// attempt=4 and attempt=7 rows assert the 4th-and-later waits stay at 2s.
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 2 * time.Second},
		{4, 2 * time.Second},
		{7, 2 * time.Second},
	}
	for _, tc := range cases {
		if got := defaultBackoff(tc.attempt); got != tc.want {
			t.Errorf("defaultBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestWithUserAgentEmptyIgnored(t *testing.T) {
	c := NewClient(WithUserAgent(""))
	if c.userAgent != "ionhour-go/0.1.0" {
		t.Errorf("empty user agent overrode default: got %q", c.userAgent)
	}
}

func TestWithHTTPClientNilIgnored(t *testing.T) {
	c := NewClient(WithHTTPClient(nil))
	if c.httpClient == nil {
		t.Error("nil http client should be ignored, leaving the default in place")
	}
}

func TestJobStringRedactsCredentials(t *testing.T) {
	t.Setenv(envBaseURL, "")

	job := mustRegister(t, NewClient(), "raw-token-value")

	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, job)
		if strings.Contains(out, "raw-token-value") {
			t.Errorf("%s leaked the token: %q", verb, out)
		}
		if !strings.Contains(out, "redacted") {
			t.Errorf("%s should mark the redaction: %q", verb, out)
		}
	}
}

func TestClientStringShowsSettingsWithoutAPIKey(t *testing.T) {
	t.Setenv(envBaseURL, "")

	c := NewClient(WithBaseURL("https://x.test/api"))

	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, "apiKey") {
			t.Errorf("%s should not mention an api key (pings send none): %q", verb, out)
		}
		if !strings.Contains(out, "https://x.test/api") {
			t.Errorf("%s should still show the non-sensitive base URL: %q", verb, out)
		}
	}
}

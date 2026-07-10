package ionhour

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewClientDefaults(t *testing.T) {
	t.Setenv(envAPIKey, "")
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
	if c.apiKey != "" {
		t.Errorf("apiKey = %q, want empty", c.apiKey)
	}
	if c.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNewClientEnvFallback(t *testing.T) {
	t.Setenv(envAPIKey, "env-key")
	t.Setenv(envBaseURL, "https://env.example/api")

	c := NewClient()
	if c.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key", c.apiKey)
	}
	if c.baseURL != "https://env.example/api" {
		t.Errorf("baseURL = %q, want env value", c.baseURL)
	}
}

func TestNewClientOptionsOverrideEnv(t *testing.T) {
	t.Setenv(envAPIKey, "env-key")
	t.Setenv(envBaseURL, "https://env.example/api")

	c := NewClient(
		WithAPIKey("opt-key"),
		WithBaseURL("https://opt.example"),
		WithTimeout(3*time.Second),
		WithUserAgent("custom/9.9"),
	)
	if c.apiKey != "opt-key" {
		t.Errorf("apiKey = %q, want opt-key", c.apiKey)
	}
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

func TestEffectiveAPIKeyPrecedence(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")

	c := NewClient(WithAPIKey("client-key"))

	clientOnly := mustRegister(t, c, "tok")
	if got := clientOnly.effectiveAPIKey(); got != "client-key" {
		t.Errorf("client-only key = %q, want client-key", got)
	}

	perJob := mustRegister(t, c, "tok", WithJobAPIKey("job-key"))
	if got := perJob.effectiveAPIKey(); got != "job-key" {
		t.Errorf("per-job key = %q, want job-key", got)
	}

	noKey := mustRegister(t, NewClient(), "tok")
	if got := noKey.effectiveAPIKey(); got != "" {
		t.Errorf("no key = %q, want empty", got)
	}
}

func TestAPIKeyTrimmedFromOptionAndEnv(t *testing.T) {
	t.Setenv(envBaseURL, "")

	// Option value is trimmed.
	t.Setenv(envAPIKey, "")
	if c := NewClient(WithAPIKey("  ionh_opt\n")); c.apiKey != "ionh_opt" {
		t.Errorf("option apiKey = %q, want ionh_opt", c.apiKey)
	}

	// Whitespace-only option is treated as absent, so the env fallback applies.
	t.Setenv(envAPIKey, "ionh_env")
	if c := NewClient(WithAPIKey("   ")); c.apiKey != "ionh_env" {
		t.Errorf("whitespace option should fall back to env, got %q", c.apiKey)
	}

	// Env value is trimmed.
	t.Setenv(envAPIKey, "\tionh_env2 ")
	if c := NewClient(); c.apiKey != "ionh_env2" {
		t.Errorf("env apiKey = %q, want ionh_env2", c.apiKey)
	}

	// Whitespace-only env is treated as absent.
	t.Setenv(envAPIKey, "  \n")
	if c := NewClient(); c.apiKey != "" {
		t.Errorf("whitespace-only env apiKey = %q, want empty", c.apiKey)
	}
}

func TestJobStringRedactsCredentials(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")

	c := NewClient(WithAPIKey("ionh_super_secret"))
	job := mustRegister(t, c, "raw-token-value", WithJobAPIKey("ionh_job_secret"))

	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, job)
		if strings.Contains(out, "raw-token-value") {
			t.Errorf("%s leaked the token: %q", verb, out)
		}
		if strings.Contains(out, "ionh_job_secret") || strings.Contains(out, "ionh_super_secret") {
			t.Errorf("%s leaked an api key: %q", verb, out)
		}
		if !strings.Contains(out, "redacted") {
			t.Errorf("%s should mark the redaction: %q", verb, out)
		}
	}
}

func TestClientStringRedactsAPIKey(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")

	c := NewClient(WithAPIKey("ionh_super_secret"), WithBaseURL("https://x.test/api"))

	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, "ionh_super_secret") {
			t.Errorf("%s leaked the api key: %q", verb, out)
		}
		if !strings.Contains(out, "https://x.test/api") {
			t.Errorf("%s should still show the non-sensitive base URL: %q", verb, out)
		}
	}
}

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWhoamiSendsBearerAndDefaultUserAgent(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	c, _ := newTestClient(t, f)

	id, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	role, roleErr := id.ProjectedRole.Get()
	prefix, prefixErr := id.ApiKeyPrefix.Get()
	if id.WorkspaceId != 42 || id.WorkspaceName != "Acme" || id.PermissionLevel != ApiKeyPermissionReadWrite ||
		id.PrincipalKind != PrincipalKindPersonal || roleErr != nil || role != WorkspaceRoleAdmin ||
		prefixErr != nil || prefix != "ionh_live_9f" {
		t.Fatalf("whoami = %+v", *id)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.Path != "/api/v1/whoami" {
		t.Fatalf("request = %s %s, want GET /api/v1/whoami", r.Method, r.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Fatalf("Authorization = %q", got)
	}
	if got := r.Header.Get("User-Agent"); got != "ionhour-api-go/"+Version {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestWithUserAgentOverridesAndEmptyIsIgnored(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	c, _ := newTestClient(t, f, WithUserAgent("terraform-provider-ionhour/0.1.0"), WithUserAgent(""))
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got := f.requests()[0].Header.Get("User-Agent"); got != "terraform-provider-ionhour/0.1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestNewClientRequiresAPIKey(t *testing.T) {
	clearEnv(t)
	_, err := NewClient(WithAPIKey(""))
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("err = %v, want ErrMissingAPIKey", err)
	}
}

func TestAPIKeyFallsBackToEnv(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	clearEnv(t)
	t.Setenv(EnvAPIKey, "ionh_from_env_0123456789")
	c, err := NewClient(WithBaseURL(f.baseURL()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got := f.requests()[0].Header.Get("Authorization"); got != "Bearer ionh_from_env_0123456789" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestExplicitAPIKeyBeatsEnv(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	clearEnv(t)
	t.Setenv(EnvAPIKey, "ionh_from_env_0123456789")
	c, err := NewClient(WithAPIKey(testKey), WithBaseURL(f.baseURL()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got := f.requests()[0].Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestBaseURLPrecedence(t *testing.T) {
	good := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	wrong := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		t.Error("request reached the env base URL although WithBaseURL was set")
		w.WriteHeader(http.StatusTeapot)
	})

	t.Run("default", func(t *testing.T) {
		clearEnv(t)
		c, err := NewClient(WithAPIKey(testKey))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if c.BaseURL() != DefaultBaseURL {
			t.Fatalf("BaseURL = %q, want %q", c.BaseURL(), DefaultBaseURL)
		}
	})
	t.Run("env beats default", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvBaseURL, good.baseURL()+"/")
		c, err := NewClient(WithAPIKey(testKey))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if c.BaseURL() != good.baseURL() {
			t.Fatalf("BaseURL = %q, want trailing slash trimmed %q", c.BaseURL(), good.baseURL())
		}
		if _, err := c.Whoami(context.Background()); err != nil {
			t.Fatalf("Whoami: %v", err)
		}
	})
	t.Run("option beats env", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvBaseURL, wrong.baseURL())
		c, err := NewClient(WithAPIKey(testKey), WithBaseURL(good.baseURL()))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if _, err := c.Whoami(context.Background()); err != nil {
			t.Fatalf("Whoami: %v", err)
		}
	})
	if n := len(good.requests()); n != 2 {
		t.Fatalf("good server requests = %d, want 2", n)
	}
}

func TestInvalidBaseURLIsAnErrorWithoutCredentials(t *testing.T) {
	clearEnv(t)
	for _, raw := range []string{
		"ftp://api.ionhour.com", "api.ionhour.com/api", "https://user:s3cr3t-pass@/api", "://bad",
		"https://user:s3cr3t-pass@api.ionhour.com/api", "https://api.ionhour.com/api?token=s3cr3t-pass", "https://api.ionhour.com/api#frag", "https://api.ionhour.com/api?",
	} {
		_, err := NewClient(WithAPIKey(testKey), WithBaseURL(raw))
		if err == nil {
			t.Fatalf("NewClient(%q) succeeded, want error", raw)
		}
		if strings.Contains(err.Error(), "s3cr3t-pass") || strings.Contains(err.Error(), testKey) {
			t.Fatalf("error leaks a credential: %v", err)
		}
	}
}

// countingTransport records that the caller's transport is the one used.
type countingTransport struct {
	n    int
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return c.next.RoundTrip(r)
}

func TestWithHTTPClientIsUsedButNotMutated(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	ct := &countingTransport{next: http.DefaultTransport}
	user := &http.Client{Transport: ct}
	c, _ := newTestClient(t, f, WithHTTPClient(user), WithHTTPClient(nil))

	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if ct.n != 1 {
		t.Fatalf("custom transport used %d times, want 1", ct.n)
	}
	if user.Transport != ct {
		t.Fatal("caller's *http.Client was mutated")
	}
}

func TestClientFormattingNeverRevealsKey(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {})
	c, _ := newTestClient(t, f)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		for _, v := range []any{c, *c} {
			out := fmt.Sprintf(verb, v)
			if strings.Contains(out, testKey) {
				t.Fatalf("fmt %s leaked the key: %s", verb, out)
			}
		}
	}
	if c.GoString() != c.String() {
		t.Fatalf("GoString = %q", c.GoString())
	}
	if !strings.Contains(c.String(), redactToken(testKey)) {
		t.Fatalf("String() should show the redacted prefix, got %s", c.String())
	}
}

func TestWhoamiErrorIsAPIError(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusUnauthorized, errorBody(http.StatusUnauthorized, "INVALID_KEY", "Invalid API key."))
	})
	c, _ := newTestClient(t, f)
	_, err := c.Whoami(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != "INVALID_KEY" {
		t.Fatalf("err = %#v, want 401 INVALID_KEY", err)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("401 was retried: %d requests", n)
	}
}

func TestWithAPIKeyTrimsAndFallsBackWhenBlank(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	cases := []struct {
		name, option, env, want string
	}{
		{"surrounding whitespace is trimmed", "  " + testKey + "\n", "", testKey},
		{"blank option falls back to env", " \t ", "ionh_from_env_0123456789", "ionh_from_env_0123456789"},
		{"env is trimmed too", "", " ionh_from_env_0123456789\n", "ionh_from_env_0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(EnvAPIKey, tc.env)
			c, err := NewClient(WithAPIKey(tc.option), WithBaseURL(f.baseURL()))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if _, err := c.Whoami(context.Background()); err != nil {
				t.Fatalf("Whoami: %v", err)
			}
			reqs := f.requests()
			if got := reqs[len(reqs)-1].Header.Get("Authorization"); got != "Bearer "+tc.want {
				t.Fatalf("Authorization = %q", got)
			}
		})
	}
	t.Run("blank option and blank env is ErrMissingAPIKey", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvAPIKey, "  ")
		if _, err := NewClient(WithAPIKey("\t")); !errors.Is(err, ErrMissingAPIKey) {
			t.Fatalf("err = %v, want ErrMissingAPIKey", err)
		}
	})
}

func TestAPIKeyWithControlOrSpaceBytesIsRejected(t *testing.T) {
	for _, key := range []string{"ionh_ab cd_secretvalue", "ionh_ab\tcd", "ionh_ab\ncd", "ionh_ab\x00cd", "ionh_ab\x7fcd", "ionh_ab\x1fcd"} {
		clearEnv(t)
		_, err := NewClient(WithAPIKey(key))
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("key %q: err = %v, want ErrInvalidAPIKey", key, err)
		}
		if strings.Contains(err.Error(), "secretvalue") {
			t.Fatalf("error echoes the key: %v", err)
		}
		if strings.ContainsRune(key, 0) {
			continue // the OS cannot hold a NUL in an environment variable
		}
		t.Setenv(EnvAPIKey, key)
		if _, err := NewClient(); !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("env key %q: err = %v, want ErrInvalidAPIKey", key, err)
		}
	}
}

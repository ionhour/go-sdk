package ionhour

import (
	"errors"
	"testing"
)

func TestRegisterJobValidation(t *testing.T) {
	c := NewClient()
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"empty", "", true},
		{"single space", " ", true},
		{"inner space", "abc def", true},
		{"leading space", " abc", true},
		{"trailing space", "abc ", true},
		{"tab", "abc\tdef", true},
		{"newline", "abc\ndef", true},
		{"valid", "ionh_abc-123", false},
		{"valid opaque", "9f2a41a8b8c4", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job, err := c.RegisterJob(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for token %q, got nil", tc.token)
				}
				if job != nil {
					t.Errorf("expected nil job on error, got %+v", job)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for token %q: %v", tc.token, err)
			}
			if job.Token() != tc.token {
				t.Errorf("Token() = %q, want %q", job.Token(), tc.token)
			}
		})
	}
}

func TestRegisterJobEmptyTokenSentinel(t *testing.T) {
	_, err := NewClient().RegisterJob("")
	if !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("error = %v, want ErrEmptyToken", err)
	}
}

func TestRegisterJobWhitespaceErrorType(t *testing.T) {
	_, err := NewClient().RegisterJob("bad token")
	var invalid *InvalidTokenError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidTokenError", err)
	}
}

func TestPackageLevelRegisterJob(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envBaseURL, "")

	// The default client freezes the environment on first use; reset it so this
	// test's env assumptions hold regardless of prior package-level use, and
	// restore a clean slate afterwards so later tests are not poisoned.
	resetDefaultClientForTest()
	t.Cleanup(resetDefaultClientForTest)

	unprotected, err := RegisterJob("tok", "")
	if err != nil {
		t.Fatalf("RegisterJob unprotected: %v", err)
	}
	if got := unprotected.effectiveAPIKey(); got != "" {
		t.Errorf("unprotected key = %q, want empty", got)
	}

	protected, err := RegisterJob("tok", "my-key")
	if err != nil {
		t.Fatalf("RegisterJob protected: %v", err)
	}
	if got := protected.effectiveAPIKey(); got != "my-key" {
		t.Errorf("protected key = %q, want my-key", got)
	}

	if _, err := RegisterJob("", ""); !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("empty token error = %v, want ErrEmptyToken", err)
	}
}

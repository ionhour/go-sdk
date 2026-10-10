package ionhour

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestTransportErrorScrubsTokenFromLogAndResult is the security regression: a
// transport failure produces a net/http *url.Error whose message embeds the
// request URL, and the URL path carries the job token. That message must NOT
// reach the caller's logger or PingResult.Err in full — only a short attribution
// prefix is permitted.
func TestTransportErrorScrubsTokenFromLogAndResult(t *testing.T) {
	t.Setenv(envBaseURL, "")

	// A distinctive token with no URL-reserved characters, so its raw form is
	// exactly how it appears in the request URL path (the real leak vector).
	const token = "distinctive-secret-token-9f2a41a8b8c4-do-not-log"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	// Start then immediately close a server so its address refuses connections,
	// forcing httpClient.Do to fail with a *url.Error (mirrors the connection-
	// refused test). Env is cleared above so no ambient base URL interferes.
	srv := httptest.NewServer(http.HandlerFunc(okHandler))
	closedURL := srv.URL
	srv.Close()

	c := NewClient(WithBaseURL(closedURL), WithLogger(logger))
	c.backoffFn = func(int) time.Duration { return 0 }
	job := mustRegister(t, c, token)

	res, err := job.Success(context.Background())
	if err != nil {
		t.Fatalf("usage error = %v, want nil (transport failure is not a usage error)", err)
	}
	if res.OK {
		t.Fatalf("expected a transport failure, got %+v", res)
	}
	if res.Err == nil {
		t.Fatal("expected a transport error in PingResult.Err")
	}

	logOut := buf.String()
	resErr := res.Err.Error()
	escaped := url.PathEscape(token) // == token here (no reserved chars), asserted anyway

	for _, probe := range []struct {
		name string
		text string
	}{
		{"log line", logOut},
		{"PingResult.Err", resErr},
	} {
		if strings.Contains(probe.text, token) {
			t.Errorf("%s leaked the raw job token: %q", probe.name, probe.text)
		}
		if escaped != token && strings.Contains(probe.text, escaped) {
			t.Errorf("%s leaked the URL-encoded job token: %q", probe.name, probe.text)
		}
	}

	// The context survives redaction: the log still records the failure and its
	// kind, and the error still describes an ionhour ping failure.
	if !strings.Contains(logOut, "level=ERROR") {
		t.Errorf("expected an error-level log line, got: %q", logOut)
	}
	if !strings.Contains(logOut, "heartbeat ping failed") {
		t.Errorf("expected the failure context in the log, got: %q", logOut)
	}
	if !strings.Contains(resErr, "ionhour: ping request failed") {
		t.Errorf("expected the ionhour error context in PingResult.Err, got: %q", resErr)
	}

	// The underlying error stays wrapped so errors.Is/As still work — the message
	// is scrubbed, the chain is not severed.
	var urlErr *url.Error
	if !errors.As(res.Err, &urlErr) {
		t.Error("expected the underlying *url.Error to remain reachable via errors.As")
	}
}

// TestScrubSecretsRedactsRawAndEncodedToken exercises the URL-encoded branch:
// a token with reserved characters appears path-escaped in a *url.Error, so both
// the raw and escaped forms must be scrubbed.
func TestScrubSecretsRedactsRawAndEncodedToken(t *testing.T) {
	const token = "tok#with/reserved?chars-and-more-length"
	escaped := url.PathEscape(token)
	if escaped == token {
		t.Fatal("test setup: token must contain reserved chars so its escaped form differs")
	}
	msg := fmt.Sprintf(`Get "https://signal.ionhour.com/api/signals/ping/%s?rid=r1": dial tcp`, escaped)

	got := scrubSecrets(msg, token)

	if strings.Contains(got, token) {
		t.Errorf("raw token leaked: %q", got)
	}
	if strings.Contains(got, escaped) {
		t.Errorf("URL-encoded token leaked: %q", got)
	}
	// Non-secret context is preserved so the message is still useful.
	if !strings.Contains(got, "dial tcp") {
		t.Errorf("expected the surrounding error context to survive: %q", got)
	}
}

func TestRedactTokenBoundsPrefix(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		want   string // the prefix portion, before the ellipsis
	}{
		{"long token caps at 8", "abcdefghijklmnopqrstuvwxyz", "abcdefgh"},
		{"short token halves", "abcdef", "abc"},
		{"tiny token", "ab", "a"},
		{"single char yields no prefix", "x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactToken(tc.secret)
			if !strings.HasPrefix(got, tc.want) || !strings.HasSuffix(got, "…") {
				t.Errorf("redactToken(%q) = %q, want prefix %q + ellipsis", tc.secret, got, tc.want)
			}
			// The full secret must never survive redaction (length > prefix).
			if len([]rune(tc.secret)) > 1 && strings.Contains(got, tc.secret) {
				t.Errorf("redactToken(%q) = %q leaked the full secret", tc.secret, got)
			}
			// The retained prefix is never more than 8 runes nor more than half.
			prefix := strings.TrimSuffix(got, "…")
			if pr := len([]rune(prefix)); pr > 8 || pr > len([]rune(tc.secret))/2 {
				t.Errorf("redactToken(%q) prefix %q is too long (%d runes)", tc.secret, prefix, pr)
			}
		})
	}
}

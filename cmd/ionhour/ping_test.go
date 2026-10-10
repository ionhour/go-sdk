package main

import (
	"net/http"
	"strings"
	"testing"

	ionhour "github.com/ionhour/go-sdk"
)

func TestPingSendsSuccessGet(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("ping", "--base-url", srv.URL, "tok")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (a bare ping has no payload)", got.method)
	}
	if got.path != "/signals/ping/tok" {
		t.Errorf("path = %s, want /signals/ping/tok", got.path)
	}
}

func TestFailSendsFailGet(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("fail", "--base-url", srv.URL, "tok")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.method)
	}
	if got.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail", got.path)
	}
}

func TestPingWithRid(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, _ := runCLI("ping", "--base-url", srv.URL, "--rid", "run-9", "tok")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if rid := rec.last(t).query.Get("rid"); rid != "run-9" {
		t.Errorf("rid = %q, want run-9", rid)
	}
}

func TestPingTransportFailureExitsNonZero(t *testing.T) {
	srv, _ := newRecordingServer(t, statusHandler(http.StatusBadRequest))

	code, _, stderr := runCLI("ping", "--base-url", srv.URL, "tok")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (bare ping reports delivery failure)", code)
	}
	if !strings.Contains(stderr, "ionhour:") {
		t.Errorf("stderr = %q, want a delivery warning", stderr)
	}
}

func TestPingMissingTokenIsUsageError(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("ping", "--base-url", srv.URL)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (missing token)", code)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr = %q, want a mention of the missing token", stderr)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0", rec.count())
	}
}

// --- credentials: the job token is the only one ---

// Protected jobs are not enforced yet (spec 027). A stray IONHOUR_API_KEY in
// the shell must never turn into an Authorization header on any ping kind.
func TestPingsNeverSendAuthHeader(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	t.Setenv("IONHOUR_API_KEY", "ionh_stray_env_key")

	for _, cmd := range []string{"ping", "fail", "start"} {
		if code, _, stderr := runCLI(cmd, "--base-url", srv.URL, "tok"); code != 0 {
			t.Fatalf("%s exit code = %d, want 0; stderr=%q", cmd, code, stderr)
		}
	}

	if rec.count() != 3 {
		t.Fatalf("recorded %d requests, want 3", rec.count())
	}
	for i, got := range rec.all() {
		if auth := got.header.Get("Authorization"); auth != "" {
			t.Errorf("request %d Authorization = %q, want none", i, auth)
		}
	}
}

func TestBaseUrlFromEnv(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	t.Setenv("IONHOUR_BASE_URL", srv.URL)

	code, _, stderr := runCLI("ping", "tok") // no --base-url flag

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if rec.count() != 1 {
		t.Errorf("recorded %d requests, want 1 (env base URL should be used)", rec.count())
	}
}

// --- secret scrubbing ---

func TestScrubSecretsRedactsTokenInUrl(t *testing.T) {
	// A net/http transport error embeds the ping URL, which carries the token.
	msg := `ionhour: ping request failed: Get "http://127.0.0.1:9/api/signals/ping/secret-token-123?duration=5": dial tcp: connection refused`

	got := scrubSecrets(msg, "secret-token-123")

	if strings.Contains(got, "secret-token-123") {
		t.Errorf("scrubbed message still contains the token: %q", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Errorf("scrubbed message has no redaction marker: %q", got)
	}
}

func TestScrubSecretsEmptyTokenIsNoOp(t *testing.T) {
	msg := "a generic error with no secrets"

	if got := scrubSecrets(msg, ""); got != msg {
		t.Errorf("scrubSecrets with empty secrets = %q, want the message unchanged", got)
	}
}

// --- dispatch: version, help, unknown ---

func TestVersionPrintsSdkVersion(t *testing.T) {
	code, stdout, _ := runCLI("version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, ionhour.Version) {
		t.Errorf("stdout = %q, want it to contain version %q", stdout, ionhour.Version)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := runCLI("bogus")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q, want an unknown-command error", stderr)
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	code, _, stderr := runCLI()

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want usage text", stderr)
	}
}

func TestHelpPrintsUsageToStdout(t *testing.T) {
	code, stdout, _ := runCLI("help")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout = %q, want usage text", stdout)
	}
}

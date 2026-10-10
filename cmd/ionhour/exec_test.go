package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// execExitCode reads the exit_code query parameter from a recorded ping. The
// contract (sdks/CONTRACT.md) carries the exit status as `exit_code=` in the
// query string, never in a JSON body — a body would turn the ping into a POST
// and count against the 10 KiB payload cap for no benefit.
func execExitCode(t *testing.T, got capturedRequest) int {
	t.Helper()
	if len(got.body) != 0 {
		t.Fatalf("exec ping must not carry a body (exit_code is a query param), got %q", got.body)
	}
	raw := got.query.Get("exit_code")
	if raw == "" {
		t.Fatalf("ping has no exit_code query parameter: %v", got.query)
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("exit_code %q is not an integer: %v", raw, err)
	}
	return code
}

func TestExecSuccessPingsSuccessWithDurationAndExitCodeZero(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (command succeeded); stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (exit_code travels in the query string, not a body)", got.method)
	}
	if got.path != "/signals/ping/tok" {
		t.Errorf("path = %s, want /signals/ping/tok (success has no /fail)", got.path)
	}
	if ec := execExitCode(t, got); ec != 0 {
		t.Errorf("exit_code query param = %d, want 0", ec)
	}
	dur := got.query.Get("duration")
	if dur == "" {
		t.Error("expected a duration query parameter")
	} else if _, err := strconv.Atoi(dur); err != nil {
		t.Errorf("duration %q is not an integer: %v", dur, err)
	}
}

func TestExecFailurePingsFailWithExitCode(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "exit 7")

	if code != 7 {
		t.Fatalf("exit code = %d, want 7 (command's own exit passthrough); stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (no payload on exec pings)", got.method)
	}
	if got.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail", got.path)
	}
	if ec := execExitCode(t, got); ec != 7 {
		t.Errorf("exit_code query param = %d, want 7", ec)
	}
	if got.query.Get("duration") == "" {
		t.Error("expected a duration query parameter on the fail ping")
	}
}

func TestExecSignaledCommandReports128PlusSignal(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	// The child sends SIGTERM (15) to itself; shell convention is exit 128+15.
	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "kill -TERM $$")

	if code != 143 {
		t.Fatalf("exit code = %d, want 143 (128 + SIGTERM); stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail (signaled command is a failure)", got.path)
	}
	if ec := execExitCode(t, got); ec != 143 {
		t.Errorf("exit_code query param = %d, want 143", ec)
	}
}

func TestExecCommandNotFoundReports127AndPingsFail(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "ionhour-no-such-command-xyz")

	if code != 127 {
		t.Fatalf("exit code = %d, want 127 (command not found)", code)
	}
	if !strings.Contains(stderr, "ionhour:") {
		t.Errorf("stderr = %q, want an ionhour-prefixed error explaining the failure", stderr)
	}
	got := rec.last(t)
	if got.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail (a command that can't start is a failure)", got.path)
	}
	if ec := execExitCode(t, got); ec != 127 {
		t.Errorf("exit_code query param = %d, want 127", ec)
	}
}

// The --api-key flag was removed with the unenforced protected-jobs option. An
// old crontab still passing it must fail loudly as a usage error, before the
// command runs or any ping is sent, and the message must name the flag.
func TestExecRejectsRemovedApiKeyFlag(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "--api-key", "k", "tok", "--", "true")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0", rec.count())
	}
	if !strings.Contains(stderr, "api-key") {
		t.Errorf("stderr should name the rejected flag: %q", stderr)
	}
}

func TestExecMissingSeparator(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "/bin/sh", "-c", "exit 0")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "--") {
		t.Errorf("stderr = %q, want a mention of the required -- separator", stderr)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0 (nothing should ping on a usage error)", rec.count())
	}
}

func TestExecMissingCommandAfterSeparator(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "command") {
		t.Errorf("stderr = %q, want a mention of the missing command", stderr)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0", rec.count())
	}
}

func TestExecMissingToken(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, _ := runCLI("exec", "--base-url", srv.URL, "--", "/bin/sh", "-c", "exit 0")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (missing token is a usage error)", code)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0", rec.count())
	}
}

func TestExecEmptyTokenRejected(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "", "--", "/bin/sh", "-c", "exit 0")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (empty token is a usage error)", code)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr = %q, want a token validation error", stderr)
	}
	if rec.count() != 0 {
		t.Errorf("recorded %d requests, want 0", rec.count())
	}
}

func TestExecPingFailureDoesNotAffectExitCode(t *testing.T) {
	// A 400 is non-retryable, so this exercises the ping-failure path without
	// incurring the SDK's real retry backoff. It stands in for any undeliverable
	// ping: the command still ran, its exit code survives, and a warning is shown.
	srv, _ := newRecordingServer(t, statusHandler(http.StatusBadRequest))

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "exit 5")

	if code != 5 {
		t.Fatalf("exit code = %d, want 5 (command's exit must survive a failed ping)", code)
	}
	if !strings.Contains(stderr, "ionhour:") {
		t.Errorf("stderr = %q, want an ionhour-prefixed ping warning", stderr)
	}
}

func TestExecQuietSuppressesPingWarning(t *testing.T) {
	srv, _ := newRecordingServer(t, statusHandler(http.StatusBadRequest))

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "--quiet", "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty output under --quiet", stderr)
	}
}

func TestExecRidPropagatedToPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, _ := runCLI("exec", "--base-url", srv.URL, "--rid", "run-42", "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if rid := rec.last(t).query.Get("rid"); rid != "run-42" {
		t.Errorf("rid = %q, want run-42", rid)
	}
}

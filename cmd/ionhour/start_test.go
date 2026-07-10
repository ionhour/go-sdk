package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestStartSubcommandSendsStartPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("start", "--base-url", srv.URL, "tok")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (a bare start ping has no payload)", got.method)
	}
	if got.path != "/signals/ping/tok/start" {
		t.Errorf("path = %s, want /signals/ping/tok/start", got.path)
	}
}

func TestStartSubcommandTransportFailureExitsNonZero(t *testing.T) {
	srv, _ := newRecordingServer(t, statusHandler(http.StatusBadRequest))

	code, _, stderr := runCLI("start", "--base-url", srv.URL, "tok")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (undelivered start ping)", code)
	}
	if !strings.Contains(stderr, "start ping was not delivered") {
		t.Errorf("stderr = %q, want a start-ping delivery warning", stderr)
	}
}

func TestExecSendsStartPingSharingRid(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2 (start + terminal)", len(reqs))
	}
	if reqs[0].path != "/signals/ping/tok/start" {
		t.Errorf("first request path = %s, want the start ping", reqs[0].path)
	}
	if reqs[1].path != "/signals/ping/tok" {
		t.Errorf("second request path = %s, want the terminal success ping", reqs[1].path)
	}
	rid := reqs[0].query.Get("rid")
	if rid == "" {
		t.Error("exec start ping is missing an auto-generated rid")
	}
	if reqs[1].query.Get("rid") != rid {
		t.Errorf("terminal rid = %q, want the start ping's rid %q", reqs[1].query.Get("rid"), rid)
	}
}

func TestExecExplicitRidSharedAcrossStartAndTerminal(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, _ := runCLI("exec", "--base-url", srv.URL, "--rid", "run-77", "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		if got := r.query.Get("rid"); got != "run-77" {
			t.Errorf("%s rid = %q, want run-77 (explicit --rid shared)", r.path, got)
		}
	}
}

func TestExecNoStartSkipsStartPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "--no-start", "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1 (start ping suppressed by --no-start)", len(reqs))
	}
	if reqs[0].path != "/signals/ping/tok" {
		t.Errorf("path = %s, want only the terminal success ping", reqs[0].path)
	}
}

func TestExecStartPingFailureDoesNotAffectExitCode(t *testing.T) {
	// Only the /start ping fails (400, non-retryable so there is no backoff); the
	// command and its terminal ping are unaffected.
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		okHandler(w, r)
	})

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "tok", "--", "/bin/sh", "-c", "exit 5")

	if code != 5 {
		t.Fatalf("exit code = %d, want 5 (the command's exit survives a failed start ping)", code)
	}
	if !strings.Contains(stderr, "start ping was not delivered") {
		t.Errorf("stderr = %q, want a start-ping warning", stderr)
	}
}

func TestExecNoStartUnderQuietStaysSilent(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)

	code, _, stderr := runCLI("exec", "--base-url", srv.URL, "--no-start", "--quiet", "tok", "--", "/bin/sh", "-c", "exit 0")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if rec.count() != 1 {
		t.Errorf("recorded %d requests, want 1 (terminal only)", rec.count())
	}
}

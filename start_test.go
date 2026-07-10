package ionhour

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestStartSendsStartPath(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok-123")

	res, err := job.Start(context.Background())
	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (start has no payload)", got.method)
	}
	if got.path != "/signals/ping/tok-123/start" {
		t.Errorf("path = %s, want /signals/ping/tok-123/start", got.path)
	}
}

func TestStartWithPayloadPostsToStartPath(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Start(context.Background(), WithPayload(map[string]any{"trigger": "cron"}))
	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	got := rec.last(t)
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.path != "/signals/ping/tok/start" {
		t.Errorf("path = %s, want /signals/ping/tok/start", got.path)
	}
}

func TestRunSendsStartPingBeforeTerminal(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	if err := job.Run(context.Background(), func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2 (start + terminal)", len(reqs))
	}
	start, terminal := reqs[0], reqs[1]
	if start.path != "/signals/ping/tok/start" {
		t.Errorf("first request path = %s, want the start ping", start.path)
	}
	if terminal.path != "/signals/ping/tok" {
		t.Errorf("second request path = %s, want the terminal success ping", terminal.path)
	}
	// The start ping correlates with the terminal via a shared, auto-generated rid
	// and carries no duration (the run has not finished when it is sent).
	rid := start.query.Get("rid")
	if rid == "" {
		t.Error("start ping is missing an auto-generated rid")
	}
	if terminal.query.Get("rid") != rid {
		t.Errorf("terminal rid = %q, want the start ping's rid %q", terminal.query.Get("rid"), rid)
	}
	if start.query.Has("duration") {
		t.Error("start ping must not carry a duration")
	}
	if terminal.query.Get("duration") == "" {
		t.Error("terminal ping should carry the measured duration")
	}
}

func TestRunPreservesCallerRIDAcrossStartAndTerminal(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	err := job.Run(context.Background(), func(_ context.Context) error { return nil }, WithRID("my-run-id"))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		if got := r.query.Get("rid"); got != "my-run-id" {
			t.Errorf("%s rid = %q, want the caller-supplied my-run-id", r.path, got)
		}
	}
}

func TestRunWithoutStartPingSkipsStart(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	err := job.Run(context.Background(), func(_ context.Context) error { return nil }, WithoutStartPing())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1 (terminal only, start suppressed)", len(reqs))
	}
	if reqs[0].path != "/signals/ping/tok" {
		t.Errorf("path = %s, want only the terminal success ping", reqs[0].path)
	}
}

func TestRunStartPingCarriesHostEnvOnly(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	err := job.Run(context.Background(), func(_ context.Context) error { return nil },
		WithHost("web-1"), WithEnv("prod"), WithPayload(map[string]any{"k": "v"}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	start := reqs[0]
	if start.method != http.MethodGet {
		t.Errorf("start method = %s, want GET (the start ping never carries a payload body)", start.method)
	}
	if start.query.Get("host") != "web-1" {
		t.Errorf("start host = %q, want web-1", start.query.Get("host"))
	}
	if start.query.Get("env") != "prod" {
		t.Errorf("start env = %q, want prod", start.query.Get("env"))
	}
	if len(start.body) != 0 {
		t.Errorf("start ping must not carry the payload body, got %q", start.body)
	}
	if terminal := reqs[1]; terminal.method != http.MethodPost {
		t.Errorf("terminal method = %s, want POST (it carries the caller's payload)", terminal.method)
	}
}

func TestRunStartPingFailureDoesNotAffectResult(t *testing.T) {
	// Only the /start ping fails (500); the terminal ping succeeds. Run must still
	// run fn and return nil — a start-ping problem can never affect the result.
	srv, rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		okHandler(w, r)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	ran := false
	err := job.Run(context.Background(), func(_ context.Context) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned %v, want nil (a failed start ping must not affect the result)", err)
	}
	if !ran {
		t.Error("fn did not run")
	}
	if got := rec.last(t); got.path != "/signals/ping/tok" {
		t.Errorf("terminal path = %s, want the success ping", got.path)
	}
}

func TestRunValueSendsStartPingWithSharedRID(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	got, err := RunValue(context.Background(), job, func(_ context.Context) (int, error) {
		return 9, nil
	})
	if err != nil {
		t.Fatalf("RunValue returned error: %v", err)
	}
	if got != 9 {
		t.Errorf("value = %d, want 9", got)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2 (start + terminal)", len(reqs))
	}
	if reqs[0].path != "/signals/ping/tok/start" {
		t.Errorf("first request = %s, want the start ping", reqs[0].path)
	}
	rid := reqs[0].query.Get("rid")
	if rid == "" || reqs[1].query.Get("rid") != rid {
		t.Errorf("start rid %q and terminal rid %q must be non-empty and match", rid, reqs[1].query.Get("rid"))
	}
}

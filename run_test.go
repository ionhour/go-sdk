package ionhour

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestRunSuccessSendsDurationPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	err := job.Run(context.Background(), func(_ context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := rec.last(t)
	if got.method != http.MethodGet {
		t.Errorf("method = %s, want GET (success has no payload)", got.method)
	}
	if got.path != "/signals/ping/tok" {
		t.Errorf("path = %s, want /signals/ping/tok (success, no /fail)", got.path)
	}
	durStr := got.query.Get("duration")
	if durStr == "" {
		t.Fatal("expected a duration query param")
	}
	ms, convErr := strconv.Atoi(durStr)
	if convErr != nil {
		t.Fatalf("duration %q is not an integer: %v", durStr, convErr)
	}
	if ms < 1 {
		t.Errorf("duration = %d ms, want >= 1 (fn slept 10ms)", ms)
	}
}

func TestRunErrorSendsFailPingAndRethrows(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	sentinel := errors.New("boom")
	err := job.Run(context.Background(), func(_ context.Context) error {
		return sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("Run returned %v, want the original sentinel error", err)
	}
	got := rec.last(t)
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST (fail carries an error payload)", got.method)
	}
	if got.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail", got.path)
	}
	var body map[string]any
	if jerr := json.Unmarshal(got.body, &body); jerr != nil {
		t.Fatalf("fail body is not JSON: %v (%q)", jerr, got.body)
	}
	if body["error"] != "boom" {
		t.Errorf("body[error] = %v, want boom", body["error"])
	}
	if got.query.Get("duration") == "" {
		t.Error("expected a duration query param on the fail ping")
	}
}

func TestRunErrorMergesUserPayload(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	err := job.Run(context.Background(), func(_ context.Context) error {
		return errors.New("disk full")
	}, WithPayload(map[string]any{"stage": "deploy", "host": "web-1"}))

	if err == nil {
		t.Fatal("expected the original error to be returned")
	}
	var body map[string]any
	if jerr := json.Unmarshal(rec.last(t).body, &body); jerr != nil {
		t.Fatalf("fail body is not JSON: %v", jerr)
	}
	if body["error"] != "disk full" {
		t.Errorf("body[error] = %v, want disk full", body["error"])
	}
	if body["stage"] != "deploy" {
		t.Errorf("body[stage] = %v, want deploy (user payload must be preserved)", body["stage"])
	}
	if body["host"] != "web-1" {
		t.Errorf("body[host] = %v, want web-1", body["host"])
	}
}

func TestRunErrorNestsNonMapPayload(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	job.Run(context.Background(), func(_ context.Context) error {
		return errors.New("nope")
	}, WithPayload("raw-string-context"))

	var body map[string]any
	if jerr := json.Unmarshal(rec.last(t).body, &body); jerr != nil {
		t.Fatalf("fail body is not JSON: %v", jerr)
	}
	if body["error"] != "nope" {
		t.Errorf("body[error] = %v, want nope", body["error"])
	}
	if body["payload"] != "raw-string-context" {
		t.Errorf("body[payload] = %v, want raw-string-context", body["payload"])
	}
}

func TestRunSwallowsPingTransportError(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	// The success ping will fail with 500 on every attempt, but Run must still
	// return nil because fn itself succeeded.
	err := job.Run(context.Background(), func(_ context.Context) error {
		return nil
	})

	if err != nil {
		t.Fatalf("Run returned %v, want nil (ping transport errors are swallowed)", err)
	}
}

func TestRunNilFunctionReturnsError(t *testing.T) {
	job := mustRegister(t, NewClient(), "tok")

	err := job.Run(context.Background(), nil)

	if err == nil {
		t.Fatal("expected an error for a nil function")
	}
}

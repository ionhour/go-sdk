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

func TestRunValueSuccessReturnsValueAndDurationPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	got, err := RunValue(context.Background(), job, func(_ context.Context) (int, error) {
		time.Sleep(10 * time.Millisecond)
		return 42, nil
	})

	if err != nil {
		t.Fatalf("RunValue returned error: %v", err)
	}
	if got != 42 {
		t.Errorf("value = %d, want 42 (fn's own result)", got)
	}
	req := rec.last(t)
	if req.method != http.MethodGet {
		t.Errorf("method = %s, want GET (success has no payload)", req.method)
	}
	if req.path != "/signals/ping/tok" {
		t.Errorf("path = %s, want /signals/ping/tok (success, no /fail)", req.path)
	}
	durStr := req.query.Get("duration")
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

func TestRunValueErrorReturnsZeroAndFailPing(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	sentinel := errors.New("boom")
	// fn returns a non-zero value alongside the error; RunValue must discard it
	// and hand back the zero value so the error path is unambiguous.
	got, err := RunValue(context.Background(), job, func(_ context.Context) (string, error) {
		return "partial", sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("RunValue returned %v, want the original sentinel error", err)
	}
	if got != "" {
		t.Errorf("value = %q, want the zero value of T on error", got)
	}
	req := rec.last(t)
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST (fail carries an error payload)", req.method)
	}
	if req.path != "/signals/ping/tok/fail" {
		t.Errorf("path = %s, want /signals/ping/tok/fail", req.path)
	}
	var body map[string]any
	if jerr := json.Unmarshal(req.body, &body); jerr != nil {
		t.Fatalf("fail body is not JSON: %v (%q)", jerr, req.body)
	}
	if body["error"] != "boom" {
		t.Errorf("body[error] = %v, want boom", body["error"])
	}
	if req.query.Get("duration") == "" {
		t.Error("expected a duration query param on the fail ping")
	}
}

func TestRunValueSwallowsPingTransportError(t *testing.T) {
	srv, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	// The success ping fails with 500 on every attempt, but RunValue must still
	// return fn's value and a nil error because fn itself succeeded.
	got, err := RunValue(context.Background(), job, func(_ context.Context) (int, error) {
		return 7, nil
	})

	if err != nil {
		t.Fatalf("RunValue returned %v, want nil (ping transport errors are swallowed)", err)
	}
	if got != 7 {
		t.Errorf("value = %d, want 7 (fn's result must survive a ping failure)", got)
	}
}

func TestRunValueNilFunctionReturnsError(t *testing.T) {
	job := mustRegister(t, NewClient(), "tok")

	got, err := RunValue[int](context.Background(), job, nil)

	if err == nil {
		t.Fatal("expected an error for a nil function")
	}
	if got != 0 {
		t.Errorf("value = %d, want the zero value on a usage error", got)
	}
}

func TestRunValueNilJobReturnsError(t *testing.T) {
	called := false
	got, err := RunValue(context.Background(), nil, func(context.Context) (int, error) {
		called = true
		return 5, nil
	})

	if !errors.Is(err, errRunNilJob) {
		t.Fatalf("err = %v, want errRunNilJob (usage error, not a panic)", err)
	}
	if got != 0 {
		t.Errorf("value = %d, want the zero value on a usage error", got)
	}
	if called {
		t.Error("fn must not run when the job is nil (a usage error fails before any work)")
	}
}

func TestRunNilReceiverReturnsError(t *testing.T) {
	var job *Job // nil receiver

	// Calling a pointer-receiver method on a nil pointer is legal in Go; Run must
	// return the usage error rather than dereference the nil job and panic.
	err := job.Run(context.Background(), func(context.Context) error { return nil })

	if !errors.Is(err, errRunNilJob) {
		t.Fatalf("err = %v, want errRunNilJob (nil-receiver Run must not panic)", err)
	}
}

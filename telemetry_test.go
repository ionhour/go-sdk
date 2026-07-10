package ionhour

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestHostEnvExitCodeQueryParams(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	res, err := job.Success(context.Background(),
		WithHost("web-1.example.internal"),
		WithEnv("production"),
		WithExitCode(3),
	)
	if err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
	q := rec.last(t).query
	if got := q.Get("host"); got != "web-1.example.internal" {
		t.Errorf("host = %q, want web-1.example.internal", got)
	}
	if got := q.Get("env"); got != "production" {
		t.Errorf("env = %q, want production", got)
	}
	if got := q.Get("exit_code"); got != "3" {
		t.Errorf("exit_code = %q, want 3", got)
	}
}

func TestHostTruncatedTo253(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	if _, err := job.Success(context.Background(), WithHost(strings.Repeat("h", 300))); err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if got := rec.last(t).query.Get("host"); len(got) != 253 {
		t.Errorf("host length = %d, want 253 (silently truncated)", len(got))
	}
}

func TestEnvTruncatedTo32(t *testing.T) {
	srv, rec := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	if _, err := job.Success(context.Background(), WithEnv(strings.Repeat("e", 40))); err != nil {
		t.Fatalf("unexpected usage error: %v", err)
	}
	if got := rec.last(t).query.Get("env"); len(got) != 32 {
		t.Errorf("env length = %d, want 32 (silently truncated)", len(got))
	}
}

// Truncation is a silent, never-fail operation — an over-long host must not
// surface as the loud usage-error channel.
func TestOverlongHostIsNotAUsageError(t *testing.T) {
	srv, _ := newRecordingServer(t, okHandler)
	job := mustRegister(t, testClient(t, srv.URL), "tok")

	if _, err := job.Success(context.Background(), WithHost(strings.Repeat("h", 999))); err != nil {
		t.Fatalf("over-long host returned a usage error %v, want nil (truncation is silent)", err)
	}
}

func TestExitCodeOutOfRangeIsUsageError(t *testing.T) {
	for _, code := range []int{-1, 256, 1000} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			srv, rec := newRecordingServer(t, okHandler)
			job := mustRegister(t, testClient(t, srv.URL), "tok")

			res, err := job.Success(context.Background(), WithExitCode(code))

			var rangeErr *ExitCodeRangeError
			if !errors.As(err, &rangeErr) {
				t.Fatalf("err = %v, want *ExitCodeRangeError", err)
			}
			if rangeErr.Code != code {
				t.Errorf("ExitCodeRangeError.Code = %d, want %d", rangeErr.Code, code)
			}
			if res.OK {
				t.Errorf("expected res.OK false, got %+v", res)
			}
			if !errors.As(res.Err, &rangeErr) {
				t.Errorf("res.Err = %v, want *ExitCodeRangeError (mirrored)", res.Err)
			}
			if rec.count() != 0 {
				t.Errorf("no request should be sent for an out-of-range exit code, got %d", rec.count())
			}
		})
	}
}

func TestExitCodeBoundaryValuesAccepted(t *testing.T) {
	for _, code := range []int{0, 255} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			srv, rec := newRecordingServer(t, okHandler)
			job := mustRegister(t, testClient(t, srv.URL), "tok")

			res, err := job.Success(context.Background(), WithExitCode(code))
			if err != nil {
				t.Fatalf("unexpected usage error for exit code %d: %v", code, err)
			}
			if !res.OK {
				t.Fatalf("expected OK, got %+v", res)
			}
			if got := rec.last(t).query.Get("exit_code"); got != strconv.Itoa(code) {
				t.Errorf("exit_code = %q, want %d", got, code)
			}
		})
	}
}

func TestNewRIDFormat(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		rid := NewRID()
		if len(rid) != 16 {
			t.Fatalf("NewRID() = %q, want 16 characters, got %d", rid, len(rid))
		}
		for _, r := range rid {
			if !strings.ContainsRune("0123456789abcdef", r) {
				t.Fatalf("NewRID() = %q contains a non-hex character %q", rid, r)
			}
		}
		if seen[rid] {
			t.Fatalf("NewRID() returned a duplicate value %q within 100 calls", rid)
		}
		seen[rid] = true
	}
}

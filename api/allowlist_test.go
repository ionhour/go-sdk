package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// Only POST /v1/checks, /v1/jobs and /v1/projects honour Idempotency-Key.

func TestContextKeyIsIgnoredAndNoRetryOutsideTheAllowlist(t *testing.T) {
	f := newFakeAPI(t, statusSequence(`{}`, 503, 201))
	c, _ := newTestClient(t, f)
	ctx := WithIdempotencyKey(context.Background(), "incident-key-1")
	rsp, err := c.IncidentsCreateWithResponse(ctx, CreateIncidentV1Dto{Title: "Payments down"})
	if err != nil {
		t.Fatalf("IncidentsCreate: %v", err)
	}
	reqs := f.requests()
	if rsp.StatusCode() != 503 || len(reqs) != 1 {
		t.Fatalf("status %d after %d requests, want 503 after exactly 1", rsp.StatusCode(), len(reqs))
	}
	if got := reqs[0].Header.Get("Idempotency-Key"); got != "" {
		t.Fatalf("Idempotency-Key = %q sent to a route that does not honour it", got)
	}
}

func TestActionPostsAreNeverRetriedOn5xx(t *testing.T) {
	cases := []struct {
		name string
		call func(ctx context.Context, c *Client) error
	}{
		{"checks_pause", func(ctx context.Context, c *Client) error {
			_, err := c.ChecksPauseWithResponse(ctx, "101")
			return err
		}},
		{"checks_resume", func(ctx context.Context, c *Client) error {
			_, err := c.ChecksResumeWithResponse(ctx, "101")
			return err
		}},
		{"jobs_pause", func(ctx context.Context, c *Client) error { _, err := c.JobsPauseWithResponse(ctx, "456"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, statusSequence(checkBody, 503, 200))
			c, _ := newTestClient(t, f)
			if err := tc.call(WithIdempotencyKey(context.Background(), "k"), c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if n := len(f.requests()); n != 1 {
				t.Fatalf("requests = %d, want 1", n)
			}
		})
	}
}

func TestIdempotencyKeyEditorOutsideTheAllowlistIsAUsageError(t *testing.T) {
	f := newFakeAPI(t, statusSequence(`{}`, 201))
	c, _ := newTestClient(t, f)
	_, err := c.IncidentsCreateWithResponse(context.Background(), CreateIncidentV1Dto{Title: "x"}, IdempotencyKeyEditor("k-1"))
	if !errors.Is(err, ErrIdempotencyKeyNotSupported) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyNotSupported", err)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requests sent", n)
	}
}

func TestAllowlistedCreatesGetAnAutomaticKeyStableAcrossRetries(t *testing.T) {
	cases := []struct {
		name string
		body string
		call func(ctx context.Context, c *Client) (int, error)
	}{
		{"projects_create", projectBody, func(ctx context.Context, c *Client) (int, error) {
			r, err := c.ProjectsCreateWithResponse(ctx, &ProjectsCreateParams{}, CreateProjectV1Dto{Name: "Payments"})
			return status(r, err), err
		}},
		{"jobs_create", jobBody, func(ctx context.Context, c *Client) (int, error) {
			r, err := c.JobsCreateWithResponse(ctx, nil, CreateJobV1Dto{Name: "Nightly", ProjectId: 7})
			return status(r, err), err
		}},
		{"checks_create", checkBody, func(ctx context.Context, c *Client) (int, error) {
			r, err := c.ChecksCreateWithResponse(ctx, nil, CreateCheckV1Dto{Name: "x", ProjectId: 7, Config: V1HttpCheckConfigDto{Url: "https://e.com"}})
			return status(r, err), err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, statusSequence(tc.body, 503, 201))
			c, _ := newTestClient(t, f)
			got, err := tc.call(context.Background(), c)
			if err != nil || got != 201 {
				t.Fatalf("status %d err %v, want 201", got, err)
			}
			reqs := f.requests()
			if len(reqs) != 2 {
				t.Fatalf("requests = %d, want 2 (auto key makes the create replayable)", len(reqs))
			}
			key := reqs[0].Header.Get("Idempotency-Key")
			if !uuidV4.MatchString(key) {
				t.Fatalf("auto key %q is not a UUIDv4", key)
			}
			if reqs[1].Header.Get("Idempotency-Key") != key {
				t.Fatalf("retry changed the key: %q -> %q", key, reqs[1].Header.Get("Idempotency-Key"))
			}
		})
	}
}

func TestAutomaticKeysDifferPerCall(t *testing.T) {
	f := newFakeAPI(t, statusSequence(projectBody, 201))
	c, _ := newTestClient(t, f)
	for i := 0; i < 2; i++ {
		if _, err := c.ProjectsCreateWithResponse(context.Background(), nil, CreateProjectV1Dto{Name: "P"}); err != nil {
			t.Fatalf("ProjectsCreate: %v", err)
		}
	}
	reqs := f.requests()
	if reqs[0].Header.Get("Idempotency-Key") == reqs[1].Header.Get("Idempotency-Key") {
		t.Fatal("two logical creates shared one automatic key")
	}
}

func TestInvalidParamsIdempotencyKeyIsAUsageError(t *testing.T) {
	f := newFakeAPI(t, statusSequence(projectBody, 201))
	c, _ := newTestClient(t, f)
	_, err := c.ProjectsCreateWithResponse(context.Background(), &ProjectsCreateParams{IdempotencyKey: ptr("has space")}, CreateProjectV1Dto{Name: "P"})
	if !errors.Is(err, ErrInvalidIdempotencyKey) {
		t.Fatalf("err = %v, want ErrInvalidIdempotencyKey", err)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requests sent", n)
	}
}

func TestIsIdempotentCreate(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/api/v1/checks", true},
		{http.MethodPost, "/api/v1/jobs", true},
		{http.MethodPost, "/api/v1/projects", true},
		{http.MethodPost, "/v1/projects", true},
		{http.MethodGet, "/api/v1/checks", false},
		{http.MethodPost, "/api/v1/checks/101/pause", false},
		{http.MethodPost, "/api/v1/incidents", false},
		{http.MethodPost, "/api/v1/deployments", false},
		{http.MethodPost, "/api/v1/maintenance-windows", false},
		{http.MethodPost, "/api/v1/subchecks", false},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, "https://api.example.com"+tc.path, nil)
		if got := isIdempotentCreate(req); got != tc.want {
			t.Fatalf("%s %s = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// A caller's own editor can put the header on any route; replay safety still
// follows the allowlist, never header presence.
func TestRawHeaderOutsideTheAllowlistDoesNotEnableRetries(t *testing.T) {
	f := newFakeAPI(t, statusSequence(`{}`, 503, 201))
	c, _ := newTestClient(t, f)
	rawHeader := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Idempotency-Key", "hand-set-key")
		return nil
	}
	rsp, err := c.IncidentsCreateWithResponse(context.Background(), CreateIncidentV1Dto{Title: "x"}, rawHeader)
	if err != nil {
		t.Fatalf("IncidentsCreate: %v", err)
	}
	if rsp.StatusCode() != 503 || len(f.requests()) != 1 {
		t.Fatalf("status %d after %d requests, want 503 after 1", rsp.StatusCode(), len(f.requests()))
	}
}

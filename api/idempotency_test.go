package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIdempotencyKeyIsUniqueUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		key, err := NewIdempotencyKey()
		if err != nil {
			t.Fatalf("NewIdempotencyKey: %v", err)
		}
		if !uuidV4.MatchString(key) || !validIdempotencyKey(key) {
			t.Fatalf("key %q is not a valid UUIDv4 idempotency key", key)
		}
		if seen[key] {
			t.Fatalf("duplicate key %q", key)
		}
		seen[key] = true
	}
}

func TestIdempotencyKeySources(t *testing.T) {
	cases := []struct {
		name    string
		ctxKey  string
		param   *string
		editors []RequestEditorFn
		want    string
	}{
		{"context", "ctx-key", nil, nil, "ctx-key"},
		{"params beat context", "ctx-key", ptr("param-key"), nil, "param-key"},
		{"editor", "", nil, []RequestEditorFn{IdempotencyKeyEditor("editor-key")}, "editor-key"},
		{"none: automatic UUIDv4", "", nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, statusSequence(jobBody, 201))
			c, _ := newTestClient(t, f)
			ctx := context.Background()
			if tc.ctxKey != "" {
				ctx = WithIdempotencyKey(ctx, tc.ctxKey)
			}
			_, err := c.JobsCreateWithResponse(ctx, &JobsCreateParams{IdempotencyKey: tc.param},
				CreateJobV1Dto{Name: "Nightly", ProjectId: 7}, tc.editors...)
			if err != nil {
				t.Fatalf("JobsCreate: %v", err)
			}
			got := f.requests()[0].Header.Get("Idempotency-Key")
			if tc.want == "" {
				if !uuidV4.MatchString(got) {
					t.Fatalf("Idempotency-Key = %q, want an automatic UUIDv4", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("Idempotency-Key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInvalidIdempotencyKeyFailsBeforeSending(t *testing.T) {
	for _, bad := range []string{"has space", strings.Repeat("k", 129), "tab\tkey", "ünïcode"} {
		f := newFakeAPI(t, statusSequence(jobBody, 201))
		c, _ := newTestClient(t, f)
		_, err := c.JobsCreateWithResponse(WithIdempotencyKey(context.Background(), bad), &JobsCreateParams{},
			CreateJobV1Dto{Name: "Nightly", ProjectId: 7})
		if !errors.Is(err, ErrInvalidIdempotencyKey) {
			t.Fatalf("key %q: err = %v, want ErrInvalidIdempotencyKey", bad, err)
		}
		if n := len(f.requests()); n != 0 {
			t.Fatalf("key %q: %d requests sent", bad, n)
		}
	}
	if !validIdempotencyKey(strings.Repeat("k", 128)) {
		t.Fatal("128-char key must be valid")
	}
}

func TestContextKeyIsNotSentOnReadsOrToLaterCalls(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusCreated, projectBody)
			return
		}
		writeJSON(w, http.StatusOK, whoamiBody)
	})
	c, _ := newTestClient(t, f)
	ctx := WithIdempotencyKey(context.Background(), "only-this-call")
	if _, err := c.Whoami(ctx); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if _, err := c.ProjectsCreateWithResponse(ctx, nil, CreateProjectV1Dto{Name: "P"}); err != nil {
		t.Fatalf("ProjectsCreate: %v", err)
	}
	if _, err := c.ProjectsCreateWithResponse(context.Background(), nil, CreateProjectV1Dto{Name: "P"}); err != nil {
		t.Fatalf("ProjectsCreate: %v", err)
	}
	reqs := f.requests()
	if reqs[0].Header.Get("Idempotency-Key") != "" {
		t.Fatalf("whoami carried %q", reqs[0].Header.Get("Idempotency-Key"))
	}
	if reqs[1].Header.Get("Idempotency-Key") != "only-this-call" {
		t.Fatalf("create carried %q", reqs[1].Header.Get("Idempotency-Key"))
	}
	if k := reqs[2].Header.Get("Idempotency-Key"); k == "only-this-call" || !uuidV4.MatchString(k) {
		t.Fatalf("later create carried %q, want a fresh automatic key", k)
	}
}

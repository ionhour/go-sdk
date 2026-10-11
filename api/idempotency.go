package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	idempotencyHeader    = "Idempotency-Key"
	maxIdempotencyKeyLen = 128
)

// idempotentCreatePaths is the ONE allowlist of operations whose server
// handler honours Idempotency-Key: POST to these paths (relative to the base
// URL). It drives which requests carry a key and which POSTs are replayable.
var idempotentCreatePaths = [...]string{"/v1/checks", "/v1/jobs", "/v1/projects"}

var (
	// ErrInvalidIdempotencyKey is returned (before any request is sent) when an
	// idempotency key is not 1-128 printable ASCII characters without spaces,
	// the server's `^[\x21-\x7E]{1,128}$` rule.
	ErrInvalidIdempotencyKey = errors.New("ionhour api: idempotency key must be 1-128 printable ASCII characters without spaces")

	// ErrIdempotencyKeyNotSupported is returned (before any request is sent)
	// when [IdempotencyKeyEditor] is used on an operation other than
	// checks_create, jobs_create or projects_create.
	ErrIdempotencyKeyNotSupported = errors.New("ionhour api: Idempotency-Key is only honoured by checks_create, jobs_create and projects_create")
)

type idempotencyKeyCtx struct{}

// WithIdempotencyKey returns a context whose key the client sends as
// `Idempotency-Key` on checks_create, jobs_create and projects_create, the
// only operations that honour it. Other operations made with the context
// ignore it. A retry with the same key and body replays the original result
// for 24 hours. An explicit Params.IdempotencyKey takes precedence.
//
// You only need this to reuse one key across your OWN retries of a logical
// create: when no key is given, the client mints one per call (see
// [NewClient]).
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// IdempotencyKeyEditor is the per-call alternative to [WithIdempotencyKey]:
// pass it as a trailing RequestEditorFn to ChecksCreate, JobsCreate or
// ProjectsCreate. On any other operation the call fails with
// [ErrIdempotencyKeyNotSupported] before sending.
func IdempotencyKeyEditor(key string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if !isIdempotentCreate(req) {
			return ErrIdempotencyKeyNotSupported
		}
		return setIdempotencyKey(req, key)
	}
}

// NewIdempotencyKey returns a random UUIDv4 (crypto/rand) for use as an
// idempotency key. It fails only if the system random source does.
func NewIdempotencyKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ionhour api: generate idempotency key: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// isIdempotentCreate reports whether req is one of the allowlisted creates.
func isIdempotentCreate(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	for _, p := range idempotentCreatePaths {
		if strings.HasSuffix(req.URL.Path, p) {
			return true
		}
	}
	return false
}

// applyIdempotencyKey runs in the client's request editor, before the request
// reaches the transport, so the key is fixed for every retry of the call.
// Precedence on an allowlisted create: Params (already on the header, but
// validated here), then the context key, then a freshly minted UUIDv4. Every
// other operation is left untouched.
func applyIdempotencyKey(ctx context.Context, req *http.Request) error {
	if !isIdempotentCreate(req) {
		return nil
	}
	if existing := req.Header.Get(idempotencyHeader); existing != "" {
		if !validIdempotencyKey(existing) {
			return ErrInvalidIdempotencyKey
		}
		return nil
	}
	if key, ok := ctx.Value(idempotencyKeyCtx{}).(string); ok {
		return setIdempotencyKey(req, key)
	}
	key, err := NewIdempotencyKey()
	if err != nil {
		return err
	}
	req.Header.Set(idempotencyHeader, key)
	return nil
}

func setIdempotencyKey(req *http.Request, key string) error {
	if !validIdempotencyKey(key) {
		return ErrInvalidIdempotencyKey
	}
	req.Header.Set(idempotencyHeader, key)
	return nil
}

func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKeyLen {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

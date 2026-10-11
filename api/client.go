package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

var (
	// ErrMissingAPIKey is returned by [NewClient] when neither [WithAPIKey] nor
	// IONHOUR_API_KEY provides a non-blank key.
	ErrMissingAPIKey = errors.New("ionhour api: no API key (use WithAPIKey or set " + EnvAPIKey + ")")

	// ErrInvalidAPIKey is returned by [NewClient] when the (trimmed) key
	// contains a space, a control character or DEL. The key is never echoed.
	ErrInvalidAPIKey = errors.New("ionhour api: API key contains whitespace or control characters")

	// ErrInvalidBaseURL is returned by [NewClient] for a base URL that is not
	// an absolute http(s) URL, or that carries userinfo, a query or a fragment.
	ErrInvalidBaseURL = errors.New("ionhour api: invalid base URL")
)

// Client is the Ionhour Integration API v1 client. It embeds the generated
// [ClientWithResponses], so every operation is available as a method named
// after its operationId (checks_create -> ChecksCreateWithResponse), and adds
// bearer auth, the User-Agent, the contract retry policy and error parsing.
//
// Always construct it with [NewClient]: the zero value has no transport,
// credentials or retry policy and is not usable. A Client is safe for
// concurrent use and immutable after construction.
//
// Printing a Client (any fmt verb) never reveals the API key.
type Client struct {
	*ClientWithResponses

	key       string
	baseURL   string
	userAgent string
}

// NewClient builds a Client. Configuration precedence is option, then
// environment (IONHOUR_API_KEY, IONHOUR_API_BASE_URL), then default. It
// returns an error (never panics) when no API key is available
// ([ErrMissingAPIKey]), the key is malformed ([ErrInvalidAPIKey]) or the base
// URL is unusable ([ErrInvalidBaseURL]).
//
// On checks_create, jobs_create and projects_create the client always sends an
// Idempotency-Key: the caller's (Params, [WithIdempotencyKey] or
// [IdempotencyKeyEditor]) or, when none is given, a fresh UUIDv4 per call. The
// key is fixed before the first attempt, so automatic retries replay it.
func NewClient(opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	key := firstNonEmpty(cfg.apiKey, strings.TrimSpace(os.Getenv(EnvAPIKey)))
	if key == "" {
		return nil, ErrMissingAPIKey
	}
	if !validAPIKey(key) {
		return nil, ErrInvalidAPIKey
	}
	baseURL, err := normalizeBaseURL(firstNonEmpty(cfg.baseURL, strings.TrimSpace(os.Getenv(EnvBaseURL)), DefaultBaseURL))
	if err != nil {
		return nil, err
	}

	c := &Client{key: key, baseURL: baseURL, userAgent: cfg.userAgent}
	raw, err := NewRawClientWithResponses(baseURL,
		WithRawHTTPClient(newHTTPClient(cfg, key)),
		WithRawRequestEditorFn(c.editRequest),
	)
	if err != nil {
		return nil, fmt.Errorf("ionhour api: %s", scrubSecrets(err.Error(), key))
	}
	c.ClientWithResponses = raw
	return c, nil
}

// BaseURL returns the resolved API base URL (without a trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// editRequest runs on every generated request, before any per-call editor and
// before the transport: bearer auth, User-Agent and, on the allowlisted
// creates only, the Idempotency-Key (see applyIdempotencyKey).
func (c *Client) editRequest(ctx context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", c.userAgent)
	return applyIdempotencyKey(ctx, req)
}

// validAPIKey rejects any byte <= 0x20 (space and C0 controls) or 0x7f (DEL):
// such a key can never authenticate and would corrupt the header.
func validAPIKey(key string) bool {
	for i := 0; i < len(key); i++ {
		if key[i] <= 0x20 || key[i] == 0x7f {
			return false
		}
	}
	return true
}

// Whoami validates the credential and returns the workspace, permission level,
// projected role and principal kind it maps to. Non-2xx responses come back as
// [*APIError].
func (c *Client) Whoami(ctx context.Context) (*WhoamiV1Response, error) {
	rsp, err := c.WhoamiWhoamiWithResponse(ctx)
	if err != nil {
		return nil, c.scrubError("whoami", err)
	}
	if err := c.CheckResponse(rsp.HTTPResponse, rsp.Body); err != nil {
		return nil, err
	}
	if rsp.JSON200 != nil {
		return rsp.JSON200, nil
	}
	// The body was not labelled JSON; decode it anyway.
	var me WhoamiV1Response
	if err := json.Unmarshal(rsp.Body, &me); err != nil {
		return nil, c.scrubError("whoami: decode response", err)
	}
	return &me, nil
}

// CheckResponse returns nil for a 2xx response and an [*APIError] otherwise,
// parsed from the v1 envelope. Use it with any generated *XxxResponse:
//
//	rsp, err := c.ChecksGetOneWithResponse(ctx, "slug:public-api")
//	if err != nil { ... }
//	if err := c.CheckResponse(rsp.HTTPResponse, rsp.Body); err != nil { ... }
func (c *Client) CheckResponse(rsp *http.Response, body []byte) error {
	apiErr := parseAPIError(rsp, body)
	if apiErr == nil {
		return nil
	}
	apiErr.Message = scrubSecrets(apiErr.Message, c.key)
	for i, m := range apiErr.Messages {
		apiErr.Messages[i] = scrubSecrets(m, c.key)
	}
	return apiErr
}

func (c *Client) scrubError(op string, err error) error {
	return newScrubbedError(fmt.Errorf("ionhour api: %s: %w", op, err), c.key)
}

// String, GoString and Format keep the API key out of every fmt verb,
// including %+v / %#v, which would otherwise print unexported fields.
func (c Client) String() string {
	return fmt.Sprintf("api.Client{baseURL: %q, userAgent: %q, apiKey: %q}", c.baseURL, c.userAgent, redactToken(c.key))
}

// GoString implements fmt.GoStringer.
func (c Client) GoString() string { return c.String() }

// Format implements fmt.Formatter so no verb falls through to reflection.
func (c Client) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// normalizeBaseURL never echoes the raw value: a mistyped base URL may carry
// a credential in its userinfo or query.
func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return "", fmt.Errorf("%w: want an absolute http(s) URL", ErrInvalidBaseURL)
	case u.User != nil:
		return "", fmt.Errorf("%w: must not contain userinfo (pass the key with WithAPIKey)", ErrInvalidBaseURL)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return "", fmt.Errorf("%w: must not contain a query or fragment", ErrInvalidBaseURL)
	}
	return strings.TrimRight(raw, "/"), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

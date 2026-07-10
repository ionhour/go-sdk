package ionhour

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// SDK identity and default configuration.
const (
	// Version is the SDK version, surfaced in the default User-Agent header.
	Version = "0.1.0"

	// DefaultBaseURL is the Ionhour signals ingestion endpoint. Override it with
	// [WithBaseURL] or the IONHOUR_BASE_URL environment variable.
	DefaultBaseURL = "https://signal.ionhour.com/api"

	// DefaultTimeout is the per-attempt HTTP timeout.
	DefaultTimeout = 10 * time.Second

	// DefaultRetries is the default total number of ping attempts (not the
	// number of additional retries). With the default of 3 the client makes up
	// to three attempts, sleeping 1s then 2s between them.
	DefaultRetries = 3

	defaultUserAgent = "ionhour-go/" + Version

	// maxPayloadBytes mirrors the server's 10 KiB JSON body ceiling.
	maxPayloadBytes = 10 * 1024

	// maxHostLen and maxEnvLen bound the host= and env= telemetry query params (in
	// characters) per the Ionhour SDK contract. Over-long values are truncated
	// client-side rather than rejected — telemetry must never fail a heartbeat.
	maxHostLen = 253
	maxEnvLen  = 32

	envAPIKey  = "IONHOUR_API_KEY"
	envBaseURL = "IONHOUR_BASE_URL"
)

// Client is a configured Ionhour SDK client. It is safe for concurrent use by
// multiple goroutines. Create one with [NewClient] and register jobs against it
// with [Client.RegisterJob].
type Client struct {
	apiKey     string
	baseURL    string
	userAgent  string
	timeout    time.Duration
	retries    int
	httpClient *http.Client
	logger     *slog.Logger

	// backoffFn returns the delay to wait after a failed attempt number
	// (1-based). It is unexported and injectable so tests can eliminate real
	// sleeps; production code always uses defaultBackoff.
	backoffFn func(attempt int) time.Duration
}

// NewClient builds a client from the given options.
//
// Defaults follow the Ionhour SDK contract: base URL https://signal.ionhour.com/api,
// a 10-second per-attempt timeout, three total attempts with 1s/2s backoff, and
// the User-Agent "ionhour-go/0.1.0". When [WithAPIKey] or [WithBaseURL] are not
// supplied, NewClient falls back to the IONHOUR_API_KEY and IONHOUR_BASE_URL
// environment variables respectively.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		userAgent:  defaultUserAgent,
		timeout:    DefaultTimeout,
		retries:    DefaultRetries,
		httpClient: &http.Client{},
		backoffFn:  defaultBackoff,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	// Environment fallbacks apply only when the corresponding option was absent.
	// The key is trimmed so a padded value from a .env file (e.g. a trailing
	// newline) can never produce a malformed "Bearer" header.
	if c.apiKey == "" {
		c.apiKey = strings.TrimSpace(os.Getenv(envAPIKey))
	}
	if c.baseURL == "" {
		if env := os.Getenv(envBaseURL); env != "" {
			c.baseURL = env
		} else {
			c.baseURL = DefaultBaseURL
		}
	}

	// Guard against options that zeroed out required fields.
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	if c.retries < 1 {
		c.retries = 1
	}
	if c.backoffFn == nil {
		c.backoffFn = defaultBackoff
	}

	return c
}

// defaultBackoff implements the contract's clamped retry schedule: it waits 1s
// before the second attempt, then 2s before every subsequent attempt (1s, 2s,
// 2s, 2s, …). The delay never grows linearly or exponentially, so a stalled
// endpoint adds a bounded amount of latency no matter how high WithRetries is
// set. attempt is the 1-based number of the attempt that just failed.
func defaultBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return time.Second
	}
	return 2 * time.Second
}

// String implements fmt.Stringer, redacting the API key so that logging a Client
// with %v or %+v cannot leak the credential. The base URL and other transport
// settings are shown because they are not sensitive.
//
// Note: %#v (Go's verbose GoString syntax) bypasses fmt.Stringer and will print
// the unexported apiKey field — avoid %#v on a Client.
func (c *Client) String() string {
	return fmt.Sprintf(
		"ionhour.Client(baseURL=%s, apiKey=<redacted>, timeout=%s, retries=%d, userAgent=%s)",
		c.baseURL, c.timeout, c.retries, c.userAgent,
	)
}

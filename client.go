package ionhour

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

	envBaseURL = "IONHOUR_BASE_URL"
)

// Client is a configured Ionhour SDK client. It is safe for concurrent use by
// multiple goroutines. Create one with [NewClient] and register jobs against it
// with [Client.RegisterJob].
type Client struct {
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
// the User-Agent "ionhour-go/0.1.0". When [WithBaseURL] is not supplied,
// NewClient falls back to the IONHOUR_BASE_URL environment variable. Pings
// authenticate by job token alone and never send an Authorization header.
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

	// The environment fallback applies only when the option was absent.
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

// String implements fmt.Stringer with the client's transport settings. None of
// them is sensitive: a Client holds no credential (the job token lives on the
// [Job]).
func (c *Client) String() string {
	return fmt.Sprintf(
		"ionhour.Client(baseURL=%s, timeout=%s, retries=%d, userAgent=%s)",
		c.baseURL, c.timeout, c.retries, c.userAgent,
	)
}

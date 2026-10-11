package ionhour

// PingResult is the outcome of a heartbeat ping.
//
// Ping methods ([Job.Success], [Job.Fail], [Job.Ping]) never panic and never
// return a transport error as a Go error the caller must handle. Instead they
// report the result here so that a failure to reach Ionhour can never break the
// host application.
type PingResult struct {
	// OK is true when the server acknowledged the ping with a 2xx response.
	OK bool

	// Status is the HTTP status code of the final attempt. It is 0 when the
	// request never produced an HTTP response — a network error, a per-attempt
	// timeout, context cancellation, or a client-side usage error (such as an
	// oversized payload) detected before any request was sent.
	Status int

	// Err carries the reason the ping did not succeed. It is nil when OK is
	// true. Callers who care about failures can inspect it (for example with
	// errors.As against *PayloadTooLargeError or *HTTPStatusError); callers who
	// treat monitoring as best-effort can ignore it entirely.
	Err error

	// Created is true when a slug-mode ping auto-created the job (HTTP 201 or a
	// "created": true body). Always false for token pings.
	Created bool
}

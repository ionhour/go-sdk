package ionhour

import (
	"errors"
	"fmt"
)

// ErrEmptyToken is returned by [Client.RegisterJob] and [RegisterJob] when the
// supplied job token is empty.
var ErrEmptyToken = errors.New("ionhour: job token must not be empty")

// InvalidTokenError is returned by registration when a job token is malformed
// (for example, it contains whitespace). The token value itself is deliberately
// omitted from the message to avoid leaking a credential into logs.
type InvalidTokenError struct {
	// Reason describes why the token was rejected.
	Reason string
}

func (e *InvalidTokenError) Error() string {
	return "ionhour: invalid job token: " + e.Reason
}

// PayloadTooLargeError is carried in [PingResult.Err] when a ping payload
// exceeds the server's 10 KiB JSON body limit. It is detected client-side
// before any request is sent.
type PayloadTooLargeError struct {
	// Size is the encoded payload size in bytes.
	Size int
	// Max is the maximum allowed payload size in bytes.
	Max int
}

func (e *PayloadTooLargeError) Error() string {
	return fmt.Sprintf("ionhour: payload is %d bytes, which exceeds the %d-byte limit", e.Size, e.Max)
}

// HTTPStatusError is carried in [PingResult.Err] when the server responds with a
// non-2xx status code.
type HTTPStatusError struct {
	// Status is the HTTP status code returned by the server.
	Status int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("ionhour: server responded with HTTP status %d", e.Status)
}

// ConfigError is carried in [PingResult.Err] when the client is misconfigured
// in a way that prevents a request from being built (for example, an empty or
// unparsable base URL).
type ConfigError struct {
	// Reason describes the configuration problem.
	Reason string
}

func (e *ConfigError) Error() string {
	return "ionhour: configuration error: " + e.Reason
}

package ionhour

import (
	"fmt"
	"net/url"
	"strings"
)

// redactToken returns the short, non-secret prefix of a credential that the
// Ionhour SDK contract permits in an automatic log for attribution: at most 8
// runes, and never more than half the value. A full token is replaced with
// this form (plus an ellipsis marking the truncation) in any error string that
// may be logged or exposed via PingResult.Err.
func redactToken(secret string) string {
	runes := []rune(secret)
	n := len(runes) / 2
	if n > 8 {
		n = 8
	}
	return string(runes[:n]) + "…"
}

// scrubSecrets removes the job token from a message that may be logged or
// returned to the caller. net/http's *url.Error embeds the request URL — which
// carries the token in its path — so both the raw token and its
// URL-path-escaped form (how it actually appears in the URL) are replaced with
// the redacted prefix. It is a no-op when there is nothing to replace.
func scrubSecrets(msg, token string) string {
	if token == "" {
		return msg
	}
	redacted := redactToken(token)
	msg = strings.ReplaceAll(msg, token, redacted)
	if escaped := url.PathEscape(token); escaped != token {
		msg = strings.ReplaceAll(msg, escaped, redacted)
	}
	return msg
}

// scrubbedError wraps a transport error so its message can be logged or exposed
// via PingResult.Err without leaking the job token that a net/http
// *url.Error embeds in the request URL. Error returns the scrubbed message;
// Unwrap preserves the original error so errors.Is / errors.As chains (for
// example against context.Canceled or context.DeadlineExceeded) keep working.
//
// A caller that deliberately unwraps and inspects the underlying error — the
// equivalent of a logging hook running in the caller's own code — can still
// reach the full text, exactly as the SDK contract permits for deliberate
// access. The guarantee is only that the error's own Error() output, which is
// what reaches an automatic logger and PingResult.Err.Error(), is scrubbed.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrubbedTransportError wraps err under the given "ionhour: …" context and
// returns a PingResult whose Err has the job's token redacted from
// its message, while keeping the original error reachable for errors.Is/As.
func scrubbedTransportError(context string, err error, job *Job) PingResult {
	wrapped := fmt.Errorf("%s: %w", context, err)
	return PingResult{Err: &scrubbedError{
		msg: scrubSecrets(wrapped.Error(), job.token),
		err: wrapped,
	}}
}

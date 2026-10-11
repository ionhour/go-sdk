package api

import "strings"

// redactToken returns the short, non-secret prefix the SDK contract allows in
// logs: at most 8 runes and never more than half the value, plus an ellipsis.
// Same rule as the heartbeat module.
func redactToken(secret string) string {
	runes := []rune(secret)
	n := len(runes) / 2
	if n > 8 {
		n = 8
	}
	return string(runes[:n]) + "…"
}

// scrubSecrets replaces every occurrence of secret in msg with its redacted
// prefix. No-op for an empty secret.
func scrubSecrets(msg, secret string) string {
	if secret == "" || !strings.Contains(msg, secret) {
		return msg
	}
	return strings.ReplaceAll(msg, secret, redactToken(secret))
}

// scrubbedError carries a message with the credential removed. Unwrap keeps
// errors.Is / errors.As working (context.Canceled, *url.Error, ...); a caller
// that deliberately unwraps can still see the original text, but Error() —
// what reaches loggers — is always scrubbed.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// newScrubbedError returns err unchanged when its text does not contain the
// secret, so ordinary errors keep their concrete type at the top of the chain.
func newScrubbedError(err error, secret string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	clean := scrubSecrets(text, secret)
	if clean == text {
		return err
	}
	return &scrubbedError{msg: clean, err: err}
}

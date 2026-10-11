package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError is a non-2xx response from the Integration API, parsed from the v1
// envelope `{statusCode, error, code, message, detail?}`.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the stable machine-readable code (NOT_FOUND, SLUG_TAKEN,
	// RATE_LIMIT_EXCEEDED_KEY, ...). When the body is not the v1 envelope (for
	// example an HTML 502 from a proxy) it is "HTTP_<status>" and Message is
	// "HTTP <status>", the same convention as the Node client.
	Code string
	// Message is the human-readable message. Validation failures carry a list,
	// joined here with "; " and kept verbatim in Messages.
	Message string
	// Messages is the message list for validation failures; nil otherwise.
	Messages []string
	// Detail is the structured context the server attaches (plan limits,
	// rate-limit scope, ...): the envelope's `detail` when it is a JSON
	// object, otherwise the envelope's extra top-level fields. Nil when absent.
	Detail map[string]any
	// RetryAfter is the parsed Retry-After header (429 / 503); zero when absent.
	RetryAfter time.Duration
}

// Error implements error. It never includes the request URL or headers.
func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ionhour api: %d", e.Status)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	return b.String()
}

// IsRateLimited reports whether err is a 429 [*APIError]. Its RetryAfter field
// says how long to wait.
func IsRateLimited(err error) bool { return hasStatus(err, http.StatusTooManyRequests) }

// IsNotFound reports whether err is a 404 [*APIError] (unknown id or slug,
// soft-deleted resource, or a resource outside the key's workspace).
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is a 409 [*APIError] (SLUG_TAKEN,
// IDEMPOTENCY_KEY_IN_PROGRESS, non-empty project delete, ...).
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsUnauthorized reports whether err is a 401 [*APIError] (missing, invalid,
// expired or inactive credential).
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether err is a 403 [*APIError] (valid credential that
// may not perform the call: read-only key, scope, role, plan limit, ...).
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

func hasStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// envelopeKeys are the v1 envelope's own fields; anything else at the top
// level is structured context (the flat 429 body carries scope, limit, plan).
var envelopeKeys = map[string]bool{"statusCode": true, "error": true, "code": true, "message": true, "detail": true}

// maxRetryAfter clamps a parsed Retry-After so absurd values neither overflow
// time.Duration nor read as 0 (which would retry at once).
const maxRetryAfter = 24 * time.Hour

// parseAPIError returns nil for a nil or 2xx response. Same rules as the Node
// client: the body is the v1 envelope only when `code` is a non-empty string
// and `message` is a string or a string list; anything else (HTML from a
// proxy, an empty body, an envelope without code) becomes Code "HTTP_<status>"
// and Message "HTTP <status>".
func parseAPIError(rsp *http.Response, body []byte) *APIError {
	if rsp == nil || (rsp.StatusCode >= 200 && rsp.StatusCode < 300) {
		return nil
	}
	apiErr := &APIError{
		Status:     rsp.StatusCode,
		RetryAfter: parseRetryAfter(rsp.Header.Get("Retry-After"), time.Now()),
	}
	fields, ok := decodeEnvelope(body)
	if !ok {
		apiErr.Code = fmt.Sprintf("HTTP_%d", rsp.StatusCode)
		apiErr.Message = fmt.Sprintf("HTTP %d", rsp.StatusCode)
		return apiErr
	}
	_ = json.Unmarshal(fields["code"], &apiErr.Code)
	apiErr.Message, apiErr.Messages, _ = decodeMessage(fields["message"])
	apiErr.Detail = decodeDetail(fields)
	return apiErr
}

// decodeEnvelope returns the top-level fields when body is a v1 envelope.
func decodeEnvelope(body []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, false
	}
	var code string
	if err := json.Unmarshal(fields["code"], &code); err != nil || code == "" {
		return nil, false
	}
	if _, _, ok := decodeMessage(fields["message"]); !ok {
		return nil, false
	}
	return fields, true
}

func decodeMessage(raw json.RawMessage) (string, []string, bool) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil, false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single, nil, true
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil && list != nil {
		return strings.Join(list, "; "), list, true
	}
	return "", nil, false
}

// decodeDetail returns `detail` when it is a JSON object; a non-object detail
// is dropped (never the envelope). Without an object detail, extra top-level
// fields become the detail, as in the Node client.
func decodeDetail(fields map[string]json.RawMessage) map[string]any {
	if raw, ok := fields["detail"]; ok && isJSONObject(raw) {
		var detail map[string]any
		if err := json.Unmarshal(raw, &detail); err == nil {
			return detail
		}
	}
	extras := map[string]any{}
	for key, raw := range fields {
		if envelopeKeys[key] {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			extras[key] = v
		}
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// parseRetryAfter accepts delta-seconds (digits only) or an HTTP-date (RFC
// 9110 10.2.3), rounded up to whole seconds and clamped to maxRetryAfter.
// Anything else, or a date in the past, yields 0.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if isDigits(value) {
		secs, err := strconv.ParseInt(value, 10, 64)
		if err != nil || secs > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter // only overflow can fail on an all-digit string
		}
		return time.Duration(secs) * time.Second
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	d := at.Sub(now)
	if d <= 0 {
		return 0
	}
	if d >= maxRetryAfter {
		return maxRetryAfter
	}
	return ((d + time.Second - 1) / time.Second) * time.Second
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

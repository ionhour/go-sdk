package ionhour

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// NewRID returns a fresh 16-character hex run-correlation id, suitable for
// [WithRID] when you want to correlate a [Job.Start] ping with its terminal
// [Job.Success] or [Job.Fail]. [Job.Run] and [RunValue] call it automatically to
// generate a rid when the caller supplies none, so the start and terminal pings
// of a wrapped run share one id.
//
// It draws 8 bytes from crypto/rand; on the astronomically rare read failure it
// falls back to a nanosecond timestamp so a run's pings still correlate rather
// than aborting. The result is always within the contract's 64-character rid
// limit.
func NewRID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

package ionhour

import (
	"context"
	"errors"
	"time"
)

// errRunNilFn is returned by [Job.Run] and [RunValue] when the wrapped function
// is nil — a usage (programmer) error surfaced loudly instead of panicking.
var errRunNilFn = errors.New("ionhour: Run requires a non-nil function")

// errRunNilJob is returned by [RunValue] (and by [Job.Run] invoked on a nil
// receiver) when no job is supplied — a usage error surfaced loudly instead of
// dereferencing a nil pointer.
var errRunNilJob = errors.New("ionhour: Run/RunValue require a non-nil job")

// Run wraps a unit of work with heartbeat reporting. Before invoking fn it sends
// a best-effort start ping (see [Job.Start]); then it times fn with a monotonic
// clock and:
//
//   - on success, pings Success with the measured duration and returns nil;
//   - on error, pings Fail with the measured duration and a payload that merges
//     {"error": err.Error()} into any caller-supplied payload, then returns fn's
//     error unchanged.
//
// The start and terminal pings share a run-correlation id: the caller's [WithRID]
// when supplied, otherwise one generated with [NewRID]. The start ping carries
// only that rid (plus [WithHost]/[WithEnv] when set); disable it with
// [WithoutStartPing].
//
// fn's error is always propagated so the caller sees their own failure. Errors
// from Run's own heartbeat pings — both transport failures and usage errors such
// as an oversized merged payload — are logged (when a logger is configured) but
// never returned or allowed to mask fn's result. A nil fn (or calling Run on a
// nil *Job) is a usage error and is returned as such, never a panic.
//
// When the wrapped work returns a value, use the package-level [RunValue]
// instead of threading the result through an outer closure variable.
func (j *Job) Run(ctx context.Context, fn func(ctx context.Context) error, opts ...PingOption) error {
	if fn == nil {
		return errRunNilFn
	}
	// Adapt fn to the value-returning shape so the timing and ping-dispatch logic
	// lives once, in runValue. The discarded struct{} carries no data.
	_, err := runValue(ctx, j, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	}, opts)
	return err
}

// RunValue wraps a value-returning unit of work with heartbeat reporting,
// mirroring [Job.Run] exactly but returning fn's own result. Because Go methods
// cannot take type parameters, it is a package-level generic that takes the job
// explicitly:
//
//	rows, err := ionhour.RunValue(ctx, job, func(ctx context.Context) (int, error) {
//		return importRows(ctx)
//	})
//
// Like [Job.Run] it sends a best-effort start ping before fn and shares a
// run-correlation id (caller's [WithRID] or a generated [NewRID]) across the
// start and terminal pings; [WithoutStartPing] disables the start ping. It times
// fn with a monotonic clock and, on success, pings Success with the measured
// duration and returns (value, nil). On error it pings Fail with the duration
// and a payload that merges {"error": err.Error()} into any caller-supplied
// payload, then returns (the zero value of T, fn's error). fn's
// error is always propagated so the caller sees their own failure; RunValue's
// own ping errors — transport and usage alike — are logged when a logger is
// configured but never returned or allowed to mask fn's result. A nil job or a
// nil fn is a usage error, returned as (zero value, error) — never a panic.
func RunValue[T any](ctx context.Context, job *Job, fn func(context.Context) (T, error), opts ...PingOption) (T, error) {
	return runValue(ctx, job, fn, opts)
}

// runValue is the shared core of [Job.Run] and [RunValue]: it validates fn,
// times it, emits the success or fail heartbeat, and enforces the error
// philosophy (propagate fn's error, swallow the SDK's own ping errors). Both
// entry points funnel here so the timing and ping-dispatch logic exists exactly
// once.
func runValue[T any](ctx context.Context, job *Job, fn func(context.Context) (T, error), opts []PingOption) (T, error) {
	var zero T
	if job == nil {
		return zero, errRunNilJob
	}
	if fn == nil {
		return zero, errRunNilFn
	}

	// Resolve the run-correlation id and start-ping preference from the caller's
	// options. When no rid was supplied, mint one so the start and terminal pings
	// correlate as a pair; a caller-supplied rid is preserved.
	base := buildPingRequest(opts)
	rid := base.rid
	if rid == "" {
		rid = NewRID()
	}
	ridOpt := WithRID(rid)

	if !base.noStartPing {
		// Best-effort start ping carrying rid/host/env only — never the duration,
		// payload, or exit code. Its errors are swallowed like every internal run
		// ping so a start-ping problem can never affect fn or its result.
		startOpts := []PingOption{ridOpt}
		if base.host != nil {
			startOpts = append(startOpts, WithHost(*base.host))
		}
		if base.env != nil {
			startOpts = append(startOpts, WithEnv(*base.env))
		}
		_, _ = job.Start(ctx, startOpts...)
	}

	start := time.Now()
	value, runErr := fn(ctx)
	elapsed := time.Since(start)

	if runErr == nil {
		successOpts := append([]PingOption{WithDuration(elapsed), ridOpt}, opts...)
		// Ping errors (transport or usage) are logged by doPing and swallowed
		// here — they must never mask fn's successful result.
		_, _ = job.Success(ctx, successOpts...)
		return value, nil
	}

	failOpts := append([]PingOption{WithDuration(elapsed), ridOpt}, opts...)
	failOpts = append(failOpts, withMergedErrorPayload(runErr, opts))
	_, _ = job.Fail(ctx, failOpts...)
	return zero, runErr
}

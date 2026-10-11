// Package ionhour is the official Go SDK for Ionhour heartbeat monitoring.
//
// Customers register Jobs (inbound heartbeat monitors) with one parameter: the
// job's ping token, which is the job's credential. Token pings send no
// Authorization header. Slug mode ([Client.RegisterJobBySlug]) addresses a job
// by slug instead and sends the API key ([WithAPIKey], IONHOUR_API_KEY) as a
// Bearer header to the API host. Once registered, a Job reports Success or Fail
// pings, or wraps a unit of work with Run so timing and failure reporting
// happen automatically.
//
// # Design guarantee
//
// Monitoring must never take down the host application. Success and Fail never
// panic. Their transport outcome — success, a network error, or 429/5xx after
// retries — is reported in a [PingResult] value, and the accompanying error
// return is nil for every transport case. That error is reserved for usage
// (programmer) errors: an oversized (> 10 KiB) or non-serializable payload, in
// which case no request is sent. Run rethrows the wrapped function's own error
// (so callers still see their failure) but swallows its own ping errors. An
// empty or whitespace token fails loudly at registration.
//
// # Quick start
//
// The simplest install uses the package-level registration and a default
// client (which reads IONHOUR_BASE_URL from the environment):
//
//	job, err := ionhour.RegisterJob("job-token")
//	if err != nil {
//		log.Fatal(err)
//	}
//	res, err := job.Success(context.Background())
//	// err is non-nil only for a usage error; res.OK reports the transport outcome.
//	_ = res
//
// For full control, build a client with functional options:
//
//	client := ionhour.NewClient(
//		ionhour.WithTimeout(5*time.Second),
//	)
//	job, _ := client.RegisterJob("job-token")
//	err := job.Run(context.Background(), func(ctx context.Context) error {
//		return doWork(ctx)
//	})
//
// When the wrapped work returns a value, use the package-level generic
// [RunValue] instead of threading the result through an outer closure variable:
//
//	n, err := ionhour.RunValue(context.Background(), job, func(ctx context.Context) (int, error) {
//		return importRows(ctx)
//	})
//	_ = n
//
// See https://docs.ionhour.com for the full documentation.
package ionhour

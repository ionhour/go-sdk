package ionhour

import (
	"regexp"
	"strings"
	"unicode"
)

// slugPattern is the server's slug rule for /signals/s/{slug}.
var slugPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// slugJobConfig is the accumulated state from SlugJobOptions.
type slugJobConfig struct {
	create bool
}

// SlugJobOption configures [Client.RegisterJobBySlug].
type SlugJobOption func(*slugJobConfig)

// WithCreate makes every ping of the slug job send ?create=1, so the first ping
// auto-creates the job in the API key's workspace (HTTP 201,
// [PingResult.Created]). It needs the server's job_auto_provisioning flag and a
// key allowed mcp:jobs; otherwise the server answers 403 FEATURE_DISABLED or
// KEY_SCOPE_DENIED.
func WithCreate() SlugJobOption {
	return func(cfg *slugJobConfig) { cfg.create = true }
}

// RegisterJobBySlug registers a job addressed by its human-readable slug in the
// workspace of the client's API key ([WithAPIKey] / IONHOUR_API_KEY). Its pings
// go to {apiBaseURL}/signals/s/{slug} (plus /fail and /start) with
// "Authorization: Bearer <key>"; the returned [Job] has the same
// Success/Fail/Start/Run surface as a token job.
//
// Registration is local (no network call). It returns *InvalidSlugError when
// the slug does not match ^[a-z0-9_-]{1,64}$, ErrMissingAPIKey when no key is
// configured, and ErrInvalidAPIKey when the key contains whitespace or control
// characters.
func (c *Client) RegisterJobBySlug(slug string, opts ...SlugJobOption) (*Job, error) {
	if !slugPattern.MatchString(slug) {
		return nil, &InvalidSlugError{Slug: slug}
	}
	if err := validateAPIKey(c.apiKey); err != nil {
		return nil, err
	}
	cfg := slugJobConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Job{slug: slug, create: cfg.create, client: c}, nil
}

// RegisterJobBySlug is the package-level slug-mode convenience on the default
// client, which reads IONHOUR_API_BASE_URL and IONHOUR_API_KEY on first use
// (see the environment note on [RegisterJob]).
func RegisterJobBySlug(slug string, opts ...SlugJobOption) (*Job, error) {
	return sharedDefaultClient().RegisterJobBySlug(slug, opts...)
}

// validateAPIKey enforces that a key is present and header-safe. The key is
// already trimmed by NewClient.
func validateAPIKey(key string) error {
	if key == "" {
		return ErrMissingAPIKey
	}
	if strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ErrInvalidAPIKey
	}
	return nil
}

// Package config loads glossa-server's configuration from the
// environment (12-factor). Everything is validated once at startup and
// every problem is reported in a single error, so a misconfigured
// deployment fails fast with the full list instead of one field at a
// time.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// MigrateMode controls whether glossa-server applies migrations at boot.
type MigrateMode string

const (
	// MigrateOff never touches the schema (the default; migrations run
	// as a separate deploy step).
	MigrateOff MigrateMode = "off"
	// MigrateUp applies pending migrations, then serves.
	MigrateUp MigrateMode = "up"
	// MigrateOnly applies pending migrations and exits (k8s Job mode).
	MigrateOnly MigrateMode = "only"
)

// Secret holds a value that must never be logged, such as a DSN with a
// password. String redacts; Reveal returns the raw value.
type Secret struct{ value string }

// String implements fmt.Stringer with the password (if any) redacted.
func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	u, err := url.Parse(s.value)
	if err != nil || u.Scheme == "" {
		return "[redacted]"
	}
	return u.Redacted()
}

// Reveal returns the raw secret for the one place that needs it.
func (s Secret) Reveal() string { return s.value }

// IsZero reports whether the secret is unset.
func (s Secret) IsZero() bool { return s.value == "" }

// HTTP configures the public listener.
type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxBodyBytes      int64
}

// OTel configures trace export. Standard OTEL_* variables beyond the
// endpoint (headers, protocol, sampler) are read by the SDK itself.
type OTel struct {
	Endpoint    string
	ServiceName string
}

// Enabled reports whether an OTLP exporter should be installed.
func (o OTel) Enabled() bool { return o.Endpoint != "" }

// Outbox configures the in-process outbox dispatcher.
type Outbox struct {
	Enabled        bool
	PollInterval   time.Duration
	BatchSize      int
	MaxAttempts    int
	Lease          time.Duration
	HandlerTimeout time.Duration
}

// DefaultDBConnectTimeout is GLOSSA_DB_CONNECT_TIMEOUT's default, and
// MaxDBConnectTimeout its upper bound: past it, an orchestrator's
// restart is the better retry.
const (
	DefaultDBConnectTimeout = 60 * time.Second
	MaxDBConnectTimeout     = 10 * time.Minute
)

// Config is the complete, validated configuration.
type Config struct {
	// DatabaseURL is the application role's DSN (NOSUPERUSER,
	// NOBYPASSRLS). Pool settings go in the DSN (pool_max_conns=…).
	DatabaseURL Secret
	// MigrationDatabaseURL is the schema owner's DSN, used only for
	// migrations. Required when Migrate is up or only.
	MigrationDatabaseURL Secret
	Migrate              MigrateMode
	// DBConnectTimeout bounds the retry of the first database connection
	// at startup and in -migrate=only (#66): a database that is still
	// starting, or a pod whose network policy is not programmed yet,
	// delays startup instead of failing it.
	DBConnectTimeout time.Duration
	HTTP             HTTP
	LogLevel         slog.Level
	ShutdownTimeout  time.Duration
	OTel             OTel
	Outbox           Outbox
	Identity         Identity
	Storage          Storage
	Release          Release
	Intelligence     Intelligence
	Integration      Integration
	Purge            Purge
	Branches         Branches
	Context          Context
	GitHub           GitHub
	MCP              MCP
	Audit            Audit
	Workflow         Workflow
}

// Workflow configures the Workflow context's housekeeping (RFC 0006
// §2.5, §13 wave 6).
type Workflow struct {
	// InstanceRetention is how long a finished workflow instance and its
	// transition log are kept; the daily workflow.retention job (one of
	// the Purge jobs) deletes them afterwards. Running instances are
	// never deleted. At least a day.
	InstanceRetention time.Duration
}

// DefaultInstanceRetention keeps a finished instance's log for half a
// year: long enough to answer "why was this approved?" for a release
// cycle or two; the audit trail keeps who did what for longer.
const DefaultInstanceRetention = 180 * 24 * time.Hour

// minInstanceRetention mirrors workflow/app.MinInstanceRetention, which
// the sweep enforces too: config does not import a context.
const minInstanceRetention = 24 * time.Hour

// Audit configures the Audit context's exports (RFC 0006 §6.2).
//
// An export is signed with the audit key, never the release signing
// key: one key, one purpose. There is no development fallback derived
// from GLOSSA_AUTH_SECRET as there is for releases — an export is
// evidence, and evidence signed by a key nobody chose is not evidence.
// So exports are off until an operator configures a key and turns them
// on, and turning them on without a key refuses to start.
type Audit struct {
	// ExportsEnabled lets audit export jobs run (RFC 0006 wave 5).
	ExportsEnabled bool
	// SigningKey is the active audit key, "keyId=base64(32-byte Ed25519
	// seed)": exactly one. Rotation: a new key here, the old one's public
	// key in RetiredKeys.
	SigningKey Secret
	// RetiredKeys are public keys older exports still verify with,
	// "keyId=base64(ed25519 public key),…". Never drop one while an
	// export it signed may still need verifying.
	RetiredKeys string
	// ExportRetention is how long an export job's two objects are kept;
	// the sweep deletes them afterwards (the job stays).
	ExportRetention time.Duration
}

// MCP configures the Model Context Protocol endpoint (RFC 0005 §7):
// /mcp on this same server, behind the same middleware, tenancy and RLS
// as REST.
//
// It is off by default, and deliberately so. MCP is a second façade on
// the API for agents, it authenticates with tenant API tokens that were
// minted before the endpoint existed, and RFC 0005 §15 leaves "hosted,
// local-only, or both?" open for the owner. An operator turns it on;
// nobody gets an agent surface by upgrading.
type MCP struct {
	// Enabled serves /mcp.
	Enabled bool
	// SessionTimeout closes a session idle this long, so an agent that
	// walks away does not hold one open forever.
	SessionTimeout time.Duration
	// Rate and Burst bound tool calls per tenant, the same wall a
	// runaway script hits (RFC 0005 §7.4).
	Rate  int
	Burst int
}

// GitHub tunes the GitHub integration's worker and sweep (RFC 0004
// §6.2). Whether the integration exists at all is decided by the App's
// own environment (GLOSSA_GITHUB_APP_ID and friends, read by the GitHub
// adapter): with none of it set, the endpoints answer
// `github_not_configured` and nothing here matters.
type GitHub struct {
	// InboxEnabled runs the webhook inbox worker in this process. Off,
	// deliveries are still stored and acknowledged — nothing processes
	// them.
	InboxEnabled bool
	// InboxWorkers is the number of deliveries this process handles at
	// once.
	InboxWorkers int
	PollInterval time.Duration
	// HandlerTimeout bounds one attempt; Lease (longer) is how long a
	// claimed delivery is reserved before another worker takes it over.
	HandlerTimeout time.Duration
	Lease          time.Duration
	// DepthInterval is how often the inbox-depth metric is sampled.
	DepthInterval time.Duration

	// The Glossa PR check (RFC 0004 §6.4). Its worker is a second queue
	// worker on the same integration: it renders a pull request's report
	// and writes the check run and the sticky comment.

	// ChecksEnabled runs the check worker in this process. Off, checks
	// are still queued by the webhook — nothing reports them.
	ChecksEnabled bool
	// CheckWorkers is the number of checks this process renders at once.
	CheckWorkers int
	// CheckPollInterval is how long an idle check worker waits.
	CheckPollInterval time.Duration
	// CheckTimeout bounds one attempt; CheckLease (longer) is how long a
	// claimed check is reserved against the other replicas.
	CheckTimeout time.Duration
	CheckLease   time.Duration
	// CheckDepthInterval is how often the queue-depth metric is sampled.
	CheckDepthInterval time.Duration
}

// Context configures the Context context's capture storage
// (RFC 0004 §3.3, §10).
type Context struct {
	// StorageQuotaBytes caps the capture images one tenant keeps in
	// object storage. An upload whose new pixels would pass it is
	// refused with storage_quota_exceeded; retention frees space again.
	StorageQuotaBytes int64
}

// Purge configures the daily retention job (RFC 0004 §2.3): Context's
// builds, captures and images, and Catalog's proposal sweep. One run
// happens per deployment, not per replica — a lease decides which
// replica leads it.
type Purge struct {
	// Enabled runs the purge scheduler in this process. Off, nothing
	// purges: builds and their images accumulate.
	Enabled bool
	// Interval is how often the jobs run across the deployment.
	Interval time.Duration
	// Timeout bounds one run of one job; Lease (longer) is how long a
	// run reserves its job against the other replicas.
	Timeout time.Duration
	Lease   time.Duration
	// PollInterval is how often a replica asks whether a job is due.
	PollInterval time.Duration
	// Jitter spreads each poll by up to this fraction of PollInterval,
	// so replicas started together don't all ask at once.
	Jitter float64
	// BatchSize bounds the object-store deletes one purge issues at a
	// time, so freeing a large backlog of images doesn't flood MinIO.
	BatchSize int
}

// Branches configures the branch publisher (RFC 0004 §4): it publishes
// a branch's preview environment once its debounced request is due. The
// proposal sweep is not here — it is one of the daily Purge jobs, so
// that one replica leads it rather than all of them.
type Branches struct {
	// PublisherEnabled runs the publisher in this process. It is
	// idempotent (a publish is keyed by its request), so every replica
	// may run it.
	PublisherEnabled bool
	// PublishInterval is how often the publisher looks for due requests.
	PublishInterval time.Duration
}

// Integration configures the import and export jobs (RFC 0003 §5–§6):
// the workers that run in glossa-server, uploads and retention.
type Integration struct {
	// WorkersEnabled runs import/export workers in this process.
	WorkersEnabled bool
	// Workers is the number of jobs this process runs at once.
	Workers      int
	PollInterval time.Duration
	// JobTimeout bounds one attempt of a job; Lease (longer) is how long
	// a claimed job is reserved before another worker may take it over.
	JobTimeout time.Duration
	Lease      time.Duration
	// MaxUploadBytes bounds an import's file.
	MaxUploadBytes int64
	// UploadTimeout bounds reading one upload's body (and writing one
	// download), instead of the HTTP read and write timeouts.
	UploadTimeout time.Duration
	// Retention is how long uploaded and exported files are kept.
	Retention time.Duration
}

// Intelligence configures the AI translation job workers that run in
// glossa-server and the providers they call (RFC 0003 §3).
type Intelligence struct {
	// WorkersEnabled runs job workers in this process.
	WorkersEnabled bool
	// Workers is the number of jobs this process runs at once.
	Workers      int
	PollInterval time.Duration
	// JobTimeout bounds one job, model calls and retries included; Lease
	// (longer) is how long a claimed job is reserved before another
	// worker may take it over.
	JobTimeout time.Duration
	Lease      time.Duration
	// AllowPrivateEndpoints lets tenants point providers at loopback and
	// private addresses (self-hosted models in the cluster).
	AllowPrivateEndpoints bool
	// ProviderConcurrency caps the calls in flight per configured
	// provider in this process.
	ProviderConcurrency int
}

// Identity configures authentication (the Identity context).
type Identity struct {
	// AuthSecret is the root key (base64, ≥ 32 bytes) from which the CSRF,
	// TOTP-sealing and passkey-state keys are derived. Rotating it signs
	// everyone's CSRF tokens anew, drops in-flight passkey ceremonies and
	// makes enrolled TOTP secrets unreadable (people re-enroll).
	AuthSecret Secret
	// StudioURL is Studio's origin; emailed links point into it.
	StudioURL string
	// SessionTTL is a session's lifetime.
	SessionTTL time.Duration
	Mail       Mail
	WebAuthn   WebAuthn
	// TrustedProxies are the reverse proxies (CIDRs) whose
	// X-Forwarded-For names the client, for limits per client address
	// such as device sign-in starts. Empty: the peer is the client.
	TrustedProxies []netip.Prefix
}

// AuthKey returns the decoded AuthSecret (validated by Load).
func (i Identity) AuthKey() []byte {
	k, _ := decodeKey(i.AuthSecret.Reveal())
	return k
}

// Mail configures outbound email.
type Mail struct {
	// Driver is "none" (the default: the deployment sends no email, so
	// magic links and password reset by email are unavailable), "smtp",
	// or "log" (development only: mail, links included, goes to the log).
	Driver string
	From   string
	// SMTPAddr is host:port of the submission server.
	SMTPAddr           string
	SMTPUsername       string
	SMTPPassword       Secret
	SMTPAllowPlaintext bool
}

// Enabled reports whether email is sent (or, with log, written out).
func (m Mail) Enabled() bool { return m.Driver != "none" }

// WebAuthn configures the passkey relying party. Passkeys are off while
// RPID is empty.
type WebAuthn struct {
	RPID    string
	RPName  string
	Origins []string
}

// Enabled reports whether passkeys are configured.
func (w WebAuthn) Enabled() bool { return w.RPID != "" }

const minAuthKeyLen = 32

func decodeKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// String renders the configuration with secrets redacted.
func (c Config) String() string {
	return fmt.Sprintf(
		"database=%s db_connect_timeout=%s migrate=%s http=%s log=%s shutdown=%s otel=%q outbox=%t storage=%s ai_workers=%d purge=%s mcp=%t",
		c.DatabaseURL, c.DBConnectTimeout, c.Migrate, c.HTTP.Addr, c.LogLevel, c.ShutdownTimeout,
		c.OTel.Endpoint, c.Outbox.Enabled, c.Storage.Driver, c.aiWorkers(), c.purge(), c.MCP.Enabled,
	)
}

// purge renders the retention job's schedule, or "off".
func (c Config) purge() string {
	if !c.Purge.Enabled {
		return "off"
	}
	return c.Purge.Interval.String()
}

func (c Config) aiWorkers() int {
	if !c.Intelligence.WorkersEnabled {
		return 0
	}
	return c.Intelligence.Workers
}

// Load reads and validates the configuration.
func Load(lookup LookupFunc) (Config, error) {
	r := reader{lookup: lookup}
	cfg := Config{
		DatabaseURL:          Secret{r.required("DATABASE_URL")},
		MigrationDatabaseURL: Secret{r.str("MIGRATION_DATABASE_URL", "")},
		Migrate:              r.migrateMode("GLOSSA_MIGRATE"),
		HTTP:                 r.http(":8080"),
		LogLevel:             r.logLevel("GLOSSA_LOG_LEVEL"),
		ShutdownTimeout:      r.duration("GLOSSA_SHUTDOWN_TIMEOUT", 25*time.Second),
		DBConnectTimeout:     r.duration("GLOSSA_DB_CONNECT_TIMEOUT", DefaultDBConnectTimeout),
		OTel: OTel{
			Endpoint:    r.str("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			ServiceName: r.str("OTEL_SERVICE_NAME", "glossa-server"),
		},
		Outbox: Outbox{
			Enabled:        r.boolean("GLOSSA_OUTBOX_ENABLED", true),
			PollInterval:   r.duration("GLOSSA_OUTBOX_POLL_INTERVAL", time.Second),
			BatchSize:      r.intRange("GLOSSA_OUTBOX_BATCH_SIZE", 50, 1, 1000),
			MaxAttempts:    r.intRange("GLOSSA_OUTBOX_MAX_ATTEMPTS", 10, 1, 100),
			Lease:          r.duration("GLOSSA_OUTBOX_LEASE", time.Minute),
			HandlerTimeout: r.duration("GLOSSA_OUTBOX_HANDLER_TIMEOUT", 30*time.Second),
		},
	}
	cfg.Identity = r.identity()
	cfg.Storage = r.storage()
	cfg.Release = r.release()
	cfg.Intelligence = Intelligence{
		WorkersEnabled:        r.boolean("GLOSSA_AI_WORKERS_ENABLED", true),
		Workers:               r.intRange("GLOSSA_AI_WORKERS", 2, 1, 64),
		PollInterval:          r.duration("GLOSSA_AI_POLL_INTERVAL", time.Second),
		JobTimeout:            r.duration("GLOSSA_AI_JOB_TIMEOUT", 10*time.Minute),
		Lease:                 r.duration("GLOSSA_AI_JOB_LEASE", 15*time.Minute),
		AllowPrivateEndpoints: r.boolean("GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS", false),
		ProviderConcurrency:   r.intRange("GLOSSA_AI_PROVIDER_CONCURRENCY", 4, 1, 256),
	}
	cfg.Integration = Integration{
		WorkersEnabled: r.boolean("GLOSSA_INTEGRATION_WORKERS_ENABLED", true),
		Workers:        r.intRange("GLOSSA_INTEGRATION_WORKERS", 1, 1, 64),
		PollInterval:   r.duration("GLOSSA_INTEGRATION_POLL_INTERVAL", time.Second),
		JobTimeout:     r.duration("GLOSSA_INTEGRATION_JOB_TIMEOUT", 30*time.Minute),
		Lease:          r.duration("GLOSSA_INTEGRATION_JOB_LEASE", 35*time.Minute),
		MaxUploadBytes: int64(r.intRange("GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES", 64<<20, 1, 2<<30)),
		UploadTimeout:  r.duration("GLOSSA_INTEGRATION_UPLOAD_TIMEOUT", 10*time.Minute),
		Retention:      r.duration("GLOSSA_INTEGRATION_RETENTION", 7*24*time.Hour),
	}
	cfg.Purge = Purge{
		Enabled:      r.boolean("GLOSSA_PURGE_ENABLED", true),
		Interval:     r.duration("GLOSSA_PURGE_INTERVAL", 24*time.Hour),
		Timeout:      r.duration("GLOSSA_PURGE_TIMEOUT", 30*time.Minute),
		Lease:        r.duration("GLOSSA_PURGE_LEASE", 35*time.Minute),
		PollInterval: r.duration("GLOSSA_PURGE_POLL_INTERVAL", 5*time.Minute),
		Jitter:       r.ratio("GLOSSA_PURGE_JITTER", 0.2),
		BatchSize:    r.intRange("GLOSSA_PURGE_BATCH_SIZE", 100, 1, 10000),
	}
	cfg.Branches = Branches{
		PublisherEnabled: r.boolean("GLOSSA_BRANCH_PUBLISHER_ENABLED", true),
		PublishInterval:  r.duration("GLOSSA_BRANCH_PUBLISH_INTERVAL", 5*time.Second),
	}
	cfg.Context = Context{
		// 2 GB by default (the owner's decision of 2026-09-20); at least
		// one image's worth, and at most a terabyte.
		StorageQuotaBytes: int64(r.intRange("GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES", 2<<30, 10<<20, 1<<40)),
	}
	cfg.GitHub = GitHub{
		InboxEnabled:   r.boolean("GLOSSA_GITHUB_INBOX_ENABLED", true),
		InboxWorkers:   r.intRange("GLOSSA_GITHUB_INBOX_WORKERS", 2, 1, 64),
		PollInterval:   r.duration("GLOSSA_GITHUB_INBOX_POLL_INTERVAL", time.Second),
		HandlerTimeout: r.duration("GLOSSA_GITHUB_INBOX_TIMEOUT", 30*time.Second),
		Lease:          r.duration("GLOSSA_GITHUB_INBOX_LEASE", 2*time.Minute),
		DepthInterval:  r.duration("GLOSSA_GITHUB_INBOX_DEPTH_INTERVAL", 30*time.Second),

		ChecksEnabled:      r.boolean("GLOSSA_GITHUB_CHECKS_ENABLED", true),
		CheckWorkers:       r.intRange("GLOSSA_GITHUB_CHECK_WORKERS", 2, 1, 64),
		CheckPollInterval:  r.duration("GLOSSA_GITHUB_CHECK_POLL_INTERVAL", time.Second),
		CheckTimeout:       r.duration("GLOSSA_GITHUB_CHECK_TIMEOUT", time.Minute),
		CheckLease:         r.duration("GLOSSA_GITHUB_CHECK_LEASE", 5*time.Minute),
		CheckDepthInterval: r.duration("GLOSSA_GITHUB_CHECK_DEPTH_INTERVAL", 30*time.Second),
	}
	cfg.MCP = MCP{
		Enabled:        r.boolean("GLOSSA_MCP_ENABLED", false),
		SessionTimeout: r.duration("GLOSSA_MCP_SESSION_TIMEOUT", 30*time.Minute),
		// 120 calls a minute in bursts of 240: an agent exploring a
		// catalog makes a burst of reads and then thinks, and a loop that
		// never thinks is exactly what the limit is for.
		Rate:  r.intRange("GLOSSA_MCP_RATE", 120, 1, 100_000),
		Burst: r.intRange("GLOSSA_MCP_BURST", 240, 1, 100_000),
	}
	cfg.Audit = r.audit()
	cfg.Workflow = Workflow{
		InstanceRetention: r.duration("GLOSSA_WORKFLOW_INSTANCE_RETENTION", DefaultInstanceRetention),
	}
	cfg.validate(&r)
	if len(r.errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  %w", errors.Join(r.errs...))
	}
	return cfg, nil
}

func (r *reader) audit() Audit {
	a := Audit{
		ExportsEnabled: r.boolean("GLOSSA_AUDIT_EXPORTS_ENABLED", false),
		SigningKey:     Secret{r.str("GLOSSA_AUDIT_SIGNING_KEY", "")},
		RetiredKeys:    r.str("GLOSSA_AUDIT_RETIRED_KEYS", ""),
		// M2's export files' default (GLOSSA_INTEGRATION_RETENTION).
		ExportRetention: r.duration("GLOSSA_AUDIT_EXPORT_RETENTION", 7*24*time.Hour),
	}
	if key := a.SigningKey.Reveal(); key != "" {
		if strings.Contains(key, ",") {
			r.fail("GLOSSA_AUDIT_SIGNING_KEY", "exactly one keyId=base64(seed): an export carries one signature; put the previous key's public key in GLOSSA_AUDIT_RETIRED_KEYS")
		} else {
			r.keyList("GLOSSA_AUDIT_SIGNING_KEY", key)
		}
	}
	r.keyList("GLOSSA_AUDIT_RETIRED_KEYS", a.RetiredKeys)
	if a.ExportsEnabled && a.SigningKey.IsZero() {
		r.fail("GLOSSA_AUDIT_SIGNING_KEY", "required when GLOSSA_AUDIT_EXPORTS_ENABLED is true: "+
			"audit exports are signed with their own key, never the release key (RFC 0006 §6.2); "+
			"set keyId=base64(32-byte seed), e.g. audit-2026=$(openssl rand -base64 32)")
	}
	return a
}

// validate checks the cross-field rules.
func (c Config) validate(r *reader) {
	if c.Migrate != MigrateOff && c.MigrationDatabaseURL.IsZero() {
		r.fail("MIGRATION_DATABASE_URL", "required when GLOSSA_MIGRATE is up or only")
	}
	if c.Outbox.Lease <= c.Outbox.HandlerTimeout {
		r.fail("GLOSSA_OUTBOX_LEASE", "must be longer than GLOSSA_OUTBOX_HANDLER_TIMEOUT")
	}
	if c.DBConnectTimeout > MaxDBConnectTimeout {
		r.fail("GLOSSA_DB_CONNECT_TIMEOUT", "must be at most %s", MaxDBConnectTimeout)
	}
	if c.Intelligence.Lease <= c.Intelligence.JobTimeout {
		r.fail("GLOSSA_AI_JOB_LEASE", "must be longer than GLOSSA_AI_JOB_TIMEOUT")
	}
	if c.Integration.Lease <= c.Integration.JobTimeout {
		r.fail("GLOSSA_INTEGRATION_JOB_LEASE", "must be longer than GLOSSA_INTEGRATION_JOB_TIMEOUT")
	}
	if c.Purge.Lease <= c.Purge.Timeout {
		r.fail("GLOSSA_PURGE_LEASE", "must be longer than GLOSSA_PURGE_TIMEOUT")
	}
	if c.GitHub.Lease <= c.GitHub.HandlerTimeout {
		r.fail("GLOSSA_GITHUB_INBOX_LEASE", "must be longer than GLOSSA_GITHUB_INBOX_TIMEOUT")
	}
	if c.GitHub.CheckLease <= c.GitHub.CheckTimeout {
		r.fail("GLOSSA_GITHUB_CHECK_LEASE", "must be longer than GLOSSA_GITHUB_CHECK_TIMEOUT")
	}
	if c.Purge.Timeout > c.Purge.Interval {
		r.fail("GLOSSA_PURGE_TIMEOUT", "must fit inside GLOSSA_PURGE_INTERVAL")
	}
	if c.Purge.PollInterval > c.Purge.Interval {
		r.fail("GLOSSA_PURGE_POLL_INTERVAL", "must not exceed GLOSSA_PURGE_INTERVAL")
	}
	if c.Workflow.InstanceRetention > 0 && c.Workflow.InstanceRetention < minInstanceRetention {
		r.fail("GLOSSA_WORKFLOW_INSTANCE_RETENTION", "must be at least 24h: a finished instance's log is kept a day at least")
	}
}

func (r *reader) identity() Identity {
	id := Identity{
		AuthSecret: Secret{r.required("GLOSSA_AUTH_SECRET")},
		StudioURL:  r.absoluteURL("GLOSSA_STUDIO_URL", "http://localhost:5173"),
		SessionTTL: r.duration("GLOSSA_SESSION_TTL", 14*24*time.Hour),
		Mail: Mail{
			Driver:             r.str("GLOSSA_MAIL_DRIVER", "none"),
			From:               r.str("GLOSSA_MAIL_FROM", "Glossa <no-reply@localhost>"),
			SMTPAddr:           r.str("GLOSSA_SMTP_ADDR", ""),
			SMTPUsername:       r.str("GLOSSA_SMTP_USERNAME", ""),
			SMTPPassword:       Secret{r.str("GLOSSA_SMTP_PASSWORD", "")},
			SMTPAllowPlaintext: r.boolean("GLOSSA_SMTP_ALLOW_PLAINTEXT", false),
		},
		WebAuthn: WebAuthn{
			RPID:   r.str("GLOSSA_WEBAUTHN_RP_ID", ""),
			RPName: r.str("GLOSSA_WEBAUTHN_RP_NAME", "Glossa"),
		},
	}
	if s := id.AuthSecret.Reveal(); s != "" {
		if k, err := decodeKey(s); err != nil || len(k) < minAuthKeyLen {
			r.fail("GLOSSA_AUTH_SECRET", "must be base64 of at least %d random bytes (openssl rand -base64 32)", minAuthKeyLen)
		}
	}
	switch id.Mail.Driver {
	case "none", "log":
	case "smtp":
		if id.Mail.SMTPAddr == "" {
			r.fail("GLOSSA_SMTP_ADDR", "required when GLOSSA_MAIL_DRIVER is smtp")
		}
	default:
		r.fail("GLOSSA_MAIL_DRIVER", "must be none, smtp or log (got %q)", id.Mail.Driver)
	}
	for _, raw := range strings.Split(r.str("GLOSSA_TRUSTED_PROXIES", ""), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, aerr := netip.ParseAddr(raw)
			if aerr != nil {
				r.fail("GLOSSA_TRUSTED_PROXIES", "%q is not a CIDR or an address", raw)
				continue
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		id.TrustedProxies = append(id.TrustedProxies, p.Masked())
	}
	if id.WebAuthn.Enabled() {
		for _, o := range strings.Split(r.str("GLOSSA_WEBAUTHN_ORIGINS", id.StudioURL), ",") {
			if o = strings.TrimSpace(o); o != "" {
				id.WebAuthn.Origins = append(id.WebAuthn.Origins, o)
			}
		}
	}
	return id
}

func (r *reader) absoluteURL(key, def string) string {
	raw := r.str(key, def)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		r.fail(key, "must be an absolute http(s) URL (got %q)", raw)
	}
	return strings.TrimRight(raw, "/")
}

// reader accumulates parse errors so Load can report all of them.
type reader struct {
	lookup LookupFunc
	errs   []error
}

func (r *reader) fail(key, format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf("%s: "+format, append([]any{key}, args...)...))
}

func (r *reader) str(key, def string) string {
	if v, ok := r.lookup(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) required(key string) string {
	v := r.str(key, "")
	if v == "" {
		r.fail(key, "required")
	}
	return v
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		r.fail(key, "invalid duration %q", raw)
		return def
	}
	if d <= 0 {
		r.fail(key, "must be positive")
	}
	return d
}

func (r *reader) intRange(key string, def, lo, hi int) int {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		r.fail(key, "must be between %d and %d", lo, hi)
		return def
	}
	return n
}

// ratio reads a fraction between 0 and 1 inclusive.
func (r *reader) ratio(key string, def float64) float64 {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < 0 || f > 1 {
		r.fail(key, "must be a number between 0 and 1")
		return def
	}
	return f
}

func (r *reader) boolean(key string, def bool) bool {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		r.fail(key, "invalid boolean %q", raw)
		return def
	}
	return b
}

func (r *reader) migrateMode(key string) MigrateMode {
	raw := strings.ToLower(r.str(key, string(MigrateOff)))
	switch m := MigrateMode(raw); m {
	case MigrateOff, MigrateUp, MigrateOnly:
		return m
	}
	r.fail(key, "must be one of off, up, only (got %q)", raw)
	return MigrateOff
}

func (r *reader) logLevel(key string) slog.Level {
	raw := strings.ToLower(r.str(key, "info"))
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo,
		"warn": slog.LevelWarn, "error": slog.LevelError,
	}
	if l, ok := levels[raw]; ok {
		return l
	}
	r.fail(key, "must be one of debug, info, warn, error (got %q)", raw)
	return slog.LevelInfo
}

package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
)

// testSecret is base64 of 42 bytes.
const testSecret = "YS10ZXN0LWF1dGgtc2VjcmV0LXRoYXQtaXMtbG9uZy1lbm91Z2gtMTIz"

func TestIdentityDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	id := cfg.Identity
	if len(id.AuthKey()) != 42 || id.StudioURL != "http://localhost:5173" || id.SessionTTL != 14*24*time.Hour {
		t.Errorf("identity = %+v", id)
	}
	// No email unless configured: the log mailer is for development and
	// must be asked for.
	if id.Mail.Driver != "none" || id.Mail.Enabled() || id.WebAuthn.Enabled() {
		t.Errorf("mail = %+v, webauthn = %+v", id.Mail, id.WebAuthn)
	}
	if strings.Contains(cfg.String(), testSecret) || strings.Contains(id.AuthSecret.String(), testSecret) {
		t.Error("the auth secret leaks through String()")
	}
}

func TestIdentityOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":            "postgres://app@db/glossa",
		"GLOSSA_AUTH_SECRET":      testSecret,
		"GLOSSA_STUDIO_URL":       "https://app.glossa.test/",
		"GLOSSA_MAIL_DRIVER":      "smtp",
		"GLOSSA_SMTP_ADDR":        "smtp.test:587",
		"GLOSSA_WEBAUTHN_RP_ID":   "glossa.test",
		"GLOSSA_WEBAUTHN_ORIGINS": "https://app.glossa.test, https://studio.glossa.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := cfg.Identity
	if id.StudioURL != "https://app.glossa.test" || id.Mail.SMTPAddr != "smtp.test:587" {
		t.Errorf("identity = %+v", id)
	}
	if !id.WebAuthn.Enabled() || len(id.WebAuthn.Origins) != 2 || id.WebAuthn.Origins[1] != "https://studio.glossa.test" {
		t.Errorf("webauthn = %+v", id.WebAuthn)
	}
}

func TestIdentityValidation(t *testing.T) {
	_, err := config.Load(env(map[string]string{
		"DATABASE_URL":       "postgres://app@db/glossa",
		"GLOSSA_AUTH_SECRET": "c2hvcnQ=", // "short"
		"GLOSSA_STUDIO_URL":  "localhost:5173",
		"GLOSSA_MAIL_DRIVER": "smtp",
	}))
	for _, want := range []string{
		"GLOSSA_AUTH_SECRET: must be base64 of at least 32 random bytes",
		`GLOSSA_STUDIO_URL: must be an absolute http(s) URL`,
		"GLOSSA_SMTP_ADDR: required when GLOSSA_MAIL_DRIVER is smtp",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v\ndoes not contain %q", err, want)
		}
	}
}

func TestMailDrivers(t *testing.T) {
	for driver, enabled := range map[string]bool{"none": false, "log": true} {
		cfg, err := config.Load(env(map[string]string{
			"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret, "GLOSSA_MAIL_DRIVER": driver,
		}))
		if err != nil || cfg.Identity.Mail.Enabled() != enabled {
			t.Errorf("%s: %v %+v", driver, err, cfg.Identity.Mail)
		}
	}
	_, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret, "GLOSSA_MAIL_DRIVER": "sendmail",
	}))
	if err == nil || !strings.Contains(err.Error(), `GLOSSA_MAIL_DRIVER: must be none, smtp or log (got "sendmail")`) {
		t.Errorf("unknown driver: %v", err)
	}
}

func env(kv map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		v, ok := kv[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":       "postgres://glossa_app:secret@db:5432/glossa",
		"GLOSSA_AUTH_SECRET": testSecret,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want :8080", cfg.HTTP.Addr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 25*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 25s", cfg.ShutdownTimeout)
	}
	if cfg.DBConnectTimeout != time.Minute {
		t.Errorf("DBConnectTimeout = %v, want 1m", cfg.DBConnectTimeout)
	}
	if cfg.Migrate != config.MigrateOff {
		t.Errorf("Migrate = %q, want off", cfg.Migrate)
	}
	if !cfg.Outbox.Enabled {
		t.Error("Outbox.Enabled = false, want true")
	}
	if cfg.HTTP.MaxBodyBytes != 1<<20 {
		t.Errorf("HTTP.MaxBodyBytes = %d, want 1MiB", cfg.HTTP.MaxBodyBytes)
	}
	if cfg.OTel.Enabled() {
		t.Error("OTel enabled without an endpoint")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":                "postgres://app@db/glossa",
		"GLOSSA_AUTH_SECRET":          testSecret,
		"MIGRATION_DATABASE_URL":      "postgres://owner@db/glossa",
		"GLOSSA_MIGRATE":              "up",
		"GLOSSA_HTTP_ADDR":            "127.0.0.1:9000",
		"GLOSSA_LOG_LEVEL":            "debug",
		"GLOSSA_SHUTDOWN_TIMEOUT":     "10s",
		"GLOSSA_DB_CONNECT_TIMEOUT":   "2m",
		"GLOSSA_OUTBOX_BATCH_SIZE":    "7",
		"GLOSSA_OUTBOX_MAX_ATTEMPTS":  "4",
		"GLOSSA_OUTBOX_ENABLED":       "false",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel:4318",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Migrate != config.MigrateUp {
		t.Errorf("Migrate = %q, want up", cfg.Migrate)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9000" {
		t.Errorf("HTTP.Addr = %q", cfg.HTTP.Addr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
	if cfg.DBConnectTimeout != 2*time.Minute {
		t.Errorf("DBConnectTimeout = %v, want 2m", cfg.DBConnectTimeout)
	}
	if cfg.Outbox.BatchSize != 7 || cfg.Outbox.MaxAttempts != 4 || cfg.Outbox.Enabled {
		t.Errorf("Outbox = %+v", cfg.Outbox)
	}
	if !cfg.OTel.Enabled() {
		t.Error("OTel not enabled with an endpoint")
	}
}

func TestIntegrationConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	in := cfg.Integration
	if !in.WorkersEnabled || in.Workers != 1 || in.PollInterval != time.Second || in.JobTimeout != 30*time.Minute ||
		in.Lease != 35*time.Minute || in.MaxUploadBytes != 64<<20 || in.UploadTimeout != 10*time.Minute ||
		in.Retention != 7*24*time.Hour {
		t.Errorf("defaults = %+v", in)
	}
	over := map[string]string{
		"GLOSSA_INTEGRATION_WORKERS_ENABLED": "false", "GLOSSA_INTEGRATION_WORKERS": "3",
		"GLOSSA_INTEGRATION_JOB_TIMEOUT": "5m", "GLOSSA_INTEGRATION_JOB_LEASE": "6m",
		"GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES": "1048576", "GLOSSA_INTEGRATION_UPLOAD_TIMEOUT": "1m",
		"GLOSSA_INTEGRATION_RETENTION": "24h",
	}
	for k, v := range base {
		over[k] = v
	}
	if cfg, err = config.Load(env(over)); err != nil {
		t.Fatal(err)
	}
	in = cfg.Integration
	if in.WorkersEnabled || in.Workers != 3 || in.JobTimeout != 5*time.Minute || in.Lease != 6*time.Minute ||
		in.MaxUploadBytes != 1<<20 || in.UploadTimeout != time.Minute || in.Retention != 24*time.Hour {
		t.Errorf("overrides = %+v", in)
	}
	over["GLOSSA_INTEGRATION_JOB_LEASE"] = "5m"
	over["GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES"] = "0"
	_, err = config.Load(env(over))
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_INTEGRATION_JOB_LEASE") ||
		!strings.Contains(err.Error(), "GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES") {
		t.Errorf("invalid integration settings: %v", err)
	}
}

func TestWorkflowInstanceRetention(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workflow.InstanceRetention != 180*24*time.Hour {
		t.Errorf("default = %s", cfg.Workflow.InstanceRetention)
	}
	base["GLOSSA_WORKFLOW_INSTANCE_RETENTION"] = "720h"
	if cfg, err = config.Load(env(base)); err != nil || cfg.Workflow.InstanceRetention != 720*time.Hour {
		t.Errorf("720h = %s (%v)", cfg.Workflow.InstanceRetention, err)
	}
	for _, bad := range []string{"23h", "0", "soon"} {
		base["GLOSSA_WORKFLOW_INSTANCE_RETENTION"] = bad
		if _, err := config.Load(env(base)); err == nil || !strings.Contains(err.Error(), "GLOSSA_WORKFLOW_INSTANCE_RETENTION") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestPurgeConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Purge
	if !p.Enabled || p.Interval != 24*time.Hour || p.Timeout != 30*time.Minute || p.Lease != 35*time.Minute ||
		p.PollInterval != 5*time.Minute || p.Jitter != 0.2 || p.BatchSize != 100 {
		t.Errorf("defaults = %+v", p)
	}
	if !strings.Contains(cfg.String(), "purge=24h0m0s") {
		t.Errorf("String() = %q, want the purge interval", cfg.String())
	}

	over := map[string]string{
		"GLOSSA_PURGE_ENABLED": "false", "GLOSSA_PURGE_INTERVAL": "6h", "GLOSSA_PURGE_TIMEOUT": "2m",
		"GLOSSA_PURGE_LEASE": "3m", "GLOSSA_PURGE_POLL_INTERVAL": "30s", "GLOSSA_PURGE_JITTER": "0",
		"GLOSSA_PURGE_BATCH_SIZE": "25",
	}
	for k, v := range base {
		over[k] = v
	}
	if cfg, err = config.Load(env(over)); err != nil {
		t.Fatal(err)
	}
	p = cfg.Purge
	if p.Enabled || p.Interval != 6*time.Hour || p.Timeout != 2*time.Minute || p.Lease != 3*time.Minute ||
		p.PollInterval != 30*time.Second || p.Jitter != 0 || p.BatchSize != 25 {
		t.Errorf("overrides = %+v", p)
	}
	if !strings.Contains(cfg.String(), "purge=off") {
		t.Errorf("String() with the purge disabled = %q", cfg.String())
	}

	// A lease no longer than the timeout, a timeout that can't fit in
	// the interval, a poll beyond it, and a jitter outside 0–1 are all
	// reported at once.
	over["GLOSSA_PURGE_LEASE"] = "2m"
	over["GLOSSA_PURGE_INTERVAL"] = "10s"
	over["GLOSSA_PURGE_JITTER"] = "1.5"
	_, err = config.Load(env(over))
	for _, want := range []string{
		"GLOSSA_PURGE_LEASE", "GLOSSA_PURGE_TIMEOUT", "GLOSSA_PURGE_POLL_INTERVAL", "GLOSSA_PURGE_JITTER",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("invalid purge settings: %v does not mention %s", err, want)
		}
	}
}

func TestContextStorageQuotaConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	// 2 GB, the owner's decision of 2026-09-20 (RFC 0004 §3.3).
	if cfg.Context.StorageQuotaBytes != 2<<30 {
		t.Errorf("default quota = %d, want %d", cfg.Context.StorageQuotaBytes, int64(2)<<30)
	}
	over := map[string]string{"GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES": "536870912"}
	for k, v := range base {
		over[k] = v
	}
	if cfg, err = config.Load(env(over)); err != nil || cfg.Context.StorageQuotaBytes != 512<<20 {
		t.Errorf("override = %d, %v", cfg.Context.StorageQuotaBytes, err)
	}
	over["GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES"] = "1024"
	if _, err = config.Load(env(over)); err == nil ||
		!strings.Contains(err.Error(), "GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES") {
		t.Errorf("a quota below one image's worth: %v", err)
	}
}

func TestBranchesConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Branches
	if !b.PublisherEnabled || b.PublishInterval != 5*time.Second {
		t.Errorf("defaults = %+v", b)
	}
	over := map[string]string{
		"GLOSSA_BRANCH_PUBLISHER_ENABLED": "false", "GLOSSA_BRANCH_PUBLISH_INTERVAL": "2s",
	}
	for k, v := range base {
		over[k] = v
	}
	if cfg, err = config.Load(env(over)); err != nil {
		t.Fatal(err)
	}
	if b = cfg.Branches; b.PublisherEnabled || b.PublishInterval != 2*time.Second {
		t.Errorf("overrides = %+v", b)
	}
	over["GLOSSA_BRANCH_PUBLISH_INTERVAL"] = "nope"
	if _, err := config.Load(env(over)); err == nil || !strings.Contains(err.Error(), "GLOSSA_BRANCH_PUBLISH_INTERVAL") {
		t.Errorf("invalid interval: %v", err)
	}
}

func TestIntelligenceConfig(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret,
	}))
	if err != nil {
		t.Fatal(err)
	}
	ai := cfg.Intelligence
	if !ai.WorkersEnabled || ai.Workers != 2 || ai.PollInterval != time.Second || ai.JobTimeout != 10*time.Minute ||
		ai.Lease != 15*time.Minute || ai.AllowPrivateEndpoints || ai.ProviderConcurrency != 4 {
		t.Errorf("defaults = %+v", ai)
	}
	cfg, err = config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret,
		"GLOSSA_AI_WORKERS_ENABLED": "false", "GLOSSA_AI_WORKERS": "8", "GLOSSA_AI_JOB_TIMEOUT": "2m",
		"GLOSSA_AI_JOB_LEASE": "5m", "GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS": "true", "GLOSSA_AI_PROVIDER_CONCURRENCY": "16",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ai = cfg.Intelligence
	if ai.WorkersEnabled || ai.Workers != 8 || ai.JobTimeout != 2*time.Minute || ai.Lease != 5*time.Minute ||
		!ai.AllowPrivateEndpoints || ai.ProviderConcurrency != 16 {
		t.Errorf("overrides = %+v", ai)
	}
	_, err = config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret,
		"GLOSSA_AI_JOB_TIMEOUT": "10m", "GLOSSA_AI_JOB_LEASE": "10m",
	}))
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_AI_JOB_LEASE") {
		t.Errorf("a lease no longer than the job timeout: %v", err)
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr []string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantErr: []string{"DATABASE_URL: required"},
		},
		{
			name: "migrate without migration url",
			env: map[string]string{
				"DATABASE_URL":   "postgres://app@db/glossa",
				"GLOSSA_MIGRATE": "up",
			},
			wantErr: []string{"MIGRATION_DATABASE_URL: required when GLOSSA_MIGRATE is up or only"},
		},
		{
			name: "invalid values are all reported",
			env: map[string]string{
				"DATABASE_URL":             "postgres://app@db/glossa",
				"GLOSSA_MIGRATE":           "sideways",
				"GLOSSA_LOG_LEVEL":         "loud",
				"GLOSSA_SHUTDOWN_TIMEOUT":  "soon",
				"GLOSSA_OUTBOX_BATCH_SIZE": "0",
				"GLOSSA_OUTBOX_ENABLED":    "maybe",
			},
			wantErr: []string{
				`GLOSSA_MIGRATE: must be one of off, up, only (got "sideways")`,
				`GLOSSA_LOG_LEVEL: must be one of debug, info, warn, error (got "loud")`,
				`GLOSSA_SHUTDOWN_TIMEOUT: invalid duration "soon"`,
				"GLOSSA_OUTBOX_BATCH_SIZE: must be between 1 and 1000",
				`GLOSSA_OUTBOX_ENABLED: invalid boolean "maybe"`,
			},
		},
		{
			name: "lease must outlast the handler timeout",
			env: map[string]string{
				"DATABASE_URL":                  "postgres://app@db/glossa",
				"GLOSSA_OUTBOX_LEASE":           "5s",
				"GLOSSA_OUTBOX_HANDLER_TIMEOUT": "10s",
			},
			wantErr: []string{"GLOSSA_OUTBOX_LEASE: must be longer than GLOSSA_OUTBOX_HANDLER_TIMEOUT"},
		},
		{
			name: "non-positive durations are rejected",
			env: map[string]string{
				"DATABASE_URL":            "postgres://app@db/glossa",
				"GLOSSA_SHUTDOWN_TIMEOUT": "0s",
			},
			wantErr: []string{"GLOSSA_SHUTDOWN_TIMEOUT: must be positive"},
		},
		{
			name: "db connect timeout is bounded",
			env: map[string]string{
				"DATABASE_URL":              "postgres://app@db/glossa",
				"GLOSSA_DB_CONNECT_TIMEOUT": "1h",
			},
			wantErr: []string{"GLOSSA_DB_CONNECT_TIMEOUT: must be at most 10m0s"},
		},
		{
			name: "db connect timeout must be positive",
			env: map[string]string{
				"DATABASE_URL":              "postgres://app@db/glossa",
				"GLOSSA_DB_CONNECT_TIMEOUT": "0s",
			},
			wantErr: []string{"GLOSSA_DB_CONNECT_TIMEOUT: must be positive"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(env(tc.env))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q\ndoes not contain %q", err, want)
				}
			}
		})
	}
}

func TestStringRedactsSecrets(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":           "postgres://glossa_app:hunter2@db:5432/glossa",
		"GLOSSA_AUTH_SECRET":     testSecret,
		"MIGRATION_DATABASE_URL": "postgres://owner:s3cr3t@db:5432/glossa",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, s := range []string{cfg.String(), cfg.DatabaseURL.String(), cfg.MigrationDatabaseURL.String()} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "s3cr3t") {
			t.Errorf("secret leaked: %s", s)
		}
	}
	if got := cfg.DatabaseURL.Reveal(); !strings.Contains(got, "hunter2") {
		t.Errorf("Reveal() = %q, want the original DSN", got)
	}
}

// MCP is off by default: a deployment gains an agent surface (RFC 0005
// §7) by asking for one, never by upgrading.
func TestMCPDefaultsOff(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCP.Enabled {
		t.Error("GLOSSA_MCP_ENABLED defaults to on")
	}
	if !strings.Contains(cfg.String(), "mcp=false") {
		t.Errorf("String() = %q, want it to say mcp=false", cfg.String())
	}

	base["GLOSSA_MCP_ENABLED"] = "true"
	cfg, err = config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MCP.Enabled || cfg.MCP.SessionTimeout != 30*time.Minute || cfg.MCP.Rate != 120 || cfg.MCP.Burst != 240 {
		t.Errorf("mcp = %+v, want it enabled with its documented defaults", cfg.MCP)
	}
}

// Audit exports are off by default and refuse to start without their
// own key (RFC 0006 §6.2): there is no fallback derived from the auth
// secret.
func TestAuditExportKey(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	cfg, err := config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.ExportsEnabled || !cfg.Audit.SigningKey.IsZero() {
		t.Errorf("audit = %+v, want exports off and no key", cfg.Audit)
	}

	base["GLOSSA_AUDIT_EXPORTS_ENABLED"] = "true"
	_, err = config.Load(env(base))
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_AUDIT_SIGNING_KEY: required when GLOSSA_AUDIT_EXPORTS_ENABLED is true") {
		t.Fatalf("exports without a key: %v", err)
	}

	base["GLOSSA_AUDIT_SIGNING_KEY"] = "a=AAAA,b=BBBB"
	if _, err = config.Load(env(base)); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("two active keys: %v", err)
	}

	base["GLOSSA_AUDIT_SIGNING_KEY"] = "audit-2026=c2VlZA"
	base["GLOSSA_AUDIT_RETIRED_KEYS"] = "audit-2025=cHVi"
	cfg, err = config.Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Audit.ExportsEnabled || cfg.Audit.SigningKey.Reveal() != "audit-2026=c2VlZA" || cfg.Audit.RetiredKeys != "audit-2025=cHVi" {
		t.Errorf("audit = %+v", cfg.Audit)
	}
	if strings.Contains(cfg.String(), "c2VlZA") || strings.Contains(cfg.Audit.SigningKey.String(), "c2VlZA") {
		t.Error("the audit signing key leaks through String")
	}
}

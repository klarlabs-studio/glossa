package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	err := run(context.Background(), nil, lookupFrom(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want a DATABASE_URL error", err)
	}
}

func TestMigrateFlagOverridesEnv(t *testing.T) {
	// -migrate=up without MIGRATION_DATABASE_URL must fail validation,
	// proving the flag reached the config.
	err := run(context.Background(), []string{"-migrate=up"},
		lookupFrom(map[string]string{"DATABASE_URL": "postgres://app@localhost/glossa"}), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "MIGRATION_DATABASE_URL") {
		t.Fatalf("err = %v, want MIGRATION_DATABASE_URL required", err)
	}
}

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, lookupFrom(nil), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Error("no version printed")
	}
}

func TestUnknownFlag(t *testing.T) {
	if err := run(context.Background(), []string{"-nope"}, lookupFrom(nil), &bytes.Buffer{}); err == nil {
		t.Error("unknown flag accepted")
	}
}

// Audit exports turned on without their key stop the server at startup
// with a message that says which variable and why (RFC 0006 §6.2).
func TestAuditExportsNeedTheirKey(t *testing.T) {
	err := run(context.Background(), nil, lookupFrom(map[string]string{
		"DATABASE_URL":                 "postgres://app@localhost/glossa",
		"GLOSSA_AUDIT_EXPORTS_ENABLED": "true",
	}), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_AUDIT_SIGNING_KEY: required when GLOSSA_AUDIT_EXPORTS_ENABLED is true") {
		t.Fatalf("err = %v, want the audit key required", err)
	}
}

func TestAuditKeys(t *testing.T) {
	seed := func(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }
	load := func(vars map[string]string) (config.Config, error) {
		vars["DATABASE_URL"] = "postgres://app@localhost/glossa"
		vars["GLOSSA_AUTH_SECRET"] = seed(9)
		return config.Load(lookupFrom(vars))
	}

	cfg, err := load(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if keys, err := newAuditKeys(cfg); err != nil || keys != nil {
		t.Fatalf("no key configured: %v, %v", keys, err)
	}

	retired := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32)).Public().(ed25519.PublicKey)
	cfg, err = load(map[string]string{
		"GLOSSA_AUDIT_SIGNING_KEY":  "audit-2026=" + seed(1),
		"GLOSSA_AUDIT_RETIRED_KEYS": "audit-2025=" + base64.StdEncoding.EncodeToString(retired),
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := newAuditKeys(cfg)
	if err != nil || keys.Active().ID != "audit-2026" || len(keys.PublicKeys()) != 2 {
		t.Fatalf("keys = %+v, %v", keys, err)
	}

	for name, vars := range map[string]map[string]string{
		"a short seed":            {"GLOSSA_AUDIT_SIGNING_KEY": "a=" + base64.StdEncoding.EncodeToString([]byte("short"))},
		"a malformed retired key": {"GLOSSA_AUDIT_SIGNING_KEY": "a=" + seed(1), "GLOSSA_AUDIT_RETIRED_KEYS": "b=AAAA"},
		"the release key reused":  {"GLOSSA_AUDIT_SIGNING_KEY": "a=" + seed(3), "GLOSSA_RELEASE_SIGNING_KEYS": "r=" + seed(3)},
	} {
		cfg, err := load(vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := newAuditKeys(cfg); !errors.Is(err, auditdomain.ErrInvalidKey) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

//go:build integration || system

package v0test

// Fixed ids of the seed.
const (
	TenantID     = "a0000000-0000-0000-0000-000000000001"
	OtherTenant  = "a0000000-0000-0000-0000-000000000002"
	ProjectID    = "b0000000-0000-0000-0000-000000000001"
	AliceID      = "c0000000-0000-0000-0000-00000000000a"
	BobID        = "c0000000-0000-0000-0000-00000000000b"
	CarolID      = "c0000000-0000-0000-0000-00000000000c"
	TrEnCart     = "e0000000-0000-0000-0000-000000000003"
	TrEnHome     = "e0000000-0000-0000-0000-000000000004"
	GoneTrID     = "e0000000-0000-0000-0000-0000000000ff"
	CartDesc     = "Cart badge; count is the number of items"
	CheckoutDesc = "Pay button, amount in EUR"
)

// Seed is a v0.3 tenant "klarlabs" with project "brotwerk" (source de;
// en; fr disabled), three keys with descriptions, translations written
// by a person, by v0.3's AI translator and by nobody v0.3 recorded,
// three users (an admin, a translator for en, a translator with no
// locales), a change history including a row whose translation is gone,
// and the rows the import does not carry. A second tenant has its own
// "brotwerk" and history that must never leak into klarlabs' import.
// Written against apps/api's schema as its migrations leave it (0006).
const Seed = `
INSERT INTO tenants (id, slug, name) VALUES
  ('` + TenantID + `', 'klarlabs', 'Klarlabs'),
  ('` + OtherTenant + `', 'other', 'Other Org');

INSERT INTO projects (id, tenant_id, slug, name, default_locale) VALUES
  ('` + ProjectID + `', '` + TenantID + `', 'brotwerk', 'Brotwerk', 'de'),
  ('b0000000-0000-0000-0000-000000000002', '` + OtherTenant + `', 'brotwerk', 'Other Brotwerk', 'de');

INSERT INTO locales (id, project_id, code, label, enabled) VALUES
  ('d0000000-0000-0000-0000-000000000001', '` + ProjectID + `', 'de', 'Deutsch', true),
  ('d0000000-0000-0000-0000-000000000002', '` + ProjectID + `', 'en', 'English (UK)', true),
  ('d0000000-0000-0000-0000-000000000003', '` + ProjectID + `', 'fr', 'Français', false);

INSERT INTO keys (id, project_id, key, description, first_seen_at) VALUES
  ('f0000000-0000-0000-0000-000000000001', '` + ProjectID + `', 'cart.items', '` + CartDesc + `', '2025-01-01T00:00:00Z'),
  ('f0000000-0000-0000-0000-000000000002', '` + ProjectID + `', 'home.title', NULL, '2025-01-01T00:00:00Z'),
  ('f0000000-0000-0000-0000-000000000003', '` + ProjectID + `', 'checkout.pay', '` + CheckoutDesc + `', '2025-01-02T00:00:00Z');

INSERT INTO users (id, tenant_id, email, password_hash, role, locales, created_at) VALUES
  ('` + AliceID + `', '` + TenantID + `', 'alice@example.com', '\x00', 'admin', '{}', '2025-01-01T00:00:00Z'),
  ('` + BobID + `', '` + TenantID + `', 'bob@example.com', '\x00', 'translator', '{en}', '2025-01-01T00:00:00Z'),
  ('` + CarolID + `', '` + TenantID + `', 'carol@example.com', '\x00', 'translator', '{}', '2025-01-01T00:00:00Z'),
  ('c0000000-0000-0000-0000-0000000000ee', '` + OtherTenant + `', 'eve@other.example', '\x00', 'admin', '{}', '2025-01-01T00:00:00Z');

INSERT INTO translations (id, key_id, locale_id, value, status, updated_by, updated_at) VALUES
  ('e0000000-0000-0000-0000-000000000001', 'f0000000-0000-0000-0000-000000000001', 'd0000000-0000-0000-0000-000000000001',
   '{count, plural, one {# Artikel} other {# Artikel}}', 'approved', '` + AliceID + `', '2025-02-01T09:00:00Z'),
  ('e0000000-0000-0000-0000-000000000002', 'f0000000-0000-0000-0000-000000000002', 'd0000000-0000-0000-0000-000000000001',
   'Willkommen bei Brotwerk', 'approved', '` + AliceID + `', '2025-02-01T09:00:00Z'),
  ('e0000000-0000-0000-0000-000000000005', 'f0000000-0000-0000-0000-000000000003', 'd0000000-0000-0000-0000-000000000001',
   '{amount, number} zahlen', 'approved', NULL, '2025-02-01T09:00:00Z'),
  ('` + TrEnCart + `', 'f0000000-0000-0000-0000-000000000001', 'd0000000-0000-0000-0000-000000000002',
   '{count, plural, one {# item} other {# items}}', 'approved', '` + BobID + `', '2025-02-02T10:30:00Z'),
  ('` + TrEnHome + `', 'f0000000-0000-0000-0000-000000000002', 'd0000000-0000-0000-0000-000000000002',
   'Welcome to Brotwerk', 'ai_translated', NULL, '2025-02-03T08:00:00Z'),
  ('e0000000-0000-0000-0000-000000000006', 'f0000000-0000-0000-0000-000000000002', 'd0000000-0000-0000-0000-000000000003',
   'Bienvenue chez Brotwerk', 'pending', NULL, '2025-02-04T08:00:00Z');

INSERT INTO audit_log (tenant_id, translation_id, before_value, after_value, changed_by, actor_kind, actor_label, changed_at) VALUES
  ('` + TenantID + `', '` + TrEnCart + `', NULL, '{count, plural, other {# items}}', '` + BobID + `', 'user', '', '2025-02-01T10:00:00Z'),
  ('` + TenantID + `', '` + TrEnCart + `', '{count, plural, other {# items}}', '{count, plural, one {# item} other {# items}}', '` + BobID + `', 'user', '', '2025-02-02T10:30:00Z'),
  ('` + TenantID + `', '` + TrEnHome + `', NULL, 'Welcome to Brotwerk', NULL, 'ai', 'openai', '2025-02-03T08:00:00Z'),
  ('` + TenantID + `', '` + GoneTrID + `', 'Alt', 'Neu', NULL, 'user', '', '2025-01-15T12:00:00Z'),
  ('` + OtherTenant + `', NULL, 'secret', 'other tenant', NULL, 'user', '', '2025-01-15T12:00:00Z');

INSERT INTO project_api_keys (project_id, hash, scope, label) VALUES ('` + ProjectID + `', '\x01', 'read', 'site');
INSERT INTO ai_translation_providers (tenant_id, kind, label, model, api_key_ct, api_key_nonce)
  VALUES ('` + TenantID + `', 'openai', 'OpenAI', 'gpt-x', '\x02', '\x03');
INSERT INTO analytics_events (tenant_id, project_id, kind) VALUES
  ('` + TenantID + `', '` + ProjectID + `', 'key_synced'), ('` + TenantID + `', '` + ProjectID + `', 'consumer_request');
`

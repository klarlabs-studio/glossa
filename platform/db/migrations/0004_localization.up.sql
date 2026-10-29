-- 0004 — Localization: locales, fallback graphs, translations and their
-- append-only revision log, plus Localization's projection of Catalog's
-- messages.
--
-- Every table is tenant-owned under the standard isolation policy. None
-- references a catalog_* table: contexts never touch each other's tables
-- (RFC 0002 §4). Localization learns about projects and messages from
-- Catalog's events and application ports, and keeps what it needs in
-- localization_messages.
--
-- The revision log is append-only by grant: glossa_app may SELECT and
-- INSERT localization_translation_revisions, never UPDATE or DELETE
-- them (deleting a translation cascades as the owner, which only a
-- project's deletion does).

-- ── locales ────────────────────────────────────────────────────────

CREATE TABLE localization_locales (
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        NOT NULL,
    -- Canonical BCP 47 (RFC 5646 §4.5), no extensions or private use.
    code       text        NOT NULL CHECK (char_length(code) BETWEEN 2 AND 35),
    is_source  boolean     NOT NULL DEFAULT false,
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, code)
);
CREATE UNIQUE INDEX localization_locales_one_source ON localization_locales (project_id) WHERE is_source;
CREATE INDEX localization_locales_tenant ON localization_locales (tenant_id);

-- The project's fallback graph (intent §39), exactly as manifests carry
-- it: {"de-AT": ["de"], "*": ["en"]}. Validated by the domain.
CREATE TABLE localization_fallback_graphs (
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        PRIMARY KEY,
    edges      jsonb       NOT NULL CHECK (jsonb_typeof(edges) = 'object'),
    version    integer     NOT NULL CHECK (version > 0),
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX localization_fallback_graphs_tenant ON localization_fallback_graphs (tenant_id);

-- ── message projection ─────────────────────────────────────────────
-- What Localization knows of each Catalog message: enough to derive
-- "outdated" (a translation's source_revision < source_revision here)
-- and to list missing and outdated messages. Every catalog.message.*
-- event carries a full snapshot with the message's version; a row is
-- replaced only by a higher version, so events apply in any order and
-- any number of times.

CREATE TABLE localization_messages (
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    message_id      uuid        PRIMARY KEY,
    project_id      uuid        NOT NULL,
    key             text        NOT NULL CHECK (char_length(key) <= 200),
    namespace       text        NOT NULL,
    state           text        NOT NULL CHECK (state IN ('active', 'obsolete')),
    source_revision integer     NOT NULL CHECK (source_revision > 0),
    version         integer     NOT NULL CHECK (version > 0),
    updated_at      timestamptz NOT NULL
);
-- Not unique: two renames delivered out of order can briefly share a key.
CREATE INDEX localization_messages_project_key ON localization_messages (project_id, key text_pattern_ops);
CREATE INDEX localization_messages_tenant ON localization_messages (tenant_id);

-- ── translations ───────────────────────────────────────────────────
-- A translation is the projection of its latest revision; there is no
-- stored "outdated" status (it is derived, see above).

CREATE TABLE localization_translations (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id      uuid        NOT NULL,
    message_id      uuid        NOT NULL,
    locale          text        NOT NULL CHECK (char_length(locale) BETWEEN 2 AND 35),
    syntax          text        NOT NULL CHECK (syntax IN ('mf1', 'mf2')),
    text            text        NOT NULL CHECK (octet_length(text) <= 20000),
    model           jsonb       NOT NULL CHECK (jsonb_typeof(model) = 'object'),
    state           text        NOT NULL CHECK (state IN ('draft', 'needs_review', 'approved', 'rejected')),
    origin          text        NOT NULL CHECK (origin IN ('human', 'ai', 'translation_memory',
                                                           'machine_translation', 'import', 'adaptation')),
    author          text        NOT NULL,
    source_revision integer     NOT NULL CHECK (source_revision > 0),
    warnings        jsonb       NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(warnings) = 'array'),
    revision        integer     NOT NULL CHECK (revision > 0),
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    UNIQUE (message_id, locale)
);
CREATE INDEX localization_translations_project_locale ON localization_translations (project_id, locale, state);
CREATE INDEX localization_translations_tenant ON localization_translations (tenant_id);

CREATE TABLE localization_translation_revisions (
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    translation_id  uuid        NOT NULL REFERENCES localization_translations (id) ON DELETE CASCADE,
    revision        integer     NOT NULL CHECK (revision > 0),
    kind            text        NOT NULL CHECK (kind IN ('content', 'review')),
    syntax          text        NOT NULL CHECK (syntax IN ('mf1', 'mf2')),
    text            text        NOT NULL CHECK (octet_length(text) <= 20000),
    model           jsonb       NOT NULL CHECK (jsonb_typeof(model) = 'object'),
    state           text        NOT NULL CHECK (state IN ('draft', 'needs_review', 'approved', 'rejected')),
    -- Provenance (intent §22): how, the specifics, who, and against
    -- which source revision.
    origin          text        NOT NULL CHECK (origin IN ('human', 'ai', 'translation_memory',
                                                           'machine_translation', 'import', 'adaptation')),
    origin_detail   jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(origin_detail) = 'object'),
    author          text        NOT NULL,
    source_revision integer     NOT NULL CHECK (source_revision > 0),
    findings        jsonb       NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(findings) = 'array'),
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (translation_id, revision)
);
CREATE INDEX localization_translation_revisions_tenant ON localization_translation_revisions (tenant_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['localization_locales', 'localization_fallback_graphs', 'localization_messages',
                             'localization_translations', 'localization_translation_revisions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, DELETE ON localization_locales TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON localization_fallback_graphs TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON localization_messages TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON localization_translations TO glossa_app;
GRANT SELECT, INSERT ON localization_translation_revisions TO glossa_app;

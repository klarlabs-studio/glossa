-- 0007 — Knowledge (RFC 0003 §2): translation memory derived from
-- approved translations, the termbase (concepts and terms) and style
-- guides, each with its history.
--
-- Every table is tenant-owned under the standard isolation policy, with
-- an optional project scope (project_id NULL is tenant-wide). None
-- references another context's tables: Knowledge learns about
-- translations from Localization's events and application port.
--
-- pg_trgm powers fuzzy TM matching (similarity) and substring search
-- (concordance, term search) through GIN trigram indexes. It ships with
-- PostgreSQL's contrib modules — in the official postgres images and
-- CloudNativePG's operand images — and is a trusted extension (PG 13+),
-- so the database owner that runs migrations may create it without
-- superuser rights. A cluster built without contrib must add it first.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ── translation memory ─────────────────────────────────────────────
-- One row per unit, active or retired. A unit derived from a
-- translation names it and the revision it was derived from; when that
-- translation is approved with other text, loses its approval or gets
-- unapproved text, the unit is retired, never deleted: retired rows are
-- the TM's history. At most one unit per translation is active.

CREATE TABLE knowledge_tm_units (
    id                   uuid        PRIMARY KEY,
    tenant_id            uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id           uuid,
    origin               text        NOT NULL CHECK (origin IN ('translation', 'import')),
    translation_id       uuid,
    translation_revision integer     CHECK (translation_revision > 0),
    message_id           uuid,
    message_key          text        NOT NULL DEFAULT '' CHECK (char_length(message_key) <= 200),
    namespace            text        NOT NULL DEFAULT '' CHECK (char_length(namespace) <= 64),
    source_locale        text        NOT NULL CHECK (char_length(source_locale) BETWEEN 2 AND 35),
    target_locale        text        NOT NULL CHECK (char_length(target_locale) BETWEEN 2 AND 35),
    -- Canonical MF2 syntax of both sides; the target's data model for
    -- structural reuse.
    source_mf2           text        NOT NULL CHECK (octet_length(source_mf2) <= 100000),
    target_mf2           text        NOT NULL CHECK (octet_length(target_mf2) <= 100000),
    target_model         jsonb       NOT NULL CHECK (jsonb_typeof(target_model) = 'object'),
    -- Normalized text: placeholders by position ({1}), markup as tags.
    source_normalized    text        NOT NULL,
    target_normalized    text        NOT NULL,
    source_hash          text        NOT NULL CHECK (char_length(source_hash) = 64),
    -- Placeholder types by position: "1:number/plural,2:string".
    signature            text        NOT NULL,
    -- Variable names by position, to rename a reused target's variables.
    source_vars          jsonb       NOT NULL CHECK (jsonb_typeof(source_vars) = 'array'),
    hit_count            integer     NOT NULL DEFAULT 0 CHECK (hit_count >= 0),
    last_hit_at          timestamptz,
    created_by           text        NOT NULL,
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    retired_at           timestamptz,
    retired_reason       text        CHECK (retired_reason IN ('superseded', 'unapproved', 'overwritten', 'deleted')),
    retired_by           text,
    CHECK ((retired_at IS NULL) = (retired_reason IS NULL)),
    CHECK (origin <> 'translation' OR (translation_id IS NOT NULL AND translation_revision IS NOT NULL))
);
CREATE INDEX knowledge_tm_units_tenant ON knowledge_tm_units (tenant_id);
CREATE INDEX knowledge_tm_units_project ON knowledge_tm_units (project_id);
CREATE INDEX knowledge_tm_units_translation ON knowledge_tm_units (translation_id);
CREATE UNIQUE INDEX knowledge_tm_units_one_active ON knowledge_tm_units (translation_id)
    WHERE retired_at IS NULL AND translation_id IS NOT NULL;
-- Exact matches: the normalized text's hash in a locale pair.
CREATE INDEX knowledge_tm_units_exact ON knowledge_tm_units (source_locale, target_locale, source_hash)
    WHERE retired_at IS NULL;
-- Fuzzy matches (%, similarity) and concordance (ILIKE) on either side.
CREATE INDEX knowledge_tm_units_source_trgm ON knowledge_tm_units USING gin (source_normalized gin_trgm_ops)
    WHERE retired_at IS NULL;
CREATE INDEX knowledge_tm_units_target_trgm ON knowledge_tm_units USING gin (target_normalized gin_trgm_ops)
    WHERE retired_at IS NULL;

-- The latest translation revision each translation's units reflect, so
-- a subscriber that reads an older state than one already applied
-- changes nothing (events arrive in any order and more than once).
CREATE TABLE knowledge_tm_derivations (
    translation_id uuid        PRIMARY KEY,
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id     uuid        NOT NULL,
    revision       integer     NOT NULL CHECK (revision >= 0),
    updated_at     timestamptz NOT NULL
);
CREATE INDEX knowledge_tm_derivations_tenant ON knowledge_tm_derivations (tenant_id);
CREATE INDEX knowledge_tm_derivations_project ON knowledge_tm_derivations (project_id);

-- ── termbase ───────────────────────────────────────────────────────
-- A concept is the aggregate; its terms are replaced with it. Every
-- version is a snapshot in knowledge_concept_revisions, which outlives
-- the concept (no foreign key): deleting a concept records a final
-- 'deleted' revision.

CREATE TABLE knowledge_concepts (
    id          uuid        PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id  uuid,
    definition  text        NOT NULL DEFAULT '',
    domain      text        NOT NULL DEFAULT '' CHECK (char_length(domain) <= 100),
    note        text        NOT NULL DEFAULT '',
    product_ref text        NOT NULL DEFAULT '' CHECK (char_length(product_ref) <= 200),
    version     integer     NOT NULL CHECK (version > 0),
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_by  text        NOT NULL,
    updated_at  timestamptz NOT NULL
);
CREATE INDEX knowledge_concepts_tenant ON knowledge_concepts (tenant_id);
CREATE INDEX knowledge_concepts_project ON knowledge_concepts (project_id);

CREATE TABLE knowledge_terms (
    id             uuid    PRIMARY KEY,
    tenant_id      uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    concept_id     uuid    NOT NULL REFERENCES knowledge_concepts (id) ON DELETE CASCADE,
    position       integer NOT NULL CHECK (position >= 0),
    locale         text    NOT NULL CHECK (char_length(locale) BETWEEN 2 AND 35),
    text           text    NOT NULL CHECK (char_length(text) BETWEEN 1 AND 200),
    status         text    NOT NULL CHECK (status IN ('preferred', 'admitted', 'deprecated', 'forbidden')),
    part_of_speech text    NOT NULL DEFAULT ''
                           CHECK (part_of_speech IN ('', 'noun', 'verb', 'adjective', 'adverb', 'proper_noun', 'phrase', 'other')),
    case_sensitive boolean NOT NULL DEFAULT false,
    note           text    NOT NULL DEFAULT '',
    UNIQUE (concept_id, position)
);
CREATE INDEX knowledge_terms_tenant ON knowledge_terms (tenant_id);
CREATE INDEX knowledge_terms_locale ON knowledge_terms (locale, concept_id);
CREATE INDEX knowledge_terms_text_trgm ON knowledge_terms USING gin (text gin_trgm_ops);

CREATE TABLE knowledge_concept_revisions (
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    concept_id uuid        NOT NULL,
    project_id uuid,
    version    integer     NOT NULL CHECK (version > 0),
    action     text        NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    -- The concept with its terms as the API renders it.
    snapshot   jsonb       NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    author     text        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (concept_id, version)
);
CREATE INDEX knowledge_concept_revisions_tenant ON knowledge_concept_revisions (tenant_id);
CREATE INDEX knowledge_concept_revisions_project ON knowledge_concept_revisions (project_id);

-- ── style guides ───────────────────────────────────────────────────
-- One guide per scope (tenant, project, locale, namespace in any
-- combination; a namespace needs a project). Every version is kept in
-- knowledge_style_guide_versions, which outlives the guide.

CREATE TABLE knowledge_style_guides (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid,
    locale     text        CHECK (char_length(locale) BETWEEN 2 AND 35),
    namespace  text        CHECK (char_length(namespace) BETWEEN 1 AND 64),
    name       text        NOT NULL DEFAULT '',
    fields     jsonb       NOT NULL CHECK (jsonb_typeof(fields) = 'object'),
    rules      jsonb       NOT NULL CHECK (jsonb_typeof(rules) = 'array'),
    version    integer     NOT NULL CHECK (version > 0),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (namespace IS NULL OR project_id IS NOT NULL)
);
CREATE UNIQUE INDEX knowledge_style_guides_scope ON knowledge_style_guides (tenant_id, project_id, locale, namespace)
    NULLS NOT DISTINCT;
CREATE INDEX knowledge_style_guides_project ON knowledge_style_guides (project_id);

CREATE TABLE knowledge_style_guide_versions (
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    style_guide_id uuid        NOT NULL,
    version        integer     NOT NULL CHECK (version > 0),
    action         text        NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    project_id     uuid,
    locale         text,
    namespace      text,
    name           text        NOT NULL,
    fields         jsonb       NOT NULL CHECK (jsonb_typeof(fields) = 'object'),
    rules          jsonb       NOT NULL CHECK (jsonb_typeof(rules) = 'array'),
    author         text        NOT NULL,
    created_at     timestamptz NOT NULL,
    PRIMARY KEY (style_guide_id, version)
);
CREATE INDEX knowledge_style_guide_versions_tenant ON knowledge_style_guide_versions (tenant_id);
CREATE INDEX knowledge_style_guide_versions_project ON knowledge_style_guide_versions (project_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['knowledge_tm_units', 'knowledge_tm_derivations', 'knowledge_concepts',
                             'knowledge_terms', 'knowledge_concept_revisions', 'knowledge_style_guides',
                             'knowledge_style_guide_versions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_tm_units TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_tm_derivations TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_concepts TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_terms TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_style_guides TO glossa_app;
-- History is append-only: no UPDATE. DELETE is only for erasing a
-- deleted project's knowledge (knowledge.drop_project).
GRANT SELECT, INSERT, DELETE ON knowledge_concept_revisions TO glossa_app;
GRANT SELECT, INSERT, DELETE ON knowledge_style_guide_versions TO glossa_app;

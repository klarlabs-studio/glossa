# RFC 0003 — Knowledge and Intelligence (M2)

**Status:** Accepted — 2026-09-19
**Builds on:** [RFC 0002](./0002-platform-architecture.md) §4 (Knowledge and Intelligence contexts), §10, §13 (M2)
**Intent:** §18–§24, §29.3, §54–§56, §62.9–10, §71

## 1. Goal

M2 makes AI translation **amplify accumulated knowledge instead of replacing it** (intent §54). Every approved translation, term and style rule makes the next translation better. Every AI suggestion is structurally valid, explains its confidence, and says which knowledge it used. Humans review by exception.

M2 is done when a catalog in a new locale can be filled by the platform and handed to a reviewer as a short queue of risky items, with everything else routed by policy. The internal exit test is a fixture catalog of ~600 keys (de/en source, es/fr/ja targets, deliberately partial, like the Armada case in RFC 0002 §12). The real Armada migration happens in the dogfood phase.

## 2. Knowledge context

Four separate aggregates (intent §19: never one "AI context" blob), all tenant-owned under forced RLS, with optional project scope.

### 2.1 Translation memory

- A **TM unit** is `(source locale, target locale, source text, target text)`. It records its provenance (the approved translation revision, or a TMX import), its scope (tenant, or one project) and usage (hit count, last used).
- Units are **derived** from approved revisions through the `localization.translation.reviewed` event. People don't curate them by hand. Rejected or overwritten approvals retire their unit instead of deleting it, and history is kept.
- The stored text is the MF2 **pattern text with placeholders normalized** (`{$amount}` → `{1}`, markup kept as tags). Matches then ignore variable names but keep structure. The canonical MF2 message is stored too, so a match can be reused structurally.
- **Matching:**
  - *Exact*: the normalized-text hash plus the same placeholder signature, scoring 100 (101 when neighbouring messages match too).
  - *Fuzzy*: `pg_trgm` similarity over normalized text, scored 50–99, for the top N.
  - *Semantic* (vector search over embeddings) stays behind a port and isn't built in M2. It needs `pgvector`, which is a separate infrastructure decision.
- Import and export TMX 1.4b.

### 2.2 Termbase

- A **concept** has a definition, a domain, a note and an optional product-concept link. Each **term** belongs to one locale and has a status: `preferred`, `admitted`, `deprecated` or `forbidden`. A term also carries part of speech, case sensitivity and usage notes.
- **Term recognition** in source text uses locale-aware tokenization with case-folding (per term's flag) and simple inflection tolerance (prefix match within word boundaries). Stemming is a later port.
- **Terminology QA** (intent §29.3), available as a check the Quality layer and the CLI can run:
  - a source term was found but the translation uses none of the target locale's preferred or admitted terms → `term_missing`
  - the translation uses a forbidden or deprecated target term → `term_forbidden`
- Import and export TBX (TBX-Basic).

### 2.3 Style guides

- Structured, not prose. Fields are formality (`formal`/`informal` plus pronoun, e.g. de `du`/`Sie`), tone tags, punctuation and typography preferences (quotes, dashes, spaces before units), and number and date conventions beyond CLDR.
- Plus a list of **rules**, each with a rationale and good/bad examples.
- Scopes stack **tenant → project → locale → namespace**, and the narrower scope wins field by field. Every guide version is recorded in provenance.

### 2.4 Product knowledge

M2 only models **message context** that already exists (description, max length, usages, neighbours by key prefix, namespace). The product context graph (intent §18) is Phase 2 and later.

## 3. Intelligence context

### 3.1 Providers (model-agnostic, intent §21)

- A **Provider** port with adapters for **Anthropic** (Messages API), **OpenAI-compatible** (covering self-hosted and Mistral-style endpoints) and **Gemini**.
- Credentials are BYO per tenant, sealed at rest with the kernel's secret sealing.
- A **routing policy** (tenant/project, per locale) chooses provider and model by task (`translate`, `review`, `explain`), with an ordered fallback.
- Defaults when a tenant uses Anthropic: `claude-sonnet-5` for translate and review, `claude-haiku-4-5-20251001` for cheap checks. They're configurable and never hard-coded in domain logic.
- `fortify` wraps every provider call: timeout, retry with backoff, a circuit breaker per provider, and a per-tenant rate limit and **budget cap**. The budget is checked before each job and recorded after.

### 3.2 The translation agent

- Built on `agent-go` as a state machine: `gather → draft → validate → (repair ≤ 2) → assess → done`, with an audit ledger and budget enforcement.
- The planner has an `axi-go` toolset, each tool scoped to the job's tenant and project:

| Tool | Returns |
|---|---|
| `tm_lookup` | Exact and fuzzy matches with scores and provenance |
| `term_lookup` | Concepts and terms recognized in the source, with target terms and status |
| `style_rules` | The effective style guide for the target locale and namespace |
| `message_context` | Description, max length, arguments, usages, neighbouring messages |
| `validate` | `messageformat.CheckCompat` on the draft, plus the terminology QA check and max length |

- **Drafts are MF2 data-model messages.** The model gets the source as MF2 syntax and must answer in MF2 syntax, which is parsed with the Go kernel. Plural and select variants come from the **target** locale's CLDR categories (a German source's `one`/`*` becomes Polish `one`/`few`/`many`/`*`); the prompt lists them explicitly.
- **Structural validity is a hard gate** (RFC 0002 §10). A draft that fails `CheckCompat` goes back to the model with the findings, at most twice. Then the job fails with `invalid_output` and never becomes a translation.
- An exact TM hit (100/101) with no terminology findings is reused **without a model call**, with provenance `translation_memory`.
- **Prompts are versioned artifacts** in the repo (`platform/internal/intelligence/prompts/<task>/<version>.tmpl`), and the version is recorded in provenance.

### 3.3 Confidence and review routing (intent §23–24)

- **Signals:** TM match level, terminology findings, structural repairs needed, the model's self-assessment (a structured field, weighted lightly), length ratio against the target locale's norms and `max_length`, and message risk (legal/marketing namespaces and markup density are policy-tagged).
- `decisionkit` turns the signals into a score in `[0, 1]` plus an **explanation** listing each factor and its contribution. Both are stored with the suggestion.
- A **per-project routing policy** maps score bands to actions:
  - `auto_approve`: off by default, and only allowed for environments whose policy accepts `approved`
  - `approve_recommended`
  - `review_required`
- The **review queue** is ordered by risk, not by key.
- Human edits to AI output are recorded as structured diffs (character edit distance plus which terms and style fields changed). They feed the M2 metrics (acceptance rate, edit distance per locale). Turning corrections into proposed terms or style rules is Phase 5 (intent §55): suggested, never applied without approval.

### 3.4 Jobs and triggers

- An **Intelligence job** is created from outbox events:
  - `catalog.message.created` and `localization.translation.outdated`, for locales with auto-translate on
  - `localization.locale.added`, a batch fill
  - an explicit API request (Studio "Fill with AI" or the CLI)
- Jobs are rows in a queue claimed with `FOR UPDATE SKIP LOCKED` and processed by outbox-style workers. They're idempotent by `(message, locale, source_revision, knowledge fingerprint)`, and concurrency is capped per tenant and per provider.
- The result is a **Suggestion**, stored separately. A suggestion becomes a translation revision (provenance `ai` or `translation_memory`, with `origin_detail`: provider, model, prompt version, TM unit IDs, term IDs, style-guide version, score and explanation) according to the routing policy, or waits in the review queue.

## 4. Evals as code

- **Golden sets** live in `platform/internal/intelligence/evals/testdata/<locale-pair>/*.json`: source, context, expected properties (required terms, forbidden terms, structure, formality) and optionally reference translations.
- **CI:** the agent runs against **recorded provider responses** (cassettes), so tests are deterministic and cost nothing. A cassette mismatch fails loudly with instructions for re-recording.
- **Live mode:** `glossa-server eval --live --provider …` (or a Go test with a build tag) runs against real providers. It reports per locale: structural pass rate, terminology compliance, formality compliance, edit distance to references and cost. A prompt or model change must not regress the tracked metrics.

## 5. Interchange

Converters live in `platform/internal/integration/formats`, as pure functions tested by round-trips:
- **XLIFF 2.1** in and out, with MF2 through the XLIFF `mf2` mapping where possible, and ICU text otherwise
- **TMX 1.4b** in and out
- **TBX-Basic** in and out
- **JSON** (flat and nested `{key: ICU}`) in and out
- **gettext PO** in only (M2 scope)

## 6. Surfaces

- **API:** knowledge CRUD and search, provider configuration, routing policy, jobs (create, list, cancel), suggestions (accept, edit and accept, reject), the review queue, eval results (read), and imports/exports as async jobs.
- **Studio:**
  - TM and terms panes in the editor, with term highlighting and violations
  - the AI suggestion showing its confidence and explanation, with "why this?"
  - a review queue view
  - termbase and style-guide editors
  - provider settings and budget
  - import and export
- **CLI:** `glossa tm import|export|search`, `glossa terms import|export|check`, `glossa translate --locale ja [--dry-run]`, and `glossa import|export --format xliff|tmx|tbx|json|po`.

## 7. Privacy and data handling (intent §50)

Sending text to a provider is an explicit per-tenant setting, recorded per job: which provider saw which message IDs. It's disabled by default and enabled through provider configuration. Messages under namespaces tagged `sensitive` are never sent to a provider and always need humans. The data a provider receives is exactly what the agent's tools returned, and the audit ledger stores it.

## 8. Work breakdown

| Wave | Task | Touches the API spec |
|---|---|---|
| 1 | Knowledge context (TM, termbase, style guides, terminology QA) + API | yes |
| 1 | Intelligence core: provider port and adapters, agent and tools against ports, confidence, prompts, evals with cassettes (library only) | no |
| 1 | Interchange converters (XLIFF 2.1, TMX, TBX, JSON, PO), library only | no |
| 2 | Intelligence wiring: jobs, triggers, suggestions, routing, provider config, budgets; import/export jobs + API | yes |
| 3 | Studio M2 + CLI M2 | no (consume) |

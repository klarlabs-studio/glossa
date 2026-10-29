# Glossa positioning brief

Revised 2026-09-19. The first version (2026-05-25) positioned Glossa as a self-hosted translation backend for indie EU SaaS teams. [`docs/product-intent.md`](./product-intent.md) replaces that: Glossa becomes **localization infrastructure**. This brief turns the intent into positioning. Where they disagree, the intent wins.

This is the authoritative source for the README hero, the GHCR description and the npm package descriptions. Reflect changes here in those surfaces within the same PR.

The brief follows April Dunford's five components: alternatives → capabilities → value → best-fit customer → category.

## Shipped vs direction

Positioning must never claim what doesn't exist yet. Everything below is marked **shipped** (in v0.3) or with the rewrite milestone that delivers it (**M1**, **M2**, …; see [RFC 0002](./rfcs/0002-platform-architecture.md) §13).

## Early Customer Profile (ECP)

**Small product teams (1–10 engineers) that ship one product across more than one surface — a web frontend plus a backend that sends email, PDFs or notifications — in two or more languages, and that have already felt the cost of adding a language by hand.**

Glossa has about one paying user (Felix, across Brotwerk and IRI). The ECP is the first 20 teams. The ICP comes from patterns across those 20.

What makes this the ECP:
- **Burning pain.** Each new locale means re-touching strings across the frontend *and* the backend. Frontend i18n libraries don't cover the backend, and TMS tools don't cover the runtime.
- **Willingness to pay.** They already pay for infrastructure: a managed database, a Hetzner box, LLM API credits.
- **Proximity.** Reachable through the open-source and self-hosting communities Glossa already lives in.
- **Willingness to recommend.** This crowd shares tools openly.

Not a fit *yet*:
- **Organizations that need vendor management, SSO/SCIM or configurable approval workflows.** That's intent Phase 4. The architecture leaves room for it (intent §49), but the product doesn't serve them today.
- **English-only apps.** Nothing to localize.

## The 5 positioning components (Dunford)

### 1. Competitive alternatives

| Alternative | What the ECP hates about it |
|---|---|
| **i18n library + JSON files in git** (the default) | Every locale change is a PR and a deploy. Frontend and backend keep separate catalogs. No context for translators. Adding a language is an engineering project. |
| **Lokalise / Crowdin / Phrase** | File sync bolted onto the repo. Translations are files, not product data. Cloud-only, with per-word or per-seat pricing that grows with success. |
| **Tolgee** (closest OSS option) | Strong in-context editing, but still a TMS attached to the app, not the runtime. No typed messages, no backend runtime. |
| **Build your own** | Months away from the product that pays the bills. |

### 2. Differentiated capabilities

1. **One message model across every runtime.** Typed messages (`messages.checkout.pay({ amount })`) on the web, the same messages through `i18n.T(ctx, …)` in Go. One MessageFormat semantics, one conformance suite. *Shipped: web components and TS SDK. M1: typed accessors, Vue/Astro and the Go runtime.*
2. **AI translation grounded in your knowledge, with provenance.** BYO provider (OpenAI, Anthropic, Gemini, OpenAI-compatible). *Shipped:* automatic fan-out with per-row attribution. *M2:* translation memory, terminology, structural validation, confidence, and a record of the model, prompt and knowledge used for each translation.
3. **Immutable releases, resilient delivery.** Publish, promote and roll back per environment. Content-addressed bundles keep serving when the control plane is down. *Shipped:* live updates over SSE and fallback-first rendering. *M1:* releases, environments, delivery plane and fallback graph.
4. **You own it.** MIT-licensed and self-hostable (Helm chart, Docker Compose). Tenant isolation is enforced by Postgres RLS. Your data, your LLM keys. *Shipped.*

### 3. Differentiated value

- **The next language is configuration, not a project.** This is the metric that matters (intent §70): the engineering effort to ship one more language should keep falling.
- **Translators get context engineers never wrote down.** Source locations and usage first. Screenshots and live preview come in Phase 2.
- **Localization behaves like CI.** `glossa check` fails the build on broken placeholders or missing translations (M1, full policies in M4) instead of users finding them in production.
- **No lock-in.** Standard formats in and out, and your own database.

### 4. Best-fit target customer

- **Persona:** the tech lead of a small product team shipping in two or more languages, who owns both the frontend and the backend that sends localized email.
- **Trigger:** "we're adding a third language," or "our German invoice email is still in English."
- **Where they live:** open-source and self-hosting communities, and the dev communities around Go, TypeScript and web components.

### 5. Market category

**Localization infrastructure.**

Not "translation management," a category defined by files and translator workflows. Glossa sits where Stripe sits for payments or Sentry for errors: infrastructure your code integrates once, with a professional workspace for the humans who work in it.

## Three sentences to land on

These appear verbatim in the README hero, the GHCR image description and the npm package descriptions:

1. **For product teams who want every new language to be configuration, not an engineering project,**
2. **Glossa is open-source localization infrastructure: one typed message model for web and backend, AI translation grounded in your terminology and translation memory, and immutable releases delivered to every runtime.**
3. **Unlike file-centric translation tools, Glossa treats translations as versioned product data you own: self-hosted, with your own LLM keys.**

## Out of scope

From intent §61. What Glossa is not, even when asked nicely:

- **Not a translation file editor.** Files are how data gets in and out, not the product.
- **Not an LLM wrapper.** The value is context, memory, terminology, quality and delivery, not the model call.
- **Not only a CAT tool.** A professional translator workspace is one part of the platform.
- **Not only a frontend i18n library.** Backend, mobile and generated documents matter equally.
- **Not a replacement for language experts.** AI handles the predictable work and surfaces uncertainty. Humans own judgment.
- **Not a standards inventor.** BCP 47, CLDR and MessageFormat first.

## Riskiest assumption

> "Teams will adopt a new localization runtime (typed messages plus releases) in place of the i18n library and JSON files they already have."

Validation plan:
1. **Dogfood.** Move Brotwerk (the M1 pilot), then KraftSport, frontend *and* backend, onto the new runtime. Measure engineering hours to add one more locale before and after. That number is the headline claim or it's nothing.
2. **Second-team test.** Onboard one team outside Felix's projects. Time to the first localized environment is the target (intent §66: minutes, not days).
3. **If adoption stalls at the runtime,** lead with the pieces that don't require a runtime switch (`glossa check` in CI, knowledge-aware AI translation over existing catalogs) and let the runtime follow.

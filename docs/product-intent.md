# Product Intent

> **Build once. Speak everywhere.**

## 1. Purpose of This Document

This document defines the product intent, principles, architecture boundaries, core concepts, and long-term direction of the product.

It exists primarily to guide:

- coding agents,
- engineers,
- product designers,
- AI agents,
- technical architects,
- localization specialists,
- and future contributors.

When implementation details are ambiguous, decisions should be made in a way that moves the system toward the product described here.

This is not an implementation specification.

Individual technical design documents may define how particular capabilities are implemented. Those designs must remain consistent with the intent and principles defined here.

## 2. Product Vision

The product is a universal localization platform for software.

Its purpose is to make internationalization and localization a foundational infrastructure capability rather than a collection of translation files, manual workflows, and framework-specific integrations.

A product team should implement localization once.

After that, adding another language should primarily be a localization operation—not an engineering project.

The desired experience is:

```text
Build application
      ↓
Integrate localization runtime once
      ↓
Messages are automatically discovered
      ↓
Product context is captured
      ↓
Translations are generated and managed
      ↓
Quality is verified
      ↓
Human experts review uncertainty
      ↓
Translations are published
      ↓
Every application receives them
```

The long-term goal is:

> Adding the 40th language should require approximately the same engineering effort as adding the 4th language: nearly zero.

Human linguistic and cultural expertise remains important.

Repeated engineering work should not.

## 3. Product Thesis

Most localization systems originated from a file-centric model:

```text
translation files
    ↓
upload
    ↓
translation project
    ↓
translator
    ↓
download
    ↓
repository
```

Modern software needs a different abstraction:

```text
application
    ↓
messages
    ↓
context
    ↓
localization intelligence
    ↓
translations
    ↓
quality
    ↓
release
    ↓
runtime delivery
```

The fundamental product thesis is:

> **Translations are not files. They are structured, contextual, versioned product data.**

JSON, YAML, PO, XLIFF, Android XML, Apple strings, gettext catalogs, and similar formats are compatibility representations.

They are not the canonical domain model.

The platform owns a universal message representation from which these formats may be imported, exported, generated, or synchronized.

## 4. What We Are Building

The product combines six traditionally separate systems:

1. Localization runtime
2. Developer platform
3. Translation environment
4. Linguistic knowledge system
5. AI localization engine
6. Translation delivery infrastructure

These components form one continuous system.

```text
                        PRODUCT CONTEXT
                              │
                              ▼
                        MESSAGE REGISTRY
                              │
             ┌────────────────┼────────────────┐
             │                │                │
             ▼                ▼                ▼
      Translation Memory   Terminology     Style Rules
             │                │                │
             └────────────────┼────────────────┘
                              │
                              ▼
                       AI Localization
                              │
                              ▼
                      Translation Studio
                              │
                              ▼
                           QA / LQA
                              │
                              ▼
                      Release Management
                              │
                              ▼
                        Global Delivery
                              │
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                 ▼
          Web               Mobile           Backend
```

The system must work equally well for frontend, backend, mobile, generated documents, transactional communications, and other software surfaces.

## 5. Primary Product Promise

The platform should allow a team to:

> **Implement localization once and make languages configuration.**

Adding a language should eventually feel like:

```text
Add language

Japanese
日本語

Messages
2,847

Translation Memory
✓ Apply

Terminology
✓ Apply

Product Context
✓ Apply

Style Guide
✓ Apply

AI Translation
✓ Enabled

AI Quality Review
✓ Enabled

Visual QA
✓ Enabled

[Add Japanese]
```

The platform then performs:

```text
Analyze context
      ↓
Apply existing translations
      ↓
Apply terminology
      ↓
Apply locale style
      ↓
Generate missing translations
      ↓
Run linguistic QA
      ↓
Run structural QA
      ↓
Run visual QA
      ↓
Identify uncertainty
      ↓
Request human review where necessary
```

Engineering intervention should normally not be required.

## 6. Target Users

The platform serves several personas simultaneously.

### 6.1 Developers

Developers want localization to behave like modern infrastructure.

They care about:

- excellent SDKs,
- type safety,
- performance,
- framework integration,
- CI/CD,
- local development,
- predictable APIs,
- versioning,
- observability,
- reliability,
- and minimal application complexity.

Localization should not force developers to become localization experts.

### 6.2 Translators

Professional translators need a serious translation environment.

They care about:

- context,
- translation memory,
- terminology,
- concordance,
- efficient keyboard workflows,
- linguistic QA,
- comments,
- history,
- visual previews,
- confidence signals,
- and precise control over translations.

The translator interface must not be a simplified spreadsheet designed primarily for developers.

### 6.3 Localization Managers

Localization managers need operational control.

They care about:

- language coverage,
- workflows,
- assignments,
- approvals,
- translation quality,
- terminology consistency,
- releases,
- vendors,
- costs,
- auditability,
- and progress.

Localization should become observable and measurable.

### 6.4 Product and Design Teams

Product managers and designers should be able to inspect and improve localized experiences without navigating translation files.

They need:

- in-product editing,
- screenshots,
- live previews,
- character constraints,
- copy review,
- locale comparison,
- and visual QA.

### 6.5 AI and Coding Agents

Agents are first-class users of the platform.

They should be able to:

- discover messages,
- create messages,
- inspect terminology,
- inspect localization rules,
- query translation state,
- run localization checks,
- add languages,
- generate translations,
- diagnose localization problems,
- and interact through stable APIs and agent-oriented interfaces.

The platform should be designed for a future in which much software development is agent-mediated.

## 7. Core Domain Primitive: Message

The primary domain object is the Message.

A message represents a piece of localizable product communication.

Example:

```text
checkout.pay
```

A message is not merely:

```text
key → translated string
```

It contains structured semantics.

Conceptually:

```text
Message
├── ID
├── source locale
├── source content
├── description
├── variables
├── selectors
├── plural rules
├── formatting requirements
├── markup
├── product context
├── source locations
├── visual context
├── usage locations
├── constraints
├── translations
├── translation provenance
├── QA state
├── review state
├── release state
└── history
```

Messages must remain stable independently of any particular storage format.

## 8. Message Identity

Messages require durable identities.

Example:

```text
checkout.payment.submit
```

Message identity must not depend on translated content.

Changing:

```text
Pay now
```

to:

```text
Complete payment
```

must not implicitly create an unrelated message unless explicitly requested.

Stable identities allow the platform to retain:

- translation history,
- translation memory,
- terminology associations,
- context,
- screenshots,
- reviews,
- quality data,
- and usage information.

## 9. Message Semantics

The canonical message system must support:

- interpolation,
- numbers,
- currencies,
- dates,
- times,
- durations,
- plurals,
- ordinals,
- grammatical selection,
- markup,
- nested formatting,
- locale-aware functions,
- and extensibility.

The product should align with open internationalization standards wherever practical.

Unicode CLDR and MessageFormat semantics should strongly influence the model.

Do not invent proprietary pluralization or formatting semantics when established standards solve the problem.

## 10. Typed Messages

Messages should be type-safe wherever the host language allows it.

Example:

```ts
messages.checkout.pay({
  amount: order.total
})
```

If the message requires:

```text
amount: Money
```

then:

```ts
messages.checkout.pay({})
```

should fail during development or compilation.

Generated APIs should provide:

- message discovery,
- argument types,
- autocomplete,
- compile-time validation,
- and refactoring support.

Stringly typed localization APIs should remain available for dynamic use cases but should not be the preferred developer experience.

## 11. Universal Runtime

All SDKs should expose the same conceptual localization model.

Supported ecosystems should eventually include:

```text
Web
├── JavaScript
├── TypeScript
├── React
├── Vue
├── Next.js
├── Nuxt
├── Svelte
└── other major frameworks

Backend
├── Go
├── Node.js
├── Java
├── Kotlin
├── Python
├── Rust
├── .NET
└── others

Mobile
├── Swift
├── Kotlin
├── Flutter
└── React Native
```

Language-specific SDKs may feel idiomatic.

Their semantics must remain consistent.

A developer moving between Go, React, and Swift should recognize the same conceptual localization system.

## 12. Backend Localization Is First-Class

Localization is not limited to browser interfaces.

The same platform must support:

- transactional email,
- push notifications,
- SMS,
- API-generated text,
- PDFs,
- invoices,
- receipts,
- reports,
- exports,
- scheduled jobs,
- server-side rendering,
- command output,
- and other backend-generated communication.

Example:

```go
message := i18n.T(
    ctx,
    "invoice.payment_received",
    i18n.Args{
        "amount": payment.Amount,
        "date": payment.Date,
    },
)
```

Frontend and backend must use the same message definitions and translation knowledge.

## 13. Locale Resolution

Locale selection should be a reusable platform capability.

Applications should be able to configure resolution strategies such as:

```text
explicit request locale
        ↓
user preference
        ↓
organization preference
        ↓
request metadata
        ↓
Accept-Language
        ↓
application default
```

Locale resolution must be deterministic, inspectable, and configurable.

Applications should not repeatedly implement locale negotiation themselves.

## 14. Locale Intelligence

Localization extends beyond strings.

The runtime should provide correct locale-aware behavior for:

- numbers,
- currencies,
- percentages,
- dates,
- times,
- time zones,
- durations,
- relative time,
- units,
- measurement systems,
- pluralization,
- ordinals,
- lists,
- calendars,
- names,
- addresses,
- sorting,
- collation,
- week boundaries,
- scripts,
- and text direction.

Where possible, use CLDR-derived or platform-native locale data rather than maintaining proprietary datasets.

## 15. RTL Is a First-Class Requirement

Right-to-left languages must not be treated as an afterthought.

The platform must understand:

```text
language
script
region
direction
```

as related but distinct concepts.

SDKs should expose directionality and help applications apply it correctly.

The system should eventually detect:

- broken RTL layouts,
- incorrect direction inheritance,
- mixed-direction problems,
- mirrored-layout issues,
- and visual regressions.

## 16. Context Is Core Product Data

A translation without context is incomplete.

Every message should accumulate context over time.

Possible context includes:

```text
source file
line/location
component
route
screen
feature
surrounding messages
DOM position
visual hierarchy
screenshot
rendered dimensions
character constraints
product concept
Git commit
branch
application
platform
```

Context should be captured automatically whenever possible.

Developers should not have to manually describe every message.

## 17. Context Telemetry

SDKs and development tooling should be capable of collecting localization-specific telemetry.

For example:

```text
message:
checkout.pay

component:
PaymentFooter > PrimaryButton

route:
/checkout/payment

source:
src/checkout/PaymentFooter.tsx

neighbors:
Payment method
Total
Cancel

available width:
182px

visual context:
[screenshot]
```

This information improves:

- translator understanding,
- AI translation,
- visual QA,
- source copy linting,
- and debugging.

Context telemetry must respect privacy and deployment policies.

Production collection must be explicitly controlled.

## 18. Product Context Graph

The platform should evolve beyond isolated messages.

It should understand product concepts.

Example:

```text
Workspace
│
├── is a product concept
├── contains Projects
├── contains Members
├── has Owners
│
└── appears in
    ├── Dashboard
    ├── Settings
    ├── Invitations
    └── Billing
```

This creates a Product Context Graph.

The graph connects:

```text
concepts
messages
terminology
screens
components
features
translations
documentation
style rules
```

AI localization should eventually operate over this graph rather than individual strings.

## 19. Linguistic Knowledge Layer

The platform maintains several distinct linguistic knowledge systems.

They must not be collapsed into a single generic “AI context” blob.

### 19.1 Translation Memory

Translation Memory stores previously accepted translations.

Example:

```text
Create account
→
Konto erstellen
```

Translation memory should support:

- exact matches,
- fuzzy matches,
- semantic matches,
- provenance,
- locale pairs,
- project scope,
- organization scope,
- confidence,
- usage,
- and lifecycle management.

Human-approved translations should become valuable reusable knowledge.

### 19.2 Terminology

Terminology represents concepts rather than sentences.

Example:

```text
Concept:
Workspace

Definition:
Shared environment containing projects and members.

German:
Arbeitsbereich

Forbidden:
Workspace
Projektbereich

Status:
approved
```

Terminology should support:

- preferred terms,
- forbidden terms,
- definitions,
- grammatical metadata,
- locale-specific variants,
- product scopes,
- approvals,
- and history.

Terminology compliance should be machine-checkable.

### 19.3 Style Guides

Style guides define how the product communicates.

Example:

```text
Locale:
de-DE

Address:
informal

Pronoun:
du

Tone:
concise
friendly
technical
direct

CTA:
prefer imperative verbs

Avoid:
unnecessary Anglicisms
corporate language
```

Style guides may exist at organization, product, application, locale, and feature scope.

AI systems must treat style rules as explicit constraints.

### 19.4 Product Knowledge

Product knowledge explains what things mean.

This includes:

- concepts,
- domain terminology,
- feature relationships,
- user roles,
- product behavior,
- and relevant documentation.

Translation systems should understand that the same English word can require different translations depending on product semantics.

## 20. AI Localization

AI is not a bolt-on translation button.

AI is part of the localization execution engine.

Translation generation should combine:

```text
source message
      +
message structure
      +
product context
      +
visual context
      +
neighboring messages
      +
translation memory
      +
terminology
      +
style guide
      +
locale conventions
      +
previous human corrections
```

before generating a translation.

The system should translate meaning in context, not isolated strings.

## 21. AI Must Be Model-Agnostic

The product should not be architecturally dependent on one AI provider or model.

An AI orchestration layer should support:

- multiple providers,
- specialized translation models,
- general LLMs,
- customer-provided models,
- self-hosted models where appropriate,
- quality-based routing,
- cost-based routing,
- locale-based routing,
- and fallback strategies.

Model selection is an implementation concern.

Localization knowledge belongs to the platform.

## 22. Translation Provenance

Every translation should record how it was created.

Examples:

```text
human
translation_memory
machine_translation
ai
import
adaptation
```

Additional provenance may include:

```text
model
model version
prompt/configuration version
context used
TM matches
terminology version
style guide version
creator
reviewer
timestamps
```

Users must be able to understand why a translation exists.

## 23. Confidence and Uncertainty

The system should estimate translation uncertainty.

Confidence must never be presented as unquestionable truth.

Instead, it should help prioritize review.

Conceptually:

```text
Very high confidence
→ eligible for automatic approval

High confidence
→ normal automated QA

Medium confidence
→ human review recommended

Low confidence
→ human review required
```

Policies must remain configurable.

Different organizations have different risk tolerances.

## 24. Human Review by Exception

The goal of AI is not to remove humans from localization.

The goal is to focus human expertise where it provides the most value.

Instead of requiring translators to inspect every message, the system might identify:

```text
41 messages require linguistic judgment
7 terminology conflicts
3 ambiguous source messages
2 visual issues
```

Professional translators become language owners, reviewers, and decision-makers rather than mechanical processors of predictable segments.

## 25. Translator Studio

The translator experience must be professional-grade.

It should support:

- keyboard-first workflows,
- translation memory,
- fuzzy matches,
- semantic matches,
- concordance search,
- terminology highlighting,
- terminology violations,
- spell checking,
- grammar checking,
- QA warnings,
- comments,
- mentions,
- review states,
- history,
- diffs,
- bulk operations,
- filtering,
- character constraints,
- screenshots,
- live context,
- alternative translations,
- AI suggestions,
- AI explanations,
- shortening,
- expansion,
- formal/informal adaptation,
- and locale variants.

Developer simplicity must never come at the expense of translator productivity.

## 26. Translation Should Happen Inside the Product

Where technically possible, users should be able to interact directly with localizable content inside a running application.

A modifier-click on localizable content should open localization controls for that message.

Users should be able to:

- inspect translations,
- edit translations,
- view history,
- add context,
- inspect terminology,
- request AI suggestions,
- comment,
- and open the full translator workspace.

This capability should work particularly well in development, preview, and staging environments.

## 27. Live Product Preview

Static screenshots are useful but insufficient.

The long-term target is interactive translation inside real product context.

Translators should be able to see source, translation, surrounding interface, layout constraints, neighboring messages, and actual formatting simultaneously.

Changing a translation should update the preview immediately whenever possible.

## 28. Source Copy Intelligence

Localization quality begins with source text.

The platform should detect problematic source copy before translation.

Examples:

```text
"Delete it"
⚠ Ambiguous pronoun.

"3 item(s)"
✗ Manual pluralization.

"Last updated: " + date
✗ Localized sentence constructed through concatenation.

"Open"
⚠ Ambiguous without context.
```

Source linting should integrate with IDEs, CLI, CI, pull requests, and the web application.

## 29. Quality Assurance

Localization QA should be layered.

The system should distinguish at least:

```text
structural QA
linguistic QA
terminology QA
semantic QA
visual QA
runtime QA
```

### 29.1 Structural QA

Detect missing arguments, extra arguments, invalid selectors, invalid MessageFormat, malformed markup, unsupported constructs, broken placeholders, and incompatible message structures.

### 29.2 Linguistic QA

Detect or flag likely mistranslations, grammar issues, spelling issues, inconsistent phrasing, inappropriate tone, and source/target semantic divergence.

### 29.3 Terminology QA

Detect missing required terminology, forbidden terms, inconsistent concept translation, and terminology drift.

### 29.4 Visual QA

Detect clipping, overflow, overlapping elements, excessive wrapping, untranslated strings, mixed locales, RTL issues, missing glyphs, formatting errors, and suspicious layout changes.

Visual QA should eventually operate automatically against real application surfaces.

## 30. Localization as CI

Localization must integrate into normal engineering workflows.

Example:

```text
$ localization check

✓ 2,847 messages discovered
✓ message structures valid
✓ arguments valid
✓ terminology valid
✓ German complete
✓ French complete
✓ Spanish complete

✗ Japanese
  checkout.payment_failed
  Missing translation

✗ Arabic
  dashboard.invite
  RTL visual regression

Localization check failed.
```

Projects should configure policies.

Localization should feel like testing, linting, observability, and deployment—not external project management.

## 31. Git Integration

Git is an important integration surface.

It is not necessarily the canonical translation database.

Support GitHub, GitLab, Bitbucket, branches, pull requests, webhooks, CI, and CLI workflows.

Two primary deployment models should be supported: build-time catalogs and runtime delivery.

Teams may choose either or combine them.

## 32. Compiler

Where appropriate, SDK ecosystems should include compile-time tooling.

The compiler can discover message usages and produce metadata such as source location, arguments, component, and application usage.

The compiler should enable:

- message discovery,
- type generation,
- validation,
- dead-message detection,
- source mapping,
- bundle optimization,
- and context extraction.

## 33. Performance

Localization must not meaningfully degrade application performance.

Frontend implementations should support:

- locale chunking,
- tree shaking,
- lazy loading,
- caching,
- preloading,
- server rendering,
- streaming,
- and minimal runtime overhead.

Applications should not have to download an organization’s entire translation database.

## 34. Delivery Plane

Runtime translation delivery is infrastructure.

Published translations should become immutable artifacts.

```text
Translation Release
       ↓
Immutable Bundles
       ↓
Global Edge
       ↓
SDK Cache
       ↓
Application
```

The delivery plane should prioritize availability, correctness, latency, and rollback safety.

The management application being unavailable must never prevent an application from displaying previously published translations.

## 35. Control Plane and Delivery Plane Separation

The architecture should explicitly distinguish the control plane from the delivery plane.

A failure in the control plane must not propagate into customer runtime failures.

## 36. Translation Releases

Translations must be versioned and releasable.

Releases should be:

- immutable,
- auditable,
- reproducible,
- rollbackable,
- and environment-aware.

## 37. Environments

Support development, preview, staging, and production as first-class concepts.

Applications should be able to consume different translation releases in different environments.

Preview environments should integrate naturally with branch-based development.

## 38. Over-the-Air Updates

Applications should be able to receive translation changes without complete application deployments where platform constraints permit.

OTA capabilities should support:

- immutable versions,
- caching,
- ETags or equivalent validation,
- staged rollout,
- instant rollback,
- local persistence,
- offline operation,
- signed artifacts,
- and deterministic fallback.

Translation updates must never create an unnecessary single point of failure.

## 39. Fallback

Fallback is a graph, not merely a default language.

Example:

```text
fr-CA
 ↓
fr
 ↓
en-CA
 ↓
en
```

Fallback rules should be configurable.

The runtime must make fallback behavior observable so developers can diagnose why a particular message was displayed.

## 40. Regional Adaptation

Regional language variants should not always require independent translation from the source language.

Examples:

```text
en-US → en-GB
fr-FR → fr-CA
es-ES → es-MX
pt-PT → pt-BR
```

The platform should support adaptation workflows that preserve shared linguistic knowledge while allowing regional differences.

## 41. Language Capability Model

Do not pretend every language has equal AI, linguistic, or human-resource support.

Distinguish runtime support, formatting support, AI translation quality, AI QA quality, human review availability, visual QA support, and regional adaptation support.

Runtime architecture should remain as language-agnostic as possible.

## 42. Workflow Engine

Translation workflows must be configurable.

Do not encode one organization’s localization process into the core domain model.

## 43. Quality Is Observable

Localization should have measurable operational health.

Metrics should exist at organization, project, application, locale, release, and message levels where appropriate.

## 44. Translation History

Translations are versioned data.

Never overwrite important linguistic decisions without history.

Users should be able to understand what changed, who changed it, when, why, what source version it corresponded to, what context was used, and what was published.

Historical releases should remain reproducible.

## 45. APIs First

Every important platform capability should ultimately be automatable.

The product should expose APIs around messages, locales, translations, releases, terminology, memory, style guides, context, QA, workflows, projects, applications, and environments.

Avoid important capabilities that only exist as manual dashboard actions.

## 46. CLI

The CLI is a first-class developer interface.

Expected capabilities include:

```text
init
login
pull
push
extract
check
compile
status
release
diff
locales
messages
```

Commands should work naturally in both interactive terminals and CI.

Machine-readable output should be available.

## 47. Agent Interface

Coding and AI agents should be able to operate the platform without scraping UI.

Provide agent-friendly interfaces through APIs and, where useful, protocols such as MCP.

The platform should return structured, explainable answers.

## 48. Import and Export

Users own their linguistic data.

Support common localization standards and formats, including where appropriate:

```text
XLIFF
TMX
TBX
PO
JSON
YAML
CSV
Android XML
Apple formats
MessageFormat
```

Import/export exists for migration, interoperability, backups, external vendors, and legacy systems.

It must not dictate the internal architecture.

## 49. Enterprise Requirements

The architecture must leave room for:

- organizations,
- teams,
- projects,
- applications,
- RBAC,
- SSO/SAML,
- SCIM,
- audit logs,
- data residency,
- retention policies,
- encryption,
- customer-managed keys,
- private model routing,
- self-hosted model options,
- vendor management,
- approval policies,
- cost attribution,
- and compliance requirements.

Do not prematurely implement every enterprise feature.

Do not make architectural decisions that make them impossible.

## 50. Security and Privacy

Localization data may contain unreleased product copy, customer communication templates, legal text, and confidential terminology.

Treat localization content as potentially sensitive customer data.

AI integrations must make data handling explicit.

## 51. Reliability Principle

A localization service outage must not cause customer applications to become unusable.

SDKs and delivery infrastructure should prefer:

```text
cached current translation
        ↓
previous valid translation
        ↓
configured fallback locale
        ↓
source message
```

over runtime failure.

Localization infrastructure should degrade gracefully.

## 52. Developer Experience Principle

The developer experience should aspire to the quality associated with modern developer infrastructure products.

Installation should be obvious.

Configuration should be minimal.

Errors should explain what happened, where, why, and how to fix it.

Documentation should be executable and example-driven.

The common path must be extremely simple.

Complexity should be available when needed rather than imposed by default.

## 53. Translator Experience Principle

Translators should receive more context than they would receive from the engineering team manually.

The platform should continuously ask:

> What information would help a language expert understand exactly what this message means here?

The system should capture that information automatically whenever possible.

## 54. AI Principle

AI must amplify accumulated organizational knowledge.

Do not design AI translation as a source-string-to-LLM-to-translation pipeline.

Design it around product knowledge, product context, translation memory, terminology, style guides, visual context, locale knowledge, AI translation, AI QA, and human judgment.

The surrounding knowledge system is more strategically important than any individual model.

## 55. Human Corrections Are Learning Signals

When a qualified reviewer changes an AI translation into an approved translation, the system should eventually understand what changed, why it likely changed, whether terminology changed, whether style changed, whether similar messages exist, and whether future translation behavior should adapt.

Human review should improve future output.

Do not blindly fine-tune or globally propagate corrections.

Learning must respect scope, locale, product, terminology, and explicit approvals.

## 56. The Long-Term Data Moat

The strategic asset is not the translation editor.

It is the organization’s multilingual product knowledge.

Over time the platform understands:

- what product concepts mean,
- how the company talks,
- how each locale expresses those concepts,
- which terminology is approved,
- which phrases humans reject,
- why corrections happen,
- where messages appear,
- how much UI space they have,
- which translations cause visual problems,
- and which language decisions are regional.

This becomes a continuously improving multilingual representation of the customer’s product.

The product should be architected to accumulate this knowledge safely and structurally.

## 57. Product Objects

The domain will likely contain concepts similar to:

```text
Organization
├── Workspace
│   ├── Project
│   │   ├── Application
│   │   ├── Environment
│   │   ├── Locale
│   │   ├── Message
│   │   │   ├── Translation
│   │   │   ├── Context
│   │   │   └── Usage
│   │   ├── Release
│   │   └── Workflow
│   │
│   ├── Translation Memory
│   ├── Termbase
│   ├── Style Guide
│   └── Product Knowledge
```

This diagram expresses conceptual relationships only.

Coding agents must not infer database tables directly from it.

Domain models should be designed deliberately through technical design work.

## 58. Product Surfaces

The product will eventually expose several major surfaces:

- Developer Platform
- Translation Platform
- Localization Operations
- Runtime Infrastructure
- Intelligence

These surfaces share one domain model.

They are not independent products connected by exports.

## 59. Initial Product Wedge

Do not attempt to reproduce every enterprise TMS capability before proving the core product.

The strongest initial wedge is:

```text
Universal localization runtime
             +
Excellent SDK/compiler experience
             +
Automatic context capture
             +
AI localization
             +
Professional translator interface
             +
Simple production delivery
```

The initial product should demonstrate that localization can be dramatically simpler without sacrificing professional translation quality.

## 60. Suggested Product Evolution

This is directional rather than a committed roadmap.

### Phase 1 — Foundation

Prove the core loop:

```text
code
 ↓
message
 ↓
translation
 ↓
release
 ↓
runtime
```

Focus on:

- canonical message model,
- TypeScript/web SDK,
- one backend SDK,
- extraction/compiler,
- locales,
- basic translator editor,
- AI translation,
- terminology,
- basic translation memory,
- API,
- CLI,
- immutable releases,
- runtime delivery,
- caching,
- fallback.

### Phase 2 — Context

Make localization context-aware.

Add:

- automatic context capture,
- screenshots,
- live preview,
- in-product editing,
- source locations,
- neighboring messages,
- style guides,
- product knowledge,
- source copy linting,
- richer AI context.

### Phase 3 — Quality

Make localization trustworthy at scale.

Add:

- automated LQA,
- confidence,
- review by exception,
- visual QA,
- terminology enforcement,
- quality dashboards,
- CI policies,
- source/target semantic checks.

### Phase 4 — Operations

Support sophisticated organizations.

Add:

- workflow engine,
- assignments,
- approvals,
- vendors,
- enterprise permissions,
- SSO,
- audit logs,
- advanced release policies,
- data residency.

### Phase 5 — Localization Intelligence

Move toward autonomous localization operations.

The system should increasingly be able to detect new product content, understand context, translate it, validate it, identify uncertainty, route exceptions, publish safe changes, observe results, and learn from corrections.

Humans remain responsible for linguistic and product judgment where needed.

## 61. Non-Goals

### We Are Not Building a Translation File Editor

Files are interoperability mechanisms.

They are not the core product abstraction.

### We Are Not Building an LLM Wrapper

Calling a translation model is easy.

The value lies in context, linguistic memory, terminology, workflow, quality, runtime integration, delivery, and learning.

### We Are Not Building Only a CAT Tool

Professional translation tooling is necessary.

It is one part of a larger localization infrastructure platform.

### We Are Not Building Only a Frontend i18n Library

Frontend localization is one runtime surface.

Backend, mobile, communications, documents, and other surfaces matter equally.

### We Are Not Replacing Human Language Expertise

AI should automate predictable work and surface uncertainty.

Humans remain essential for cultural judgment, brand language, ambiguity, sensitive content, legal language, creative language, and high-impact communication.

### We Are Not Inventing Standards Without Need

Prefer established standards and locale datasets when they solve the problem.

Innovation should happen where existing abstractions are insufficient.

## 62. Architectural Decision Principles

When coding agents encounter ambiguity, use the following principles.

1. **Messages over files** — Model localization around messages and semantics, not serialization formats.
2. **Structured data over opaque strings** — If information has useful semantics, represent those semantics explicitly.
3. **Context over guessing** — Capture context rather than forcing humans or models to infer it.
4. **Standards over proprietary syntax** — Use Unicode, CLDR, BCP 47, MessageFormat, and established localization standards where appropriate.
5. **Compile-time safety where possible** — Catch localization defects before production.
6. **Runtime resilience** — Localization failures must degrade gracefully.
7. **Immutable releases** — Published localization state must be reproducible and rollbackable.
8. **APIs over UI-only capabilities** — Important functionality must be automatable.
9. **Human judgment over false certainty** — AI uncertainty should be surfaced, not hidden.
10. **Knowledge accumulation over stateless generation** — Every approved translation should make the system more useful.
11. **One conceptual model across platforms** — React, Go, Swift, Kotlin, and future SDKs should represent the same localization concepts.
12. **Separation of control and delivery** — Translation management failures must not become application runtime failures.

## 63. Coding Agent Guidance

When implementing functionality, agents should ask:

1. Does this treat messages as the canonical abstraction?
2. Does this work beyond one framework?
3. Are we preserving useful context?
4. Are we introducing proprietary behavior where a standard exists?
5. Can the behavior eventually be exposed through the API?
6. Can the behavior be automated?
7. Is important state versioned?
8. Can users understand where a translation came from?
9. Does failure degrade safely?
10. Does this architecture still make sense with 100 locales?
11. Does it still make sense with millions of messages?
12. Does it work for both humans and agents?
13. Are we accumulating reusable localization knowledge?
14. Does this reduce or increase engineering work required to add another language?

If an implementation makes the 40th language significantly harder than the 4th language, reconsider the design.

## 64. UX Decision Principle

Whenever there is tension between exposing localization complexity and hiding it, follow this rule:

> **Simple by default, explicit when necessary.**

A developer should normally write:

```ts
messages.checkout.pay({
  amount
})
```

They should not need to understand translation bundles, fallback graphs, plural categories, CDN versions, TM matches, or release manifests for ordinary use.

But those systems must remain inspectable when debugging or advanced configuration requires them.

## 65. Product Quality Bar

The product should feel like infrastructure that happens to solve localization.

The expected qualities are:

```text
fast
predictable
typed
observable
versioned
automatable
recoverable
explainable
interoperable
```

The translator experience should simultaneously feel:

```text
contextual
efficient
professional
trustworthy
language-native
```

Neither side should feel like an afterthought.

## 66. North-Star User Journey

A new team should eventually be able to:

1. Create project
2. Install SDK
3. Replace product strings with messages
4. Run application
5. Platform discovers messages and context
6. Select target languages
7. Platform creates initial translations
8. AI and automated QA identify uncertainty
9. Translators review relevant exceptions
10. Team publishes release
11. Applications receive translations
12. Future source changes automatically enter the same lifecycle

The first successful localized environment should be achievable in minutes, not days.

## 67. North-Star Developer Experience

Idealized:

```bash
npm install @product/react
```

```tsx
import { messages } from "@product/react";

export function Checkout({ total }) {
  return (
    <Button>
      {messages.checkout.pay({ amount: total })}
    </Button>
  );
}
```

Then:

```bash
product dev
```

The system should be capable of discovering message identity, component, source, arguments, visual context, and application usage without repetitive configuration.

## 68. North-Star Translator Experience

A translator opens a message and immediately sees:

- source,
- target translation,
- live product context,
- surrounding UI,
- terminology,
- translation-memory matches,
- AI recommendations,
- alternatives,
- and quality checks.

The translator should rarely need to ask:

> “Where does this string appear?”

The platform should already know.

## 69. North-Star Language Launch

Launching a locale should eventually look like:

```text
Japanese

2,847 messages

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Translation Memory applied      841
AI translated                 1,963
Human review required            43

Structural QA                   ✓
Terminology QA                  ✓
Linguistic QA                   ✓
Visual QA                       ✓

Coverage                     100%
High-confidence              98.5%

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

43 messages need review.

[Review]

[Publish to staging]
```

This is the product promise made tangible.

## 70. Success Metrics

Useful product metrics may include:

### Engineering

- time to first localized application,
- engineering time required per added locale,
- SDK integration time,
- localization-related production incidents,
- runtime localization latency.

### Localization

- translation coverage,
- time from source change to localized production,
- human-reviewed percentage,
- translation reuse,
- terminology compliance,
- QA failure rate.

### AI

- human acceptance rate,
- edit distance after AI generation,
- review-required percentage,
- false-confidence rate,
- quality by locale,
- cost per translated message.

### Product

The most important long-term metric is approximately:

> **How much human and engineering effort is required to safely ship one additional language?**

That number should continuously decrease.

## 71. Strategic Moat

The moat should not depend primarily on proprietary translation models.

Models will improve and commoditize.

The durable advantages should come from:

```text
developer integration
        +
runtime adoption
        +
product context
        +
translation memory
        +
terminology
        +
style knowledge
        +
human correction history
        +
quality data
        +
delivery infrastructure
        +
workflow integration
```

Together these create a localization knowledge layer deeply integrated with how the customer’s software is built and operated.

## 72. Ultimate Product Direction

The long-term system should behave like an autonomous localization layer attached to software development.

A source change should cause the platform to:

```text
source changed
       ↓
affected translations identified
       ↓
existing linguistic knowledge retrieved
       ↓
product context retrieved
       ↓
translations updated
       ↓
quality evaluated
       ↓
uncertain locales routed for review
       ↓
safe translations prepared
       ↓
release created
       ↓
CI validates localized application
       ↓
production release
```

The engineering team should not manually coordinate this process.

## 73. Final Product Principle

The product exists to make language a native capability of software.

Not a post-processing step.

Not a folder of JSON files.

Not a translation project disconnected from development.

Not a button that sends strings to an LLM.

Instead:

> **Localization should be continuous infrastructure connecting source code, product meaning, language expertise, AI, quality assurance, and runtime delivery.**

A team builds the product once.

The platform makes that product understandable everywhere.

## 74. Decision Hierarchy

When future requirements conflict, prioritize in this order:

```text
1. Correctness and user meaning
2. Runtime reliability
3. Localization quality
4. Developer simplicity
5. Translator productivity
6. Automation
7. Performance
8. Interoperability
9. Operational efficiency
10. Feature breadth
```

Do not sacrifice correctness or runtime safety merely to automate more translation.

Do not sacrifice professional localization quality merely to make the developer API appear simpler.

Do not add feature breadth at the expense of the core end-to-end localization loop.

## 75. North Star

The ultimate test for every major product decision is:

> **Does this move us closer to a world where software teams build once, and language becomes infrastructure?**

If yes, it is aligned with the product.

If it instead introduces another manual translation pipeline, framework-specific localization silo, opaque AI process, or file-centric workflow, reconsider the design.

# Glossa

> **For product teams who want every new language to be configuration, not an engineering project, Glossa is open-source localization infrastructure: one typed message model for web and backend, AI translation grounded in your terminology and translation memory, and immutable releases delivered to every runtime.** Unlike file-centric translation tools, Glossa treats translations as versioned product data you own: self-hosted, with your own LLM keys.

**Build once. Speak everywhere.** Where Glossa is going and why: [`docs/product-intent.md`](./docs/product-intent.md). How it gets there: [RFC 0002 — Platform architecture](./docs/rfcs/0002-platform-architecture.md), a rewrite. v0.3, the service this repository started as, was retired on 2026-10-09.

![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Postgres](https://img.shields.io/badge/Postgres-16-336791?logo=postgresql&logoColor=white)
![Lit](https://img.shields.io/badge/Lit-3-324FFF?logo=lit&logoColor=white)

Glossa is the localization backbone for [Brotwerk](https://brotwerk.felixgeelhaar.de), [IRI](https://github.com/felixgeelhaar/iri), and [Kraftsport](https://kraftsport-coach.de). One deployment, many tenants. Full positioning rationale in [`docs/positioning.md`](./docs/positioning.md).

---

## Project layout

```
glossa/
├── platform/                   # Go platform: /v1 API, workers, CLI (`glossa`), edge
├── messageformat/              # MessageFormat 2 kernel (Go + TS) and conformance suite
├── runtimes/                   # Go and JS runtimes: web components, Vue, React, Astro, capture, overlay
├── studio/                     # Studio, the Vue admin UI
├── site/                       # landing site
├── deploy/charts/glossa-platform/  # Helm chart
├── apps/api/                   # what is left of v0.3 (Go service); see below
├── packages/format/            # what is left of v0.3 (ICU MessageFormat renderer); see below
└── docs/                       # product intent, positioning, RFCs, runbooks
```

`apps/api` and `packages/format` are the only v0.3 code kept. They are fixtures, not a product: the M5 exit test builds v0.3 from `apps/api`, the importer's tests apply its migrations, and `glossa import --from v0 --verify` renders with `packages/format`. They get no other work.

---

## Development

```bash
make platform-test      # Go modules of the rewrite
make packages           # build the JS packages (topological order)
make lint && make test  # what the warden gate runs
```

See [`platform/README.md`](./platform/README.md) for the platform itself.

---

## Roadmap

Glossa is being **rewritten** as the platform the [product intent](./docs/product-intent.md) describes, and the Klarlabs products are the first users. Architecture, milestones and adoption waves: [RFC 0002](./docs/rfcs/0002-platform-architecture.md). v0.3 is retired (production shut down 2026-10-09; its code is deleted except `apps/api` and `packages/format`, below). How projects moved off it: [`docs/runbooks/retire-v0.md`](./docs/runbooks/retire-v0.md).

| Milestone | Delivers | Done when |
|---|---|---|
| M0 Foundations | Server kernel, forced RLS, `auth-go`, MessageFormat 2 kernel (Go + TS) with conformance suite | Conformance + RLS suites green |
| M1 Core loop | Messages → translations → immutable releases → edge → JS / web components / Vue / Astro / Go runtimes, CLI, Studio v0 | Brotwerk runs on it, web and email |
| M2 Knowledge + AI | TM, termbase, style guides, translation agent with provenance and confidence, translator workspace | Armada's missing locales filled by review by exception |
| M3 Context | Usages, in-product editing, preview environments, screenshots, GitHub checks, React | Translators see where every message appears |
| M4 Quality | Layered QA, CI policies, visual QA, Flutter | `glossa check` gates CI in every product |
| M5 Operations | Workflows, assignments, vendors, audit export | All products migrated, v0.3 retired |

Each milestone has its design RFC: [M2 — 0003](./docs/rfcs/0003-knowledge-and-intelligence.md), [M3 — 0004](./docs/rfcs/0004-context.md), [M4 — 0005](./docs/rfcs/0005-quality.md), [M5 — 0006](./docs/rfcs/0006-operations.md).

---

## License

[MIT](LICENSE)

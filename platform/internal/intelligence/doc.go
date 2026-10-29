// Package intelligence is Glossa's Intelligence bounded context (RFC 0003
// §3–§4, §7): AI translation that amplifies accumulated knowledge instead
// of replacing it (intent §54).
//
//   - domain: the model-agnostic provider port and its errors, routing
//     policy, budget port, pricing, the Knowledge ports the agent's tools
//     consume, the Suggestion with its provenance, the confidence score
//     with its explanation and review routing, and the wiring's model:
//     provider configuration, settings, jobs, stored suggestions and edit
//     diffs. Pure.
//   - app: the translation agent (agent-go state machine driving an
//     axi-go toolset), the provider router with fallback and budget
//     guard, validation and the Translator entry point; and the Service
//     that wires it into the platform — configuration, budgets, fills,
//     outbox triggers, the job Worker, suggestions and the review queue.
//   - adapters: Anthropic (official SDK), OpenAI-compatible and Gemini
//     providers, the fortify resilience wrapper, the cassette provider
//     that replays recorded responses, in-memory fakes; and the wiring's
//     Postgres store and claimer, sources (Catalog, Localization,
//     Release, Knowledge), the provider factory, Prometheus metrics and
//     the HTTP edge.
//   - prompts: versioned prompt templates, embedded.
//   - evals: golden sets per locale pair, the eval runner and the tracked
//     baseline.
package intelligence

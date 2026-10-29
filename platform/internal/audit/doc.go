// Package audit is the Audit bounded context (RFC 0006 §6): one place
// that answers "who did what, when", in a form someone who does not
// trust us can check.
//
// It owns audit_entries (migration 0045) and reads nothing else's
// tables; the outbox history it backfills from is the kernel's, read
// through the kernel's own reader.
//
//   - domain: the entry, its canonical encoding and hash chain, the
//     verifier that recomputes it, and the projection table that says
//     how every outbox event type becomes an entry — content-free: ids
//     and selectors verbatim, everything else as its shape (§6.1, §9.5).
//   - app: the outbox subscriber, the Recorder port that sign-ins and
//     MCP tool calls are written through, the backfill of history and a
//     chain verification over stored entries.
//   - adapters/postgres: the append-only table, serialized per tenant.
//
// The read and export API is wave 5; the export file format and
// `glossa audit verify` are wave 4, built on domain.Verifier.
package audit

// Package workflow is the Workflow bounded context (RFC 0006 §2): an
// organisation's localization process as data.
//
// Intent §42 is the constraint the context exists to honour —
// "translation workflows must be configurable; do not encode one
// organisation's localization process into the core domain model" — and
// RFC 0006 §2.1 names what would break it in this codebase:
//
//  1. a fifth review state in localization/domain.ReviewState;
//  2. a process compiled into Go (the default workflow is a document,
//     defaults/review.json, loaded exactly like a tenant's);
//  3. Localization, Release or Intelligence branching on who the tenant
//     is;
//  4. guards or actions that name a process rather than a generic
//     primitive with parameters.
//
// architecture_test.go in this directory is what makes those four
// enforceable rather than a matter of discipline. The layout follows the
// other contexts:
//
//   - domain: the glossa.workflow/v1 document, the closed vocabulary of
//     events, guards and actions, compiling a definition through
//     statekit.FromJSON, lint-on-save, and bindings with the platform's
//     one precedence rule.
//   - app: saving definitions as immutable versions, binding them,
//     resolving the binding for a subject; the instance runner (an
//     outbox subscriber that steps instances, actions run as the
//     triggering actor) and its timer sweep; assignments and approvals.
//   - adapters/postgres: the workflow_* tables (migrations 0040, 0043,
//     0044).
//   - adapters/sources, adapters/identity: the runner's ports onto
//     Catalog, Localization, Quality, Intelligence and Identity.
//   - adapters/release: release requests (RFC 0006 §5.1) — the
//     runner's port onto Release, and Release's port onto approvals.
//   - defaults: the default definitions (review, release approval), as
//     documents.
//
// Workflow depends on the other contexts' ports; none of them depends
// on Workflow.
package workflow

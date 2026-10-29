// Package m4 is Glossa's M4 exit test (RFC 0005 §12): the thing that
// decides whether M4 is done.
//
// It runs the eight numbered criteria of §12 against one real
// glossa-server on Postgres and MinIO, with a fake GitHub on loopback
// and a headless Chrome the test starts, and writes REPORT.md — like
// M2 and M3.
//
// The eight, in the order the test runs them:
//
//  1. A fixture repository with real CI. testdata/repo is the M3
//     fixture application (internal/systemtest/m3/testdata/app) plus
//     what M4 adds: a `.github/workflows` job that runs `glossa check`,
//     a capture plan, an overlay that gives the checkout page a
//     fixed-width pay button, and the seeded defects. The test
//     materializes the repository, pushes it, and saves the project's
//     policy v3 — `de` and `en` complete, `terminology` an error in the
//     `legal` namespace, `visual` in `warn`.
//  2. Findings across layers. The check is run the way the workflow
//     runs it; its `--json` document is validated against
//     glossa.finding/v1, and the layers it found are compared with the
//     nine §12.2 names one by one. The visual layer is real: `glossa
//     capture --check` drives Chrome over the fixture, the Japanese pay
//     button clips, and the test reads the region back through the API
//     and crops the stored screenshot to it.
//  3. The PR check agrees. The same commit, through the fake GitHub,
//     produces a check run whose conclusion, error count and per-layer
//     counts are compared with the CLI's. This is the exit criterion:
//     two surfaces, one verdict.
//  4. Waivers and policy rollout. A waiver with a reason moves a
//     finding to `waived`; the counts change and the conclusion does
//     not; the source revision behind it is changed and it comes back.
//     A policy v4 promoting `visual` from `warn` to `enforce` is saved
//     with `dry_run` first and then with a grace: the open pull request
//     keeps grading against v3 and says so, a new one grades against v4.
//  5. Release gate. Publishing to `production` with `fr` incomplete
//     fails with `policy_not_met`; the forced publish succeeds and is
//     audited with its reason.
//  6. MCP. A read-only token is refused on `translation_propose`; a
//     write token in a write session proposes and it lands in review; a
//     second tenant's token sees nothing of the first's project; a CI
//     token and an in-context grant are refused at connect.
//  7. Flutter. The Dart runtime's conformance suites are run, and the
//     §6.4 size and startup budgets are measured and recorded.
//  8. Dashboard. A Playwright test opens Studio's `quality` view
//     against this server and asserts the seven numbers match the API.
//
// The test never calls an AI provider and never reaches the network:
// the linguistic layer runs on a cassette, the provider is a loopback
// fake, and the GitHub is the one from RFC 0004 §12.
//
// It is behind the `system` build tag and needs Docker, a Chrome, a
// Dart SDK and — for part 8 — a built Studio:
//
//	go test -tags=system -timeout=2700s ./internal/systemtest/m4/...   (or: make system-m4)
//
// REPORT.md is written whether or not the criteria hold, because the
// report is the verdict: a criterion that cannot be met is named in it
// rather than dropped from it. The test then fails if any of the eight
// did not hold.
package m4

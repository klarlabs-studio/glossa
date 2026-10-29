// Package m5 is Glossa's M5 exit test (RFC 0006 §12): the thing that
// decides whether M5 is done.
//
// Unlike M2–M4's, it exists from the milestone's first wave, with every
// criterion written as an assertion against the public surfaces and
// red (RFC 0006 §11.1). Each later wave turns some of it green and says
// which in its merge note. M4's exit test arrived in its last wave and
// found four unbuilt layers and several unjoined seams at once; this is
// how M5 avoids that. So a criterion here fails because the thing it
// checks is missing — and says, in REPORT.md, exactly what is missing —
// never because it was skipped.
//
// The seven, in the order the test runs them:
//
//  7. Earlier exits hold — first, before anything is deployed, because
//     two exit tests running at once collide at sign-in. The M2, M3 and
//     M4 exit tests run unchanged against this build; their verdict
//     lines are copied into the report.
//  1. Two workflows, one event. Project A binds the seeded default
//     definition, project B `vendor-then-four-eyes` (testdata) for
//     `de`; one source revision in both goes down two paths. Three
//     invalid definitions are refused with 422, and §2.1's
//     architecture test runs.
//  2. Vendor visibility on every surface. The vendor's translator —
//     registered with a password, verified through the test mailer —
//     is called on every GET operation generated from
//     platform/api/openapi.yaml, inside and outside an assignment of
//     20 `de` units, plus the MCP read tools; writes outside, export,
//     import and TM search must be refused.
//  3. Release approvals, read at glossa-edge over HTTP: no pointer
//     moves before the second approval; a rollback needs none.
//  4. Staged rollout across three runtimes, each checked id for id
//     against runtimes/testdata/rollout/cohorts.json and generate.py —
//     a fourth implementation — never against each other.
//  5. Audit export, compared with the harness's own log of the calls it
//     made (never the outbox), verified offline, tampered, and searched
//     for the fixture's canary words.
//  6. v0.3 imports and renders the same: v0.3 built from apps/api,
//     migrated, seeded, dumped and restored with the platform's restore
//     script; imported with `--v0-db`; every key in every locale
//     rendered by v0.3's own formatter and by @klarlabs-studio/glossa, two
//     implementations that share no code.
//
// The M5 surfaces it drives (paths, CLI commands, runtime options) do
// not exist when it is written; surfaces_test.go and the drivers under
// testdata/rollout name them as this test reads RFC 0006, and the slice
// that builds each one makes them agree with it.
//
// Nothing here reaches the network or a real AI provider. It is behind
// the `system` build tag and needs Docker, Node with the built
// @klarlabs-studio/glossa and @felixgeelhaar/glossa-format, the Dart SDK,
// Python 3 with the generator's modules, and — for §12.7 — everything
// the M2–M4 exit tests need:
//
//	make system-m5
//
// REPORT.md is written whether or not the criteria hold, because the
// report is the verdict. The test then fails if any of the seven did
// not hold.
package m5

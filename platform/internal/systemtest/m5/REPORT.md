# M5 exit test — report

Written by `TestM5Exit` (`make system-m5`, or `go test -tags=system ./internal/systemtest/m5/...` in
`platform/`) against a real glossa-server on Postgres and MinIO that sends its mail over SMTP to a
capturing test mailer, a real glossa-edge on the same bucket, a v0.3 server built from `apps/api` on its
own Postgres, and the JS, Go and Dart runtimes. Every observation below comes from the public API, the
edge, a runtime or the CLI. RFC 0006 §12.

This test exists from M5's first wave with every criterion written and red (RFC 0006 §11.1): each later
wave turns some of it green, and its merge note says which. A criterion that cannot hold says what is
missing; the steps after the first missing one are listed as *not reached*.

## The verdict

**7 of the 7 exit criteria hold.**

| § | Criterion | Verdict | Fails if |
|---|---|---|---|
| 12.1 | Two workflows, one event | met | B needs a code path A doesn't; an instance strands short of a final state; the author's or a token's grant counts; Workflow's principal approves anything. |
| 12.2 | Vendor visibility on every surface | met | any generated operation leaks an id outside the assignment, or an operation in the spec has no verdict in the coverage table. |
| 12.3 | Release approvals | met | any pointer moves before the second approval as seen at the edge, or a rollback waits. |
| 12.4 | Staged rollout across three runtimes | met | any runtime disagrees with the generator on any id, the share is outside 9–11 %, or an aborted installation stays on the candidate. Runtimes are compared with the generator, never with each other. |
| 12.5 | Audit export | met | a call the harness recorded has no entry (compared with the harness's own log, not the outbox), an entry has the wrong actor, a tampered export verifies, or a canary leaks. |
| 12.6 | v0.3 imports and renders the same | met | any rendering differs between v0.3's formatter and @klarlabs-studio/glossa (two implementations that share no code) other than by v0.3's known apostrophe defect, which is reported with its count and every row, or a carried field is missing. |
| 12.7 | Earlier exits hold | met | any earlier exit criterion fails. A failure here blocks the M5 verdict whatever 12.1–12.6 say. |

## The fixture

- **An organisation of three people and a vendor's translator**, all signed in through mail the server
  delivered over SMTP (`GLOSSA_MAIL_DRIVER=smtp`, with credentials) to the test mailer: the owner
  (`lena@acme.example`), two `de` reviewers (`ana@acme.example`, `ben@acme.example`), and the vendor's translator (`vera@lingua.example`), who registers
  with a password, follows the verification link the mailer received, and so accepts the invitation.
  The test mailer accepted 4 messages and refused 0 unauthenticated sessions.
- **Project A** (`ledger`) and **project B** (`portal`), source `en`, target `de`, 30 messages each plus
  `shared.welcome`, which both hold and §12.1 revises in both. Project B's first 20 `de` units are the vendor's
  assignment (§12.2); the rest, and all of project A, are outside it.
- The vendor's translator is a vendor member with visibility `assigned`, scoped to project B.
- **Something to address in each project** (§12.2), made through the API as the owner: an application,
  a check policy, a staging release and one of its artifacts, an import job, a Git connection (one
  repository of a fake GitHub, under a path per project), a branch push, a capture upload, a check run,
  a linguistic job, and an AI fill of `a.unit.19` / `b.unit.19` with its job and suggestion, drafted by a fake
  provider on loopback. Project B's are the sweep's ids inside the assignment, project A's outside it.
- **Canaries**, words that exist only in this fixture's text, in source and translation text of both projects:
  `Quokkafjordine`, `Velutinaquill`, `Brombeerzwirnt`, `Nachtfalterzopf`, `Zwielichtmarmor`. §12.5 fails if any of them reaches the audit export.
- **A v0.3 server built from `apps/api`** (§12.6), migrated with its own migrations, seeded through its own API.

## §12.1 — two workflows, one event

| | Step | What happened |
|---|---|---|
| ✅ | refuse a definition naming a guard the vocabulary does not have with 422 `invalid_workflow` | held |
| ✅ | refuse a definition with an unreachable state with 422 `invalid_workflow` | held |
| ✅ | refuse a definition with a delayed transition with 422 `invalid_workflow` | held |
| ✅ | §2.1's architecture test passes | held |
| ✅ | save `vendor-then-four-eyes` (testdata) through the API | held |
| ✅ | find the seeded default definition (`defaults/review.json`) | held |
| ✅ | bind project A to the default and project B to `vendor-then-four-eyes` for `de` | held |
| ✅ | revise `shared.welcome`'s source in both projects to the same revision | held |
| ✅ | A: an instance of the default definition exists and the `de` translation is `needs_review` | held |
| ✅ | A: one reviewer's approval makes it `approved`, by that reviewer in the revision log | held |
| ✅ | B: the `de` unit is assigned to the vendor and appears in the vendor member's work | held |
| ✅ | B: the vendor member writes it and completes the assignment | held |
| ✅ | B: the author's own approval is refused | held |
| ✅ | B: a token's approval is refused (no scope grants `approvals.decide`) | held |
| ✅ | B: after one reviewer the translation is still not `approved` | held |
| ✅ | B: the second reviewer's approval makes it `approved`, by that reviewer in the revision log | held |
| ✅ | both instances reached a final state, every action ran as a person, never as Workflow's principal | held |

§2.1's architecture test: --- PASS: TestTheVocabularyIsClosed (0.00s) / PASS / ok  	github.com/klarlabs-studio/glossa/platform/internal/workflow/domain	0.283s

The transition logs:

```text
cd4afba0:  —translation.outdated→ reviewing (person:01a1078d-b558-7ee0-a497-b7e7ec8d07b2)
cd4afba0: reviewing —translation.reviewed→ reviewing (person:01a1078d-b558-7ee0-a497-b7e7ec8d07b2)
cd4afba0: reviewing —approval.granted→ done (person:01a1078d-b590-7c63-a26e-ddd2036ec62e)
26982911:  —translation.outdated→ translating (person:01a1078d-b558-7ee0-a497-b7e7ec8d07b2)
26982911: translating —translation.revised→ translating (person:01a1078d-b6cd-7b37-ae25-8395d6b5a41e)
26982911: translating —assignment.completed→ reviewing (person:01a1078d-b6cd-7b37-ae25-8395d6b5a41e)
26982911: reviewing —approval.granted→ reviewing (person:01a1078d-b590-7c63-a26e-ddd2036ec62e)
26982911: reviewing —approval.granted→ done (person:01a1078d-b5a6-7704-9c98-330eed3e5056)
```

## §12.2 — vendor visibility on every surface

| | Step | What happened |
|---|---|---|
| ✅ | the vendor exists and its translator is a vendor member with visibility `assigned`, scoped to project B | held |
| ✅ | assign 20 `de` units of project B to the vendor | held |
| ✅ | writes outside the assignment are refused | held |
| ✅ | export, import and TM search are refused | held |

The sweep generated 130 GET operations from platform/api/openapi.yaml.

The vendor member could not create an API token (HTTP 403: {"type":"urn:glossa:problem:forbidden","title":"Forbidden","status":403,"code":"forbidden","detail":"missing permission tokens.manage"}), so no MCP tool is reachable as them. Each tool is listed from an owner's session with the verdict "unreachable as the member"; none was called.

### The coverage table

Every GET operation of `platform/api/openapi.yaml`, called as the vendor's translator — inside the
assignment (project B, an assigned unit) and, where the operation is addressed by a project or a
message, outside it (project A, an unassigned unit, which must answer 404). **130 hold, 0 show something
outside the assignment or answer undocumented, 0 have no verdict** (no fixture id to address them).
An operation of the spec with no row at all, or a row marked ∅, fails §12.2.

| | Operation | Inside | Outside | Why |
|---|---|---|---|---|
| ✅ | `getDeviceAuthorization` | 200 | — |  |
| ✅ | `getMe` | 200 | — |  |
| ✅ | `listPasskeys` | 200 | — |  |
| ✅ | `getMeta` | 200 | — |  |
| ✅ | `listTenants` | 200 | — |  |
| ✅ | `getTenant` | 200 | — |  |
| ✅ | `getAIBudget` | 403 | — |  |
| ✅ | `listAIDisclosures` | 403 | — |  |
| ✅ | `getAIEvalBaseline` | 403 | — |  |
| ✅ | `getAIFill` | 403 | 404 |  |
| ✅ | `listAIJobs` | 403 | — |  |
| ✅ | `getAIJob` | 403 | 404 |  |
| ✅ | `getAIPrices` | 403 | — |  |
| ✅ | `listAIProviders` | 403 | — |  |
| ✅ | `getAIProvider` | 403 | — |  |
| ✅ | `getAIRoutingPolicy` | 403 | — |  |
| ✅ | `getAISettings` | 403 | — |  |
| ✅ | `listAISpend` | 403 | — |  |
| ✅ | `listAISuggestions` | 403 | — |  |
| ✅ | `getAISuggestion` | 403 | 404 |  |
| ✅ | `listApprovals` | 403 | — |  |
| ✅ | `getApproval` | 403 | — |  |
| ✅ | `getAssignmentReport` | 403 | — |  |
| ✅ | `listAssignments` | 200 | — |  |
| ✅ | `getAssignment` | 200 | — |  |
| ✅ | `listAuditEntries` | 403 | — |  |
| ✅ | `getAuditEntry` | 403 | — |  |
| ✅ | `listAuditExportJobs` | 403 | — |  |
| ✅ | `getAuditExportJob` | 403 | — |  |
| ✅ | `downloadAuditExportEntries` | 403 | — |  |
| ✅ | `downloadAuditExportManifest` | 403 | — |  |
| ✅ | `getEffectiveStyleGuide` | 403 | — |  |
| ✅ | `listExportJobs` | 403 | — |  |
| ✅ | `getExportJob` | 404 | — |  |
| ✅ | `downloadExportFile` | 404 | — |  |
| ✅ | `listGitConnections` | 403 | — |  |
| ✅ | `getGitConnection` | 403 | 404 |  |
| ✅ | `listGitHubInstallations` | 403 | — |  |
| ✅ | `listGroups` | 403 | — |  |
| ✅ | `getGroup` | 403 | — |  |
| ✅ | `listImportJobs` | 403 | — |  |
| ✅ | `getImportJob` | 403 | 404 |  |
| ✅ | `listImportResults` | 403 | 404 |  |
| ✅ | `listMembers` | 403 | — |  |
| ✅ | `getMember` | 403 | — |  |
| ✅ | `listProjects` | 200 | — |  |
| ✅ | `getProject` | 200 | 404 |  |
| ✅ | `getAIMetrics` | 403 | 404 |  |
| ✅ | `getAIReviewQueue` | 403 | 404 |  |
| ✅ | `getProjectAIRoutingPolicy` | 403 | 404 |  |
| ✅ | `getProjectAISettings` | 403 | 404 |  |
| ✅ | `listApplications` | 403 | 404 |  |
| ✅ | `getApplication` | 403 | 404 |  |
| ✅ | `listBranches` | 403 | 404 |  |
| ✅ | `getBranch` | 403 | 404 |  |
| ✅ | `listBranchProposals` | 403 | 404 |  |
| ✅ | `listCaptureFindings` | 403 | 404 |  |
| ✅ | `getCaptureImage` | 200 | 404 |  |
| ✅ | `getCheckPolicy` | 403 | 404 |  |
| ✅ | `exportCheckPolicy` | 403 | 404 |  |
| ✅ | `listCheckPolicyVersions` | 403 | 404 |  |
| ✅ | `getCheckPolicyVersion` | 403 | 404 |  |
| ✅ | `listCheckRuns` | 403 | 404 |  |
| ✅ | `getCheckRun` | 403 | 404 |  |
| ✅ | `listContextBuilds` | 403 | 404 |  |
| ✅ | `listDeliveryKeys` | 403 | 404 |  |
| ✅ | `listEnvironments` | 403 | 404 |  |
| ✅ | `getEnvironment` | 403 | 404 |  |
| ✅ | `listDeployments` | 403 | 404 |  |
| ✅ | `listRollouts` | 403 | 404 |  |
| ✅ | `getRollout` | 403 | 404 |  |
| ✅ | `getFallbackGraph` | 403 | 404 |  |
| ✅ | `listFindings` | 403 | 404 |  |
| ✅ | `listLinguisticJobs` | 403 | 404 |  |
| ✅ | `getLinguisticJob` | 403 | 404 |  |
| ✅ | `listLocales` | 200 | 404 |  |
| ✅ | `getLocale` | 200 | 404 |  |
| ✅ | `listMessages` | 200 | 404 |  |
| ✅ | `getMessage` | 200 | 404, 404 |  |
| ✅ | `listMessageCaptures` | 200 | 404, 404 |  |
| ✅ | `listSourceRevisions` | 200 | 404, 404 |  |
| ✅ | `listMessageTranslations` | 200 | 404, 404 |  |
| ✅ | `getTranslation` | 200 | 404, 404 |  |
| ✅ | `listUnitAISuggestions` | 200 | 404, 404 |  |
| ✅ | `listTranslationRevisions` | 200 | 404, 404 |  |
| ✅ | `listUnitTMMatches` | 200 | 404, 404 |  |
| ✅ | `listMessageUsages` | 200 | 404, 404 |  |
| ✅ | `listNamespaces` | 403 | 404 |  |
| ✅ | `listPreviewOrigins` | 403 | 404 |  |
| ✅ | `getQualitySummary` | 403 | 404 |  |
| ✅ | `listReleaseRequests` | 403 | 404 |  |
| ✅ | `getReleaseRequest` | 403 | 404 |  |
| ✅ | `listReleaseSigningKeys` | 403 | 404 |  |
| ✅ | `listReleases` | 403 | 404 |  |
| ✅ | `getRelease` | 403 | 404 |  |
| ✅ | `getReleaseArtifact` | 403 | 404 |  |
| ✅ | `getReleaseDiff` | 403 | 404 |  |
| ✅ | `getReleaseManifest` | 403 | 404 |  |
| ✅ | `listProjectTerminologyFindings` | 403 | 404 |  |
| ✅ | `getTranslationStats` | 403 | 404 |  |
| ✅ | `listProjectTranslations` | 200 | 404 |  |
| ✅ | `listUnusedMessages` | 403 | 404 |  |
| ✅ | `listUsages` | 403 | 404 |  |
| ✅ | `listWaivers` | 403 | 404 |  |
| ✅ | `listWorkflowBindings` | 403 | 404 |  |
| ✅ | `listWorkflowInstances` | 403 | 404 |  |
| ✅ | `getWorkflowInstance` | 403 | 404 |  |
| ✅ | `listWorkflowTransitions` | 403 | 404 |  |
| ✅ | `resolveWorkflow` | 403 | 404 |  |
| ✅ | `listStyleGuides` | 403 | — |  |
| ✅ | `getStyleGuide` | 403 | — |  |
| ✅ | `listStyleGuideVersions` | 403 | — |  |
| ✅ | `listTermConcepts` | 403 | — |  |
| ✅ | `getTermConcept` | 403 | — |  |
| ✅ | `listTermConceptRevisions` | 403 | — |  |
| ✅ | `listTermbaseExportJobs` | 403 | — |  |
| ✅ | `listTermbaseImportJobs` | 403 | — |  |
| ✅ | `searchTranslationMemory` | 403 | — |  |
| ✅ | `listTMExportJobs` | 403 | — |  |
| ✅ | `listTMImportJobs` | 403 | — |  |
| ✅ | `listTranslationMemoryUnits` | 403 | — |  |
| ✅ | `getTranslationMemoryUnit` | 403 | — |  |
| ✅ | `listTokens` | 403 | — |  |
| ✅ | `getToken` | 403 | — |  |
| ✅ | `listVendors` | 403 | — |  |
| ✅ | `getVendor` | 403 | — |  |
| ✅ | `listWorkflowDefinitions` | 403 | — |  |
| ✅ | `getWorkflowDefinition` | 403 | — |  |
| ✅ | `listWorkflowDefinitionVersions` | 403 | — |  |
| ✅ | `getWorkflowDefinitionVersion` | 403 | — |  |

The MCP read tools, as the same member:

| | Tool | Answer | Why |
|---|---|---|---|
| ✅ | `(connect)` | no token | refused at token creation |
| ✅ | `assignments_list` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `assignments_report` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `catalog_search` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `check_run` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `explain_delivery` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `findings_list` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `message_get` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `release_requests_list` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `rollouts_list` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `style_rules` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `term_lookup` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `tm_search` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `translation_get` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `usages_get` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `whoami` | unreachable | the member can hold no token, so cannot open a session; not called |
| ✅ | `workflow_state` | unreachable | the member can hold no token, so cannot open a session; not called |

## §12.3 — release approvals

| | Step | What happened |
|---|---|---|
| ✅ | `production` requires two approvals, distinct from the requester | held |
| ✅ | a publish creates a release request and the edge still serves the previous release | held |
| ✅ | the requester's own approval is refused | held |
| ✅ | after the first approval the edge still serves the previous release | held |
| ✅ | after the second approval the edge serves the new release | held |
| ✅ | a forced publish over an unmet gate still waits for approvals, and the approvers see its reason | held |
| ✅ | a rollback takes effect at the edge at once, with no approval | held |

What `glossa-edge` served for project B's `production`, read over HTTP from the edge process:

| | When | The edge served |
|---|---|---|
| ✅ | before approvals are required | v4 |
| ✅ | right after the publish | previous release |
| ✅ | after one approval | previous release |
| ✅ | after the second approval | release ed134611 |
| ✅ | after a rollback, no approval | the earlier release, within 1.9s |

## §12.4 — staged rollout across three runtimes

| | Step | What happened |
|---|---|---|
| ✅ | the generator's cohort table: 10,000 installation ids with their SPEC §1.4 cohorts | held |
| ✅ | each runtime implements SPEC §1.4 (probe: the edge's manifests, a 10 % rollout under the table's salt) | held |
| ✅ | start a rollout of the candidate at 10 % in project A's `production` | held |
| ✅ | the edge's signed manifest carries the rollout: 10 %, the candidate, a salt | held |
| ✅ | three runtimes at 10 %: within 9–11 %, and each agrees id for id with the generator | held |
| ✅ | rollout support off: every id stays on stable | held |
| ✅ | advance to 50 %: every 10 % installation stays in the candidate | held |
| ✅ | abort: every installation is back on stable at the next refresh | held |
| ✅ | complete: the pointer moves to the candidate and the rollout member is gone | held |

The generator's table (`runtimes/testdata/rollout/cohorts.json`): 10000 installation ids; under its salt `c3RhZ2VkLXJvbGxvdXQtMQ`, 1004 are in the candidate at 10 %.
The edge's manifest carried salt `7XVZI5VVGkpY1msLyCaoUA`; the expected cohorts under it come from `generate.py`.

| | Phase | Runtime | In the candidate | Disagree with the generator | Note |
|---|---|---|---:|---:|---|
| ✅ | probe at 10 % | js | 1004 | 0 |  |
| ✅ | probe at 10 % | go | 1004 | 0 |  |
| ✅ | probe at 10 % | dart | 1004 | 0 |  |
| ✅ | probe, rollout support off | js | 0 | 0 |  |
| ✅ | probe, rollout support off | go | 0 | 0 |  |
| ✅ | probe, rollout support off | dart | 0 | 0 |  |
| ✅ | 10 % | js | 1046 | 0 |  |
| ✅ | 10 % | go | 1046 | 0 |  |
| ✅ | 10 % | dart | 1046 | 0 |  |
| ✅ | 10 %, rollout support off | js | 0 | 0 |  |
| ✅ | 10 %, rollout support off | go | 0 | 0 |  |
| ✅ | 10 %, rollout support off | dart | 0 | 0 |  |
| ✅ | 50 % | js | 4972 | 0 |  |
| ✅ | 50 % | go | 4972 | 0 |  |
| ✅ | 50 % | dart | 4972 | 0 |  |
| ✅ | aborted | js | 0 | 0 |  |
| ✅ | aborted | go | 0 | 0 |  |
| ✅ | aborted | dart | 0 | 0 |  |

## §12.5 — audit export

| | Step | What happened |
|---|---|---|
| ✅ | the tenant's audit entries can be listed | held |
| ✅ | the audit public keys are published at /.well-known/glossa-audit-keys.json | held |
| ✅ | export the run's range as an audit export job | held |
| ✅ | `glossa audit verify` passes on the export, offline | held |
| ✅ | an entry for every call the harness recorded, with its actor | held |
| ✅ | one altered byte makes `glossa audit verify` fail | held |
| ✅ | no canary string appears anywhere in the export | held |

The harness recorded 31 successful mutating calls in §12.1–§12.4 itself, never from the platform: map[12.1:10 12.2:2 12.3:10 12.4:9].

The export held 235 entries.

| § | Successful mutating calls the harness recorded |
|---|---:|
| 12.1 | 10 |
| 12.2 | 2 |
| 12.3 | 10 |
| 12.4 | 9 |

## §12.6 — v0.3 imports and renders the same

| | Step | What happened |
|---|---|---|
| ✅ | a v0.3 server built from apps/api, migrated with its own migrations, seeded: 300 keys in de/en/es, descriptions, a change history, three users | held |
| ✅ | dump it and restore the dump with `platform/scripts/v0-restore.sh`, which writes the restore marker | held |
| ✅ | `glossa import --from v0 --v0-db` imports the restore | held |
| ✅ | publish, and render every key in every locale both ways with `glossa import --from v0 --verify`: zero mismatches apart from v0.3's known apostrophe defect, which is reported with its count (imported by --v0-db) | held |
| ✅ | `glossa import --from v0 --v0-db --history` sends v0.3's history to the audit trail as the owner, signed in with `glossa login --device` | held |
| ✅ | descriptions are on the messages | held |
| ✅ | the three users are invitations with mapped roles and locales | held |
| ✅ | v0.3's history is visible as imported audit entries | held |
| ✅ | the importer refuses a DSN without the restore marker | held |

v0.3 holds 300 keys × 3 locales, 930 audit-log rows and 3 users.

51 renderings differ only by v0.3's known apostrophe defect (`v0_bare_apostrophe`): v0.3's formatter reads a bare apostrophe as opening a quoted run; each is listed below with v0.3's text requoted as evidence.

Imported by --v0-db; **3150 renderings** of 300 keys in de/en/es: **51 differ only by v0.3's known apostrophe defect**, **0 differ otherwise**.

Known v0.3 defect (`v0_bare_apostrophe`): v0.3's formatter reads a bare apostrophe as opening a quoted run. Each row is in this category only because v0.3's own formatter, given the same text with its apostrophes requoted the ICU way, renders exactly the runtime's output.

| Key | Locale | Arguments | v0.3's formatter | @klarlabs-studio/glossa |
|---|---|---|---|---|
| `copy.bare_101` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_101` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_101` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_11` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_11` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_11` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_119` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_119` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_119` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_137` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_137` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_137` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_155` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_155` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_155` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_173` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_173` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_173` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_191` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_191` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_191` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_209` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_209` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_209` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_227` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_227` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_227` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_245` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_245` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_245` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_263` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_263` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_263` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_281` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_281` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_281` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_29` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| `copy.bare_29` | en | `map[name:Ada]` | "Dont wait, {name}" | "Don't wait, Ada" |
| `copy.bare_29` | es | `map[name:Ada]` | "Rocknroll con Ada" | "Rock'n'roll con Ada" |
| `copy.bare_299` | de | `map[name:Ada]` | "Gehts gut, {name}?" | "Geht's gut, Ada?" |
| … | | | 11 more | |

## §12.7 — earlier exits hold

M2's exit test passed in 36s.

M3's exit test passed in 71s.

M4's exit test passed in 96s.

### M2 — **passed** in 36s

```text
--- PASS: TestM2Exit (34.22s)
--- PASS: TestFixtureIsCurrent (0.11s)
--- PASS: TestFixtureShape (0.00s)
--- PASS: TestInterchangeFilesRead (0.03s)
ok  	github.com/klarlabs-studio/glossa/platform/internal/systemtest/m2	34.616s
ok  	github.com/klarlabs-studio/glossa/platform/internal/systemtest/m2/fixture	0.358s
```

### M3 — **passed** in 71s

```text
--- PASS: TestM3Exit (67.72s)
--- PASS: TestFixtureIsCurrent (0.01s)
--- PASS: TestFixtureShape (0.00s)
ok  	github.com/klarlabs-studio/glossa/platform/internal/systemtest/m3	68.175s
ok  	github.com/klarlabs-studio/glossa/platform/internal/systemtest/m3/fixture	0.237s
```

### M4 — **passed** in 96s

```text
--- PASS: TestM4Exit (93.91s)
ok  	github.com/klarlabs-studio/glossa/platform/internal/systemtest/m4	94.305s
**8 of the 8 exit criteria hold.**
| § | Criterion | Verdict |
|---|---|---|
| 12.1 | A fixture repository with real CI | met |
| 12.2 | Findings across layers | met |
| 12.3 | The PR check agrees | met |
| 12.4 | Waivers and policy rollout | met |
| 12.5 | Release gate | met |
| 12.6 | MCP | met |
| 12.7 | Flutter | met |
| 12.8 | Dashboard | met |
```

## What this test cannot prove

The first four are RFC 0006 §12's list; the rest are what this harness fakes or does not look at. All of
it is the dogfood phase's to prove (§7.4), or another suite's.

- **Production rendering in real products (RFC 0006 §1.3, §12).** The renderings compared in §12.6 are of a generated fixture of 300 keys, not of any Klarlabs product's strings, locales or arguments.
- **A real vendor can be onboarded (§12).** The invitation, verification link and sign-in travel over SMTP to a capturing **test mailer** the harness runs. No real mail provider, spam filter, link rewriting or delay was involved.
- **Real users land in rollout cohorts in the proportions the fixture shows (§12).** §12.4 drives the runtimes with 10,000 installation ids from a fixture through a **fake transport** over the edge's real manifest. Real installation ids, real refresh timing and real network failure were not.
- **v0.3's namespace can be deleted without someone noticing (§12).** §12.6 imports a **seeded** v0.3 server built from `apps/api` on its own Postgres. Nothing here shuts a production v0.3 down or finds who still calls it.
- **Anything a real GitHub does.** The Git connection of §12.2 talks to a **fake GitHub** on loopback: no App installation, webhook signature, rate limit or permission model of the real one.
- **Anything a real AI provider does.** The AI fill of §12.2 is drafted by a **fake provider** on loopback. Quality, latency, errors and cost of a real model are untested.
- **Studio in a browser.** This test reads the public API, the edge, the runtimes and the CLI. Studio's own end-to-end tests are a separate suite; nothing here shows a user can reach any of this through the UI.
- **That the access surface is closed beyond what the spec and the MCP tool list say.** §12.2's sweep generates **GET** operations from `platform/api/openapi.yaml` and calls them with fixture ids; writes are checked by a fixed list. A leak through a route that is not in the spec, through an id the fixture did not make, or by timing is invisible to it. A tool that needs an argument the fixture cannot fill is marked ∅ and fails the criterion.
- **The audit export is complete for calls the harness did not make.** §12.5 compares the export with the harness's own log of the calls *it* made in §12.1–§12.4. Entries for system actions, other callers or calls the harness did not record are not checked against anything.
- **Behaviour under load, partial failure, clock skew or restarts.** One server, one edge, one Postgres and one bucket, started once on loopback, with no faults injected.
- **The Dart, Go and JS runtimes on real devices.** They are run as host processes against a fake transport; no mobile OS, app lifecycle or storage is involved.

## Flake review

What the harness does about timing and shared state, so a red run can be told from a flaky one:

- **Positive waits** (a pointer arrives, a state is reached, a service is ready) are polls with a stated deadline
  (`softly`, `edgeServes`): 10–90 s, the last observed state in the failure. No positive claim rests on a fixed sleep.
- **Negative claims** (the edge has *not* moved, the translation is *not yet* approved) are watched for their whole
  window by `staysFor`, which fails on the first violation and treats a check it cannot make as a failure, not a pass.
  The window (3 s) must cover the outbox and the edge's refresh; a window too short could only make such a step pass
  wrongly, never fail, so it is the one place a wait is a lower bound on rigour. It is never the only evidence: each
  is followed by the positive step that the next approval does move it.
- **No retries.** No step is repeated until it passes; an HTTP call that fails once fails the step.
- **Order.** §12.1–§12.4 share one server, one fixture and one `production` environment and run in that order;
  §12.4 starts from the stable release §12.3 leaves, and §12.5 compares the export with the calls of §12.1–§12.4.
  Each criterion records its own gaps, so a failure in one does not stop the next, but a later one that needs an
  earlier one's state reports *not reached* with the reason. §12.7 runs first and alone, because the earlier exit
  tests collide at sign-in with a second running server.
- **Known platform-side flake, not fixed here.** In M3's PR-check queue, `integration_github_checks.available_at`
  is set with the application clock but claimed with Postgres `now()`; with a skewed clock a check can be claimed
  early or late. M5's harness does not touch that queue (§12.2 creates a check *run* through the API, not a pull-request
  check), but §12.7 runs M3's exit test, so a red §12.7 naming a PR check is this, not M5.


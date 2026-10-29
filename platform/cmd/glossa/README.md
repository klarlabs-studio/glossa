# glossa

The Glossa CLI: one static binary for developers and CI (product intent
§46, RFC 0002 §9). It talks to `glossa-server` through the `/v1` API with
an API token, and runs the MessageFormat kernel locally, so `check
--offline`, `extract` and `generate` work without a server.

```sh
go build -o glossa ./cmd/glossa        # from platform/
glossa init --server https://glossa.example.com --project brotwerk
glossa login                            # in GitHub Actions: no secret at all
glossa push && glossa check && glossa generate
```

Release binaries (darwin/linux/windows × amd64/arm64, `CGO_ENABLED=0`):
`goreleaser build --snapshot --clean --config cmd/glossa/.goreleaser.yaml`
from `platform/`. Nothing is published.

## glossa.yaml

Found by walking up from the working directory. Paths are relative to
the file. `GLOSSA_SERVER`, `GLOSSA_TENANT` and `GLOSSA_PROJECT` override
it.

```yaml
version: 1
server: https://glossa.example.com
tenant: acme                # optional: a tenant slug or ID (default: the API token's own tenant)
project: brotwerk          # slug or ID
source_locale: de          # init reads it from the server
syntax: mf1                # local catalogs: mf1 (ICU, default) or mf2
catalogs:
  path: locales/{locale}.json   # the source file is pushed; the others hold translations
  style: flat                   # how pull writes: flat (default) or nested
extract:
  include: ["**/*.{ts,tsx,js,jsx,mjs,vue,astro,svelte,html,go}"]
  exclude: ["**/*.test.*", "**/*_test.go", "**/*.d.ts"]
  templates: ["**/*.{tmpl,gotmpl,gohtml}"]  # read as Go templates
  application: web               # the usages document's application
generate:
  typescript: src/glossa/messages.ts
  vue: src/glossa/glossa-vue.ts  # needs typescript
  react: src/glossa/glossa-react.ts  # needs typescript
  go: internal/msg/messages.go
  go_package: msg                # default: the directory name
check:
  require_complete: [de, en]     # default: the project's check policy
  fail_on: error                 # or warning, or never
pull:
  states: [approved]             # default
capture:                         # glossa capture: screenshots with message regions
  base_url: http://localhost:4173  # a preview with fixture data; --base-url overrides
  application: web               # default: extract.application
  output: .glossa/captures       # without --upload (default)
  locales: [de, ja]              # default: the source locale
  locale: { query: lang }        # how a page picks the locale: query or cookie,
                                 # or put {locale} in every route's url
  viewports:                     # default: 1280×800 and 390×844
    - { width: 1280, height: 800 }
    - { width: 390, height: 844, device_scale_factor: 2, mobile: true }
  routes:
    - route: /                   # the pattern usages name; url defaults to it
    - route: /checkout/[step]
      url: /checkout/payment     # relative to base_url, or absolute
      playbook: .glossa/playbooks/open-card.json  # scout playbook, replayed after load
  cookies:                       # fixture login, set on base_url
    - { name: session, value: "${FIXTURE_SESSION}" }
  headers:                       # sent only to base_url's origin
    X-Fixture-User: "${FIXTURE_USER}"
```

Cookie and header values reference environment variables as `${NAME}`;
they're never printed, and an unset one is an error that names only the
variable.

Catalogs are JSON objects of message ID to text, flat
(`{"checkout.pay": "…"}`) or nested (`{"checkout": {"pay": "…"}}`).

## Authentication

`GLOSSA_TOKEN` wins. Otherwise `glossa login` verifies a token against
the server and stores it per server URL in the macOS Keychain or the
Secret Service keyring (Linux, via `secret-tool`), falling back to a
0600 `glossa/credentials.json` under the user config directory. Tokens
reach the keychain tools on stdin, never on the command line.
`glossa logout` removes it; `glossa whoami` shows what's in use.

### Signing in as yourself: `glossa login --device`

For what an API token may not do (importing v0.3's history writes the
organisation's audit trail, which only an owner may), sign in as
yourself. `glossa login --device [--client-name NAME]` prints a link and
a code such as `BCDF-GHJK` on stderr and waits; open the link in Studio,
check the code and the name shown, and approve. The CLI stores your own
session as `glossa_dev_…` like any other credential (it acts with
exactly your roles and projects, and lasts `GLOSSA_SESSION_TTL`).
`glossa whoami` then names you and says it is a device session.
`glossa logout` ends that session on the server as well as forgetting it
(it still forgets it when the server cannot be reached). Codes expire
after 15 minutes and work once; `--json` keeps stdout to one document.

### In GitHub Actions: no stored secret

With `permissions: { id-token: write }`, `glossa login` needs no token
at all (RFC 0004 §6.3). It sees `ACTIONS_ID_TOKEN_REQUEST_URL` and its
request token, asks GitHub for an OIDC ID token with audience `glossa`,
and exchanges it at `POST /v1/auth/github-oidc-exchanges`.

```yaml
permissions:
  contents: read
  id-token: write

steps:
  - uses: ./.github/actions/glossa      # or the commands by hand:
  - run: glossa login                   # no secret, nothing to rotate
  - run: glossa push --branch "$GITHUB_HEAD_REF" --pr "$PR_NUMBER"
```

The server verifies the issuer, the audience, the signature and the
expiry, then matches the token's `repository_id` — GitHub's immutable
number, never the repository's name — against a **Git connection**
(*Git connections*, below). What it hands back:

- lives **30 minutes** and cannot be refreshed: the next job asks GitHub
  for a fresh ID token;
- is bound to **one project**, so a request under another project of the
  same workspace is refused;
- allows exactly `catalog.read` and `catalog.write` — what `push`,
  `extract --upload`, `context push`, `capture --upload` and
  `preview register` need. It cannot import translations
  (`push --translations`), publish a release, use the termbase or AI, or
  read the workspace's members or tokens. Those still need an API token.

`glossa login --json` says which project it authenticated for, what the
credential may do and when it dies; the credential itself is stored the
same way as any other, and never printed.

Refusals, and what they mean:

| Code | What to do |
|---|---|
| `repository_not_connected` | Connect the repository to a project in Studio → Settings → GitHub. A project the repository does not feed gets the same answer. |
| `ambiguous_project` | The repository feeds several projects (a monorepo, one connection per path). `login` prints their IDs; pass `--project <id>`. |
| `invalid_id_token` | The ID token didn't verify: check the audience is `glossa`, and the server's clock and GitHub configuration. |
| `github_not_configured` | The server has no GitHub App, so there are no connections to match against. Use an API token. |

**Forks.** A pull request from a fork gets no ID token and no secrets
from GitHub, so a fork's job cannot authenticate and Glossa's check on
it completes as `neutral` with an explanation (RFC 0004 §6.4).

**Keeping a token instead.** `--token-stdin` means an API token even
inside Actions, and `GLOSSA_TOKEN` wins over everything, so a workflow
that already keeps a secret goes on working unchanged. On CI that isn't
GitHub Actions, that is the way: set `GLOSSA_TOKEN` and run the same
commands (see `.github/actions/glossa/README.md`).

A self-hosted deployment, or GitHub Enterprise Server, sets the issuer
and audience it accepts with `GLOSSA_GITHUB_OIDC_ISSUER`,
`GLOSSA_GITHUB_OIDC_AUDIENCE` and `GLOSSA_GITHUB_OIDC_MAX_STALE`.

## Commands

Every command takes `--json`, `--quiet`, `--no-color` and `--config`.
Colors appear only on a terminal (and never with `NO_COLOR`).

| Command | What it does |
|---|---|
| `init` | Writes glossa.yaml. Prompts on a terminal; with a token, reads the tenant and source locale from the server. `--server --project --source-locale --catalogs --typescript --vue --react --go --force` |
| `login` / `logout` / `whoami` | Credential storage; `--server`, `--token-stdin`, `--project` (GitHub Actions OIDC), `--device`, `--client-name` (sign in as yourself; see *Authentication*) |
| `push` | Sends the source catalog through `message-upserts` (500 per request). Reports created/revised/updated/unchanged/failed per key. `--dry-run` compares canonical models with the server instead of writing. `--translations` also imports the other catalogs as translations (provenance `import`). `--branch <name> [--pr <n>] [--commit <sha>]` pushes as a feature branch instead (RFC 0004 §4.1): the whole catalog in one request, where a new key becomes a `proposed` message the branch owns, changed source for a live key a source proposal, and nothing live changes. It answers with the branch's status report (new keys, source proposals, removed keys, conflicts, outdated per locale). Without values, the branch, commit and pull request come from the CI environment (`GITHUB_HEAD_REF`/`GITHUB_REF_NAME`, `GITHUB_SHA`, `PR_NUMBER`); `--dry-run` doesn't apply to a branch push. |
| `pull` | Writes translations to the catalogs, sorted and deterministic. `--states approved,needs_review\|all`, `--locales`. `--release <id\|v<N>\|latest> [--environment env] [--out dir]` writes a release bundle instead (see *Release*). |
| `extract` | Finds message usages and prints them as a `glossa.usages/v1` document (RFC 0004 §2.2; contract and fixtures: `runtimes/testdata/usages/`). Go is parsed with `go/parser`: `.T(…)` calls (`Client.T(ctx, "…")`, `l.T("…")`, `For(…).T("…")`) and the generated accessors, with the enclosing `pkg.Func` / `pkg.(*Type).Method` as component; files starting with `// Code generated … DO NOT EDIT.` are skipped. Go templates (`extract.templates`) are parsed with `text/template/parse`: `{{t}}`, `{{td}}`, `{{th}}`. Web files are scanned lexically: `t("…")`/`$t("…")`, `<glossa-text\|rich\|plural\|select key\|message>`, `<GlossaText id>`, `<T id>`, typed accessors; Vue and Astro files are their own component, Astro pages carry their route. Only literal keys count. Reports keys missing from the catalog and catalog messages nothing uses; `--strict` exits 1 on unknown keys. `--upload` sends the document to the project's context builds as an `extract` build (what `context push` does). The document's application is `--application`, `GLOSSA_APPLICATION` or `extract.application`; its commit and branch are `--commit`/`--branch`, `GLOSSA_COMMIT`/`GLOSSA_BRANCH`, GitHub Actions (a pull request's head, not its merge commit) or GitLab CI, else git. |
| `context push <file>` | Uploads a `glossa.usages/v1` document — `@klarlabs-studio/glossa/unplugin`'s `.glossa/usages.json`, or a saved `extract --json` — to the project's context builds (`POST …/context-builds`, RFC 0004 §2). `--source plugin\|extract\|runtime\|capture` names the collector; by default `extract` when the document's tool is `glossa`, else `plugin`. Prints the build and how many usages name keys the catalog doesn't know; the same document again is "Already uploaded" (the server answers with the first build). Whether a build is of the default branch is the project's `default_branch` setting, not the uploader's say. A document the server refuses (`invalid_usages`, `too_many_usages`, `unknown_application`, `invalid_source`, `payload_too_large`) exits 2. |
| `capture` | Screenshots the pages of the capture plan (`capture:` in glossa.yaml) in headless Chrome, with `scout`, at every viewport and locale, and records where each message renders (RFC 0004 §3.1–§3.2): one `glossa.captures/v1` document (schema: `runtimes/testdata/schemas/captures.v1.schema.json`) with a full-page PNG per (route, viewport, locale), in a fixed order (route, URL, locale, the plan's viewport order). Before the page's scripts run it injects `@klarlabs-studio/glossa/capture`'s agent, which hooks every `@klarlabs-studio/glossa` on the page; after load (and the route's playbook) it waits until the DOM is quiet. It refuses a page whose runtime reports a `production` manifest (`production_page`), has no active release (`environment_unknown`), has no runtime (`no_runtime`) or doesn't render the requested locale (`locale_mismatch`), and blacks out `data-glossa-redact` elements before the screenshot (their regions are `visible: false`). Without `--upload` the manifest (`captures.json`) and the images (`<sha256>.png`) go to `capture.output` or `--out`; with `--upload` they're posted to the Captures API. The coverage report lists the messages with a current usage of the application (the branch's view) but no visible region; it reads the Context API, so `--no-coverage` is needed offline. Application, commit and branch are found like `extract`'s (`capture.application` first). `--cdp` (or `GLOSSA_CAPTURE_CDP`) attaches to a browser you started yourself instead of launching one — see "Attaching to a browser" below. A page that fails to load, a refusal, no Chrome (`no_browser`) or an endpoint it won't attach to (`invalid_cdp_endpoint`, `cdp_unreachable`) exits 2. |
| `generate` | Typed accessors from the catalog's argument metadata. `--check` writes nothing and exits 1 when the files are stale; `--from-server` uses the server's messages. |
| `check` | Runs the Quality library's layers over the project (RFC 0005 §3) and lets the check policy grade what they found: `structure` (every message and translation parses), `parity` (a translation fits its source — arguments, selectors, plural categories, markup), `completeness` (missing and outdated translations) and, with `--terminology`, `terminology`. Findings are `glossa.finding/v1` findings, the same shape the pull request and the server report. `--offline` checks the local catalogs against the cached policy. `--layer <name>` (repeatable, or comma-separated) runs only those layers; an unknown name exits 2 and a layer this run can't compute is named and exits 4. `--explain-policy` says, per finding, which rule gave it its severity and whether that rule can fail the run. `--require-complete=de,en\|none`, `--fail-on=error\|warning\|never`; without them, the project's check policy. `--fix` applies the structured fixes the findings carry (see *Fixing what a finding describes*). `--record` puts the run and its findings on the project's record, which is how Studio, `glossa findings` and the quality summary see what CI checked — the default in CI, off on a laptop (see *Recording a run*). |
| `findings` | The findings the server stored, with RFC 0005 §2.1's filters: `--layer --severity --code --locale --namespace --message --waived[=false]`, and which run to read — `--branch --commit --run --limit`. It consumes the Quality API and recomputes nothing: the layers that need a capture, a termbase or a provider ran on the server. The human output prints each finding's fingerprint, which is what `glossa waive` takes. |
| `waive` | Accept a finding: `glossa waive <fingerprint> --reason "why this is fine"`, with `--scope project\|branch --ref --expires --source-revision`. **The reason is required.** `--list [--fingerprint --layer --code --message --active[=false] --limit]` shows the project's waivers, `--revoke <id>` takes one back. |
| `policy` | The check policy as a file: `show`, `diff --file policy.yaml` (the impact preview — how many stored findings change severity and how many open pull requests would newly fail, per rule — storing nothing), `export [--file]`, `import --file [--grace-days n] [--dry-run]`. `--file -` is stdin or stdout. |
| `status` | Coverage per locale: translated, approved, needs review, draft, outdated, missing. One request: the server's `translation-stats`. `--offline` counts the local catalogs. `--quality` prints the seven numbers of RFC 0005 §8 instead — the same ones the dashboard shows — with `--locale`, `--environment` and `--since`. |
| `diff` | Local catalogs vs the server by canonical model (so MF1 spelling changes aren't changes). `--exit-code`. |
| `projects list` / `projects create --name N --slug S --source-locale L [--locales de,en] [--idempotency-key K]` | The configured tenant's projects (id, slug, name, source locale, locales); create one (`createProject` with an Idempotency-Key, then `addLocale` for the extra locales) and print its ID. Needs `catalog.read` / `catalog.write`. An existing slug exits 3 (`slug_taken`); if adding a locale fails the project exists and the error says so. |
| `tenants` | Every tenant the credential can act in (`listTenants`: a person's active memberships, a token's own tenant): id, slug, name, kind. `tenant` in glossa.yaml takes a slug or an ID; with none and several tenants, commands fail with `tenant_ambiguous` and list them in `fix`. |
| `locales`, `messages`, `namespaces` | Lists. `messages --prefix --namespace --missing-in --outdated-in --state active\|obsolete\|all`; `namespaces`: each namespace with its active and obsolete message counts (`GET …/namespaces`) |
| `import --format xliff\|json\|po\|tmx\|tbx <file>` | Imports an interchange file through the server's import jobs: a dry run unless `--apply` (merge) or `--overwrite`; conflicts and invalid items as `file:line:column` (see *Import and export*). |
| `export --format xliff\|json\|tmx\|tbx` | Exports through the server's export jobs and downloads the file, checked against its SHA-256. `-o`, `--unzip`, `--job` (see *Import and export*). |
| `jobs` | Import and export jobs: `list [--direction --state --all-projects --limit]` (the project's and the workspace's TMX and TBX jobs), `show <id> [--wait] [--all-results]`, `cancel <id>`. |
| `import --from v0` | Imports a Glossa v0.3 project (below). |
| `release` | `publish [--dry-run]`, `list`, `show`, `diff`, `promote`, `rollback`, `environments`, `keys [list\|create\|scope\|revoke]`, `requests [list\|show\|withdraw]` (release requests: publishes and promotes held for approval), `rollout start\|status\|list\|advance\|complete\|abort` (staged rollouts) (see *Release*, *Release approvals* and *Staged rollouts*). A publish or promote held for approval exits 5. |
| `approve`, `deny` | A person's decision (RFC 0006 §3.2, §5.1): `approve` with no argument lists what waits for approval in the project — pending release requests and translation approvals (`--environment`, `--locale`, `--message`); `approve <ref> [--reason R]` grants, `deny <ref> --reason R` denies. `<ref>` is a release request's ID, an approval's ID, or `key@locale` for a translation unit. **Human-only**: an API token is refused (`person_required`, exit 3), and the message says to sign in with `glossa login --device` (see *Release approvals*). |
| `translations review` | `translations review <key>@<locale>... --state approved\|rejected\|draft\|needs_review`: records a review decision on one or more translation units through the API's `reviewTranslation`; the text and its provenance stay, and you are recorded as the reviewer. Each unit is read first and reviewed against the ETag that was read (`If-Match`); a `412` is read again and tried once more, then reported. Approving and rejecting need `translations.review` for the locale (`review_forbidden`, exit 3, otherwise), the other states `translations.write`; `invalid_transition` (409) and unknown units (not found) are exit 3, `invalid_state` exit 2. Several units are each reported; exit 4 when some were reviewed and some not. Whether you may approve text you wrote yourself is the server's rule (issue #73), not the CLI's. For a unit that waits on a workflow approval, `glossa approve key@locale` is the four-eyes path. |
| `branch` | `status [<name>]`: what the branch proposes — new keys, source proposals, removed keys, key conflicts with other open branches, and the translations per locale merging it will make outdated (exit 1 on a conflict). `close [<name>]`: closes an unmerged branch, which destroys its preview environment; its proposed messages become obsolete 14 days later unless it is reopened. Without `<name>`, the branch comes from `GITHUB_HEAD_REF`/`GITHUB_REF_NAME`. |
| `preview register --url <url>` | Records where CI deployed the branch's preview (`--branch`, else the CI environment's). Studio and the pull request comment link to it. |
| `github connections` | Git connections — which repository feeds which project and application (RFC 0004 §6.1): `list [--project P \| --all-projects] [--installation I]`, `add --repository <id\|owner/name> --application A [--project P] [--path apps/web] [--branch main] [--installation I]`, `remove <connection-id>` (see *Git connections*). |
| `workflow` | Workflow definitions as files (RFC 0006 §2): `lint <file>` (the server's compile and lint, every finding with its rule, severity and path; exit 1 when a save would be refused), `push <file> [--project P] [--if-version N]` (create, or save the next version under `If-Match`; an unchanged document saves nothing), `pull <name> [--version N] [-o file]`, `export <name> [--version N] [-o file]` and `import <file> [--project P] [--if-version N]` (the same two by their portable names: the document alone, to move a definition between tenants or servers), `list`, `show <name>`, `bind <name> [--locales de,fr] [--namespace ns]`, `unbind <name> \| --binding <id>`, `bindings`, `instances [--status --definition --locale --message]`, `log <instance-id>`, `rebase <instance-id> [--version N]` (move a running instance to a newer version, keeping its state) (see *Workflows and assignments*). |
| `assignments` | My work (RFC 0006 §3.1): `list` (the default; `--all` for everyone's, `--project --state --locale --message`), `show <id>`, `accept <id>`, `complete <id>`, `decline <id> [--reason]`, `create --to member:<id\|email>\|role:<role>\|group:<name>\|vendor:<name> --units key@locale,… [--project P] [--due T] [--permission P]`, `report [--vendor ID] [--project P] [--since T]` (per-vendor quality numbers, RFC 0006 §3.4) (see *Workflows and assignments*). |
| `tm` | Translation memory: `search <text> --to L`, `concordance <text>`, `units [--locale-pair de:en] [--retire <id>]` (see *Knowledge and AI*); `export` / `import <file>`: TMX (`export`/`import --format tmx`). |
| `terms` | Termbase: `list`, `show`, `add`, `edit`, `deprecate`, `forbid`, and `check`, terminology QA over the project's translations; `export` / `import <file>`: TBX (`export`/`import --format tbx`). |
| `style` | `show [--locale --namespace]`: the effective style guide; `edit --file style.yaml`: create or replace one. |
| `translate` | `--locale L [--namespace --key-prefix] [--missing\|--outdated] [--dry-run] [--wait]`: fills locales with AI suggestions. |
| `review` | The AI review queue: `list [--locale]`, `accept <id\|key> [--text]`, `reject <id\|key> [--reason]`. |
| `ai status` | Provider consent, the monthly budget and spend, providers (never their keys), and the project's auto-translate locales, namespace tags and review routing. |
| `audit list [--from T --to T --actor A --action X --project P --source S --limit N] [--json\|--csv]` | The tenant's audit entries, newest first, paged through up to `--limit` (default 100, 0: all). Needs `audit.read` (owner, admin; no token scope). See *Audit*. |
| `audit export (--from T --to T \| --first-sequence N [--last-sequence M]) --out <dir> [--public-key K] [--no-wait] [--job <id>] [--force]` | Creates an audit export job, waits, downloads `entries.jsonl` and `manifest.json` into `<dir>` (each checked against its SHA-256) and, with `--public-key`, verifies them like `audit verify`. Owner only. |
| `audit csv <export dir\|entries.jsonl> [--out file]` | Converts an export's lines to CSV, offline. |
| `audit verify <export> --public-key <key>` | Checks a signed `glossa.audit/v1` export **offline** — no server, no glossa.yaml, no token: the manifest's signature, every line's hash and link, no gaps, and that the file is exactly what the manifest signs. Exit 1 at the first thing that fails, named (see *Audit*). |
| `mcp` | Speaks MCP on stdin and stdout, proxying to `glossa-server`'s `/mcp` endpoint with the stored token, so an editor that speaks only stdio gets the same tools without a second server (RFC 0005 §7.1). It is framing only — it registers no tool and validates no argument, so it cannot diverge from the endpoint. Read-only unless you ask: `--allow-write` offers the write tools (needs a token with the `write` scope), `--allow-publish` the release tools (needs `publish`). The two are alternatives, not a pair. Asking is not getting: the server still checks the token's scopes and refuses a session it may not open. See `internal/mcp/README.md`. |

`check` reads like CI output:

```text
✓ 42 messages discovered (brotwerk on https://glossa.example.com)
  policy: server v7
  layers: structure, parity, completeness
✓ message structures valid
✓ parity valid
✓ de complete
✗ en 1 missing
  error checkout.payment_failed  missing translation

Localization check failed: 1 error, 0 warnings.
```

Missing translations are errors in required locales and warnings in the
others; outdated translations and compatibility warnings are warnings.
A layer is a `layers.Checker` in `internal/quality`, so the command, the
pull-request check and the server's jobs run one implementation and
cannot disagree about what was found or where.

### Which policy `check` uses

The project stores a check policy (Studio: **Settings → Pull request
check**), and it is the same one the Glossa pull-request check decides
by, so the terminal and the pull request agree. `check` resolves it in
this order, most specific first:

1. **Flags** — `--require-complete`, `--fail-on`.
2. **`glossa.yaml`** — `check.require_complete`, `check.fail_on`.
3. **The project's check policy**, fetched from the server and cached at
   `.glossa/policy.json` with its version.
4. **That cache**, when the run is `--offline` or the server is out of
   reach. The output says so, and `policy.source` is `cache`.
5. **The built-in default** — every locale must be complete, an
   untranslated key in one is an error, and errors fail — when there is
   neither a server nor a cache. The output says *that*, loudly: a check
   must never silently grade itself.

Each level fills in only what it names, so `check.fail_on: warning` in
`glossa.yaml` leaves the project's required locales alone.

One setting has no flag and no `glossa.yaml` key: `missing_translations`
(whether an untranslated key in a required locale is an error or only a
warning) is always the project's. Whether a gap blocks is the project's
call, not a local one — otherwise a green terminal and a red pull
request would disagree about the one question the check exists to
answer. `check --json` says which policy it used in `policy.source`
(`server`, `cache` or `default`) and which version in `policy.version`.
`--layer` is a local override like the other two: it is honoured for
this run and ignored by the pull-request check, so a developer can
narrow their own loop and cannot change what CI decides.

`check --terminology` (or `--layer terminology`) adds the termbase layer
— the server's terminology QA over every translation but rejected ones:
a forbidden term is an error, a deprecated or missing one a warning.

### Recording a run

**In CI the check records itself.** The run and its findings are posted
to the project (`POST …/check-runs`), which is what makes them visible
in Studio's quality view, in `glossa findings`, in the findings list and
in the quality summary: a product whose CI already runs `glossa check`
populates the dashboard by doing what it already does, with no new flag
in the workflow and no wider token — recording needs `catalog.write`,
which a CI token holds beside `catalog.read`.

It is **not** the default on a laptop. A developer running the check in
a loop while editing a message would post a run every few seconds, and
each one becomes the project's newest — the run every dashboard reads;
and a local run may carry local overrides the pull-request check
ignores on purpose. `--record` records anyway, `--record=false` never
does, and `--offline` (or a run that fell back to the cached policy
because the server was out of reach) cannot: there is nothing to record
to, and a run graded against a cache is not the project's verdict.

What is sent is the findings, the layers that ran, the branch and the
commit — and nothing else. The server computes every **fingerprint**
itself, over the catalog message the key resolved to (a print minted
locally over a key is not the one the waiver list and the pull-request
check compute for the same finding), re-grades every severity against
the project's stored policy, and reaches its own conclusion. A failed
recording never changes the exit code: a check reports what it found,
and failing to file the report is not the same as failing the check.

`check --explain-policy` answers "why did this fail?" mechanically. For
each finding it names the rule that decided its severity, how specific
that rule was, and what stops it from failing the run:

```text
Why each finding has its severity
  terminology term_forbidden  rule 2 → error (enforce)
    rule 2 (layer=terminology, namespace=legal) is the most specific match (2 of 5 selector fields); ties go to the later rule; fail_on is error, so it fails the run
  visual text-clipped  rule 3 → warning (warn)
    rule 3 (layer=visual) is the most specific match (1 of 5 selector fields); ties go to the later rule; mode: warn, so it reports and can't change the conclusion
```

`check`'s exit codes are RFC 0005 §4.4's, exactly: **0** nothing at or
above `fail_on` after waivers and the policy · **1** the policy failed
the run, the only code CI should branch on · **2** the policy couldn't
be resolved, an unknown layer was named, or the configuration is wrong
· **3** the server or auth is unreachable *and* there is no cached
policy (with a cache the check runs offline and exits 0 or 1) · **4**
some layers ran and some couldn't — the findings are reported, the
layers that didn't run are named, and CI decides. A run that both failed
the policy and lost a layer exits 1.

### Fixing what a finding describes

`check --fix` applies the structured fixes findings carry, to the local
catalogs. Its whole discipline is one sentence: **apply only what a fix
actually describes.**

| Fix | What happens |
|---|---|
| `replace` with a `hint` | The message becomes that text. |
| `shorten` with a `hint` | The same: the hint is the shortened text. Without one the fix names a length and no words, so it is reported and not applied — shortening is a decision about the words. |
| `use-term` with a `hint` and a target span | The span's bytes become the term, in place. Applied only while the catalog still reads exactly the subject the layer found at those offsets: a span is an offset into the text the layer saw, and text that has moved on makes the same offsets point at different words. |
| `adopt-source-change` | Never applied. It asks for a translation made against the new source, which is a translation and not an edit; `glossa translate` or a translator makes one. |

A waived finding is never touched — somebody accepted it on purpose. A
run applies at most one fix per message, because a second fix's span was
measured against the text the first one replaced; the rest are reported
and the command says to run again. Every fix, applied or not, is in
`--json`'s `fixes` with its reason, and an edited catalog is written in
the canonical form `glossa pull` uses (sorted keys, two-space indent).

The verdict a `--fix` run prints is the run's own, from *before* the
edits, and so is its exit code: the check graded what it found, and a
green exit would be grading a project nobody has checked. Run `glossa
check` again to confirm.

## Findings, waivers and the policy as a file

`glossa findings` lists what the server stored — including the layers
`glossa check` never computes, because they need a capture, a termbase
or a provider (RFC 0005 §2.2). It prints each finding's **fingerprint**,
which is its identity across the terminal, the pull request and the
server, and the argument the next command takes.

`glossa waive <fingerprint> --reason "…"` accepts one finding. The
reason is required, and refused locally before the request goes out: a
waived finding is still computed, still reported at severity `waived`
and counted on its own, and the reason is what makes it reviewable
later. A waiver without one is a suppression nobody has to justify. A
waiver is made against the source revision the finding carries and dies
when the source moves past it; `--scope branch` limits it to one branch
(the CI branch unless `--ref` names another), and `--expires
2026-12-31` retires it at the end of that day. `--list` shows the
project's waivers with what each accepts, and `--revoke <id>` takes one
back.

`glossa policy export --file policy.yaml` and `glossa policy import
--file policy.yaml` are how the document becomes reviewable in a pull
request without making the policy a property of a commit — the server
stays the source of truth (RFC 0005 §4.2). The file holds what the
policy *says*; `version`, `effective_from`, `grace_until` and `previous`
are the server's, and a file carrying them is refused rather than
quietly stripped. `import` works with a GitHub Actions credential, so a
workflow needs no stored secret.

`glossa policy diff --file policy.yaml` answers §4.3's question before
anyone saves anything: which parts of the document change, how many
stored findings move severity, and — the number that decides whether a
policy ships with a grace — how many **open pull requests would newly
fail**, per rule. It stores nothing.

```text
check policy v7 → the document you passed
WHAT                  NOW      WOULD BE
missing_translations  warning  error
rules[1]              layer=terminology, namespace=legal → error (enforce)  layer=terminology → error (enforce)

Impact  measured against 210 stored findings from 12 runs
  34 raised · 2 lowered · 5 no longer computed
  ! 34 findings start failing a run
  ✗ 40 open pull requests would newly fail
    ship the rules in `mode: warn` first, or save with --grace-days, so nobody is failed for something they did not do
```

## `status --quality`: not measured is not zero

`glossa status --quality` prints RFC 0005 §8's seven numbers from the
server's summary endpoint, so the terminal and the dashboard cannot
disagree about how healthy a project is (intent §45, §46).

The rule that matters is that a number nobody computed is **absent**
from `--json` and named in `unmeasured` with why, and printed in the
human output as `not measured — <reason>` where a number would go. A
`0 %` for "nobody has measured this" would be the same lie a dashboard
would tell, somewhere harder to notice. The same holds one level down: a
percentile over an empty sample and a pass rate over no graded check are
absent, not 0. Per locale, every layer is listed available or not, so a
layer a locale cannot run never reads as a clean one (intent §41).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | OK |
| 1 | A check failed: `check`, `terms check`, `diff --exit-code`, `generate --check`, `extract --strict`, `release publish --dry-run` (not releasable), `translate --dry-run` (a refusal: consent off, no budget, no provider), `import --format` (conflicts or invalid items, dry run or not), `jobs show --wait` (the same for an import), `import --from v0 --verify` (a rendering differs other than by v0.3's known apostrophe defect: `renderings_differ`), `workflow lint` (a save would be refused), `audit verify`, or `audit export --public-key` (the export does not verify, including one missing a file) |
| 2 | Usage or configuration: bad flags, missing/invalid glossa.yaml, catalog or style file, unavailable command, `audit verify` without a usable `--public-key` (`public_key_required`, `invalid_public_key`) or with an export path that isn't there (`export_unreadable`), an `audit export` without a usable range or `--out` (or into files that exist: `output_exists`), `range_not_contiguous`, `range_too_long`, `sequence_out_of_range`, input the server rejects as invalid (`invalid_environment`, `invalid_note`, `invalid_key_name`, `idempotency_key_reused`, and every 400 of the Knowledge and Intelligence APIs, e.g. `invalid_locale`, `duplicate_term`), `locale_not_found`, an ambiguous term or key (`term_ambiguous`, `suggestion_ambiguous`), a Git connection the flags can't name (`unknown_repository`, `repository_ambiguous`, `unknown_installation`, `unknown_application`, `project_not_found`, `invalid_connection`), `check` with an unknown `--layer` or a cached policy it can't read (`invalid_policy_cache`), `waive` with no reason (`waiver_needs_a_reason`) or something that isn't a fingerprint, a policy file that can't be read or isn't a document (`policy_file_unreadable`, `invalid_policy_file`), and a policy the server refuses as invalid (`invalid_check_policy`, `invalid_severity`, `unknown_layer`, `advisory_layer`, `unknown_locale`, `invalid_environment`, `invalid_waiver`), a workflow file that can't be read or isn't a document (`workflow_file_unreadable`, `invalid_workflow_file`, `workflow_file_unwritable`), a workflow document the server refuses (`invalid_workflow`, with its findings), a definition or binding the arguments can't name (`workflow_not_found`, `workflow_ambiguous`, `workflow_not_bound`, `binding_ambiguous`), a binding or assignment the server refuses as invalid (`invalid_workflow_binding`, `workflow_definition_out_of_scope`, `invalid_assignment`, `unknown_party`), `deny` without `--reason` (`reason_required`), a decision or withdrawal the server refuses as invalid (`invalid_approval`, `invalid_withdraw_reason`), a rollout start or advance the server refuses as invalid (`invalid_percent`, `invalid_max_duration`, `force_reason_required`, `invalid_force_reason`, `invalid_idempotency_key`) |
| 3 | Network or auth: server unreachable, token missing or refused, forbidden, not found (`term_not_found`, `suggestion_not_found`), server error, or the server refusing the operation (`release_ineligible`, `no_rollback_target`, `not_in_history`, `not_releasable`, `key_revoked`, `storage_unavailable`, `suggestion_decided`, `suggestion_outdated`, `translation_conflict`, `translation_rejected`, `precondition_failed`, `job_not_cancellable`, `upload_not_expected`, `export_not_ready`, `file_expired`, `github_not_configured`, `github_unavailable`, `repository_not_visible`, `application_not_found`, `connection_exists`, `installation_revoked`, `workflows_scope_required`, `assignments_manage_required`, `workflow_definition_exists`, `workflow_binding_exists`, `workflow_limit_reached`, `workflow_instances_unavailable`, `assignment_state`, `policy_not_met`, `branch_release_not_promotable`, `rollout_active`, `rollout_no_stable`, `rollout_candidate_served`, `rollout_branch_environment`, `rollout_source_locale`, `rollout_needs_approval`, `rollout_ended`, `no_active_rollout`, `precondition_failed` on a rollout someone changed since it was read, `person_required` (an API token deciding an approval), `own_text`, `not_eligible`, `approval_not_requested`, `release_request_closed`, `approval_closed`, `approval_superseded`, `approval_not_found`), `translate --wait`, `import`, `export` or `jobs show --wait` giving up (`wait_timeout`), a transfer that doesn't check out (`upload_corrupted`, `download_corrupted`, `download_interrupted`). Import/export input the server rejects (`invalid_format`, `invalid_options`, `empty_file`, `file_too_large`, …) is 2. `check` only gets here when there is no cached policy either: with `.glossa/policy.json` it runs against the local catalogs and exits 0 or 1; `waive --revoke` on a waiver that isn't there (`waiver_not_found`), `policy show`/`export` against a server whose Quality context predates the endpoint (`no_check_policy`) |
| 4 | Partial failure: `check` ran some layers and couldn't run others (they're named in the output, never dropped in silence); `push` or `import --from v0` went through but some items failed; `translate --wait`: some jobs failed; `import --format`, `export`, `jobs show --wait`: the job failed or was cancelled |
| 5 | Held for approval (`release_held`): `release publish` or `release promote` into an environment that requires approvals went through, but nothing was deployed — the release is recorded and a release request waits for people to approve it (`glossa approve <request>`). The `--json` document is the command's own, with `held: true` and the `release_request`, not an error |

Errors print what happened, where, why and how to fix it:

```text
error: no API token for https://glossa.example.com
  where: https://glossa.example.com
  why:   GLOSSA_TOKEN is unset and `glossa login` stored no token for this server
  fix:   run `glossa login`, or set GLOSSA_TOKEN (CI) to a token created in Studio
```

## JSON output

With `--json` a run prints exactly one JSON document to stdout, tagged
with `schema`. New fields may be added; existing ones keep their meaning.

| Schema | Shape |
|---|---|
| `glossa.cli.error/v1` | `{error: {code, exit_code, message, where?, why?, fix?}}` |
| `glossa.cli.init/v1` | `{path, config, checked_with_server}` |
| `glossa.cli.login/v1` | `{server, stored_in, tenant: {id, slug, name, kind}}` |
| `glossa.cli.whoami/v1` | `{server, token (redacted), token_source, tenant, project?: {id, slug, name, source_locale}}` |
| `glossa.cli.push/v1` | `{dry_run, source, branch?: Branch, summary: {created, revised, updated, unchanged, failed}, messages: [{key, status, revision?, state?, error?: {code, detail}}], translations?: [{key, locale, status, state?, error?}]}`; with `--branch` a message's status is `new_key`, `source_proposal`, `unchanged`, `key_conflict` or `failed` |
| `glossa.cli.branch/v1` | `{action (status \| close), branch: Branch}` where Branch is `{id, name, state, pr_number?, head_commit?, preview_url?, new_keys?, source_proposals?, removed?, conflicts?: [{key, branches}], outdated?: {locale: n}}` |
| `glossa.cli.preview/v1` | `{url, branch: Branch}` |
| `glossa.cli.github.connections.list/v1` | `{project_id (null: the whole workspace), connections: [Connection]}` |
| `glossa.cli.github.connections.add/v1` | `{connection: Connection}` |
| `glossa.cli.github.connections.remove/v1` | `{connection_id}` |
| `glossa.cli.workflow/v1` | Every `workflow` action, with `action` naming it: `lint` `{file, valid, errors, warnings, findings: [Finding]}`; `push` and `import` `{file, result (created \| saved \| unchanged \| invalid), definition: Definition \| null, findings: [Finding]}` (with `invalid`, exit 2 and the findings that refused it); `pull` and `export` with `-o` `{file, definition: Definition, version}` (without `-o` they print the document itself, which is what `push` and `import` read); `rebase` `{from_version, instance: Instance}`; `list` `{project_id, definitions: [Definition]}`; `show` `{definition: Definition, version, document, versions: [{version, created_by, created_at}], project_id, bindings: [Binding]}`; `bind`, `unbind` `{result (created \| unchanged \| removed), binding: Binding}`; `bindings` `{project_id, bindings: [Binding]}`; `instances` `{project_id, instances: [Instance]}`; `log` `{instance: Instance, transitions: [Transition]}` |
| `glossa.cli.assignments/v1` | Every `assignments` action, with `action` naming it: `list` `{mine (false only with --all), assignments: [Assignment]}`; `show`, `accept`, `complete`, `decline` `{assignment: Assignment}`; `create` `{replayed?, assignment: Assignment}`; `report` `{project?, vendor?, since?, generated_at, truncated, rows: [AssignmentReportRow]}` |
| `glossa.cli.pull/v1` | `{states, locales: [{locale, path, messages, skipped: {state: n}, outdated, changed}], release?: {dir, release_id, version, environment, locales, artifacts, bytes, removed}}` |
| `glossa.usages/v1` | `extract --json` (with or without `--upload`): `{application, commit, branch, tool: {name, version}, usages: [{key, file, line, column, component?, route?, kind}]}`, sorted by key, file, line, column; kind is `t`, `component`, `element`, `accessor` or `template` (the schema: `runtimes/testdata/schemas/usages.v1.schema.json`) |
| `glossa.cli.context.push/v1` | `{file, source, replayed, build: {id, application_id, commit, branch, on_default_branch, source, tool: {name, version}, digest, usages, unknown_keys, created_by, created_at}}` (the API's `ContextBuild`) |
| `glossa.cli.capture/v1` | `{application, commit, branch, captures: [{route, url, locale, viewport: {width, height, deviceScaleFactor?}, image: {sha256, width, height}, renders, regions, visible, redacted, truncated}], output?: {dir, manifest, images}, upload?: {build, captures, images_stored, images_deduplicated, unknown_keys, replayed}, coverage: {messages, captured, not_captured: [{key, usages, file, line, routes?}]} \| null}` (`capture`; the regions themselves are in the manifest) |
| `glossa.cli.generate/v1` | `{source, messages, check, files: [{path, kind, changed}], warnings: [{key, reason}]}` |
| `glossa.cli.check/v2` | `{policy: {require_complete (null = all), fail_on, missing_translations, source (server \| cache \| default), version?, overridden?, offline?, fetched_at?, grace?: {previous_version, until}}, origin, messages, invalid_messages, locales: [{code, is_source, required, messages, translated, missing, outdated, errors, warnings, waived, complete}], layers, skipped_layers, unavailable_layers: [{layer, why}], findings: [`glossa.finding/v1`], explain?: [{fingerprint, layer, code, severity, rule: {index, selector, severity, mode} \| null, mode, fails, why}], fixes?: [{fingerprint, layer, code, kind, locale?, key?, applied, file?, from?, to?, why?}] (`--fix`; the counts and the conclusion are the run's from before them), errors, warnings, waived, conclusion, passed}` — a finding is the Quality context's own (`runtimes/testdata/schemas/finding.v1.schema.json`), with its locus, spans and evidence intact |
| `glossa.cli.findings/v1` | `{run: {id, ref, commit?, trigger, conclusion?, policy_version, layers, started_at} \| null, counts: {errors, warnings, waived} (the whole run's, whatever the filters selected), findings: [`glossa.finding/v1`], truncated}` |
| `glossa.cli.waive/v1` | `{action: created \| unchanged \| revoked, waiver: Waiver}` |
| `glossa.cli.waivers/v1` | `{waivers: [Waiver]}` (newest first) |
| `glossa.cli.policy/v1` | `{version, policy: `glossa.check-policy/v1`, created_by?, created_at?, grace?: {previous_version, until}}` (`policy show`) |
| `glossa.check-policy/v1` | `policy export` without `--file` writes the document itself — YAML, or JSON with `--json` — which is exactly what `policy import` reads: `{schema, require_complete (null = every locale, [] = none), fail_on, missing_translations, environments?: {name: {require_complete?, require_review?}}, rules?: [{layer?, code?, locale?, namespace?, environment?, severity, mode?}]}` |
| `glossa.cli.policy.export/v1` | `{file, policy: `glossa.check-policy/v1`}` (`policy export --file`) |
| `glossa.cli.policy.diff/v1` | `{from_version, from, to (both `glossa.check-policy/v1`), changes: [{what, from, to}], impact: Impact}` |
| `glossa.cli.policy.import/v1` | `{dry_run, file, version, policy, impact: Impact, grace?: {previous_version, until}}` |
| `glossa.cli.status/v1` | `{origin, messages, locales: [{code, direction, is_source, translated, approved, needs_review, draft, rejected, outdated, missing, coverage}]}` |
| `glossa.cli.status.quality/v1` | `{project_id, environment, locale?, since, computed_at, cached, run: {…} \| null, numbers: {coverage?, findings?, ai?, queue?, context?, lead_time?, checks?, by_layer?}, locales: [{code, direction, is_source, coverage?, findings?, ai?, queue?, lead_time?, layers: [{layer, available, checked, unavailable?, findings?}]}], unmeasured: [{number, reason}]}` — **every number is optional, and an absent one means nobody measured it**, never 0; `unmeasured` says why |
| `glossa.cli.diff/v1` | `{source: {locale, added, changed: [{key, local, server}], removed, unchanged}, translations: [same], identical}` |
| `glossa.cli.locales/v1` | `{locales: [{code, direction, is_source}], fallback}` |
| `glossa.cli.projects.list/v1` | `{tenant, projects: [{id, slug, name, source_locale, locales}]}` |
| `glossa.cli.projects.create/v1` | `{tenant, project: {id, slug, name, source_locale, locales}, locales_added}` |
| `glossa.cli.tenants/v1` | `{tenants: [{id, slug, name, kind}]}` |
| `glossa.cli.messages/v1` | `{messages: [{key, namespace, state, source_revision, text, syntax, arguments: [{name, type}], description?}]}` |
| `glossa.cli.namespaces/v1` | `{namespaces: [{name, active_messages, obsolete_messages}]}` (by name) |
| `glossa.cli.import/v1` | `{from, source: {url, project} \| {db, tenant, project, project_name, default_locale}, dry_run, locales_added, summary: {message: {status: n}, translation: {status: n}}, items: [{kind, key, locale, status, v0_status?, state?, downgraded?, reason?, error?, description?, origin_detail?}], restore?, locales?, invitations?: [{v0_user_id, email, roles, locales, v0_role, v0_locales, v0_created_at, status, reason?, member_id?}], audit_entries?, history?: {sent, recorded, existing}, not_carried?, warnings?}` (`--from v0`; the optional members come from `--v0-db`; an invitation's `status` is `planned`, `held`, or with `--invite` `invited`, `exists` or `failed`; `history` is `--history`'s) |
| `glossa.cli.import-verify/v1` | `{from, source: {db, tenant, project, restore} \| {url, project}, edge, environment, release, summary: {renderings, keys, locales, match, known_defect, mismatch}, known_defects: [row], mismatches: [row], runtime_errors?}`, a row being `{key, locale, args, v0, runtime, v0_error?, runtime_error?, verdict, defect?, v0_requoted?}` (`import --from v0 --verify`; `defect` is `v0_bare_apostrophe`, and `v0_requoted` its evidence) |
| `glossa.cli.import.job/v1` | `{format, mode (dry_run \| merge \| overwrite), dry_run, scope (project \| tenant), file: {path, size, sha256}, job: Job, waited, results: [Result], results_filter (problems \| all)}` (`import --format`, `tm import`, `terms import`) |
| `glossa.cli.export/v1` | `{format, scope, job: Job, waited, file: {path?, name, size, sha256, content_type, verified} \| null, extracted: [{path, size}]}` (`export`, `tm export`, `terms export`) |
| `glossa.cli.jobs.list/v1` | `{jobs: [Job]}` (newest first; the project's and the workspace's, or with `--all-projects` the tenant's) |
| `glossa.cli.jobs.show/v1` | `{job: Job, results: [Result], results_filter}` |
| `glossa.cli.jobs.cancel/v1` | `{job: Job}` |
| `glossa.cli.release.publish/v1` | `{replayed, idempotency_key, release: Release, held, release_request?: ReleaseRequest}` — `held: true` (exit 5) when the environment requires approvals: the release was recorded, `release_request` waits for them, nothing was deployed |
| `glossa.cli.release.preview/v1` | `{environment, policy: {states, include_outdated}, base: Ref \| null, releasable, problems: [{code, detail, key?, locale?}], release: {source_locale, locales: [code], manifest_digest, counts} \| null, changes: [{locale, added, changed, removed}], identical}` (`release publish --dry-run`) |
| `glossa.cli.release.list/v1` | `{releases: [Release & {serving: [environment]}]}` (newest first) |
| `glossa.cli.release.show/v1` | `{release: Release, serving: [environment]}` |
| `glossa.cli.release.diff/v1` | `{release: Ref, base: Ref \| null, locales: [{locale, added, changed, removed}], identical}` |
| `glossa.cli.release.promote/v1`, `glossa.cli.release.rollback/v1` | `{environment: Environment, previous: Ref \| null, held, release?: Ref, release_request?: ReleaseRequest}` — a held promote (exit 5) leaves `environment` as it was; `release` is what `release_request` would deploy |
| `glossa.cli.release.requests/v1` | `release requests`, with `action` naming it: `list` `{environment?, state? (absent: all), release_requests: [ReleaseRequest & {release: Ref}]}` (newest first); `show` `{release_request: ReleaseRequest & {release: Ref}, approval: Approval \| null}` (null: not asked yet, or a credential that can't read workflows); `withdraw` `{release_request}` |
| `glossa.cli.release.rollout/v1` | `release rollout`, with `action` naming it: `start`, `status`, `advance`, `complete`, `abort` `{environment, replayed? (start), idempotency_key? (start), etag (the rollout's version, for --if-match), previous_percent? (advance), rollout: Rollout \| null}` (`status` with no active rollout: `null`); `list` `{environment, rollouts: [Rollout]}` (newest first) |
| `glossa.cli.approve/v1` | `approve`, `deny`: `list` `{project_id, release_requests: [ReleaseRequest & {release: Ref}], approvals: [Approval]}` (pending only: release requests newest first, translation approvals oldest first); `approve`, `deny` `{kind (release_request \| translation), release_request?: ReleaseRequest & {release: Ref} (read again after the decision: `deployed` once the last approval deployed it), approval: Approval}` |
| `glossa.cli.translations.review/v1` | `translations review`: `{state, reviewed, failed, units: [{key, locale, from?, state?, revision?, outdated?, retried?, error?: {code, message, why?, fix?}}]}` (a single unit that fails is the usual `glossa.cli.error/v1`) |
| `glossa.cli.release.environments/v1` | `{environments: [Environment]}` |
| `glossa.cli.release.keys/v1` | `{keys: [DeliveryKey]}` |
| `glossa.cli.release.key/v1` | `{action: created \| scoped \| revoked, key: DeliveryKey}` |

| `glossa.cli.tm.search/v1` | `{query: {text, from, to, syntax}, source_normalized, matches: [{score, kind (exact \| context \| fuzzy), target (MF2), target_text, target_syntax (the query's syntax; mf2 when MF1 can't express the target), variables_adapted, unit: Unit}]}` |
| `glossa.cli.tm.concordance/v1` | `{query: {text, side, from?, to?}, matches: [{similarity, unit: Unit}]}` |
| `glossa.cli.tm.units/v1` | `{units: [Unit]}` |
| `glossa.cli.tm.retire/v1` | `{unit: Unit}` |
| `glossa.cli.terms.list/v1` | `{concepts: [Concept]}` |
| `glossa.cli.terms.show/v1` | `{concept: Concept}` |
| `glossa.cli.terms.change/v1` | `{action: created \| updated \| unchanged \| deprecated \| forbidden, concept: Concept}` (`add`, `edit`, `deprecate`, `forbid`) |
| `glossa.cli.terms.check/v1` | `{fail_on, locales: [{code, checked, errors, warnings}], findings: [{code (term_forbidden \| term_missing), severity, locale, key, side (source \| target), text, start, end, suggestions, concept_id, term_id, message}], checked, errors, warnings, passed}` |
| `glossa.cli.style.show/v1` | `{scope: Scope, fields: StyleFields, rules: [StyleRule], sources: [{style_guide_id, version, project_id?, locale?, namespace?}]}` |
| `glossa.cli.style.edit/v1` | `{action: created \| updated \| unchanged, guide: {id, name, scope: Scope, version, fields: StyleFields, rules: [StyleRule]}}` |
| `glossa.cli.translate/v1` | `{dry_run, locales, filter: {namespace?, key_prefix?, missing, outdated}, plan: [{locale, queue, keys, existing, tm_exact, provider, refused: {reason: n}, cost: Cost}] (dry run), skipped: {reason: n}, refusals: [{code, message, fix}], cost?: Cost (dry run), fills: [{id, locales, keys?, jobs_created, jobs_existing, skipped, job_states, warnings}], wait: {elapsed_ms, job_states: {state: n}, failed: [{id, key, locale, state, failure_code?, error?}]} \| null}`; `Cost` is `{estimated_micro_usd, max_micro_usd, unpriced}` |
| `glossa.cli.review.list/v1` | `{suggestions: [Suggestion]}` (riskiest first) |
| `glossa.cli.review.decision/v1` | `{decision: accepted \| rejected, edited, suggestion: Suggestion}` |
| `glossa.cli.ai.status/v1` | `{consent: {enabled, changed_at?, changed_by?}, max_concurrent_jobs, budget: {monthly_micro_usd, spent_micro_usd, remaining_micro_usd, month_start, calls, by_provider: [{provider, model, calls, cost_micro_usd, input_tokens, output_tokens}]}, providers: [{name, kind, enabled, api_key_set, base_url?, models}], project: {auto_translate_locales, namespace_tags: {namespace: [tag]}, review: {auto_approve, auto_approve_min, recommend_min, auto_approve_environments?, force_review?}}}` |
| `glossa.cli.audit.list/v1` | `{tenant, filter: {from, to, actor?, action?, project?, source?}, count, limit, more, entries: [AuditEntry]}` (the API's entries, as served) |
| `glossa.cli.audit.export/v1` | `{job: {id, state, from?, to?, first_sequence?, last_sequence?, entry_count?, key_id?, failure_code?, expires_at}, waited, dir?, files: [{name, path, size, sha256, verified}], verification: <audit.verify document> \| null}` |
| `glossa.cli.audit.csv/v1` | `{source, out, rows, columns}` (with `--out`) |
| `glossa.cli.audit.verify/v1` | `{path, ok, tenant_id?, key_id?, created_at?, first_sequence?, last_sequence?, last_hash?, entry_count, verified, failure: {check, line?, sequence?, reason} \| null}`; `check` is one of the codes under *Audit*, and the manifest's fields are absent when it did not parse |

The import and export shapes share:

- `Job`: `{id, direction (import \| export), kind (catalog \| tm \| termbase), format, mode? (imports), state (awaiting_upload \| queued \| running \| succeeded \| failed \| cancelled), project_id (null: tenant-wide), file_name, file?: {size, sha256, content_type}, options (the API's ImportOptions or ExportOptions), total_items?, processed_items?, summary?: {created, updated, unchanged, conflict, invalid, by_kind: {kind: counts}} (imports), written? (exports), reused_job_id?, failure_code?, failure_message?, cancel_requested, created_by, created_at, started_at?, finished_at?, expires_at, files_deleted_at?}`
- `Result`: `{seq, kind (message \| translation \| tm_unit \| concept), key, locale?, status (created \| updated \| unchanged \| conflict \| invalid), code?, detail?, line?, column?, ref? (the item in the format's own terms: an XLIFF fragment identifier `#/f=…/u=…`, a JSON pointer, `msgctxt "…" msgid "…"`, `tu[n]`, `conceptEntry[n]`), location? (file:line:column)}`

The Knowledge and AI shapes share:

- `Unit`: `{id, source_locale, target_locale, source, target, project_id (null: tenant-wide), message_key?, namespace?, origin, state (active \| retired), hit_count, created_at, retired_at?, retired_reason?}`
- `Concept`: `{id, project_id (null: tenant-wide), definition, domain, note, version, terms: [{id, locale, text, status (preferred \| admitted \| deprecated \| forbidden), part_of_speech?, case_sensitive, note?}], updated_at}`
- `Scope`: `{project_id, locale, namespace}` (null: broader)
- `StyleFields`, `StyleRule`: the API's (`formality: {register, pronoun}`, `tone`, `punctuation`, `numbers`, `dates`; `{id, title?, rationale?, good?, bad?, disabled?}`)
- `Suggestion`: `{id, key, locale, namespace, text (MF2), score, action (auto_approve \| approve_recommended \| review_required), action_note?, risk_tags, status, origin (ai \| translation_memory), provider?, model?, explanation: [{factor, value, contribution, reason}], findings: [{code, severity?, message, term?}], translation_revision?, created_at}`

The release shapes share:

- `Release`: `{id, version, environment (published to), parent_id?, note?, author, created_at, source_locale, locales: [code], manifest_digest, policy: {states, include_outdated}, counts: {messages, artifacts, new_artifacts, bytes, locales: {code: {messages, outdated}}}}`
- `Ref`: `{id, version}`
- `Environment`: `{name, release: Ref | null, policy: {states, include_outdated}, updated_at}`
- `DeliveryKey`: `{id, name, key, scope: {environments, branches}, created_at, revoked_at?}`
- `ReleaseRequest`: `{id, environment, release_id, action (publish \| promote), requester, approval: {n, from: {member? \| role? \| group?}, distinct_from_requester}, gate: {met, unmet?}, forced, force_reason?, state (pending \| deployed \| denied \| withdrawn \| refused), decided_by?, decided_at?, reason?, created_at}`
- `Approval`: `{id, project_id, instance_id?, subject (translation \| release_request), subject_id (the message, or the release request), message? (the key, when it could be read), locale?, required, granted (distinct grants so far), eligible: {kind, id?, role?}, distinct_from_author, due_at?, state (pending \| granted \| denied), decisions: [{principal, decision (granted \| denied), reason?, at}], created_by, created_at, closed_at?}`
- `Rollout`: `{id, environment, release_id (the candidate), stable_release_id, release: Ref, stable_release: Ref, percent, status (active \| completed \| aborted), end? (completed \| aborted \| expired \| rolled_back), max_duration_seconds, expires_at, forced, force_reason?, started_by, started_at, updated_at, ended_by?, ended_at?}`

The Quality shapes share:

- `Waiver`: `{id, fingerprint, reason, scope (project | branch), ref?, source_revision, active, expires_at?, revoked_at?, created_by, created_at, accepts: {layer?, code?, locale?, key?, namespace?, message?} | null}` — `accepts` is read from the most recent stored finding carrying the fingerprint, and is null where none does any more, which is exactly the unexamined waiver a dashboard should show
- `Impact` (RFC 0005 §4.3): `{findings, runs, raised, lowered, silenced, newly_failing, no_longer_failing, open_pull_requests, newly_failing_refs, no_longer_failing_refs, rules: [{rule (its index in the candidate's rules), selector, severity, mode, matched, changed, newly_failing}]}`

The Git connection shapes share:

- `Connection`: `{id, installation_id, repository_id (GitHub's numeric id), repository_name (owner/name, a label GitHub may change), project_id, project? (the slug, left out when it couldn't be read), application_id, application?, default_branch, path ("": the whole repository), created_by?, created_at, updated_at?, version}`

The workflow and assignment shapes share (the Workflow API's, RFC 0006 §2–§3):

- `Definition`: `{id, name, subject (translation \| release_request), project_id (null: tenant-wide), version (the latest, or the one a push saved), created_by, created_at, deleted_at?}`
- `Finding` (a workflow lint finding, not `glossa.finding/v1`): `{rule, severity (error \| warning \| info; error and warning refuse a save), path? (guards.two_approvals; absent: the whole document), state?, event?, message}`
- `Binding`: `{id, definition_id, definition? (its name, when it could be read), subject, locales ([]: every locale), namespace?, position (creation order; the later of two equally specific bindings wins), created_by, created_at}`
- `Instance`: `{id, definition_id, definition?, definition_version, subject, subject_id (the message, for a translation), locale?, state, status (active \| finished), created_at, updated_at}`
- `Transition`: `{seq, from ("" for the event that created the instance), event, to, outcome (applied \| ignored \| refused), guards: [{guard, passed}], actions: [{name, outcome (done \| refused \| failed), detail?}], actor, outbox_event_id?, at}`
- `Assignment`: `{id, project_id, instance_id? (the workflow instance whose assign action made it), units: [{message_id, message? (the key; show and create name it), locale}], assignee: {kind (member \| role \| group \| vendor), id?, role?}, permission, state (open \| accepted \| done \| declined \| expired), due_at?, created_by, created_at, updated_at, closed_by?, closed_at?, reason?}`

Finding codes are the kernel's (`missing-argument`, `extra-argument`,
`argument-type-changed`, `selector-*`, `invalid-plural-key`,
`missing-plural-category`, `markup-*`, plus the server's
`max-length-exceeded`) and the CLI's: `invalid-message`,
`invalid-translation`, `missing-translation`, `outdated-translation`,
`unknown-key`, `missing-locale`.

## Typed messages

`generate` writes, from `messageformat.Arguments`:

```ts
// messages.ts
export interface Messages {
  "cart.items": { count: number };
  "checkout.pay": { amount: number };
  "athlete.greeting": { gender: "female" | (string & {}); name: string };
}
export function createMessages(t: Translate) { … }   // messages.checkout.pay({ amount })

// glossa-vue.ts
declare module "@klarlabs-studio/glossa/vue" { interface GlossaRegister { messages: Messages } }
export function useTypedMessages() { … }              // in setup(): m.cart.items({ count })

// glossa-react.ts
declare module "@klarlabs-studio/glossa/react" { interface GlossaRegister { messages: Messages } }
export function useTypedMessages() { … }              // a hook: m.cart.items({ count }); <T id> is typed too
```

```go
// messages.go
m := msg.For(client.For("de"))          // or msg.FromContext(ctx, client)
subject := m.CheckoutPay(order.Total)   // func (m Messages) CheckoutPay(amount float64, opts ...glossa.Option) string
```

Types: numbers, integers, percents, currencies and units are `number` /
`float64` (`int` for integers); dates and times `Date | number | string` /
`time.Time`; selects the literal cases plus any string; everything else a
string. Key segments become camelCase (TS) and PascalCase (Go); keys whose
accessor would collide keep working by ID and are reported as warnings.

## Import and export

Interchange files go through the server's import and export jobs (RFC
0003 §6; platform/README.md *Integration*): XLIFF 2.1, flat or nested
JSON, gettext PO (import only), TMX 1.4b and TBX-Basic.

```sh
glossa import --format xliff translations/de.xlf            # dry run: what would happen, nothing written
glossa import --format xliff translations/de.xlf --apply    # merge
glossa import --format json locales/fr.json --locale fr --apply
glossa import --format po po/de.po --state needs_review --apply
glossa import --format xliff vendor/fr-CA.xlf --locale fr --apply  # the file's trgLang says fr-CA; the project has fr
glossa export --format xliff --locale de,fr -o out --unzip  # one XLIFF per locale
glossa export --format json --locale de --layout nested -o locales/de.json
glossa tm export -o memory.tmx          && glossa tm import vendor.tmx --apply
glossa terms export -o termbase.tbx     && glossa terms import glossary.tbx --apply
glossa tm import agency.tmx --scope tenant --apply                 # the workspace's memory (tm-import-jobs)
glossa namespaces                                                  # what --namespace can take
glossa jobs list && glossa jobs show <id> && glossa jobs cancel <id>
```

```text
Imported translations/de.xlf (xliff, 12.4 KiB) into brotwerk
✓ messages: 42 unchanged
✗ translations: 3 created · 38 unchanged · 1 conflict
translations/de.xlf:118:9: conflict translation de checkout.pay  approved_translation_conflict: …
```

- **Dry run by default.** `import --format` writes nothing unless
  `--apply` (mode `merge`) or `--overwrite` (the file's state wins;
  needs `integration.manage`) says so; `--dry-run` says it explicitly.
  The server's own default is `merge`, but a CLI step in CI that
  forgot a flag should report, not write: the dry run runs every check
  a merge runs (structure, the approval rule, concept and unit rules)
  and stores only its results, so a CI job can gate on it (exit 1) and
  a second step applies. `import --from v0` keeps its own contract
  (`--dry-run` to preview); `--from` and `--format` exclude each other,
  and each refuses the other's flags.
- **Flow.** Create the job (with a fresh `Idempotency-Key`, so a retried
  request creates one job) → `PUT` the file to its `upload_url` (streamed,
  progress on stderr on a terminal, `uploaded <name>` otherwise; the
  server's recorded SHA-256 must match what was sent, else
  `upload_corrupted` and the job is cancelled) → poll (`--poll-interval
  1s`, `--timeout 30m`, progress on stderr) → read the results, a page of
  100 at a time. `--no-wait` returns once the file is uploaded (the export
  once it is queued); `glossa jobs show <id> --wait` and `glossa export
  --job <id> -o …` pick up from there. Progress never goes to stdout, and
  not at all with `--json` or `--quiet`.
- **Results.** Conflicts and invalid items are listed in file order as
  `file:line:column: status kind [locale] key  code: detail` (the path as
  given, so editors and CI annotations jump to it) — every item, since
  the server records where each entry of the file is; `--json` adds
  `ref`, the item in the format's own terms (an XLIFF fragment
  identifier, a JSON pointer, a PO `msgctxt` and `msgid`, `tu[n]`).
  `--all-results` lists every item (`--json`: `results`). The summary counts
  created/updated/unchanged/conflict/invalid per kind (messages,
  translations, TM units, concepts). An applied import of a file and
  options already applied successfully reuses that job's result
  (`reused_job_id`) instead of applying it twice; dry runs always run.
- **Options per format.** The CLI refuses options a format doesn't take
  (exit 2) before creating a job:

  | Format | Import | Export |
  |---|---|---|
  | `xliff` | `--locale L` (the translations' locale, default its `trgLang`: names it for a file without one, or imports a file as another of the project's locales), `--syntax mf1` (read other tools' plain units as ICU) | `--locale L…` (one document per target locale; none: the source only), `--namespace N…`, `--state S…` |
  | `json` | `--locale L` (default the source locale: a source catalog, needs `integration.manage`), `--namespace N`, `--syntax mf1\|mf2` (default glossa.yaml's `syntax`), `--state S` (default `needs_review`) | `--locale L…` (default the source), `--namespace N…`, `--state S…`, `--layout flat\|nested`, `--syntax` |
  | `po` | `--locale L` (default its `Language` header), `--namespace N`, `--state S` (entries without `fuzzy`; default `approved`), `--plural-variable V` | — (import only) |
  | `tmx` | `--scope project\|tenant` | `--locale L…` (targets), `--source-locale L`, `--scope` |
  | `tbx` | `--scope project\|tenant` | `--scope` |

  `--locale`, `--namespace` and `--state` repeat or take commas on
  exports (`glossa namespaces` lists the project's); `--state` defaults
  to `approved`. An import's `--locale` must be one of the project's
  locales (`locale_not_found`, exit 2); a file whose translations are in
  a locale the project lacks fails `target_locale_mismatch` (exit 4)
  naming the project's locales — import it again with `--locale`.
  `--scope tenant` goes through the workspace's own routes
  (`tm-import-jobs`, `tm-export-jobs`, `termbase-import-jobs`,
  `termbase-export-jobs`): TMX and TBX imported tenant-wide, every unit
  or concept of the tenant exported; catalogs always belong to the
  project.
- **Downloads are verified.** `export` streams the file into a temporary
  file next to its destination while hashing it, compares the SHA-256
  with the `ETag` and the job's recorded digest, and only then renames it
  into place: a corrupted or interrupted download writes nothing (exit 3,
  `download_corrupted`). `-o` names the file (default: the server's file
  name in the working directory; an existing directory takes the file by
  that name). Several locales come as one zip (`<locale>.<ext>`
  entries); `--unzip -o <dir>` extracts it, refusing entries that would
  land outside the directory. A job's file is kept for a while (7 days by
  default); after that the download is `file_expired`.
- **Exit codes.** `import --format`: 0 ok, 1 conflicts or invalid items,
  2 usage or input the server rejects, 3 refused (permissions, a
  finished or expired job, the wait timing out, a corrupted transfer), 4
  the job failed (a malformed file: its problem is the last result, with
  line and column) or was cancelled. `export`: the same without 1.
- **Permissions.** A `read` token may export and see jobs; imports need
  `write` (translations of catalogs, within the token's locales); TMX,
  TBX, source catalogs and `--overwrite` need `integration.manage`, which
  `write` tokens hold. The job applies the access its requester had when
  they asked.

## Import from Glossa v0.3

```sh
export GLOSSA_V0_KEY=glossa_…   # a v0.3 project key (read is enough)
glossa import --from v0 --v0-url https://old.example.com/api/v1 --v0-project brotwerk-site --dry-run
glossa import --from v0 --v0-url https://old.example.com/api/v1 --v0-project brotwerk-site
```

- Reads `GET /projects/{slug}/locales` and each locale's
  `GET /projects/{slug}/locales/{locale}/messages` (`{messages, statuses}`).
- The project's source locale must exist in v0.3. Its values become
  messages (ICU MF1, parsed and validated by the kernel for the source
  locale); empty values are skipped.
- Missing locales are added. Every other non-empty value becomes a
  translation with provenance `import` and `origin_detail`
  `{source: "glossa-v0.3", url, project, status, v0_status}`.
- Statuses: `approved` → `approved`, `needs_review` and `ai_translated`
  → `needs_review`, `pending` → `draft`. Where the project requires
  review, API tokens can't approve, so approved values arrive as
  `needs_review` and are reported `downgraded`.
- Idempotent: messages are upserted; translations the server already
  has (same canonical model) aren't sent again, so a re-run never undoes a
  review made in between. Every key is reported.

### From a restored backup (`--v0-db`)

v0.3's API doesn't expose key descriptions, who last changed a
translation, its change history, its users or its locale labels; its
database does. Restore a v0.3 backup (plain `pg_dump`, gzipped or not)
into a **new** database on a scratch Postgres 16, then import from it:

```sh
PGHOST=localhost PGUSER=postgres platform/scripts/v0-restore.sh glossa-20261001.sql.gz glossa_v0_restore
PGPASSWORD=… glossa import --from v0 --v0-db postgres://postgres@localhost/glossa_v0_restore \
  --v0-tenant klarlabs --v0-project brotwerk-site --dry-run
```

- **It reads only a marked restore.** `v0-restore.sh` creates the
  database (it fails on an existing one), restores into it in one
  transaction, and runs `v0-restore-marker.sql`, which writes one row to
  `glossa_v0_restore.marker` naming the database by OID and name, and sets
  the database to default to read-only transactions. The importer refuses
  (`v0_not_a_restore`, exit 2) a database without the marker, with a
  marker written for another database (a copy), or without the read-only
  default — which a live v0.3 database can't have. There is no flag to
  skip this. It also refuses a schema that isn't v0.3's at migration 0006
  and a role row-level security would filter (it never reads fewer rows
  in silence).
- It reads in one read-only transaction and writes nothing to v0.3.
- Carried: `keys.description` as the message description;
  `translations.updated_by`, `updated_at`, `status` and `id` in the
  import revision's `origin_detail` (`v0_updated_by` is `v0:<user id>`,
  `v0:ai:<provider>` for v0.3's AI translator, or `null` where v0.3
  recorded nobody).
- v0.3's users become `invitations`: v0.3 `admin` → `admin`;
  `translator` → `translator` with its locales; a translator with no
  locales is `held`, since the platform reads an empty scope as every
  locale. By default they are reported as plans (`planned`, `held`).
  `--invite` sends the `planned` ones — never the `held` ones — through
  the members API with those roles and locales, and reports each as
  `invited`, `exists` (the address is already a member or invited:
  nobody is invited twice, and every request carries an
  `Idempotency-Key` derived from the address) or `failed` (exit 4), with
  its `member_id`. It needs a token that may invite (the `admin` scope);
  one that may not stops the run before anyone is invited. `--invite`
  and `--dry-run` exclude each other. An invitation is accepted when the
  person signs in with that address.
- v0.3's history: `audit_entries` reports each `audit_log` row as
  `v0.translation.changed` with actor `v0:<user id>`,
  `v0:ai:<provider>`, `v0:system:<label>` or `v0:unknown`. `--history`
  sends them to the organisation's audit trail
  (`POST …/projects/{project}/audit-imports`, 500 rows a request) as
  imported entries: the actor, v0.3's time, the key and locale (or
  `translation_deleted` / `no_translation` for a row whose translation
  is gone), the v0.3 row id, the restore's name and SHA-256 — and the
  **SHA-256 of the text before and after, never the text**: an audit
  entry holds no translation text (RFC 0006 §6.1), and whoever holds the
  archived dump can show which text a digest stands for. Each row is
  recorded once per organisation, keyed by its v0.3 id, so a re-run, a
  retried request, or the import of another project of the same
  organisation (whose plan carries the rows whose translation is gone
  again) records nothing twice; `history` reports `sent`, `recorded` and
  `existing`. Imported entries are appended to the end of the
  organisation's hash chain like any other: the chain is ordered by when
  an entry was recorded, `occurred_at` says when it happened. Only an
  **owner** may import history (`audit.import`); no API token scope
  reaches it, so a token is refused (`forbidden`, exit 3). Sign in as the
  owner first with `glossa login --device` (see *Authentication*): the
  device session is the owner's own and is accepted here.
  `--history` and `--dry-run` exclude each other.
- Locale labels are reported; the platform names locales from CLDR.
- `not_carried` lists every v0.3 field the import leaves behind, with
  why. The archived dump keeps all of it.

### Proving it renders the same (`--verify`)

```sh
export GLOSSA_DELIVERY_KEY=dk_…   # a delivery key for the environment
glossa import --from v0 --v0-db postgres://postgres@localhost/glossa_v0_restore --v0-tenant klarlabs \
  --v0-project brotwerk-site --verify --edge https://edge.example.com --environment production
```

`--verify` imports nothing and writes nothing. It reads v0.3's text — from
the restore (`--v0-db`) or v0.3's API (`--v0-url`) — and renders every key
in every v0.3 locale twice: with **v0.3's own formatter**
(`@felixgeelhaar/glossa-format`) over that text, and with
**`@klarlabs-studio/glossa`** over the release glossa-edge serves to the delivery
key in `--environment`, exactly as a product would load it. The two share
no code. Both get the same arguments, generated from each message's own
argument metadata (the platform's `Arguments` over the MF1 text, in every
locale): every plural with 0, 1, 2, 5, 21 and its exact keys, every select
with each of its keys and a value that reaches its catch-all, every other
argument with one value; the product of those, or each selector varied
alone past 64 sets.

- **How it runs.** The CLI is one Go binary and both formatters are
  JavaScript, so it embeds a small Node driver, writes it to a temporary
  directory and runs it with `node` (22 or later; `--node` names another).
  The two packages are not bundled — the comparison is only worth
  something against the very builds products use: it finds them under
  `node_modules/` from the working directory upwards, or in a Glossa
  checkout (`packages/format`, `runtimes/js/glossa`, built with
  `make system-m5-deps`), or where `--format-module` and
  `--runtime-module` point. A missing or unbuilt package is
  `module_not_found` / `module_not_built` (exit 2).
- **Verdicts.** Each rendering is `match`, `known_defect` or `mismatch`.
  `known_defect` is exactly one defect of v0.3's formatter
  (`v0_bare_apostrophe`): it reads any apostrophe that isn't doubled as
  the start of a quoted run lasting to the next apostrophe or the end,
  so `Geht's gut, {name}?` renders as `Gehts gut, {name}?` where ICU —
  and the runtime — say `Geht's gut, Ada?`. A rendering is put in that
  category only when the runtime rendered, v0.3 differs from it (or
  failed), and **v0.3's own formatter, given the same text with only its
  apostrophes rewritten to mean what ICU means, renders exactly the
  runtime's output** (`v0_requoted` in the row is that evidence). Any
  other difference — a changed word beside an apostrophe, a number
  formatted differently, a key the release lacks (the runtime falls
  back), an error on either side — is a `mismatch`.
- **Reported, never hidden.** Every known-defect row is listed with its
  key, locale, arguments and both outputs, and counted in `summary`;
  every mismatch too. Exit 0 when there are no mismatches (known defects
  or not), 1 (`renderings_differ`) otherwise. Run it against an
  environment that ships what was imported — a key the policy holds back
  falls back and is a mismatch.

## Release

A release is an immutable build of the project's active messages and
the translations an environment's policy ships (production and staging:
`approved`; development and preview: everything not rejected).
Environments point at releases; glossa-edge serves what they point at.
The token needs the `publish` scope to publish, promote, roll back and
manage keys; `read` is enough for the rest. Releases are named by ID or
`v<N>`.

```sh
glossa release publish --environment staging --dry-run   # what would ship, what would change; stores nothing
glossa release publish --environment staging --note "sprint 42"
glossa release promote v7 --to production      # moves the pointer, rebuilds nothing
glossa release rollback --environment production [--to v6]
glossa release diff v6 v7                      # or `diff v7`: against its parent
glossa release list | show v7 | environments
glossa release keys create web                 # keys | keys revoke web
glossa pull --release latest --environment production --out public/glossa
```

```text
✓ Published v7 to staging (0192…)
  42 messages · de 42, en 41 (1 outdated) · 2 artifacts (1 new), 12.4 KiB
  note: sprint 42

✓ production now serves v7 (0192…); was v6

v6 → v7 (0191… → 0192…)
  de  1 added, 0 changed, 0 removed
    + checkout.payment_failed
  en  unchanged
```

- The path to production: `development` and `preview` ship work in
  progress (everything not rejected); `staging` and `production` ship
  approved text only. Publish to `staging`, check it, then promote that
  release to `production`. A development or preview release can't be
  promoted to production; the refusal (`release_ineligible`) names both
  policies and what differs.
- `publish --dry-run` runs the server's build for the environment and
  stores nothing (no release, artifacts or events; `read` scope is
  enough): per-locale counts, the artifacts a publish would upload, and
  the message IDs each locale would add, change or remove against what
  the environment serves. A catalog that can't be released lists every
  problem and exits 1. `--note` and `--idempotency-key` have no effect
  with it.
- `publish` defaults to `development`, so nothing reaches production by
  accident. Every invocation sends a new `Idempotency-Key` (a CI retry of
  the whole step publishes again; retries inside the run replay). Pass
  `--idempotency-key` (e.g. the CI run ID) to make the step itself
  repeatable: a replay reports `replayed: true` and publishes nothing,
  and the same key with a different request fails with
  `idempotency_key_reused`.
- `promote` needs the environment's policy to cover the release's
  (`release_ineligible` otherwise: a preview release with drafts can't
  reach production). `rollback` without `--to` goes to the newest
  release the environment served before its current one, so running it
  twice goes two steps back.
- `environments` and `list` say which release each environment serves.
- `keys create <name>` prints the delivery key runtimes fetch releases
  with (`/v1/<key>/<environment>/manifest.json` on glossa-edge). Keys are
  publishable by design (they ship in browser bundles), scoped to the
  project and read-only. A new key reads `production` only;
  `--environments a,b` names the environments it reads and `--preview`
  adds every branch preview (RFC 0004 §4.3), which is what a key in a
  preview deployment needs. `keys scope <id|name>` changes that later
  without changing the key, so bundles that ship it keep working, and
  `keys revoke` takes the ID or the name of an active key.
- `pull --release` writes the bundle runtimes load offline
  (runtimes/SPEC.md §3.4): `manifest.json` exactly as the edge serves it
  for `--environment`, and `a/<sha256>.json` for every artifact, each
  checked against its hash, the manifest last; artifacts of an earlier
  bundle it no longer names are removed. Runtimes reject a manifest for
  another environment than theirs, so the bundle is written for one:
  `latest` is what `--environment` serves now; a release ID or `v<N>`
  defaults to the environment it was published to.

### Release approvals

An environment can require approvals (RFC 0006 §5.1): `n` distinct
people of a member, role or group, never the requester. A `publish` or
`promote` into it is **held**: the release is built and recorded, a
release request is made, and the environment keeps serving what it
served. The command says so, names the request and the next step, and
exits **5** — a CI step never reads "held" as "deployed".

```text
$ glossa release publish --environment production
! Held for approval: v8 was recorded but not deployed to production (0193…)
  release request rr_7Kq…: production keeps serving v7 until 2 people of role reviewer, none of them the requester, approve
  next: someone else runs `glossa approve rr_7Kq…`, signed in as a person (`glossa login --device`), or approves it in Studio
  nothing was deployed; `glossa release requests show rr_7Kq…` follows it
```

```sh
glossa approve                                   # what waits: pending release requests and translation approvals
glossa approve rr_7Kq… --reason "copy checked"   # grant; the last grant deploys
glossa deny rr_7Kq… --reason "de is wrong"       # deny: the request closes, nothing moves
glossa approve checkout.pay@de                   # a translation unit's pending approval
glossa release requests [--environment production] [--state pending|deployed|denied|withdrawn|refused|all]
glossa release requests show rr_7Kq…             # the requirement, the gate, force and why, every decision
glossa release requests withdraw rr_7Kq… [--reason R]
```

- **Approvals need a person.** Deciding is human-only: an API token or
  an agent is refused (`person_required`, exit 3) and the CLI says
  `approvals need a person: sign in with glossa login --device` rather
  than "forbidden". The CLI sends whatever credential it holds; signed
  in with `glossa login --device` it decides as you. Studio's approvals
  inbox is the other way.
- **Four-eyes.** The requester never approves their own request, and an
  author never approves their own text while another reviewer exists for the locale (`own_text`; alone, they may, recorded as a self-approval); a person who isn't
  of the party asked is `not_eligible`. One person's repeated grant
  counts once.
- **When the last grant lands**, the release-approval workflow deploys
  the release as that approver: `approve` reads the request again and
  says `production now serves v8`, or that the workflow is deploying it,
  or — `refused` — that the completeness requirement, run again at
  deploy time, said no. A forced publish waits for approvals too; the
  approvers see that it was forced and why.
- Right after a publish the workflow may not have asked yet
  (`approval_not_requested`): retry in a few seconds. A request that is
  no longer pending is `release_request_closed`; a newer publish or
  promote into the environment withdraws the pending one.
- `<ref>` is a release request's ID, an approval's ID (one on a release
  request decides the request), or `key@locale` for the newest pending
  approval of a translation unit. `approve` lists all pending items of
  the project; the server decides who may decide, so the list is not
  narrowed to you.
- A **rollback never needs approval** and is never delayed.

### Staged rollouts

A rollout serves a candidate release to a share of installations,
chosen by the runtimes from the signed manifest (RFC 0006 §5.2,
runtimes/SPEC.md §1.4); every other installation, and every runtime
that predates rollouts, keeps the release the environment points at.

```sh
glossa release rollout start --environment production --release v8 --percent 10 [--max-duration 14d] \
  [--force --force-reason "why"] [--idempotency-key K]
glossa release rollout status --environment production          # the active one and its etag (alias: show)
glossa release rollout advance --environment production --percent 50
glossa release rollout complete --environment production        # the pointer moves to the candidate
glossa release rollout abort --environment production           # everyone back on stable at the next refresh
glossa release rollout list --environment production [--limit N]
```

- `advance`, `complete` and `abort` act on the environment's active
  rollout, or on the one `<id>` names (`advance ro_7Kq… --percent 50`).
  `advance` goes down as well as up; the salt stays, so installations in
  the candidate at the lower share stay in it at a higher one.
- **Changes are conditional on what was read.** `advance` and `complete`
  send `If-Match` with the rollout's ETag — the one `--if-match` names
  (`status --json` prints it as `etag`), else the one read just before.
  If someone changed the rollout meanwhile, the change is refused
  (`precondition_failed`, exit 3: "the rollout changed since it was
  read") and nothing happens; read it again and decide. `abort` sends
  `If-Match` only with `--if-match`: an abort must never be slowed by a
  stale tag.
- `start` sends an `Idempotency-Key` (a new one per invocation, or
  `--idempotency-key`); a replay reports `replayed: true`.
  `--max-duration` is one hour to 90 days (`72h`, `14d`; default 14
  days), after which the server aborts the rollout so a forgotten 10 %
  doesn't become a permanent second production.
- The candidate is held to what a promote into the environment is held
  to: `release_ineligible`, `branch_release_not_promotable`, and
  `policy_not_met` unless `--force --force-reason`, which the completing
  deployment records. The environment must already serve a release
  (`rollout_no_stable`) other than the candidate
  (`rollout_candidate_served`) with the same source locale
  (`rollout_source_locale`), not be a branch environment
  (`rollout_branch_environment`), and have no other active rollout
  (`rollout_active`). An environment that requires approvals refuses
  rollouts (`rollout_needs_approval`) until a release request can carry
  one: promote there and approve the request instead.
- While a rollout is active, `publish` and `promote` into its
  environment are refused (`rollout_active`): complete or abort it
  first. A `rollback` is never refused; it aborts the rollout
  (`end: rolled_back`).
- Changing an ended rollout is `rollout_ended`; acting with none active
  is `no_active_rollout`. Every refusal names its code, exits 2 for
  input the server rejects (`invalid_percent`, `invalid_max_duration`,
  `force_reason_required`, `invalid_force_reason`) and 3 otherwise.
  Starting, advancing, completing and aborting need the `publish`
  scope; `status` and `list` need `read`.

## Git connections

A Git connection ties one of a GitHub App installation's repositories —
by GitHub's numeric id, so a rename or a transfer doesn't break it — to
a project, one of its applications, a default branch and an optional
monorepo path (RFC 0004 §6.1). It is what lets Glossa check that
repository's pull requests and comment on them, and what a GitHub
Actions run's ID token is matched against when it authenticates without
a secret (*Authentication*). One repository can feed several projects,
one per path — which is when a CI login has to name one.

Connecting a repository normally happens in **Studio**, which walks
through installing the App on the account and picking the project;
`glossa github connections` is the scripting path, for a workspace that
provisions its projects from CI. The CLI never installs the App: it can
only connect repositories an installation already sees.

- Connections belong to the workspace, not to one project, so `list`
  shows the configured project's (`--project P` another's, by slug or
  ID) and `--all-projects` widens it to every project. It pages through
  the whole list, not just the first page.
- `add --repository` takes GitHub's numeric repository id or
  `owner/name`. A name is resolved through the installations, which is
  what makes the command usable by hand; when several installations see
  a repository of that name it is refused as `repository_ambiguous`,
  naming the accounts, and `--installation <id|account>` picks one.
  `--path apps/web` limits the connection to a monorepo subdirectory
  (left out: the whole repository), `--branch` overrides the
  repository's default branch on GitHub, and `--application` takes an
  application's slug or ID — one the project doesn't have is refused
  here rather than failing quietly on the first pull request.
- `remove <connection-id>` only unlinks the repository: Glossa stops
  checking its pull requests, and the App **stays installed on GitHub**.
  Only GitHub can uninstall it, on the account's or organization's
  Applications settings page.
- A deployment with no GitHub App configured answers
  `github_not_configured` (exit 3) to every one of these commands.

## Workflows and assignments

A workflow definition is an organisation's localization process as data
(RFC 0006 §2): a `glossa.workflow/v1` document — a statechart whose
guards and actions name the platform's primitives — linted and stored
by the server in immutable versions, and bound to projects. `glossa
workflow` lets a definition live in a repository: lint it in a pull
request, push it on merge.

```yaml
- run: glossa workflow lint workflows/review.json     # exit 1 when a save would be refused
- run: glossa workflow push workflows/review.json     # created, saved as vN, or unchanged
- run: glossa workflow bind review --locales de,fr    # unchanged when already bound
  env: { GLOSSA_TOKEN: ${{ secrets.GLOSSA_WORKFLOWS_TOKEN }} }
```

- **The server is the judge.** `lint` and `push` send the document as
  it is (JSON, or YAML for a `.yaml`/`.yml` file) and print the
  server's findings — rule, severity, path, message — so the CLI can
  never disagree with what a save would do. An `error` or `warning`
  refuses a save; an `info` is a note.
- **A push never overwrites someone else's version.** An existing
  definition (found by the document's `name` in the push's scope:
  tenant-wide, or the project's own with `--project`) gets its next
  version saved under `If-Match` on the version read just before.
  `--if-version N` says which version the file was edited from — what
  `pull -o` tells you — and a save based on anything but the latest is
  refused (`precondition_failed`, exit 3) with nothing saved: pull, merge,
  push again. A document equal to the latest version (whatever its key
  order or spacing) saves nothing (`unchanged`), so a push on every
  merge is safe.
- `bind` is idempotent too: binding the same definition for the same
  selector again is `unchanged`; the selector bound to another
  definition is `workflow_binding_exists` and names it. `unbind <name>`
  removes the definition's one binding (with several, `--locales` and
  `--namespace` pick one, or `--binding <id>`).
- `instances` and `log` read what the runner did: which version an
  instance runs on, the state it is in, and every event it received —
  the guards it evaluated, the actions it ran and as whom, `ignored` when
  the world had moved on, `refused` when an action was not allowed.
- A name both the tenant and the project define is
  `workflow_ambiguous`: pass the ID.
- **Export and import** move a definition between tenants or servers:
  `export <name> [--version N] [-o file]` writes the `glossa.workflow/v1`
  document alone — no IDs, no tenant, no version number — and `import
  <file>` creates it (tenant-wide, or the project's own with
  `--project`) or saves it as the next version of the definition of its
  name, with the server's findings shown, exactly as `push` does. They
  are `pull` and `push` by the names a migration looks for; no extra
  endpoint is involved.
- **`rebase <instance-id> [--version N]`** moves a running instance to
  the definition's latest (or Nth) version, keeping its state by name
  (RFC 0006 §2.3). It is refused when that version has no such state to
  wait in (`workflow_rebase_state_missing`) or has it as final
  (`workflow_rebase_state_final`), when the version is not newer
  (`invalid_workflow_rebase`), or for a finished instance
  (`workflow_instance_finished`) — exit 2 for each, nothing changed. The
  rebase runs nothing again: what the state asked for (an assignment, an
  approval, a due date) stands, and `log` shows it as event `rebase`. It
  is sent with `If-Match` on the version read just before, so a rebase
  someone else made in between is `precondition_failed` rather than
  repeated.

**Credentials.** Reading workflows needs a token with the `read` scope.
Saving, binding and rebasing need `workflows.manage`, which an API token holds
only with the opt-in **`workflows` scope** — no other scope implies it —
and a GitHub Actions credential never holds: such a refusal is
`workflows_scope_required` and says exactly that.

**Assignments** are a batch of translation units given to a member, a
role, a group or a vendor (RFC 0006 §3.1). `glossa assignments` is my
work; `complete` is a claim, not a decision — no review state changes,
the project's workflow decides what follows. Assignment work belongs to
people, and the CLI signs in with API tokens, which are not members:

- a token's `glossa assignments` is always empty, and `show`, `accept`,
  `complete` and `decline` of a person's assignment are `not_found`
  (someone else's assignment does not exist to you) — the message says
  so;
- `create` and `--all` need `assignments.manage`, a person's permission
  (owner, admin) that no token scope grants:
  `assignments_manage_required`.

`glossa assignments report [--vendor ID] [--project P] [--since T]
[--json]` is the vendors' quality report (RFC 0006 §3.4), the server's
numbers as it computes them on read — on time and late, source words and
words delivered from translation memory, review outcomes now, the
reviewers' mean edit on the vendor's text, open findings per unit and
rework — by assignee and locale. `--vendor` is a vendor's ID, `--since`
an RFC 3339 time or a duration back from now (`720h`), and with no
filter it covers every project the credential may read. It takes
`assignments.read` (not `assignments.manage`), and a vendor's member,
who sees only their own work, is refused. A bound on the work read (`truncated`) is said in the output.

Until the CLI can sign in as a person, the other commands do their work for
a credential that is one; Studio's My work is where people act on
assignments today.

## Knowledge and AI

The termbase, translation memory and style guides (RFC 0003 §2) and AI
translation (§3). The token needs `read` to search, list and check, and
`write` to change the termbase and style guides, retire units, run
`translate` and decide suggestions. Approving translations stays with
people (Studio), and so do provider keys, consent and budgets.

```sh
glossa terms add Warenkorb --preferred en=cart --forbidden en=basket --definition "Where items wait"
glossa terms forbid trolley --locale en --concept <id>
glossa terms check --locale en            # CI: exit 1 on a forbidden term
glossa check --terminology                # structural QA plus the termbase
glossa tm search "Bezahle {amount, number}" --to en
glossa style edit --file style/de.yaml --locale de && glossa style show --locale de
glossa translate --locale ja --dry-run    # what would be queued, and why it would do little
glossa translate --locale ja --wait       # queue, then poll until the jobs finish
glossa review list --locale ja && glossa review accept checkout.pay --locale ja
```

```text
✓ 12 translations checked against the termbase (brotwerk on https://glossa.example.com)
✗ en 1 error, 0 warnings
  error cart.items  term_forbidden: "loaf" is forbidden (use "bread")
✓ ja no findings

Terminology check failed: 1 error, 0 warnings.
```

- `tm search` parses the text in glossa.yaml's `syntax` (`--syntax`)
  and matches it against the project's and the tenant-wide units
  (`--all-projects`: every project's). Targets come back renamed to the
  query's variables, in MF2 (`target`) and in the query's syntax
  (`target_text`, shown in the table; MF2 when MF1 can't express the
  target). `tm units` lists the project's units;
  `--retire <id>` takes one out of matching.
- `terms add <term>` makes it preferred in `--locale` (default: the
  source locale); `--preferred|--admitted|--deprecated|--forbidden
  L=TEXT` add more. Concepts belong to the project unless
  `--tenant-wide`. `show`, `deprecate` and `forbid` find a concept by a
  term's text (`--locale` narrows it) or take its ID; `--concept` names
  it, and lets `forbid` add a term the concept lacks. Changes replace the
  concept conditionally (`If-Match`), so concurrent edits fail instead of
  overwriting each other.
- `terms check` has the server check every translation but rejected
  ones (`--states` narrows it) in every target locale (`--locale`,
  repeatable) — `GET …/terminology-findings`, a page of 100 translations
  a request, 20 locales at a time — and fails on errors (`--fail-on
  warning`, or glossa.yaml's `check.fail_on`). `check --terminology`
  adds the same findings to structural QA.
- `style edit --file` takes YAML with `name`, `fields` and `rules` (the
  API's field names; an unknown field is an error) and creates or
  replaces the guide of exactly the scope given: the project (default) or
  the tenant (`--tenant-wide`), plus `--locale` and `--namespace`. `style
  show` merges every applicable guide, the narrowest winning, and names
  the versions it used.
- `translate` queues one job per message missing in each locale
  (`--outdated`: outdated ones instead; both flags: both) in one fill
  that selects them by state on the server (`select`), never for
  sensitive namespaces. `--dry-run` queues nothing: the server's fill
  preview (`ai-fill-previews`) lists the keys per locale and how each
  would run — `existing` jobs reused, `tm_exact` (an exact
  translation-memory match, no provider call), `provider`, or `refused`
  by reason (`provider_consent`, `no_route`, `budget_exceeded`;
  sensitive messages count as `skipped.sensitive`) — with the estimated
  and upper-bound cost of the provider calls, and the refusals the fill
  would warn about (`provider_consent_off`, `no_budget`,
  `budget_exhausted`, `no_provider`); it exits 1 when there is one. A
  push is visible to it (and to `translate`) as soon as `push` returns.
  `--wait` prints progress
  to stderr and exits 4 when a job failed, each with its `failure_code`
  (`provider_consent`, `invalid_output`, `budget_exceeded`, `no_route`,
  …). Each invocation sends a new `Idempotency-Key`.
- `review accept` writes the suggestion as the translation, in the state
  the project's review policy gives an API token (tokens never approve).
  `--text` accepts your edit instead, in glossa.yaml's syntax
  (`--syntax`). A key names that message's pending suggestion (with
  `--locale` when it has several).

## Attaching to a browser

`glossa capture` starts headless Chrome itself. Where it can't — a CI
sandbox whose kernel denies Chrome's own sandbox, a container or a
remote browser — start the browser yourself and point capture at its
DevTools endpoint:

```bash
google-chrome --headless=new --no-sandbox --disable-dev-shm-usage \
  --remote-debugging-port=9222 --user-data-dir="$(mktemp -d)" about:blank &

glossa capture --cdp http://127.0.0.1:9222     # or: export GLOSSA_CAPTURE_CDP=…
```

The endpoint is `ws://…`/`wss://…` (a browser WebSocket, as
`/json/version` reports it) or `http://…`/`https://…`, which capture
asks for that WebSocket. It keeps the endpoint's own host and port and
takes only the path, so a browser behind a tunnel or a published
container port works.

**The host must be a loopback address** (`127.0.0.1`, `::1`,
`localhost`). A DevTools endpoint is full control of that browser — its
pages, their cookies and every address it can reach — and this value
arrives from a flag or a CI variable, so capture refuses anything else
(`invalid_cdp_endpoint`) before it connects. `--cdp-allow-remote` (or
`GLOSSA_CAPTURE_CDP_ALLOW_REMOTE=1`) lifts that for a browser on another
host; then the endpoint is yours to trust.

Attached, capture owns only the tabs it opens: each is closed after its
capture, and the browser keeps running afterwards.

## Audit

An audit export (RFC 0006 §6.2) is a directory holding `entries.jsonl`
— one hash-chained, content-free entry per line — and `manifest.json`,
which signs the range, the chain's two ends, the entry count and the
file's SHA-256 with the deployment's audit key. The format is written
out in `platform/README.md`, *Audit export format*. Export jobs write
them (RFC 0006 wave 5); `glossa audit list`, `export` and `csv` read
and convert them, and `verify` checks them.

### list

```sh
glossa audit list --limit 20
glossa audit list --action release.published --from 2026-10-01 --to 2026-10-08 --json
glossa audit list --actor system:audit.exporter --limit 0 --csv > exports.csv
```

Reads `GET …/audit-entries`, newest first, following the cursor until
`--limit` entries (default 100; `0` is all of them — a long trail is
many requests). `--from`/`--to` take RFC 3339 or a date (UTC) and mean
`[from, to)`; `--actor`, `--action`, `--source` (`outbox`, `direct`,
`import`) are exact; `--project` takes a slug or ID. It needs
`audit.read` (owner and admin, no API token scope). A credential limited
to some projects sees only those projects' entries, never the tenant's
own (sign-ins, members, tokens, exports). `--csv` prints the columns
below instead of a table; `--json` prints `glossa.cli.audit.list/v1`
(`more` says the trail goes past `--limit`). Entries are content-free:
identifiers, selectors and the shape of everything else
(`"string(len=12)"`), never text.

### export

```sh
glossa audit export --from 2026-10-01 --to 2026-10-08 --out ./audit-2026-10 --public-key audit-keys.json
glossa audit export --first-sequence 3311 --out ./audit-2026-10          # to the chain's head
glossa audit export --first-sequence 1 --no-wait                        # queue it, download later
glossa audit export --job 0190… --out ./audit-2026-10 --public-key audit-keys.json
```

Creates the job (`POST …/audit-export-jobs` with an `Idempotency-Key`),
polls it (`--timeout`, `--poll-interval`), downloads both files into
`--out`, and refuses to overwrite files already there without `--force`.
A download whose SHA-256 differs from the server's (`ETag` and the job)
writes nothing (`download_corrupted`). Only the owner exports; the range
is either a time range (at most 31 days) or a sequence range (at most
1,000,000 entries) and always the tenant's whole trail, never one
project's.

With `--public-key` (same values as `verify`) the downloaded export is
verified at once, offline, by the same code as `audit verify`; the exit
is 1 when it does not verify, and `--json` has the verification in
`verification`. Without it the command says so and prints how to pin the
deployment's keys (`/.well-known/glossa-audit-keys.json`) and verify.
The digests the server reports prove the download, not the export.

Messages worth knowing: `audit_export_unavailable` (the deployment has
no audit signing key or has exports off; exit 3; an operator sets
`GLOSSA_AUDIT_SIGNING_KEY` and `GLOSSA_AUDIT_EXPORTS_ENABLED`),
`range_not_contiguous` (exit 2: imported v0.3 history sits inside the
time range; export by `--first-sequence`/`--last-sequence`), 403 (needs
`audit.export`, owner, by a credential limited to no project),
`range_too_long`, `sequence_out_of_range` (exit 2), `file_expired` (the
files are kept 7 days by default; make a new export).

### csv

```sh
glossa audit csv ./audit-2026-10 --out audit-2026-10.csv
glossa audit csv ./audit-2026-10/entries.jsonl > audit.csv
```

Converts locally, with no network and no verification (run `verify`
for that). One header row, then one row per line, columns in this
order — new columns are only ever added at the end:

`sequence, occurred_at, source, action, actor, aggregate_type, aggregate_id, project_id, locale, event_id, request_id, trace_id, summary, prev_hash, hash`

`occurred_at` is UTC with six fractional digits, as in the export; a
member that is absent or `null` is an empty cell; `summary` is the
object as compact JSON with sorted keys. `audit list --csv` writes the
same columns. Entries hold no text, so neither do rows. With `--out` and
`--json`, `glossa.cli.audit.csv/v1` (`{source, out, rows, columns}`).

`glossa audit verify` needs nothing but the files and a public key you
trust. It reads no glossa.yaml, no token and no network, so an auditor
runs it on a copy without an account:

```sh
# once: save the deployment's audit keys and keep them (pin them)
curl -fsS https://glossa.example.com/.well-known/glossa-audit-keys.json > audit-keys.json

glossa audit verify ./audit-2026-10 --public-key audit-keys.json
glossa audit verify ./audit-2026-10/manifest.json --public-key audit-2026=Base64PublicKey…
```

```text
✓ audit export verified: 1204 entries, sequences 3311–4514 of tenant 0190a1b2-…
  signed with audit-2026 at 2026-10-02T12:00:00.000000Z
  last hash 7ca45202…

✗ audit export does not verify: line 3 (sequence 3313): hash does not match the entry's content [hash_mismatch]
  2 entries verified before it
```

- `--public-key` is required and repeatable: a `glossa.audit.keys/1`
  document (`{format, keys: [{key_id, algorithm, public_key, active}]}`)
  or `keyId=base64` for one key. The key is never taken from the export:
  a signature checked against a key that travels with the file proves
  nothing. Retired keys stay in the document, so old exports keep
  verifying after a rotation.
- It stops at the first thing that fails and names it: `manifest_invalid`,
  `unknown_key`, `signature_invalid`, `entries_unreadable`,
  `line_invalid` (a truncated last line among them), `line_not_canonical`,
  `tenant_mismatch`, `sequence_gap` (a removed or reordered line),
  `prev_hash_mismatch`, `hash_mismatch` (an edited line),
  `range_mismatch` (lines missing at the end, extra lines, an entry
  outside the export's time range) or `digest_mismatch`.
- Consecutive exports join: one's `range.last_hash` is the next one's
  `range.first_prev_hash`.
- What it proves: the export is what the deployment's audit key signed,
  and its entries form an unbroken chain. What it cannot: that the
  database was not rewritten by its superuser before the export was
  made (RFC 0006 §9.5).

## Known limits

- `check`, `diff`, `pull`, `push --translations` and `import` read
  translations with the project-wide listing (`GET …/translations`,
  active messages, 100 a page, 20 locales a request) and join them to
  the catalog by message ID; `status` reads `GET …/translation-stats`.
  `check` needs every translation's content for its QA, so the stats
  (counts only) don't replace the listing there. The listing reads
  Localization's view of the catalog: current when a push (`message-upserts`)
  returns; after other message writes (Studio edits, renames) once their
  events are processed, usually within a second.
- `extract` finds literal keys, not computed ones, and doesn't read
  `.gitignore` (it skips hidden directories, `node_modules`, `dist`,
  `vendor`, `build` and `coverage`). Its web scan is lexical, the fallback
  when `@klarlabs-studio/glossa/unplugin` doesn't run: it doesn't skip comments or
  strings, and JSX files get no component. A Go file or template that
  doesn't parse is reported and skipped. Go accessor calls on an imported
  package (`strings.Title`) don't count; an unaliased import whose package
  name differs from its path is guessed from the path.
- `extract --upload` and `context push` post the document to
  `POST /v1/tenants/{tenant}/projects/{project}/context-builds?source=…`;
  the upload is idempotent by application, commit, source and the
  digest of the document's canonical form, and the server rate-limits
  uploads per tenant (10 a minute, bursts of 60; 429 is retried).
  `context push` reads only the document's header before sending it; the
  server validates the rest by the schema.
- `capture` needs Chrome or Chromium on the machine (scout looks for
  `google-chrome`, `chromium` and the usual app paths; GitHub's Ubuntu
  runners have it), or a browser to attach to with `--cdp`. scout's
  launcher passes a fixed flag list, so a sandbox that needs
  `--no-sandbox` or `--disable-dev-shm-usage` is the attach path's job
  (platform/README.md, "scout follow-ups"). It lays pages out with
  overlay scrollbars, so an
  image is as wide as its viewport; a page taller than 16 384 device
  pixels or 40 megapixels is cut there (`truncated`), and its regions
  below the cut are `visible: false`. A PNG over 10 MB fails the capture.
  Pages are captured one at a time, each on a fresh tab. A playbook's own
  starting URL is ignored: it starts on the route's page. Upload:
  `POST …/captures`, multipart, the `manifest` part first, then one
  `image/png` part per distinct image named by its SHA-256.
- Windows stores tokens in the file store.
- `translate --dry-run`'s `tm_exact` counts exact translation-memory
  matches; the job still validates one before reusing it (structure,
  plural categories, terminology), so a match that fails there goes to a
  provider after all. Its cost is an estimate from the price table, not
  a quote.
- `import --format` reads a file from disk (no `-`/stdin): the upload
  sends its size up front.

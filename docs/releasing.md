# Releasing

Two release trains, both driven from `main`:

- **npm packages** (`.github/workflows/release-packages.yml`): changesets open a
  "Version Packages" PR; merging it publishes every package whose version is not
  yet on the registry. See the header of that workflow.
- **Platform images and chart**: a `v*.*.*` tag, built by Kiln. See the chart
  README's "Cutting a release" (`deploy/charts/glossa-platform/README.md`).

## First publish of a new package

CI publishes with npm trusted publishing (OIDC), but a trusted publisher can
only be configured once the package exists on npm (or on its "Publishing access"
page before then). The very first version of a new package name is therefore
published by hand, from the package directory:

```sh
cd runtimes/js/glossa    # the package's own directory
pnpm publish --access public --no-git-checks --provenance=false --otp=<code>
```

Why each part:

- `--provenance=false`: the packages carry `publishConfig.provenance: true`,
  which only works from CI (a local publish fails with `Automatic provenance
  generation not supported for provider: null`). `NPM_CONFIG_PROVENANCE=false`
  does **not** override `publishConfig`; only the command-line flag does.
- `--otp=<code>`: an npm web-login session from a non-interactive shell (an
  agent, a `!` command) expires quickly, and the browser 2FA approval for
  publishing cannot complete there. A fresh one-time code from your
  authenticator is the reliable path.
- `--no-git-checks`: the publish runs from a feature branch or a dirty tree.
- `--access public`: scoped packages default to restricted.

A **404 on publish** to a scope means you are not authenticated (run `npm login`
and check `npm whoami`), not that the org is missing.

Build first (`pnpm --filter <name> build`); the tarball ships `dist/`.

### Then set up the trusted publisher

On npmjs.com, open the package, then Settings, then Trusted Publisher, and add
a GitHub Actions publisher:

| Field | Value |
|---|---|
| Owner | `klarlabs-studio` |
| Repository | `glossa` |
| Workflow | `release-packages.yml` |
| Environment | (empty) |

From the next version on, `release-packages.yml` publishes with provenance and
no token. A version already on npm is skipped, so the hand-published version
does not collide.

# Changesets

Per-PR changeset files live here. Each describes which packages a change affects and what severity the bump should be (patch / minor / major).

## Adding a changeset

```bash
pnpm changeset
```

Pick the affected packages, the bump level, and write a one-line summary. Commit the resulting `.changeset/<random-name>.md` file as part of your PR.

## Release flow

1. PRs land on `main` with `.changeset/*.md` files describing their impact.
2. The `release-packages.yml` workflow opens (or updates) a "Version Packages" PR that consumes every pending changeset, bumps each affected package's `version`, and rewrites its `CHANGELOG.md`.
3. Merging that PR triggers `pnpm changeset publish` — every bumped package is published to npm with provenance.
4. No changeset = no release. Docs-only PRs don't need one.

## Skipping a package

Private rewrite packages are in the `ignore` list: they are never published to npm. Changesets that affect only them are no-ops.

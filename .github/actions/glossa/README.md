# `glossa` composite action

Runs Glossa's CI job for a repository: push the branch's source
messages, upload where they appear, and optionally screenshots and the
preview URL (RFC 0004 §6.3).

It authenticates with **GitHub Actions OIDC**, so the repository keeps
no Glossa secret. The job asks GitHub for an ID token with audience
`glossa`; Glossa verifies it and matches its `repository_id` — GitHub's
immutable number, never the repository's name — against a Git
connection. The credential it hands back lives **30 minutes**, is bound
to that **one project**, and may only read and write the project's
catalog: it cannot import translations, publish a release, ask an AI
provider for anything, or read the workspace's members or tokens.

```yaml
name: Glossa
on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read
  id-token: write        # this is what lets the job ask for an ID token

jobs:
  glossa:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with: { go-version: '1.26' }
      - run: go install github.com/klarlabs-studio/glossa/platform/cmd/glossa@latest
      - uses: ./.github/actions/glossa
```

The action does not install the CLI: put `glossa` on `PATH` first, so
the workflow pins the version rather than the action choosing one.

## Inputs

| Input | What it is |
|---|---|
| `server` | The `glossa-server` URL. Default: `glossa.yaml`'s `server`. |
| `project` | The project's ID. Only needed when the repository feeds several projects (a monorepo with one Git connection per path); without it the exchange answers `ambiguous_project` and lists them. |
| `application` | Which application the usages and captures belong to. Default: `glossa.yaml`'s `extract.application`. |
| `token` | A Glossa API token, for a repository that is not connected or a server with no GitHub App. Leave empty to use OIDC. |
| `usages` | A `glossa.usages/v1` document to upload (`@klarlabs-studio/glossa-unplugin` writes `.glossa/usages.json`). Empty: `glossa extract --upload` collects them instead. |
| `capture` | `true` uploads screenshots with `glossa capture --upload`; needs a running preview (see `capture.base_url`). |
| `preview-url` | Where CI deployed this branch's preview. Registered on the branch, so Studio and the pull request's comment can link to it. |
| `working-directory` | The directory holding `glossa.yaml`. Default: the repository root. |

## What it runs

```bash
glossa login                                              # OIDC; no secret
glossa push --branch "$GITHUB_HEAD_REF" --pr "$PR_NUMBER" # default branch: glossa push
glossa extract --upload                                   # or: glossa context push <usages>
glossa capture --upload                                   # only with capture: true
glossa preview register --url "$PREVIEW_URL"              # only with preview-url
```

On a pull request the push goes to the branch: new keys become proposals
the branch owns and nothing live changes (RFC 0004 §4.1). On the default
branch it activates them.

## Forks

A pull request from a fork gets **no ID token and no secrets** from
GitHub, so there is nothing for the job to authenticate with. Glossa's
check on that pull request completes as `neutral` with an explanation
rather than failing — the fork's changes are reviewed and, once merged,
the default branch's own run pushes them.

This is a property of GitHub, not a check Glossa performs: a token
minted from a fork's own repository would be minted for that
repository's Git connections, which are not the upstream's.

## Other CI

Nothing here needs GitHub Actions except the OIDC login. On any other
runner, keep an API token in the job's secrets and run the same
commands:

```bash
export GLOSSA_TOKEN="$GLOSSA_API_TOKEN"     # a token created in Studio
glossa push --branch "$CI_BRANCH" --pr "$CI_PR_NUMBER"
glossa extract --upload
glossa context push .glossa/usages.json
glossa capture --upload
glossa preview register --url "$PREVIEW_URL"
```

`GLOSSA_TOKEN` always wins over anything `glossa login` stored, so a job
that sets it never reaches the OIDC path.

# MCP

Glossa speaks [Model Context Protocol](https://modelcontextprotocol.io)
so an agent is a first-class user of the platform, not a scraper of the
admin UI (product intent §6.5 and §47, RFC 0005 §7).

There is one implementation and two ways to reach it:

- **`glossa-server` serves MCP at `/mcp`** over streamable HTTP, in the
  same process, behind the same middleware, tenancy and row-level
  security as the REST API.
- **`glossa mcp`** is a **stdio proxy** to that endpoint, for editors
  that speak only stdio. It moves JSON-RPC frames and nothing else: it
  registers no tool, validates no argument and knows no tool name, so it
  cannot drift from the endpoint it dials.

Every tool is a thin call into a bounded context's application port.
There is no second implementation of any rule, and no capability here
that the API does not already have.

## Connecting an editor

`glossa login` once, then point the editor at the proxy. It needs no
port, no second process to supervise and no token in the editor's
config — the credential is the one the CLI already stored (or
`GLOSSA_TOKEN`).

Claude Code:

```sh
claude mcp add glossa -- glossa mcp
```

Anything that reads an `mcpServers` object (Claude Desktop, Cursor,
Zed, VS Code extensions):

```json
{
  "mcpServers": {
    "glossa": {
      "command": "glossa",
      "args": ["mcp"]
    }
  }
}
```

The proxy reads `glossa.yaml` for the server, so run the editor from the
project — or pass `--server https://glossa.example.com` and
`--config /path/to/glossa.yaml`.

A client that speaks streamable HTTP can skip the proxy and connect
straight to the endpoint with the token as a bearer:

```
https://glossa.example.com/mcp
https://glossa.example.com/mcp?toolset=write
https://glossa.example.com/mcp?toolset=publish
```

**Call `whoami` first.** It answers with the tenant the session is bound
to, the token it acts as, the scopes and permissions that token carries,
the toolset the session opened and the tools it may call — plus
`write_available` and `publish_available`, so an agent learns what
reconnecting would buy it instead of discovering a permission it never
had, a hundred refusals later.

## Credentials

A session presents an existing **tenant API token** (`glossa_api_…`) as
its bearer. MCP mints no credential of its own and re-implements no
authorization: `identity/app` already enforces
`Scope` → `GrantForScopes` → permissions, and MCP is a second façade on
the same rules.

- **The token's tenant is the session's tenant.** No tool takes a tenant
  argument, so there is nothing to confuse and nothing to escalate. A
  project of another tenant is not *forbidden*, it is *not found* — the
  same answer a typo gets, because a forbidden-with-detail would confirm
  that the id exists somewhere.
- **CI tokens (`glossa_ci_…`) and in-context grants (`glossa_ctx_…`) are
  refused at connect.** Each is minted for one job or one origin, and
  lending either to a long-lived agent session would widen it.
- Every HTTP request is re-authenticated, so revoking a token ends its
  agent's session at the next call. A session opened by one token cannot
  be driven by another.
- Per-tenant rate limits and the AI budget apply here exactly as to
  REST. A runaway agent hits the same wall a runaway script does.

## Toolsets, and the two locks

A session opens **one** toolset. Read is the default and needs no flag;
anything that changes something needs **two locks**, and the flag is
only the second one:

| Toolset | Asked for with | Needs the scope | Unlocks |
|---|---|---|---|
| `read` | nothing | any token | the ten read tools |
| `write` | `--allow-write`, `?toolset=write` | `write` | the read tools **+** the four write tools |
| `publish` | `--allow-publish`, `?toolset=publish` | `publish` | the read tools **+** the three release tools |

> **Lock 1 — the token's scope.** Checked when the session opens. A
> token without `publish` cannot open a publish session at all, and the
> refusal is a 403 naming the scope it lacks.
>
> **Lock 2 — the session's toolset.** Checked on every call. A token
> that *does* carry `publish`, in a session that opened `read`, still
> cannot publish. A token is long-lived and an agent is not a person;
> one accidental tool call should not be able to rewrite a catalog or
> move production onto a different release.

**`write` and `publish` are not a ladder.** They are orthogonal token
scopes — a `publish` token carries `releases.publish` and nothing else
beyond read — so they are orthogonal toolsets too. A write session
cannot move a release; a publish session cannot touch the catalog. An
agent that needs both runs two `glossa mcp` processes, and
`--allow-write --allow-publish` together is a usage error rather than a
silent choice.

**`admin` is not exposed at all.** No member, token, connection or
tenant management, and **no delete tool of any kind**. Obsoleting a
message is a state change and is available; destroying data is not.

## The tools

Every result carries a one-sentence human explanation beside the
structured payload.

### `read` — the ten read tools

| Tool | Answers |
|---|---|
| `catalog_search` | messages by key prefix, namespace, state, text, `unused`, `not_captured` |
| `message_get` | source, arguments, description, `max_length`, neighbours, usages and capture count |
| `translation_get` | a translation with its provenance, review state and outdated flag |
| `usages_get` | `file:line (component, route)` for a message |
| `tm_search` | exact and fuzzy translation-memory matches with scores and provenance |
| `term_lookup` | concepts and terms recognized in a text, with status |
| `style_rules` | the effective style guide for a locale and namespace |
| `check_run` | runs the deterministic quality layers and returns findings and the policy verdict |
| `findings_list` | stored findings by layer, locale, severity, waived |
| `explain_delivery` | what a release and environment currently serve, and how a message resolves |

`check_run` is a *read* tool: it computes a verdict and stores nothing,
so a read-only session runs it. That is the tool to call before asking
for a write session.

### `write` — the four write tools

| Tool | Does |
|---|---|
| `message_upsert` | creates or revises source text |
| `translation_propose` | writes a translation revision, **always routed to review** |
| `locale_add` | adds a target locale |
| `translate` | starts a server-side AI fill job and returns its id |

**An agent's translation is never auto-approved**, and this needs no new
rule: `translations.review` is in no scope's permission set, because
review is a human decision. The tool takes no review state, the port
carries none, and the adapter asks for `needs_review` on every write —
so there is no argument, no project setting and no routing policy that
can land an agent's text as an approved revision.

`translate` runs under the tenant's own provider configuration and
budget. **No AI provider key travels in either direction**: the client
never sees one, cannot set one, and `ai-providers` has no tool. The
`sensitive` namespace rule holds — those namespaces are never machine
translated.

### `publish` — the three release tools

| Tool | Does |
|---|---|
| `release_publish` | builds an immutable release under an environment's policy and points that environment at it |
| `release_promote` | points an environment at a release that already exists (staging's into production). Nothing is rebuilt |
| `release_rollback` | points an environment back at a release it served before — the one named, or the previous one |

All three are thin calls into the Release context, which owns every
rule: which review states an environment's policy admits, whether a
branch release may be promoted, the completeness gate, the immutability
of a published release and the deployment history a rollback walks.

**There is no way to force a publish past its environment's policy.** A
project that does not meet the requirement is refused with
`policy_not_met`; overriding that takes a person and a recorded reason,
for the same reason an agent's translation enters review. Not an
argument that is refused — an argument that does not exist, in the
schema and in the port.

**Nothing here destroys anything.** A release is immutable and a
rollback only moves a pointer: the release an environment was serving
stays exactly where it is, and rolling forward again is a promote.

Pass `idempotency_key` to `release_publish` and repeat it on a retry: a
timed-out call then returns the release the first attempt made instead
of publishing a second one. Agents retry more readily than people do.

## The audit ledger

Every tool call is written to `mcp_tool_calls`, whatever the outcome — a
refusal is exactly the thing an operator wants to see. The row carries
the session, the actor and token id (`token:<uuid>`), the toolset, the
tool, the outcome (`ok`, `denied`, `invalid`, `error`), the ids the call
affected, and the arguments' **shape**:

```json
{"project": "0192…", "key": "string(len=27)", "locale": "de", "note": "string(len=31)"}
```

Each tool names the few arguments that are selectors — an id, a locale,
a state, a namespace, an environment, an idempotency key — and those are
recorded verbatim, because a row nobody can read is not an audit.
Everything else becomes its JSON type and, for a string, its rune
length. **Message text and translation text never reach a log or a
ledger.**

The table is append-only for the application role: `glossa_app` may
`INSERT` and `SELECT` and nothing else, so no code path — and no agent —
can rewrite or erase its own trail.

Metrics: `glossa_mcp_tool_calls_total{tool,scope,outcome}`,
`glossa_mcp_sessions_total{transport}` and
`glossa_mcp_sessions_open{transport}`. One trace per tool call, joined
to the REST operation it wraps.

## Layout

```
domain/     Toolset and its two locks, the audited argument Shape, the errors,
            and the endpoint's wire vocabulary (/mcp, ?toolset=) that both
            the server and the stdio proxy read from one place
app/        Session, the tool Registry and the Service that authorizes,
            audits, measures and traces one call. Transport-free: nothing
            here imports an MCP SDK
tools/      The tools themselves, declared as data. Read() Write() Publish()
            return one toolset each; ports.go is the narrow interface each
            one calls into
adapters/
  mcpgo/    Streamable HTTP on the official modelcontextprotocol/go-sdk
  identity/ The Authenticator: which credentials MCP accepts
  sources/  One adapter per bounded context, wired to the ports above
  postgres/ The audit ledger
  metrics/  The Prometheus series
```

The SDK is confined to `adapters/mcpgo`. The application service speaks
Go functions over `app.Session` and `json.RawMessage`, so replacing the
library is a rewrite of that one file.

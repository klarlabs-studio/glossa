# Usage fixtures

**One behaviour, two implementations.** Two tools find where a message is used:
`@klarlabs-studio/glossa/unplugin`, the bundler plugin for web builds, and `glossa extract`, which handles Go and
anything built without a bundler ([RFC 0004 §2.1](../../../docs/rfcs/0004-context.md)). They have to
agree, so both run this suite. **A difference between them becomes a fixture here first**, and then
both are fixed. Neither implementation is the reference.

Both write the same document, `glossa.usages/v1` ([schema](../schemas/usages.v1.schema.json),
[example](../schemas/examples/usages.v1.json)).

## Layout

```
<case>/
  case.json      what the case covers, and the runner's inputs
  project/       the source tree; it is the project root, so `file` paths are relative to it
  expected.json  the glossa.usages/v1 document the extractor must produce
```

| `case.json` field | Meaning |
|---|---|
| `description` | What the case pins down |
| `implementations` | Which of `unplugin` and `extract` must pass it. A case lists an implementation only if that implementation handles every file in `project/` |
| `keys` | The catalog's message keys. They drive the typed accessor names, which follow `glossa generate` (`platform/internal/cli/codegen`). A key used in code doesn't have to be listed: unknown keys are still usages |
| `routes` | Optional. The plugin's `routes` option, mapping route patterns to file globs (`**` crosses directories, `*` doesn't) |

## Running a case

A runner for an implementation does this for every case that lists it:

1. Point the implementation at `<case>/project` as the project root. For the plugin, every file
   under `project/` is a module of the build, so a project has to build as it is with its
   framework's own tooling: an Astro dynamic route exports `getStaticPaths`. Imports of files the
   project doesn't contain (packages, generated registrations) stay external.
2. Give it `keys` as the catalog and `routes` as the routes option, plus these build inputs:
   application `fixture`, commit `0123456789abcdef0123456789abcdef01234567`, branch `main`.
3. Parse its output and compare it to `expected.json` as JSON values, ignoring `tool`. The
   comparison includes the order of `usages`.

A case with no usages expected still needs its `expected.json` (with `"usages": []`).

## The contract

### What is a usage

| Kind | Call shape | Where |
|---|---|---|
| `t` | `t("…")`, `$t("…")`, or any `.t("…")` / `.$t("…")` member call. In Go, a `.T(…)` method call: `Localizer.T("…")`, `Client.T(ctx, "…")`, `client.For(…).T("…")` | JS, TS, JSX, Vue, Astro, Go |
| `component` | `<GlossaText id="…">` (Vue) and `<T id="…">` (React) | Vue, JSX |
| `element` | `<glossa-text>`, `<glossa-rich>`, `<glossa-plural>`, `<glossa-select>` with `key="…"` or `message="…"`. `key` wins when both are set | HTML, Vue, Astro |
| `accessor` | Typed accessors from `glossa generate`: `messages.checkout.pay(…)`, `m.checkout.paymentFailed(…)`, `msg.For(l).CheckoutPay(…)` | TS, Vue, Go |
| `template` | `{{t "…"}}`, `{{td "…" "default"}}`, `{{th "…"}}` | Go templates (`*.tmpl`, `*.gotmpl`, `*.gohtml`) |

- **Only literal keys count.** The key has to be a string literal right at the call site: quoted, a
  Go raw string, a JS template literal without `${}`, a bound Vue literal (`:id="'…'"`), or a
  JSX literal in braces (`id={"…"}`). Variables, constants, concatenation, conditionals,
  `fmt.Sprintf` and `${}` are dynamic, and dynamic keys are never reported. That's why unused
  messages are only ever reported, never deleted automatically (RFC 0004 §2.2).
- **The literal has to be a valid message key** (`^[a-z0-9_-]+(\.[a-z0-9_-]+)*$`, at most 200
  characters). `t("Hello world")` isn't a usage.
- In Go, the key of a `.T` call is its first argument if that's a string literal, otherwise its
  second (`Client.T(ctx, "…")`). A plain function named `T` doesn't count. Neither does a call on an
  imported package (`strings.Title(…)`), even when its name matches an accessor.
- In Go templates, the key is the first argument of `t`, `td` or `th`, in any action, branch, block
  or parenthesized pipeline. A string literal piped into a bare `t` or `th` (`{{"…" | t}}`) is its
  first argument too. Piped into `td`, it's the default text, never a key.
- **Typed accessors** are calls `<receiver>.<path>(…)` whose path is a key's accessor path (TS:
  camelCased segments; Go: the PascalCase method name). Collisions follow codegen: of two keys with
  the same path, only the first in sort order gets it, and a key that is also a group (`nav.home`
  next to `nav.home.title`) gets none. A one-segment TS path (`title`) only counts on a receiver
  named `messages`.
- **Never usages:** anything in a comment (JS, HTML, Vue template, Go, Go template), in a string, or
  in JSX or HTML text; escaped markup; other components and elements (`<Other id>`,
  `<glossa-texts>`); the DOM `id` of a custom element; other functions (`at`, `tt`, `Explain`).
  Look-alike keys in the fixtures start with `fake.`, and the checker fails if one is expected.
- **Generated files aren't scanned.** That's any file whose first line matches
  `// Code generated … DO NOT EDIT.`, like the accessors `glossa generate` writes. Otherwise every
  message would look used.

### Position

- `line` is 1-based. `column` is 1-based and **counted in Unicode code points**, not bytes (Go's
  `token.Position`) or UTF-16 units (source maps). A tab is one column.
- Both point at the **first character of the key inside its quotes**. For an accessor, they point
  at the first segment of its path (`checkout` in `m.checkout.pay(`, `CheckoutPay` in Go). When a
  call spans lines, the line is where the key is.
- `file` is relative to the project root, uses `/`, and keeps non-ASCII names as stored (NFC).

### Component and route

- **Vue and Astro:** `component` is the file name without its extension (`PaymentFooter`,
  `payment`, `[slug]`).
- **JSX/TSX:** `component` is the nearest enclosing capitalized function or class: a declaration, a
  named function expression (`memo(function Card() …)`), or an arrow assigned to a `const`
  (`const Header = () => …`). Lowercase helpers and anonymous functions are skipped. When nothing
  qualifies, `component` is absent.
- **Go:** `component` is `pkg.Func`, `pkg.Type.Method` or `pkg.(*Type).Method` of the enclosing
  declaration, where `pkg` is the package name. Function literals belong to the declaration around
  them. Package-level initializers have no component.
- **HTML, plain TS/JS and Go templates:** no component.
- **Route:** for `src/pages/**/*.astro`, the path without the extension, with `index` segments
  dropped and `[param]` kept (`/`, `/checkout/payment`, `/blog/[slug]`). Otherwise, the first
  `routes` entry with a matching glob. With neither, `route` is absent.

### Order

`usages` are sorted by `key`, then `file`, then `line`, then `column`. Strings are compared by
Unicode code point, which is UTF-8 byte order: `src/Negatives.vue` sorts before
`src/negatives.html`. JavaScript's default `<` compares UTF-16 units, so the plugin needs a
code-point comparison to get this right. `(file, line, column)` is unique.

### Not specified yet

These shapes have no fixture, so there's no contract for them yet. Whoever needs one first adds the
case:

- anonymous arrows wrapped in a call (`const Card = memo(() => …)`, `forwardRef`)
- routes for components that pages import (the ancestry path from intent §17 comes later)
- Go constants as keys (would need type checking), methods on generic types, and `{{define}}`
  names as a template component
- Vue `<glossa-text>` resolving to `GlossaText` when `isCustomElement` isn't set

## Checking and adding cases

```
python3 runtimes/testdata/gen/check_usages.py        # what CI runs
python3 runtimes/testdata/gen/check_usages.py --fix  # rewrite expected.json in canonical form
```

The check validates both schemas and their examples, plus a list of broken variants that must be
rejected. For every case it validates `case.json`, and checks that `expected.json` validates, has the
runner header, is sorted and canonically formatted, and that every position points at its key in
the source. It also checks the component and route rules above where it can.

To add a case, create `<case>/case.json` and `<case>/project/…`, write `expected.json` by hand, and
run the check. `--fix` only sorts and reformats. It never invents usages.

Captures have no fixture suite yet, only [`captures.v1`](../schemas/captures.v1.schema.json) and its
[example](../schemas/examples/captures.v1.json). The capture slice adds its fixtures.

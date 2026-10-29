# Glossa conformance cases

Cases the Unicode suite doesn't cover, which every Glossa MessageFormat
implementation must pass as well:

- `mf1-to-mf2.json`: ICU MessageFormat 1 source → canonical MF2 data model. Produced by the Go kernel, which is the only MF1 converter; the JS runtime renders every model here and must match the MF1 reference output.
- `arguments.json`: message → extracted arguments (name, type, selector cases, markup).
- `compat.json`: (source, translation) pairs → structural compatibility findings.
- `runtime-format.json`: precompiled data model + values + locale → formatted output,
  for runtimes that interpret the data model without a parser. It covers de, en,
  es, fr and ja (plus ar and he for bidi).

Each file follows the shape documented at its top (`"$comment"`). Cases are
added whenever a bug is found in any implementation, so the fix is proven
everywhere.

`runtime-format.json` is generated from the reference formatter on one ICU/CLDR
version (`generatedWith`), but runtimes check it on whatever version they ship.
Its cases are therefore chosen so the output is the same in CLDR 47 (Node 22,
CI) and 48: regenerate with both before committing a new case. An
implementation that can't reproduce a case keeps a skip list with the reason
and the upstream gap, never a workaround: the Go runtime's is
`runtimeFormatSkips` in `runtimes/go/runtime_format_test.go` (go-intl's percent
pattern and `minimumGroupingDigits` gaps); `@klarlabs-studio/glossa` has none.

## MF1 → MF2 conversion rules

Glossa maps ICU MessageFormat 1 onto the **standard MF2 functions** wherever
they reproduce the MF1 output, so runtimes only need the standard function
set:

| MF1 | MF2 |
|---|---|
| `{n, plural, …}` | `.input {$n :number}` + `.match $n`; `=5` → key `5`, `other` → `*` |
| `{n, selectordinal, …}` | `.input {$n :number select=ordinal}` |
| `{x, select, …}` | `.input {$x :string}` |
| `#` in a plural | `{$n}` (the innermost enclosing plural, also through nested selects) |
| `offset:1` | `.local $n_minus_1 = {$n :offset subtract=1}`; exact keys select on `$n`, categories on `$n_minus_1`, `#` shows `$n_minus_1` |
| nested selects | lifted into one top-level `.match` over all selectors (cartesian product of keys, exact numbers first, `*` last) |
| `{n, number}` / `integer` / `percent` | `:number` / `:integer` / `:percent` |
| `::currency/EUR`, `::percent scale/100` | `:currency currency=EUR`, `:percent` |
| `{d, date}` / `short` / `medium` / `long` / `full` | `:date` / `length=short|medium|long` / `fields=year-month-day-weekday length=long` |
| `{t, time}` / `short` / `long` / `full` | `:time precision=second` / `precision=minute` / `precision=second timeZoneStyle=short` |
| anything else (`duration`, skeletons, patterns, `number, currency`) | `:mf1:<type>` with `@mf1:argType` / `@mf1:argStyle` attributes, as `@messageformat/icu-messageformat-1` does |

Apostrophe quoting follows ICU (`''` → `'`, `'{…}'` → literal text, `'#'` →
literal `#` inside plurals). `<b>…</b>` is not MF1 syntax and stays text.

Known divergences are recorded per case (`divergence` + `mf2Exp`) instead of
being hidden: plain `{n}` arguments of a plural variable are locale-formatted
in MF2 (`2.500`) where MF1 prints `String(n)` (`2500`), and `mf1:` fallbacks
are not formattable by standard runtimes.

## Verifying `mf1-to-mf2.json` against the reference implementations

`gen/verify-mf1.mjs` proves every conversion case: the hand-written `mf2`
parses (with `messageformat`) to `exp`, and for every sample the MF1 source
formatted by `@messageformat/core` equals the `exp` model formatted by
`messageformat` (draft functions plus the `mf1:` fallback functions of
`@messageformat/icu-messageformat-1`). It is a standalone npm project with
pinned versions, outside the pnpm workspace:

```sh
cd messageformat/testdata/glossa/gen
npm ci
npm run verify   # check the fixture; exits 1 on any mismatch
npm run write    # derive `exp` and sample outputs from the references, then check
```

Author `description`, `locale`, `src`, `mf2` and sample `params` by hand;
let `npm run write` fill in `exp` and the sample outputs. Both scripts run in
UTC so date and time output is reproducible. Run `go test ./...` in
`messageformat/` afterwards: the Go implementation must convert every `src`
to exactly `exp`.

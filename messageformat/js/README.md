# @klarlabs-studio/glossa/messageformat

Glossa's MessageFormat kernel for **tooling**: Studio preview, the bundler
plugin, import and export. It's a thin Glossa API over the reference
implementation ([`messageformat`](https://github.com/messageformat/messageformat)
v4 and `@messageformat/icu-messageformat-1`, pinned to exact versions).

Browsers don't get this package. They get `@klarlabs-studio/glossa`, which interprets
the same data model without a parser.

## The data model is the contract

Every function here speaks the canonical MessageFormat 2 data model as **plain
JSON**, exactly as the spec's JSON Schema
([`../testdata/unicode/data-model/message.schema.json`](../testdata/unicode/data-model/message.schema.json))
describes it. Release artifacts carry messages in this shape. The reference
implementation's in-memory model differs slightly (`functionRef` instead of
`function`, CST back-links, `quoted` flags); `toReference` / `fromReference`
convert between the two.

```ts
import { format, parseMF2, stringify, validateMessage } from "@klarlabs-studio/glossa/messageformat";

const msg = parseMF2(".input {$count :number} .match $count one {{One file}} * {{{$count} files}}");
stringify(msg); // back to MF2 syntax
format(msg, "en", { count: 1234 }); // "1,234 files", through the reference formatter

validateMessage(JSON.parse(artifactJson)); // zod shape check + data model errors
```

| Function | Does |
|---|---|
| `parseMF2(src)` | MF2 syntax → data model. Throws `MessageSyntaxError` / `MessageDataModelError`. |
| `stringify(message)` | Data model → MF2 syntax. |
| `format` / `formatToParts(message, locale, values?, opts?)` | Reference formatter with the draft (`:currency`, `:date`, …) and `mf1:` functions enabled. Never throws; errors go to `opts.onError`. |
| `validateMessage(value)` / `isMessage(value)` / `messageSchema` | Runtime validation with zod, proven equivalent to the JSON Schema in tests. |

## MF1 → MF2

This package has no MF1 parser. ICU MF1 is parsed on the server, by the Go
module's `ParseMF1(src, locale)` in
[`go.klarlabs.de/glossa/messageformat`](../mf1.go), and messages reach the
browser as the data model. The mapping it applies:

MF1 maps onto **standard** MF2 functions wherever one is equivalent
(`:number`, `:integer`, `:percent`, `:currency currency=…`, `:unit`,
`:date length=…`, `:time precision=…`, `:string` for `select`,
`:number select=ordinal` for `selectordinal`). A plural offset becomes a second
selector: `.local $n_offset = {$n :offset subtract=N}`, with exact keys (`=0`)
matched on `$n` and plural categories on `$n_offset`, and `#` pointing at
`$n_offset`. Only formats with no MF2 equivalent keep an `mf1:` function
(`number, currency` without a code, notations and scales, date skeletons,
`spellout`, `duration`, …). The MF1 argument type and style stay on the
expression as `@mf1:argType` / `@mf1:argStyle` attributes for export. The full
table is in [`mf1.go`](../mf1.go).

Date and time options follow the current spec (LDML 48: `length`, `fields`,
`precision`, `timeZoneStyle`), which the vendored conformance suite tests,
rather than LDML 47's `dateStyle` / `timeStyle`.

## Tests

- The whole Unicode suite: syntax errors and data model errors are rejected,
  every other case round-trips through `stringify` and formats as expected.
- The zod schemas accept every parsed suite message and reject malformed ones,
  exactly like the JSON Schema (checked with ajv).
- MF1 mapping unit tests, plus `messageformat/testdata/glossa/mf1-to-mf2.json`
  (shared with the Go converter) when it exists.
- `messageformat/testdata/glossa/runtime-format.json` is generated here from
  the reference formatter (see [`scripts/`](./scripts)); a test fails if the
  committed file drifts from its case list.

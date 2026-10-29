# messageformat

Glossa's MessageFormat kernel for Go: the canonical MF2 data model, MF2 and
ICU MF1 parsing, derived metadata, structural compatibility checks and
formatting for the Go runtime. The package documentation (`doc.go`) is the
reference; this file records how the kernel sits on its engine.

## Engine

Formatting runs on [`github.com/kaptinlin/messageformat-go`](https://github.com/kaptinlin/messageformat-go)
(v0.8.6), with CLDR data from [`github.com/agentable/go-intl`](https://github.com/agentable/go-intl)
(v0.2.17). No engine type crosses the package API (`engine.go`). The Unicode
conformance suite and `testdata/glossa/` prove the engine, and any
replacement.

## Engine gaps

Gaps are fixed at the kernel boundary, never worked around with lossy
conversions, and each one is a candidate upstream issue. When an upgrade
closes a gap, remove the kernel fix and its entry here.

| Gap | Kernel fix | Upstream |
|---|---|---|
| **No exact decimal operand.** `readNumericOperand` turns a numeric string into a `float64`, and the only other inputs are Go's numeric kinds and `big.Int`/`big.Float` (the latter via `Float64()` whenever that is exact). A tax amount such as `12345678901234.56` loses digits, and a `big.Float` operand can't be selected on (`SelectKeys` only reads `float64`). go-intl itself formats and selects decimal strings exactly (`numberformat.Decimal`, `pluralrules.Decimal`). | [`Decimal`](decimal.go) and [`Money`](decimal.go) operands. `engine_exact.go` wraps `:number`, `:integer`, `:percent`, `:currency`, `:unit` and `:offset`: the engine's function runs on a zero stand-in (so option validation, merging and errors stay the engine's), then the kernel formats and selects the exact value with go-intl and the resolved options. `TestDecimalMatchesEngine` proves both paths agree wherever the engine is exact. Unannotated `Decimal`/`Money` placeholders get `:number`/`:currency` (the engine renders unknown types with `%v`). | Issue to file: "Accept exact decimal operands (decimal strings) in the numeric functions", i.e. keep a JSON-number string as `numberformat.Decimal` instead of `float64`, and select on it with `pluralrules.Decimal`. |
| The MF2 serializer writes a pattern message starting with `.` (after MF2 whitespace) unquoted, and the output doesn't parse back. | `quoteIfAmbiguous` in `engine.go`. | Issue to file. |
| Parser panics on some malformed input (found by fuzzing). | `containEngineFailure` turns them into `CodeInternalError`; `engine_panic_test.go` lists the known inputs. | Issues to file, one per entry. |
| go-intl: the German percent pattern lacks CLDR's no-break space (`20%` instead of `20 %`); Spanish and French (`26 %`, U+00A0 and U+202F) have the same gap. | None possible at the boundary (CLDR data); the affected fixture cases are skipped with their reason (`conformance_glossa_test.go`, `runtimes/go/runtime_format_test.go`) and listed in the document goldens' `documentGaps` (`runtimes/go/documents_test.go`). | German fixed upstream (RFC 0004 §7.2); remove the skips with the go-intl upgrade. es/fr: issue to file. |
| go-intl: `minimumGroupingDigits` is ignored, so Spanish (where CLDR sets it to 2) groups a four-digit integer part (`1.234,50 €` instead of `1234,50 €`). | None possible at the boundary; skipped with its reason in `runtimes/go/runtime_format_test.go` and listed in `documentGaps` (`runtimes/go/documents_test.go`). | Issue to file. |
| go-intl: no CLDR currency spacing, so an alphabetic symbol or code touches the digits (`USD19.99`, `CHF19.99` in en and ja, where ICU writes `USD 19.99`). | None possible at the boundary; recorded as is in `runtimes/go/testdata/formatters.golden.json`. | Issue to file. |
| go-intl: the short zone name of UTC is `GMT` where ICU says `UTC`. | None possible at the boundary; skipped with its reason in `conformance_glossa_test.go`. | Issue to file. |

## Time zones

The engine formats a `time.Time` in its own location, or in the zone a
placeholder's `timeZone` option names. `WithTimeZone` sets the zone for the
date and time placeholders that don't name one (`engine_timezone.go`), so a
server's output doesn't depend on where its `time.Time` values came from.

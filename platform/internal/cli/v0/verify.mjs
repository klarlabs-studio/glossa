// `glossa import --from v0 --verify` (RFC 0006 §7.3), the Node half.
// The glossa binary embeds this file, writes it to a temporary directory
// and runs it with the Node it finds; the two formatters are loaded from
// the directories it is given, so nothing here is bundled with the CLI:
//
//   node verify.mjs <input.json> <output.json>
//
// input:  { formatModule, runtimeModule, edgeURL, deliveryKey, environment,
//           cases: [{ key, locale, v0Locale, args, text, requoted? }] }
// output: { release, errors: [...], rows: [{ key, locale, args, v0, v0Error,
//           v0Requoted, requotedError, runtime, runtimeError }] }
//
// Each case is rendered by v0.3's own formatter (@felixgeelhaar/glossa-format,
// its hand-written ICU subset) over v0.3's text, and by @klarlabs-studio/glossa over
// the release glossa-edge serves to the delivery key in the environment —
// with the same arguments. The two share no code. `requoted` is v0.3's text
// with its apostrophes rewritten the way ICU reads them; v0.3's formatter
// renders it too, so the CLI can tell v0.3's known apostrophe defect from
// every other difference. This file decides nothing: it renders.
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const [, , inPath, outPath] = process.argv;
const input = JSON.parse(readFileSync(inPath, "utf8"));
const { format } = await import(pathToFileURL(join(input.formatModule, "dist", "index.js")).href);
const { createRuntime } = await import(pathToFileURL(join(input.runtimeModule, "dist", "runtime", "index.js")).href);

const errors = [];
const runtimes = new Map();
let release = null;
async function runtimeFor(locale) {
  if (!runtimes.has(locale)) {
    const rt = createRuntime({
      edge: input.edgeURL,
      deliveryKey: input.deliveryKey,
      environment: input.environment,
      locales: [locale],
      storage: null,
      refreshInterval: 0,
      bidiIsolation: "none",
      onError: (e) => errors.push(`${locale}: ${e.type}: ${e.message ?? ""}`),
    });
    await rt.ready;
    runtimes.set(locale, rt);
  }
  return runtimes.get(locale);
}

function v0Render(text, locale, args) {
  try {
    return { out: format(text, locale, args) };
  } catch (e) {
    return { err: String(e?.message ?? e) || "v0.3's formatter failed" };
  }
}

const rows = [];
for (const c of input.cases) {
  const row = { key: c.key, locale: c.locale, args: c.args };
  const v0 = v0Render(c.text, c.v0Locale, c.args);
  row.v0 = v0.out;
  row.v0Error = v0.err;
  if (c.requoted !== undefined && c.requoted !== null) {
    const rq = v0Render(c.requoted, c.v0Locale, c.args);
    row.v0Requoted = rq.out;
    row.requotedError = rq.err;
  }
  try {
    const rt = await runtimeFor(c.locale);
    release = rt.release?.id ?? release;
    const ex = rt.explain(c.key);
    row.runtime = rt.t(c.key, c.args);
    if (ex.resolvedFrom !== c.locale) {
      row.runtimeError = `resolved from ${ex.resolvedFrom ?? "nothing (the inline default)"}, not ${c.locale}`;
    }
  } catch (e) {
    row.runtimeError = String(e?.message ?? e) || "@klarlabs-studio/glossa failed";
  }
  rows.push(row);
}
for (const rt of runtimes.values()) rt.dispose?.();
writeFileSync(outPath, JSON.stringify({ release, errors: errors.slice(0, 20), rows }));

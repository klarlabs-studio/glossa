// Verifies ../mf1-to-mf2.json against the reference MessageFormat
// implementations, so every expected MF2 conversion is proven to format
// exactly like its ICU MessageFormat 1 source.
//
//   npm ci && npm run verify     # check; exits 1 on any mismatch
//   npm run write                # fill in derived fields, then check
//
// For each conversion case:
//   1. `mf2` (hand-written MF2 syntax) is parsed by `messageformat` (the
//      MF2 reference) and must equal `exp` (the canonical data model, in
//      the shape of ../../unicode/data-model/message.schema.json).
//   2. For each sample, the MF1 `src` formatted by `@messageformat/core`
//      (the MF1 reference) must equal the MF2 `exp` model formatted by
//      `messageformat` with the draft functions and the MF1 fallback
//      functions of `@messageformat/icu-messageformat-1`, and both must
//      equal the sample's `exp` string.
//   3. A sample may instead document a known divergence: `mf2Exp` holds
//      the MF2 output and the case's `divergence` explains it. The script
//      then checks that the outputs really differ as recorded.
// Error cases (`expErrors`) must be rejected by the MF1 reference, except
// `mf1-unsupported`, which is Glossa's own refusal of valid MF1.
//
// `--write` derives `exp` from `mf2` and each sample's `exp` (and `mf2Exp`
// for divergences) from the reference output. Everything else is authored
// by hand. Runs in UTC (see package.json) so date and time output is stable.

import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import MessageFormat1 from '@messageformat/core';
import { MF1Functions } from '@messageformat/icu-messageformat-1';
import { MessageFormat, parseMessage } from 'messageformat';
import { DraftFunctions } from 'messageformat/functions';

const fixturePath = fileURLToPath(new URL('../mf1-to-mf2.json', import.meta.url));
const write = process.argv.includes('--write');

if (Intl.DateTimeFormat().resolvedOptions().timeZone !== 'UTC') {
  console.error('Run with TZ=UTC (npm run verify) so date and time output is reproducible.');
  process.exit(2);
}

// --- data model shape conversion ---------------------------------------------

// toCanonical converts a `messageformat` data model into the schema shape:
// `functionRef` becomes `function`, internal properties are dropped.
function toCanonical(msg) {
  const out = { type: msg.type, declarations: msg.declarations.map(canonicalDeclaration) };
  if (msg.type === 'message') {
    out.pattern = canonicalPattern(msg.pattern);
  } else {
    out.selectors = msg.selectors.map(canonicalOperand);
    out.variants = msg.variants.map(v => ({
      keys: v.keys.map(k => (k.type === '*' ? { type: '*' } : { type: 'literal', value: k.value })),
      value: canonicalPattern(v.value)
    }));
  }
  return out;
}

function canonicalDeclaration(d) {
  return { type: d.type, name: d.name, value: canonicalExpression(d.value) };
}

function canonicalPattern(pattern) {
  return pattern.map(el => {
    if (typeof el === 'string') return el;
    if (el.type === 'markup') return canonicalMarkup(el);
    return canonicalExpression(el);
  });
}

function canonicalExpression(e) {
  const out = { type: 'expression' };
  if (e.arg) out.arg = canonicalOperand(e.arg);
  if (e.functionRef) {
    out.function = { type: 'function', name: e.functionRef.name };
    if (e.functionRef.options && Object.keys(e.functionRef.options).length) {
      out.function.options = canonicalOptions(e.functionRef.options);
    }
  }
  if (e.attributes && Object.keys(e.attributes).length) out.attributes = canonicalAttributes(e.attributes);
  return out;
}

function canonicalMarkup(m) {
  const out = { type: 'markup', kind: m.kind, name: m.name };
  if (m.options && Object.keys(m.options).length) out.options = canonicalOptions(m.options);
  if (m.attributes && Object.keys(m.attributes).length) out.attributes = canonicalAttributes(m.attributes);
  return out;
}

function canonicalOperand(o) {
  return o.type === 'variable' ? { type: 'variable', name: o.name } : { type: 'literal', value: o.value };
}

function canonicalOptions(opts) {
  return Object.fromEntries(Object.entries(opts).map(([k, v]) => [k, canonicalOperand(v)]));
}

function canonicalAttributes(attrs) {
  return Object.fromEntries(Object.entries(attrs).map(([k, v]) => [k, v === true ? true : canonicalOperand(v)]));
}

// toReference converts the schema shape back into a `messageformat` model.
function toReference(msg) {
  return JSON.parse(JSON.stringify(msg), (key, value) => {
    if (value && typeof value === 'object' && value.type === 'expression' && value.function) {
      const { function: functionRef, ...rest } = value;
      return { ...rest, functionRef };
    }
    return value;
  });
}

// --- formatting ----------------------------------------------------------------

function params(list = []) {
  return Object.fromEntries(list.map(p => [p.name, p.type === 'datetime' ? new Date(p.value) : p.value]));
}

function formatMF1(locale, src, values) {
  return new MessageFormat1(locale).compile(src)(values);
}

function formatMF2(locale, model, values) {
  const errors = [];
  const mf = new MessageFormat(locale, toReference(model), {
    bidiIsolation: 'none',
    functions: { ...DraftFunctions, ...MF1Functions }
  });
  const out = mf.format(values, err => errors.push(err.type ?? err.message));
  return { out, errors };
}

// --- verification ----------------------------------------------------------------

const deepEqual = (a, b) => JSON.stringify(sortKeys(a)) === JSON.stringify(sortKeys(b));

function sortKeys(v) {
  if (Array.isArray(v)) return v.map(sortKeys);
  if (v && typeof v === 'object') {
    return Object.fromEntries(Object.keys(v).sort().map(k => [k, sortKeys(v[k])]));
  }
  return v;
}

function verifyErrorCase(tc, fail) {
  const codes = tc.expErrors.map(e => e.type);
  if (codes.includes('mf1-unsupported')) return; // Glossa-specific refusal of valid MF1
  try {
    new MessageFormat1(tc.locale).compile(tc.src);
    fail(`the MF1 reference accepts the source, but the case expects ${codes.join(', ')}`);
  } catch {
    // expected
  }
}

function verifyConversionCase(tc, fail) {
  const parsed = toCanonical(parseMessage(tc.mf2));
  if (write) tc.exp = parsed;
  else if (!deepEqual(parsed, tc.exp)) fail(`exp does not match mf2\n  mf2 parses to: ${JSON.stringify(parsed)}`);

  if (!tc.samples?.length) fail('a conversion case needs at least one sample');
  for (const sample of tc.samples ?? []) {
    const values = params(sample.params);
    const mf1 = formatMF1(tc.locale, tc.src, values);
    const mf2 = formatMF2(tc.locale, tc.exp, values);
    const diverges = 'mf2Exp' in sample;
    if (write) {
      sample.exp = mf1;
      if (diverges) sample.mf2Exp = mf2.errors.length ? null : mf2.out;
    }
    const label = JSON.stringify(sample.params ?? []);
    if (mf1 !== sample.exp) fail(`${label}: MF1 reference gives ${JSON.stringify(mf1)}, fixture says ${JSON.stringify(sample.exp)}`);
    if (!diverges) {
      if (mf2.errors.length) fail(`${label}: MF2 reference reports errors ${mf2.errors.join(', ')}`);
      if (mf2.out !== mf1) fail(`${label}: MF2 gives ${JSON.stringify(mf2.out)}, MF1 gives ${JSON.stringify(mf1)}`);
      continue;
    }
    if (!tc.divergence) fail(`${label}: mf2Exp without a case-level divergence reason`);
    const actual = mf2.errors.length ? null : mf2.out;
    if (actual !== sample.mf2Exp) fail(`${label}: MF2 gives ${JSON.stringify(actual)} (errors: ${mf2.errors.join(', ') || 'none'}), fixture says ${JSON.stringify(sample.mf2Exp)}`);
    if (actual === mf1) fail(`${label}: documented divergence no longer diverges; drop mf2Exp`);
  }
}

const fixture = JSON.parse(readFileSync(fixturePath, 'utf8'));
let failures = 0;
let samples = 0;
for (const [i, tc] of fixture.tests.entries()) {
  const fail = msg => {
    failures++;
    console.error(`✗ [${i}] ${tc.description}: ${msg}`);
  };
  try {
    if (tc.expErrors) verifyErrorCase(tc, fail);
    else verifyConversionCase(tc, fail);
    samples += tc.samples?.length ?? 0;
  } catch (err) {
    fail(err.stack ?? String(err));
  }
}

// ordered returns obj with its keys in the fixture's documented order.
function ordered(obj, keys) {
  const out = {};
  for (const k of keys) if (k in obj) out[k] = obj[k];
  for (const k of Object.keys(obj)) if (!(k in out)) out[k] = obj[k];
  return out;
}

if (write) {
  fixture.tests = fixture.tests.map(tc => {
    const c = ordered(tc, ['description', 'locale', 'src', 'mf2', 'exp', 'divergence', 'samples', 'expErrors']);
    if (c.samples) c.samples = c.samples.map(s => ordered(s, ['params', 'exp', 'mf2Exp']));
    return c;
  });
  writeFileSync(fixturePath, JSON.stringify(fixture, null, 2) + '\n');
}
const cases = fixture.tests.length;
const conversions = fixture.tests.filter(tc => !tc.expErrors).length;
const divergent = fixture.tests.flatMap(tc => tc.samples ?? []).filter(s => 'mf2Exp' in s).length;
if (failures) {
  console.error(`\n${failures} failure(s) in ${cases} cases`);
  process.exit(1);
}
console.log(
  `✓ ${conversions} conversion cases (${samples} samples, ${divergent} documented divergences) and ` +
    `${cases - conversions} error cases agree with the reference implementations`
);

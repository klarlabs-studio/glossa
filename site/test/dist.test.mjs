// Checks the built page (run `pnpm build` first). Cheap guards for what the
// deployment depends on: one h1, landmarks, the CTA targets, and nothing
// the strict CSP (self-only, no inline) would block.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const html = readFileSync(new URL("../dist/index.html", import.meta.url), "utf8");

test("one h1, with the landmarks", () => {
  assert.equal((html.match(/<h1[\s>]/g) ?? []).length, 1);
  for (const tag of ["<header", "<main", "<footer", '<nav aria-label="Page sections"']) assert.ok(html.includes(tag), tag);
});

test("CTAs point at Studio's routes", () => {
  for (const href of ["/auth/sign-in", "/auth/register", "/app", "https://github.com/klarlabs-studio/glossa"])
    assert.ok(html.includes(`href="${href}"`), href);
});

test("nothing the CSP would block", () => {
  assert.ok(!/<script/i.test(html), "no script");
  assert.ok(!/<style/i.test(html), "no inline style element");
  assert.ok(!/\sstyle=/i.test(html), "no style attribute");
  assert.ok(!/\son[a-z]+=/i.test(html), "no inline handlers");
  assert.ok(!/(src|href)="https?:\/\/[^"]*\.(js|css|woff2?)"/i.test(html), "no external assets");
});

test("meta for sharing", () => {
  for (const m of ['name="description"', 'property="og:title"', 'property="og:image"', 'rel="canonical"', 'rel="icon"'])
    assert.ok(html.includes(m), m);
});

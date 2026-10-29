import { describe, expect, it } from "vitest";

import { scanMarkup } from "./markup.js";

const keys = (html: string) => scanMarkup(html).map((h) => [h.key, html.slice(h.offset, h.offset + h.key.length)]);

describe("scanMarkup", () => {
  it("finds glossa elements by key, else message, with quoted and unquoted values", () => {
    const html = `<glossa-text key="a.b">x</glossa-text><GLOSSA-RICH message='c.d'></GLOSSA-RICH>
      <glossa-plural
        count=3 key=e.f></glossa-plural><glossa-select message="x.y" key="g.h"/>`;
    expect(keys(html)).toEqual([
      ["a.b", "a.b"],
      ["c.d", "c.d"],
      ["e.f", "e.f"],
      ["g.h", "g.h"],
    ]);
  });

  it("skips comments, raw text, escaped markup, other elements, ids and invalid keys", () => {
    const html = [
      `<!-- <glossa-text key="fake.comment"></glossa-text> -->`,
      `<script>const s = '<glossa-text key="fake.script">';</script>`,
      `<textarea><glossa-text key="fake.textarea"></textarea>`,
      `&lt;glossa-text key="fake.escaped"&gt;`,
      `<glossa-texts key="fake.other"></glossa-texts><my-glossa-text key="fake.prefixed">`,
      `<glossa-text id="hero"></glossa-text><glossa-text key="Not A Key"></glossa-text>`,
      `<glossa-text key="" message="fake.key-wins"></glossa-text>`,
      `<glossa-text key="nav.home"></glossa-text>`,
    ].join("\n");
    expect(keys(html)).toEqual([["nav.home", "nav.home"]]);
  });
});

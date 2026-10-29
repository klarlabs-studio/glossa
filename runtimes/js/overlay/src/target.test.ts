import { describe, expect, it } from "vitest";
import { mark } from "@klarlabs-studio/glossa-capture";
import type { LoggedRender } from "@klarlabs-studio/glossa-capture";

import { locate } from "./target.js";

const log: LoggedRender[] = [
  { id: "cart.greeting", locale: "de", digest: "0" },
  { id: "user.name", locale: "de", digest: "0" },
  { id: "search.placeholder", locale: "de", digest: "0" },
  { id: "promo.banner", locale: "en", digest: "0" },
  { id: "profile.save", locale: undefined, digest: "0" },
];

const greeting = mark(0, `Hallo ${mark(1, "Lina")}!`);

function page(html: string): HTMLElement {
  document.body.innerHTML = html;
  return document.body;
}

describe("locate", () => {
  it("picks the innermost marked range around the caret", () => {
    page(`<p id="p"></p>`);
    const p = document.getElementById("p")!;
    p.textContent = greeting;
    const node = p.firstChild!;
    const at = (visible: string) => (node as Text).data.indexOf(visible);
    expect(locate(log, [p], { node, offset: at("Hallo") + 1 })).toMatchObject({
      id: "cart.greeting",
      locale: "de",
      kind: "text",
    });
    expect(locate(log, [p], { node, offset: at("Lina") + 2 })).toMatchObject({ id: "user.name" });
  });

  it("joins text runs a framework split", () => {
    page(`<p id="p"></p>`);
    const p = document.getElementById("p")!;
    const [a, b] = [greeting.slice(0, 5), greeting.slice(5)];
    p.append(a, b);
    const second = p.childNodes[1]!;
    const offset = (second as Text).data.indexOf("Lina") + 1;
    expect(locate(log, [p], { node: second, offset })?.id).toBe("user.name");
  });

  it("finds a marked attribute of the clicked element", () => {
    page(`<input id="i">`);
    const input = document.getElementById("i") as HTMLInputElement;
    input.placeholder = mark(2, "Suchen …");
    expect(locate(log, [input, document.body])).toMatchObject({
      id: "search.placeholder",
      kind: "attribute",
    });
  });

  it("uses the nearest component host, unless the caret is in marked text inside it", () => {
    page(
      `<glossa-text id="h" data-glossa-id="cart.checkout" data-glossa-locale="de"><b id="b"></b></glossa-text>`,
    );
    const host = document.getElementById("h")!;
    const b = document.getElementById("b")!;
    expect(locate(log, [b, host, document.body])).toMatchObject({
      id: "cart.checkout",
      locale: "de",
      kind: "element",
      element: host,
    });
    b.textContent = mark(1, "Lina");
    const node = b.firstChild!;
    expect(locate(log, [b, host], { node, offset: 3 })?.id).toBe("user.name");
  });

  it("falls back to the one message an element shows, and to nothing when it shows several", () => {
    page(`<button id="one"></button><div id="many"></div>`);
    const one = document.getElementById("one")!;
    one.textContent = greeting;
    expect(locate(log, [one])?.id).toBe("cart.greeting");
    const many = document.getElementById("many")!;
    many.textContent = mark(3, "Gratis") + mark(4, "Speichern");
    expect(locate(log, [many])).toBeUndefined();
    many.textContent = mark(4, "Speichern");
    expect(locate(log, [many])).toMatchObject({ id: "profile.save", locale: undefined });
  });

  it("ignores indexes outside the log and unmarked elements", () => {
    page(`<p id="p"></p>`);
    const p = document.getElementById("p")!;
    p.textContent = mark(99, "?");
    expect(locate(log, [p])).toBeUndefined();
    p.textContent = "plain";
    expect(locate(log, [p], { node: p.firstChild!, offset: 1 })).toBeUndefined();
  });
});

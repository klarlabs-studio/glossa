// @vitest-environment jsdom
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, createElement as h } from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";

import { createGlossa } from "./index.js";
import { Root, r1 } from "./testing/app.js";

beforeAll(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

const marks = (root: Element) =>
  Array.from(root.querySelectorAll("[data-glossa-id]")).map((el) => [
    el.tagName,
    el.getAttribute("data-glossa-id"),
    el.getAttribute("data-glossa-locale"),
    (el as HTMLElement).style.display,
    el.textContent,
  ]);

const marked = [
  ["SPAN", "terms.hint", "de", "contents", "Lies die AGB."],
  ["SPAN", "athlete.greeting", "de", "contents", "Hallo, \u2068Lina\u2069!"],
  ["SPAN", "no.such.key", null, "contents", "Standard"],
];

describe("capture mode (RFC 0004 §3.1)", () => {
  it("marks <T> hosts and lets the hook decorate t() only while a hook is installed", async () => {
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(h(Root, { glossa })));
    const plain = container.innerHTML;
    expect(marks(container)).toEqual([]);

    let off = () => {};
    await act(async () => {
      off = glossa.runtime.onRender((r) => `[${r.output}]`);
    });
    expect(marks(container)).toEqual(marked);
    expect(container.querySelector("h1")!.textContent).toBe("[Zur Kasse]");

    await act(async () => off());
    expect(container.innerHTML).toBe(plain);
    root.unmount();
  });

  it("hydrates without mismatches when a session started before hydration, then marks", async () => {
    const server = createGlossa({ bundled: r1, locales: "de", storage: null });
    const html = renderToString(h(Root, { glossa: server }));
    expect(html).not.toContain("data-glossa");
    const container = document.createElement("div");
    container.innerHTML = html;
    document.body.append(container);

    const seen: string[] = [];
    const record = (...args: unknown[]) => seen.push(args.map(String).join(" "));
    vi.spyOn(console, "error").mockImplementation(record);
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    glossa.runtime.onRender(() => undefined);
    const root = await act(async () =>
      hydrateRoot(container, h(Root, { glossa }), { onRecoverableError: record }),
    );
    expect(seen).toEqual([]);
    expect(marks(container)).toEqual(marked);
    root.unmount();
  });
});

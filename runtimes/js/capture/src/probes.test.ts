/**
 * The visual probe pass (RFC 0005 §5.2). One fixture per probe, with the
 * layout stated rather than measured — jsdom has no layout engine, and a
 * browser's would make the thresholds depend on its fonts. Nothing here
 * touches the network.
 */
import { afterEach, describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { BundledRelease, Runtime } from "@klarlabs-studio/glossa-runtime";

import { probe } from "./probes.js";
import type { ProbeFinding } from "./probes.js";
import { startCapture } from "./session.js";
import type { CaptureSession, SessionCapture } from "./session.js";
import { fonts, layout } from "./testing/layout.js";
import { fixture, msg, release } from "./testing/release.js";
import { findingErrors, schemaErrors } from "./testing/schema.js";

let session: CaptureSession | undefined;
const undo: Array<() => void> = [];

afterEach(() => {
  session?.stop();
  session = undefined;
  undo.splice(0).forEach((f) => f());
  document.body.innerHTML = "";
});

const runtime = (locales: string, bundled: BundledRelease = fixture): Runtime =>
  createRuntime({ bundled, locales, environment: "preview", storage: null });

/** Codes and the keys they name, in report order. */
const found = (c: SessionCapture) => c.probes.map((p) => [p.code, p.locus.key]);
const one = (c: SessionCapture, code: string): ProbeFinding => {
  const hit = c.probes.filter((p) => p.code === code);
  expect(hit).toHaveLength(1);
  return hit[0]!;
};

/**
 * Every probe, whatever it found, is a finding.v1 once the ingest completes
 * it — and the capture manifest can carry it as written.
 *
 * The second half is not the same assertion as the first. A finding the page
 * writes is only worth writing if it reaches the server, and it reaches the
 * server on a `glossa.captures/v1` manifest, whose `findings` are that shape
 * minus the two members a page cannot know. Validating only finding.v1 left
 * the carrying end untested, which is how the probes came to be measured and
 * then dropped.
 */
const valid = (c: SessionCapture) => [
  ...c.probes.flatMap((p) => findingErrors(p)),
  ...schemaErrors(c),
];

describe("text-clipped", () => {
  it("reports the clipping container's own numbers, once for a message on three lines", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<div id="box" style="overflow-x: hidden"><span id="t"></span></div>`;
    document.getElementById("t")!.textContent = rt.t("long.text");
    undo.push(
      layout({
        box: { box: { x: 0, y: 0, width: 100, height: 60 }, client: [100, 60], scroll: [420, 60] },
        t: {
          box: { x: 0, y: 0, width: 100, height: 60 },
          lines: [
            { x: 0, y: 0, width: 100, height: 20 },
            { x: 0, y: 20, width: 100, height: 20 },
            { x: 0, y: 40, width: 100, height: 20 },
          ],
        },
      }),
    );

    const capture = session.collect();
    expect(found(capture)).toEqual([["text-clipped", "long.text"]]);
    expect(one(capture, "text-clipped")).toMatchObject({
      schema: "glossa.finding/v1",
      layer: "visual",
      severity: "warning",
      locus: { key: "long.text", region: "r_0" },
      evidence: { box: [100, 60], content: [420, 60] },
    });
    expect(capture.metrics).toEqual({ "long.text": 3 });
    expect(valid(capture)).toEqual([]);
  });

  it("says nothing when the container shows everything, or doesn't clip", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<div id="box" style="overflow-x: hidden"><span id="t"></span></div>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(
      layout({
        box: { box: { x: 0, y: 0, width: 100, height: 20 }, client: [100, 20], scroll: [100, 20] },
        t: { box: { x: 0, y: 0, width: 80, height: 20 } },
      }),
    );
    expect(session.collect().probes).toEqual([]);
  });
});

describe("region-overlap", () => {
  it("reports a pair that covers more than a quarter of the smaller, once", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span>`;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("settings.save");
    undo.push(
      layout({
        a: { box: { x: 0, y: 0, width: 100, height: 20 } },
        b: { box: { x: 50, y: 0, width: 100, height: 20 } },
      }),
    );

    const capture = session.collect();
    expect(found(capture)).toEqual([["region-overlap", "profile.save"]]);
    expect(one(capture, "region-overlap")).toMatchObject({
      subject: "settings.save",
      locus: { key: "profile.save", region: "r_0" },
      evidence: { share: 50 },
    });
    expect(valid(capture)).toEqual([]);
  });

  it("ignores a quarter or less, and a region inside another", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span><div id="outer"><span id="inner"></span></div>`;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("settings.save");
    document.getElementById("outer")!.setAttribute("data-glossa-id", "cart.checkout");
    document.getElementById("inner")!.textContent = rt.t("draft.save");
    undo.push(
      layout({
        a: { box: { x: 0, y: 0, width: 100, height: 20 } },
        b: { box: { x: 80, y: 0, width: 100, height: 20 } },
        outer: { box: { x: 0, y: 100, width: 200, height: 40 } },
        inner: { box: { x: 10, y: 110, width: 100, height: 20 } },
      }),
    );
    expect(session.collect().probes).toEqual([]);
  });
});

describe("line-growth", () => {
  it("compares the line boxes against the source capture's, above the tolerance", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t"></span>`;
    document.getElementById("t")!.textContent = rt.t("long.text");
    undo.push(
      layout({
        t: {
          box: { x: 0, y: 0, width: 100, height: 60 },
          lines: [
            { x: 0, y: 0, width: 100, height: 20 },
            { x: 0, y: 20, width: 100, height: 20 },
            { x: 0, y: 40, width: 100, height: 20 },
          ],
        },
      }),
    );

    expect(session.collect().probes).toEqual([]); // no baseline decides nothing
    const grown = session.collect(document, { baseline: { "long.text": 1 } });
    expect(found(grown)).toEqual([["line-growth", "long.text"]]);
    expect(one(grown, "line-growth")).toMatchObject({
      message: "Wraps to 3 lines, up from 1.",
      evidence: { lines: 3, source_lines: 1 },
    });
    expect(valid(grown)).toEqual([]);
    expect(session.collect(document, { baseline: { "long.text": 1 }, tolerance: 2 }).probes).toEqual(
      [],
    );
    expect(session.collect(document, { baseline: { "other.key": 1 } }).probes).toEqual([]);
  });
});

describe("rtl-not-mirrored", () => {
  it("reports a region that still lays out left to right in an RTL locale", () => {
    const rt = runtime("ar");
    expect(rt.dir).toBe("rtl");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t" style="direction: ltr"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(layout({ t: { box: { x: 0, y: 0, width: 80, height: 20 } } }));

    const capture = session.collect();
    expect(found(capture)).toEqual([["rtl-not-mirrored", "profile.save"]]);
    expect(one(capture, "rtl-not-mirrored").message).toBe("Not mirrored: direction ltr.");
    expect(valid(capture)).toEqual([]);
  });

  it("says nothing when the region mirrored, and nothing at all in an LTR locale", () => {
    const rt = runtime("ar");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t" style="direction: rtl"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(layout({ t: { box: { x: 0, y: 0, width: 80, height: 20 } } }));
    expect(session.collect().probes).toEqual([]);

    session.stop();
    document.body.innerHTML = "";
    const de = runtime("de");
    session = startCapture(de, { probe });
    document.body.innerHTML = `<span id="u" style="direction: ltr"></span>`;
    document.getElementById("u")!.textContent = de.t("profile.save");
    undo.push(layout({ u: { box: { x: 0, y: 0, width: 80, height: 20 } } }));
    expect(session.collect().probes).toEqual([]);
  });
});

describe("missing-glyph", () => {
  it("reports what document.fonts.check() refuses, with the font it asked for", () => {
    const rt = runtime("ar");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t" style="direction: rtl; font-family: Tofu; font-size: 16px"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(layout({ t: { box: { x: 0, y: 0, width: 80, height: 20 } } }), fonts(false));

    const capture = session.collect();
    expect(found(capture)).toEqual([["missing-glyph", "profile.save"]]);
    expect(one(capture, "missing-glyph").evidence).toMatchObject({ font: expect.stringContaining("Tofu") });
    expect(valid(capture)).toEqual([]);
  });

  it("says nothing when the fonts cover the text", () => {
    const rt = runtime("ar");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t" style="direction: rtl"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(layout({ t: { box: { x: 0, y: 0, width: 80, height: 20 } } }), fonts(true));
    expect(session.collect().probes).toEqual([]);
  });
});

describe("untranslated-on-screen", () => {
  it("comes from explain(), not from the text: a fallback locale on a translated screen", () => {
    const rt = runtime("ar");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a" style="direction: rtl"></span><span id="b" style="direction: rtl"></span>`;
    document.getElementById("a")!.textContent = rt.t("profile.save"); // ar has it
    document.getElementById("b")!.textContent = rt.t("settings.save"); // falls back to de

    const capture = session.collect();
    expect(found(capture)).toEqual([["untranslated-on-screen", "settings.save"]]);
    expect(one(capture, "untranslated-on-screen")).toMatchObject({
      locus: { key: "settings.save", locale: "ar", region: "r_1" },
      message: "Rendered from de on a ar screen.",
      evidence: { resolved_from: "de" },
    });
    expect(valid(capture)).toEqual([]);
  });

  it("reports an inline default the same way, and says nothing for the source locale", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span>`;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("no.such.key", {}, { default: "Standard" });

    const capture = session.collect();
    expect(found(capture)).toContainEqual(["untranslated-on-screen", "no.such.key"]);
    expect(one(capture, "untranslated-on-screen").message).toBe(
      "Rendered from the inline default on a de screen.",
    );
    expect(valid(capture)).toEqual([]);
  });
});

describe("mixed-locale", () => {
  /** Three locales off one source, so two of them share no fallback chain. */
  const three = release({
    de: { "profile.save": msg("Speichern"), "settings.save": msg("Speichern") },
    fr: { "profile.save": msg("Enregistrer"), "settings.save": msg("Enregistrer") },
    ja: { "profile.save": msg("保存"), "settings.save": msg("保存") },
  });

  it("names the screen's locale and reports the keys that resolved outside its chain", () => {
    const fr = runtime("fr", three);
    const ja = runtime("ja", three);
    session = startCapture([fr, ja], { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span><span id="c"></span>`;
    document.getElementById("a")!.textContent = fr.t("profile.save");
    document.getElementById("b")!.textContent = fr.t("settings.save");
    document.getElementById("c")!.textContent = ja.t("profile.save");

    const capture = session.collect();
    expect(found(capture)).toEqual([["mixed-locale", "profile.save"]]);
    expect(one(capture, "mixed-locale")).toMatchObject({
      locus: { key: "profile.save", locale: "ja" },
      subject: "fr",
      message: "Rendered from ja on a fr screen.",
    });
    expect(valid(capture)).toEqual([]);
  });

  it("says nothing when the two locales are in one fallback chain", () => {
    const fr = runtime("fr", three);
    const de = runtime("de", three);
    session = startCapture([fr, de], { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span>`;
    document.getElementById("a")!.textContent = fr.t("profile.save");
    document.getElementById("b")!.textContent = de.t("settings.save");
    expect(session.collect().probes).toEqual([]);
  });
});

describe("the error-channel drain", () => {
  it("turns what the session's runtimes reported into runtime-* findings", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a"></span>`;
    document.getElementById("a")!.textContent = rt.t("nope.not.here");
    expect(session.errors).toEqual([
      {
        type: "missing-message",
        detail: "not in de",
        messageId: "nope.not.here",
        locale: "de",
        releaseId: "rel_1",
      },
    ]);

    const capture = session.collect();
    expect(found(capture)).toContainEqual(["runtime-missing-message", "nope.not.here"]);
    expect(one(capture, "runtime-missing-message")).toMatchObject({
      layer: "visual",
      severity: "warning",
      locus: { key: "nope.not.here", locale: "de" },
      message: "The runtime reported missing-message: not in de",
    });
    expect(valid(capture)).toEqual([]);
  });

  it("drains every runtime in the session, and drops a message ID no locus can name", () => {
    const de = runtime("de");
    const ar = runtime("ar");
    session = startCapture([de, ar], { probe });
    de.t("nope.one");
    ar.t("Not A Key");
    const { probes } = session.collect();
    expect(probes.map((p) => [p.code, p.locus.key, p.locus.locale])).toEqual([
      ["runtime-missing-message", "nope.one", "de"],
      ["runtime-missing-message", undefined, "ar"],
    ]);
  });
});

describe("the probe pass as a whole", () => {
  it("is given to a session, not imported by it: without one, a capture just has no findings", () => {
    const rt = runtime("ar");
    session = startCapture(rt); // no { probe }: the in-product editor's session
    document.body.innerHTML = `<span id="t" style="direction: ltr"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    rt.t("nope.zero");
    undo.push(layout({ t: { box: { x: 0, y: 0, width: 80, height: 20 } } }));

    const capture = session.collect();
    expect(capture.regions).toHaveLength(1); // the regions are still collected…
    expect(capture.probes).toEqual([]); // …and nothing measured them.
    expect(capture.metrics).toEqual({});
    expect(session.errors).toHaveLength(1); // the drain still fills; only the pass is absent.
  });

  it("never breaks a capture: a probe that throws costs its own finding and no more", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="t"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    rt.t("nope.three");
    const was = Object.getOwnPropertyDescriptor(document, "fonts");
    Object.defineProperty(document, "fonts", {
      configurable: true,
      value: {
        check() {
          throw new Error("no font set for you");
        },
      },
    });
    undo.push(() => {
      if (was) Object.defineProperty(document, "fonts", was);
      else delete (document as unknown as Record<string, unknown>).fonts;
    });

    const capture = session.collect();
    expect(capture.regions).toHaveLength(1);
    expect(found(capture)).toEqual([["runtime-missing-message", "nope.three"]]);
  });

  it("hooks runtimes added after the start into the log and the drain", () => {
    session = startCapture([], { probe });
    const rt = runtime("de");
    session.add(rt);
    document.body.innerHTML = `<span id="t"></span>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    rt.t("nope.two");
    const capture = session.collect();
    expect(capture.renders.map((r) => r.key)).toEqual(["profile.save"]);
    expect(found(capture)).toEqual([["runtime-missing-message", "nope.two"]]);
  });
});

/**
 * The thresholds are the check policy's (RFC 0005 §5.2). `glossa capture`
 * reads them from the project's policy and hands them to `collect()`, so what
 * a page measures against is what the project decided — the constants in
 * probes.ts are only what a probe run without a driver falls back to.
 */
describe("the policy's thresholds", () => {
  it("decide when a region counts as clipped", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<div id="box" style="overflow-x: hidden"><span id="t"></span></div>`;
    document.getElementById("t")!.textContent = rt.t("profile.save");
    undo.push(
      layout({
        box: { box: { x: 0, y: 0, width: 100, height: 20 }, client: [100, 20], scroll: [110, 20] },
        t: { box: { x: 0, y: 0, width: 100, height: 20 } },
      }),
    );

    // Ten CSS pixels of content over the box: clipped at the default slack…
    expect(found(session.collect())).toEqual([["text-clipped", "profile.save"]]);
    // …and not at a slack the policy widened.
    expect(session.collect(document, { slack: 20 }).probes).toEqual([]);
  });

  it("decide when two regions overlap, and how many findings a capture may report", () => {
    const rt = runtime("de");
    session = startCapture(rt, { probe });
    document.body.innerHTML = `<span id="a"></span><span id="b"></span>`;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("settings.save");
    undo.push(
      layout({
        a: { box: { x: 0, y: 0, width: 100, height: 20 } },
        b: { box: { x: 50, y: 0, width: 100, height: 20 } },
      }),
    );

    // Half of the smaller region: reported at the default quarter, and
    // not at the three quarters a policy could ask for.
    expect(found(session.collect())).toEqual([["region-overlap", "profile.save"]]);
    expect(session.collect(document, { overlap: 75 }).probes).toEqual([]);
    expect(session.collect(document, { max: 0 }).probes).toEqual([]);
  });
});

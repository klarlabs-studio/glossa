import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { capture, createFakeContext } from "../../test/fake-context";
import { checkRun, createFakeQuality, finding, waiver, type FakeQuality } from "../../test/fake-quality";
import { locale, mountProjectScreen } from "../../test/project";
import QualityView from "./QualityView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const LOCALES = [locale("en", true), locale("de"), locale("fr")];

const run = checkRun({ id: "run1" });

const seeded = () => {
  const structure = finding({ layer: "structure", code: "invalid-message", severity: "error", locus: { key: "checkout.total", locale: "fr" } });
  const term = finding({
    layer: "terminology",
    code: "term_forbidden",
    severity: "error",
    locus: { key: "auth.login", locale: "de", file: "src/auth/SignIn.vue", line: 12 },
    message: "“Anmelden” is forbidden here; the termbase says “Login”.",
  });
  const length = finding({
    layer: "length",
    code: "expansion-excessive",
    severity: "warning",
    locus: { key: "checkout.pay", locale: "fr" },
    fix: { kind: "shorten", to: 28 },
  });
  const visual = finding({
    layer: "visual",
    code: "text-clipped",
    severity: "warning",
    locus: { key: "checkout.pay", locale: "ja", capture: "cap1", region: "r_18" },
  });
  return { structure, term, length, visual };
};

async function screen(
  port: FakeQuality,
  over: { roles?: Parameters<typeof mountProjectScreen>[1]["roles"]; context?: Parameters<typeof mountProjectScreen>[1]["context"]; path?: string } = {},
) {
  wrapper = await mountProjectScreen(QualityView, {
    quality: port,
    locales: LOCALES,
    path: over.path ?? "/t/t/p/p/quality",
    ...(over.roles ? { roles: over.roles } : {}),
    ...(over.context ? { context: over.context } : {}),
  });
  await flushPromises();
  return wrapper;
}

const layers = (w: VueWrapper) => w.findAll("[data-testid=layer]").map((s) => s.attributes("data-layer"));
const findings = (w: VueWrapper) => w.findAll("[data-testid=finding]");
const layer = (w: VueWrapper, name: string) => w.get(`[data-layer=${name}]`);
const button = (root: { findAll(selector: string): DOMWrapper<Element>[] }, name: string | RegExp) => {
  const b = root.findAll("button").find((x) => (typeof name === "string" ? x.text() === name : name.test(x.text())));
  if (!b) throw new Error(`no button ${name}`);
  return b;
};
const dialog = (w: VueWrapper) => w.get("dialog[open]");

describe("QualityView", () => {
  it("says nothing has been checked yet, rather than showing a clean run", async () => {
    const w = await screen(createFakeQuality());
    expect(w.get("[data-testid=quality-never-checked]").text()).toContain("Nothing has been checked yet.");
    expect(w.find("[data-testid=quality-counts]").exists()).toBe(false);
  });

  it("groups the findings by the layer that found them, in the RFC's order", async () => {
    const { structure, term, length, visual } = seeded();
    const w = await screen(createFakeQuality({ runs: [run], findings: { run1: [structure, term, length, visual] } }));
    // Every layer the run computed gets a section, findings or not.
    expect(layers(w)).toEqual(["structure", "parity", "completeness", "terminology", "length", "visual"]);
    expect(layer(w, "parity").get("[data-testid=layer-clean]").text()).toBe("Checked, nothing found.");
    expect(layer(w, "terminology").text()).toContain("term_forbidden");
    expect(layer(w, "terminology").get("[data-testid=finding-place]").text()).toBe("src/auth/SignIn.vue:12");
    expect(layer(w, "length").get("[data-testid=finding-fix]").text()).toBe("Suggested: shorten to 28 characters.");
    expect(findings(w)).toHaveLength(4);
  });

  it("names the layers the run never computed, so an unchecked layer never reads as a green one", async () => {
    const { structure } = seeded();
    const w = await screen(createFakeQuality({ runs: [run], findings: { run1: [structure] } }));
    const note = w.get("[data-testid=quality-not-checked]").text();
    expect(note).toContain("Style");
    expect(note).toContain("Linguistic");
    expect(note).not.toContain("Structure");
  });

  it("shows the run's verdict and counts waived on its own", async () => {
    const { structure, term, length } = seeded();
    const port = createFakeQuality({
      runs: [run],
      findings: { run1: [structure, term, length] },
      waivers: [waiver({ fingerprint: length.fingerprint, reason: "The French button is two lines by design." })],
    });
    const w = await screen(port);
    const counts = w.get("[data-testid=quality-counts]").text();
    expect(counts).toContain("2 errors");
    expect(counts).toContain("0 warnings");
    expect(counts).toContain("1 waived");
    expect(w.text()).toContain("Waived findings are counted on their own");
    expect(w.text()).toContain("Ran against main");
    expect(w.text()).toContain("policy v3");
  });

  it("keeps a waived finding in its layer, marked and with its reason", async () => {
    const { term } = seeded();
    const port = createFakeQuality({
      runs: [run],
      findings: { run1: [term] },
      waivers: [waiver({ fingerprint: term.fingerprint, reason: "“Anmelden” is our brand's word." })],
    });
    const w = await screen(port);
    const item = w.get("[data-testid=finding]");
    expect(item.get("[data-testid=finding-severity]").text()).toBe("Waived");
    expect(item.get("[data-testid=finding-waiver]").text()).toContain("Waived: “Anmelden” is our brand's word.");
    // It is still the terminology layer's finding, still readable, still counted.
    expect(layer(w, "terminology").text()).toContain("term_forbidden");
  });

  it("refuses to waive without a reason, before anyone waits for the server", async () => {
    const { term } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [term] } });
    const w = await screen(port);
    await w.get("[data-testid=finding-waive]").trigger("click");
    await flushPromises();
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(dialog(w).get("[data-testid=waive-reason-error]").text()).toContain("Say why.");
    expect(port.calls.some((c) => c[0] === "createWaiver")).toBe(false);
    expect(port.state.waivers).toHaveLength(0);
  });

  it("waives with a reason: the finding stays, marked, and the counts move", async () => {
    const { term } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [term] } });
    const w = await screen(port);
    expect(w.get("[data-testid=quality-counts]").text()).toContain("1 error");

    await w.get("[data-testid=finding-waive]").trigger("click");
    await flushPromises();
    await dialog(w).get("#waive-reason").setValue("“Anmelden” is our brand's word.");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();

    expect(port.calls.find((c) => c[0] === "createWaiver")?.[1]).toMatchObject({
      fingerprint: term.fingerprint,
      reason: "“Anmelden” is our brand's word.",
      scope: "project",
    });
    expect(w.get("[data-testid=quality-status]").text()).toContain("term_forbidden is accepted, and still listed.");
    expect(findings(w)).toHaveLength(1);
    expect(w.get("[data-testid=finding-severity]").text()).toBe("Waived");
    const counts = w.get("[data-testid=quality-counts]").text();
    expect(counts).toContain("0 errors");
    expect(counts).toContain("1 waived");
  });

  it("asks for the branch when a waiver only reaches one", async () => {
    const { term } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [term] } });
    const w = await screen(port);
    await w.get("[data-testid=finding-waive]").trigger("click");
    await flushPromises();
    await dialog(w).get("#waive-reason").setValue("Only while this branch is open.");
    await dialog(w).findAll("input[type=radio]")[1]!.setValue();
    await flushPromises();
    // It defaults to the ref the run graded.
    expect((dialog(w).get("#waive-ref").element as HTMLInputElement).value).toBe("main");
    await dialog(w).get("#waive-ref").setValue("");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(dialog(w).text()).toContain("Name the branch this waiver is for.");
    expect(port.state.waivers).toHaveLength(0);
  });

  it("takes a waiver back: the finding is an ordinary one again, and the waiver stays as history", async () => {
    const { term } = seeded();
    const made = waiver({ fingerprint: term.fingerprint, reason: "Agreed with legal.", accepts: { layer: "terminology", code: "term_forbidden" } });
    const port = createFakeQuality({ runs: [run], findings: { run1: [term] }, waivers: [made] });
    const w = await screen(port);
    expect(w.get("[data-testid=finding-severity]").text()).toBe("Waived");

    await w.get("[data-testid=finding-revoke]").trigger("click");
    await flushPromises();
    expect(port.calls.some((c) => c[0] === "revokeWaiver")).toBe(true);
    expect(w.get("[data-testid=finding-severity]").text()).toBe("Error");
    expect(w.get("[data-testid=quality-status]").text()).toContain("is taken back");
    // Nothing is deleted: the revoked waiver is still listed, as history.
    expect(w.get("[data-testid=waiver-list]").text()).toContain("Agreed with legal.");
    expect(w.get("[data-testid=waiver-list]").text()).toContain("Revoked");
  });

  it("lists the waivers with what they accept, and names the unexamined ones", async () => {
    const port = createFakeQuality({
      runs: [run],
      findings: { run1: [] },
      waivers: [waiver({ fingerprint: "f_00000000000000aa", reason: "Nobody remembers." })],
    });
    const w = await screen(port);
    expect(w.get("[data-testid=waiver-unexamined]").text()).toContain("No stored finding carries this fingerprint any more.");
  });

  it("filters by layer, severity and locale, and puts them in the URL", async () => {
    const { structure, term, length, visual } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [structure, term, length, visual] } });
    const w = await screen(port);

    await w.get("#q-layer").setValue("length");
    await flushPromises();
    expect(findings(w)).toHaveLength(1);
    // A filtered list shows only the layers that matched: a "clean" section here would be about the filter, not the run.
    expect(layers(w)).toEqual(["length"]);
    expect(w.get("[data-testid=quality-matched]").text()).toBe("1 finding matches.");
    expect(port.calls.at(-3)?.[1]).toMatchObject({ layer: "length" });

    await w.get("[data-testid=quality-clear]").trigger("click");
    await flushPromises();
    await w.get("#q-severity").setValue("error");
    await flushPromises();
    expect(findings(w)).toHaveLength(2);

    await w.get("[data-testid=quality-clear]").trigger("click");
    await flushPromises();
    await w.get("#q-locale").setValue("fr");
    await flushPromises();
    expect(findings(w)).toHaveLength(2);
    expect(w.vm.$route.query).toMatchObject({ locale: "fr" });
  });

  it("starts from the filters a bookmarked URL carries", async () => {
    const { structure, term, length, visual } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [structure, term, length, visual] } });
    const w = await screen(port, { path: "/t/t/p/p/quality?layer=visual" });
    expect(findings(w)).toHaveLength(1);
    expect((w.get("#q-layer").element as HTMLSelectElement).value).toBe("visual");
  });

  it("shows a visual finding's capture, cropped around the message and outlined", async () => {
    const { visual } = seeded();
    const context = createFakeContext({ captures: [capture("cap1", ["checkout.pay"], { route: "/checkout", locale: "ja" })] });
    const port = createFakeQuality({ runs: [run], findings: { run1: [visual] } });
    const w = await screen(port, { context });
    const crop = w.get("[data-testid=visual-crop]");
    expect(crop.find("[data-testid=capture-shot]").exists()).toBe(true);
    expect(crop.find("[data-testid=capture-region]").exists()).toBe(true);
    expect(crop.get("img").attributes("alt")).toContain("outlined");
    expect(crop.text()).toContain("/checkout");
  });

  it("says so when the capture a visual finding names is not in the current builds any more", async () => {
    const { visual } = seeded();
    const context = createFakeContext({ captures: [capture("other", ["checkout.pay"])] });
    const w = await screen(createFakeQuality({ runs: [run], findings: { run1: [visual] } }), { context });
    expect(w.get("[data-testid=crop-gone]").text()).toContain("not in the current builds any more");
    expect(w.find("[data-testid=capture-shot]").exists()).toBe(false);
  });

  it("offers no waiver at all to a member who cannot write the catalog, and says why", async () => {
    const { term } = seeded();
    const w = await screen(createFakeQuality({ runs: [run], findings: { run1: [term] } }), { roles: ["translator"] });
    expect(w.get("[data-testid=quality-read-only]").text()).toContain("accepting one needs the developer role");
    expect(w.find("[data-testid=finding-waive]").exists()).toBe(false);
  });

  it("names the finding in the accept and revoke buttons, so the label is never just “Accept”", async () => {
    const { term } = seeded();
    const port = createFakeQuality({ runs: [run], findings: { run1: [term] } });
    const w = await screen(port);
    expect(button(w, /Accept this finding/).text()).toContain("term_forbidden on auth.login");
  });
});

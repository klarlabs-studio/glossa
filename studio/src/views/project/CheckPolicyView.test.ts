import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import type { PolicyDocument } from "../../api/policy-schemas";
import { createFakeCheckPolicy, policyState, type FakeCheckPolicy, type FakeTarget } from "../../test/fake-policy";
import { locale, mountProjectScreen } from "../../test/project";
import CheckPolicyView from "./CheckPolicyView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const LOCALES = [locale("en", true), locale("de"), locale("ja")];

const DOCUMENT: PolicyDocument = {
  schema: "glossa.check-policy/v1",
  require_complete: "listed",
  locales: ["de", "en"],
  fail_on: "error",
  missing_translations: "error",
  rules: [
    { layer: "source", severity: "off" },
    { layer: "visual", severity: "warning", mode: "warn" },
  ],
};

/** Two terminology findings on one open pull request: the shape §4.3 exists for. */
const TARGETS: FakeTarget[] = [
  {
    layer: "terminology",
    code: "term_forbidden",
    locale: "de",
    namespace: "legal",
    severity: "warning",
    ref: "feat/checkout",
    open: true,
    pullRequest: { number: 12, url: "https://github.com/acme/shop/pull/12" },
  },
  {
    layer: "terminology",
    code: "term_missing",
    locale: "de",
    severity: "warning",
    ref: "feat/checkout",
    open: true,
    pullRequest: { number: 12, url: "https://github.com/acme/shop/pull/12" },
  },
  // A branch with no pull request, which a policy change reddens all the same.
  { layer: "terminology", code: "term_missing", locale: "ja", severity: "warning", ref: "wip/glossary" },
  { layer: "length", code: "max-length-exceeded", locale: "ja", severity: "warning", ref: "main" },
];

async function screen(port: FakeCheckPolicy, roles?: Parameters<typeof mountProjectScreen>[1]["roles"]) {
  wrapper = await mountProjectScreen(CheckPolicyView, {
    checkPolicy: port,
    locales: LOCALES,
    path: "/t/t/p/p/quality/policy",
    ...(roles ? { roles } : {}),
  });
  await flushPromises();
  return wrapper;
}

const seeded = (over: Partial<Parameters<typeof createFakeCheckPolicy>[0]> = {}) =>
  createFakeCheckPolicy({
    state: policyState({ version: 3, document: DOCUMENT, created_by: "person:me", created_at: "2026-09-18T08:00:00Z" }),
    targets: TARGETS,
    ...over,
  });

const ruleCards = (w: VueWrapper) => w.findAll("[data-testid=policy-rule]");
const text = (w: VueWrapper, testid: string) => w.get(`[data-testid=${testid}]`).text();
const click = async (w: VueWrapper, testid: string) => {
  await w.get(`[data-testid=${testid}]`).trigger("click");
  await flushPromises();
};

describe("CheckPolicyView", () => {
  it("names the version that grades and whether it pins anything", async () => {
    const w = await screen(seeded());
    expect(text(w, "policy-version")).toContain("Version 3");
    expect(text(w, "policy-no-grace")).toContain("No pull request is pinned");
  });

  it("says the grace and the version it pins to, because that is how a stricter rule rolls out", async () => {
    const port = seeded();
    port.state.state = policyState({ version: 4, document: DOCUMENT, grace_until: "2026-10-03T08:00:00Z", pinned_version: 3 });
    const w = await screen(port);
    expect(text(w, "policy-grace")).toContain("keep grading against version 3");
  });

  it("shows a rule's position, its specificity and its rollout mode, in words", async () => {
    const w = await screen(seeded());
    const cards = ruleCards(w);
    expect(cards).toHaveLength(2);
    expect(cards[0]?.text()).toContain("Rule 1 of 2");
    expect(cards[0]?.get("[data-testid=rule-specificity]").text()).toBe("Names 1 field.");
    // `warn` is not a footnote: it is a control and a badge, both spelled out.
    expect(cards[1]?.get("[data-testid=rule-warn]").text()).toContain("Warn only");
    expect(cards[1]?.get("[data-testid=rule-warn-hint]").text()).toContain("cannot change any verdict");
  });

  it("says when a rule's position decides nothing, rather than implying every move matters", async () => {
    const w = await screen(seeded());
    expect(ruleCards(w)[0]?.get("[data-testid=rule-order-free]").text()).toContain("does not change any verdict");
  });

  it("names the later rule that wins a tie, and follows it when the rules are reordered", async () => {
    const port = seeded();
    port.state.state = policyState({
      version: 2,
      document: { ...DOCUMENT, rules: [{ layer: "visual", severity: "warning" }, { locale: "ja", severity: "off" }] },
    });
    const w = await screen(port);
    // Two rules, each naming one field, that can both match a Japanese visual finding.
    expect(ruleCards(w)[0]?.get("[data-testid=rule-beaten]").text()).toContain("rule 2 decides them instead");
    expect(ruleCards(w)[1]?.get("[data-testid=rule-beats]").text()).toContain("comes later");

    await ruleCards(w)[1]!.get("[data-testid=rule-up]").trigger("click");
    await flushPromises();
    // The move is announced, and the sentences swap with the rules.
    expect(text(w, "policy-status")).toBe("Moved from position 2 to position 1 of 2.");
    expect(ruleCards(w)[0]?.text()).toContain("Locale: ja");
    expect(ruleCards(w)[1]?.find("[data-testid=rule-beats]").exists()).toBe(true);
  });

  it("marks a rule an identical later selector shadows, which decides nothing at all", async () => {
    const port = seeded();
    port.state.state = policyState({
      version: 1,
      document: { ...DOCUMENT, rules: [{ layer: "source", severity: "off" }, { layer: "source", severity: "error" }] },
    });
    const w = await screen(port);
    expect(ruleCards(w)[0]?.get("[data-testid=rule-shadowed]").text()).toContain("decides nothing at all");
  });

  it("has no save button until the impact of this exact document has been previewed", async () => {
    const port = seeded();
    const w = await screen(port);
    await click(w, "policy-add-rule");
    expect(w.find("[data-testid=policy-save]").exists()).toBe(false);
    expect(text(w, "policy-preview-needed")).toContain("Preview the impact first");

    await click(w, "policy-preview");
    expect(w.find("[data-testid=policy-impact]").exists()).toBe(true);
    expect(w.find("[data-testid=policy-save]").exists()).toBe(true);
    // The preview is a dry run: nothing was stored.
    expect(port.calls.filter(([name]) => name === "save")).toHaveLength(1);
    expect(port.calls.at(-1)?.[2]).toMatchObject({ dryRun: true });
    expect(port.state.state.version).toBe(3);
  });

  it("answers the question the preview exists for: how many open pull requests would newly fail", async () => {
    const port = seeded();
    const w = await screen(port);
    // Make terminology an error everywhere: the classic way to redden other people's pull requests.
    await click(w, "policy-add-rule");
    const added = ruleCards(w).at(-1)!;
    await added.get("[data-testid=rule-layer]").setValue("terminology");
    await added.get("[data-testid=rule-severity-select]").setValue("error");
    await click(w, "policy-preview");
    expect(text(w, "impact-open-pull-requests")).toContain("1");
    expect(text(w, "impact-newly-failing")).toContain("3");
    expect(text(w, "impact-open-prs-hint")).toContain("would wake up to a red pull request");
    // And it names the pull request, as a link to it — not a branch to go and look up.
    const prs = w.findAll("[data-testid=impact-pull-request]");
    expect(prs).toHaveLength(1);
    expect(prs[0]?.text()).toBe("#12 feat/checkout");
    expect(prs[0]?.get("a").attributes("href")).toBe("https://github.com/acme/shop/pull/12");
    // The branch with no pull request is still named, apart from the ones that have one.
    expect(text(w, "impact-newly-failing-refs")).toBe("Other branches that would newly fail: wip/glossary");
    // A rule that changed nothing is listed too.
    expect(w.findAll("[data-testid=impact-rule]")).toHaveLength(3);
  });

  it("marks the preview stale and takes the save away again when the document changes under it", async () => {
    const w = await screen(seeded());
    await click(w, "policy-add-rule");
    await click(w, "policy-preview");
    expect(w.find("[data-testid=policy-save]").exists()).toBe(true);

    await w.get("[data-testid=policy-fail-on]").setValue("warning");
    await flushPromises();
    expect(w.find("[data-testid=policy-save]").exists()).toBe(false);
    expect(text(w, "impact-stale")).toContain("Preview it again before saving");
  });

  it("saves the document it previewed, with the grace, and shows the new version", async () => {
    const port = seeded();
    const w = await screen(port);
    await click(w, "policy-add-rule");
    await w.get("[data-testid=policy-grace-days]").setValue("30");
    await click(w, "policy-preview");
    await click(w, "policy-save");
    expect(text(w, "policy-status")).toBe("Version 4 is the policy now.");
    expect(port.state.state.version).toBe(4);
    const save = port.calls.filter(([name]) => name === "save").at(-1);
    expect(save?.[2]).toMatchObject({ graceDays: 30 });
    expect(save?.[2]).not.toMatchObject({ dryRun: true });
    expect(text(w, "policy-history")).toContain("4");
  });

  it("refuses to preview a rule that raises the model-decided layer to an error, before the server does", async () => {
    const port = seeded();
    const w = await screen(port);
    await click(w, "policy-add-rule");
    const added = ruleCards(w).at(-1)!;
    await added.get("[data-testid=rule-layer]").setValue("linguistic");
    await added.get("[data-testid=rule-severity-select]").setValue("error");
    await flushPromises();
    expect(added.get("[data-testid=rule-advisory]").text()).toContain("never fails on an opinion");
    expect((w.get("[data-testid=policy-preview]").element as HTMLButtonElement).disabled).toBe(true);
    expect(port.calls.filter(([name]) => name === "save")).toHaveLength(0);
  });

  it("discards changes back to the policy that grades", async () => {
    const w = await screen(seeded());
    await click(w, "policy-add-rule");
    expect(ruleCards(w)).toHaveLength(3);
    await click(w, "policy-discard");
    expect(ruleCards(w)).toHaveLength(2);
    expect(w.find("[data-testid=policy-dirty]").exists()).toBe(false);
  });

  it("loads a past version into the editor without saving it", async () => {
    const port = seeded();
    port.state.versions = [
      { version: 3, document: DOCUMENT, created_by: "person:me", created_at: "2026-09-18T08:00:00Z" },
      { version: 2, document: { require_complete: "all", fail_on: "warning", missing_translations: "warning" }, created_by: "person:me", created_at: "2026-09-10T08:00:00Z" },
    ];
    const w = await screen(port);
    await w.findAll("[data-testid=policy-load-version]").at(1)!.trigger("click");
    await flushPromises();
    expect(text(w, "policy-status")).toContain("Version 2 is loaded in the editor");
    expect(ruleCards(w)).toHaveLength(0);
    expect(port.state.state.version).toBe(3);
  });

  it("is read-only without the developer role: every control is disabled and there is nothing to save", async () => {
    const w = await screen(seeded(), ["translator"]);
    expect(text(w, "policy-read-only")).toContain("needs the developer role");
    expect((w.get("[data-testid=policy-fail-on]").element as HTMLSelectElement).disabled).toBe(true);
    expect(w.find("[data-testid=policy-preview]").exists()).toBe(false);
    expect(w.find("[data-testid=policy-save]").exists()).toBe(false);
  });
});

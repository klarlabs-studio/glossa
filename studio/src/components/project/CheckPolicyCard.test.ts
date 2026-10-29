import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CheckPolicy, Project } from "../../api/schemas";
import { demoProject, locale, mountProjectScreen } from "../../test/project";
import CheckPolicyCard from "./CheckPolicyCard.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const locales = [locale("en", true), locale("de"), locale("ja")];
const DEFAULT: CheckPolicy = { require_complete: "all", locales: [], fail_on: "error", missing_translations: "error" };

const withPolicy = (policy: CheckPolicy = DEFAULT): Project => ({
  ...demoProject,
  settings: { ...demoProject.settings, check_policy: policy },
});

async function card(policy?: CheckPolicy): Promise<VueWrapper> {
  return (wrapper = await mountProjectScreen(CheckPolicyCard, {
    locales,
    roles: ["developer"],
    project: withPolicy(policy),
    etag: '"1"',
    path: "/t/t/p/p/settings",
  }));
}

/** Captures the PATCH the card sends and answers with the saved project. */
function captureSave(saved: Project): { bodies: unknown[] } {
  const bodies: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      bodies.push(await req.clone().json());
      return new Response(JSON.stringify(saved), {
        status: 200,
        headers: { "Content-Type": "application/json", ETag: '"2"' },
      });
    }),
  );
  return { bodies };
}

describe("CheckPolicyCard", () => {
  it("says what the settings mean for a pull request, and keeps saying it as they change", async () => {
    const w = await card();
    expect(w.text()).toContain("A pull request fails when the check finds an error.");
    expect(w.text()).toContain("A new key with no translation in any locale is an error.");

    await w.get("#cp-missing").setValue("warning");
    expect(w.text()).toContain("A new key with no translation in any locale is a warning.");

    await w.get("#cp-fail-on").setValue("warning");
    expect(w.text()).toContain("A pull request fails when the check finds an error or a warning.");

    await w.get("#cp-fail-on").setValue("never");
    expect(w.text()).toContain("A pull request never fails this check.");

    await w.get("#cp-require").setValue("none");
    expect(w.text()).toContain("A new key with no translation is only ever a warning, in every locale.");
  });

  it("offers the target locales to pick, and names the picked ones in the sentence", async () => {
    const w = await card();
    // Only with require_complete: listed, and never the source locale.
    expect(w.findAll("fieldset input[type=checkbox]")).toHaveLength(0);
    await w.get("#cp-require").setValue("listed");
    const boxes = w.findAll("fieldset input[type=checkbox]");
    expect(boxes.map((b) => b.attributes("value"))).toEqual(["de", "ja"]);
    // Nothing picked yet is nothing required, and the card says so.
    expect(w.text()).toContain("Pick at least one locale");
    expect(w.text()).toContain("A new key with no translation is only ever a warning");

    await boxes[0]!.setValue(true);
    expect(w.text()).toContain("A new key with no translation in de is an error; in any other locale it is a warning.");
  });

  it("saves the picked locales with the rest of the settings, and keeps the new ETag", async () => {
    const w = await card();
    await w.get("#cp-require").setValue("listed");
    await w.findAll("fieldset input[type=checkbox]")[1]!.setValue(true);
    await w.get("#cp-missing").setValue("warning");

    const policy: CheckPolicy = { require_complete: "listed", locales: ["ja"], fail_on: "error", missing_translations: "warning" };
    const { bodies } = captureSave(withPolicy(policy));
    await w.get("form").trigger("submit");
    await flushPromises();

    expect(bodies).toEqual([
      { settings: { default_syntax: "mf1", review_required: true, check_policy: policy } },
    ]);
    expect(w.text()).toContain("Check policy saved.");
  });

  it("sends an empty pick as none, because that is what it means", async () => {
    const w = await card();
    await w.get("#cp-require").setValue("listed");
    const { bodies } = captureSave(withPolicy({ ...DEFAULT, require_complete: "none" }));
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(bodies).toEqual([
      {
        settings: {
          default_syntax: "mf1",
          review_required: true,
          check_policy: { require_complete: "none", locales: [], fail_on: "error", missing_translations: "error" },
        },
      },
    ]);
  });

  it("shows a project's stored policy, not the default", async () => {
    const w = await card({ require_complete: "listed", locales: ["ja"], fail_on: "never", missing_translations: "warning" });
    expect((w.get("#cp-require").element as HTMLSelectElement).value).toBe("listed");
    expect((w.get("#cp-fail-on").element as HTMLSelectElement).value).toBe("never");
    expect((w.get("#cp-missing").element as HTMLSelectElement).value).toBe("warning");
    const checked = w.findAll("fieldset input[type=checkbox]").filter((b) => (b.element as HTMLInputElement).checked);
    expect(checked.map((b) => b.attributes("value"))).toEqual(["ja"]);
  });

  it("is read-only without catalog.write", async () => {
    wrapper = await mountProjectScreen(CheckPolicyCard, {
      locales,
      roles: ["translator"],
      project: withPolicy(),
      etag: '"1"',
      path: "/t/t/p/p/settings",
    });
    expect(wrapper.find("button[type=submit]").exists()).toBe(false);
    expect(wrapper.get("#cp-require").attributes("disabled")).toBeDefined();
  });
});

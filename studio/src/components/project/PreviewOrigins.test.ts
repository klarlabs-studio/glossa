import { flushPromises } from "@vue/test-utils";
import { beforeEach, describe, expect, it } from "vitest";
import { fakeInContext, previewOrigin, type FakeInContext } from "../../test/fake-in-context";
import { mountProjectScreen } from "../../test/project";
import PreviewOrigins from "./PreviewOrigins.vue";

/**
 * Project settings → In-product editing (RFC 0004 §5.2). Registering an
 * origin is a permission decision, so the screen is about being clear
 * what is on the list, who may change it, and what removing one does.
 */
describe("PreviewOrigins", () => {
  let port: FakeInContext;

  beforeEach(() => {
    port = fakeInContext();
  });

  const mount = (roles: Parameters<typeof mountProjectScreen>[1]["roles"] = ["developer"]) =>
    mountProjectScreen(PreviewOrigins, { inContext: port, roles, path: "/t/t/p/p/settings" });

  it("lists the registered origins and marks a developer's own machine", async () => {
    port.originList = [
      previewOrigin(),
      previewOrigin({ id: "po-2", origin: "http://localhost:5173", label: "my machine", development: true }),
    ];
    const w = await mount();

    const rows = w.findAll('[data-testid="preview-origins"] tbody tr');
    expect(rows).toHaveLength(2);
    // Sorted by origin, as the server returns them.
    expect(rows[0]!.text()).toContain("http://localhost:5173");
    expect(rows[0]!.text()).toContain("Development");
    expect(rows[1]!.text()).toContain("https://preview.example.com");
    expect(rows[1]!.text()).not.toContain("Development");
  });

  it("says the editor cannot run anywhere when the list is empty", async () => {
    const w = await mount();
    expect(w.find('[data-testid="preview-origins"]').exists()).toBe(false);
    expect(w.text()).toContain("cannot run anywhere");
  });

  it("registers an origin and clears the form", async () => {
    const w = await mount();
    await w.find("#po-origin").setValue("https://preview.example.com");
    await w.find("#po-label").setValue("shared preview");
    await w.find("form").trigger("submit");
    await flushPromises();

    expect(port.originList.map((o) => o.origin)).toEqual(["https://preview.example.com"]);
    expect(port.calls.some(([name]) => name === "register")).toBe(true);
    expect((w.find("#po-origin").element as HTMLInputElement).value).toBe("");
    expect(w.text()).toContain("may now run on https://preview.example.com");
  });

  it("shows the server's words when an origin is refused", async () => {
    const w = await mount();
    await w.find("#po-origin").setValue("http://preview.example.com");
    await w.find("form").trigger("submit");
    await flushPromises();

    expect(port.originList).toHaveLength(0);
    expect(w.find('[role="alert"]').text()).toContain("scheme, host and port");
  });

  it("confirms before removing, and says the sessions end with it", async () => {
    port.originList = [previewOrigin()];
    const w = await mount();
    // A grant is out on that origin.
    await port.mint("t", "p", "https://preview.example.com");
    expect(port.minted).toHaveLength(1);

    await w.find('[data-testid="preview-origins"] .btn-danger').trigger("click");
    await flushPromises();
    const dialog = w.find("dialog");
    expect(dialog.text()).toContain("straight away");

    const confirm = dialog.findAll("button").find((b) => b.text() === "Remove")!;
    await confirm.trigger("click");
    await flushPromises();

    expect(port.originList).toHaveLength(0);
    // Removing the origin ended the editor session on it.
    expect(port.minted).toHaveLength(0);
    expect(w.text()).toContain("no longer runs on");
  });

  it("shows the list to a translator but offers no way to change it", async () => {
    port.originList = [previewOrigin()];
    const w = await mount(["translator"]);

    expect(w.find('[data-testid="preview-origins"]').exists()).toBe(true);
    expect(w.find("form").exists()).toBe(false);
    expect(w.findAll(".btn-danger")).toHaveLength(0);
    expect(w.text()).toContain("permission to manage tokens");
  });
});

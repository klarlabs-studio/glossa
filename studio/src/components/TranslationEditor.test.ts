import { flushPromises, mount } from "@vue/test-utils";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/errors";
import type { Message, Project, ProjectLocale, Translation } from "../api/schemas";
import { grantFor } from "../session/permissions";
import { loadFormatter, MF1_PREVIEW_DELAY_MS, MF1_PREVIEW_RETRY_MS } from "../lib/preview";
import TranslationEditor from "./TranslationEditor.vue";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  review: vi.fn(),
  revisions: vi.fn(),
  sourceRevisions: vi.fn(),
  preview: vi.fn(),
}));
vi.mock("../api/endpoints", () => ({
  translations: { get: api.get, put: api.put, review: api.review, revisions: api.revisions },
  messages: { sourceRevisions: api.sourceRevisions },
  preview: { message: api.preview },
}));

/** Past the MF1 preview's debounce. */
const debounce = () => new Promise((r) => setTimeout(r, MF1_PREVIEW_DELAY_MS + 30));
const hallo = (name: string) => ({
  type: "message",
  declarations: [],
  pattern: ["Hallo, ", { type: "expression", arg: { type: "variable", name } }, "!"],
});

const now = "2026-09-19T12:00:00Z";
const project: Project = {
  id: "p",
  slug: "demo",
  name: "Demo",
  source_locale: "en",
  settings: { default_syntax: "mf1", review_required: true },
  created_at: now,
  updated_at: now,
};
const message: Message = {
  id: "m1",
  key: "greeting",
  namespace: "default",
  description: "Dashboard welcome",
  state: "active",
  source: {
    text: "Hello, {name}!",
    syntax: "mf1",
    model: { type: "message", declarations: [], pattern: ["Hello, ", { type: "expression", arg: { type: "variable", name: "name" } }, "!"] },
    arguments: [{ name: "name", type: "string" }],
    markup: [],
  },
  source_revision: 2,
  created_at: now,
  updated_at: now,
};
const de: ProjectLocale = { code: "de", direction: "ltr", is_source: false, created_at: now };
const en: ProjectLocale = { code: "en", direction: "ltr", is_source: true, created_at: now };

const translation = (over: Partial<Translation> = {}): Translation => ({
  id: "t1",
  message_id: "m1",
  locale: "de",
  text: "Hallo, {name}!",
  syntax: "mf1",
  model: { type: "message", declarations: [], pattern: ["Hallo, ", { type: "expression", arg: { type: "variable", name: "name" } }, "!"] },
  state: "needs_review",
  origin: "human",
  author: "person:me",
  source_revision: 2,
  current_source_revision: 2,
  outdated: false,
  warnings: [],
  revision: 1,
  created_at: now,
  updated_at: now,
  ...over,
});

const mountEditor = (roles: Array<"owner" | "translator"> = ["owner"], locales: string[] = []) =>
  mount(TranslationEditor, {
    props: { tenant: "t", project, message, locale: de, source: en, grant: grantFor({ roles, locales }) },
    attachTo: document.body,
  });

// The preview panel imports the formatter on demand. Load it first, so a
// preview assertion never races that import (slow on a cold, busy run).
beforeAll(() => loadFormatter());

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset();
});

describe("TranslationEditor", () => {
  it("shows the source, its arguments and the developers' context", async () => {
    api.get.mockResolvedValue(null);
    const w = mountEditor();
    await flushPromises();
    expect(w.text()).toContain("Dashboard welcome");
    expect(w.get(".source-text").text()).toBe("Hello, {name}!");
    expect(w.get(".chips").text()).toContain("$name");
    const area = w.get("textarea");
    expect(area.attributes()).toMatchObject({ lang: "de", dir: "ltr" });
    expect(w.text()).not.toContain("Proposed");
    w.unmount();
  });

  it("marks a message proposed on a branch and still edits it", async () => {
    api.get.mockResolvedValue(null);
    const w = mount(TranslationEditor, {
      props: {
        tenant: "t",
        project,
        message: { ...message, state: "proposed" },
        locale: de,
        source: en,
        grant: grantFor({ roles: ["owner"], locales: [] }),
      },
      attachTo: document.body,
    });
    await flushPromises();
    const pill = w.findAll(".pill").find((p) => p.text() === "Proposed");
    expect(pill?.attributes("title")).toContain("open branch");
    expect(w.find("textarea").exists()).toBe(true);
    w.unmount();
  });

  it("shows structural QA findings inline and marks the field invalid", async () => {
    api.get.mockResolvedValue(null);
    const findings = [
      { code: "missing-argument", severity: "error" as const, subject: "name", message: "the translation does not use {$name}" },
      { code: "extra-argument", severity: "error" as const, subject: "nam", message: "the translation uses {$nam}" },
    ];
    api.put.mockRejectedValue(
      new ApiError(422, "structural_qa_failed", "structurally incompatible", { type: "x", title: "t", status: 422, code: "structural_qa_failed", findings }),
    );
    const w = mountEditor();
    await flushPromises();
    await w.get("textarea").setValue("Hallo, {nam}!");
    await (w.vm as unknown as { save: (a?: boolean) => Promise<void> }).save();
    await flushPromises();
    const qa = w.get("[data-testid=qa-findings]");
    expect(qa.attributes("role")).toBe("alert");
    expect(qa.text()).toContain("missing-argument");
    expect(qa.text()).toContain("extra-argument");
    expect(w.get("textarea").attributes("aria-invalid")).toBe("true");
    expect(w.get("textarea").attributes("aria-describedby")).toContain("qa-errors");
    w.unmount();
  });

  it("saves with the ETag, and save+approve asks for approval", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    api.put.mockResolvedValue({ value: translation({ text: "Hallo {name}!", state: "approved", revision: 2 }), etag: '"e2"' });
    const w = mountEditor();
    await flushPromises();
    await w.get("textarea").setValue("Hallo {name}!");
    await (w.vm as unknown as { save: (a?: boolean) => Promise<void> }).save(true);
    await flushPromises();
    expect(api.put).toHaveBeenCalledWith(
      { tenant: "t", project: "p", message: "greeting", locale: "de" },
      { text: "Hallo {name}!", syntax: "mf1", state: "approved" },
      '"e1"',
    );
    expect(w.get("[data-testid=editor-status]").text()).toBe("Approved.");
    expect(w.get("[data-testid=translation-state]").text()).toBe("Approved");
    expect(w.emitted("changed")).toHaveLength(1);
    w.unmount();
  });

  it("approves an unchanged translation through the review endpoint", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    api.review.mockResolvedValue({ value: translation({ state: "approved" }), etag: '"e2"' });
    const w = mountEditor();
    await flushPromises();
    await (w.vm as unknown as { save: (a?: boolean) => Promise<void> }).save(true);
    await flushPromises();
    expect(api.put).not.toHaveBeenCalled();
    expect(api.review).toHaveBeenCalledWith(expect.anything(), "approved", '"e1"');
    w.unmount();
  });

  it("withholds approve and reject from the author of the text, and explains why", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    const w = mount(TranslationEditor, {
      props: { tenant: "t", project, message, locale: de, source: en, grant: grantFor({ roles: ["owner"], locales: [] }), selfId: "me" },
      attachTo: document.body,
    });
    await flushPromises();
    const labels = w.findAll("button").map((b) => b.text());
    expect(labels).not.toContain("Approve");
    expect(labels).not.toContain("Reject");
    expect(w.get("[data-testid=own-text-hint]").text()).toContain("Ask another reviewer");
    await (w.vm as unknown as { save: (a?: boolean) => Promise<void> }).save(true);
    await flushPromises();
    expect(api.review).not.toHaveBeenCalled();
    w.unmount();
  });

  it("lets a solo author approve their own text when the server says no one else could review", async () => {
    api.get.mockResolvedValue({ value: translation({ review_by_author_allowed: true }), etag: '"e1"' });
    api.review.mockResolvedValue({ value: translation({ state: "approved", revision: 2, review_by_author_allowed: true }), etag: '"e2"' });
    const w = mount(TranslationEditor, {
      props: { tenant: "t", project, message, locale: de, source: en, grant: grantFor({ roles: ["owner"], locales: [] }), selfId: "me" },
      attachTo: document.body,
    });
    await flushPromises();
    expect(w.findAll("button").map((b) => b.text())).toContain("Approve");
    expect(w.find("[data-testid=own-text-hint]").exists()).toBe(false);
    await w.findAll("button").find((b) => b.text() === "Approve")?.trigger("click");
    await flushPromises();
    expect(api.review).toHaveBeenCalledWith(expect.anything(), "approved", '"e1"');
    w.unmount();
  });

  it("offers Save & approve only while self-approval is allowed", async () => {
    for (const [allowed, offered] of [[false, false], [true, true]] as const) {
      api.get.mockResolvedValue({ value: translation({ review_by_author_allowed: allowed }), etag: '"e1"' });
      const w = mount(TranslationEditor, {
        props: { tenant: "t", project, message, locale: de, source: en, grant: grantFor({ roles: ["owner"], locales: [] }), selfId: "me" },
        attachTo: document.body,
      });
      await flushPromises();
      expect(w.findAll("button").some((b) => b.text().startsWith("Save & approve"))).toBe(offered);
      w.unmount();
    }
  });

  it("says so when a Save & approve landed as needs review", async () => {
    api.get.mockResolvedValue(null);
    api.put.mockResolvedValue({ value: translation({ state: "needs_review" }), etag: '"e1"' });
    const w = mountEditor();
    await flushPromises();
    (w.vm as unknown as { setDraft: (t: string, s: "mf1") => void }).setDraft("Hallo, {name}!", "mf1");
    await (w.vm as unknown as { save: (a?: boolean) => Promise<void> }).save(true);
    await flushPromises();
    expect(w.get("[data-testid=editor-status]").text()).toContain("Another reviewer has to approve it");
    w.unmount();
  });

  it("keeps imported text reviewable by the person who imported it", async () => {
    api.get.mockResolvedValue({ value: translation({ origin: "import" }), etag: '"e1"' });
    const w = mount(TranslationEditor, {
      props: { tenant: "t", project, message, locale: de, source: en, grant: grantFor({ roles: ["owner"], locales: [] }), selfId: "me" },
      attachTo: document.body,
    });
    await flushPromises();
    expect(w.findAll("button").map((b) => b.text())).toContain("Approve");
    expect(w.find("[data-testid=own-text-hint]").exists()).toBe(false);
    w.unmount();
  });

  it("hides review actions from translators and keeps them in their locales", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    const w = mountEditor(["translator"], ["fr"]);
    await flushPromises();
    expect(w.find("textarea").attributes("readonly")).toBeDefined();
    expect(w.findAll("button").map((b) => b.text())).not.toContain("Approve");
    expect(w.text()).toContain("You can't write de translations");
    w.unmount();
  });

  it("previews MF1 as you type, through the server's kernel", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    api.preview.mockResolvedValue({ valid: true, message: hallo("name"), arguments: [{ name: "name", type: "string" }], markup: [], errors: [] });
    const w = mountEditor();
    await flushPromises();
    // The saved text needs no request: its model came with the translation.
    await debounce();
    expect(api.preview).not.toHaveBeenCalled();
    await w.get("textarea").setValue("Servus, {name}");
    await w.get("textarea").setValue("Servus, {name}!");
    await debounce();
    await flushPromises();
    // Debounced: one request for the last text.
    expect(api.preview).toHaveBeenCalledTimes(1);
    expect(api.preview.mock.calls[0]![0]).toEqual({ source: "Servus, {name}!", syntax: "mf1", locale: "de" });
    expect(w.get("[data-testid=preview-target]").text()).toBe("Hallo, Ada!");
    expect(w.text()).not.toContain("the preview shows the last saved text");
    w.unmount();
  });

  it("shows the kernel's errors inline for text that doesn't parse, before saving", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    api.preview.mockResolvedValue({
      valid: false,
      arguments: [],
      markup: [],
      errors: [{ stage: "parse", code: "mf1-syntax-error", message: "unclosed placeholder at 7" }],
    });
    const w = mountEditor();
    await flushPromises();
    await w.get("textarea").setValue("Hallo, {name");
    await debounce();
    await flushPromises();
    const errors = w.get("[data-testid=preview-errors]");
    expect(errors.text()).toContain("mf1-syntax-error");
    expect(errors.text()).toContain("unclosed placeholder at 7");
    expect(w.get("textarea").attributes("aria-describedby")).toContain("preview-errors");
    expect(w.get("[data-testid=preview-target]").text()).toBe("Nothing to preview yet.");
    expect(api.put).not.toHaveBeenCalled();
    w.unmount();
  });

  it("pauses the preview when rate limited, then retries", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    api.preview
      .mockRejectedValueOnce(new ApiError(429, "rate_limited", "slow down", { type: "x", title: "t", status: 429, code: "rate_limited" }))
      .mockResolvedValue({ valid: true, message: hallo("name"), arguments: [], markup: [], errors: [] });
    const w = mountEditor();
    await flushPromises();
    await w.get("textarea").setValue("Servus, {name}!");
    await debounce();
    await flushPromises();
    expect(w.text()).toContain("Preview paused");
    await new Promise((r) => setTimeout(r, MF1_PREVIEW_RETRY_MS + 50));
    await flushPromises();
    expect(api.preview).toHaveBeenCalledTimes(2);
    expect(w.text()).not.toContain("Preview paused");
    expect(w.get("[data-testid=preview-target]").text()).toBe("Hallo, Ada!");
    w.unmount();
  });

  it("flags an outdated translation and diffs the source it was made against", async () => {
    api.get.mockResolvedValue({ value: translation({ outdated: true, source_revision: 1 }), etag: '"e1"' });
    api.sourceRevisions.mockResolvedValue([
      { revision: 2, text: "Hello, {name}!", syntax: "mf1", model: message.source.model, author: "person:x", created_at: now },
      { revision: 1, text: "Hi {name}!", syntax: "mf1", model: message.source.model, author: "person:x", created_at: now },
    ]);
    const w = mountEditor();
    await flushPromises();
    expect(w.text()).toContain("Translated against source revision 1; the source is now at revision 2.");
    await w.findAll("button").find((b) => b.text() === "Show source changes")!.trigger("click");
    await flushPromises();
    expect(w.find("del").text()).toBe("Hi");
    expect(w.find("ins").text()).toBe("Hello,");
    w.unmount();
  });

  it("highlights recognized terms in the source and lists terminology findings on the draft", async () => {
    api.get.mockResolvedValue(null);
    const term = { id: "t1", locale: "en", text: "Hello", status: "preferred" as const, case_sensitive: false };
    const w = mount(TranslationEditor, {
      props: {
        tenant: "t",
        project,
        message,
        locale: de,
        source: en,
        grant: grantFor({ roles: ["owner"], locales: [] }),
        termHits: { analyzed_text: "Hello, ￼!", hits: [{ concept_id: "c1", definition: "A greeting", term, start: 0, end: 5, text: "Hello" }] },
        termFindings: [
          { code: "term_forbidden" as const, severity: "error" as const, concept_id: "c1", term_id: "t2", side: "target" as const, start: 0, end: 4, text: "Moin", suggestions: ["Hallo"], message: "x" },
        ],
      },
      attachTo: document.body,
    });
    await flushPromises();
    const mark = w.get("[data-testid=term-highlight]");
    expect(mark.text()).toBe("Hello");
    expect(mark.attributes("title")).toBe("A greeting");
    expect(w.get("[data-testid=source-text]").text()).toBe("Hello, {name}!");
    const findings = w.get("[data-testid=term-findings]");
    expect(findings.text()).toContain("“Moin” is a term to avoid here.");
    expect(findings.text()).toContain("Use: Hallo");
    expect(w.get("textarea").attributes("aria-describedby")).toContain("term-findings");
    w.unmount();
  });

  it("takes a TM match or suggestion as MF2 and reports the draft as it changes", async () => {
    api.get.mockResolvedValue({ value: translation(), etag: '"e1"' });
    const w = mountEditor();
    await flushPromises();
    (w.vm as unknown as { setDraft: (t: string, s: "mf2") => void }).setDraft("Hallo {$name}!", "mf2");
    await flushPromises();
    expect((w.get("textarea").element as HTMLTextAreaElement).value).toBe("Hallo {$name}!");
    expect((w.get("#target-syntax").element as HTMLSelectElement).value).toBe("mf2");
    expect(document.activeElement).toBe(w.get("textarea").element);
    expect(w.emitted("draft")!.at(-1)).toEqual([{ text: "Hallo {$name}!", syntax: "mf2" }]);
    w.unmount();
  });
});

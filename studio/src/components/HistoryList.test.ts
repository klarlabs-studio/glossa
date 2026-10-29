import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import type { TranslationRevision } from "../api/schemas";
import HistoryList from "./HistoryList.vue";

const revision = (over: Partial<TranslationRevision>): TranslationRevision => ({
  revision: 1,
  kind: "content",
  text: "Hallo",
  syntax: "mf1",
  state: "needs_review",
  origin: "human",
  origin_detail: {},
  author: "person:me",
  source_revision: 1,
  findings: [],
  self_review: false,
  created_at: "2026-10-08T08:00:00Z",
  ...over,
});

describe("HistoryList", () => {
  it("marks a review the author made on their own text as self-approved", () => {
    const w = mount(HistoryList, {
      props: {
        revisions: [revision({ revision: 2, kind: "review", state: "approved", self_review: true }), revision({})],
        lang: "de",
        dir: "ltr",
        selfId: "me",
      },
    });
    const marks = w.findAll("[data-testid=self-review]");
    expect(marks).toHaveLength(1);
    expect(marks[0]?.text()).toBe("self-approved");
  });
});

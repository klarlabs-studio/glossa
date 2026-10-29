import { describe, expect, it } from "vitest";
import { isOwnText, offersSaveApprove, reviewActions, stateTone } from "./review";

describe("stateTone", () => {
  it("maps review states to tones", () => {
    expect(stateTone("approved")).toBe("ok");
    expect(stateTone("needs_review")).toBe("warn");
    expect(stateTone("rejected")).toBe("err");
    expect(stateTone("draft")).toBe("neutral");
  });
});

describe("reviewActions", () => {
  it("offers review only to reviewers", () => {
    expect(reviewActions("needs_review", true, false)).toEqual([]);
    expect(reviewActions("needs_review", true, true)).toEqual(["approve", "reject"]);
    expect(reviewActions("approved", true, true)).toEqual(["reject"]);
    expect(reviewActions("rejected", true, true)).toEqual(["approve", "request"]);
  });
  it("lets writers send drafts to review", () => {
    expect(reviewActions("draft", true, false)).toEqual(["request"]);
    expect(reviewActions(undefined, true, true)).toEqual([]);
    expect(reviewActions("needs_review", true, true, true)).toEqual([]);
    expect(reviewActions("rejected", true, true, true)).toEqual(["request"]);
  });
});

describe("isOwnText", () => {
  it("is true only for the person who wrote non-imported text", () => {
    expect(isOwnText({ author: "person:me", origin: "human" }, "me")).toBe(true);
    expect(isOwnText({ author: "person:x", origin: "human" }, "me")).toBe(false);
    expect(isOwnText({ author: "token:me", origin: "ai" }, "me")).toBe(false);
    expect(isOwnText({ author: "person:me", origin: "import" }, "me")).toBe(false);
    expect(isOwnText({ author: "person:me", origin: "human" }, undefined)).toBe(false);
    expect(isOwnText(null, "me")).toBe(false);
  });
  it("lets the author review when the server says no one else could", () => {
    expect(isOwnText({ author: "person:me", origin: "human", review_by_author_allowed: true }, "me")).toBe(false);
    expect(isOwnText({ author: "person:me", origin: "human", review_by_author_allowed: false }, "me")).toBe(true);
  });
});

describe("offersSaveApprove", () => {
  it("is offered to reviewers unless the server said someone else must approve", () => {
    expect(offersSaveApprove(false, null)).toBe(false);
    expect(offersSaveApprove(true, null)).toBe(true);
    expect(offersSaveApprove(true, {})).toBe(true);
    expect(offersSaveApprove(true, { review_by_author_allowed: true })).toBe(true);
    expect(offersSaveApprove(true, { review_by_author_allowed: false })).toBe(false);
  });
});

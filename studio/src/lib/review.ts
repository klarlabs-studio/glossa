import type { ReviewState } from "../api/schemas";

/** Colour tone of a review state (pill-ok / pill-warn / …). */
export function stateTone(state: ReviewState): "ok" | "warn" | "err" | "neutral" {
  return { approved: "ok", needs_review: "warn", rejected: "err", draft: "neutral" }[state] as "ok" | "warn" | "err" | "neutral";
}

/**
 * Whether `self` wrote the current text and may therefore not approve or
 * reject it: an author never approves their own work when someone else
 * could review it (RFC 0006 §15 Q6). The server says when no one else
 * could (`review_by_author_allowed`), and then the author may; anything
 * else is treated as not allowed. Imported text is written by the
 * importer, not authored, so it stays reviewable.
 */
export function isOwnText(
  translation: { author: string; origin: string; review_by_author_allowed?: boolean | undefined } | null | undefined,
  selfId: string | undefined,
): boolean {
  if (!translation || !selfId || translation.origin === "import") return false;
  if (translation.review_by_author_allowed === true) return false;
  return translation.author === `person:${selfId}`;
}

/**
 * Whether "Save & approve" is offered: not when the server said that
 * someone else must approve (the write would land as needs_review). With
 * no translation yet there is nothing to ask, so it is offered and the
 * response says where the text landed.
 */
export function offersSaveApprove(canReview: boolean, translation: { review_by_author_allowed?: boolean | undefined } | null | undefined): boolean {
  return canReview && translation?.review_by_author_allowed !== false;
}

/** Review actions a person may take on a translation in `state`; `ownText` withholds approve and reject. */
export function reviewActions(state: ReviewState | undefined, canWrite: boolean, canReview: boolean, ownText = false): Array<"approve" | "reject" | "request"> {
  if (!state) return [];
  const out: Array<"approve" | "reject" | "request"> = [];
  if (canReview && !ownText && state !== "approved") out.push("approve");
  if (canReview && !ownText && state !== "rejected") out.push("reject");
  if (canWrite && (state === "draft" || state === "rejected")) out.push("request");
  return out;
}

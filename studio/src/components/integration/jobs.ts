/** How the import/export screens show a job: its state's tone and why it failed. */
import type { IntegrationJobState } from "../../api/integration-schemas";
import { strings } from "../../strings";

const s = strings.integration;

export function stateTone(state: IntegrationJobState): string {
  switch (state) {
    case "succeeded":
      return "pill-ok";
    case "failed":
      return "pill-err";
    case "running":
    case "queued":
    case "awaiting_upload":
      return "pill-accent";
    default:
      return "pill-neutral";
  }
}

/** Why a job failed, in words, with the server's detail when it has one. */
export function failureText(code: string | undefined, message: string | undefined): string {
  const known = code ? s.failures[code] : undefined;
  if (known && message) return `${known} (${message})`;
  return known ?? message ?? code ?? s.failures.internal!;
}

/** Why a result is a conflict or invalid. */
export const resultWhy = (code: string | undefined, detail: string | undefined): string => {
  const known = code ? s.codes[code] : undefined;
  if (known && detail && detail !== known) return `${known} ${detail}`;
  return known ?? detail ?? code ?? "";
};

export const resultTone: Record<string, string> = {
  created: "pill-ok",
  updated: "pill-accent",
  unchanged: "pill-neutral",
  conflict: "pill-warn",
  invalid: "pill-err",
};

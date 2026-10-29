/** Pure helpers for the translator workspace's message list. */
import type { Message, ProjectTranslation } from "../api/schemas";

export type RowStatus = "missing" | "outdated" | "translated" | "unknown";

export interface MessageRow {
  id: string;
  key: string;
  text: string;
  namespace: string;
  status: RowStatus;
}

export type CoverageFilter = "all" | "missing" | "outdated";

/** Which messages have a usable translation in a locale, and which of those are outdated, by message ID. */
export interface Coverage {
  translated: Set<string>;
  outdated: Set<string>;
}

/** Coverage from the locale's translations (one bulk listing): rejected text counts as missing. */
export function coverageOf(translations: Iterable<Pick<ProjectTranslation, "message_id" | "state" | "outdated">>): Coverage {
  const c: Coverage = { translated: new Set(), outdated: new Set() };
  for (const t of translations) {
    if (t.state === "rejected") continue;
    c.translated.add(t.message_id);
    if (t.outdated) c.outdated.add(t.message_id);
  }
  return c;
}

/** Status of a message in the target locale (unknown until coverage loads). */
export function statusOf(messageId: string, coverage: Coverage | undefined): RowStatus {
  if (!coverage) return "unknown";
  if (!coverage.translated.has(messageId)) return "missing";
  if (coverage.outdated.has(messageId)) return "outdated";
  return "translated";
}

/** Case-insensitive search over key, source text and the developers' context. */
export function matches(m: Pick<Message, "key" | "description" | "source">, query: string): boolean {
  const q = query.trim().toLocaleLowerCase();
  if (q === "") return true;
  return (
    m.key.toLocaleLowerCase().includes(q) ||
    m.source.text.toLocaleLowerCase().includes(q) ||
    m.description.toLocaleLowerCase().includes(q)
  );
}

/** Namespaces seen so far, sorted, `default` first. */
export function namespacesOf(messages: readonly Pick<Message, "namespace">[], extra: readonly string[] = []): string[] {
  const set = new Set([...messages.map((m) => m.namespace), ...extra.filter(Boolean)]);
  return [...set].sort((a, b) => (a === "default" ? -1 : b === "default" ? 1 : a.localeCompare(b)));
}

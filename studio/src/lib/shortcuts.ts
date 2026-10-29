/**
 * Studio's keyboard map. This list is the single source of truth: the
 * `?` sheet renders it, the handlers match against it, and a test keeps
 * studio/README.md in step with it.
 */
import { onBeforeUnmount, onMounted } from "vue";

export type ShortcutId =
  | "help"
  | "search"
  | "next"
  | "prev"
  | "edit"
  | "leave"
  | "save"
  | "saveApprove"
  | "insertMatch"
  | "editSuggestion"
  | "acceptSuggestion"
  | "queueNext"
  | "queuePrev"
  | "queueAccept"
  | "queueEdit"
  | "queueReject"
  | "queueAcceptEdit"
  | "queueCancelEdit"
  | "publish"
  | "importFile"
  | "exportFile";

export interface Shortcut {
  id: ShortcutId;
  /** Key caps as shown to people; "Mod" renders as ⌘ on Apple platforms and Ctrl elsewhere. */
  keys: string[];
  description: string;
  /** Where it applies. */
  context: "Everywhere" | "Translator workspace" | "Translation editor" | "Review queue" | "Releases" | "Import & export";
  /** Fires while focus is in a text field (only chords with a modifier or Escape do). */
  inText: boolean;
  match: (e: KeyboardEvent) => boolean;
}

const mod = (e: KeyboardEvent) => e.metaKey || e.ctrlKey;
const plain = (e: KeyboardEvent) => !e.metaKey && !e.ctrlKey && !e.altKey;
/** Mod+Alt, but not AltGr (which Windows reports as Ctrl+Alt and which types `{`, `@`, … on many layouts). */
const modAlt = (e: KeyboardEvent) => mod(e) && e.altKey && !e.shiftKey && !e.getModifierState?.("AltGraph");
const letter = (e: KeyboardEvent, k: string) => e.key === k && plain(e) && !e.shiftKey;

/** Which translation-memory match (1–9) a Mod+Alt+digit chord inserts; by physical key, so any layout works. */
export function matchNumber(e: KeyboardEvent): number | undefined {
  const m = /^Digit([1-9])$/.exec(e.code);
  return m ? Number(m[1]) : undefined;
}

export const SHORTCUTS: readonly Shortcut[] = [
  {
    id: "help", keys: ["?"], description: "Show keyboard shortcuts", context: "Everywhere", inText: false,
    match: (e) => e.key === "?" && !e.metaKey && !e.ctrlKey && !e.altKey,
  },
  {
    id: "search", keys: ["/"], description: "Search messages", context: "Translator workspace", inText: false,
    match: (e) => e.key === "/" && plain(e),
  },
  {
    id: "next", keys: ["j"], description: "Next message", context: "Translator workspace", inText: false,
    match: (e) => e.key === "j" && plain(e) && !e.shiftKey,
  },
  {
    id: "prev", keys: ["k"], description: "Previous message", context: "Translator workspace", inText: false,
    match: (e) => e.key === "k" && plain(e) && !e.shiftKey,
  },
  {
    id: "edit", keys: ["Enter"], description: "Edit the translation", context: "Translator workspace", inText: false,
    // Enter still activates a focused link, button or tab.
    match: (e) => e.key === "Enter" && plain(e) && !e.shiftKey && !isActivatable(e.target),
  },
  {
    id: "leave", keys: ["Esc"], description: "Leave the editor, back to the message list", context: "Translation editor",
    inText: true, match: (e) => e.key === "Escape" && plain(e),
  },
  {
    id: "save", keys: ["Mod", "Enter"], description: "Save the translation", context: "Translation editor", inText: true,
    match: (e) => e.key === "Enter" && mod(e) && !e.shiftKey && !e.altKey,
  },
  {
    id: "saveApprove", keys: ["Mod", "Shift", "Enter"], description: "Save and approve", context: "Translation editor",
    inText: true, match: (e) => e.key === "Enter" && mod(e) && e.shiftKey && !e.altKey,
  },
  {
    id: "insertMatch", keys: ["Mod", "Alt", "1–9"], description: "Insert translation-memory match 1–9", context: "Translation editor",
    inText: true, match: (e) => modAlt(e) && matchNumber(e) !== undefined,
  },
  {
    id: "editSuggestion", keys: ["Mod", "Alt", "0"], description: "Edit the AI suggestion, then accept it", context: "Translation editor",
    inText: true, match: (e) => modAlt(e) && e.code === "Digit0",
  },
  {
    id: "acceptSuggestion", keys: ["Mod", "Alt", "Enter"], description: "Accept the AI suggestion as it is", context: "Translation editor",
    inText: true, match: (e) => e.key === "Enter" && modAlt(e),
  },
  {
    id: "queueNext", keys: ["j"], description: "Next suggestion", context: "Review queue", inText: false,
    match: (e) => letter(e, "j"),
  },
  {
    id: "queuePrev", keys: ["k"], description: "Previous suggestion", context: "Review queue", inText: false,
    match: (e) => letter(e, "k"),
  },
  {
    id: "queueAccept", keys: ["a"], description: "Accept the suggestion", context: "Review queue", inText: false,
    match: (e) => letter(e, "a"),
  },
  {
    id: "queueEdit", keys: ["e"], description: "Edit the suggestion before accepting it", context: "Review queue", inText: false,
    match: (e) => letter(e, "e"),
  },
  {
    id: "queueReject", keys: ["r"], description: "Reject the suggestion", context: "Review queue", inText: false,
    match: (e) => letter(e, "r"),
  },
  {
    id: "queueAcceptEdit", keys: ["Mod", "Enter"], description: "Accept your edit", context: "Review queue", inText: true,
    match: (e) => e.key === "Enter" && mod(e) && !e.shiftKey && !e.altKey,
  },
  {
    id: "queueCancelEdit", keys: ["Esc"], description: "Cancel the edit", context: "Review queue", inText: true,
    match: (e) => e.key === "Escape" && plain(e),
  },
  {
    id: "publish", keys: ["p"], description: "Publish a release", context: "Releases", inText: false,
    match: (e) => e.key === "p" && plain(e) && !e.shiftKey,
  },
  {
    id: "importFile", keys: ["i"], description: "Import a file", context: "Import & export", inText: false,
    match: (e) => letter(e, "i"),
  },
  {
    id: "exportFile", keys: ["x"], description: "Export files", context: "Import & export", inText: false,
    match: (e) => letter(e, "x"),
  },
];

export const isApple = (): boolean => /Mac|iPhone|iPad/.test(globalThis.navigator?.platform ?? "");

/** How a key cap is shown on this platform. */
export function keyLabel(key: string, apple = isApple()): string {
  if (key === "Mod") return apple ? "⌘" : "Ctrl";
  if (key === "Shift") return apple ? "⇧" : "Shift";
  if (key === "Alt") return apple ? "⌥" : "Alt";
  if (key === "Enter") return apple ? "↵ Return" : "Enter";
  return key;
}

/** Key caps as `aria-keyshortcuts` wants them ("Meta+Alt+1", "Control+Enter"). */
export function ariaKeys(keys: readonly string[], apple = isApple()): string {
  return keys.map((k) => (k === "Mod" ? (apple ? "Meta" : "Control") : k === "Esc" ? "Escape" : k)).join("+");
}

/** Is focus somewhere that takes text? */
export function isTextTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  if (target instanceof HTMLInputElement) {
    return !["checkbox", "radio", "button", "submit", "reset", "range", "color", "file"].includes(target.type);
  }
  return false;
}

/** Does Enter already mean something on this element? */
export function isActivatable(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && target.closest("a[href], button, summary, [role='button'], [role='tab'], [role='link']") !== null;
}

/** The shortcut an event triggers, if any. */
export function matchShortcut(e: KeyboardEvent, enabled: ReadonlySet<ShortcutId>): Shortcut | undefined {
  if (e.isComposing || e.defaultPrevented) return undefined;
  const inText = isTextTarget(e.target);
  return SHORTCUTS.find((s) => enabled.has(s.id) && (s.inText || !inText) && s.match(e));
}

/** A handler returns `false` when the key isn't for it, leaving the event to others. */
export type ShortcutHandlers = Partial<Record<ShortcutId, (e: KeyboardEvent) => void | false>>;

/** Bind handlers for the lifetime of the calling component. */
export function useShortcuts(handlers: ShortcutHandlers): void {
  const enabled = new Set(Object.keys(handlers) as ShortcutId[]);
  const onKey = (e: KeyboardEvent) => {
    const s = matchShortcut(e, enabled);
    if (!s) return;
    // A modal owns the keyboard; only the sheet's own toggle passes through.
    if (s.id !== "help" && document.querySelector("dialog[open]")) return;
    if (handlers[s.id]?.(e) !== false) e.preventDefault();
  };
  onMounted(() => window.addEventListener("keydown", onKey));
  onBeforeUnmount(() => window.removeEventListener("keydown", onKey));
}
